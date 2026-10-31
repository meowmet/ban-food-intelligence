package controllers

// Alert thresholds and their evidence (GET/POST /alerts/rules), and problems found by hand that no
// alert flagged (POST /alerts/missed). The synthetic stress test of the rules
// (ml-engine/calibrate_live_alerts.py) is served with the thresholds, so a supervisor sees what a
// value did in that test before choosing it. Field accuracy only comes from what is recorded here:
// reviewed alerts give the precision, missed findings the recall.

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

//go:embed aimodel/live_alert_calibration.json
var liveAlertCalibration []byte

// ruleUnits: band98 = multiple of the 98 % band, pct = % above the reference price, eur = shortage value.
var ruleUnits = map[string]string{"PRODUCTION_YIELD": "band98", "BUTCHERY_YIELD": "band98", "PURCHASE_COST": "pct", "COUNT_SHORTAGE": "eur"}
var unitMax = map[string]float64{"band98": 10, "pct": 500, "eur": 1e6}

// missTasks: the checks a missed problem can be charged to.
var missTasks = map[string]bool{"PRODUCTION_YIELD": true, "BUTCHERY_YIELD": true, "PURCHASE_COST": true, "LOT_MOVEMENT": true}

type AlertRuleDto struct {
	Rule        string     `json:"rule"`
	Unit        string     `json:"unit"`
	Warn        float64    `json:"warn"`
	Crit        float64    `json:"crit"`
	DefaultWarn float64    `json:"default_warn"`
	DefaultCrit float64    `json:"default_crit"`
	UpdatedBy   string     `json:"updated_by"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

// PriceChangesDto: unit price of every recorded purchase line against the mean of the product's
// last three earlier lines, i.e. the price volatility of this business as data accumulates.
type PriceChangesDto struct {
	Changes        int      `json:"changes"`
	P95IncreasePct *float64 `json:"p95_increase_pct"`
	AboveWarnPct   *float64 `json:"above_warn_pct"` // share of changes above the current warning threshold
}

type AlertRulesDto struct {
	Rules                []AlertRuleDto  `json:"rules"`
	ObservedPriceChanges PriceChangesDto `json:"observed_price_changes"`
	Calibration          json.RawMessage `json:"calibration"`
}

// Rules handles GET /alerts/rules.
func (c *AlertController) Rules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return
	}
	ctx := r.Context()
	byRule := map[string]AlertRuleDto{}
	for rule, d := range defaultThresholds {
		byRule[rule] = AlertRuleDto{Rule: rule, Unit: ruleUnits[rule], Warn: d.Warn, Crit: d.Crit, DefaultWarn: d.Warn, DefaultCrit: d.Crit, UpdatedBy: "default"}
	}
	rows, err := c.db.QueryContext(ctx, `SELECT rule, warn, crit, updated_by, updated_at FROM alert_threshold`)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	for rows.Next() {
		var rule, by string
		var warn, crit float64
		var at time.Time
		if err := rows.Scan(&rule, &warn, &crit, &by, &at); err != nil {
			rows.Close()
			writeDBError(w, r, err)
			return
		}
		if d, ok := byRule[rule]; ok {
			d.Warn, d.Crit, d.UpdatedBy, d.UpdatedAt = warn, crit, by, &at
			byRule[rule] = d
		}
	}
	rows.Close()
	out := AlertRulesDto{Calibration: json.RawMessage(liveAlertCalibration)}
	for _, rule := range []string{"PRODUCTION_YIELD", "BUTCHERY_YIELD", "PURCHASE_COST", "COUNT_SHORTAGE"} {
		out.Rules = append(out.Rules, byRule[rule])
	}
	if out.ObservedPriceChanges, err = c.priceChanges(r, byRule["PURCHASE_COST"].Warn); err != nil {
		writeDBError(w, r, err)
		return
	}
	writeJSON(w, out)
}

func (c *AlertController) priceChanges(r *http.Request, warn float64) (PriceChangesDto, error) {
	rows, err := c.db.QueryContext(r.Context(),
		`SELECT prl.product_id, prl.unit_price FROM purchase_receipt_line prl JOIN purchase_receipt pr ON pr.id = prl.receipt_id
		 ORDER BY prl.product_id, pr.receipt_date, prl.id`)
	if err != nil {
		return PriceChangesDto{}, err
	}
	defer rows.Close()
	var pct []float64
	var product int64 = -1
	var last []float64
	for rows.Next() {
		var p int64
		var price float64
		if err := rows.Scan(&p, &price); err != nil {
			return PriceChangesDto{}, err
		}
		if p != product {
			product, last = p, nil
		}
		if len(last) > 0 {
			ref := 0.0
			for _, v := range last {
				ref += v
			}
			if ref /= float64(len(last)); ref > 0 {
				pct = append(pct, 100*(price/ref-1))
			}
		}
		if last = append(last, price); len(last) > costReferenceLines {
			last = last[1:]
		}
	}
	out := PriceChangesDto{Changes: len(pct)}
	if len(pct) > 0 {
		sort.Float64s(pct)
		above := 0
		for _, v := range pct {
			if round(v, 3) > warn {
				above++
			}
		}
		out.P95IncreasePct = ptr(round(pct[int(math.Ceil(0.95*float64(len(pct))))-1], 2))
		out.AboveWarnPct = ptr(round(100*float64(above)/float64(len(pct)), 2))
	}
	return out, rows.Err()
}

type AlertRuleRequest struct {
	Rule string  `json:"rule"`
	Warn float64 `json:"warn"`
	Crit float64 `json:"crit"`
}

// UpdateRule handles POST /alerts/rules (supervisor session). New alerts use the new values; alerts
// already written keep the thresholds they were graded with. The change is written to audit_log.
func (c *AlertController) UpdateRule(w http.ResponseWriter, r *http.Request) {
	var req AlertRuleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	unit, ok := ruleUnits[req.Rule]
	fe := fieldErrors{}
	if !ok {
		fe["rule"] = "PRODUCTION_YIELD, BUTCHERY_YIELD, PURCHASE_COST or COUNT_SHORTAGE"
	}
	if math.IsNaN(req.Warn) || !(req.Warn > 0) {
		fe["warn"] = "must be > 0"
	}
	if math.IsNaN(req.Crit) || !(req.Crit > req.Warn) || (ok && req.Crit > unitMax[unit]) {
		fe["crit"] = "must be above warn and at most the unit's maximum"
	}
	if len(fe) > 0 {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fe)
		return
	}
	req.Warn, req.Crit = round(req.Warn, 3), round(req.Crit, 3)
	ctx := r.Context()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer tx.Rollback()
	d := defaultThresholds[req.Rule]
	old := d
	err = tx.QueryRowContext(ctx, `SELECT warn, crit FROM alert_threshold WHERE rule = ? FOR UPDATE`, req.Rule).Scan(&old.Warn, &old.Crit)
	if err != nil && err != sql.ErrNoRows {
		writeDBError(w, r, err)
		return
	}
	actor := nonEmpty(OperatorFrom(ctx), "api")
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO alert_threshold (rule, warn, crit, updated_by) VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE warn = VALUES(warn), crit = VALUES(crit), updated_by = VALUES(updated_by)`,
		req.Rule, req.Warn, req.Crit, actor); err != nil {
		writeDBError(w, r, err)
		return
	}
	previous, _ := json.Marshal(map[string]float64{"warn": old.Warn, "crit": old.Crit})
	current, _ := json.Marshal(map[string]interface{}{"rule": req.Rule, "warn": req.Warn, "crit": req.Crit})
	out := AlertRuleDto{Rule: req.Rule, Unit: unit, Warn: req.Warn, Crit: req.Crit, DefaultWarn: d.Warn, DefaultCrit: d.Crit, UpdatedBy: actor}
	var id int64
	var at time.Time
	if err := tx.QueryRowContext(ctx, `SELECT id, updated_at FROM alert_threshold WHERE rule = ?`, req.Rule).Scan(&id, &at); err != nil {
		writeDBError(w, r, err)
		return
	}
	out.UpdatedAt = &at
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO audit_log (table_name, row_id, action, old_value, new_value, changed_by) VALUES ('alert_threshold', ?, 'THRESHOLD_CHANGE', ?, ?, ?)`,
		id, string(previous), string(current), actor); err != nil {
		writeDBError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeDBError(w, r, err)
		return
	}
	writeJSON(w, out)
}

type AlertMissRequest struct {
	Task       string  `json:"task"`
	RefNo      string  `json:"ref_no"`
	LotNo      string  `json:"lot_no"`
	Note       string  `json:"note"`
	EstLossEUR float64 `json:"est_loss_eur"`
}

type AlertMissDto struct {
	ID         int64     `json:"id"`
	Task       string    `json:"task"`
	RefNo      string    `json:"ref_no,omitempty"`
	LotNo      string    `json:"lot_no,omitempty"`
	Note       string    `json:"note"`
	EstLossEUR float64   `json:"est_loss_eur"`
	FoundBy    string    `json:"found_by"`
	FoundAt    time.Time `json:"found_at"`
}

type AlertMissTotalDto struct {
	Task       string  `json:"task"`
	Findings   int     `json:"findings"`
	EstLossEUR float64 `json:"est_loss_eur"`
}

// Missed handles POST /alerts/missed (supervisor session): a problem found by hand that no alert
// flagged, charged to the check that should have caught it. 201 with its id.
func (c *AlertController) Missed(w http.ResponseWriter, r *http.Request) {
	var req AlertMissRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Note, req.RefNo, req.LotNo = strings.TrimSpace(req.Note), strings.TrimSpace(req.RefNo), strings.TrimSpace(req.LotNo)
	fe := fieldErrors{}
	if !missTasks[req.Task] {
		fe["task"] = "PRODUCTION_YIELD, BUTCHERY_YIELD, PURCHASE_COST or LOT_MOVEMENT"
	}
	if req.Note == "" || len([]rune(req.Note)) > 255 {
		fe["note"] = "required, max 255 characters"
	}
	if len(req.RefNo) > 30 {
		fe["ref_no"] = "max 30 characters"
	}
	if math.IsNaN(req.EstLossEUR) || req.EstLossEUR < 0 || req.EstLossEUR > 1e9 {
		fe["est_loss_eur"] = "must be >= 0"
	}
	if len(fe) > 0 {
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
	var lot sql.NullInt64
	if req.LotNo != "" {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM lot WHERE lot_no = ?`, req.LotNo).Scan(&lot); err == sql.ErrNoRows {
			writeAPIError(w, r, http.StatusNotFound, "LOT_NOT_FOUND", "lot "+req.LotNo)
			return
		} else if err != nil {
			writeDBError(w, r, err)
			return
		}
	}
	actor := nonEmpty(OperatorFrom(ctx), "api")
	res, err := tx.ExecContext(ctx,
		`INSERT INTO alert_miss (task, ref_no, lot_id, note, est_loss_eur, found_by) VALUES (?, NULLIF(?, ''), ?, ?, ?, ?)`,
		req.Task, req.RefNo, lot, req.Note, round(req.EstLossEUR, 2), actor)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	id, _ := res.LastInsertId()
	current, _ := json.Marshal(req)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO audit_log (table_name, row_id, action, old_value, new_value, changed_by) VALUES ('alert_miss', ?, 'MISSED_FINDING', NULL, ?, ?)`,
		id, string(current), actor); err != nil {
		writeDBError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeDBError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]int64{"id": id})
}

// loadMisses returns the missed findings per check and the latest 20.
func loadMisses(r *http.Request, q sqlRunner) ([]AlertMissTotalDto, []AlertMissDto, error) {
	totals, recent := []AlertMissTotalDto{}, []AlertMissDto{}
	rows, err := q.QueryContext(r.Context(), `SELECT task, COUNT(*), SUM(est_loss_eur) FROM alert_miss GROUP BY task ORDER BY task`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var t AlertMissTotalDto
		if err := rows.Scan(&t.Task, &t.Findings, &t.EstLossEUR); err != nil {
			rows.Close()
			return nil, nil, err
		}
		totals = append(totals, t)
	}
	rows.Close()
	rows, err = q.QueryContext(r.Context(),
		`SELECT m.id, m.task, COALESCE(m.ref_no, ''), COALESCE(l.lot_no, ''), m.note, m.est_loss_eur, m.found_by, m.found_at
		 FROM alert_miss m LEFT JOIN lot l ON l.id = m.lot_id ORDER BY m.found_at DESC, m.id DESC LIMIT 20`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m AlertMissDto
		if err := rows.Scan(&m.ID, &m.Task, &m.RefNo, &m.LotNo, &m.Note, &m.EstLossEUR, &m.FoundBy, &m.FoundAt); err != nil {
			return nil, nil, err
		}
		recent = append(recent, m)
	}
	return totals, recent, rows.Err()
}
