package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vnovakov/dns-he-net-automation/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	// Set only the required fields; all others should use defaults.
	// HE_ACCOUNTS is now optional (VaultAddr may be set instead).
	t.Setenv("HE_ACCOUNTS", `[{"id":"test","username":"user","password":"pass"}]`)
	t.Setenv("JWT_SECRET", "test-secret-at-least-32-chars-long")

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, 9001, cfg.Port)
	assert.Equal(t, "dnshenet-server.db", cfg.DBPath)
	assert.Equal(t, `[{"id":"test","username":"user","password":"pass"}]`, cfg.HEAccountsJSON)
	assert.True(t, cfg.PlaywrightHeadless)
	assert.Equal(t, 0.0, cfg.PlaywrightSlowMo)
	assert.Equal(t, 30, cfg.OperationTimeoutSec)
	assert.Equal(t, 60, cfg.OperationQueueTimeoutSec)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, 1.5, cfg.MinOperationDelaySec)
	assert.Equal(t, 1800, cfg.SessionMaxAgeSec)

	// Vault defaults (VAULT-01..06)
	assert.Equal(t, "", cfg.VaultAddr)
	assert.Equal(t, "token", cfg.VaultAuthMethod)
	assert.Equal(t, "", cfg.VaultToken)
	assert.Equal(t, "secret", cfg.VaultMountPath)
	assert.Equal(t, "dns-he-net/%s", cfg.VaultSecretPathTmpl)
	assert.Equal(t, 300, cfg.VaultCredentialTTLSec)

	// Resilience defaults (RES-02, RES-03)
	assert.Equal(t, 100, cfg.RateLimitPerTokenRPM)
	assert.Equal(t, 1000, cfg.RateLimitGlobalRPM)
	assert.Equal(t, uint32(5), cfg.CircuitBreakerMaxFailures)
	assert.Equal(t, 30, cfg.CircuitBreakerTimeoutSec)

	// Screenshot default (OBS-03): empty = disabled
	assert.Equal(t, "", cfg.ScreenshotDir)

	// Jitter default (BROWSER-08)
	assert.Equal(t, 3.0, cfg.MaxOperationDelaySec)
}

func TestLoad_MissingRequired(t *testing.T) {
	// HE_ACCOUNTS is now optional (Vault may be configured instead).
	// JWT_SECRET remains required -- ensure Load() fails when it is absent.
	t.Setenv("HE_ACCOUNTS", "")
	t.Setenv("JWT_SECRET", "")

	_, err := config.Load()
	assert.Error(t, err, "Load() should fail when JWT_SECRET is empty or missing")
}

func TestLoad_CustomValues(t *testing.T) {
	t.Setenv("HE_ACCOUNTS", `[{"id":"prod","username":"vnovakov","password":"secret"}]`)
	t.Setenv("JWT_SECRET", "custom-secret-at-least-32-chars-long")
	t.Setenv("PORT", "9090")
	t.Setenv("DB_PATH", "/data/custom.db")
	t.Setenv("PLAYWRIGHT_HEADLESS", "false")
	t.Setenv("PLAYWRIGHT_SLOW_MO", "100.5")
	t.Setenv("OPERATION_TIMEOUT_SEC", "60")
	t.Setenv("OPERATION_QUEUE_TIMEOUT_SEC", "120")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("MIN_OPERATION_DELAY_SEC", "2.0")
	t.Setenv("SESSION_MAX_AGE_SEC", "900")

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, 9090, cfg.Port)
	assert.Equal(t, "/data/custom.db", cfg.DBPath)
	assert.False(t, cfg.PlaywrightHeadless)
	assert.Equal(t, 100.5, cfg.PlaywrightSlowMo)
	assert.Equal(t, 60, cfg.OperationTimeoutSec)
	assert.Equal(t, 120, cfg.OperationQueueTimeoutSec)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, 2.0, cfg.MinOperationDelaySec)
	assert.Equal(t, 900, cfg.SessionMaxAgeSec)
}
