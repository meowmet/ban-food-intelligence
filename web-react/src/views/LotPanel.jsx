import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { useApi } from '../lib/api';
import { useLang } from '../lib/i18n';
import { Async, Group, Kpi, Mono, Pill, Table } from '../lib/ui';

// Every lot number on the dashboard opens this panel: the lot's facts, stock per warehouse,
// where it came from, where it went, a recall and every movement. Read-only, built from the
// existing trace / movements / recall endpoints (exact records, no AI).

const LotPanelContext = createContext(null);

const today = () => new Date().toISOString().slice(0, 10);

/** Days from today to an ISO date (0 = last day, negative = past); null without a date. */
export const daysUntil = (iso) => (iso ? Math.round((Date.parse(iso.slice(0, 10)) - Date.parse(today())) / 86_400_000) : null);

/** A lot can be sold or shipped through its expiry day (same rule as the stock trigger). */
export const isExpired = (iso) => (daysUntil(iso) ?? 0) < 0;

export function LotStatus({ status, expiry }) {
  const { t } = useLang();
  if (status === 'RECALLED' || status === 'BLOCKED') return <Pill tone="bad">{t(`ls_${status}`)}</Pill>;
  if (isExpired(expiry)) return <Pill tone="bad">{t('ls_expired')}</Pill>;
  if (status === 'DEPLETED') return <Pill tone="neutral">{t('ls_DEPLETED')}</Pill>;
  return <Pill tone="ok">{t('ls_ACTIVE')}</Pill>;
}

/** Expiry date with the days left; orange from SOON_DAYS on, red once past. */
export const SOON_DAYS = 3;
export function ExpiryText({ expiry }) {
  const { t, fmt } = useLang();
  const d = daysUntil(expiry);
  if (d == null) return '–';
  const tone = d < 0 ? 'tone-bad' : d <= SOON_DAYS ? 'tone-warn' : '';
  return (
    <>
      {fmt.date(expiry.slice(0, 10))}
      <div className={`cell-sub ${tone}`}>{d < 0 ? t('ls_daysPast', -d) : d === 0 ? t('ls_lastDay') : t('ls_daysLeft', d)}</div>
    </>
  );
}

/** The lot card opener (null outside LotPanelProvider), for lot references that are not links. */
export const useOpenLot = () => useContext(LotPanelContext);

export function LotLink({ lot }) {
  const open = useContext(LotPanelContext);
  if (!lot || lot === '–' || !open) return <Mono>{lot || '–'}</Mono>;
  return <button type="button" className="lot-link" onClick={() => open(lot)}>{lot}</button>;
}

/** "A,B" (a recall line lists several lots) -> one link each. */
export function LotLinks({ lots }) {
  const list = String(lots || '').split(',').map((s) => s.trim()).filter(Boolean);
  if (list.length === 0) return <Mono>–</Mono>;
  return list.map((l, i) => <React.Fragment key={l}>{i > 0 && ', '}<LotLink lot={l} /></React.Fragment>);
}

export function LotPanelProvider({ children, onOpenTrace }) {
  const [lot, setLot] = useState(null);
  const current = useRef(null);
  const opener = useRef(null);
  const open = useCallback((l) => {
    if (!current.current) opener.current = document.activeElement; // focus returns here on close
    current.current = l;
    setLot(l);
  }, []);
  const close = useCallback(() => {
    current.current = null;
    setLot(null);
    opener.current?.focus?.();
  }, []);
  return (
    <LotPanelContext.Provider value={open}>
      {children}
      {lot && <LotPanel key={lot} lot={lot} onClose={close} onOpenTrace={(l) => { close(); onOpenTrace(l); }} />}
    </LotPanelContext.Provider>
  );
}

function LotPanel({ lot, onClose, onOpenTrace }) {
  const { t } = useLang();
  const enc = encodeURIComponent(lot);
  const back = useApi(`/trace/${enc}?direction=backward`);
  const closeRef = useRef(null);
  useEffect(() => {
    closeRef.current?.focus();
    const onKey = (e) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);
  const root = back.data?.lots?.find((l) => l.depth === 0);

  return (
    <div className="drawer-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <aside className="drawer" role="dialog" aria-modal="true" aria-labelledby="lot-panel-title">
        <header className="drawer-head">
          <div>
            <h2 id="lot-panel-title">{lot}</h2>
            {root && <div className="cell-sub">{root.product_name} · {t(`origin_${root.origin_type}`)}</div>}
          </div>
          <button ref={closeRef} type="button" className="btn-text" onClick={onClose}>{t('en_close')}</button>
        </header>
        <Async result={back} rows={4}>
          {(tr) => <LotDetails lot={lot} root={tr.lots.find((l) => l.depth === 0)} back={tr} />}
        </Async>
        <div className="drawer-actions">
          <button type="button" className="btn-primary" onClick={() => onOpenTrace(lot)}>{t('lp_openTrace')}</button>
        </div>
      </aside>
    </div>
  );
}

function LotDetails({ lot, root, back }) {
  const { t, fmt, movementType } = useLang();
  const enc = encodeURIComponent(lot);
  const fwd = useApi(`/trace/${enc}?direction=forward`);
  const moves = useApi(`/movements?lot_no=${enc}`);
  const recalls = useApi('/recall');
  if (!root) return null;
  const recall = (recalls.data || []).find((rc) => (rc.affected_lots || []).includes(lot));
  const stock = (fwd.data?.stock || []).filter((s) => s.lot_no === lot);
  const inStock = stock.reduce((a, s) => a + s.qty, 0);
  const sources = back.lots.filter((l) => l.depth > 0);
  const madeFrom = back.links.filter((l) => l.output_lot_no === lot);
  // a lot without any movement answers 404 on /movements: that is an empty history, not an error
  const noMoves = moves.error?.status === 404;

  return (
    <>
      <div className="drawer-pills">
        <LotStatus status={root.status} expiry={root.expiry_date} />
        {recall && <Pill tone="bad">{recall.recall_code}</Pill>}
      </div>
      {recall && <p className="notice notice-bad drawer-notice">{t('lp_recalled', recall.recall_code, recall.trigger_lot, recall.reason || '')}</p>}

      <Group>
        <div className="kpi-row">
          <Kpi label={t('lp_initial')} value={fmt.qty(root.qty, root.uom_code)} sub={root.lot_date ? fmt.date(root.lot_date.slice(0, 10)) : undefined} />
          <Kpi label={t('lp_inStock')} value={fwd.data ? fmt.qty(inStock, root.uom_code) : '…'} />
          <Kpi label={t('expiry')} value={<span className="kpi-small"><ExpiryText expiry={root.expiry_date} /></span>} />
          <Kpi label={t('unitCost')} value={fmt.money(root.unit_cost)} sub={`/ ${fmt.unit(root.uom_code, 1)}`} />
        </div>
        {root.supplier_name && (
          <p className="drawer-fact">{t('supplier')}: <strong>{root.supplier_name}</strong>{root.supplier_lot_no ? <> · {t('supplierLot')} <Mono>{root.supplier_lot_no}</Mono></> : null}</p>
        )}
      </Group>

      {stock.length > 0 && (
        <Group title={t('stockNow')}>
          <Table rows={stock} rowKey={(s) => s.warehouse_code} columns={[
            { key: 'w', label: t('warehouse'), render: (s) => <Mono>{s.warehouse_code}</Mono> },
            { key: 'q', label: t('qty'), align: 'num', render: (s) => fmt.qty(s.qty, s.uom_code) },
          ]} />
        </Group>
      )}

      {madeFrom.length > 0 && (
        <Group title={t('lp_madeFrom')}>
          <Table rows={madeFrom} rowKey={(l) => `${l.document_no}:${l.input_lot_no}`} columns={[
            { key: 'in', label: t('inputLot'), render: (l) => <LotLink lot={l.input_lot_no} /> },
            { key: 'c', label: t('consumed'), align: 'num', render: (l) => fmt.qty(l.qty_consumed, l.input_uom) },
            { key: 'd', label: t('document'), render: (l) => <><Mono>{l.document_no}</Mono><div className="cell-sub">{l.link_type}{l.subcontractor ? ` · ${l.subcontractor}` : ''}</div></> },
          ]} />
        </Group>
      )}
      {sources.some((l) => l.supplier_name) && (
        <Group title={t('lp_sources')}>
          <Table rows={sources.filter((l) => l.supplier_name)} rowKey={(l) => l.lot_no} columns={[
            { key: 'l', label: t('lot'), render: (l) => <><LotLink lot={l.lot_no} /><div className="cell-sub">{l.product_name}</div></> },
            { key: 's', label: t('supplier'), render: (l) => <>{l.supplier_name}<div className="cell-sub">{l.supplier_lot_no || ''}</div></> },
          ]} />
        </Group>
      )}

      <Async result={fwd} rows={2}>
        {(f) => {
          const derived = f.lots.filter((l) => l.depth > 0);
          return (
            <>
              {derived.length > 0 && (
                <Group title={t('lp_derived')}>
                  <Table rows={derived} rowKey={(l) => l.lot_no} columns={[
                    { key: 'l', label: t('lot'), render: (l) => <><LotLink lot={l.lot_no} /><div className="cell-sub">{l.product_name}</div></> },
                    { key: 'q', label: t('qty'), align: 'num', render: (l) => fmt.qty(l.qty, l.uom_code) },
                    { key: 's', label: t('status'), render: (l) => <LotStatus status={l.status} expiry={l.expiry_date} /> },
                  ]} />
                </Group>
              )}
              <Group title={t('lp_buyers')}>
                <Table rows={f.shipments || []} empty={t('lp_noBuyers')} rowKey={(s, i) => `${s.shipment_no}:${s.lot_no}:${i}`} columns={[
                  { key: 'c', label: t('customer'), render: (s) => <>{s.customer_name}<div className="cell-sub">{t('tier')} {s.tier}{s.received_via ? ` · ${t('via')}: ${s.received_via}` : ''}</div></> },
                  { key: 'l', label: t('lot'), render: (s) => <LotLink lot={s.lot_no} /> },
                  { key: 'q', label: t('qty'), align: 'num', render: (s) => fmt.qty(s.qty, s.uom_code) },
                ]} />
                {(f.sales || []).length > 0 && (
                  <p className="drawer-fact">{t('posSales')}: {f.sales.map((s) => `${s.lot_no} · ${s.store_code} ${fmt.num(s.qty_sold, 3)}`).join(' | ')}</p>
                )}
              </Group>
            </>
          );
        }}
      </Async>

      <Group title={t('lp_movements')}>
        {noMoves ? <p className="empty">{t('noData')}</p> : (
          <Async result={moves} rows={3}>
            {(rows) => (
              <Table rows={rows} rowTone={(r) => (r.qty < 0 ? 'out' : 'in')} columns={[
                { key: 'd', label: t('date'), render: (r) => fmt.dateTime(r.moved_at) },
                { key: 't', label: t('type'), render: (r) => <>{movementType(r.movement_type)}<div className="cell-sub"><Mono>{r.warehouse_code}</Mono></div></> },
                { key: 'q', label: t('qty'), align: 'num', render: (r) => <span className={r.qty < 0 ? 'neg' : 'pos'}>{r.qty > 0 ? '+' : ''}{fmt.qty(r.qty, r.uom_code)}</span> },
                { key: 'b', label: t('balanceAfter'), align: 'num', render: (r) => fmt.qty(r.balance_after, r.uom_code) },
              ]} />
            )}
          </Async>
        )}
      </Group>
    </>
  );
}
