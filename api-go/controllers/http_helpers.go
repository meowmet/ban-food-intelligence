package controllers

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// APIError is the single error body every endpoint returns:
// {"code": "LOT_NOT_FOUND", "message": "<localised>", "details": "<technical>"}.
// Clients branch on Code; Message is chosen from Accept-Language (tr, de, en).
type APIError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

var errorMessages = map[string]map[string]string{
	"INVALID_INPUT":       {"en": "The request is not valid.", "tr": "İstek geçersiz.", "de": "Die Anfrage ist ungültig."},
	"NOT_FOUND":           {"en": "Record not found.", "tr": "Kayıt bulunamadı.", "de": "Datensatz nicht gefunden."},
	"LOT_NOT_FOUND":       {"en": "Lot not found.", "tr": "Lot bulunamadı.", "de": "Charge nicht gefunden."},
	"METHOD_NOT_ALLOWED":  {"en": "This method is not allowed here.", "tr": "Bu işlem burada kullanılamaz.", "de": "Diese Methode ist hier nicht erlaubt."},
	"CONFLICT":            {"en": "The record already exists or was changed.", "tr": "Kayıt zaten var veya değiştirilmiş.", "de": "Der Datensatz existiert bereits oder wurde geändert."},
	"INSUFFICIENT_STOCK":  {"en": "Not enough stock for this lot.", "tr": "Bu lot için yeterli stok yok.", "de": "Nicht genügend Bestand für diese Charge."},
	"LOT_BLOCKED":         {"en": "This lot is recalled or blocked. Sale blocked.", "tr": "Bu lot geri çağrıldı veya bloke. Satış engellendi.", "de": "Diese Charge ist zurückgerufen oder gesperrt. Verkauf gesperrt."},
	"LOT_EXPIRED":         {"en": "This lot is past its expiry date. Sale blocked.", "tr": "Bu lotun son tüketim tarihi geçti. Satış engellendi.", "de": "Das Haltbarkeitsdatum dieser Charge ist überschritten. Verkauf gesperrt."},
	"EXCEEDS_DELIVERED":   {"en": "More than the customer's recorded deliveries (minus onward deliveries and returns).", "tr": "Müşteriye kayıtlı teslimattan fazla (ileri sevk ve iadeler düşülür).", "de": "Mehr als die erfassten Lieferungen an den Kunden (abzüglich Weiterlieferungen und Rücknahmen)."},
	"ORDER_STATE":         {"en": "The production order is not in the right state for this step.", "tr": "Üretim emri bu adım için uygun durumda değil.", "de": "Der Fertigungsauftrag ist für diesen Schritt nicht im richtigen Status."},
	"FORECAST_FROZEN":     {"en": "A forecast is already frozen for this carcass; it cannot be replaced.", "tr": "Bu karkas için tahmin zaten donduruldu; değiştirilemez.", "de": "Für diesen Schlachtkörper ist bereits eine Prognose eingefroren; sie kann nicht ersetzt werden."},
	"FORECAST_TOO_LATE":   {"en": "This carcass is already cut; a forecast can only be frozen before.", "tr": "Bu karkas zaten parçalandı; tahmin yalnızca önceden dondurulabilir.", "de": "Dieser Schlachtkörper ist bereits zerlegt; eine Prognose kann nur vorher eingefroren werden."},
	"INVALID_PIN":         {"en": "Wrong PIN.", "tr": "PIN hatalı.", "de": "Falsche PIN."},
	"LOCKED_OUT":          {"en": "Too many wrong attempts. Try again later.", "tr": "Çok fazla hatalı deneme. Daha sonra tekrar deneyin.", "de": "Zu viele Fehlversuche. Bitte später erneut versuchen."},
	"SIGNATURE_REJECTED":  {"en": "The device signature was rejected.", "tr": "Cihaz imzası reddedildi.", "de": "Die Gerätesignatur wurde abgelehnt."},
	"AUTH_REQUIRED":       {"en": "Sign in with a supervisor PIN first.", "tr": "Önce yetkili PIN'i ile giriş yapın.", "de": "Bitte zuerst mit der Aufsichts-PIN anmelden."},
	"PAYLOAD_TOO_LARGE":   {"en": "The request is too large.", "tr": "İstek çok büyük.", "de": "Die Anfrage ist zu groß."},
	"RATE_LIMITED":        {"en": "Too many requests. Slow down.", "tr": "Çok fazla istek. Lütfen yavaşlayın.", "de": "Zu viele Anfragen. Bitte langsamer."},
	"DEVICE_NOT_ENROLLED": {"en": "This device is not registered for sync.", "tr": "Bu cihaz senkronizasyon için kayıtlı değil.", "de": "Dieses Gerät ist nicht für die Synchronisierung registriert."},
	"KEY_MISMATCH":        {"en": "This device is registered with a different key.", "tr": "Bu cihaz farklı bir anahtarla kayıtlı.", "de": "Dieses Gerät ist mit einem anderen Schlüssel registriert."},
	"REPLAY_MISMATCH":     {"en": "This record ID was already used for different data.", "tr": "Bu kayıt kimliği farklı verilerle zaten kullanıldı.", "de": "Diese Datensatz-ID wurde bereits für andere Daten verwendet."},
	"DB_ERROR":            {"en": "The database could not complete the request.", "tr": "Veritabanı isteği tamamlayamadı.", "de": "Die Datenbank konnte die Anfrage nicht ausführen."},
}

// lang picks tr / de / en from the Accept-Language header (default en).
func lang(r *http.Request) string {
	if r == nil {
		return "en"
	}
	al := strings.ToLower(r.Header.Get("Accept-Language"))
	switch {
	case strings.HasPrefix(al, "tr"):
		return "tr"
	case strings.HasPrefix(al, "de"):
		return "de"
	default:
		return "en"
	}
}

func messageFor(code string, r *http.Request) string {
	if m, ok := errorMessages[code]; ok {
		return m[lang(r)]
	}
	return code
}

// writeAPIError writes the standard error body with a localised message.
func writeAPIError(w http.ResponseWriter, r *http.Request, status int, code string, details interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIError{Code: code, Message: messageFor(code, r), Details: details})
}

// writeDBError maps a MySQL error (SIGNAL texts from schema.sql triggers/procedures)
// to the right HTTP status and error code.
func writeDBError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := classifyDBError(err)
	if status == http.StatusInternalServerError {
		// Unexpected driver errors can name tables, columns or hosts: log them, do not return them.
		log.Printf("[ERROR] [DB] %s %s: %v", r.Method, r.URL.Path, err)
		writeAPIError(w, r, status, code, nil)
		return
	}
	writeAPIError(w, r, status, code, err.Error())
}

func classifyDBError(err error) (int, string) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "LOT_NOT_FOUND"):
		return http.StatusNotFound, "LOT_NOT_FOUND"
	case strings.Contains(msg, "INSUFFICIENT_STOCK"):
		return http.StatusConflict, "INSUFFICIENT_STOCK"
	case strings.Contains(msg, "LOT_BLOCKED"):
		return http.StatusConflict, "LOT_BLOCKED"
	case strings.Contains(msg, "LOT_EXPIRED"):
		return http.StatusConflict, "LOT_EXPIRED"
	case strings.Contains(msg, "ORDER_STATE"):
		return http.StatusConflict, "ORDER_STATE"
	case strings.Contains(msg, "FORECAST_TOO_LATE"):
		return http.StatusConflict, "FORECAST_TOO_LATE"
	case strings.Contains(msg, "INVALID_INPUT"), strings.Contains(msg, "ACTUAL_QTY"), strings.Contains(msg, "MOVEMENT_SIGN"), strings.Contains(msg, "EXPIRY_REQUIRED"), strings.Contains(msg, "Check constraint"):
		return http.StatusUnprocessableEntity, "INVALID_INPUT"
	case strings.Contains(msg, "Duplicate entry"):
		return http.StatusConflict, "CONFLICT"
	default:
		return http.StatusInternalServerError, "DB_ERROR"
	}
}

// writeJSON encodes v as a JSON response body.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONError keeps older call sites working: the code is derived from the status.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	code := "DB_ERROR"
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		code = "INVALID_INPUT"
	case http.StatusNotFound:
		code = "NOT_FOUND"
	case http.StatusMethodNotAllowed:
		code = "METHOD_NOT_ALLOWED"
	case http.StatusConflict:
		code = "CONFLICT"
	}
	writeAPIError(w, nil, status, code, message)
}
