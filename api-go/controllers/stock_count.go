package controllers

// Stock count (sayım): the counted quantity of each lot in one warehouse against the ledger
// balance when the count is saved. A difference becomes an ADJUSTMENT_IN / ADJUSTMENT_OUT movement
// with its reason, so the ledger matches the shelf again and the mass balance shows the
// difference as undocumented. A shortage worth checking raises an alert (alerts.go).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

type StockCountController struct {
	db *sql.DB
}

func NewStockCountController(db *sql.DB) *StockCountController {
	return &StockCountController{db: db}
}

type StockCountLineRequest struct {
	LotNo       string   `json:"lot_no"`
	CountedQty  float64  `json:"counted_qty"`
	ExpectedQty *float64 `json:"expected_qty,omitempty"` // the balance the counter saw; 409 if it changed meanwhile
	Reason      string   `json:"reason"`                 // required when counted differs from the balance
}

type StockCountRequest struct {
	CountNo   string                  `json:"count_no"` // default SAY-<UTC timestamp>
	Warehouse string                  `json:"warehouse"`
	Note      string                  `json:"note"`
	Lines     []StockCountLineRequest `json:"lines"`
}

type StockCountLineDto struct {
	LotNo        string  `json:"lot_no"`
	ProductName  string  `json:"product_name"`
	Uom          string  `json:"uom_code"`
	SystemQty    float64 `json:"system_qty"`
	CountedQty   float64 `json:"counted_qty"`
	DiffQty      float64 `json:"diff_qty"`
	ValueEUR     float64 `json:"value_eur"` // difference x lot unit cost
	Reason       string  `json:"reason,omitempty"`
	MovementType string  `json:"movement_type,omitempty"`
}

type StockCountDto struct {
	CountNo     string              `json:"count_no"`
	Warehouse   string              `json:"warehouse"`
	CountedAt   time.Time           `json:"counted_at"`
	CountedBy   string              `json:"counted_by"`
	Note        string              `json:"note,omitempty"`
	Lines       []StockCountLineDto `json:"lines"`
	ShortageEUR float64             `json:"shortage_eur"`
	SurplusEUR  float64             `json:"surplus_eur"`
}

func validateStockCount(req *StockCountRequest) fieldErrors {
	fe := fieldErrors{}
	if len(req.CountNo) > 30 {
		fe["count_no"] = "max 30 characters"
	}
	if strings.TrimSpace(req.Warehouse) == "" {
		fe["warehouse"] = "required"
	}
	if len([]rune(req.Note)) > 255 {
		fe["note"] = "max 255 characters"
	}
	if len(req.Lines) == 0 || len(req.Lines) > 200 {
		fe["lines"] = "1 to 200 lines"
	}
	seen := map[string]bool{}
	for i, l := range req.Lines {
		k := fmt.Sprintf("lines[%d].", i)
		if l.LotNo == "" {
			fe[k+"lot_no"] = "required"
		} else if seen[l.LotNo] {
			fe[k+"lot_no"] = "duplicate in this count"
		}
		seen[l.LotNo] = true
		if math.IsNaN(l.CountedQty) || l.CountedQty < 0 || l.CountedQty > 1e9 {
			fe[k+"counted_qty"] = "must be >= 0"
		}
		if len([]rune(l.Reason)) > 255 {
			fe[k+"reason"] = "max 255 characters"
		}
	}
	return fe
}

// Create handles POST /stock-counts (supervisor session). All lines are saved or none: 422 for
// a difference without reason or a fraction of a pack, 409 when a lot's balance changed after
// the counter loaded it (expected_qty), 404 for an unknown lot or warehouse.
func (c *StockCountController) Create(w http.ResponseWriter, r *http.Request) {
	var req StockCountRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.CountNo = strings.TrimSpace(nonEmpty(req.CountNo, "SAY-"+time.Now().UTC().Format("20060102-150405")))
	req.Note = strings.TrimSpace(req.Note)
	if fe := validateStockCount(&req); len(fe) > 0 {
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
	wh, err := warehouseID(ctx, tx, req.Warehouse)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	actor := nonEmpty(OperatorFrom(ctx), "api")
	res, err := tx.ExecContext(ctx, `INSERT INTO stock_count (count_no, warehouse_id, counted_by, note) VALUES (?, ?, ?, NULLIF(?, ''))`,
		req.CountNo, wh, actor, req.Note)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	countID, _ := res.LastInsertId()
	out := StockCountDto{CountNo: req.CountNo, Warehouse: req.Warehouse, CountedBy: actor, Note: req.Note, Lines: []StockCountLineDto{}}
	var shortages []alertRow
	th := loadThresholds(ctx, tx)["COUNT_SHORTAGE"]
	for i, l := range req.Lines {
		k := fmt.Sprintf("lines[%d].", i)
		lot, cost, err := lotID(ctx, tx, l.LotNo)
		var nf notFoundError
		if errors.As(err, &nf) {
			writeAPIError(w, r, http.StatusNotFound, "LOT_NOT_FOUND", "lot "+l.LotNo)
			return
		}
		if err != nil {
			writeDBError(w, r, err)
			return
		}
		if err := wholeUnits(ctx, tx, lot, l.CountedQty); err != nil {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{k + "counted_qty": "whole units only for this lot"})
			return
		}
		line := StockCountLineDto{LotNo: l.LotNo, CountedQty: round3(l.CountedQty), Reason: strings.TrimSpace(l.Reason)}
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT p.name, l.uom_code, l.status FROM lot l JOIN product p ON p.id = l.product_id WHERE l.id = ?`, lot).
			Scan(&line.ProductName, &line.Uom, &status); err != nil {
			writeDBError(w, r, err)
			return
		}
		// the balance row is locked: a sale syncing meanwhile waits and cannot slip between count and adjustment
		err = tx.QueryRowContext(ctx, `SELECT qty FROM stock_balance WHERE lot_id = ? AND warehouse_id = ? FOR UPDATE`, lot, wh).Scan(&line.SystemQty)
		if err != nil && err != sql.ErrNoRows {
			writeDBError(w, r, err)
			return
		}
		if l.ExpectedQty != nil && math.Abs(*l.ExpectedQty-line.SystemQty) > massBalanceTolerance {
			writeAPIError(w, r, http.StatusConflict, "CONFLICT", fmt.Sprintf("%s: balance changed from %.3f to %.3f after the count list was loaded", l.LotNo, *l.ExpectedQty, line.SystemQty))
			return
		}
		line.DiffQty = round3(line.CountedQty - line.SystemQty)
		line.ValueEUR = round2(line.DiffQty * cost)
		var movementID sql.NullInt64
		if math.Abs(line.DiffQty) > massBalanceTolerance {
			if line.Reason == "" {
				writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{k + "reason": "required for a difference"})
				return
			}
			line.MovementType = "ADJUSTMENT_IN"
			if line.DiffQty < 0 {
				line.MovementType = "ADJUSTMENT_OUT"
			}
			res, err := tx.ExecContext(ctx,
				`INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, ref_id, created_by, note)
				 VALUES (?, ?, ?, ?, ?, 'stock_count', ?, ?, ?)`, lot, wh, line.MovementType, line.DiffQty, cost, countID, actor,
				clip(req.CountNo+": "+line.Reason, 255))
			if err != nil {
				writeDBError(w, r, err)
				return
			}
			id, _ := res.LastInsertId()
			movementID = sql.NullInt64{Int64: id, Valid: true}
		} else {
			line.DiffQty, line.ValueEUR, line.Reason = 0, 0, ""
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO stock_count_line (count_id, lot_id, system_qty, counted_qty, reason, stock_movement_id) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)`,
			countID, lot, line.SystemQty, line.CountedQty, line.Reason, movementID)
		if err != nil {
			writeDBError(w, r, err)
			return
		}
		lineID, _ := res.LastInsertId()
		if line.DiffQty < 0 {
			out.ShortageEUR += -line.ValueEUR
			if a := countShortageAlert(lineID, line, status, req.Warehouse, req.CountNo, th); a != nil {
				shortages = append(shortages, *a)
			}
		} else {
			out.SurplusEUR += line.ValueEUR
		}
		out.Lines = append(out.Lines, line)
	}
	for _, a := range shortages {
		logAlertError("stock count "+req.CountNo, insertAlert(ctx, tx, a))
	}
	if err := tx.QueryRowContext(ctx, `SELECT counted_at FROM stock_count WHERE id = ?`, countID).Scan(&out.CountedAt); err != nil {
		writeDBError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeDBError(w, r, err)
		return
	}
	out.ShortageEUR, out.SurplusEUR = round2(out.ShortageEUR), round2(out.SurplusEUR)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, out)
}

// countShortageAlert grades a count line with less on the shelf than in the ledger: by its value
// at lot cost (a shortage up to the warning value is weighing tolerance), and always critical for
// a recalled or blocked lot (those goods must be found).
func countShortageAlert(lineID int64, l StockCountLineDto, lotStatus, warehouse, countNo string, th threshold) *alertRow {
	if l.DiffQty >= 0 {
		return nil
	}
	loss := -l.ValueEUR
	sev := severity(round(loss, 2), th.Warn, th.Crit)
	blocked := lotStatus == "RECALLED" || lotStatus == "BLOCKED"
	if blocked {
		sev = "CRITICAL"
	}
	if sev == "" {
		return nil
	}
	qty := fmt.Sprintf("%s %s", strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", -l.DiffQty), "0"), "."), strings.ToLower(l.Uom))
	what := "Sayımda eksik"
	if blocked {
		what = "Geri çağrılmış/bloke lot sayımda eksik"
	}
	return &alertRow{Task: "LOT_MOVEMENT", Detector: "rule", ModelVersion: ruleVersion, RefType: "stock_count_line", RefID: lineID,
		Score: loss, ThresholdWarn: th.Warn, ThresholdCrit: th.Crit, Severity: sev, Direction: "COUNT_SHORTAGE",
		Expected: l.SystemQty, Actual: l.CountedQty, Deviation: l.DiffQty, EstLoss: loss,
		Detail:  clip(fmt.Sprintf("%s · %s", warehouse, l.Reason), 120),
		Message: fmt.Sprintf("%s: %s, %s %s deposunda bulunamadı (≈%.0f €). Gerekçe: %s.", what, l.LotNo, qty, warehouse, round(loss, 0), l.Reason)}
}

// List handles GET /stock-counts: the latest 20 counts with their lines.
func (c *StockCountController) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return
	}
	out, err := loadStockCounts(r.Context(), c.db, 20)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	writeJSON(w, out)
}

func loadStockCounts(ctx context.Context, q sqlRunner, limit int) ([]StockCountDto, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT sc.count_no, w.code, sc.counted_at, sc.counted_by, COALESCE(sc.note, ''),
		        l.lot_no, p.name, l.uom_code, scl.system_qty, scl.counted_qty, scl.diff_qty,
		        ROUND(scl.diff_qty * l.unit_cost, 2), COALESCE(scl.reason, ''), COALESCE(sm.movement_type, '')
		 FROM (SELECT * FROM stock_count ORDER BY counted_at DESC, id DESC LIMIT ?) sc
		 JOIN warehouse w ON w.id = sc.warehouse_id
		 JOIN stock_count_line scl ON scl.count_id = sc.id
		 JOIN lot l ON l.id = scl.lot_id JOIN product p ON p.id = l.product_id
		 LEFT JOIN stock_movement sm ON sm.id = scl.stock_movement_id
		 ORDER BY sc.counted_at DESC, sc.id DESC, scl.id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StockCountDto{}
	for rows.Next() {
		var c StockCountDto
		var l StockCountLineDto
		if err := rows.Scan(&c.CountNo, &c.Warehouse, &c.CountedAt, &c.CountedBy, &c.Note, &l.LotNo, &l.ProductName, &l.Uom,
			&l.SystemQty, &l.CountedQty, &l.DiffQty, &l.ValueEUR, &l.Reason, &l.MovementType); err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].CountNo != c.CountNo {
			c.Lines = []StockCountLineDto{}
			out = append(out, c)
		}
		last := &out[len(out)-1]
		last.Lines = append(last.Lines, l)
		if l.ValueEUR < 0 {
			last.ShortageEUR = round2(last.ShortageEUR - l.ValueEUR)
		} else {
			last.SurplusEUR = round2(last.SurplusEUR + l.ValueEUR)
		}
	}
	return out, rows.Err()
}
