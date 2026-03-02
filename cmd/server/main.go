// Package main is the entry point for the dns-he-net-automation service.
// It loads configuration, initializes the SQLite database, starts the Playwright browser,
// and handles OS signals for graceful shutdown.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vnovakov/dns-he-net-automation/internal/api"
	"github.com/vnovakov/dns-he-net-automation/internal/browser"
	"github.com/vnovakov/dns-he-net-automation/internal/config"
	"github.com/vnovakov/dns-he-net-automation/internal/credential"
	"github.com/vnovakov/dns-he-net-automation/internal/metrics"
	"github.com/vnovakov/dns-he-net-automation/internal/resilience"
	"github.com/vnovakov/dns-he-net-automation/internal/store"
	"github.com/vnovakov/dns-he-net-automation/internal/token"
)

func main() {
	// Bootstrap subcommand: "./server token create --account <id> --role <role>"
	// Issues an admin/viewer token directly to stdout without going through the HTTP API.
	// This solves the chicken-and-egg: the first token must be created before any API call.
	// Usage: HE_ACCOUNTS=dummy JWT_SECRET=... DB_PATH=... ./server token create --account prod --role admin
	if len(os.Args) >= 2 && os.Args[1] == "token" {
		runTokenCreate()
		return
	}

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

	// SECURITY: Never log cfg.JWTSecret, cfg.HEAccountsJSON, or any credential value.
	// OPS-02 requires structured fields; SEC-01/SEC-02 prohibit credential exposure.
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

	// Initialize credential provider. Priority: Vault > HE_ACCOUNTS env var > DB.
	//
	// WHY three-tier priority:
	//   Vault   — production: secret rotation, audit log, multi-host (highest security)
	//   Env var — CI/test: credentials injected at runtime without a Vault instance
	//   DB      — default for self-hosted: credentials stored in admin UI, no env var needed
	//
	// SECURITY (SEC-03): Never log credential values (usernames, passwords, tokens).
	var credProvider credential.Provider
	if cfg.VaultAddr != "" {
		slog.Info("using Vault credential provider", "vault_addr", cfg.VaultAddr, "auth_method", cfg.VaultAuthMethod)
		vaultProvider, err := credential.NewVaultProvider(&credential.VaultConfig{
			VaultAddr:             cfg.VaultAddr,
			VaultAuthMethod:       cfg.VaultAuthMethod,
			VaultToken:            cfg.VaultToken,
			VaultAppRoleRoleID:    cfg.VaultAppRoleRoleID,
			VaultAppRoleSecretID:  cfg.VaultAppRoleSecretID,
			VaultMountPath:        cfg.VaultMountPath,
			VaultSecretPathTmpl:   cfg.VaultSecretPathTmpl,
			VaultCredentialTTLSec: cfg.VaultCredentialTTLSec,
		})
		if err != nil {
			slog.Error("failed to initialize Vault provider", "error", err)
			os.Exit(1)
		}
		credProvider = vaultProvider
	} else if cfg.HEAccountsJSON != "" {
		// HE_ACCOUNTS env var present — use EnvProvider (backward compatible with Phase 1-4).
		slog.Info("using env credential provider (HE_ACCOUNTS)")
		envProvider, err := credential.NewEnvProvider(cfg.HEAccountsJSON)
		if err != nil {
			slog.Error("failed to initialize env credential provider", "error", err)
			os.Exit(1)
		}
		credProvider = envProvider
		// Log account IDs only — SECURITY (SEC-03): never log usernames or passwords.
		ids, err := credProvider.ListAccountIDs(context.Background())
		if err != nil {
			slog.Error("failed to list account IDs", "error", err)
			os.Exit(1)
		}
		slog.Info("accounts loaded from env", "count", len(ids), "ids", ids)
	} else {
		// No Vault, no HE_ACCOUNTS — use DB-backed provider.
		// Credentials are read from the accounts table (set via admin UI).
		// This is the default for self-hosted single-operator deployments.
		slog.Info("using DB credential provider (credentials managed via admin UI)")
		credProvider = credential.NewDBProvider(db)
	}

	// Initialize Prometheus metrics registry (OBS-01).
	// All application metrics (HTTP, browser, queue, sessions) are scoped to this registry.
	// The registry is passed to NewSessionManager and NewRouter; it is never registered on
	// prometheus.DefaultRegisterer (custom registry pattern — avoids test panics).
	reg := metrics.NewRegistry()

	// Initialize Playwright browser launcher (BROWSER-01).
	launcher, err := browser.NewLauncher(cfg.PlaywrightHeadless, cfg.PlaywrightSlowMo)
	if err != nil {
		slog.Error("failed to launch browser", "error", err)
		os.Exit(1)
	}
	// Close browser BEFORE the signal wait so defer executes on both normal exit and signal.
	defer launcher.Close()

	// Build session manager durations from config int/float fields.
	queueTimeout := time.Duration(cfg.OperationQueueTimeoutSec) * time.Second
	opTimeout := time.Duration(cfg.OperationTimeoutSec) * time.Second
	reloginAge := time.Duration(cfg.SessionMaxAgeSec) * time.Second
	minOpDelay := time.Duration(cfg.MinOperationDelaySec * float64(time.Second))
	maxOpDelay := time.Duration(cfg.MaxOperationDelaySec * float64(time.Second))

	// Create session manager with per-account mutex serialization (REL-02, REL-03).
	sm := browser.NewSessionManager(launcher, credProvider, queueTimeout, opTimeout, reloginAge, minOpDelay, maxOpDelay, cfg.ScreenshotDir, reg)
	defer sm.Close()

	// Initialize per-account circuit breaker registry (RES-02, RES-03).
	breakers := resilience.NewBreakerRegistry(cfg.CircuitBreakerMaxFailures, cfg.CircuitBreakerTimeoutSec)

	// Build vaultHealthFn closure for the /healthz endpoint (VAULT-04).
	// When VaultProvider is active, calls Vault Sys().Health() on each request.
	// When EnvProvider is active, returns "disabled" (no Vault configured).
	var vaultHealthFn func() string
	if cfg.VaultAddr != "" {
		vp := credProvider.(*credential.VaultProvider)
		vaultHealthFn = func() string {
			health, err := vp.Client().Sys().Health()
			if err != nil {
				return "degraded: " + err.Error()
			}
			if !health.Initialized || health.Sealed {
				return "degraded: vault sealed or uninitialized"
			}
			return "ok"
		}
	} else {
		vaultHealthFn = func() string { return "disabled" }
	}

	// Set up OS signal handling for graceful shutdown (SIGTERM for Docker/k8s, SIGINT for Ctrl+C).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Wire chi router and start HTTP server (Phase 2).
	// Admin UI credentials are passed here and threaded into RegisterAdminRoutes.
	// This is the SINGLE point where admin config enters the router — plans 03 and 04
	// do not need to change main.go or the NewRouter signature. (UI-01, Checker issue 5 fix)
	handler := api.NewRouter(db, sm, launcher, []byte(cfg.JWTSecret), breakers,
		cfg.RateLimitGlobalRPM, cfg.RateLimitPerTokenRPM, vaultHealthFn, reg,
		cfg.AdminUsername, cfg.AdminPassword, cfg.AdminSessionKey)
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: handler,
	}

	go func() {
		slog.Info("http server listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// errors.Is(err, http.ErrServerClosed) is required:
			// srv.Shutdown() causes ListenAndServe to return this sentinel value.
			// Without this check, the server logs a spurious fatal error on graceful shutdown (research pitfall #5).
			slog.Error("http server error", "error", err)
			os.Exit(1)
		}
	}()

	// Dedicated metrics server on MetricsPort (default 9090).
	//
	// WHY a separate port for /metrics (not only on the main API port):
	//   The main port may sit behind a TLS terminator, auth proxy, or rate limiter that
	//   blocks Prometheus scrapers. A dedicated metrics port bypasses all of that — scrapers
	//   target :9090 directly on the internal network without credentials or TLS.
	//   Set METRICS_PORT=0 to disable this server (metrics remain available on the main port).
	var metricsSrv *http.Server
	if cfg.MetricsPort > 0 {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", reg.Handler())
		metricsSrv = &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.MetricsPort),
			Handler: metricsMux,
		}
		go func() {
			slog.Info("metrics server listening", "port", cfg.MetricsPort)
			if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("metrics server error", "error", err)
				os.Exit(1)
			}
		}()
	}

	slog.Info("service ready, waiting for requests or shutdown signal", "port", cfg.Port)

	// Block until a shutdown signal is received.
	<-ctx.Done()
	stop() // release signal resources

	slog.Info("shutting down http server")

	// Use context.Background() (not the signal context) for the shutdown timeout.
	// The signal context is already cancelled at this point — using it would give an
	// already-cancelled context to Shutdown, causing immediate abort rather than a 30s drain.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http server shutdown error", "error", err)
	}
	slog.Info("http server stopped")

	if metricsSrv != nil {
		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("metrics server shutdown error", "error", err)
		}
		slog.Info("metrics server stopped")
	}

	// Note: deferred sm.Close(), launcher.Close(), db.Close() run after this in LIFO order.
	// sm was registered last (runs first), then launcher, then db (registered first, runs last).
	slog.Info("shutting down", "reason", "signal")
}

// runTokenCreate implements the "token create" bootstrap subcommand.
//
// Usage: HE_ACCOUNTS=dummy JWT_SECRET=<secret> DB_PATH=<path> ./server token create --account <id> --role <role>
//
// This subcommand issues a token without going through the HTTP API, solving the
// chicken-and-egg problem of needing a token to make API calls.
//
// Note: HE_ACCOUNTS can be set to any non-empty value (e.g., "dummy") since this subcommand
// only touches the DB and JWT signing — browser/credential functionality is not used.
func runTokenCreate() {
	// Parse flags: --account, --role, --label, --expires-in-days
	// os.Args layout: [binary, "token", "create", --account, ..., --role, ...]
	// We skip "create" (os.Args[2]) and parse from os.Args[3:].
	// If os.Args[2] is not "create", treat all of os.Args[2:] as flags (backward compat).
	fs := flag.NewFlagSet("token create", flag.ExitOnError)
	accountID := fs.String("account", "", "Account ID to scope the token to (required)")
	role := fs.String("role", "admin", "Token role: admin or viewer")
	label := fs.String("label", "", "Optional human-readable label")
	expiresInDays := fs.Int("expires-in-days", 0, "Expiry in days; 0 = unlimited")

	parseArgs := os.Args[2:]
	if len(parseArgs) > 0 && parseArgs[0] == "create" {
		parseArgs = parseArgs[1:]
	}
	_ = fs.Parse(parseArgs)

	if *accountID == "" {
		fmt.Fprintln(os.Stderr, "error: --account is required")
		fmt.Fprintln(os.Stderr, "usage: HE_ACCOUNTS=dummy JWT_SECRET=<secret> DB_PATH=<path> ./server token create --account <id> --role admin|viewer [--label <text>] [--expires-in-days <n>]")
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
		fmt.Fprintln(os.Stderr, "note: set HE_ACCOUNTS=dummy for token create subcommand (browser not used)")
		os.Exit(1)
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Ensure the account row exists (tokens table has FK -> accounts).
	// Bootstrap scenario: account may not yet be registered via POST /api/v1/accounts.
	// INSERT OR IGNORE so repeated bootstrap calls are idempotent.
	_, err = db.ExecContext(context.Background(),
		`INSERT OR IGNORE INTO accounts (id, username) VALUES (?, ?)`,
		*accountID, *accountID,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: ensure account: %v\n", err)
		os.Exit(1)
	}

	rawToken, jti, err := token.IssueToken(context.Background(), db, *accountID, *role, *label, *expiresInDays, []byte(cfg.JWTSecret))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: issue token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Token issued (shown once -- store securely):\n")
	fmt.Printf("  JTI:   %s\n", jti)
	fmt.Printf("  Role:  %s\n", *role)
	fmt.Printf("  Token: %s\n", rawToken)
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
