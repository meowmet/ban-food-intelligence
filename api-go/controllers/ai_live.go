package controllers

// Live production forecast (spec 4A). The ml-engine champion (ali_ridge) is linear, so its
// numbers are exported by ml-engine/export_live_model.py and evaluated here the moment an
// order's material is sent. The forecast is stored in ml_prediction next to the
// historical-mean baseline, so /ai/summary, the dashboard and the phone show it like the
// seeded ones; the actual quantity, EKP and anomaly flag are filled in when the order is
// received. Forecasts never touch stock, cost or lot status.

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

// predictionOrigin labels the forecasts this deployment writes (ml_prediction.data_origin): FIELD
// only when DATA_ORIGIN=FIELD is set for a real operation, otherwise DEMO, so a missing setting
// never claims field data. FIELD is not "verified": that needs the source document and a
// verifier outside the app (ml-engine/audit_field.py).
func predictionOrigin() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("DATA_ORIGIN")), "FIELD") {
		return "FIELD"
	}
	return "DEMO"
}

//go:embed aimodel/production_ridge.json
var productionModelJSON []byte

// linearModel is one exported sklearn Ridge pipeline: one-hot categories (an unknown category
// adds nothing, like OneHotEncoder(handle_unknown="ignore")) and median-imputed, standardised numbers.
type linearModel struct {
	Intercept   float64 `json:"intercept"`
	Categorical []struct {
		Name string             `json:"name"`
		Coef map[string]float64 `json:"coef"`
	} `json:"categorical"`
	Numeric []struct {
		Name   string  `json:"name"`
		Median float64 `json:"median"`
		Mean   float64 `json:"mean"`
		Scale  float64 `json:"scale"`
		Coef   float64 `json:"coef"`
	} `json:"numeric"`
}

// eval returns the model output; a nil, NaN or infinite number takes the training median.
func (m *linearModel) eval(cats map[string]string, nums map[string]*float64) float64 {
	y := m.Intercept
	for _, c := range m.Categorical {
		y += c.Coef[cats[c.Name]]
	}
	for _, n := range m.Numeric {
		x := n.Median
		if v := nums[n.Name]; v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) {
			x = *v
		}
		y += n.Coef * (x - n.Mean) / n.Scale
	}
	return y
}

func monthFeatures(d time.Time) (sin, cos float64) {
	month := float64(d.Month())
	return math.Sin(2 * math.Pi * month / 12), math.Cos(2 * math.Pi * month / 12)
}

type ridgeModel struct {
	linearModel
	Model     string             `json:"model"`
	Version   string             `json:"version"`
	HalfWidth map[string]float64 `json:"interval_half_width"` // yield points, "80" and "98"
	HistMean  map[string]float64 `json:"baseline_hist_mean_yield_pct"`
}

var productionModel = mustLoadRidge(productionModelJSON)

func mustLoadRidge(b []byte) *ridgeModel {
	var m ridgeModel
	if err := json.Unmarshal(b, &m); err != nil || len(m.Numeric) == 0 {
		panic(fmt.Sprintf("aimodel/production_ridge.json: %v", err))
	}
	return &m
}

// ProductionInputs are the order facts the yield model reads (ml-engine production_features
// before derivation). Nil numbers are unknown and take the training median.
type ProductionInputs struct {
	OrderDate         time.Time
	ProductSKU        string
	SubcontractorCode string
	MeatSupplierCode  string
	PackSizeKg        float64
	MeatKg            float64
	SpiceKg           float64
	CasingKg          float64
	MeatFatPct        *float64
	MeatAgeDays       *float64
	DryingDays        *float64
	PlannedQty        float64
}

// predictYield returns the expected yield (finished kg per 100 kg meat).
func (m *ridgeModel) predictYield(in ProductionInputs) float64 {
	summer := 0.0
	if mo := in.OrderDate.Month(); mo >= 6 && mo <= 8 {
		summer = 1
	}
	msin, mcos := monthFeatures(in.OrderDate)
	cats := map[string]string{
		"product_sku": in.ProductSKU, "subcontractor_code": in.SubcontractorCode, "meat_supplier_code": in.MeatSupplierCode,
	}
	nums := map[string]*float64{
		"pack_size_kg": ptr(in.PackSizeKg), "meat_kg": ptr(in.MeatKg), "spice_kg": ptr(in.SpiceKg),
		"casing_kg": ptr(in.CasingKg), "meat_fat_pct": in.MeatFatPct, "meat_age_days": in.MeatAgeDays,
		"drying_days": in.DryingDays, "planned_qty": ptr(in.PlannedQty),
		"month_sin": ptr(msin), "month_cos": ptr(mcos), "is_summer": ptr(summer), "inv_meat_kg": ptr(1 / in.MeatKg),
		"spice_ratio": ptr(in.SpiceKg / in.MeatKg), "casing_ratio": ptr(in.CasingKg / in.MeatKg),
	}
	return m.eval(cats, nums)
}

// ProductionForecastDto is the forecast of one external production order, in packages and EKP.
type ProductionForecastDto struct {
	Model            string   `json:"model"`
	ModelVersion     string   `json:"model_version"`
	YieldPct         float64  `json:"yield_pct,omitempty"`
	Packages         float64  `json:"packages"`
	PackagesLow      *float64 `json:"packages_low,omitempty"`
	PackagesHigh     *float64 `json:"packages_high,omitempty"`
	EKP              *float64 `json:"ekp,omitempty"`
	EKPLow           *float64 `json:"ekp_low,omitempty"`
	EKPHigh          *float64 `json:"ekp_high,omitempty"`
	IntervalLevel    *float64 `json:"interval_level,omitempty"`
	BaselinePackages *float64 `json:"baseline_packages,omitempty"`
	BaselineEKP      *float64 `json:"baseline_ekp,omitempty"`
	ActualPackages   *float64 `json:"actual_packages,omitempty"`
	ActualEKP        *float64 `json:"actual_ekp,omitempty"`
	Anomaly          *bool    `json:"anomaly,omitempty"`
}

type sqlRunner interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

type orderInputs struct {
	orderID      int64
	in           ProductionInputs
	serviceUnit  float64
	materialCost float64
}

func loadOrderInputs(ctx context.Context, q sqlRunner, orderNo string) (*orderInputs, error) {
	var o orderInputs
	var date string
	var pack, fat, age, drying sql.NullFloat64
	err := q.QueryRowContext(ctx,
		`SELECT order_id, DATE_FORMAT(order_date, '%Y-%m-%d'), product_sku, pack_size_kg,
		        COALESCE(subcontractor_code, ''), COALESCE(meat_supplier_code, ''),
		        meat_kg, spice_kg, casing_kg, meat_fat_pct, meat_age_days, drying_days,
		        planned_qty, service_unit_price, material_cost
		 FROM v_ml_production_order_inputs WHERE order_no = ?`, orderNo).
		Scan(&o.orderID, &date, &o.in.ProductSKU, &pack, &o.in.SubcontractorCode, &o.in.MeatSupplierCode,
			&o.in.MeatKg, &o.in.SpiceKg, &o.in.CasingKg, &fat, &age, &drying,
			&o.in.PlannedQty, &o.serviceUnit, &o.materialCost)
	if err != nil {
		return nil, err
	}
	o.in.OrderDate, _ = time.Parse("2006-01-02", date)
	o.in.PackSizeKg = pack.Float64
	nullable := func(v sql.NullFloat64) *float64 {
		if !v.Valid {
			return nil
		}
		return &v.Float64
	}
	o.in.MeatFatPct, o.in.MeatAgeDays, o.in.DryingDays = nullable(fat), nullable(age), nullable(drying)
	return &o, nil
}

func round(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
}

func ptr(v float64) *float64 { return &v }

// forecastOrder computes the forecast of an order from its sent material. Without meat (or a
// pack size) there is nothing to forecast and it returns nil.
func forecastOrder(o *orderInputs, m *ridgeModel) *ProductionForecastDto {
	if o.in.MeatKg <= 0 || o.in.PackSizeKg <= 0 {
		return nil
	}
	packages := func(yield float64) float64 { return o.in.MeatKg * yield / 100 / o.in.PackSizeKg }
	ekp := func(pk float64) float64 { return (o.materialCost + pk*o.serviceUnit) / pk }
	y := m.predictYield(o.in)
	half := m.HalfWidth["80"]
	pk, lo, hi := packages(y), packages(y-half), packages(y+half)
	// Extrapolation outside the training range can give negative yield or a zero denominator.
	// Withhold that forecast rather than storing a nonphysical quantity or nonfinite EKP.
	if math.IsNaN(y) || math.IsInf(y, 0) || round(lo, 1) <= 0 || y*o.in.MeatKg/100 > o.in.MeatKg+o.in.SpiceKg+o.in.CasingKg {
		return nil
	}
	fc := &ProductionForecastDto{
		Model: m.Model, ModelVersion: m.Version, YieldPct: round(y, 3),
		Packages: round(pk, 1), PackagesLow: ptr(round(lo, 1)), PackagesHigh: ptr(round(hi, 1)),
		EKP: ptr(round(ekp(pk), 4)), EKPLow: ptr(round(ekp(hi), 4)), EKPHigh: ptr(round(ekp(lo), 4)),
		IntervalLevel: ptr(0.8),
	}
	if base, ok := m.HistMean[o.in.ProductSKU]; ok {
		bp := packages(base)
		fc.BaselinePackages, fc.BaselineEKP = ptr(round(bp, 1)), ptr(round(ekp(bp), 4))
	}
	return fc
}

// storeForecast writes the forecast and its baseline into ml_prediction (actual still empty).
func storeForecast(ctx context.Context, q sqlRunner, orderID int64, fc *ProductionForecastDto) error {
	const ins = `INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name,
	             predicted_value, interval_low, interval_high, interval_level, data_origin)
	             VALUES (?, ?, ?, 'production_order', ?, ?, ?, ?, ?, ?, ?)`
	rows := [][]interface{}{
		{"PRODUCTION_YIELD", championModel, fc.ModelVersion, orderID, "actual_qty", fc.Packages, fc.PackagesLow, fc.PackagesHigh, fc.IntervalLevel},
		{"PRODUCTION_EKP", championModel, fc.ModelVersion, orderID, "ekp", fc.EKP, fc.EKPLow, fc.EKPHigh, fc.IntervalLevel},
	}
	if fc.BaselinePackages != nil {
		rows = append(rows,
			[]interface{}{"PRODUCTION_YIELD", baselineModel, "baseline-1", orderID, "actual_qty", fc.BaselinePackages, nil, nil, nil},
			[]interface{}{"PRODUCTION_EKP", baselineModel, "baseline-1", orderID, "ekp", fc.BaselineEKP, nil, nil, nil})
	}
	origin := predictionOrigin()
	for _, args := range rows {
		if _, err := q.ExecContext(ctx, ins, append(args, origin)...); err != nil {
			return err
		}
	}
	return nil
}

// recordForecastActuals fills in the actual packages and EKP of a received order, flags the
// package forecast as an anomaly when the actual is outside the 98 % band and raises the alert
// (alerts.go). Rows that already carry an actual (the seeded ones) are left as they are.
func recordForecastActuals(ctx context.Context, q sqlRunner, orderNo string) error {
	o, err := loadOrderInputs(ctx, q, orderNo)
	if err != nil {
		return err
	}
	var actualQty, actualEKP float64
	if err := q.QueryRowContext(ctx, `SELECT actual_qty, ekp FROM v_production_cost WHERE order_no = ?`, orderNo).
		Scan(&actualQty, &actualEKP); err != nil {
		return err
	}
	const upd = `UPDATE ml_prediction SET actual_value = ?
	             WHERE ref_type = 'production_order' AND ref_id = ? AND target_name = ? AND actual_value IS NULL`
	if _, err := q.ExecContext(ctx, upd, actualQty, o.orderID, "actual_qty"); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, upd, actualEKP, o.orderID, "ekp"); err != nil {
		return err
	}
	if o.in.PackSizeKg <= 0 {
		return nil
	}
	band := o.in.MeatKg * productionModel.HalfWidth["98"] / 100 / o.in.PackSizeKg
	if _, err = q.ExecContext(ctx,
		`UPDATE ml_prediction SET is_anomaly = (ABS(actual_value - predicted_value) > ?)
		 WHERE ref_type = 'production_order' AND ref_id = ? AND model_name = ? AND target_name = 'actual_qty'
		   AND is_anomaly IS NULL`, band, o.orderID, championModel); err != nil {
		return err
	}
	return raiseProductionAlert(ctx, q, o, band)
}

// loadForecasts returns the stored production forecasts (seeded and live) by order number.
func loadForecasts(ctx context.Context, q sqlRunner) map[string]*ProductionForecastDto {
	out := map[string]*ProductionForecastDto{}
	rows, err := q.QueryContext(ctx,
		`SELECT po.order_no, mp.model_name, mp.model_version, mp.target_name, mp.predicted_value, mp.actual_value,
		        mp.interval_low, mp.interval_high, mp.interval_level, mp.is_anomaly
		 FROM ml_prediction mp JOIN production_order po ON po.id = mp.ref_id
		 WHERE mp.ref_type = 'production_order' AND mp.model_name IN (?, ?)
		 ORDER BY mp.id`, championModel, baselineModel)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var orderNo, model, version, target string
		var pred float64
		var actual, lo, hi, level sql.NullFloat64
		var anomaly sql.NullBool
		if rows.Scan(&orderNo, &model, &version, &target, &pred, &actual, &lo, &hi, &level, &anomaly) != nil {
			continue
		}
		fc := out[orderNo]
		if fc == nil {
			fc = &ProductionForecastDto{Model: championModel}
			out[orderNo] = fc
		}
		opt := func(v sql.NullFloat64) *float64 {
			if !v.Valid {
				return nil
			}
			return ptr(v.Float64)
		}
		switch {
		case model == championModel && target == "actual_qty":
			fc.ModelVersion, fc.Packages, fc.PackagesLow, fc.PackagesHigh, fc.IntervalLevel = version, pred, opt(lo), opt(hi), opt(level)
			fc.ActualPackages = opt(actual)
			if anomaly.Valid {
				fc.Anomaly = &anomaly.Bool
			}
		case model == championModel && target == "ekp":
			fc.EKP, fc.EKPLow, fc.EKPHigh, fc.ActualEKP = ptr(pred), opt(lo), opt(hi), opt(actual)
		case target == "actual_qty":
			fc.BaselinePackages = ptr(pred)
		case target == "ekp":
			fc.BaselineEKP = ptr(pred)
		}
	}
	return out
}
