// cmd/usage-tracking/main.go
//
// Lightweight Prometheus-backed accounting service for Authorino callbacks.
//
// POST /track  – increments llm_requests_total{user,groups,path}
// GET  /healthz – 204
// GET  /metrics – Prometheus exposition
//
// Env vars:
//   PORT       (default 8080)
//   LOG_LEVEL  info | debug | warn | error (default info)
//   PPROF      true to expose /debug/pprof (default false)

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // active only if PPROF=true
	"os"
	"os/signal"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var llmRequests = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "llm_requests_total",
		Help: "Successful LLM requests labelled by user, group and path.",
	},
	[]string{"user", "groups", "path"},
)

type trackPayload struct {
	User   string `json:"user"`
	Groups string `json:"groups"`
	Path   string `json:"path"`
	Host   string `json:"host,omitempty"`
	Method string `json:"method,omitempty"`
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newReqID() string { return ulid.Make().String() }

func trackHandler(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID := newReqID()
		logger := log.With("req_id", reqID)

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

func healthHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

func main() {
	// logging
	level := slog.LevelInfo
	switch getEnv("LOG_LEVEL", "info") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	log := slog.New(handler)

	prometheus.MustRegister(llmRequests)

	mux := http.NewServeMux()
	mux.HandleFunc("/track", trackHandler(log))
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", healthHandler)

	if getEnv("PPROF", "false") == "true" {
		log.Info("pprof endpoints enabled at /debug/pprof")
	}

	server := &http.Server{
		Addr:              ":" + getEnv("PORT", "8080"),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		ErrorLog:          slog.NewLogLogger(handler, slog.LevelError),
	}

	go func() {
		log.Info("usage-tracking listening", "addr", server.Addr, "level", level)
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Error("server died", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit
	log.Info("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
