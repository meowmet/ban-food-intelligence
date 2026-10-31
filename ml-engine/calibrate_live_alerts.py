"""Canlı uyarı kurallarının sentetik stres testi (api-go/controllers/alerts.go).

Gerçek işletme verisi olmadan bir eşiğin saha doğruluğu ölçülemez. Bu betik şunu ölçer: veri,
sentetik üreticinin varsayımlarına benzerse kural ne kadar isabetli ve ne sıklıkta yanlış alarm
verir. Her eşik için ayrı ayrı raporlanır; böylece işletme eşiği bir "alarm bütçesine" göre seçer.

  Verim (A) ve parçalama (B): anomaly_ali.py ile aynı veri, aynı şampiyon OOF/test tahminleri ve
    aynı arıza enjeksiyonu (%6, 30 tur). Puan = |gerçek - tahmin| / %98 bant (API ile aynı; az fire
    sayılmaz). Yalnız zaman bölmeli test dönemi ölçülür; bant train artıklarından gelir.
  Alış fiyatı: üreticinin fiyat süreci (trend, mevsim, tedarikçi farkı, gürültü) ile haftalık alış
    serileri. Fiyat gürültüsü bizim varsayımımız olduğu için tek sayı değil, gürültü düzeyine göre
    duyarlılık verilir: temiz alışlarda 100 alışta yanlış alarm ve sıçrama büyüklüğüne göre yakalama.
  Sayım eksiği ve reddedilen kasa satışı tahmin değil, kayıtlı olgudur; eşikleri önem (tutar)
    kararıdır, isabetle ölçülmez.

  python ml-engine/calibrate_live_alerts.py      # -> artifacts/ali/live_alert_calibration.json
                                                 #    + api-go/controllers/aimodel/live_alert_calibration.json
"""
from __future__ import annotations

import argparse
import json
import pathlib
import sys

import numpy as np
import pandas as pd

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import anomaly_ali as A  # noqa: E402
import generator_ali as G  # noqa: E402
import ml_contract as C  # noqa: E402

ART = HERE / "artifacts" / "ali"
AIMODEL = HERE.parent / "api-go" / "controllers" / "aimodel"
VERSION = "live-alerts-1.0.0"
BAND_GRID = [0.5, 0.75, 1.0, 1.25, 1.5, 1.75, 2.0, 2.5, 3.0]          # x %98 bant
PRICE_GRID = [5.0, 7.5, 10.0, 12.5, 15.0, 20.0, 25.0, 30.0]            # % son 3 alış ortalamasının üstü
PRICE_SIGMAS = [0.015, 0.03, 0.05, 0.08]                               # alış başına fiyat gürültüsü
PRICE_JUMPS = (("5-10", 0.05, 0.10), ("10-20", 0.10, 0.20), ("20-40", 0.20, 0.40))
PRICE_PRODUCTS = {  # baz fiyat (Ekim 2026), yıllık trend, mevsim etkisi, tedarikçi çarpanları
    "HM-ET-DANA": (9.70, 0.06, 0.025, (0.97, 1.00, 1.03)),
    "HM-BAHARAT": (24.00, 0.05, 0.0, (1.0,)),
    "HM-BAGIRSAK": (11.60, 0.04, 0.0, (1.0,)),
    "KZ-KARKAS": (11.50, 0.06, 0.0, (1.0,)),
}


def _curve(score: np.ndarray, label: np.ndarray, grid: list[float]) -> list[dict]:
    """Her eşik için isabet, yakalama, 100 kayıtta yanlış alarm (anomaly_ali._prf ile aynı tanım)."""
    return [{"threshold": t, **{k: round(v, 4) for k, v in A._prf(label, score > t).items() if k != "f1"}} for t in grid]


def _mean_curves(curves: list[list[dict]]) -> list[dict]:
    out = []
    for i, point in enumerate(curves[0]):
        row = {"threshold": point["threshold"]}
        for k in ("precision", "recall", "false_alarms_per_100"):
            row[k] = round(float(np.mean([c[i][k] for c in curves])), 4)
        out.append(row)
    return out


def _by_size(score, size, buckets, thresholds) -> dict:
    return {f"{t:g}": {name: round(float((score[(size >= lo) & (size < hi)] > t).mean()), 4)
                       for name, lo, hi in buckets if ((size >= lo) & (size < hi)).any()} for t in thresholds}


def production_scores(df: pd.DataFrame, pred: np.ndarray, test: np.ndarray, band98: float,
                      rounds: int, seed: int = 10) -> dict:
    """Puan = |verim - tahmin| / bant: API'deki |paket - tahmin| / paket bandı ile aynı oran."""
    rng = np.random.default_rng(seed)
    fault = df.fault.to_numpy(bool)
    curves, sizes, scores = [], [], []
    for _ in range(rounds):
        rows = A._pick(rng, np.where(test & ~fault)[0], int(test.sum()))
        y, drop = A.inject_production(df, rows, rng)
        score = np.abs(y - pred) / band98
        label = fault | (drop > 0)
        curves.append(_curve(score[test], label[test], BAND_GRID))
        sizes.append(drop[test & (drop > 0)])
        scores.append(score[test & (drop > 0)])
    buckets = (("small_1.5-3", 1.5, 3), ("medium_3-4.5", 3, 4.5), ("large_4.5+", 4.5, 99))
    return {"n_test": int(test.sum()), "band98_yield_pts": band98, "curve": _mean_curves(curves),
            "recall_by_drop_pts": _by_size(np.concatenate(scores), np.concatenate(sizes), buckets, (1.0, 2.0))}


def butchery_scores(df: pd.DataFrame, P: np.ndarray, test: np.ndarray, band98: np.ndarray,
                    rounds: int, seed: int = 11) -> dict:
    """Puan = altı parçanın en büyük |pay - tahmin| / bant'ı; fire eksiye saparsa sayılmaz (API ile aynı)."""
    rng = np.random.default_rng(seed)
    fire = C.BUTCHERY["targets"].index("fire_pct")
    curves, sizes, scores = [], [], []
    for _ in range(rounds):
        rows = A._pick(rng, np.where(test)[0], int(test.sum()))
        Y, shift = A.inject_butchery(df, rows, rng)
        D = (Y - P) / band98
        D[:, fire] = np.maximum(D[:, fire], 0)
        score = np.abs(D).max(axis=1)
        label = shift > 0
        curves.append(_curve(score[test], label[test], BAND_GRID))
        sizes.append(shift[test & label])
        scores.append(score[test & label])
    buckets = (("small_1-2", 0.5, 2), ("medium_2-3", 2, 3), ("large_3+", 3, 99))
    return {"n_test": int(test.sum()), "band98_pct": [float(b) for b in band98], "curve": _mean_curves(curves),
            "recall_by_shift_pct_of_carcass": _by_size(np.concatenate(scores), np.concatenate(sizes), buckets, (1.0, 2.0))}


def purchase_series(rng: np.random.Generator, sigma: float, weeks: int = 104) -> list[np.ndarray]:
    """Ürün başına haftalık alış fiyatları; tedarikçi her alışta rastgele seçilir."""
    t = (np.arange(weeks) - weeks) / 52.0
    month = (np.arange(weeks) // 4.345) % 12 + 1
    series = []
    for base, trend, season, suppliers in PRICE_PRODUCTS.values():
        s = rng.choice(suppliers, size=weeks)
        p = base * np.exp(trend * t) * (1 + season * np.sin(2 * np.pi * (month - 4) / 12)) * s * rng.normal(1, sigma, weeks)
        series.append(np.round(p, 2))
    return series


def price_increase_pct(prices: np.ndarray) -> np.ndarray:
    """API ile aynı: fiyat / son 3 alışın ortalaması - 1 (ilk alışta referans yok -> nan)."""
    out = np.full(len(prices), np.nan)
    for i in range(1, len(prices)):
        out[i] = 100 * (prices[i] / prices[max(0, i - 3):i].mean() - 1)
    return out


def price_sensitivity(rounds: int, seed: int = 12) -> list[dict]:
    rng = np.random.default_rng(seed)
    out = []
    for sigma in PRICE_SIGMAS:
        clean, jumps = [], {name: [] for name, _, _ in PRICE_JUMPS}
        for _ in range(rounds):
            for prices in purchase_series(rng, sigma):
                pct = price_increase_pct(prices)
                clean.append(pct[1:])
                # sıçrama: tek bir alış, referansı temiz (önceki 3 alış) -> o alışın puanı
                for name, lo, hi in PRICE_JUMPS:
                    i = int(rng.integers(4, len(prices)))
                    jumped = prices.copy()
                    jumped[i] = round(jumped[i] * (1 + rng.uniform(lo, hi)), 2)
                    jumps[name].append(price_increase_pct(jumped)[i])
        clean = np.concatenate(clean)
        out.append({"sigma": sigma, "curve": [
            {"threshold": t, "false_alarms_per_100": round(100 * float((clean > t).mean()), 4),
             "detected_by_jump_pct": {name: round(float((np.array(v) > t).mean()), 4) for name, v in jumps.items()}}
            for t in PRICE_GRID]})
    return out


def run(rounds: int = A.N_ROUNDS, seed: int = 42) -> dict:
    rng = np.random.default_rng(seed)               # anomaly_ali.main ile aynı sıra: aynı veri
    prod = G.generate_production(1500, rng, with_fault_label=True)
    butch = G.generate_butchery(2500, rng)
    csv = pd.read_csv(HERE / "data" / "train_task4a_ali.csv")
    assert csv.row_id.equals(prod.row_id) and np.allclose(csv.yield_pct, prod.yield_pct), \
        "CSV generator_ali.py varsayılanlarıyla üretilmemiş; önce generator_ali.py çalıştırın"
    metrics = json.loads((ART / "metrics.json").read_text(encoding="utf-8"))
    pred_p, split_p = A._champion_preds(pd.read_csv(ART / "oof_ali_production.csv"), metrics["production"]["champion"],
                                        prod.row_id, ["yield_pct"])
    P_b, split_b = A._champion_preds(pd.read_csv(ART / "oof_ali_butchery.csv"), metrics["butchery"]["champion"],
                                     butch.row_id, C.BUTCHERY["targets"])
    live_p = json.loads((AIMODEL / "production_ridge.json").read_text(encoding="utf-8"))
    live_b = json.loads((AIMODEL / "butchery_ridge.json").read_text(encoding="utf-8"))
    assert metrics["production"]["champion"] == live_p["model"] == live_b["model"], "canlı model şampiyonla aynı olmalı"
    assert live_b["targets"] == C.BUTCHERY["targets"], "parça sırası canlı modelle aynı olmalı"
    return {
        "version": VERSION, "model_version": live_p["version"], "inject_rate": A.INJECT_RATE, "rounds": rounds,
        "data": "synthetic (generator_ali.py, seed 42); time-split test period only",
        "production_yield": production_scores(prod, pred_p[:, 0], split_p == "test", float(live_p["interval_half_width"]["98"]), rounds),
        "butchery_yield": butchery_scores(butch, P_b, split_b == "test", np.array(live_b["interval_half_width_pct"]["98"], float), rounds),
        "purchase_cost": {"weeks": 104, "products": list(PRICE_PRODUCTS), "reference": "mean of the last 3 purchases",
                          "by_price_noise": price_sensitivity(rounds)},
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--rounds", type=int, default=A.N_ROUNDS)
    args = ap.parse_args()
    report = run(args.rounds)
    text = json.dumps(report, indent=2, ensure_ascii=False) + "\n"
    (ART / "live_alert_calibration.json").write_text(text, encoding="utf-8")
    (AIMODEL / "live_alert_calibration.json").write_text(text, encoding="utf-8")
    for task in ("production_yield", "butchery_yield"):
        print(task)
        for p in report[task]["curve"]:
            print(f"  > {p['threshold']:<4} x bant: isabet {p['precision']:.2f}  yakalama {p['recall']:.2f}  "
                  f"100 kayıtta yanlış alarm {p['false_alarms_per_100']:.2f}")
    for s in report["purchase_cost"]["by_price_noise"]:
        p10 = next(p for p in s["curve"] if p["threshold"] == 10.0)
        print(f"purchase_cost sigma {s['sigma']:.3f}: %10 eşiğinde 100 alışta {p10['false_alarms_per_100']:.2f} yanlış alarm, "
              f"yakalama {p10['detected_by_jump_pct']}")


if __name__ == "__main__":
    main()
