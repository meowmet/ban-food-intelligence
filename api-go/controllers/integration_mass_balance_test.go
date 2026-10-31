package controllers

// Integration test (MYSQL_TEST_DSN) for the mass balance of a forward trace.

import (
	"encoding/json"
	"testing"
)

func massBalance(t *testing.T, ctrl *TraceController, lot string) (TraceResponse, map[string]MassBalanceLineDto) {
	t.Helper()
	code, body := call(t, ctrl.TraceHandler, "GET", "/trace/"+lot+"?direction=forward", nil)
	var tr TraceResponse
	if err := json.Unmarshal(body, &tr); code != 200 || err != nil || tr.MassBalance == nil {
		t.Fatalf("forward trace of %s: %d %s %v", lot, code, body, err)
	}
	lines := map[string]MassBalanceLineDto{}
	for _, l := range tr.MassBalance.Lines {
		lines[l.LotNo] = l
	}
	return tr, lines
}

func TestIntegrationMassBalanceAccountsForEveryPack(t *testing.T) {
	db := testDB(t)
	trace := NewTraceController(db)

	// The mandatory scenario: the 100 kg meat lot went into the sausage order in full.
	_, seed := massBalance(t, trace, "ET-2026-001")
	et := seed["ET-2026-001"]
	if et.Declared != 100 || et.UsedInLots != 100 || et.InStock != 0 || et.Unexplained != 0 || et.AccountedPct == nil || *et.AccountedPct != 100 {
		t.Fatalf("ET-2026-001: %+v", et)
	}
	if _, ok := seed["SUC-2026-045"]; !ok {
		t.Fatalf("the sausage lot is missing from the balance: %+v", seed)
	}

	// 20 packs: 8 shipped, 2 wasted, 1 returned to quarantine, 1 written off in a stock count.
	run := runSuffix()
	lot := "MB-" + run
	shipmentLot(t, db, lot, 20, -2, 30)
	if code, body := shipAs(t, NewShipmentController(db), ShipmentRequest{ShipmentNo: "SVK-MB-" + run, CustomerCode: "MKT-A",
		ShipDate: today(t, db), Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 8}}}); code != 201 {
		t.Fatalf("shipment: %d %s", code, body)
	}
	m := NewMovementsController(db)
	if code, body := call(t, m.Waste, "POST", "/movements/waste", WasteRequest{LotNo: lot, Warehouse: "ANA-DEPO", Qty: 2, Reason: "Ambalaj yırtık"}); code != 201 {
		t.Fatalf("waste: %d %s", code, body)
	}
	if code, body := call(t, m.Return, "POST", "/movements/return", ReturnRequest{LotNo: lot, Warehouse: "KARANTINA", CustomerCode: "MKT-A", Qty: 1}); code != 201 {
		t.Fatalf("return: %d %s", code, body)
	}
	tr, lines := massBalance(t, trace, lot)
	if !tr.MassBalance.Balanced {
		t.Fatalf("documents explain every pack, yet: %+v", lines[lot])
	}
	if _, err := db.Exec(`INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, note)
		SELECT l.id, w.id, 'ADJUSTMENT_OUT', -1, 18.16, 'count' FROM lot l, warehouse w WHERE l.lot_no = ? AND w.code = 'ANA-DEPO'`, lot); err != nil {
		t.Fatal(err)
	}
	tr, lines = massBalance(t, trace, lot)
	l := lines[lot]
	if l.Declared != 20 || l.Shipped != 8 || l.Wasted != 2 || l.Returned != 1 || l.CountDiff != -1 || l.InStock != 10 ||
		l.Accounted != 19 || l.Unexplained != 1 || l.AccountedPct == nil || *l.AccountedPct != 95 {
		t.Fatalf("balance of %s: %+v", lot, l)
	}
	if tr.MassBalance.Balanced || len(tr.MassBalance.Totals) != 1 {
		t.Fatalf("a pack missing in the count must unbalance the trace: %+v", tr.MassBalance)
	}
	if tot := tr.MassBalance.Totals[0]; tot.Uom != "PAKET" || tot.AtCustomers != 7 || tot.InStock != 10 || tot.Wasted != 2 || tot.Unexplained != 1 {
		t.Fatalf("final positions: %+v", tot)
	}
	// The backward trace has no balance: an input lot's other uses are not part of it.
	code, body := call(t, trace.TraceHandler, "GET", "/trace/"+lot+"?direction=backward", nil)
	var back TraceResponse
	_ = json.Unmarshal(body, &back)
	if code != 200 || back.MassBalance != nil {
		t.Fatalf("backward trace: %d %s", code, body)
	}
}
