import React, { useState } from 'react';
import { useApi } from '../lib/api';
import { useLang } from '../lib/i18n';
import { Async, Group, Kpi, Mono, Page, Pill, Table } from '../lib/ui';
import { ForecastKpis, ProductionOrderForm, ReceiveProductionForm } from './Entry';
import { LotLink } from './LotPanel';

const isOpen = (o) => o.status === 'PLANNED' || o.status === 'MATERIAL_SENT';

export default function Production() {
  const { t } = useLang();
  const result = useApi('/production');
  const [entering, setEntering] = useState(false);
  return (
    <Page
      title={t('pr_title')}
      subtitle={t('pr_sub')}
      actions={!entering && <button className="btn-primary" onClick={() => setEntering(true)}>{t('pr_newOrder')}</button>}
    >
      {entering && <ProductionOrderForm onDone={result.reload} onCancel={() => setEntering(false)} />}
      <Async result={result}>
        {(orders) =>
          (orders || []).map((o) =>
            isOpen(o) ? <OpenOrder key={o.order_no} o={o} onReceived={result.reload} /> : <ReceivedOrder key={o.order_no} o={o} />
          )
        }
      </Async>
    </Page>
  );
}

/** Material at the subcontractor: cost so far and the AI forecast; receive the finished goods here. */
function OpenOrder({ o, onReceived }) {
  const { t, fmt } = useLang();
  const [receiving, setReceiving] = useState(false);
  return (
    <Group
      title={`${o.order_no} · ${o.product_name}`}
      aside={<Pill tone="warn">{t('pr_atSubcontractor')}</Pill>}
      footer={o.forecast ? t('pr_fcNote') : null}
    >
      <div className="kpi-row">
        <Kpi label={t('planned')} value={fmt.num(o.planned_qty, 0)} />
        <Kpi label={t('pr_inputKg')} value={fmt.qty(o.input_kg, 'KG')} />
        <Kpi label={t('materialCost')} value={fmt.money(o.material_cost)} />
      </div>
      <ForecastKpis forecast={o.forecast} />
      {receiving ? (
        <ReceiveProductionForm order={o} onDone={onReceived} onCancel={() => setReceiving(false)} />
      ) : (
        <div className="confirm-actions entry-pad">
          <button className="btn-primary" onClick={() => setReceiving(true)}>{t('pr_receive')}</button>
        </div>
      )}
    </Group>
  );
}

function ReceivedOrder({ o }) {
  const { t, fmt } = useLang();
  return (
    <Group
      title={`${o.order_no} → ${o.output_lot_number || '–'} · ${o.product_name}`}
      aside={<Pill tone="neutral">{o.status}</Pill>}
      footer={o.forecast ? t('pr_fcNote') : null}
    >
      <div className="kpi-row">
        <Kpi label={t('ekp')} value={fmt.money(o.ekp_display)} sub={t('formula', fmt.money(o.total_cost), fmt.num(o.actual_qty, 0), fmt.money(o.ekp))} />
        <Kpi label={t('vkp')} value={fmt.money(o.vkp)} />
        <Kpi label={t('gpUnit')} value={fmt.money(o.gross_profit_per_unit)} tone="ok" />
        <Kpi label={t('margin')} value={fmt.pct(o.margin_pct)} tone="ok" />
      </div>
      <div className="kpi-row">
        <Kpi label={t('planned')} value={fmt.num(o.planned_qty, 0)} />
        <Kpi label={t('actual')} value={fmt.num(o.actual_qty, 0)} />
        <Kpi label={t('variance')} value={fmt.num(o.qty_variance, 0)} tone={o.qty_variance < 0 ? 'warn' : undefined} sub={fmt.pct(o.fulfilment_pct)} />
        <Kpi label={t('revenueVkp')} value={fmt.money(o.revenue_at_vkp)} sub={`${t('gpTotal')}: ${fmt.money(o.gross_profit_total)}`} />
      </div>
      <ForecastKpis forecast={o.forecast} />
      <Table
        rows={o.breakdown || []}
        columns={[
          { key: 'lot_no', label: `${t('lot')} / ${t('document')}`, render: (r) => (r.is_service ? <Mono>{r.lot_no || '–'}</Mono> : <LotLink lot={r.lot_no} />) },
          { key: 'cost_element', label: t('costElement') },
          { key: 'qty', label: t('qty'), align: 'num', render: (r) => fmt.qty(r.qty, r.uom_code) },
          { key: 'unit_cost', label: t('unitPrice'), align: 'num', render: (r) => fmt.money(r.unit_cost) },
          { key: 'amount', label: t('amount'), align: 'num', render: (r) => <strong>{fmt.money(r.amount)}</strong> },
        ]}
        footer={
          <tr>
            <td colSpan={4}>
              {t('totalCost')} ({t('materialCost')} {fmt.money(o.material_cost)} + {t('serviceCost')} {fmt.money(o.service_cost)})
            </td>
            <td className="num"><strong>{fmt.money(o.total_cost)}</strong></td>
          </tr>
        }
      />
    </Group>
  );
}
