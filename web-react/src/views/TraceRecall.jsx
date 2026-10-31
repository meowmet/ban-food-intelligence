import React, { useState } from 'react';
import { API_BASE, apiWrite, useApi } from '../lib/api';
import { useLang } from '../lib/i18n';
import { Async, ErrorState, Group, Kpi, Mono, Page, Pill, Segmented, Table } from '../lib/ui';
import RecallProgress from './RecallProgress';
import { LotLink, LotLinks } from './LotPanel';
import TraceFlow from './TraceFlow';

// #trace?lot=SUC-2026-045 (from the lot panel) starts on that lot; the mandatory scenario otherwise.
const initialLot = () => new URLSearchParams(window.location.hash.split('?')[1] || '').get('lot') || 'ET-2026-001';

export default function TraceRecall() {
  const { t } = useLang();
  const [input, setInput] = useState(initialLot);
  const [lot, setLot] = useState(initialLot);
  const [direction, setDirection] = useState('forward');
  const trace = useApi(lot ? `/trace/${encodeURIComponent(lot)}?direction=${direction}` : null);
  const recalls = useApi('/recall');

  return (
    <Page title={t('tr_title')} subtitle={t('tr_sub')}>
      <Group>
        <form
          className="toolbar"
          onSubmit={(e) => {
            e.preventDefault();
            setLot(input.trim().toUpperCase());
          }}
        >
          <label className="field">
            <span>{t('lotNo')}</span>
            <input value={input} onChange={(e) => setInput(e.target.value)} spellCheck={false} />
          </label>
          <Segmented
            label={t('tr_title')}
            value={direction}
            onChange={setDirection}
            options={[{ value: 'forward', label: t('forward') }, { value: 'backward', label: t('backward') }]}
          />
          <button className="btn-primary" type="submit">{t('show')}</button>
        </form>
      </Group>

      <Async result={trace}>{(tr) => <TraceResult tr={tr} />}</Async>

      <RecallPanel lot={lot} recalls={recalls} />
    </Page>
  );
}

function TraceResult({ tr }) {
  const { t, fmt } = useLang();
  return (
    <>
      <Group title={t('fl_title')} footer={t(tr.direction === 'forward' ? 'fl_noteForward' : 'fl_noteBackward')}>
        <TraceFlow tr={tr} />
      </Group>
      {tr.mass_balance && <MassBalance mb={tr.mass_balance} />}
      <Group title={t('lotsReached')}>
        <Table
          rows={tr.lots}
          rowTone={(l) => (l.status === 'RECALLED' ? 'bad' : '')}
          columns={[
            { key: 'depth', label: t('depth'), align: 'num' },
            { key: 'lot_no', label: t('lot'), render: (l) => <><LotLink lot={l.lot_no} /><div className="cell-sub">{l.product_name}</div></> },
            { key: 'origin_type', label: t('origin') },
            { key: 'qty', label: t('qty'), align: 'num', render: (l) => fmt.qty(l.qty, l.uom_code) },
            { key: 'supplier_name', label: t('supplier'), render: (l) => l.supplier_name || '–' },
            { key: 'status', label: t('status'), render: (l) => <Pill tone={l.status === 'RECALLED' ? 'bad' : 'neutral'}>{l.status}</Pill> },
          ]}
        />
      </Group>
      <Group title={t('links')}>
        <Table
          rows={tr.links}
          columns={[
            { key: 'document_no', label: t('document'), render: (l) => <><Mono>{l.document_no}</Mono><div className="cell-sub">{l.link_type}{l.subcontractor ? ` · ${l.subcontractor}` : ''}</div></> },
            { key: 'input_lot_no', label: t('inputLot'), render: (l) => <LotLink lot={l.input_lot_no} /> },
            { key: 'qty_consumed', label: t('consumed'), align: 'num', render: (l) => fmt.qty(l.qty_consumed, l.input_uom) },
            { key: 'output_lot_no', label: t('outputLot2'), render: (l) => <LotLink lot={l.output_lot_no} /> },
            { key: 'qty_produced', label: t('produced'), align: 'num', render: (l) => fmt.qty(l.qty_produced, l.output_uom) },
          ]}
        />
      </Group>
      {tr.direction === 'forward' && (
        <>
          <Group title={t('shipments')}>
            <Table
              rows={tr.shipments || []}
              columns={[
                { key: 'tier', label: t('tier'), align: 'num' },
                { key: 'customer_name', label: t('customer'), render: (s) => <>{s.customer_name}{s.received_via && <div className="cell-sub">{t('via')}: {s.received_via}</div>}</> },
                { key: 'contact', label: t('contact'), render: (s) => <>{s.contact_person}<div className="cell-sub">{s.phone} · {s.email}</div></> },
                { key: 'lot_no', label: t('lot'), render: (s) => <LotLink lot={s.lot_no} /> },
                { key: 'shipment_no', label: t('document'), render: (s) => <><Mono>{s.shipment_no}</Mono><div className="cell-sub">{fmt.date(s.ship_date)}</div></> },
                { key: 'qty', label: t('qty'), align: 'num', render: (s) => <strong>{fmt.qty(s.qty, s.uom_code)}</strong> },
              ]}
            />
          </Group>
          <div className="grid-2">
            <Group title={t('stockNow')}>
              <Table
                rows={tr.stock || []}
                columns={[
                  { key: 'lot_no', label: t('lot'), render: (s) => <LotLink lot={s.lot_no} /> },
                  { key: 'warehouse_code', label: t('warehouse'), render: (s) => <Mono>{s.warehouse_code}</Mono> },
                  { key: 'qty', label: t('qty'), align: 'num', render: (s) => fmt.qty(s.qty, s.uom_code) },
                ]}
              />
            </Group>
            <Group title={t('posSales')}>
              <Table
                rows={tr.sales || []}
                columns={[
                  { key: 'lot_no', label: t('lot'), render: (s) => <LotLink lot={s.lot_no} /> },
                  { key: 'store_code', label: t('store') },
                  { key: 'qty_sold', label: t('qty'), align: 'num', render: (s) => fmt.num(s.qty_sold, 3) },
                ]}
              />
            </Group>
          </div>
        </>
      )}
    </>
  );
}

const MB_COLUMNS = [['used_in_lots', 'mb_used'], ['shipped', 'mb_shipped'], ['sold', 'mb_sold'], ['wasted', 'mb_wasted'],
  ['returned', 'mb_returned'], ['in_stock', 'mb_inStock'], ['count_diff', 'mb_countDiff']];

/** Where every recorded quantity of the traced lots is now; a gap is what no document explains. */
function MassBalance({ mb }) {
  const { t, fmt } = useLang();
  const q = (v, uom) => (Math.abs(v) > 0.0005 ? fmt.qty(v, uom) : '–');
  const shown = mb.totals.filter((x) => ['at_customers', 'sold', 'wasted', 'in_stock', 'unexplained'].some((k) => Math.abs(x[k]) > 0.0005));
  return (
    <Group
      title={t('mb_title')}
      aside={<Pill tone={mb.balanced ? 'ok' : 'bad'}>{t(mb.balanced ? 'mb_balanced' : 'mb_gap')}</Pill>}
      footer={t('mb_note')}
    >
      {shown.map((x) => (
        <div className="kpi-row" key={x.uom_code}>
          <Kpi label={t('mb_atCustomers')} value={q(x.at_customers, x.uom_code)} />
          <Kpi label={t('mb_sold')} value={q(x.sold, x.uom_code)} />
          <Kpi label={t('mb_inStock')} value={q(x.in_stock, x.uom_code)}
            sub={Math.abs(x.at_subcontractor) > 0.0005 ? t('mb_atSub', fmt.qty(x.at_subcontractor, x.uom_code)) : null} />
          <Kpi label={t('mb_wasted')} value={q(x.wasted, x.uom_code)} />
          <Kpi label={t('mb_unexplained')} value={fmt.qty(x.unexplained, x.uom_code)} tone={Math.abs(x.unexplained) > 0.0005 ? 'bad' : 'ok'} />
        </div>
      ))}
      <Table
        rows={mb.lines}
        rowKey={(l) => l.lot_no}
        rowTone={(l) => (Math.abs(l.unexplained) > 0.0005 ? 'bad' : '')}
        columns={[
          { key: 'lot_no', label: t('lot'), render: (l) => <><LotLink lot={l.lot_no} /><div className="cell-sub">{l.product_name}</div></> },
          { key: 'declared', label: t('mb_declared'), align: 'num', render: (l) => <strong>{fmt.qty(l.declared_qty, l.uom_code)}</strong> },
          // a movement kind no lot has would only add an empty column
          ...MB_COLUMNS.filter(([field]) => mb.lines.some((l) => Math.abs(l[field]) > 0.0005)).map(([field, label]) => (
            { key: field, label: t(label), align: 'num', render: (l) => q(l[field], l.uom_code) })),
          { key: 'gap', label: t('mb_unexplained'), align: 'num', render: (l) => <strong className={Math.abs(l.unexplained) > 0.0005 ? 'tone-bad' : ''}>{q(l.unexplained, l.uom_code)}</strong> },
        ]}
      />
    </Group>
  );
}

function RecallPanel({ lot, recalls }) {
  const { t, fmt } = useLang();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState(null); // { ok, text } or { error }
  const [pin, setPin] = useState('');

  const start = async () => {
    setBusy(true);
    setOutcome(null);
    try {
      const res = await apiWrite('/recall', { trigger_lot: lot, reason: t('recallReason') }, pin);
      const s = res.recall_summary;
      setOutcome({ ok: true, text: t('recallStarted', s.recall_code, (s.affected_lots || []).length) });
      recalls.reload();
    } catch (error) {
      setOutcome({ error }); // real failure, never a fake success
    } finally {
      setBusy(false);
      setConfirming(false);
      setPin('');
    }
  };

  return (
    <Group
      title={t('recallTitle')}
      aside={
        !confirming && (
          <button className="btn-danger" onClick={() => setConfirming(true)} disabled={busy || !lot}>
            {t('startRecall', lot)}
          </button>
        )
      }
    >
      {confirming && (
        <div className="confirm">
          <p>{t('confirmRecall', lot)}</p>
          <label className="pin-field">
            <span>{t('supervisorPin')}</span>
            <input
              type="password"
              inputMode="numeric"
              autoComplete="off"
              maxLength={12}
              value={pin}
              onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))}
            />
          </label>
          <p className="muted">{t('pinHint')}</p>
          <div className="confirm-actions">
            <button className="btn-text" onClick={() => setConfirming(false)}>{t('cancel')}</button>
            <button className="btn-danger" onClick={start} disabled={busy || pin.length < 4}>{t('confirm')}</button>
          </div>
        </div>
      )}
      {outcome?.ok && <p className="notice notice-ok">{outcome.text}</p>}
      {outcome?.error && (
        <>
          <p className="notice notice-bad">{t('recallFailed')}</p>
          <ErrorState error={outcome.error} />
        </>
      )}

      <Async result={recalls} rows={2}>
        {(list) =>
          !list || list.length === 0 ? (
            <p className="empty">{t('noRecall')}</p>
          ) : (
            list.map((rc) => (
              <div key={rc.recall_code} className="recall">
                <div className="recall-head">
                  <div>
                    <strong>{rc.recall_code}</strong> · <LotLink lot={rc.trigger_lot} />
                    <div className="cell-sub">{fmt.dateTime(rc.timestamp)} · {rc.reason}</div>
                  </div>
                  <div className="recall-actions">
                    {rc.lock_enforced && <Pill tone="bad">{t('lockEnforced')}</Pill>}
                    <a className="btn-text" href={`${API_BASE}/recall/contacts.csv?recall_code=${encodeURIComponent(rc.recall_code)}`}>{t('exportCsv')}</a>
                    <button className="btn-text no-print" onClick={() => window.print()}>{t('print')}</button>
                  </div>
                </div>
                <p className="cell-sub">{t('affectedLots')}: {(rc.affected_lots || []).join(', ')}</p>
                <Table
                  rows={rc.affected_markets || []}
                  columns={[
                    { key: 'tier', label: t('tier'), align: 'num' },
                    { key: 'customer_name', label: t('customer'), render: (m) => <>{m.customer_name}<div className="cell-sub">{m.customer_code}{m.received_via ? ` · ${t('via')}: ${m.received_via}` : ''}</div></> },
                    { key: 'contact', label: t('contact'), render: (m) => <>{m.contact_person}<div className="cell-sub">{m.phone} · {m.email}</div></> },
                    { key: 'address', label: t('address'), render: (m) => m.address || '–' },
                    { key: 'lots', label: t('lot'), render: (m) => <LotLinks lots={m.lots} /> },
                    { key: 'qty_shipped', label: t('shipped'), align: 'num', render: (m) => <strong>{fmt.num(m.qty_shipped, 0)}</strong> },
                    { key: 'qty_returned', label: t('returned'), align: 'num', render: (m) => fmt.num(m.qty_returned, 0) },
                    {
                      key: 'second_tier',
                      label: '',
                      render: (m) => (
                        <Pill tone={m.second_tier === 'RECORDED' ? 'neutral' : 'warn'}>
                          {m.second_tier === 'RECORDED' ? t('secondTierRecorded') : t('secondTierNotRecorded')}
                        </Pill>
                      ),
                    },
                  ]}
                />
                <RecallProgress recall={rc} onChanged={recalls.reload} />
              </div>
            ))
          )
        }
      </Async>
    </Group>
  );
}
