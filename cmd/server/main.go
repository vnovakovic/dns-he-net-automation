// Package main is the entry point for the dns-he-net-automation service.
// It loads configuration, initializes the SQLite database, and handles OS signals
// for graceful shutdown.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/vnovakov/dns-he-net-automation/internal/config"
	"github.com/vnovakov/dns-he-net-automation/internal/store"
)

func main() {
	// Load configuration from environment variables (OPS-03).
	cfg, err := config.Load()
	if err != nil {
		// Use basic stderr logging before slog is configured.
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Configure structured JSON logging based on LogLevel (OPS-03).
	level, err := parseLogLevel(cfg.LogLevel)
	if err != nil {
		slog.Error("invalid LOG_LEVEL", "value", cfg.LogLevel, "error", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})))

	// Log startup -- NEVER log HE_ACCOUNTS or any credentials (SEC-03).
	slog.Info("service starting",
		"port", cfg.Port,
		"db_path", cfg.DBPath,
		"headless", cfg.PlaywrightHeadless,
		"slow_mo", cfg.PlaywrightSlowMo,
		"log_level", cfg.LogLevel,
	)

	// Initialize SQLite database with WAL mode and migrations (OPS-06, REL-01).
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("failed to close database", "error", err)
		}
	}()

	slog.Info("database ready", "db_path", cfg.DBPath)

	// Set up OS signal handling for graceful shutdown (SIGTERM for Docker/k8s, SIGINT for Ctrl+C).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// TODO (Plan 01-02): Initialize Playwright browser and session manager here.
	// TODO (Phase 2): Start HTTP API server here.

	slog.Info("service ready, waiting for requests or shutdown signal",
		"port", cfg.Port,
	)

	// Block until a shutdown signal is received.
	<-ctx.Done()

	slog.Info("shutting down", "signal", ctx.Err())
}

// parseLogLevel converts a log level string to the corresponding slog.Level.
// Supported values: "debug", "info", "warn", "error" (case-insensitive via slog).
func parseLogLevel(level string) (slog.Level, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return slog.LevelInfo, err
	}
	return l, nil
}
