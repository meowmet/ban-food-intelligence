package controllers

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

type CostBreakdownLineDto struct {
	OrderNo     string  `json:"order_no"`
	LotNo       string  `json:"lot_no"`               // the material lot, or the service invoice number
	IsService   bool    `json:"is_service,omitempty"` // the subcontractor's service line
	CostElement string  `json:"cost_element"`
	Qty         float64 `json:"qty"`
	UomCode     string  `json:"uom_code"`
	UnitCost    float64 `json:"unit_cost"`
	Amount      float64 `json:"amount"`
}

type ProductionOrderDto struct {
	OrderNo            string                 `json:"order_no"`
	ProductSKU         string                 `json:"product_sku"`
	ProductName        string                 `json:"product_name"`
	Status             string                 `json:"status"`
	PlannedQty         float64                `json:"planned_qty"`
	ActualQty          float64                `json:"actual_qty"`
	QtyVariance        float64                `json:"qty_variance"`
	FulfilmentPct      float64                `json:"fulfilment_pct"`
	InputKg            float64                `json:"input_kg"`
	YieldPctOfMeat     float64                `json:"yield_pct_of_meat"`
	MaterialCost       float64                `json:"material_cost"`
	ServiceCost        float64                `json:"service_cost"`
	TotalCost          float64                `json:"total_cost"`
	EKP                float64                `json:"ekp"`
	EKPDisplay         float64                `json:"ekp_display"`
	VKP                float64                `json:"vkp"`
	GrossProfitPerUnit float64                `json:"gross_profit_per_unit"`
	RevenueAtVKP       float64                `json:"revenue_at_vkp"`
	GrossProfitTotal   float64                `json:"gross_profit_total"`
	MarginPct          float64                `json:"margin_pct"`
	OutputLotNumber    string                 `json:"output_lot_number,omitempty"`
	Breakdown          []CostBreakdownLineDto `json:"breakdown,omitempty"`
	Forecast           *ProductionForecastDto `json:"forecast,omitempty"` // AI 4A: packages and EKP, with actuals once received
}

type CompleteProductionRequest struct {
	OrderNo      string  `json:"order_no"`      // e.g. "URT-2026-045"
	ActualQty    float64 `json:"actual_qty"`    // e.g. 85
	OutputLotNo  string  `json:"output_lot_no"` // e.g. "SUC-2026-045"
	LotDate      string  `json:"lot_date"`      // e.g. "2026-10-15"
	ExpiryDate   string  `json:"expiry_date"`   // e.g. "2026-12-15"
	SubWarehouse string  `json:"sub_warehouse"` // e.g. "WH-FASON"
	TargetWh     string  `json:"target_wh"`     // e.g. "WH-MAMUL"
	InvoiceNo    string  `json:"invoice_no"`    // e.g. "FTR-FASON-2026-045"
}

type ProductionController struct {
	db *sql.DB
}

func NewProductionController(db *sql.DB) *ProductionController {
	return &ProductionController{db: db}
}

// ListOrders handles GET /production and POST /production
// Queries Ali's MySQL `sp_calculate_production_cost` / `v_production_cost` & `v_production_cost_breakdown`.
func (c *ProductionController) ListOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		c.CalculateOrCompleteProduction(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return
	}

	orderFilter := r.URL.Query().Get("order_no")

	query := `
		SELECT vc.order_no, vc.sku, vc.product_name, vc.status,
		       vc.planned_qty, vc.actual_qty, vc.qty_variance, vc.fulfilment_pct,
		       vc.input_kg, COALESCE(vc.yield_pct_of_meat, 0),
		       vc.material_cost, vc.service_cost, vc.total_cost,
		       vc.ekp, vc.ekp_display, COALESCE(vc.vkp, 0),
		       COALESCE(vc.gross_profit_per_unit, 0), COALESCE(vc.revenue_at_vkp, 0),
		       COALESCE(vc.gross_profit_total, 0), COALESCE(vc.margin_pct, 0),
		       COALESCE((SELECT l.lot_no FROM production_output pout
		                 JOIN production_order po ON po.id = pout.production_order_id
		                 JOIN lot l ON l.id = pout.lot_id
		                 WHERE po.order_no = vc.order_no LIMIT 1), '') AS output_lot_no
		FROM v_production_cost vc
	`
	var args []interface{}
	if orderFilter != "" {
		query += " WHERE vc.order_no = ?"
		args = append(args, orderFilter)
	}
	query += " ORDER BY vc.order_no ASC"

	rows, err := c.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		log.Printf("[ERROR] [MYSQL-PRODUCTION] Query error: %v", err)
		writeDBError(w, r, err)
		return
	}
	defer rows.Close()

	orders := []ProductionOrderDto{} // [] rather than null when nothing matches
	for rows.Next() {
		var o ProductionOrderDto
		if err := rows.Scan(
			&o.OrderNo, &o.ProductSKU, &o.ProductName, &o.Status,
			&o.PlannedQty, &o.ActualQty, &o.QtyVariance, &o.FulfilmentPct,
			&o.InputKg, &o.YieldPctOfMeat,
			&o.MaterialCost, &o.ServiceCost, &o.TotalCost,
			&o.EKP, &o.EKPDisplay, &o.VKP,
			&o.GrossProfitPerUnit, &o.RevenueAtVKP,
			&o.GrossProfitTotal, &o.MarginPct,
			&o.OutputLotNumber,
		); err != nil {
			writeDBError(w, r, err)
			return
		}
		o.Breakdown = c.fetchCostBreakdown(r, o.OrderNo)
		orders = append(orders, o)
	}
	rows.Close()

	// Orders whose material is at the subcontractor: no EKP yet, but the material cost and the forecast.
	open := `SELECT po.order_no, p.sku, p.name, po.status, po.planned_qty,
	                COALESCE(SUM(pi.qty_consumed), 0), COALESCE(SUM(pi.line_cost), 0)
	         FROM production_order po JOIN product p ON p.id = po.product_id
	         LEFT JOIN production_input pi ON pi.production_order_id = po.id
	         WHERE po.status IN ('PLANNED','MATERIAL_SENT')`
	if orderFilter != "" {
		open += " AND po.order_no = ?"
	}
	open += " GROUP BY po.id, po.order_no, p.sku, p.name, po.status, po.planned_qty ORDER BY po.order_no"
	openRows, err := c.db.QueryContext(r.Context(), open, args...)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer openRows.Close()
	for openRows.Next() {
		var o ProductionOrderDto
		if err := openRows.Scan(&o.OrderNo, &o.ProductSKU, &o.ProductName, &o.Status, &o.PlannedQty, &o.InputKg, &o.MaterialCost); err != nil {
			writeDBError(w, r, err)
			return
		}
		o.MaterialCost = round(o.MaterialCost, 2)
		orders = append(orders, o)
	}

	forecasts := loadForecasts(r.Context(), c.db)
	for i := range orders {
		orders[i].Forecast = forecasts[orders[i].OrderNo]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(orders)
}

// CalculateOrCompleteProduction handles POST /production
// Invokes Ali's MySQL stored procedure `CALL sp_calculate_production_cost(...)`
// (which wraps `sp_production_complete` when completing an order and returns the EKP breakdown).
func (c *ProductionController) CalculateOrCompleteProduction(w http.ResponseWriter, r *http.Request) {
	var req CompleteProductionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "invalid JSON request body")
		return
	}
	fe := fieldErrors{}
	if strings.TrimSpace(req.OrderNo) == "" {
		fe["order_no"] = "required"
	}
	if req.ActualQty != 0 || req.OutputLotNo != "" || req.LotDate != "" || req.ExpiryDate != "" {
		if !(req.ActualQty > 0) {
			fe["actual_qty"] = "must be > 0"
		}
		if strings.TrimSpace(req.OutputLotNo) == "" || len(req.OutputLotNo) > 30 {
			fe["output_lot_no"] = "required, max 30 characters"
		}
		if req.LotDate == "" {
			req.LotDate = time.Now().UTC().Format("2006-01-02")
		}
		if !validDate(req.LotDate) {
			fe["lot_date"] = "YYYY-MM-DD"
		}
		if req.ExpiryDate == "" && validDate(req.LotDate) {
			d, _ := time.Parse("2006-01-02", req.LotDate)
			req.ExpiryDate = d.AddDate(0, 6, 0).Format("2006-01-02")
		}
		if !validDate(req.ExpiryDate) {
			fe["expiry_date"] = "YYYY-MM-DD"
		} else if validDate(req.LotDate) && req.ExpiryDate < req.LotDate {
			fe["expiry_date"] = "before lot_date"
		}
	}
	if len(fe) > 0 {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fe)
		return
	}

	// If an unreceived order is being completed, invoke sp_production_complete first
	if req.ActualQty > 0 && req.OutputLotNo != "" {
		// Warehouse codes from seed.sql: subcontractor stock and main cold store.
		if req.SubWarehouse == "" {
			req.SubWarehouse = "FASON-01"
		}
		if req.TargetWh == "" {
			req.TargetWh = "ANA-DEPO"
		}
		_, err := c.db.ExecContext(
			r.Context(),
			`CALL sp_production_complete(?, ?, ?, ?, ?, ?, ?, ?)`,
			req.OrderNo, req.ActualQty, req.OutputLotNo, req.LotDate, req.ExpiryDate,
			req.SubWarehouse, req.TargetWh, req.InvoiceNo,
		)
		if err != nil {
			// e.g. ORDER_STATE (already received), INSUFFICIENT_STOCK, duplicate lot number
			log.Printf("[WARN] [MYSQL-SP] sp_production_complete failed for %s: %v", req.OrderNo, err)
			writeDBError(w, r, err)
			return
		}
		// AI 4A: the forecast made when the material was sent now gets its actual and anomaly flag.
		if err := recordForecastActuals(r.Context(), c.db, req.OrderNo); err != nil {
			log.Printf("[WARN] [AI] actuals for %s not recorded: %v", req.OrderNo, err)
		}
	}

	// Execute stored procedure `CALL sp_calculate_production_cost(?)`
	rows, err := c.db.QueryContext(r.Context(), `CALL sp_calculate_production_cost(?)`, req.OrderNo)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer rows.Close()

	var result ProductionOrderDto
	if rows.Next() {
		if err := rows.Scan(
			&result.OrderNo, &result.ProductSKU, &result.ProductName, &result.Status,
			&result.PlannedQty, &result.ActualQty, &result.QtyVariance, &result.FulfilmentPct,
			&result.InputKg, &result.YieldPctOfMeat,
			&result.MaterialCost, &result.ServiceCost, &result.TotalCost,
			&result.EKP, &result.EKPDisplay, &result.VKP,
			&result.GrossProfitPerUnit, &result.RevenueAtVKP,
			&result.GrossProfitTotal, &result.MarginPct,
		); err != nil {
			writeDBError(w, r, err)
			return
		}
	}
	rows.Close()

	result.Breakdown = c.fetchCostBreakdown(r, req.OrderNo)
	result.Forecast = loadForecasts(r.Context(), c.db)[req.OrderNo]

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (c *ProductionController) fetchCostBreakdown(r *http.Request, orderNo string) []CostBreakdownLineDto {
	bRows, err := c.db.QueryContext(
		r.Context(),
		`SELECT order_no, COALESCE(lot_no, ''), cost_element, qty, uom_code, unit_cost, amount, sort_key = 2
		 FROM v_production_cost_breakdown
		 WHERE order_no = ?
		 ORDER BY sort_key ASC, lot_no ASC`,
		orderNo,
	)
	if err != nil {
		return nil
	}
	defer bRows.Close()

	var lines []CostBreakdownLineDto
	for bRows.Next() {
		var line CostBreakdownLineDto
		if err := bRows.Scan(
			&line.OrderNo, &line.LotNo, &line.CostElement,
			&line.Qty, &line.UomCode, &line.UnitCost, &line.Amount, &line.IsService,
		); err == nil {
			lines = append(lines, line)
		}
	}
	return lines
}
