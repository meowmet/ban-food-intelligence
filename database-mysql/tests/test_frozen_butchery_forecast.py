"""Dondurulmuş kasap tahmini: parçalamadan önce kaydedilir, sonra değişmez, karkasın işlemine bir kez bağlanır."""
import pymysql
import pytest

from conftest import query


def _exec(conn, sql, params=None):
    with conn.cursor() as cur:
        cur.execute(sql, params)
        return cur.lastrowid


@pytest.fixture(scope="module")
def frozen(db):
    """Parçalanmamış bir karkas lotu ve tek parçalı dondurulmuş tahmini (geçmiş bir zaman damgası denenir)."""
    lot = _exec(db, "INSERT INTO lot (lot_no, product_id, origin_type, supplier_id, lot_date, expiry_date, initial_qty, uom_code, unit_cost) "
                    "SELECT 'KZ-TEST-FZ', p.id, 'PURCHASE', s.id, '2026-10-28', '2026-11-04', 25, 'KG', 11.5 "
                    "FROM product p, supplier s WHERE p.sku = 'KZ-KARKAS' AND s.code = 'TED-HAY-01'")
    fc = _exec(db, "INSERT INTO butchery_forecast (input_lot_id, input_weight_kg, op_date, frozen_by, frozen_at) "
                   "VALUES (%s, 25, '2026-10-28', 'py', '2020-01-01 00:00:00')", (lot,))
    _exec(db, "INSERT INTO butchery_forecast_part (forecast_id, model_name, model_version, target_name, predicted_value) "
              "VALUES (%s, 'ali_ridge', 'ali-1.1.0', 'but_kg', 6.5)", (fc,))
    return {"lot": lot, "forecast": fc}


def test_frozen_at_is_the_server_time_not_the_callers(db, frozen):
    row = query(db, "SELECT frozen_at > NOW(3) - INTERVAL 1 MINUTE AS now FROM butchery_forecast WHERE id = %s", (frozen["forecast"],))
    assert row == [{"now": 1}]


def test_one_frozen_forecast_per_carcass(db, frozen):
    with pytest.raises(pymysql.err.IntegrityError, match="Duplicate entry"):
        _exec(db, "INSERT INTO butchery_forecast (input_lot_id, input_weight_kg, op_date, frozen_by) "
                  "VALUES (%s, 24, '2026-10-28', 'py')", (frozen["lot"],))


def test_a_cut_carcass_cannot_get_a_forecast(db):
    with pytest.raises(pymysql.err.OperationalError, match="FORECAST_TOO_LATE"):
        _exec(db, "INSERT INTO butchery_forecast (input_lot_id, input_weight_kg, op_date, frozen_by) "
                  "SELECT id, 24, '2026-10-28', 'py' FROM lot WHERE lot_no = 'KUZU-2026-001'")


@pytest.mark.parametrize("sql", [
    "UPDATE butchery_forecast SET input_weight_kg = 24 WHERE id = %s",
    "UPDATE butchery_forecast SET frozen_at = '2020-01-01' WHERE id = %s",
    "DELETE FROM butchery_forecast WHERE id = %s",
    "UPDATE butchery_forecast_part SET predicted_value = 6.9 WHERE forecast_id = %s",
    "DELETE FROM butchery_forecast_part WHERE forecast_id = %s",
])
def test_frozen_forecast_never_changes(db, frozen, sql):
    with pytest.raises(pymysql.err.OperationalError, match="IMMUTABLE"):
        _exec(db, sql, (frozen["forecast"],))


def test_only_the_operation_of_the_same_carcass_can_be_linked(db, frozen):
    with pytest.raises(pymysql.err.OperationalError, match="another carcass"):
        _exec(db, "UPDATE butchery_forecast SET operation_id = (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001') "
                  "WHERE id = %s", (frozen["forecast"],))


def test_linked_once_then_closed(db, frozen):
    op = _exec(db, "INSERT INTO butchery_operation (op_no, recipe_id, input_lot_id, species, input_weight_kg, purchase_unit_price, "
                   "allocation_method, op_date, status) SELECT 'PRC-TEST-FZ', id, %s, species, 25, 11.5, 'VALUE', '2026-10-28', 'POSTED' "
                   "FROM butchery_recipe WHERE code = 'REC-KUZU-STD'", (frozen["lot"],))
    _exec(db, "UPDATE butchery_forecast SET operation_id = %s WHERE id = %s", (op, frozen["forecast"]))
    assert query(db, "SELECT operation_id FROM butchery_forecast WHERE id = %s", (frozen["forecast"],)) == [{"operation_id": op}]
    for sql, params in [
        ("UPDATE butchery_forecast SET operation_id = NULL WHERE id = %s", (frozen["forecast"],)),
        ("INSERT INTO butchery_forecast_part (forecast_id, model_name, model_version, target_name, predicted_value) "
         "VALUES (%s, 'ali_ridge', 'ali-1.1.0', 'sirt_kg', 3.5)", (frozen["forecast"],)),
    ]:
        with pytest.raises(pymysql.err.OperationalError, match="IMMUTABLE"):
            _exec(db, sql, params)
