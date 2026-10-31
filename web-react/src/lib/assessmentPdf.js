import { jsPDF } from 'jspdf';
import { autoTable } from 'jspdf-autotable';
import regularUrl from '../assets/fonts/NotoSans-Regular.ttf?url';
import boldUrl from '../assets/fonts/NotoSans-Bold.ttf?url';

let fonts;
async function fontData(url) {
  const response = await fetch(url);
  if (!response.ok) throw new Error('FONT_LOAD_FAILED');
  const bytes = new Uint8Array(await response.arrayBuffer());
  let binary = '';
  for (let i = 0; i < bytes.length; i += 32768) binary += String.fromCharCode(...bytes.subarray(i, i + 32768));
  return btoa(binary);
}

/** Returns a real downloadable PDF with embedded Turkish/Euro glyphs and selectable text. */
export async function createAssessmentPdf(report, { t, fmt }) {
  if (!fonts) fonts = Promise.all([fontData(regularUrl), fontData(boldUrl)]).catch((err) => { fonts = null; throw err; });
  const [regular, bold] = await fonts;
  const doc = new jsPDF({ format: 'a4', compress: true, putOnlyUsedFonts: true });
  doc.addFileToVFS('NotoSans-Regular.ttf', regular); doc.addFont('NotoSans-Regular.ttf', 'NotoSans', 'normal');
  doc.addFileToVFS('NotoSans-Bold.ttf', bold); doc.addFont('NotoSans-Bold.ttf', 'NotoSans', 'bold');
  doc.setProperties({ title: `BAN · ${t('report_title')}`, author: 'BAN Food Intelligence', subject: report.trace.root_lot_number });
  doc.setFont('NotoSans', 'normal');
  let y = 20;
  const page = () => { doc.addPage(); y = 20; };
  // The bundled Noto Sans subset has no U+2212 minus sign: print it as a hyphen-minus.
  const safe = (value) => String(value).replace(/−/g, '-');
  const text = (value, size = 10, weight = 'normal') => {
    doc.setFont('NotoSans', weight); doc.setFontSize(size); doc.setTextColor(30, 40, 50);
    const lines = doc.splitTextToSize(safe(value), 178);
    const step = size * 0.48;
    for (const line of lines) {
      if (y + step > 278) page();
      doc.text(line, 16, y); y += step;
    }
    y += 3;
  };
  const heading = (title) => { if (y > 245) page(); y += 4; text(title, 14, 'bold'); };
  const table = (head, body) => {
    if (!body.length) { text(t('noData')); return; }
    if (y > 253) page();
    autoTable(doc, { startY: y, margin: { left: 16, right: 16, top: 18, bottom: 20 }, head: [head.map(safe)], body: body.map((r) => r.map(safe)),
      styles: { font: 'NotoSans', fontSize: 8, cellPadding: 2.4, overflow: 'linebreak', textColor: [30, 40, 50] },
      headStyles: { fillColor: [35, 91, 74], textColor: [255, 255, 255], fontStyle: 'bold' }, alternateRowStyles: { fillColor: [245, 248, 246] },
      rowPageBreak: 'avoid' });
    y = doc.lastAutoTable.finalY + 7;
  };
  text('BAN Food Intelligence', 21, 'bold');
  text(t('report_title'), 14);
  text(`${t('report_snapshot')}: ${fmt.dateTime(report.generated_at)} · UTC ${report.generated_at}` , 8);
  text(`${t('report_root')}: ${report.trace.root_lot_number}`, 10, 'bold');
  text(t('report_limit'), 9);

  heading(t('report_production'));
  const p = report.production;
  text(`${p.order_no} · ${p.product_name}`);
  table([t('costElement'), t('amount')], [
    [t('materialCost'), fmt.money(p.material_cost)], [t('serviceCost'), fmt.money(p.service_cost)],
    [t('totalCost'), fmt.money(p.total_cost)], [t('actual'), fmt.qty(p.actual_qty, 'PAKET')],
    [t('ekp'), fmt.money(p.ekp_display)], [t('vkp'), fmt.money(p.vkp)], [t('margin'), fmt.pct(p.margin_pct)],
  ]);
  text(`${fmt.money(p.total_cost)} ÷ ${fmt.num(p.actual_qty, 0)} = ${fmt.money(p.ekp_display)}`, 12, 'bold');
  table([t('lot'), t('costElement'), t('qty'), t('amount')], (p.breakdown || []).map((l) =>
    [l.lot_no || '—', l.cost_element, fmt.qty(l.qty, l.uom_code), fmt.money(l.amount)]));

  heading(t('report_butchery'));
  for (const b of report.butchery) {
    text(`${b.op_no} · ${t(b.status === 'REJECTED' ? 'rejected' : 'posted')}`, 11, 'bold');
    for (const f of b.findings || []) text(f.message, 9);
    table([t('input'), t('cuts'), t('waste'), t('unrecorded'), t('totalCost')], [[
      fmt.qty(b.weight_check.input_kg, 'KG'), fmt.qty(b.weight_check.cuts_kg, 'KG'),
      fmt.qty(b.weight_check.waste_kg, 'KG'), fmt.qty(b.weight_check.unrecorded_kg, 'KG'), fmt.money(b.total_cost),
    ]]);
    table([t('product'), t('qty'), t('allocated'), t('unitCost')], (b.allocated_cuts || []).map((c) =>
      [c.name, fmt.qty(c.yield_weight_kg, 'KG'), fmt.money(c.allocated_cost), fmt.money(c.allocated_unit_cost_per_kg)]));
  }

  heading(t('report_trace'));
  const trace = report.trace;
  table([t('lot'), t('product'), t('status')], trace.lots.map((l) => [l.lot_no, l.product_name, l.status]));
  table([t('document'), t('inputLot'), t('consumed'), t('outputLot2'), t('produced')], trace.links.map((l) =>
    [l.document_no, l.input_lot_no, fmt.qty(l.qty_consumed, l.input_uom), l.output_lot_no, fmt.qty(l.qty_produced, l.output_uom)]));
  const mb = trace.mass_balance;
  if (mb) {
    text(`${t('mb_title')}: ${t(mb.balanced ? 'mb_balanced' : 'mb_gap')}`, 11, 'bold');
    const q = (v, uom) => (Math.abs(v) > 0.0005 ? fmt.qty(v, uom) : '–');
    table([t('lot'), t('mb_declared'), t('mb_used'), t('mb_shipped'), t('mb_sold'), t('mb_wasted'), t('mb_returned'), t('mb_inStock'), t('mb_unexplained')],
      mb.lines.map((l) => [l.lot_no, fmt.qty(l.declared_qty, l.uom_code), q(l.used_in_lots, l.uom_code), q(l.shipped, l.uom_code), q(l.sold, l.uom_code),
        q(l.wasted, l.uom_code), q(l.returned, l.uom_code), q(l.in_stock, l.uom_code), q(l.unexplained, l.uom_code)]));
    text(t('mb_note'), 8);
  }
  text(`${t('backward')}: ${report.backward.root_lot_number}`, 11, 'bold');
  table([t('lot'), t('product'), t('supplier')], report.backward.lots.map((l) =>
    [l.lot_no, l.product_name, [l.supplier_code, l.supplier_name].filter(Boolean).join(' · ') || '—']));
  table([t('tier'), t('customer'), t('lot'), t('qty'), t('contact')], (trace.shipments || []).map((s) =>
    [String(s.tier), `${s.customer_name}${s.received_via ? ` / ${s.received_via}` : ''}`, s.lot_no, fmt.qty(s.qty, s.uom_code),
      [s.contact_person, s.phone, s.email].filter(Boolean).join('\n')]));
  if (!report.recalls.length) text(t('report_noRecall'), 9);
  for (const rc of report.recalls) {
    text(`${t('recallTitle')}: ${rc.recall_code}`, 11, 'bold');
    text(t('rp_note'), 8);
    table([t('customer'), t('lot'), t('shipped'), t('rp_forwarded'), t('returned'), t('rp_pending'), t('rp_contact')], rc.lines.map((l) =>
      [l.customer_name, l.lot_no, fmt.qty(l.qty_received, l.uom_code), fmt.qty(l.qty_forwarded, l.uom_code),
        fmt.qty(l.qty_returned, l.uom_code), fmt.qty(l.qty_pending, l.uom_code), t(`rp_${l.contact_status}`)]));
  }

  heading(t('report_ai'));
  text(t('report_limit'), 9);
  table([t('type'), t('report_modelRole'), t('model'), t('mae'), t('mape'), t('rows')], report.metrics.map((m) =>
    [`${m.task} / ${m.target}`, m.role === 'BASELINE' ? t('baseline') : t('model'), m.model,
      `${fmt.num(m.mae, 3)} ${m.unit}`, fmt.pct(m.mape_pct), fmt.num(m.n_rows, 0)]));
  text(t('report_predictions'), 11, 'bold');
  table([t('reference'), t('origin'), t('type'), t('model'), t('qty'), t('actual')], report.predictions.map((m) =>
    [m.reference, m.origin ? t(`o_${m.origin}`) : '–', m.target, m.model, fmt.num(m.predicted, 3), fmt.num(m.actual, 3)]));
  const pages = doc.getNumberOfPages();
  for (let i = 1; i <= pages; i++) {
    doc.setPage(i); doc.setFont('NotoSans', 'normal'); doc.setFontSize(7); doc.setTextColor(90);
    doc.text(t('report_footer'), 16, 286);
    doc.text(`${t('report_page')} ${i} / ${pages}`, 194, 286, { align: 'right' });
  }
  return doc;
}
