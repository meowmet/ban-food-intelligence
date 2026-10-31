import { describe, expect, it } from 'vitest';
import { curveAt, feedbackByTask, syntheticAt, wilson } from '../views/AlertQuality.jsx';

describe('alert accuracy', () => {
  it('gives a wide 95 % interval for a few reviews and none without any', () => {
    const [lo, hi] = wilson(8, 10);
    expect(lo).toBeCloseTo(0.49, 2);
    expect(hi).toBeCloseTo(0.943, 2);
    expect(wilson(0, 0)).toBeNull();
    const [lo2, hi2] = wilson(80, 100);
    expect(hi2 - lo2).toBeLessThan(hi - lo); // more reviews, narrower interval
  });

  it('measures recall from real problems and the problems found by hand', () => {
    const [cost] = feedbackByTask(
      [{ task: 'PURCHASE_COST', alerts: 5, confirmed: 3, false_alarms: 1, confirmed_loss_eur: 90 }],
      [{ task: 'PURCHASE_COST', findings: 1, est_loss_eur: 12.5 }],
    );
    expect(cost).toMatchObject({ confirmed: 3, false_alarms: 1, missed: 1, missed_loss_eur: 12.5 });
    expect(cost.precision).toBeCloseTo(0.75);
    expect(cost.recall).toBeCloseTo(0.75);
    expect(cost.recall_ci[0]).toBeLessThan(0.75);
    const [onlyMissed] = feedbackByTask([], [{ task: 'LOT_MOVEMENT', findings: 2, est_loss_eur: 0 }]);
    expect(onlyMissed).toMatchObject({ precision: null, recall: 0 }); // nothing caught yet
  });
});

describe('synthetic stress test lookup', () => {
  const curve = [
    { threshold: 1.5, precision: 0.91, recall: 0.64, false_alarms_per_100: 0.4 },
    { threshold: 1.0, precision: 0.36, recall: 0.86, false_alarms_per_100: 9.2 },
    { threshold: 2.0, precision: 1, recall: 0.39, false_alarms_per_100: 0 },
  ];

  it('interpolates between tested thresholds and clamps outside them', () => {
    expect(curveAt(curve, 1.25, 'recall')).toBeCloseTo(0.75);
    expect(curveAt(curve, 0.2, 'recall')).toBe(0.86);
    expect(curveAt(curve, 9, 'false_alarms_per_100')).toBe(0);
  });

  it('reads the yield curve, the price table by volatility, and nothing for the count', () => {
    const calibration = {
      butchery_yield: { curve },
      purchase_cost: { by_price_noise: [
        { sigma: 0.03, curve: [{ threshold: 10, false_alarms_per_100: 0.5, detected_by_jump_pct: { '10-20': 0.87 } }] },
      ] },
    };
    expect(syntheticAt(calibration, 'BUTCHERY_YIELD', 1.5)).toMatchObject({ kind: 'yield', precision: 0.91, falseAlarms: 0.4 });
    expect(syntheticAt(calibration, 'PURCHASE_COST', 10).bySigma).toEqual([{ sigma: 0.03, falseAlarms: 0.5, caught: 0.87 }]);
    expect(syntheticAt(calibration, 'COUNT_SHORTAGE', 10)).toBeNull();
  });
});
