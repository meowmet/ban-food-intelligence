import React, { useState } from 'react';
import { useApi } from '../lib/api';
import { useLang } from '../lib/i18n';
import { Async, Group, Kpi, Mono, Page, Pill, Segmented, Table } from '../lib/ui';
import { ButcheryForm } from './Entry';
import { LotLink } from './LotPanel';

const toneFor = { ERROR: 'bad', WARNING: 'warn', OK: 'ok' };

export default function Butchery() {
  const { t } = useLang();
  const result = useApi('/butchery');
  const [entering, setEntering] = useState(false);
  return (
    <Page
      title={t('bu_title')}
      subtitle={t('bu_sub')}
      actions={!entering && <button className="btn-primary" onClick={() => setEntering(true)}>{t('en_newButchery')}</button>}
    >
      {entering && <ButcheryForm onDone={result.reload} onCancel={() => setEntering(false)} />}
      <Async result={result}>
        {(ops) => (ops || []).map((op) => <Operation key={op.op_no} op={op} />)}
      </Async>
    </Page>
  );
}

function Operation({ op }) {
  const { t, fmt } = useLang();
  const wc = op.weight_check || {};
  const rejected = op.status === 'REJECTED';
  return (
    <Group
      className={rejected ? 'group-warn' : ''}
      title={`${op.op_no} · ${op.species}`}
      aside={<Pill tone={rejected ? 'bad' : 'ok'}>{rejected ? t('rejected') : t('posted')}</Pill>}
    >
      <ul className="findings">
        {(op.findings || []).map((f) => (
          <li key={f.code} className={`finding finding-${toneFor[f.severity] || 'neutral'}`}>
            <Pill tone={toneFor[f.severity] || 'neutral'}>{t(`f_${f.code}`)}</Pill>
            <span>{f.message}</span>
          </li>
        ))}
      </ul>

      <div className="kpi-row">
        <Kpi label={t('input')} value={fmt.qty(wc.input_kg, 'KG')} sub={`${fmt.qty(op.input_weight_kg, 'KG')} × ${fmt.money(op.purchase_unit_price)}`} />
        <Kpi label={t('cuts')} value={fmt.qty(wc.cuts_kg, 'KG')} sub={wc.by_product_kg ? `${t('byProduct')}: ${fmt.qty(wc.by_product_kg, 'KG')}` : undefined} />
        <Kpi label={t('waste')} value={fmt.qty(wc.waste_kg, 'KG')} />
        <Kpi
          label={t('unrecorded')}
          value={fmt.qty(wc.unrecorded_kg, 'KG')}
          tone={wc.status === 'BALANCED' ? 'ok' : 'bad'}
          sub={`${t('tolerance')} ±${fmt.qty(wc.tolerance_kg, 'KG')}`}
        />
      </div>
      <div className="kpi-row">
        <Kpi label={t('computed')} value={fmt.money(op.total_cost)} />
        {op.declared_total_cost != null && (
          <Kpi label={t('declared')} value={fmt.money(op.declared_total_cost)} tone="warn" sub={`${op.declared_cost_diff > 0 ? '+' : ''}${fmt.money(op.declared_cost_diff)}`} />
        )}
        <Kpi label={t('allocated')} value={fmt.money(op.allocated_total)} tone={op.allocation_ok ? 'ok' : rejected ? undefined : 'bad'} />
      </div>

      {!rejected && <AllocationTables op={op} />}
    </Group>
  );
}

function AllocationTables({ op }) {
  const { t, fmt } = useLang();
  const [method, setMethod] = useState(op.allocation_method || 'VALUE');
  const preview = useApi(`/butchery/allocation?op_no=${encodeURIComponent(op.op_no)}&method=${method}`);
  return (
    <>
      <h3 className="sub-title">{t('allocationStored')}</h3>
      <Table
        rows={op.allocated_cuts || []}
        rowTone={(c) => (c.output_kind === 'WASTE' ? 'muted' : '')}
        columns={[
          { key: 'name', label: t('product'), render: (c) => <>{c.name}<div className="cell-sub"><LotLink lot={c.lot_no} /></div></> },
          { key: 'w', label: t('qty'), align: 'num', render: (c) => fmt.qty(c.yield_weight_kg, 'KG') },
          { key: 'y', label: t('yield'), align: 'num', render: (c) => fmt.pct(c.yield_pct) },
          { key: 'p', label: t('vkp'), align: 'num', render: (c) => (c.output_kind === 'WASTE' ? '–' : fmt.money(c.market_price_per_kg)) },
          { key: 'a', label: t('allocated'), align: 'num', render: (c) => <strong>{fmt.money(c.allocated_cost)}</strong> },
          { key: 'u', label: t('unitCost'), align: 'num', render: (c) => (c.output_kind === 'WASTE' ? '–' : `${fmt.money(c.allocated_unit_cost_per_kg)}/kg`) },
          { key: 'm', label: t('margin'), align: 'num', render: (c) => (c.output_kind === 'WASTE' ? '–' : fmt.pct(c.margin_percentage, 1)) },
        ]}
        footer={
          <tr>
            <td>{t('total')}</td>
            <td className="num">{fmt.qty(op.output_weight_kg, 'KG')}</td>
            <td colSpan={2} />
            <td className="num"><strong>{fmt.money(op.allocated_total)}</strong></td>
            <td colSpan={2} className={op.allocation_ok ? 'pos' : 'neg'}>{op.allocation_ok ? t('ov_alloc_ok') : t('ov_alloc_bad')}</td>
          </tr>
        }
      />

      <div className="sub-head">
        <h3 className="sub-title">{t('allocationPreview')}</h3>
        <Segmented
          label={t('allocationPreview')}
          value={method}
          onChange={setMethod}
          options={[{ value: 'VALUE', label: t('methodValue') }, { value: 'QUANTITY', label: t('methodQty') }]}
        />
      </div>
      <Async result={preview} rows={3}>
        {(p) => (
          <>
            <Table
              rows={p.lines || []}
              columns={[
                { key: 'name', label: t('product') },
                { key: 'w', label: t('qty'), align: 'num', render: (l) => fmt.qty(l.weight_kg, 'KG') },
                { key: 's', label: t('share'), align: 'num', render: (l) => fmt.pct(l.share_pct) },
                { key: 'a', label: t('allocated'), align: 'num', render: (l) => <strong>{fmt.money(l.allocated_cost)}</strong> },
                { key: 'u', label: t('unitCost'), align: 'num', render: (l) => (l.unit_cost_per_kg ? `${fmt.money(l.unit_cost_per_kg)}/kg` : '–') },
              ]}
              footer={
                <tr>
                  <td colSpan={3}>{t('total')}</td>
                  <td className="num"><strong>{fmt.money(p.allocated_total)}</strong></td>
                  <td className={p.sums_exactly ? 'pos' : 'neg'}>{p.sums_exactly ? t('sumsExact', fmt.money(p.total_cost)) : '≠'}</td>
                </tr>
              }
            />
            <p className="group-footer">{t('previewNote')}</p>
          </>
        )}
      </Async>
    </>
  );
}
