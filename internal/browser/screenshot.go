package browser

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	playwright "github.com/playwright-community/playwright-go"
)

// SaveDebugScreenshot captures a full-page PNG of the current page state and saves it
// to dir with a filename encoding the timestamp, accountID, and operation name.
// If dir is empty or page is nil, the function is a no-op (OBS-03: disabled by default).
// Screenshot failure is logged as a warning but does NOT mask the original error.
//
// Filename format: 20060102-150405-<accountID>-<operation>.png
func SaveDebugScreenshot(page playwright.Page, dir, accountID, operation string) {
	// OBS-03: no-op when dir is empty (disabled by default) or page is unavailable.
	if dir == "" || page == nil {
		return
	}

	// Ensure the screenshot directory exists (create if missing).
	if err := os.MkdirAll(dir, 0750); err != nil {
		slog.Warn("cannot create screenshot dir", "dir", dir, "err", err)
		return
	}

	filename := fmt.Sprintf("%s-%s-%s.png", time.Now().Format("20060102-150405"), accountID, operation)
	path := filepath.Join(dir, filename)

	if _, err := page.Screenshot(playwright.PageScreenshotOptions{
		Path:     playwright.String(path),
		FullPage: playwright.Bool(true),
	}); err != nil {
		slog.Warn("debug screenshot failed", "path", path, "err", err)
		return
	}

	slog.Info("debug screenshot saved", "path", path, "account", accountID, "operation", operation)
}
