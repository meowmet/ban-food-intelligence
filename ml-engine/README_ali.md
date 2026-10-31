# Ali — KI: üretim verimi/EKP (4A) ve parçalama verimi (4B)

```bash
pip install -r ml-engine/requirements.txt
python ml-engine/generator_ali.py         # data/train_task4a_ali.csv (1500), data/train_task4b_ali.csv (2500)
python ml-engine/train_model_ali.py       # artifacts/ali/metrics.json, oof_ali_*.csv, *_model.joblib (~1 dk)
python ml-engine/anomaly_ali.py           # artifacts/ali/anomaly_metrics.json, anomaly_detector.joblib (~10 sn)
python ml-engine/calibrate_live_alerts.py # canlı API uyarı eşiklerinin sentetik stres testi -> artifacts/ali/ + api-go aimodel (~20 sn)
python ml-engine/predict_ali.py production ml-engine/data/seed_cases_production.csv
python ml-engine/predict_ali.py butchery   ml-engine/data/seed_cases_butchery.csv --emit-sql /tmp/pred.sql
python ml-engine/predict_ali.py production ml-engine/data/demo_cases_anomaly_production.csv --alerts --demo
pytest ml-engine/tests -q
```

## Veri (sentetik — sınırlılık açıkça)

Yarışmada gerçek veri verilmediği için Ocak 2024 – Eylül 2026 arası geçmiş, şartname rakamlarına
sabitlenmiş bir süreç modeliyle üretildi:

* **4A**: standart koşulda 100 kg et + 2 kg baharat + 5 kg bağırsak → 85 × 1 kg (%85 verim); fiyatlar
  Ekim 2026'da 9,70 / 24,00 / 11,60 / 5,50 €, geçmişe doğru yıllık ~%6 enflasyon. Verimi etkileyenler:
  et yağ oranı, kurutma süresi, etin bekleme günü, fason firma, tedarikçi, yaz ayları, fason×yaz
  etkileşimi, parti büyüklüğüne bağlı sabit dolum kaybı; %2 arıza (anomali) satırı, %4 eksik lab sonucu.
* **4B**: tür (kuzu/oğlak/koyun) bazında reçete payları (kuzu %26/14/20/12/16/12 → 25 kg'da 22 kg parça + 3 kg
  fire, şartname görseliyle aynı); ırk, yağ ve karkas skoru, ağırlık, kasap becerisi, soğutma süresi etkileri.
  Fire = ağırlık − parçalar, yani kütle dengesi her satırda tam.

Sonuçlar bu sentetik sürecin ne kadar öğrenilebildiğini ölçer; gerçek işletme performansını kanıtlamaz.
Gerçek kayıt biriktikçe aynı kolonlar `v_ml_production_features` / `v_ml_butchery_features`
görünümlerinden (database-mysql/schema.sql) gelir ve model değiştirilmeden yeniden eğitilir.

## Yöntem

* **Hedef seçimi**: 4A'da EKP doğrudan öğrenilmez. Verim (`yield_pct`) tahmin edilir; paket sayısı ve EKP
  şartname formülüyle hesaplanır, böylece maliyet tarafı hep kesin muhasebeyle tutarlıdır.
  4B'de 6 pay ayrı tahmin edilip toplam 100'e normalize edilir → tahmin edilen parçalar + fire = giriş ağırlığı.
* **Modeller**: CatBoost (takım planında Ali'nin modeli; kategorikleri doğal işler, 4B'de 6 payı tek
  MultiRMSE modelinde birlikte öğrenir) ve Ridge (one-hot + ölçekleme). Şampiyon **test'e bakılmadan**
  5 katlı CV hatasıyla seçilir: Ridge. Stacking (Nuri'nin meta-learner'ı) denetlendi, tek modele göre
  kazanç getirmedi; sunulmuyor ([docs/AI.md](../docs/AI.md#why-not-stacking)).
* **Zaman bazlı testin yakaladığı hata**: Fiyat kolonları verimi fiziksel olarak etkilemez ama enflasyonla
  zamana bağlı artar. İlk denemede CatBoost bunları "zaman vekili" olarak kullandı. Rastgele CV bunu
  göstermedi (0,96), zaman bazlı test gösterdi (test verim MAE 1,45). Fiyatlar verim modelinden çıkarılınca
  hata 0,99'a düştü. Fiyatlar yalnız EKP formülünde kullanılır.
* **Referanslar**: (1) reçete standardı (%85; kuzu reçete payları), (2) geçmiş ortalaması (ürün/tür bazında).
* **Değerlendirme**: zaman bazlı holdout (son %20, 302 emir / 500 karkas); train içinde takımla ortak
  deterministik fold'lar (bkz. [DATA_CONTRACT](DATA_CONTRACT.md)). Ek olarak 5 katlı rolling-origin,
  bootstrap güven aralıkları ve Diebold-Mariano testi: [docs/AI.md](../docs/AI.md), [ML_REPORT.md](ML_REPORT.md).
* **Belirsizlik**: zaman sıralı split-conformal; gösterilen aralık %80. Model kalibrasyon satırlarını
  görmez, genişlik gelecekteki hatayı yansıtır. `anomaly` = gerçek değer %98 bandının dışında (%80 aralığın
  dışı yalnız bilgi amaçlı `outside_interval`; tanım gereği normal kayıtların ~%20'si oraya düşer).
  Uyarı üretmek için ayrıca kalibre edilmiş dedektör kullanılır (aşağıda).

## Sonuçlar (zaman bazlı test kümesi)

**4A — üretim** (verim → paket → EKP)

| Model | Paket MAE | Paket MAPE | EKP MAE | Verim R² |
|---|---|---|---|---|
| Reçete standardı (%85) | 3,26 | %2,85 | 0,320 € | −0,57 |
| Geçmiş ortalaması | 2,69 | %2,24 | 0,259 € | 0,00 |
| **Ridge (şampiyon)** | **1,37** | **%1,18** | **0,138 €** | **0,67** |
| CatBoost | 1,40 | %1,21 | 0,139 € | 0,66 |

Geçmiş ortalamasına göre paket hatasında **%49,1** iyileşme. %80 aralık ±1,34 verim puanı, rolling-origin
kapsama %79,3.

**4B — parçalama** (6 çıktı, kg)

| Model | Parça kg MAE | Pay MAE (puan) | Satılabilir toplam kg MAE |
|---|---|---|---|
| Reçete standardı | 0,185 | 0,86 | 0,256 |
| Geçmiş ortalaması | 0,184 | 0,86 | 0,251 |
| **Ridge (şampiyon)** | **0,115** | **0,54** | **0,113** |
| CatBoost (MultiRMSE) | 0,117 | 0,55 | 0,116 |

Geçmiş ortalamasına göre **%37,5** iyileşme; tahminlerde kütle dengesi hatası < 1e-12 kg; %80 aralığın
parça başına rolling-origin kapsaması %80,4.

Dürüst not: Ridge, CatBoost'u az farkla geçiyor; çünkü sentetik süreç büyük ölçüde doğrusal. Bu yüzden daha
basit olan Ridge sunuluyor.

## Örnek tahminler (seed kayıtları, `v_ml_prediction_deviation`)

| Kayıt | Tahmin | %80 aralık | Gerçek | Sapma |
|---|---|---|---|---|
| URT-2026-045 paket | 84,6 | 83,2 – 86,0 | 85 | +0,4 |
| URT-2026-045 EKP | 18,22 € | 18,01 – 18,43 € | 18,16 € | −0,06 € |
| URT-2026-046 paket (yağsız et, 6 gün kurutma) | 82,8 | 81,4 – 84,3 | 83 | +0,2 |
| PRC-2026-001 kuzu fire | 2,61 kg | 2,39 – 2,83 | 3,00 kg | +0,39 (aralık dışı) |
| PRC-2026-001 kuzu but | 6,79 kg | 6,57 – 7,02 | 6,50 kg | −0,29 (aralık dışı) |

Kuzu örneğinde model, Kıvırcık ırkı ve iyi kasap için beklenenden ~0,3 kg fazla fire ve ~0,3 kg az but
görüyor. Parça bazında %80 aralığın dışında ama %98 bandının içinde (`anomaly` yok); 6 parça birlikte
değerlendirildiğinde de (d² = 7,9 < 18)
normal varyasyon içinde: kalibre edilmiş dedektör **uyarı üretmez**. Tek tek aralık kontrolü bu tür
kayıtlarda yanlış alarm verir (aşağıdaki tabloda "%90 aralık kuralı").

## KI anomali uyarıları (isteğe bağlı özellik: düşük verim / beklenmedik fire)

Kod: [`anomaly_ali.py`](anomaly_ali.py), sonuçlar: `artifacts/ali/anomaly_metrics.json`.

**Fikir.** Sabit bir kural ("verim %82'nin altındaysa alarm") yağsız et, 6 gün kurutma, yaz ayı gibi
*meşru* sebepleri de alarm sanır. Verim modeli bu koşulları zaten bildiği için, gerçek değer ile model
tahmini arasındaki **açıklanamayan** sapmaya bakarız.

* **4A üretim — sağlam z-skoru.** Artık `r = gerçek verim − tahmin` (OOF). Ölçek medyan ve MAD'dan
  (`σ ≈ 1,4826·MAD`), çünkü eğitim verisindeki gerçek arızalar standart sapmayı şişirir (std 1,72 puan,
  sağlam σ 1,01 puan). `z ≤ −2,5` → WARN, `z ≤ −3,5` → CRITICAL (`LOW_YIELD`); `z ≥ +3,5` → `HIGH_YIELD`
  (sayım/kayıt hatası şüphesi). Uyarı metni beklenen paket, eksik paket ve **tahmini kayıp €**
  (eksik paket × EKP) içerir.
* **4B parçalama — Mahalanobis uzaklığı.** 6 payın artık vektörü birlikte değerlendirilir. Paylar toplamı
  100 olduğu için artıklar ilişkilidir (but azalınca fire artar); tek tek eşik koymak bunu kaçırır. Sağlam
  kovaryans (`MinCovDet`, ilk 5 pay; 6. diğerlerinden belirlenir) ile `d²` hesaplanır. `d² ≥ 18` → WARN,
  `d² ≥ 27` → CRITICAL. Yön: fire belirgin fazlaysa `FIRE_HIGH`, değilse `PART_MISMATCH` (yanlış etiket/
  tartım). En çok sapan iki parça metne yazılır: *"fire +1,29 kg, but −1,19 kg"*.
* **Eşikler sızıntısız seçilir.** Uyarı eşiği yalnız train OOF artıklarına enjekte edilmiş anomalilerle
  F1'i en yüksek yapacak şekilde seçilir; kritik eşik temiz train satırlarında en fazla %0,1 yanlış alarm
  verecek şekilde. Test kümesine hiç bakılmaz (test: `test_thresholds_are_chosen_without_test_rows`).

### Nasıl ölçüldü

Gerçek etiketli arıza verisi olmadığı için ölçüm sentetik ve **etiketi kesin** anomalilerle yapılır:

* 4A: üretecin kendi doğal arızaları (%2, verim 4–10 puan düşer; `with_fault_label=True` ile etiket) +
  temiz satırların %6'sına **1,5–6 puan** verim düşüşü (paket tam sayıya yuvarlanır).
* 4B: karkasların %6'sında karkasın **%1–4'ü** (25 kg kuzuda 0,25–1 kg) bir satılabilir parçadan fireye
  (%60: kayıp/kötü kesim) veya başka parçaya (%40: yanlış etiket) kaydırılır; kütle dengesi korunur.
* Zaman bazlı test kümesi (4A 302 emir, 4B 500 karkas), 30 farklı rastgele enjeksiyon turu, ortalama ± std.
* İki referans kural, eşikleri aynı train validasyonuyla adil şekilde ayarlanmış: **reçete kuralı**
  (verim < 85 − k; fire > reçete + k) ve mevcut **%90 aralık kuralı**.

### Sonuçlar (test, 30 tur ortalaması)

**4A — düşük verim uyarısı** (tur başına 24 anomali / 302 emir)

| Yöntem | Precision | Recall | F1 | Yanlış alarm / 100 emir |
|---|---|---|---|---|
| Reçete kuralı (verim < %78,75) | 0,57 | 0,55 | 0,56 ± 0,07 | 3,3 |
| %90 aralık kuralı | 0,47 | 0,92 | 0,62 ± 0,03 | 8,4 |
| **Dedektör (sağlam z)** | **0,85** | **0,85** | **0,85 ± 0,04** | **1,2** |

Düşüş büyüklüğüne göre recall: 1,5–3 puan %33, 3–4,5 puan %85, 4,5+ puan %100. Ortalama precision
(PR-AUC) 0,93.

**4B — beklenmedik fire / parça kayması** (tur başına 30 anomali / 500 karkas)

| Yöntem | Precision | Recall | F1 | Yanlış alarm / 100 karkas |
|---|---|---|---|---|
| Reçete fire kuralı (fire > reçete + 2,25 puan) | 0,31 | 0,35 | 0,33 ± 0,08 | 4,7 |
| %90 aralık kuralı (herhangi bir parça dışarıda) | 0,12 | 0,97 | 0,21 ± 0,01 | 43,7 |
| **Dedektör (Mahalanobis)** | **0,95** | **0,66** | **0,78 ± 0,07** | **0,2** |

Kayma büyüklüğüne göre recall: karkasın %1–2'si %23, %2–3'ü %78, %3+ %99. PR-AUC 0,84.

**Dürüst notlar.** (1) Rakamlar sentetik süreç üzerindedir; gerçek işletmede doğruluk
`v_ml_anomaly_feedback` görünümüyle ölçülür (kullanıcı uyarıyı RESOLVED / FALSE_ALARM kapatır →
gözlenen precision). (2) Çok küçük sapmalar (1,5–3 puan, < %2 karkas) model hata payıyla karışır;
dedektör bunları bilinçli olarak az yakalar, çünkü yakalamak yanlış alarmı katlar. (3) Parçalama
dedektörü yanlış alarmı çok düşük tutacak şekilde ayarlandı (100 karkasta 0,2): kasap ekibinin
"sürekli öten alarm"ı görmezden gelmemesi için.

### Uyarıların akışı

```bash
python ml-engine/predict_ali.py production <v_ml_production_features çıktısı>.csv --alerts --emit-sql alerts.sql
mysql food_intel < alerts.sql        # ml_anomaly_alert'e yazar (aynı kayıt + sürüm tekrar yazılırsa günceller)
```

Seed kayıtları (URT-2026-045/046, PRC-2026-001) normal çıkar, uyarı yoktur. Video/panel için
what-if senaryosu: [`database-mysql/demo/anomaly_demo.sql`](../database-mysql/demo/anomaly_demo.sql)
(78 paket teslim → CRITICAL, ≈120 € kayıp; 0,9 kg but fireye → CRITICAL FIRE_HIGH). API sözleşmesi:
[database-mysql/README.md](../database-mysql/README.md#ki-anomali-uyarıları-api-için).
