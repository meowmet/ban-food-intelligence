"""SKT: süresi geçmiş lot satılamaz ve sevk edilemez; demo takvimi yüklendiği güne göre kayar."""
from decimal import Decimal as D

import pymysql
import pytest

from conftest import query


def _exec(conn, sql, params=None):
    with conn.cursor() as cur:
        cur.execute(sql, params)


def _sale(conn, sale_uuid, lot_no, qty, sold_at_sql):
    _exec(conn, "INSERT INTO sale (sale_uuid, device_id, warehouse_id, sold_at) "
                f"SELECT %s, 'POS-TEST', w.id, {sold_at_sql} FROM warehouse w WHERE w.code='MAGAZA-01'",
          (sale_uuid,))
    _exec(conn, "INSERT INTO sale_line (sale_id, line_no, lot_id, qty, unit_price) "
                "SELECT s.id, 1, l.id, %s, 16 FROM sale s, lot l WHERE s.sale_uuid = %s AND l.lot_no = %s",
          (qty, sale_uuid, lot_no))


def test_seed_calendar_is_relative_to_today(db):
    days = {r["lot_no"]: r["d"] for r in query(
        db, "SELECT lot_no, DATEDIFF(expiry_date, CURRENT_DATE) d FROM lot "
            "WHERE lot_no IN ('KZB-2026-001', 'KZY-2026-001', 'SUC-2026-046')")}
    assert days == {"KZB-2026-001": 2, "KZY-2026-001": -1, "SUC-2026-046": 88}
    latest = query(db, "SELECT DATEDIFF(CURRENT_DATE, MAX(sold_at)) d FROM sale")[0]["d"]
    assert latest == 1                                  # son seed satışı dün, gelecekte kayıt yok


def test_seed_movements_carry_document_time(db):
    # prosedür/tetikleyici hareketleri yükleme anına düşerse belge tarihli transfer ve satıştan
    # sonra sıralanır; hareket raporundaki ara bakiye (moved_at, id sırası) eksiye iner
    latest = query(db, "SELECT DATEDIFF(CURRENT_DATE, MAX(moved_at)) d FROM stock_movement")[0]["d"]
    assert latest == 1
    negative = query(db, "SELECT lot_no, warehouse_id, moved_at, bal FROM ("
                         " SELECT l.lot_no, sm.warehouse_id, sm.moved_at, SUM(sm.qty) OVER ("
                         "   PARTITION BY sm.lot_id, sm.warehouse_id ORDER BY sm.moved_at, sm.id) bal"
                         " FROM stock_movement sm JOIN lot l ON l.id = sm.lot_id) x WHERE bal < 0")
    assert negative == []


def test_expired_lot_cannot_be_sold(db):
    with pytest.raises(pymysql.err.OperationalError, match="LOT_EXPIRED"):
        _sale(db, "01000000-0000-7000-8000-00000000e001", "KZY-2026-001", 0.5, "NOW(3)")


def test_sale_made_before_expiry_is_accepted_when_synced_later(db):
    _sale(db, "01000000-0000-7000-8000-00000000e002", "KZY-2026-001", 0.5,
          "(SELECT TIMESTAMP(expiry_date, '18:00:00') FROM lot WHERE lot_no = 'KZY-2026-001')")
    rows = query(db, "SELECT sm.movement_type, sm.qty FROM sale s JOIN sale_line sl ON sl.sale_id = s.id "
                     "JOIN stock_movement sm ON sm.id = sl.stock_movement_id "
                     "WHERE s.sale_uuid = '01000000-0000-7000-8000-00000000e002'")
    assert [(r["movement_type"], r["qty"]) for r in rows] == [("SALE", D("-0.500"))]


def test_expired_lot_cannot_be_shipped_but_can_be_wasted(db):
    _exec(db, "INSERT INTO shipment (shipment_no, customer_id, from_warehouse_id, ship_date) "
              "SELECT 'SVK-TEST-EXP', c.id, w.id, CURRENT_DATE FROM customer c, warehouse w "
              "WHERE c.code='MKT-A' AND w.code='MAGAZA-01'")
    with pytest.raises(pymysql.err.OperationalError, match="LOT_EXPIRED"):
        _exec(db, "INSERT INTO shipment_line (shipment_id, lot_id, qty) SELECT s.id, l.id, 0.5 "
                  "FROM shipment s, lot l WHERE s.shipment_no='SVK-TEST-EXP' AND l.lot_no='KZY-2026-001'")
    _exec(db, "INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, note) "
              "SELECT l.id, w.id, 'WASTE', -0.5, l.unit_cost, 'SKT geçti, imha' FROM lot l, warehouse w "
              "WHERE l.lot_no='KZY-2026-001' AND w.code='MAGAZA-01'")
    assert query(db, "SELECT * FROM v_stock_ledger_check WHERE diff <> 0") == []
