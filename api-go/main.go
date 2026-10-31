package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"ban-food-intelligence/api-go/controllers"
)

// apiVersion is reported by GET /health.
const apiVersion = "1.3.0"

const defaultMySQLDSN = "root:ban@tcp(127.0.0.1:3306)/food_intel?parseTime=true&multiStatements=true&charset=utf8mb4"

func logInfo(component, msg string, args ...interface{}) {
	log.Printf("[INFO] [%s] "+msg, append([]interface{}{component}, args...)...)
}

func logWarn(component, msg string, args ...interface{}) {
	log.Printf("[WARN] [%s] "+msg, append([]interface{}{component}, args...)...)
}

func logError(component, msg string, args ...interface{}) {
	log.Printf("[ERROR] [%s] "+msg, append([]interface{}{component}, args...)...)
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	logInfo("MAIN", "Starting BAN Food Intelligence AVATAR ERP Go Backend...")
	logInfo("MYSQL", "Initializing MySQL 8.0 connection pool with startup retry policy...")

	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = defaultMySQLDSN
	}

	db, err := initMySQLPool(dsn)
	if err != nil {
		logWarn("MYSQL", "MySQL initial ping exhausted retries (%v) — pool remains active for on-demand reconnect.", err)
	} else {
		logInfo("MYSQL", "MySQL 8.0 connection pool verified against schema 'food_intel'.")
	}
	defer func() {
		if db != nil {
			_ = db.Close()
		}
	}()

	logInfo("SECURITY", "Enforcing ECDSA P-256 (SHA256withECDSA) synchronization contract...")
	productionCtrl := controllers.NewProductionController(db)
	butcheryCtrl := controllers.NewButcheryController(db)
	recallCtrl := controllers.NewRecallController(db)
	syncCtrl := controllers.NewSyncController(db)
	traceCtrl := controllers.NewTraceController(db)
	lotCtrl := controllers.NewLotController(db)
	yieldCtrl := controllers.NewYieldController(db)
	reportsCtrl := controllers.NewReportsController(db)
	sessions := controllers.NewSessionManager()
	authCtrl := controllers.NewSupervisorAuthController(db).WithSessions(sessions)
	entryCtrl := controllers.NewEntryController(db)
	// Back-office writes need a supervisor session (POST /auth/session); reads stay open.
	// The POS only writes through /sync (ECDSA-signed) and /auth/supervisor/verify.
	op := sessions.RequireOperator
	movementsCtrl := controllers.NewMovementsController(db)
	shipmentCtrl := controllers.NewShipmentController(db)
	aiCtrl := controllers.NewAIController(db)
	alertCtrl := controllers.NewAlertController(db)
	countCtrl := controllers.NewStockCountController(db)

	mux := http.NewServeMux()

	// Health & Readiness Endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		dbStatus := "CONNECTED"
		if db == nil || db.PingContext(r.Context()) != nil {
			dbStatus = "DEGRADED"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"service":      "ban-food-intelligence-api-go",
			"version":      apiVersion,
			"status":       "UP",
			"mysql_status": dbStatus,
			"sync_crypto":  "ECDSA-P256-SHA256 (crypto/ecdsa + crypto/x509)",
			"server_time":  time.Now().UTC(),
		})
	})

	// Task 1: External Production & EKP Cost Calculation
	mux.HandleFunc("/production", op(productionCtrl.ListOrders))
	mux.HandleFunc("/production/orders", op(entryCtrl.ProductionOrder))
	mux.HandleFunc("/purchases", getOrPost(reportsCtrl.Purchases, op(entryCtrl.Purchase)))

	// Task 2: Whole Lamb Butchery & Cost Allocation
	mux.HandleFunc("/butchery", butcheryCtrl.ReconcileButchery)
	mux.HandleFunc("/butchery/allocation", butcheryCtrl.Allocation)
	mux.HandleFunc("/butchery/operations", op(entryCtrl.ButcheryOperation))

	// Task 3: Recursive Lot Traceability & Emergency Recall
	mux.HandleFunc("/recall", op(recallCtrl.TriggerRecall))
	mux.HandleFunc("/recall/contacts.csv", recallCtrl.ContactsCSV)
	mux.HandleFunc("/recall/progress", recallCtrl.Progress)
	mux.HandleFunc("/recall/followup", op(recallCtrl.Followup))
	mux.HandleFunc("/shipments", getOrPost(shipmentCtrl.List, op(shipmentCtrl.Create)))
	mux.HandleFunc("/customers", shipmentCtrl.Customers)
	mux.HandleFunc("/trace/", traceCtrl.TraceHandler)
	mux.HandleFunc("/lots", lotCtrl.ListStockByLot)
	mux.HandleFunc("/warehouses", lotCtrl.Warehouses)

	// Supervisor unlock of a recalled POS terminal (bcrypt, lockout, audit)
	mux.HandleFunc("/auth/supervisor/verify", authCtrl.Verify)
	mux.HandleFunc("/auth/supervisor/audit", authCtrl.Audit)
	mux.HandleFunc("/auth/session", authCtrl.Session)

	// Reports: purchases, lot movement ledger, POS sales
	mux.HandleFunc("/movements", reportsCtrl.Movements)
	mux.HandleFunc("/sales", reportsCtrl.Sales)
	mux.HandleFunc("/reports/assessment", reportsCtrl.Assessment)

	// Lot movements: transfer, customer return, waste, rework (sale/shipment/production elsewhere)
	mux.HandleFunc("/movements/transfer", op(movementsCtrl.Transfer))
	mux.HandleFunc("/movements/return", op(movementsCtrl.Return))
	mux.HandleFunc("/movements/waste", op(movementsCtrl.Waste))
	mux.HandleFunc("/rework", op(movementsCtrl.Rework))
	mux.HandleFunc("/stock-counts", getOrPost(countCtrl.List, op(countCtrl.Create)))

	// Task 4: ML Yield Predictions & Deviation Telemetry
	mux.HandleFunc("/yield", yieldCtrl.PredictYield)
	mux.HandleFunc("/ai/summary", aiCtrl.Summary)
	mux.HandleFunc("/ai/forecast/butchery", aiCtrl.ButcheryForecast)
	mux.HandleFunc("/ai/forecast/butchery/freeze", op(aiCtrl.FreezeButcheryForecast))

	// Alert inbox: yield, purchase cost and refused POS sales; reviewed by a supervisor
	mux.HandleFunc("/alerts", alertCtrl.List)
	mux.HandleFunc("/alerts/review", op(alertCtrl.Review))
	mux.HandleFunc("/alerts/rules", getOrPost(alertCtrl.Rules, op(alertCtrl.UpdateRule)))
	mux.HandleFunc("/alerts/missed", op(alertCtrl.Missed))

	// Offline Edge POS Synchronization (Hardware ECDSA P-256 + UUIDv7 Idempotency)
	mux.HandleFunc("/sync", syncCtrl.IngestOutbox)
	mux.HandleFunc("/sync/register-key", op(syncCtrl.RegisterDeviceKey))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           withCORSAndLogging(withSecurity(newRateLimiter(rateFromEnv()), mux)),
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Graceful shutdown: on SIGINT/SIGTERM stop accepting requests and let running
	// ones finish (up to 10 s) before closing the DB pool.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		logInfo("HTTP", "Go HTTP Server listening on :%s (all interfaces)", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logError("HTTP", "Server fatal error: %v", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	logInfo("HTTP", "Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logError("HTTP", "Graceful shutdown failed: %v", err)
	}
}

func initMySQLPool(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		logError("MYSQL", "Failed to initialize driver handle: %v", err)
		return nil, err
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(2 * time.Minute)

	const maxRetries = 5
	const retryDelay = 2 * time.Second

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		lastErr = db.PingContext(ctx)
		cancel()
		if lastErr == nil {
			logInfo("MYSQL", "Database connection established on attempt %d/%d", attempt, maxRetries)
			return db, nil
		}
		logWarn("MYSQL", "Database ping attempt %d/%d failed: %v (retrying in %s)", attempt, maxRetries, lastErr, retryDelay)
		if attempt < maxRetries {
			time.Sleep(retryDelay)
		}
	}

	logError("MYSQL", "Database connection failed after %d retries: %v", maxRetries, lastErr)
	return db, fmt.Errorf("mysql unreachable after %d retries: %w", maxRetries, lastErr)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func withCORSAndLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); allowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept-Language, Authorization, X-Tenant-ID, X-Device-ID, X-Actor-Role, X-Signature, X-Signature-Alg, X-Key-Fingerprint, X-Idempotency-UUIDv7, X-POS-Public-Key")
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logInfo("HTTP", "%s %s -> %d (%s)", r.Method, r.URL.RequestURI(), rec.status, time.Since(start).Round(time.Millisecond))
	})
}

// getOrPost serves a report (GET) and a new document (POST) on one path: /purchases, /shipments.
func getOrPost(get, post http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			post(w, r)
			return
		}
		get(w, r)
	}
}
