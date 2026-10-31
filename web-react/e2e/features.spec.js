import { expect, test } from '@playwright/test';
import fs from 'node:fs/promises';
import path from 'node:path';

const api = process.env.E2E_API_BASE || 'http://127.0.0.1:8080';

test('downloads one Turkish PDF with the live assessment evidence', async ({ page }) => {
  await page.goto('/');
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Değerlendirme raporu (PDF)' }).click();
  const file = await download;
  expect(file.suggestedFilename()).toMatch(/^BAN-assessment-ET-2026-001-.*\.pdf$/);
  await fs.mkdir('test-results', { recursive: true });
  await file.saveAs(path.resolve('test-results/assessment-tr.pdf'));
  const bytes = await fs.readFile('test-results/assessment-tr.pdf');
  expect(bytes.subarray(0, 5).toString()).toBe('%PDF-');
  expect(bytes.length).toBeGreaterThan(20_000);
});

test('report failures stay visible and do not download incomplete evidence', async ({ page }) => {
  await page.route('**/reports/assessment', (route) => route.fulfill({ status: 404, contentType: 'application/json',
    body: JSON.stringify({ code: 'NOT_FOUND', message: 'Scenario record missing' }) }));
  let downloads = 0;
  page.on('download', () => downloads++);
  await page.goto('/');
  await page.getByRole('button', { name: 'Değerlendirme raporu (PDF)' }).click();
  await expect(page.getByText('Scenario record missing')).toBeVisible();
  expect(downloads).toBe(0);
  await expect(page.getByRole('button', { name: 'Değerlendirme raporu (PDF)' })).toBeEnabled();
});

test('records contact acknowledgement and physical recovery separately', async ({ page, request }) => {
  const auth = await request.post(`${api}/auth/session`, { data: { pin: '246813' } });
  expect(auth.ok()).toBeTruthy();
  const { token } = await auth.json();
  const code = `RCL-E2E-${Date.now()}`;
  const recall = await request.post(`${api}/recall`, { headers: { Authorization: `Bearer ${token}` },
    data: { recall_code: code, trigger_lot: 'ET-2026-001', reason: 'Browser test' } });
  expect(recall.status()).toBe(201);
  const before = await (await request.get(`${api}/recall/progress?recall_code=${code}`)).json();
  const a = before.lines.find((l) => l.customer_code === 'MKT-A');
  expect(a.qty_pending).toBeGreaterThanOrEqual(2);
  await page.goto('/#trace');
  const panel = page.locator('.recall').filter({ hasText: code });
  await panel.locator('summary').click();
  const row = panel.getByRole('row').filter({ hasText: 'MKT-A' }).filter({ has: page.getByRole('button', { name: 'Takibi güncelle' }) });
  await row.getByRole('button', { name: 'Takibi güncelle' }).click();
  await panel.getByRole('combobox', { name: 'İletişim', exact: true }).selectOption('CONFIRMED');
  await panel.getByLabel('Görüşme notu / teslim tutanağı').fill('Müşteri rafı boşalttığını teyit etti.');
  await panel.getByLabel('Yetkili PIN').fill('246813');
  await panel.getByRole('button', { name: 'Kaydet', exact: true }).click();
  await expect(panel.getByText('Takip kaydedildi.')).toBeVisible();
  const contacted = await (await request.get(`${api}/recall/progress?recall_code=${code}`)).json();
  expect(contacted.lines.find((l) => l.customer_code === 'MKT-A').qty_returned).toBe(a.qty_returned);
  await row.getByRole('button', { name: 'İade alındı kaydet' }).click();
  await panel.getByLabel('Miktar (PAKET)').fill('2');
  await panel.getByLabel('Yetkili PIN').fill('246813');
  await panel.getByRole('button', { name: 'Kaydet', exact: true }).click();
  await expect(panel.getByText('Takip kaydedildi.')).toBeVisible();
  const recovered = await (await request.get(`${api}/recall/progress?recall_code=${code}`)).json();
  expect(recovered.lines.find((l) => l.customer_code === 'MKT-A').qty_pending).toBe(a.qty_pending - 2);
  expect(recovered.totals[0].qty_to_recover).toBe(85); // 30/25/20 + recorded tier-2 10, not 95
  await page.screenshot({ path: 'test-results/recall-progress.png', fullPage: true });
});

test('records a shipment and a customer onward delivery that reaches the trace', async ({ page, request }) => {
  const lots = await (await request.get(`${api}/lots`)).json();
  const lot = lots.find((l) => l.warehouse_code === 'ANA-DEPO' && l.uom_code === 'PAKET' && l.status === 'ACTIVE' && l.qty >= 2);
  expect(lot, 'a sellable pack lot in ANA-DEPO').toBeTruthy();
  const stock = async () => (await (await request.get(`${api}/lots`)).json())
    .find((l) => l.lot_no === lot.lot_no && l.warehouse_code === 'ANA-DEPO')?.qty ?? 0;
  const count = async () => (await (await request.get(`${api}/shipments`)).json()).length;
  const run = Date.now() % 1_000_000;

  await page.goto('/#shipments');
  await page.getByRole('button', { name: 'Yeni sevkiyat' }).click();
  await page.getByLabel('Sevkiyat no').fill(`SVK-E2E-${run}`);
  await page.getByRole('combobox', { name: 'Alıcı' }).selectOption('MKT-B');
  await page.getByRole('combobox', { name: 'Lot', exact: true }).fill(lot.lot_no);
  await page.getByRole('textbox', { name: 'Miktar', exact: true }).fill('1');
  await page.getByLabel('Yetkili PIN').fill('246813');
  await page.getByRole('button', { name: 'Sevkiyatı kaydet' }).click();
  await expect(page.getByText(`SVK-E2E-${run} kaydedildi (Market B).`)).toBeVisible();
  expect(await stock()).toBe(lot.qty - 1);
  await page.getByRole('button', { name: 'Kapat' }).click();

  // Choosing the source must not submit the form, even with the PIN already typed.
  const before = await count();
  await page.getByRole('button', { name: 'Yeni sevkiyat' }).click();
  await page.getByLabel('Yetkili PIN').fill('246813');
  await page.getByRole('tab', { name: 'Müşterinin ileri sevki (2. kademe)' }).click();
  expect(await count()).toBe(before);
  await page.getByLabel('Sevkiyat no').fill(`MKB-E2E-${run}`);
  await page.getByRole('combobox', { name: 'İleri sevk eden müşteri' }).selectOption('MKT-B');
  await page.getByRole('combobox', { name: 'Alıcı' }).selectOption('REST-01');
  await page.getByRole('combobox', { name: 'Lot', exact: true }).fill(lot.lot_no);
  await page.getByRole('textbox', { name: 'Miktar', exact: true }).fill('1');
  await page.getByRole('button', { name: 'Sevkiyatı kaydet' }).click();
  await expect(page.getByText(`MKB-E2E-${run} kaydedildi (Örnek Lokanta).`)).toBeVisible();
  expect(await stock()).toBe(lot.qty - 1); // an onward delivery moves none of our stock
  await page.screenshot({ path: 'test-results/shipments.png', fullPage: true });

  const trace = await (await request.get(`${api}/trace/${lot.lot_no}?direction=forward`)).json();
  expect(trace.shipments).toContainEqual(expect.objectContaining({
    customer_code: 'REST-01', tier: 2, received_via: 'Market B', shipment_no: `MKB-E2E-${run}`, qty: 1,
  }));
});

test('butchery form shows the AI expectation per part and the API verdict after a check', async ({ page, request }) => {
  const run = Date.now() % 1_000_000;
  const day = (n) => new Date(Date.now() + n * 86_400_000).toISOString().slice(0, 10);
  const auth = await request.post(`${api}/auth/session`, { data: { pin: '246813' } });
  const { token } = await auth.json();
  const lot = `KUZU-E2E-${run}`;
  const receipt = await request.post(`${api}/purchases`, { headers: { Authorization: `Bearer ${token}` }, data: {
    receipt_no: `ALS-E2E-${run}`, supplier_code: 'TED-HAY-01', receipt_date: day(0),
    lines: [{ sku: 'KZ-KARKAS', lot_no: lot, supplier_lot_no: `KUPE-${run}`, qty: 24, unit_price: 11.8, expiry_date: day(5) }],
  } });
  expect(receipt.status()).toBe(201);
  const fc = await (await request.get(`${api}/ai/forecast/butchery?input_lot_no=${lot}&input_weight_kg=24&op_date=${day(0)}&operator_name=`)).json();
  expect(fc.parts).toHaveLength(6);

  await page.goto('/#butchery');
  await page.getByRole('button', { name: 'Parçalama kaydet' }).click();
  await page.getByLabel('İşlem no').fill(`PRC-E2E-${run}`);
  await page.getByRole('combobox', { name: 'Girdi lotu' }).fill(lot);
  await page.getByLabel('Giriş (kg)').fill('24');
  // the products come from the forecast, the weights never do
  await page.getByRole('button', { name: 'Parçaları tahminden ekle' }).click();
  const weights = page.getByRole('textbox', { name: 'Miktar', exact: true });
  await expect(weights).toHaveCount(6);
  for (let i = 0; i < 6; i++) await expect(weights.nth(i)).toHaveValue('');
  const table = page.locator('table.entry-lines');
  await expect(table.getByRole('columnheader', { name: 'Beklenen (YZ)' })).toBeVisible();
  const leg = fc.parts.find((p) => p.part === 'but');
  await expect(table.locator('tbody tr').first() // the leg row, e.g. "6,24 kg"
    .getByText(`${leg.kg.toFixed(2).replace('.', ',')} kg`, { exact: true })).toBeVisible();

  // weigh: the forecast itself, with the rounding remainder on the waste so the balance holds
  const kg = fc.parts.map((p) => p.kg);
  kg[5] = Math.round((24 - kg.slice(0, 5).reduce((a, v) => a + v, 0)) * 1000) / 1000;
  const lotFor = { but: 'KZB', sirt: 'KZS', omuz: 'KZO', kaburga: 'KZK', kiyma: 'KZY' };
  const lots = page.getByRole('textbox', { name: 'Çıktı lotu' });
  for (let i = 0; i < 6; i++) {
    await weights.nth(i).fill(String(kg[i]).replace('.', ','));
    if (i < 5) await lots.nth(i).fill(`${lotFor[fc.parts[i].part]}-E2E-${run}`);
  }
  await expect(table.getByText(/· Aralıkta$/).first()).toBeVisible();
  await page.getByLabel('Yetkili PIN').fill('246813');
  await page.getByRole('button', { name: 'Kontrol et' }).click();
  await expect(page.getByText('YZ tahmini ve gerçekleşen')).toBeVisible();
  const verdict = page.locator('table').filter({ has: page.getByRole('columnheader', { name: 'Referans (geçmiş ortalama)' }) });
  await expect(verdict.getByRole('row')).toHaveCount(7);
  await expect(verdict.getByText('Aralıkta')).toHaveCount(6);
  await page.screenshot({ path: 'test-results/butchery-forecast.png', fullPage: true });
});

test('a forecast frozen before weighing is the one the saved butchery is compared with', async ({ page, request }) => {
  const run = Date.now() % 1_000_000;
  const day = (n) => new Date(Date.now() + n * 86_400_000).toISOString().slice(0, 10);
  const { token } = await (await request.post(`${api}/auth/session`, { data: { pin: '246813' } })).json();
  const lot = `KUZU-FZ-${run}`;
  expect((await request.post(`${api}/purchases`, { headers: { Authorization: `Bearer ${token}` }, data: {
    receipt_no: `ALS-FZ-${run}`, supplier_code: 'TED-HAY-01', receipt_date: day(0),
    lines: [{ sku: 'KZ-KARKAS', lot_no: lot, supplier_lot_no: `KUPE-FZ-${run}`, qty: 24, unit_price: 11.8, expiry_date: day(5) }],
  } })).status()).toBe(201);

  await page.goto('/#butchery');
  await page.getByRole('button', { name: 'Parçalama kaydet' }).click();
  await page.getByLabel('İşlem no').fill(`PRC-FZ-${run}`);
  await page.getByRole('combobox', { name: 'Girdi lotu' }).fill(lot);
  await page.getByLabel('Giriş (kg)').fill('24');
  await page.getByRole('button', { name: 'Parçaları tahminden ekle' }).click();
  await page.getByLabel('Yetkili PIN').fill('246813');
  const freeze = page.getByRole('button', { name: 'Tahmini dondur' });
  await expect(freeze).toBeEnabled();
  // once a weight is entered the forecast would no longer precede the weighing
  const weights = page.getByRole('textbox', { name: 'Miktar', exact: true });
  await weights.first().fill('6');
  await expect(freeze).toBeDisabled();
  await expect(page.getByText('Ağırlık girildi: bundan sonra dondurulan tahmin')).toBeVisible();
  await weights.first().fill('');
  await freeze.click();
  await expect(page.getByText(/^Tahmin donduruldu: .*24,00 kg için/)).toBeVisible();
  await expect(freeze).toHaveCount(0);

  const fc = await (await request.get(`${api}/ai/forecast/butchery?input_lot_no=${lot}&input_weight_kg=24&op_date=${day(0)}&operator_name=`)).json();
  expect(fc.frozen).toMatchObject({ input_weight_kg: 24, frozen_by: expect.any(String) });
  expect(fc.frozen.op_no).toBeUndefined();
  const kg = fc.frozen.forecast.parts.map((p) => p.kg);
  kg[5] = Math.round((24 - kg.slice(0, 5).reduce((a, v) => a + v, 0)) * 1000) / 1000;
  const lotFor = { but: 'FZB', sirt: 'FZS', omuz: 'FZO', kaburga: 'FZK', kiyma: 'FZY' };
  const lots = page.getByRole('textbox', { name: 'Çıktı lotu' });
  for (let i = 0; i < 6; i++) {
    await weights.nth(i).fill(String(kg[i]).replace('.', ','));
    if (i < 5) await lots.nth(i).fill(`${lotFor[fc.frozen.forecast.parts[i].part]}-${run}`);
  }
  await page.getByRole('button', { name: 'Parçalamayı kaydet' }).click();
  await expect(page.getByText(/^Dondurulmuş YZ tahmini \(.+\) ve gerçekleşen$/)).toBeVisible();
  const after = await (await request.get(`${api}/ai/forecast/butchery?input_lot_no=${lot}&input_weight_kg=24`)).json();
  expect(after.frozen.op_no).toBe(`PRC-FZ-${run}`);
  await page.screenshot({ path: 'test-results/butchery-frozen.png', fullPage: true });
});

test('lot card opens from any lot number, walks the chain and hands over to the trace', async ({ page }) => {
  await page.goto('/#lots');
  await expect(page.getByRole('heading', { name: 'Lotlar ve stok' })).toBeVisible();
  await page.getByRole('button', { name: 'SUC-2026-046', exact: true }).first().click();
  const card = page.getByRole('dialog');
  await expect(card.getByRole('heading', { name: 'SUC-2026-046' })).toBeVisible();
  await expect(card.getByText('Nereden yapıldı')).toBeVisible();
  await expect(card.getByText('Market B').first()).toBeVisible();
  await expect(card.getByRole('button', { name: 'Kapat' })).toBeFocused();
  await page.screenshot({ path: 'test-results/lot-card.png' });

  // a source lot inside the card opens that lot's card
  await card.getByRole('button', { name: 'ET-2026-002', exact: true }).first().click();
  await expect(card.getByRole('heading', { name: 'ET-2026-002' })).toBeVisible();
  await expect(card.getByText('Örnek Et Tedarik A.Ş.')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(card).toBeHidden();

  await page.getByRole('button', { name: 'SUC-2026-046', exact: true }).first().click();
  await card.getByRole('button', { name: 'İzleme ve geri çağırmada aç' }).click();
  await expect(page).toHaveURL(/#trace\?lot=SUC-2026-046$/);
  await expect(page.getByLabel('Lot numarası')).toHaveValue('SUC-2026-046');
});

test('records a transfer from the movements tab and refuses half a pack', async ({ page, request }) => {
  const stockIn = async (lot, wh) => (await (await request.get(`${api}/lots`)).json())
    .find((l) => l.lot_no === lot && l.warehouse_code === wh)?.qty ?? 0;
  const lots = await (await request.get(`${api}/lots`)).json();
  const lot = lots.find((l) => l.warehouse_code === 'ANA-DEPO' && l.uom_code === 'PAKET' && l.status === 'ACTIVE' && l.qty >= 2);
  expect(lot).toBeTruthy();
  const before = await stockIn(lot.lot_no, 'MAGAZA-01');

  await page.goto('/#movements');
  await page.getByRole('button', { name: 'Hareket kaydet' }).click();
  await page.getByRole('combobox', { name: 'Lot', exact: true }).fill(lot.lot_no);
  await page.getByRole('combobox', { name: 'Çıkış deposu' }).selectOption('ANA-DEPO');
  await page.getByRole('combobox', { name: 'Giriş deposu' }).selectOption('MAGAZA-01');
  await page.getByLabel('Miktar (paket)').fill('1,5');
  await page.getByLabel('Yetkili PIN').fill('246813');
  await page.getByRole('button', { name: 'Hareketi kaydet' }).click();
  await expect(page.getByText('İstek geçersiz.')).toBeVisible(); // the API refuses half a pack
  expect(await stockIn(lot.lot_no, 'MAGAZA-01')).toBe(before);

  await page.getByLabel('Miktar (paket)').fill('1');
  await page.getByRole('button', { name: 'Hareketi kaydet' }).click();
  await expect(page.getByText('Transfer girişi kaydedildi:')).toBeVisible();
  expect(await stockIn(lot.lot_no, 'MAGAZA-01')).toBe(before + 1);

  // waste needs a reason before it can be saved
  await page.getByRole('button', { name: 'Yeni hareket' }).click();
  await page.getByRole('tab', { name: 'Fire / imha' }).click();
  await page.getByRole('combobox', { name: 'Lot', exact: true }).fill(lot.lot_no);
  await page.getByLabel('Miktar (paket)').fill('1');
  await page.getByLabel('Yetkili PIN').fill('246813');
  await expect(page.getByRole('button', { name: 'Hareketi kaydet' })).toBeDisabled();
  await page.getByLabel('Fire nedeni').fill('Ambalaj yırtık');
  await expect(page.getByRole('button', { name: 'Hareketi kaydet' })).toBeEnabled();
  await page.screenshot({ path: 'test-results/movement-form.png', fullPage: true });
});

test('production order preview shows the AI forecast and writes nothing until saved', async ({ page, request }) => {
  const run = Date.now() % 1_000_000;
  const day = (n) => new Date(Date.now() + n * 86_400_000).toISOString().slice(0, 10);
  const auth = await request.post(`${api}/auth/session`, { data: { pin: '246813' } });
  const { token } = await auth.json();
  const lots = [
    ['TED-ET-01', 'HM-ET-DANA', `ET-E2E-${run}`, 100, 9.7, day(10)],
    ['TED-BAH-01', 'HM-BAHARAT', `BAH-E2E-${run}`, 2, 24, day(300)],
    ['TED-BAG-01', 'HM-BAGIRSAK', `BAG-E2E-${run}`, 5, 11.6, day(150)],
  ];
  for (const [supplier, sku, lot, qty, price, expiry] of lots) {
    const receipt = await request.post(`${api}/purchases`, { headers: { Authorization: `Bearer ${token}` }, data: {
      receipt_no: `ALS-${lot}`, supplier_code: supplier, receipt_date: day(0),
      lines: [{ sku, lot_no: lot, supplier_lot_no: `S-${lot}`, qty, unit_price: price, expiry_date: expiry }],
    } });
    expect(receipt.status()).toBe(201);
  }
  const orderNo = `URT-E2E-${run}`;

  await page.goto('/#production');
  await page.getByRole('button', { name: 'Yeni üretim emri' }).click();
  await page.getByLabel('Emir no').fill(orderNo);
  await page.getByRole('button', { name: '+ Satır ekle' }).click();
  await page.getByRole('button', { name: '+ Satır ekle' }).click();
  const lotCells = page.getByRole('combobox', { name: 'Lot', exact: true });
  const qtyCells = page.getByRole('textbox', { name: 'Miktar', exact: true });
  for (const [i, [, , lot, qty]] of lots.entries()) {
    await lotCells.nth(i).fill(lot);
    await qtyCells.nth(i).fill(String(qty));
  }
  await page.getByLabel('Yetkili PIN').fill('246813');
  await page.getByRole('button', { name: 'Önizle' }).click();
  const preview = page.getByRole('status').filter({ hasText: 'henüz kaydedilmedi' });
  await expect(preview).toBeVisible();
  const packs = preview.locator('.kpi').filter({ hasText: 'Tahmini paket (YZ)' }).locator('.kpi-value');
  await expect(packs).toHaveText(/^\d+,\d$/);
  const expected = await packs.textContent();
  await page.screenshot({ path: 'test-results/production-preview.png', fullPage: true });
  // the preview wrote nothing: no order, the meat is still in the cold store
  expect(await (await request.get(`${api}/production?order_no=${orderNo}`)).json()).toHaveLength(0);
  const stock = await (await request.get(`${api}/lots`)).json();
  expect(stock.filter((l) => l.lot_no === lots[0][2]).map((l) => [l.warehouse_code, l.qty])).toEqual([['ANA-DEPO', 100]]);

  // any change makes the preview stale; saving stores the same forecast
  await qtyCells.nth(2).fill('5,0');
  await expect(preview).toBeHidden();
  await page.getByRole('button', { name: 'Emri kaydet ve hammaddeyi sevk et' }).click();
  const saved = page.locator('.group-wrap').filter({ hasText: `${orderNo} kaydedildi` });
  await expect(saved.locator('.kpi').filter({ hasText: 'Tahmini paket (YZ)' }).locator('.kpi-value')).toHaveText(expected);
});

test('AI tab keeps the synthetic test apart from the records and labels their origin', async ({ page }) => {
  await page.goto('/#ai');
  await expect(page.getByText('Sentetik test: MAE / MAPE')).toBeVisible();
  const records = page.locator('.group-wrap').filter({ hasText: 'Bu veritabanındaki kayıtlar' });
  await expect(records.getByRole('row').filter({ hasText: 'URT-2026-045' }).first().getByText('Seed örneği')).toBeVisible();
  await expect(records.locator('.kpi').filter({ hasText: 'Seed örneği' }).locator('.kpi-value')).toHaveText('3');
  await expect(records.locator('.kpi').filter({ hasText: 'Doğrulanmış saha örneği' }).locator('.kpi-value')).toHaveText('0');
  await page.screenshot({ path: 'test-results/ai-origins.png', fullPage: true });

  await page.goto('/');
  const box = page.locator('.group-wrap').filter({ hasText: 'Yapay zekâ (sentetik test)' });
  await expect(box.getByText('saha başarımı ölçülmedi')).toBeVisible();
});

test('a purchase above recent prices raises an alert that a supervisor reviews and closes', async ({ page, request }) => {
  const auth = await request.post(`${api}/auth/session`, { data: { pin: '246813' } });
  expect(auth.ok()).toBeTruthy();
  const { token } = await auth.json();
  const run = Date.now();
  const day = new Date().toISOString().slice(0, 10);
  const expiry = new Date(Date.now() + 30 * 864e5).toISOString().slice(0, 10);
  const receipt = `ALS-E2E-AL-${run}`;
  const bought = await request.post(`${api}/purchases`, { headers: { Authorization: `Bearer ${token}` }, data: {
    receipt_no: receipt, supplier_code: 'TED-BAH-01', receipt_date: day,
    lines: [{ sku: 'HM-BAHARAT', lot_no: `BAH-E2E-${run}`, qty: 5, unit_price: 40, expiry_date: expiry }] } });
  expect(bought.status()).toBe(201); // spice was bought at 24.00 €/kg until now

  await page.goto('/#overview');
  await page.getByRole('button', { name: /^Bildirimler/ }).click();
  await page.getByRole('menuitem', { name: /Açık uyarı/ }).click();
  await expect(page).toHaveURL(/#alerts$/);
  const row = page.getByRole('row').filter({ hasText: receipt });
  await expect(row).toContainText('Alış fiyatı artışı');
  await expect(row).toContainText('Kritik');
  await expect(row).toContainText('40,00 €/kg');
  await page.screenshot({ path: 'test-results/alerts-open.png', fullPage: true });

  await row.getByRole('button', { name: 'İncele' }).click();
  await page.getByRole('combobox', { name: 'Karar' }).selectOption('RESOLVED');
  await page.getByLabel('Yetkili PIN').fill('246813');
  await expect(page.getByRole('button', { name: 'Kaydet', exact: true })).toBeDisabled(); // closing needs a note
  await page.getByLabel('Not (kapatmak için zorunlu)').fill('Tedarikçi yeni fiyat listesi gönderdi.');
  await page.getByRole('button', { name: 'Kaydet', exact: true }).click();
  await expect(page.getByText('Uyarı güncellendi.')).toBeVisible();
  await expect(page.getByRole('row').filter({ hasText: receipt })).toHaveCount(0);

  await page.getByRole('tab', { name: 'Kapatılan' }).click();
  const closed = page.getByRole('row').filter({ hasText: receipt });
  await expect(closed).toContainText('Gerçek sorun · kapatıldı');
  await expect(closed).toContainText('Tedarikçi yeni fiyat listesi gönderdi.');
  const stock = (await (await request.get(`${api}/lots`)).json()).find((l) => l.lot_no === `BAH-E2E-${run}`);
  expect(stock.qty).toBe(5); // an alert never moves stock
});

test('trace shows the lot flow and a mass balance that documents every quantity', async ({ page }) => {
  await page.goto('/#trace?lot=ET-2026-001');
  const flow = page.getByRole('group', { name: 'ET-2026-001 lotunun akış diyagramı' });
  await expect(flow).toBeVisible();
  await expect(flow).toContainText('100,00 kg → 85 paket');
  for (const market of ['Market A', 'Market B', 'Market C']) await expect(flow).toContainText(market);
  const balance = page.locator('.group-wrap').filter({ has: page.getByText('Kütle dengesi', { exact: true }) });
  await expect(balance.getByText('Her miktar belgeli')).toBeVisible();
  const meat = balance.getByRole('row').filter({ hasText: 'ET-2026-001' });
  await expect(meat.getByRole('cell').nth(1)).toHaveText('100,00 kg'); // lot quantity
  await expect(meat.getByRole('cell').nth(2)).toHaveText('100,00 kg'); // all of it went into the sausage order
  await page.screenshot({ path: 'test-results/trace-flow.png', fullPage: true });

  await flow.getByRole('button', { name: /^SUC-2026-045/ }).click(); // a lot box opens the lot card
  await expect(page.getByRole('dialog')).toContainText('SUC-2026-045');
});

test('a stock count books a difference only with its reason and lists it in the history', async ({ page, request }) => {
  const stockOf = async (lotNo) => (await (await request.get(`${api}/lots`)).json())
    .find((l) => l.lot_no === lotNo && l.warehouse_code === 'ANA-DEPO')?.qty;
  const lot = (await (await request.get(`${api}/lots`)).json())
    .find((l) => l.warehouse_code === 'ANA-DEPO' && l.uom_code === 'PAKET' && l.qty >= 3);
  expect(lot).toBeTruthy();

  await page.goto('/#lots');
  await page.getByRole('button', { name: 'Stok sayımı yap' }).click();
  await page.getByLabel(`${lot.lot_no} sayılan miktar`).fill(String(lot.qty - 1));
  await expect(page.getByText('Fark için gerekçe yazın.')).toBeVisible();
  await page.getByLabel('Yetkili PIN').fill('246813');
  await expect(page.getByRole('button', { name: 'Sayımı kaydet' })).toBeDisabled();
  await page.getByLabel(`${lot.lot_no} fark gerekçesi`).fill('Ambalaj yırtık bulundu');
  await page.screenshot({ path: 'test-results/stock-count.png', fullPage: true });
  await page.getByRole('button', { name: 'Sayımı kaydet' }).click();
  await expect(page.getByText(/kaydedildi: 1 lot/)).toBeVisible();
  expect(await stockOf(lot.lot_no)).toBe(lot.qty - 1);
  await expect(page.getByRole('row').filter({ hasText: 'Ambalaj yırtık bulundu' })).toBeVisible();
});

test('thresholds show their synthetic test and a missed problem is recorded for recall', async ({ page, request }) => {
  const auth = await request.post(`${api}/auth/session`, { data: { pin: '246813' } });
  const { token } = await auth.json();
  await page.goto('/#alerts');
  const rules = page.locator('.group-wrap').filter({ has: page.getByText('Uyarı eşikleri', { exact: true }) });
  const butchery = rules.getByRole('row').filter({ hasText: 'Parçalama · YZ (B)' });
  await expect(butchery).toContainText('1,50 × %98 bant');
  await expect(butchery).toContainText('100 kayıtta');
  try {
    await butchery.getByRole('button', { name: 'Değiştir' }).click();
    await rules.getByLabel('Uyarı eşiği').fill('1,25');
    await expect(rules.getByText(/Sentetik testte 1,25 × %98 bant/)).toBeVisible();
    await rules.getByLabel('Yetkili PIN').fill('246813');
    await rules.getByRole('button', { name: 'Eşiği kaydet' }).click();
    await expect(rules.getByText(/Eşik güncellendi/)).toBeVisible();
    const after = await (await request.get(`${api}/alerts/rules`)).json();
    expect(after.rules.find((r) => r.rule === 'BUTCHERY_YIELD')).toMatchObject({ warn: 1.25, updated_by: expect.any(String) });
  } finally { // later runs grade with the default
    await request.post(`${api}/alerts/rules`, { headers: { Authorization: `Bearer ${token}` }, data: { rule: 'BUTCHERY_YIELD', warn: 1.5, crit: 2 } });
  }

  const note = `Fire tartımı elle kontrol edildi ${Date.now()}`;
  await page.getByRole('button', { name: 'Kaçırılan sorun kaydet' }).click();
  await page.getByRole('combobox', { name: 'Hangi kontrol yakalamalıydı?' }).selectOption('BUTCHERY_YIELD');
  await page.getByLabel('Ne bulundu?').fill(note);
  await page.getByLabel('Tahmini kayıp (€)').fill('8,5');
  await page.getByLabel('Yetkili PIN').fill('246813');
  await page.getByRole('button', { name: 'Kaydet', exact: true }).click();
  await expect(page.getByText('Kaçırılan sorun kaydedildi.')).toBeVisible();
  await expect(page.getByRole('row').filter({ hasText: note })).toContainText('8,50 €');
  await page.screenshot({ path: 'test-results/alert-accuracy.png', fullPage: true });
});
