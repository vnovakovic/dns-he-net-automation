package browser

import (
	"fmt"
	"log/slog"

	playwright "github.com/playwright-community/playwright-go"
)

// Launcher manages the Playwright + Chromium browser lifecycle.
// It holds a single Playwright driver process and a single Browser instance.
// Per-account isolation is achieved via BrowserContext (see NewAccountContext).
//
// Usage:
//
//	l, err := NewLauncher(true, 0)
//	defer l.Close()
//	ctx, err := l.NewAccountContext(30000)
type Launcher struct {
	pw       *playwright.Playwright
	browser  playwright.Browser
	headless bool
	slowMo   float64
}

// NewLauncher starts the Playwright driver process and launches a Chromium browser.
// headless=true runs without a visible window. slowMo adds delay between actions (ms).
//
// The caller must call Close() to stop the browser and driver when done.
func NewLauncher(headless bool, slowMo float64) (*Launcher, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("start playwright: %w", err)
	}

	opts := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(headless),
	}
	if slowMo > 0 {
		opts.SlowMo = playwright.Float(slowMo)
	}

	browser, err := pw.Chromium.Launch(opts)
	if err != nil {
		// Stop the driver before returning error to avoid process leak.
		pw.Stop() //nolint:errcheck
		return nil, fmt.Errorf("launch chromium: %w", err)
	}

	slog.Info("browser launched", "headless", headless, "slowMo", slowMo)

	return &Launcher{
		pw:       pw,
		browser:  browser,
		headless: headless,
		slowMo:   slowMo,
	}, nil
}

// NewAccountContext creates an isolated BrowserContext for one HE.net account.
// Each context has independent cookies -- no cross-contamination between accounts.
// defaultTimeoutMs sets the default timeout for all Playwright actions in this context.
func (l *Launcher) NewAccountContext(defaultTimeoutMs float64) (playwright.BrowserContext, error) {
	ctx, err := l.browser.NewContext()
	if err != nil {
		return nil, fmt.Errorf("new browser context: %w", err)
	}
	ctx.SetDefaultTimeout(defaultTimeoutMs)
	return ctx, nil
}

// Close stops the Chromium browser and the Playwright driver process.
// It is safe to call Close on a partially-initialized Launcher.
func (l *Launcher) Close() {
	if l.browser != nil {
		l.browser.Close() //nolint:errcheck
	}
	if l.pw != nil {
		l.pw.Stop() //nolint:errcheck
	}
	slog.Info("browser closed")
}

// IsConnected reports whether the browser is still connected to the Playwright driver.
// Used by session health checks to detect a crashed or disconnected browser.
func (l *Launcher) IsConnected() bool {
	return l.browser != nil && l.browser.IsConnected()
}
