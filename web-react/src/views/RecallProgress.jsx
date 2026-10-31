import React, { useState } from 'react';
import { apiWrite, useApi } from '../lib/api';
import { useLang } from '../lib/i18n';
import { Async, ErrorState, Kpi, Mono, Pill, Table } from '../lib/ui';
import { LotLink } from './LotPanel';

export default function RecallProgress({ recall, onChanged }) {
  const { t, fmt } = useLang();
  const [expanded, setExpanded] = useState(false);
  const progress = useApi(expanded ? `/recall/progress?recall_code=${encodeURIComponent(recall.recall_code)}` : null);
  const [entry, setEntry] = useState(null);
  const [pin, setPin] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const [success, setSuccess] = useState(false);

  const edit = (line, mode) => {
    setEntry({ line, mode, contact_status: line.contact_status, note: line.note, qty: '' });
    setPin(''); setError(null); setSuccess(false);
  };
  const submit = async (e) => {
    e.preventDefault();
    setBusy(true); setError(null); setSuccess(false);
    try {
      if (entry.mode === 'return') {
        const qty = Number(entry.qty.trim().replace(',', '.'));
        if (!Number.isFinite(qty) || qty <= 0 || qty > entry.line.qty_pending) {
          throw Object.assign(new Error(t('rp_badQty')), { code: 'INVALID_INPUT', details: t('rp_badQty') });
        }
        await apiWrite('/movements/return', { lot_no: entry.line.lot_no, customer_code: entry.line.customer_code,
          warehouse: 'KARANTINA', qty, note: entry.note.trim() }, pin);
      } else {
        await apiWrite('/recall/followup', { recall_code: recall.recall_code, customer_code: entry.line.customer_code,
          contact_status: entry.contact_status, note: entry.note.trim() }, pin);
      }
      setEntry(null); setPin(''); setSuccess(true); progress.reload(); onChanged?.();
    } catch (err) { setError(err); }
    finally { setBusy(false); }
  };

  return <details className="recall-progress" onToggle={(e) => setExpanded(e.currentTarget.open)}>
    <summary>{t('rp_title')}</summary>
    {expanded && <>
      <p className="muted">{t('rp_note')}</p>
      <Async result={progress} rows={2}>{(p) => <>
        <div className="kpi-row">
          {(p.totals || []).map((total) => <Kpi key={total.uom_code} label={t('rp_pending')}
            value={fmt.qty(total.qty_pending, total.uom_code)} tone={p.all_recovered ? 'ok' : 'warn'}
            sub={t('rp_total', fmt.qty(total.qty_returned, total.uom_code), fmt.qty(total.qty_to_recover, total.uom_code))} />)}
        </div>
        {p.all_recovered && <p className="notice notice-ok">{t('rp_recovered')}</p>}
        <Table rows={p.lines} rowKey={(l) => `${l.customer_code}:${l.lot_no}`} columns={[
          { key: 'customer', label: t('customer'), render: (l) => <>{l.customer_name}<div className="cell-sub">{l.customer_code} · {t('tier')} {l.tier}</div></> },
          { key: 'lot_no', label: t('lot'), render: (l) => <LotLink lot={l.lot_no} /> },
          { key: 'qty_received', label: t('shipped'), align: 'num', render: (l) => fmt.qty(l.qty_received, l.uom_code) },
          { key: 'qty_forwarded', label: t('rp_forwarded'), align: 'num', render: (l) => fmt.qty(l.qty_forwarded, l.uom_code) },
          { key: 'qty_returned', label: t('returned'), align: 'num', render: (l) => fmt.qty(l.qty_returned, l.uom_code) },
          { key: 'qty_pending', label: t('rp_pending'), align: 'num', render: (l) => <strong>{fmt.qty(l.qty_pending, l.uom_code)}</strong> },
          { key: 'contact_status', label: t('rp_contact'), render: (l) => <>
            <Pill tone={l.contact_status === 'CONFIRMED' ? 'ok' : l.contact_status === 'CONTACTED' ? 'neutral' : 'warn'}>{t(`rp_${l.contact_status}`)}</Pill>
            {l.note && <div className="cell-sub">{l.note}</div>}
            {l.updated_by && <div className="cell-sub">{l.updated_by} · {fmt.dateTime(l.updated_at)}</div>}
            {l.quantity_warning && <div className="field-error">{t('rp_quantityWarning')}</div>}
          </> },
          { key: 'actions', label: '', render: (l) => recall.status === 'OPEN' ? <div className="recall-progress-actions no-print">
            <button className="btn-text" disabled={busy} onClick={() => edit(l, 'contact')}>{t('rp_update')}</button>
            <button className="btn-text" disabled={busy || l.qty_pending <= 0 || l.quantity_warning} onClick={() => edit(l, 'return')}>{t('rp_return')}</button>
          </div> : null },
        ]} />
      </>}</Async>
      {success && <p className="notice notice-ok" role="status">{t('rp_saved')}</p>}
      {entry && <form className="confirm no-print" onSubmit={submit}>
        <h3>{t(entry.mode === 'return' ? 'rp_return' : 'rp_update')} · {entry.line.customer_name}</h3>
        <p><Mono>{entry.line.lot_no}</Mono>{entry.mode === 'return' ? ` · ${t('rp_quarantine')}` : ''}</p>
        <div className="toolbar">
          {entry.mode === 'contact' ? <label className="field"><span>{t('rp_contact')}</span>
            <select value={entry.contact_status} onChange={(e) => setEntry({ ...entry, contact_status: e.target.value })}>
              {['PENDING', 'CONTACTED', 'CONFIRMED'].map((s) => <option key={s} value={s}>{t(`rp_${s}`)}</option>)}
            </select></label> : <label className="field"><span>{t('qty')} ({entry.line.uom_code})</span>
            <input inputMode="decimal" value={entry.qty} onChange={(e) => setEntry({ ...entry, qty: e.target.value })} required /></label>}
          <label className="field field-wide"><span>{t('rp_noteLabel')}</span><input value={entry.note} maxLength={500} onChange={(e) => setEntry({ ...entry, note: e.target.value })} /></label>
          <label className="field"><span>{t('supervisorPin')}</span><input type="password" inputMode="numeric" autoComplete="off" maxLength={12} value={pin} onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))} /></label>
        </div>
        {error && <ErrorState error={error} />}
        <div className="confirm-actions">
          <button type="button" className="btn-text" disabled={busy} onClick={() => { setEntry(null); setPin(''); }}>{t('cancel')}</button>
          <button type="submit" className="btn-primary" disabled={busy || pin.length < 4}>{t('rp_save')}</button>
        </div>
      </form>}
    </>}
  </details>;
}
