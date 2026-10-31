package controllers

// Alert inbox (spec 5: anomaly alerts). The API writes ml_anomaly_alert when a document is
// recorded; the ml-engine batch detector (anomaly_ali.py) writes the same table. Four checks:
//
//   - production receipt: actual packages outside the live forecast's 98 % band (AI 4A)
//   - butchery: a part or the waste outside its 98 % band (AI 4B)
//   - purchase: unit price above the mean of the product's last three purchases
//   - POS sync: a sale of a recalled, blocked or expired lot that the server refused
//
// The thresholds are configuration (alert_threshold, GET/POST /alerts/rules); their defaults come from
// the synthetic stress test in ml-engine/calibrate_live_alerts.py. An alert never changes stock, cost
// or lot status. A supervisor acknowledges or closes it; closed alerts and the problems found by hand
// that no alert flagged (alert_miss) give the observed precision and recall.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	costReferenceLines = 3 // the reference is the mean of the product's last three earlier purchase lines
	ruleVersion        = "rule-1"
)

// threshold of one check: above Warn the record is flagged, from Crit on it is critical.
type threshold struct{ Warn, Crit float64 }

// defaultThresholds: alarm budget of at most one false alarm per 100 records in the synthetic test,
// then the highest recall (docs/AI.md). Yield rules are multiples of the 98 % band, the price rule a
// % above the reference, the count rule a shortage value in EUR (materiality, not a prediction).
var defaultThresholds = map[string]threshold{
	"PRODUCTION_YIELD": {1.0, 1.5},
	"BUTCHERY_YIELD":   {1.5, 2.0}, // six parts: at 1.0 one of them leaves its band on ~9 % of normal carcasses
	"PURCHASE_COST":    {10, 25},
	"COUNT_SHORTAGE":   {10, 100},
}

// loadThresholds reads alert_threshold; a missing table or row keeps the default.
func loadThresholds(ctx context.Context, q sqlRunner) map[string]threshold {
	out := map[string]threshold{}
	for k, v := range defaultThresholds {
		out[k] = v
	}
	rows, err := q.QueryContext(ctx, `SELECT rule, warn, crit FROM alert_threshold`)
	if err != nil {
		log.Printf("[WARN] [ALERT] thresholds not read, defaults used: %v", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var rule string
		var t threshold
		if rows.Scan(&rule, &t.Warn, &t.Crit) == nil {
			out[rule] = t
		}
	}
	return out
}

type alertRow struct {
	Task, Detector, ModelVersion, RefType string
	RefID                                 int64
	EventRef                              string
	Score, ThresholdWarn, ThresholdCrit   float64
	Severity, Direction                   string
	Expected, Actual, Deviation, EstLoss  float64
	Detail, Message                       string
}

// severity grades a non-negative score: above warn is WARN, from crit on CRITICAL.
func severity(score, warn, crit float64) string {
	switch {
	case score >= crit:
		return "CRITICAL"
	case score > warn:
		return "WARN"
	default:
		return ""
	}
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// insertAlert writes one alert. The same record again (same check, document and event) keeps
// the existing row and its review status.
func insertAlert(ctx context.Context, q sqlRunner, a alertRow) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO ml_anomaly_alert (task, detector, model_version, ref_type, ref_id, event_ref, score, threshold_warn,
		        threshold_crit, severity, direction, expected_value, actual_value, deviation, est_loss_eur, top_parts, message)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)
		 ON DUPLICATE KEY UPDATE id = id`,
		a.Task, a.Detector, a.ModelVersion, a.RefType, a.RefID, a.EventRef, round(a.Score, 3), a.ThresholdWarn,
		a.ThresholdCrit, a.Severity, a.Direction, round(a.Expected, 3), round(a.Actual, 3), round(a.Deviation, 3),
		round(a.EstLoss, 2), clip(a.Detail, 120), clip(a.Message, 255))
	return err
}

// --- detectors ---------------------------------------------------------------------------

// productionAlert grades a received order: predicted and actual packages, the 98 % band in
// packages and the forecast EKP (a missing package is valued at it).
func productionAlert(orderID int64, version string, predicted, actual, band, ekp float64, th threshold) *alertRow {
	if band <= 0 {
		return nil
	}
	dev := actual - predicted
	score := dev / band
	sev := severity(math.Abs(round(score, 3)), th.Warn, th.Crit)
	if sev == "" {
		return nil
	}
	a := &alertRow{Task: "PRODUCTION_YIELD", Detector: "conformal_band", ModelVersion: version + "+band98",
		RefType: "production_order", RefID: orderID, Score: score, ThresholdWarn: th.Warn, ThresholdCrit: th.Crit,
		Severity: sev, Expected: predicted, Actual: actual, Deviation: dev}
	if dev < 0 {
		a.Direction, a.EstLoss = "LOW_YIELD", -dev*ekp
		a.Message = fmt.Sprintf("Verim beklenenden düşük: %.0f paket, beklenen %.1f (≈%.0f € kayıp). Fason dolum/kurutma ve et lotunu kontrol edin.",
			actual, predicted, round(a.EstLoss, 0))
	} else {
		a.Direction = "HIGH_YIELD"
		a.Message = fmt.Sprintf("Verim beklenenden yüksek: %.0f paket, beklenen %.1f. Paket ağırlığını ve kabul sayımını kontrol edin.",
			actual, predicted)
	}
	return a
}

// raiseProductionAlert reads the champion forecast of a received order and writes its alert.
func raiseProductionAlert(ctx context.Context, q sqlRunner, o *orderInputs, band float64) error {
	var version string
	var pred, actual float64
	var ekp sql.NullFloat64
	err := q.QueryRowContext(ctx,
		`SELECT mp.model_version, mp.predicted_value, mp.actual_value, e.predicted_value
		 FROM ml_prediction mp
		 LEFT JOIN ml_prediction e ON e.ref_type = mp.ref_type AND e.ref_id = mp.ref_id AND e.model_name = mp.model_name AND e.target_name = 'ekp'
		 WHERE mp.ref_type = 'production_order' AND mp.ref_id = ? AND mp.model_name = ? AND mp.target_name = 'actual_qty'
		   AND mp.actual_value IS NOT NULL
		 ORDER BY mp.id DESC LIMIT 1`, o.orderID, championModel).Scan(&version, &pred, &actual, &ekp)
	if err == sql.ErrNoRows {
		return nil // no live forecast (e.g. no meat in the order)
	}
	if err != nil {
		return err
	}
	if a := productionAlert(o.orderID, version, pred, actual, band, ekp.Float64, loadThresholds(ctx, q)["PRODUCTION_YIELD"]); a != nil {
		return insertAlert(ctx, q, *a)
	}
	return nil
}

// butcheryAlert grades a posted butchery operation from its forecast with recorded weights. The
// score is the largest |deviation| / 98 % band of the six parts; the two largest deviations are
// listed as in the ml-engine detector. Less waste than expected does not count: it is no loss, and
// by-products are recorded apart from waste. Extra waste is valued at the carcass purchase price.
func butcheryAlert(opID int64, fc *ButcheryForecastDto, purchasePrice float64, th threshold) *alertRow {
	if fc == nil {
		return nil
	}
	type dev struct {
		part                    string
		kg, actual, diff, score float64
	}
	var devs []dev
	for _, p := range fc.Parts {
		if p.ActualKg == nil || p.band98 <= 0 {
			continue
		}
		d := *p.ActualKg - p.Kg
		score := math.Abs(d) / p.band98
		if p.Part == "fire" && d < 0 {
			score = 0
		}
		devs = append(devs, dev{p.Part, p.Kg, *p.ActualKg, d, score})
	}
	if len(devs) == 0 {
		return nil
	}
	worst, fire := devs[0], dev{}
	for _, d := range devs {
		if d.score > worst.score {
			worst = d
		}
		if d.part == "fire" {
			fire = d
		}
	}
	sev := severity(round(worst.score, 3), th.Warn, th.Crit)
	if sev == "" {
		return nil
	}
	sort.SliceStable(devs, func(i, j int) bool { return math.Abs(devs[i].diff) > math.Abs(devs[j].diff) })
	var top []string
	for _, d := range devs[:min(2, len(devs))] {
		top = append(top, fmt.Sprintf("%s %+.2f kg", d.part, d.diff))
	}
	detail := strings.Join(top, ", ")
	a := &alertRow{Task: "BUTCHERY_YIELD", Detector: "conformal_band", ModelVersion: fc.ModelVersion + "+band98",
		RefType: "butchery_operation", RefID: opID, Score: worst.score, ThresholdWarn: th.Warn, ThresholdCrit: th.Crit,
		Severity: sev, Detail: detail}
	if fire.part != "" && fire.diff > 0 && round(fire.score, 3) > th.Warn {
		a.Direction, a.Expected, a.Actual, a.Deviation, a.EstLoss = "FIRE_HIGH", fire.kg, fire.actual, fire.diff, fire.diff*purchasePrice
		a.Message = fmt.Sprintf("Beklenmedik fire: %s (≈%.0f € kayıp). Kesim/tartım ve soğutmayı kontrol edin.", detail, round(a.EstLoss, 0))
	} else {
		a.Direction, a.Expected, a.Actual, a.Deviation = "PART_MISMATCH", worst.kg, worst.actual, worst.diff
		a.Message = fmt.Sprintf("Parça ağırlıkları beklenenden farklı: %s. Kesim reçetesini ve tartımı kontrol edin.", detail)
	}
	return a
}

// costIncreaseAlert grades one purchase line against the reference unit price of n earlier lines.
func costIncreaseAlert(lineID int64, sku, uom string, qty, price, ref float64, n int, th threshold) *alertRow {
	if ref <= 0 || n == 0 {
		return nil
	}
	pct := round((price/ref-1)*100, 3)
	sev := severity(pct, th.Warn, th.Crit)
	if sev == "" {
		return nil
	}
	diff := price - ref
	unit := strings.ToLower(uom)
	a := &alertRow{Task: "PURCHASE_COST", Detector: "rule", ModelVersion: ruleVersion, RefType: "purchase_receipt_line",
		RefID: lineID, Score: pct, ThresholdWarn: th.Warn, ThresholdCrit: th.Crit, Severity: sev,
		Direction: "COST_INCREASE", Expected: ref, Actual: price, Deviation: diff, EstLoss: diff * qty,
		Detail: fmt.Sprintf("ref %.2f €/%s, %d alış", ref, unit, n)}
	a.Message = fmt.Sprintf("Alış fiyatı arttı: %s %.2f €/%s, son %d alışın ortalaması %.2f € (+%.1f %%, ≈%.0f € ek maliyet). Tedarikçi fiyatını ve faturayı kontrol edin.",
		sku, price, unit, n, ref, pct, round(a.EstLoss, 0))
	return a
}

// raisePurchaseCostAlerts checks every line of a receipt. The reference is the mean unit price of
// the product's last three purchase lines on or before the receipt date (any supplier, other receipts).
func raisePurchaseCostAlerts(ctx context.Context, q sqlRunner, receiptID int64) error {
	type line struct {
		id, product int64
		sku, uom    string
		qty, price  float64
		receiptDate string
	}
	rows, err := q.QueryContext(ctx,
		`SELECT prl.id, prl.product_id, p.sku, p.uom_code, prl.qty, prl.unit_price, DATE_FORMAT(pr.receipt_date, '%Y-%m-%d')
		 FROM purchase_receipt_line prl JOIN purchase_receipt pr ON pr.id = prl.receipt_id JOIN product p ON p.id = prl.product_id
		 WHERE prl.receipt_id = ? ORDER BY prl.id`, receiptID)
	if err != nil {
		return err
	}
	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.id, &l.product, &l.sku, &l.uom, &l.qty, &l.price, &l.receiptDate); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	th := loadThresholds(ctx, q)["PURCHASE_COST"]
	for _, l := range lines {
		var n int
		var ref sql.NullFloat64
		if err := q.QueryRowContext(ctx,
			`SELECT COUNT(*), AVG(unit_price) FROM (
			   SELECT prl.unit_price FROM purchase_receipt_line prl JOIN purchase_receipt pr ON pr.id = prl.receipt_id
			    WHERE prl.product_id = ? AND prl.receipt_id <> ? AND pr.receipt_date <= ?
			    ORDER BY pr.receipt_date DESC, prl.id DESC LIMIT ?) last_lines`,
			l.product, receiptID, l.receiptDate, costReferenceLines).Scan(&n, &ref); err != nil {
			return err
		}
		if a := costIncreaseAlert(l.id, l.sku, l.uom, l.qty, l.price, ref.Float64, n, th); a != nil {
			if err := insertAlert(ctx, q, *a); err != nil {
				return err
			}
		}
	}
	return nil
}

// raiseRejectedSaleAlert records a POS sale the server refused (LOT_BLOCKED or LOT_EXPIRED). The
// sale happened at the till, possibly offline, so the goods may be with a customer and the shelf
// holds less than the ledger. Each sale (its UUIDv7) gives one alert, also when the phone retries.
func raiseRejectedSaleAlert(ctx context.Context, q sqlRunner, lotNo, code, eventUUID, deviceID string, qty float64, soldAt time.Time) error {
	var lotID int64
	var uom string
	err := q.QueryRowContext(ctx, `SELECT id, uom_code FROM lot WHERE lot_no = ?`, lotNo).Scan(&lotID, &uom)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	a := alertRow{Task: "LOT_MOVEMENT", Detector: "rule", ModelVersion: ruleVersion, RefType: "lot", RefID: lotID,
		EventRef: eventUUID, Score: qty, Severity: "CRITICAL", Actual: qty, Deviation: qty,
		Detail: fmt.Sprintf("%s · %s UTC", deviceID, soldAt.UTC().Format("2006-01-02 15:04"))}
	what := "Geri çağrılmış veya bloke lot"
	a.Direction = "BLOCKED_SALE"
	if code == "LOT_EXPIRED" {
		what, a.Direction = "Son tüketim tarihi geçmiş lot", "EXPIRED_SALE"
	}
	a.Message = fmt.Sprintf("%s kasada satılmış: %s, %s %s (%s). Sunucu satışı reddetti; ürün müşteriye gitmiş olabilir, raf stoku kayıttan düşük.",
		what, lotNo, strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", qty), "0"), "."), strings.ToLower(uom), a.Detail)
	return insertAlert(ctx, q, a)
}

// --- GET /alerts, POST /alerts/review -----------------------------------------------------

type AlertController struct {
	db *sql.DB
}

func NewAlertController(db *sql.DB) *AlertController {
	return &AlertController{db: db}
}

type AlertDto struct {
	ID             int64      `json:"id"`
	Task           string     `json:"task"`
	Severity       string     `json:"severity"`
	Direction      string     `json:"direction"`
	Status         string     `json:"status"`
	RefType        string     `json:"ref_type"`
	RefNo          string     `json:"ref_no"`
	RefDate        string     `json:"ref_date,omitempty"`
	LotNo          string     `json:"lot_no,omitempty"`
	Uom            string     `json:"uom_code,omitempty"`
	Responsible    string     `json:"responsible,omitempty"`
	Expected       float64    `json:"expected_value"`
	Actual         float64    `json:"actual_value"`
	Deviation      float64    `json:"deviation"`
	EstLossEUR     float64    `json:"est_loss_eur"`
	Score          float64    `json:"score"`
	ThresholdWarn  float64    `json:"threshold_warn"`
	ThresholdCrit  float64    `json:"threshold_crit"`
	Detector       string     `json:"detector"`
	ModelVersion   string     `json:"model_version"`
	Detail         string     `json:"detail,omitempty"`
	Message        string     `json:"message"`
	EventRef       string     `json:"event_ref,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	ResolvedBy     string     `json:"resolved_by,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	ResolutionNote string     `json:"resolution_note,omitempty"`
}

type AlertFeedbackDto struct {
	Task              string   `json:"task"`
	ModelVersion      string   `json:"model_version"`
	Alerts            int      `json:"alerts"`
	Open              int      `json:"open_alerts"`
	Confirmed         int      `json:"confirmed"`
	FalseAlarms       int      `json:"false_alarms"`
	PrecisionObserved *float64 `json:"precision_observed"` // null until one alert is closed
	ConfirmedLossEUR  float64  `json:"confirmed_loss_eur"`
}

type AlertsDto struct {
	Alerts       []AlertDto          `json:"alerts"`
	Open         int                 `json:"open"`
	CriticalOpen int                 `json:"critical_open"`
	Feedback     []AlertFeedbackDto  `json:"feedback"`
	Missed       []AlertMissTotalDto `json:"missed"`        // problems found by hand per check
	MissedRecent []AlertMissDto      `json:"missed_recent"` // the latest 20
}

const openStatuses = "('OPEN','ACKNOWLEDGED')"

// List handles GET /alerts[?status=open|closed|all]: open alerts first, critical first, newest first.
func (c *AlertController) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return
	}
	filter := map[string]string{
		"": "", "all": "", "open": "WHERE v.status IN " + openStatuses, "closed": "WHERE v.status IN ('RESOLVED','FALSE_ALARM')",
	}
	where, ok := filter[strings.ToLower(r.URL.Query().Get("status"))]
	if !ok {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{"status": "open, closed or all"})
		return
	}
	ctx := r.Context()
	out := AlertsDto{Alerts: []AlertDto{}, Feedback: []AlertFeedbackDto{}}
	rows, err := c.db.QueryContext(ctx,
		`SELECT v.id, v.task, v.severity, v.direction, v.status, v.ref_type, COALESCE(v.ref_no, ''),
		        COALESCE(DATE_FORMAT(v.ref_date, '%Y-%m-%d'), ''), COALESCE(v.lot_no, ''), COALESCE(l.uom_code, ''),
		        COALESCE(v.responsible, ''), v.expected_value, v.actual_value, v.deviation, v.est_loss_eur, v.score,
		        v.threshold_warn, v.threshold_crit, v.detector, v.model_version, COALESCE(v.top_parts, ''), v.message,
		        v.event_ref, v.created_at, COALESCE(v.resolved_by, ''), v.resolved_at, COALESCE(v.resolution_note, '')
		 FROM v_ml_anomaly_alert v LEFT JOIN lot l ON l.lot_no = v.lot_no `+where+`
		 ORDER BY v.status IN `+openStatuses+` DESC, v.severity = 'CRITICAL' DESC, v.created_at DESC, v.id DESC
		 LIMIT 200`)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	for rows.Next() {
		var a AlertDto
		var resolved sql.NullTime
		if err := rows.Scan(&a.ID, &a.Task, &a.Severity, &a.Direction, &a.Status, &a.RefType, &a.RefNo, &a.RefDate, &a.LotNo,
			&a.Uom, &a.Responsible, &a.Expected, &a.Actual, &a.Deviation, &a.EstLossEUR, &a.Score, &a.ThresholdWarn,
			&a.ThresholdCrit, &a.Detector, &a.ModelVersion, &a.Detail, &a.Message, &a.EventRef, &a.CreatedAt,
			&a.ResolvedBy, &resolved, &a.ResolutionNote); err != nil {
			rows.Close()
			writeDBError(w, r, err)
			return
		}
		if resolved.Valid {
			a.ResolvedAt = &resolved.Time
		}
		out.Alerts = append(out.Alerts, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeDBError(w, r, err)
		return
	}
	if err := c.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(status IN `+openStatuses+`), 0), COALESCE(SUM(status IN `+openStatuses+` AND severity = 'CRITICAL'), 0)
		 FROM ml_anomaly_alert`).Scan(&out.Open, &out.CriticalOpen); err != nil {
		writeDBError(w, r, err)
		return
	}
	fb, err := c.db.QueryContext(ctx,
		`SELECT task, model_version, alerts, open_alerts, confirmed, false_alarms, precision_observed, confirmed_loss_eur
		 FROM v_ml_anomaly_feedback ORDER BY task, model_version`)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer fb.Close()
	for fb.Next() {
		var f AlertFeedbackDto
		var precision sql.NullFloat64
		if err := fb.Scan(&f.Task, &f.ModelVersion, &f.Alerts, &f.Open, &f.Confirmed, &f.FalseAlarms, &precision, &f.ConfirmedLossEUR); err != nil {
			writeDBError(w, r, err)
			return
		}
		if precision.Valid {
			f.PrecisionObserved = &precision.Float64
		}
		out.Feedback = append(out.Feedback, f)
	}
	if err := fb.Err(); err != nil {
		writeDBError(w, r, err)
		return
	}
	if out.Missed, out.MissedRecent, err = loadMisses(r, c.db); err != nil {
		writeDBError(w, r, err)
		return
	}
	writeJSON(w, out)
}

type AlertReviewRequest struct {
	ID     int64  `json:"id"`
	Status string `json:"status"` // ACKNOWLEDGED | RESOLVED (a real problem) | FALSE_ALARM
	Note   string `json:"note"`   // required to close
}

// Review handles POST /alerts/review (supervisor session). An open alert can be acknowledged or
// closed; a closed one is final (409). Closing needs a note. Every review is in audit_log.
func (c *AlertController) Review(w http.ResponseWriter, r *http.Request) {
	var req AlertReviewRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	closing := req.Status == "RESOLVED" || req.Status == "FALSE_ALARM"
	fe := fieldErrors{}
	if req.ID <= 0 {
		fe["id"] = "required"
	}
	if !closing && req.Status != "ACKNOWLEDGED" {
		fe["status"] = "ACKNOWLEDGED, RESOLVED or FALSE_ALARM"
	}
	if closing && req.Note == "" {
		fe["note"] = "required to close an alert"
	}
	if len([]rune(req.Note)) > 255 {
		fe["note"] = "max 255 characters"
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
	var status, note string
	err = tx.QueryRowContext(ctx, `SELECT status, COALESCE(resolution_note, '') FROM ml_anomaly_alert WHERE id = ? FOR UPDATE`, req.ID).
		Scan(&status, &note)
	if err == sql.ErrNoRows {
		writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", nil)
		return
	}
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	if status == "RESOLVED" || status == "FALSE_ALARM" {
		writeAPIError(w, r, http.StatusConflict, "CONFLICT", "alert is closed")
		return
	}
	actor := nonEmpty(OperatorFrom(ctx), "api")
	if _, err := tx.ExecContext(ctx,
		`UPDATE ml_anomaly_alert SET status = ?, resolved_by = ?, resolution_note = NULLIF(?, ''),
		        resolved_at = IF(? IN ('RESOLVED','FALSE_ALARM'), CURRENT_TIMESTAMP(3), NULL)
		 WHERE id = ?`, req.Status, actor, req.Note, req.Status, req.ID); err != nil {
		writeDBError(w, r, err)
		return
	}
	previous, _ := json.Marshal(map[string]string{"status": status, "note": note})
	current, _ := json.Marshal(map[string]string{"status": req.Status, "note": req.Note})
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO audit_log (table_name, row_id, action, old_value, new_value, changed_by) VALUES ('ml_anomaly_alert', ?, 'ALERT_REVIEW', ?, ?, ?)`,
		req.ID, string(previous), string(current), actor); err != nil {
		writeDBError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeDBError(w, r, err)
		return
	}
	writeJSON(w, map[string]interface{}{"id": req.ID, "status": req.Status})
}

// logAlertError keeps a failed alert from failing the business record that triggered it.
func logAlertError(what string, err error) {
	if err != nil {
		log.Printf("[WARN] [ALERT] %s: %v", what, err)
	}
}
