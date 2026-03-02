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
	Port int `env:"PORT" envDefault:"8080"`

	// MetricsPort is the dedicated Prometheus metrics listen port (default: 9090).
	// Serving metrics on a separate port keeps the scrape target isolated from the API:
	// rate limiting, auth middleware, and TLS termination on the API port do not affect
	// Prometheus scrapers. Set to 0 to disable the dedicated metrics server (metrics
	// will still be available on the main port via /metrics).
	MetricsPort int `env:"METRICS_PORT" envDefault:"9090"`

	// DBPath is the SQLite database file path (default: dns-he-net.db).
	DBPath string `env:"DB_PATH" envDefault:"dns-he-net.db"`

	// HEAccountsJSON is used when VAULT_ADDR is not set. Optional when Vault is configured.
	// Format: [{"id":"prod","username":"user","password":"pass"}]
	// SECURITY: This field contains credentials -- never log its value (SEC-03).
	HEAccountsJSON string `env:"HE_ACCOUNTS"`

	// PlaywrightHeadless controls whether Chromium runs in headless mode (default: true).
	// Set to false during development to see the browser window.
	PlaywrightHeadless bool `env:"PLAYWRIGHT_HEADLESS" envDefault:"true"`

	// PlaywrightSlowMo adds a delay (in ms) between Playwright actions for debugging (default: 0).
	PlaywrightSlowMo float64 `env:"PLAYWRIGHT_SLOW_MO" envDefault:"0"`

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
