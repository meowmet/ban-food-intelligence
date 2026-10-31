import React from 'react';
import { useApi } from '../lib/api';
import { useLang } from '../lib/i18n';
import { Async, Group, Kpi, Page } from '../lib/ui';
import { FieldSampleKpi } from './AI';
import AssessmentReportButton from './AssessmentReportButton';
import { SOON_DAYS } from './LotPanel';
import { lotBuckets } from './Lots';

const ORDER = 'URT-2026-045';

export default function Overview({ onOpen }) {
  const { t, fmt } = useLang();
  const production = useApi('/production');
  const butchery = useApi('/butchery');
  const sales = useApi('/sales');
  const lots = useApi('/lots');
  const recalls = useApi('/recall');
  const ai = useApi('/ai/summary');

  return (
    <Page title={t('ov_title')} subtitle={t('ov_sub')}>
      <AssessmentReportButton />
      <div className="grid-2">
        <Group title={t('ov_production', ORDER)} aside={<button className="btn-text" onClick={() => onOpen('production')}>›</button>}>
          <Async result={production} rows={2}>
            {(data) => {
              const o = (data || []).find((x) => x.order_no === ORDER);
              if (!o) return <p className="empty">{t('noData')}</p>;
              return (
                <div className="kpi-row">
                  <Kpi label={t('ov_ekp')} value={fmt.money(o.ekp_display)} sub={t('formula', fmt.money(o.total_cost), fmt.num(o.actual_qty, 0), fmt.money(o.ekp_display))} />
                  <Kpi label={t('ov_vkp')} value={fmt.money(o.vkp)} />
                  <Kpi label={t('ov_margin')} value={fmt.pct(o.margin_pct)} tone="ok" />
                  <Kpi label={t('ov_variance')} value={fmt.num(o.qty_variance, 0)} sub={`${fmt.num(o.planned_qty, 0)} → ${fmt.num(o.actual_qty, 0)}`} />
                </div>
              );
            }}
          </Async>
        </Group>

        <Group title={t('ov_butchery')} aside={<button className="btn-text" onClick={() => onOpen('butchery')}>›</button>}>
          <Async result={butchery} rows={2}>
            {(ops) => (
              <div className="kpi-row">
                {(ops || []).map((op) =>
                  op.status === 'POSTED' ? (
                    <Kpi
                      key={op.op_no}
                      label={`${op.op_no} · ${op.allocation_ok ? t('ov_alloc_ok') : t('ov_alloc_bad')}`}
                      value={fmt.money(op.allocated_total)}
                      sub={`${fmt.qty(op.input_weight_kg, 'KG')} = ${fmt.qty(op.cut_weight_kg, 'KG')} + ${fmt.qty(op.waste_weight_kg, 'KG')}`}
                      tone={op.allocation_ok ? 'ok' : 'bad'}
                    />
                  ) : (
                    <Kpi
                      key={op.op_no}
                      label={t('ov_rejected', op.op_no)}
                      value={fmt.money(op.declared_total_cost)}
                      sub={t('ov_rej_sub', fmt.money(op.total_cost), fmt.money(op.declared_cost_diff), fmt.qty(op.weight_diff_kg, 'KG'))}
                      tone="warn"
                    />
                  )
                )}
              </div>
            )}
          </Async>
        </Group>

        <Group title={t('ov_sales')} aside={<button className="btn-text" onClick={() => onOpen('sales')}>›</button>}>
          <Async result={sales} rows={2}>
            {(rows) => {
              const revenue = rows.reduce((a, r) => a + r.line_total, 0);
              const gp = rows.reduce((a, r) => a + r.gross_profit, 0);
              return (
                <div className="kpi-row">
                  <Kpi label={t('ov_revenue')} value={fmt.money(revenue)} sub={t('ov_lines', rows.length)} />
                  <Kpi label={t('ov_gp')} value={fmt.money(gp)} tone="ok" />
                  <Kpi label={t('ov_margin')} value={revenue > 0 ? fmt.pct((100 * gp) / revenue) : '–'} />
                </div>
              );
            }}
          </Async>
        </Group>

        <Group title={t('ov_stock')} aside={<button className="btn-text" onClick={() => onOpen('lots')}>›</button>}>
          <Async result={lots} rows={2}>
            {(rows) => {
              const b = lotBuckets(rows);
              const value = b.stock.reduce((a, r) => a + r.stock_value, 0);
              const soon = new Set(b.soon.map((r) => r.lot_no)).size;
              return (
                <div className="kpi-row">
                  <Kpi label={t('ov_lots_in_stock')} value={fmt.num(new Set(b.stock.map((r) => r.lot_no)).size, 0)} />
                  <Kpi label={t('ov_stock_value')} value={fmt.money(value)} />
                  <Kpi label={t('lots_soon', SOON_DAYS)} value={fmt.num(soon, 0)} tone={soon ? 'warn' : 'ok'} />
                </div>
              );
            }}
          </Async>
        </Group>

        <Group title={t('ov_recall')} aside={<button className="btn-text" onClick={() => onOpen('trace')}>›</button>}>
          <Async result={recalls} rows={1}>
            {(rows) => {
              const active = (rows || []).filter((r) => r.lock_enforced);
              return (
                <div className="kpi-row">
                  <Kpi
                    label={t('ov_active_recalls')}
                    value={fmt.num(active.length, 0)}
                    tone={active.length ? 'bad' : 'ok'}
                    sub={active.length ? active.map((r) => r.recall_code).join(', ') : t('noRecall')}
                  />
                </div>
              );
            }}
          </Async>
        </Group>

        <Group title={t('ov_ai')} aside={<button className="btn-text" onClick={() => onOpen('ai')}>›</button>}>
          <Async result={ai} rows={1}>
            {(s) => {
              const m = s.metrics.find((x) => x.task === 'PRODUCTION' && x.target === 'actual_qty' && x.role === 'MODEL');
              const b = s.metrics.find((x) => x.task === 'PRODUCTION' && x.target === 'actual_qty' && x.model === s.baseline);
              return m ? (
                <div className="kpi-row">
                  <Kpi label={t('ov_ai_packages')} value={fmt.pct(m.mape_pct)} sub={t('ov_vs_baseline', fmt.pct(b?.mape_pct))} />
                  <FieldSampleKpi records={s.records} />
                </div>
              ) : (
                <p className="empty">{t('noData')}</p>
              );
            }}
          </Async>
        </Group>
      </div>
    </Page>
  );
}
