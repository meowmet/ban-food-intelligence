# FEFO, recovery, assessment PDF, alerts, mass balance and stock count

## Use

**Android → Sell:** available lots are ordered by product and expiry. The earliest-expiring
sellable lot for each SKU has a **Sell first · FEFO** badge. Expired/recalled/blocked/exhausted
lots cannot be recommended. Unknown dates sort last; unknown SKU identity is not guessed.
The cashier may still choose another eligible lot. The same logic works on the cached catalog
and subtracts local pending sales before choosing the recommendation.

**Dashboard → Trace & recall:** expand **Recovery and customer follow-up** on a recall.
Each customer/lot has received, forwarded, returned and pending quantities. Contact statuses
are **Not contacted / Contacted / Customer confirmed**. Save a contact note with the supervisor
PIN. These notes are audited and do not move stock.

Use **Record physical return** only after receiving goods; this posts the actual return into
quarantine. The API caps it at received minus onward shipments minus earlier returns. If
Market C received 30 packs and forwarded 10 to the recorded restaurant, its responsibility is
20 and the restaurant's is 10. Totals are separate for kg/packs and never count the onward
delivery twice. A completed recovery does not release lot sale blocks. Unrecorded onward
sales cannot be inferred from the contact confirmation or from AI.

**Dashboard → Overview → Assessment report (PDF):** one click downloads a PDF with:

- URT-2026-045 material/service cost, actual packs, EKP, VKP, margin and cost breakdown;
- rejected PRC-2026-000 with both the 63.50 EUR and 3 kg findings, then PRC-2026-001 weights
  and actual cost allocation;
- ET-2026-001 forward lot chain, the production output's backward suppliers, recorded market
  deliveries/contacts and any recorded recall recovery progress;
- model/reference metrics and sample predictions, explicitly labelled synthetic data.

The API reads the report on a single repeatable-read database snapshot. Export never starts a
recall or changes stock. A missing scenario record or unavailable API produces a visible error
instead of an incomplete PDF. Turkish text and the Euro sign use bundled OFL Noto Sans fonts;
no customer data is sent to a PDF service. Default scenario IDs can be changed through the
API query parameters documented in API.md.

**Dashboard → Alerts and the sidebar bell:** the bell counts open alerts (with the critical
ones), lots with 3 days or less to expiry and active recalls; each line opens its tab. Alerts are
written when a record is saved: a received order outside the yield forecast's 98 % band, a
butchery with unexpected waste or part weights, a purchase price more than 10 % above the
product's last three purchases, and a POS sale of a recalled or expired lot that the server
refused (the goods may already be with a customer). **Review** with the supervisor PIN marks an
alert under review or closes it as a real problem or a false alarm (note required, audited).
Closed alerts give the observed precision and confirmed loss per check. Alerts never change
stock, cost or lot status. To see one in the demo, record a spice receipt above 24.00 €/kg in
*Purchases*, or receive an order with far fewer packs than forecast.

**Alerts → accuracy and thresholds:** per check, the reviewed alerts give the precision and the
problems found by hand (**Record a missed problem**) the recall, each with its 95 % interval. The
**Alert thresholds** section lists each check's warning and critical value, what that value did in
the synthetic stress test (precision, recall, false alarms per 100 records; for prices, by price
volatility, next to the volatility observed in the recorded purchases) and who changed it last.
**Change** shows the test result for a typed value before saving with the supervisor PIN; new alerts
use it, older ones keep the values they were graded with. Background: docs/AI.md, *Calibrating the
live alerts without field data*.

**Dashboard → Trace & recall → Lot flow and Mass balance (forward trace):** the diagram draws the
lots by depth, the customers by distribution tier and the stock, till, waste and gap positions,
each arrow with its recorded quantity; a lot box opens the lot card, hover highlights its arrows.
The mass balance shows, per lot, where its quantity went (used in other lots, shipped, sold,
wasted, returned, in stock) and the undocumented gap, plus the final positions per unit: for
ET-2026-001, 100 kg went into URT-2026-045 and the 85 packs are at Markets A/B/C. A stock-count
write-off appears as a gap, because no document says where the goods went. The assessment PDF
carries the same table.

**Dashboard → Lots & stock → Count stock:** pick a warehouse, type what is on the shelf for the
lots you counted (empty rows are not counted), give a reason for every difference and save with
the supervisor PIN. Each difference is booked as a count adjustment; *Recent counts* lists the
counts with their differences, reasons and value. If a lot's stock changed after the list was
opened (e.g. a till sale synced), the count is refused and the list refreshes. A shortage above
10 € (or any shortage of a recalled lot) raises an alert.

## Existing installation

New installations use schema.sql normally. Existing databases need the additive/procedure
migrations, preserving records (Git Bash example from the repository root):

```bash
docker compose exec -T mysql mysql -uroot -pban --comments < database-mysql/migrations/20261029_production_dispatch_guard.sql
docker compose exec -T mysql mysql -uroot -pban --comments < database-mysql/migrations/20261029_recall_followup.sql
docker compose exec -T mysql mysql -uroot -pban --comments < database-mysql/migrations/20261030_prediction_origin.sql
docker compose exec -T mysql mysql -uroot -pban --comments < database-mysql/migrations/20261030_alert_inbox.sql
docker compose exec -T mysql mysql -uroot -pban --comments < database-mysql/migrations/20261030_stock_count.sql
docker compose exec -T mysql mysql -uroot -pban --comments < database-mysql/migrations/20261031_alert_rules.sql
docker compose exec -T mysql mysql -uroot -pban --comments < database-mysql/migrations/20261031_frozen_butchery_forecast.sql
docker compose up -d --build api-go web-react
```

Rebuild/install the Android APK to get the FEFO badge. Updating the web/API does not update a
phone's installed app. No app installation or public release is performed by the test suite.

## Validation

- Go integration: 30 → 20 + 10 tier responsibility, contact vs physical return, operator
  authentication, append-only audit, closed-recall writes, return cap, report evidence,
  missing records, no stock side effects and a concurrent-change snapshot test.
- Android: five FEFO tests plus the existing validation/security tests; lint and debug build.
- Browser: real PDF download, report failure without download, contact acknowledgement and
  physical return through the actual forms.
- `python scripts/verify-assessment-pdf.py`: extracts the exported PDF with pypdf and checks
  Turkish text, the required amounts, lots and market names. Optional `--preview` also needs
  PyMuPDF and renders two preview PNGs next to the PDF.
- CI runs the browser checks on Chromium with seeded MySQL/API and uploads the PDF and screenshots.

Local browser test (requires a seeded test API, does write a recall/return in that database):

```bash
cd web-react
E2E_API_BASE=http://127.0.0.1:18080 npm run test:e2e
# On Windows use E2E_BROWSER_CHANNEL=msedge to use an installed Edge browser.
```
