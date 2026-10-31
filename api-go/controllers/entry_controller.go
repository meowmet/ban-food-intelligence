package controllers

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// EntryController records new business documents: raw-material receipts (POST /purchases)
// and butchery operations (POST /butchery/operations). Both run in one transaction and
// post their stock movements and lot links, so the ledger and genealogy stay complete.
type EntryController struct {
	db *sql.DB
}

func NewEntryController(db *sql.DB) *EntryController {
	return &EntryController{db: db}
}

// --- POST /purchases -------------------------------------------------------------------

type PurchaseLineRequest struct {
	SKU           string   `json:"sku"`
	LotNo         string   `json:"lot_no"`          // our lot number, e.g. ET-2026-003
	SupplierLotNo string   `json:"supplier_lot_no"` // the supplier's batch number
	Qty           float64  `json:"qty"`
	UnitPrice     float64  `json:"unit_price"`
	ExpiryDate    string   `json:"expiry_date"` // required when the product has a shelf life
	FatPct        *float64 `json:"fat_pct,omitempty"`
}

type PurchaseRequest struct {
	ReceiptNo      string                `json:"receipt_no"`
	SupplierCode   string                `json:"supplier_code"`
	Warehouse      string                `json:"warehouse"` // default ANA-DEPO
	ReceiptDate    string                `json:"receipt_date"`
	InvoiceNo      string                `json:"invoice_no"`
	DeliveryNoteNo string                `json:"delivery_note_no"`
	Lines          []PurchaseLineRequest `json:"lines"`
}

type PurchaseResultLineDto struct {
	LotNo      string  `json:"lot_no"`
	SKU        string  `json:"sku"`
	Qty        float64 `json:"qty"`
	UnitPrice  float64 `json:"unit_price"`
	LineTotal  float64 `json:"line_total"`
	ExpiryDate string  `json:"expiry_date,omitempty"`
}

type PurchaseResultDto struct {
	ReceiptNo    string                  `json:"receipt_no"`
	SupplierCode string                  `json:"supplier_code"`
	Warehouse    string                  `json:"warehouse"`
	ReceiptDate  string                  `json:"receipt_date"`
	Total        float64                 `json:"total"`
	Lines        []PurchaseResultLineDto `json:"lines"`
}

// fieldErrors collects every validation problem so the client can fix them in one go.
type fieldErrors map[string]string

func validDate(v string) bool {
	_, err := time.Parse("2006-01-02", v)
	return err == nil
}

// validatePurchase checks the request without touching the database.
func validatePurchase(req *PurchaseRequest) fieldErrors {
	fe := fieldErrors{}
	if strings.TrimSpace(req.ReceiptNo) == "" || len(req.ReceiptNo) > 30 {
		fe["receipt_no"] = "required, max 30 characters"
	}
	if strings.TrimSpace(req.SupplierCode) == "" {
		fe["supplier_code"] = "required"
	}
	if !validDate(req.ReceiptDate) {
		fe["receipt_date"] = "required, YYYY-MM-DD"
	}
	if len(req.Lines) == 0 || len(req.Lines) > 100 {
		fe["lines"] = "1 to 100 lines"
	}
	seen := map[string]bool{}
	for i, l := range req.Lines {
		k := fmt.Sprintf("lines[%d].", i)
		if l.SKU == "" {
			fe[k+"sku"] = "required"
		}
		if strings.TrimSpace(l.LotNo) == "" || len(l.LotNo) > 30 {
			fe[k+"lot_no"] = "required, max 30 characters"
		} else if seen[l.LotNo] {
			fe[k+"lot_no"] = "duplicate in this receipt"
		}
		seen[l.LotNo] = true
		if len(l.SupplierLotNo) > 50 {
			fe[k+"supplier_lot_no"] = "max 50 characters"
		}
		if !(l.Qty > 0) || l.Qty > 1e9 {
			fe[k+"qty"] = "must be > 0"
		}
		if !(l.UnitPrice >= 0) || l.UnitPrice > 1e7 {
			fe[k+"unit_price"] = "must be >= 0"
		}
		if l.ExpiryDate != "" {
			if !validDate(l.ExpiryDate) {
				fe[k+"expiry_date"] = "YYYY-MM-DD"
			} else if validDate(req.ReceiptDate) && l.ExpiryDate < req.ReceiptDate {
				fe[k+"expiry_date"] = "before receipt_date"
			}
		}
	}
	return fe
}

// Purchase handles POST /purchases: one supplier delivery. Every line becomes a PURCHASE lot
// with supplier, supplier batch number, date, quantity, price and (for perishable products)
// expiry date, a receipt line and a PURCHASE_IN movement. 201 with the receipt; 422 with
// per-field errors; 404 for an unknown supplier, product or warehouse; 409 for a used number.
func (c *EntryController) Purchase(w http.ResponseWriter, r *http.Request) {
	var req PurchaseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Warehouse = nonEmpty(req.Warehouse, "ANA-DEPO")
	if fe := validatePurchase(&req); len(fe) > 0 {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fe)
		return
	}
	ctx := r.Context()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer tx.Rollback()

	var supplierID int64
	var supplierType string
	if err := tx.QueryRowContext(ctx, `SELECT id, supplier_type FROM supplier WHERE code = ?`, req.SupplierCode).Scan(&supplierID, &supplierType); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", "supplier "+req.SupplierCode)
		return
	}
	if supplierType == "SUBCONTRACTOR" {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{"supplier_code": "a subcontractor does not deliver raw material"})
		return
	}
	wh, err := warehouseID(ctx, tx, req.Warehouse)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO purchase_receipt (receipt_no, supplier_id, warehouse_id, receipt_date, invoice_no, delivery_note_no)
		 VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))`,
		req.ReceiptNo, supplierID, wh, req.ReceiptDate, req.InvoiceNo, req.DeliveryNoteNo)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	receiptID, _ := res.LastInsertId()

	out := PurchaseResultDto{ReceiptNo: req.ReceiptNo, SupplierCode: req.SupplierCode, Warehouse: req.Warehouse, ReceiptDate: req.ReceiptDate}
	var totalCents int64
	for i, l := range req.Lines {
		var productID int64
		var productType, uom string
		var shelf sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT id, product_type, uom_code, shelf_life_days FROM product WHERE sku = ?`, l.SKU).
			Scan(&productID, &productType, &uom, &shelf); err != nil {
			writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", "product "+l.SKU)
			return
		}
		if productType != "RAW_MATERIAL" && productType != "CARCASS" {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{fmt.Sprintf("lines[%d].sku", i): "only raw material or carcasses are purchased"})
			return
		}
		if shelf.Valid && l.ExpiryDate == "" {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{fmt.Sprintf("lines[%d].expiry_date", i): "required: the product has a shelf life"})
			return
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO lot (lot_no, product_id, origin_type, supplier_id, supplier_lot_no, lot_date, expiry_date, initial_qty, uom_code, unit_cost, fat_pct)
			 VALUES (?, ?, 'PURCHASE', ?, NULLIF(?, ''), ?, NULLIF(?, ''), ?, ?, ?, ?)`,
			l.LotNo, productID, supplierID, l.SupplierLotNo, req.ReceiptDate, l.ExpiryDate, l.Qty, uom, l.UnitPrice, l.FatPct)
		if err != nil {
			writeDBError(w, r, err)
			return
		}
		lot, _ := res.LastInsertId()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO purchase_receipt_line (receipt_id, product_id, lot_id, qty, unit_price) VALUES (?, ?, ?, ?, ?)`,
			receiptID, productID, lot, l.Qty, l.UnitPrice); err != nil {
			writeDBError(w, r, err)
			return
		}
		if err := insertMovement(ctx, tx, lot, wh, "PURCHASE_IN", l.Qty, l.UnitPrice, "purchase_receipt", &receiptID, req.ReceiptNo); err != nil {
			writeDBError(w, r, err)
			return
		}
		cents := int64(math.Round(l.Qty * l.UnitPrice * 100))
		totalCents += cents
		out.Lines = append(out.Lines, PurchaseResultLineDto{LotNo: l.LotNo, SKU: l.SKU, Qty: l.Qty, UnitPrice: l.UnitPrice,
			LineTotal: float64(cents) / 100, ExpiryDate: l.ExpiryDate})
	}
	logAlertError("purchase "+req.ReceiptNo, raisePurchaseCostAlerts(ctx, tx, receiptID))
	if err := tx.Commit(); err != nil {
		writeDBError(w, r, err)
		return
	}
	out.Total = float64(totalCents) / 100
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, out)
}

// --- POST /butchery/operations ---------------------------------------------------------

type ButcheryOutputRequest struct {
	SKU      string  `json:"sku"`
	Kind     string  `json:"output_kind"` // CUT | BY_PRODUCT | WASTE
	WeightKg float64 `json:"weight_kg"`
	LotNo    string  `json:"lot_no"` // required for CUT and BY_PRODUCT; waste gets no lot
}

type ButcheryOperationRequest struct {
	OpNo               string                  `json:"op_no"`
	RecipeCode         string                  `json:"recipe_code"` // default REC-KUZU-STD
	InputLotNo         string                  `json:"input_lot_no"`
	InputWeightKg      float64                 `json:"input_weight_kg"`
	PurchaseUnitPrice  float64                 `json:"purchase_unit_price"` // default: the input lot's unit cost
	AdditionalCost     float64                 `json:"additional_cost"`
	AdditionalCostNote string                  `json:"additional_cost_note"`
	AllocationMethod   string                  `json:"allocation_method"` // QUANTITY | VALUE
	OpDate             string                  `json:"op_date"`
	OperatorName       string                  `json:"operator_name"`
	Warehouse          string                  `json:"warehouse"` // default ANA-DEPO
	DryRun             bool                    `json:"dry_run"`   // validate + allocate, write nothing
	Outputs            []ButcheryOutputRequest `json:"outputs"`
}

type ButcheryAllocationLineDto struct {
	SKU        string  `json:"sku"`
	Kind       string  `json:"output_kind"`
	LotNo      string  `json:"lot_no,omitempty"`
	WeightKg   float64 `json:"weight_kg"`
	SalePrice  float64 `json:"sale_price"`
	Basis      float64 `json:"basis"`
	Cost       float64 `json:"allocated_cost"`
	UnitCostKg float64 `json:"unit_cost_per_kg"`
}

type ButcheryKeyResultDto struct {
	Method    string                      `json:"method"`
	Allocated float64                     `json:"allocated_total"`
	SumsExact bool                        `json:"sums_exact"`
	Lines     []ButcheryAllocationLineDto `json:"lines"`
}

type ButcheryOperationResultDto struct {
	OpNo             string                          `json:"op_no"`
	Status           string                          `json:"status"` // POSTED | DRY_RUN
	InputLotNo       string                          `json:"input_lot_no"`
	TotalCost        float64                         `json:"total_cost"`
	AllocationMethod string                          `json:"allocation_method"`
	WeightCheck      WeightCheckDto                  `json:"weight_check"`
	Allocation       ButcheryKeyResultDto            `json:"allocation"`
	Comparison       map[string]ButcheryKeyResultDto `json:"allocation_by_key"`         // both keys, for the decision
	Forecast         *ButcheryForecastDto            `json:"forecast,omitempty"`        // AI 4B: expected vs recorded part weights
	FrozenForecast   *FrozenButcheryDto              `json:"frozen_forecast,omitempty"` // frozen before weighing, with the recorded weights
}

// allocateButchery spreads totalCents over the outputs with the chosen key. Waste carries no
// cost; cuts and by-products share it by kg (QUANTITY) or by kg x sale price (VALUE).
func allocateButchery(method string, totalCost float64, outs []ButcheryOutputRequest, prices []float64, lots []string) ButcheryKeyResultDto {
	bases := make([]float64, len(outs))
	for i, o := range outs {
		if o.Kind == "WASTE" {
			continue
		}
		if method == "QUANTITY" {
			bases[i] = o.WeightKg
		} else {
			bases[i] = o.WeightKg * prices[i]
		}
	}
	alloc := AllocateCost(totalCost, bases)
	res := ButcheryKeyResultDto{Method: method}
	var cents int64
	for i, o := range outs {
		line := ButcheryAllocationLineDto{SKU: o.SKU, Kind: o.Kind, LotNo: lots[i], WeightKg: o.WeightKg, SalePrice: prices[i],
			Basis: math.Round(bases[i]*10000) / 10000, Cost: alloc[i]}
		if o.WeightKg > 0 && alloc[i] > 0 {
			line.UnitCostKg = math.Round(alloc[i]/o.WeightKg*10000) / 10000
		}
		cents += int64(math.Round(alloc[i] * 100))
		res.Lines = append(res.Lines, line)
	}
	res.Allocated = float64(cents) / 100
	res.SumsExact = cents == int64(math.Round(totalCost*100))
	return res
}

func validateButchery(req *ButcheryOperationRequest) fieldErrors {
	fe := fieldErrors{}
	if strings.TrimSpace(req.OpNo) == "" || len(req.OpNo) > 30 {
		fe["op_no"] = "required, max 30 characters"
	}
	if req.InputLotNo == "" {
		fe["input_lot_no"] = "required"
	}
	if !(req.InputWeightKg > 0) || req.InputWeightKg > 10000 {
		fe["input_weight_kg"] = "must be > 0"
	}
	if req.PurchaseUnitPrice < 0 || req.AdditionalCost < 0 {
		fe["purchase_unit_price"] = "prices must be >= 0"
	}
	if req.AdditionalCost > 0 && strings.TrimSpace(req.AdditionalCostNote) == "" {
		fe["additional_cost_note"] = "an additional cost needs a document reference"
	}
	if req.AllocationMethod != "QUANTITY" && req.AllocationMethod != "VALUE" {
		fe["allocation_method"] = "QUANTITY or VALUE"
	}
	if !validDate(req.OpDate) {
		fe["op_date"] = "required, YYYY-MM-DD"
	}
	if len(req.Outputs) == 0 || len(req.Outputs) > 50 {
		fe["outputs"] = "1 to 50 outputs"
	}
	seen := map[string]bool{}
	for i, o := range req.Outputs {
		k := fmt.Sprintf("outputs[%d].", i)
		if o.Kind != "CUT" && o.Kind != "BY_PRODUCT" && o.Kind != "WASTE" {
			fe[k+"output_kind"] = "CUT, BY_PRODUCT or WASTE"
		}
		if o.SKU == "" {
			fe[k+"sku"] = "required"
		}
		if !(o.WeightKg >= 0) {
			fe[k+"weight_kg"] = "must be >= 0"
		}
		if o.Kind != "WASTE" {
			if o.LotNo == "" || len(o.LotNo) > 30 {
				fe[k+"lot_no"] = "required for cuts and by-products"
			} else if seen[o.LotNo] {
				fe[k+"lot_no"] = "duplicate"
			}
			seen[o.LotNo] = true
		}
	}
	return fe
}

// ButcheryOperation handles POST /butchery/operations: records a carcass breakdown with
// actual weights. The weight balance (input = cuts + by-products + waste, 10 g tolerance)
// must hold, otherwise 422 WEIGHT_IMBALANCE and nothing is written. The total cost
// (input kg x price + documented extra cost) is allocated with the chosen key; the response
// also shows the other key so the two can be compared. On success: operation POSTED, one
// lot per cut/by-product with its allocated unit cost, BUTCHERY_CONSUME / BUTCHERY_OUTPUT
// movements and lot_link rows from the carcass lot to every part lot. A forecast frozen for the
// carcass is linked to the operation and returned with the recorded weights (frozen_forecast).
func (c *EntryController) ButcheryOperation(w http.ResponseWriter, r *http.Request) {
	var req ButcheryOperationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.AllocationMethod = strings.ToUpper(req.AllocationMethod)
	req.RecipeCode = nonEmpty(req.RecipeCode, "REC-KUZU-STD")
	req.Warehouse = nonEmpty(req.Warehouse, "ANA-DEPO")
	if fe := validateButchery(&req); len(fe) > 0 {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fe)
		return
	}
	ctx := r.Context()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer tx.Rollback()

	var inLot, recipeID int64
	var inCost float64
	var inType, inStatus string
	var inExpiry sql.NullTime
	var inDate time.Time
	if err := tx.QueryRowContext(ctx,
		`SELECT l.id, l.unit_cost, p.product_type, l.status, l.expiry_date, l.lot_date FROM lot l JOIN product p ON p.id = l.product_id WHERE l.lot_no = ? FOR UPDATE`,
		req.InputLotNo).Scan(&inLot, &inCost, &inType, &inStatus, &inExpiry, &inDate); err != nil {
		if err == sql.ErrNoRows {
			writeAPIError(w, r, http.StatusNotFound, "LOT_NOT_FOUND", "lot "+req.InputLotNo)
		} else {
			writeDBError(w, r, err)
		}
		return
	}
	if inType != "CARCASS" {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{"input_lot_no": "must be a carcass lot"})
		return
	}
	if inStatus == "RECALLED" || inStatus == "BLOCKED" {
		writeAPIError(w, r, http.StatusConflict, "LOT_BLOCKED", nil)
		return
	}
	if inDate.Format("2006-01-02") > req.OpDate {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{"op_date": "before carcass receipt"})
		return
	}
	if inExpiry.Valid && inExpiry.Time.Format("2006-01-02") < req.OpDate {
		writeAPIError(w, r, http.StatusConflict, "LOT_EXPIRED", nil)
		return
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM butchery_recipe WHERE code = ?`, req.RecipeCode).Scan(&recipeID); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", "recipe "+req.RecipeCode)
		return
	}
	wh, err := warehouseID(ctx, tx, req.Warehouse)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	if req.PurchaseUnitPrice == 0 {
		req.PurchaseUnitPrice = inCost
	}

	prices := make([]float64, len(req.Outputs))
	products := make([]int64, len(req.Outputs))
	shelf := make([]sql.NullInt64, len(req.Outputs))
	lots := make([]string, len(req.Outputs))
	wc := WeightCheckDto{InputKg: req.InputWeightKg, ToleranceKg: WeightToleranceKg}
	for i, o := range req.Outputs {
		var ptype string
		var price sql.NullFloat64
		if err := tx.QueryRowContext(ctx, `SELECT id, product_type, sale_price, shelf_life_days FROM product WHERE sku = ?`, o.SKU).
			Scan(&products[i], &ptype, &price, &shelf[i]); err != nil {
			writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", "product "+o.SKU)
			return
		}
		want := map[string]string{"CUT": "CUT", "BY_PRODUCT": "BY_PRODUCT", "WASTE": "WASTE"}[o.Kind]
		if ptype != want {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{fmt.Sprintf("outputs[%d].sku", i): o.SKU + " is " + ptype + ", not " + o.Kind})
			return
		}
		if o.Kind != "WASTE" {
			lots[i] = o.LotNo
			if !price.Valid && req.AllocationMethod == "VALUE" {
				writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{fmt.Sprintf("outputs[%d].sku", i): o.SKU + " has no sale price for the VALUE key"})
				return
			}
		}
		prices[i] = price.Float64
		switch o.Kind {
		case "WASTE":
			wc.WasteKg += o.WeightKg
		case "BY_PRODUCT":
			wc.ByProductKg += o.WeightKg
		default:
			wc.CutsKg += o.WeightKg
		}
	}
	wc.CutsKg, wc.ByProductKg, wc.WasteKg = round3(wc.CutsKg), round3(wc.ByProductKg), round3(wc.WasteKg)
	wc.UnrecordedKg = round3(wc.InputKg - wc.CutsKg - wc.ByProductKg - wc.WasteKg)
	switch {
	case wc.UnrecordedKg > WeightToleranceKg:
		wc.Status = "UNRECORDED_LOSS"
	case wc.UnrecordedKg < -WeightToleranceKg:
		wc.Status = "OVER_RECORDED"
	default:
		wc.Status = "BALANCED"
	}
	if wc.Status != "BALANCED" {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "WEIGHT_IMBALANCE", wc)
		return
	}

	totalCost := round2(round2(req.InputWeightKg*req.PurchaseUnitPrice) + req.AdditionalCost)
	result := ButcheryOperationResultDto{
		OpNo: req.OpNo, Status: "POSTED", InputLotNo: req.InputLotNo, TotalCost: totalCost,
		AllocationMethod: req.AllocationMethod, WeightCheck: wc,
		Comparison: map[string]ButcheryKeyResultDto{
			"QUANTITY": allocateButchery("QUANTITY", totalCost, req.Outputs, prices, lots),
			"VALUE":    allocateButchery("VALUE", totalCost, req.Outputs, prices, lots),
		},
	}
	result.Allocation = result.Comparison[req.AllocationMethod]
	if in, ptype, err := loadCarcass(ctx, tx, req.InputLotNo); err == nil && ptype == "CARCASS" {
		in.CarcassWeightKg, in.OperatorCode = req.InputWeightKg, req.OperatorName
		in.OpDate, _ = time.Parse("2006-01-02", req.OpDate)
		result.Forecast = forecastButchery(in, butcheryForecastModel)
		applyButcheryActuals(result.Forecast, req.Outputs)
		frozen, err := loadFrozenButchery(ctx, tx, req.InputLotNo, in.Species)
		if err != nil {
			writeDBError(w, r, err)
			return
		}
		if frozen != nil && frozen.OpNo == "" {
			applyButcheryActuals(frozen.Forecast, req.Outputs)
			same := math.Abs(frozen.InputWeightKg-req.InputWeightKg) < 0.0005
			frozen.InputMatches = &same
			result.FrozenForecast = frozen
		}
	}
	if req.DryRun {
		result.Status = "DRY_RUN"
		writeJSON(w, result)
		return
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO butchery_operation (op_no, recipe_id, input_lot_id, species, input_weight_kg, purchase_unit_price,
		        additional_cost, additional_cost_note, allocation_method, op_date, operator_name, status, reconciliation_note)
		 SELECT ?, ?, ?, species, ?, ?, ?, NULLIF(?, ''), ?, ?, NULLIF(?, ''), 'POSTED', ?
		   FROM butchery_recipe WHERE id = ?`,
		req.OpNo, recipeID, inLot, req.InputWeightKg, req.PurchaseUnitPrice, req.AdditionalCost, req.AdditionalCostNote,
		req.AllocationMethod, req.OpDate, req.OperatorName, "recorded via API by "+nonEmpty(OperatorFrom(ctx), "api"), recipeID)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	opID, _ := res.LastInsertId()
	if err := insertMovement(ctx, tx, inLot, wh, "BUTCHERY_CONSUME", -req.InputWeightKg, req.PurchaseUnitPrice, "butchery_operation", &opID, req.OpNo); err != nil {
		writeDBError(w, r, err)
		return
	}
	for i, o := range req.Outputs {
		line := result.Allocation.Lines[i]
		var lotID sql.NullInt64
		if o.Kind != "WASTE" && o.WeightKg > 0 {
			unitCost := 0.0
			if o.WeightKg > 0 {
				unitCost = math.Round(line.Cost/o.WeightKg*10000) / 10000
			}
			res, err := tx.ExecContext(ctx,
				`INSERT INTO lot (lot_no, product_id, origin_type, lot_date, expiry_date, initial_qty, uom_code, unit_cost)
				 SELECT ?, id, 'BUTCHERY', ?, IF(shelf_life_days IS NULL, NULL, DATE_ADD(?, INTERVAL shelf_life_days DAY)), ?, uom_code, ?
				   FROM product WHERE id = ?`,
				o.LotNo, req.OpDate, req.OpDate, o.WeightKg, unitCost, products[i])
			if err != nil {
				writeDBError(w, r, err)
				return
			}
			id, _ := res.LastInsertId()
			lotID = sql.NullInt64{Int64: id, Valid: true}
			if err := insertMovement(ctx, tx, id, wh, "BUTCHERY_OUTPUT", o.WeightKg, unitCost, "butchery_operation", &opID, req.OpNo); err != nil {
				writeDBError(w, r, err)
				return
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO lot_link (parent_lot_id, child_lot_id, link_type, qty_consumed, qty_produced, ref_type, ref_id)
				 VALUES (?, ?, 'BUTCHERY', ?, ?, 'butchery_operation', ?)`, inLot, id, o.WeightKg, o.WeightKg, opID); err != nil {
				writeDBError(w, r, err)
				return
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO butchery_output (operation_id, product_id, lot_id, output_kind, weight_kg, allocation_basis, allocated_cost)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`, opID, products[i], lotID, o.Kind, o.WeightKg, line.Basis, line.Cost); err != nil {
			writeDBError(w, r, err)
			return
		}
	}
	if f := result.FrozenForecast; f != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE butchery_forecast SET operation_id = ? WHERE id = ?`, opID, f.id); err != nil {
			writeDBError(w, r, err)
			return
		}
		f.OpNo = req.OpNo
	}
	if result.Forecast != nil {
		if err := storeButcheryForecast(ctx, tx, opID, result.Forecast); err != nil {
			writeDBError(w, r, err)
			return
		}
		if a := butcheryAlert(opID, result.Forecast, req.PurchaseUnitPrice, loadThresholds(ctx, tx)["BUTCHERY_YIELD"]); a != nil {
			logAlertError("butchery "+req.OpNo, insertAlert(ctx, tx, *a))
		}
	}
	if err := tx.Commit(); err != nil {
		writeDBError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, result)
}
