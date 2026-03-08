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
// driverDir is the directory where the Playwright driver binary was installed by
// playwright-install. Pass "" to use the default os.UserCacheDir() location (suitable
// for interactive / Docker use; NOT suitable for Windows service running as LocalSystem).
//
// WHY driverDir is explicit (not derived from PLAYWRIGHT_BROWSERS_PATH):
//   PLAYWRIGHT_BROWSERS_PATH controls where browser binaries live; the driver is a
//   separate binary that playwright-go uses to communicate with the browser. When the
//   service runs as LocalSystem its os.UserCacheDir() differs from the installing user's
//   profile, so the driver installed during setup is not found. Passing driverDir from
//   PLAYWRIGHT_DRIVER_PATH (set in the service registry) ensures both the installer and
//   the service use the same fixed path.
//
// The caller must call Close() to stop the browser and driver when done.
func NewLauncher(headless bool, slowMo float64, driverDir string) (*Launcher, error) {
	// Build RunOptions only when a custom driver directory is specified.
	// Passing an empty RunOptions would still override defaults, so only append when set.
	var runOpts []*playwright.RunOptions
	if driverDir != "" {
		runOpts = append(runOpts, &playwright.RunOptions{DriverDirectory: driverDir})
	}
	pw, err := playwright.Run(runOpts...)
	if err != nil {
		return nil, fmt.Errorf("start playwright: %w", err)
	}

	// WHY --no-sandbox:
	//   When the service runs as LocalSystem (NT AUTHORITY\SYSTEM), Chromium's
	//   sandbox cannot create the required lower-privileged process token because
	//   SYSTEM is at the highest privilege level. Without --no-sandbox the sandbox
	//   init hangs for the full SCM ServicesPipeTimeout (180 s on this machine) and
	//   the service is killed before it ever reports SERVICE_RUNNING → Error 1053.
	//   Our usage of Chromium is limited to automating dns.he.net — no untrusted
	//   content is loaded — so disabling the sandbox is acceptable here.
	//
	// WHY --disable-gpu:
	//   LocalSystem has no desktop session or GPU context. Chromium's GPU process
	//   tries to initialize DirectX/D3D and can hang or crash when no display device
	//   is available. Disabling GPU rendering forces Chromium to use the software
	//   rasterizer, which works reliably in a headless service environment.
	//
	// PREVIOUSLY TRIED: running without these flags as LocalSystem.
	//   playwright.Run() returned successfully but pw.Chromium.Launch() blocked for
	//   exactly 180 seconds (ServicesPipeTimeout) before the SCM killed the process.
	//   Same binary works fine when started interactively as a regular user.
	opts := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(headless),
		Args:     []string{"--no-sandbox", "--disable-gpu"},
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
