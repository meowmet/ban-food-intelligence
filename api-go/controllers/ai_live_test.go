package controllers

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
)

// The Go evaluation of the exported Ridge must give the same yield as sklearn's pipeline
// (golden rows written by ml-engine/export_live_model.py: the two seeded orders, 31 held-out
// orders incl. missing fat %, summer months and 500 g packs).
func TestLiveYieldMatchesTheTrainedModel(t *testing.T) {
	raw, err := os.ReadFile("aimodel/production_ridge_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden []struct {
		RowID  string `json:"row_id"`
		Inputs struct {
			OrderDate         string   `json:"order_date"`
			ProductSKU        string   `json:"product_sku"`
			SubcontractorCode string   `json:"subcontractor_code"`
			MeatSupplierCode  string   `json:"meat_supplier_code"`
			PackSizeKg        float64  `json:"pack_size_kg"`
			MeatKg            float64  `json:"meat_kg"`
			SpiceKg           float64  `json:"spice_kg"`
			CasingKg          float64  `json:"casing_kg"`
			MeatFatPct        *float64 `json:"meat_fat_pct"`
			MeatAgeDays       *float64 `json:"meat_age_days"`
			DryingDays        *float64 `json:"drying_days"`
			PlannedQty        float64  `json:"planned_qty"`
		} `json:"inputs"`
		YieldPct float64 `json:"yield_pct"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil || len(golden) < 30 {
		t.Fatalf("golden file: %v (%d rows)", err, len(golden))
	}
	for _, g := range golden {
		d, _ := time.Parse("2006-01-02", g.Inputs.OrderDate)
		got := productionModel.predictYield(ProductionInputs{
			OrderDate: d, ProductSKU: g.Inputs.ProductSKU, SubcontractorCode: g.Inputs.SubcontractorCode,
			MeatSupplierCode: g.Inputs.MeatSupplierCode, PackSizeKg: g.Inputs.PackSizeKg, MeatKg: g.Inputs.MeatKg,
			SpiceKg: g.Inputs.SpiceKg, CasingKg: g.Inputs.CasingKg, MeatFatPct: g.Inputs.MeatFatPct,
			MeatAgeDays: g.Inputs.MeatAgeDays, DryingDays: g.Inputs.DryingDays, PlannedQty: g.Inputs.PlannedQty,
		})
		if math.Abs(got-g.YieldPct) > 1e-9 {
			t.Errorf("%s: Go %.12f, sklearn %.12f", g.RowID, got, g.YieldPct)
		}
	}
}

// URT-2026-045 (spec: 100 kg meat, 2 kg spice, 5 kg casing, 85 x 5.50 EUR service) gives the
// seeded forecast: 84.6 packages, EKP 18.2172 EUR, 80 % interval 83.2 - 86.0.
func TestForecastOfTheSpecOrder(t *testing.T) {
	fat, age, drying := 22.0, 1.0, 5.0
	o := &orderInputs{
		in: ProductionInputs{OrderDate: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), ProductSKU: "MM-SUCUK-1KG",
			SubcontractorCode: "FAS-01", MeatSupplierCode: "TED-ET-01", PackSizeKg: 1, MeatKg: 100, SpiceKg: 2, CasingKg: 5,
			MeatFatPct: &fat, MeatAgeDays: &age, DryingDays: &drying, PlannedQty: 85},
		serviceUnit: 5.5, materialCost: 1076,
	}
	fc := forecastOrder(o, productionModel)
	if fc == nil || fc.Packages != 84.6 || *fc.EKP != 18.2172 || *fc.PackagesLow != 83.2 || *fc.PackagesHigh != 86.0 {
		t.Fatalf("forecast %+v", fc)
	}
	if *fc.EKPLow >= *fc.EKP || *fc.EKPHigh <= *fc.EKP || fc.BaselinePackages == nil {
		t.Fatalf("EKP bounds or baseline wrong: %+v", fc)
	}
	o.in.MeatKg = 0
	if forecastOrder(o, productionModel) != nil {
		t.Fatal("an order without meat has nothing to forecast")
	}
}

func TestProductionForecastWithholdsNonphysicalExtrapolation(t *testing.T) {
	// The review reproduced a negative package count for a very old meat lot.
	age := 10000.0
	order := &orderInputs{in: ProductionInputs{OrderDate: time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC),
		ProductSKU: "MM-SUCUK-1KG", MeatKg: 1, PackSizeKg: 1, MeatAgeDays: &age}, materialCost: 10, serviceUnit: 1}
	if fc := forecastOrder(order, productionModel); fc != nil {
		t.Fatalf("nonphysical forecast %+v", fc)
	}
}

// Study B: the Go evaluation of the six exported share models (clip at 0.1 %, rescale to 100 %)
// gives sklearn's shares on 34 golden rows, incl. a carcass without chill hours and fat score.
func TestLiveButcherySharesMatchTheTrainedModel(t *testing.T) {
	raw, err := os.ReadFile("aimodel/butchery_ridge_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden []struct {
		RowID  string `json:"row_id"`
		Inputs struct {
			OpDate            string   `json:"op_date"`
			Species           string   `json:"species"`
			Breed             string   `json:"breed"`
			SupplierCode      string   `json:"supplier_code"`
			OperatorCode      string   `json:"operator_code"`
			Sex               string   `json:"sex"`
			CarcassWeightKg   float64  `json:"carcass_weight_kg"`
			AgeMonths         *float64 `json:"age_months"`
			FatScore          *float64 `json:"fat_score"`
			ConformationScore *float64 `json:"conformation_score"`
			ChillHours        *float64 `json:"chill_hours"`
		} `json:"inputs"`
		Shares []float64 `json:"shares_pct"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil || len(golden) < 30 {
		t.Fatalf("golden file: %v (%d rows)", err, len(golden))
	}
	for _, g := range golden {
		d, _ := time.Parse("2006-01-02", g.Inputs.OpDate)
		got := butcheryForecastModel.predictShares(ButcheryInputs{OpDate: d, Species: g.Inputs.Species, Breed: g.Inputs.Breed,
			SupplierCode: g.Inputs.SupplierCode, OperatorCode: g.Inputs.OperatorCode, Sex: g.Inputs.Sex,
			CarcassWeightKg: g.Inputs.CarcassWeightKg, AgeMonths: g.Inputs.AgeMonths, FatScore: g.Inputs.FatScore,
			ConformationScore: g.Inputs.ConformationScore, ChillHours: g.Inputs.ChillHours})
		for j := range got {
			if math.Abs(got[j]-g.Shares[j]) > 1e-9 {
				t.Errorf("%s share %d: Go %.12f, sklearn %.12f", g.RowID, j, got[j], g.Shares[j])
			}
		}
	}
}

// PRC-2026-001 (25 kg lamb) gives the seeded part forecast (leg 6.794 kg, 6.565 - 7.023), parts +
// waste = input, and the recorded weights are within the 98 % band.
func TestForecastOfTheSeedCarcass(t *testing.T) {
	age, fat, conf, chill := 5.0, 3.0, 3.0, 24.0
	fc := forecastButchery(ButcheryInputs{OpDate: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC), Species: "KUZU",
		Breed: "KIVIRCIK", SupplierCode: "TED-HAY-01", OperatorCode: "OP-1", Sex: "E", CarcassWeightKg: 25,
		AgeMonths: &age, FatScore: &fat, ConformationScore: &conf, ChillHours: &chill}, butcheryForecastModel)
	leg := fc.Parts[0]
	if leg.Part != "but" || leg.SKU != "KZ-BUT" || leg.Kg != 6.794 || leg.KgLow != 6.565 || leg.KgHigh != 7.023 {
		t.Fatalf("leg forecast %+v", leg)
	}
	sum := 0.0
	for _, p := range fc.Parts {
		sum += p.SharePct
	}
	if math.Abs(sum-100) > 0.006 { // six shares, each rounded to 0.001
		t.Fatalf("shares sum to %.6f", sum)
	}
	applyButcheryActuals(fc, []ButcheryOutputRequest{{SKU: "KZ-BUT", Kind: "CUT", WeightKg: 6.5}, {SKU: "KZ-SIRT", Kind: "CUT", WeightKg: 3.5},
		{SKU: "KZ-OMUZ", Kind: "CUT", WeightKg: 5}, {SKU: "KZ-KABURGA", Kind: "CUT", WeightKg: 3}, {SKU: "KZ-KIYMA", Kind: "CUT", WeightKg: 4},
		{SKU: "KZ-FIRE", Kind: "WASTE", WeightKg: 3}})
	for _, p := range fc.Parts {
		if p.ActualKg == nil || p.Anomaly == nil || *p.Anomaly {
			t.Fatalf("part %s: actual %v anomaly %v", p.Part, p.ActualKg, p.Anomaly)
		}
	}
}

// Only an explicit DATA_ORIGIN=FIELD labels new forecasts as field data; anything else stays DEMO.
func TestPredictionOriginDefaultsToDemo(t *testing.T) {
	for env, want := range map[string]string{"": "DEMO", "demo": "DEMO", "field": "FIELD", " FIELD ": "FIELD", "verified": "DEMO"} {
		t.Setenv("DATA_ORIGIN", env)
		if got := predictionOrigin(); got != want {
			t.Errorf("DATA_ORIGIN=%q: got %s, want %s", env, got, want)
		}
	}
}
