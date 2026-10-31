-- Additive migration; apply once, after 20261030_stock_count.sql, on existing databases.
-- Alert thresholds become configuration (defaults from the synthetic stress test,
-- ml-engine/calibrate_live_alerts.py) and missed findings are recorded for the observed recall.
USE food_intel;
-- Canlı uyarı eşikleri (alerts.go). Varsayılanlar sentetik stres testinden: 100 kayıtta en fazla 1 yanlış
-- alarm, bu bütçede en yüksek yakalama (ml-engine/calibrate_live_alerts.py, docs/AI.md). Verim kuralları
-- %98 bandın katı, alış % artış, sayım eksiği €. Yetkili POST /alerts/rules ile değiştirir (audit_log).
CREATE TABLE alert_threshold (
  id          INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  rule        ENUM('PRODUCTION_YIELD','BUTCHERY_YIELD','PURCHASE_COST','COUNT_SHORTAGE') NOT NULL UNIQUE,
  warn        DECIMAL(10,3) NOT NULL,
  crit        DECIMAL(10,3) NOT NULL,
  updated_by  VARCHAR(64)  NOT NULL DEFAULT 'default',
  updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  CONSTRAINT ck_at_order CHECK (warn > 0 AND crit > warn)
) ENGINE=InnoDB;
INSERT INTO alert_threshold (rule, warn, crit) VALUES
  ('PRODUCTION_YIELD', 1.000, 1.500), ('BUTCHERY_YIELD', 1.500, 2.000),
  ('PURCHASE_COST', 10.000, 25.000), ('COUNT_SHORTAGE', 10.000, 100.000);

-- Sistemin uyarmadığı, elle bulunan sorunlar (POST /alerts/missed). Gerçek sorun olarak kapatılan
-- uyarılarla birlikte sahada gözlenen yakalama oranını verir: doğrulanan / (doğrulanan + kaçırılan).
CREATE TABLE alert_miss (
  id            INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  task          ENUM('PRODUCTION_YIELD','BUTCHERY_YIELD','PURCHASE_COST','LOT_MOVEMENT') NOT NULL,
  ref_no        VARCHAR(30)  NULL,
  lot_id        INT UNSIGNED NULL,
  note          VARCHAR(255) NOT NULL,
  est_loss_eur  DECIMAL(12,2) NOT NULL DEFAULT 0,
  found_by      VARCHAR(64)  NOT NULL,
  found_at      DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  CONSTRAINT fk_am_lot  FOREIGN KEY (lot_id) REFERENCES lot(id),
  CONSTRAINT ck_am_loss CHECK (est_loss_eur >= 0)
) ENGINE=InnoDB;
