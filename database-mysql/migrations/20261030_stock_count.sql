-- Additive migration; apply once, after 20261030_alert_inbox.sql, on existing databases.
-- Stock counts (POST /stock-counts): count documents and lines; a difference becomes an
-- ADJUSTMENT_IN/OUT movement with its reason. A shortage can raise a COUNT_SHORTAGE alert.
USE food_intel;
-- Stok sayımı (POST /stock-counts): sayılan miktar ile sayım kaydedildiği andaki defter bakiyesi.
-- Fark gerekçesiyle ADJUSTMENT_IN/OUT hareketi olur (stock_movement_id); farksız satır sayım kanıtıdır.
CREATE TABLE stock_count (
  id            INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  count_no      VARCHAR(30)  NOT NULL UNIQUE,
  warehouse_id  INT UNSIGNED NOT NULL,
  counted_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  counted_by    VARCHAR(64)  NOT NULL,
  note          VARCHAR(255) NULL,
  CONSTRAINT fk_sc_warehouse FOREIGN KEY (warehouse_id) REFERENCES warehouse(id)
) ENGINE=InnoDB;

CREATE TABLE stock_count_line (
  id                INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  count_id          INT UNSIGNED NOT NULL,
  lot_id            INT UNSIGNED NOT NULL,
  system_qty        DECIMAL(14,3) NOT NULL,
  counted_qty       DECIMAL(14,3) NOT NULL,
  diff_qty          DECIMAL(14,3) AS (counted_qty - system_qty) STORED,
  reason            VARCHAR(255) NULL,
  stock_movement_id BIGINT UNSIGNED NULL UNIQUE,
  UNIQUE KEY uq_scl_lot (count_id, lot_id),
  CONSTRAINT fk_scl_count FOREIGN KEY (count_id)          REFERENCES stock_count(id),
  CONSTRAINT fk_scl_lot   FOREIGN KEY (lot_id)            REFERENCES lot(id),
  CONSTRAINT fk_scl_sm    FOREIGN KEY (stock_movement_id) REFERENCES stock_movement(id),
  CONSTRAINT ck_scl_counted CHECK (counted_qty >= 0),
  CONSTRAINT ck_scl_reason  CHECK (counted_qty = system_qty OR reason IS NOT NULL)   -- fark gerekçesiz kaydedilmez
) ENGINE=InnoDB;

ALTER TABLE ml_anomaly_alert
  MODIFY direction ENUM('LOW_YIELD','HIGH_YIELD','FIRE_HIGH','PART_MISMATCH','COST_INCREASE','BLOCKED_SALE','EXPIRED_SALE','COUNT_SHORTAGE') NOT NULL;

CREATE OR REPLACE VIEW v_ml_anomaly_alert AS
SELECT a.id, a.task, a.severity, a.direction, a.status,
       COALESCE(po.order_no, bo.op_no, pr.receipt_no, sc.count_no, ml.lot_no)       AS ref_no,
       COALESCE(po.order_date, bo.op_date, pr.receipt_date, DATE(sc.counted_at), DATE(a.created_at)) AS ref_date,
       COALESCE(pl.lot_no, bl.lot_no, prl_lot.lot_no, scl_lot.lot_no, ml.lot_no) AS lot_no,
       COALESCE(sub.name, bo.operator_name, ps.name, sc.counted_by)                 AS responsible,
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
LEFT JOIN stock_count_line scl  ON a.ref_type = 'stock_count_line' AND scl.id = a.ref_id
LEFT JOIN stock_count sc        ON sc.id = scl.count_id
LEFT JOIN lot scl_lot           ON scl_lot.id = scl.lot_id
LEFT JOIN lot ml                ON a.ref_type = 'lot' AND ml.id = a.ref_id;
