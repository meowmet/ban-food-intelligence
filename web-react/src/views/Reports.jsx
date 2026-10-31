import React, { useState } from 'react';
import { useApi } from '../lib/api';
import { useLang } from '../lib/i18n';
import { Async, Group, Kpi, Mono, Page, Pill, Table } from '../lib/ui';
import { MovementForm, ReceiptForm, ShipmentForm } from './Entry';
import { LotLink } from './LotPanel';

export function Purchases() {
  const { t, fmt } = useLang();
  const result = useApi('/purchases');
  const [entering, setEntering] = useState(false);
  return (
    <Page
      title={t('pu_title')}
      subtitle={t('pu_sub')}
      actions={!entering && <button className="btn-primary" onClick={() => setEntering(true)}>{t('en_newReceipt')}</button>}
    >
      {entering && <ReceiptForm onDone={result.reload} onCancel={() => setEntering(false)} />}
      <Async result={result}>
        {(rows) => (
          <Group>
            <Table
              rows={rows}
              columns={[
                { key: 'receipt_no', label: t('receipt'), render: (r) => <><Mono>{r.receipt_no}</Mono><div className="cell-sub">{fmt.date(r.receipt_date)} · {r.invoice_no}</div></> },
                { key: 'supplier', label: t('supplier'), render: (r) => r.supplier_name },
                { key: 'lot_no', label: t('lot'), render: (r) => <><LotLink lot={r.lot_no} /><div className="cell-sub">{r.product_name}</div></> },
                { key: 'supplier_lot_no', label: t('supplierLot'), render: (r) => <Mono>{r.supplier_lot_no || '–'}</Mono> },
                { key: 'qty', label: t('qty'), align: 'num', render: (r) => fmt.qty(r.qty, r.uom_code) },
                { key: 'unit_price', label: t('unitPrice'), align: 'num', render: (r) => fmt.money(r.unit_price) },
                { key: 'line_total', label: t('amount'), align: 'num', render: (r) => <strong>{fmt.money(r.line_total)}</strong> },
                { key: 'expiry_date', label: t('expiry'), render: (r) => fmt.date(r.expiry_date) },
              ]}
              footer={
                <tr>
                  <td colSpan={6}>{t('total')}</td>
                  <td className="num"><strong>{fmt.money(rows.reduce((a, r) => a + r.line_total, 0))}</strong></td>
                  <td />
                </tr>
              }
            />
          </Group>
        )}
      </Async>
    </Page>
  );
}

export function Movements() {
  const { t, fmt, movementType } = useLang();
  const lots = useApi('/lots');
  const [lot, setLot] = useState('');
  const result = useApi(lot ? `/movements?lot_no=${encodeURIComponent(lot)}` : '/movements');
  const lotNos = [...new Set((lots.data || []).map((r) => r.lot_no))].sort();
  const [entering, setEntering] = useState(false);
  return (
    <Page
      title={t('mv_title')}
      subtitle={t('mv_sub')}
      actions={
        <>
          <label className="field">
            <span>{t('filterLot')}</span>
            <select value={lot} onChange={(e) => setLot(e.target.value)}>
              <option value="">{t('allLots')}</option>
              {lotNos.map((l) => (
                <option key={l} value={l}>{l}</option>
              ))}
            </select>
          </label>
          {!entering && <button className="btn-primary" onClick={() => setEntering(true)}>{t('mf_new')}</button>}
        </>
      }
    >
      {entering && <MovementForm onDone={() => { result.reload(); lots.reload(); }} onCancel={() => setEntering(false)} />}
      <Async result={result}>
        {(rows) => (
          <Group>
            <Table
              rows={rows}
              rowTone={(r) => (r.qty < 0 ? 'out' : 'in')}
              columns={[
                { key: 'moved_at', label: t('date'), render: (r) => fmt.dateTime(r.moved_at) },
                { key: 'lot_no', label: t('lot'), render: (r) => <><LotLink lot={r.lot_no} /><div className="cell-sub">{r.product_name}</div></> },
                { key: 'warehouse_code', label: t('warehouse'), render: (r) => <Mono>{r.warehouse_code}</Mono> },
                { key: 'movement_type', label: t('type'), render: (r) => <Pill tone={r.qty < 0 ? 'neutral' : 'ok'}>{movementType(r.movement_type)}</Pill> },
                { key: 'qty', label: t('qty'), align: 'num', render: (r) => <span className={r.qty < 0 ? 'neg' : 'pos'}>{r.qty > 0 ? '+' : ''}{fmt.qty(r.qty, r.uom_code)}</span> },
                { key: 'balance_after', label: t('balanceAfter'), align: 'num', render: (r) => <strong>{fmt.qty(r.balance_after, r.uom_code)}</strong> },
                { key: 'ref', label: t('reference'), render: (r) => <span className="cell-sub">{r.note || `${r.ref_type} ${r.ref_id || ''}`}</span> },
              ]}
            />
          </Group>
        )}
      </Async>
    </Page>
  );
}

export function Shipments() {
  const { t, fmt } = useLang();
  const result = useApi('/shipments');
  const [entering, setEntering] = useState(false);
  return (
    <Page
      title={t('sh_title')}
      subtitle={t('sh_sub')}
      actions={!entering && <button className="btn-primary" onClick={() => setEntering(true)}>{t('sh_new')}</button>}
    >
      {entering && <ShipmentForm onDone={result.reload} onCancel={() => setEntering(false)} />}
      <Async result={result}>
        {(list) => {
          const rows = list.flatMap((s) => s.lines.map((l) => ({ ...s, ...l })));
          return (
            <Group>
              <Table
                rows={rows}
                rowKey={(r) => `${r.shipment_no}:${r.lot_no}`}
                columns={[
                  { key: 'shipment_no', label: t('sh_no'), render: (r) => <><Mono>{r.shipment_no}</Mono><div className="cell-sub">{fmt.date(r.ship_date)}{r.delivery_note_no ? ` · ${r.delivery_note_no}` : ''}</div></> },
                  { key: 'tier', label: t('tier'), render: (r) => <Pill tone={r.tier === 1 ? 'neutral' : 'warn'}>{r.tier}</Pill> },
                  {
                    key: 'customer',
                    label: t('customer'),
                    render: (r) => <>{r.customer_name}<div className="cell-sub">{r.tier === 1 ? <Mono>{r.from_warehouse}</Mono> : `${t('via')}: ${r.from_customer_name}`}</div></>,
                  },
                  { key: 'lot_no', label: t('lot'), render: (r) => <><LotLink lot={r.lot_no} /><div className="cell-sub">{r.product_name}</div></> },
                  { key: 'qty', label: t('qty'), align: 'num', render: (r) => fmt.qty(r.qty, r.uom_code) },
                  { key: 'unit_price', label: t('unitPrice'), align: 'num', render: (r) => (r.unit_price == null ? '–' : fmt.money(r.unit_price)) },
                  { key: 'line_total', label: t('amount'), align: 'num', render: (r) => (r.line_total == null ? '–' : <strong>{fmt.money(r.line_total)}</strong>) },
                ]}
                footer={
                  <tr>
                    <td colSpan={6}>{t('sh_totalOurs')}</td>
                    <td className="num"><strong>{fmt.money(rows.reduce((a, r) => a + (r.tier === 1 ? r.line_total || 0 : 0), 0))}</strong></td>
                  </tr>
                }
              />
            </Group>
          );
        }}
      </Async>
    </Page>
  );
}

export function Sales() {
  const { t, fmt } = useLang();
  const result = useApi('/sales');
  return (
    <Page title={t('sa_title')} subtitle={t('sa_sub')}>
      <Async result={result}>
        {(rows) => {
          const revenue = rows.reduce((a, r) => a + r.line_total, 0);
          const cost = rows.reduce((a, r) => a + r.cost, 0);
          const gp = revenue - cost;
          return (
            <>
              <Group>
                <div className="kpi-row">
                  <Kpi label={t('ov_revenue')} value={fmt.money(revenue)} sub={t('ov_lines', rows.length)} />
                  <Kpi label={t('cost')} value={fmt.money(cost)} />
                  <Kpi label={t('gp')} value={fmt.money(gp)} tone="ok" />
                  <Kpi label={t('margin')} value={revenue > 0 ? fmt.pct((100 * gp) / revenue) : '–'} />
                </div>
              </Group>
              <Group>
                <Table
                  rows={rows}
                  columns={[
                    { key: 'sold_at', label: t('soldAt'), render: (r) => fmt.dateTime(r.sold_at) },
                    { key: 'lot_no', label: t('lot'), render: (r) => <><LotLink lot={r.lot_no} /><div className="cell-sub">{r.product_name}</div></> },
                    { key: 'qty', label: t('qty'), align: 'num', render: (r) => fmt.qty(r.qty, r.uom_code) },
                    { key: 'unit_price', label: t('unitPrice'), align: 'num', render: (r) => fmt.money(r.unit_price) },
                    { key: 'line_total', label: t('lineTotal'), align: 'num', render: (r) => fmt.money(r.line_total) },
                    { key: 'cost', label: t('cost'), align: 'num', render: (r) => fmt.money(r.cost) },
                    { key: 'gross_profit', label: t('gp'), align: 'num', render: (r) => <strong>{fmt.money(r.gross_profit)}</strong> },
                    { key: 'device_id', label: t('device'), render: (r) => <span className="cell-sub">{r.store_code} · {r.device_id}</span> },
                  ]}
                />
              </Group>
            </>
          );
        }}
      </Async>
    </Page>
  );
}
