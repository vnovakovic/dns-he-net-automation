package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vnovakov/dns-he-net-automation/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	// Set only the required field; all others should use defaults.
	t.Setenv("HE_ACCOUNTS", `[{"id":"test","username":"user","password":"pass"}]`)

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "dns-he-net.db", cfg.DBPath)
	assert.Equal(t, `[{"id":"test","username":"user","password":"pass"}]`, cfg.HEAccountsJSON)
	assert.True(t, cfg.PlaywrightHeadless)
	assert.Equal(t, 0.0, cfg.PlaywrightSlowMo)
	assert.Equal(t, 30, cfg.OperationTimeoutSec)
	assert.Equal(t, 60, cfg.OperationQueueTimeoutSec)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, 1.5, cfg.MinOperationDelaySec)
	assert.Equal(t, 1800, cfg.SessionMaxAgeSec)
}

func TestLoad_MissingRequired(t *testing.T) {
	// Ensure HE_ACCOUNTS is not set -- Load() must return an error.
	t.Setenv("HE_ACCOUNTS", "")

	// caarlos0/env treats empty string as "not set" for required fields.
	// We also need to ensure that the env var is absent, not just empty.
	// t.Setenv sets to empty string; unset is tested via the env package behavior.
	// The required tag means a missing or empty value returns an error.
	_, err := config.Load()
	assert.Error(t, err, "Load() should fail when HE_ACCOUNTS is empty or missing")
}

func TestLoad_CustomValues(t *testing.T) {
	t.Setenv("HE_ACCOUNTS", `[{"id":"prod","username":"vnovakov","password":"secret"}]`)
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
