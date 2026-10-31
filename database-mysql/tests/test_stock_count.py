"""Stok sayımı: fark gerekçesiz kaydedilemez, sayılan miktar negatif olamaz, fark sütunu hesaplanır."""
from decimal import Decimal as D

import pymysql
import pytest

from conftest import query


@pytest.fixture(scope="module")
def count_id(db):
    with db.cursor() as cur:
        cur.execute("INSERT INTO stock_count (count_no, warehouse_id, counted_by) "
                    "SELECT 'SAY-PY-1', id, 'pytest' FROM warehouse WHERE code = 'ANA-DEPO'")
        return cur.lastrowid


def _line(db, count_id, system, counted, reason):
    with db.cursor() as cur:
        cur.execute("INSERT INTO stock_count_line (count_id, lot_id, system_qty, counted_qty, reason) "
                    "SELECT %s, id, %s, %s, %s FROM lot WHERE lot_no = 'ET-2026-001'", (count_id, system, counted, reason))


def test_difference_needs_a_reason(db, count_id):
    with pytest.raises(pymysql.err.OperationalError):     # CHECK ck_scl_reason
        _line(db, count_id, 20, 17, None)


def test_counted_quantity_cannot_be_negative(db, count_id):
    with pytest.raises(pymysql.err.OperationalError):     # CHECK ck_scl_counted
        _line(db, count_id, 20, -1, "x")


def test_difference_is_computed_from_the_two_quantities(db, count_id):
    _line(db, count_id, 20, 17, "Ambalaj yırtık")
    row = query(db, "SELECT diff_qty FROM stock_count_line WHERE count_id = %s", (count_id,))[0]
    assert row["diff_qty"] == D("-3.000")
