# Spec compliance (AVATAR assessment, bullet by bullet)

Every bullet of the assessment brief ([ASSESSMENT.md](ASSESSMENT.md)), where it is implemented,
and how it is proven.
Evidence types: **T** = automated test (name), **C** = live `curl` against the compose stack
(2026-10-28, API 1.3.0, clean seed), **S** = sample record in [`docs/samples/`](samples/).
All tests run in CI ([`.github/workflows/ci.yml`](../.github/workflows/ci.yml)) and locally with
`./scripts/check-all.sh`.

Status: **Done** = implemented and proven; **Done (fixed 2026-10-28)** = a gap found in this audit and
closed; **Partial** = works, with a stated limit.

## Task 1 - External production and real cost (EKP)

| Requirement | Where | Evidence | Status |
|---|---|---|---|
| Raw-material cards, purchase prices, units, lot numbers, stock | `product` (uom, standard price, shelf life), `unit_of_measure`, `lot`, `stock_balance` (ledger cache); `GET /lots`, `GET /purchases`; dashboard *Purchases* | S `lots-stock.json`, `purchases.json`; T `test_balance_cache_equals_ledger` | Done |
| Raw-material entry can be *recorded* (not only seeded) | `POST /purchases` (receipt + lot + receipt line + `PURCHASE_IN` in one transaction); dashboard *Purchases* → *New receipt* form (supervisor PIN, per-field API errors) | T `TestIntegrationPurchaseNeedsSupplierAndExpiry`; C `POST /purchases` → 201 `{"total":283.2,...}` | Done (fixed 2026-10-28) |
| External production order, material shipment, service cost, finished-goods receipt | `production_order`, `production_input` (qty_sent / qty_returned / qty_consumed, cost snapshot), `production_output`, `sp_production_send_input` / `sp_production_add_input`, `sp_production_complete`; `TRANSFER_OUT/IN` to `FASON-01`, `PRODUCTION_CONSUME`, `PRODUCTION_RECEIPT` | T `test_cost_breakdown_lines_match_spec_table`, `test_every_production_input_has_lot_link`; S `production-orders.json`, `lot-movements.json` | Done |
| An order can be *recorded* and its goods *received* (not only seeded) | `POST /production/orders` (order + material dispatch + forecast in one transaction), `POST /production` (receipt); dashboard *Production* → *New production order*, *Receive finished goods* (supervisor PIN) | T `TestIntegrationProductionOrderWithLiveForecast` (also: a failing input leaves nothing); C URT-2026-047 → 201, received with 82 packs → EKP 18.87 € | Done (added 2026-10-29) |
| **Quantity variances** recorded | output: `planned_qty` vs `actual_qty` → `qty_variance`, `fulfilment_pct` (`v_production_cost`); material: `qty_sent - qty_returned = qty_consumed` per input lot; `yield_pct_of_meat` | S URT-2026-046: planned 85, actual 83, `qty_variance` −2, fulfilment 97.65 %; T `test_quantity_variance_is_recorded` | Done |
| EKP from the real output quantity | `v_production_cost.ekp = (material + actual_qty × service price) / actual_qty` | T `test_ekp_matches_spec`, `TestIntegrationTask1EKP`: 1,543.50 € / 85 = **18.16 €** (18.1588); URT-2026-046 with 83 packs → 18.64 € | Done |
| Raw materials not on the service invoice | service cost = `actual_qty × service_unit_price` only (467.50 €); material from lot cost snapshots (1,076.00 €) | T `test_finished_lot_carries_ekp_and_raw_materials_not_on_service_invoice` | Done |
| VKP, gross profit, margin | `v_production_cost.vkp`, `gross_profit_per_unit`, `margin_pct` | S URT-2026-045: VKP 24.90 €, profit 6.74 €/pack, margin 27.07 %; T `test_vkp_margin` | Done |
| All cost and stock movements reviewable retroactively | append-only `stock_movement` (UPDATE/DELETE blocked by triggers), `audit_log` (append-only), `GET /movements?lot_no=` | T `test_ledger_is_append_only`; S `lot-movements.json` | Done |

## Task 2 - Whole-lamb butchery and Kotlin sales

| Requirement | Where | Evidence | Status |
|---|---|---|---|
| Recipe and allocation key definable in advance; actual weights recorded at processing | `butchery_recipe` (default key), `butchery_recipe_line` (expected yield %, value price), `butchery_output.weight_kg` | seed `REC-KUZU-STD`; S `butchery-operations.json` | Done |
| Butchery operation can be *recorded* via the API | `POST /butchery/operations` (carcass lot, weight, price, extra cost with document note, date, operator, outputs; `dry_run`); dashboard *Butchery* → *Record butchery* form (supervisor PIN, live weight balance, *Check* = `dry_run`) | T `TestIntegrationButcheryWithByProductBothKeys`; C PRC-2026-002 → 201 | Done (fixed 2026-10-28) |
| Input animal/lot, weight, real total purchase cost, date | `butchery_operation` (input_lot_id, input_weight_kg, purchase_unit_price, additional_cost, total_cost, op_date) | S PRC-2026-001: 25 kg × 11.50 € = 287.50 € | Done |
| Part products, quantities, **waste / by-products**, weight balance check | `output_kind` CUT / BY_PRODUCT / WASTE; `v_butchery_reconciliation.weight_ok` (10 g tolerance); API `weight_check` input = cuts + by-products + waste + unrecorded; the POST refuses an unbalanced operation (422 `WEIGHT_IMBALANCE`, nothing written). Seed product `KZ-SAKATAT` (offal) is the by-product | T `TestIntegrationButcheryWithByProductBothKeys` (24 kg = 21.1 cuts + 0.8 by-product + 2.1 waste; 0.5 kg gap → 422); C PRC-2026-002 `weight BALANCED`, by-product lot KZC-2026-002 with 10.34 € | Done (by-product data and recording fixed 2026-10-28) |
| Cost allocated by **quantity- or value-weighted key**; allocated total = total cost | `AllocateCost` (largest remainder, cent-exact), `GET /butchery/allocation?op_no=&method=QUANTITY\|VALUE`, `POST /butchery/operations` returns both keys (`allocation_by_key`) | T `TestAllocateCostValueKeyMatchesSeed`, `TestAllocateCostQuantityKeySumsExactly`, `TestAllocateButcheryBothKeysWithByProduct`, `test_value_weighted_allocation_reference`; S PRC-2026-001 VALUE 95.61 / 60.84 / 60.18 / 28.08 / 42.79 and QUANTITY 84.94 / 45.74 / 65.34 / 39.21 / 52.27, both **= 287.50 €**, `sums_exactly: true`; C PRC-2026-002 both keys = 283.20 € | Done |
| Spec image inconsistencies detected and explained (25 × 11.50 = 287.50 ≠ 351.00; parts = 22 kg) | PRC-2026-000 kept as REJECTED draft; findings `DECLARED_COST_MISMATCH` (+63.50 €) and `WEIGHT_IMBALANCE` (3 kg) in tr/de/en | T `test_spec_example_is_rejected_by_reconciliation_gate`, `TestIntegrationButcheryFindingsAndAllocation`; S `butchery-operations.json` | Done |
| Parts stocked per lot | one lot per cut / by-product (`origin_type BUTCHERY`), unit cost = allocated cost / kg, `BUTCHERY_OUTPUT` movements | T `test_posted_butchery_cuts_have_lots_and_links` | Done |
| Sold from the Kotlin Android app | POS *Sell* → SQLite outbox → signed `POST /sync` → `sale` + `sale_line` + `SALE` movement | T `test_each_pos_sale_line_has_its_stock_movement`; real-device walkthrough 2026-10-28 (`docs/screenshots/android-flow-*`) | Done |

## Task 3 - Lot traceability and recall

| Requirement | Where | Evidence | Status |
|---|---|---|---|
| Mandatory scenario ET-2026-001 → URT-2026-045 → SUC-2026-045 (85) → Markets A/B/C 30/25/30 | seed + `lot_link`, `shipment` | `./scripts/demo-reset.sh` prints the path; S `trace-forward-ET-2026-001.json` | Done |
| **Supplier, lot no, date, quantity, expiry on every raw-material entry** | `lot.supplier_id` (CHECK `ck_lot_purchase_supplier`), `supplier_lot_no`, `lot_date`, `initial_qty`, `expiry_date`; new trigger `trg_lot_before_insert` refuses a purchased perishable lot without expiry or with expiry before entry; `POST /purchases` returns 422 per field | T `test_every_purchased_lot_has_supplier_date_qty_and_expiry`, `test_purchased_perishable_lot_without_expiry_is_rejected`, `test_purchase_without_supplier_is_rejected`; C missing expiry → 422 `lines[0].expiry_date: required` | Done (DB-level rule added 2026-10-28) |
| Many-to-many with quantities (several raw lots per order; one raw lot into several finished lots) | `lot_link(parent, child, qty_consumed, qty_produced, ref)` | T `test_many_to_many_one_raw_lot_into_two_finished_lots` | Done |
| Butchery: input lot linked to every part lot | `lot_link` type BUTCHERY (also for API-recorded operations, incl. by-products) | T `test_posted_butchery_cuts_have_lots_and_links`, `TestIntegrationButcheryWithByProductBothKeys` (6 links) | Done |
| **Transfer, sale, return, rework, waste** per lot | `POST /movements/transfer` (TRANSFER_OUT/IN), POS sale (SALE), `POST /movements/return` (RETURN), `POST /rework` (REWORK_CONSUME/OUTPUT + new lot + lot_link), `POST /movements/waste` (WASTE, reason required); sign rules and negative-stock guard in `trg_sm_before_insert` | T `TestIntegrationMovements` (all four API movements, rework lot traced back to the carcass, over-stock waste → 409), `test_wrong_sign_is_rejected`, `test_negative_stock_is_rejected` | Done |
| Stock count (beyond the brief: keeps the ledger equal to the shelf) | `POST /stock-counts` (supervisor): counted vs ledger balance at save time, difference → `ADJUSTMENT_IN/OUT` with a required reason, `stock_count` / `stock_count_line` kept as evidence, stale list → 409; shortage alert; dashboard *Lots & stock* → **Count stock** and **Recent counts** | T `TestIntegrationStockCountBooksDifferencesWithReason` (no reason / half a pack / stale list / unknown lot leave nothing; −3 packs booked, alert, mass-balance gap), `test_stock_count.py` (3 tests), `stockCount.test.js`, e2e `a stock count books a difference…` | Done (added 2026-10-30) |
| Forward trace: affected finished lots, stock, shipments, known buyers | `sp_trace_forward`, `GET /trace/{lot}?direction=forward` | T `test_forward_trace_finds_markets_affected_by_et_2026_001`, `TestIntegrationTrace`; S markets A 30, B 25, C 30, tier-2 Örnek Lokanta 10 | Done |
| Backward trace: raw lots, suppliers, production orders | `sp_trace_backward`, `GET /trace/{lot}?direction=backward` | T `test_backward_trace_reaches_raw_lots_suppliers_and_order`, `test_backward_trace_from_butchery_cut`; S `trace-backward-SUC-2026-045.json` | Done |
| Recall blocks affected products; per-customer quantity, shipment and contact list; tier 2 only when recorded | `sp_recall_initiate` (lots → RECALLED), `sp_recall_report`, `POST /recall` (supervisor session), `GET /recall/contacts.csv`; `second_tier` RECORDED / NOT_RECORDED; new deliveries and reported onward deliveries via `POST /shipments` (dashboard *Shipments*) | T `test_recall_blocks_affected_lots_only`, `test_recall_report_lists_customers_with_quantities_and_contacts`, `TestIntegrationRecallContactsCSV`, `TestIntegrationShipmentFirstAndSecondTier`; S `recall-ET-2026-001.json`, `recall-ET-2026-001-contacts.csv` | Done |
| Kotlin: each sale line stores lot, quantity and its stock movement | `sale_line.lot_id`, `qty`, `stock_movement_id UNIQUE` (trigger posts the SALE movement) | T `test_each_pos_sale_line_has_its_stock_movement` | Done |
| **Sale of recalled lots is blocked** | DB: `trg_sm_before_insert` → `LOT_BLOCKED`; API: sync returns `rejected[].code = LOT_BLOCKED`; app: `isBlocked` / `sellableLots` hide and block recalled lots, also offline from the cached recall list | T `test_recalled_lot_cannot_be_sold_but_can_be_returned`, `TestIntegrationSaleOfRecalledLotIsRejected` (API), `RecalledLotBlockingTest` (Kotlin, 5 cases) | Done |
| Expired lots are not sold or shipped (beyond the brief: the expiry date is recorded, so it is enforced) | DB: `trg_sm_before_insert` → `LOT_EXPIRED` for SALE / SHIPMENT after `expiry_date`, judged by the movement's own time (an offline sale from before the expiry still syncs); API: sync `rejected[].code = LOT_EXPIRED` (permanent); app: `isExpiredOn` / `sellableLots` move the lot to "SKT geçti" with the reason; waste and returns stay allowed. The seed calendar is relative to the load day, so minced lamb KZY-2026-001 (2-day use-by) shows as expired in every demo | T `test_expiry.py` (4 tests), `TestIntegrationSaleOfExpiredLotIsRejected`, `ExpiredLotTest` (Kotlin, 5 cases) | Done (added 2026-10-29) |
| Mass balance and lot flow (beyond the brief: proves the trace accounts for every quantity) | `mass_balance` in the forward trace and the assessment report (`mass_balance.go`): per lot, lot quantity = used in lots + shipped + sold + waste + stock − returned from the ledger, undocumented gap flagged; dashboard *Trace & recall* → **Lot flow** diagram (lots by depth, customers by tier, stock/till/waste/gap) and **Mass balance**; PDF section 3 | T `TestIntegrationMassBalanceAccountsForEveryPack` (ET-2026-001: 100 kg all used; a 1-pack count write-off unbalances), `traceFlow.test.js`, e2e `trace shows the lot flow and a mass balance…`; `verify-assessment-pdf.py` requires the section | Done (added 2026-10-30) |

## Task 4 - Required AI work

Owned by the ML workstream ([`docs/AI.md`](AI.md)); listed for completeness, not changed in this audit.

| Requirement | Where | Evidence | Status |
|---|---|---|---|
| A: expected output qty and unit cost from past orders; prediction, actual, deviation shown together | `ml-engine/`, `ml_prediction`, `v_ml_prediction_deviation`, `GET /ai/summary`, dashboard *AI* and *Production*; new orders are forecast live when recorded and get their actual when received ([AI.md](AI.md#live-forecasts-for-new-orders-study-a)) | T `test_ml_prediction_shown_with_actual_and_deviation`, `TestLiveYieldMatchesTheTrainedModel` (Go = sklearn on 33 rows), `TestIntegrationProductionOrderWithLiveForecast`; S `ai-summary.json` | Done |
| B: part weights from species, input weight, history; actual vs predicted | `ml-engine/`, `GET /ai/summary` | S `ai-summary.json` | Done |
| Sample data, approach, baseline comparison, error metric, limits | `ml_model_metric` (model vs baseline MAPE/MAE), docs/AI.md | S `ai-summary.json` | Done |
| Trace and recall from exact records, not AI | recall uses only `lot_link` / shipments; AI anomalies never lock the POS | `sp_recall_initiate` source | Done |

## 5 - Optional AI

| Requirement | Where | Evidence | Status |
|---|---|---|---|
| Invoice / delivery-note reading as suggestions, saved after human confirmation | Android *Lots → Receive from invoice* (NVIDIA vision model) | app; disabled in the published demo APK because no API key is shipped (clear message on the row) | Partial (by design in the APK) |
| Anomaly alerts (waste, cost, low yield, inconsistent movements) | written when a record is saved: yield outside the forecast's 98 % band (receipt), unexpected waste / part weights (butchery), purchase price > 10 % above the last three purchases, POS sale of a recalled or expired lot refused at sync (`alerts.go`); ml-engine batch detector; `GET /alerts`, `POST /alerts/review` (supervisor, note to close, audited); dashboard **Alerts** tab and sidebar bell; thresholds are configuration (`GET/POST /alerts/rules`, audited) with defaults from a synthetic stress test of every rule (`calibrate_live_alerts.py`: the first butchery default gave 9 false alarms per 100 carcasses and was raised to 1.5 bands); observed precision and recall with 95 % intervals from reviewed alerts and missed problems (`POST /alerts/missed`) | T `TestIntegrationAlertPurchaseCostIncreaseAndReview`, `TestIntegrationAlertLowYieldOnProductionReceipt`, `TestIntegrationAlertExtraWasteOnButchery`, `TestIntegrationAlertRefusedSaleOfRecalledLot`, `alerts_test.go`, `TestIntegrationAlertThresholdsAreConfigurationAndMissesAreCounted`, `test_ml_anomaly_alert.py` (6 tests), `test_alert_rules.py` (3), `test_calibrate_live_alerts.py` (5), `alertQuality.test.js`, e2e `a purchase above recent prices raises an alert…` | Done (live alerts 2026-10-30; calibration, thresholds and recall 2026-10-31). Field accuracy not established: no business data |

## 6 - Technical requirements

| Requirement | Where | Evidence | Status |
|---|---|---|---|
| Android app in Kotlin | `mobile-kotlin/` (Compose) | CI job *Android POS*; release APK `scripts/build-release-apk.sh` | Done |
| Local storage SQLite | `android.database.sqlite` in WAL mode (`AppDatabase`: `sync_outbox_queue`, local movements, catalog cache) | `mobile-kotlin/.../database` | Done |
| Central storage MySQL | `database-mysql/schema.sql` | CI job *MySQL* | Done |
| Backend Go, REST | `api-go/` | CI job *Go API*; [`docs/API.md`](API.md) | Done |
| Web management in React | `web-react/` | CI job *React*; screenshots `docs/screenshots/web-*` | Done |
| AI service with API integration | A: forecast on `POST /production/orders`, actual on `POST /production`. B: `GET /ai/forecast/butchery`, forecast + actual stored by `POST /butchery/operations`. Both use the exported Ridge in Go and expose comparisons through `/ai/summary`. | T `TestLiveYieldMatchesTheTrainedModel`, `TestIntegrationProductionOrderWithLiveForecast`, `TestIntegrationButcheryLiveForecast` | Done: B's expected weights shown live in the dashboard butchery form (e2e `butchery form shows the AI expectation per part`); local changes need deployment |
| SQLite ↔ MySQL sync explained | [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) (outbox, UUIDv7 idempotency, signed batches); now with pinned device keys and replay-mismatch detection ([`docs/SECURITY.md`](SECURITY.md)) | T `TestIntegrationSyncIdempotentAndReplayMismatch` (same batch twice → 1 sale, 1 movement) | Done |
| Reports: purchase, production, butchery, lot movements, sales, recall | dashboard tabs Purchases, Production, Butchery, Lot movements, Sales, Trace & recall | screenshots `docs/screenshots/web-*.png` | Done |

## 7 - Deliverables

| Requirement | Where | Evidence | Status |
|---|---|---|---|
| Runnable prototype, source, setup instructions | README *Quick start* (Docker/Podman), demo APK (`scripts/build-release-apk.sh`, GitHub Release) | fresh-clone test 2026-10-28 | Done |
| Data model, API description, **sample records**, basic automated tests | `docs/ARCHITECTURE.md`, `docs/API.md`, **`docs/samples/`** (new, regenerated by `scripts/export-samples.sh`), Go / pytest / Kotlin / vitest suites in CI | this document | Done (samples added 2026-10-28) |
| End-to-end demo of the three tasks, especially the markets affected by ET-2026-001 | `docs/DEMO_SCRIPT.md` | real-device walkthrough 2026-10-28 | Done |
| AI method description, sample predictions, accuracy evaluation | `docs/AI.md`, `GET /ai/summary` | — | Done |

## Gaps found in this audit and how they were closed

1. **No way to record a raw-material receipt or a butchery operation** except the seed. Added
   `POST /purchases` and `POST /butchery/operations` (transactional, with movements and lot links).
2. **Expiry date was optional at DB level.** A trigger now requires it for purchased products that
   have a shelf life, and refuses an expiry before the entry date.
3. **No by-product in the data.** Added the by-product item `KZ-SAKATAT`; by-products are weighed
   in the balance and carry cost under both keys (the seed's PRC-2026-001 numbers are unchanged).
4. **Back-office writes (recall, movements, rework) had no authentication** and the recall
   initiator was a free-text field. They now need a supervisor session; the operator is recorded.
5. **POS signatures were not really verified** (the app never sent its key, so the server only
   checked the signature's shape). Keys are now pinned per device and every batch is verified.
6. **No published sample records.** `docs/samples/` with the main records and the ET-2026-001
   recall output.
