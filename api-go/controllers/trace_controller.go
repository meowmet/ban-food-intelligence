package controllers

import (
	"database/sql"
	"net/http"
	"strings"
)

type TraceController struct {
	db sqlRunner
}

func NewTraceController(db *sql.DB) *TraceController {
	return &TraceController{db: db}
}

type TraceLotDto struct {
	Depth         int     `json:"depth"`
	LotNo         string  `json:"lot_no"`
	SKU           string  `json:"sku"`
	ProductName   string  `json:"product_name"`
	OriginType    string  `json:"origin_type"`
	Qty           float64 `json:"qty"`
	UomCode       string  `json:"uom_code"`
	Status        string  `json:"status"`
	LotDate       string  `json:"lot_date,omitempty"`
	ExpiryDate    string  `json:"expiry_date,omitempty"`
	UnitCost      float64 `json:"unit_cost,omitempty"`
	SupplierCode  string  `json:"supplier_code,omitempty"`
	SupplierName  string  `json:"supplier_name,omitempty"`
	SupplierLotNo string  `json:"supplier_lot_no,omitempty"`
	Path          string  `json:"path"`
}

// TraceLinkDto is one recorded many-to-many lot relation with its quantities.
type TraceLinkDto struct {
	LinkType      string  `json:"link_type"` // PRODUCTION | BUTCHERY | REWORK
	DocumentNo    string  `json:"document_no"`
	Subcontractor string  `json:"subcontractor,omitempty"`
	InputLotNo    string  `json:"input_lot_no"`
	QtyConsumed   float64 `json:"qty_consumed"`
	InputUom      string  `json:"input_uom"`
	OutputLotNo   string  `json:"output_lot_no"`
	QtyProduced   float64 `json:"qty_produced"`
	OutputUom     string  `json:"output_uom"`
}

type TraceStockDto struct {
	LotNo     string  `json:"lot_no"`
	Warehouse string  `json:"warehouse_code"`
	Qty       float64 `json:"qty"`
	UomCode   string  `json:"uom_code"`
}

type TraceShipmentDto struct {
	Tier          int     `json:"tier"`
	CustomerCode  string  `json:"customer_code"`
	CustomerName  string  `json:"customer_name"`
	ContactPerson string  `json:"contact_person"`
	Phone         string  `json:"phone"`
	Email         string  `json:"email"`
	ReceivedVia   string  `json:"received_via,omitempty"`
	LotNo         string  `json:"lot_no"`
	ShipmentNo    string  `json:"shipment_no"`
	ShipDate      string  `json:"ship_date"`
	DeliveryNote  string  `json:"delivery_note_no"`
	Qty           float64 `json:"qty"`
	UomCode       string  `json:"uom_code"`
}

type TraceSaleDto struct {
	LotNo     string  `json:"lot_no"`
	Store     string  `json:"store_code"`
	SaleLines int     `json:"sale_lines"`
	QtySold   float64 `json:"qty_sold"`
	FirstSale string  `json:"first_sale"`
	LastSale  string  `json:"last_sale"`
}

// TraceResponse: lots reached from the root lot plus the exact recorded relations.
// Forward traces also list stock, shipments (buyers), POS sales and the mass balance of every reached lot.
type TraceResponse struct {
	RootLotNumber string             `json:"root_lot_number"`
	Direction     string             `json:"direction"`
	Lots          []TraceLotDto      `json:"lots"`
	Links         []TraceLinkDto     `json:"links"`
	Stock         []TraceStockDto    `json:"stock,omitempty"`
	Shipments     []TraceShipmentDto `json:"shipments,omitempty"`
	Sales         []TraceSaleDto     `json:"sales,omitempty"`
	MassBalance   *MassBalanceDto    `json:"mass_balance,omitempty"` // forward only
}

// TraceHandler handles GET /trace/{lotNumber}?direction=forward|backward
// (sp_trace_forward / sp_trace_backward, WITH RECURSIVE over lot_link).
func (c *TraceController) TraceHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	lotNumber := ""
	if len(parts) >= 2 && parts[len(parts)-1] != "trace" {
		lotNumber = parts[len(parts)-1]
	}
	if lotNumber == "" {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "usage: /trace/{lot_no}?direction=forward|backward")
		return
	}
	direction := strings.ToLower(r.URL.Query().Get("direction"))
	if direction != "forward" && direction != "backward" {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "direction must be forward or backward")
		return
	}

	resp := TraceResponse{RootLotNumber: lotNumber, Direction: direction, Lots: []TraceLotDto{}, Links: []TraceLinkDto{}}
	var err error
	if direction == "forward" {
		err = c.forward(r, lotNumber, &resp)
	} else {
		err = c.backward(r, lotNumber, &resp)
	}
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	if err := c.loadLinks(r, &resp); err != nil {
		writeDBError(w, r, err)
		return
	}
	if direction == "forward" {
		if resp.MassBalance, err = loadMassBalance(r.Context(), c.db, resp.Lots); err != nil {
			writeDBError(w, r, err)
			return
		}
	}
	writeJSON(w, resp)
}

func (c *TraceController) forward(r *http.Request, lotNo string, resp *TraceResponse) error {
	rows, err := c.db.QueryContext(r.Context(), `CALL sp_trace_forward(?)`, lotNo)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var d TraceLotDto
		var exp sql.NullString
		if err := rows.Scan(&d.Depth, &d.LotNo, &d.SKU, &d.ProductName, &d.OriginType, &d.Qty, &d.UomCode, &d.Status, &exp, &d.Path); err != nil {
			return err
		}
		d.ExpiryDate = dateOnly(exp.String)
		resp.Lots = append(resp.Lots, d)
	}
	if rows.NextResultSet() {
		for rows.Next() {
			var s TraceStockDto
			if err := rows.Scan(&s.LotNo, &s.Warehouse, &s.Qty, &s.UomCode); err != nil {
				return err
			}
			resp.Stock = append(resp.Stock, s)
		}
	}
	if rows.NextResultSet() {
		for rows.Next() {
			var s TraceShipmentDto
			var contact, phone, email, via, note, date sql.NullString
			if err := rows.Scan(&s.Tier, &s.CustomerCode, &s.CustomerName, &contact, &phone, &email, &via,
				&s.LotNo, &s.ShipmentNo, &date, &note, &s.Qty, &s.UomCode); err != nil {
				return err
			}
			s.ContactPerson, s.Phone, s.Email, s.ReceivedVia, s.DeliveryNote = contact.String, phone.String, email.String, via.String, note.String
			s.ShipDate = dateOnly(date.String)
			resp.Shipments = append(resp.Shipments, s)
		}
	}
	if rows.NextResultSet() {
		for rows.Next() {
			var s TraceSaleDto
			var first, last sql.NullString
			if err := rows.Scan(&s.LotNo, &s.Store, &s.SaleLines, &s.QtySold, &first, &last); err != nil {
				return err
			}
			s.FirstSale, s.LastSale = first.String, last.String
			resp.Sales = append(resp.Sales, s)
		}
	}
	return rows.Err()
}

func (c *TraceController) backward(r *http.Request, lotNo string, resp *TraceResponse) error {
	rows, err := c.db.QueryContext(r.Context(), `CALL sp_trace_backward(?)`, lotNo)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d TraceLotDto
		var lotDate, exp, supCode, supName, supLot, supContact, supPhone sql.NullString
		if err := rows.Scan(&d.Depth, &d.LotNo, &d.SKU, &d.ProductName, &d.OriginType,
			&lotDate, &exp, &d.Qty, &d.UomCode, &d.UnitCost,
			&supCode, &supName, &supLot, &supContact, &supPhone, &d.Path, &d.Status); err != nil {
			return err
		}
		d.LotDate, d.ExpiryDate = dateOnly(lotDate.String), dateOnly(exp.String)
		d.SupplierCode, d.SupplierName, d.SupplierLotNo = supCode.String, supName.String, supLot.String
		resp.Lots = append(resp.Lots, d)
	}
	return rows.Err()
}

// loadLinks adds every lot_link edge between lots of the trace (parent->child), with the
// production order / butchery operation it came from.
func (c *TraceController) loadLinks(r *http.Request, resp *TraceResponse) error {
	if len(resp.Lots) == 0 {
		return nil
	}
	placeholders := make([]string, len(resp.Lots))
	args := make([]interface{}, 0, 2*len(resp.Lots))
	for i, l := range resp.Lots {
		placeholders[i] = "?"
		args = append(args, l.LotNo)
	}
	for _, l := range resp.Lots {
		args = append(args, l.LotNo)
	}
	in := strings.Join(placeholders, ",")
	rows, err := c.db.QueryContext(r.Context(),
		`SELECT ll.link_type, COALESCE(po.order_no, bo.op_no, CONCAT(ll.ref_type, '#', ll.ref_id)), COALESCE(sub.name, ''),
		        pl.lot_no, ll.qty_consumed, pl.uom_code, cl.lot_no, ll.qty_produced, cl.uom_code
		 FROM lot_link ll
		 JOIN lot pl ON pl.id = ll.parent_lot_id
		 JOIN lot cl ON cl.id = ll.child_lot_id
		 LEFT JOIN production_order po   ON ll.ref_type = 'production_order'   AND po.id = ll.ref_id
		 LEFT JOIN supplier sub          ON sub.id = po.subcontractor_id
		 LEFT JOIN butchery_operation bo ON ll.ref_type = 'butchery_operation' AND bo.id = ll.ref_id
		 WHERE pl.lot_no IN (`+in+`) AND cl.lot_no IN (`+in+`)
		 ORDER BY 2, pl.lot_no, cl.lot_no`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var l TraceLinkDto
		if err := rows.Scan(&l.LinkType, &l.DocumentNo, &l.Subcontractor, &l.InputLotNo, &l.QtyConsumed, &l.InputUom,
			&l.OutputLotNo, &l.QtyProduced, &l.OutputUom); err != nil {
			return err
		}
		resp.Links = append(resp.Links, l)
	}
	return rows.Err()
}

// dateOnly trims "2026-10-11T00:00:00Z" / "2026-10-11 00:00:00" to "2026-10-11".
func dateOnly(v string) string {
	if len(v) >= 10 {
		return v[:10]
	}
	return v
}
