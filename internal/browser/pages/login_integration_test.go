//go:build integration

package pages

import (
	"encoding/json"
	"os"
	"testing"

	playwright "github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// heAccount is used to parse the HE_ACCOUNTS JSON env var for integration tests.
type heAccount struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// TestLogin_Integration performs a real login against dns.he.net.
// It is skipped unless the HE_ACCOUNTS environment variable is set.
// Run with: go test -tags integration ./internal/browser/pages/...
func TestLogin_Integration(t *testing.T) {
	accountsJSON := os.Getenv("HE_ACCOUNTS")
	if accountsJSON == "" {
		t.Skip("HE_ACCOUNTS env var not set; skipping integration test")
	}

	var accounts []heAccount
	require.NoError(t, json.Unmarshal([]byte(accountsJSON), &accounts),
		"HE_ACCOUNTS must be a valid JSON array")
	require.NotEmpty(t, accounts, "HE_ACCOUNTS must contain at least one account")

	acct := accounts[0]

	// Start Playwright and launch Chromium headless.
	pw, err := playwright.Run()
	require.NoError(t, err, "start playwright")
	t.Cleanup(func() { pw.Stop() }) //nolint:errcheck

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	require.NoError(t, err, "launch chromium")
	t.Cleanup(func() { browser.Close() }) //nolint:errcheck

	ctx, err := browser.NewContext()
	require.NoError(t, err, "new browser context")
	t.Cleanup(func() { ctx.Close() }) //nolint:errcheck

	page, err := ctx.NewPage()
	require.NoError(t, err, "new page")

	lp := NewLoginPage(page)

	// Perform login.
	err = lp.Login(acct.Username, acct.Password)
	require.NoError(t, err, "Login() must succeed")

	// Verify IsLoggedIn returns true on the same page.
	loggedIn, err := lp.IsLoggedIn()
	require.NoError(t, err, "IsLoggedIn() must not error")
	assert.True(t, loggedIn, "IsLoggedIn() must return true after successful login")
}
