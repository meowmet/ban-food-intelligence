-- Read-only snapshot. Inventory booking time is a proxy, not a physical observation timestamp.
-- A butchery operation with a linked frozen forecast (butchery_forecast, stored before the parts
-- were weighed) is exported with that forecast only; its ml_prediction rows, computed in the same
-- request as the recorded weights, are left out so that every operation has one forecast version.
SET NAMES utf8mb4;
SET time_zone = '+00:00';
SET TRANSACTION ISOLATION LEVEL REPEATABLE READ;
START TRANSACTION READ ONLY;
SELECT row_json FROM (
SELECT mp.ref_type AS k_type, mp.ref_id AS k_id, mp.target_name AS k_target, mp.model_name AS k_model,
  JSON_OBJECT(
  'exported_at', DATE_FORMAT(UTC_TIMESTAMP(6), '%Y-%m-%dT%H:%i:%s.%fZ'),
  'reference_type', mp.ref_type, 'reference', COALESCE(po.order_no, bo.op_no),
  'task', mp.task, 'target', mp.target_name, 'model', mp.model_name,
  'version', mp.model_version, 'predicted', mp.predicted_value,
  'stored_actual', mp.actual_value,
  'actual', CASE mp.target_name
    WHEN 'actual_qty' THEN vc.actual_qty WHEN 'ekp' THEN vc.ekp
    WHEN 'but_kg' THEN bf.but_kg WHEN 'sirt_kg' THEN bf.sirt_kg
    WHEN 'omuz_kg' THEN bf.omuz_kg WHEN 'kaburga_kg' THEN bf.kaburga_kg
    WHEN 'kiyma_kg' THEN bf.kiyma_kg WHEN 'fire_kg' THEN bf.fire_kg END,
  'low', mp.interval_low, 'high', mp.interval_high, 'level', mp.interval_level,
  'predicted_at', DATE_FORMAT(mp.created_at, '%Y-%m-%dT%H:%i:%s.%fZ'),
  'outcome_booked_at', (
    SELECT DATE_FORMAT(MIN(sm.moved_at), '%Y-%m-%dT%H:%i:%s.%fZ')
    FROM stock_movement sm WHERE sm.ref_type = mp.ref_type AND sm.ref_id = mp.ref_id
      AND sm.movement_type IN ('PRODUCTION_RECEIPT', 'BUTCHERY_OUTPUT')),
  'forecast_source', 'ml_prediction'
  ) AS row_json
FROM ml_prediction mp
LEFT JOIN production_order po ON mp.ref_type = 'production_order' AND po.id = mp.ref_id
LEFT JOIN v_production_cost vc ON vc.order_no = po.order_no
LEFT JOIN butchery_operation bo ON mp.ref_type = 'butchery_operation' AND bo.id = mp.ref_id
LEFT JOIN v_ml_butchery_features bf ON bf.row_id = CONCAT('DB-B-', bo.op_no)
WHERE mp.model_name IN ('ali_ridge', 'baseline_hist_mean')
  AND NOT EXISTS (SELECT 1 FROM butchery_forecast fz WHERE mp.ref_type = 'butchery_operation' AND fz.operation_id = mp.ref_id)
UNION ALL
SELECT 'butchery_operation', bo.id, p.target_name, p.model_name,
  JSON_OBJECT(
  'exported_at', DATE_FORMAT(UTC_TIMESTAMP(6), '%Y-%m-%dT%H:%i:%s.%fZ'),
  'reference_type', 'butchery_operation', 'reference', bo.op_no,
  'task', 'BUTCHERY_YIELD', 'target', p.target_name, 'model', p.model_name,
  'version', p.model_version, 'predicted', p.predicted_value,
  'stored_actual', NULL,
  'actual', CASE p.target_name
    WHEN 'but_kg' THEN bf.but_kg WHEN 'sirt_kg' THEN bf.sirt_kg
    WHEN 'omuz_kg' THEN bf.omuz_kg WHEN 'kaburga_kg' THEN bf.kaburga_kg
    WHEN 'kiyma_kg' THEN bf.kiyma_kg WHEN 'fire_kg' THEN bf.fire_kg END,
  'low', p.interval_low, 'high', p.interval_high, 'level', p.interval_level,
  'predicted_at', DATE_FORMAT(fz.frozen_at, '%Y-%m-%dT%H:%i:%s.%fZ'),
  'outcome_booked_at', (
    SELECT DATE_FORMAT(MIN(sm.moved_at), '%Y-%m-%dT%H:%i:%s.%fZ')
    FROM stock_movement sm WHERE sm.ref_type = 'butchery_operation' AND sm.ref_id = bo.id
      AND sm.movement_type = 'BUTCHERY_OUTPUT'),
  'forecast_source', 'butchery_forecast',
  'forecast_input_kg', fz.input_weight_kg, 'outcome_input_kg', bo.input_weight_kg
  )
FROM butchery_forecast_part p
JOIN butchery_forecast fz ON fz.id = p.forecast_id
JOIN butchery_operation bo ON bo.id = fz.operation_id
LEFT JOIN v_ml_butchery_features bf ON bf.row_id = CONCAT('DB-B-', bo.op_no)
WHERE p.model_name IN ('ali_ridge', 'baseline_hist_mean')
) snapshot
ORDER BY k_type, k_id, k_target, k_model;
COMMIT;
