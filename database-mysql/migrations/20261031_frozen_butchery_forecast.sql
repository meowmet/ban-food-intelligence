-- Additive migration; apply once, after 20261031_alert_rules.sql, on existing databases.
-- Frozen butchery forecasts (POST /ai/forecast/butchery/freeze): the part forecast of a carcass is
-- stored before its parts are weighed and never changes; the posted operation links to it once.
USE food_intel;
-- Dondurulmuş kasap tahmini (POST /ai/forecast/butchery/freeze): parçalar tartılmadan önce karkas
-- girdisiyle kaydedilir ve sonra değişmez (tetikleyiciler). Karkas başına en fazla bir tane; birkaç
-- tahminden sonradan iyisini seçmek mümkün değil. frozen_at sunucu saatidir. İşlem kaydedilince
-- operation_id bir kez bağlanır; gerçekleşen ağırlıklar işlemin çıktılarından okunur
-- (ml-engine/field_audit_export.sql). ml_prediction'daki kayıt anı tahmini bundan ayrıdır.
CREATE TABLE butchery_forecast (
  id               INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  input_lot_id     INT UNSIGNED  NOT NULL UNIQUE,
  input_weight_kg  DECIMAL(10,3) NOT NULL,
  op_date          DATE          NOT NULL,
  operator_name    VARCHAR(100)  NULL,
  frozen_by        VARCHAR(64)   NOT NULL,
  data_origin      ENUM('SEED','DEMO','FIELD') NOT NULL DEFAULT 'DEMO',
  frozen_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  operation_id     INT UNSIGNED  NULL UNIQUE,
  CONSTRAINT fk_bf_lot    FOREIGN KEY (input_lot_id) REFERENCES lot(id),
  CONSTRAINT fk_bf_op     FOREIGN KEY (operation_id) REFERENCES butchery_operation(id),
  CONSTRAINT ck_bf_weight CHECK (input_weight_kg > 0)
) ENGINE=InnoDB;

-- Dondurulan parça tahminleri: model ve basit referans, ml_prediction ile aynı sütunlar.
-- anomaly_band_kg: %98 bandın yarı genişliği (kg); gerçekleşen bunun dışındaysa anomali.
CREATE TABLE butchery_forecast_part (
  forecast_id      INT UNSIGNED  NOT NULL,
  model_name       VARCHAR(50)   NOT NULL,
  model_version    VARCHAR(30)   NOT NULL,
  target_name      VARCHAR(50)   NOT NULL,                 -- but_kg ... fire_kg
  predicted_value  DECIMAL(14,4) NOT NULL,
  interval_low     DECIMAL(14,4) NULL,
  interval_high    DECIMAL(14,4) NULL,
  interval_level   DECIMAL(4,3)  NULL,
  anomaly_band_kg  DECIMAL(14,4) NULL,
  PRIMARY KEY (forecast_id, model_name, target_name),
  CONSTRAINT fk_bfp_forecast FOREIGN KEY (forecast_id) REFERENCES butchery_forecast(id)
) ENGINE=InnoDB;

DELIMITER $$
-- Dondurulmuş kasap tahmini: zaman sunucudan; parçalanmış karkas için dondurulamaz; değişmez,
-- yalnızca aynı karkası parçalayan işleme bir kez bağlanır.
CREATE TRIGGER trg_bf_before_insert
BEFORE INSERT ON butchery_forecast
FOR EACH ROW
BEGIN
  SET NEW.frozen_at = CURRENT_TIMESTAMP(3);
  IF NEW.operation_id IS NOT NULL OR EXISTS (
      SELECT 1 FROM butchery_operation WHERE input_lot_id = NEW.input_lot_id AND status = 'POSTED') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'FORECAST_TOO_LATE: the carcass is already cut';
  END IF;
END$$

CREATE TRIGGER trg_bf_before_update
BEFORE UPDATE ON butchery_forecast
FOR EACH ROW
BEGIN
  IF OLD.operation_id IS NOT NULL OR NEW.operation_id IS NULL
     OR NOT (NEW.id <=> OLD.id AND NEW.input_lot_id <=> OLD.input_lot_id
             AND NEW.input_weight_kg <=> OLD.input_weight_kg AND NEW.op_date <=> OLD.op_date
             AND NEW.operator_name <=> OLD.operator_name AND NEW.frozen_by <=> OLD.frozen_by
             AND NEW.data_origin <=> OLD.data_origin AND NEW.frozen_at <=> OLD.frozen_at) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IMMUTABLE: a frozen forecast only gets its butchery operation, once';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM butchery_operation WHERE id = NEW.operation_id AND input_lot_id = OLD.input_lot_id) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'INVALID_INPUT: the operation cuts another carcass';
  END IF;
END$$

CREATE TRIGGER trg_bf_no_delete
BEFORE DELETE ON butchery_forecast
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IMMUTABLE: a frozen forecast is never deleted';
END$$

CREATE TRIGGER trg_bfp_before_insert
BEFORE INSERT ON butchery_forecast_part
FOR EACH ROW
BEGIN
  IF (SELECT operation_id FROM butchery_forecast WHERE id = NEW.forecast_id) IS NOT NULL THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IMMUTABLE: the frozen forecast is already linked to its operation';
  END IF;
END$$

CREATE TRIGGER trg_bfp_no_update
BEFORE UPDATE ON butchery_forecast_part
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IMMUTABLE: frozen forecast parts never change';
END$$

CREATE TRIGGER trg_bfp_no_delete
BEFORE DELETE ON butchery_forecast_part
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IMMUTABLE: frozen forecast parts never change';
END$$
DELIMITER ;
