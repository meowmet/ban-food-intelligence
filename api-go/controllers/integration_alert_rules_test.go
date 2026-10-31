package controllers

// Integration test (MYSQL_TEST_DSN) for configurable alert thresholds and missed findings.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIntegrationAlertThresholdsAreConfigurationAndMissesAreCounted(t *testing.T) {
	db := testDB(t)
	ctrl := NewAlertController(db)
	sessions := NewSessionManager()
	token, _ := sessions.Issue("rules-supervisor")
	post := func(h http.HandlerFunc, path string, body interface{}) (int, []byte) {
		t.Helper()
		payload, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		sessions.RequireOperator(h)(rec, r)
		return rec.Code, rec.Body.Bytes()
	}
	t.Cleanup(func() { // other tests grade with the defaults
		d := defaultThresholds["PURCHASE_COST"]
		post(ctrl.UpdateRule, "/alerts/rules", AlertRuleRequest{Rule: "PURCHASE_COST", Warn: d.Warn, Crit: d.Crit})
	})

	code, body := call(t, ctrl.Rules, "GET", "/alerts/rules", nil)
	var rules AlertRulesDto
	if err := json.Unmarshal(body, &rules); code != 200 || err != nil || len(rules.Rules) != 4 {
		t.Fatalf("GET /alerts/rules: %d %s %v", code, body, err)
	}
	var calibration map[string]interface{}
	if json.Unmarshal(rules.Calibration, &calibration) != nil || calibration["butchery_yield"] == nil || calibration["purchase_cost"] == nil {
		t.Fatalf("the synthetic stress test is not served: %s", rules.Calibration)
	}
	if rules.Rules[1].Rule != "BUTCHERY_YIELD" || rules.Rules[1].DefaultWarn != 1.5 || rules.ObservedPriceChanges.Changes == 0 {
		t.Fatalf("rules: %+v, observed %+v", rules.Rules, rules.ObservedPriceChanges)
	}

	// Only a supervisor changes a threshold; warn must stay below crit.
	if code, _ := call(t, sessions.RequireOperator(ctrl.UpdateRule), "POST", "/alerts/rules", AlertRuleRequest{Rule: "PURCHASE_COST", Warn: 20, Crit: 30}); code != http.StatusUnauthorized {
		t.Fatalf("change without session: %d", code)
	}
	for _, bad := range []AlertRuleRequest{{Rule: "PURCHASE_COST", Warn: 30, Crit: 20}, {Rule: "NOPE", Warn: 1, Crit: 2}, {Rule: "PRODUCTION_YIELD", Warn: 1, Crit: 50}} {
		if code, body := post(ctrl.UpdateRule, "/alerts/rules", bad); code != http.StatusUnprocessableEntity {
			t.Fatalf("%+v: want 422, got %d %s", bad, code, body)
		}
	}
	if code, body := post(ctrl.UpdateRule, "/alerts/rules", AlertRuleRequest{Rule: "PURCHASE_COST", Warn: 20, Crit: 30}); code != 200 {
		t.Fatalf("change: %d %s", code, body)
	}
	var audits int
	_ = db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE table_name = 'alert_threshold' AND action = 'THRESHOLD_CHANGE' AND changed_by = 'rules-supervisor'`).Scan(&audits)
	if audits == 0 {
		t.Fatal("threshold change not audited")
	}

	// The next purchase is graded with the new values: +29 % is a warning at 20/30, not critical.
	e := NewEntryController(db)
	run := runSuffix()
	day := time.Now().Format("2006-01-02")
	in30 := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	for i, price := range []float64{24, 24, 24, 31} {
		n := string(rune('A' + i))
		if code, body := call(t, e.Purchase, "POST", "/purchases", PurchaseRequest{ReceiptNo: "ALS-TH-" + n + run, SupplierCode: "TED-BAH-01", ReceiptDate: day,
			Lines: []PurchaseLineRequest{{SKU: "HM-BAHARAT", LotNo: "BAH-TH-" + n + run, Qty: 10, UnitPrice: price, ExpiryDate: in30}}}); code != 201 {
			t.Fatalf("purchase %s: %d %s", n, code, body)
		}
	}
	got := alertsFor(t, db, "ALS-TH-D"+run)
	if len(got) != 1 || got[0].Severity != "WARN" || got[0].ThresholdWarn != 20 || got[0].ThresholdCrit != 30 {
		t.Fatalf("graded with the changed thresholds: %+v", got)
	}

	// A problem found by hand that no alert flagged counts against the check's recall.
	for _, bad := range []AlertMissRequest{{Task: "NOPE", Note: "x"}, {Task: "PURCHASE_COST"}, {Task: "PURCHASE_COST", Note: "x", EstLossEUR: -1}} {
		if code, _ := post(ctrl.Missed, "/alerts/missed", bad); code != http.StatusUnprocessableEntity {
			t.Fatalf("%+v: want 422, got %d", bad, code)
		}
	}
	if code, _ := post(ctrl.Missed, "/alerts/missed", AlertMissRequest{Task: "PURCHASE_COST", LotNo: "NO-LOT-" + run, Note: "x"}); code != http.StatusNotFound {
		t.Fatalf("unknown lot: %d", code)
	}
	note := "Invoice checked by hand: casing 9 % above contract (" + run + ")"
	if code, body := post(ctrl.Missed, "/alerts/missed", AlertMissRequest{Task: "PURCHASE_COST", RefNo: "ALS-TH-C" + run,
		LotNo: "BAH-TH-C" + run, Note: note, EstLossEUR: 12.5}); code != http.StatusCreated {
		t.Fatalf("missed finding: %d %s", code, body)
	}
	code, body = call(t, ctrl.List, "GET", "/alerts?status=all", nil)
	var all AlertsDto
	_ = json.Unmarshal(body, &all)
	found := false
	for _, m := range all.MissedRecent {
		found = found || (m.Note == note && m.LotNo == "BAH-TH-C"+run && m.FoundBy == "rules-supervisor" && m.EstLossEUR == 12.5)
	}
	counted := false
	for _, m := range all.Missed {
		counted = counted || (m.Task == "PURCHASE_COST" && m.Findings >= 1)
	}
	if code != 200 || !found || !counted {
		t.Fatalf("missed findings in GET /alerts: %d %s", code, body)
	}
}
