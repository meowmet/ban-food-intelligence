import React, { useState } from 'react';
import { useLang } from '../lib/i18n';
import { useOpenLot } from './LotPanel';

// Layered flow of a trace: lots by depth (raw material on the left), then where the goods went:
// customers by distribution tier, and the stock, till, waste and unexplained positions from the
// mass balance. Widths are not proportional on purpose: kg and packs are not one unit. Every
// arrow carries its recorded quantity; the tables below are the same data as a table view.

const NODE_W = 176;
const NODE_H = 52;
const COL_GAP = 136; // room for a label like "100,00 kg → 85 paket"
const ROW_GAP = 14;

const clip = (s, n) => (s && s.length > n ? `${s.slice(0, n - 1)}…` : s || '');

/** Nodes (with column) and edges of a trace response; exported for tests. */
export function buildFlow(tr, t, fmt) {
  const nodes = new Map();
  const edges = [];
  const add = (id, node) => {
    if (!nodes.has(id)) nodes.set(id, { id, ...node });
    return nodes.get(id);
  };
  const edge = (from, to, qty, uom, title) => {
    const e = edges.find((x) => x.from === from && x.to === to && x.uom === uom && x.qty != null);
    if (e) e.qty += qty;
    else edges.push({ from, to, qty, uom, title });
  };
  const forward = tr.direction === 'forward';
  const depth = {};
  for (const l of tr.lots) depth[l.lot_no] = Math.min(depth[l.lot_no] ?? Infinity, l.depth);
  const maxDepth = Math.max(0, ...Object.values(depth));
  for (const l of tr.lots) {
    if (nodes.has(`lot:${l.lot_no}`)) continue;
    const blocked = l.status === 'RECALLED' || l.status === 'BLOCKED';
    add(`lot:${l.lot_no}`, {
      kind: 'lot', lot: l.lot_no, col: forward ? depth[l.lot_no] : maxDepth - depth[l.lot_no], bad: blocked,
      title: l.lot_no, sub: `${fmt.qty(l.qty, l.uom_code)} · ${l.product_name}`,
      badge: blocked ? t(l.status === 'RECALLED' ? 'fl_recalled' : 'fl_blocked') : '',
      tip: [l.lot_no, l.product_name, fmt.qty(l.qty, l.uom_code), l.supplier_name, l.status].filter(Boolean).join(' · '),
    });
  }
  for (const k of tr.links) {
    if (!nodes.has(`lot:${k.input_lot_no}`) || !nodes.has(`lot:${k.output_lot_no}`)) continue;
    edges.push({ from: `lot:${k.input_lot_no}`, to: `lot:${k.output_lot_no}`,
      label: `${fmt.qty(k.qty_consumed, k.input_uom)} → ${fmt.qty(k.qty_produced, k.output_uom)}`,
      title: `${k.document_no}${k.subcontractor ? ` · ${k.subcontractor}` : ''}` });
  }
  if (forward) {
    const dest = maxDepth + 1;
    const codeByName = {};
    for (const s of (tr.shipments || []).filter((x) => x.tier === 1)) {
      codeByName[s.customer_name] = s.customer_code;
      add(`cust:${s.customer_code}`, { kind: 'customer', col: dest, title: s.customer_name, sub: `${s.customer_code} · ${t('tier')} 1`, tip: s.customer_name });
      edge(`lot:${s.lot_no}`, `cust:${s.customer_code}`, s.qty, s.uom_code, `${s.lot_no} → ${s.customer_name}`);
    }
    for (const s of (tr.shipments || []).filter((x) => x.tier > 1)) {
      const via = codeByName[s.received_via];
      add(`cust:${s.customer_code}`, { kind: 'customer', col: dest + s.tier - 1, title: s.customer_name, sub: `${s.customer_code} · ${t('tier')} ${s.tier}`, tip: s.customer_name });
      edge(via ? `cust:${via}` : `lot:${s.lot_no}`, `cust:${s.customer_code}`, s.qty, s.uom_code, `${s.lot_no}: ${s.received_via || ''} → ${s.customer_name}`);
      if (s.tier === 2) codeByName[s.customer_name] = s.customer_code;
    }
    const positions = [
      ['in_stock', 'stock', 'fl_stock'], ['sold', 'sold', 'fl_sold'], ['wasted', 'waste', 'fl_waste'], ['unexplained', 'gap', 'fl_gap'],
    ];
    for (const m of tr.mass_balance?.lines || []) {
      for (const [field, kind, label] of positions) {
        const qty = m[field];
        if (!(Math.abs(qty) > 0.0005)) continue;
        add(`pos:${kind}`, { kind, col: dest, title: t(label), sub: '', bad: kind === 'gap', tip: t(label) });
        edge(`lot:${m.lot_no}`, `pos:${kind}`, qty, m.uom_code, `${m.lot_no} → ${t(label)}`);
      }
    }
  }
  for (const e of edges) if (e.label == null) e.label = fmt.qty(e.qty, e.uom);
  return { nodes: [...nodes.values()], edges };
}

/** Column order by the mean row of each node's sources (one pass), so arrows cross less. */
export function layoutFlow(nodes, edges) {
  const cols = [];
  for (const n of nodes) (cols[n.col] ||= []).push(n);
  const row = {};
  cols.forEach((col = [], c) => {
    if (c > 0) {
      const score = (n) => {
        const src = edges.filter((e) => e.to === n.id && row[e.from] != null).map((e) => row[e.from]);
        return src.length ? src.reduce((a, b) => a + b, 0) / src.length : Infinity;
      };
      col.sort((a, b) => score(a) - score(b));
    }
    col.forEach((n, i) => { row[n.id] = i; });
  });
  const rows = Math.max(1, ...cols.map((c) => (c ? c.length : 0)));
  const pos = {};
  cols.forEach((col = [], c) => {
    const offset = ((rows - col.length) * (NODE_H + ROW_GAP)) / 2;
    col.forEach((n, i) => {
      pos[n.id] = { x: c * (NODE_W + COL_GAP), y: offset + i * (NODE_H + ROW_GAP) };
    });
  });
  return { pos, width: cols.length * NODE_W + (cols.length - 1) * COL_GAP, height: rows * (NODE_H + ROW_GAP) - ROW_GAP };
}

export default function TraceFlow({ tr }) {
  const { t, fmt } = useLang();
  const openLot = useOpenLot();
  const [hover, setHover] = useState(null);
  const { nodes, edges } = buildFlow(tr, t, fmt);
  if (nodes.length === 0) return null;
  const { pos, width, height } = layoutFlow(nodes, edges);
  const linked = (e) => hover && (e.from === hover || e.to === hover);
  const near = new Set(hover ? [hover, ...edges.filter(linked).flatMap((e) => [e.from, e.to])] : []);
  const pad = 4;

  return (
    <div className="flow-scroll">
      <svg className="flow" style={{ maxWidth: width + 2 * pad }} viewBox={`${-pad} ${-pad} ${width + 2 * pad} ${height + 2 * pad}`}
        role="group" aria-label={t('fl_aria', tr.root_lot_number)}>
        {edges.map((e, i) => {
          const a = pos[e.from];
          const b = pos[e.to];
          if (!a || !b) return null;
          const x1 = a.x + NODE_W;
          const y1 = a.y + NODE_H / 2;
          const x2 = b.x;
          const y2 = b.y + NODE_H / 2;
          const xm = (x1 + x2) / 2;
          const cls = hover ? (linked(e) ? 'flow-edge on' : 'flow-edge dim') : 'flow-edge';
          return (
            <g key={i} className={cls}>
              <title>{e.title}</title>
              <path d={`M${x1},${y1} C${xm},${y1} ${xm},${y2} ${x2 - 6},${y2}`} />
              <path className="flow-arrow" d={`M${x2 - 7},${y2 - 4} L${x2},${y2} L${x2 - 7},${y2 + 4} Z`} />
              <text x={xm} y={(y1 + y2) / 2 - 4} textAnchor="middle" className="flow-label">{e.label}</text>
            </g>
          );
        })}
        {nodes.map((n) => {
          const p = pos[n.id];
          const open = n.kind === 'lot' && openLot ? () => openLot(n.lot) : null;
          return (
            <g key={n.id} transform={`translate(${p.x},${p.y})`}
              className={`flow-node flow-${n.kind}${n.bad ? ' flow-bad' : ''}${hover && !near.has(n.id) ? ' dim' : ''}`}
              onMouseEnter={() => setHover(n.id)} onMouseLeave={() => setHover(null)}
              onFocus={() => setHover(n.id)} onBlur={() => setHover(null)}
              {...(open ? { role: 'button', tabIndex: 0, onClick: open, onKeyDown: (e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), open()) } : {})}>
              <title>{n.tip}</title>
              <rect width={NODE_W} height={NODE_H} rx={8} />
              <text x={12} y={21} className={n.kind === 'lot' ? 'flow-title flow-mono' : 'flow-title'}>{clip(n.title, n.kind === 'lot' ? 18 : 22)}</text>
              {n.badge && <text x={NODE_W - 10} y={21} textAnchor="end" className="flow-badge">{n.badge}</text>}
              {n.sub && <text x={12} y={39} className="flow-sub">{clip(n.sub, 28)}</text>}
            </g>
          );
        })}
      </svg>
    </div>
  );
}
