package controllers

import (
	"net/http"
	"testing"
)

// Packs are counted: no movement may book half a pack. Weights stay fractional.
func TestIntegrationMovementsBookWholePacks(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	c := NewMovementsController(db)
	run := runSuffix()
	lot := "PCK-" + run
	shipmentLot(t, db, lot, 10, -1, 30)

	for name, tc := range map[string]struct {
		h    http.HandlerFunc
		url  string
		body interface{}
	}{
		"transfer": {c.Transfer, "/movements/transfer", TransferRequest{LotNo: lot, FromWarehouse: "ANA-DEPO", ToWarehouse: "MAGAZA-01", Qty: 1.5}},
		"waste":    {c.Waste, "/movements/waste", WasteRequest{LotNo: lot, Warehouse: "ANA-DEPO", Qty: 0.5, Reason: "torn pack"}},
		"rework":   {c.Rework, "/rework", ReworkRequest{InputLotNo: lot, Warehouse: "ANA-DEPO", QtyConsumed: 2, OutputLotNo: "PCK-R-" + run, OutputQty: 1.5}},
	} {
		if code, body := call(t, tc.h, "POST", tc.url, tc.body); code != http.StatusUnprocessableEntity {
			t.Errorf("%s with a fraction of a pack: want 422, got %d %s", name, code, body)
		}
	}
	if code, body := call(t, c.Transfer, "POST", "/movements/transfer", TransferRequest{LotNo: lot, FromWarehouse: "ANA-DEPO", ToWarehouse: "MAGAZA-01", Qty: 2}); code != http.StatusCreated {
		t.Fatalf("whole packs: %d %s", code, body)
	}
	var stock float64
	_ = db.QueryRow(`SELECT SUM(qty) FROM stock_balance sb JOIN lot l ON l.id = sb.lot_id WHERE l.lot_no = ?`, lot).Scan(&stock)
	if stock != 10 {
		t.Fatalf("refused movements changed the stock: %.3f", stock)
	}
}
