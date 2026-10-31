#!/usr/bin/env bash
# Restores the clean demo database (schema.sql + seed.sql) in the running compose stack
# and restarts the API so cached state is dropped. This is the only supported reset.
#
#   ./scripts/demo-reset.sh
#   SEED_SHIFT=0 ./scripts/demo-reset.sh   (original calendar 20.09-15.10.2026 instead of one ending yesterday)
#
# Uses `docker compose` by default; set COMPOSE="podman compose" for Podman.
set -euo pipefail
cd "$(dirname "$0")/.."
COMPOSE=${COMPOSE:-"docker compose"}
MYSQL="$COMPOSE exec -T mysql mysql -uroot -pban --default-character-set=utf8mb4"
SEED=database-mysql/seed.sql

echo ">> reloading food_intel from database-mysql/schema.sql + seed.sql"
$MYSQL -e "DROP DATABASE IF EXISTS food_intel" 2>&1 | grep -v "Using a password" || true
$MYSQL < database-mysql/schema.sql 2>&1 | grep -v "Using a password" || true
if [ -n "${SEED_SHIFT:-}" ]; then
  sed "s/^SET @seed_shift := .*/SET @seed_shift := $SEED_SHIFT;/" "$SEED" | $MYSQL 2>&1 | grep -v "Using a password" || true
else
  $MYSQL < "$SEED" 2>&1 | grep -v "Using a password" || true
fi

echo ">> restarting api-go"
$COMPOSE restart api-go >/dev/null

for _ in $(seq 1 30); do
  if curl -fsS http://localhost:8080/health 2>/dev/null | grep -q CONNECTED; then break; fi
  sleep 1
done

echo ">> demo path check"
$MYSQL food_intel -N -e "
  SELECT CONCAT('ET-2026-001 -> ', po.order_no, ' -> ', l.lot_no, ' (', CAST(po.actual_qty AS UNSIGNED), ' paket)')
    FROM production_order po JOIN production_output pout ON pout.production_order_id = po.id
    JOIN lot l ON l.id = pout.lot_id WHERE po.order_no = 'URT-2026-045';
  SELECT CONCAT(c.name, ': ', CAST(sl.qty AS UNSIGNED)) FROM shipment_line sl
    JOIN shipment s ON s.id = sl.shipment_id JOIN customer c ON c.id = s.customer_id
    JOIN lot l ON l.id = sl.lot_id WHERE l.lot_no = 'SUC-2026-045' AND s.from_customer_id IS NULL ORDER BY c.code;
  SELECT CONCAT('recalls: ', COUNT(*)) FROM recall;" 2>&1 | grep -v "Using a password"
echo ">> clean seed restored"
