"""KI anomali uyarıları: ml_anomaly_alert tablosu + API'nin okuduğu v_ml_anomaly_alert / v_ml_anomaly_feedback."""
from decimal import Decimal as D

import pymysql
import pytest

from conftest import DB_DIR, query, run_sql_file

DEMO = DB_DIR / "demo" / "anomaly_demo.sql"


@pytest.fixture(scope="module")
def alerts(db):
    assert query(db, "SELECT COUNT(*) n FROM ml_anomaly_alert")[0]["n"] == 0   # seed kayıtları normal
    run_sql_file(DEMO)
    return db


def test_view_resolves_order_lot_and_subcontractor(alerts):
    row = query(alerts, "SELECT * FROM v_ml_anomaly_alert WHERE ref_no = 'URT-2026-045'")[0]
    assert (row["task"], row["severity"], row["direction"], row["status"]) == \
        ("PRODUCTION_YIELD", "CRITICAL", "LOW_YIELD", "OPEN")
    assert row["lot_no"] == "SUC-2026-045"
    assert row["responsible"] == "Örnek Fason Sucuk Üretim"
    assert row["actual_value"] == D("78.000")
    assert row["deviation"] < 0 and row["est_loss_eur"] > 0
    assert row["score"] <= -row["threshold_crit"]


def test_butchery_alert_points_to_carcass_lot_and_parts(alerts):
    row = query(alerts, "SELECT * FROM v_ml_anomaly_alert WHERE ref_no = 'PRC-2026-001'")[0]
    assert (row["task"], row["direction"]) == ("BUTCHERY_YIELD", "FIRE_HIGH")
    assert row["lot_no"] == "KUZU-2026-001"
    assert "fire +" in row["top_parts"] and "but -" in row["top_parts"]
    assert row["score"] >= row["threshold_warn"]


def test_rerun_is_idempotent(alerts):
    run_sql_file(DEMO)
    assert query(alerts, "SELECT COUNT(*) n FROM ml_anomaly_alert")[0]["n"] == 2


def test_feedback_view_measures_observed_precision(alerts):
    with alerts.cursor() as cur:
        cur.execute("UPDATE ml_anomaly_alert SET status = 'RESOLVED', resolved_by = 'ali', resolved_at = NOW(3) "
                    "WHERE task = 'PRODUCTION_YIELD'")
        cur.execute("UPDATE ml_anomaly_alert SET status = 'FALSE_ALARM' WHERE task = 'BUTCHERY_YIELD'")
    rows = {r["task"]: r for r in query(alerts, "SELECT * FROM v_ml_anomaly_feedback")}
    assert rows["PRODUCTION_YIELD"]["precision_observed"] == D("1.000")
    assert rows["PRODUCTION_YIELD"]["confirmed_loss_eur"] > 0
    assert rows["BUTCHERY_YIELD"]["precision_observed"] == D("0.000")


def test_invalid_severity_rejected(alerts):
    with pytest.raises(pymysql.err.DataError):
        with alerts.cursor() as cur:
            cur.execute("INSERT INTO ml_anomaly_alert (task, detector, model_version, ref_type, ref_id, score, "
                        "threshold_warn, threshold_crit, severity, direction, expected_value, actual_value, deviation, "
                        "message) VALUES ('PRODUCTION_YIELD', 'robust_z', 'x', 'production_order', 1, 0, 1, 2, "
                        "'PANIC', 'LOW_YIELD', 1, 1, 0, 'x')")


def test_seed_predictions_are_labelled_seed(db):
    # uygulamanın yazdığı tahminler DEMO/FIELD olur; seed'in hepsi SEED (AI sekmesi bunu ayırır)
    rows = query(db, "SELECT data_origin, COUNT(*) n FROM ml_prediction GROUP BY data_origin")
    assert [(r["data_origin"], r["n"]) for r in rows] == [("SEED", 20)]


def test_api_rule_alerts_resolve_receipt_and_lot_one_row_per_event(alerts):
    # api-go/controllers/alerts.go: alış fiyatı artışı (alış satırı) ve kasada reddedilen satış (lot + olay)
    with alerts.cursor() as cur:
        cur.execute("INSERT INTO ml_anomaly_alert (task, detector, model_version, ref_type, ref_id, score, threshold_warn, "
                    "threshold_crit, severity, direction, expected_value, actual_value, deviation, est_loss_eur, message) "
                    "SELECT 'PURCHASE_COST', 'rule', 'rule-1', 'purchase_receipt_line', prl.id, 30, 10, 25, 'CRITICAL', "
                    "'COST_INCREASE', 9.7, 12.61, 2.91, 291, 'x' "
                    "FROM purchase_receipt_line prl JOIN lot l ON l.id = prl.lot_id WHERE l.lot_no = 'ET-2026-001'")
        for event in ("e1", "e2", "e1"):   # telefon aynı satışı tekrar gönderir: yine tek satır
            cur.execute("INSERT INTO ml_anomaly_alert (task, detector, model_version, ref_type, ref_id, event_ref, score, "
                        "threshold_warn, threshold_crit, severity, direction, expected_value, actual_value, deviation, message) "
                        "SELECT 'LOT_MOVEMENT', 'rule', 'rule-1', 'lot', id, %s, 2, 0, 0, 'CRITICAL', 'BLOCKED_SALE', 0, 2, 2, 'x' "
                        "FROM lot WHERE lot_no = 'SUC-2026-045' ON DUPLICATE KEY UPDATE ml_anomaly_alert.id = ml_anomaly_alert.id", (event,))
    cost = query(alerts, "SELECT * FROM v_ml_anomaly_alert WHERE task = 'PURCHASE_COST'")[0]
    assert (cost["ref_no"], cost["lot_no"]) == ("ALS-2026-101", "ET-2026-001")
    assert cost["responsible"]                       # tedarikçi adı
    sales = query(alerts, "SELECT * FROM v_ml_anomaly_alert WHERE task = 'LOT_MOVEMENT' ORDER BY event_ref")
    assert [(s["ref_no"], s["lot_no"], s["event_ref"]) for s in sales] == \
        [("SUC-2026-045", "SUC-2026-045", "e1"), ("SUC-2026-045", "SUC-2026-045", "e2")]
