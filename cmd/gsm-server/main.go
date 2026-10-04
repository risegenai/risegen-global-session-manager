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
	"strings"
	"syscall"
	"time"

	"github.com/gocql/gocql"
	"github.com/risegenai/risegen-global-session-manager/internal/cassandra"
	"github.com/risegenai/risegen-global-session-manager/internal/handlers"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("gsm: starting")

	hosts := strings.Split(cassandra.GetEnv("GSM_CASSANDRA_HOSTS", "192.168.31.162:9042"), ",")
	keyspace := cassandra.GetEnv("GSM_CASSANDRA_KEYSPACE", "global_sessions")

	var cassSess *gocql.Session
	cassReady := false
	if sess, err := cassandra.Connect(hosts, keyspace, logger); err != nil {
		logger.Warn("gsm: cassandra unavailable, starting without persistence", "error", err)
	} else {
		cassSess = sess
		cassReady = true
		defer sess.Close()
		if err := cassandra.RunMigrations(sess, logger); err != nil {
			logger.Warn("gsm: migration warning", "error", err)
		}
	}

	router := handlers.NewRouter(&handlers.Router{
		Cassandra: cassSess,
		Logger:    logger,
	})

	addr := cassandra.GetEnv("GSM_ADDR", ":8091")
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logger.Info("gsm: listening", "addr", addr, "cassandra", cassReady)
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