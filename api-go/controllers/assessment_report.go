package controllers

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

type AssessmentPrediction struct {
	Reference string   `json:"reference"`
	Target    string   `json:"target"`
	Model     string   `json:"model"`
	Predicted float64  `json:"predicted"`
	Actual    *float64 `json:"actual"`
	Origin    string   `json:"origin"` // SEED | DEMO | FIELD
}

type AssessmentReport struct {
	GeneratedAt time.Time                     `json:"generated_at"`
	Production  ProductionOrderDto            `json:"production"`
	Butchery    []ButcheryOperationSummaryDto `json:"butchery"`
	Trace       TraceResponse                 `json:"trace"`
	Backward    TraceResponse                 `json:"backward"`
	Recalls     []RecallProgressDto           `json:"recalls"`
	Metrics     []AIMetricDto                 `json:"metrics"`
	Predictions []AssessmentPrediction        `json:"predictions"`
	DataNote    string                        `json:"data_note"`
}

// Assessment reads all evidence on one connection and one repeatable-read snapshot. It never
// initiates a recall, resets data, records a movement or serves hardcoded scenario results.
func (c *ReportsController) Assessment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, r, 405, "METHOD_NOT_ALLOWED", nil)
		return
	}
	order := nonEmpty(r.URL.Query().Get("order_no"), "URT-2026-045")
	root := nonEmpty(r.URL.Query().Get("root_lot"), "ET-2026-001")
	rejected := nonEmpty(r.URL.Query().Get("rejected_op"), "PRC-2026-000")
	posted := nonEmpty(r.URL.Query().Get("posted_op"), "PRC-2026-001")
	tx, err := c.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer tx.Rollback()
	report, err := loadAssessment(r, tx, order, root, rejected, posted)
	if err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "NOT_FOUND", "assessment scenario record missing")
		return
	}
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeDBError(w, r, err)
		return
	}
	writeJSON(w, report)
}

func loadAssessment(r *http.Request, q sqlRunner, order, root, rejected, posted string) (*AssessmentReport, error) {
	ctx := r.Context()
	out := &AssessmentReport{GeneratedAt: time.Now().UTC(), Butchery: []ButcheryOperationSummaryDto{}, Recalls: []RecallProgressDto{},
		Metrics: []AIMetricDto{}, Predictions: []AssessmentPrediction{}, DataNote: "Synthetic demo/training data. Accuracy on real operations has not been established. All traceability comes from recorded lot relationships and deliveries."}
	p := &out.Production
	err := q.QueryRowContext(ctx, `SELECT vc.order_no,vc.sku,vc.product_name,vc.status,vc.planned_qty,vc.actual_qty,vc.qty_variance,vc.fulfilment_pct,
        vc.input_kg,COALESCE(vc.yield_pct_of_meat,0),vc.material_cost,vc.service_cost,vc.total_cost,vc.ekp,vc.ekp_display,COALESCE(vc.vkp,0),
        COALESCE(vc.gross_profit_per_unit,0),COALESCE(vc.revenue_at_vkp,0),COALESCE(vc.gross_profit_total,0),COALESCE(vc.margin_pct,0),
        (SELECT l.lot_no FROM production_output po JOIN production_order o ON o.id=po.production_order_id JOIN lot l ON l.id=po.lot_id WHERE o.order_no=vc.order_no LIMIT 1)
        FROM v_production_cost vc WHERE vc.order_no=?`, order).Scan(&p.OrderNo, &p.ProductSKU, &p.ProductName, &p.Status, &p.PlannedQty, &p.ActualQty, &p.QtyVariance, &p.FulfilmentPct,
		&p.InputKg, &p.YieldPctOfMeat, &p.MaterialCost, &p.ServiceCost, &p.TotalCost, &p.EKP, &p.EKPDisplay, &p.VKP, &p.GrossProfitPerUnit, &p.RevenueAtVKP, &p.GrossProfitTotal, &p.MarginPct, &p.OutputLotNumber)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT order_no,COALESCE(lot_no,''),cost_element,qty,uom_code,unit_cost,amount FROM v_production_cost_breakdown WHERE order_no=? ORDER BY sort_key,lot_no`, order)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l CostBreakdownLineDto
		if err = rows.Scan(&l.OrderNo, &l.LotNo, &l.CostElement, &l.Qty, &l.UomCode, &l.UnitCost, &l.Amount); err != nil {
			rows.Close()
			return nil, err
		}
		p.Breakdown = append(p.Breakdown, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, op := range []string{rejected, posted} {
		b, err := assessmentButchery(r, q, op)
		if err != nil {
			return nil, err
		}
		out.Butchery = append(out.Butchery, *b)
	}
	tc := &TraceController{db: q}
	out.Trace = TraceResponse{RootLotNumber: root, Direction: "forward", Lots: []TraceLotDto{}, Links: []TraceLinkDto{}}
	if err = tc.forward(r, root, &out.Trace); err != nil {
		return nil, err
	}
	if err = tc.loadLinks(r, &out.Trace); err != nil {
		return nil, err
	}
	if out.Trace.MassBalance, err = loadMassBalance(ctx, q, out.Trace.Lots); err != nil {
		return nil, err
	}
	out.Backward = TraceResponse{RootLotNumber: p.OutputLotNumber, Direction: "backward", Lots: []TraceLotDto{}, Links: []TraceLinkDto{}}
	if err = tc.backward(r, p.OutputLotNumber, &out.Backward); err != nil {
		return nil, err
	}
	if err = tc.loadLinks(r, &out.Backward); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT DISTINCT rc.recall_no FROM recall rc JOIN recall_lot rl ON rl.recall_id=rc.id JOIN lot l ON l.id=rl.lot_id WHERE l.lot_no=? ORDER BY rc.recall_no`, root)
	if err != nil {
		return nil, err
	}
	var codes []string
	for rows.Next() {
		var code string
		if err = rows.Scan(&code); err != nil {
			rows.Close()
			return nil, err
		}
		codes = append(codes, code)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, code := range codes {
		progress, err := loadRecallProgress(ctx, q, code)
		if err != nil {
			return nil, err
		}
		out.Recalls = append(out.Recalls, *progress)
	}
	rows, err = q.QueryContext(ctx, `SELECT task,target_name,unit,model_name,role,eval_split,n_rows,mae,mape_pct FROM ml_model_metric ORDER BY task,target_name,role DESC,model_name`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var m AIMetricDto
		if err = rows.Scan(&m.Task, &m.Target, &m.Unit, &m.Model, &m.Role, &m.EvalSplit, &m.NRows, &m.MAE, &m.MAPEPct); err != nil {
			rows.Close()
			return nil, err
		}
		out.Metrics = append(out.Metrics, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out.Predictions, err = assessmentPredictions(ctx, q, order, posted)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func assessmentButchery(r *http.Request, q sqlRunner, op string) (*ButcheryOperationSummaryDto, error) {
	var b ButcheryOperationSummaryDto
	var declared, diff sql.NullFloat64
	err := q.QueryRowContext(r.Context(), `SELECT op_no,species,allocation_method,status,input_weight_kg,output_weight_kg,cut_weight_kg,waste_weight_kg,weight_diff_kg,
        purchase_unit_price,purchase_cost,additional_cost,total_cost,declared_total_cost,declared_cost_diff,allocated_total,allocation_diff,weight_ok,declared_cost_ok,allocation_ok
        FROM v_butchery_reconciliation WHERE op_no=?`, op).Scan(&b.OpNo, &b.Species, &b.AllocationMethod, &b.Status, &b.InputWeightKg, &b.OutputWeightKg, &b.CutWeightKg, &b.WasteWeightKg, &b.WeightDiffKg,
		&b.PurchaseUnitPrice, &b.PurchaseCost, &b.AdditionalCost, &b.TotalCost, &declared, &diff, &b.AllocatedTotal, &b.AllocationDiff, &b.WeightOk, &b.DeclaredCostOk, &b.AllocationOk)
	if err != nil {
		return nil, err
	}
	if declared.Valid {
		b.DeclaredTotalCost = &declared.Float64
	}
	if diff.Valid {
		b.DeclaredCostDiff = &diff.Float64
	}
	rows, err := q.QueryContext(r.Context(), `SELECT p.sku,p.name,COALESCE(l.lot_no,''),bo.output_kind,bo.weight_kg,
        COALESCE(p.sale_price,0),bo.allocation_basis,bo.allocated_cost,bo.unit_cost
        FROM butchery_output bo JOIN butchery_operation o ON o.id=bo.operation_id JOIN product p ON p.id=bo.product_id LEFT JOIN lot l ON l.id=bo.lot_id
        WHERE o.op_no=? ORDER BY bo.id`, op)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	b.Cuts = []ButcheryCutAllocatedOutput{}
	for rows.Next() {
		var c ButcheryCutAllocatedOutput
		if err = rows.Scan(&c.CutSKU, &c.Name, &c.LotNumber, &c.OutputKind, &c.YieldWeightKg, &c.MarketPricePerKg, &c.AllocationBasis, &c.AllocatedCost, &c.AllocatedUnitCost); err != nil {
			return nil, err
		}
		if b.InputWeightKg > 0 {
			c.YieldPct = 100 * c.YieldWeightKg / b.InputWeightKg
		}
		c.TotalSalesValue = c.YieldWeightKg * c.MarketPricePerKg
		if c.TotalSalesValue > 0 {
			c.MarginPercentage = 100 * (c.TotalSalesValue - c.AllocatedCost) / c.TotalSalesValue
		}
		b.Cuts = append(b.Cuts, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	analyseButchery(r, &b)
	return &b, nil
}

func assessmentPredictions(ctx context.Context, q sqlRunner, order, op string) ([]AssessmentPrediction, error) {
	rows, err := q.QueryContext(ctx, `SELECT COALESCE(po.order_no,bo.op_no),mp.target_name,mp.model_name,mp.predicted_value,mp.actual_value,mp.data_origin
        FROM ml_prediction mp
        LEFT JOIN production_order po ON mp.ref_type='production_order' AND po.id=mp.ref_id
        LEFT JOIN butchery_operation bo ON mp.ref_type='butchery_operation' AND bo.id=mp.ref_id
        WHERE (po.order_no=? OR bo.op_no=?) AND mp.model_name IN (?,?) ORDER BY 1,2,3`, order, op, championModel, baselineModel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssessmentPrediction{}
	for rows.Next() {
		var p AssessmentPrediction
		var actual sql.NullFloat64
		if err = rows.Scan(&p.Reference, &p.Target, &p.Model, &p.Predicted, &actual, &p.Origin); err != nil {
			return nil, err
		}
		if actual.Valid {
			p.Actual = &actual.Float64
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
