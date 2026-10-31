#!/usr/bin/env bash
# Exports the main seed records as JSON/CSV into docs/samples/ from the running API,
# including the recall result for ET-2026-001, then restores the clean seed.
# The export uses the seed's original calendar (20.09-15.10.2026), so the dates in the
# samples do not depend on the export day.
#
#   ./scripts/export-samples.sh        (needs the compose stack; demo PIN from the README)
set -euo pipefail
cd "$(dirname "$0")/.."
API=${API:-http://localhost:8080}
PIN=${DEMO_SUPERVISOR_PIN:-246813}
OUT=docs/samples
mkdir -p "$OUT"
PY=$(command -v python3 || command -v python)
pretty() { "$PY" -c "import json,sys; json.dump(json.load(sys.stdin), sys.stdout, ensure_ascii=False, indent=2); print()"; }
get() { curl -fsS -H 'Accept-Language: en' "$API$1" | pretty > "$OUT/$2"; echo "  $OUT/$2"; }

SEED_SHIFT=0 ./scripts/demo-reset.sh >/dev/null 2>&1
echo ">> exporting seed records"
get "/lots" lots-stock.json
get "/purchases" purchases.json
get "/production" production-orders.json
get "/butchery" butchery-operations.json
get "/butchery/allocation?op_no=PRC-2026-001&method=VALUE" butchery-allocation-value.json
get "/butchery/allocation?op_no=PRC-2026-001&method=QUANTITY" butchery-allocation-quantity.json
get "/movements" lot-movements.json
get "/sales" pos-sales.json
get "/trace/ET-2026-001?direction=forward" trace-forward-ET-2026-001.json
get "/trace/SUC-2026-045?direction=backward" trace-backward-SUC-2026-045.json
get "/ai/summary" ai-summary.json

echo ">> recall of ET-2026-001 (supervisor session)"
TOKEN=$(curl -fsS -X POST "$API/auth/session" -d "{\"pin\":\"$PIN\"}" | "$PY" -c "import json,sys; print(json.load(sys.stdin)['token'])")
curl -fsS -X POST "$API/recall" -H "Authorization: Bearer $TOKEN" -H 'Accept-Language: en' \
  -d '{"recall_code":"RCL-2026-001","trigger_lot":"ET-2026-001","reason":"Quality issue found in meat lot ET-2026-001"}' \
  | pretty > "$OUT/recall-ET-2026-001.json"
echo "  $OUT/recall-ET-2026-001.json"
curl -fsS "$API/recall/contacts.csv?recall_code=RCL-2026-001" > "$OUT/recall-ET-2026-001-contacts.csv"
echo "  $OUT/recall-ET-2026-001-contacts.csv"

./scripts/demo-reset.sh >/dev/null 2>&1
echo ">> clean seed restored"
