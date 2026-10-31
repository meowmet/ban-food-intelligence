# REST API (Go, `api-go`)

Base URL: `http://localhost:8080` (docker compose). All responses are JSON unless stated.
Money values are EUR, quantities are in the lot's unit (`KG`, `PAKET`).

## Conventions

**Errors.** Every error has the same body and a meaningful status code:

```json
{ "code": "LOT_NOT_FOUND", "message": "Lot bulunamadı.", "details": "Error 1644 (45000): LOT_NOT_FOUND" }
```

`message` is chosen from `Accept-Language` (`tr`, `de`, otherwise `en`); clients branch on `code`.

| Status | Codes |
|---|---|
| 400 | `INVALID_INPUT` (missing/invalid fields) |
| 401 | `INVALID_PIN` |
| 403 | `SIGNATURE_REJECTED` (sync signature checks) |
| 404 | `NOT_FOUND`, `LOT_NOT_FOUND` |
| 405 | `METHOD_NOT_ALLOWED` |
| 409 | `INSUFFICIENT_STOCK`, `LOT_BLOCKED` (recalled/blocked lot), `LOT_EXPIRED` (sale or shipment after the expiry date), `EXCEEDS_DELIVERED` (onward delivery above what the customer holds), `ORDER_STATE`, `CONFLICT` |
| 422 | `INVALID_INPUT` (rejected by a database rule) |
| 429 | `LOCKED_OUT` (too many wrong PINs) |
| 500 | `DB_ERROR` |

**CORS.** Allowed for dashboard origins (ports 5173, 3000, 4173 on localhost/private LAN hosts) or the list in
`CORS_ALLOWED_ORIGINS`. **Logging:** one line per request with status and duration.
**Shutdown:** SIGINT/SIGTERM drain in-flight requests (10 s).

## Authentication for writes

Reads are open. Every back-office write needs a supervisor session:

```bash
TOKEN=$(curl -s -X POST localhost:8080/auth/session -d '{"pin":"246813"}' | jq -r .token)
curl -X POST localhost:8080/recall -H "Authorization: Bearer $TOKEN" -d '{"trigger_lot":"ET-2026-001"}'
```

`POST /auth/session` `{"pin","device_id"?}` → `200 {"token","token_type":"Bearer","expires_at","username","display_name"}`
(30 minutes); `401 INVALID_PIN` / `429 LOCKED_OUT` as for the supervisor unlock. Protected:
`POST /recall`, `/movements/transfer|return|waste`, `/rework`, `/production`, `/production/orders`,
`/purchases`, `/butchery/operations`, `/ai/forecast/butchery/freeze`, `/sync/register-key`. Without a valid token: `401 AUTH_REQUIRED`.
The recall initiator and the movements' `created_by` are the signed-in supervisor.

Limits for every request: body ≤ 1 MiB (`413 PAYLOAD_TOO_LARGE`), 20 requests/s per client IP
with a burst of 120 (`429 RATE_LIMITED`, `Retry-After`). See [SECURITY.md](SECURITY.md).

## Health

`GET /health`
```json
{"service":"ban-food-intelligence-api-go","version":"1.3.0","status":"UP","mysql_status":"CONNECTED","sync_crypto":"ECDSA-P256-SHA256 (crypto/ecdsa + crypto/x509)","server_time":"2026-10-28T20:21:52Z"}
```

## Task 1: production and real cost (EKP)

`GET /production[?order_no=URT-2026-045]` - orders with EKP, VKP, gross profit, margin, quantity
variance and the cost breakdown (from `v_production_cost`, `v_production_cost_breakdown`); orders
whose material is still at the subcontractor (`MATERIAL_SENT`) follow with `planned_qty`,
`input_kg` and `material_cost`. Every order with a forecast carries `forecast` (see below).
```json
{"order_no":"URT-2026-045","status":"RECEIVED","planned_qty":85,"actual_qty":85,"qty_variance":0,
 "material_cost":1076,"service_cost":467.5,"total_cost":1543.5,"ekp":18.1588,"ekp_display":18.16,
 "vkp":24.9,"gross_profit_per_unit":6.74,"gross_profit_total":573,"margin_pct":27.07,
 "output_lot_number":"SUC-2026-045",
 "breakdown":[{"lot_no":"ET-2026-001","cost_element":"Dana et (sucukluk)","qty":100,"uom_code":"KG","unit_cost":9.7,"amount":970}, "...",
              {"lot_no":"FAS-FTR-2026-045","is_service":true,"cost_element":"Fason hizmet / işçilik","qty":85,"amount":467.5}]}
```
On the service line (`is_service: true`) `lot_no` carries the service invoice number, not a lot.

`POST /production/orders` - record an external production order and send its raw material to the
subcontractor, in one transaction: the order, a `TRANSFER_OUT`/`TRANSFER_IN` pair per lot with
its cost snapshot (`sp_production_send_input`), and the AI forecast stored in `ml_prediction`.
Inputs must be raw-material lots received by the order date, not blocked/recalled, and valid
on that date. Rejections roll back the order (`409 LOT_BLOCKED` / `LOT_EXPIRED`,
`422 INVALID_INPUT` for the wrong product type, receipt date or identical warehouses).
```json
{"order_no":"URT-2026-047","product_sku":"MM-SUCUK-1KG","subcontractor_code":"FAS-01","planned_qty":85,
 "service_unit_price":5.5,"drying_days":5,"order_date":"2026-10-29",
 "inputs":[{"lot_no":"ET-2026-003","qty":100},{"lot_no":"BAH-2026-002","qty":2},{"lot_no":"BAG-2026-002","qty":5}]}
```
With `"dry_run": true` the same checks and the forecast run inside the transaction, which is then
rolled back: `200` with `"status":"DRY_RUN"` and the same body as below, nothing written (no
order, movement or `ml_prediction` row). The dashboard's *Preview* button uses it.
`201`:
```json
{"order_no":"URT-2026-047","status":"MATERIAL_SENT","material_cost":1096,"inputs":["..."],
 "forecast":{"model":"ali_ridge","model_version":"ali-1.1.0","yield_pct":84.44,"packages":84.4,
  "packages_low":83,"packages_high":85.9,"ekp":18.4796,"ekp_low":18.2634,"ekp_high":18.7032,
  "interval_level":0.8,"baseline_packages":83.3,"baseline_ekp":18.6495}}
```
`422 INVALID_INPUT` per field (e.g. `inputs[1].lot_no`: lot listed twice; the product must be a
finished good, the partner a subcontractor); `404 LOT_NOT_FOUND`, `409 INSUFFICIENT_STOCK` /
`LOT_BLOCKED` / `CONFLICT` (order number exists). On any error nothing is written. `forecast` is
`null` when no meat was sent or the model extrapolates to nonphysical quantities or bounds.

`POST /production` - receive the finished goods of an order whose material was sent (`sp_production_complete`):
```json
{"order_no":"URT-2026-047","actual_qty":82,"output_lot_no":"SUC-2026-047","invoice_no":"FAS-FTR-2026-047",
 "sub_warehouse":"FASON-01","target_wh":"ANA-DEPO"}
```
Returns the computed order (as above); its `forecast` now has `actual_packages`, `actual_ekp` and
`anomaly` (actual outside the 98 % band). `409 ORDER_STATE` if already received,
`409 INSUFFICIENT_STOCK` if the material is not at the subcontractor, `409 CONFLICT` if the
finished lot number exists.
Incomplete receipt fields, nonpositive quantities, invalid dates or an expiry before the lot
date return `422 INVALID_INPUT`. An explicit `order_no` is required. An order-only request
still calculates its existing cost.

## Task 2: butchery

`GET /butchery` - every operation with weight balance, findings and the posted allocation.
```json
{"op_no":"PRC-2026-000","status":"REJECTED","input_weight_kg":25,"cut_weight_kg":22,"waste_weight_kg":0,
 "total_cost":287.5,"declared_total_cost":351,"declared_cost_diff":63.5,"allocated_total":0,
 "weight_check":{"input_kg":25,"cuts_kg":22,"by_product_kg":0,"waste_kg":0,"unrecorded_kg":3,"tolerance_kg":0.01,"status":"UNRECORDED_LOSS"},
 "findings":[
   {"code":"DECLARED_COST_MISMATCH","severity":"ERROR","message":"Declared cost 351.00 EUR does not match 25.00 kg x 11.50 EUR = 287.50 EUR. ...",
    "values":{"declared_total_cost":351,"computed_total_cost":287.5,"difference":63.5}},
   {"code":"WEIGHT_IMBALANCE","severity":"ERROR","message":"Input 25.00 kg, but parts 22.00 kg and recorded waste 0.00 kg leave 3.00 kg unrecorded. ...",
    "values":{"input_kg":25,"outputs_kg":22,"waste_kg":0,"unrecorded_kg":3}}],
 "allocated_cuts":[...]}
```
`PRC-2026-001` (posted) returns `WEIGHT_BALANCED` and `ALLOCATION_BALANCED` and the cut list with
`allocated_cost`, `allocated_unit_cost_per_kg`, `margin_percentage`.

`GET /butchery/allocation?op_no=PRC-2026-001&method=QUANTITY|VALUE` - cost split computed with the
largest-remainder method; the cents always sum to the total cost.
```json
{"op_no":"PRC-2026-001","method":"QUANTITY","total_cost":287.5,"allocated_total":287.5,"sums_exactly":true,
 "lines":[{"cut_sku":"KZ-BUT","weight_kg":6.5,"basis":6.5,"share_pct":29.55,"allocated_cost":84.94,"unit_cost_per_kg":13.0677}, "..."]}
```

`POST /butchery/operations` (session) - record a carcass breakdown with actual weights. Cuts and
by-products get a lot each; waste gets none. The weight balance must hold (10 g tolerance), otherwise
`422 WEIGHT_IMBALANCE` with the `weight_check` and nothing is written. The response shows the
allocation with the chosen key and with both keys (`allocation_by_key`); `"dry_run": true` only
calculates.
Both previews and posted records reject blocked/recalled carcasses (`409 LOT_BLOCKED`), an
expiry before `op_date` (`409 LOT_EXPIRED`), and an operation before receipt (`422 INVALID_INPUT`).

The response also includes `forecast`: six expected part weights, their 80% prediction
intervals and species baseline, recorded weights, and flags for actuals outside the 98% band.
A posted operation stores six model and six baseline rows in `ml_prediction`; a dry run
stores none. By-products are outside the model; cuts match by SKU and waste by output kind.
If the carcass has a frozen forecast (below), the response adds `frozen_forecast` with the same
part rows compared against the recorded weights, `frozen_at`, `input_weight_kg` and
`input_matches` (frozen for the operation's input kg). Posting links it to the operation (`op_no`);
a dry run does not.

Example operation request:
```json
{"op_no":"PRC-2026-002","input_lot_no":"KUZU-2026-002","input_weight_kg":24,"allocation_method":"QUANTITY",
 "op_date":"2026-10-28","outputs":[{"sku":"KZ-BUT","output_kind":"CUT","weight_kg":6.2,"lot_no":"KZB-2026-002"}, "...",
  {"sku":"KZ-SAKATAT","output_kind":"BY_PRODUCT","weight_kg":0.8,"lot_no":"KZC-2026-002"},
  {"sku":"KZ-FIRE","output_kind":"WASTE","weight_kg":2.1}]}
```
`201 {"status":"POSTED","total_cost":283.2,"weight_check":{"status":"BALANCED",...},"allocation":{...},
"allocation_by_key":{"QUANTITY":{"allocated_total":283.2,"sums_exact":true,...},"VALUE":{...}}}`.

`GET /ai/forecast/butchery?input_lot_no=KUZU-2026-001&input_weight_kg=25[&op_date=2026-10-29&operator_name=OP-1]`
previews the forecast without saving. Weight must be finite and `0 < kg <= 500`. Unknown lots
return `404 LOT_NOT_FOUND`, non-carcass lots or invalid fields `422 INVALID_INPUT`.
The dashboard's *Record butchery* form calls it while the carcass lot and weight are typed and shows
the expected kg and interval next to each weighed part (*Expected (AI)*); it can prefill the part
products from the forecast, never their weights. If the carcass has a frozen forecast, the response
also carries it as `frozen` (`frozen_at`, `frozen_by`, `input_weight_kg`, `op_date`, `op_no` once
linked, `forecast`).

`POST /ai/forecast/butchery/freeze` (session) `{"input_lot_no","input_weight_kg","op_date"?,"operator_name"?}`
stores the forecast of a carcass before its parts are weighed (`butchery_forecast`,
`butchery_forecast_part`): `201` with the frozen forecast. It never changes afterwards (database
triggers), the time is the server's, and a carcass gets at most one, so no forecast can be picked
after the outcome: `409 FORECAST_FROZEN` (details: the existing one), `409 FORECAST_TOO_LATE` once
the carcass is cut. Validation as for the preview. The posted operation is compared with it (above);
`ml-engine/field_audit_export.sql` exports a linked operation with its frozen forecast only, the only
butchery forecast that can precede the weighing in `ml-engine/audit_field.py`.

## Task 3: traceability and recall

`GET /trace/{lot_no}?direction=forward|backward` - recursive trace over `lot_link` (no AI).
Forward: every lot made from the root, the many-to-many links with quantities, stock, shipments to
buyers (tier 1 and recorded tier 2) and POS sales. Backward: input lots, suppliers, documents.
```json
{"root_lot_number":"ET-2026-001","direction":"forward",
 "lots":[{"depth":0,"lot_no":"ET-2026-001","origin_type":"PURCHASE","qty":100,"uom_code":"KG","status":"ACTIVE"},
         {"depth":1,"lot_no":"SUC-2026-045","origin_type":"PRODUCTION","qty":85,"uom_code":"PAKET","status":"ACTIVE"}],
 "links":[{"link_type":"PRODUCTION","document_no":"URT-2026-045","input_lot_no":"ET-2026-001","qty_consumed":100,
           "output_lot_no":"SUC-2026-045","qty_produced":85}],
 "shipments":[{"tier":1,"customer_code":"MKT-A","customer_name":"Market A","contact_person":"...","phone":"...","qty":30}, "...",
              {"tier":2,"customer_code":"REST-01","received_via":"Market C","qty":10}],
 "mass_balance":{"balanced":true,
   "lines":[{"lot_no":"ET-2026-001","uom_code":"KG","declared_qty":100,"used_in_lots":100,"shipped":0,"sold":0,"wasted":0,
             "returned":0,"count_diff":0,"in_stock":0,"at_subcontractor":0,"accounted":100,"unexplained":0,"accounted_pct":100},
            {"lot_no":"SUC-2026-045","uom_code":"PAKET","declared_qty":85,"shipped":85,"unexplained":0,"...":"..."}],
   "totals":[{"uom_code":"PAKET","at_customers":85,"sold":0,"wasted":0,"in_stock":0,"at_subcontractor":0,"unexplained":0}]}}
```

`mass_balance` (forward only) answers "where is every recorded quantity now" from the stock
ledger, per lot: `declared_qty` (the lot quantity) = `used_in_lots` (production, butchery, rework;
followed in the lots made from it) + `shipped` (tier 1) + `sold` (POS) + `wasted` + `in_stock` (all
warehouses, `at_subcontractor` included) − `returned`. Transfers cancel out. `unexplained` is what
no document explains: a stock-count adjustment (`count_diff`) or a lot quantity that was never
booked in; `balanced` is false when any lot has one. `totals` sums the final positions per unit
(`at_customers` = shipped − returned). The assessment report carries the same block in `trace`.

`GET /recall` - recalls with affected lots and the per-customer call-down list (address,
`second_tier` = `RECORDED` / `NOT_RECORDED`). `lock_enforced` is true while the recall is OPEN and
its lots are RECALLED. Empty list when there is no recall.

`POST /recall` (session) - start a recall (blocks the root and every lot made from it in one
transaction); the initiator is the signed-in supervisor:
```json
{"recall_code":"RCL-2026-001","trigger_lot":"ET-2026-001","reason":"Quality problem"}
```
`201` with `recall_summary`; `404 LOT_NOT_FOUND` for an unknown lot. `trigger_lot` is required
(`422 INVALID_INPUT`); a reused recall code returns `409 CONFLICT`. Only `CLASS_I` is currently
supported. POS terminals poll while the app is open (normally every 15 seconds); the legacy
`broadcast_to_pos` field does not enable push notifications. Failed detail/contact queries
return an error rather than an incomplete recall list.

`GET /recall/contacts.csv?recall_code=RCL-2026-001` - the contact list as CSV (UTF-8 with BOM).

`GET /recall/progress?recall_code=...` - customer/lot recovery lines and totals **per unit**.
Each line includes `qty_received`, `qty_forwarded`, `qty_to_recover`, `qty_returned`,
`qty_pending`, `contact_status`, `note`, `updated_by`, `updated_at`, and `quantity_warning`.
Responsibility is received minus recorded onward shipment; pending subtracts physical returns
from that responsibility. A 30-pack tier-1 delivery followed by 10 packs to tier 2 is 20 + 10,
not 40 to recover. Quantities come from recorded shipments and the stock ledger. Contact
confirmation never changes quantities or unblocks sales. Inconsistent historical quantities
are flagged and cannot produce `all_recovered: true`; no deliveries also means false.

`POST /recall/followup` (session):
```json
{"recall_code":"RCL-2026-001","customer_code":"MKT-A","contact_status":"CONTACTED","note":"Called branch manager"}
```
`contact_status` is `PENDING`, `CONTACTED` or `CONFIRMED`; note max 500 characters.
The customer must have a recorded affected delivery. Closed recalls reject updates (`409`);
unknown recall/unaffected customer returns `404`. Each change records the supervisor and an
append-only audit entry. Record actual recovered goods with `POST /movements/return` into
`KARANTINA`, not by changing contact status.

`GET /lots` - stock per lot and warehouse with status, sale price (VKP) and expiry date.

`GET /warehouses` - code, name and type (`MAIN`, `COLD`, `STORE`, `QUARANTINE`, `SUBCONTRACTOR`).

## Shipments to customers

`POST /shipments` (session) - one delivery note. **Tier 1** (`from_warehouse`, default `ANA-DEPO`)
leaves our stock: each line posts a `SHIPMENT` movement through the ledger triggers, so recalled,
blocked or expired lots and more than the stock are refused. Quarantine and subcontractor
warehouses cannot ship to customers. An omitted `unit_price` uses the product's VKP.
```json
{"shipment_no":"SVK-2026-216","customer_code":"MKT-A","from_warehouse":"ANA-DEPO","ship_date":"2026-10-30",
 "delivery_note_no":"IRS-SVK-216","lines":[{"lot_no":"SUC-2026-046","qty":5}]}
```
**Tier 2** (`from_customer_code` instead of `from_warehouse`) records an onward delivery a customer
reported (e.g. Market C -> restaurant). It moves none of our stock and is capped at what that
customer received minus its earlier onward deliveries and returns (`409 EXCEEDS_DELIVERED`). It
may be recorded after a recall: forward trace, `GET /recall` and `/recall/progress` read shipments
live, so the next buyer joins the call-down list as `RECORDED`. Nothing is inferred for onward
deliveries that were not recorded.
```json
{"shipment_no":"MKC-IRS-7790","customer_code":"REST-01","from_customer_code":"MKT-C","ship_date":"2026-10-30",
 "lines":[{"lot_no":"SUC-2026-045","qty":5}]}
```
`201` with the stored shipment; `422 INVALID_INPUT` with per-field `details` (both sources given,
future `ship_date`, a date before the lot existed, fractional packs for a non-weight unit,
duplicate lot); `404` unknown customer / warehouse / lot; `409 CONFLICT` for a used shipment
number. One transaction: a refused line leaves nothing behind. The `SHIPMENT` movement is booked
under the signed-in supervisor.

`GET /shipments[?shipment_no=]` - shipments in recording order with `tier`, customer, source
(`from_warehouse` or `from_customer_code`/`from_customer_name`) and lines (lot, product, qty,
unit, `unit_price`, `line_total`; both `null` when no price was recorded).

`GET /customers` - code, name, type and the contact data used by the recall list.

## Raw-material receipt

`POST /purchases` (session) - one supplier delivery; each line becomes a PURCHASE lot with supplier,
supplier batch, date, quantity, price and expiry date, plus a `PURCHASE_IN` movement.
```json
{"receipt_no":"ALS-2026-200","supplier_code":"TED-HAY-01","receipt_date":"2026-10-27","invoice_no":"FTR-HAY-0050",
 "lines":[{"sku":"KZ-KARKAS","lot_no":"KUZU-2026-002","supplier_lot_no":"KUPE-TR-10-445900",
           "qty":24,"unit_price":11.8,"expiry_date":"2026-11-03"}]}
```
`201` with the receipt and total; `422 INVALID_INPUT` with per-field `details`
(e.g. `{"lines[0].expiry_date":"required: the product has a shelf life"}`); `404` unknown
supplier / product; `409 CONFLICT` for a used receipt or lot number. The database refuses a
purchased perishable lot without expiry as well (`EXPIRY_REQUIRED`).

## Reports and lot movements

`GET /reports/assessment` returns the complete assessment evidence as one JSON snapshot;
the dashboard renders it as a downloadable PDF. Default selectors are
`order_no=URT-2026-045`, `root_lot=ET-2026-001`, `rejected_op=PRC-2026-000`,
`posted_op=PRC-2026-001`. All may be overridden with query parameters. A required record
missing from the snapshot returns `404` rather than a partial report. Includes production
costs/breakdown, both butchery findings/allocations, forward/backward trace, recorded customer
deliveries/contact details, recall recovery lines, model/reference metrics and sample predictions.
The report never initiates a recall or books a stock movement. ML evidence is labelled synthetic.

| Endpoint | Content |
|---|---|
| `GET /purchases` | receipt, date, supplier, lot, supplier lot, qty, unit price, line total, expiry |
| `GET /movements[?lot_no=]` | stock ledger with `balance_after` per lot and warehouse; 404 for a lot without movements |
| `GET /sales` | POS sale lines with revenue, lot cost and gross profit |

Example `GET /movements?lot_no=KZB-2026-001` row:
```json
{"moved_at":"2026-10-13T09:45:00Z","lot_no":"KZB-2026-001","warehouse_code":"ANA-DEPO","movement_type":"BUTCHERY_OUTPUT",
 "qty":6.5,"uom_code":"KG","unit_cost":14.7092,"balance_after":6.5,"ref_type":"butchery_operation","note":"PRC-2026-001"}
```

Recording movements (all go through the ledger triggers: sign check, no negative stock, recalled
lots cannot be consumed). Each returns `201` with the balances after the movement. Lots counted in
packs or pieces move in whole units only (`422 INVALID_INPUT` for 1.5 packs); kg may be fractional.
The dashboard's *Lot movements → Record movement* form uses transfer, waste and rework.

| Endpoint | Body |
|---|---|
| `POST /movements/transfer` | `{"lot_no","from_warehouse","to_warehouse","qty","note"}` |
| `POST /movements/return` | `{"lot_no","warehouse","customer_code","qty","note"}` (counted as returned in the recall report) |
| `POST /movements/waste` | `{"lot_no","warehouse","qty","reason"}` |
| `POST /rework` | `{"input_lot_no","warehouse","qty_consumed","output_lot_no","output_qty","note"}` (new REWORK lot linked in `lot_link`) |

### Stock count

`POST /stock-counts` (session) - what was counted on the shelf of one warehouse:
```json
{"count_no": "SAY-2026-001", "warehouse": "ANA-DEPO", "note": "Month end",
 "lines": [{"lot_no": "SUC-2026-046", "counted_qty": 5, "expected_qty": 6, "reason": "Torn pack found"},
           {"lot_no": "KZO-2026-001", "counted_qty": 5, "expected_qty": 5}]}
```
`count_no` defaults to `SAY-<UTC timestamp>`. The system quantity is the ledger balance when the
count is saved (the balance row is locked). Each difference becomes an `ADJUSTMENT_IN` /
`ADJUSTMENT_OUT` movement (`ref_type` `stock_count`) with its reason; a line without difference
is kept as evidence. All lines or none: `422` for a difference without `reason` or a fraction of
a pack, `409 CONFLICT` when a lot's balance is no longer `expected_qty` (the counter saw an old
list) or for a used `count_no`, `404` for an unknown lot or warehouse. `201` with each line's
`system_qty`, `counted_qty`, `diff_qty`, `value_eur` (difference × lot cost) and `movement_type`,
plus `shortage_eur` / `surplus_eur`. A shortage above 10 € raises a `COUNT_SHORTAGE` alert
(critical from 100 € and always for a recalled or blocked lot). In the mass balance a count
difference is an undocumented gap.

`GET /stock-counts` - the latest 20 counts with their lines.

## POS sync (SQLite -> MySQL)

Customer returns through `/movements/return` cannot exceed recorded deliveries for the same
customer and lot minus recorded onward shipments and earlier returns, across all warehouses
(`409 CONFLICT`). Concurrent
returns are serialized on the lot row. Unrecorded deliveries must be investigated before
stock can be restored as a customer return.

`POST /sync` - signed outbox batch from the Android POS. Each item is verified (ECDSA P-256), then
applied once: `sync_inbox` (UUIDv7 primary key) + `sale` + `sale_line` in one transaction; the
sale line trigger writes the SALE stock movement. Replays are acknowledged without inserting.
`acknowledged_ids` is always an array, including `[]` when no item was accepted. The POS only
clears explicitly acknowledged IDs from its submitted batch; a missing, malformed or
contradictory acknowledgement keeps the batch pending for retry.
```json
{"processed_count":1,"acknowledged_ids":["019a3000-..."],"rejected":[
  {"id":"019a3000-...","code":"LOT_BLOCKED","message":"Bu lot geri çağrıldı veya bloke. Satış engellendi."}],
 "status":"PARTIAL","signature_verified":true}
```
Only acknowledged ids are removed from the device queue. `rejected` codes `INSUFFICIENT_STOCK`,
`LOT_BLOCKED`, `LOT_EXPIRED`, `INVALID_INPUT`, `CONFLICT` are permanent (the POS stops retrying and shows the reason);
`DB_ERROR` is retried; `REPLAY_MISMATCH` (a known UUIDv7 with different data) is permanent too.

Security: the envelope header `X-Signature` (ECDSA over `tenant:device:role:uuid:body`) is
required. The device's key is pinned in `pos_device_key` on its first signed sync (the app sends
`X-POS-Public-Key`); later batches signed with another key get `403 KEY_MISMATCH`, an unknown
device without a key `403 DEVICE_NOT_ENROLLED`. With `POS_ENROLLMENT=closed` only keys provisioned
by a supervisor via `POST /sync/register-key` (session) `{"device_id","public_key_b64"}` are
accepted; the same call rotates a key after a reinstall.

## Supervisor unlock

`POST /auth/supervisor/verify` - `{"pin","device_id","lot_no","recall_code"}`. bcrypt check against
`supervisor.pin_hash`; `401 INVALID_PIN` with `details.attempts_left`; after 5 wrong PINs per
device (or 10 per client IP) `429 LOCKED_OUT` for 5 minutes. Every attempt is written to `audit_log`.

`GET /auth/supervisor/audit` - the latest unlock attempts (who, when, device, lot, recall).

## AI (spec section 4)

`GET /ai/summary` - metrics (MAE, MAPE on the time-split test period of the synthetic data, for the
model and the baselines) and, per record in this database and target, the actual value, model and
baseline predictions and their deviations (abs and %). Each comparison has an `origin`: `SEED`
(seed.sql), `DEMO` (entered in a demo deployment) or `FIELD` (entered while the API runs with
`DATA_ORIGIN=FIELD`; not verified). `records` counts the documents per origin,
e.g. `{"SEED":3,"DEMO":1,"FIELD":0}`. Optional per comparison (absent when the row has none):
`interval_low` / `interval_high` / `interval_level` (80 % conformal interval of `model_pred`, same
unit), `anomaly` (actual outside the 98 % band; not for `ekp`) and `outside_interval` (actual
outside the 80 % interval). See [AI.md](AI.md). Orders recorded through `POST /production/orders`
appear here too: with predictions only until they are received, then with actuals.

`GET /yield` - raw `v_ml_prediction_deviation` rows (kept for compatibility).

## Alerts (spec section 5)

The API writes an alert when a production order is received with a yield outside the forecast's
98 % band, when a butchery has unexpected waste or part weights, when a purchase price is more than
10 % above the product's last three purchases, and when `/sync` refuses a POS sale of a recalled,
blocked or expired lot. The ml-engine batch detector writes the same table. An alert never changes
stock, cost or lot status. Details: [AI.md](AI.md#live-alerts-and-their-review).

`GET /alerts[?status=open|closed|all]` (default `all`; `open` = `OPEN` + `ACKNOWLEDGED`) - up to 200
alerts, open first, critical first, newest first, plus counts and the review outcome per check:

```json
{"alerts": [{"id": 7, "task": "PURCHASE_COST", "severity": "CRITICAL", "direction": "COST_INCREASE",
  "status": "OPEN", "ref_type": "purchase_receipt_line", "ref_no": "ALS-2026-130", "ref_date": "2026-10-30",
  "lot_no": "BAH-2026-004", "uom_code": "KG", "responsible": "Örnek Baharat Ltd.",
  "expected_value": 24.333, "actual_value": 31, "deviation": 6.667, "est_loss_eur": 66.67,
  "score": 27.397, "threshold_warn": 10, "threshold_crit": 25, "detector": "rule", "model_version": "rule-1",
  "detail": "ref 24.33 €/kg, 3 alış", "message": "Alış fiyatı arttı: ...", "created_at": "..."}],
 "open": 1, "critical_open": 1,
 "feedback": [{"task": "PURCHASE_COST", "model_version": "rule-1", "alerts": 1, "open_alerts": 1,
   "confirmed": 0, "false_alarms": 0, "precision_observed": null, "confirmed_loss_eur": 0}]}
```

`task` / `direction`: `PRODUCTION_YIELD` (`LOW_YIELD`, `HIGH_YIELD`), `BUTCHERY_YIELD` (`FIRE_HIGH`,
`PART_MISMATCH`), `PURCHASE_COST` (`COST_INCREASE`), `LOT_MOVEMENT` (`BLOCKED_SALE`, `EXPIRED_SALE`:
`event_ref` is the sale's UUIDv7, `detail` the device and sale time; `COUNT_SHORTAGE`: `ref_no` is the
stock count, expected = ledger, actual = counted). Values are in the unit of the check: packs, kg,
€ per unit or the lot's unit. `422` for an unknown `status`.

`POST /alerts/review` (supervisor session) - `{"id": 7, "status": "ACKNOWLEDGED" | "RESOLVED" | "FALSE_ALARM",
"note": "..."}`. `RESOLVED` means a real problem. Closing needs a `note` (max 255). The operator and
time are stored and the change is written to `audit_log`. `404` unknown alert, `409 CONFLICT` for
an alert already closed, `422` invalid status or missing note.

`GET /alerts` also returns `missed` (problems found by hand per check: `task`, `findings`,
`est_loss_eur`) and `missed_recent` (the latest 20).

`GET /alerts/rules` - the thresholds in force and their defaults, the synthetic stress test
(`calibration`, the content of `ml-engine/artifacts/ali/live_alert_calibration.json`) and the price
volatility observed in the recorded purchases:
```json
{"rules": [{"rule": "BUTCHERY_YIELD", "unit": "band98", "warn": 1.5, "crit": 2, "default_warn": 1.5,
            "default_crit": 2, "updated_by": "default"}, "..."],
 "observed_price_changes": {"changes": 6, "p95_increase_pct": 4.2, "above_warn_pct": 0},
 "calibration": {"production_yield": {"curve": [{"threshold": 1.0, "precision": 1.0, "recall": 0.644,
                 "false_alarms_per_100": 0}, "..."]}, "butchery_yield": {}, "purchase_cost": {}}}
```
Units: `band98` = multiple of the 98 % band (`PRODUCTION_YIELD`, `BUTCHERY_YIELD`), `pct` = % above
the reference price (`PURCHASE_COST`), `eur` = shortage value (`COUNT_SHORTAGE`).

`POST /alerts/rules` (session) - `{"rule": "PURCHASE_COST", "warn": 15, "crit": 30}`. `warn > 0`,
`crit > warn`, `crit` at most 10 bands / 500 % / 1,000,000 €; otherwise `422`. New alerts are graded
with the new values (each alert stores the thresholds it was graded with); the change is written to
`audit_log` (`THRESHOLD_CHANGE`).

`POST /alerts/missed` (session) - a problem found by hand that no alert flagged:
`{"task": "BUTCHERY_YIELD", "ref_no": "PRC-2026-003", "lot_no": "KUZU-2026-003", "note": "...",
"est_loss_eur": 8.5}`. `task` is the check that should have caught it (`PRODUCTION_YIELD`,
`BUTCHERY_YIELD`, `PURCHASE_COST`, `LOT_MOVEMENT`); `note` is required. `201` with its `id`, `404`
for an unknown lot, `422` otherwise; audited (`MISSED_FINDING`). It counts against the check's recall.
