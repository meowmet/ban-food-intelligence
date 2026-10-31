-- =====================================================================
-- BAN Food Intelligence | database-mysql/schema.sql
-- Bölümler: (1) tablolar  (2) raporlama görünümleri  (3) tetikleyiciler + saklı yordamlar
-- Yükleme sırası: schema.sql, sonra seed.sql (bkz. database-mysql/README.md)
-- =====================================================================

-- =====================================================================
-- BAN - Food Intelligence | Merkezi MySQL 8 şeması
-- Sahibi: Ali  (tüketenler: Go API - Berke, Kotlin/SQLite sync - Nuri)
--
-- Tasarım kuralları
--   * Tüm para alanları DECIMAL (asla FLOAT). Birim maliyet 4 hane, tutar 2 hane.
--   * Stok YALNIZCA stock_movement defterinden (append-only) türetilir;
--     stock_balance tetikleyici ile tutulan bir önbellektir ve negatif olamaz.
--   * Lot soyağacı tek bir kenar tablosunda (lot_link) tutulur; üretim,
--     parçalama ve yeniden işleme aynı WITH RECURSIVE sorgusuyla izlenir.
--   * Geri çağırma KI tahminine değil yalnızca bu kayıt ilişkilerine dayanır.
-- =====================================================================

SET NAMES utf8mb4;
SET time_zone = '+00:00';

CREATE DATABASE IF NOT EXISTS food_intel
  DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
USE food_intel;

-- ---------------------------------------------------------------------
-- 1. Ana kartlar
-- ---------------------------------------------------------------------
CREATE TABLE unit_of_measure (
  code        VARCHAR(10)  PRIMARY KEY,           -- KG, PAKET, ADET
  name        VARCHAR(50)  NOT NULL,
  is_weight   BOOLEAN      NOT NULL DEFAULT FALSE
) ENGINE=InnoDB;

CREATE TABLE supplier (
  id              INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  code            VARCHAR(20)  NOT NULL UNIQUE,
  name            VARCHAR(150) NOT NULL,
  supplier_type   ENUM('RAW_MATERIAL','SUBCONTRACTOR','LIVESTOCK') NOT NULL,
  contact_person  VARCHAR(100),
  phone           VARCHAR(30),
  email           VARCHAR(150),
  address         VARCHAR(255),
  created_at      DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB;

CREATE TABLE customer (
  id              INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  code            VARCHAR(20)  NOT NULL UNIQUE,
  name            VARCHAR(150) NOT NULL,
  customer_type   ENUM('MARKET','WHOLESALER','RESTAURANT','WALK_IN') NOT NULL,
  contact_person  VARCHAR(100),
  phone           VARCHAR(30),
  email           VARCHAR(150),
  address         VARCHAR(255),
  created_at      DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB;

CREATE TABLE warehouse (
  id              INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  code            VARCHAR(20)  NOT NULL UNIQUE,
  name            VARCHAR(100) NOT NULL,
  warehouse_type  ENUM('MAIN','COLD','STORE','QUARANTINE','SUBCONTRACTOR') NOT NULL
) ENGINE=InnoDB;

CREATE TABLE product (
  id                      INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  sku                     VARCHAR(30)  NOT NULL UNIQUE,
  name                    VARCHAR(150) NOT NULL,
  product_type            ENUM('RAW_MATERIAL','FINISHED_GOOD','CARCASS','CUT','BY_PRODUCT','WASTE') NOT NULL,
  uom_code                VARCHAR(10)  NOT NULL,
  pack_size_kg            DECIMAL(10,3) NULL,          -- 1 paket Narin Sucuk = 1.000 kg
  species                 VARCHAR(20)  NULL,           -- KUZU, KOYUN, OGLAK ... (parçalama)
  standard_purchase_price DECIMAL(12,4) NULL,          -- alış fiyatı (öneri; gerçek fiyat lotta)
  sale_price              DECIMAL(12,4) NULL,          -- VKP (net satış fiyatı)
  shelf_life_days         INT UNSIGNED NULL,
  is_active               BOOLEAN NOT NULL DEFAULT TRUE,
  CONSTRAINT fk_product_uom FOREIGN KEY (uom_code) REFERENCES unit_of_measure(code)
) ENGINE=InnoDB;

-- ---------------------------------------------------------------------
-- 2. Lot (parti / Charge)
-- ---------------------------------------------------------------------
CREATE TABLE lot (
  id               INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  lot_no           VARCHAR(30)  NOT NULL UNIQUE,         -- ET-2026-001, SUC-2026-045 ...
  product_id       INT UNSIGNED NOT NULL,
  origin_type      ENUM('PURCHASE','PRODUCTION','BUTCHERY','REWORK','RETURN') NOT NULL,
  supplier_id      INT UNSIGNED NULL,                    -- satın alma lotlarında zorunlu
  supplier_lot_no  VARCHAR(50)  NULL,                    -- tedarikçinin kendi parti no'su
  lot_date         DATE         NOT NULL,                -- giriş / üretim tarihi
  expiry_date      DATE         NULL,                    -- SKT (uygun olduğunda)
  initial_qty      DECIMAL(14,3) NOT NULL,
  uom_code         VARCHAR(10)  NOT NULL,
  unit_cost        DECIMAL(14,4) NOT NULL,               -- alış fiyatı veya hesaplanan EKP
  -- KI özellikleri (girişte ölçülen; opsiyonel)
  fat_pct            DECIMAL(5,2) NULL,                  -- et: laboratuvar yağ oranı
  animal_breed       VARCHAR(30)  NULL,                  -- karkas: ırk
  animal_age_months  DECIMAL(4,1) NULL,
  animal_sex         ENUM('E','D') NULL,
  fat_score          TINYINT UNSIGNED NULL,              -- 1 (yağsız) .. 5 (çok yağlı)
  conformation_score TINYINT UNSIGNED NULL,              -- 1 (P) .. 5 (E) karkas yapısı
  status           ENUM('ACTIVE','BLOCKED','RECALLED','DEPLETED') NOT NULL DEFAULT 'ACTIVE',
  blocked_reason   VARCHAR(255) NULL,
  created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  CONSTRAINT fk_lot_product  FOREIGN KEY (product_id)  REFERENCES product(id),
  CONSTRAINT fk_lot_supplier FOREIGN KEY (supplier_id) REFERENCES supplier(id),
  CONSTRAINT fk_lot_uom      FOREIGN KEY (uom_code)    REFERENCES unit_of_measure(code),
  CONSTRAINT ck_lot_qty      CHECK (initial_qty > 0),
  CONSTRAINT ck_lot_cost     CHECK (unit_cost >= 0),
  CONSTRAINT ck_lot_purchase_supplier CHECK (origin_type <> 'PURCHASE' OR supplier_id IS NOT NULL),
  INDEX ix_lot_product (product_id),
  INDEX ix_lot_status (status)
) ENGINE=InnoDB;

-- Lot soyağacı: çoktan-çoğa, miktarlı. Üretim / parçalama / yeniden işleme.
CREATE TABLE lot_link (
  id               INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  parent_lot_id    INT UNSIGNED NOT NULL,                -- girdi (ör. ET-2026-001)
  child_lot_id     INT UNSIGNED NOT NULL,                -- çıktı (ör. SUC-2026-045)
  link_type        ENUM('PRODUCTION','BUTCHERY','REWORK') NOT NULL,
  qty_consumed     DECIMAL(14,3) NOT NULL,               -- parent biriminde kullanılan miktar
  qty_produced     DECIMAL(14,3) NOT NULL,               -- child biriminde oluşan miktar
  ref_type         VARCHAR(30)  NOT NULL,                -- production_order / butchery_operation
  ref_id           INT UNSIGNED NOT NULL,
  created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  CONSTRAINT fk_ll_parent FOREIGN KEY (parent_lot_id) REFERENCES lot(id),
  CONSTRAINT fk_ll_child  FOREIGN KEY (child_lot_id)  REFERENCES lot(id),
  CONSTRAINT ck_ll_self   CHECK (parent_lot_id <> child_lot_id),
  CONSTRAINT ck_ll_qty    CHECK (qty_consumed > 0 AND qty_produced > 0),
  UNIQUE KEY uq_ll (parent_lot_id, child_lot_id, ref_type, ref_id),
  INDEX ix_ll_child (child_lot_id)
) ENGINE=InnoDB;

-- ---------------------------------------------------------------------
-- 3. Satın alma (hammadde girişi)
-- ---------------------------------------------------------------------
CREATE TABLE purchase_receipt (
  id             INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  receipt_no     VARCHAR(30)  NOT NULL UNIQUE,
  supplier_id    INT UNSIGNED NOT NULL,
  warehouse_id   INT UNSIGNED NOT NULL,
  receipt_date   DATE         NOT NULL,
  invoice_no     VARCHAR(50)  NULL,
  delivery_note_no VARCHAR(50) NULL,
  CONSTRAINT fk_pr_supplier  FOREIGN KEY (supplier_id)  REFERENCES supplier(id),
  CONSTRAINT fk_pr_warehouse FOREIGN KEY (warehouse_id) REFERENCES warehouse(id)
) ENGINE=InnoDB;

CREATE TABLE purchase_receipt_line (
  id           INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  receipt_id   INT UNSIGNED NOT NULL,
  product_id   INT UNSIGNED NOT NULL,
  lot_id       INT UNSIGNED NOT NULL UNIQUE,
  qty          DECIMAL(14,3) NOT NULL,
  unit_price   DECIMAL(14,4) NOT NULL,
  line_total   DECIMAL(14,2) AS (ROUND(qty * unit_price, 2)) STORED,
  CONSTRAINT fk_prl_receipt FOREIGN KEY (receipt_id) REFERENCES purchase_receipt(id),
  CONSTRAINT fk_prl_product FOREIGN KEY (product_id) REFERENCES product(id),
  CONSTRAINT fk_prl_lot     FOREIGN KEY (lot_id)     REFERENCES lot(id),
  CONSTRAINT ck_prl_qty     CHECK (qty > 0)
) ENGINE=InnoDB;

-- ---------------------------------------------------------------------
-- 4. Görev 1 - Dış hizmetle (fason) üretim
-- ---------------------------------------------------------------------
CREATE TABLE production_order (
  id                    INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  order_no              VARCHAR(30)  NOT NULL UNIQUE,      -- URT-2026-045
  order_type            ENUM('EXTERNAL','INTERNAL') NOT NULL DEFAULT 'EXTERNAL',
  subcontractor_id      INT UNSIGNED NULL,                 -- fason üretici (supplier)
  product_id            INT UNSIGNED NOT NULL,             -- mamul
  planned_qty           DECIMAL(14,3) NOT NULL,
  actual_qty            DECIMAL(14,3) NULL,                -- teslim alınan gerçek miktar
  service_unit_price    DECIMAL(14,4) NOT NULL DEFAULT 0,  -- 5,50 € / paket
  service_invoice_no    VARCHAR(50)  NULL,
  service_invoice_total DECIMAL(14,2) NULL,                -- = actual_qty × service_unit_price
  drying_days           TINYINT UNSIGNED NULL,             -- planlanan kurutma süresi (KI özelliği)
  status                ENUM('PLANNED','MATERIAL_SENT','RECEIVED','CLOSED','CANCELLED') NOT NULL DEFAULT 'PLANNED',
  order_date            DATE NOT NULL,
  received_date         DATE NULL,
  notes                 VARCHAR(255) NULL,
  CONSTRAINT fk_po_sub     FOREIGN KEY (subcontractor_id) REFERENCES supplier(id),
  CONSTRAINT fk_po_product FOREIGN KEY (product_id)       REFERENCES product(id),
  CONSTRAINT ck_po_qty     CHECK (planned_qty > 0 AND (actual_qty IS NULL OR actual_qty >= 0))
) ENGINE=InnoDB;

-- Fasona sevk edilen hammadde lotları (bir emirde birden çok lot olabilir)
CREATE TABLE production_input (
  id                   INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  production_order_id  INT UNSIGNED NOT NULL,
  lot_id               INT UNSIGNED NOT NULL,
  qty_sent             DECIMAL(14,3) NOT NULL,
  qty_returned         DECIMAL(14,3) NOT NULL DEFAULT 0,       -- fasondan iade gelen artık
  qty_consumed         DECIMAL(14,3) AS (qty_sent - qty_returned) STORED,
  unit_cost            DECIMAL(14,4) NOT NULL,                 -- sevk anındaki lot maliyeti (snapshot)
  line_cost            DECIMAL(14,4) AS ((qty_sent - qty_returned) * unit_cost) STORED,
  CONSTRAINT fk_pi_order FOREIGN KEY (production_order_id) REFERENCES production_order(id),
  CONSTRAINT fk_pi_lot   FOREIGN KEY (lot_id) REFERENCES lot(id),
  CONSTRAINT ck_pi_qty   CHECK (qty_sent > 0 AND qty_returned >= 0 AND qty_returned <= qty_sent),
  UNIQUE KEY uq_pi (production_order_id, lot_id)
) ENGINE=InnoDB;

-- Teslim alınan mamul lotları (bir emir birden çok mamul lotu üretebilir)
CREATE TABLE production_output (
  id                   INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  production_order_id  INT UNSIGNED NOT NULL,
  lot_id               INT UNSIGNED NOT NULL UNIQUE,
  qty                  DECIMAL(14,3) NOT NULL,
  unit_cost            DECIMAL(14,4) NOT NULL,                 -- EKP
  CONSTRAINT fk_pout_order FOREIGN KEY (production_order_id) REFERENCES production_order(id),
  CONSTRAINT fk_pout_lot   FOREIGN KEY (lot_id) REFERENCES lot(id),
  CONSTRAINT ck_pout_qty   CHECK (qty > 0)
) ENGINE=InnoDB;

-- ---------------------------------------------------------------------
-- 5. Görev 2 - Bütün kuzu parçalama
-- ---------------------------------------------------------------------
CREATE TABLE butchery_recipe (
  id                 INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  code               VARCHAR(30)  NOT NULL UNIQUE,
  name               VARCHAR(100) NOT NULL,
  species            VARCHAR(20)  NOT NULL,
  default_allocation ENUM('QUANTITY','VALUE') NOT NULL DEFAULT 'VALUE'
) ENGINE=InnoDB;

CREATE TABLE butchery_recipe_line (
  id                 INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  recipe_id          INT UNSIGNED NOT NULL,
  product_id         INT UNSIGNED NOT NULL,
  output_kind        ENUM('CUT','BY_PRODUCT','WASTE') NOT NULL,
  expected_yield_pct DECIMAL(6,3) NOT NULL,           -- reçete beklentisi (KI referansı)
  value_price_per_kg DECIMAL(12,4) NULL,              -- değer ağırlıklı dağıtım anahtarı
  CONSTRAINT fk_brl_recipe  FOREIGN KEY (recipe_id)  REFERENCES butchery_recipe(id),
  CONSTRAINT fk_brl_product FOREIGN KEY (product_id) REFERENCES product(id),
  UNIQUE KEY uq_brl (recipe_id, product_id)
) ENGINE=InnoDB;

CREATE TABLE butchery_operation (
  id                  INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  op_no               VARCHAR(30)  NOT NULL UNIQUE,
  recipe_id           INT UNSIGNED NOT NULL,
  input_lot_id        INT UNSIGNED NOT NULL,
  species             VARCHAR(20)  NOT NULL,
  input_weight_kg     DECIMAL(10,3) NOT NULL,
  purchase_unit_price DECIMAL(12,4) NOT NULL,                       -- 11,50 €/kg
  purchase_cost       DECIMAL(14,2) AS (ROUND(input_weight_kg * purchase_unit_price, 2)) STORED,
  additional_cost     DECIMAL(14,2) NOT NULL DEFAULT 0,             -- belgeli ek maliyet (kesim, nakliye)
  additional_cost_note VARCHAR(255) NULL,
  total_cost          DECIMAL(14,2) AS (ROUND(input_weight_kg * purchase_unit_price, 2) + additional_cost) STORED,
  declared_total_cost DECIMAL(14,2) NULL,                           -- faturada/görselde beyan edilen (ör. 351,00)
  allocation_method   ENUM('QUANTITY','VALUE') NOT NULL,
  op_date             DATE NOT NULL,
  operator_name       VARCHAR(100) NULL,
  chill_hours         DECIMAL(5,1) NULL,                            -- kesimden parçalamaya soğutma süresi
  status              ENUM('DRAFT','RECONCILED','POSTED','REJECTED') NOT NULL DEFAULT 'DRAFT',
  reconciliation_note VARCHAR(500) NULL,
  CONSTRAINT fk_bo_recipe FOREIGN KEY (recipe_id)    REFERENCES butchery_recipe(id),
  CONSTRAINT fk_bo_lot    FOREIGN KEY (input_lot_id) REFERENCES lot(id),
  CONSTRAINT ck_bo_weight CHECK (input_weight_kg > 0)
) ENGINE=InnoDB;

CREATE TABLE butchery_output (
  id               INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  operation_id     INT UNSIGNED NOT NULL,
  product_id       INT UNSIGNED NOT NULL,
  lot_id           INT UNSIGNED NULL UNIQUE,              -- fire için lot açılmaz
  output_kind      ENUM('CUT','BY_PRODUCT','WASTE') NOT NULL,
  weight_kg        DECIMAL(10,3) NOT NULL,
  allocation_basis DECIMAL(14,4) NOT NULL DEFAULT 0,      -- QUANTITY: kg | VALUE: kg × €/kg
  allocated_cost   DECIMAL(14,2) NOT NULL DEFAULT 0,
  unit_cost        DECIMAL(14,4) AS (IF(weight_kg > 0, allocated_cost / weight_kg, 0)) STORED,
  CONSTRAINT fk_bout_op      FOREIGN KEY (operation_id) REFERENCES butchery_operation(id),
  CONSTRAINT fk_bout_product FOREIGN KEY (product_id)   REFERENCES product(id),
  CONSTRAINT fk_bout_lot     FOREIGN KEY (lot_id)       REFERENCES lot(id),
  CONSTRAINT ck_bout_weight  CHECK (weight_kg >= 0)
  -- POSTED işlemlerde fire dışındaki her çıktının lotu olmalı: testlerde doğrulanır
  -- (REJECTED taslaklarda lot açılmaz).
) ENGINE=InnoDB;

-- ---------------------------------------------------------------------
-- 6. Sevkiyat (market) ve POS satış (Kotlin)
-- ---------------------------------------------------------------------
CREATE TABLE shipment (
  id                 INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  shipment_no        VARCHAR(30) NOT NULL UNIQUE,
  customer_id        INT UNSIGNED NOT NULL,              -- alıcı
  from_warehouse_id  INT UNSIGNED NULL,                  -- 1. kademe: bizim depomuz
  from_customer_id   INT UNSIGNED NULL,                  -- 2. kademe: alıcının kaydedilmiş ileri sevki
  ship_date          DATE NOT NULL,
  delivery_note_no   VARCHAR(50) NULL,
  distribution_tier  TINYINT AS (IF(from_customer_id IS NULL, 1, 2)) STORED,
  CONSTRAINT fk_sh_customer FOREIGN KEY (customer_id)       REFERENCES customer(id),
  CONSTRAINT fk_sh_wh       FOREIGN KEY (from_warehouse_id) REFERENCES warehouse(id),
  CONSTRAINT fk_sh_from     FOREIGN KEY (from_customer_id)  REFERENCES customer(id),
  CONSTRAINT ck_sh_source   CHECK ((from_warehouse_id IS NULL) <> (from_customer_id IS NULL))
) ENGINE=InnoDB;

CREATE TABLE shipment_line (
  id           INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  shipment_id  INT UNSIGNED NOT NULL,
  lot_id       INT UNSIGNED NOT NULL,
  qty          DECIMAL(14,3) NOT NULL,
  unit_price   DECIMAL(14,4) NULL,
  CONSTRAINT fk_shl_sh  FOREIGN KEY (shipment_id) REFERENCES shipment(id),
  CONSTRAINT fk_shl_lot FOREIGN KEY (lot_id)      REFERENCES lot(id),
  CONSTRAINT ck_shl_qty CHECK (qty > 0),
  INDEX ix_shl_lot (lot_id)
) ENGINE=InnoDB;

CREATE TABLE sale (
  id             INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  sale_uuid      CHAR(36)     NOT NULL UNIQUE,          -- Kotlin tarafında üretilen UUIDv7 (idempotency)
  device_id      VARCHAR(64)  NOT NULL,
  warehouse_id   INT UNSIGNED NOT NULL,                 -- satış noktası (STORE)
  customer_id    INT UNSIGNED NULL,
  sold_at        DATETIME(3)  NOT NULL,
  synced_at      DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  CONSTRAINT fk_sale_wh   FOREIGN KEY (warehouse_id) REFERENCES warehouse(id),
  CONSTRAINT fk_sale_cust FOREIGN KEY (customer_id)  REFERENCES customer(id)
) ENGINE=InnoDB;

-- ---------------------------------------------------------------------
-- 7. Stok hareket defteri (append-only) + bakiye
-- ---------------------------------------------------------------------
CREATE TABLE stock_movement (
  id              BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  movement_uuid   CHAR(36)     NOT NULL UNIQUE DEFAULT (UUID()),
  lot_id          INT UNSIGNED NOT NULL,
  warehouse_id    INT UNSIGNED NOT NULL,
  movement_type   ENUM(
                    'PURCHASE_IN',          -- +  hammadde girişi
                    'TRANSFER_OUT',         -- -  depo transferi çıkış (fasona sevk dahil)
                    'TRANSFER_IN',          -- +  depo transferi giriş
                    'PRODUCTION_CONSUME',   -- -  üretimde tüketim
                    'PRODUCTION_RECEIPT',   -- +  mamul kabulü
                    'BUTCHERY_CONSUME',     -- -  parçalamaya giren karkas
                    'BUTCHERY_OUTPUT',      -- +  parça ürün lotu
                    'SHIPMENT',             -- -  markete sevk
                    'SALE',                 -- -  POS satış
                    'RETURN',               -- +  müşteri iadesi (geri çağırma dahil)
                    'REWORK_CONSUME',       -- -  yeniden işleme girdi
                    'REWORK_OUTPUT',        -- +  yeniden işleme çıktı
                    'WASTE',                -- -  fire / imha
                    'ADJUSTMENT_IN',        -- +  sayım farkı
                    'ADJUSTMENT_OUT'        -- -  sayım farkı
                  ) NOT NULL,
  qty             DECIMAL(14,3) NOT NULL,                 -- işaretli: + giriş, - çıkış
  unit_cost       DECIMAL(14,4) NOT NULL,
  ref_type        VARCHAR(30)  NULL,                      -- purchase_receipt, production_order, shipment, sale ...
  ref_id          INT UNSIGNED NULL,
  moved_at        DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_by      VARCHAR(64)  NOT NULL DEFAULT 'system',
  note            VARCHAR(255) NULL,
  CONSTRAINT fk_sm_lot FOREIGN KEY (lot_id)       REFERENCES lot(id),
  CONSTRAINT fk_sm_wh  FOREIGN KEY (warehouse_id) REFERENCES warehouse(id),
  CONSTRAINT ck_sm_qty CHECK (qty <> 0),
  INDEX ix_sm_lot_wh (lot_id, warehouse_id),
  INDEX ix_sm_ref (ref_type, ref_id)
) ENGINE=InnoDB;

CREATE TABLE stock_balance (
  lot_id        INT UNSIGNED NOT NULL,
  warehouse_id  INT UNSIGNED NOT NULL,
  qty           DECIMAL(14,3) NOT NULL DEFAULT 0,
  updated_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (lot_id, warehouse_id),
  CONSTRAINT fk_sb_lot FOREIGN KEY (lot_id)       REFERENCES lot(id),
  CONSTRAINT fk_sb_wh  FOREIGN KEY (warehouse_id) REFERENCES warehouse(id),
  CONSTRAINT ck_sb_nonneg CHECK (qty >= 0)
) ENGINE=InnoDB;

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

CREATE TABLE sale_line (
  id                 INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  sale_id            INT UNSIGNED NOT NULL,
  line_no            SMALLINT UNSIGNED NOT NULL,
  lot_id             INT UNSIGNED NOT NULL,
  qty                DECIMAL(14,3) NOT NULL,
  unit_price         DECIMAL(14,4) NOT NULL,
  line_total         DECIMAL(14,2) AS (ROUND(qty * unit_price, 2)) STORED,
  stock_movement_id  BIGINT UNSIGNED NOT NULL UNIQUE,     -- her satış satırı → tek stok hareketi
  CONSTRAINT fk_sl_sale FOREIGN KEY (sale_id) REFERENCES sale(id),
  CONSTRAINT fk_sl_lot  FOREIGN KEY (lot_id)  REFERENCES lot(id),
  CONSTRAINT fk_sl_sm   FOREIGN KEY (stock_movement_id) REFERENCES stock_movement(id),
  CONSTRAINT ck_sl_qty  CHECK (qty > 0),
  UNIQUE KEY uq_sl (sale_id, line_no)
) ENGINE=InnoDB;

-- ---------------------------------------------------------------------
-- 8. Geri çağırma
-- ---------------------------------------------------------------------
CREATE TABLE recall (
  id            INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  recall_no     VARCHAR(30)  NOT NULL UNIQUE,
  root_lot_id   INT UNSIGNED NOT NULL,
  reason        VARCHAR(500) NOT NULL,
  severity      ENUM('CLASS_I','CLASS_II','CLASS_III') NOT NULL DEFAULT 'CLASS_I',
  status        ENUM('OPEN','CLOSED') NOT NULL DEFAULT 'OPEN',
  initiated_by  VARCHAR(64)  NOT NULL,
  initiated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  closed_at     DATETIME(3)  NULL,
  CONSTRAINT fk_recall_lot FOREIGN KEY (root_lot_id) REFERENCES lot(id)
) ENGINE=InnoDB;

CREATE TABLE recall_lot (
  recall_id        INT UNSIGNED NOT NULL,
  lot_id           INT UNSIGNED NOT NULL,
  depth            TINYINT UNSIGNED NOT NULL,              -- 0 = kök lot
  previous_status  VARCHAR(20) NOT NULL,
  PRIMARY KEY (recall_id, lot_id),
  CONSTRAINT fk_rl_recall FOREIGN KEY (recall_id) REFERENCES recall(id),
  CONSTRAINT fk_rl_lot    FOREIGN KEY (lot_id)    REFERENCES lot(id)
) ENGINE=InnoDB;

-- ---------------------------------------------------------------------
-- Recall contact follow-up; physical recovery quantities always come from the stock ledger.
CREATE TABLE recall_customer_followup (
  id             BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  recall_id      INT UNSIGNED NOT NULL,
  customer_id    INT UNSIGNED NOT NULL,
  contact_status ENUM('PENDING','CONTACTED','CONFIRMED') NOT NULL DEFAULT 'PENDING',
  note           VARCHAR(500) NOT NULL DEFAULT '',
  updated_by     VARCHAR(64) NOT NULL,
  updated_at     DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_rcf_customer (recall_id, customer_id),
  CONSTRAINT fk_rcf_recall FOREIGN KEY (recall_id) REFERENCES recall(id),
  CONSTRAINT fk_rcf_customer FOREIGN KEY (customer_id) REFERENCES customer(id)
) ENGINE=InnoDB;

-- 9. KI tahminleri (tahmin + gerçek + sapma birlikte saklanır)
-- ---------------------------------------------------------------------
CREATE TABLE ml_prediction (
  id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  task             ENUM('PRODUCTION_YIELD','PRODUCTION_EKP','BUTCHERY_YIELD') NOT NULL,
  model_name       VARCHAR(50)  NOT NULL,                  -- ali_lgbm, berke_xgb, nuri_cat, stack_meta
  model_version    VARCHAR(30)  NOT NULL,
  ref_type         VARCHAR(30)  NOT NULL,                  -- production_order / butchery_operation
  ref_id           INT UNSIGNED NOT NULL,
  target_name      VARCHAR(50)  NOT NULL,                  -- actual_qty, ekp, but_kg ...
  predicted_value  DECIMAL(14,4) NOT NULL,
  actual_value     DECIMAL(14,4) NULL,
  -- isteğe bağlı: konformal tahmin aralığı (ml-engine/train_model_ali.py, %80) ve anomali bayrağı
  -- (gerçek değer %98 bandın dışında); eski satırlarda NULL
  interval_low     DECIMAL(14,4) NULL,
  interval_high    DECIMAL(14,4) NULL,
  interval_level   DECIMAL(4,3)  NULL,
  is_anomaly       TINYINT(1)    NULL,
  -- kaynak: SEED = seed.sql örneği, DEMO = demo kurulumunda uygulamadan, FIELD = saha kurulumunda
  -- uygulamadan (API ortamı DATA_ORIGIN=FIELD). FIELD "doğrulanmış" demek değildir: doğrulama kaynak
  -- belge ve doğrulayan kişiyle uygulama dışında yapılır (ml-engine/audit_field.py).
  data_origin      ENUM('SEED','DEMO','FIELD') NOT NULL DEFAULT 'DEMO',
  created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  INDEX ix_mlp_ref (ref_type, ref_id)
) ENGINE=InnoDB;

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

-- KI doğruluk ölçümü: model ve basit referans (geçmiş ortalama oran) aynı zaman-bölmeli test
-- döneminde karşılaştırılır (ml-engine/export_ai_report.py üretir). Veri sentetiktir.
CREATE TABLE ml_model_metric (
  id           INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  task         ENUM('PRODUCTION','BUTCHERY') NOT NULL,
  target_name  VARCHAR(30)  NOT NULL,                     -- actual_qty, ekp, part_kg, fire_kg
  unit         VARCHAR(10)  NOT NULL,
  model_name   VARCHAR(50)  NOT NULL,
  role         ENUM('MODEL','BASELINE') NOT NULL,
  eval_split   VARCHAR(30)  NOT NULL,
  n_rows       INT UNSIGNED NOT NULL,
  mae          DECIMAL(12,4) NOT NULL,
  mape_pct     DECIMAL(8,4)  NOT NULL,
  UNIQUE KEY uq_mmm (task, target_name, model_name, eval_split)
) ENGINE=InnoDB;

-- KI anomali uyarıları (ml-engine/anomaly_ali.py): tahmin edilen ile gerçek arasındaki açıklanamayan sapma.
-- Berke'nin API'si v_ml_anomaly_alert'ten okur, status'ü günceller; FALSE_ALARM/RESOLVED geri bildirimi
-- v_ml_anomaly_feedback ile gerçek işletmedeki precision'ı ölçer.
-- API kayıt anında da yazar (api-go/controllers/alerts.go): canlı tahminin %98 bandı dışındaki üretim
-- kabulü/parçalama, son alışlara göre fiyat artışı, kasada reddedilen geri çağrılmış/SKT'si geçmiş lot
-- satışı ve stok sayımında eksik. Uyarı stok, maliyet veya lot durumunu değiştirmez.
CREATE TABLE ml_anomaly_alert (
  id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  task             ENUM('PRODUCTION_YIELD','BUTCHERY_YIELD','PURCHASE_COST','LOT_MOVEMENT') NOT NULL,
  detector         VARCHAR(30)  NOT NULL,                  -- robust_z / mahalanobis_mcd / conformal_band / rule
  model_version    VARCHAR(60)  NOT NULL,                  -- ali-1.0.0+ali-anomaly-1.0.0
  ref_type         VARCHAR(30)  NOT NULL,                  -- production_order / butchery_operation / purchase_receipt_line / lot / stock_count_line
  ref_id           INT UNSIGNED NOT NULL,
  event_ref        VARCHAR(64)  NOT NULL DEFAULT '',       -- tek olay (POS satışının UUIDv7'si); model uyarılarında boş
  score            DECIMAL(10,3) NOT NULL,                 -- üretim: z (negatif = düşük verim); parçalama: d²; canlı: sapma / %98 bandı; fiyat: % artış
  threshold_warn   DECIMAL(10,3) NOT NULL,
  threshold_crit   DECIMAL(10,3) NOT NULL,
  severity         ENUM('WARN','CRITICAL') NOT NULL,
  direction        ENUM('LOW_YIELD','HIGH_YIELD','FIRE_HIGH','PART_MISMATCH','COST_INCREASE','BLOCKED_SALE','EXPIRED_SALE','COUNT_SHORTAGE') NOT NULL,
  expected_value   DECIMAL(14,3) NOT NULL,                 -- üretim: paket; parçalama: fire kg; alış: referans birim fiyat; satış: 0; sayım: defter bakiyesi
  actual_value     DECIMAL(14,3) NOT NULL,
  deviation        DECIMAL(14,3) NOT NULL,                 -- gerçek − beklenen
  est_loss_eur     DECIMAL(12,2) NOT NULL DEFAULT 0,       -- eksik paket × EKP / fazla fire × alış fiyatı / fiyat farkı × miktar / sayım eksiği × lot maliyeti
  top_parts        VARCHAR(120) NULL,                      -- 'fire +0.95 kg, but -0.90 kg'; kural uyarılarında kısa ayrıntı
  message          VARCHAR(255) NOT NULL,
  status           ENUM('OPEN','ACKNOWLEDGED','RESOLVED','FALSE_ALARM') NOT NULL DEFAULT 'OPEN',
  resolved_by      VARCHAR(64)  NULL,
  resolved_at      DATETIME(3)  NULL,
  resolution_note  VARCHAR(255) NULL,
  created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_mla_ref (task, ref_type, ref_id, model_version, event_ref),   -- yeniden çalıştırma idempotent
  INDEX ix_mla_open (status, severity)
) ENGINE=InnoDB;

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

-- ---------------------------------------------------------------------
-- 10. Denetim günlüğü ve senkronizasyon idempotency (Nuri ile ortak)
-- ---------------------------------------------------------------------
CREATE TABLE audit_log (
  id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  table_name  VARCHAR(50)  NOT NULL,
  row_id      BIGINT UNSIGNED NOT NULL,
  action      VARCHAR(30)  NOT NULL,
  old_value   JSON NULL,
  new_value   JSON NULL,
  changed_by  VARCHAR(64)  NULL,
  changed_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB;

-- POS kiosk kilidini açabilen yetkililer. PIN yalnızca bcrypt özeti olarak saklanır;
-- her deneme (başarılı/başarısız) audit_log'a 'terminal_lock' kaydı olarak yazılır.
CREATE TABLE supervisor (
  id            INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  username      VARCHAR(50)  NOT NULL UNIQUE,
  display_name  VARCHAR(100) NOT NULL,
  pin_hash      VARCHAR(100) NOT NULL,                   -- bcrypt ($2a$/$2b$, cost >= 10)
  is_active     BOOLEAN      NOT NULL DEFAULT TRUE,
  created_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  CONSTRAINT ck_sup_hash CHECK (pin_hash LIKE '$2%')
) ENGINE=InnoDB;

CREATE TABLE sync_inbox (
  event_uuid    CHAR(36)    PRIMARY KEY,                 -- outbox paketindeki UUIDv7
  device_id     VARCHAR(64) NOT NULL,
  entity        VARCHAR(30) NOT NULL,                    -- sale, return, stock_count ...
  payload_hash  CHAR(64)    NOT NULL,                    -- SHA-256 (HMAC doğrulaması API'de)
  received_at   DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB;

-- POS cihaz anahtarları (ECDSA P-256 SPKI). İlk imzalı senkronda sabitlenir (TOFU) veya
-- yetkili POST /sync/register-key ile kaydeder/döndürür; farklı anahtarla gelen senkron reddedilir.
CREATE TABLE pos_device_key (
  device_id       VARCHAR(64)  PRIMARY KEY,
  public_key_b64  VARCHAR(255) NOT NULL,
  fingerprint     CHAR(64)     NOT NULL,                 -- SHA-256(SPKI DER), hex
  enrolled_by     VARCHAR(64)  NOT NULL,                 -- 'tofu' veya yetkili kullanıcı adı
  enrolled_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB;

-- =====================================================================
-- Raporlama görünümleri (maliyet, stok, mutabakat, KI sapması)
-- =====================================================================
USE food_intel;

-- Lot × depo stok bakiyesi ve değeri
CREATE OR REPLACE VIEW v_stock_by_lot AS
SELECT l.lot_no, p.sku, p.name AS product_name, w.code AS warehouse_code,
       sb.qty, l.uom_code, l.unit_cost,
       ROUND(sb.qty * l.unit_cost, 2) AS stock_value, l.status, l.expiry_date
FROM stock_balance sb
JOIN lot l       ON l.id = sb.lot_id
JOIN product p   ON p.id = l.product_id
JOIN warehouse w ON w.id = sb.warehouse_id;

-- Defterden yeniden hesaplanan bakiye (stock_balance önbelleğinin denetimi için)
CREATE OR REPLACE VIEW v_stock_ledger_check AS
SELECT sm.lot_id, sm.warehouse_id,
       SUM(sm.qty)                 AS ledger_qty,
       COALESCE(MAX(sb.qty), 0)    AS balance_qty,
       SUM(sm.qty) - COALESCE(MAX(sb.qty), 0) AS diff
FROM stock_movement sm
LEFT JOIN stock_balance sb ON sb.lot_id = sm.lot_id AND sb.warehouse_id = sm.warehouse_id
GROUP BY sm.lot_id, sm.warehouse_id;

-- Görev 1: maliyet kırılımı (şartnamedeki tablo ile birebir)
CREATE OR REPLACE VIEW v_production_cost_breakdown AS
SELECT po.order_no, 1 AS sort_key, l.lot_no, p.name AS cost_element,
       pi.qty_consumed AS qty, l.uom_code, pi.unit_cost,
       ROUND(pi.line_cost, 2) AS amount
FROM production_order po
JOIN production_input pi ON pi.production_order_id = po.id
JOIN lot l     ON l.id = pi.lot_id
JOIN product p ON p.id = l.product_id
UNION ALL
SELECT po.order_no, 2, po.service_invoice_no, 'Fason hizmet / işçilik',
       po.actual_qty, 'PAKET', po.service_unit_price,
       ROUND(po.actual_qty * po.service_unit_price, 2)
FROM production_order po
WHERE po.actual_qty IS NOT NULL;

-- Görev 1: EKP, VKP, brüt kâr ve marj
CREATE OR REPLACE VIEW v_production_cost AS
SELECT po.order_no,
       p.sku, p.name AS product_name,
       po.status,
       po.planned_qty,
       po.actual_qty,
       po.actual_qty - po.planned_qty                                AS qty_variance,
       ROUND(100 * po.actual_qty / po.planned_qty, 2)                AS fulfilment_pct,
       m.input_kg,
       ROUND(100 * po.actual_qty * COALESCE(p.pack_size_kg, 1) / NULLIF(m.main_input_kg, 0), 2) AS yield_pct_of_meat,
       ROUND(m.material_cost, 2)                                     AS material_cost,
       ROUND(po.actual_qty * po.service_unit_price, 2)               AS service_cost,
       ROUND(m.material_cost + po.actual_qty * po.service_unit_price, 2) AS total_cost,
       ROUND((m.material_cost + po.actual_qty * po.service_unit_price) / po.actual_qty, 4) AS ekp,
       ROUND((m.material_cost + po.actual_qty * po.service_unit_price) / po.actual_qty, 2) AS ekp_display,
       p.sale_price                                                  AS vkp,
       ROUND(p.sale_price - (m.material_cost + po.actual_qty * po.service_unit_price) / po.actual_qty, 2) AS gross_profit_per_unit,
       ROUND(po.actual_qty * p.sale_price, 2)                        AS revenue_at_vkp,
       ROUND(po.actual_qty * p.sale_price - (m.material_cost + po.actual_qty * po.service_unit_price), 2) AS gross_profit_total,
       ROUND(100 * (1 - (m.material_cost + po.actual_qty * po.service_unit_price) / (po.actual_qty * p.sale_price)), 2) AS margin_pct
FROM production_order po
JOIN product p ON p.id = po.product_id
JOIN (
  SELECT pi.production_order_id,
         SUM(pi.line_cost) AS material_cost,
         SUM(IF(l.uom_code = 'KG', pi.qty_consumed, 0)) AS input_kg,
         MAX(IF(pr.sku LIKE 'HM-ET%', pi.qty_consumed, 0)) AS main_input_kg
  FROM production_input pi
  JOIN lot l      ON l.id = pi.lot_id
  JOIN product pr ON pr.id = l.product_id
  GROUP BY pi.production_order_id
) m ON m.production_order_id = po.id
WHERE po.actual_qty > 0;

-- Görev 2: parçalama mutabakat kapısı (ağırlık dengesi + maliyet dengesi)
CREATE OR REPLACE VIEW v_butchery_reconciliation AS
SELECT bo.op_no, bo.species, bo.allocation_method, bo.status,
       bo.input_weight_kg,
       o.output_weight_kg,
       o.cut_weight_kg,
       o.waste_weight_kg,
       bo.input_weight_kg - o.output_weight_kg                   AS weight_diff_kg,
       bo.purchase_unit_price,
       bo.purchase_cost,
       bo.additional_cost,
       bo.total_cost,
       bo.declared_total_cost,
       bo.declared_total_cost - bo.total_cost                    AS declared_cost_diff,
       o.allocated_total,
       bo.total_cost - o.allocated_total                         AS allocation_diff,
       (ABS(bo.input_weight_kg - o.output_weight_kg) <= 0.010)   AS weight_ok,
       (bo.declared_total_cost IS NULL
        OR ABS(bo.declared_total_cost - bo.total_cost) < 0.005)  AS declared_cost_ok,
       (ABS(bo.total_cost - o.allocated_total) < 0.005)          AS allocation_ok
FROM butchery_operation bo
JOIN (
  SELECT operation_id,
         SUM(weight_kg)                               AS output_weight_kg,
         SUM(IF(output_kind <> 'WASTE', weight_kg, 0)) AS cut_weight_kg,
         SUM(IF(output_kind =  'WASTE', weight_kg, 0)) AS waste_weight_kg,
         SUM(allocated_cost)                          AS allocated_total
  FROM butchery_output GROUP BY operation_id
) o ON o.operation_id = bo.id;

CREATE OR REPLACE VIEW v_butchery_cost_allocation AS
SELECT bo.op_no, p.name AS product_name, bout.output_kind, l.lot_no,
       bout.weight_kg,
       ROUND(100 * bout.weight_kg / bo.input_weight_kg, 2) AS yield_pct,
       bout.allocation_basis, bout.allocated_cost, bout.unit_cost
FROM butchery_output bout
JOIN butchery_operation bo ON bo.id = bout.operation_id
JOIN product p ON p.id = bout.product_id
LEFT JOIN lot l ON l.id = bout.lot_id;

-- KI: tahmin, gerçek ve sapma birlikte (şartname 4A/4B)
CREATE OR REPLACE VIEW v_ml_prediction_deviation AS
SELECT id, task, model_name, model_version, ref_type, ref_id, target_name,
       predicted_value, actual_value,
       actual_value - predicted_value                                      AS deviation,
       ROUND(100 * (actual_value - predicted_value) / NULLIF(actual_value, 0), 2) AS deviation_pct,
       created_at
FROM ml_prediction;

-- KI anomali uyarıları: API/panel için okunabilir hali (emir/parçalama/alış no, mamul, karkas ya da alış lotu)
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

-- Gerçek kullanımda ölçülebilirlik: kapatılan uyarılardan precision (RESOLVED = gerçek sorun)
CREATE OR REPLACE VIEW v_ml_anomaly_feedback AS
SELECT task, model_version,
       COUNT(*)                                                AS alerts,
       SUM(status = 'OPEN' OR status = 'ACKNOWLEDGED')         AS open_alerts,
       SUM(status = 'RESOLVED')                                AS confirmed,
       SUM(status = 'FALSE_ALARM')                             AS false_alarms,
       ROUND(SUM(status = 'RESOLVED') / NULLIF(SUM(status IN ('RESOLVED','FALSE_ALARM')), 0), 3) AS precision_observed,
       SUM(IF(status = 'RESOLVED', est_loss_eur, 0))           AS confirmed_loss_eur
FROM ml_anomaly_alert
GROUP BY task, model_version;

-- KI veri sözleşmesi (ml-engine/DATA_CONTRACT.md) ile aynı kolonlar: gerçek kayıtlar
-- biriktikçe model sentetik veri yerine doğrudan bu görünümlerden eğitilir.
CREATE OR REPLACE VIEW v_ml_production_features AS
SELECT CONCAT('DB-P-', po.order_no)                           AS row_id,
       'db'                                                   AS source,
       po.order_date,
       p.sku                                                  AS product_sku,
       p.pack_size_kg,
       sub.code                                               AS subcontractor_code,
       MAX(IF(rp.sku = 'HM-ET-DANA', ms.code, NULL))          AS meat_supplier_code,
       SUM(IF(rp.sku = 'HM-ET-DANA',  pi.qty_consumed, 0))    AS meat_kg,
       SUM(IF(rp.sku = 'HM-BAHARAT',  pi.qty_consumed, 0))    AS spice_kg,
       SUM(IF(rp.sku = 'HM-BAGIRSAK', pi.qty_consumed, 0))    AS casing_kg,
       MAX(IF(rp.sku = 'HM-ET-DANA', l.fat_pct, NULL))        AS meat_fat_pct,
       MAX(IF(rp.sku = 'HM-ET-DANA', DATEDIFF(po.order_date, l.lot_date), NULL)) AS meat_age_days,
       po.drying_days,
       MAX(IF(rp.sku = 'HM-ET-DANA',  pi.unit_cost, NULL))    AS meat_unit_price,
       MAX(IF(rp.sku = 'HM-BAHARAT',  pi.unit_cost, NULL))    AS spice_unit_price,
       MAX(IF(rp.sku = 'HM-BAGIRSAK', pi.unit_cost, NULL))    AS casing_unit_price,
       po.service_unit_price,
       po.planned_qty,
       po.actual_qty,
       ROUND(100 * po.actual_qty * p.pack_size_kg / NULLIF(SUM(IF(rp.sku = 'HM-ET-DANA', pi.qty_consumed, 0)), 0), 3) AS yield_pct,
       ROUND((SUM(pi.line_cost) + po.actual_qty * po.service_unit_price) / po.actual_qty, 4) AS ekp
FROM production_order po
JOIN product p   ON p.id = po.product_id
LEFT JOIN supplier sub ON sub.id = po.subcontractor_id
JOIN production_input pi ON pi.production_order_id = po.id
JOIN lot l       ON l.id = pi.lot_id
JOIN product rp  ON rp.id = l.product_id
LEFT JOIN supplier ms ON ms.id = l.supplier_id
WHERE po.status IN ('RECEIVED','CLOSED') AND po.actual_qty > 0
GROUP BY po.id, po.order_no, po.order_date, p.sku, p.pack_size_kg, sub.code, po.drying_days,
         po.service_unit_price, po.planned_qty, po.actual_qty;

-- Aynı girdiler her durumdaki emir için (hedef kolonları yok): API yeni bir emrin verimini
-- ve EKP'sini malzeme sevk edilir edilmez buradan tahmin eder (api-go ai_live.go).
CREATE OR REPLACE VIEW v_ml_production_order_inputs AS
SELECT po.id                                                  AS order_id,
       po.order_no,
       po.status,
       po.order_date,
       p.sku                                                  AS product_sku,
       p.pack_size_kg,
       sub.code                                               AS subcontractor_code,
       MAX(IF(rp.sku = 'HM-ET-DANA', ms.code, NULL))          AS meat_supplier_code,
       SUM(IF(rp.sku = 'HM-ET-DANA',  pi.qty_consumed, 0))    AS meat_kg,
       SUM(IF(rp.sku = 'HM-BAHARAT',  pi.qty_consumed, 0))    AS spice_kg,
       SUM(IF(rp.sku = 'HM-BAGIRSAK', pi.qty_consumed, 0))    AS casing_kg,
       MAX(IF(rp.sku = 'HM-ET-DANA', l.fat_pct, NULL))        AS meat_fat_pct,
       MAX(IF(rp.sku = 'HM-ET-DANA', DATEDIFF(po.order_date, l.lot_date), NULL)) AS meat_age_days,
       po.drying_days,
       po.planned_qty,
       po.service_unit_price,
       SUM(pi.line_cost)                                      AS material_cost
FROM production_order po
JOIN product p   ON p.id = po.product_id
LEFT JOIN supplier sub ON sub.id = po.subcontractor_id
JOIN production_input pi ON pi.production_order_id = po.id
JOIN lot l       ON l.id = pi.lot_id
JOIN product rp  ON rp.id = l.product_id
LEFT JOIN supplier ms ON ms.id = l.supplier_id
GROUP BY po.id, po.order_no, po.status, po.order_date, p.sku, p.pack_size_kg, sub.code, po.drying_days,
         po.planned_qty, po.service_unit_price;

CREATE OR REPLACE VIEW v_ml_butchery_features AS
SELECT CONCAT('DB-B-', bo.op_no)          AS row_id,
       'db'                               AS source,
       bo.op_date, bo.species,
       l.animal_breed                     AS breed,
       s.code                             AS supplier_code,
       bo.operator_name                   AS operator_code,
       bo.input_weight_kg                 AS carcass_weight_kg,
       l.animal_age_months                AS age_months,
       l.animal_sex                       AS sex,
       l.fat_score, l.conformation_score, bo.chill_hours,
       bo.purchase_unit_price,
       SUM(IF(p.sku = 'KZ-BUT',     o.weight_kg, 0)) AS but_kg,
       SUM(IF(p.sku = 'KZ-SIRT',    o.weight_kg, 0)) AS sirt_kg,
       SUM(IF(p.sku = 'KZ-OMUZ',    o.weight_kg, 0)) AS omuz_kg,
       SUM(IF(p.sku = 'KZ-KABURGA', o.weight_kg, 0)) AS kaburga_kg,
       SUM(IF(p.sku = 'KZ-KIYMA',   o.weight_kg, 0)) AS kiyma_kg,
       SUM(IF(o.output_kind = 'WASTE', o.weight_kg, 0)) AS fire_kg
FROM butchery_operation bo
JOIN lot l ON l.id = bo.input_lot_id
LEFT JOIN supplier s ON s.id = l.supplier_id
JOIN butchery_output o ON o.operation_id = bo.id
JOIN product p ON p.id = o.product_id
WHERE bo.status = 'POSTED'
GROUP BY bo.id, bo.op_no, bo.op_date, bo.species, l.animal_breed, s.code, bo.operator_name, bo.input_weight_kg,
         l.animal_age_months, l.animal_sex, l.fat_score, l.conformation_score, bo.chill_hours, bo.purchase_unit_price;

-- =====================================================================
-- Tetikleyiciler ve saklı yordamlar
--   * stock_movement: işaret kontrolü, negatif stok engeli, bloke lot engeli,
--     değiştirilemezlik (UPDATE/DELETE yasak), bakiye güncelleme
--   * shipment_line / sale_line: otomatik stok hareketi
--   * Üretim: sp_production_add_input, sp_production_complete (EKP)
--   * İzlenebilirlik: sp_trace_forward, sp_trace_backward (WITH RECURSIVE)
--   * Geri çağırma: sp_recall_initiate, sp_recall_report
-- =====================================================================
USE food_intel;

DELIMITER $$

-- ---------------------------------------------------------------------
-- Stok defteri
-- ---------------------------------------------------------------------
CREATE TRIGGER trg_sm_before_insert
BEFORE INSERT ON stock_movement
FOR EACH ROW
BEGIN
  DECLARE v_status VARCHAR(20);
  DECLARE v_expiry DATE;
  DECLARE v_balance DECIMAL(14,3);

  -- 1) Hareket türü ile işaret uyumlu olmalı
  IF NEW.movement_type IN ('PURCHASE_IN','TRANSFER_IN','PRODUCTION_RECEIPT','BUTCHERY_OUTPUT',
                           'RETURN','REWORK_OUTPUT','ADJUSTMENT_IN') AND NEW.qty <= 0 THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'MOVEMENT_SIGN: inbound movement must have qty > 0';
  END IF;
  IF NEW.movement_type IN ('TRANSFER_OUT','PRODUCTION_CONSUME','BUTCHERY_CONSUME','SHIPMENT',
                           'SALE','REWORK_CONSUME','WASTE','ADJUSTMENT_OUT') AND NEW.qty >= 0 THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'MOVEMENT_SIGN: outbound movement must have qty < 0';
  END IF;

  -- 2) Geri çağrılan / bloke lot satılamaz, sevk edilemez, üretimde kullanılamaz.
  --    (İade, imha ve karantinaya transfer serbest.)
  SELECT status, expiry_date INTO v_status, v_expiry FROM lot WHERE id = NEW.lot_id;
  IF v_status IN ('BLOCKED','RECALLED') AND NEW.movement_type IN
     ('SALE','SHIPMENT','PRODUCTION_CONSUME','BUTCHERY_CONSUME','REWORK_CONSUME') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'LOT_BLOCKED: lot is blocked or recalled';
  END IF;

  -- 2b) SKT'si geçmiş lot müşteriye satılamaz ve sevk edilemez. Ölçüt hareketin kendi zamanı:
  --     SKT'den önce çevrimdışı yapılan bir POS satışı sonradan senkronlansa da geçerlidir.
  --     Üretim/parçalama tüketimi emir kapanınca, fiili kullanımdan sonra yazıldığı için kapsam dışı.
  IF v_expiry IS NOT NULL AND DATE(COALESCE(NEW.moved_at, NOW(3))) > v_expiry
     AND NEW.movement_type IN ('SALE','SHIPMENT') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'LOT_EXPIRED: lot is past its expiry date';
  END IF;

  -- 3) Negatif stok engeli (bakiye satırı kilitlenir -> eşzamanlı satışlarda yarış yok)
  IF NEW.qty < 0 THEN
    SELECT qty INTO v_balance FROM stock_balance
     WHERE lot_id = NEW.lot_id AND warehouse_id = NEW.warehouse_id
     FOR UPDATE;
    IF COALESCE(v_balance, 0) + NEW.qty < 0 THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'INSUFFICIENT_STOCK: movement would make lot balance negative';
    END IF;
  END IF;
END$$

CREATE TRIGGER trg_sm_after_insert
AFTER INSERT ON stock_movement
FOR EACH ROW
BEGIN
  -- Çıkışta satır zaten var (BEFORE tetikleyicisi kilitledi). Upsert kullanılmaz:
  -- MySQL CHECK'i aday satırın (negatif qty) üzerinde de değerlendirir.
  IF NEW.qty > 0 THEN
    INSERT INTO stock_balance (lot_id, warehouse_id, qty)
    VALUES (NEW.lot_id, NEW.warehouse_id, NEW.qty)
    ON DUPLICATE KEY UPDATE qty = qty + NEW.qty;
  ELSE
    UPDATE stock_balance SET qty = qty + NEW.qty
     WHERE lot_id = NEW.lot_id AND warehouse_id = NEW.warehouse_id;
  END IF;
END$$

CREATE TRIGGER trg_sm_no_update
BEFORE UPDATE ON stock_movement
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IMMUTABLE: stock_movement is append-only; post a correcting movement';
END$$

CREATE TRIGGER trg_sm_no_delete
BEFORE DELETE ON stock_movement
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IMMUTABLE: stock_movement is append-only; post a correcting movement';
END$$

CREATE TRIGGER trg_audit_no_update
BEFORE UPDATE ON audit_log
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IMMUTABLE: audit_log is append-only';
END$$

CREATE TRIGGER trg_audit_no_delete
BEFORE DELETE ON audit_log
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IMMUTABLE: audit_log is append-only';
END$$

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

-- Lot durum değişiklikleri denetim günlüğüne
-- Hammadde girişi: tedarikçi (CHECK ck_lot_purchase_supplier) ve raf ömrü olan üründe SKT zorunlu,
-- SKT giriş tarihinden önce olamaz (şartname 3: "uygun olduğunda son tüketim tarihi").
CREATE TRIGGER trg_lot_before_insert
BEFORE INSERT ON lot
FOR EACH ROW
BEGIN
  DECLARE v_shelf INT;
  IF NEW.origin_type = 'PURCHASE' THEN
    SELECT shelf_life_days INTO v_shelf FROM product WHERE id = NEW.product_id;
    IF v_shelf IS NOT NULL AND NEW.expiry_date IS NULL THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'EXPIRY_REQUIRED: purchased lot of a perishable product needs expiry_date';
    END IF;
    IF NEW.expiry_date IS NOT NULL AND NEW.expiry_date < NEW.lot_date THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'EXPIRY_REQUIRED: expiry_date is before lot_date';
    END IF;
  END IF;
END$$

CREATE TRIGGER trg_lot_status_audit
AFTER UPDATE ON lot
FOR EACH ROW
BEGIN
  IF NOT (OLD.status <=> NEW.status) THEN
    INSERT INTO audit_log (table_name, row_id, action, old_value, new_value, changed_by)
    VALUES ('lot', NEW.id, 'STATUS_CHANGE',
            JSON_OBJECT('lot_no', OLD.lot_no, 'status', OLD.status),
            JSON_OBJECT('lot_no', NEW.lot_no, 'status', NEW.status, 'reason', NEW.blocked_reason),
            COALESCE(@app_user, CURRENT_USER()));
  END IF;
END$$

-- 1. kademe sevkiyat satırı -> SHIPMENT stok hareketi (bloke lot ise reddedilir)
CREATE TRIGGER trg_shl_after_insert
AFTER INSERT ON shipment_line
FOR EACH ROW
BEGIN
  DECLARE v_wh INT UNSIGNED;
  SELECT from_warehouse_id INTO v_wh FROM shipment WHERE id = NEW.shipment_id;
  IF v_wh IS NOT NULL THEN
    INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, ref_id, created_by)
    SELECT NEW.lot_id, v_wh, 'SHIPMENT', -NEW.qty, l.unit_cost, 'shipment', NEW.shipment_id, COALESCE(@app_user, 'system')
    FROM lot l WHERE l.id = NEW.lot_id;
  END IF;
END$$

-- POS satış satırı -> SALE stok hareketi; satır hareketin id'sini saklar
CREATE TRIGGER trg_sl_before_insert
BEFORE INSERT ON sale_line
FOR EACH ROW
BEGIN
  DECLARE v_wh INT UNSIGNED;
  DECLARE v_sold_at DATETIME(3);
  IF NEW.stock_movement_id IS NULL THEN
    -- hareket zamanı = satış zamanı (çevrimdışı satışlar sonradan senkronize olur)
    SELECT warehouse_id, sold_at INTO v_wh, v_sold_at FROM sale WHERE id = NEW.sale_id;
    INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, ref_id, moved_at, created_by)
    SELECT NEW.lot_id, v_wh, 'SALE', -NEW.qty, l.unit_cost, 'sale', NEW.sale_id, v_sold_at, COALESCE(@app_user, 'pos')
    FROM lot l WHERE l.id = NEW.lot_id;
    SET NEW.stock_movement_id = LAST_INSERT_ID();
  END IF;
END$$

-- ---------------------------------------------------------------------
-- Görev 1: fason üretim
-- ---------------------------------------------------------------------

-- Hammadde lotunu fasona sevk et (transfer + üretim girdisi, maliyet snapshot)
-- Hammaddeyi fasona sevk eder (transfer + maliyet snapshot'ı). Transaction açmaz: çağıranın
-- transaction'ına katılır, böylece API emri ve tüm girdilerini tek seferde kaydedebilir.
CREATE PROCEDURE sp_production_send_input(
  IN p_order_no VARCHAR(30), IN p_lot_no VARCHAR(30), IN p_qty DECIMAL(14,3),
  IN p_from_wh VARCHAR(20), IN p_to_wh VARCHAR(20), IN p_user VARCHAR(64))
BEGIN
  DECLARE v_order INT UNSIGNED; DECLARE v_status VARCHAR(20);
  DECLARE v_lot INT UNSIGNED;   DECLARE v_cost DECIMAL(14,4);
  DECLARE v_lot_status VARCHAR(20); DECLARE v_type VARCHAR(30);
  DECLARE v_expiry DATE; DECLARE v_lot_date DATE; DECLARE v_order_date DATE;
  DECLARE v_from INT UNSIGNED;  DECLARE v_to INT UNSIGNED;

  SELECT id, status, order_date INTO v_order, v_status, v_order_date FROM production_order WHERE order_no = p_order_no FOR UPDATE;
  IF v_order IS NULL OR v_status NOT IN ('PLANNED','MATERIAL_SENT') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'ORDER_STATE: order not found or already received';
  END IF;
  SELECT l.id, l.unit_cost, l.status, l.expiry_date, l.lot_date, p.product_type
    INTO v_lot, v_cost, v_lot_status, v_expiry, v_lot_date, v_type
    FROM lot l JOIN product p ON p.id = l.product_id WHERE l.lot_no = p_lot_no FOR SHARE;
  IF v_lot IS NULL THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'LOT_NOT_FOUND: input lot does not exist';
  END IF;
  IF v_type <> 'RAW_MATERIAL' OR v_lot_date > v_order_date THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'INVALID_INPUT: expected raw material received by order date';
  END IF;
  IF v_lot_status IN ('BLOCKED','RECALLED') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'LOT_BLOCKED: input lot is blocked or recalled';
  END IF;
  -- Historical orders use their recorded dispatch date; delayed receipt does not age the input.
  IF v_expiry IS NOT NULL AND v_expiry < v_order_date THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'LOT_EXPIRED: input expired before order date';
  END IF;
  IF p_from_wh = p_to_wh THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'INVALID_INPUT: dispatch warehouses must differ';
  END IF;
  SELECT id INTO v_from FROM warehouse WHERE code = p_from_wh;
  SELECT id INTO v_to   FROM warehouse WHERE code = p_to_wh;

  INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, ref_id, created_by, note)
  VALUES (v_lot, v_from, 'TRANSFER_OUT', -p_qty, v_cost, 'production_order', v_order, p_user, CONCAT('Fasona sevk ', p_order_no)),
         (v_lot, v_to,   'TRANSFER_IN',   p_qty, v_cost, 'production_order', v_order, p_user, CONCAT('Fasona sevk ', p_order_no));

  INSERT INTO production_input (production_order_id, lot_id, qty_sent, unit_cost)
  VALUES (v_order, v_lot, p_qty, v_cost);

  UPDATE production_order SET status = 'MATERIAL_SENT' WHERE id = v_order;
END$$

CREATE PROCEDURE sp_production_add_input(
  IN p_order_no VARCHAR(30), IN p_lot_no VARCHAR(30), IN p_qty DECIMAL(14,3),
  IN p_from_wh VARCHAR(20), IN p_to_wh VARCHAR(20))
BEGIN
  DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN ROLLBACK; RESIGNAL; END;
  START TRANSACTION;
  CALL sp_production_send_input(p_order_no, p_lot_no, p_qty, p_from_wh, p_to_wh, 'system');
  COMMIT;
END$$

-- Mamul kabulü: gerçek miktara göre EKP hesapla, mamul lotu aç, tüketimi düş,
-- soyağacı kenarlarını (lot_link) yaz. Tek transaction.
--   EKP = (Σ tüketilen hammadde × lot maliyeti + gerçek miktar × hizmet birim fiyatı) / gerçek miktar
CREATE PROCEDURE sp_production_complete(
  IN p_order_no VARCHAR(30), IN p_actual_qty DECIMAL(14,3),
  IN p_output_lot_no VARCHAR(30), IN p_lot_date DATE, IN p_expiry DATE,
  IN p_sub_wh VARCHAR(20), IN p_target_wh VARCHAR(20), IN p_invoice_no VARCHAR(50))
BEGIN
  DECLARE v_order INT UNSIGNED; DECLARE v_status VARCHAR(20);
  DECLARE v_product INT UNSIGNED; DECLARE v_uom VARCHAR(10);
  DECLARE v_service DECIMAL(14,4); DECLARE v_material DECIMAL(16,4);
  DECLARE v_ekp DECIMAL(14,4); DECLARE v_lot INT UNSIGNED;
  DECLARE v_sub INT UNSIGNED; DECLARE v_target INT UNSIGNED;
  DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN ROLLBACK; RESIGNAL; END;

  IF p_actual_qty IS NULL OR p_actual_qty <= 0 THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'ACTUAL_QTY: must be > 0';
  END IF;

  START TRANSACTION;
  SELECT po.id, po.status, po.product_id, p.uom_code, po.service_unit_price
    INTO v_order, v_status, v_product, v_uom, v_service
    FROM production_order po JOIN product p ON p.id = po.product_id
   WHERE po.order_no = p_order_no FOR UPDATE;
  IF v_order IS NULL OR v_status <> 'MATERIAL_SENT' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'ORDER_STATE: material must be sent before receipt';
  END IF;
  SELECT id INTO v_sub    FROM warehouse WHERE code = p_sub_wh;
  SELECT id INTO v_target FROM warehouse WHERE code = p_target_wh;

  SELECT SUM(line_cost) INTO v_material FROM production_input WHERE production_order_id = v_order;
  SET v_ekp = ROUND((v_material + p_actual_qty * v_service) / p_actual_qty, 4);

  INSERT INTO lot (lot_no, product_id, origin_type, lot_date, expiry_date, initial_qty, uom_code, unit_cost)
  VALUES (p_output_lot_no, v_product, 'PRODUCTION', p_lot_date, p_expiry, p_actual_qty, v_uom, v_ekp);
  SET v_lot = LAST_INSERT_ID();

  INSERT INTO production_output (production_order_id, lot_id, qty, unit_cost)
  VALUES (v_order, v_lot, p_actual_qty, v_ekp);

  -- fasonda tüketilen hammadde
  INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, ref_id, note)
  SELECT pi.lot_id, v_sub, 'PRODUCTION_CONSUME', -pi.qty_consumed, pi.unit_cost, 'production_order', v_order, p_order_no
    FROM production_input pi WHERE pi.production_order_id = v_order AND pi.qty_consumed > 0;

  -- mamul kabulü
  INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, ref_id, note)
  VALUES (v_lot, v_target, 'PRODUCTION_RECEIPT', p_actual_qty, v_ekp, 'production_order', v_order, p_order_no);

  -- soyağacı: her girdi lotu -> mamul lotu
  INSERT INTO lot_link (parent_lot_id, child_lot_id, link_type, qty_consumed, qty_produced, ref_type, ref_id)
  SELECT pi.lot_id, v_lot, 'PRODUCTION', pi.qty_consumed, p_actual_qty, 'production_order', v_order
    FROM production_input pi WHERE pi.production_order_id = v_order AND pi.qty_consumed > 0;

  UPDATE production_order
     SET actual_qty = p_actual_qty, status = 'RECEIVED', received_date = p_lot_date,
         service_invoice_no = p_invoice_no,
         service_invoice_total = ROUND(p_actual_qty * v_service, 2)
   WHERE id = v_order;
  COMMIT;
END$$

-- ---------------------------------------------------------------------
-- Görev 3: izlenebilirlik (WITH RECURSIVE)
-- ---------------------------------------------------------------------

-- Yardımcı: kök lottan ileri (DOWN) veya geri (UP) tüm lotları tmp_trace_lot'a yazar.
-- Döngü koruması: yol (path) içinde aynı lot ikinci kez yer alamaz + derinlik sınırı.
CREATE PROCEDURE sp_fill_trace(IN p_lot_no VARCHAR(30), IN p_direction VARCHAR(4))
BEGIN
  DROP TEMPORARY TABLE IF EXISTS tmp_trace_lot;
  CREATE TEMPORARY TABLE tmp_trace_lot (
    lot_id INT UNSIGNED PRIMARY KEY,
    depth  TINYINT UNSIGNED NOT NULL,
    path   VARCHAR(1000) NOT NULL
  );

  IF p_direction = 'DOWN' THEN
    INSERT INTO tmp_trace_lot (lot_id, depth, path)
    WITH RECURSIVE fwd (lot_id, depth, path) AS (
      SELECT l.id, 0, CAST(CONCAT('/', l.lot_no, '/') AS CHAR(1000))
        FROM lot l WHERE l.lot_no = p_lot_no
      UNION ALL
      -- Join sırası sabit: bulunan lot -> bağlantı -> çocuk. İpucu olmadan optimizer bazen bulunan
      -- lotları tüm lot tablosuyla çaprazlıyordu (4.000 lotta ~20 sn yerine ~25 ms).
      SELECT /*+ JOIN_ORDER(f, ll, c) */ ll.child_lot_id, f.depth + 1, CONCAT(f.path, c.lot_no, '/')
        FROM fwd f
        JOIN lot_link ll ON ll.parent_lot_id = f.lot_id
        JOIN lot c       ON c.id = ll.child_lot_id
       WHERE f.depth < 20 AND LOCATE(CONCAT('/', c.lot_no, '/'), f.path) = 0
    )
    SELECT lot_id, MIN(depth), MIN(path) FROM fwd GROUP BY lot_id;
  ELSE
    INSERT INTO tmp_trace_lot (lot_id, depth, path)
    WITH RECURSIVE bwd (lot_id, depth, path) AS (
      SELECT l.id, 0, CAST(CONCAT('/', l.lot_no, '/') AS CHAR(1000))
        FROM lot l WHERE l.lot_no = p_lot_no
      UNION ALL
      SELECT /*+ JOIN_ORDER(b, ll, pl) */ ll.parent_lot_id, b.depth + 1, CONCAT(b.path, pl.lot_no, '/')
        FROM bwd b
        JOIN lot_link ll ON ll.child_lot_id = b.lot_id
        JOIN lot pl      ON pl.id = ll.parent_lot_id
       WHERE b.depth < 20 AND LOCATE(CONCAT('/', pl.lot_no, '/'), b.path) = 0
    )
    SELECT lot_id, MIN(depth), MIN(path) FROM bwd GROUP BY lot_id;
  END IF;

  IF (SELECT COUNT(*) FROM tmp_trace_lot) = 0 THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'LOT_NOT_FOUND';
  END IF;
END$$

-- İleri izleme: 4 sonuç kümesi
--   1) etkilenen lotlar  2) depolardaki stok  3) sevkiyat/alıcılar (1. ve kaydedilmiş 2. kademe)
--   4) POS satışları
CREATE PROCEDURE sp_trace_forward(IN p_lot_no VARCHAR(30))
BEGIN
  CALL sp_fill_trace(p_lot_no, 'DOWN');

  SELECT t.depth, l.lot_no, p.sku, p.name AS product_name, l.origin_type,
         l.initial_qty, l.uom_code, l.status, l.expiry_date, t.path
    FROM tmp_trace_lot t JOIN lot l ON l.id = t.lot_id JOIN product p ON p.id = l.product_id
   ORDER BY t.depth, l.lot_no;

  SELECT l.lot_no, w.code AS warehouse_code, sb.qty, l.uom_code
    FROM tmp_trace_lot t
    JOIN stock_balance sb ON sb.lot_id = t.lot_id AND sb.qty > 0
    JOIN lot l ON l.id = t.lot_id
    JOIN warehouse w ON w.id = sb.warehouse_id
   ORDER BY l.lot_no, w.code;

  WITH RECURSIVE dist (shipment_line_id, shipment_id, customer_id, via_customer_id, lot_id, qty, tier) AS (
    SELECT sl.id, s.id, s.customer_id, CAST(NULL AS UNSIGNED), sl.lot_id, sl.qty, 1
      FROM tmp_trace_lot t
      JOIN shipment_line sl ON sl.lot_id = t.lot_id
      JOIN shipment s       ON s.id = sl.shipment_id AND s.from_customer_id IS NULL
    UNION ALL
    SELECT sl.id, s.id, s.customer_id, s.from_customer_id, sl.lot_id, sl.qty, d.tier + 1
      FROM dist d
      JOIN shipment s       ON s.from_customer_id = d.customer_id
      JOIN shipment_line sl ON sl.shipment_id = s.id AND sl.lot_id = d.lot_id
     WHERE d.tier < 5
  )
  SELECT MIN(d.tier) AS tier, c.code AS customer_code, c.name AS customer_name,
         c.contact_person, c.phone, c.email,
         vc.name AS received_via, l.lot_no, s.shipment_no, s.ship_date, s.delivery_note_no,
         MAX(d.qty) AS qty, l.uom_code
    FROM dist d
    JOIN shipment s  ON s.id = d.shipment_id
    JOIN customer c  ON c.id = d.customer_id
    LEFT JOIN customer vc ON vc.id = d.via_customer_id
    JOIN lot l       ON l.id = d.lot_id
   GROUP BY d.shipment_line_id, c.code, c.name, c.contact_person, c.phone, c.email,
            vc.name, l.lot_no, s.shipment_no, s.ship_date, s.delivery_note_no, l.uom_code
   ORDER BY tier, c.code, l.lot_no;

  SELECT l.lot_no, w.code AS store_code, COUNT(*) AS sale_lines, SUM(sl.qty) AS qty_sold,
         MIN(s.sold_at) AS first_sale, MAX(s.sold_at) AS last_sale
    FROM tmp_trace_lot t
    JOIN sale_line sl ON sl.lot_id = t.lot_id
    JOIN sale s       ON s.id = sl.sale_id
    JOIN lot l        ON l.id = t.lot_id
    JOIN warehouse w  ON w.id = s.warehouse_id
   GROUP BY l.lot_no, w.code;
END$$

-- Geri izleme: 2 sonuç kümesi
--   1) kullanılan tüm lotlar + tedarikçi  2) ilgili üretim emirleri / parçalama işlemleri
CREATE PROCEDURE sp_trace_backward(IN p_lot_no VARCHAR(30))
BEGIN
  CALL sp_fill_trace(p_lot_no, 'UP');

  SELECT t.depth, l.lot_no, p.sku, p.name AS product_name, l.origin_type,
         l.lot_date, l.expiry_date, l.initial_qty, l.uom_code, l.unit_cost,
         s.code AS supplier_code, s.name AS supplier_name, l.supplier_lot_no,
         s.contact_person AS supplier_contact, s.phone AS supplier_phone, t.path, l.status
    FROM tmp_trace_lot t
    JOIN lot l ON l.id = t.lot_id
    JOIN product p ON p.id = l.product_id
    LEFT JOIN supplier s ON s.id = l.supplier_id
   ORDER BY t.depth, l.lot_no;

  SELECT DISTINCT ll.link_type, ll.ref_type,
         COALESCE(po.order_no, bo.op_no) AS document_no,
         sub.name AS subcontractor,
         pl.lot_no AS input_lot_no, ll.qty_consumed,
         cl.lot_no AS output_lot_no, ll.qty_produced
    FROM tmp_trace_lot t
    JOIN lot_link ll ON ll.child_lot_id = t.lot_id
    JOIN lot pl ON pl.id = ll.parent_lot_id
    JOIN lot cl ON cl.id = ll.child_lot_id
    LEFT JOIN production_order po  ON ll.ref_type = 'production_order'   AND po.id = ll.ref_id
    LEFT JOIN supplier sub         ON sub.id = po.subcontractor_id
    LEFT JOIN butchery_operation bo ON ll.ref_type = 'butchery_operation' AND bo.id = ll.ref_id
   ORDER BY document_no, input_lot_no;
END$$

-- ---------------------------------------------------------------------
-- Geri çağırma
-- ---------------------------------------------------------------------
-- Kök lot ve ileri izlemede bulunan tüm lotlar RECALLED olur (tek transaction).
-- Bloke lotlarda SALE/SHIPMENT/üretim tüketimi tetikleyici tarafından reddedilir.
CREATE PROCEDURE sp_recall_initiate(
  IN p_recall_no VARCHAR(30), IN p_lot_no VARCHAR(30),
  IN p_reason VARCHAR(500), IN p_user VARCHAR(64))
BEGIN
  DECLARE v_recall INT UNSIGNED; DECLARE v_root INT UNSIGNED;
  DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN ROLLBACK; RESIGNAL; END;

  CALL sp_fill_trace(p_lot_no, 'DOWN');
  SELECT id INTO v_root FROM lot WHERE lot_no = p_lot_no;

  START TRANSACTION;
  SET @app_user = p_user;
  INSERT INTO recall (recall_no, root_lot_id, reason, initiated_by)
  VALUES (p_recall_no, v_root, p_reason, p_user);
  SET v_recall = LAST_INSERT_ID();

  INSERT INTO recall_lot (recall_id, lot_id, depth, previous_status)
  SELECT v_recall, t.lot_id, t.depth, l.status
    FROM tmp_trace_lot t JOIN lot l ON l.id = t.lot_id;

  UPDATE lot l JOIN tmp_trace_lot t ON t.lot_id = l.id
     SET l.status = 'RECALLED',
         l.blocked_reason = CONCAT(p_recall_no, ': ', p_reason);
  COMMIT;
  SET @app_user = NULL;

  SELECT v_recall AS recall_id, COUNT(*) AS blocked_lots FROM recall_lot WHERE recall_id = v_recall;
END$$

-- Müşteri/market bazlı miktar, sevkiyat ve iletişim listesi + depodaki bloke stok
CREATE PROCEDURE sp_recall_report(IN p_recall_no VARCHAR(30))
BEGIN
  DECLARE v_recall INT UNSIGNED;
  SELECT id INTO v_recall FROM recall WHERE recall_no = p_recall_no;

  WITH RECURSIVE dist (shipment_line_id, customer_id, via_customer_id, lot_id, qty, tier) AS (
    SELECT sl.id, s.customer_id, CAST(NULL AS UNSIGNED), sl.lot_id, sl.qty, 1
      FROM recall_lot rl
      JOIN shipment_line sl ON sl.lot_id = rl.lot_id
      JOIN shipment s ON s.id = sl.shipment_id AND s.from_customer_id IS NULL
     WHERE rl.recall_id = v_recall
    UNION ALL
    SELECT sl.id, s.customer_id, s.from_customer_id, sl.lot_id, sl.qty, d.tier + 1
      FROM dist d
      JOIN shipment s ON s.from_customer_id = d.customer_id
      JOIN shipment_line sl ON sl.shipment_id = s.id AND sl.lot_id = d.lot_id
     WHERE d.tier < 5
  ),
  uniq AS (
    SELECT shipment_line_id, MIN(tier) AS tier, MAX(customer_id) AS customer_id,
           MAX(via_customer_id) AS via_customer_id, MAX(lot_id) AS lot_id, MAX(qty) AS qty
      FROM dist GROUP BY shipment_line_id
  )
  SELECT u.tier, c.code AS customer_code, c.name AS customer_name, c.contact_person, c.phone, c.email,
         vc.name AS received_via,
         c.address,
         -- 2. kademe: yalnızca kayıtlı ileri sevk varsa kesin; yoksa "kayıtlı değil"
         EXISTS (SELECT 1 FROM shipment s2 WHERE s2.from_customer_id = c.id) AS onward_recorded,
         GROUP_CONCAT(DISTINCT l.lot_no ORDER BY l.lot_no) AS lots,
         SUM(u.qty) AS qty_shipped,
         COALESCE((SELECT SUM(sm.qty) FROM stock_movement sm
                    JOIN recall_lot rl2 ON rl2.recall_id = v_recall AND rl2.lot_id = sm.lot_id
                   WHERE sm.movement_type = 'RETURN' AND sm.ref_type = 'customer'
                     AND sm.ref_id = c.id), 0) AS qty_returned
    FROM uniq u
    JOIN customer c ON c.id = u.customer_id
    LEFT JOIN customer vc ON vc.id = u.via_customer_id
    JOIN lot l ON l.id = u.lot_id
   GROUP BY u.tier, c.id, c.code, c.name, c.contact_person, c.phone, c.email, vc.name, c.address
   ORDER BY u.tier, c.code;

  SELECT l.lot_no, w.code AS warehouse_code, sb.qty, l.uom_code, l.status
    FROM recall_lot rl
    JOIN stock_balance sb ON sb.lot_id = rl.lot_id AND sb.qty > 0
    JOIN lot l ON l.id = rl.lot_id
    JOIN warehouse w ON w.id = sb.warehouse_id
   WHERE rl.recall_id = v_recall;
END$$

-- ---------------------------------------------------------------------
-- API kısayolları (Go API'nin çağırdığı adlar; önceden API açılışta oluşturuyordu)
-- ---------------------------------------------------------------------
CREATE PROCEDURE sp_calculate_production_cost(IN p_order_no VARCHAR(30))
BEGIN
  SELECT order_no, sku, product_name, status,
         planned_qty, actual_qty, qty_variance, fulfilment_pct,
         input_kg, COALESCE(yield_pct_of_meat, 0) AS yield_pct_of_meat,
         material_cost, service_cost, total_cost,
         ekp, ekp_display, COALESCE(vkp, 0) AS vkp,
         COALESCE(gross_profit_per_unit, 0) AS gross_profit_per_unit,
         COALESCE(revenue_at_vkp, 0) AS revenue_at_vkp,
         COALESCE(gross_profit_total, 0) AS gross_profit_total,
         COALESCE(margin_pct, 0) AS margin_pct
  FROM v_production_cost
  WHERE order_no = p_order_no;
END$$

CREATE PROCEDURE sp_execute_butchery(IN p_op_no VARCHAR(30), IN p_alloc_method VARCHAR(20))
BEGIN
  SELECT op_no, species, allocation_method, status,
         input_weight_kg, output_weight_kg, cut_weight_kg, waste_weight_kg, weight_diff_kg,
         purchase_unit_price, purchase_cost, additional_cost, total_cost,
         declared_total_cost, declared_cost_diff, allocated_total, allocation_diff,
         weight_ok, declared_cost_ok, allocation_ok
  FROM v_butchery_reconciliation
  WHERE op_no = p_op_no OR (p_op_no = '' AND allocation_method = p_alloc_method)
  LIMIT 1;
END$$

CREATE PROCEDURE sp_recall_by_lot(
  IN p_recall_no VARCHAR(30),
  IN p_lot_no VARCHAR(30),
  IN p_reason VARCHAR(500),
  IN p_user VARCHAR(64)
)
BEGIN
  IF NOT EXISTS (SELECT 1 FROM recall WHERE recall_no = p_recall_no) THEN
    CALL sp_recall_initiate(p_recall_no, p_lot_no, p_reason, p_user);
  END IF;
  CALL sp_recall_report(p_recall_no);
END$$

DELIMITER ;
