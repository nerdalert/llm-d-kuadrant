// cmd/usage-tracking/main.go
//
// Lightweight Prometheus-backed accounting service for Authorino callbacks.
//
// ┌─ ENDPOINTS ───────────────────────────────────────────────────────────────┐
// │ POST /track   → increments llm_requests_total{user,groups,path}           │
// │ GET  /healthz → 204 No Content                                            │
// │ GET  /metrics → Prometheus exposition                                     │
// │ (opt) /debug/pprof/* when PPROF=true                                       │
// └───────────────────────────────────────────────────────────────────────────┘
//
// ┌─ CONFIGURATION ───────────────────────────────────────────────────────────┐
// │ The service is configured entirely via environment variables              │
// │ (parsed with github.com/spf13/viper):                                     │
// │   PORT       – listen port                 (default: 8080)                │
// │   LOG_LEVEL  – debug | info | warn | error  (default: info)              │
// │   PPROF      – true to expose /debug/pprof  (default: false)             │
// └───────────────────────────────────────────────────────────────────────────┘
//
// © 2025 Brent • Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // only active if PPROF=true
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/viper"
)

// ──────────────────────────── Prometheus metric ──────────────────────────────

var llmRequests = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "llm_requests_total",
		Help: "Successful LLM requests, labelled by user, group and path.",
	},
	[]string{"user", "groups", "path"},
)

// ──────────────────────────────── Payload ────────────────────────────────────

type trackPayload struct {
	User   string `json:"user"`
	Groups string `json:"groups"`
	Path   string `json:"path"`
	Host   string `json:"host,omitempty"`
	Method string `json:"method,omitempty"`
}

// ───────────────────────────── Utilities ─────────────────────────────────────

func newReqID() string { return ulid.Make().String() }

// ───────────────────────────── HTTP handlers ────────────────────────────────

func trackHandler(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := newReqID()
		logger := log.With("req_id", id)

		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			logger.Warn("invalid method", "method", r.Method)
			return
		}
		defer r.Body.Close()

		var p trackPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			logger.Warn("decode error", "err", err)
			return
		}
		if p.User == "" {
			http.Error(w, "missing user", http.StatusUnprocessableEntity)
			logger.Warn("missing user")
			return
		}

		llmRequests.WithLabelValues(p.User, p.Groups, p.Path).Inc()
		logger.Info("counter incremented",
			"user", p.User, "groups", p.Groups, "path", p.Path)
		logger.Debug("payload dump", "payload", p)

		w.WriteHeader(http.StatusAccepted)
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// ──────────────────────────────── main ───────────────────────────────────────

func main() {
	// ── Configuration (viper) ───────────────────────────────────────────────
	viper.AutomaticEnv()
	viper.SetDefault("port", 8080)
	viper.SetDefault("log_level", "info")
	viper.SetDefault("pprof", "false")

	// ── Logging (slog) ──────────────────────────────────────────────────────
	level := slog.LevelInfo
	switch viper.GetString("log_level") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	// ── Prometheus metric registration ──────────────────────────────────────
	prometheus.MustRegister(llmRequests)

	// ── Routing ─────────────────────────────────────────────────────────────
	mux := http.NewServeMux()
	mux.HandleFunc("/track", trackHandler(logger))
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", healthHandler)

	if viper.GetString("pprof") == "true" {
		logger.Info("pprof endpoints enabled at /debug/pprof")
		// net/http/pprof is already registered in DefaultServeMux
	}

	// ── Server setup & graceful shutdown ────────────────────────────────────
	port := viper.GetInt("port")     // <- now used!
	addr := ":" + strconv.Itoa(port) // ":8080" etc.
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	go func() {
		logger.Info("usage-tracking listening", "addr", addr, "level", level)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server died", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit
	logger.Info("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
