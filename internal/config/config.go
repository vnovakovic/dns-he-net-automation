// Package config loads application configuration from environment variables.
package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

// Config holds all application configuration loaded from environment variables.
// All fields use 12-factor style env var binding (OPS-03).
type Config struct {
	// Port is the HTTP listen port (default: 8080).
	Port int `env:"PORT" envDefault:"9001"`

	// MetricsPort is the dedicated Prometheus metrics listen port (default: 9090).
	// Serving metrics on a separate port keeps the scrape target isolated from the API:
	// rate limiting, auth middleware, and TLS termination on the API port do not affect
	// Prometheus scrapers. Set to 0 to disable the dedicated metrics server (metrics
	// will still be available on the main port via /metrics).
	MetricsPort int `env:"METRICS_PORT" envDefault:"9090"`

	// DBPath is the SQLite database file path (default: dnshenet-server.db).
	DBPath string `env:"DB_PATH" envDefault:"dnshenet-server.db"`

	// HEAccountsJSON is used when VAULT_ADDR is not set. Optional when Vault is configured.
	// Format: [{"id":"prod","username":"user","password":"pass"}]
	// SECURITY: This field contains credentials -- never log its value (SEC-03).
	HEAccountsJSON string `env:"HE_ACCOUNTS"`

	// PlaywrightHeadless controls whether Chromium runs in headless mode (default: true).
	// Set to false during development to see the browser window.
	PlaywrightHeadless bool `env:"PLAYWRIGHT_HEADLESS" envDefault:"true"`

	// PlaywrightSlowMo adds a delay (in ms) between Playwright actions for debugging (default: 0).
	PlaywrightSlowMo float64 `env:"PLAYWRIGHT_SLOW_MO" envDefault:"0"`

	// PlaywrightDriverPath is the directory where the Playwright driver binary is stored.
	// When empty, playwright-go uses os.UserCacheDir()/ms-playwright-go/<version> which
	// differs between the installing user and the LocalSystem service account → driver not found.
	//
	// WHY needed for Windows service installs:
	//   playwright.Install() (run by the installer as the admin user) stores the driver in
	//   %LOCALAPPDATA%\ms-playwright-go\1.57.0 (e.g. C:\Users\vladimir\AppData\Local\...).
	//   When the service runs as LocalSystem, os.UserCacheDir() resolves to
	//   C:\Windows\System32\config\systemprofile\AppData\Local — a completely different
	//   directory. playwright.Run() cannot find the driver → "please install the driver first".
	//
	// FIX: set PLAYWRIGHT_DRIVER_PATH=C:\Program Files\dnshenet-server\driver in the
	//   service registry environment (same technique already used for PLAYWRIGHT_BROWSERS_PATH).
	//   The installer passes the same path to playwright-install so the driver lands where
	//   LocalSystem can find it regardless of which user account ran the installer.
	//
	// PREVIOUSLY: driver installed to %LOCALAPPDATA%\ms-playwright-go\1.57.0 (user profile).
	//   Service (LocalSystem) looked in systemprofile\AppData → not found → Error 1053.
	PlaywrightDriverPath string `env:"PLAYWRIGHT_DRIVER_PATH"`

	// OperationTimeoutSec is the per-operation browser timeout in seconds (default: 30).
	OperationTimeoutSec int `env:"OPERATION_TIMEOUT_SEC" envDefault:"30"`

	// OperationQueueTimeoutSec is the max time a request waits for the per-account mutex
	// before returning 429 Too Many Requests (default: 60).
	OperationQueueTimeoutSec int `env:"OPERATION_QUEUE_TIMEOUT_SEC" envDefault:"60"`

	// LogLevel sets the slog logging level: debug, info, warn, error (default: info).
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`

	// MinOperationDelaySec is the minimum delay between browser operations in seconds (default: 1.5).
	// This rate-limits interactions with dns.he.net to avoid triggering server-side rate limits.
	MinOperationDelaySec float64 `env:"MIN_OPERATION_DELAY_SEC" envDefault:"1.5"`

	// SessionMaxAgeSec is the maximum browser session age in seconds before proactive re-login
	// (default: 1800 = 30 minutes).
	SessionMaxAgeSec int `env:"SESSION_MAX_AGE_SEC" envDefault:"1800"`

	// JWTSecret is the HMAC-SHA256 signing secret for JWT bearer tokens.
	// SECURITY: Must be at least 32 characters. Never log this value (SEC-02).
	JWTSecret string `env:"JWT_SECRET,required,notEmpty"`

	// Vault configuration (VAULT-01..06)
	// When VaultAddr is non-empty, VaultProvider is used instead of EnvProvider.
	// SECURITY: VaultToken, AppRoleSecretID are credentials -- never log their values.

	VaultAddr           string `env:"VAULT_ADDR"`
	VaultAuthMethod     string `env:"VAULT_AUTH_METHOD"          envDefault:"token"`
	VaultToken          string `env:"VAULT_TOKEN"`
	VaultAppRoleRoleID  string `env:"VAULT_APPROLE_ROLE_ID"`
	VaultAppRoleSecretID string `env:"VAULT_APPROLE_SECRET_ID"`
	VaultMountPath      string `env:"VAULT_MOUNT_PATH"           envDefault:"secret"`
	VaultSecretPathTmpl string `env:"VAULT_SECRET_PATH_TMPL"     envDefault:"dns-he-net/%s"`
	VaultCredentialTTLSec int  `env:"VAULT_CREDENTIAL_TTL_SEC"   envDefault:"300"`

	// Resilience configuration (RES-02, RES-03)
	RateLimitPerTokenRPM     int    `env:"RATE_LIMIT_PER_TOKEN_RPM"     envDefault:"100"`
	RateLimitGlobalRPM       int    `env:"RATE_LIMIT_GLOBAL_RPM"        envDefault:"1000"`
	CircuitBreakerMaxFailures uint32 `env:"CIRCUIT_BREAKER_MAX_FAILURES" envDefault:"5"`
	CircuitBreakerTimeoutSec  int    `env:"CIRCUIT_BREAKER_TIMEOUT_SEC"  envDefault:"30"`

	// Screenshot configuration (OBS-03)
	// Empty string disables screenshots.
	ScreenshotDir string `env:"SCREENSHOT_DIR"`

	// Maximum inter-operation delay for jitter (BROWSER-08)
	MaxOperationDelaySec float64 `env:"MAX_OPERATION_DELAY_SEC" envDefault:"3.0"`

	// LogFile is an optional path to a log file. When set, slog writes to both
	// stdout AND the file — useful for Windows service mode where stdout is /dev/null.
	// Set LOG_FILE=C:\dnshenet-service.log in the service registry Environment to
	// capture startup errors that would otherwise be invisible.
	LogFile string `env:"LOG_FILE"`

	// TLS configuration.
	// When both SSLCert and SSLKey are set, the server listens on HTTPS with TLS 1.2+.
	// When either is empty the server falls back to plain HTTP (useful for local dev or
	// when TLS is terminated upstream by a reverse proxy).
	// SECURITY: SSLKey is a private key path — never log its contents.
	SSLCert string `env:"SSL_CERT"` // path to PEM-encoded certificate (or chain)
	SSLKey  string `env:"SSL_KEY"`  // path to PEM-encoded private key

	// TokenRecoveryEnabled controls whether raw token strings are stored (encrypted) in the
	// database so that account owners can retrieve a forgotten token via the admin UI by
	// confirming their portal password.
	//
	// ┌─────────────────────────────────────────────────────────────────────────────────┐
	// │  HOW TO ENABLE / DISABLE THIS FEATURE                                          │
	// │                                                                                 │
	// │  Set the environment variable:  TOKEN_RECOVERY_ENABLED=true   (enables)        │
	// │                                 TOKEN_RECOVERY_ENABLED=false  (disables, default)│
	// │                                                                                 │
	// │  When DISABLED (default):                                                       │
	// │    • token_value column stays NULL for all new tokens                           │
	// │    • /admin/tokens/{jti}/reveal always returns 403 Forbidden                   │
	// │    • Existing stored ciphertexts are NOT deleted — they remain in the DB but    │
	// │      are inaccessible until the flag is re-enabled.                             │
	// │                                                                                 │
	// │  When ENABLED:                                                                  │
	// │    • Each newly issued token is encrypted with AES-256-GCM and stored in        │
	// │      token_value. Key = SHA-256("dns-he-net-token-recovery-v1|" + JWT_SECRET). │
	// │    • Tokens issued BEFORE the flag was turned on are NOT retroactively stored.  │
	// │    • The reveal endpoint verifies the caller's portal password before returning  │
	// │      the decrypted token string.                                                 │
	// │                                                                                 │
	// │  SECURITY NOTE:                                                                 │
	// │    Storing encrypted tokens increases the blast radius of a DB + secret leak.   │
	// │    Enable only in deployments where recovery is more important than this risk.   │
	// │    The default is false for this reason.                                         │
	// └─────────────────────────────────────────────────────────────────────────────────┘
	// WHY default true (not false):
	//   Most deployments benefit from recovery — the operator created an admin token and
	//   did not copy it. The previous default of false meant the feature silently did nothing
	//   until explicitly enabled, which was confusing. Operators who want to disable it must
	//   explicitly set TOKEN_RECOVERY_ENABLED=false. The security trade-off (encrypted token
	//   stored in DB) is acceptable for self-hosted deployments where the DB and JWT_SECRET
	//   are already on the same host.
	TokenRecoveryEnabled bool `env:"TOKEN_RECOVERY_ENABLED" envDefault:"true"`

	// Admin UI authentication (UI-04).
	// Both AdminUsername and AdminPassword are required when the admin UI is accessed.
	// ADMIN_SESSION_KEY is separate from JWT_SECRET — rotating the JWT secret must not
	// invalidate admin sessions. AdminSessionKey should be a hex-encoded 32-byte value.
	// (RESEARCH.md open question 2 resolution: separate keys for separate auth domains)
	// SECURITY: Never log these values (SEC-03).
	AdminUsername   string `env:"ADMIN_USERNAME"`
	AdminPassword   string `env:"ADMIN_PASSWORD"`
	AdminSessionKey string `env:"ADMIN_SESSION_KEY"` // hex-encoded 32-byte key for HMAC session signing
}

// Load reads configuration from environment variables and returns a populated Config.
// Returns an error if any required fields are missing or values cannot be parsed.
func Load() (*Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}
