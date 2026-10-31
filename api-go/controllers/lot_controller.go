package controllers

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

type LotController struct {
	db *sql.DB
}

func NewLotController(db *sql.DB) *LotController {
	return &LotController{db: db}
}

// ListStockByLot handles GET /lots
// Queries Ali's MySQL `v_stock_by_lot` view.
func (c *LotController) ListStockByLot(w http.ResponseWriter, r *http.Request) {
	rows, err := c.db.QueryContext(
		r.Context(),
		`SELECT v.lot_no, v.sku, v.product_name, v.warehouse_code, v.qty, v.uom_code, v.unit_cost, v.stock_value, v.status,
		        p.sale_price, COALESCE(DATE_FORMAT(v.expiry_date, '%Y-%m-%d'), '')
		 FROM v_stock_by_lot v
		 JOIN product p ON p.sku = v.sku
		 ORDER BY v.lot_no ASC, v.warehouse_code ASC`,
	)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer rows.Close()

	type StockLotDto struct {
		LotNo         string   `json:"lot_no"`
		SKU           string   `json:"sku"`
		ProductName   string   `json:"product_name"`
		WarehouseCode string   `json:"warehouse_code"`
		Qty           float64  `json:"qty"`
		UomCode       string   `json:"uom_code"`
		UnitCost      float64  `json:"unit_cost"`
		StockValue    float64  `json:"stock_value"`
		Status        string   `json:"status"`
		SalePrice     *float64 `json:"sale_price"` // VKP; null when the product is not sold
		ExpiryDate    string   `json:"expiry_date"`
	}

	var list []StockLotDto
	for rows.Next() {
		var s StockLotDto
		var salePrice sql.NullFloat64
		if err := rows.Scan(
			&s.LotNo, &s.SKU, &s.ProductName, &s.WarehouseCode,
			&s.Qty, &s.UomCode, &s.UnitCost, &s.StockValue, &s.Status, &salePrice, &s.ExpiryDate,
		); err == nil {
			if salePrice.Valid {
				s.SalePrice = &salePrice.Float64
			}
			list = append(list, s)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

type WarehouseDto struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	WarehouseType string `json:"warehouse_type"` // MAIN, COLD, STORE, QUARANTINE, SUBCONTRACTOR
}

// Warehouses handles GET /warehouses: the stock locations a movement can name.
func (c *LotController) Warehouses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return
	}
	rows, err := c.db.QueryContext(r.Context(), `SELECT code, name, warehouse_type FROM warehouse ORDER BY id`)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	defer rows.Close()
	list := []WarehouseDto{}
	for rows.Next() {
		var d WarehouseDto
		if err := rows.Scan(&d.Code, &d.Name, &d.WarehouseType); err != nil {
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
