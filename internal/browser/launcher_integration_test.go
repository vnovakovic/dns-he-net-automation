//go:build integration

package browser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewLauncher_Integration tests launching real Chromium via Playwright.
// Requires: playwright install --with-deps chromium
// Run with: go test -tags integration ./internal/browser/...
func TestNewLauncher_Integration(t *testing.T) {
	l, err := NewLauncher(true, 0)
	if err != nil {
		t.Skipf("playwright not installed or chromium unavailable: %v", err)
	}
	defer l.Close()

	assert.True(t, l.IsConnected(), "browser should be connected after launch")

	ctx, err := l.NewAccountContext(30000)
	require.NoError(t, err)
	defer ctx.Close()

	page, err := ctx.NewPage()
	require.NoError(t, err)
	defer page.Close()

	assert.NotNil(t, page)
}
