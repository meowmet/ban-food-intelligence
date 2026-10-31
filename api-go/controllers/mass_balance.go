package controllers

// Mass balance of a forward trace: for every lot reached, where its declared quantity went
// according to the stock ledger. A recall needs the answer per unit: how much is at customers,
// sold at the till, wasted, still in our stock (or at the subcontractor), and how much no
// document explains. Quantities used to make other lots are followed in those lots.

import (
	"context"
	"math"
	"sort"
	"strings"
)

// massBalanceTolerance absorbs the 3-decimal rounding of the ledger.
const massBalanceTolerance = 0.001

type MassBalanceLineDto struct {
	LotNo           string   `json:"lot_no"`
	ProductName     string   `json:"product_name"`
	Uom             string   `json:"uom_code"`
	Status          string   `json:"status"`
	Declared        float64  `json:"declared_qty"`     // lot.initial_qty
	UsedInLots      float64  `json:"used_in_lots"`     // production, butchery and rework consumption
	Shipped         float64  `json:"shipped"`          // to tier-1 customers
	Sold            float64  `json:"sold"`             // POS
	Wasted          float64  `json:"wasted"`           // waste / destruction
	Returned        float64  `json:"returned"`         // customer returns, back in stock
	CountDiff       float64  `json:"count_diff"`       // stock-count adjustments, signed
	InStock         float64  `json:"in_stock"`         // ledger balance, all warehouses
	AtSubcontractor float64  `json:"at_subcontractor"` // part of in_stock
	Accounted       float64  `json:"accounted"`        // used + shipped + sold + wasted + in stock - returned
	Unexplained     float64  `json:"unexplained"`      // declared - accounted
	AccountedPct    *float64 `json:"accounted_pct,omitempty"`
}

// MassBalanceTotalDto: the final positions of all lots in one unit (consumption is not one; it
// continues in the lots made from it).
type MassBalanceTotalDto struct {
	Uom             string  `json:"uom_code"`
	AtCustomers     float64 `json:"at_customers"` // shipped - returned
	Sold            float64 `json:"sold"`
	Wasted          float64 `json:"wasted"`
	InStock         float64 `json:"in_stock"`
	AtSubcontractor float64 `json:"at_subcontractor"`
	Unexplained     float64 `json:"unexplained"`
}

type MassBalanceDto struct {
	Lines    []MassBalanceLineDto  `json:"lines"`
	Totals   []MassBalanceTotalDto `json:"totals"`
	Balanced bool                  `json:"balanced"` // every lot fully explained by documents
}

// loadMassBalance computes the balance of the given lots from stock_movement. Transfers cancel
// out inside a lot. An unexplained quantity comes from a stock-count adjustment or from a lot
// whose declared quantity was never booked in.
func loadMassBalance(ctx context.Context, q sqlRunner, lots []TraceLotDto) (*MassBalanceDto, error) {
	out := &MassBalanceDto{Lines: []MassBalanceLineDto{}, Totals: []MassBalanceTotalDto{}, Balanced: true}
	seen := map[string]bool{}
	var order []string
	for _, l := range lots {
		if !seen[l.LotNo] {
			seen[l.LotNo] = true
			order = append(order, l.LotNo)
		}
	}
	if len(order) == 0 {
		return out, nil
	}
	args := make([]interface{}, len(order))
	for i, l := range order {
		args[i] = l
	}
	rows, err := q.QueryContext(ctx,
		`SELECT l.lot_no, p.name, l.uom_code, l.status, l.initial_qty,
		        COALESCE(-SUM(IF(sm.movement_type IN ('PRODUCTION_CONSUME','BUTCHERY_CONSUME','REWORK_CONSUME'), sm.qty, 0)), 0),
		        COALESCE(-SUM(IF(sm.movement_type = 'SHIPMENT', sm.qty, 0)), 0),
		        COALESCE(-SUM(IF(sm.movement_type = 'SALE', sm.qty, 0)), 0),
		        COALESCE(-SUM(IF(sm.movement_type = 'WASTE', sm.qty, 0)), 0),
		        COALESCE(SUM(IF(sm.movement_type = 'RETURN', sm.qty, 0)), 0),
		        COALESCE(SUM(IF(sm.movement_type IN ('ADJUSTMENT_IN','ADJUSTMENT_OUT'), sm.qty, 0)), 0),
		        COALESCE(SUM(sm.qty), 0),
		        COALESCE(SUM(IF(w.warehouse_type = 'SUBCONTRACTOR', sm.qty, 0)), 0)
		 FROM lot l JOIN product p ON p.id = l.product_id
		 LEFT JOIN stock_movement sm ON sm.lot_id = l.id
		 LEFT JOIN warehouse w ON w.id = sm.warehouse_id
		 WHERE l.lot_no IN (?`+strings.Repeat(",?", len(order)-1)+`)
		 GROUP BY l.id, l.lot_no, p.name, l.uom_code, l.status, l.initial_qty`, args...)
	if err != nil {
		return nil, err
	}
	byLot := map[string]MassBalanceLineDto{}
	for rows.Next() {
		var m MassBalanceLineDto
		if err := rows.Scan(&m.LotNo, &m.ProductName, &m.Uom, &m.Status, &m.Declared, &m.UsedInLots, &m.Shipped, &m.Sold,
			&m.Wasted, &m.Returned, &m.CountDiff, &m.InStock, &m.AtSubcontractor); err != nil {
			rows.Close()
			return nil, err
		}
		byLot[m.LotNo] = m
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	totals := map[string]*MassBalanceTotalDto{}
	for _, lot := range order {
		m, ok := byLot[lot]
		if !ok {
			continue
		}
		m.Accounted = round3(m.UsedInLots + m.Shipped + m.Sold + m.Wasted + m.InStock - m.Returned)
		m.Unexplained = round3(m.Declared - m.Accounted)
		if m.Declared > 0 {
			m.AccountedPct = ptr(round(100*m.Accounted/m.Declared, 2))
		}
		if math.Abs(m.Unexplained) > massBalanceTolerance {
			out.Balanced = false
		}
		out.Lines = append(out.Lines, m)
		t := totals[m.Uom]
		if t == nil {
			t = &MassBalanceTotalDto{Uom: m.Uom}
			totals[m.Uom] = t
		}
		t.AtCustomers += m.Shipped - m.Returned
		t.Sold += m.Sold
		t.Wasted += m.Wasted
		t.InStock += m.InStock
		t.AtSubcontractor += m.AtSubcontractor
		t.Unexplained += m.Unexplained
	}
	for _, t := range totals {
		t.AtCustomers, t.Sold, t.Wasted = round3(t.AtCustomers), round3(t.Sold), round3(t.Wasted)
		t.InStock, t.AtSubcontractor, t.Unexplained = round3(t.InStock), round3(t.AtSubcontractor), round3(t.Unexplained)
		out.Totals = append(out.Totals, *t)
	}
	sort.Slice(out.Totals, func(i, j int) bool { return out.Totals[i].Uom < out.Totals[j].Uom })
	return out, nil
}
