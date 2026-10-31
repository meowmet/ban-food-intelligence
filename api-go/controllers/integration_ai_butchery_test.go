package controllers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestButcheryForecastRejectsInvalidInputs(t *testing.T) {
	ctrl := NewAIController(nil) // invalid requests must never reach the database
	for _, weight := range []string{"NaN", "Inf", "-1", "0", "501", "oops", ""} {
		code, body := call(t, ctrl.ButcheryForecast, "GET", "/ai/forecast/butchery?input_lot_no=KZ&input_weight_kg="+weight, nil)
		if code != http.StatusUnprocessableEntity {
			t.Errorf("%q: %d %s", weight, code, body)
		}
	}
	code, _ := call(t, ctrl.ButcheryForecast, "POST", "/ai/forecast/butchery", nil)
	if code != http.StatusMethodNotAllowed {
		t.Fatal(code)
	}
	for _, req := range []ButcheryFreezeRequest{{InputWeightKg: 25}, {InputLotNo: "KZ", InputWeightKg: 0},
		{InputLotNo: "KZ", InputWeightKg: 501}, {InputLotNo: "KZ", InputWeightKg: 25, OpDate: "31.10.2026"}} {
		if code, body := call(t, ctrl.FreezeButcheryForecast, "POST", "/ai/forecast/butchery/freeze", req); code != http.StatusUnprocessableEntity {
			t.Errorf("%+v: %d %s", req, code, body)
		}
	}
	if code, _ := call(t, ctrl.FreezeButcheryForecast, "GET", "/ai/forecast/butchery/freeze", nil); code != http.StatusMethodNotAllowed {
		t.Fatal(code)
	}
}

// A forecast frozen before weighing is kept as it was and compared with the posted operation.
func TestIntegrationFrozenButcheryForecast(t *testing.T) {
	db := testDB(t)
	entry, ai := NewEntryController(db), NewAIController(db)
	suffix := runSuffix()
	today := time.Now().UTC().Format("2006-01-02")
	expiry := time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02")
	matched, other, late := "KZ-FZA-"+suffix, "KZ-FZB-"+suffix, "KZ-FZC-"+suffix
	var lines []PurchaseLineRequest
	for _, lot := range []string{matched, other, late} {
		lines = append(lines, PurchaseLineRequest{SKU: "KZ-KARKAS", LotNo: lot, Qty: 25, UnitPrice: 11.5, ExpiryDate: expiry})
	}
	if code, body := call(t, entry.Purchase, "POST", "/purchases", PurchaseRequest{
		ReceiptNo: "ALS-FZ-" + suffix, SupplierCode: "TED-HAY-01", ReceiptDate: today, Lines: lines}); code != 201 {
		t.Fatalf("purchase: %d %s", code, body)
	}
	butchery := func(opNo, lot string, kg float64) ButcheryOperationRequest {
		// the standard split, scaled to the input weight
		part := func(sku, kind string, share float64) ButcheryOutputRequest {
			o := ButcheryOutputRequest{SKU: sku, Kind: kind, WeightKg: round(kg*share, 3)}
			if kind != "WASTE" {
				o.LotNo = sku[3:] + "-" + opNo
			}
			return o
		}
		outs := []ButcheryOutputRequest{part("KZ-BUT", "CUT", 0.26), part("KZ-SIRT", "CUT", 0.14), part("KZ-OMUZ", "CUT", 0.20),
			part("KZ-KABURGA", "CUT", 0.12), part("KZ-KIYMA", "CUT", 0.16)}
		sum := 0.0
		for _, o := range outs {
			sum += o.WeightKg
		}
		outs = append(outs, ButcheryOutputRequest{SKU: "KZ-FIRE", Kind: "WASTE", WeightKg: round(kg-sum, 3)})
		return ButcheryOperationRequest{OpNo: opNo, InputLotNo: lot, InputWeightKg: kg, AllocationMethod: "VALUE",
			OpDate: today, OperatorName: "OP-1", Outputs: outs}
	}
	freeze := func(lot string, kg float64) (int, []byte) {
		return call(t, ai.FreezeButcheryForecast, "POST", "/ai/forecast/butchery/freeze",
			ButcheryFreezeRequest{InputLotNo: lot, InputWeightKg: kg, OpDate: today, OperatorName: "OP-1"})
	}

	code, body := freeze(matched, 25)
	var frozen FrozenButcheryDto
	if err := json.Unmarshal(body, &frozen); err != nil || code != 201 || frozen.Forecast == nil || len(frozen.Forecast.Parts) != 6 {
		t.Fatalf("freeze: %d %s %v", code, body, err)
	}
	if frozen.FrozenBy != "api" || frozen.OpNo != "" || frozen.InputWeightKg != 25 || frozen.FrozenAt.IsZero() {
		t.Fatalf("frozen header: %+v", frozen)
	}
	code, body = call(t, ai.ButcheryForecast, "GET", "/ai/forecast/butchery?input_lot_no="+matched+"&input_weight_kg=25&op_date="+today+"&operator_name=OP-1", nil)
	var live ButcheryForecastResponse
	if err := json.Unmarshal(body, &live); err != nil || code != 200 || live.ButcheryForecastDto == nil || live.Frozen == nil {
		t.Fatalf("forecast with frozen: %d %s %v", code, body, err)
	}
	for i, p := range live.Frozen.Forecast.Parts {
		l := live.Parts[i]
		if p.Part != l.Part || p.SKU != l.SKU || p.Kg != l.Kg || p.KgLow != l.KgLow || p.KgHigh != l.KgHigh ||
			p.BaselineKg == nil || *p.BaselineKg != *l.BaselineKg || p.ActualKg != nil {
			t.Fatalf("frozen part %+v differs from the live forecast %+v", p, l)
		}
	}
	if live.Frozen.Forecast.ModelVersion != live.ModelVersion || live.Frozen.Forecast.IntervalLevel != 0.8 {
		t.Fatalf("frozen model: %+v", live.Frozen.Forecast)
	}
	if code, body = freeze(matched, 24); code != http.StatusConflict || !json.Valid(body) {
		t.Fatalf("second freeze: %d %s", code, body)
	}
	var apiErr APIError
	_ = json.Unmarshal(body, &apiErr)
	if apiErr.Code != "FORECAST_FROZEN" {
		t.Fatalf("second freeze code: %s", body)
	}

	// The posting links the frozen forecast and reports it with the recorded weights.
	req := butchery("PRC-FZA-"+suffix, matched, 25)
	code, body = call(t, entry.ButcheryOperation, "POST", "/butchery/operations", req)
	var result ButcheryOperationResultDto
	if err := json.Unmarshal(body, &result); err != nil || code != 201 || result.FrozenForecast == nil {
		t.Fatalf("post: %d %s %v", code, body, err)
	}
	f := result.FrozenForecast
	if f.OpNo != req.OpNo || f.InputMatches == nil || !*f.InputMatches || !f.FrozenAt.Equal(frozen.FrozenAt) {
		t.Fatalf("linked forecast: %+v", f)
	}
	for i, p := range f.Forecast.Parts {
		if p.ActualKg == nil || p.Anomaly == nil || p.Kg != frozen.Forecast.Parts[i].Kg || *p.ActualKg != *result.Forecast.Parts[i].ActualKg {
			t.Fatalf("frozen part with actual: %+v", p)
		}
	}
	var linkedOp string
	if err := db.QueryRow(`SELECT bo.op_no FROM butchery_forecast bf JOIN lot l ON l.id = bf.input_lot_id
        JOIN butchery_operation bo ON bo.id = bf.operation_id WHERE l.lot_no = ?`, matched).Scan(&linkedOp); err != nil || linkedOp != req.OpNo {
		t.Fatalf("link: %q %v", linkedOp, err)
	}
	if _, err := db.Exec(`UPDATE butchery_forecast_part p JOIN butchery_forecast bf ON bf.id = p.forecast_id
        JOIN lot l ON l.id = bf.input_lot_id SET p.predicted_value = 1 WHERE l.lot_no = ?`, matched); err == nil {
		t.Fatal("a frozen part must not change")
	}
	if code, body = freeze(matched, 25); code != http.StatusConflict {
		t.Fatalf("freeze after posting: %d %s", code, body)
	}

	// A forecast frozen for another weight is still linked, flagged as not matching.
	if code, body = freeze(other, 25.4); code != 201 {
		t.Fatalf("freeze other: %d %s", code, body)
	}
	req = butchery("PRC-FZB-"+suffix, other, 25)
	req.DryRun = true
	code, body = call(t, entry.ButcheryOperation, "POST", "/butchery/operations", req)
	result = ButcheryOperationResultDto{}
	if err := json.Unmarshal(body, &result); err != nil || code != 200 || result.FrozenForecast == nil ||
		result.FrozenForecast.InputMatches == nil || *result.FrozenForecast.InputMatches || result.FrozenForecast.OpNo != "" {
		t.Fatalf("dry run with another weight: %d %s %v", code, body, err)
	}
	var linked sql.NullInt64
	if err := db.QueryRow(`SELECT bf.operation_id FROM butchery_forecast bf JOIN lot l ON l.id = bf.input_lot_id WHERE l.lot_no = ?`, other).
		Scan(&linked); err != nil || linked.Valid {
		t.Fatalf("a dry run must not link: %v %v", linked, err)
	}

	// A carcass cut without a frozen forecast cannot get one afterwards.
	if code, body = call(t, entry.ButcheryOperation, "POST", "/butchery/operations", butchery("PRC-FZC-"+suffix, late, 25)); code != 201 {
		t.Fatalf("post without freeze: %d %s", code, body)
	}
	code, body = freeze(late, 25)
	apiErr = APIError{}
	_ = json.Unmarshal(body, &apiErr)
	if code != http.StatusConflict || apiErr.Code != "FORECAST_TOO_LATE" {
		t.Fatalf("freeze after cutting: %d %s", code, body)
	}
}

func TestIntegrationButcheryLiveForecast(t *testing.T) {
	db := testDB(t)
	entry, ai := NewEntryController(db), NewAIController(db)
	suffix := runSuffix()
	carcass := "KZ-AI-" + suffix
	today := time.Now().UTC().Format("2006-01-02")
	code, body := call(t, entry.Purchase, "POST", "/purchases", PurchaseRequest{
		ReceiptNo: "ALS-AI-" + suffix, SupplierCode: "TED-HAY-01", ReceiptDate: today,
		Lines: []PurchaseLineRequest{{SKU: "KZ-KARKAS", LotNo: carcass, Qty: 25, UnitPrice: 11.5,
			ExpiryDate: time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02")}},
	})
	if code != 201 {
		t.Fatalf("purchase: %d %s", code, body)
	}
	path := "/ai/forecast/butchery?input_lot_no=" + carcass + "&input_weight_kg=25&op_date=" + today + "&operator_name=OP-1"
	code, body = call(t, ai.ButcheryForecast, "GET", path, nil)
	var forecast ButcheryForecastDto
	if err := json.Unmarshal(body, &forecast); err != nil || code != 200 || len(forecast.Parts) != 6 {
		t.Fatalf("forecast: %d %s %v", code, body, err)
	}
	for _, p := range forecast.Parts {
		if p.ActualKg != nil || p.Anomaly != nil || p.BaselineKg == nil || p.KgLow >= p.Kg || p.KgHigh <= p.Kg {
			t.Fatalf("preview part: %+v", p)
		}
	}
	for _, tc := range []struct {
		lot  string
		code int
	}{{"MISSING", 404}, {"ET-2026-001", 422}} {
		code, body = call(t, ai.ButcheryForecast, "GET", "/ai/forecast/butchery?input_lot_no="+tc.lot+"&input_weight_kg=25", nil)
		if code != tc.code {
			t.Fatalf("lot %s: %d %s", tc.lot, code, body)
		}
	}
	// Deliberately unusual leg and back weights: keep the mass balance, trigger the anomaly.
	req := ButcheryOperationRequest{OpNo: "PRC-AI-" + suffix, InputLotNo: carcass, InputWeightKg: 25,
		AllocationMethod: "VALUE", OpDate: today, OperatorName: "OP-1", DryRun: true,
		Outputs: []ButcheryOutputRequest{
			{SKU: "KZ-BUT", Kind: "CUT", WeightKg: 0.5, LotNo: "KZB-AI-" + suffix},
			{SKU: "KZ-SIRT", Kind: "CUT", WeightKg: 9.5, LotNo: "KZS-AI-" + suffix},
			{SKU: "KZ-OMUZ", Kind: "CUT", WeightKg: 5, LotNo: "KZO-AI-" + suffix},
			{SKU: "KZ-KABURGA", Kind: "CUT", WeightKg: 3, LotNo: "KZK-AI-" + suffix},
			{SKU: "KZ-KIYMA", Kind: "CUT", WeightKg: 4, LotNo: "KZY-AI-" + suffix},
			{SKU: "KZ-FIRE", Kind: "WASTE", WeightKg: 3},
		}}
	code, body = call(t, entry.ButcheryOperation, "POST", "/butchery/operations", req)
	var result ButcheryOperationResultDto
	if err := json.Unmarshal(body, &result); err != nil || code != 200 || result.Forecast == nil {
		t.Fatalf("dry run: %d %s %v", code, body, err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM butchery_operation WHERE op_no=?`, req.OpNo).Scan(&n); err != nil || n != 0 {
		t.Fatalf("dry run wrote data: %d %v", n, err)
	}
	req.DryRun = false
	code, body = call(t, entry.ButcheryOperation, "POST", "/butchery/operations", req)
	if err := json.Unmarshal(body, &result); err != nil || code != 201 || result.Forecast == nil {
		t.Fatalf("post: %d %s %v", code, body, err)
	}
	for i, p := range result.Forecast.Parts {
		if p.Kg != forecast.Parts[i].Kg || p.ActualKg == nil || p.Anomaly == nil {
			t.Fatalf("part %+v", p)
		}
		var pred, actual, lo, hi, level float64
		var anomaly bool
		err := db.QueryRow(`SELECT predicted_value, actual_value, interval_low, interval_high, interval_level, is_anomaly
            FROM ml_prediction mp JOIN butchery_operation bo ON bo.id=mp.ref_id
            WHERE bo.op_no=? AND mp.ref_type='butchery_operation' AND mp.model_name=? AND mp.target_name=?`,
			req.OpNo, championModel, p.Part+"_kg").Scan(&pred, &actual, &lo, &hi, &level, &anomaly)
		if err != nil || pred != p.Kg || actual != *p.ActualKg || lo != p.KgLow || hi != p.KgHigh || level != 0.8 || anomaly != *p.Anomaly {
			t.Fatalf("stored part %s: %g %g %g %g %g %v (%v), response %+v", p.Part, pred, actual, lo, hi, level, anomaly, err, p)
		}
	}
	if !*result.Forecast.Parts[0].Anomaly {
		t.Fatal("unusual leg weight must be flagged")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM ml_prediction mp JOIN butchery_operation bo ON bo.id=mp.ref_id
        WHERE bo.op_no=? AND mp.ref_type='butchery_operation'`, req.OpNo).Scan(&n); err != nil || n != 12 {
		t.Fatalf("want six model and six baseline rows: %d %v", n, err)
	}
	code, body = call(t, ai.Summary, "GET", "/ai/summary", nil)
	var summary AISummaryDto
	if err := json.Unmarshal(body, &summary); err != nil || code != 200 {
		t.Fatalf("summary: %d %s", code, body)
	}
	n = 0
	for _, row := range summary.Comparisons {
		if row.RefNo == req.OpNo {
			n++
			if row.Actual == nil || row.ModelPred == nil || row.BaselinePred == nil || row.Anomaly == nil {
				t.Fatalf("summary row %+v", row)
			}
		}
	}
	if n != 6 {
		t.Fatalf("summary has %d part rows", n)
	}
	// A failed duplicate operation must leave neither an operation nor additional predictions.
	req.OpNo += "X"
	code, body = call(t, entry.ButcheryOperation, "POST", "/butchery/operations", req)
	if code != 409 {
		t.Fatalf("duplicate part lot: %d %s", code, body)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM butchery_operation WHERE op_no=?`, req.OpNo).Scan(&n); err != nil || n != 0 {
		t.Fatalf("failed operation was not rolled back: %d %v", n, err)
	}
}
