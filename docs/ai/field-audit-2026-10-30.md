# 30 Ekim 2026: saha başarımı için veri ve tahmin denetimi

**Sonuç: doğrulanmış saha başarımı şu an ölçülemiyor.** Canlı veritabanında üç tamamlanmış
üretim emri ve bir tamamlanmış parçalama kaydı bulunuyor. Bunlar seed/demo veya önceki
oturumda açıkça demo denemesi olarak kaydedilmiş işlemler. Gerçek işletme kaynağıyla
doğrulanmış bağımsız saha örneği yok; saha metrikleri bu yüzden boş bırakıldı.

## Hesaplanan hata — yalnızca demo tanısı

Ölçüm kaydedilmiş tahminlerden yapıldı; model yeniden eğitilmedi, tahminler sonuca göre
yeniden hesaplanmadı. Gerçekleşen değerler güncel üretim/parçalama kayıtlarından alındı ve
tahmin tablosunda saklanan gerçekleşenlerle karşılaştırıldı. İki yöntem aynı satırlarda ölçüldü.

| Hedef | Bağımsız işlem | Puanlanan değer | Model MAE | Geçmiş ortalama MAE |
|---|---:|---:|---:|---:|
| Üretim miktarı | 3 emir | 3 | 1,0000 paket | 1,1000 paket |
| Birim maliyet / EKP | aynı 3 emir | 3 | 0,1564 €/paket | 0,1741 €/paket |
| Parçalama, beş parça + fire | 1 karkas | 6 | 0,1452 kg | 0,0361 kg |

Bu örneklerde üretim MAE'sinde model az farkla iyi; parçalamada geçmiş ortalama daha iyi.
Üretimde RMSE ise model için 1,4095 paket, referans için 1,2298 pakettir. Tek bir ölçüte
bakarak modelin genel olarak üstün olduğu söylenemez. Örnek sayısı saha başarısı, anlamlı
üstünlük veya ekonomik fayda iddiası için kanıt oluşturmuyor. Altı parça **altı bağımsız
karkas değildir**. Üretim miktarı ile EKP de bağımsız altı üretim örneği değildir.

%80 tahmin aralıklarının bu demodaki kapsamı üretimde 2/3, parçalamada 4/6'dır. Bu kadar az
ve demo veriyle saha kalibrasyonu sonucu çıkarılmaz. MAPE yerine burada MAE/RMSE/bias ve
WAPE raporlandı; sıfır toplam gerçekleşen için WAPE tanımsız bırakılır.

## Kaynak ve zamanlama

- `URT-2026-045`, `URT-2026-046`, `PRC-2026-001`: `database-mysql/seed.sql` tarafından
  oluşturulan örnekler. Veritabanında olmaları gerçek saha kaydı olduklarını göstermez.
- `URT-2026-047`: önceki Claude devir notunda demo veritabanındaki deneme olarak tanımlanmış.
  Tahmin 29 Ekim 17:11:48.388 UTC'de, kabul stok hareketi 17:11:48.519 UTC'de yazılmış:
  yaklaşık 131 ms'lik teknik deneme akışı. Bu, gerçek üretimden önce tahmin yapılmış bir saha
  gözlemi olarak kullanılamaz.
- İki seed üretim emrinde ve parçalama örneğinde tahminler sonucun stok kaydından sonra
  yazılmış. Bunlar ileriye dönük canlı performans kanıtı değildir.
- Stok hareketinin zamanı, operatörün fiziksel sonucu ilk öğrendiği zaman değildir.
  Gerçek tartım/teslim kayıtları ve kaynak doğrulaması olmadan bu fark kapatılamaz.
- Yeni parçalama akışı tahmin ve gerçekleşeni aynı kayıt isteğinde saklıyor. İleriye dönük
  saha testi için tartımdan önceki tahminin ayrıca dondurulup izlenmesi gerekir.

Gerçek geçmiş işletme kayıtları sağlanırsa, dondurulmuş modelle ayrı bir **geriye dönük dış veri
testi** yapılabilir. Bu da demo testinden farklıdır; canlı kullanım faydasını ölçen ileriye
dönük pilotla aynı iddia olarak sunulmaz.

## Saha ölçümünü tamamlamak için gereken veri

1. Gerçek üretim ve parçalama kayıtlarının dosya konumu veya açıkça doğrulanmış kayıt
   numaraları; kaynak işletme/tartım/teslim belgesi ve doğrulayan kişi.
2. Modelin karar anında erişebildiği girdiler, kullanılan model sürümü, dondurulmuş model
   ve referans tahminleri; gerçekleşen çıktı ve fiziksel gözlem zamanı.
3. Eğitimden ayrı tutulan kayıtlar. Başarısız, sıfır çıktılı, eksik veya tahmin üretilmemiş
   işlemler de korunmalı; yalnız başarılı örnek seçilmemeli.
4. Üretimde paket hatası ve EKP hatası; parçalamada parça/fire hatası. Birimler ve ürün/tür,
   tedarikçi, operatör ve dönem ayrımları korunmalı. Ekonomik fayda ancak gerçek tolerans ve
   operasyon maliyetleri tanımlandıktan sonra ayrıca ölçülmeli.

Mevcut sentetik öğrenme eğrisindeki “400 emir / 100–200 karkas” sayıları gerçek saha için
garanti edilen örneklem ihtiyacı değildir. Yeterlilik gerçek hata değişkenliği ve hedeflenen
güven aralığına göre belirlenir. Bu denetim anlamlılık veya kabul eşiği üretmez.

## Tekrarlanabilir kanıt

- [Salt okunur dışa aktarma sorgusu](../../ml-engine/field_audit_export.sql)
- [Kaynak sınıflandırmaları](../../ml-engine/field_origins.json)
- [24 tahmin satırlık snapshot](../../ml-engine/artifacts/field-audit-2026-10-30/predictions.jsonl)
- [Makine okunur sonuç ve girdi SHA-256 değerleri](../../ml-engine/artifacts/field-audit-2026-10-30/report.json)
- [Standart Python ile çalışan denetleyici](../../ml-engine/audit_field.py)

```bash
python ml-engine/audit_field.py ml-engine/artifacts/field-audit-2026-10-30/predictions.jsonl \
  --origins ml-engine/field_origins.json \
  --output ml-engine/artifacts/field-audit-2026-10-30/report.json
python -m unittest discover -s ml-engine/tests -p test_field_audit.py -v
```

Denetleyici demo/bilinmeyen kaynağı, eksik kaynak doğrulamasını, eğitim bağımsızlığı
belirsizliğini, fiziksel gözlemden sonra yazılmış tahmini, eski gerçekleşen değerini ve çift
tahmin satırlarını saha puanlamasına almaz. Farklı model sürümleri birlikte puanlanmaz.
Geçmiş veriye sonradan model çalıştırmak için ayrı bir dış veri protokolü gerekir.

Bu sorgu kaydedilmiş tahminleri denetler; tahmin hiç üretilmeyen işlemlerin toplamı ayrıca
sayılmalıdır. Canlı tablolara, tahminlere, model ağırlıklarına ve servis yapılandırmasına
yazılmadı. Uygulamaya saha başarımı yüzdesi eklenmedi.
