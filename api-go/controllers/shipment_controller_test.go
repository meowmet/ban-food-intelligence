package controllers

import "testing"

func TestValidateShipment(t *testing.T) {
	price := -1.0
	ok := ShipmentRequest{ShipmentNo: "SVK-1", CustomerCode: "MKT-A", FromWarehouse: "ANA-DEPO", ShipDate: "2026-10-30",
		Lines: []ShipmentLineRequest{{LotNo: "SUC-1", Qty: 5}}}
	if fe := validateShipment(&ok); len(fe) != 0 {
		t.Fatalf("valid request refused: %v", fe)
	}
	cases := map[string]struct {
		edit  func(*ShipmentRequest)
		field string
	}{
		"both sources":      {func(r *ShipmentRequest) { r.FromCustomerCode = "MKT-B" }, "from_warehouse"},
		"to itself":         {func(r *ShipmentRequest) { r.FromWarehouse = ""; r.FromCustomerCode = "MKT-A" }, "from_customer_code"},
		"no number":         {func(r *ShipmentRequest) { r.ShipmentNo = " " }, "shipment_no"},
		"bad date":          {func(r *ShipmentRequest) { r.ShipDate = "30.10.2026" }, "ship_date"},
		"no lines":          {func(r *ShipmentRequest) { r.Lines = nil }, "lines"},
		"zero qty":          {func(r *ShipmentRequest) { r.Lines[0].Qty = 0 }, "lines[0].qty"},
		"negative price":    {func(r *ShipmentRequest) { r.Lines[0].UnitPrice = &price }, "lines[0].unit_price"},
		"same lot twice":    {func(r *ShipmentRequest) { r.Lines = append(r.Lines, ShipmentLineRequest{LotNo: "SUC-1", Qty: 1}) }, "lines[1].lot_no"},
		"long delivery no.": {func(r *ShipmentRequest) { r.DeliveryNoteNo = string(make([]byte, 51)) }, "delivery_note_no"},
	}
	for name, tc := range cases {
		req := ok
		req.Lines = append([]ShipmentLineRequest(nil), ok.Lines...)
		tc.edit(&req)
		if fe := validateShipment(&req); fe[tc.field] == "" {
			t.Errorf("%s: want an error on %s, got %v", name, tc.field, fe)
		}
	}
}
