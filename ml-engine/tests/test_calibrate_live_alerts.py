"""Canlı uyarı kurallarının sentetik stres testi: API ile aynı puan, eğrilerin tutarlılığı, yayımlanan sonuç."""
import json
import pathlib
import sys

import numpy as np
import pytest

ENGINE = pathlib.Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ENGINE))
import calibrate_live_alerts as L  # noqa: E402


@pytest.fixture(scope="module")
def report():
    return L.run(rounds=3)


def test_false_alarms_fall_and_recall_falls_as_the_threshold_rises(report):
    for task in ("production_yield", "butchery_yield"):
        curve = report[task]["curve"]
        fa = [p["false_alarms_per_100"] for p in curve]
        rec = [p["recall"] for p in curve]
        assert fa == sorted(fa, reverse=True) and rec == sorted(rec, reverse=True), task


def test_six_parts_at_one_band_alarm_too_often_and_the_default_stays_in_budget(report):
    at = {p["threshold"]: p for p in report["butchery_yield"]["curve"]}
    assert at[1.0]["false_alarms_per_100"] > 3          # one of six parts leaves its 98 % band often
    assert at[1.5]["false_alarms_per_100"] <= 1.0       # default: alarm budget 1 per 100 carcasses
    assert at[1.5]["precision"] > 0.8


def test_price_false_alarms_grow_with_price_noise(report):
    fa10 = [next(p for p in s["curve"] if p["threshold"] == 10.0)["false_alarms_per_100"]
            for s in report["purchase_cost"]["by_price_noise"]]
    assert fa10 == sorted(fa10) and fa10[0] < 0.5 and fa10[-1] > 5


def test_price_increase_uses_the_mean_of_the_last_three_purchases():
    pct = L.price_increase_pct(np.array([10.0, 10.0, 12.0, 11.0, 13.2]))
    assert np.isnan(pct[0])
    assert pct[2] == pytest.approx(20.0)                 # 12 / mean(10, 10)
    assert pct[4] == pytest.approx(20.0)                 # 13.2 / mean(10, 12, 11)


def test_published_result_is_what_the_api_serves():
    art = (L.ART / "live_alert_calibration.json").read_text(encoding="utf-8")
    api = (L.AIMODEL / "live_alert_calibration.json").read_text(encoding="utf-8")
    assert art == api and json.loads(api)["version"] == L.VERSION
