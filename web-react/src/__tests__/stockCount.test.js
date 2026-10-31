import { describe, expect, it } from 'vitest';
import { countLines, parseCount } from '../views/StockCount.jsx';

const rows = [
  { lot_no: 'SUC-1', uom_code: 'PAKET', qty: 20, unit_cost: 18.16 },
  { lot_no: 'KZB-1', uom_code: 'KG', qty: 4.5, unit_cost: 15 },
  { lot_no: 'SUC-2', uom_code: 'PAKET', qty: 5, unit_cost: 18.16 },
];

describe('stock count form', () => {
  it('reads quantities with a comma or a dot and treats an empty field as not counted', () => {
    expect(parseCount('')).toBeNull();
    expect(parseCount(' 4,25 ')).toBe(4.25);
    expect(parseCount('17')).toBe(17);
    expect(parseCount('-1')).toBeNaN();
    expect(parseCount('abc')).toBeNaN();
  });

  it('sends only counted lots, with the balance seen and a reason for each difference', () => {
    const { lines, problems } = countLines(rows, { 'SUC-1': { counted: '17', reason: ' Kırık ' }, 'KZB-1': { counted: '4,5' } });
    expect(problems).toEqual({});
    expect(lines.map(({ lot_no, counted_qty, expected_qty, reason, diff }) => ({ lot_no, counted_qty, expected_qty, reason, diff }))).toEqual([
      { lot_no: 'SUC-1', counted_qty: 17, expected_qty: 20, reason: 'Kırık', diff: -3 },
      { lot_no: 'KZB-1', counted_qty: 4.5, expected_qty: 4.5, reason: '', diff: 0 },
    ]);
    expect(lines[0].value).toBeCloseTo(-54.48);
  });

  it('blocks a difference without reason and half a pack', () => {
    const { problems } = countLines(rows, { 'SUC-1': { counted: '18' }, 'SUC-2': { counted: '4,5', reason: 'x' }, 'KZB-1': { counted: '4,25', reason: 'tartı' } });
    expect(problems).toEqual({ 'SUC-1': 'sc_reasonNeeded', 'SUC-2': 'sc_whole' });
  });
});
