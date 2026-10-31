import { describe, expect, it } from 'vitest';
import { alertValue, isOpen } from '../views/Alerts.jsx';
import { feedbackByTask } from '../views/AlertQuality.jsx';

const fmt = {
  num: (v, d = 2) => Number(v).toFixed(d),
  unit: (u) => (u === 'PAKET' ? 'paket' : (u || '').toLowerCase()),
  qty: (v, u) => `${Number(v).toFixed(u === 'KG' ? 2 : 0)} ${u === 'PAKET' ? 'paket' : (u || '').toLowerCase()}`,
  money: (v) => `${Number(v).toFixed(2)} €`,
};

describe('alert inbox', () => {
  it('counts acknowledged alerts as open and closed ones as done', () => {
    expect(['OPEN', 'ACKNOWLEDGED', 'RESOLVED', 'FALSE_ALARM'].map((status) => isOpen({ status }))).toEqual([true, true, false, false]);
  });

  it('measures precision per task over closed alerts only, across detector versions', () => {
    const rows = feedbackByTask([
      { task: 'PURCHASE_COST', alerts: 3, confirmed: 1, false_alarms: 1, confirmed_loss_eur: 66.67 },
      { task: 'PURCHASE_COST', alerts: 1, confirmed: 1, false_alarms: 0, confirmed_loss_eur: 10 },
      { task: 'LOT_MOVEMENT', alerts: 2, confirmed: 0, false_alarms: 0, confirmed_loss_eur: 0 },
    ]);
    const cost = rows.find((r) => r.task === 'PURCHASE_COST');
    expect(cost).toMatchObject({ alerts: 4, confirmed: 2, false_alarms: 1 });
    expect(cost.precision).toBeCloseTo(2 / 3);
    expect(cost.confirmed_loss_eur).toBeCloseTo(76.67);
    expect(rows.find((r) => r.task === 'LOT_MOVEMENT').precision).toBeNull(); // nothing closed yet
  });

  it('shows each value in its own unit', () => {
    expect(alertValue({ task: 'PRODUCTION_YIELD' }, 84.6, fmt)).toBe('84.6 paket');
    expect(alertValue({ task: 'PRODUCTION_YIELD' }, 70, fmt)).toBe('70 paket');
    expect(alertValue({ task: 'BUTCHERY_YIELD' }, 3.9, fmt)).toBe('3.90 kg');
    expect(alertValue({ task: 'PURCHASE_COST', uom_code: 'KG' }, 31, fmt)).toBe('31.00 €/kg');
    expect(alertValue({ task: 'LOT_MOVEMENT', uom_code: 'PAKET' }, 2, fmt)).toBe('2 paket');
    expect(alertValue({ task: 'BUTCHERY_YIELD' }, null, fmt)).toBe('–');
  });
});
