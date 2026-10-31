import { describe, expect, it } from 'vitest';
import { freezeState } from '../views/Entry.jsx';

const live = { weight_kg: 25, interval_level: 0.8, parts: [{ part: 'but', kg: 6.6 }] };
const empty = [{ weight_kg: '' }, { weight_kg: ' ' }];

describe('butchery forecast freeze', () => {
  it('can freeze the live forecast only before any part is weighed', () => {
    expect(freezeState(live, 25, empty)).toMatchObject({ frozen: null, shown: live, canFreeze: true, weighed: false });
    expect(freezeState(live, 25, [{ weight_kg: '6,5' }, { weight_kg: '' }])).toMatchObject({ canFreeze: false, weighed: true });
    expect(freezeState(null, 25, empty).canFreeze).toBe(false);
    expect(freezeState({ ...live, parts: [] }, 25, empty).canFreeze).toBe(false);
  });

  it('compares with the frozen forecast and flags another input weight', () => {
    const frozenFc = { weight_kg: 25.4, interval_level: 0.8, parts: [{ part: 'but', kg: 6.7 }] };
    const fc = { ...live, frozen: { input_weight_kg: 25.4, forecast: frozenFc } };
    expect(freezeState(fc, 25.4, empty)).toMatchObject({ shown: frozenFc, otherKg: false, canFreeze: false });
    expect(freezeState(fc, 25, empty).otherKg).toBe(true);
    expect(freezeState(fc, NaN, empty).otherKg).toBe(true);
  });
});
