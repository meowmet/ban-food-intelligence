import React, { createContext, useContext, useMemo, useState } from 'react';
import { setApiLanguage } from './api';

// Terms follow docs/GLOSSARY.md.
export const DICT = {
  tr: {
    rp_title: 'Geri toplama ve müşteri takibi', rp_note: 'Bekleyen miktar = teslimat − kayıtlı ileri sevk − fiziksel iade. Müşteri teyidi iade miktarını değiştirmez. Tutarlar birim bazında ayrı hesaplanır.',
    rp_pending: 'Toplanması beklenen', rp_forwarded: 'İleri sevk', rp_contact: 'İletişim',
    rp_PENDING: 'Ulaşılmadı', rp_CONTACTED: 'Ulaşıldı', rp_CONFIRMED: 'Müşteri teyit etti',
    rp_update: 'Takibi güncelle', rp_return: 'İade alındı kaydet', rp_noteLabel: 'Görüşme notu / teslim tutanağı', rp_save: 'Kaydet',
    rp_quarantine: 'İade karantina deposuna alınır', rp_saved: 'Takip kaydedildi.',
    rp_badQty: 'Miktar sıfırdan büyük ve bekleyen miktardan fazla olmamalıdır.',
    rp_total: '{0} geri alındı / {1} sorumlu miktar', rp_recovered: 'Kayıtlı teslimatların geri toplanması tamamlandı. Lotların satış blokesi devam eder.',
    rp_quantityWarning: 'İleri sevk ve iade toplamı teslimatı aşıyor. Kayıtları inceleyin.',
    report_download: 'Değerlendirme raporu (PDF)', report_busy: 'Rapor hazırlanıyor…', report_note: 'Üç görev, maliyet ve ağırlık açıklamaları, lot zinciri, müşteriler ve YZ sonuçları tek PDF’de.',
    report_title: 'Değerlendirme raporu', report_snapshot: 'Kayıt anı', report_production: '1 · Dış üretim ve gerçek maliyet',
    report_butchery: '2 · Parçalama ve tutarsızlıklar', report_trace: '3 · Lot zinciri ve etkilenen müşteriler', report_ai: '4 · YZ: tahmin ve referans',
    report_limit: 'Veri sentetik demo/eğitim verisidir. Gerçek işletme başarımı kanıtlanmamıştır. İzlenebilirlik yalnızca kayıtlı lot ve sevkiyat ilişkilerine dayanır.',
    report_noRecall: 'Bu lot için kayıtlı geri çağırma yok. Aşağıda etkilenecek kayıtlı teslimatlar gösterilir.',
    report_root: 'İzlenen kök lot', report_footer: 'BAN Food Intelligence · Kaynak: canlı veritabanı kayıtları', report_missingFont: 'Rapor yazı tipi yüklenemedi. Lütfen tekrar deneyin.',
    report_findings: 'Kontrol açıklamaları', report_page: 'Sayfa', report_predictions: 'Örnek tahminler', report_modelRole: 'Yöntem',
    appName: 'BAN Food Intelligence',
    appSub: 'Üretim, maliyet, satış ve lot izlenebilirliği',
    tab_overview: 'Genel bakış', tab_purchases: 'Alışlar', tab_production: 'Üretim (EKP)', tab_butchery: 'Parçalama',
    tab_movements: 'Stok hareketleri', tab_sales: 'Satışlar', tab_trace: 'İzleme ve geri çağırma', tab_ai: 'Yapay zekâ',
    loading: 'Yükleniyor…', retry: 'Tekrar dene', details: 'Teknik ayrıntılar', noData: 'Kayıt yok.',
    errorNetwork: "Sunucuya bağlanılamadı. Go API'nin çalıştığını kontrol edin.",
    total: 'Toplam', lot: 'Lot', product: 'Ürün', qty: 'Miktar', unitPrice: 'Birim fiyat', amount: 'Tutar', date: 'Tarih',
    supplier: 'Tedarikçi', customer: 'Müşteri', warehouse: 'Depo', status: 'Durum', document: 'Belge',

    ov_title: 'Genel bakış', ov_sub: 'Tüm değerler canlı olarak MySQL üzerinden Go API ile gelir.',
    ov_production: 'Üretim emri {0}', ov_ekp: 'Birim maliyet (EKP)', ov_vkp: 'Satış fiyatı (VKP)', ov_margin: 'Marj',
    ov_variance: 'Miktar farkı', ov_butchery: 'Parçalama', ov_alloc_ok: 'Dağıtılan = toplam maliyet', ov_alloc_bad: 'Dağıtım farkı',
    ov_rejected: 'Reddedilen kayıt {0}', ov_sales: 'Satışlar', ov_revenue: 'Ciro', ov_gp: 'Brüt kâr', ov_stock: 'Stok',
    ov_lots_in_stock: 'Stokta lot', ov_stock_value: 'Stok değeri', ov_recall: 'Geri çağırma', ov_active_recalls: 'Aktif geri çağırma',
    ov_ai: 'Yapay zekâ (sentetik test)', ov_ai_packages: 'Paket tahmini MAPE', ov_vs_baseline: 'referans {0}',
    ov_lines: '{0} satır',
    ov_rej_sub: 'beyan; hesaplanan {0}, fark +{1}, {2} kayıtsız',

    pu_title: 'Alışlar', pu_sub: 'Hammadde girişleri: tedarikçi, lot, miktar, fiyat ve son tüketim tarihi.',
    receipt: 'Giriş fişi', invoice: 'Fatura', expiry: 'SKT', supplierLot: 'Tedarikçi lotu',

    pr_title: 'Üretim ve gerçek maliyet (EKP)', pr_sub: 'Dış hizmetle üretim: hammadde maliyeti + hizmet faturası, gerçek mamul miktarına bölünür.',
    planned: 'Planlanan', actual: 'Gerçekleşen', variance: 'Fark', totalCost: 'Toplam maliyet', materialCost: 'Hammadde',
    serviceCost: 'Hizmet faturası', ekp: 'Birim maliyet (EKP)', vkp: 'Satış fiyatı (VKP)', gpUnit: 'Brüt kâr / paket',
    gpTotal: 'Brüt kâr (toplam)', margin: 'Marj', revenueVkp: 'VKP ile ciro', breakdown: 'Maliyet kırılımı', costElement: 'Maliyet unsuru',
    outputLot: 'Mamul lotu', formula: '{0} ÷ {1} paket = {2}',
    pr_newOrder: 'Yeni üretim emri', pr_orderTitle: 'Dış üretim emri', pr_orderNo: 'Emir no', pr_subcontractor: 'Fason üretici',
    pr_plannedQty: 'Planlanan paket', pr_servicePrice: 'Hizmet bedeli / paket (€)', pr_dryingDays: 'Kurutma (gün)',
    pr_available: 'Depoda', pr_saveOrder: 'Emri kaydet ve hammaddeyi sevk et', pr_preview: 'Önizle',
    pr_orderNote: "Hammadde ana depodan fasona sevk edilir; maliyeti sevk anında sabitlenir. Önizle, hiçbir şey kaydetmeden bütün kontrolleri yapar ve yapay zekânın beklediği paket sayısını ve EKP'yi gösterir; kayıtla birlikte tahmin saklanır.",
    pr_previewOk: 'Kontrol tamam, henüz kaydedilmedi. Hammadde maliyeti {0}.',
    pr_noForecast: 'Bu girdilerle tahmin yapılamıyor: et girdisi yok ya da girdiler modelin öğrendiği aralığın dışında.',
    pr_orderSaved: '{0} kaydedildi, hammadde fasona sevk edildi. Hammadde maliyeti {1}.',
    pr_atSubcontractor: 'Fasonda', pr_inputKg: 'Sevk edilen', pr_receive: 'Mamul kabulü', pr_saveReceive: 'Kabulü kaydet',
    pr_actualQty: 'Teslim alınan paket',
    pr_receiveNote: "Gerçek paket sayısı EKP'yi belirler; yapay zekâ tahmini bununla karşılaştırılır.",
    pr_fcPackages: 'Tahmini paket (YZ)', pr_fcEkp: 'Tahmini EKP (YZ)', pr_fcRange: '%80 aralık {0}',
    pr_fcDeviation: 'Gerçekleşen − tahmin', pr_fcWithin: 'Beklenen bantta',
    pr_fcNote: 'Tahmin malzeme sevk edilirken yapıldı (Ridge, sentetik geçmiş veri). Referans: ürünün geçmiş ortalama verimi. Anomali: gerçekleşen değer %98 bandın dışında.',

    bu_title: 'Parçalama ve maliyet dağıtımı', bu_sub: 'Giriş lotu, ağırlık dengesi ve maliyetin parça lotlarına dağıtımı.',
    findings: 'Kontroller', weightCheck: 'Ağırlık dengesi', input: 'Giriş', cuts: 'Parçalar', byProduct: 'Yan ürün', waste: 'Fire',
    unrecorded: 'Kayıtsız', tolerance: 'Tolerans', allocationStored: 'Kayıtlı dağıtım', allocationPreview: 'Dağıtım anahtarı',
    methodValue: 'Değer ağırlıklı (kg × VKP)', methodQty: 'Miktar ağırlıklı (kg)', share: 'Pay', allocated: 'Dağıtılan maliyet',
    unitCost: 'Birim maliyet', salesValue: 'Satış değeri', yield: 'Verim', sumsExact: 'Toplam tam olarak {0}',
    rejected: 'Reddedildi', posted: 'Kaydedildi', declared: 'Beyan edilen', computed: 'Hesaplanan', previewNote: 'Kuruş farkları en büyük kalanlı satıra verilir; toplam her zaman toplam maliyete eşittir.',

    mv_title: 'Stok hareketleri', mv_sub: 'Değiştirilemez stok defteri; her hareketten sonra lot ve depo bakiyesi.',
    filterLot: 'Lot filtresi', allLots: 'Tüm lotlar', type: 'Hareket', balanceAfter: 'Bakiye', reference: 'Referans',

    sa_title: 'Satışlar', sa_sub: 'Kotlin satış uygulamasından gelen satış satırları, lot maliyeti ve brüt kâr.',
    soldAt: 'Satış zamanı', store: 'Mağaza', device: 'Cihaz', lineTotal: 'Satır tutarı', cost: 'Maliyet', gp: 'Brüt kâr',

    tab_alerts: 'Uyarılar', al_title: 'Uyarılar',
    al_sub: 'Kayıt anında otomatik işaretlenen sapmalar: YZ tahmininin %98 bandı dışında kalan verim ve fire, son alışlara göre fiyat artışı, kasada reddedilen geri çağrılmış veya SKT’si geçmiş lot satışı, stok sayımında eksik. Uyarı stoku, maliyeti ve lot durumunu değiştirmez. Yetkili inceler ve kapatır.',
    al_open: 'Açık uyarı', al_critical: 'Kritik (açık)', al_precision: 'Gözlenen isabet', al_precisionSub: '{0} gerçek sorun / {1} yanlış alarm',
    al_precisionNone: 'Henüz kapatılan uyarı yok', al_loss: 'Doğrulanan kayıp', al_lossSub: 'Gerçek sorun olarak kapatılan uyarıların tahmini tutarı',
    al_list: 'Uyarı listesi', al_f_open: 'Açık', al_f_closed: 'Kapatılan', al_f_all: 'Tümü', al_noneOpen: 'Açık uyarı yok.',
    al_kind: 'Uyarı', al_expected: 'Beklenen', al_actual: 'Gerçekleşen', al_impact: 'Tahmini etki', al_pctUp: 'son alışlara göre +{0}', al_count: 'Uyarı sayısı',
    al_sev_WARN: 'Uyarı', al_sev_CRITICAL: 'Kritik',
    al_task_PRODUCTION_YIELD: 'Üretim verimi · YZ (A)', al_task_BUTCHERY_YIELD: 'Parçalama · YZ (B)', al_task_PURCHASE_COST: 'Alış maliyeti', al_task_LOT_MOVEMENT: 'Lot hareketi (kasa, sayım)',
    al_dir_LOW_YIELD: 'Düşük verim', al_dir_HIGH_YIELD: 'Beklenenden yüksek verim', al_dir_FIRE_HIGH: 'Beklenmedik fire', al_dir_PART_MISMATCH: 'Parça ağırlığı sapması',
    al_dir_COST_INCREASE: 'Alış fiyatı artışı', al_dir_BLOCKED_SALE: 'Geri çağrılmış lot satıldı', al_dir_EXPIRED_SALE: 'SKT’si geçmiş lot satıldı',
    al_hint_LOW_YIELD: 'Fason dolum/kurutma ve et lotunu kontrol edin.', al_hint_HIGH_YIELD: 'Paket ağırlığını ve kabul sayımını kontrol edin.',
    al_hint_FIRE_HIGH: 'Kesim, tartım ve soğutmayı kontrol edin.', al_hint_PART_MISMATCH: 'Kesim reçetesini ve tartımı kontrol edin.',
    al_hint_COST_INCREASE: 'Tedarikçi fiyatını ve faturayı kontrol edin.',
    al_hint_BLOCKED_SALE: 'Sunucu satışı reddetti, ürün müşteriye gitmiş olabilir. Müşteriye ulaşın, rafı sayın.',
    al_hint_EXPIRED_SALE: 'Sunucu satışı reddetti, ürün müşteriye gitmiş olabilir. Rafı ve stoku kontrol edin.',
    al_st_OPEN: 'Açık', al_st_ACKNOWLEDGED: 'İnceleniyor', al_st_RESOLVED: 'Gerçek sorun · kapatıldı', al_st_FALSE_ALARM: 'Yanlış alarm · kapatıldı',
    al_review: 'İncele', al_decision: 'Karar', al_do_ACKNOWLEDGED: 'İnceleniyor olarak işaretle', al_do_RESOLVED: 'Gerçek sorun, kapat', al_do_FALSE_ALARM: 'Yanlış alarm, kapat',
    al_note: 'Not', al_noteRequired: 'Not (kapatmak için zorunlu)', al_save: 'Kaydet', al_saved: 'Uyarı güncellendi.',
    al_precisionTitle: 'Uyarı başarımı (sahadaki kayıtlar)', al_confirmed: 'Gerçek sorun', al_falseAlarms: 'Yanlış alarm',
    al_precisionNote: 'İsabet = gerçek sorun / (gerçek sorun + yanlış alarm); yakalama = gerçek sorun / (gerçek sorun + kaçırılan). Altında %95 güven aralığı: az kayıtla aralık geniştir ve genel bir başarı iddiası kurulamaz. Açık uyarılar sayılmaz.',
    al_missed: 'Kaçırılan', al_recall: 'Yakalama', al_ci: '%95 GA {0}–{1}', al_missAdd: 'Kaçırılan sorun kaydet',
    al_missHint: 'Sistemin uyarmadığı ama elle bulunan bir sorun. Yakalama oranı yalnızca bu kayıtlarla ölçülebilir.',
    al_missTask: 'Hangi kontrol yakalamalıydı?', al_missRef: 'Belge no', al_missNote: 'Ne bulundu?', al_missLoss: 'Tahmini kayıp (€)',
    al_missSaved: 'Kaçırılan sorun kaydedildi.', al_missRecent: 'Son kaçırılan sorunlar',
    ar_title: 'Uyarı eşikleri',
    ar_note: 'Varsayılanlar sentetik stres testinden gelir: 100 kayıtta en fazla 1 yanlış alarm, bu bütçede en yüksek yakalama. Bu test, veri üreticinin varsayımlarına benzerse kuralın ne yapacağını gösterir; sahadaki doğruluğu yukarıdaki inceleme ve kaçırılan sorun kayıtları gösterir. Değişiklik yeni uyarılara uygulanır ve denetim kaydına yazılır.',
    ar_rule: 'Kontrol', ar_warn: 'Uyarı eşiği', ar_crit: 'Kritik eşik', ar_synth: 'Sentetik testte (uyarı eşiğinde)', ar_changed: 'Son değişiklik', ar_default: 'varsayılan',
    ar_edit: 'Değiştir', ar_reset: 'Varsayılana dön', ar_save: 'Eşiği kaydet', ar_saved: 'Eşik güncellendi. Yeni uyarılar bu değerle değerlendirilir.',
    ar_band: '{0} × %98 bant', ar_preview: 'Sentetik testte {0}:',
    ar_synthYield: 'isabet {0} · yakalama {1} · 100 kayıtta {2} yanlış alarm',
    ar_synthPrice: '100 alışta yanlış alarm (fiyat oynaklığına göre): {0}', ar_synthPriceJump: '%10–20 artışların {0} kadarı yakalanır (oynaklık %3)',
    ar_observed: 'Kayıtlı {0} fiyat değişiminin %95’i {1} altında; {2} kadarı eşiği aşıyor.', ar_observedNone: 'Henüz karşılaştırılacak kayıtlı alış yok.',
    ar_count: 'Önem eşiği: sayım eksiği ölçülmüş bir farktır, tahmin değil.',
    bell_title: 'Bildirimler', bell_label: 'Bildirimler: {0} açık konu', bell_alerts: 'Açık uyarı', bell_critical: '{0} kritik',
    fl_title: 'Lot akışı', fl_aria: '{0} lotunun akış diyagramı', fl_recalled: 'GERİ ÇAĞRILDI', fl_blocked: 'BLOKE',
    fl_noteForward: 'Kutular lot, müşteri ve son durumlardır; oklarda kayıtlı miktarlar yazar. Lot kutusuna tıklayınca lot kartı açılır. Kutu boyutları miktarla orantılı değildir, çünkü kg ve paket farklı birimlerdir.',
    fl_noteBackward: 'Soldaki hammadde lotlarından sağdaki lota; oklarda kayıtlı tüketim ve üretim miktarları yazar. Lot kutusuna tıklayınca lot kartı açılır.',
    fl_stock: 'Stokta', fl_sold: 'Kasada satıldı', fl_waste: 'Fire / imha', fl_gap: 'Belgesiz fark',
    sc_start: 'Stok sayımı yap', sc_title: 'Stok sayımı',
    sc_intro: 'Rafta saydığınız miktarı girin; boş bırakılan satır sayılmamış sayılır ve kaydedilmez. Fark, gerekçesiyle sayım düzeltmesi olarak deftere işlenir ve kütle dengesinde belgesiz fark olarak görünür. Liste açıldıktan sonra bir lotun stoğu değiştiyse sayım reddedilir; güncel miktarlarla tekrar kontrol edin.',
    sc_system: 'Sistemde', sc_counted: 'Sayılan', sc_countedFor: '{0} sayılan miktar', sc_diff: 'Fark', sc_reason: 'Gerekçe', sc_reasonFor: '{0} fark gerekçesi',
    sc_reasonNeeded: 'Fark için gerekçe yazın.', sc_whole: 'Bu lot tam birimle sayılır.', sc_bad: 'Geçerli bir miktar girin.',
    sc_summary: '{0} lot sayıldı · {1} fark · eksik {2} · fazla {3}', sc_note: 'Sayım notu', sc_save: 'Sayımı kaydet',
    sc_saved: '{0} kaydedildi: {1} lot, eksik {2}, fazla {3}.', sc_none: 'Bu depoda stokta lot yok.',
    sc_history: 'Son sayımlar', sc_lines: 'Sayılan lot', sc_diffs: 'Farklar', sc_noDiff: 'Fark yok', sc_short: 'Eksik', sc_over: 'Fazla',
    al_dir_COUNT_SHORTAGE: 'Sayımda eksik', al_hint_COUNT_SHORTAGE: 'Rafı, karantinayı ve kayıtsız fire veya satışı kontrol edin.',
    mb_title: 'Kütle dengesi', mb_balanced: 'Her miktar belgeli', mb_gap: 'Belgesiz fark var',
    mb_atCustomers: 'Müşteride', mb_sold: 'Kasada satıldı', mb_inStock: 'Stokta', mb_atSub: 'fasoncuda {0}', mb_wasted: 'Fire / imha',
    mb_unexplained: 'Belgesiz fark', mb_declared: 'Lot miktarı', mb_used: 'Lotlara işlendi', mb_shipped: 'Sevk', mb_returned: 'İade (+)',
    mb_countDiff: 'Sayım farkı',
    mb_note: 'Lot miktarı = lotlara işlenen + sevk + kasa satışı + fire + stok − iade. Lotlara işlenen miktar, ondan yapılan lotlarda izlenir. Belgesiz fark bir sayım düzeltmesinden veya girişi hiç yapılmamış lot miktarından gelir; geri çağırmada bu miktarın yeri bilinmez. Müşteride = 1. kademe sevk − iade.',
    tab_lots: 'Lotlar ve stok', lots_title: 'Lotlar ve stok',
    lots_sub: 'Her lotun depodaki miktarı, durumu ve SKT’si. SKT’sine {0} gün veya daha az kalan satılabilir lotlar önce satılmalı. Bir lot numarasına tıklayınca lot kartı açılır.',
    lots_soon: 'SKT ≤ {0} gün', lots_unsellable: 'Satılamaz', lots_f_stock: 'Stokta', lots_f_all: 'Tümü (sıfır stok dahil)',
    lots_search: 'Lot veya ürün ara', lots_value: 'Stok değeri',
    ls_ACTIVE: 'Aktif', ls_RECALLED: 'Geri çağrıldı', ls_BLOCKED: 'Bloke', ls_DEPLETED: 'Tükendi', ls_expired: 'SKT geçti',
    ls_daysLeft: '{0} gün kaldı', ls_lastDay: 'Bugün son gün', ls_daysPast: '{0} gün geçti',
    origin_PURCHASE: 'Alış', origin_PRODUCTION: 'Üretim', origin_BUTCHERY: 'Parçalama', origin_REWORK: 'Yeniden işleme', origin_RETURN: 'İade',
    lp_openTrace: 'İzleme ve geri çağırmada aç', lp_recalled: '{0} kapsamında geri çağrıldı (kök lot {1}). {2}',
    lp_initial: 'İlk miktar', lp_inStock: 'Stokta', lp_madeFrom: 'Nereden yapıldı', lp_sources: 'Tedarikçiye kadar kaynak lotlar',
    lp_derived: 'Bu lottan yapılan lotlar', lp_buyers: 'Sevkiyatlar ve alıcılar', lp_noBuyers: 'Bu lot ve ondan yapılanlar için kayıtlı sevkiyat yok.',
    lp_movements: 'Stok hareketleri',
    mf_new: 'Hareket kaydet', mf_title: 'Stok hareketi', mf_transfer: 'Depo transferi', mf_waste: 'Fire / imha', mf_rework: 'Yeniden işleme',
    mf_to: 'Giriş deposu', mf_consumed: 'Kullanılan miktar', mf_outputLot: 'Yeni lot no', mf_outputQty: 'Çıkan miktar',
    mf_reason: 'Fire nedeni', mf_noteLabel: 'Not', mf_tooMuch: 'Depoda yalnızca {0} var', mf_noStock: '{0} için stok yok.',
    mf_save: 'Hareketi kaydet', mf_saved: '{0} kaydedildi:', mf_balances: 'Hareket sonrası bakiye', mf_again: 'Yeni hareket',
    mf_note_transfer: 'Lot, miktar ve maliyet değişmeden depo değiştirir. Fasona hammadde yalnızca üretim emriyle gönderilir. Geri çağrılmış lot karantinaya taşınabilir.',
    mf_note_waste: 'Stoktan fire olarak düşer; neden zorunludur. Geri çağrılmış veya SKT’si geçmiş lotlar da imha edilebilir.',
    mf_note_rework: 'Lotun bir kısmı tüketilir ve aynı ürünün yeni bir lotu oluşur. İki lot birbirine bağlanır (izlemede görünür), değer yeni lota taşınır, SKT aynı kalır. Geri çağrılmış lot yeniden işlenemez.',

    tab_shipments: 'Sevkiyatlar', sh_title: 'Sevkiyatlar',
    sh_sub: 'Müşterilere sevk edilen lotlar. İleriye izleme ve geri çağırma alıcıları yalnızca bu kayıtlardan bulur.',
    sh_new: 'Yeni sevkiyat', sh_formTitle: 'Sevkiyat kaydı', sh_no: 'Sevkiyat no', sh_deliveryNote: 'İrsaliye no',
    sh_source: 'Kaynak', sh_ourStock: 'Depomuzdan (1. kademe)', sh_onward: 'Müşterinin ileri sevki (2. kademe)',
    sh_fromWarehouse: 'Çıkış deposu', sh_fromCustomer: 'İleri sevk eden müşteri', sh_toCustomer: 'Alıcı', sh_choose: 'Seçin',
    sh_holding: 'Müşteride', sh_badNumber: 'Sayı girin', sh_save: 'Sevkiyatı kaydet', sh_saved: '{0} kaydedildi ({1}).',
    sh_totalOurs: 'Depomuzdan sevk toplamı',
    sh_note: 'Stok çıkış deposundan düşer. Geri çağrılmış, bloke veya SKT’si geçmiş lotlar ve stoktan fazlası reddedilir. Birim fiyat boşsa VKP kullanılır.',
    sh_onwardNote: 'Müşterinin bildirdiği ileri sevk kaydedilir; bizim stoğumuz değişmez. Miktar, müşteriye teslim edilenden önceki ileri sevkler ve iadeler düşülerek sınırlanır. Geri çağırmadan sonra da kaydedilebilir; alıcı o zaman geri çağırma listesine girer.',

    tr_title: 'İzleme ve geri çağırma', tr_sub: 'Sonuçlar yalnızca kayıtlı lot ilişkilerinden gelir; yapay zekâ kullanılmaz.',
    lotNo: 'Lot numarası', forward: 'İleriye izleme', backward: 'Geriye izleme', show: 'Göster',
    lotsReached: 'Ulaşılan lotlar', depth: 'Düzey', origin: 'Kaynak', links: 'Lot ilişkileri', inputLot: 'Girdi lotu', outputLot2: 'Çıktı lotu',
    consumed: 'Kullanılan', produced: 'Üretilen', shipments: 'Sevkiyatlar ve alıcılar', tier: 'Kademe', via: 'Üzerinden',
    contact: 'Yetkili', phone: 'Telefon', email: 'E-posta', address: 'Adres', stockNow: 'Depolardaki stok', posSales: 'Mağaza satışları',
    recallTitle: 'Geri çağırma', startRecall: '{0} için geri çağırma başlat', confirmRecall: '{0} ve ondan üretilen tüm lotlar bloke edilecek. Emin misiniz?',
    confirm: 'Evet, başlat', cancel: 'Vazgeç', recallStarted: '{0} başlatıldı. {1} lot bloke edildi.', recallFailed: 'Geri çağırma başlatılamadı.', supervisorPin: 'Yetkili PIN', pinHint: 'Geri çağırmayı yalnızca yetkili başlatabilir. İşlem adınızla kaydedilir.',
    activeRecalls: 'Geri çağırmalar', noRecall: 'Henüz geri çağırma yok.', exportCsv: 'İletişim listesi (CSV)', print: 'Yazdır',
    secondTierRecorded: 'İleri sevk kayıtlı', secondTierNotRecorded: 'İleri sevk kayıtlı değil', shipped: 'Sevk edilen', returned: 'İade',
    affectedLots: 'Etkilenen lotlar', reason: 'Neden', recallReason: 'Hammadde lotunda kalite sorunu', lockEnforced: 'Satış engelli',

    ai_title: 'Yapay zekâ: verim ve maliyet tahmini', ai_sub: 'İki ayrı kanıt: sentetik verideki test hatası ve bu veritabanındaki kayıtlarda tahmin ile gerçekleşen. Model her ikisinde de basit bir referans yöntemle karşılaştırılır.',
    chartTitle: 'Sentetik test: dönem hatası (MAPE, düşük olan daha iyi)', model: 'Model', baseline: 'Referans (geçmiş ortalama)',
    deviation: 'Sapma', predictions: 'Bu veritabanındaki kayıtlar: tahmin ve gerçekleşen', mae: 'MAE', mape: 'MAPE', rows: 'Test satırı',
    ai_metricsTitle: 'Sentetik test: MAE / MAPE',
    o_SEED: 'Seed örneği', o_DEMO: 'Demo kaydı', o_FIELD: 'Saha · doğrulanmamış',
    ai_fieldVerified: 'Doğrulanmış saha örneği', ai_fieldNone: 'saha başarımı ölçülmedi', ai_fieldOutside: 'uygulama dışında doğrulanır',
    originNote: 'Kaynak: seed örneği (seed.sql) ya da demo kurulumunda uygulamadan girilen kayıt; saha kurulumu (DATA_ORIGIN=FIELD) saha kaydı yazar. Saha başarımı yalnızca kaynak belgesi ve doğrulayanı olan, eğitimden ayrı tutulmuş ve tahmini sonuçtan önce yazılmış kayıtlarla ölçülür (ml-engine/audit_field.py).',
    t_actual_qty: 'Paket', t_ekp: 'Birim maliyet (EKP)', t_part_kg: 'Parça ağırlıkları', t_fire_kg: 'Fire', t_but_kg: 'But',
    t_sirt_kg: 'Sırt', t_omuz_kg: 'Omuz', t_kaburga_kg: 'Kaburga', t_kiyma_kg: 'Kıyma',
    task_PRODUCTION: 'Üretim (A)', task_BUTCHERY: 'Parçalama (B)',
    interval: 'Aralık (%{0})', st_anomaly: 'Anomali', st_outside: 'Aralık dışı', st_inside: 'Aralıkta',
    intervalNote: 'Aralık: modelin konformal tahmin aralığı (gerçek değerlerin yaklaşık %80’i içine düşer). Anomali: gerçekleşen değer %98 bandın dışında; aralık dışı ama bant içi kayıtlar bilgi amaçlıdır.',
    aiNote: "Modeller sentetik, anonim geçmiş veriyle eğitildi (ml-engine/generator_ali.py). Hata, tarihlerin son %20'sinde (zaman bölmeli test) ölçülür. Veri az ve sentetik: sonuçlar yol göstericidir.",
    f_DECLARED_COST_MISMATCH: 'Maliyet tutarsızlığı', f_WEIGHT_IMBALANCE: 'Ağırlık farkı', f_WEIGHT_BALANCED: 'Ağırlık dengesi',
    f_ALLOCATION_BALANCED: 'Dağıtım', f_ALLOCATION_MISMATCH: 'Dağıtım farkı',
    en_newReceipt: 'Yeni giriş', en_receiptTitle: 'Hammadde girişi', en_newButchery: 'Parçalama kaydet', en_butcheryTitle: 'Parçalama kaydı',
    en_receiptNote: 'Her satır tedarikçi lotu ve SKT ile yeni bir alış lotu olur. Raf ömrü olan ürünlerde SKT zorunludur.',
    en_butcheryNote: 'Giriş = parçalar + yan ürünler + fire (±10 g). "Kontrol et" hiçbir şey kaydetmeden dağıtımı hesaplar.',
    en_receiptSaved: '{0} kaydedildi. Toplam {1}.', en_butcherySaved: '{0} kaydedildi. Toplam maliyet {1}.',
    en_dryRunOk: 'Kontrol tamam, henüz kaydedilmedi. Toplam maliyet {0}.',
    en_imbalance: 'Ağırlık dengesi tutmuyor: {0} kayıtsız. Hiçbir şey kaydedilmedi.',
    en_addLine: '+ Satır ekle', en_addOutput: '+ Çıktı ekle', en_removeLine: 'Satırı sil', en_close: 'Kapat',
    en_saveReceipt: 'Girişi kaydet', en_saveButchery: 'Parçalamayı kaydet', en_check: 'Kontrol et',
    en_opNo: 'İşlem no', en_operator: 'Operatör', en_additionalCost: 'Ek maliyet (€)', en_additionalCostNote: 'Ek maliyet belgesi',
    en_kind: 'Tür', en_kind_CUT: 'Parça', en_kind_BY_PRODUCT: 'Yan ürün', en_kind_WASTE: 'Fire',
    en_outputLot: 'Çıktı lotu', en_noLotForWaste: 'Fire lot almaz', en_outputsTotal: 'Çıktılar',
    en_expected: 'Beklenen (YZ)', en_samePart: 'üstteki satırla birlikte', en_partsFromForecast: 'Parçaları tahminden ekle',
    en_expectedNote: 'Beklenen: yapay zekâ tahmini ve %{0} aralığı (sentetik geçmiş veriyle eğitildi). Ağırlıkları teraziden girin; tahmin alanları doldurmaz. Yan ürünler model dışıdır. Kaydedilen işlemde tahmin ve gerçekleşen birlikte saklanır.',
    en_fcTitle: 'YZ tahmini ve gerçekleşen', en_fcFooter: 'Model aralığı %{0}. Anomali: gerçekleşen değer %98 bandın dışında. Veri sentetik; saha başarımı kanıtlanmadı.',
    en_freeze: 'Tahmini dondur',
    en_freezeHint: 'Parçaları tartmadan önce tahmini dondurun: saha doğruluğu yalnızca önceden dondurulmuş tahminle ölçülebilir. Dondurulan tahmin değiştirilemez, karkas başına bir kez yapılır.',
    en_freezeWeighed: 'Ağırlık girildi: bundan sonra dondurulan tahmin tartımdan önce yapılmış sayılmaz, dondurma kapalı.',
    en_frozenAt: 'Tahmin donduruldu: {0}, {1} için, {2} tarafından. Kaydedilen işlem bu tahminle karşılaştırılır.',
    en_frozenLinked: 'Bu karkasın dondurulmuş tahmini {0} işlemine bağlı.',
    en_frozenOtherKg: 'Dondurulan tahmin {0} içindi, girdi {1}. İşlem bu tahmine yine bağlanır; saha denetimi başka bir ağırlık için yapılmış tahmini puanlamaz.',
    en_fcFrozenTitle: 'Dondurulmuş YZ tahmini ({0}) ve gerçekleşen',
    en_fcNotFrozen: 'Bu tahmin kayıtla birlikte hesaplandı, tartımdan önce dondurulmadı; saha doğruluğuna kanıt sayılmaz.',
  },
  en: {
    rp_title: 'Recovery and customer follow-up', rp_note: 'Pending = received − recorded onward shipments − physical returns. Customer confirmation does not change returned quantities. Units are totalled separately.',
    rp_pending: 'Pending recovery', rp_forwarded: 'Forwarded', rp_contact: 'Contact',
    rp_PENDING: 'Not contacted', rp_CONTACTED: 'Contacted', rp_CONFIRMED: 'Customer confirmed',
    rp_update: 'Update follow-up', rp_return: 'Record physical return', rp_noteLabel: 'Contact note / receipt reference', rp_save: 'Save',
    rp_quarantine: 'Returned goods go to quarantine', rp_saved: 'Follow-up saved.',
    rp_badQty: 'Quantity must be positive and no greater than the pending quantity.',
    rp_total: '{0} recovered / {1} accountable quantity', rp_recovered: 'All recorded deliveries have been recovered. Lot sale blocks remain in place.',
    rp_quantityWarning: 'Forwarded plus returned exceeds received quantity. Review the records.',
    report_download: 'Assessment report (PDF)', report_busy: 'Preparing report…', report_note: 'Three tasks, cost and weight findings, lot chain, customers and AI results in one PDF.',
    report_title: 'Assessment report', report_snapshot: 'Snapshot time', report_production: '1 · External production and actual cost',
    report_butchery: '2 · Butchery and inconsistencies', report_trace: '3 · Lot chain and affected customers', report_ai: '4 · AI: predictions and baseline',
    report_limit: 'Synthetic demo/training data. Performance on real operations has not been established. Traceability uses recorded lot relationships and deliveries only.',
    report_noRecall: 'No recall is recorded for this lot. The recorded deliveries that would be affected are shown below.',
    report_root: 'Trace root lot', report_footer: 'BAN Food Intelligence · Source: live database records', report_missingFont: 'Report font could not be loaded. Please try again.',
    report_findings: 'Findings', report_page: 'Page', report_predictions: 'Sample predictions', report_modelRole: 'Method',
    appName: 'BAN Food Intelligence',
    appSub: 'Production, cost, sales and lot traceability',
    tab_overview: 'Overview', tab_purchases: 'Purchases', tab_production: 'Production (EKP)', tab_butchery: 'Butchery',
    tab_movements: 'Lot movements', tab_sales: 'Sales', tab_trace: 'Trace & recall', tab_ai: 'AI',
    loading: 'Loading…', retry: 'Try again', details: 'Technical details', noData: 'No records.',
    errorNetwork: "Can't reach the server. Check that the Go API is running.",
    total: 'Total', lot: 'Lot', product: 'Product', qty: 'Quantity', unitPrice: 'Unit price', amount: 'Amount', date: 'Date',
    supplier: 'Supplier', customer: 'Customer', warehouse: 'Warehouse', status: 'Status', document: 'Document',

    ov_title: 'Overview', ov_sub: 'All values come live from MySQL through the Go API.',
    ov_production: 'Production order {0}', ov_ekp: 'Unit cost (EKP)', ov_vkp: 'Sale price (VKP)', ov_margin: 'Margin',
    ov_variance: 'Quantity variance', ov_butchery: 'Butchery', ov_alloc_ok: 'Allocated = total cost', ov_alloc_bad: 'Allocation difference',
    ov_rejected: 'Rejected record {0}', ov_sales: 'Sales', ov_revenue: 'Revenue', ov_gp: 'Gross profit', ov_stock: 'Stock',
    ov_lots_in_stock: 'Lots in stock', ov_stock_value: 'Stock value', ov_recall: 'Recall', ov_active_recalls: 'Active recalls',
    ov_ai: 'AI (synthetic test)', ov_ai_packages: 'Package forecast MAPE', ov_vs_baseline: 'baseline {0}',
    ov_lines: '{0} lines',
    ov_rej_sub: 'declared; computed {0}, difference +{1}, {2} unrecorded',

    pu_title: 'Purchases', pu_sub: 'Raw material receipts: supplier, lot, quantity, price and expiry date.',
    receipt: 'Receipt', invoice: 'Invoice', expiry: 'Expiry', supplierLot: 'Supplier lot',

    pr_title: 'Production and real cost (EKP)', pr_sub: 'External production: material cost + service invoice, divided by the packages actually received.',
    planned: 'Planned', actual: 'Actual', variance: 'Variance', totalCost: 'Total cost', materialCost: 'Materials',
    serviceCost: 'Service invoice', ekp: 'Unit cost (EKP)', vkp: 'Sale price (VKP)', gpUnit: 'Gross profit / pack',
    gpTotal: 'Gross profit (total)', margin: 'Margin', revenueVkp: 'Revenue at VKP', breakdown: 'Cost breakdown', costElement: 'Cost element',
    outputLot: 'Finished lot', formula: '{0} ÷ {1} packs = {2}',
    pr_newOrder: 'New production order', pr_orderTitle: 'External production order', pr_orderNo: 'Order no.', pr_subcontractor: 'Subcontractor',
    pr_plannedQty: 'Planned packs', pr_servicePrice: 'Service price / pack (€)', pr_dryingDays: 'Drying (days)',
    pr_available: 'In stock', pr_saveOrder: 'Save order and send material', pr_preview: 'Preview',
    pr_orderNote: 'The raw material goes from the main store to the subcontractor; its cost is fixed when it is sent. Preview runs every check without saving anything and shows the packages and EKP the AI expects; saving the order stores the forecast.',
    pr_previewOk: 'Check passed, not saved yet. Material cost {0}.',
    pr_noForecast: 'No forecast for these inputs: there is no meat input, or the inputs are outside the range the model learned.',
    pr_orderSaved: '{0} saved, material sent to the subcontractor. Material cost {1}.',
    pr_atSubcontractor: 'At subcontractor', pr_inputKg: 'Sent', pr_receive: 'Receive finished goods', pr_saveReceive: 'Save receipt',
    pr_actualQty: 'Packs received',
    pr_receiveNote: 'The actual number of packs sets the EKP; the AI forecast is compared with it.',
    pr_fcPackages: 'Forecast packs (AI)', pr_fcEkp: 'Forecast EKP (AI)', pr_fcRange: '80% interval {0}',
    pr_fcDeviation: 'Actual − forecast', pr_fcWithin: 'Within the expected band',
    pr_fcNote: "Forecast made when the material was sent (Ridge, synthetic history). Baseline: the product's historical mean yield. Anomaly: the actual is outside the 98% band.",

    bu_title: 'Butchery and cost allocation', bu_sub: 'Input lot, weight balance and how the cost is split over the part lots.',
    findings: 'Checks', weightCheck: 'Weight balance', input: 'Input', cuts: 'Parts', byProduct: 'By-product', waste: 'Waste',
    unrecorded: 'Unrecorded', tolerance: 'Tolerance', allocationStored: 'Posted allocation', allocationPreview: 'Allocation key',
    methodValue: 'Value-weighted (kg × VKP)', methodQty: 'Quantity-weighted (kg)', share: 'Share', allocated: 'Allocated cost',
    unitCost: 'Unit cost', salesValue: 'Sales value', yield: 'Yield', sumsExact: 'Sums exactly to {0}',
    rejected: 'Rejected', posted: 'Posted', declared: 'Declared', computed: 'Computed', previewNote: 'Rounding cents go to the lines with the largest remainder; the sum always equals the total cost.',

    mv_title: 'Lot movements', mv_sub: 'Append-only stock ledger with the lot and warehouse balance after each movement.',
    filterLot: 'Lot filter', allLots: 'All lots', type: 'Movement', balanceAfter: 'Balance', reference: 'Reference',

    sa_title: 'Sales', sa_sub: 'Sale lines from the Kotlin POS with lot cost and gross profit.',
    soldAt: 'Sold at', store: 'Shop', device: 'Device', lineTotal: 'Line total', cost: 'Cost', gp: 'Gross profit',

    tab_alerts: 'Alerts', al_title: 'Alerts',
    al_sub: 'Deviations flagged automatically when a record is saved: yield and waste outside the 98 % band of the AI forecast, a price above recent purchases, POS sales of recalled or expired lots that the server refused, and stock-count shortages. An alert never changes stock, cost or lot status. A supervisor reviews and closes it.',
    al_open: 'Open alerts', al_critical: 'Critical (open)', al_precision: 'Observed precision', al_precisionSub: '{0} real problems / {1} false alarms',
    al_precisionNone: 'No alert closed yet', al_loss: 'Confirmed loss', al_lossSub: 'Estimated amount of alerts closed as real problems',
    al_list: 'Alert list', al_f_open: 'Open', al_f_closed: 'Closed', al_f_all: 'All', al_noneOpen: 'No open alerts.',
    al_kind: 'Alert', al_expected: 'Expected', al_actual: 'Actual', al_impact: 'Est. impact', al_pctUp: '+{0} on recent purchases', al_count: 'Alerts',
    al_sev_WARN: 'Warning', al_sev_CRITICAL: 'Critical',
    al_task_PRODUCTION_YIELD: 'Production yield · AI (A)', al_task_BUTCHERY_YIELD: 'Butchery · AI (B)', al_task_PURCHASE_COST: 'Purchase cost', al_task_LOT_MOVEMENT: 'Lot movement (till, count)',
    al_dir_LOW_YIELD: 'Low yield', al_dir_HIGH_YIELD: 'Higher yield than expected', al_dir_FIRE_HIGH: 'Unexpected waste', al_dir_PART_MISMATCH: 'Part weight deviation',
    al_dir_COST_INCREASE: 'Purchase price increase', al_dir_BLOCKED_SALE: 'Recalled lot sold', al_dir_EXPIRED_SALE: 'Expired lot sold',
    al_hint_LOW_YIELD: 'Check the subcontractor’s filling/drying and the meat lot.', al_hint_HIGH_YIELD: 'Check the pack weight and the receipt count.',
    al_hint_FIRE_HIGH: 'Check cutting, weighing and chilling.', al_hint_PART_MISMATCH: 'Check the cutting recipe and the weighing.',
    al_hint_COST_INCREASE: 'Check the supplier price and the invoice.',
    al_hint_BLOCKED_SALE: 'The server refused the sale; the product may be with a customer. Contact the customer, count the shelf.',
    al_hint_EXPIRED_SALE: 'The server refused the sale; the product may be with a customer. Check the shelf and the stock.',
    al_st_OPEN: 'Open', al_st_ACKNOWLEDGED: 'Under review', al_st_RESOLVED: 'Real problem · closed', al_st_FALSE_ALARM: 'False alarm · closed',
    al_review: 'Review', al_decision: 'Decision', al_do_ACKNOWLEDGED: 'Mark as under review', al_do_RESOLVED: 'Real problem, close', al_do_FALSE_ALARM: 'False alarm, close',
    al_note: 'Note', al_noteRequired: 'Note (required to close)', al_save: 'Save', al_saved: 'Alert updated.',
    al_precisionTitle: 'Alert accuracy (field records)', al_confirmed: 'Real problem', al_falseAlarms: 'False alarm',
    al_precisionNote: 'Precision = real problems / (real problems + false alarms); recall = real problems / (real problems + missed). Below each, the 95 % interval: with few records it is wide and supports no general claim of accuracy. Open alerts are not counted.',
    al_missed: 'Missed', al_recall: 'Recall', al_ci: '95 % CI {0}–{1}', al_missAdd: 'Record a missed problem',
    al_missHint: 'A problem found by hand that no alert flagged. Recall can only be measured with these records.',
    al_missTask: 'Which check should have caught it?', al_missRef: 'Document no', al_missNote: 'What was found?', al_missLoss: 'Estimated loss (€)',
    al_missSaved: 'Missed problem recorded.', al_missRecent: 'Recent missed problems',
    ar_title: 'Alert thresholds',
    ar_note: 'The defaults come from the synthetic stress test: at most one false alarm per 100 records, the highest recall within that budget. The test shows what a rule does if the data look like the generator’s assumptions; field accuracy comes from the reviews and missed problems above. A change applies to new alerts and is written to the audit log.',
    ar_rule: 'Check', ar_warn: 'Warning', ar_crit: 'Critical', ar_synth: 'Synthetic test (at the warning)', ar_changed: 'Last change', ar_default: 'default',
    ar_edit: 'Change', ar_reset: 'Back to default', ar_save: 'Save threshold', ar_saved: 'Threshold updated. New alerts are graded with it.',
    ar_band: '{0} × 98 % band', ar_preview: 'Synthetic test at {0}:',
    ar_synthYield: 'precision {0} · recall {1} · {2} false alarms per 100 records',
    ar_synthPrice: 'False alarms per 100 purchases (by price volatility): {0}', ar_synthPriceJump: '{0} of 10–20 % increases are caught (volatility 3 %)',
    ar_observed: '95 % of the {0} recorded price changes are below {1}; {2} exceed the threshold.', ar_observedNone: 'No recorded purchases to compare yet.',
    ar_count: 'Materiality: a count shortage is a measured difference, not a prediction.',
    bell_title: 'Notifications', bell_label: 'Notifications: {0} open items', bell_alerts: 'Open alerts', bell_critical: '{0} critical',
    fl_title: 'Lot flow', fl_aria: 'Flow diagram of lot {0}', fl_recalled: 'RECALLED', fl_blocked: 'BLOCKED',
    fl_noteForward: 'Boxes are lots, customers and final positions; arrows carry the recorded quantities. Click a lot to open its card. Box sizes are not proportional to quantity, because kg and packs are different units.',
    fl_noteBackward: 'From the raw-material lots on the left to the lot on the right; arrows carry the recorded consumption and output. Click a lot to open its card.',
    fl_stock: 'In stock', fl_sold: 'Sold at the till', fl_waste: 'Waste / destroyed', fl_gap: 'Undocumented gap',
    sc_start: 'Count stock', sc_title: 'Stock count',
    sc_intro: 'Enter what you counted on the shelf; an empty row is not counted and not saved. A difference is booked with its reason as a count adjustment and shows as an undocumented gap in the mass balance. If a lot’s stock changed after the list was opened, the count is refused; check again with the current quantities.',
    sc_system: 'In the ledger', sc_counted: 'Counted', sc_countedFor: 'Counted quantity of {0}', sc_diff: 'Difference', sc_reason: 'Reason', sc_reasonFor: 'Reason for the difference of {0}',
    sc_reasonNeeded: 'Enter a reason for the difference.', sc_whole: 'This lot is counted in whole units.', sc_bad: 'Enter a valid quantity.',
    sc_summary: '{0} lots counted · {1} differences · short {2} · over {3}', sc_note: 'Count note', sc_save: 'Save count',
    sc_saved: '{0} saved: {1} lots, short {2}, over {3}.', sc_none: 'No lot in stock in this warehouse.',
    sc_history: 'Recent counts', sc_lines: 'Lots counted', sc_diffs: 'Differences', sc_noDiff: 'No difference', sc_short: 'Short', sc_over: 'Over',
    al_dir_COUNT_SHORTAGE: 'Short in the stock count', al_hint_COUNT_SHORTAGE: 'Check the shelf, quarantine and unrecorded waste or sales.',
    mb_title: 'Mass balance', mb_balanced: 'Every quantity documented', mb_gap: 'Undocumented gap',
    mb_atCustomers: 'At customers', mb_sold: 'Sold at the till', mb_inStock: 'In stock', mb_atSub: 'at subcontractor {0}', mb_wasted: 'Waste / destroyed',
    mb_unexplained: 'Undocumented', mb_declared: 'Lot quantity', mb_used: 'Used in lots', mb_shipped: 'Shipped', mb_returned: 'Returned (+)',
    mb_countDiff: 'Count difference',
    mb_note: 'Lot quantity = used in lots + shipped + sold + waste + in stock − returned. What is used in lots continues in the lots made from it. An undocumented gap comes from a stock-count adjustment or a lot quantity that was never booked in; in a recall its whereabouts are unknown. At customers = tier-1 shipped − returned.',
    tab_lots: 'Lots & stock', lots_title: 'Lots & stock',
    lots_sub: 'Quantity, status and expiry of every lot per warehouse. Sellable lots with {0} days or less to expiry should be sold first. Click a lot number to open its lot card.',
    lots_soon: 'Expiry ≤ {0} days', lots_unsellable: 'Not sellable', lots_f_stock: 'In stock', lots_f_all: 'All (incl. zero stock)',
    lots_search: 'Search lot or product', lots_value: 'Stock value',
    ls_ACTIVE: 'Active', ls_RECALLED: 'Recalled', ls_BLOCKED: 'Blocked', ls_DEPLETED: 'Depleted', ls_expired: 'Expired',
    ls_daysLeft: '{0} days left', ls_lastDay: 'Last day today', ls_daysPast: '{0} days past',
    origin_PURCHASE: 'Purchase', origin_PRODUCTION: 'Production', origin_BUTCHERY: 'Butchery', origin_REWORK: 'Rework', origin_RETURN: 'Return',
    lp_openTrace: 'Open in trace & recall', lp_recalled: 'Recalled under {0} (root lot {1}). {2}',
    lp_initial: 'Initial quantity', lp_inStock: 'In stock', lp_madeFrom: 'Made from', lp_sources: 'Source lots up to the supplier',
    lp_derived: 'Lots made from this lot', lp_buyers: 'Shipments and buyers', lp_noBuyers: 'No recorded shipment for this lot or the lots made from it.',
    lp_movements: 'Stock movements',
    mf_new: 'Record movement', mf_title: 'Stock movement', mf_transfer: 'Transfer', mf_waste: 'Waste / disposal', mf_rework: 'Rework',
    mf_to: 'To warehouse', mf_consumed: 'Quantity used', mf_outputLot: 'New lot no.', mf_outputQty: 'Quantity out',
    mf_reason: 'Waste reason', mf_noteLabel: 'Note', mf_tooMuch: 'Only {0} in this warehouse', mf_noStock: 'No stock for {0}.',
    mf_save: 'Save movement', mf_saved: '{0} saved:', mf_balances: 'Balance after the movement', mf_again: 'Another movement',
    mf_note_transfer: 'Moves the lot to another warehouse; lot, quantity and cost stay the same. Raw material goes to the subcontractor only with a production order. A recalled lot may be moved to quarantine.',
    mf_note_waste: 'Books the quantity out as waste; a reason is required. Recalled or expired lots can be disposed of too.',
    mf_note_rework: 'Uses part of the lot and creates a new lot of the same product. Both lots are linked (visible in the trace), the value moves to the new lot and the expiry stays. A recalled lot cannot be reworked.',

    tab_shipments: 'Shipments', sh_title: 'Shipments',
    sh_sub: 'Lots shipped to customers. Forward trace and recall find buyers only from these records.',
    sh_new: 'New shipment', sh_formTitle: 'Shipment', sh_no: 'Shipment no.', sh_deliveryNote: 'Delivery note no.',
    sh_source: 'Source', sh_ourStock: 'From our warehouse (tier 1)', sh_onward: "Customer's onward delivery (tier 2)",
    sh_fromWarehouse: 'From warehouse', sh_fromCustomer: 'Forwarded by', sh_toCustomer: 'Buyer', sh_choose: 'Choose',
    sh_holding: 'At customer', sh_badNumber: 'Enter a number', sh_save: 'Save shipment', sh_saved: '{0} saved ({1}).',
    sh_totalOurs: 'Shipped from our warehouses',
    sh_note: 'The stock leaves the chosen warehouse. Recalled, blocked or expired lots and more than the stock are refused. An empty unit price uses the VKP.',
    sh_onwardNote: "Records an onward delivery the customer reported; our stock does not change. The quantity is capped at what the customer received minus earlier onward deliveries and returns. It can be recorded after a recall too; the buyer then joins the recall list.",

    tr_title: 'Trace & recall', tr_sub: 'Results come only from recorded lot relations; no AI is used.',
    lotNo: 'Lot number', forward: 'Forward trace', backward: 'Backward trace', show: 'Show',
    lotsReached: 'Lots reached', depth: 'Level', origin: 'Origin', links: 'Lot relations', inputLot: 'Input lot', outputLot2: 'Output lot',
    consumed: 'Used', produced: 'Produced', shipments: 'Shipments and buyers', tier: 'Tier', via: 'Via',
    contact: 'Contact', phone: 'Phone', email: 'Email', address: 'Address', stockNow: 'Stock in warehouses', posSales: 'Shop sales',
    recallTitle: 'Recall', startRecall: 'Start recall for {0}', confirmRecall: '{0} and every lot made from it will be blocked. Are you sure?',
    confirm: 'Yes, start', cancel: 'Cancel', recallStarted: '{0} started. {1} lots blocked.', recallFailed: 'The recall could not be started.', supervisorPin: 'Supervisor PIN', pinHint: 'Only a supervisor can start a recall. It is recorded under your name.',
    activeRecalls: 'Recalls', noRecall: 'No recall yet.', exportCsv: 'Contact list (CSV)', print: 'Print',
    secondTierRecorded: 'Onward delivery recorded', secondTierNotRecorded: 'Onward delivery not recorded', shipped: 'Shipped', returned: 'Returned',
    affectedLots: 'Affected lots', reason: 'Reason', recallReason: 'Quality problem in the raw material lot', lockEnforced: 'Sale blocked',

    ai_title: 'AI: yield and cost forecasts', ai_sub: 'Two separate kinds of evidence: the test error on synthetic data, and forecast vs actual on the records in this database. In both, the model is compared with a simple reference method.',
    chartTitle: 'Synthetic test: error on the test period (MAPE, lower is better)', model: 'Model', baseline: 'Baseline (historical mean)',
    deviation: 'Deviation', predictions: 'Records in this database: forecast and actual', mae: 'MAE', mape: 'MAPE', rows: 'Test rows',
    ai_metricsTitle: 'Synthetic test: MAE / MAPE',
    o_SEED: 'Seed sample', o_DEMO: 'Demo record', o_FIELD: 'Field · unverified',
    ai_fieldVerified: 'Verified field samples', ai_fieldNone: 'field performance not measured', ai_fieldOutside: 'verified outside the app',
    originNote: 'Source: a seed sample (seed.sql) or a record entered in the app of a demo deployment; a field deployment (DATA_ORIGIN=FIELD) writes field records. Field performance is measured only on records with a source document and a verifier, held out from training and forecast before the outcome (ml-engine/audit_field.py).',
    t_actual_qty: 'Packages', t_ekp: 'Unit cost (EKP)', t_part_kg: 'Part weights', t_fire_kg: 'Waste', t_but_kg: 'Leg',
    t_sirt_kg: 'Loin', t_omuz_kg: 'Shoulder', t_kaburga_kg: 'Ribs', t_kiyma_kg: 'Mince',
    task_PRODUCTION: 'Production (A)', task_BUTCHERY: 'Butchery (B)',
    interval: 'Interval ({0}%)', st_anomaly: 'Anomaly', st_outside: 'Outside interval', st_inside: 'In interval',
    intervalNote: 'Interval: the model’s conformal prediction interval (about 80% of actual values fall inside). Anomaly: the actual value is outside the 98% band; outside the interval but inside the band is informational.',
    aiNote: 'Models are trained on synthetic, anonymised history (ml-engine/generator_ali.py). Error is measured on the latest 20% of dates (time split). Small and synthetic data: indicative only.',
    f_DECLARED_COST_MISMATCH: 'Cost mismatch', f_WEIGHT_IMBALANCE: 'Weight gap', f_WEIGHT_BALANCED: 'Weight balance',
    f_ALLOCATION_BALANCED: 'Allocation', f_ALLOCATION_MISMATCH: 'Allocation gap',
    en_newReceipt: 'New receipt', en_receiptTitle: 'Raw material receipt', en_newButchery: 'Record butchery', en_butcheryTitle: 'Butchery record',
    en_receiptNote: 'Each line becomes a new purchase lot with the supplier batch and expiry date. Expiry is required for products with a shelf life.',
    en_butcheryNote: 'Input = cuts + by-products + waste (±10 g). "Check" calculates the allocation without saving anything.',
    en_receiptSaved: '{0} saved. Total {1}.', en_butcherySaved: '{0} saved. Total cost {1}.',
    en_dryRunOk: 'Check passed, not saved yet. Total cost {0}.',
    en_imbalance: 'The weights do not balance: {0} unrecorded. Nothing was saved.',
    en_addLine: '+ Add line', en_addOutput: '+ Add output', en_removeLine: 'Remove line', en_close: 'Close',
    en_saveReceipt: 'Save receipt', en_saveButchery: 'Save butchery', en_check: 'Check',
    en_opNo: 'Operation no.', en_operator: 'Operator', en_additionalCost: 'Additional cost (€)', en_additionalCostNote: 'Additional cost document',
    en_kind: 'Type', en_kind_CUT: 'Cut', en_kind_BY_PRODUCT: 'By-product', en_kind_WASTE: 'Waste',
    en_outputLot: 'Output lot', en_noLotForWaste: 'Waste gets no lot', en_outputsTotal: 'Outputs',
    en_expected: 'Expected (AI)', en_samePart: 'counted with the row above', en_partsFromForecast: 'Add parts from the forecast',
    en_expectedNote: 'Expected: AI forecast with its {0} % interval (trained on synthetic history). Enter the weights from the scale; the forecast never fills them. By-products are outside the model. A saved operation stores forecast and actual together.',
    en_fcTitle: 'AI forecast and actual', en_fcFooter: 'Model interval {0} %. Anomaly: the actual is outside the 98 % band. Synthetic data; field performance is not proven.',
    en_freeze: 'Freeze forecast',
    en_freezeHint: 'Freeze the forecast before weighing the parts: field accuracy can only be measured with a forecast frozen beforehand. A frozen forecast cannot be changed and is made once per carcass.',
    en_freezeWeighed: 'Weights are entered: a forecast frozen now would not precede the weighing, so freezing is off.',
    en_frozenAt: 'Forecast frozen {0} for {1} by {2}. The saved operation is compared with it.',
    en_frozenLinked: "This carcass's frozen forecast is linked to operation {0}.",
    en_frozenOtherKg: 'The frozen forecast was for {0}, the input is {1}. The operation is still linked to it; the field audit does not score a forecast made for another weight.',
    en_fcFrozenTitle: 'Frozen AI forecast ({0}) and actual',
    en_fcNotFrozen: 'This forecast was computed with the record, not frozen before the weighing; it is no evidence of field accuracy.',
  },
};

const MOVEMENT_TYPES = {
  tr: {
    PURCHASE_IN: 'Alış girişi', TRANSFER_OUT: 'Transfer çıkışı', TRANSFER_IN: 'Transfer girişi', PRODUCTION_CONSUME: 'Üretimde tüketim',
    PRODUCTION_RECEIPT: 'Mamul kabulü', BUTCHERY_CONSUME: 'Parçalamaya giriş', BUTCHERY_OUTPUT: 'Parçalama çıktısı', SHIPMENT: 'Sevkiyat',
    SALE: 'Satış', RETURN: 'İade', REWORK_CONSUME: 'Yeniden işleme girişi', REWORK_OUTPUT: 'Yeniden işleme çıktısı', WASTE: 'Fire',
    ADJUSTMENT_IN: 'Sayım fazlası', ADJUSTMENT_OUT: 'Sayım eksiği',
  },
  en: {
    PURCHASE_IN: 'Purchase receipt', TRANSFER_OUT: 'Transfer out', TRANSFER_IN: 'Transfer in', PRODUCTION_CONSUME: 'Used in production',
    PRODUCTION_RECEIPT: 'Production receipt', BUTCHERY_CONSUME: 'Into butchery', BUTCHERY_OUTPUT: 'Butchery output', SHIPMENT: 'Shipment',
    SALE: 'Sale', RETURN: 'Return', REWORK_CONSUME: 'Into rework', REWORK_OUTPUT: 'Rework output', WASTE: 'Waste',
    ADJUSTMENT_IN: 'Count surplus', ADJUSTMENT_OUT: 'Count shortage',
  },
};

const LangContext = createContext(null);

function readLang() {
  try {
    const saved = localStorage.getItem('ban-lang');
    if (saved === 'tr' || saved === 'en') return saved;
  } catch {
    /* storage unavailable */
  }
  return navigator.language?.startsWith('tr') ? 'tr' : 'en';
}

export function LangProvider({ children }) {
  const [lang, setLangState] = useState(readLang);
  setApiLanguage(lang);
  const value = useMemo(() => {
    const setLang = (l) => {
      try {
        localStorage.setItem('ban-lang', l);
      } catch {
        /* ignore */
      }
      setApiLanguage(l);
      setLangState(l);
    };
    const t = (key, ...args) => {
      const s = DICT[lang][key] ?? DICT.en[key] ?? key;
      return args.reduce((acc, a, i) => acc.replace(`{${i}}`, a), s);
    };
    return { lang, setLang, t, fmt: formatters(lang), movementType: (m) => MOVEMENT_TYPES[lang][m] ?? m };
  }, [lang]);
  return <LangContext.Provider value={value}>{children}</LangContext.Provider>;
}

export function useLang() {
  return useContext(LangContext);
}

function formatters(lang) {
  const locale = lang === 'tr' ? 'tr-TR' : 'en-GB';
  const num = (v, d = 2) =>
    v == null || Number.isNaN(Number(v))
      ? '–'
      : new Intl.NumberFormat(locale, { minimumFractionDigits: d, maximumFractionDigits: d }).format(Number(v));
  const unit = (uom, q) => {
    const u = (uom || '').toUpperCase();
    if (u === 'KG') return 'kg';
    if (u === 'PAKET') return lang === 'tr' ? 'paket' : Number(q) === 1 ? 'pack' : 'packs';
    if (u === 'ADET') return lang === 'tr' ? 'adet' : 'pcs';
    return (uom || '').toLowerCase();
  };
  return {
    num,
    unit,
    money: (v) => (v == null ? '–' : lang === 'tr' ? `${num(v)} €` : `€${num(v)}`),
    qty: (v, uom) => (v == null ? '–' : `${num(v, (uom || '').toUpperCase() === 'KG' ? 2 : 0)} ${unit(uom, v)}`),
    pct: (v, d = 2) => {
      if (v == null) return '–';
      if (lang !== 'tr') return `${num(v, d)}%`;
      return Number(v) < 0 ? `-%${num(Math.abs(v), d)}` : `%${num(v, d)}`;
    },
    date: (v) => {
      if (!v) return '–';
      const dt = new Date(v.length === 10 ? `${v}T00:00:00` : v);
      if (Number.isNaN(dt.getTime())) return v;
      return lang === 'tr'
        ? dt.toLocaleDateString('tr-TR', { day: '2-digit', month: '2-digit', year: 'numeric' })
        : dt.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
    },
    dateTime: (v) => {
      if (!v) return '–';
      const dt = new Date(v);
      if (Number.isNaN(dt.getTime())) return v;
      return dt.toLocaleString(locale, { day: '2-digit', month: lang === 'tr' ? '2-digit' : 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' });
    },
  };
}
