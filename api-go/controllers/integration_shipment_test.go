package controllers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// shipmentLot creates a finished-goods lot with qty packs in ANA-DEPO; expiry is relative to today.
func shipmentLot(t *testing.T, db *sql.DB, lot string, qty float64, lotDays, expiryDays int) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO lot(lot_no,product_id,origin_type,lot_date,expiry_date,initial_qty,uom_code,unit_cost)
        SELECT ?,id,'PRODUCTION',DATE_ADD(CURRENT_DATE,INTERVAL ? DAY),DATE_ADD(CURRENT_DATE,INTERVAL ? DAY),?,'PAKET',18.16
        FROM product WHERE sku='MM-SUCUK-1KG'`, lot, lotDays, expiryDays, qty); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO stock_movement(lot_id,warehouse_id,movement_type,qty,unit_cost)
        SELECT l.id,w.id,'PRODUCTION_RECEIPT',?,18.16 FROM lot l,warehouse w WHERE l.lot_no=? AND w.code='ANA-DEPO'`, qty, lot); err != nil {
		t.Fatal(err)
	}
}

// shipAs posts a shipment with an authenticated operator, as RequireOperator would.
func shipAs(t *testing.T, c *ShipmentController, req ShipmentRequest) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(req)
	r := httptest.NewRequest("POST", "/shipments", &buf)
	r = r.WithContext(context.WithValue(r.Context(), operatorKey{}, "it-shipper"))
	rec := httptest.NewRecorder()
	c.Create(rec, r)
	return rec.Code, rec.Body.Bytes()
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var e APIError
	_ = json.Unmarshal(body, &e)
	return e.Code
}

func today(t *testing.T, db *sql.DB) string {
	t.Helper()
	var d string
	if err := db.QueryRow(`SELECT DATE_FORMAT(CURRENT_DATE, '%Y-%m-%d')`).Scan(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestIntegrationShipmentFirstAndSecondTier(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	c := NewShipmentController(db)
	run := runSuffix()
	lot := "SHP-" + run
	shipmentLot(t, db, lot, 40, -1, 30)
	day := today(t, db)
	stock := func() float64 {
		var q float64
		_ = db.QueryRow(`SELECT COALESCE(SUM(sb.qty),0) FROM stock_balance sb JOIN lot l ON l.id=sb.lot_id
            JOIN warehouse w ON w.id=sb.warehouse_id WHERE l.lot_no=? AND w.code='ANA-DEPO'`, lot).Scan(&q)
		return q
	}
	shipments := func(no string) int {
		var n int
		_ = db.QueryRow(`SELECT COUNT(*) FROM shipment WHERE shipment_no=?`, no).Scan(&n)
		return n
	}

	// First tier: 25 of 40 packs to Market A; price defaults to the VKP, the trigger books
	// the SHIPMENT movement as the signed-in operator.
	code, body := shipAs(t, c, ShipmentRequest{ShipmentNo: "SVK-IT-" + run, CustomerCode: "MKT-A", ShipDate: day,
		DeliveryNoteNo: "IRS-IT-" + run, Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 25}}})
	if code != http.StatusCreated {
		t.Fatalf("first tier: %d %s", code, body)
	}
	var s ShipmentDto
	_ = json.Unmarshal(body, &s)
	if s.Tier != 1 || s.FromWarehouse != "ANA-DEPO" || len(s.Lines) != 1 || s.Lines[0].UnitPrice == nil ||
		*s.Lines[0].UnitPrice != 24.9 || *s.Lines[0].LineTotal != 622.5 {
		t.Fatalf("first tier stored as %+v", s)
	}
	if got := stock(); got != 15 {
		t.Fatalf("stock after shipment %.3f, want 15", got)
	}
	var by string
	_ = db.QueryRow(`SELECT sm.created_by FROM stock_movement sm JOIN lot l ON l.id=sm.lot_id
        WHERE l.lot_no=? AND sm.movement_type='SHIPMENT'`, lot).Scan(&by)
	if by != "it-shipper" {
		t.Fatalf("SHIPMENT movement created_by %q, want the operator", by)
	}
	// The operator variable must not leak into later statements on pooled connections.
	var leaked sql.NullString
	for i := 0; i < 5; i++ {
		_ = db.QueryRow(`SELECT @app_user`).Scan(&leaked)
		if leaked.Valid {
			t.Fatalf("@app_user still set on a pooled connection: %q", leaked.String)
		}
	}

	// More than the stock: the trigger refuses it and nothing is left behind.
	code, body = shipAs(t, c, ShipmentRequest{ShipmentNo: "SVK-IT-B" + run, CustomerCode: "MKT-B", ShipDate: day,
		Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 20}}})
	if code != http.StatusConflict || errorCode(t, body) != "INSUFFICIENT_STOCK" || shipments("SVK-IT-B"+run) != 0 {
		t.Fatalf("over stock: %d %s, shipment rows %d", code, body, shipments("SVK-IT-B"+run))
	}

	// Packs are whole units; a shipment cannot be dated in the future or come from quarantine.
	code, body = shipAs(t, c, ShipmentRequest{ShipmentNo: "SVK-IT-C" + run, CustomerCode: "MKT-B", ShipDate: day,
		Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 1.5}}})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("1.5 packs: want 422, got %d %s", code, body)
	}
	code, body = shipAs(t, c, ShipmentRequest{ShipmentNo: "SVK-IT-C" + run, CustomerCode: "MKT-B", ShipDate: "2099-01-01",
		Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 1}}})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("future date: want 422, got %d %s", code, body)
	}
	code, body = shipAs(t, c, ShipmentRequest{ShipmentNo: "SVK-IT-C" + run, CustomerCode: "MKT-B", FromWarehouse: "KARANTINA", ShipDate: day,
		Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 1}}})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("from quarantine: want 422, got %d %s", code, body)
	}
	code, _ = shipAs(t, c, ShipmentRequest{ShipmentNo: "SVK-IT-C" + run, CustomerCode: "NOPE", ShipDate: day,
		Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 1}}})
	if code != http.StatusNotFound {
		t.Fatalf("unknown customer: want 404, got %d", code)
	}
	code, _ = shipAs(t, c, ShipmentRequest{ShipmentNo: "SVK-IT-" + run, CustomerCode: "MKT-B", ShipDate: day,
		Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 1}}})
	if code != http.StatusConflict {
		t.Fatalf("used shipment number: want 409, got %d", code)
	}
	if got := stock(); got != 15 {
		t.Fatalf("refused shipments changed the stock: %.3f", got)
	}

	// Second tier: Market A reports 10 packs forwarded to the restaurant. No stock moves; the
	// next 16 would exceed the 15 Market A still holds.
	code, body = shipAs(t, c, ShipmentRequest{ShipmentNo: "MKA-IT-" + run, CustomerCode: "REST-01", FromCustomerCode: "MKT-A",
		ShipDate: day, Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 10}}})
	if code != http.StatusCreated {
		t.Fatalf("second tier: %d %s", code, body)
	}
	var s2 ShipmentDto
	_ = json.Unmarshal(body, &s2)
	if s2.Tier != 2 || s2.FromCustomerCode != "MKT-A" || s2.FromWarehouse != "" || s2.Lines[0].UnitPrice != nil {
		t.Fatalf("second tier stored as %+v", s2)
	}
	if got := stock(); got != 15 {
		t.Fatalf("an onward delivery moved our stock: %.3f", got)
	}
	code, body = shipAs(t, c, ShipmentRequest{ShipmentNo: "MKA-IT-B" + run, CustomerCode: "MKT-C", FromCustomerCode: "MKT-A",
		ShipDate: day, Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 16}}})
	if code != http.StatusConflict || errorCode(t, body) != "EXCEEDS_DELIVERED" || shipments("MKA-IT-B"+run) != 0 {
		t.Fatalf("onward delivery above holding: want 409, got %d %s", code, body)
	}

	// Forward trace shows the recorded second tier via Market A.
	code, body = call(t, NewTraceController(db).TraceHandler, "GET", "/trace/"+lot+"?direction=forward", nil)
	var tr TraceResponse
	if err := json.Unmarshal(body, &tr); code != 200 || err != nil {
		t.Fatalf("trace %d %s", code, body)
	}
	var tier2 bool
	for _, sh := range tr.Shipments {
		if sh.CustomerCode == "REST-01" && sh.Tier == 2 && sh.ReceivedVia == "Market A" && sh.Qty == 10 {
			tier2 = true
		}
	}
	if !tier2 {
		t.Fatalf("trace misses the recorded second tier: %+v", tr.Shipments)
	}

	// After a recall our stock cannot be shipped, but a customer's onward delivery that already
	// happened can still be recorded: that is what the recall needs to reach the next buyer.
	status, body := call(t, NewRecallController(db).TriggerRecall, "POST", "/recall", RecallTriggerRequest{RecallCode: "RCL-SHP-" + run, TriggerLot: lot})
	if status != http.StatusCreated {
		t.Fatalf("recall %d %s", status, body)
	}
	code, body = shipAs(t, c, ShipmentRequest{ShipmentNo: "SVK-IT-D" + run, CustomerCode: "MKT-B", ShipDate: day,
		Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 1}}})
	if code != http.StatusConflict || errorCode(t, body) != "LOT_BLOCKED" {
		t.Fatalf("recalled lot shipped: %d %s", code, body)
	}
	code, body = shipAs(t, c, ShipmentRequest{ShipmentNo: "MKA-IT-C" + run, CustomerCode: "MKT-C", FromCustomerCode: "MKT-A",
		ShipDate: day, Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 5}}})
	if code != http.StatusCreated {
		t.Fatalf("onward delivery of a recalled lot not recordable: %d %s", code, body)
	}
	// The recall report reads shipments live: Market C, reported after the recall, is on the list.
	code, body = call(t, NewRecallController(db).TriggerRecall, "GET", "/recall", nil)
	var recalls []RecallEventDto
	if err := json.Unmarshal(body, &recalls); code != 200 || err != nil {
		t.Fatalf("recall list %d %s", code, body)
	}
	var marketC bool
	for _, rc := range recalls {
		for _, m := range rc.AffectedMarkets {
			if rc.RecallCode == "RCL-SHP-"+run && m.CustomerCode == "MKT-C" && m.Tier == 2 && m.QtyShipped == 5 {
				marketC = true
			}
		}
	}
	if !marketC {
		t.Fatalf("recall misses the onward delivery recorded after it: %s", body)
	}

	// GET /shipments lists the stored document with its lines.
	code, body = call(t, c.List, "GET", "/shipments?shipment_no=SVK-IT-"+run, nil)
	var list []ShipmentDto
	if err := json.Unmarshal(body, &list); code != 200 || err != nil || len(list) != 1 || list[0].Lines[0].LotNo != lot ||
		list[0].DeliveryNoteNo != "IRS-IT-"+run {
		t.Fatalf("list %d %s", code, body)
	}
}

func TestIntegrationShipmentOfExpiredLotIsRejected(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	run := runSuffix()
	lot := "SHX-" + run
	shipmentLot(t, db, lot, 10, -20, -1)
	code, body := shipAs(t, NewShipmentController(db), ShipmentRequest{ShipmentNo: "SVK-ITX-" + run, CustomerCode: "MKT-A",
		ShipDate: today(t, db), Lines: []ShipmentLineRequest{{LotNo: lot, Qty: 1}}})
	if code != http.StatusConflict || errorCode(t, body) != "LOT_EXPIRED" {
		t.Fatalf("expired lot shipped: %d %s", code, body)
	}
}

func TestIntegrationMasterDataLists(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	code, body := call(t, NewLotController(db).Warehouses, "GET", "/warehouses", nil)
	var whs []WarehouseDto
	if err := json.Unmarshal(body, &whs); code != 200 || err != nil || len(whs) < 4 || whs[0].Code != "ANA-DEPO" {
		t.Fatalf("warehouses %d %s", code, body)
	}
	code, body = call(t, NewShipmentController(db).Customers, "GET", "/customers", nil)
	var cs []CustomerDto
	if err := json.Unmarshal(body, &cs); code != 200 || err != nil || len(cs) < 5 || cs[0].Code != "MKT-A" || cs[0].ContactPerson == "" {
		t.Fatalf("customers %d %s", code, body)
	}
}
