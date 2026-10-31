package controllers

import (
	"math"
	"testing"
)

func TestSeverityGrades(t *testing.T) {
	for _, c := range []struct {
		score float64
		want  string
	}{{0.5, ""}, {1, ""}, {1.01, "WARN"}, {1.99, "WARN"}, {2, "CRITICAL"}, {7, "CRITICAL"}} {
		if got := severity(c.score, 1, 2); got != c.want {
			t.Errorf("severity(%.2f) = %q, want %q", c.score, got, c.want)
		}
	}
}

// band: warning outside the 98 % band, critical from twice the band (the unit tests' own values)
var band = threshold{1, 2}

func TestProductionAlertValuesMissingPacksAtForecastEKP(t *testing.T) {
	// 100 kg meat, 1 kg packs: the 98 % band is about 3.87 packs.
	if a := productionAlert(1, "v", 84.6, 82, 3.87, 18.2, band); a != nil {
		t.Fatalf("inside the band must not alert: %+v", a)
	}
	a := productionAlert(1, "v", 84.6, 76, 3.87, 18.2, band)
	if a == nil || a.Direction != "LOW_YIELD" || a.Severity != "CRITICAL" || a.Score > -2 || a.ModelVersion != "v+band98" {
		t.Fatalf("8.6 packs short: %+v", a)
	}
	if math.Abs(a.EstLoss-8.6*18.2) > 1e-9 || a.Deviation > -8.59 {
		t.Fatalf("loss %.4f deviation %.3f", a.EstLoss, a.Deviation)
	}
	high := productionAlert(1, "v", 84.6, 89.5, 3.87, 18.2, band)
	if high == nil || high.Direction != "HIGH_YIELD" || high.Severity != "WARN" || high.EstLoss != 0 {
		t.Fatalf("4.9 packs over: %+v", high)
	}
	if productionAlert(1, "v", 84.6, 70, 0, 18.2, band) != nil {
		t.Fatal("no band, no grade")
	}
}

func butcheryCase(fireKg, butKg float64) *ButcheryForecastDto {
	parts := []struct {
		part         string
		kg, band, at float64
	}{{"but", 6.4, 0.6, butKg}, {"sirt", 3.3, 0.4, 3.3}, {"omuz", 4.7, 0.5, 4.7}, {"kaburga", 3.0, 0.4, 3.0},
		{"kiyma", 4.4, 0.6, 4.4}, {"fire", 2.2, 0.5, fireKg}}
	fc := &ButcheryForecastDto{ModelVersion: "v"}
	for _, p := range parts {
		at := p.at
		fc.Parts = append(fc.Parts, ButcheryPartForecastDto{Part: p.part, Kg: p.kg, ActualKg: &at, band98: p.band})
	}
	return fc
}

func TestButcheryAlertFlagsExtraWasteAndListsTopParts(t *testing.T) {
	if a := butcheryAlert(1, butcheryCase(2.4, 6.2), 11.5, band); a != nil {
		t.Fatalf("normal weights must not alert: %+v", a)
	}
	a := butcheryAlert(1, butcheryCase(3.5, 5.2), 11.5, band) // 1.3 kg extra waste, 1.2 kg less leg
	if a == nil || a.Direction != "FIRE_HIGH" || a.Severity != "CRITICAL" || math.Abs(a.Score-2.6) > 1e-9 {
		t.Fatalf("extra waste: %+v", a)
	}
	if a.Expected != 2.2 || a.Actual != 3.5 || math.Abs(a.EstLoss-1.3*11.5) > 1e-9 || a.Detail != "fire +1.30 kg, but -1.20 kg" {
		t.Fatalf("extra waste values: %+v", a)
	}
	m := butcheryAlert(1, butcheryCase(2.2, 7.3), 11.5, band) // leg 0.9 kg heavier, waste as expected
	if m == nil || m.Direction != "PART_MISMATCH" || m.Severity != "WARN" || m.EstLoss != 0 || m.Expected != 6.4 {
		t.Fatalf("part mismatch: %+v", m)
	}
	if a := butcheryAlert(1, butcheryCase(1.0, 6.4), 11.5, band); a != nil {
		t.Fatalf("less waste than expected is no loss: %+v", a)
	}
	if butcheryAlert(1, nil, 11.5, band) != nil {
		t.Fatal("no forecast, no alert")
	}
}

var price = threshold{10, 25}

func TestCostIncreaseAlertAgainstRecentPurchases(t *testing.T) {
	if costIncreaseAlert(1, "HM-ET-DANA", "KG", 100, 10.5, 9.7, 3, price) != nil {
		t.Fatal("+8 % is not an increase to report")
	}
	if costIncreaseAlert(1, "HM-ET-DANA", "KG", 100, 30, 9.7, 0, price) != nil {
		t.Fatal("no earlier purchase, no reference")
	}
	w := costIncreaseAlert(1, "HM-ET-DANA", "KG", 100, 11, 9.7, 3, price)
	if w == nil || w.Severity != "WARN" || math.Abs(w.Score-13.402) > 1e-9 {
		t.Fatalf("+13.4 %%: %+v", w)
	}
	c := costIncreaseAlert(1, "HM-ET-DANA", "KG", 100, 12.5, 9.7, 3, price)
	if c == nil || c.Severity != "CRITICAL" || c.Direction != "COST_INCREASE" || math.Abs(c.EstLoss-280) > 1e-9 ||
		c.Expected != 9.7 || c.Actual != 12.5 || c.Detail != "ref 9.70 €/kg, 3 alış" {
		t.Fatalf("+28.9 %%: %+v", c)
	}
}

var count = threshold{10, 100}

func TestCountShortageAlertByValueAndRecall(t *testing.T) {
	line := func(diff, value float64) StockCountLineDto {
		return StockCountLineDto{LotNo: "SUC-1", Uom: "PAKET", SystemQty: 20, CountedQty: 20 + diff, DiffQty: diff, ValueEUR: value, Reason: "kayıp"}
	}
	if countShortageAlert(1, line(-0.5, -5), "ACTIVE", "ANA-DEPO", "SAY-1", count) != nil {
		t.Fatal("a 5 € shortage is weighing tolerance")
	}
	if countShortageAlert(1, line(2, 36.32), "ACTIVE", "ANA-DEPO", "SAY-1", count) != nil {
		t.Fatal("a surplus is no shortage")
	}
	w := countShortageAlert(1, line(-3, -54.48), "ACTIVE", "ANA-DEPO", "SAY-1", count)
	if w == nil || w.Severity != "WARN" || w.Direction != "COUNT_SHORTAGE" || w.EstLoss != 54.48 || w.Expected != 20 || w.Actual != 17 {
		t.Fatalf("3 packs short: %+v", w)
	}
	if c := countShortageAlert(1, line(-6, -108.96), "ACTIVE", "ANA-DEPO", "SAY-1", count); c == nil || c.Severity != "CRITICAL" {
		t.Fatalf("over 100 €: %+v", c)
	}
	if r := countShortageAlert(1, line(-0.5, -5), "RECALLED", "ANA-DEPO", "SAY-1", count); r == nil || r.Severity != "CRITICAL" {
		t.Fatalf("recalled goods missing are always critical: %+v", r)
	}
}

func TestDefaultThresholdsKeepWarnBelowCritical(t *testing.T) {
	for rule, th := range defaultThresholds {
		if !(th.Warn > 0 && th.Crit > th.Warn) {
			t.Errorf("%s: %+v", rule, th)
		}
	}
	if defaultThresholds["BUTCHERY_YIELD"].Warn != 1.5 {
		t.Fatal("six parts at 1x the band give ~9 false alarms per 100 carcasses in the synthetic test")
	}
}
