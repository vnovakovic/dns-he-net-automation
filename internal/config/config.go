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

	// DBPath is the SQLite database file path (default: dns-he-net.db).
	DBPath string `env:"DB_PATH" envDefault:"dns-he-net.db"`

	// HEAccountsJSON is a JSON array of HE.net account credentials (required, must not be empty).
	// Format: [{"id":"prod","username":"user","password":"pass"}]
	// SECURITY: This field contains credentials -- never log its value (SEC-03).
	// notEmpty ensures the var must exist AND be non-empty (required only checks existence).
	HEAccountsJSON string `env:"HE_ACCOUNTS,required,notEmpty"`

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
