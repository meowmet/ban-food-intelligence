import React, { useState } from 'react';
import { apiWrite, useApi } from '../lib/api';
import { useLang } from '../lib/i18n';
import { ErrorState, Group, Mono, Table } from '../lib/ui';
import { LotLink } from './LotPanel';

// Stock count: the counter types what is on the shelf; the API compares it with the ledger when
// the count is saved and books each difference, with its reason, as an adjustment movement.

const COUNTABLE = ['MAIN', 'COLD', 'STORE', 'QUARANTINE']; // a subcontractor's stock is not ours to count

/** A counted quantity typed with a comma or a dot; null when the field is empty (not counted). */
export function parseCount(text) {
  const s = String(text ?? '').trim().replace(',', '.');
  if (s === '') return null;
  return /^\d+(\.\d+)?$/.test(s) ? Number(s) : NaN;
}

/** The lines to send for the counted rows and, per lot, what blocks saving. */
export function countLines(rows, entries) {
  const lines = [];
  const problems = {};
  for (const r of rows) {
    const e = entries[r.lot_no] || {};
    const counted = parseCount(e.counted);
    if (counted == null) continue;
    if (!Number.isFinite(counted)) {
      problems[r.lot_no] = 'sc_bad';
      continue;
    }
    if (r.uom_code !== 'KG' && !Number.isInteger(counted)) {
      problems[r.lot_no] = 'sc_whole';
      continue;
    }
    const diff = Math.round((counted - r.qty) * 1000) / 1000;
    const reason = (e.reason || '').trim();
    if (diff !== 0 && !reason) problems[r.lot_no] = 'sc_reasonNeeded';
    lines.push({ lot_no: r.lot_no, counted_qty: counted, expected_qty: r.qty, reason: diff !== 0 ? reason : '', diff, value: diff * r.unit_cost });
  }
  return { lines, problems };
}

export function StockCountForm({ rows, onSaved, onStale, onCancel }) {
  const { t, fmt } = useLang();
  const warehouses = useApi('/warehouses');
  const [wh, setWh] = useState('ANA-DEPO');
  const [entries, setEntries] = useState({});
  const [note, setNote] = useState('');
  const [pin, setPin] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);

  const list = rows.filter((r) => r.warehouse_code === wh && r.qty > 0).sort((a, b) => a.lot_no.localeCompare(b.lot_no));
  const { lines, problems } = countLines(list, entries);
  const diffs = lines.filter((l) => l.diff !== 0);
  const short = -diffs.filter((l) => l.diff < 0).reduce((s, l) => s + l.value, 0);
  const over = diffs.filter((l) => l.diff > 0).reduce((s, l) => s + l.value, 0);
  const set = (lot, patch) => setEntries((e) => ({ ...e, [lot]: { ...e[lot], ...patch } }));
  const signed = (v, uom) => `${v > 0 ? '+' : ''}${fmt.qty(v, uom)}`;

  const submit = async (e) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await apiWrite('/stock-counts', { warehouse: wh, note: note.trim(),
        lines: lines.map(({ lot_no, counted_qty, expected_qty, reason }) => ({ lot_no, counted_qty, expected_qty, reason })) }, pin);
      setEntries({});
      setPin('');
      onSaved(res);
    } catch (err) {
      setError(err);
      if (err.status === 409) onStale(); // the ledger moved: reload the system quantities, keep what was typed
    } finally {
      setBusy(false);
    }
  };

  return (
    <Group title={t('sc_title')} footer={t('sc_intro')}>
      <form onSubmit={submit}>
        <div className="toolbar">
          <label className="field"><span>{t('warehouse')}</span>
            <select value={wh} onChange={(e) => { setWh(e.target.value); setEntries({}); setError(null); }}>
              {(warehouses.data || [{ code: 'ANA-DEPO', name: 'ANA-DEPO', warehouse_type: 'MAIN' }])
                .filter((w) => COUNTABLE.includes(w.warehouse_type))
                .map((w) => <option key={w.code} value={w.code}>{w.code} · {w.name}</option>)}
            </select></label>
        </div>
        <Table
          rows={list}
          empty={t('sc_none')}
          rowKey={(r) => r.lot_no}
          rowTone={(r) => (problems[r.lot_no] ? 'bad' : '')}
          columns={[
            { key: 'lot', label: t('lot'), render: (r) => <><LotLink lot={r.lot_no} /><div className="cell-sub">{r.product_name}</div></> },
            { key: 'system', label: t('sc_system'), align: 'num', render: (r) => fmt.qty(r.qty, r.uom_code) },
            { key: 'counted', label: t('sc_counted'), align: 'num', render: (r) => (
              <input className="count-input" inputMode="decimal" aria-label={t('sc_countedFor', r.lot_no)}
                value={entries[r.lot_no]?.counted ?? ''} onChange={(e) => set(r.lot_no, { counted: e.target.value })} />) },
            { key: 'diff', label: t('sc_diff'), align: 'num', render: (r) => {
              const l = lines.find((x) => x.lot_no === r.lot_no);
              if (!l) return '–';
              return <strong className={l.diff < 0 ? 'tone-bad' : l.diff > 0 ? 'tone-warn' : 'tone-ok'}>{l.diff === 0 ? '0' : signed(l.diff, r.uom_code)}</strong>;
            } },
            { key: 'reason', label: t('sc_reason'), render: (r) => {
              const l = lines.find((x) => x.lot_no === r.lot_no);
              return <>
                {l && l.diff !== 0 && <input className="count-reason" aria-label={t('sc_reasonFor', r.lot_no)} maxLength={255}
                  value={entries[r.lot_no]?.reason ?? ''} onChange={(e) => set(r.lot_no, { reason: e.target.value })} />}
                {problems[r.lot_no] && <div className="field-error">{t(problems[r.lot_no])}</div>}
              </>;
            } },
          ]}
        />
        <p className="notice" role="status">{t('sc_summary', lines.length, diffs.length, fmt.money(short), fmt.money(over))}</p>
        <div className="toolbar">
          <label className="field field-wide"><span>{t('sc_note')}</span>
            <input value={note} maxLength={255} onChange={(e) => setNote(e.target.value)} /></label>
          <label className="field"><span>{t('supervisorPin')}</span>
            <input type="password" inputMode="numeric" autoComplete="off" maxLength={12} value={pin}
              onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))} /></label>
        </div>
        {error && <ErrorState error={error} />}
        <div className="confirm-actions count-actions">
          <button type="button" className="btn-text" disabled={busy} onClick={onCancel}>{t('cancel')}</button>
          <button type="submit" className="btn-primary" disabled={busy || lines.length === 0 || Object.keys(problems).length > 0 || pin.length < 4}>
            {t('sc_save')}
          </button>
        </div>
      </form>
    </Group>
  );
}

export function StockCountHistory({ counts }) {
  const { t, fmt } = useLang();
  if (!counts || counts.length === 0) return null;
  return (
    <Group title={t('sc_history')}>
      <Table
        rows={counts}
        rowKey={(c) => c.count_no}
        columns={[
          { key: 'count_no', label: t('document'), render: (c) => <><Mono>{c.count_no}</Mono><div className="cell-sub">{c.counted_by} · {fmt.dateTime(c.counted_at)}</div></> },
          { key: 'warehouse', label: t('warehouse'), render: (c) => <Mono>{c.warehouse}</Mono> },
          { key: 'lines', label: t('sc_lines'), align: 'num', render: (c) => fmt.num(c.lines.length, 0) },
          { key: 'diffs', label: t('sc_diffs'), render: (c) => {
            const d = c.lines.filter((l) => l.diff_qty !== 0);
            if (d.length === 0) return <span className="cell-sub">{t('sc_noDiff')}</span>;
            return d.map((l) => <div key={l.lot_no}>
              <LotLink lot={l.lot_no} /> <strong className={l.diff_qty < 0 ? 'tone-bad' : 'tone-warn'}>{l.diff_qty > 0 ? '+' : ''}{fmt.qty(l.diff_qty, l.uom_code)}</strong>
              <div className="cell-sub">{l.reason}</div>
            </div>);
          } },
          { key: 'short', label: t('sc_short'), align: 'num', render: (c) => (c.shortage_eur ? fmt.money(c.shortage_eur) : '–') },
          { key: 'over', label: t('sc_over'), align: 'num', render: (c) => (c.surplus_eur ? fmt.money(c.surplus_eur) : '–') },
        ]}
      />
    </Group>
  );
}
