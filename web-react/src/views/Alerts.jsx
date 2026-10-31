import React, { useEffect, useRef, useState } from 'react';
import { Bell } from 'lucide-react';
import { apiWrite, useApi } from '../lib/api';
import { useLang } from '../lib/i18n';
import { Async, ErrorState, Group, Kpi, Mono, Page, Pill, Segmented, Table } from '../lib/ui';
import { AlertAccuracy, AlertRules, feedbackByTask, wilson } from './AlertQuality';
import { LotLink, SOON_DAYS } from './LotPanel';
import { lotBuckets } from './Lots';

// Alerts are written by the API when a record is saved (alerts.go) and by the ml-engine batch
// detector. They never change stock, cost or lot status: a supervisor reviews and closes them;
// closed alerts and missed problems give the observed precision and recall (AlertQuality.jsx).

export const isOpen = (a) => a.status === 'OPEN' || a.status === 'ACKNOWLEDGED';

/** Expected / actual value of an alert in its unit: packs, kg, price per unit or sold quantity. */
export function alertValue(a, v, fmt) {
  if (v == null) return '–';
  switch (a.task) {
    case 'PRODUCTION_YIELD':
      return `${fmt.num(v, Number.isInteger(Number(v)) ? 0 : 1)} ${fmt.unit('PAKET', v)}`;
    case 'BUTCHERY_YIELD':
      return fmt.qty(v, 'KG');
    case 'PURCHASE_COST':
      return `${fmt.money(v)}/${fmt.unit(a.uom_code || 'KG', 1)}`;
    default:
      return fmt.qty(v, a.uom_code);
  }
}

export default function Alerts() {
  const { t, fmt } = useLang();
  const [filter, setFilter] = useState('open');
  const result = useApi(`/alerts?status=${filter}`);
  const [entry, setEntry] = useState(null);
  const [pin, setPin] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const [saved, setSaved] = useState(false);

  const review = (a) => {
    setEntry({ alert: a, status: a.status === 'OPEN' ? 'ACKNOWLEDGED' : 'RESOLVED', note: '' });
    setError(null);
    setSaved(false);
  };
  const closing = entry && entry.status !== 'ACKNOWLEDGED';
  const submit = async (e) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await apiWrite('/alerts/review', { id: entry.alert.id, status: entry.status, note: entry.note.trim() }, pin);
      setEntry(null);
      setPin('');
      setSaved(true);
      result.reload();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Page title={t('al_title')} subtitle={t('al_sub')}>
      <Async result={result}>
        {(d) => {
          const fb = feedbackByTask(d.feedback, d.missed);
          const confirmed = fb.reduce((s, x) => s + x.confirmed, 0);
          const falseAlarms = fb.reduce((s, x) => s + x.false_alarms, 0);
          const loss = fb.reduce((s, x) => s + x.confirmed_loss_eur, 0);
          const range = wilson(confirmed, confirmed + falseAlarms);
          return (
            <>
              <Group>
                <div className="kpi-row">
                  <Kpi label={t('al_open')} value={fmt.num(d.open, 0)} tone={d.open ? 'warn' : 'ok'} />
                  <Kpi label={t('al_critical')} value={fmt.num(d.critical_open, 0)} tone={d.critical_open ? 'bad' : 'ok'} />
                  <Kpi label={t('al_precision')} value={confirmed + falseAlarms ? fmt.pct((100 * confirmed) / (confirmed + falseAlarms), 0) : '–'}
                    sub={range ? `${t('al_precisionSub', confirmed, falseAlarms)} · ${t('al_ci', fmt.pct(100 * range[0], 0), fmt.pct(100 * range[1], 0))}` : t('al_precisionNone')} />
                  <Kpi label={t('al_loss')} value={fmt.money(loss)} sub={t('al_lossSub')} />
                </div>
              </Group>
              <Group
                title={t('al_list')}
                aside={<Segmented label={t('al_list')} value={filter} onChange={(v) => { setFilter(v); setEntry(null); }}
                  options={['open', 'closed', 'all'].map((v) => ({ value: v, label: t(`al_f_${v}`) }))} />}
              >
                {saved && <p className="notice notice-ok" role="status">{t('al_saved')}</p>}
                <Table
                  rows={d.alerts}
                  empty={filter === 'open' ? t('al_noneOpen') : t('noData')}
                  rowKey={(a) => a.id}
                  rowTone={(a) => (isOpen(a) && a.severity === 'CRITICAL' ? 'bad' : isOpen(a) ? '' : 'muted')}
                  columns={[
                    { key: 'kind', label: t('al_kind'), render: (a) => <div className="alert-kind">
                      <Pill tone={a.severity === 'CRITICAL' ? 'bad' : 'warn'}>{t(`al_sev_${a.severity}`)}</Pill>
                      <strong>{t(`al_dir_${a.direction}`)}</strong>
                      <div className="cell-sub">{t(`al_task_${a.task}`)}</div>
                      <div className="cell-sub">{t(`al_hint_${a.direction}`)}</div>
                    </div> },
                    { key: 'ref', label: `${t('document')} / ${t('lot')}`, render: (a) => <div className="alert-ref">
                      {a.ref_type === 'lot' ? <LotLink lot={a.lot_no} /> : <Mono>{a.ref_no}</Mono>}
                      {a.lot_no && a.ref_type !== 'lot' && <div><LotLink lot={a.lot_no} /></div>}
                      <div className="cell-sub">{[a.responsible, fmt.date(a.ref_date)].filter(Boolean).join(' · ')}</div>
                    </div> },
                    { key: 'expected', label: t('al_expected'), align: 'num', render: (a) => (a.task === 'LOT_MOVEMENT' && a.direction !== 'COUNT_SHORTAGE' ? '–' : alertValue(a, a.expected_value, fmt)) },
                    { key: 'actual', label: t('al_actual'), align: 'num', render: (a) => <>
                      <strong>{alertValue(a, a.actual_value, fmt)}</strong>
                      {a.task === 'PURCHASE_COST' && <div className="cell-sub">{t('al_pctUp', fmt.pct(a.score, 1))}</div>}
                      {a.detail && a.task !== 'PURCHASE_COST' && <div className="cell-sub">{a.detail}</div>}
                    </> },
                    { key: 'loss', label: t('al_impact'), align: 'num', render: (a) => (a.est_loss_eur ? fmt.money(a.est_loss_eur) : '–') },
                    { key: 'status', label: t('status'), render: (a) => <>
                      <Pill tone={{ OPEN: 'warn', RESOLVED: 'ok' }[a.status] || 'neutral'}>{t(`al_st_${a.status}`)}</Pill>
                      {a.resolved_by && <div className="cell-sub">{a.resolved_by}{a.resolved_at ? ` · ${fmt.dateTime(a.resolved_at)}` : ''}</div>}
                      {a.resolution_note && <div className="cell-sub">{a.resolution_note}</div>}
                    </> },
                    { key: 'actions', label: '', render: (a) => (isOpen(a)
                      ? <button className="btn-text no-print" disabled={busy} onClick={() => review(a)}>{t('al_review')}</button> : null) },
                  ]}
                />
                {entry && (
                  <form className="confirm no-print" onSubmit={submit}>
                    <h3>{t('al_review')} · {t(`al_dir_${entry.alert.direction}`)} · <Mono>{entry.alert.ref_no}</Mono></h3>
                    <div className="toolbar">
                      <label className="field"><span>{t('al_decision')}</span>
                        <select value={entry.status} onChange={(e) => setEntry({ ...entry, status: e.target.value })}>
                          {(entry.alert.status === 'OPEN' ? ['ACKNOWLEDGED'] : []).concat(['RESOLVED', 'FALSE_ALARM'])
                            .map((s) => <option key={s} value={s}>{t(`al_do_${s}`)}</option>)}
                        </select></label>
                      <label className="field field-wide"><span>{t(closing ? 'al_noteRequired' : 'al_note')}</span>
                        <input value={entry.note} maxLength={255} onChange={(e) => setEntry({ ...entry, note: e.target.value })} required={closing} /></label>
                      <label className="field"><span>{t('supervisorPin')}</span>
                        <input type="password" inputMode="numeric" autoComplete="off" maxLength={12} value={pin}
                          onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))} /></label>
                    </div>
                    {error && <ErrorState error={error} />}
                    <div className="confirm-actions">
                      <button type="button" className="btn-text" disabled={busy} onClick={() => { setEntry(null); setPin(''); }}>{t('cancel')}</button>
                      <button type="submit" className="btn-primary" disabled={busy || pin.length < 4 || (closing && !entry.note.trim())}>{t('al_save')}</button>
                    </div>
                  </form>
                )}
              </Group>
              <AlertAccuracy data={d} onSaved={result.reload} />
              <AlertRules />
            </>
          );
        }}
      </Async>
    </Page>
  );
}

/**
 * Sidebar bell: open alerts, lots close to expiry and active recalls, each a link to its tab.
 * It refreshes on navigation, after every saved write and once a minute.
 */
export function AlertBell({ onOpen }) {
  const { t, fmt } = useLang();
  const alerts = useApi('/alerts?status=open');
  const lots = useApi('/lots');
  const recalls = useApi('/recall');
  const [open, setOpen] = useState(false);
  const box = useRef(null);
  const reloads = useRef([]);
  reloads.current = [alerts.reload, lots.reload, recalls.reload];

  useEffect(() => {
    const refresh = () => reloads.current.forEach((r) => r());
    const timer = setInterval(refresh, 60_000);
    window.addEventListener('hashchange', refresh);
    window.addEventListener('data:changed', refresh);
    return () => {
      clearInterval(timer);
      window.removeEventListener('hashchange', refresh);
      window.removeEventListener('data:changed', refresh);
    };
  }, []);
  useEffect(() => {
    if (!open) return undefined;
    const close = (e) => {
      if (e.type === 'keydown' ? e.key === 'Escape' : !box.current?.contains(e.target)) setOpen(false);
    };
    document.addEventListener('mousedown', close);
    document.addEventListener('keydown', close);
    return () => {
      document.removeEventListener('mousedown', close);
      document.removeEventListener('keydown', close);
    };
  }, [open]);

  const nAlerts = alerts.data?.open ?? 0;
  const critical = alerts.data?.critical_open ?? 0;
  const soon = lots.data ? new Set(lotBuckets(lots.data).soon.map((r) => r.lot_no)).size : 0;
  const active = (recalls.data || []).filter((r) => r.lock_enforced).length;
  const total = nAlerts + soon + active;
  const go = (route) => {
    setOpen(false);
    onOpen(route);
  };
  const item = (route, label, n, tone, extra) => (
    <button type="button" role="menuitem" onClick={() => go(route)}>
      <span>{label}</span>
      {extra}
      <strong className={n ? `tone-${tone}` : ''}>{fmt.num(n, 0)}</strong>
    </button>
  );

  return (
    <div className="bell" ref={box}>
      <button type="button" className="bell-btn" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen((o) => !o)}
        aria-label={t('bell_label', total)}>
        <Bell size={18} aria-hidden="true" />
        <span className="bell-text">{t('bell_title')}</span>
        {total > 0 && <span className={`badge ${critical || active ? 'badge-bad' : 'badge-warn'}`} aria-hidden="true">{total}</span>}
      </button>
      {open && (
        <div className="bell-menu" role="menu" aria-label={t('bell_title')}>
          {item('alerts', t('bell_alerts'), nAlerts, critical ? 'bad' : 'warn',
            critical > 0 && <Pill tone="bad">{t('bell_critical', critical)}</Pill>)}
          {item('lots?filter=soon', t('lots_soon', SOON_DAYS), soon, 'warn')}
          {item('trace', t('ov_active_recalls'), active, 'bad')}
        </div>
      )}
    </div>
  );
}
