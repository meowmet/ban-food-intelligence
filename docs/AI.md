# AI studies (spec section 4)

Two mandatory models, each compared with simple reference methods on data the model has not
seen, with confidence intervals, significance tests, calibrated prediction intervals and
plain-language explanations. Lot traceability and recall **never** use these predictions; they
come only from recorded lot relations (`lot_link`, `shipment_line`, `sale_line`) in MySQL.

| Study | Predicts | Champion | Main reference | Result (rolling-origin, 748 / 1,250 unseen rows) |
|---|---|---|---|---|
| A, production | yield % of an external sucuk order -> packages and unit cost (EKP) | Ridge regression | historical mean yield of the product | packages MAE **1.31** [1.20, 1.42] vs 2.55 [2.35, 2.75]; EKP MAE **0.128 EUR** vs 0.245 EUR |
| B, butchery | share of each part (leg, loin, shoulder, ribs, mince) and waste -> kg | Ridge per share, projected so parts + waste = input | historical mean shares of the species | part kg MAE **0.118** [0.116, 0.122] vs 0.184 [0.180, 0.189] |

Brackets are 95 % bootstrap confidence intervals. Both differences are significant
(Diebold-Mariano p < 1e-50). The full report, including what was tried and dropped, is in
[`ml-engine/ML_REPORT.md`](../ml-engine/ML_REPORT.md).

## Data (synthetic)

- No real history exists for the competition. `ml-engine/generator_ali.py` creates a
  **synthetic, anonymised** history (Jan 2024 to Sep 2026; 1,500 production orders, 2,500
  butchery operations) from a documented process model anchored on the spec numbers
  (100 kg meat + 2 kg spice + 5 kg casing -> 85 packages; lamb 25 kg -> 22 kg parts + 3 kg waste).
  It is seeded (`--seed 42`), so the files are reproducible. About 2 % of production orders carry
  an injected machine fault (yield 4-10 points lower); these are not labelled in the data.
- The generator was **not** tuned to make the model look better. Every number in this document
  comes from that unchanged data.
- The same columns are exposed from the real database by the views `v_ml_production_features`
  and `v_ml_butchery_features`, so the models can be retrained on real records without code changes.

## Features

| Study | Inputs | Deliberately left out |
|---|---|---|
| A | product, subcontractor, meat supplier, meat / spice / casing kg, meat fat %, meat age, drying days, planned packages, month (sin/cos), summer flag, 1 / meat kg (fixed filler loss), spice and casing ratios | all prices: they rise with inflation and act as a hidden clock; the cost formula uses them exactly instead |
| B | species, breed, supplier, operator, sex, carcass kg, age, fat and conformation score, chill hours, month (sin/cos), weight vs species norm, warm-cut flag | purchase price (same reason) |

No feature contains the target or anything known only after production.

## Models: what AI does and what stays exact

- **Production:** the model predicts the **yield ratio** (finished kg / meat kg). Packages and
  EKP are then calculated with the exact cost formula
  (EKP = (material cost + packages x service price) / packages). AI estimates yield; costing stays
  arithmetic.
- **Butchery:** six shares are predicted and projected onto the simplex, so the predicted parts
  plus waste always add up to the input weight (see *Physical consistency*).

Candidates compared under the same protocol. The champion is the simplest model that is not
significantly worse than the best on the selection folds:

| Production candidate | Packages MAE | | Butchery candidate | Part kg MAE (6 parts) |
|---|---|---|---|---|
| recipe 85 % x meat kg | 3.21 | | species recipe shares | 0.186 |
| historical mean per product | 2.55 | | historical mean per species | 0.184 |
| physics: a - b / meat kg per product | 2.54 | | **Ridge per share + projection** | **0.118** |
| **Ridge** | **1.31** | | Ridge on log-ratios (ALR) | 0.119 |
| ElasticNet | 1.30 | | LightGBM on log-ratios | 0.124 |
| Huber | 1.30 | | CatBoost multi-output + projection | 0.120 |
| LightGBM (tuned, nested) | 1.40 | | | |
| CatBoost | 1.35 | | | |

ElasticNet and Huber are 0.01 packages better than Ridge. On the selection folds that
difference has a 95 % CI that contains zero, so Ridge stays champion. The tree models are worse:
the yield process is close to additive, and the trees have less data per split.

## Validation

- **Rolling origin (expanding window), 5 folds.** Rows are sorted by date; each fold trains on
  everything before a block and predicts the block (about 150 orders / 250 carcasses each,
  May 2025 to Sep 2026). Block edges snap to date boundaries, so no day is on both sides.
- **Selection vs confirmation.** Folds 1-3 lie inside the original training period and are the
  only folds used to choose the champion. Folds 4-5 are the original 20 % time holdout and confirm it.
- **Nested tuning.** Hyperparameters (Ridge alpha, ElasticNet, Huber, LightGBM grid) are chosen
  on the last 20 % of each fold's own training window, never on the scored block.
- **Uncertainty of the metric.** Mean +/- std across folds, and a moving-block bootstrap 95 % CI
  (block 10, 2,000 resamples) on the pooled out-of-sample rows.
- **Is the difference real?** Diebold-Mariano test (Newey-West variance) and a paired bootstrap CI
  of the MAE difference against each baseline.
- A random split was rejected: price columns, and any real drift, would leak the future.

## Results

Rolling-origin, pooled over 5 folds (748 production orders, 1,250 carcasses):

| Study | Target | Model | Hist. mean baseline | Recipe baseline | Model - hist. mean (95 % CI) | DM p |
|---|---|---|---|---|---|---|
| A | packages MAE | **1.31** [1.20, 1.42] | 2.55 [2.35, 2.75] | 3.21 [2.98, 3.43] | -1.24 [-1.40, -1.08] | 8e-53 |
| A | packages MAPE / wMAPE | 1.15 % / 1.09 % | 2.19 % / 2.13 % | 2.79 % / 2.68 % | | |
| A | EKP MAE | **0.128 EUR** (0.79 %) | 0.245 EUR (1.50 %) | 0.300 EUR (1.85 %) | | |
| A | fold mean +/- std (packages) | 1.31 +/- 0.06 | 2.55 +/- 0.22 | 3.21 +/- 0.51 | | |
| B | part kg MAE (6 parts) | **0.118** [0.116, 0.122] | 0.184 [0.180, 0.189] | 0.186 [0.181, 0.191] | -0.066 [-0.070, -0.062] | 4e-253 |
| B | sellable part kg MAPE / wMAPE | 3.27 % / 3.07 % | 4.67 % / 4.45 % | 4.68 % / 4.45 % | | |
| B | waste kg MAE | **0.119** (4.6 %) | 0.248 (9.6 %) | 0.258 (10.0 %) | | |
| B | fold mean +/- std (kg) | 0.118 +/- 0.006 | 0.184 +/- 0.005 | 0.186 +/- 0.004 | | |

wMAPE (total error / total actual) is reported because MAPE is unstable on small values.

**Business impact.** Production: the EUR value of the cost error (|EKP error| x packages made)
drops from **26.6 EUR** to **13.7 EUR** per order [12.6, 14.7]. Butchery: kg booked to the wrong
part falls from 1.11 kg to **0.71 kg** per carcass.

The original single time split (latest 20 %, the numbers served by `GET /ai/summary`) agrees:
packages MAE 1.37 vs 2.69, EKP 0.138 vs 0.259 EUR, part kg 0.115 vs 0.184.

## Prediction intervals and coverage

Each prediction carries an **80 % conformal interval**. Calibration is split conformal on a
time-ordered window: the model is fit on the first 75 % of the training period, and its absolute
errors on the last 25 % set the half-width (finite-sample quantile). The model never sees the
calibration rows, so the width reflects *future* error, not training error.

| Study | Nominal | Empirical coverage (rolling-origin) | Per fold | Mean half-width |
|---|---|---|---|---|
| A, yield | 80 % | **79.3 %** | 70-84 % | +/- 1.34 yield points (about +/- 1.5 packages per 100 kg) |
| A, yield | 90 % | 90.6 % | | +/- 1.84 points |
| B, each part | 80 % | **80.4 %** (every part 79-82 %) | | +/- 0.19 kg |
| B, each part | 90 % | 90.2 % | | +/- 0.25 kg |

Intervals in study B are per part. All six parts land inside their intervals at once for only
29 % of carcasses, as expected for six 80 % intervals. Conformalised quantile regression (CQR,
LightGBM quantiles) was tried for A: coverage was similar (81.7 %) but intervals were 25 % wider,
so it was dropped.

## Anomaly flag

`anomaly: true` means the actual value is outside the **98 %** conformal band, not the 80 %
interval. Flagging on the 80 % interval would mark one normal order in five. Measured against the
14 injected machine faults in the rolling-origin rows (labels from the seeded generator, not
seen by the model):

| Rule | Orders flagged | Faults caught | Precision | False alarms per 100 normal orders |
|---|---|---|---|---|
| outside 80 % interval | 20.7 % | 14 / 14 | 9 % | 19.2 |
| **outside 98 % band (`anomaly`)** | 1.6 % | 11 / 14 | **92 %** | **0.14** |

The calibrated alert detector in `anomaly_ali.py` (robust z-score and Mahalanobis distance,
reported separately in `artifacts/ali/anomaly_metrics.json`) writes `ml_anomaly_alert` rows in
batch. A flag never changes stock, cost or lot status; a person checks it.

### Live alerts and their review

The API also writes `ml_anomaly_alert` the moment a document is saved
([`alerts.go`](../api-go/controllers/alerts.go)), so a new deviation reaches the dashboard's
**Alerts** tab and the sidebar bell without a batch run. The thresholds are configuration
(`alert_threshold`, `GET/POST /alerts/rules`); the defaults below come from the stress test in the
next section.

| Check | When | Score; default warning / critical | Estimated impact |
|---|---|---|---|
| Low or high yield (A) | order received | \|actual − forecast\| / 98 % band; above 1 / from 1.5 | missing packs × forecast EKP |
| Unexpected waste or part deviation (B) | butchery posted | largest \|actual − forecast\| / 98 % band of the six parts; above 1.5 / from 2. Less waste than expected does not count (no loss; by-products are recorded apart) | extra waste kg × carcass price |
| Purchase price increase | receipt saved | % above the mean of the product's last three purchase lines; above 10 / from 25 | price difference × quantity |
| Recalled, blocked or expired lot sold at the POS | sync refuses the sale | rule: always critical, one alert per sale (UUIDv7), also when the phone retries | – |
| Shortage in a stock count | count saved | shortage value at lot cost; above 10 € / from 100 €; always critical for a recalled or blocked lot | shortage × lot cost |

A supervisor marks an alert **under review**, then closes it as a **real problem** or a **false
alarm** (note required, `audit_log` entry). A problem found by hand that no alert flagged is
recorded as a **missed problem** (`POST /alerts/missed`) against the check that should have caught
it. Per check the dashboard shows precision = real / (real + false alarms) and recall = real /
(real + missed), each with its 95 % Wilson interval: 8 real problems out of 10 closed alerts is
49–94 %, which is why a handful of reviews is reported as a count, not as an accuracy.

### Calibrating the live alerts without field data

No business data set was provided, so the field accuracy of a threshold cannot be measured yet.
What can be measured, and is ([`calibrate_live_alerts.py`](../ml-engine/calibrate_live_alerts.py),
`artifacts/ali/live_alert_calibration.json`), is how each rule behaves **if the data look like the
synthetic generator's assumptions**. The yield rules use the same data, champion predictions and
fault injection as `anomaly_ali.py` (6 % of records, 30 rounds, time-split test period only; the
band comes from the training residuals) and the score the API computes. Every threshold is
reported, so a business can choose by its own alarm budget. The defaults follow one: **at most one
false alarm per 100 records, then the highest recall**.

Production yield (A), 302 test orders, injected drops of 1.5–6 yield points plus natural faults:

| Warning at × 98 % band | Precision | Recall | False alarms / 100 orders |
|---|---|---|---|
| 0.75 | 0.87 | 0.79 | 0.97 |
| **1.0 (default)** | **1.00** | **0.64** | **0.00** |
| 1.25 | 1.00 | 0.44 | 0.00 |
| 1.5 (default critical) | 1.00 | 0.32 | 0.00 |
| 2.0 | 1.00 | 0.06 | 0.00 |

At 1.0 the rule catches 4 % of 1.5–3 point drops, 42 % of 3–4.5 and 88 % above 4.5. A critical
threshold of 2 bands (the first choice) would have marked only 6 % of faults critical; 1.5 marks the
large ones. 0.75 stays inside the budget too and catches more; 1.0 was kept because field noise is
likely larger than the generator's and the alert then matches the `anomaly` flag exactly.

Butchery (B), 500 test carcasses, 1–4 % of the carcass moved to waste or to another part:

| Warning at × 98 % band | Precision | Recall | False alarms / 100 carcasses |
|---|---|---|---|
| 1.0 | 0.36 | 0.86 | **9.24** |
| 1.25 | 0.77 | 0.76 | 1.33 |
| **1.5 (default)** | **0.91** | **0.64** | **0.39** |
| 2.0 (default critical) | 1.00 | 0.39 | 0.00 |

The first default (1.0) failed this test: each part alone leaves its 98 % band on 2 % of normal
carcasses, but the alert looks at six parts, so one of them does on roughly 1 − 0.98⁶ ≈ 11 %
(observed 9 per 100). This multiple-comparison effect is why the default is now 1.5, which is in
line with the batch Mahalanobis detector (precision 0.95, recall 0.66). At 1.5 the rule catches 64 %
of 1–2 % shifts and all shifts above 3 %. The per-part `anomaly` flag in `/ai/summary` stays at the
98 % band: it is a statement about one part, not an alert.

Purchase price: a price series from the generator cannot judge a threshold, because its noise
(1.5 % per purchase) is our own assumption. The test therefore reports the false alarms per 100
clean purchases **as a function of the business's price volatility** (weekly purchases over two
years, trend, season, three suppliers), and the share of single price jumps caught:

| Price noise per purchase | 10 % (default) | 15 % | 20 % | 10–20 % jumps caught at 10 % |
|---|---|---|---|---|
| 1.5 % | 0.01 | 0.00 | 0.00 | 91 % |
| 3 % | 0.53 | 0.02 | 0.00 | 87 % |
| 5 % | 5.21 | 0.76 | 0.11 | 84 % |
| 8 % | 15.1 | 6.42 | 2.28 | 65 % |

10 % suits a business whose prices move by about 3 % or less between purchases; at 5 % it should be
15 %, at 8 % around 25 %. The **Alerts → thresholds** section shows the volatility actually observed
in the recorded purchases (95th percentile of the increases, share above the threshold), so this
choice can be made from the business's own invoices as soon as there are some. Stock-count
shortages and refused POS sales are measured facts, not predictions: their thresholds are a
materiality decision and have no precision to test.

What this does **not** show: how often the rules are right on real operations. That needs the
reviews and missed problems above, collected over a pilot, and is the next step once real records
exist. Public price series (carcass and meat market prices) could replace the assumed price noise
before that; this was not done.

## Physical consistency (butchery)

Predicted parts + waste must equal the input weight. Six independent regressors do not
guarantee this. With per-share tuning, **2.0 %** of carcasses missed the balance by more than
50 g (worst 0.19 kg). After projection onto the simplex (clip at 0.1 %, renormalise to 100 %)
the violation rate is **0 %**, with a maximum gap below 1e-12 kg, and no part is negative. An
additive log-ratio model (softmax back-transform) satisfies the constraint by construction and
gave the same accuracy (0.119 vs 0.118 kg). The simpler projection was kept; both are tested.

## Explainability

Permutation importance on the time holdout: one raw input is shuffled at a time and the error
increase is measured (derived features follow their source). Each sentence is a what-if on the
trained model (input moved from its 25th to 75th percentile, or across categories, with all
other inputs unchanged).

**A, production (yield)**

1. Meat fat %: higher fat % (20.1 -> 23.9) -> higher yield (+1.5 points); lean meat loses more water.
2. Subcontractor: FAS-03 gives the highest predicted yield, FAS-02 the lowest (2.5 points apart).
3. Drying days: one extra day (5 -> 6) -> lower yield (-1.2 points).
4. Season: predicted yield is lowest in August and highest in February (1.1 points apart).
5. Meat supplier: TED-ET-03 highest, TED-ET-02 lowest (1.3 points apart).

**B, butchery (part kg)**

1. Species: sheep (KOYUN) gives the highest mince share, kid (OGLAK) the lowest (3.5 points apart).
2. Fat score: fatter carcass (2 -> 4) -> more mince (+1.6 share points).
3. Carcass weight: heavier carcass (17.6 -> 25.0 kg) -> less waste (-1.2 share points).
4. Breed: kil goat (KIL_KECISI) has the highest waste share, Kivircik the lowest (1.4 points apart).
5. Conformation score: better conformation (3 -> 4) -> more leg (+0.6 share points).

These match the process model the generator was written from. That is evidence the model learns
the process rather than noise, but on synthetic data it is also expected; real data must
re-confirm it.

![Production yield: prediction vs actual with 80 % intervals](ai/production_pred_vs_actual.png)

## Learning curve: how much real data is needed?

Trained on the most recent *n* rows before the holdout, scored on the 20 % holdout:

![Learning curve](ai/learning_curve.png)

| Rows | 25 | 50 | 100 | 200 | 400 | 800 | 1,198 / 2,000 |
|---|---|---|---|---|---|---|---|
| A packages MAE (model / hist. mean) | 4.45 / 2.95 | 5.02 / 2.67 | 5.99 / 2.69 | 3.21 / 2.69 | **1.38** / 2.72 | 1.36 / 2.70 | 1.37 / 2.69 |
| B part kg MAE (model / hist. mean) | 0.178 / 0.196 | 0.195 / 0.189 | 0.176 / 0.186 | 0.159 / 0.185 | **0.126** / 0.184 | 0.116 / 0.184 (1,000) | 0.116 / 0.184 |

- **Production needs about 400 orders** (roughly 9 months at the synthetic volume) before the
  model beats the historical mean. With fewer rows it is clearly *worse*: a few weeks of
  history cannot separate season from subcontractor, and the model extrapolates. Until then the
  historical mean should be served.
- **Butchery needs 100-200 carcasses** to beat the mean and about 1,000 to plateau.
- Beyond those sizes, more data of the same kind adds little. Improvement would need new
  information (e.g. moisture measurements, machine logs).

## Why not stacking?

`nuri_stack_meta` (a level-2 learner over 4-5 base models) was audited on the same holdout rows.
In production it is not distinguishable from its best single base model, Huber
(difference +0.0003 yield points, 95 % CI [-0.014, 0.014], p = 0.97). In butchery it is slightly
*worse* than its best base model, simplex Ridge (p = 0.02). Its small edge over `ali_ridge`
(0.02 yield points, about 0.03 packages per order) comes from Huber's robust loss, not from
stacking, and the rolling-origin comparison above found Huber's edge over Ridge not significant.
Same accuracy with one model instead of six: the single Ridge is served.

## Live forecasts for new orders (study A)

The champion is linear (one-hot categories + median-imputed, standardised numbers -> Ridge), so it
runs inside the Go API without Python:

1. `ml-engine/export_live_model.py` fits the champion exactly as `train_model_ali.py` does (same
   data, time split and rows), writes its intercept, per-category and per-feature coefficients,
   imputation medians, scaling, the 80 % / 98 % conformal half-widths and the historical-mean
   baseline per product to `api-go/controllers/aimodel/production_ridge.json`, and refuses to write
   if the linear form differs from sklearn's own predictions (largest gap 7e-14).
2. `POST /production/orders` records the order and sends the material; in the same transaction the
   API reads the order's inputs (`v_ml_production_order_inputs`), forecasts the yield, turns it
   into packages and EKP with the exact cost formula, and stores forecast, 80 % interval and
   baseline in `ml_prediction`.
3. When the finished goods are received (`POST /production`), the actual packages and EKP are
   written next to the forecast, and `is_anomaly` is set if the actual is outside the 98 % band.
   The dashboard (*Production*, *AI*) and the phone (*Yield forecasts*) show it like the seeded rows.

Before saving, *Preview* in the order form sends the same request with `dry_run`: every check and
the forecast run, the transaction is rolled back, and the expected packages and EKP with their
80 % interval are shown at the moment the order is decided. Saving stores the same forecast.

## Synthetic test and recorded data are kept apart

The *AI* tab has two sections. The chart and the MAE / MAPE table are the **synthetic test**
(held-out dates of the generated history). The second section is forecast vs actual on the
**records in this database**, each labelled with its origin (`ml_prediction.data_origin`):

| Origin | Meaning |
|---|---|
| `SEED` | written by `database-mysql/seed.sql` (URT-2026-045, URT-2026-046, PRC-2026-001); the forecasts were added after the outcome |
| `DEMO` | entered through the app in a demo deployment (the default) |
| `FIELD` | entered while the API runs with `DATA_ORIGIN=FIELD`; still **not verified** |

The app cannot verify a record: that needs the source document, a verifier, independence from
training and a forecast frozen before the outcome, checked by `ml-engine/audit_field.py` (see
[ai/field-audit-2026-10-30.md](ai/field-audit-2026-10-30.md)). The tab and the *Overview* box
therefore state "verified field samples: 0" while there are no field records, and "verified
outside the app" once there are. The *Overview* AI box is titled *synthetic test* and is not
coloured as a success.

Evidence: `TestLiveYieldMatchesTheTrainedModel` compares the Go evaluation with sklearn on 33 golden
rows (both seeded orders and 31 held-out orders, incl. missing fat %, summer months and 500 g packs):
all within 1e-9. URT-2026-045 gives the seeded 84.6 packages and EKP 18.2172 EUR.
`TestIntegrationProductionOrderWithLiveForecast` runs purchase -> order -> forecast -> receipt ->
`/ai/summary`, and checks that a failing input leaves nothing behind.

## Predictions on the demo records

`GET /ai/summary` shows, for the seeded records, the model prediction, the baseline prediction,
the actual value and the deviation (absolute and %):

| Record | Target | Actual | Model | Model dev. | Baseline | Baseline dev. |
|---|---|---|---|---|---|---|
| URT-2026-045 | packages | 85 | 84.6 | 0.47 % | 83.35 | 1.94 % |
| URT-2026-045 | EKP | 18.16 EUR | 18.22 EUR | -0.32 % | 18.41 EUR | -1.38 % |
| URT-2026-046 | packages | 83 | 82.8 | 0.24 % | 83.35 | -0.42 % |
| PRC-2026-001 | leg | 6.50 kg | 6.79 kg | -4.52 % | 6.56 kg | -0.91 % |
| PRC-2026-001 | waste | 3.00 kg | 2.61 kg | 13.1 % | 2.92 kg | 2.55 % |

On the single butchery demo record the baseline is closer for four of six parts. That record was
written by hand to match the standard lamb recipe split (26/14/20/12/16/12 %), which is almost
exactly the baseline. One record proves nothing either way; the rolling-origin results above are
the measurement. `GET /ai/summary` also returns each row's 80 % interval (`interval_low`,
`interval_high`, `interval_level`) and, for packages and part weights, the `anomaly` and
`outside_interval` flags; the dashboard *AI* tab and the Android *Yield forecasts* screen show them.
On the demo records nothing is an anomaly; leg and waste of PRC-2026-001 are outside the 80 %
interval but inside the 98 % band.

## Limitations

- Both studies now run in the Go API. Study B's expected weight per part is shown live in the
  dashboard's butchery form, and the API verdict (model, baseline, actual, interval, anomaly) after
  *Check* or *Save*. The forecast stored with a posted operation (`ml_prediction`) is computed in
  the same request as the actuals, so it cannot serve as prospective field evidence.
  `TestIntegrationButcheryLiveForecast` verifies preview, dry run, stored
  model/baseline/actual/interval/anomaly rows, `/ai/summary`, and rollback on a failed record.
- A butchery forecast becomes prospective only when it is frozen before the parts are weighed
  (*Freeze forecast* in the form, `POST /ai/forecast/butchery/freeze`). The frozen forecast never
  changes, carries the server time, and a carcass gets one, made before it is cut; the posted
  operation links to it and is compared with it. The field audit export then uses only the frozen
  forecast for that operation, and `audit_field.py` excludes one frozen for another input weight.
  Freezing is a precondition, not proof: the scale record must still show the weighing came after
  `frozen_at`, and no verified field operation exists yet. The form only offers the freeze while no
  part weight is entered; it cannot know whether the parts were already weighed.
  `TestIntegrationFrozenButcheryForecast`, `database-mysql/tests/test_frozen_butchery_forecast.py`
  and the e2e test *a forecast frozen before weighing…* cover it.
- A production extrapolation with a nonpositive lower package bound or output exceeding the
  material mass is withheld (`forecast` null/absent). This guard prevents invalid arithmetic;
  it is not an estimate of accuracy outside the training distribution.

- The training data is synthetic. The results show how well the *process* can be learned, not
  how the model performs in a real plant. They must be re-measured on real orders.
- The same author wrote the generator and the features (fixed filler loss, summer effect).
  These are plausible physical priors, but on synthetic data they flatter the model.
- Few real records exist (2 production orders, 1 posted butchery operation), so deviations on the
  demo records are anecdotes, not accuracy.
- Interval coverage is guaranteed on average under exchangeability. Under drift it degrades;
  per-fold coverage already ranges from 70 % to 84 %.
- The models do not know about events that are not in the features (machine failures,
  mislabelled lots). Large deviations are flagged for a human; they never change stock, cost or
  lot status.

## What changes when real data arrives

1. **Switch-over rule.** Serve the historical mean until the learning-curve thresholds are met
   (about 400 real production orders, 200 butchery operations), then serve the model only if it
   beats the mean on a rolling-origin evaluation of the real data (`evaluate_ali.py` runs unchanged
   on the `v_ml_*_features` views).
2. **Retraining.** Monthly, expanding window, same protocol. A new model replaces the old one only
   if the paired test on the latest months says it is not worse.
3. **Drift monitoring.** Track every month: rolling MAE vs the baseline, the share of actuals
   outside the 80 % interval (alarm if it leaves 70-90 % for two months), and the input mix
   (supplier, subcontractor, fat %, carcass weight vs training ranges). Re-calibrate the
   intervals on the most recent window first; retrain if coverage stays off.
4. **Re-check the priors.** Confirm with real data that the explanation sentences above still
   hold. Drop features that do not carry over.
5. **Feedback.** Use the `ml_anomaly_alert` status (`FALSE_ALARM` / `RESOLVED`) to measure the
   flag's real precision.

## Why traceability never uses AI

Recall decisions must be exact and auditable: which lots, which customers, how many packages.
They are answered by recursive SQL over recorded relations (`sp_trace_forward`,
`sp_trace_backward`, `sp_recall_initiate`). A prediction can be wrong; a recorded shipment cannot
be "probably" at Market B. The AI features only estimate yields and costs to support planning and
to flag unusual results.

## Reproduce

```bash
pip install -r ml-engine/requirements.txt
python ml-engine/generator_ali.py      # synthetic history (seeded)
python ml-engine/train_model_ali.py    # time-split training, intervals, metrics.json
python ml-engine/anomaly_ali.py        # calibrated alert detector
python ml-engine/evaluate_ali.py       # rolling-origin evaluation, CIs, tests, charts (~10 min)
python ml-engine/ai_summary.py         # /ai/summary preview with the optional fields
python ml-engine/export_ai_report.py   # writes the AI block of database-mysql/seed.sql
python ml-engine/export_live_model.py  # the champion as numbers for the Go API's live forecasts
pytest ml-engine/tests -q
```
