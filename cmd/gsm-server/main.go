// Global Session Manager (GSM) — superior-scope session instance.
//
// Owns global-scope sessions (Conclave, Medium Mode, etc.) and aggregates
// Nexus-scoped sessions by reference. Separate keyspace global_sessions on
// the shared Cassandra cluster. Does not replace the per-Nexus Session Manager
// (ADR 0018).
//
// Hub ADR 0083 · risegen-gen docs/specs/global-session-manager/
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("gsm: starting")

	addr := getEnv("GSM_ADDR", ":8091")
	srv := &http.Server{
		Addr:         addr,
		Handler:      http.HandlerFunc(healthzHandler),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logger.Info("gsm: listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("gsm: serve error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("gsm: shutting down", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("gsm: shutdown error", "error", err)
	}
	logger.Info("gsm: stopped")
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}