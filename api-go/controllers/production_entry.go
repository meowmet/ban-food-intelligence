package controllers

import (
	"fmt"
	"net/http"
	"strings"
)

// External production order entry (spec Task 1): the order, the raw-material lots sent to the
// subcontractor (each a TRANSFER_OUT/IN pair with its cost snapshot, via sp_production_send_input)
// and the AI forecast of packages and EKP, in one transaction. Receiving the finished goods
// stays POST /production. With dry_run the same checks and the forecast run inside the
// transaction, which is then rolled back: a preview at decision time that writes nothing.

type ProductionInputRequest struct {
	LotNo string  `json:"lot_no"`
	Qty   float64 `json:"qty"`
}

type ProductionOrderRequest struct {
	OrderNo           string                   `json:"order_no"`
	ProductSKU        string                   `json:"product_sku"`
	SubcontractorCode string                   `json:"subcontractor_code"`
	PlannedQty        float64                  `json:"planned_qty"`
	ServiceUnitPrice  float64                  `json:"service_unit_price"`
	DryingDays        *int                     `json:"drying_days"`
	OrderDate         string                   `json:"order_date"`
	FromWarehouse     string                   `json:"from_warehouse"` // default ANA-DEPO
	SubWarehouse      string                   `json:"sub_warehouse"`  // default FASON-01
	Notes             string                   `json:"notes"`
	DryRun            bool                     `json:"dry_run"` // check + forecast, write nothing
	Inputs            []ProductionInputRequest `json:"inputs"`
}

type ProductionOrderResultDto struct {
	OrderNo      string                   `json:"order_no"`
	Status       string                   `json:"status"` // MATERIAL_SENT | DRY_RUN
	MaterialCost float64                  `json:"material_cost"`
	Inputs       []ProductionInputRequest `json:"inputs"`
	Forecast     *ProductionForecastDto   `json:"forecast"`
}

func validateProductionOrder(req *ProductionOrderRequest) fieldErrors {
	fe := fieldErrors{}
	if strings.TrimSpace(req.OrderNo) == "" || len(req.OrderNo) > 30 {
		fe["order_no"] = "required, at most 30 characters"
	}
	if req.ProductSKU == "" {
		fe["product_sku"] = "required"
	}
	if req.SubcontractorCode == "" {
		fe["subcontractor_code"] = "required"
	}
	if req.PlannedQty <= 0 {
		fe["planned_qty"] = "must be > 0"
	}
	if req.ServiceUnitPrice < 0 {
		fe["service_unit_price"] = "must be >= 0"
	}
	if req.DryingDays != nil && (*req.DryingDays < 0 || *req.DryingDays > 60) {
		fe["drying_days"] = "0 to 60 days"
	}
	if !validDate(req.OrderDate) {
		fe["order_date"] = "YYYY-MM-DD"
	}
	if len(req.Inputs) == 0 {
		fe["inputs"] = "at least one raw-material lot"
	}
	seen := map[string]bool{}
	for i, in := range req.Inputs {
		switch {
		case strings.TrimSpace(in.LotNo) == "":
			fe[fmt.Sprintf("inputs[%d].lot_no", i)] = "required"
		case seen[in.LotNo]:
			fe[fmt.Sprintf("inputs[%d].lot_no", i)] = "lot listed twice"
		}
		seen[in.LotNo] = true
		if in.Qty <= 0 {
			fe[fmt.Sprintf("inputs[%d].qty", i)] = "must be > 0"
		}
	}
	return fe
}

// ProductionOrder handles POST /production/orders.
func (c *EntryController) ProductionOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return
	}
	var req ProductionOrderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.FromWarehouse = nonEmpty(req.FromWarehouse, "ANA-DEPO")
	req.SubWarehouse = nonEmpty(req.SubWarehouse, "FASON-01")
	if fe := validateProductionOrder(&req); len(fe) > 0 {
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

	var productID, subID int64
	var productType, supplierType string
	if err := tx.QueryRowContext(ctx, `SELECT id, product_type FROM product WHERE sku = ?`, req.ProductSKU).Scan(&productID, &productType); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", "product "+req.ProductSKU)
		return
	}
	if productType != "FINISHED_GOOD" {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{"product_sku": "an external production order makes a finished good"})
		return
	}
	if err := tx.QueryRowContext(ctx, `SELECT id, supplier_type FROM supplier WHERE code = ?`, req.SubcontractorCode).Scan(&subID, &supplierType); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", "subcontractor "+req.SubcontractorCode)
		return
	}
	if supplierType != "SUBCONTRACTOR" {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{"subcontractor_code": "not a subcontractor"})
		return
	}
	for _, wh := range []string{req.FromWarehouse, req.SubWarehouse} {
		if _, err := warehouseID(ctx, tx, wh); err != nil {
			writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", err.Error())
			return
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO production_order (order_no, order_type, subcontractor_id, product_id, planned_qty,
		                               service_unit_price, drying_days, order_date, notes)
		 VALUES (?, 'EXTERNAL', ?, ?, ?, ?, ?, ?, NULLIF(?, ''))`,
		req.OrderNo, subID, productID, req.PlannedQty, req.ServiceUnitPrice, req.DryingDays, req.OrderDate, req.Notes); err != nil {
		writeDBError(w, r, err)
		return
	}
	operator := nonEmpty(OperatorFrom(ctx), "api")
	for _, in := range req.Inputs {
		if _, err := tx.ExecContext(ctx, `CALL sp_production_send_input(?, ?, ?, ?, ?, ?)`,
			req.OrderNo, in.LotNo, in.Qty, req.FromWarehouse, req.SubWarehouse, operator); err != nil {
			writeDBError(w, r, fmt.Errorf("input lot %s: %w", in.LotNo, err))
			return
		}
	}

	o, err := loadOrderInputs(ctx, tx, req.OrderNo)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	fc := forecastOrder(o, productionModel)
	result := ProductionOrderResultDto{
		OrderNo: req.OrderNo, Status: "MATERIAL_SENT", MaterialCost: round(o.materialCost, 2), Inputs: req.Inputs, Forecast: fc,
	}
	if req.DryRun {
		// the deferred rollback undoes the order and the dispatch; the forecast is not stored
		result.Status = "DRY_RUN"
		writeJSON(w, result)
		return
	}
	if fc != nil {
		if err := storeForecast(ctx, tx, o.orderID, fc); err != nil {
			writeDBError(w, r, err)
			return
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
