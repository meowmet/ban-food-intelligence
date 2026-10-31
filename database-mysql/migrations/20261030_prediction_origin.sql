-- Additive migration; apply once, after the 20261029 migrations, on existing databases.
-- Each prediction records where its document came from: SEED (seed.sql), DEMO (entered in a demo
-- deployment) or FIELD (entered with DATA_ORIGIN=FIELD; not verified). Existing rows become DEMO,
-- except those of the three seeded scenario documents.
USE food_intel;
ALTER TABLE ml_prediction
  ADD COLUMN data_origin ENUM('SEED','DEMO','FIELD') NOT NULL DEFAULT 'DEMO' AFTER is_anomaly;
UPDATE ml_prediction mp
  LEFT JOIN production_order po   ON mp.ref_type = 'production_order'   AND po.id = mp.ref_id
  LEFT JOIN butchery_operation bo ON mp.ref_type = 'butchery_operation' AND bo.id = mp.ref_id
   SET mp.data_origin = 'SEED'
 WHERE po.order_no IN ('URT-2026-045', 'URT-2026-046') OR bo.op_no = 'PRC-2026-001';
