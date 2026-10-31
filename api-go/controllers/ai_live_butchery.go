package controllers

// Live butchery forecast (spec 4B): the expected weight of each part of a carcass from its species,
// breed, weight and history, evaluated in Go from the exported ml-engine champion (one Ridge per
// share, clipped at 0.1 % and rescaled to 100 %, so parts + waste = input weight). The dashboard
// shows it while a butchery is entered; a posted operation stores forecast and recorded weights.
// Frozen before the parts are weighed (POST /ai/forecast/butchery/freeze), the forecast is kept
// unchanged in butchery_forecast and the posted operation is compared with it.

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed aimodel/butchery_ridge.json
var butcheryModelJSON []byte

type butcheryModel struct {
	Model     string             `json:"model"`
	Version   string             `json:"version"`
	Targets   []string           `json:"targets"` // but_pct ... fire_pct
	RefWeight map[string]float64 `json:"species_ref_weight_kg"`
	Floor     float64            `json:"share_floor_pct"`
	Parts     []struct {
		Target string `json:"target"`
		linearModel
	} `json:"parts"`
	HalfWidth map[string][]float64 `json:"interval_half_width_pct"` // share points per part, "80" and "98"
	HistMean  map[string][]float64 `json:"baseline_hist_mean_pct"`  // share per part, by species
}

var butcheryForecastModel = mustLoadButchery(butcheryModelJSON)

func mustLoadButchery(b []byte) *butcheryModel {
	var m butcheryModel
	if err := json.Unmarshal(b, &m); err != nil || len(m.Parts) != len(m.Targets) || len(m.Parts) == 0 {
		panic(fmt.Sprintf("aimodel/butchery_ridge.json: %v", err))
	}
	return &m
}

// cutSKU maps a forecast part to the product recorded for it, per species (the demo has lamb).
var cutSKU = map[string]map[string]string{
	"KUZU": {"but": "KZ-BUT", "sirt": "KZ-SIRT", "omuz": "KZ-OMUZ", "kaburga": "KZ-KABURGA", "kiyma": "KZ-KIYMA", "fire": "KZ-FIRE"},
}

// ButcheryInputs are the carcass facts the model reads; nil numbers take the training median.
type ButcheryInputs struct {
	OpDate            time.Time
	Species           string
	Breed             string
	SupplierCode      string
	OperatorCode      string
	Sex               string
	CarcassWeightKg   float64
	AgeMonths         *float64
	FatScore          *float64
	ConformationScore *float64
	ChillHours        *float64
}

// predictShares returns the six shares (%), summing to 100.
func (m *butcheryModel) predictShares(in ButcheryInputs) []float64 {
	msin, mcos := monthFeatures(in.OpDate)
	vsSpecies := 0.0
	if ref, ok := m.RefWeight[in.Species]; ok {
		vsSpecies = in.CarcassWeightKg - ref
	}
	warm := 0.0 // unknown chill hours count as not warm-cut, as in pandas (NaN < 24 is false)
	if in.ChillHours != nil && *in.ChillHours < 24 {
		warm = 1
	}
	cats := map[string]string{"species": in.Species, "breed": in.Breed, "supplier_code": in.SupplierCode,
		"operator_code": in.OperatorCode, "sex": in.Sex}
	nums := map[string]*float64{"carcass_weight_kg": ptr(in.CarcassWeightKg), "age_months": in.AgeMonths,
		"fat_score": in.FatScore, "conformation_score": in.ConformationScore, "chill_hours": in.ChillHours,
		"month_sin": ptr(msin), "month_cos": ptr(mcos), "weight_vs_species": ptr(vsSpecies), "warm_cut": ptr(warm)}
	shares := make([]float64, len(m.Parts))
	sum := 0.0
	for j, p := range m.Parts {
		shares[j] = math.Max(p.eval(cats, nums), m.Floor)
		sum += shares[j]
	}
	for j := range shares {
		shares[j] = 100 * shares[j] / sum
	}
	return shares
}

type ButcheryPartForecastDto struct {
	Part       string   `json:"part"` // but, sirt, omuz, kaburga, kiyma, fire
	SKU        string   `json:"sku,omitempty"`
	SharePct   float64  `json:"share_pct"`
	Kg         float64  `json:"kg"`
	KgLow      float64  `json:"kg_low"`
	KgHigh     float64  `json:"kg_high"`
	BaselineKg *float64 `json:"baseline_kg,omitempty"`
	ActualKg   *float64 `json:"actual_kg,omitempty"`
	Anomaly    *bool    `json:"anomaly,omitempty"` // actual outside the 98 % band
	band98     float64
}

type ButcheryForecastDto struct {
	Model         string                    `json:"model"`
	ModelVersion  string                    `json:"model_version"`
	Species       string                    `json:"species"`
	WeightKg      float64                   `json:"weight_kg"`
	IntervalLevel float64                   `json:"interval_level"`
	Parts         []ButcheryPartForecastDto `json:"parts"`
}

func forecastButchery(in ButcheryInputs, m *butcheryModel) *ButcheryForecastDto {
	if in.CarcassWeightKg <= 0 {
		return nil
	}
	w := in.CarcassWeightKg
	shares := m.predictShares(in)
	fc := &ButcheryForecastDto{Model: m.Model, ModelVersion: m.Version, Species: in.Species, WeightKg: w, IntervalLevel: 0.8}
	for j, t := range m.Targets {
		part := strings.TrimSuffix(t, "_pct")
		kg, half := shares[j]*w/100, m.HalfWidth["80"][j]*w/100
		p := ButcheryPartForecastDto{Part: part, SKU: cutSKU[in.Species][part], SharePct: round(shares[j], 3),
			Kg: round(kg, 3), KgLow: round(kg-half, 3), KgHigh: round(kg+half, 3), band98: m.HalfWidth["98"][j] * w / 100}
		if base, ok := m.HistMean[in.Species]; ok {
			p.BaselineKg = ptr(round(base[j]*w/100, 3))
		}
		fc.Parts = append(fc.Parts, p)
	}
	return fc
}

// applyButcheryActuals sets the recorded weight of each part (cuts by product, waste by kind; a
// by-product is not one of the six parts) and flags parts outside the 98 % band.
func applyButcheryActuals(fc *ButcheryForecastDto, outs []ButcheryOutputRequest) {
	if fc == nil {
		return
	}
	for i := range fc.Parts {
		p := &fc.Parts[i]
		actual, known := 0.0, p.Part == "fire" || p.SKU != ""
		for _, o := range outs {
			if (p.Part == "fire" && o.Kind == "WASTE") || (p.Part != "fire" && p.SKU != "" && o.SKU == p.SKU) {
				actual += o.WeightKg
			}
		}
		if !known {
			continue
		}
		a := round(actual, 3)
		out := math.Abs(a-p.Kg) > p.band98
		p.ActualKg, p.Anomaly = &a, &out
	}
}

func storeButcheryForecast(ctx context.Context, q sqlRunner, opID int64, fc *ButcheryForecastDto) error {
	const ins = `INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name,
	             predicted_value, actual_value, interval_low, interval_high, interval_level, is_anomaly, data_origin)
	             VALUES ('BUTCHERY_YIELD', ?, ?, 'butchery_operation', ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	origin := predictionOrigin()
	for _, p := range fc.Parts {
		target := p.Part + "_kg"
		if _, err := q.ExecContext(ctx, ins, championModel, fc.ModelVersion, opID, target, p.Kg, p.ActualKg,
			p.KgLow, p.KgHigh, fc.IntervalLevel, p.Anomaly, origin); err != nil {
			return err
		}
		if p.BaselineKg != nil {
			if _, err := q.ExecContext(ctx, ins, baselineModel, "baseline-1", opID, target, *p.BaselineKg, p.ActualKg,
				nil, nil, nil, nil, origin); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadCarcass reads the carcass facts of a lot; productType tells whether it is a carcass at all.
func loadCarcass(ctx context.Context, q sqlRunner, lotNo string) (in ButcheryInputs, productType string, err error) {
	var age, fat, conf sql.NullFloat64
	err = q.QueryRowContext(ctx,
		`SELECT p.product_type, COALESCE(p.species, ''), COALESCE(l.animal_breed, ''), COALESCE(s.code, ''),
		        l.animal_age_months, COALESCE(l.animal_sex, ''), l.fat_score, l.conformation_score
		 FROM lot l JOIN product p ON p.id = l.product_id LEFT JOIN supplier s ON s.id = l.supplier_id
		 WHERE l.lot_no = ?`, lotNo).
		Scan(&productType, &in.Species, &in.Breed, &in.SupplierCode, &age, &in.Sex, &fat, &conf)
	opt := func(v sql.NullFloat64) *float64 {
		if !v.Valid {
			return nil
		}
		return ptr(v.Float64)
	}
	in.AgeMonths, in.FatScore, in.ConformationScore = opt(age), opt(fat), opt(conf)
	return in, productType, err
}

// FrozenButcheryDto is a forecast stored before the parts of its carcass were weighed. It never
// changes; the operation that cuts the carcass links to it once (op_no).
type FrozenButcheryDto struct {
	FrozenAt      time.Time            `json:"frozen_at"`
	FrozenBy      string               `json:"frozen_by"`
	InputWeightKg float64              `json:"input_weight_kg"`
	OpDate        string               `json:"op_date"`
	OperatorName  string               `json:"operator_name,omitempty"`
	OpNo          string               `json:"op_no,omitempty"`
	InputMatches  *bool                `json:"input_matches,omitempty"` // in a butchery result: frozen for the operation's input kg
	Forecast      *ButcheryForecastDto `json:"forecast"`
	id            int64
}

// ButcheryForecastResponse is the live forecast and, if the carcass has one, its frozen forecast.
type ButcheryForecastResponse struct {
	*ButcheryForecastDto
	Frozen *FrozenButcheryDto `json:"frozen,omitempty"`
}

// loadFrozenButchery returns the frozen forecast of a carcass lot, or nil if it has none.
func loadFrozenButchery(ctx context.Context, q sqlRunner, lotNo, species string) (*FrozenButcheryDto, error) {
	f := &FrozenButcheryDto{}
	var opDate time.Time
	var operator, opNo sql.NullString
	err := q.QueryRowContext(ctx,
		`SELECT bf.id, bf.frozen_at, bf.frozen_by, bf.input_weight_kg, bf.op_date, bf.operator_name, bo.op_no
		 FROM butchery_forecast bf JOIN lot l ON l.id = bf.input_lot_id
		 LEFT JOIN butchery_operation bo ON bo.id = bf.operation_id
		 WHERE l.lot_no = ?`, lotNo).
		Scan(&f.id, &f.FrozenAt, &f.FrozenBy, &f.InputWeightKg, &opDate, &operator, &opNo)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f.OpDate, f.OperatorName, f.OpNo = opDate.Format("2006-01-02"), operator.String, opNo.String
	fc := &ButcheryForecastDto{Model: championModel, Species: species, WeightKg: f.InputWeightKg}
	index := map[string]int{}
	for _, t := range butcheryForecastModel.Targets {
		part := strings.TrimSuffix(t, "_pct")
		index[part+"_kg"] = len(fc.Parts)
		fc.Parts = append(fc.Parts, ButcheryPartForecastDto{Part: part, SKU: cutSKU[species][part]})
	}
	rows, err := q.QueryContext(ctx,
		`SELECT model_name, model_version, target_name, predicted_value, interval_low, interval_high, interval_level, anomaly_band_kg
		 FROM butchery_forecast_part WHERE forecast_id = ?`, f.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var model, version, target string
		var pred float64
		var lo, hi, level, band sql.NullFloat64
		if err := rows.Scan(&model, &version, &target, &pred, &lo, &hi, &level, &band); err != nil {
			return nil, err
		}
		i, ok := index[target]
		if !ok {
			continue
		}
		p := &fc.Parts[i]
		switch model {
		case championModel:
			fc.ModelVersion, fc.IntervalLevel = version, level.Float64
			p.Kg, p.KgLow, p.KgHigh, p.band98 = pred, lo.Float64, hi.Float64, band.Float64
			p.SharePct = round(100*pred/f.InputWeightKg, 3)
		case baselineModel:
			p.BaselineKg = ptr(pred)
		}
	}
	f.Forecast = fc
	return f, rows.Err()
}

// ButcheryFreezeRequest is the carcass as it goes onto the cutting table.
type ButcheryFreezeRequest struct {
	InputLotNo    string  `json:"input_lot_no"`
	InputWeightKg float64 `json:"input_weight_kg"`
	OpDate        string  `json:"op_date"`
	OperatorName  string  `json:"operator_name"`
}

func validateForecastInput(lotNo string, weight float64, weightErr error, date string) fieldErrors {
	fe := fieldErrors{}
	if lotNo == "" {
		fe["input_lot_no"] = "required"
	}
	if weightErr != nil || math.IsNaN(weight) || math.IsInf(weight, 0) || weight <= 0 || weight > 500 {
		fe["input_weight_kg"] = "0 < kg <= 500"
	}
	if !validDate(date) {
		fe["op_date"] = "YYYY-MM-DD"
	}
	return fe
}

// carcassForecast loads the carcass, checks it is one and forecasts it; false: the error is written.
func carcassForecast(w http.ResponseWriter, r *http.Request, q sqlRunner, lotNo string, weight float64, date, operator string) (*ButcheryForecastDto, ButcheryInputs, bool) {
	in, ptype, err := loadCarcass(r.Context(), q, lotNo)
	if err == sql.ErrNoRows {
		writeAPIError(w, r, http.StatusNotFound, "LOT_NOT_FOUND", "lot "+lotNo)
		return nil, in, false
	}
	if err != nil {
		writeDBError(w, r, err)
		return nil, in, false
	}
	if ptype != "CARCASS" {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{"input_lot_no": "must be a carcass lot"})
		return nil, in, false
	}
	in.CarcassWeightKg, in.OperatorCode = weight, operator
	in.OpDate, _ = time.Parse("2006-01-02", date)
	return forecastButchery(in, butcheryForecastModel), in, true
}

// ButcheryForecast handles GET /ai/forecast/butchery?input_lot_no=&input_weight_kg=[&op_date=&operator_name=]
func (c *AIController) ButcheryForecast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return
	}
	q := r.URL.Query()
	weight, err := strconv.ParseFloat(strings.Replace(q.Get("input_weight_kg"), ",", ".", 1), 64)
	date := nonEmpty(q.Get("op_date"), time.Now().Format("2006-01-02"))
	if fe := validateForecastInput(q.Get("input_lot_no"), weight, err, date); len(fe) > 0 {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fe)
		return
	}
	fc, in, ok := carcassForecast(w, r, c.db, q.Get("input_lot_no"), weight, date, q.Get("operator_name"))
	if !ok {
		return
	}
	frozen, err := loadFrozenButchery(r.Context(), c.db, q.Get("input_lot_no"), in.Species)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	writeJSON(w, ButcheryForecastResponse{fc, frozen})
}

// FreezeButcheryForecast handles POST /ai/forecast/butchery/freeze: stores the forecast of a
// carcass before its parts are weighed, so it can later count as a forecast made before the
// outcome. One per carcass, never changed: 409 FORECAST_FROZEN if the carcass already has one,
// 409 FORECAST_TOO_LATE once it is cut.
func (c *AIController) FreezeButcheryForecast(w http.ResponseWriter, r *http.Request) {
	var req ButcheryFreezeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.InputLotNo = strings.TrimSpace(req.InputLotNo)
	req.OpDate = nonEmpty(req.OpDate, time.Now().Format("2006-01-02"))
	if fe := validateForecastInput(req.InputLotNo, req.InputWeightKg, nil, req.OpDate); len(fe) > 0 {
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
	// Lock the carcass first, like POST /butchery/operations: a freeze and a posting of the same
	// carcass run one after the other, and the trigger then sees a posting that came first.
	var lotID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM lot WHERE lot_no = ? FOR UPDATE`, req.InputLotNo).Scan(&lotID); err == sql.ErrNoRows {
		writeAPIError(w, r, http.StatusNotFound, "LOT_NOT_FOUND", "lot "+req.InputLotNo)
		return
	} else if err != nil {
		writeDBError(w, r, err)
		return
	}
	fc, in, ok := carcassForecast(w, r, tx, req.InputLotNo, req.InputWeightKg, req.OpDate, strings.TrimSpace(req.OperatorName))
	if !ok {
		return
	}
	if existing, err := loadFrozenButchery(ctx, tx, req.InputLotNo, in.Species); err != nil {
		writeDBError(w, r, err)
		return
	} else if existing != nil {
		writeAPIError(w, r, http.StatusConflict, "FORECAST_FROZEN", existing)
		return
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO butchery_forecast (input_lot_id, input_weight_kg, op_date, operator_name, frozen_by, data_origin)
		 VALUES (?, ?, ?, NULLIF(?, ''), ?, ?)`,
		lotID, req.InputWeightKg, req.OpDate, in.OperatorCode, nonEmpty(OperatorFrom(ctx), "api"), predictionOrigin())
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	id, _ := res.LastInsertId()
	const ins = `INSERT INTO butchery_forecast_part (forecast_id, model_name, model_version, target_name,
	             predicted_value, interval_low, interval_high, interval_level, anomaly_band_kg)
	             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, p := range fc.Parts {
		if _, err := tx.ExecContext(ctx, ins, id, championModel, fc.ModelVersion, p.Part+"_kg", p.Kg, p.KgLow, p.KgHigh,
			fc.IntervalLevel, round(p.band98, 4)); err != nil {
			writeDBError(w, r, err)
			return
		}
		if p.BaselineKg != nil {
			if _, err := tx.ExecContext(ctx, ins, id, baselineModel, "baseline-1", p.Part+"_kg", *p.BaselineKg,
				nil, nil, nil, nil); err != nil {
				writeDBError(w, r, err)
				return
			}
		}
	}
	frozen, err := loadFrozenButchery(ctx, tx, req.InputLotNo, in.Species)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeDBError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, frozen)
}
