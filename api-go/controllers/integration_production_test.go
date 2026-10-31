package controllers

// Integration test (MYSQL_TEST_DSN) for an external production order recorded through the API:
// raw material bought, order + material dispatch + live AI forecast in one transaction, finished
// goods received, forecast compared with the actual.

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"
)

func TestIntegrationProductionOrderWithLiveForecast(t *testing.T) {
	db := testDB(t)
	e := NewEntryController(db)
	run := runSuffix()
	today := time.Now().Format("2006-01-02")
	in10 := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	fat := 21.5

	buy := func(supplier, sku, lot string, qty, price float64, fatPct *float64) {
		code, body := call(t, e.Purchase, "POST", "/purchases", PurchaseRequest{
			ReceiptNo: "ALS-PO-" + lot, SupplierCode: supplier, ReceiptDate: today,
			Lines: []PurchaseLineRequest{{SKU: sku, LotNo: lot, Qty: qty, UnitPrice: price, ExpiryDate: in10, FatPct: fatPct}},
		})
		if code != http.StatusCreated {
			t.Fatalf("purchase %s: %d %s", lot, code, body)
		}
	}
	meat, spice, casing := "ET-PO-"+run, "BAH-PO-"+run, "BAG-PO-"+run
	buy("TED-ET-01", "HM-ET-DANA", meat, 100, 9.7, &fat)
	buy("TED-BAH-01", "HM-BAHARAT", spice, 2, 24, nil)
	buy("TED-BAG-01", "HM-BAGIRSAK", casing, 5, 11.6, nil)
	drying := 5
	order := ProductionOrderRequest{
		OrderNo: "URT-IT-" + run, ProductSKU: "MM-SUCUK-1KG", SubcontractorCode: "FAS-01", PlannedQty: 85,
		ServiceUnitPrice: 5.5, DryingDays: &drying, OrderDate: today,
		Inputs: []ProductionInputRequest{{LotNo: meat, Qty: 100}, {LotNo: spice, Qty: 2}, {LotNo: casing, Qty: 5}},
	}

	// All or nothing: a second input that does not exist leaves no order and moves no meat.
	bad := order
	bad.Inputs = []ProductionInputRequest{{LotNo: meat, Qty: 100}, {LotNo: "NO-SUCH-LOT-" + run, Qty: 1}}
	if code, body := call(t, e.ProductionOrder, "POST", "/production/orders", bad); code != http.StatusNotFound {
		t.Fatalf("unknown input lot: want 404, got %d %s", code, body)
	}
	var orders, meatLeft int
	var meatStock float64
	_ = db.QueryRow(`SELECT COUNT(*) FROM production_order WHERE order_no = ?`, order.OrderNo).Scan(&orders)
	_ = db.QueryRow(`SELECT sb.qty FROM stock_balance sb JOIN lot l ON l.id = sb.lot_id JOIN warehouse w ON w.id = sb.warehouse_id
		WHERE l.lot_no = ? AND w.code = 'ANA-DEPO'`, meat).Scan(&meatStock)
	if orders != 0 || meatStock != 100 {
		t.Fatalf("failed order left data: %d orders, %.3f kg meat in ANA-DEPO", orders, meatStock)
	}
	dup := order
	dup.Inputs = []ProductionInputRequest{{LotNo: meat, Qty: 50}, {LotNo: meat, Qty: 50}}
	if code, _ := call(t, e.ProductionOrder, "POST", "/production/orders", dup); code != http.StatusUnprocessableEntity {
		t.Fatalf("lot listed twice: want 422, got %d", code)
	}

	// Preview (dry_run): the same checks and the forecast, then rolled back. An unknown lot gives
	// the same 404 as saving would; a valid order gives its forecast and writes nothing.
	badPreview := bad
	badPreview.DryRun = true
	if code, body := call(t, e.ProductionOrder, "POST", "/production/orders", badPreview); code != http.StatusNotFound {
		t.Fatalf("preview with unknown input lot: want 404, got %d %s", code, body)
	}
	preview := order
	preview.DryRun = true
	pcode, pbody := call(t, e.ProductionOrder, "POST", "/production/orders", preview)
	var pre ProductionOrderResultDto
	_ = json.Unmarshal(pbody, &pre)
	if pcode != http.StatusOK || pre.Status != "DRY_RUN" || !near(pre.MaterialCost, 1076) || pre.Forecast == nil {
		t.Fatalf("preview: %d %s", pcode, pbody)
	}
	var written int
	_ = db.QueryRow(`SELECT (SELECT COUNT(*) FROM production_order WHERE order_no = ?)
		+ (SELECT COUNT(*) FROM stock_movement sm JOIN lot l ON l.id = sm.lot_id
		   WHERE l.lot_no IN (?, ?, ?) AND sm.movement_type <> 'PURCHASE_IN')`, order.OrderNo, meat, spice, casing).Scan(&written)
	if written != 0 {
		t.Fatalf("preview wrote %d rows (order or movements)", written)
	}

	// The order: material at the subcontractor, cost snapshot, forecast stored next to the baseline.
	code, body := call(t, e.ProductionOrder, "POST", "/production/orders", order)
	if code != http.StatusCreated {
		t.Fatalf("create order: %d %s", code, body)
	}
	var res ProductionOrderResultDto
	_ = json.Unmarshal(body, &res)
	if res.Status != "MATERIAL_SENT" || !near(res.MaterialCost, 1076) || res.Forecast == nil {
		t.Fatalf("create order result: %s", body)
	}
	fc := res.Forecast
	if fc.Packages < 75 || fc.Packages > 95 || *fc.PackagesLow >= fc.Packages || *fc.PackagesHigh <= fc.Packages ||
		fc.BaselinePackages == nil || math.Abs(*fc.EKP-(1076+fc.Packages*5.5)/fc.Packages) > 0.01 {
		t.Fatalf("forecast not plausible: %s", body)
	}
	if fc.Packages != pre.Forecast.Packages || *fc.EKP != *pre.Forecast.EKP {
		t.Fatalf("saved forecast %.1f / %.4f differs from the preview %.1f / %.4f", fc.Packages, *fc.EKP, pre.Forecast.Packages, *pre.Forecast.EKP)
	}
	// stored as DEMO: the test API runs without DATA_ORIGIN=FIELD
	_ = db.QueryRow(`SELECT COUNT(*) FROM ml_prediction mp JOIN production_order po ON po.id = mp.ref_id
		WHERE mp.ref_type = 'production_order' AND po.order_no = ? AND mp.actual_value IS NULL AND mp.data_origin = 'DEMO'`, order.OrderNo).Scan(&orders)
	if orders != 4 {
		t.Fatalf("want 4 stored DEMO predictions (model + baseline, packages + EKP), got %d", orders)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM stock_balance sb JOIN lot l ON l.id = sb.lot_id JOIN warehouse w ON w.id = sb.warehouse_id
		WHERE l.lot_no IN (?, ?, ?) AND w.code = 'FASON-01' AND sb.qty > 0`, meat, spice, casing).Scan(&meatLeft)
	if meatLeft != 3 {
		t.Fatalf("material not at the subcontractor: %d of 3 lots", meatLeft)
	}
	code, body = call(t, NewProductionController(db).ListOrders, "GET", "/production?order_no="+order.OrderNo, nil)
	var listed []ProductionOrderDto
	_ = json.Unmarshal(body, &listed)
	if code != 200 || len(listed) != 1 || listed[0].Status != "MATERIAL_SENT" || listed[0].Forecast == nil {
		t.Fatalf("open order not listed with its forecast: %d %s", code, body)
	}

	// Received: 84 packs. The forecast gets the actual; 84 is inside the 98 % band, so no anomaly.
	code, body = call(t, NewProductionController(db).CalculateOrCompleteProduction, "POST", "/production", CompleteProductionRequest{
		OrderNo: order.OrderNo, ActualQty: 84, OutputLotNo: "SUC-IT-" + run, LotDate: today,
		ExpiryDate: time.Now().AddDate(0, 3, 0).Format("2006-01-02"), InvoiceNo: "FAS-FTR-IT-" + run,
	})
	var done ProductionOrderDto
	_ = json.Unmarshal(body, &done)
	if code != 200 || done.Forecast == nil || done.Forecast.ActualPackages == nil || *done.Forecast.ActualPackages != 84 ||
		done.Forecast.ActualEKP == nil || !near(*done.Forecast.ActualEKP, done.EKP) ||
		done.Forecast.Anomaly == nil || *done.Forecast.Anomaly {
		t.Fatalf("received order: %d %s", code, body)
	}
	code, body = call(t, NewProductionController(db).CalculateOrCompleteProduction, "POST", "/production", CompleteProductionRequest{
		OrderNo: order.OrderNo, ActualQty: 84, OutputLotNo: "SUC-IT2-" + run,
	})
	if code != http.StatusConflict {
		t.Fatalf("receiving twice: want 409 ORDER_STATE, got %d %s", code, body)
	}

	code, body = call(t, NewAIController(db).Summary, "GET", "/ai/summary", nil)
	var sum AISummaryDto
	_ = json.Unmarshal(body, &sum)
	found, seedLabelled := false, false
	for _, c := range sum.Comparisons {
		if c.RefNo == order.OrderNo && c.Target == "actual_qty" {
			found = c.Actual != nil && *c.Actual == 84 && c.ModelPred != nil && c.BaselinePred != nil && c.Anomaly != nil && c.Origin == "DEMO"
		}
		if c.RefNo == "URT-2026-045" && c.Target == "actual_qty" {
			seedLabelled = c.Origin == "SEED"
		}
	}
	if code != 200 || !found || !seedLabelled {
		t.Fatalf("/ai/summary misses the live DEMO forecast of %s or the SEED label of URT-2026-045", order.OrderNo)
	}
	// the three seeded documents (URT-2026-045, URT-2026-046, PRC-2026-001); no field data in a test run
	if sum.Records["SEED"] != 3 || sum.Records["DEMO"] < 1 || sum.Records["FIELD"] != 0 {
		t.Fatalf("records by origin: %v", sum.Records)
	}
}
