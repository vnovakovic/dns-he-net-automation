// cmd/screenshots/main.go — Captures portal UI screenshots for the About page workflow section.
//
// WHY a standalone command (not a test or shell script):
//   Screenshots must reflect the actual running portal with real browser rendering.
//   A Go command using playwright-go can reuse the same dependency already vendored for
//   browser automation. Shell-based curl cannot capture rendered HTML; a headless browser is
//   required to get pixel-accurate screenshots of the admin UI.
//
// WHY demo account "example-org":
//   Using a real HE.net account in screenshots would expose credentials and real domain names
//   in git-committed PNGs. The demo account uses fictional data (admin@example.org) so images
//   are safe to commit. The account is deleted at the end of the run.
//
// USAGE:
//   go run ./cmd/screenshots/ [flags]
//   go run ./cmd/screenshots/ --url https://localhost:9001 --username admin --password admin123
//
// OUTPUT: PNG files written to --out directory (default: internal/api/admin/static/screenshots/).
// Existing files are overwritten; no other files are modified.
//
// DEPENDENCY: The portal must be running and reachable at --url before this command is executed.
// DEPENDENCY: playwright-go must be installed; the command calls playwright.Install() on startup.
//
// UI NOTES (important for selector accuracy):
//   - accounts.templ: "Register Account" form is always visible at the bottom of the page.
//     Fields: id="account_name", id="username", id="password". Submit: "Register".
//   - tokens.templ: Tokens loaded on-demand via htmx "Load Tokens" button per account card.
//     After loading, IssueTokenForm appears with input[name=label] and button "Issue Token".
//   - Show button: pure JS onclick="showRevealDialog(this.dataset.jti)" → opens <dialog id="reveal-dialog">.
//
// WHY JavaScript injection for form fills and button clicks (not playwright Fill/Click):
//   playwright's Fill() and Click() wait 30s for an element to be "interactable" (visible,
//   enabled, stable). On a page with htmx and self-signed TLS the element can be present but
//   fail playwright's interactability probe. JS injection sets .value directly and dispatches
//   click events, which is simpler and more reliable for screenshot capture.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	playwright "github.com/playwright-community/playwright-go"
)

func main() {
	url := flag.String("url", "https://localhost:9001", "Admin portal base URL")
	username := flag.String("username", "admin", "Admin username")
	password := flag.String("password", "admin123", "Admin password")
	outDir := flag.String("out", "internal/api/admin/static/screenshots", "Output directory for screenshots")
	flag.Parse()

	// Ensure playwright browsers are installed.
	// WHY Install() here (not assumed to be pre-installed):
	//   The binary may run on a fresh CI runner or a developer machine that has never
	//   run playwright before. Install() is a no-op if browsers are already present.
	if err := playwright.Install(); err != nil {
		log.Fatalf("playwright.Install: %v", err)
	}

	pw, err := playwright.Run()
	if err != nil {
		log.Fatalf("playwright.Run: %v", err)
	}
	defer pw.Stop() //nolint:errcheck

	// WHY IgnoreHTTPSErrors: true:
	//   The portal runs with a self-signed TLS certificate (see MEMORY.md).
	//   Without this flag every navigation would fail with ERR_CERT_AUTHORITY_INVALID.
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		log.Fatalf("Launch browser: %v", err)
	}
	defer browser.Close() //nolint:errcheck

	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		IgnoreHttpsErrors: playwright.Bool(true),
		Viewport: &playwright.Size{
			Width:  1280,
			Height: 800,
		},
	})
	if err != nil {
		log.Fatalf("NewContext: %v", err)
	}
	defer ctx.Close() //nolint:errcheck

	page, err := ctx.NewPage()
	if err != nil {
		log.Fatalf("NewPage: %v", err)
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("MkdirAll %s: %v", *outDir, err)
	}

	// settle pauses briefly so CSS transitions finish before the screenshot.
	settle := func() { time.Sleep(500 * time.Millisecond) }

	// maskUsernames hides the (username) span rendered next to account names.
	// WHY two selectors:
	//   accounts.templ renders: <h3>acc.Name <span class="text-muted">(acc.Username)</span></h3>
	//   tokens.templ renders:   <div class="card-header"><strong>...</strong> <span class="text-muted">(...)</span>
	//   Both selectors are needed to catch both page layouts.
	// WHY visibility:hidden (not display:none): keeps layout stable; the blank space
	//   where the username was becomes plain white — no element reflow.
	maskUsernames := func() {
		_, _ = page.Evaluate(`
			document.querySelectorAll('h3 span.text-muted, .card-header span.text-muted').forEach(el => {
				el.style.visibility = 'hidden';
			});
		`)
	}

	shot := func(name string) {
		maskUsernames()
		settle()
		path := filepath.Join(*outDir, name)
		if _, err := page.Screenshot(playwright.PageScreenshotOptions{
			Path:     playwright.String(path),
			FullPage: playwright.Bool(false),
		}); err != nil {
			log.Printf("screenshot %s: %v", name, err)
		} else {
			log.Printf("saved %s", path)
		}
	}

	nav := func(u string) {
		if _, err := page.Goto(u, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateNetworkidle,
		}); err != nil {
			log.Fatalf("goto %s: %v", u, err)
		}
		settle()
	}

	// jsv sets a form input value via JavaScript, bypassing playwright's interactability check.
	// WHY JS (not page.Fill): playwright Fill waits 30s for an element to pass its "can be typed
	// into" probe. On some page configurations (htmx-heavy, self-signed TLS) this probe times out
	// even when the element is visually present. Setting .value + dispatching 'input' is reliable.
	jsv := func(selector, val string) {
		// WHY map[string]interface{} (not map[string]string):
		//   playwright-go's serializeValue panics on map[string]string — it only handles
		//   map[string]interface{} when serialising arguments to pass to the JS runtime.
		_, _ = page.Evaluate(`(args) => {
			const el = document.querySelector(args.sel);
			if (el) { el.value = args.val; el.dispatchEvent(new Event('input', {bubbles:true})); }
		}`, map[string]interface{}{"sel": selector, "val": val})
	}

	// jscl clicks a DOM element by JS, bypassing playwright's "element must be stable" check.
	jscl := func(selector string) {
		_, _ = page.Evaluate(`(sel) => {
			const el = document.querySelector(sel);
			if (el) el.click();
		}`, selector)
	}

	waitIdle := func() {
		_ = page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State: playwright.LoadStateNetworkidle,
		})
	}

	// ── Step 1: Login page ────────────────────────────────────────────────────
	nav(*url + "/admin/login")
	shot("01-login.png")

	// Fill login form via native playwright (login page has no htmx complications).
	if err := page.Fill("input[name=username]", *username); err != nil {
		log.Fatalf("fill username: %v", err)
	}
	if err := page.Fill("input[name=password]", *password); err != nil {
		log.Fatalf("fill password: %v", err)
	}
	if err := page.Click("button[type=submit]"); err != nil {
		log.Fatalf("click submit: %v", err)
	}
	waitIdle()

	// ── Step 2: Accounts list ─────────────────────────────────────────────────
	nav(*url + "/admin/accounts")
	shot("02-accounts.png")

	// ── Step 3: Register Account form filled with demo data ───────────────────
	// accounts.templ always renders the "Register Account" form at the bottom of the page
	// (no toggle button needed). Fill via JS to bypass playwright's interactability probe.
	// Scroll the form into view first so it is visible in the 800-px-tall screenshot.
	_, _ = page.Evaluate(`document.querySelector('#account_name')?.scrollIntoView({behavior:'instant',block:'center'})`)
	settle()
	jsv("#account_name", "example-org")
	jsv("#username", "admin@example.org")
	jsv("#password", "demo-password")
	shot("03-create-account.png")

	// Clear the demo values — do not submit to avoid creating a broken account.
	jsv("#account_name", "")
	jsv("#username", "")
	jsv("#password", "")

	// ── Step 4: Account card with zones (Load Zones button visible) ───────────
	// Navigate back to accounts; the card with loaded zones is already visible.
	// Screenshot shows an account card with its zone list and the "Load zones from HE" button.
	nav(*url + "/admin/accounts")
	// Scroll to the first account card to make it prominent.
	_, _ = page.Evaluate(`document.querySelector('.card')?.scrollIntoView({behavior:'instant',block:'start'})`)
	shot("04-zones-empty.png")

	// ── Step 5: Tokens list ───────────────────────────────────────────────────
	nav(*url + "/admin/tokens")
	shot("05-tokens-list.png")

	// ── Step 6: Issue Token form (inline, after clicking "Load Tokens") ────────
	// Click "Load Tokens" via JS to trigger htmx XHR which swaps in the token table
	// + IssueTokenForm. Wait for networkidle after the htmx response arrives.
	jscl(`button[hx-get]`) // first htmx-get button on the tokens page = "Load Tokens"
	waitIdle()
	settle()
	// Scroll to the issue-token form (it's below the token table).
	_, _ = page.Evaluate(`document.querySelector('input[name=label]')?.scrollIntoView({behavior:'instant',block:'center'})`)
	settle()
	jsv("input[name=label]", "ansible-prod")
	shot("06-token-create.png")

	// Submit the form via JS to issue the token so we can capture the "token issued" view.
	_, _ = page.Evaluate(`document.querySelector('button[type=submit]:last-of-type')?.click()`)
	waitIdle()
	// Scroll to the new token row which contains the raw token value (.token-reveal).
	_, _ = page.Evaluate(`document.querySelector('.token-reveal')?.scrollIntoView({behavior:'instant',block:'center'})`)
	shot("07-token-issued.png")

	// ── Step 8: Token Reveal dialog ───────────────────────────────────────────
	// Navigate back to tokens, load tokens again, click "Show" button for the first
	// recoverable token, which opens <dialog id="reveal-dialog"> via JS.
	nav(*url + "/admin/tokens")
	jscl(`button[hx-get]`) // click "Load Tokens" to expand the token list
	waitIdle()
	settle()
	// Click the "Show" button (onclick="showRevealDialog(this.dataset.jti)")
	jscl(`button[onclick*='showRevealDialog']`)
	settle()
	shot("08-token-reveal.png")

	log.Printf("done — screenshots written to %s", *outDir)
}
