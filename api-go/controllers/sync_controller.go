package controllers

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"ban-food-intelligence/api-go/models"
)

// SyncController handles batch synchronization from Nuri's Kotlin POS outbox
// using Hardware ECDSA P-256 (SHA256withECDSA) verification and MySQL `sync_inbox` persistence.
type SyncController struct {
	db         *sql.DB
	deviceKeys sync.Map // deviceID -> pinnedKey; cache of pos_device_key (the only store when db is nil)
}

// maxOutboxItems caps one sync request (the POS sends batches of 50).
const maxOutboxItems = 500

// pinnedKey is the one public key a POS device may sign with.
type pinnedKey struct {
	key         *ecdsa.PublicKey
	fingerprint string
}

func NewSyncController(db *sql.DB) *SyncController {
	log.Println("[INFO] [SYNC] Enforcing ECDSA P-256 (SECP256R1 / SHA256withECDSA) sync contract")
	return &SyncController{
		db: db,
	}
}

// RegisterDeviceKey handles POST /sync/register-key {device_id, public_key_b64}. It is behind
// the operator session (see main.go): a supervisor provisions a device or rotates its key,
// e.g. after the app was reinstalled. Without it the first signed sync pins the key (TOFU).
func (c *SyncController) RegisterDeviceKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return
	}

	var body struct {
		DeviceID     string `json:"device_id"`
		PublicKeyB64 string `json:"public_key_b64"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DeviceID == "" || body.PublicKeyB64 == "" || len(body.DeviceID) > 64 {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "device_id (max 64) and public_key_b64 are required")
		return
	}

	pubKey, fingerprint, err := parseP256PublicKeyBase64(body.PublicKeyB64)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "invalid ECDSA P-256 SPKI key: "+err.Error())
		return
	}
	by := OperatorFrom(r.Context())
	if by == "" {
		by = "operator"
	}
	if err := c.pinKey(r, body.DeviceID, body.PublicKeyB64, pubKey, fingerprint, by, true); err != nil {
		writeDBError(w, r, err)
		return
	}
	log.Printf("[INFO] [SYNC-ENROLL] device=%s key sha256=%s registered by %s", body.DeviceID, fingerprint, by)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":              "ENROLLED",
		"device_id":           body.DeviceID,
		"signature_alg":       "ECDSA-P256-SHA256",
		"key_fingerprint_hex": fingerprint,
	})
}

// loadPinned returns the pinned key of a device from the cache or pos_device_key.
func (c *SyncController) loadPinned(r *http.Request, deviceID string) (*pinnedKey, error) {
	if v, ok := c.deviceKeys.Load(deviceID); ok {
		return v.(*pinnedKey), nil
	}
	if c.db == nil {
		return nil, nil
	}
	var b64 string
	err := c.db.QueryRowContext(r.Context(), `SELECT public_key_b64 FROM pos_device_key WHERE device_id = ?`, deviceID).Scan(&b64)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	key, fp, err := parseP256PublicKeyBase64(b64)
	if err != nil {
		return nil, err
	}
	pk := &pinnedKey{key: key, fingerprint: fp}
	c.deviceKeys.Store(deviceID, pk)
	return pk, nil
}

// pinKey stores the device key. replace=false keeps an existing pin (first writer wins).
func (c *SyncController) pinKey(r *http.Request, deviceID, b64 string, key *ecdsa.PublicKey, fp, by string, replace bool) error {
	if c.db != nil {
		stmt := `INSERT IGNORE INTO pos_device_key (device_id, public_key_b64, fingerprint, enrolled_by) VALUES (?, ?, ?, ?)`
		if replace {
			stmt = `REPLACE INTO pos_device_key (device_id, public_key_b64, fingerprint, enrolled_by) VALUES (?, ?, ?, ?)`
		}
		if _, err := c.db.ExecContext(r.Context(), stmt, deviceID, b64, fp, by); err != nil {
			return err
		}
		c.deviceKeys.Delete(deviceID)
		return nil
	}
	if replace {
		c.deviceKeys.Store(deviceID, &pinnedKey{key: key, fingerprint: fp})
	} else {
		c.deviceKeys.LoadOrStore(deviceID, &pinnedKey{key: key, fingerprint: fp})
	}
	return nil
}

// IngestOutbox handles POST /sync
// 1. Parses `outbox_items` JSON envelope from Nuri's Kotlin SyncWorker.
// 2. Validates ASN.1 DER ECDSA P-256 (`SHA256withECDSA`) signatures via `crypto/ecdsa` & `crypto/x509`.
// 3. Enforces UUIDv7 idempotency and persists into Ali's MySQL `sync_inbox` (and `sale`/`sale_line` when applicable).
func (c *SyncController) IngestOutbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", nil)
		return
	}

	rawBodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "failed to read request body")
		return
	}

	var req models.SyncBatchRequest
	if err := json.Unmarshal(rawBodyBytes, &req); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "invalid JSON body for outbox_items envelope")
		return
	}

	if len(req.OutboxItems) == 0 {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "outbox_items cannot be empty")
		return
	}
	if len(req.OutboxItems) > maxOutboxItems {
		writeAPIError(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", fmt.Sprintf("at most %d outbox_items per request", maxOutboxItems))
		return
	}

	if req.DeviceID == "" || len(req.DeviceID) > 64 {
		writeAPIError(w, r, http.StatusBadRequest, "INVALID_INPUT", "device_id (max 64 chars) is required")
		return
	}

	// The device key: the pinned one, or (first contact, POS_ENROLLMENT != "closed") the key
	// offered in X-POS-Public-Key, pinned after the envelope signature has been verified.
	pinned, err := c.loadPinned(r, req.DeviceID)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	offeredB64 := strings.TrimSpace(r.Header.Get("X-POS-Public-Key"))
	if offeredB64 == "" {
		offeredB64 = strings.TrimSpace(req.PublicKeyB64)
	}
	var devicePubKey *ecdsa.PublicKey
	var offeredFP string
	if offeredB64 != "" {
		parsedKey, fp, err := parseP256PublicKeyBase64(offeredB64)
		if err != nil {
			writeAPIError(w, r, http.StatusForbidden, "SIGNATURE_REJECTED", "invalid X-POS-Public-Key SPKI: "+err.Error())
			return
		}
		devicePubKey, offeredFP = parsedKey, fp
	}
	if headerFP := strings.TrimSpace(r.Header.Get("X-Key-Fingerprint")); headerFP != "" && offeredFP != "" && !strings.EqualFold(headerFP, offeredFP) {
		writeAPIError(w, r, http.StatusForbidden, "SIGNATURE_REJECTED", "X-Key-Fingerprint does not match X-POS-Public-Key")
		return
	}
	switch {
	case pinned != nil && offeredFP != "" && offeredFP != pinned.fingerprint:
		log.Printf("[WARN] [SYNC] device=%s offered key %s, pinned %s: refused", req.DeviceID, offeredFP, pinned.fingerprint)
		writeAPIError(w, r, http.StatusForbidden, "KEY_MISMATCH", "device "+req.DeviceID+" is pinned to another key; a supervisor can re-register it via POST /sync/register-key")
		return
	case pinned != nil:
		devicePubKey = pinned.key
	case devicePubKey == nil || strings.EqualFold(os.Getenv("POS_ENROLLMENT"), "closed"):
		writeAPIError(w, r, http.StatusForbidden, "DEVICE_NOT_ENROLLED", "send X-POS-Public-Key on first sync, or register the device via POST /sync/register-key")
		return
	}

	// The envelope signature is mandatory: it covers the whole body, so no field can be changed.
	envelopeSigB64 := strings.TrimSpace(r.Header.Get("X-Signature"))
	idempotencyUUID := strings.TrimSpace(r.Header.Get("X-Idempotency-UUIDv7"))
	if idempotencyUUID == "" {
		idempotencyUUID = req.SyncEnvelopeUUIDv7
	}
	envelopeCanonical := fmt.Sprintf(
		"%s:%s:%s:%s:%s",
		req.TenantID,
		req.DeviceID,
		req.ActorRole,
		idempotencyUUID,
		string(rawBodyBytes),
	)
	if envelopeSigB64 == "" || !verifyECDSAP256Signature(devicePubKey, envelopeCanonical, envelopeSigB64) {
		writeAPIError(w, r, http.StatusForbidden, "SIGNATURE_REJECTED", "ECDSA-P256 envelope signature missing or invalid")
		return
	}
	if pinned == nil {
		if err := c.pinKey(r, req.DeviceID, offeredB64, devicePubKey, offeredFP, "tofu", false); err != nil {
			writeDBError(w, r, err)
			return
		}
		// A concurrent first sync may have pinned a different key: re-check.
		if p, err := c.loadPinned(r, req.DeviceID); err != nil || p == nil || p.fingerprint != offeredFP {
			writeAPIError(w, r, http.StatusForbidden, "KEY_MISMATCH", "device "+req.DeviceID+" was pinned to another key")
			return
		}
		log.Printf("[INFO] [SYNC-ENROLL] device=%s pinned to key sha256=%s on first signed sync", req.DeviceID, offeredFP)
	}

	ackIDs := []string{} // no accepted records is [], never null (clients must not infer success)
	rejected := []models.SyncRejectionDto{}
	for _, item := range req.OutboxItems {
		if item.Timestamp == "" {
			continue
		}

		// Items are signed by the same device key; an item may not claim another device.
		itemPubKey := devicePubKey
		if item.DeviceID != "" && item.DeviceID != req.DeviceID {
			writeAPIError(w, r, http.StatusForbidden, "SIGNATURE_REJECTED", "outbox item device_id differs from the envelope device_id")
			return
		}

		// Build canonical v2 payload matching Nuri's KeystoreManager.buildCanonicalYieldPayload()
		tenantID := item.TenantID
		if tenantID == "" {
			tenantID = req.TenantID
		}
		deviceID := item.DeviceID
		if deviceID == "" {
			deviceID = req.DeviceID
		}
		actorRole := item.ActorRole
		if actorRole == "" {
			actorRole = req.ActorRole
		}

		canonicalRecord := fmt.Sprintf(
			"v2|%s|%s|%s|%s|%.4f|%s",
			tenantID,
			deviceID,
			actorRole,
			item.BatchID,
			item.PredictedYield,
			item.Timestamp,
		)

		// Verify ASN.1 DER format or cryptographic ECDSA signature
		if item.Signature == "" {
			writeAPIError(w, r, http.StatusForbidden, "SIGNATURE_REJECTED", "security alert: missing ECDSA signature on outbox item")
			return
		}

		if !verifyECDSAP256Signature(itemPubKey, canonicalRecord, item.Signature) {
			log.Printf("[ERROR] [SYNC] ECDSA P-256 signature verification failed for device=%s UUIDv7=%s", deviceID, item.Timestamp)
			writeAPIError(w, r, http.StatusForbidden, "SIGNATURE_REJECTED", "ECDSA-P256 signature mismatch for UUIDv7="+item.Timestamp)
			return
		}

		// Persist to MySQL `sync_inbox` (idempotent via PRIMARY KEY `event_uuid`)
		payloadHash := sha256.Sum256([]byte(canonicalRecord))
		payloadHashHex := hex.EncodeToString(payloadHash[:])

		if c.db != nil {
			if dbErr := c.applyOutboxItem(r, item, deviceID, payloadHashHex); dbErr != nil {
				// Not acknowledged. The code tells the POS whether a retry can help.
				log.Printf("[WARN] [SYNC-MYSQL] item %s not applied: %v", item.Timestamp, dbErr)
				_, code := classifyDBError(dbErr)
				if errors.Is(dbErr, errReplayMismatch) {
					code = "REPLAY_MISMATCH"
				}
				if strings.Contains(dbErr.Error(), "sale qty must be > 0") || strings.Contains(dbErr.Error(), "has no sale_price") {
					code = "INVALID_INPUT"
				}
				rejected = append(rejected, models.SyncRejectionDto{ID: item.Timestamp, Code: code, Message: messageFor(code, r)})
				if code == "LOT_BLOCKED" || code == "LOT_EXPIRED" {
					logAlertError("rejected sale "+item.Timestamp, raiseRejectedSaleAlert(r.Context(), c.db, item.BatchID, code,
						item.Timestamp, deviceID, item.PredictedYield, uuidV7Time(item.Timestamp)))
				}
				continue
			}
		}

		ackIDs = append(ackIDs, item.Timestamp)
	}

	status := "SUCCESS"
	if len(ackIDs) < len(req.OutboxItems) {
		status = "PARTIAL"
	}
	resp := models.SyncBatchResponse{
		ProcessedCount:    len(ackIDs),
		AcknowledgedIDs:   ackIDs,
		Rejected:          rejected,
		Status:            status,
		SignatureVerified: true,
		SignatureAlg:      "ECDSA-P256-SHA256",
		ServerTime:        time.Now().UTC(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// applyOutboxItem records one verified outbox item in MySQL in a single transaction.
// If batch_id is a known lot, the item is a POS sale: sync_inbox + sale + sale_line
// (trg_sl_before_insert posts the SALE stock movement). Otherwise it is a yield
// prediction and only sync_inbox is written. A UUIDv7 already in sync_inbox is a
// replay and is acknowledged without inserting anything.
func (c *SyncController) applyOutboxItem(r *http.Request, item models.OutboxItemDto, deviceID, payloadHash string) error {
	ctx := r.Context()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var lotID int64
	var salePrice sql.NullFloat64
	err = tx.QueryRowContext(ctx,
		`SELECT l.id, p.sale_price FROM lot l JOIN product p ON p.id = l.product_id WHERE l.lot_no = ?`,
		item.BatchID,
	).Scan(&lotID, &salePrice)
	isSale := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	entity := "yield_prediction"
	if isSale {
		entity = "sale"
	}
	res, err := tx.ExecContext(ctx,
		`INSERT IGNORE INTO sync_inbox (event_uuid, device_id, entity, payload_hash) VALUES (?, ?, ?, ?)`,
		item.Timestamp, deviceID, entity, payloadHash,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Replay of a known UUIDv7: acknowledge only if it is the same signed record.
		var stored string
		if err := tx.QueryRowContext(ctx, `SELECT payload_hash FROM sync_inbox WHERE event_uuid = ?`, item.Timestamp).Scan(&stored); err != nil {
			return err
		}
		if stored != payloadHash {
			return errReplayMismatch
		}
		return tx.Commit()
	}
	if !isSale {
		return tx.Commit()
	}

	if item.PredictedYield <= 0 {
		return fmt.Errorf("sale qty must be > 0, got %v", item.PredictedYield)
	}
	if !salePrice.Valid {
		return fmt.Errorf("lot %s has no sale_price", item.BatchID)
	}

	res, err = tx.ExecContext(ctx,
		`INSERT INTO sale (sale_uuid, device_id, warehouse_id, sold_at)
		 SELECT ?, ?, w.id, ? FROM warehouse w WHERE w.code = 'MAGAZA-01'`,
		item.Timestamp, deviceID, uuidV7Time(item.Timestamp),
	)
	if err != nil {
		return err
	}
	saleID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO sale_line (sale_id, line_no, lot_id, qty, unit_price) VALUES (?, 1, ?, ?, ?)`,
		saleID, lotID, item.PredictedYield, salePrice.Float64,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// errReplayMismatch: a UUIDv7 already in sync_inbox arrived with a different payload.
var errReplayMismatch = errors.New("REPLAY_MISMATCH: event uuid already used for a different payload")

// uuidV7Time returns the creation time embedded in a UUIDv7 (first 48 bits = Unix ms),
// falling back to now for malformed input.
func uuidV7Time(uuid string) time.Time {
	hexPart := strings.ReplaceAll(uuid, "-", "")
	if len(hexPart) < 12 {
		return time.Now().UTC()
	}
	ms, err := strconv.ParseInt(hexPart[:12], 16, 64)
	if err != nil {
		return time.Now().UTC()
	}
	return time.UnixMilli(ms).UTC()
}

// parseP256PublicKeyBase64 parses a Base64-encoded X.509 SPKI public key from
// Android Keystore (`KeystoreManager.exportPublicKeyBase64()`) and ensures it is
// on the NIST P-256 (`secp256r1`) curve.
func parseP256PublicKeyBase64(pubKeyB64 string) (*ecdsa.PublicKey, string, error) {
	derBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubKeyB64))
	if err != nil {
		return nil, "", fmt.Errorf("base64 decode failed: %w", err)
	}

	parsedKey, err := x509.ParsePKIXPublicKey(derBytes)
	if err != nil {
		return nil, "", fmt.Errorf("x509 SPKI parse failed: %w", err)
	}

	ecKey, ok := parsedKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, "", errors.New("public key is not an ECDSA key")
	}
	if ecKey.Curve != elliptic.P256() {
		return nil, "", errors.New("ECDSA curve must be P-256 (secp256r1)")
	}

	fp := sha256.Sum256(derBytes)
	return ecKey, hex.EncodeToString(fp[:]), nil
}

// verifyECDSAP256Signature verifies a Base64-encoded ASN.1 DER `SHA256withECDSA` signature.
func verifyECDSAP256Signature(pubKey *ecdsa.PublicKey, canonicalPayload string, signatureB64 string) bool {
	sigDER, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signatureB64))
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(canonicalPayload))
	return ecdsa.VerifyASN1(pubKey, digest[:], sigDER)
}
