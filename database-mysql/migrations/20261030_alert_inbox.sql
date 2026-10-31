-- Additive migration; apply once, after 20261030_prediction_origin.sql, on existing databases.
-- The API now writes alerts itself (api-go/controllers/alerts.go): live yield outside the 98 % band,
-- purchase price increases, and POS sales of recalled or expired lots that the server refused.
-- Existing alerts keep their values; event_ref is '' for them, so the ml-engine upsert stays idempotent.
USE food_intel;
ALTER TABLE ml_anomaly_alert
  MODIFY task ENUM('PRODUCTION_YIELD','BUTCHERY_YIELD','PURCHASE_COST','LOT_MOVEMENT') NOT NULL,
  MODIFY direction ENUM('LOW_YIELD','HIGH_YIELD','FIRE_HIGH','PART_MISMATCH','COST_INCREASE','BLOCKED_SALE','EXPIRED_SALE') NOT NULL,
  ADD COLUMN event_ref VARCHAR(64) NOT NULL DEFAULT '' AFTER ref_id,
  DROP INDEX uq_mla_ref,
  ADD UNIQUE KEY uq_mla_ref (task, ref_type, ref_id, model_version, event_ref);

CREATE OR REPLACE VIEW v_ml_anomaly_alert AS
SELECT a.id, a.task, a.severity, a.direction, a.status,
       COALESCE(po.order_no, bo.op_no, pr.receipt_no, ml.lot_no)        AS ref_no,
       COALESCE(po.order_date, bo.op_date, pr.receipt_date, DATE(a.created_at)) AS ref_date,
       COALESCE(pl.lot_no, bl.lot_no, prl_lot.lot_no, ml.lot_no)        AS lot_no,
       COALESCE(sub.name, bo.operator_name, ps.name)                    AS responsible,
       a.expected_value, a.actual_value, a.deviation, a.est_loss_eur,
       a.score, a.threshold_warn, a.threshold_crit, a.detector, a.model_version,
       a.top_parts, a.message, a.created_at, a.resolved_by, a.resolved_at, a.resolution_note,
       a.ref_type, a.ref_id, a.event_ref
FROM ml_anomaly_alert a
LEFT JOIN production_order po   ON a.ref_type = 'production_order'   AND po.id = a.ref_id
LEFT JOIN supplier sub          ON sub.id = po.subcontractor_id
LEFT JOIN (SELECT pout.production_order_id, MIN(l.lot_no) AS lot_no
             FROM production_output pout JOIN lot l ON l.id = pout.lot_id
            GROUP BY pout.production_order_id) pl ON pl.production_order_id = po.id
LEFT JOIN butchery_operation bo ON a.ref_type = 'butchery_operation' AND bo.id = a.ref_id
LEFT JOIN lot bl                ON bl.id = bo.input_lot_id
LEFT JOIN purchase_receipt_line prl ON a.ref_type = 'purchase_receipt_line' AND prl.id = a.ref_id
LEFT JOIN purchase_receipt pr   ON pr.id = prl.receipt_id
LEFT JOIN supplier ps           ON ps.id = pr.supplier_id
LEFT JOIN lot prl_lot           ON prl_lot.id = prl.lot_id
LEFT JOIN lot ml                ON a.ref_type = 'lot' AND ml.id = a.ref_id;
