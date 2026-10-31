package controllers

// Integration test (MYSQL_TEST_DSN) for stock counts: adjustments, guards, alerts, mass balance.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIntegrationStockCountBooksDifferencesWithReason(t *testing.T) {
	db := testDB(t)
	run := runSuffix()
	short, even, recalled := "CNT-S-"+run, "CNT-E-"+run, "CNT-R-"+run
	for _, lot := range []string{short, even, recalled} {
		shipmentLot(t, db, lot, 20, -3, 30)
	}
	if code, body := call(t, NewRecallController(db).TriggerRecall, "POST", "/recall",
		RecallTriggerRequest{RecallCode: "RCL-CNT-" + run, TriggerLot: recalled}); code != http.StatusCreated {
		t.Fatalf("recall: %d %s", code, body)
	}
	sessions := NewSessionManager()
	create := sessions.RequireOperator(NewStockCountController(db).Create)
	token, _ := sessions.Issue("count-supervisor")
	send := func(req StockCountRequest) (int, []byte) {
		t.Helper()
		payload, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", "/stock-counts", bytes.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		create(rec, r)
		return rec.Code, rec.Body.Bytes()
	}
	exp := func(v float64) *float64 { return &v }
	countNo := "SAY-IT-" + run
	counted := func() int {
		var n int
		_ = db.QueryRow(`SELECT COUNT(*) FROM stock_count WHERE count_no = ?`, countNo).Scan(&n)
		return n
	}

	if code, _ := call(t, create, "POST", "/stock-counts", StockCountRequest{Warehouse: "ANA-DEPO"}); code != http.StatusUnauthorized {
		t.Fatalf("count without session: %d", code)
	}
	for name, c := range map[string]struct {
		line StockCountLineRequest
		want int
	}{
		"no reason":   {StockCountLineRequest{LotNo: short, CountedQty: 17}, http.StatusUnprocessableEntity},
		"half a pack": {StockCountLineRequest{LotNo: short, CountedQty: 16.5, Reason: "x"}, http.StatusUnprocessableEntity},
		"stale list":  {StockCountLineRequest{LotNo: short, CountedQty: 17, ExpectedQty: exp(25), Reason: "x"}, http.StatusConflict},
		"unknown lot": {StockCountLineRequest{LotNo: "NO-LOT-" + run, CountedQty: 1, Reason: "x"}, http.StatusNotFound},
	} {
		code, body := send(StockCountRequest{CountNo: countNo, Warehouse: "ANA-DEPO", Lines: []StockCountLineRequest{
			{LotNo: even, CountedQty: 20}, c.line}})
		if code != c.want || counted() != 0 {
			t.Fatalf("%s: want %d and nothing saved, got %d %s (saved %d)", name, c.want, code, body, counted())
		}
	}

	code, body := send(StockCountRequest{CountNo: countNo, Warehouse: "ANA-DEPO", Note: "Ay sonu", Lines: []StockCountLineRequest{
		{LotNo: short, CountedQty: 17, ExpectedQty: exp(20), Reason: "Ambalaj yırtık, imha edilmiş ama kaydı yok"},
		{LotNo: even, CountedQty: 20, ExpectedQty: exp(20)},
		{LotNo: recalled, CountedQty: 19, Reason: "Bulunamadı"},
	}})
	var res StockCountDto
	_ = json.Unmarshal(body, &res)
	if code != http.StatusCreated || len(res.Lines) != 3 || res.CountedBy != "count-supervisor" {
		t.Fatalf("count: %d %s", code, body)
	}
	if l := res.Lines[0]; l.SystemQty != 20 || l.DiffQty != -3 || l.MovementType != "ADJUSTMENT_OUT" || l.ValueEUR != -54.48 {
		t.Fatalf("short line: %+v", l)
	}
	if l := res.Lines[1]; l.DiffQty != 0 || l.MovementType != "" || l.Reason != "" {
		t.Fatalf("even line: %+v", l)
	}
	if res.ShortageEUR != 72.64 || res.SurplusEUR != 0 {
		t.Fatalf("totals: %+v", res)
	}
	var stock float64
	_ = db.QueryRow(`SELECT sb.qty FROM stock_balance sb JOIN lot l ON l.id = sb.lot_id JOIN warehouse w ON w.id = sb.warehouse_id
		WHERE l.lot_no = ? AND w.code = 'ANA-DEPO'`, short).Scan(&stock)
	if stock != 17 {
		t.Fatalf("ledger after the count: %.3f, want 17", stock)
	}
	if code, _ := send(StockCountRequest{CountNo: countNo, Warehouse: "ANA-DEPO", Lines: []StockCountLineRequest{{LotNo: even, CountedQty: 20}}}); code != http.StatusConflict {
		t.Fatalf("reused count number: %d", code)
	}

	// The shortage shows in the alerts (critical for the recalled lot) and as a gap in the mass balance.
	byLot := map[string]AlertDto{}
	for _, a := range alertsFor(t, db, countNo) {
		byLot[a.LotNo] = a
	}
	if a := byLot[short]; a.Direction != "COUNT_SHORTAGE" || a.Severity != "WARN" || a.EstLossEUR != 54.48 || a.Responsible != "count-supervisor" {
		t.Fatalf("shortage alert: %+v", a)
	}
	if a := byLot[recalled]; a.Severity != "CRITICAL" {
		t.Fatalf("recalled lot short: %+v", a)
	}
	if _, ok := byLot[even]; ok || len(byLot) != 2 {
		t.Fatalf("alerts: %+v", byLot)
	}
	_, lines := massBalance(t, NewTraceController(db), short)
	if l := lines[short]; l.CountDiff != -3 || l.Unexplained != 3 {
		t.Fatalf("mass balance after the count: %+v", l)
	}

	code, body = call(t, NewStockCountController(db).List, "GET", "/stock-counts", nil)
	var list []StockCountDto
	_ = json.Unmarshal(body, &list)
	if code != 200 || len(list) == 0 || list[0].CountNo != countNo || len(list[0].Lines) != 3 || list[0].ShortageEUR != 72.64 {
		t.Fatalf("count history: %d %s", code, body)
	}
}
