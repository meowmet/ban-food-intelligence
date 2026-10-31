-- =====================================================================
-- BAN Food Intelligence | database-mysql/seed.sql
-- Bölümler: (1) zorunlu senaryo  (2) demo ilişkileri  (3) KI tahmin örnekleri
-- =====================================================================

-- docker-entrypoint loads each file in its own session: without this the
-- Turkish names would be stored double-encoded ("Ã–rnek" instead of "Örnek").
SET NAMES utf8mb4;

-- =====================================================================
-- Zorunlu senaryo seed'i (şartname Görev 1 + Görev 3)
--   ET-2026-001 (100 kg et) -> URT-2026-045 -> SUC-2026-045 (85 paket)
--   -> Market A 30 / Market B 25 / Market C 30
-- EKP = (970,00 + 48,00 + 58,00 + 467,50) / 85 = 1.543,50 / 85 = 18,16 €
-- Not: tedarikçi/müşteri adları ve iletişim bilgileri anonim örnektir.
-- Baharat ve bağırsak lot numaraları şartnamede verilmediği için örnektir;
-- bu lotlar 2 kg / 5 kg'dan büyük alınmıştır ki "bir hammadde lotu birden
-- çok mamul lotuna dönüşür" ilişkisi demo seed'inde gösterilebilsin.
-- =====================================================================
USE food_intel;

-- Demo takvimi göreli: seed 20.09.2026 - 15.10.2026 arasını anlatır ve yüklendiği gün
-- 16.10.2026 kabul edilir. Her tarih bu kadar kaydırılır; böylece demo hangi gün kurulursa
-- kurulsun olaylar geçmişte, SKT'ler anlamlı kalır (ör. 2 günlük kıyma dün dolmuş olur).
-- Orijinal takvimi görmek için: SET @seed_shift := 0;
SET @seed_shift := DATEDIFF(CURRENT_DATE, '2026-10-16');
-- Üretim prosedürleri ve sevkiyat tetikleyicisi hareket zamanını NOW(3) ile yazar. Seed onları
-- çağırmadan önce oturum saatini belge zamanına çeker (SET TIMESTAMP), sonra DEFAULT'a döndürür;
-- yoksa kabul ve sevkiyatlar yükleme anına düşer, belge tarihli transfer ve satışlardan sonra
-- sıralanır ve stok hareketleri raporunda bakiye eksiye iner.

INSERT INTO unit_of_measure (code, name, is_weight) VALUES
  ('KG', 'Kilogram', TRUE), ('PAKET', 'Paket', FALSE), ('ADET', 'Adet', FALSE);

INSERT INTO warehouse (code, name, warehouse_type) VALUES
  ('ANA-DEPO',  'Ana soğuk depo',            'COLD'),
  ('FASON-01',  'Fason üretici deposu',      'SUBCONTRACTOR'),
  ('MAGAZA-01', 'Kasap mağaza (Kotlin POS)', 'STORE'),
  ('KARANTINA', 'Karantina / bloke stok',    'QUARANTINE');

INSERT INTO supplier (code, name, supplier_type, contact_person, phone, email, address) VALUES
  ('TED-ET-01',  'Örnek Et Tedarik A.Ş.',        'RAW_MATERIAL',  'Kalite Sorumlusu', '+90 212 000 00 01', 'kalite@et-tedarik.example',   'İstanbul'),
  ('TED-BAH-01', 'Örnek Baharat Ltd.',           'RAW_MATERIAL',  'Satış',            '+90 212 000 00 02', 'satis@baharat.example',       'Gaziantep'),
  ('TED-BAG-01', 'Örnek Doğal Bağırsak Ltd.',    'RAW_MATERIAL',  'Satış',            '+90 212 000 00 03', 'satis@bagirsak.example',      'Konya'),
  ('FAS-01',     'Örnek Fason Sucuk Üretim',     'SUBCONTRACTOR', 'Üretim Müdürü',    '+90 212 000 00 04', 'uretim@fason.example',        'Afyon'),
  ('TED-HAY-01', 'Örnek Besi Çiftliği',          'LIVESTOCK',     'Çiftlik Sahibi',   '+90 212 000 00 05', 'ciftlik@besi.example',        'Balıkesir');

-- POS kilit açma yetkilisi (yalnızca demo). PIN yalnızca bcrypt özeti olarak saklanır;
-- demo PIN'i README.md "Demo credentials" bölümündedir.
INSERT INTO supervisor (username, display_name, pin_hash) VALUES
  ('kalite_muduru', 'Kalite Müdürü (demo)', '$2a$12$63SzM09Iqm1GeXxrWgoRJ.NDhh31OV2JaNWWY2fKHi0nzfBmdD3E2');

INSERT INTO customer (code, name, customer_type, contact_person, phone, email, address) VALUES
  ('MKT-A', 'Market A', 'MARKET', 'Şube Müdürü A', '+90 216 000 00 0A', 'market-a@musteri.example', 'Kadıköy'),
  ('MKT-B', 'Market B', 'MARKET', 'Şube Müdürü B', '+90 216 000 00 0B', 'market-b@musteri.example', 'Üsküdar'),
  ('MKT-C', 'Market C', 'MARKET', 'Şube Müdürü C', '+90 216 000 00 0C', 'market-c@musteri.example', 'Ataşehir');

INSERT INTO product (sku, name, product_type, uom_code, pack_size_kg, standard_purchase_price, sale_price, shelf_life_days) VALUES
  ('HM-ET-DANA',   'Dana et (sucukluk)',       'RAW_MATERIAL',  'KG',    NULL,  9.7000, NULL,  10),
  ('HM-BAHARAT',   'Sucuk baharat karışımı',   'RAW_MATERIAL',  'KG',    NULL, 24.0000, NULL, 365),
  ('HM-BAGIRSAK',  'Doğal bağırsak',           'RAW_MATERIAL',  'KG',    NULL, 11.6000, NULL, 180),
  -- VKP şartnamede verilmemiştir; 24,90 € örnek net satış fiyatıdır.
  ('MM-SUCUK-1KG', 'Narin Sucuk 1 kg',         'FINISHED_GOOD', 'PAKET', 1.000, NULL,   24.9000, 90);

-- ---------------------------------------------------------------------
-- Hammadde girişi (tedarikçi, lot, tarih, miktar, SKT)
-- ---------------------------------------------------------------------
INSERT INTO lot (lot_no, product_id, origin_type, supplier_id, supplier_lot_no, lot_date, expiry_date, initial_qty, uom_code, unit_cost, fat_pct)
SELECT 'ET-2026-001', p.id, 'PURCHASE', s.id, 'TED-ET-B7731', DATE_ADD('2026-10-01', INTERVAL @seed_shift DAY), DATE_ADD('2026-10-11', INTERVAL @seed_shift DAY), 100.000, 'KG', 9.7000, 22.00
  FROM product p, supplier s WHERE p.sku = 'HM-ET-DANA' AND s.code = 'TED-ET-01';
INSERT INTO lot (lot_no, product_id, origin_type, supplier_id, supplier_lot_no, lot_date, expiry_date, initial_qty, uom_code, unit_cost)
SELECT 'BAH-2026-001', p.id, 'PURCHASE', s.id, 'BHR-55120', DATE_ADD('2026-09-20', INTERVAL @seed_shift DAY), DATE_ADD('2027-09-20', INTERVAL @seed_shift DAY), 5.000, 'KG', 24.0000
  FROM product p, supplier s WHERE p.sku = 'HM-BAHARAT' AND s.code = 'TED-BAH-01';
INSERT INTO lot (lot_no, product_id, origin_type, supplier_id, supplier_lot_no, lot_date, expiry_date, initial_qty, uom_code, unit_cost)
SELECT 'BAG-2026-001', p.id, 'PURCHASE', s.id, 'BGR-0912', DATE_ADD('2026-09-25', INTERVAL @seed_shift DAY), DATE_ADD('2027-03-25', INTERVAL @seed_shift DAY), 10.000, 'KG', 11.6000
  FROM product p, supplier s WHERE p.sku = 'HM-BAGIRSAK' AND s.code = 'TED-BAG-01';

INSERT INTO purchase_receipt (receipt_no, supplier_id, warehouse_id, receipt_date, invoice_no, delivery_note_no)
SELECT 'ALS-2026-101', s.id, w.id, DATE_ADD('2026-10-01', INTERVAL @seed_shift DAY), 'FTR-ET-88120', 'IRS-ET-88120' FROM supplier s, warehouse w WHERE s.code='TED-ET-01'  AND w.code='ANA-DEPO';
INSERT INTO purchase_receipt (receipt_no, supplier_id, warehouse_id, receipt_date, invoice_no, delivery_note_no)
SELECT 'ALS-2026-097', s.id, w.id, DATE_ADD('2026-09-20', INTERVAL @seed_shift DAY), 'FTR-BH-1201', 'IRS-BH-1201'   FROM supplier s, warehouse w WHERE s.code='TED-BAH-01' AND w.code='ANA-DEPO';
INSERT INTO purchase_receipt (receipt_no, supplier_id, warehouse_id, receipt_date, invoice_no, delivery_note_no)
SELECT 'ALS-2026-099', s.id, w.id, DATE_ADD('2026-09-25', INTERVAL @seed_shift DAY), 'FTR-BG-3310', 'IRS-BG-3310'   FROM supplier s, warehouse w WHERE s.code='TED-BAG-01' AND w.code='ANA-DEPO';

INSERT INTO purchase_receipt_line (receipt_id, product_id, lot_id, qty, unit_price)
SELECT r.id, l.product_id, l.id, l.initial_qty, l.unit_cost
  FROM purchase_receipt r JOIN lot l ON (r.receipt_no, l.lot_no) IN
       (('ALS-2026-101','ET-2026-001'), ('ALS-2026-097','BAH-2026-001'), ('ALS-2026-099','BAG-2026-001'));

INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, ref_id, moved_at, note)
SELECT prl.lot_id, r.warehouse_id, 'PURCHASE_IN', prl.qty, prl.unit_price, 'purchase_receipt', r.id,
       TIMESTAMP(r.receipt_date, '08:00:00'), r.receipt_no
  FROM purchase_receipt_line prl JOIN purchase_receipt r ON r.id = prl.receipt_id;

-- ---------------------------------------------------------------------
-- Görev 1: URT-2026-045 dış üretim emri
-- ---------------------------------------------------------------------
INSERT INTO production_order (order_no, order_type, subcontractor_id, product_id, planned_qty, service_unit_price, drying_days, order_date, notes)
SELECT 'URT-2026-045', 'EXTERNAL', s.id, p.id, 85.000, 5.5000, 5, DATE_ADD('2026-10-02', INTERVAL @seed_shift DAY),
       'Hammadde işletmeye aittir; fason yalnızca işçilik/hizmet faturası keser.'
  FROM supplier s, product p WHERE s.code = 'FAS-01' AND p.sku = 'MM-SUCUK-1KG';

SET TIMESTAMP = UNIX_TIMESTAMP(DATE_ADD('2026-10-02 08:30:00', INTERVAL @seed_shift DAY));
CALL sp_production_add_input('URT-2026-045', 'ET-2026-001', 100.000, 'ANA-DEPO', 'FASON-01');
CALL sp_production_add_input('URT-2026-045', 'BAH-2026-001',   2.000, 'ANA-DEPO', 'FASON-01');
CALL sp_production_add_input('URT-2026-045', 'BAG-2026-001',   5.000, 'ANA-DEPO', 'FASON-01');

-- 85 paket teslim, hizmet faturası 85 × 5,50 = 467,50 €
SET TIMESTAMP = UNIX_TIMESTAMP(DATE_ADD('2026-10-06 14:00:00', INTERVAL @seed_shift DAY));
CALL sp_production_complete('URT-2026-045', 85.000, 'SUC-2026-045', DATE_ADD('2026-10-06', INTERVAL @seed_shift DAY), DATE_ADD('2027-01-04', INTERVAL @seed_shift DAY),
                            'FASON-01', 'ANA-DEPO', 'FAS-FTR-2026-045');
SET TIMESTAMP = DEFAULT;

-- ---------------------------------------------------------------------
-- Görev 3: marketlere sevkiyat 30 / 25 / 30
-- ---------------------------------------------------------------------
INSERT INTO shipment (shipment_no, customer_id, from_warehouse_id, ship_date, delivery_note_no)
SELECT 'SVK-2026-201', c.id, w.id, DATE_ADD('2026-10-07', INTERVAL @seed_shift DAY), 'IRS-SVK-201' FROM customer c, warehouse w WHERE c.code='MKT-A' AND w.code='ANA-DEPO';
INSERT INTO shipment (shipment_no, customer_id, from_warehouse_id, ship_date, delivery_note_no)
SELECT 'SVK-2026-202', c.id, w.id, DATE_ADD('2026-10-07', INTERVAL @seed_shift DAY), 'IRS-SVK-202' FROM customer c, warehouse w WHERE c.code='MKT-B' AND w.code='ANA-DEPO';
INSERT INTO shipment (shipment_no, customer_id, from_warehouse_id, ship_date, delivery_note_no)
SELECT 'SVK-2026-203', c.id, w.id, DATE_ADD('2026-10-08', INTERVAL @seed_shift DAY), 'IRS-SVK-203' FROM customer c, warehouse w WHERE c.code='MKT-C' AND w.code='ANA-DEPO';

-- satır eklenince tetikleyici SHIPMENT stok hareketini yazar (zamanı: sevk günü 09:00)
SET TIMESTAMP = UNIX_TIMESTAMP(DATE_ADD('2026-10-07 09:00:00', INTERVAL @seed_shift DAY));
INSERT INTO shipment_line (shipment_id, lot_id, qty, unit_price)
SELECT s.id, l.id, x.qty, 24.9000
  FROM (SELECT 'SVK-2026-201' AS shipment_no, 30.000 AS qty
        UNION ALL SELECT 'SVK-2026-202', 25.000) x
  JOIN shipment s ON s.shipment_no = x.shipment_no
  JOIN lot l ON l.lot_no = 'SUC-2026-045';
SET TIMESTAMP = UNIX_TIMESTAMP(DATE_ADD('2026-10-08 09:00:00', INTERVAL @seed_shift DAY));
INSERT INTO shipment_line (shipment_id, lot_id, qty, unit_price)
SELECT s.id, l.id, 30.000, 24.9000 FROM shipment s, lot l
 WHERE s.shipment_no = 'SVK-2026-203' AND l.lot_no = 'SUC-2026-045';
SET TIMESTAMP = DEFAULT;

-- =====================================================================
-- Demo seed'i: zorunlu senaryonun ötesindeki ilişkileri göstermek için
--   * Çoktan-çoğa: BAH-2026-001 ve BAG-2026-001 iki farklı sucuk lotunda
--     (SUC-2026-045 ve SUC-2026-046) kullanılır. ET-2026-001 ise yalnızca
--     SUC-2026-045'te -> geri çağırma ET-2026-001'i aşmamalı.
--   * 2. kademe dağıtım: Market C, SUC-2026-045'ten 10 paketi kaydedilmiş
--     bir sevkle Örnek Lokanta'ya iletir (yalnızca kayıtlı olduğu için kesin).
--   * Görev 2: bütün kuzu parçalama. Görseldeki (351,00 € / 22 kg) taslak
--     mutabakat kapısında REDDEDİLİR; düzeltilmiş işlem (287,50 € / 25 kg,
--     3 kg fire kayıtlı) değer ağırlıklı dağıtımla POSTED olur.
--   * Kotlin POS satışı (sale + sale_line -> SALE stok hareketi).
-- =====================================================================
USE food_intel;

INSERT INTO customer (code, name, customer_type, contact_person, phone, email, address) VALUES
  ('TOP-01',  'Örnek Toptancı',  'WHOLESALER', 'Satın Alma', '+90 216 000 00 10', 'satinalma@toptanci.example', 'Bayrampaşa'),
  ('REST-01', 'Örnek Lokanta',   'RESTAURANT', 'İşletmeci',  '+90 216 000 00 11', 'info@lokanta.example',       'Ataşehir');

-- ---------------------------------------------------------------------
-- İkinci sucuk üretimi: URT-2026-046 (ET-2026-002 + paylaşılan baharat/bağırsak lotları)
-- ---------------------------------------------------------------------
INSERT INTO lot (lot_no, product_id, origin_type, supplier_id, supplier_lot_no, lot_date, expiry_date, initial_qty, uom_code, unit_cost, fat_pct)
SELECT 'ET-2026-002', p.id, 'PURCHASE', s.id, 'TED-ET-B7790', DATE_ADD('2026-10-09', INTERVAL @seed_shift DAY), DATE_ADD('2026-10-19', INTERVAL @seed_shift DAY), 100.000, 'KG', 9.8500, 20.50
  FROM product p, supplier s WHERE p.sku = 'HM-ET-DANA' AND s.code = 'TED-ET-01';
INSERT INTO purchase_receipt (receipt_no, supplier_id, warehouse_id, receipt_date, invoice_no, delivery_note_no)
SELECT 'ALS-2026-112', s.id, w.id, DATE_ADD('2026-10-09', INTERVAL @seed_shift DAY), 'FTR-ET-88291', 'IRS-ET-88291' FROM supplier s, warehouse w WHERE s.code='TED-ET-01' AND w.code='ANA-DEPO';
INSERT INTO purchase_receipt_line (receipt_id, product_id, lot_id, qty, unit_price)
SELECT r.id, l.product_id, l.id, l.initial_qty, l.unit_cost FROM purchase_receipt r, lot l
 WHERE r.receipt_no = 'ALS-2026-112' AND l.lot_no = 'ET-2026-002';
INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, ref_id, moved_at, note)
SELECT prl.lot_id, r.warehouse_id, 'PURCHASE_IN', prl.qty, prl.unit_price, 'purchase_receipt', r.id, TIMESTAMP(r.receipt_date, '08:00:00'), r.receipt_no
  FROM purchase_receipt_line prl JOIN purchase_receipt r ON r.id = prl.receipt_id WHERE r.receipt_no = 'ALS-2026-112';

INSERT INTO production_order (order_no, order_type, subcontractor_id, product_id, planned_qty, service_unit_price, drying_days, order_date)
SELECT 'URT-2026-046', 'EXTERNAL', s.id, p.id, 85.000, 5.5000, 6, DATE_ADD('2026-10-10', INTERVAL @seed_shift DAY)
  FROM supplier s, product p WHERE s.code = 'FAS-01' AND p.sku = 'MM-SUCUK-1KG';
SET TIMESTAMP = UNIX_TIMESTAMP(DATE_ADD('2026-10-10 08:30:00', INTERVAL @seed_shift DAY));
CALL sp_production_add_input('URT-2026-046', 'ET-2026-002', 100.000, 'ANA-DEPO', 'FASON-01');
CALL sp_production_add_input('URT-2026-046', 'BAH-2026-001',   2.000, 'ANA-DEPO', 'FASON-01');
CALL sp_production_add_input('URT-2026-046', 'BAG-2026-001',   5.000, 'ANA-DEPO', 'FASON-01');
-- bu partide verim düşük: 83 paket (miktar farkı -2; daha yağsız et + 6 gün kurutma)
SET TIMESTAMP = UNIX_TIMESTAMP(DATE_ADD('2026-10-14 14:00:00', INTERVAL @seed_shift DAY));
CALL sp_production_complete('URT-2026-046', 83.000, 'SUC-2026-046', DATE_ADD('2026-10-14', INTERVAL @seed_shift DAY), DATE_ADD('2027-01-12', INTERVAL @seed_shift DAY),
                            'FASON-01', 'ANA-DEPO', 'FAS-FTR-2026-046');

INSERT INTO shipment (shipment_no, customer_id, from_warehouse_id, ship_date, delivery_note_no)
SELECT 'SVK-2026-214', c.id, w.id, DATE_ADD('2026-10-15', INTERVAL @seed_shift DAY), 'IRS-SVK-214' FROM customer c, warehouse w WHERE c.code='MKT-B'  AND w.code='ANA-DEPO';
INSERT INTO shipment (shipment_no, customer_id, from_warehouse_id, ship_date, delivery_note_no)
SELECT 'SVK-2026-215', c.id, w.id, DATE_ADD('2026-10-15', INTERVAL @seed_shift DAY), 'IRS-SVK-215' FROM customer c, warehouse w WHERE c.code='TOP-01' AND w.code='ANA-DEPO';
SET TIMESTAMP = UNIX_TIMESTAMP(DATE_ADD('2026-10-15 09:00:00', INTERVAL @seed_shift DAY));
INSERT INTO shipment_line (shipment_id, lot_id, qty, unit_price)
SELECT s.id, l.id, x.qty, 24.9000
  FROM (SELECT 'SVK-2026-214' AS shipment_no, 40.000 AS qty UNION ALL SELECT 'SVK-2026-215', 30.000) x
  JOIN shipment s ON s.shipment_no = x.shipment_no JOIN lot l ON l.lot_no = 'SUC-2026-046';
SET TIMESTAMP = DEFAULT;

-- 5 paket mağazaya transfer (POS)
INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, moved_at, note)
SELECT l.id, w.id, IF(w.code='ANA-DEPO','TRANSFER_OUT','TRANSFER_IN'), IF(w.code='ANA-DEPO',-5,5), l.unit_cost, 'transfer', DATE_ADD('2026-10-15 10:00:00', INTERVAL @seed_shift DAY), 'Mağazaya transfer'
  FROM lot l, warehouse w WHERE l.lot_no = 'SUC-2026-046' AND w.code IN ('ANA-DEPO','MAGAZA-01')
 ORDER BY w.code = 'ANA-DEPO' DESC;

-- ---------------------------------------------------------------------
-- 2. kademe: Market C -> Örnek Lokanta, 10 paket SUC-2026-045 (kayıtlı ileri sevk)
-- ---------------------------------------------------------------------
INSERT INTO shipment (shipment_no, customer_id, from_customer_id, ship_date, delivery_note_no)
SELECT 'MKC-IRS-7781', r.id, c.id, DATE_ADD('2026-10-09', INTERVAL @seed_shift DAY), 'MKC-IRS-7781' FROM customer r, customer c WHERE r.code='REST-01' AND c.code='MKT-C';
INSERT INTO shipment_line (shipment_id, lot_id, qty)
SELECT s.id, l.id, 10.000 FROM shipment s, lot l WHERE s.shipment_no='MKC-IRS-7781' AND l.lot_no='SUC-2026-045';

-- ---------------------------------------------------------------------
-- Görev 2: bütün kuzu parçalama
-- ---------------------------------------------------------------------
INSERT INTO product (sku, name, product_type, uom_code, species, standard_purchase_price, sale_price, shelf_life_days) VALUES
  ('KZ-KARKAS',  'Bütün kuzu karkas', 'CARCASS', 'KG', 'KUZU', 11.5000, NULL,    7),
  ('KZ-BUT',     'Kuzu but',          'CUT',     'KG', 'KUZU', NULL,    22.0000, 5),
  ('KZ-SIRT',    'Kuzu sırt/pirzola', 'CUT',     'KG', 'KUZU', NULL,    26.0000, 5),
  ('KZ-OMUZ',    'Kuzu omuz',         'CUT',     'KG', 'KUZU', NULL,    18.0000, 5),
  ('KZ-KABURGA', 'Kuzu kaburga',      'CUT',     'KG', 'KUZU', NULL,    14.0000, 5),
  ('KZ-KIYMA',   'Kuzu kıyma',        'CUT',     'KG', 'KUZU', NULL,    16.0000, 2),
  ('KZ-FIRE',    'Kemik / fire',      'WASTE',   'KG', 'KUZU', NULL,    NULL,    NULL),
  ('KZ-SAKATAT', 'Kuzu sakatat (ciğer, böbrek)', 'BY_PRODUCT', 'KG', 'KUZU', NULL, 8.0000, 2);

INSERT INTO butchery_recipe (code, name, species, default_allocation) VALUES ('REC-KUZU-STD', 'Standart kuzu parçalama', 'KUZU', 'VALUE');
INSERT INTO butchery_recipe_line (recipe_id, product_id, output_kind, expected_yield_pct, value_price_per_kg)
SELECT r.id, p.id, x.kind, x.pct, p.sale_price
  FROM butchery_recipe r
  JOIN (SELECT 'KZ-BUT' sku, 'CUT' kind, 26.0 pct UNION ALL SELECT 'KZ-SIRT','CUT',14.0
        UNION ALL SELECT 'KZ-OMUZ','CUT',20.0 UNION ALL SELECT 'KZ-KABURGA','CUT',12.0
        UNION ALL SELECT 'KZ-KIYMA','CUT',16.0 UNION ALL SELECT 'KZ-FIRE','WASTE',12.0) x
  JOIN product p ON p.sku = x.sku
 WHERE r.code = 'REC-KUZU-STD';

INSERT INTO lot (lot_no, product_id, origin_type, supplier_id, supplier_lot_no, lot_date, expiry_date, initial_qty, uom_code, unit_cost,
                 animal_breed, animal_age_months, animal_sex, fat_score, conformation_score)
SELECT 'KUZU-2026-001', p.id, 'PURCHASE', s.id, 'KUPE-TR-10-445812', DATE_ADD('2026-10-12', INTERVAL @seed_shift DAY), DATE_ADD('2026-10-19', INTERVAL @seed_shift DAY), 25.000, 'KG', 11.5000,
       'KIVIRCIK', 5.0, 'E', 3, 3
  FROM product p, supplier s WHERE p.sku='KZ-KARKAS' AND s.code='TED-HAY-01';
INSERT INTO purchase_receipt (receipt_no, supplier_id, warehouse_id, receipt_date, invoice_no)
SELECT 'ALS-2026-120', s.id, w.id, DATE_ADD('2026-10-12', INTERVAL @seed_shift DAY), 'FTR-HAY-0042' FROM supplier s, warehouse w WHERE s.code='TED-HAY-01' AND w.code='ANA-DEPO';
INSERT INTO purchase_receipt_line (receipt_id, product_id, lot_id, qty, unit_price)
SELECT r.id, l.product_id, l.id, 25.000, 11.5000 FROM purchase_receipt r, lot l WHERE r.receipt_no='ALS-2026-120' AND l.lot_no='KUZU-2026-001';
INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, ref_id, moved_at, note)
SELECT l.id, r.warehouse_id, 'PURCHASE_IN', 25.000, 11.5000, 'purchase_receipt', r.id, DATE_ADD('2026-10-12 07:30:00', INTERVAL @seed_shift DAY), r.receipt_no
  FROM lot l, purchase_receipt r WHERE l.lot_no='KUZU-2026-001' AND r.receipt_no='ALS-2026-120';

-- (a) Örnek görseldeki taslak: 351,00 € beyan, parçalar 22 kg, fire kaydı yok -> REDDEDİLDİ
INSERT INTO butchery_operation (op_no, recipe_id, input_lot_id, species, input_weight_kg, purchase_unit_price,
                                declared_total_cost, allocation_method, op_date, operator_name, status, reconciliation_note)
SELECT 'PRC-2026-000', r.id, l.id, 'KUZU', 25.000, 11.5000, 351.00, 'VALUE', DATE_ADD('2026-10-13', INTERVAL @seed_shift DAY), 'OP-1', 'REJECTED',
       '25 kg x 11,50 EUR = 287,50 EUR; beyan 351,00 EUR (fark 63,50 EUR belgesiz). Parçalar 22 kg, 3 kg kayıp kaydı yok.'
  FROM butchery_recipe r, lot l WHERE r.code='REC-KUZU-STD' AND l.lot_no='KUZU-2026-001';
INSERT INTO butchery_output (operation_id, product_id, output_kind, weight_kg)
SELECT o.id, p.id, 'CUT', x.kg FROM butchery_operation o
  JOIN (SELECT 'KZ-BUT' sku, 6.5 kg UNION ALL SELECT 'KZ-SIRT',3.5 UNION ALL SELECT 'KZ-OMUZ',5.0
        UNION ALL SELECT 'KZ-KABURGA',3.0 UNION ALL SELECT 'KZ-KIYMA',4.0) x
  JOIN product p ON p.sku = x.sku
 WHERE o.op_no = 'PRC-2026-000';

-- (b) Düzeltilmiş işlem: 25 kg = 22 kg parça + 3 kg kemik/fire; maliyet 287,50 € değer ağırlıklı dağıtıldı.
--     Dağıtım: pay_i = 287,50 × (kg_i × VKP_i) / Σ(kg × VKP), Σ = 430,00; kuruş artığı en büyük
--     kesirli kalana (omuz) verildi (largest remainder) -> toplam tam 287,50 €.
INSERT INTO butchery_operation (op_no, recipe_id, input_lot_id, species, input_weight_kg, purchase_unit_price,
                                allocation_method, op_date, operator_name, chill_hours, status, reconciliation_note)
SELECT 'PRC-2026-001', r.id, l.id, 'KUZU', 25.000, 11.5000, 'VALUE', DATE_ADD('2026-10-13', INTERVAL @seed_shift DAY), 'OP-1', 24.0, 'POSTED',
       'PRC-2026-000 düzeltmesi: fire 3 kg tartılıp kaydedildi; ek maliyet belgesi olmadığı için maliyet 287,50 EUR.'
  FROM butchery_recipe r, lot l WHERE r.code='REC-KUZU-STD' AND l.lot_no='KUZU-2026-001';

INSERT INTO lot (lot_no, product_id, origin_type, lot_date, expiry_date, initial_qty, uom_code, unit_cost)
SELECT x.lot_no, p.id, 'BUTCHERY', DATE_ADD('2026-10-13', INTERVAL @seed_shift DAY), DATE_ADD(DATE_ADD('2026-10-13', INTERVAL @seed_shift DAY), INTERVAL p.shelf_life_days DAY), x.kg, 'KG', ROUND(x.cost / x.kg, 4)
  FROM (SELECT 'KZB-2026-001' lot_no, 'KZ-BUT' sku, 6.5 kg, 95.61 cost
        UNION ALL SELECT 'KZS-2026-001','KZ-SIRT',    3.5, 60.84
        UNION ALL SELECT 'KZO-2026-001','KZ-OMUZ',    5.0, 60.18
        UNION ALL SELECT 'KZK-2026-001','KZ-KABURGA', 3.0, 28.08
        UNION ALL SELECT 'KZY-2026-001','KZ-KIYMA',   4.0, 42.79) x
  JOIN product p ON p.sku = x.sku;

INSERT INTO butchery_output (operation_id, product_id, lot_id, output_kind, weight_kg, allocation_basis, allocated_cost)
SELECT o.id, l.product_id, l.id, 'CUT', l.initial_qty, l.initial_qty * p.sale_price, ROUND(l.initial_qty * l.unit_cost, 2)
  FROM butchery_operation o, lot l JOIN product p ON p.id = l.product_id
 WHERE o.op_no = 'PRC-2026-001' AND l.lot_no IN ('KZB-2026-001','KZS-2026-001','KZO-2026-001','KZK-2026-001','KZY-2026-001');
INSERT INTO butchery_output (operation_id, product_id, output_kind, weight_kg, allocation_basis, allocated_cost)
SELECT o.id, p.id, 'WASTE', 3.000, 0, 0 FROM butchery_operation o, product p WHERE o.op_no='PRC-2026-001' AND p.sku='KZ-FIRE';

INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, ref_id, moved_at, note)
SELECT l.id, w.id, 'BUTCHERY_CONSUME', -25.000, 11.5000, 'butchery_operation', o.id, DATE_ADD('2026-10-13 09:00:00', INTERVAL @seed_shift DAY), o.op_no
  FROM lot l, warehouse w, butchery_operation o WHERE l.lot_no='KUZU-2026-001' AND w.code='ANA-DEPO' AND o.op_no='PRC-2026-001';
INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, ref_id, moved_at, note)
SELECT bo.lot_id, w.id, 'BUTCHERY_OUTPUT', bo.weight_kg, bo.unit_cost, 'butchery_operation', o.id, DATE_ADD('2026-10-13 09:45:00', INTERVAL @seed_shift DAY), o.op_no
  FROM butchery_output bo JOIN butchery_operation o ON o.id = bo.operation_id, warehouse w
 WHERE o.op_no='PRC-2026-001' AND bo.lot_id IS NOT NULL AND w.code='ANA-DEPO';
INSERT INTO lot_link (parent_lot_id, child_lot_id, link_type, qty_consumed, qty_produced, ref_type, ref_id)
SELECT o.input_lot_id, bo.lot_id, 'BUTCHERY', bo.weight_kg, bo.weight_kg, 'butchery_operation', o.id
  FROM butchery_output bo JOIN butchery_operation o ON o.id = bo.operation_id
 WHERE o.op_no='PRC-2026-001' AND bo.lot_id IS NOT NULL;

-- Parçaları mağazaya transfer + Kotlin POS satışı
INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, moved_at, note)
SELECT l.id, w.id, IF(w.code='ANA-DEPO','TRANSFER_OUT','TRANSFER_IN'), IF(w.code='ANA-DEPO',-l.initial_qty,l.initial_qty),
       l.unit_cost, 'transfer', DATE_ADD('2026-10-13 11:00:00', INTERVAL @seed_shift DAY), 'Mağazaya transfer'
  FROM lot l, warehouse w WHERE l.lot_no IN ('KZB-2026-001','KZY-2026-001') AND w.code IN ('ANA-DEPO','MAGAZA-01')
 ORDER BY l.id, w.code = 'ANA-DEPO' DESC;

INSERT INTO sale (sale_uuid, device_id, warehouse_id, sold_at)
SELECT '019a2b3c-4d5e-7f60-8a1b-2c3d4e5f6a70', 'POS-ANDROID-01', w.id, DATE_ADD('2026-10-14 17:05:12', INTERVAL @seed_shift DAY) FROM warehouse w WHERE w.code='MAGAZA-01';
INSERT INTO sale_line (sale_id, line_no, lot_id, qty, unit_price)
SELECT s.id, x.line_no, l.id, x.qty, p.sale_price
  FROM sale s
  JOIN (SELECT 1 line_no, 'KZB-2026-001' lot_no, 1.250 qty UNION ALL SELECT 2, 'KZY-2026-001', 0.500) x
  JOIN lot l ON l.lot_no = x.lot_no JOIN product p ON p.id = l.product_id
 WHERE s.sale_uuid = '019a2b3c-4d5e-7f60-8a1b-2c3d4e5f6a70';
INSERT INTO sale (sale_uuid, device_id, warehouse_id, sold_at)
SELECT '019a2b3c-9e10-7a20-9b3c-4d5e6f708192', 'POS-ANDROID-01', w.id, DATE_ADD('2026-10-15 12:40:03', INTERVAL @seed_shift DAY) FROM warehouse w WHERE w.code='MAGAZA-01';
INSERT INTO sale_line (sale_id, line_no, lot_id, qty, unit_price)
SELECT s.id, 1, l.id, 2.000, 24.9000 FROM sale s, lot l
 WHERE s.sale_uuid = '019a2b3c-9e10-7a20-9b3c-4d5e6f708192' AND l.lot_no = 'SUC-2026-046';

-- Otomatik üretildi: python ml-engine/predict_ali.py ... --emit-sql (model tahmini + gerçek + sapma)
USE food_intel;
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value, interval_low, interval_high, interval_level, is_anomaly) VALUES ('PRODUCTION_YIELD', 'ali_ridge', 'ali-1.1.0', 'production_order', (SELECT id FROM production_order WHERE order_no = 'URT-2026-045'), 'actual_qty', 84.6, 85.0, 83.2, 86.0, 0.8, 0);
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value, interval_low, interval_high, interval_level, is_anomaly) VALUES ('PRODUCTION_EKP', 'ali_ridge', 'ali-1.1.0', 'production_order', (SELECT id FROM production_order WHERE order_no = 'URT-2026-045'), 'ekp', 18.2172, 18.1588, 18.0116, 18.4327, 0.8, NULL);
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value, interval_low, interval_high, interval_level, is_anomaly) VALUES ('PRODUCTION_YIELD', 'ali_ridge', 'ali-1.1.0', 'production_order', (SELECT id FROM production_order WHERE order_no = 'URT-2026-046'), 'actual_qty', 82.8, 83.0, 81.4, 84.3, 0.8, 0);
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value, interval_low, interval_high, interval_level, is_anomaly) VALUES ('PRODUCTION_EKP', 'ali_ridge', 'ali-1.1.0', 'production_order', (SELECT id FROM production_order WHERE order_no = 'URT-2026-046'), 'ekp', 18.6692, 18.6446, 18.4419, 18.9029, 0.8, NULL);
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value, interval_low, interval_high, interval_level, is_anomaly) VALUES ('BUTCHERY_YIELD', 'ali_ridge', 'ali-1.1.0', 'butchery_operation', (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001'), 'but_kg', 6.794, 6.5, 6.565, 7.023, 0.8, 0);
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value, interval_low, interval_high, interval_level, is_anomaly) VALUES ('BUTCHERY_YIELD', 'ali_ridge', 'ali-1.1.0', 'butchery_operation', (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001'), 'sirt_kg', 3.632, 3.5, 3.445, 3.819, 0.8, 0);
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value, interval_low, interval_high, interval_level, is_anomaly) VALUES ('BUTCHERY_YIELD', 'ali_ridge', 'ali-1.1.0', 'butchery_operation', (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001'), 'omuz_kg', 5.001, 5.0, 4.755, 5.248, 0.8, 0);
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value, interval_low, interval_high, interval_level, is_anomaly) VALUES ('BUTCHERY_YIELD', 'ali_ridge', 'ali-1.1.0', 'butchery_operation', (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001'), 'kaburga_kg', 2.957, 3.0, 2.77, 3.145, 0.8, 0);
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value, interval_low, interval_high, interval_level, is_anomaly) VALUES ('BUTCHERY_YIELD', 'ali_ridge', 'ali-1.1.0', 'butchery_operation', (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001'), 'kiyma_kg', 4.008, 4.0, 3.763, 4.253, 0.8, 0);
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value, interval_low, interval_high, interval_level, is_anomaly) VALUES ('BUTCHERY_YIELD', 'ali_ridge', 'ali-1.1.0', 'butchery_operation', (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001'), 'fire_kg', 2.607, 3.0, 2.387, 2.827, 0.8, 0);

-- BEGIN ai-report (generated by ml-engine/export_ai_report.py; do not edit by hand)
-- Synthetic training data (generator_ali.py); test = latest 20% of dates (time split).
USE food_intel;
INSERT INTO ml_model_metric (task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct) VALUES ('PRODUCTION', 'actual_qty', 'paket', 'ali_ridge', 'MODEL', 'TEST_TIME_SPLIT', 302, 1.3688, 1.1825);
INSERT INTO ml_model_metric (task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct) VALUES ('PRODUCTION', 'ekp', 'EUR', 'ali_ridge', 'MODEL', 'TEST_TIME_SPLIT', 302, 0.1376, 0.8145);
INSERT INTO ml_model_metric (task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct) VALUES ('PRODUCTION', 'actual_qty', 'paket', 'baseline_hist_mean', 'BASELINE', 'TEST_TIME_SPLIT', 302, 2.6888, 2.2438);
INSERT INTO ml_model_metric (task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct) VALUES ('PRODUCTION', 'ekp', 'EUR', 'baseline_hist_mean', 'BASELINE', 'TEST_TIME_SPLIT', 302, 0.2589, 1.5416);
INSERT INTO ml_model_metric (task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct) VALUES ('PRODUCTION', 'actual_qty', 'paket', 'baseline_recipe', 'BASELINE', 'TEST_TIME_SPLIT', 302, 3.2632, 2.8463);
INSERT INTO ml_model_metric (task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct) VALUES ('PRODUCTION', 'ekp', 'EUR', 'baseline_recipe', 'BASELINE', 'TEST_TIME_SPLIT', 302, 0.3200, 1.8972);
INSERT INTO ml_model_metric (task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct) VALUES ('BUTCHERY', 'part_kg', 'kg', 'ali_ridge', 'MODEL', 'TEST_TIME_SPLIT', 500, 0.1154, 3.2671);
INSERT INTO ml_model_metric (task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct) VALUES ('BUTCHERY', 'fire_kg', 'kg', 'ali_ridge', 'MODEL', 'TEST_TIME_SPLIT', 500, 0.1131, 4.3980);
INSERT INTO ml_model_metric (task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct) VALUES ('BUTCHERY', 'part_kg', 'kg', 'baseline_hist_mean', 'BASELINE', 'TEST_TIME_SPLIT', 500, 0.1704, 4.7904);
INSERT INTO ml_model_metric (task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct) VALUES ('BUTCHERY', 'fire_kg', 'kg', 'baseline_hist_mean', 'BASELINE', 'TEST_TIME_SPLIT', 500, 0.2508, 9.8065);
INSERT INTO ml_model_metric (task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct) VALUES ('BUTCHERY', 'part_kg', 'kg', 'baseline_recipe', 'BASELINE', 'TEST_TIME_SPLIT', 500, 0.1706, 4.8054);
INSERT INTO ml_model_metric (task, target_name, unit, model_name, role, eval_split, n_rows, mae, mape_pct) VALUES ('BUTCHERY', 'fire_kg', 'kg', 'baseline_recipe', 'BASELINE', 'TEST_TIME_SPLIT', 500, 0.2561, 10.0265);
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value) VALUES ('PRODUCTION_YIELD', 'baseline_hist_mean', 'baseline-1', 'production_order', (SELECT id FROM production_order WHERE order_no = 'URT-2026-045'), 'actual_qty', 83.3492, (SELECT actual_qty FROM production_order WHERE order_no = 'URT-2026-045'));
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value) VALUES ('PRODUCTION_EKP', 'baseline_hist_mean', 'baseline-1', 'production_order', (SELECT id FROM production_order WHERE order_no = 'URT-2026-045'), 'ekp', 18.4095, (SELECT unit_cost FROM production_output pout JOIN production_order po ON po.id = pout.production_order_id WHERE po.order_no = 'URT-2026-045' LIMIT 1));
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value) VALUES ('PRODUCTION_YIELD', 'baseline_hist_mean', 'baseline-1', 'production_order', (SELECT id FROM production_order WHERE order_no = 'URT-2026-046'), 'actual_qty', 83.3492, (SELECT actual_qty FROM production_order WHERE order_no = 'URT-2026-046'));
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value) VALUES ('PRODUCTION_EKP', 'baseline_hist_mean', 'baseline-1', 'production_order', (SELECT id FROM production_order WHERE order_no = 'URT-2026-046'), 'ekp', 18.5895, (SELECT unit_cost FROM production_output pout JOIN production_order po ON po.id = pout.production_order_id WHERE po.order_no = 'URT-2026-046' LIMIT 1));
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value) VALUES ('BUTCHERY_YIELD', 'baseline_hist_mean', 'baseline-1', 'butchery_operation', (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001'), 'but_kg', 6.5590, (SELECT SUM(o.weight_kg) FROM butchery_output o JOIN product p ON p.id = o.product_id WHERE o.operation_id = (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001') AND p.sku = 'KZ-BUT'));
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value) VALUES ('BUTCHERY_YIELD', 'baseline_hist_mean', 'baseline-1', 'butchery_operation', (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001'), 'sirt_kg', 3.5305, (SELECT SUM(o.weight_kg) FROM butchery_output o JOIN product p ON p.id = o.product_id WHERE o.operation_id = (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001') AND p.sku = 'KZ-SIRT'));
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value) VALUES ('BUTCHERY_YIELD', 'baseline_hist_mean', 'baseline-1', 'butchery_operation', (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001'), 'omuz_kg', 5.0158, (SELECT SUM(o.weight_kg) FROM butchery_output o JOIN product p ON p.id = o.product_id WHERE o.operation_id = (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001') AND p.sku = 'KZ-OMUZ'));
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value) VALUES ('BUTCHERY_YIELD', 'baseline_hist_mean', 'baseline-1', 'butchery_operation', (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001'), 'kaburga_kg', 3.0029, (SELECT SUM(o.weight_kg) FROM butchery_output o JOIN product p ON p.id = o.product_id WHERE o.operation_id = (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001') AND p.sku = 'KZ-KABURGA'));
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value) VALUES ('BUTCHERY_YIELD', 'baseline_hist_mean', 'baseline-1', 'butchery_operation', (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001'), 'kiyma_kg', 3.9682, (SELECT SUM(o.weight_kg) FROM butchery_output o JOIN product p ON p.id = o.product_id WHERE o.operation_id = (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001') AND p.sku = 'KZ-KIYMA'));
INSERT INTO ml_prediction (task, model_name, model_version, ref_type, ref_id, target_name, predicted_value, actual_value) VALUES ('BUTCHERY_YIELD', 'baseline_hist_mean', 'baseline-1', 'butchery_operation', (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001'), 'fire_kg', 2.9236, (SELECT SUM(o.weight_kg) FROM butchery_output o JOIN product p ON p.id = o.product_id WHERE o.operation_id = (SELECT id FROM butchery_operation WHERE op_no = 'PRC-2026-001') AND p.sku = 'KZ-FIRE'));
-- END ai-report

-- Buraya kadarki tahminlerin hepsi seed örneğidir; uygulamanın yazdıkları DEMO ya da FIELD olur.
UPDATE ml_prediction SET data_origin = 'SEED';
