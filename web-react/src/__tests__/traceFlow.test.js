import { describe, expect, it } from 'vitest';
import { buildFlow, layoutFlow } from '../views/TraceFlow.jsx';

const t = (k, ...a) => [k, ...a].join(':');
const fmt = { qty: (v, u) => `${v} ${u}` };

const forward = {
  direction: 'forward', root_lot_number: 'ET-1',
  lots: [
    { depth: 0, lot_no: 'ET-1', product_name: 'Et', qty: 100, uom_code: 'KG', status: 'ACTIVE' },
    { depth: 1, lot_no: 'SUC-1', product_name: 'Sucuk', qty: 85, uom_code: 'PAKET', status: 'RECALLED' },
  ],
  links: [{ document_no: 'URT-1', input_lot_no: 'ET-1', qty_consumed: 100, input_uom: 'KG', output_lot_no: 'SUC-1', qty_produced: 85, output_uom: 'PAKET' }],
  shipments: [
    { tier: 1, customer_code: 'A', customer_name: 'Market A', lot_no: 'SUC-1', qty: 30, uom_code: 'PAKET' },
    { tier: 1, customer_code: 'C', customer_name: 'Market C', lot_no: 'SUC-1', qty: 20, uom_code: 'PAKET' },
    { tier: 1, customer_code: 'C', customer_name: 'Market C', lot_no: 'SUC-1', qty: 10, uom_code: 'PAKET' },
    { tier: 2, customer_code: 'R', customer_name: 'Lokanta', received_via: 'Market C', lot_no: 'SUC-1', qty: 10, uom_code: 'PAKET' },
  ],
  mass_balance: { lines: [
    { lot_no: 'ET-1', uom_code: 'KG', in_stock: 0, sold: 0, wasted: 0, unexplained: 0 },
    { lot_no: 'SUC-1', uom_code: 'PAKET', in_stock: 25, sold: 0, wasted: 0, unexplained: 0 },
  ] },
};

describe('trace flow', () => {
  it('places lots by depth, then customers by tier and the final positions', () => {
    const { nodes, edges } = buildFlow(forward, t, fmt);
    const col = Object.fromEntries(nodes.map((n) => [n.id, n.col]));
    expect(col).toEqual({ 'lot:ET-1': 0, 'lot:SUC-1': 1, 'cust:A': 2, 'cust:C': 2, 'cust:R': 3, 'pos:stock': 2 });
    expect(nodes.find((n) => n.id === 'lot:SUC-1')).toMatchObject({ bad: true, badge: 'fl_recalled' });
    const label = (from, to) => edges.find((e) => e.from === from && e.to === to)?.label;
    expect(label('lot:ET-1', 'lot:SUC-1')).toBe('100 KG → 85 PAKET');
    expect(label('lot:SUC-1', 'cust:C')).toBe('30 PAKET'); // two deliveries, one arrow
    expect(label('cust:C', 'cust:R')).toBe('10 PAKET'); // tier 2 comes from the customer who forwarded it
    expect(label('lot:SUC-1', 'pos:stock')).toBe('25 PAKET');
    expect(edges.some((e) => e.to === 'pos:sold' || e.to === 'pos:gap')).toBe(false); // zero positions are not drawn
  });

  it('puts raw material on the left in a backward trace and draws no destinations', () => {
    const back = { ...forward, direction: 'backward', lots: [
      { depth: 0, lot_no: 'SUC-1', product_name: 'Sucuk', qty: 85, uom_code: 'PAKET', status: 'ACTIVE' },
      { depth: 1, lot_no: 'ET-1', product_name: 'Et', qty: 100, uom_code: 'KG', status: 'ACTIVE', supplier_name: 'Tedarikçi' },
    ] };
    const { nodes } = buildFlow(back, t, fmt);
    expect(nodes.map((n) => [n.id, n.col])).toEqual([['lot:SUC-1', 1], ['lot:ET-1', 0]]);
  });

  it('lays out columns without overlapping boxes', () => {
    const { nodes, edges } = buildFlow(forward, t, fmt);
    const { pos, width, height } = layoutFlow(nodes, edges);
    const seen = new Set();
    for (const n of nodes) {
      const key = `${pos[n.id].x}:${pos[n.id].y}`;
      expect(seen.has(key)).toBe(false);
      seen.add(key);
    }
    expect(width).toBeGreaterThan(0);
    expect(height).toBeGreaterThan(0);
    expect(pos['cust:R'].x).toBeGreaterThan(pos['cust:C'].x);
  });
});
