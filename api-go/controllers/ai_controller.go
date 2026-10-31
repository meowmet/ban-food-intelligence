package controllers

import (
	"database/sql"
	"math"
	"net/http"
	"sort"
)

// AIController serves the mandatory AI studies (spec section 4): prediction vs actual vs
// deviation for production (A) and butchery (B), next to a simple baseline, plus the
// held-out error metrics. Predictions come from ml-engine (synthetic training data);
// traceability and recall never use them.
type AIController struct {
	db *sql.DB
}

func NewAIController(db *sql.DB) *AIController {
	return &AIController{db: db}
}

const championModel = "ali_ridge"
const baselineModel = "baseline_hist_mean"

type AIMetricDto struct {
	Task      string  `json:"task"`
	Target    string  `json:"target"`
	Unit      string  `json:"unit"`
	Model     string  `json:"model"`
	Role      string  `json:"role"` // MODEL | BASELINE
	EvalSplit string  `json:"eval_split"`
	NRows     int     `json:"n_rows"`
	MAE       float64 `json:"mae"`
	MAPEPct   float64 `json:"mape_pct"`
}

type AIComparisonDto struct {
	Task              string   `json:"task"`   // PRODUCTION | BUTCHERY
	RefNo             string   `json:"ref_no"` // URT-2026-045 / PRC-2026-001
	Target            string   `json:"target"` // actual_qty, ekp, but_kg ...
	Origin            string   `json:"origin"` // SEED | DEMO | FIELD (ml_prediction.data_origin; FIELD is unverified)
	Actual            *float64 `json:"actual"`
	ModelPred         *float64 `json:"model_pred"`
	ModelDev          *float64 `json:"model_dev"`
	ModelDevPct       *float64 `json:"model_dev_pct"`
	BaselinePred      *float64 `json:"baseline_pred"`
	BaselineDev       *float64 `json:"baseline_dev"`
	BaselineDevPct    *float64 `json:"baseline_dev_pct"`
	ModelCloserThanBL *bool    `json:"model_closer_than_baseline"`
	// Optional (ml-engine ali-1.1.0): conformal interval of model_pred in the same unit, its level
	// (0.8), and the anomaly flag (actual outside the 98 % band). Absent on rows without them.
	IntervalLow     *float64 `json:"interval_low,omitempty"`
	IntervalHigh    *float64 `json:"interval_high,omitempty"`
	IntervalLevel   *float64 `json:"interval_level,omitempty"`
	Anomaly         *bool    `json:"anomaly,omitempty"`
	OutsideInterval *bool    `json:"outside_interval,omitempty"`
}

type AISummaryDto struct {
	Model       string            `json:"model"`
	Baseline    string            `json:"baseline"`
	DataNote    string            `json:"data_note"`
	Metrics     []AIMetricDto     `json:"metrics"`     // synthetic held-out test
	Comparisons []AIComparisonDto `json:"comparisons"` // records in this database
	Records     map[string]int    `json:"records"`     // documents behind the comparisons by origin
}

// Summary handles GET /ai/summary.
func (c *AIController) Summary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return
	}
	resp := AISummaryDto{
		Model:    championModel,
		Baseline: baselineModel,
		DataNote: "Models are trained on synthetic, anonymised history (ml-engine/generator_ali.py). Metrics are measured on the latest 20% of dates (time split). Small and synthetic data: indicative only. " +
			"Comparisons are records in this database by origin: SEED (seed.sql), DEMO (entered in a demo deployment), FIELD (entered with DATA_ORIGIN=FIELD, not verified). " +
			"Verified field performance is measured outside the app (ml-engine/audit_field.py).",
		Metrics: []AIMetricDto{}, Comparisons: []AIComparisonDto{},
		Records: map[string]int{"SEED": 0, "DEMO": 0, "FIELD": 0},
	}

	rows, err := c.db.QueryContext(r.Context(),
		`SELECT task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct
		 FROM ml_model_metric ORDER BY task, target_name, role DESC, mae`)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	for rows.Next() {
		var m AIMetricDto
		if err := rows.Scan(&m.Task, &m.Target, &m.Unit, &m.Model, &m.Role, &m.EvalSplit, &m.NRows, &m.MAE, &m.MAPEPct); err == nil {
			resp.Metrics = append(resp.Metrics, m)
		}
	}
	rows.Close()

	prow, err := c.db.QueryContext(r.Context(),
		`SELECT IF(mp.ref_type = 'production_order', 'PRODUCTION', 'BUTCHERY'),
		        COALESCE(po.order_no, bo.op_no), mp.target_name, mp.model_name, mp.predicted_value, mp.actual_value,
		        mp.interval_low, mp.interval_high, mp.interval_level, mp.is_anomaly, mp.data_origin
		 FROM ml_prediction mp
		 LEFT JOIN production_order po   ON mp.ref_type = 'production_order'   AND po.id = mp.ref_id
		 LEFT JOIN butchery_operation bo ON mp.ref_type = 'butchery_operation' AND bo.id = mp.ref_id
		 WHERE mp.model_name IN (?, ?)`, championModel, baselineModel)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer prow.Close()

	byKey := map[string]*AIComparisonDto{}
	docs := map[string]string{} // task|ref -> origin
	for prow.Next() {
		var task, ref, target, model, origin string
		var pred float64
		var actual, lo, hi, level sql.NullFloat64
		var anomaly sql.NullBool
		if err := prow.Scan(&task, &ref, &target, &model, &pred, &actual, &lo, &hi, &level, &anomaly, &origin); err != nil {
			continue
		}
		key := task + "|" + ref + "|" + target
		cmp, ok := byKey[key]
		if !ok {
			cmp = &AIComparisonDto{Task: task, RefNo: ref, Target: target, Origin: origin}
			byKey[key] = cmp
		}
		docs[task+"|"+ref] = origin
		if actual.Valid {
			a := actual.Float64
			cmp.Actual = &a
		}
		p := pred
		if model == championModel {
			cmp.ModelPred = &p
			// the interval and the anomaly flag belong to the champion's prediction only
			if lo.Valid && hi.Valid {
				l, h := lo.Float64, hi.Float64
				cmp.IntervalLow, cmp.IntervalHigh = &l, &h
				if level.Valid {
					lv := level.Float64
					cmp.IntervalLevel = &lv
				}
			}
			if anomaly.Valid {
				a := anomaly.Bool
				cmp.Anomaly = &a
			}
		} else {
			cmp.BaselinePred = &p
		}
	}
	for _, cmp := range byKey {
		if cmp.Actual != nil {
			cmp.ModelDev, cmp.ModelDevPct = deviation(cmp.ModelPred, *cmp.Actual)
			cmp.BaselineDev, cmp.BaselineDevPct = deviation(cmp.BaselinePred, *cmp.Actual)
			if cmp.ModelDev != nil && cmp.BaselineDev != nil {
				closer := math.Abs(*cmp.ModelDev) <= math.Abs(*cmp.BaselineDev)
				cmp.ModelCloserThanBL = &closer
			}
			// informational, only where ml-engine also flags anomalies (not for ekp)
			if cmp.Anomaly != nil && cmp.IntervalLow != nil {
				out := *cmp.Actual < *cmp.IntervalLow || *cmp.Actual > *cmp.IntervalHigh
				cmp.OutsideInterval = &out
			}
		}
		resp.Comparisons = append(resp.Comparisons, *cmp)
	}
	for _, origin := range docs {
		resp.Records[origin]++
	}
	order := map[string]int{"actual_qty": 0, "ekp": 1, "but_kg": 2, "sirt_kg": 3, "omuz_kg": 4, "kaburga_kg": 5, "kiyma_kg": 6, "fire_kg": 7}
	sort.Slice(resp.Comparisons, func(i, j int) bool {
		a, b := resp.Comparisons[i], resp.Comparisons[j]
		if a.Task != b.Task {
			return a.Task > b.Task // PRODUCTION first
		}
		if a.RefNo != b.RefNo {
			return a.RefNo < b.RefNo
		}
		return order[a.Target] < order[b.Target]
	})
	writeJSON(w, resp)
}

// deviation returns actual - predicted (abs) and its share of the actual (%).
func deviation(pred *float64, actual float64) (*float64, *float64) {
	if pred == nil {
		return nil, nil
	}
	d := math.Round((actual-*pred)*10000) / 10000
	if actual == 0 {
		return &d, nil
	}
	pct := math.Round(10000*(actual-*pred)/actual) / 100
	return &d, &pct
}
