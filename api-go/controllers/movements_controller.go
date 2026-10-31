package controllers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
)

// MovementsController records the lot movements that are not created by production,
// butchery, shipment or POS sale: transfer, customer return, waste and rework.
// Every row goes through the stock_movement triggers (sign check, no negative stock,
// recalled/blocked lots cannot be consumed).
type MovementsController struct {
	db *sql.DB
}

func NewMovementsController(db *sql.DB) *MovementsController {
	return &MovementsController{db: db}
}

type TransferRequest struct {
	LotNo         string  `json:"lot_no"`
	FromWarehouse string  `json:"from_warehouse"`
	ToWarehouse   string  `json:"to_warehouse"`
	Qty           float64 `json:"qty"`
	Note          string  `json:"note"`
}

type ReturnRequest struct {
	LotNo        string  `json:"lot_no"`
	Warehouse    string  `json:"warehouse"`
	CustomerCode string  `json:"customer_code"`
	Qty          float64 `json:"qty"`
	Note         string  `json:"note"`
}

type WasteRequest struct {
	LotNo     string  `json:"lot_no"`
	Warehouse string  `json:"warehouse"`
	Qty       float64 `json:"qty"`
	Reason    string  `json:"reason"`
}

type ReworkRequest struct {
	InputLotNo  string  `json:"input_lot_no"`
	Warehouse   string  `json:"warehouse"`
	QtyConsumed float64 `json:"qty_consumed"`
	OutputLotNo string  `json:"output_lot_no"`
	OutputQty   float64 `json:"output_qty"`
	Note        string  `json:"note"`
}

type MovementResultDto struct {
	MovementType string             `json:"movement_type"`
	LotNo        string             `json:"lot_no"`
	Balances     map[string]float64 `json:"balances"` // warehouse -> balance after the movement
	OutputLotNo  string             `json:"output_lot_no,omitempty"`
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if r.Method != http.MethodPost {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "invalid JSON: "+err.Error())
		return false
	}
	return true
}

// Transfer handles POST /movements/transfer.
func (c *MovementsController) Transfer(w http.ResponseWriter, r *http.Request) {
	var req TransferRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.LotNo == "" || req.FromWarehouse == "" || req.ToWarehouse == "" || req.FromWarehouse == req.ToWarehouse || req.Qty <= 0 {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "lot_no, two different warehouses and qty > 0 are required")
		return
	}
	c.inTx(w, r, func(tx *sql.Tx) (*MovementResultDto, error) {
		lot, cost, err := lotID(r.Context(), tx, req.LotNo)
		if err != nil {
			return nil, err
		}
		if err := wholeUnits(r.Context(), tx, lot, req.Qty); err != nil {
			return nil, err
		}
		from, err := warehouseID(r.Context(), tx, req.FromWarehouse)
		if err != nil {
			return nil, err
		}
		to, err := warehouseID(r.Context(), tx, req.ToWarehouse)
		if err != nil {
			return nil, err
		}
		note := nonEmpty(req.Note, "transfer "+req.FromWarehouse+" -> "+req.ToWarehouse)
		if err := insertMovement(r.Context(), tx, lot, from, "TRANSFER_OUT", -req.Qty, cost, "transfer", nil, note); err != nil {
			return nil, err
		}
		if err := insertMovement(r.Context(), tx, lot, to, "TRANSFER_IN", req.Qty, cost, "transfer", nil, note); err != nil {
			return nil, err
		}
		return &MovementResultDto{MovementType: "TRANSFER", LotNo: req.LotNo,
			Balances: balances(r.Context(), tx, lot, req.FromWarehouse, req.ToWarehouse)}, nil
	})
}

// Return handles POST /movements/return: goods coming back from a customer (also used for
// recalled goods; the recall report counts them as qty_returned for that customer).
func (c *MovementsController) Return(w http.ResponseWriter, r *http.Request) {
	var req ReturnRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.LotNo == "" || req.Warehouse == "" || req.CustomerCode == "" || req.Qty <= 0 {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "lot_no, warehouse, customer_code and qty > 0 are required")
		return
	}
	c.inTx(w, r, func(tx *sql.Tx) (*MovementResultDto, error) {
		lot, cost, err := lotID(r.Context(), tx, req.LotNo)
		if err != nil {
			return nil, err
		}
		if err := wholeUnits(r.Context(), tx, lot, req.Qty); err != nil {
			return nil, err
		}
		wh, err := warehouseID(r.Context(), tx, req.Warehouse)
		if err != nil {
			return nil, err
		}
		var customer int64
		if err := tx.QueryRowContext(r.Context(), `SELECT id FROM customer WHERE code = ?`, req.CustomerCode).Scan(&customer); err != nil {
			return nil, notFound("customer " + req.CustomerCode)
		}
		// lotID holds the lot row lock before any snapshot read: concurrent returns cannot
		// both claim the same delivered goods, even when they go to different warehouses.
		var delivered, returned, forwarded float64
		if err := tx.QueryRowContext(r.Context(), `SELECT
		    (SELECT COALESCE(SUM(sl.qty),0) FROM shipment_line sl JOIN shipment sh ON sh.id=sl.shipment_id WHERE sl.lot_id=? AND sh.customer_id=?),
		    (SELECT COALESCE(SUM(sm.qty),0) FROM stock_movement sm WHERE sm.lot_id=? AND sm.movement_type='RETURN' AND sm.ref_type='customer' AND sm.ref_id=?),
		    (SELECT COALESCE(SUM(sl.qty),0) FROM shipment_line sl JOIN shipment sh ON sh.id=sl.shipment_id WHERE sl.lot_id=? AND sh.from_customer_id=?)`,
			lot, customer, lot, customer, lot, customer).Scan(&delivered, &returned, &forwarded); err != nil {
			return nil, err
		}
		if round3(returned+forwarded+req.Qty) > delivered {
			return nil, movementConflict("return exceeds the customer's recorded delivered quantity")
		}
		if err := insertMovement(r.Context(), tx, lot, wh, "RETURN", req.Qty, cost, "customer", &customer,
			nonEmpty(req.Note, "return from "+req.CustomerCode)); err != nil {
			return nil, err
		}
		return &MovementResultDto{MovementType: "RETURN", LotNo: req.LotNo, Balances: balances(r.Context(), tx, lot, req.Warehouse)}, nil
	})
}

// Waste handles POST /movements/waste (fire / disposal).
func (c *MovementsController) Waste(w http.ResponseWriter, r *http.Request) {
	var req WasteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.LotNo == "" || req.Warehouse == "" || req.Qty <= 0 || strings.TrimSpace(req.Reason) == "" {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "lot_no, warehouse, qty > 0 and a reason are required")
		return
	}
	c.inTx(w, r, func(tx *sql.Tx) (*MovementResultDto, error) {
		lot, cost, err := lotID(r.Context(), tx, req.LotNo)
		if err != nil {
			return nil, err
		}
		if err := wholeUnits(r.Context(), tx, lot, req.Qty); err != nil {
			return nil, err
		}
		wh, err := warehouseID(r.Context(), tx, req.Warehouse)
		if err != nil {
			return nil, err
		}
		if err := insertMovement(r.Context(), tx, lot, wh, "WASTE", -req.Qty, cost, "waste", nil, req.Reason); err != nil {
			return nil, err
		}
		return &MovementResultDto{MovementType: "WASTE", LotNo: req.LotNo, Balances: balances(r.Context(), tx, lot, req.Warehouse)}, nil
	})
}

// Rework handles POST /rework: consumes part of a lot and creates a new REWORK lot of the
// same product, linked to its input in lot_link (so it is traced forward and backward).
// The new lot carries the consumed value: unit_cost = input cost x qty_consumed / output_qty.
func (c *MovementsController) Rework(w http.ResponseWriter, r *http.Request) {
	var req ReworkRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.InputLotNo == "" || req.OutputLotNo == "" || req.Warehouse == "" || req.QtyConsumed <= 0 || req.OutputQty <= 0 {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "input_lot_no, output_lot_no, warehouse, qty_consumed > 0 and output_qty > 0 are required")
		return
	}
	c.inTx(w, r, func(tx *sql.Tx) (*MovementResultDto, error) {
		in, cost, err := lotID(r.Context(), tx, req.InputLotNo)
		if err != nil {
			return nil, err
		}
		if err := wholeUnits(r.Context(), tx, in, req.QtyConsumed, req.OutputQty); err != nil { // the new lot keeps the unit
			return nil, err
		}
		wh, err := warehouseID(r.Context(), tx, req.Warehouse)
		if err != nil {
			return nil, err
		}
		outCost := cost * req.QtyConsumed / req.OutputQty
		res, err := tx.ExecContext(r.Context(),
			`INSERT INTO lot (lot_no, product_id, origin_type, lot_date, expiry_date, initial_qty, uom_code, unit_cost)
			 SELECT ?, product_id, 'REWORK', CURRENT_DATE, expiry_date, ?, uom_code, ROUND(?, 4) FROM lot WHERE id = ?`,
			req.OutputLotNo, req.OutputQty, outCost, in)
		if err != nil {
			return nil, err
		}
		out, _ := res.LastInsertId()
		note := nonEmpty(req.Note, "rework "+req.InputLotNo+" -> "+req.OutputLotNo)
		if err := insertMovement(r.Context(), tx, in, wh, "REWORK_CONSUME", -req.QtyConsumed, cost, "rework", &out, note); err != nil {
			return nil, err
		}
		if err := insertMovement(r.Context(), tx, out, wh, "REWORK_OUTPUT", req.OutputQty, outCost, "rework", &out, note); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(r.Context(),
			`INSERT INTO lot_link (parent_lot_id, child_lot_id, link_type, qty_consumed, qty_produced, ref_type, ref_id)
			 VALUES (?, ?, 'REWORK', ?, ?, 'rework', ?)`, in, out, req.QtyConsumed, req.OutputQty, out); err != nil {
			return nil, err
		}
		return &MovementResultDto{MovementType: "REWORK", LotNo: req.InputLotNo, OutputLotNo: req.OutputLotNo,
			Balances: balances(r.Context(), tx, in, req.Warehouse)}, nil
	})
}

// --- helpers ---------------------------------------------------------------

type notFoundError string
type movementConflict string
type invalidMovement string

func (e invalidMovement) Error() string { return string(e) }

func (e movementConflict) Error() string { return string(e) }

func (e notFoundError) Error() string { return string(e) + " not found" }
func notFound(what string) error      { return notFoundError(what) }

func (c *MovementsController) inTx(w http.ResponseWriter, r *http.Request, fn func(*sql.Tx) (*MovementResultDto, error)) {
	tx, err := c.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer tx.Rollback()
	res, err := fn(tx)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		var invalid invalidMovement
		if errors.As(err, &invalid) {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", invalid.Error())
			return
		}
		var conflict movementConflict
		if errors.As(err, &conflict) {
			writeAPIError(w, r, http.StatusConflict, "CONFLICT", conflict.Error())
			return
		}
		var nf notFoundError
		if errors.As(err, &nf) {
			writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", err.Error())
			return
		}
		writeDBError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(res)
}

func lotID(ctx context.Context, tx *sql.Tx, lotNo string) (int64, float64, error) {
	var id int64
	var cost float64
	if err := tx.QueryRowContext(ctx, `SELECT id, unit_cost FROM lot WHERE lot_no = ? FOR UPDATE`, lotNo).Scan(&id, &cost); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, 0, err
		}
		return 0, 0, notFound("lot " + lotNo)
	}
	return id, cost, nil
}

// wholeUnits refuses a fraction of a counted unit (packs, pieces); weights may be fractional.
func wholeUnits(ctx context.Context, tx *sql.Tx, lot int64, qty ...float64) error {
	var countable bool
	if err := tx.QueryRowContext(ctx,
		`SELECT NOT u.is_weight FROM lot l JOIN unit_of_measure u ON u.code = l.uom_code WHERE l.id = ?`, lot).Scan(&countable); err != nil {
		return err
	}
	for _, q := range qty {
		if countable && q != math.Trunc(q) {
			return invalidMovement("qty: whole units only for this lot")
		}
	}
	return nil
}

func warehouseID(ctx context.Context, tx *sql.Tx, code string) (int64, error) {
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM warehouse WHERE code = ?`, code).Scan(&id); err != nil {
		return 0, notFound("warehouse " + code)
	}
	return id, nil
}

func insertMovement(ctx context.Context, tx *sql.Tx, lot, wh int64, typ string, qty, cost float64, refType string, refID *int64, note string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO stock_movement (lot_id, warehouse_id, movement_type, qty, unit_cost, ref_type, ref_id, created_by, note)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, lot, wh, typ, qty, cost, refType, refID, nonEmpty(OperatorFrom(ctx), "api"), note)
	return err
}

func balances(ctx context.Context, tx *sql.Tx, lot int64, warehouses ...string) map[string]float64 {
	out := map[string]float64{}
	for _, code := range warehouses {
		var qty float64
		_ = tx.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(sb.qty), 0) FROM stock_balance sb JOIN warehouse w ON w.id = sb.warehouse_id
			 WHERE sb.lot_id = ? AND w.code = ?`, lot, code).Scan(&qty)
		out[code] = qty
	}
	return out
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
