import React, { useState } from 'react';
import { apiWrite, useApi } from '../lib/api';
import { useLang } from '../lib/i18n';
import { Async, ErrorState, Group, Mono, Table } from '../lib/ui';
import { LotLink } from './LotPanel';

// How good the alerts are. In the field: reviewed alerts give the precision, problems found by hand
// that no alert flagged give the recall, both with a 95 % interval (a handful of records proves
// little). Before field data: the synthetic stress test of each threshold (calibrate_live_alerts.py).

const RULES = ['PRODUCTION_YIELD', 'BUTCHERY_YIELD', 'PURCHASE_COST', 'COUNT_SHORTAGE'];
const MISS_TASKS = ['PRODUCTION_YIELD', 'BUTCHERY_YIELD', 'PURCHASE_COST', 'LOT_MOVEMENT'];
export const ruleLabel = (t, rule) => t(rule === 'COUNT_SHORTAGE' ? 'al_dir_COUNT_SHORTAGE' : `al_task_${rule}`);

/** 95 % Wilson interval [lo, hi] of k successes in n trials; null without trials. */
export function wilson(k, n, z = 1.96) {
  if (!n) return null;
  const p = k / n;
  const z2 = z * z;
  const centre = (p + z2 / (2 * n)) / (1 + z2 / n);
  const half = (z * Math.sqrt((p * (1 - p)) / n + z2 / (4 * n * n))) / (1 + z2 / n);
  return [Math.max(0, centre - half), Math.min(1, centre + half)];
}

/** Per check: alerts, real problems, false alarms and missed problems; precision and recall with intervals. */
export function feedbackByTask(feedback, missed) {
  const by = {};
  const row = (task) => (by[task] ||= { task, alerts: 0, confirmed: 0, false_alarms: 0, missed: 0, confirmed_loss_eur: 0, missed_loss_eur: 0 });
  for (const f of feedback || []) {
    const x = row(f.task);
    x.alerts += f.alerts;
    x.confirmed += f.confirmed;
    x.false_alarms += f.false_alarms;
    x.confirmed_loss_eur += f.confirmed_loss_eur;
  }
  for (const m of missed || []) {
    const x = row(m.task);
    x.missed += m.findings;
    x.missed_loss_eur += m.est_loss_eur;
  }
  return Object.values(by).map((x) => {
    const closed = x.confirmed + x.false_alarms;
    const found = x.confirmed + x.missed;
    return { ...x, precision: closed ? x.confirmed / closed : null, precision_ci: wilson(x.confirmed, closed),
      recall: found ? x.confirmed / found : null, recall_ci: wilson(x.confirmed, found) };
  });
}

/** Linear interpolation of a curve's key at threshold t (clamped to the tested range). */
export function curveAt(curve, t, key) {
  if (!curve?.length) return null;
  const pts = [...curve].sort((a, b) => a.threshold - b.threshold);
  if (t <= pts[0].threshold) return pts[0][key];
  for (let i = 1; i < pts.length; i++) {
    const a = pts[i - 1];
    const b = pts[i];
    if (t === b.threshold) return b[key];
    if (t < b.threshold) return a[key] + ((b[key] - a[key]) * (t - a.threshold)) / (b.threshold - a.threshold);
  }
  return pts[pts.length - 1][key];
}

/** What the synthetic stress test says about a warning threshold, or null when it does not apply. */
export function syntheticAt(calibration, rule, warn) {
  const yieldCurve = { PRODUCTION_YIELD: calibration?.production_yield?.curve, BUTCHERY_YIELD: calibration?.butchery_yield?.curve }[rule];
  if (yieldCurve) {
    return { kind: 'yield', precision: curveAt(yieldCurve, warn, 'precision'), recall: curveAt(yieldCurve, warn, 'recall'),
      falseAlarms: curveAt(yieldCurve, warn, 'false_alarms_per_100') };
  }
  if (rule === 'PURCHASE_COST' && calibration?.purchase_cost) {
    return { kind: 'price', bySigma: calibration.purchase_cost.by_price_noise.map((s) => ({
      sigma: s.sigma,
      falseAlarms: curveAt(s.curve, warn, 'false_alarms_per_100'),
      caught: curveAt(s.curve.map((p) => ({ threshold: p.threshold, v: p.detected_by_jump_pct['10-20'] })), warn, 'v'),
    })) };
  }
  return null;
}

const ci = (t, fmt, range) => (range ? t('al_ci', fmt.pct(100 * range[0], 0), fmt.pct(100 * range[1], 0)) : '');

export function AlertAccuracy({ data, onSaved }) {
  const { t, fmt } = useLang();
  const rows = feedbackByTask(data.feedback, data.missed);
  const [entry, setEntry] = useState(null);
  const [pin, setPin] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const [saved, setSaved] = useState(false);
  const submit = async (e) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const loss = Number(String(entry.loss).trim().replace(',', '.') || 0);
      await apiWrite('/alerts/missed', { task: entry.task, ref_no: entry.ref.trim(), lot_no: entry.lot.trim().toUpperCase(),
        note: entry.note.trim(), est_loss_eur: loss }, pin);
      setEntry(null);
      setPin('');
      setSaved(true);
      onSaved();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };
  const ratio = (v, range) => (v == null ? '–' : <><strong>{fmt.pct(100 * v, 0)}</strong><div className="cell-sub">{ci(t, fmt, range)}</div></>);

  return (
    <Group title={t('al_precisionTitle')} footer={t('al_precisionNote')}
      aside={!entry && <button className="btn-text no-print" onClick={() => { setEntry({ task: 'PURCHASE_COST', ref: '', lot: '', note: '', loss: '' }); setSaved(false); }}>{t('al_missAdd')}</button>}>
      {saved && <p className="notice notice-ok" role="status">{t('al_missSaved')}</p>}
      {entry && (
        <form className="confirm no-print" onSubmit={submit}>
          <h3>{t('al_missAdd')}</h3>
          <p className="muted">{t('al_missHint')}</p>
          <div className="toolbar">
            <label className="field"><span>{t('al_missTask')}</span>
              <select value={entry.task} onChange={(e) => setEntry({ ...entry, task: e.target.value })}>
                {MISS_TASKS.map((k) => <option key={k} value={k}>{t(`al_task_${k}`)}</option>)}
              </select></label>
            <label className="field"><span>{t('al_missRef')}</span><input value={entry.ref} maxLength={30} onChange={(e) => setEntry({ ...entry, ref: e.target.value })} /></label>
            <label className="field"><span>{t('lotNo')}</span><input value={entry.lot} maxLength={30} spellCheck={false} onChange={(e) => setEntry({ ...entry, lot: e.target.value })} /></label>
            <label className="field field-wide"><span>{t('al_missNote')}</span><input value={entry.note} maxLength={255} required onChange={(e) => setEntry({ ...entry, note: e.target.value })} /></label>
            <label className="field"><span>{t('al_missLoss')}</span><input inputMode="decimal" value={entry.loss} onChange={(e) => setEntry({ ...entry, loss: e.target.value })} /></label>
            <label className="field"><span>{t('supervisorPin')}</span>
              <input type="password" inputMode="numeric" autoComplete="off" maxLength={12} value={pin} onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))} /></label>
          </div>
          {error && <ErrorState error={error} />}
          <div className="confirm-actions">
            <button type="button" className="btn-text" disabled={busy} onClick={() => { setEntry(null); setPin(''); }}>{t('cancel')}</button>
            <button type="submit" className="btn-primary" disabled={busy || pin.length < 4 || !entry.note.trim()}>{t('al_save')}</button>
          </div>
        </form>
      )}
      <Table
        rows={rows}
        empty={t('al_precisionNone')}
        rowKey={(x) => x.task}
        columns={[
          { key: 'task', label: t('al_kind'), render: (x) => t(`al_task_${x.task}`) },
          { key: 'alerts', label: t('al_count'), align: 'num', render: (x) => fmt.num(x.alerts, 0) },
          { key: 'confirmed', label: t('al_confirmed'), align: 'num', render: (x) => fmt.num(x.confirmed, 0) },
          { key: 'false', label: t('al_falseAlarms'), align: 'num', render: (x) => fmt.num(x.false_alarms, 0) },
          { key: 'missed', label: t('al_missed'), align: 'num', render: (x) => fmt.num(x.missed, 0) },
          { key: 'precision', label: t('al_precision'), align: 'num', render: (x) => ratio(x.precision, x.precision_ci) },
          { key: 'recall', label: t('al_recall'), align: 'num', render: (x) => ratio(x.recall, x.recall_ci) },
          { key: 'loss', label: t('al_loss'), align: 'num', render: (x) => fmt.money(x.confirmed_loss_eur) },
        ]}
      />
      {data.missed_recent?.length > 0 && (
        <Table
          rows={data.missed_recent}
          rowKey={(m) => m.id}
          columns={[
            { key: 'found', label: t('al_missRecent'), render: (m) => <>{m.note}<div className="cell-sub">{m.found_by} · {fmt.dateTime(m.found_at)}</div></> },
            { key: 'task', label: t('al_kind'), render: (m) => t(`al_task_${m.task}`) },
            { key: 'ref', label: `${t('document')} / ${t('lot')}`, render: (m) => <>{m.ref_no ? <Mono>{m.ref_no}</Mono> : null}{m.lot_no && <div><LotLink lot={m.lot_no} /></div>}</> },
            { key: 'loss', label: t('al_missLoss'), align: 'num', render: (m) => (m.est_loss_eur ? fmt.money(m.est_loss_eur) : '–') },
          ]}
        />
      )}
    </Group>
  );
}

export function AlertRules() {
  const { t, fmt } = useLang();
  const result = useApi('/alerts/rules');
  const [edit, setEdit] = useState(null);
  const [pin, setPin] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const [saved, setSaved] = useState(false);
  const num = (s) => Number(String(s).trim().replace(',', '.'));
  const value = (unit, v) => (unit === 'band98' ? t('ar_band', fmt.num(v, 2)) : unit === 'pct' ? fmt.pct(v, 1) : fmt.money(v));

  const synth = (calibration, rule, warn, observed) => {
    const s = syntheticAt(calibration, rule, warn);
    if (rule === 'COUNT_SHORTAGE') return <span className="cell-sub">{t('ar_count')}</span>;
    if (!s) return '–';
    if (s.kind === 'yield') return t('ar_synthYield', fmt.pct(100 * s.precision, 0), fmt.pct(100 * s.recall, 0), fmt.num(s.falseAlarms, 1));
    const three = s.bySigma.find((x) => x.sigma === 0.03);
    return <>
      {t('ar_synthPrice', s.bySigma.map((x) => `${fmt.pct(100 * x.sigma, 1)} → ${fmt.num(x.falseAlarms, 1)}`).join(' · '))}
      {three && <div className="cell-sub">{t('ar_synthPriceJump', fmt.pct(100 * three.caught, 0))}</div>}
      {observed !== undefined && <div className="cell-sub">{observed?.changes
        ? t('ar_observed', observed.changes, fmt.pct(observed.p95_increase_pct, 1), fmt.pct(observed.above_warn_pct, 1))
        : t('ar_observedNone')}</div>}
    </>;
  };

  const submit = async (e) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await apiWrite('/alerts/rules', { rule: edit.rule, warn: num(edit.warn), crit: num(edit.crit) }, pin);
      setEdit(null);
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
    <Group title={t('ar_title')} footer={t('ar_note')}>
      {saved && <p className="notice notice-ok" role="status">{t('ar_saved')}</p>}
      <Async result={result} rows={3}>
        {(d) => (
          <>
            <Table
              rows={RULES.map((r) => d.rules.find((x) => x.rule === r)).filter(Boolean)}
              rowKey={(r) => r.rule}
              columns={[
                { key: 'rule', label: t('ar_rule'), render: (r) => <strong>{ruleLabel(t, r.rule)}</strong> },
                { key: 'warn', label: t('ar_warn'), align: 'num', render: (r) => value(r.unit, r.warn) },
                { key: 'crit', label: t('ar_crit'), align: 'num', render: (r) => value(r.unit, r.crit) },
                { key: 'synth', label: t('ar_synth'), render: (r) => <div className="rule-synth">{synth(d.calibration, r.rule, r.warn, d.observed_price_changes)}</div> },
                { key: 'changed', label: t('ar_changed'), render: (r) => (r.updated_by === 'default' ? <span className="cell-sub">{t('ar_default')}</span>
                  : <span className="cell-sub">{r.updated_by} · {fmt.dateTime(r.updated_at)}</span>) },
                { key: 'edit', label: '', render: (r) => <button className="btn-text no-print" disabled={busy}
                  onClick={() => { setEdit({ rule: r.rule, unit: r.unit, warn: String(r.warn), crit: String(r.crit), defaults: [r.default_warn, r.default_crit] }); setSaved(false); setError(null); }}>{t('ar_edit')}</button> },
              ]}
            />
            {edit && (
              <form className="confirm no-print" onSubmit={submit}>
                <h3>{t('ar_edit')} · {ruleLabel(t, edit.rule)}</h3>
                <div className="toolbar">
                  <label className="field"><span>{t('ar_warn')}</span><input inputMode="decimal" value={edit.warn} onChange={(e) => setEdit({ ...edit, warn: e.target.value })} /></label>
                  <label className="field"><span>{t('ar_crit')}</span><input inputMode="decimal" value={edit.crit} onChange={(e) => setEdit({ ...edit, crit: e.target.value })} /></label>
                  <label className="field"><span>{t('supervisorPin')}</span>
                    <input type="password" inputMode="numeric" autoComplete="off" maxLength={12} value={pin} onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))} /></label>
                  <button type="button" className="btn-text" onClick={() => setEdit({ ...edit, warn: String(edit.defaults[0]), crit: String(edit.defaults[1]) })}>{t('ar_reset')}</button>
                </div>
                {Number.isFinite(num(edit.warn)) && <p className="muted rule-preview">{t('ar_preview', value(edit.unit, num(edit.warn)))} {synth(d.calibration, edit.rule, num(edit.warn))}</p>}
                {error && <ErrorState error={error} />}
                <div className="confirm-actions">
                  <button type="button" className="btn-text" disabled={busy} onClick={() => { setEdit(null); setPin(''); }}>{t('cancel')}</button>
                  <button type="submit" className="btn-primary" disabled={busy || pin.length < 4 || !(num(edit.warn) > 0) || !(num(edit.crit) > num(edit.warn))}>{t('ar_save')}</button>
                </div>
              </form>
            )}
          </>
        )}
      </Async>
    </Group>
  );
}
