"""Uyarı eşikleri yapılandırmadır (sentetik stres testinden varsayılanlar); kaçırılan sorunlar ayrı kaydedilir."""
from decimal import Decimal as D

import pymysql
import pytest

from conftest import query


def test_default_thresholds_from_the_synthetic_stress_test(db):
    rows = {r["rule"]: (r["warn"], r["crit"]) for r in query(db, "SELECT rule, warn, crit FROM alert_threshold")}
    assert rows == {"PRODUCTION_YIELD": (D("1.000"), D("1.500")), "BUTCHERY_YIELD": (D("1.500"), D("2.000")),
                    "PURCHASE_COST": (D("10.000"), D("25.000")), "COUNT_SHORTAGE": (D("10.000"), D("100.000"))}


def test_warning_must_stay_below_critical(db):
    with pytest.raises(pymysql.err.OperationalError):          # CHECK ck_at_order
        with db.cursor() as cur:
            cur.execute("UPDATE alert_threshold SET warn = 30, crit = 20 WHERE rule = 'PURCHASE_COST'")


def test_missed_finding_needs_a_known_check_and_no_negative_loss(db):
    with pytest.raises(pymysql.err.OperationalError):          # CHECK ck_am_loss
        with db.cursor() as cur:
            cur.execute("INSERT INTO alert_miss (task, note, est_loss_eur, found_by) VALUES ('PURCHASE_COST', 'x', -1, 'py')")
    with pytest.raises(pymysql.err.DataError):
        with db.cursor() as cur:
            cur.execute("INSERT INTO alert_miss (task, note, found_by) VALUES ('NOPE', 'x', 'py')")
