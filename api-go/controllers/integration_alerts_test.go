package controllers

// Integration tests (MYSQL_TEST_DSN) for the alert inbox: alerts written by purchases, production
// receipts, butchery operations and refused POS sales, and the supervisor review.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ban-food-intelligence/api-go/models"
)

// alertsFor returns the alerts of one document or lot from GET /alerts.
func alertsFor(t *testing.T, db *sql.DB, refNo string) []AlertDto {
	t.Helper()
	code, body := call(t, NewAlertController(db).List, "GET", "/alerts?status=all", nil)
	var res AlertsDto
	if err := json.Unmarshal(body, &res); code != 200 || err != nil {
		t.Fatalf("GET /alerts: %d %s %v", code, body, err)
	}
	var out []AlertDto
	for _, a := range res.Alerts {
		if a.RefNo == refNo {
			out = append(out, a)
		}
	}
	return out
}

func TestIntegrationAlertPurchaseCostIncreaseAndReview(t *testing.T) {
	db := testDB(t)
	e := NewEntryController(db)
	run := runSuffix()
	today := time.Now().Format("2006-01-02")
	in30 := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	buy := func(n string, price float64) {
		t.Helper()
		code, body := call(t, e.Purchase, "POST", "/purchases", PurchaseRequest{
			ReceiptNo: "ALS-AL-" + n + run, SupplierCode: "TED-BAH-01", ReceiptDate: today,
			Lines: []PurchaseLineRequest{{SKU: "HM-BAHARAT", LotNo: "BAH-AL-" + n + run, Qty: 10, UnitPrice: price, ExpiryDate: in30}},
		})
		if code != http.StatusCreated {
			t.Fatalf("purchase %s: %d %s", n, code, body)
		}
	}
	for _, n := range []string{"A", "B", "C"} {
		buy(n, 24)
	}
	buy("D", 25) // +4.2 % on 24.00: normal
	buy("E", 31) // reference = mean of the last three (25, 24, 24) = 24.33: +27.4 %
	if got := alertsFor(t, db, "ALS-AL-D"+run); len(got) != 0 {
		t.Fatalf("a 4 %% increase raised an alert: %+v", got)
	}
	got := alertsFor(t, db, "ALS-AL-E"+run)
	if len(got) != 1 {
		t.Fatalf("want 1 alert for the 31.00 receipt, got %+v", got)
	}
	a := got[0]
	if a.Task != "PURCHASE_COST" || a.Direction != "COST_INCREASE" || a.Severity != "CRITICAL" || a.Status != "OPEN" ||
		a.LotNo != "BAH-AL-E"+run || a.Responsible == "" || a.Actual != 31 || math.Abs(a.Expected-24.333) > 0.001 ||
		math.Abs(a.EstLossEUR-66.67) > 0.005 || math.Abs(a.Score-27.397) > 0.001 {
		t.Fatalf("cost alert: %+v", a)
	}
	var stock float64
	_ = db.QueryRow(`SELECT sb.qty FROM stock_balance sb JOIN lot l ON l.id = sb.lot_id WHERE l.lot_no = ?`, "BAH-AL-E"+run).Scan(&stock)
	if stock != 10 {
		t.Fatalf("the alert changed stock: %.3f", stock)
	}

	// Review: supervisor only; closing needs a note; a closed alert is final; every step audited.
	sessions := NewSessionManager()
	review := sessions.RequireOperator(NewAlertController(db).Review)
	if code, body := call(t, review, "POST", "/alerts/review", AlertReviewRequest{ID: a.ID, Status: "ACKNOWLEDGED"}); code != http.StatusUnauthorized {
		t.Fatalf("review without session: %d %s", code, body)
	}
	token, _ := sessions.Issue("alert-supervisor")
	send := func(req AlertReviewRequest) int {
		t.Helper()
		payload, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", "/alerts/review", bytes.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		review(rec, r)
		return rec.Code
	}
	if code := send(AlertReviewRequest{ID: a.ID, Status: "RESOLVED"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("close without note: %d", code)
	}
	if code := send(AlertReviewRequest{ID: a.ID, Status: "IGNORED", Note: "x"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown status: %d", code)
	}
	if code := send(AlertReviewRequest{ID: 999999999, Status: "ACKNOWLEDGED"}); code != http.StatusNotFound {
		t.Fatalf("unknown alert: %d", code)
	}
	if code := send(AlertReviewRequest{ID: a.ID, Status: "ACKNOWLEDGED", Note: "Asked the supplier"}); code != 200 {
		t.Fatalf("acknowledge: %d", code)
	}
	if code := send(AlertReviewRequest{ID: a.ID, Status: "RESOLVED", Note: "New price list from 1 Nov"}); code != 200 {
		t.Fatalf("resolve: %d", code)
	}
	if code := send(AlertReviewRequest{ID: a.ID, Status: "FALSE_ALARM", Note: "changed my mind"}); code != http.StatusConflict {
		t.Fatalf("closed alert changed: %d", code)
	}
	a = alertsFor(t, db, "ALS-AL-E"+run)[0]
	if a.Status != "RESOLVED" || a.ResolvedBy != "alert-supervisor" || a.ResolvedAt == nil || a.ResolutionNote != "New price list from 1 Nov" {
		t.Fatalf("reviewed alert: %+v", a)
	}
	var audits int
	_ = db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE table_name = 'ml_anomaly_alert' AND row_id = ? AND action = 'ALERT_REVIEW'
		AND changed_by = 'alert-supervisor'`, a.ID).Scan(&audits)
	if audits != 2 {
		t.Fatalf("want 2 audited reviews, got %d", audits)
	}
	code, body := call(t, NewAlertController(db).List, "GET", "/alerts?status=closed", nil)
	var closed AlertsDto
	_ = json.Unmarshal(body, &closed)
	found := false
	for _, f := range closed.Feedback {
		found = found || (f.Task == "PURCHASE_COST" && f.Confirmed >= 1 && f.PrecisionObserved != nil && f.ConfirmedLossEUR >= 66.67)
	}
	if code != 200 || !found {
		t.Fatalf("feedback misses the confirmed cost alert: %d %s", code, body)
	}
	if code, _ := call(t, NewAlertController(db).List, "GET", "/alerts?status=maybe", nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown filter: %d", code)
	}
}

func TestIntegrationAlertLowYieldOnProductionReceipt(t *testing.T) {
	db := testDB(t)
	e := NewEntryController(db)
	run := runSuffix()
	today := time.Now().Format("2006-01-02")
	in10 := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	fat := 21.5
	for _, l := range []struct {
		supplier, sku, lot string
		qty, price         float64
		fat                *float64
	}{{"TED-ET-01", "HM-ET-DANA", "ET-AL-" + run, 100, 9.7, &fat}, {"TED-BAH-01", "HM-BAHARAT", "BAH-AY-" + run, 2, 24, nil},
		{"TED-BAG-01", "HM-BAGIRSAK", "BAG-AL-" + run, 5, 11.6, nil}} {
		code, body := call(t, e.Purchase, "POST", "/purchases", PurchaseRequest{ReceiptNo: "ALS-" + l.lot, SupplierCode: l.supplier, ReceiptDate: today,
			Lines: []PurchaseLineRequest{{SKU: l.sku, LotNo: l.lot, Qty: l.qty, UnitPrice: l.price, ExpiryDate: in10, FatPct: l.fat}}})
		if code != http.StatusCreated {
			t.Fatalf("purchase %s: %d %s", l.lot, code, body)
		}
	}
	drying := 5
	order := ProductionOrderRequest{OrderNo: "URT-AL-" + run, ProductSKU: "MM-SUCUK-1KG", SubcontractorCode: "FAS-01", PlannedQty: 85,
		ServiceUnitPrice: 5.5, DryingDays: &drying, OrderDate: today,
		Inputs: []ProductionInputRequest{{LotNo: "ET-AL-" + run, Qty: 100}, {LotNo: "BAH-AY-" + run, Qty: 2}, {LotNo: "BAG-AL-" + run, Qty: 5}}}
	code, body := call(t, e.ProductionOrder, "POST", "/production/orders", order)
	var res ProductionOrderResultDto
	_ = json.Unmarshal(body, &res)
	if code != http.StatusCreated || res.Forecast == nil {
		t.Fatalf("order: %d %s", code, body)
	}
	if got := alertsFor(t, db, order.OrderNo); len(got) != 0 {
		t.Fatalf("an order in progress has no actual to grade: %+v", got)
	}
	// 70 packs against a forecast of about 84: far outside the 98 % band (about 3.9 packs).
	code, body = call(t, NewProductionController(db).CalculateOrCompleteProduction, "POST", "/production", CompleteProductionRequest{
		OrderNo: order.OrderNo, ActualQty: 70, OutputLotNo: "SUC-AL-" + run, LotDate: today,
		ExpiryDate: time.Now().AddDate(0, 3, 0).Format("2006-01-02"),
	})
	if code != 200 {
		t.Fatalf("receipt: %d %s", code, body)
	}
	got := alertsFor(t, db, order.OrderNo)
	if len(got) != 1 {
		t.Fatalf("want 1 alert, got %+v", got)
	}
	a := got[0]
	missing := res.Forecast.Packages - 70
	if a.Task != "PRODUCTION_YIELD" || a.Direction != "LOW_YIELD" || a.Severity != "CRITICAL" || a.LotNo != "SUC-AL-"+run ||
		a.Uom != "PAKET" || a.Actual != 70 || math.Abs(a.Deviation+missing) > 0.01 || a.Score > -defaultThresholds["PRODUCTION_YIELD"].Crit ||
		math.Abs(a.EstLossEUR-missing*(*res.Forecast.EKP)) > 0.06 || a.Detector != "conformal_band" {
		t.Fatalf("low-yield alert: %+v (forecast %.1f packs, EKP %.4f)", a, res.Forecast.Packages, *res.Forecast.EKP)
	}
}

func TestIntegrationAlertExtraWasteOnButchery(t *testing.T) {
	db := testDB(t)
	e := NewEntryController(db)
	run := runSuffix()
	today := time.Now().Format("2006-01-02")
	carcass := "KUZU-AL-" + run
	code, body := call(t, e.Purchase, "POST", "/purchases", PurchaseRequest{
		ReceiptNo: "ALS-KA-" + run, SupplierCode: "TED-HAY-01", ReceiptDate: today,
		Lines: []PurchaseLineRequest{{SKU: "KZ-KARKAS", LotNo: carcass, Qty: 24, UnitPrice: 11.8, ExpiryDate: time.Now().AddDate(0, 0, 7).Format("2006-01-02")}},
	})
	if code != http.StatusCreated {
		t.Fatalf("carcass: %d %s", code, body)
	}
	// About 2.9 kg waste is expected for 24 kg (98 % band about 0.4 kg); 3.9 kg recorded, the leg 0.9 kg light.
	req := ButcheryOperationRequest{OpNo: "PRC-AL-" + run, InputLotNo: carcass, InputWeightKg: 24, AllocationMethod: "QUANTITY",
		OpDate: today, OperatorName: "OP-AL", Outputs: []ButcheryOutputRequest{
			{SKU: "KZ-BUT", Kind: "CUT", WeightKg: 5.3, LotNo: "KZB-AL-" + run},
			{SKU: "KZ-SIRT", Kind: "CUT", WeightKg: 3.3, LotNo: "KZS-AL-" + run},
			{SKU: "KZ-OMUZ", Kind: "CUT", WeightKg: 4.8, LotNo: "KZO-AL-" + run},
			{SKU: "KZ-KABURGA", Kind: "CUT", WeightKg: 2.9, LotNo: "KZK-AL-" + run},
			{SKU: "KZ-KIYMA", Kind: "CUT", WeightKg: 3.8, LotNo: "KZY-AL-" + run},
			{SKU: "KZ-FIRE", Kind: "WASTE", WeightKg: 3.9},
		}}
	dry := req
	dry.DryRun = true
	if code, body := call(t, e.ButcheryOperation, "POST", "/butchery/operations", dry); code != 200 {
		t.Fatalf("dry run: %d %s", code, body)
	}
	if got := alertsFor(t, db, req.OpNo); len(got) != 0 {
		t.Fatalf("a dry run wrote an alert: %+v", got)
	}
	code, body = call(t, e.ButcheryOperation, "POST", "/butchery/operations", req)
	if code != http.StatusCreated {
		t.Fatalf("butchery: %d %s", code, body)
	}
	got := alertsFor(t, db, req.OpNo)
	if len(got) != 1 {
		t.Fatalf("want 1 alert, got %+v", got)
	}
	a := got[0]
	if a.Task != "BUTCHERY_YIELD" || a.Direction != "FIRE_HIGH" || a.Severity != "CRITICAL" || a.LotNo != carcass ||
		a.Actual != 3.9 || a.Deviation <= 0.8 || math.Abs(a.EstLossEUR-a.Deviation*11.8) > 0.01 || a.Responsible != "OP-AL" {
		t.Fatalf("extra-waste alert: %+v", a)
	}
}

// A sale of a recalled lot refused at sync is an alert once per sale, also when the phone retries.
func TestIntegrationAlertRefusedSaleOfRecalledLot(t *testing.T) {
	db := testDB(t)
	run := runSuffix()
	lot := "SUC-AR-" + run
	shipmentLot(t, db, lot, 20, -5, 60)
	if code, body := call(t, NewRecallController(db).TriggerRecall, "POST", "/recall",
		RecallTriggerRequest{RecallCode: "RCL-AR-" + run, TriggerLot: lot}); code != http.StatusCreated {
		t.Fatalf("recall: %d %s", code, body)
	}
	pos := newTestPOS(t, "POS-AR-"+run)
	uuid := testUUIDv7()
	for i := 0; i < 2; i++ {
		rec := httptestRecorder(NewSyncController(db), signedSale(t, pos, lot, 2, uuid))
		var resp models.SyncBatchResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Rejected) != 1 || resp.Rejected[0].Code != "LOT_BLOCKED" {
			t.Fatalf("send %d: %s", i+1, rec.Body.String())
		}
	}
	got := alertsFor(t, db, lot)
	if len(got) != 1 {
		t.Fatalf("want 1 alert for the refused sale, got %+v", got)
	}
	a := got[0]
	if a.Task != "LOT_MOVEMENT" || a.Direction != "BLOCKED_SALE" || a.Severity != "CRITICAL" || a.EventRef != uuid ||
		a.Actual != 2 || a.LotNo != lot || a.Detail == "" {
		t.Fatalf("refused-sale alert: %+v", a)
	}
}
