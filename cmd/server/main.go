// Package main is the entry point for the dns-he-net-automation service.
// It loads configuration, initializes the SQLite database, starts the Playwright browser,
// and handles OS signals for graceful shutdown.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	playwright "github.com/playwright-community/playwright-go"
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
	// DIAGNOSTIC: Write a startup trace before any config/slog setup.
	// This file always gets written when the binary starts, even in service mode where
	// stdout is /dev/null. Reading it after a crash reveals exactly how far we got.
	// Remove after the service startup issue is resolved.
	writeEarlyDebug("main() started")
	defer writeEarlyDebug("main() exiting")

	// Load .env file before anything else so all subsequent config reads see its values.
	// Variables already set in the process environment take precedence (godotenv.Load semantics).
	loadEnvFile()
	writeEarlyDebug("loadEnvFile done")

	// Bootstrap subcommand: "./server token create --account <id> --role <role>"
	// Issues an admin/viewer token directly to stdout without going through the HTTP API.
	// This solves the chicken-and-egg: the first token must be created before any API call.
	// Usage: HE_ACCOUNTS=dummy JWT_SECRET=... DB_PATH=... ./server token create --account prod --role admin
	if len(os.Args) >= 2 && os.Args[1] == "token" {
		runTokenCreate()
		return
	}

	// playwright-install subcommand: pre-installs Playwright browser binaries.
	// Required for Windows service deployments where the service account (LocalSystem)
	// does not have access to the installing user's %LOCALAPPDATA%\ms-playwright.
	//
	// Usage (run as Administrator from install dir):
	//   PLAYWRIGHT_BROWSERS_PATH="C:\Program Files\dnshenet-server\browsers" dnshenet-server.exe playwright-install
	//
	// WHY a subcommand instead of auto-install on first start:
	//   playwright.Run() downloads browsers (~200 MB) on first run if they are not found.
	//   In service mode this blocks main() for minutes and causes SCM Error 1053.
	//   Running playwright-install once during setup avoids this delay at service start.
	if len(os.Args) >= 2 && os.Args[1] == "playwright-install" {
		fmt.Println("Installing Playwright browser binaries...")
		// WHY pass DriverDirectory when PLAYWRIGHT_DRIVER_PATH is set:
		//   The installer runs this subcommand as the admin user with PLAYWRIGHT_DRIVER_PATH
		//   pointing to the install dir (e.g. C:\Program Files\dnshenet-server\driver).
		//   Without DriverDirectory the driver would land in the admin user's %LOCALAPPDATA%
		//   which the LocalSystem service account cannot access → "driver not found" on first start.
		//   Passing the same fixed path here and in NewLauncher() keeps the driver in one
		//   location reachable by both the installer context and the running service.
		var installOpts []*playwright.RunOptions
		if driverDir := os.Getenv("PLAYWRIGHT_DRIVER_PATH"); driverDir != "" {
			installOpts = append(installOpts, &playwright.RunOptions{DriverDirectory: driverDir})
		}
		if err := playwright.Install(installOpts...); err != nil {
			fmt.Fprintf(os.Stderr, "error: playwright install: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Done.")
		return
	}

	// gen-cert subcommand: generates a self-signed TLS certificate + private key in PEM format.
	// Called by the Inno Setup installer [Run] section when server.crt does not yet exist.
	//
	// Usage:
	//   dnshenet-server.exe gen-cert --cert <path.crt> --key <path.key>
	//
	// WHY a subcommand instead of bundling certs from the project root:
	//   server.crt and server.key are gitignored — CI builds never have them, so the
	//   Inno Setup installer cannot bundle them. Each install needs its own key pair.
	//   Generating at install time is the correct approach: unique per deployment,
	//   no secrets in the repo, works from both local and CI builds.
	//
	// WHY Go crypto/tls (not openssl):
	//   openssl is not installed by default on Windows. The binary already links
	//   crypto/x509 and crypto/tls, so no extra dependency is introduced.
	//   The cert gets SAN entries for localhost and 127.0.0.1 so browsers accept it
	//   when accessed locally (CN alone is ignored by modern browsers for TLS validation).
	//
	// PREVIOUSLY TRIED: skipifsourcedoesntexist in the .iss [Files] section.
	//   Worked for local builds but broke CI (certs gitignored) → ListenAndServeTLS
	//   failed with "no such file" on first service start → Error 1067.
	if len(os.Args) >= 2 && os.Args[1] == "gen-cert" {
		runGenCert(os.Args[2:])
		return
	}

	// Load configuration from environment variables (OPS-03).
	cfg, err := config.Load()
	if err != nil {
		// Use basic stderr logging before slog is configured.
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	writeEarlyDebug("config loaded: port=" + fmt.Sprint(cfg.Port) + " logfile=" + cfg.LogFile)

	// Configure structured JSON logging based on LogLevel (OPS-03).
	level, err := parseLogLevel(cfg.LogLevel)
	if err != nil {
		slog.Error("invalid LOG_LEVEL", "value", cfg.LogLevel, "error", err)
		os.Exit(1)
	}
	// WHY io.MultiWriter when LOG_FILE is set:
	//   Windows services have stdout/stderr redirected to /dev/null — all slog output
	//   is silently dropped. LOG_FILE lets an operator set a file path via the service
	//   registry Environment (LOG_FILE=C:\dnshenet-service.log) to capture startup
	//   errors that would otherwise be invisible. Without LOG_FILE, logs go to stdout
	//   only (unchanged behaviour for interactive / Docker / systemd deployments).
	var logDest io.Writer = os.Stdout
	if cfg.LogFile != "" {
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot open log file %q: %v\n", cfg.LogFile, err)
		} else {
			defer f.Close()
			logDest = io.MultiWriter(os.Stdout, f)
		}
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(logDest, &slog.HandlerOptions{
		Level: level,
	})))

	// SECURITY: Never log cfg.JWTSecret, cfg.HEAccountsJSON, or any credential value.
	// OPS-02 requires structured fields; SEC-01/SEC-02 prohibit credential exposure.
	envFile := os.Getenv("ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	slog.Info("service starting",
		"port", cfg.Port,
		"db_path", cfg.DBPath,
		"headless", cfg.PlaywrightHeadless,
		"slow_mo", cfg.PlaywrightSlowMo,
		"log_level", cfg.LogLevel,
		"env_file", envFile,
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

	// Resolve admin password: if ADMIN_PASSWORD env var is set, bcrypt-hash it and upsert
	// to the DB (env var overrides DB). If empty, read the stored hash from the DB; on a
	// fresh DB with no hash, seed the default "admin123" bcrypt hash.
	//
	// WHY resolve here (after DB open, before router init):
	//   The router receives the bcrypt hash directly — it never reads the env var or DB.
	//   Centralising the resolution here makes the auth layer stateless with respect to
	//   the DB: once the hash is in the router, password checks are pure in-memory bcrypt.
	//
	// WHY pass the hash (not plaintext) into the router:
	//   Passing plaintext into the router would require it to be stored in a closure for
	//   the lifetime of the server process. A bcrypt hash is safe to store in memory —
	//   even a memory dump reveals only the hash, not the password.
	adminPasswordHash, err := store.EnsureAdminPassword(context.Background(), db, cfg.AdminPassword)
	if err != nil {
		slog.Error("failed to resolve admin password", "error", err)
		os.Exit(1)
	}
	if cfg.AdminPassword != "" {
		slog.Info("admin password set from environment variable (ADMIN_PASSWORD)")
	} else {
		slog.Info("admin password loaded from database")
	}

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

	writeEarlyDebug("db opened, about to call makeShutdownCtx")

	// Set up shutdown context BEFORE browser launch.
	//
	// WHY here (before browser.NewLauncher), not after all initialisation:
	//   When running as a Windows service, makeShutdownCtx() starts svc.Run() in a
	//   goroutine which registers with the SCM and immediately reports StartPending →
	//   Running. The SCM timeout (default 180 s on this machine) starts counting from
	//   the moment the service process starts. browser.NewLauncher() calls
	//   playwright.Run() which can take 10-30 s (driver unpack + Chromium launch).
	//   If PLAYWRIGHT_BROWSERS_PATH is wrong or browsers are missing it can block for
	//   the full 180 s timeout → SCM kills the process → Error 1053 before we ever
	//   reach svc.Run(). Registering with the SCM first means any subsequent failure
	//   produces a more informative error (service stopped unexpectedly / Event Log
	//   shows the actual Go error) instead of a generic timeout.
	//
	// PREVIOUSLY: makeShutdownCtx() was called after all initialisation, which caused
	//   Error 1053 every time the service was started because svc.Run() was never reached
	//   within the 180-second SCM timeout.
	ctx, stop := makeShutdownCtx()
	defer stop()

	writeEarlyDebug("makeShutdownCtx returned (SCM registered), about to launch browser")
	writeEarlyDebug("PLAYWRIGHT_BROWSERS_PATH=" + os.Getenv("PLAYWRIGHT_BROWSERS_PATH"))
	writeEarlyDebug("PLAYWRIGHT_HEADLESS=" + fmt.Sprint(cfg.PlaywrightHeadless))

	// Initialize Playwright browser launcher (BROWSER-01).
	// Pass PlaywrightDriverPath so LocalSystem can find the driver installed during setup.
	// See config.PlaywrightDriverPath and browser.NewLauncher comments for full rationale.
	launcher, err := browser.NewLauncher(cfg.PlaywrightHeadless, cfg.PlaywrightSlowMo, cfg.PlaywrightDriverPath)
	if err != nil {
		writeEarlyDebug("browser.NewLauncher ERROR: " + err.Error())
		slog.Error("failed to launch browser", "error", err)
		os.Exit(1)
	}
	writeEarlyDebug("browser launched OK")
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

	// Wire chi router and start HTTP server (Phase 2).
	// Admin UI credentials are passed here and threaded into RegisterAdminRoutes.
	// This is the SINGLE point where admin config enters the router — plans 03 and 04
	// do not need to change main.go or the NewRouter signature. (UI-01, Checker issue 5 fix)
	// Pass adminPasswordHash (bcrypt hash, not plaintext) — the router and admin UI
	// use bcrypt.CompareHashAndPassword for all admin password checks.
	handler := api.NewRouter(db, sm, launcher, []byte(cfg.JWTSecret), breakers,
		cfg.RateLimitGlobalRPM, cfg.RateLimitPerTokenRPM, vaultHealthFn, reg,
		cfg.AdminUsername, adminPasswordHash, cfg.AdminSessionKey,
		cfg.TokenRecoveryEnabled)

	// Auto-generate a self-signed TLS certificate on first start when SSL_CERT/SSL_KEY paths
	// are configured but the files do not yet exist.
	//
	// WHY here (in main, not only as a CLI subcommand):
	//   Docker containers have no installer or pre-start hook — the Inno Setup [Run] trick
	//   that works on Windows cannot be used. The binary must generate the cert itself on
	//   first run. Checking file existence here, after slog is configured, allows a
	//   structured log message and clean os.Exit on failure.
	//
	// WHY only when both SSL_CERT and SSL_KEY are non-empty:
	//   Empty paths mean the operator chose plain HTTP (reverse proxy, local dev). Auto-gen
	//   must not fire in that case — it would create unexpected cert files in the working dir.
	//
	// WHY not fall back to HTTP if gen fails:
	//   The operator explicitly configured TLS paths. Silently serving HTTP would hide the
	//   misconfiguration and expose the service without encryption. Fail fast is safer.
	//
	// PREVIOUSLY: Docker deployments fell back to HTTP because SSL_CERT/SSL_KEY paths either
	//   pointed to non-existent files (service crash) or were left empty (plain HTTP silently).
	//   This auto-gen ensures HTTPS is available on first container start without any manual step.
	if cfg.SSLCert != "" && cfg.SSLKey != "" {
		_, certMissing := os.Stat(cfg.SSLCert)
		_, keyMissing := os.Stat(cfg.SSLKey)
		if os.IsNotExist(certMissing) || os.IsNotExist(keyMissing) {
			slog.Info("TLS cert/key not found — auto-generating self-signed certificate",
				"cert", cfg.SSLCert, "key", cfg.SSLKey)
			if err := genCertFiles(cfg.SSLCert, cfg.SSLKey); err != nil {
				slog.Error("failed to auto-generate TLS certificate", "error", err)
				os.Exit(1)
			}
			slog.Info("self-signed TLS certificate generated", "cert", cfg.SSLCert, "key", cfg.SSLKey, "valid_years", 10)
		}
	}

	// WHY tls.Config instead of relying on ListenAndServeTLS defaults:
	//   Go's default TLS config allows TLS 1.0/1.1 for broader compatibility, but those
	//   versions have known vulnerabilities (BEAST, POODLE). Setting MinVersion = TLS12
	//   drops support for < TLS 1.2 while retaining TLS 1.3 (which Go also supports).
	//   TLS 1.2 is the minimum accepted by PCI-DSS, NIST SP 800-52r2, and most modern
	//   security scanners. Go's stdlib TLS 1.2 implementation enables AEAD ciphers
	//   (AES-GCM, ChaCha20-Poly1305) by default — no manual cipher list needed.
	//
	// WHY optional TLS (fall back to HTTP when SSL_CERT / SSL_KEY are not set):
	//   Allows plain HTTP for local dev and for deployments where TLS is terminated by
	//   an upstream reverse proxy (nginx, Traefik, AWS ALB). Forcing TLS in those setups
	//   would require clients to bypass the proxy, defeating its purpose.
	//
	// SECURITY: Never log cfg.SSLKey (private key path reveals the key location;
	//   its contents must never appear in logs under any circumstances).
	tlsEnabled := cfg.SSLCert != "" && cfg.SSLKey != ""
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: handler,
	}
	if tlsEnabled {
		srv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	go func() {
		if tlsEnabled {
			slog.Info("https server listening", "port", cfg.Port, "tls_min_version", "TLS1.2", "cert", cfg.SSLCert)
			if err := srv.ListenAndServeTLS(cfg.SSLCert, cfg.SSLKey); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("https server error", "error", err)
				os.Exit(1)
			}
		} else {
			slog.Info("http server listening", "port", cfg.Port)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				// errors.Is(err, http.ErrServerClosed) is required:
				// srv.Shutdown() causes ListenAndServe to return this sentinel value.
				// Without this check, the server logs a spurious fatal error on graceful shutdown (research pitfall #5).
				slog.Error("http server error", "error", err)
				os.Exit(1)
			}
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
	// Load .env file so token create works without manually exporting every variable.
	loadEnvFile()

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

	rawToken, jti, err := token.IssueToken(context.Background(), db, *accountID, *role, *label, *expiresInDays, []byte(cfg.JWTSecret), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: issue token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Token issued (shown once -- store securely):\n")
	fmt.Printf("  JTI:   %s\n", jti)
	fmt.Printf("  Role:  %s\n", *role)
	fmt.Printf("  Token: %s\n", rawToken)
}

// loadEnvFile loads environment variables from a .env file without overwriting
// variables that are already set in the process environment.
//
// WHY godotenv.Load (not Overload):
//   Load() skips keys that are already present in the environment, so explicitly
//   set env vars (e.g. from a Docker/k8s secret, CI pipeline, or shell export) always
//   win over .env file values. This matches the 12-factor app convention: runtime
//   environment has higher precedence than config files.
//
// WHY ENV_FILE override:
//   Different deployment environments (dev, staging, prod) may store the config at
//   different paths. ENV_FILE lets the operator point to any file without changing code.
//   If ENV_FILE is not set, the default ".env" in the working directory is tried.
//
// WHY silent on missing file (os.IsNotExist):
//   .env is optional — deployments that inject all vars via the environment (Docker,
//   systemd EnvironmentFile, k8s envFrom) should not fail just because no .env exists.
//   Any other error (permission denied, malformed file) is still logged as a warning
//   so the operator knows something unexpected happened.
func loadEnvFile() {
	path := os.Getenv("ENV_FILE")
	if path == "" {
		path = ".env"
	}
	if err := godotenv.Load(path); err != nil && !os.IsNotExist(err) {
		// Use basic stderr output — slog is not yet configured at this point.
		fmt.Fprintf(os.Stderr, "warning: could not load env file %q: %v\n", path, err)
	}
}

// genCertFiles generates a self-signed ECDSA-P256 TLS certificate + private key in PEM format
// and writes them to the given paths. Returns an error instead of calling os.Exit so it can
// be used both from the gen-cert subcommand and from the auto-generate path in main().
//
// WHY ECDSA-P256 (not RSA 2048):
//   Smaller key, faster handshake, equivalent security. Supported by all modern TLS stacks.
//   RSA 2048 would also work but is slower and produces a larger key file.
//
// WHY SAN for localhost and 127.0.0.1 (not just CN):
//   Modern browsers and Go's tls.Dial ignore CN for validation — they only check SAN.
//   Without a SAN entry for localhost/127.0.0.1, curl and browsers reject the cert even
//   if the CN matches. Both the DNS name and IP forms are included to cover all access patterns.
//
// WHY extracted from runGenCert:
//   main() needs to call this on Docker first-start (no installer/pre-start hook available
//   in containers). Returning an error instead of os.Exit lets main() log it via slog and
//   exit cleanly with a structured message. runGenCert wraps this for the CLI subcommand path.
func genCertFiles(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generate serial: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "dnshenet-server"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}

	certFile, err := os.Create(certPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", certPath, err)
	}
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		certFile.Close()
		return fmt.Errorf("write cert PEM: %w", err)
	}
	certFile.Close()

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	keyFile, err := os.Create(keyPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", keyPath, err)
	}
	if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		keyFile.Close()
		return fmt.Errorf("write key PEM: %w", err)
	}
	keyFile.Close()

	return nil
}

// runGenCert is the CLI entrypoint for "server gen-cert --cert <path> --key <path>".
// Called by the Inno Setup installer [Run] section when server.crt does not yet exist.
// Wraps genCertFiles and exits with a human-readable message on success or failure.
func runGenCert(args []string) {
	fs := flag.NewFlagSet("gen-cert", flag.ExitOnError)
	certPath := fs.String("cert", "server.crt", "path to write the PEM certificate")
	keyPath := fs.String("key", "server.key", "path to write the PEM private key")
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if err := genCertFiles(*certPath, *keyPath); err != nil {
		fmt.Fprintf(os.Stderr, "gen-cert: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generated self-signed certificate:\n  cert: %s\n  key:  %s\n  valid: 10 years\n", *certPath, *keyPath)
}

// Used for diagnosing service startup failures before slog is configured.
// Remove once the service starts reliably.
func writeEarlyDebug(msg string) {
	f, err := os.OpenFile(`C:\dnshenet-early.log`, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] pid=%d %s\n", time.Now().Format(time.RFC3339), os.Getpid(), msg)
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
