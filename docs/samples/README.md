# Sample records

Exported from the running API on the clean demo seed by `./scripts/export-samples.sh`
(which restores the seed afterwards). Field meanings: [docs/API.md](../API.md).
Dates follow the seed's original calendar (20.09–15.10.2026), not the day of the export.

| File | Content |
|---|---|
| `lots-stock.json` | every lot per warehouse: quantity, unit, unit cost, status, sale price, expiry |
| `purchases.json` | raw-material receipts: supplier, supplier batch, date, quantity, price, expiry |
| `production-orders.json` | URT-2026-045 (85 packs, EKP 18.16 €) and URT-2026-046 (83 of 85 planned, variance −2) with cost breakdown, VKP, profit, margin |
| `butchery-operations.json` | PRC-2026-000 (the spec image: rejected, 63.50 € and 3 kg findings) and PRC-2026-001 (posted, balanced) |
| `butchery-allocation-value.json`, `butchery-allocation-quantity.json` | PRC-2026-001's 287.50 € split by value and by quantity, both summing exactly |
| `lot-movements.json` | the stock ledger (purchase, transfer, production, butchery, shipment, sale) |
| `pos-sales.json` | POS sale lines with lot, quantity, price and stock movement |
| `trace-forward-ET-2026-001.json` | ET-2026-001 → SUC-2026-045 → Market A 30 / B 25 / C 30, tier 2 Örnek Lokanta 10 |
| `trace-backward-SUC-2026-045.json` | SUC-2026-045 → raw lots, suppliers, URT-2026-045 |
| `recall-ET-2026-001.json` | result of `POST /recall` for ET-2026-001: blocked lots and per-customer list |
| `recall-ET-2026-001-contacts.csv` | the recall call-down list (contacts, quantities, tier, second-tier status) |
| `ai-summary.json` | AI predictions vs actuals and model vs baseline errors |

Customer and supplier contacts are fictitious (`.example` addresses).
