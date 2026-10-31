package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
)

// ShipmentController records deliveries to customers (POST /shipments) and lists them
// (GET /shipments, GET /customers for the form).
//
// A first-tier shipment leaves one of our warehouses. The shipment_line trigger posts its
// SHIPMENT movement, so the stock triggers refuse recalled, blocked and expired lots and
// negative stock. A second-tier shipment is a customer's onward delivery that was reported
// to us: it moves none of our stock and is capped at what that customer received minus its
// earlier onward deliveries and returns. Forward trace and recall then show the next buyer
// as RECORDED; nothing is inferred for deliveries that were never recorded.
type ShipmentController struct {
	db *sql.DB
}

func NewShipmentController(db *sql.DB) *ShipmentController {
	return &ShipmentController{db: db}
}

type ShipmentLineRequest struct {
	LotNo     string   `json:"lot_no"`
	Qty       float64  `json:"qty"`
	UnitPrice *float64 `json:"unit_price,omitempty"` // default for our own stock: the product's VKP
}

type ShipmentRequest struct {
	ShipmentNo       string                `json:"shipment_no"`
	CustomerCode     string                `json:"customer_code"`
	FromWarehouse    string                `json:"from_warehouse"`     // first tier, default ANA-DEPO
	FromCustomerCode string                `json:"from_customer_code"` // second tier: the customer who forwarded the goods
	ShipDate         string                `json:"ship_date"`
	DeliveryNoteNo   string                `json:"delivery_note_no"`
	Lines            []ShipmentLineRequest `json:"lines"`
}

type ShipmentLineDto struct {
	LotNo       string   `json:"lot_no"`
	SKU         string   `json:"sku"`
	ProductName string   `json:"product_name"`
	Qty         float64  `json:"qty"`
	UomCode     string   `json:"uom_code"`
	UnitPrice   *float64 `json:"unit_price"`
	LineTotal   *float64 `json:"line_total"`
}

type ShipmentDto struct {
	ShipmentNo       string            `json:"shipment_no"`
	ShipDate         string            `json:"ship_date"`
	DeliveryNoteNo   string            `json:"delivery_note_no"`
	Tier             int               `json:"tier"` // 1 = from our warehouse, 2 = onward delivery by a customer
	CustomerCode     string            `json:"customer_code"`
	CustomerName     string            `json:"customer_name"`
	FromWarehouse    string            `json:"from_warehouse,omitempty"`
	FromCustomerCode string            `json:"from_customer_code,omitempty"`
	FromCustomerName string            `json:"from_customer_name,omitempty"`
	Lines            []ShipmentLineDto `json:"lines"`
}

type CustomerDto struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	CustomerType  string `json:"customer_type"`
	ContactPerson string `json:"contact_person"`
	Phone         string `json:"phone"`
	Email         string `json:"email"`
	Address       string `json:"address"`
}

// validateShipment checks the request without touching the database.
func validateShipment(req *ShipmentRequest) fieldErrors {
	fe := fieldErrors{}
	if strings.TrimSpace(req.ShipmentNo) == "" || len(req.ShipmentNo) > 30 {
		fe["shipment_no"] = "required, max 30 characters"
	}
	if strings.TrimSpace(req.CustomerCode) == "" {
		fe["customer_code"] = "required"
	}
	if req.FromCustomerCode != "" && req.FromWarehouse != "" {
		fe["from_warehouse"] = "either from_warehouse (our stock) or from_customer_code (onward delivery), not both"
	}
	if req.FromCustomerCode != "" && req.FromCustomerCode == req.CustomerCode {
		fe["from_customer_code"] = "must differ from customer_code"
	}
	if !validDate(req.ShipDate) {
		fe["ship_date"] = "required, YYYY-MM-DD"
	}
	if len(req.DeliveryNoteNo) > 50 {
		fe["delivery_note_no"] = "max 50 characters"
	}
	if len(req.Lines) == 0 || len(req.Lines) > 100 {
		fe["lines"] = "1 to 100 lines"
	}
	seen := map[string]bool{}
	for i, l := range req.Lines {
		k := fmt.Sprintf("lines[%d].", i)
		if strings.TrimSpace(l.LotNo) == "" {
			fe[k+"lot_no"] = "required"
		} else if seen[l.LotNo] {
			fe[k+"lot_no"] = "duplicate in this shipment"
		}
		seen[l.LotNo] = true
		if !(l.Qty > 0) || l.Qty > 1e9 {
			fe[k+"qty"] = "must be > 0"
		}
		if l.UnitPrice != nil && (!(*l.UnitPrice >= 0) || *l.UnitPrice > 1e7) {
			fe[k+"unit_price"] = "must be >= 0"
		}
	}
	return fe
}

// Create handles POST /shipments. 201 with the shipment; 422 with per-field errors; 404 for an
// unknown customer, warehouse or lot; 409 CONFLICT for a used number, EXCEEDS_DELIVERED for an
// onward delivery above what the customer holds, LOT_BLOCKED / LOT_EXPIRED / INSUFFICIENT_STOCK
// from the stock triggers. Everything is one transaction: a failing line leaves nothing behind.
func (c *ShipmentController) Create(w http.ResponseWriter, r *http.Request) {
	var req ShipmentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.FromCustomerCode == "" {
		req.FromWarehouse = nonEmpty(req.FromWarehouse, "ANA-DEPO")
	}
	if fe := validateShipment(&req); len(fe) > 0 {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fe)
		return
	}
	ctx := r.Context()

	// The shipment_line trigger books the SHIPMENT movement as @app_user. The variable lives on
	// this connection only and is cleared before the connection goes back to the pool.
	conn, err := c.db.Conn(ctx)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET @app_user = ?`, nonEmpty(OperatorFrom(ctx), "api")); err != nil {
		writeDBError(w, r, err)
		return
	}
	defer conn.ExecContext(context.Background(), `SET @app_user = NULL`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer tx.Rollback()

	customer, err := customerID(ctx, tx, req.CustomerCode)
	if err != nil {
		writeShipmentError(w, r, err)
		return
	}
	var warehouse, fromCustomer sql.NullInt64
	if req.FromCustomerCode != "" {
		id, err := customerID(ctx, tx, req.FromCustomerCode)
		if err != nil {
			writeShipmentError(w, r, err)
			return
		}
		fromCustomer = sql.NullInt64{Int64: id, Valid: true}
	} else {
		var whType string
		if err := tx.QueryRowContext(ctx, `SELECT id, warehouse_type FROM warehouse WHERE code = ?`, req.FromWarehouse).
			Scan(&warehouse.Int64, &whType); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				err = notFound("warehouse " + req.FromWarehouse)
			}
			writeShipmentError(w, r, err)
			return
		}
		warehouse.Valid = true
		if whType == "QUARANTINE" || whType == "SUBCONTRACTOR" {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT",
				fieldErrors{"from_warehouse": "quarantine and subcontractor stock is not shipped to customers"})
			return
		}
	}
	// Same clock as the stock ledger: a shipment is recorded when the goods leave, not before.
	var future bool
	if err := tx.QueryRowContext(ctx, `SELECT CAST(? AS DATE) > CURRENT_DATE`, req.ShipDate).Scan(&future); err != nil {
		writeDBError(w, r, err)
		return
	}
	if future {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{"ship_date": "in the future"})
		return
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO shipment (shipment_no, customer_id, from_warehouse_id, from_customer_id, ship_date, delivery_note_no)
		 VALUES (?, ?, ?, ?, ?, NULLIF(?, ''))`,
		req.ShipmentNo, customer, warehouse, fromCustomer, req.ShipDate, req.DeliveryNoteNo)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	shipmentID, _ := res.LastInsertId()

	for i, l := range req.Lines {
		k := fmt.Sprintf("lines[%d].", i)
		// lotID locks the lot row: concurrent onward deliveries cannot both claim the same goods.
		lot, _, err := lotID(ctx, tx, l.LotNo)
		if err != nil {
			writeShipmentError(w, r, err)
			return
		}
		var lotDate string
		var countable bool
		var salePrice sql.NullFloat64
		if err := tx.QueryRowContext(ctx,
			`SELECT DATE_FORMAT(l.lot_date, '%Y-%m-%d'), NOT u.is_weight, p.sale_price
			 FROM lot l JOIN product p ON p.id = l.product_id JOIN unit_of_measure u ON u.code = l.uom_code
			 WHERE l.id = ?`, lot).Scan(&lotDate, &countable, &salePrice); err != nil {
			writeDBError(w, r, err)
			return
		}
		if req.ShipDate < lotDate {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{k + "lot_no": "lot date " + lotDate + " is after ship_date"})
			return
		}
		if countable && l.Qty != math.Trunc(l.Qty) {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "INVALID_INPUT", fieldErrors{k + "qty": "whole units only"})
			return
		}
		if fromCustomer.Valid {
			var received, forwarded, returned float64
			if err := tx.QueryRowContext(ctx, `SELECT
			    (SELECT COALESCE(SUM(sl.qty),0) FROM shipment_line sl JOIN shipment sh ON sh.id=sl.shipment_id WHERE sl.lot_id=? AND sh.customer_id=?),
			    (SELECT COALESCE(SUM(sl.qty),0) FROM shipment_line sl JOIN shipment sh ON sh.id=sl.shipment_id WHERE sl.lot_id=? AND sh.from_customer_id=?),
			    (SELECT COALESCE(SUM(sm.qty),0) FROM stock_movement sm WHERE sm.lot_id=? AND sm.movement_type='RETURN' AND sm.ref_type='customer' AND sm.ref_id=?)`,
				lot, fromCustomer.Int64, lot, fromCustomer.Int64, lot, fromCustomer.Int64).Scan(&received, &forwarded, &returned); err != nil {
				writeDBError(w, r, err)
				return
			}
			if round3(forwarded+returned+l.Qty) > received {
				writeAPIError(w, r, http.StatusConflict, "EXCEEDS_DELIVERED", fmt.Sprintf(
					"lot %s: %s received %.3f, already forwarded %.3f and returned %.3f", l.LotNo, req.FromCustomerCode, received, forwarded, returned))
				return
			}
		}
		price := l.UnitPrice
		if price == nil && warehouse.Valid && salePrice.Valid {
			price = &salePrice.Float64
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO shipment_line (shipment_id, lot_id, qty, unit_price) VALUES (?, ?, ?, ?)`,
			shipmentID, lot, l.Qty, price); err != nil {
			writeDBError(w, r, fmt.Errorf("lot %s: %w", l.LotNo, err))
			return
		}
	}

	list, err := loadShipments(ctx, tx, req.ShipmentNo)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, list[0])
}

// List handles GET /shipments[?shipment_no=]: every shipment with its lines in recording order.
func (c *ShipmentController) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return
	}
	list, err := loadShipments(r.Context(), c.db, r.URL.Query().Get("shipment_no"))
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	writeJSON(w, list)
}

// Customers handles GET /customers: the buyers with the contact data the recall list uses.
func (c *ShipmentController) Customers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return
	}
	rows, err := c.db.QueryContext(r.Context(),
		`SELECT code, name, customer_type, COALESCE(contact_person, ''), COALESCE(phone, ''), COALESCE(email, ''), COALESCE(address, '')
		 FROM customer ORDER BY code`)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer rows.Close()
	list := []CustomerDto{}
	for rows.Next() {
		var d CustomerDto
		if err := rows.Scan(&d.Code, &d.Name, &d.CustomerType, &d.ContactPerson, &d.Phone, &d.Email, &d.Address); err != nil {
			writeDBError(w, r, err)
			return
		}
		list = append(list, d)
	}
	if err := rows.Err(); err != nil {
		writeDBError(w, r, err)
		return
	}
	writeJSON(w, list)
}

func loadShipments(ctx context.Context, q sqlRunner, shipmentNo string) ([]ShipmentDto, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT sh.shipment_no, DATE_FORMAT(sh.ship_date, '%Y-%m-%d'), COALESCE(sh.delivery_note_no, ''), sh.distribution_tier,
		        c.code, c.name, COALESCE(w.code, ''), COALESCE(fc.code, ''), COALESCE(fc.name, ''),
		        l.lot_no, p.sku, p.name, sl.qty, l.uom_code, sl.unit_price
		 FROM shipment sh
		 JOIN customer c       ON c.id = sh.customer_id
		 LEFT JOIN warehouse w ON w.id = sh.from_warehouse_id
		 LEFT JOIN customer fc ON fc.id = sh.from_customer_id
		 JOIN shipment_line sl ON sl.shipment_id = sh.id
		 JOIN lot l            ON l.id = sl.lot_id
		 JOIN product p        ON p.id = l.product_id
		 WHERE ? = '' OR sh.shipment_no = ?
		 ORDER BY sh.ship_date, sh.id, sl.id`, shipmentNo, shipmentNo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ShipmentDto{}
	for rows.Next() {
		var s ShipmentDto
		var l ShipmentLineDto
		var price sql.NullFloat64
		if err := rows.Scan(&s.ShipmentNo, &s.ShipDate, &s.DeliveryNoteNo, &s.Tier,
			&s.CustomerCode, &s.CustomerName, &s.FromWarehouse, &s.FromCustomerCode, &s.FromCustomerName,
			&l.LotNo, &l.SKU, &l.ProductName, &l.Qty, &l.UomCode, &price); err != nil {
			return nil, err
		}
		if price.Valid {
			p, total := price.Float64, round(price.Float64*l.Qty, 2)
			l.UnitPrice, l.LineTotal = &p, &total
		}
		if n := len(list); n > 0 && list[n-1].ShipmentNo == s.ShipmentNo {
			list[n-1].Lines = append(list[n-1].Lines, l)
			continue
		}
		s.Lines = []ShipmentLineDto{l}
		list = append(list, s)
	}
	return list, rows.Err()
}

func customerID(ctx context.Context, tx *sql.Tx, code string) (int64, error) {
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM customer WHERE code = ?`, code).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, notFound("customer " + code)
		}
		return 0, err
	}
	return id, nil
}

func writeShipmentError(w http.ResponseWriter, r *http.Request, err error) {
	var nf notFoundError
	if errors.As(err, &nf) {
		writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	writeDBError(w, r, err)
}
