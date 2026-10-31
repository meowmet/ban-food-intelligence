import React, { useState } from 'react';
import { useApi } from '../lib/api';
import { useLang } from '../lib/i18n';
import { Async, Group, Kpi, Mono, Page, Segmented, Table } from '../lib/ui';
import { ExpiryText, LotLink, LotStatus, SOON_DAYS, daysUntil, isExpired } from './LotPanel';
import { StockCountForm, StockCountHistory } from './StockCount';

const blocked = (r) => r.status === 'RECALLED' || r.status === 'BLOCKED';

/** Stock rows split the way a shop acts on them: sell first, cannot sell, everything. */
export function lotBuckets(rows) {
  const all = rows || [];
  const inStock = all.filter((r) => r.qty > 0);
  const soon = inStock.filter((r) => !blocked(r) && !isExpired(r.expiry_date) && daysUntil(r.expiry_date) != null && daysUntil(r.expiry_date) <= SOON_DAYS);
  const unsellable = inStock.filter((r) => blocked(r) || isExpired(r.expiry_date));
  return { stock: inStock, soon, unsellable, all };
}

export default function Lots() {
  const { t, fmt } = useLang();
  const result = useApi('/lots');
  // #lots?filter=soon opens a bucket directly (the sidebar bell links there)
  const [filter, setFilter] = useState(() => {
    const f = new URLSearchParams(window.location.hash.split('?')[1] || '').get('filter');
    return ['stock', 'soon', 'unsellable', 'all'].includes(f) ? f : 'stock';
  });
  const [q, setQ] = useState('');
  const counts = useApi('/stock-counts');
  const [counting, setCounting] = useState(false);
  const [saved, setSaved] = useState(null);
  return (
    <Page title={t('lots_title')} subtitle={t('lots_sub', SOON_DAYS)}
      actions={!counting && <button className="btn-primary" onClick={() => { setCounting(true); setSaved(null); }}>{t('sc_start')}</button>}>
      {saved && <p className="notice notice-ok" role="status">
        {t('sc_saved', saved.count_no, saved.lines.length, fmt.money(saved.shortage_eur), fmt.money(saved.surplus_eur))}</p>}
      <Async result={result}>
        {(rows) => {
          const b = lotBuckets(rows);
          const value = (xs) => xs.reduce((a, r) => a + r.stock_value, 0);
          const lots = (xs) => new Set(xs.map((r) => r.lot_no)).size;
          const needle = q.trim().toUpperCase();
          const shown = b[filter].filter((r) => !needle || r.lot_no.includes(needle) || r.sku.includes(needle) || r.product_name.toUpperCase().includes(needle));
          return (
            <>
              {counting && <StockCountForm rows={rows} onStale={result.reload} onCancel={() => setCounting(false)}
                onSaved={(res) => { setSaved(res); setCounting(false); result.reload(); counts.reload(); }} />}
              <Group>
                <div className="kpi-row">
                  <Kpi label={t('ov_lots_in_stock')} value={fmt.num(lots(b.stock), 0)} sub={fmt.money(value(b.stock))} />
                  <Kpi label={t('lots_soon', SOON_DAYS)} value={fmt.num(lots(b.soon), 0)} sub={fmt.money(value(b.soon))} tone={b.soon.length ? 'warn' : 'ok'} />
                  <Kpi label={t('lots_unsellable')} value={fmt.num(lots(b.unsellable), 0)} sub={fmt.money(value(b.unsellable))} tone={b.unsellable.length ? 'bad' : 'ok'} />
                </div>
              </Group>
              <Group>
                <div className="toolbar">
                  <Segmented
                    label={t('lots_title')}
                    value={filter}
                    onChange={setFilter}
                    options={[
                      { value: 'stock', label: t('lots_f_stock') },
                      { value: 'soon', label: t('lots_soon', SOON_DAYS) },
                      { value: 'unsellable', label: t('lots_unsellable') },
                      { value: 'all', label: t('lots_f_all') },
                    ]}
                  />
                  <label className="field">
                    <span>{t('lots_search')}</span>
                    <input type="search" value={q} onChange={(e) => setQ(e.target.value)} spellCheck={false} />
                  </label>
                </div>
                <Table
                  rows={shown}
                  rowKey={(r) => `${r.lot_no}:${r.warehouse_code}`}
                  rowTone={(r) => (blocked(r) || (r.qty > 0 && isExpired(r.expiry_date)) ? 'bad' : '')}
                  columns={[
                    { key: 'lot_no', label: t('lot'), render: (r) => <><LotLink lot={r.lot_no} /><div className="cell-sub">{r.product_name}</div></> },
                    { key: 'warehouse_code', label: t('warehouse'), render: (r) => <Mono>{r.warehouse_code}</Mono> },
                    { key: 'qty', label: t('qty'), align: 'num', render: (r) => <strong>{fmt.qty(r.qty, r.uom_code)}</strong> },
                    { key: 'status', label: t('status'), render: (r) => <LotStatus status={r.status} expiry={r.expiry_date} /> },
                    { key: 'expiry_date', label: t('expiry'), render: (r) => <ExpiryText expiry={r.expiry_date} /> },
                    { key: 'unit_cost', label: t('unitCost'), align: 'num', render: (r) => fmt.money(r.unit_cost) },
                    { key: 'stock_value', label: t('lots_value'), align: 'num', render: (r) => fmt.money(r.stock_value) },
                  ]}
                  footer={
                    <tr>
                      <td colSpan={6}>{t('total')}</td>
                      <td className="num"><strong>{fmt.money(value(shown))}</strong></td>
                    </tr>
                  }
                />
              </Group>
              <StockCountHistory counts={counts.data} />
            </>
          );
        }}
      </Async>
    </Page>
  );
}
