package browser

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	playwright "github.com/playwright-community/playwright-go"
	"github.com/vnovakovic/dns-he-net-automation/internal/browser/pages"
	"github.com/vnovakovic/dns-he-net-automation/internal/credential"
	"github.com/vnovakovic/dns-he-net-automation/internal/metrics"
)

// AccountSession holds the browser state for one HE.net account.
// All fields (except mu) must only be accessed while holding mu.
type AccountSession struct {
	mu        sync.Mutex
	ctx       playwright.BrowserContext // isolated browser context for this account
	page      playwright.Page           // active page within ctx
	accountID string
	lastLogin time.Time
	healthy   bool
	lastOp    time.Time // tracks minimum inter-operation delay
}

// SessionManager manages per-account browser sessions with mutex serialization,
// queue timeout, operation timeout, and minimum inter-operation delay.
//
// Key invariants:
//   - Only one operation runs per account at a time (REL-02).
//   - Requests that cannot acquire the mutex within queueTimeout receive ErrQueueTimeout (REL-03).
//   - Each operation is wrapped in a context.WithTimeout(opTimeout).
type SessionManager struct {
	launcher      *Launcher
	credProvider  credential.Provider
	sessions      map[string]*AccountSession
	sessionsMu    sync.RWMutex // protects the sessions map (NOT per-account mutex)
	queueTimeout  time.Duration
	opTimeout     time.Duration
	reloginAge    time.Duration
	minOpDelay    time.Duration
	maxOpDelay    time.Duration
	screenshotDir string           // OBS-03: directory for debug screenshots; empty = disabled
	metrics       *metrics.Registry // OBS-01: nil-guarded; pass nil for unit tests
}

// NewSessionManager creates a SessionManager.
//   - launcher: manages browser context creation
//   - credProvider: retrieves credentials for ensureHealthy
//   - queueTimeout: max wait time to acquire per-account mutex; returns ErrQueueTimeout
//   - opTimeout: context timeout for the operation function
//   - reloginAge: max session age before proactive re-login
//   - minOpDelay: minimum delay between operations on the same account (BROWSER-08)
//   - maxOpDelay: maximum delay between operations on the same account (BROWSER-08 jitter upper bound)
//   - screenshotDir: directory for debug screenshots on failure; empty string disables screenshots (OBS-03)
//   - reg: Prometheus metrics registry for instrumentation (OBS-01); pass nil to disable (unit tests)
func NewSessionManager(
	launcher *Launcher,
	credProvider credential.Provider,
	queueTimeout, opTimeout, reloginAge, minOpDelay, maxOpDelay time.Duration,
	screenshotDir string,
	reg *metrics.Registry,
) *SessionManager {
	return &SessionManager{
		launcher:      launcher,
		credProvider:  credProvider,
		sessions:      make(map[string]*AccountSession),
		queueTimeout:  queueTimeout,
		opTimeout:     opTimeout,
		reloginAge:    reloginAge,
		minOpDelay:    minOpDelay,
		maxOpDelay:    maxOpDelay,
		screenshotDir: screenshotDir,
		metrics:       reg,
	}
}

// getOrCreateSession returns the AccountSession for the given accountID, creating one
// if it does not exist. Uses double-checked locking for safety.
func (sm *SessionManager) getOrCreateSession(accountID string) *AccountSession {
	// Fast path: read lock, check existence.
	sm.sessionsMu.RLock()
	session, ok := sm.sessions[accountID]
	sm.sessionsMu.RUnlock()
	if ok {
		return session
	}

	// Slow path: write lock, double-check.
	sm.sessionsMu.Lock()
	defer sm.sessionsMu.Unlock()

	if session, ok = sm.sessions[accountID]; ok {
		return session
	}

	session = &AccountSession{
		accountID: accountID,
		healthy:   false,
	}
	sm.sessions[accountID] = session
	return session
}

// WithAccount acquires the per-account mutex with a queue timeout, ensures a healthy session,
// enforces the minimum inter-operation delay, and calls op with the active page.
//
// opType is a fine-grained label for Prometheus metrics (e.g. "list_zones", "create_record").
// It is recorded in dnshe_browser_operations_total and dnshe_browser_operation_duration_seconds.
//
// Returns:
//   - ErrQueueTimeout if the mutex is not acquired within queueTimeout
//   - ctx.Err() if the caller's context is cancelled while waiting
//   - A wrapped ErrSessionUnhealthy if session recovery fails
//   - The result of op(page)
func (sm *SessionManager) WithAccount(ctx context.Context, accountID string, opType string, op func(playwright.Page) error) error {
	session := sm.getOrCreateSession(accountID)

	// Queue depth: increment while waiting to acquire the per-account mutex (OBS-01).
	if sm.metrics != nil {
		sm.metrics.QueueDepth.WithLabelValues(accountID).Inc()
	}

	// Queue with timeout using goroutine-based lock acquisition.
	//
	// Why goroutine: sync.Mutex.Lock() is not cancellable. We spawn a goroutine to
	// acquire the lock and signal via a non-buffered channel. This allows the caller to
	// select between lock acquisition, timeout, and context cancellation.
	//
	// Lock ownership transfer protocol:
	//   - Goroutine acquires session.mu, then selects between:
	//     a) Sending on acquired (non-buffered): if caller is still waiting, lock is
	//        transferred to the caller (caller defers Unlock).
	//     b) Receiving on done (closed by caller on timeout/cancel): goroutine unlocks
	//        immediately since caller is gone.
	//   - This guarantees no goroutine leak and no abandoned lock regardless of timing.
	acquired := make(chan struct{})
	done := make(chan struct{})

	go func() {
		session.mu.Lock()
		select {
		case acquired <- struct{}{}:
			// Lock ownership transferred to caller; caller defers Unlock.
		case <-done:
			// Caller timed out or was cancelled. Release the lock.
			session.mu.Unlock()
		}
	}()

	select {
	case <-acquired:
		// Successfully acquired the lock. Decrement queue depth — no longer waiting.
		if sm.metrics != nil {
			sm.metrics.QueueDepth.WithLabelValues(accountID).Dec()
		}
		defer session.mu.Unlock()

	case <-time.After(sm.queueTimeout):
		// Timed out. Signal goroutine to release the lock when it acquires it.
		if sm.metrics != nil {
			sm.metrics.QueueDepth.WithLabelValues(accountID).Dec()
		}
		close(done)
		return ErrQueueTimeout

	case <-ctx.Done():
		// Caller cancelled. Signal goroutine to release the lock when it acquires it.
		if sm.metrics != nil {
			sm.metrics.QueueDepth.WithLabelValues(accountID).Dec()
		}
		close(done)
		return ctx.Err()
	}

	// Wrap the operation in an independent timeout.
	opCtx, cancel := context.WithTimeout(ctx, sm.opTimeout)
	defer cancel()

	// Ensure the session is healthy before running the operation.
	if err := sm.ensureHealthy(opCtx, session); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnhealthy, err)
	}

	// Enforce inter-operation delay with jitter to rate-limit dns.he.net interactions.
	// BROWSER-08: Jitter avoids a predictable scraping pattern that could trigger server-side
	// rate limiting. math/rand (not crypto/rand) is sufficient — this is rate-limiting, not security.
	if !session.lastOp.IsZero() {
		elapsed := time.Since(session.lastOp)
		if elapsed < sm.maxOpDelay {
			// Jitter: random delay between minOpDelay and maxOpDelay.
			jitterRange := sm.maxOpDelay - sm.minOpDelay
			jitter := time.Duration(rand.Int63n(int64(jitterRange + 1)))
			target := sm.minOpDelay + jitter
			remaining := target - elapsed
			if remaining > 0 {
				select {
				case <-time.After(remaining):
				case <-opCtx.Done():
					return opCtx.Err()
				}
			}
		}
	}
	session.lastOp = time.Now()

	// Record start time for op duration measurement (OBS-01).
	start := time.Now()
	err := op(session.page)

	// Instrument browser operation: count and duration, labelled by opType and result.
	// CRITICAL: measurement occurs AFTER op returns — measures actual operation time (OBS-01).
	if sm.metrics != nil {
		result := "success"
		if err != nil {
			result = "error"
		}
		sm.metrics.BrowserOpsTotal.WithLabelValues(opType, result).Inc()
		sm.metrics.BrowserOpDuration.WithLabelValues(opType).Observe(time.Since(start).Seconds())
	}

	return err
}

// createBrowserSession creates a new BrowserContext + Page and logs in to dns.he.net.
// On success, it updates session.ctx, session.page, session.lastLogin, session.healthy.
// On failure, it cleans up any partially-created resources.
//
// SECURITY (SEC-03): Credentials are never included in error messages or log entries.
// Must be called with session.mu held.
func (sm *SessionManager) createBrowserSession(ctx context.Context, session *AccountSession) error {
	opTimeoutMs := float64(sm.opTimeout.Milliseconds())

	browserCtx, err := sm.launcher.NewAccountContext(opTimeoutMs)
	if err != nil {
		return fmt.Errorf("create browser context for account %q: %w", session.accountID, err)
	}

	page, err := browserCtx.NewPage()
	if err != nil {
		browserCtx.Close() //nolint:errcheck
		return fmt.Errorf("create page for account %q: %w", session.accountID, err)
	}

	cred, err := sm.credProvider.GetCredential(ctx, session.accountID)
	if err != nil {
		browserCtx.Close() //nolint:errcheck
		return fmt.Errorf("get credential for account %q: %w", session.accountID, err)
	}

	// SEC-03: log accountID only, never password.
	if err := pages.NewLoginPage(page).Login(cred.Username, cred.Password); err != nil {
		// OBS-03: capture screenshot on login failure for post-mortem.
		if page != nil {
			SaveDebugScreenshot(page, sm.screenshotDir, session.accountID, "login-failure")
		}
		browserCtx.Close() //nolint:errcheck
		return fmt.Errorf("login for account %q: %w", session.accountID, err)
	}

	session.ctx = browserCtx
	session.page = page
	session.lastLogin = time.Now()
	session.healthy = true

	// Track active browser sessions gauge (OBS-01).
	if sm.metrics != nil {
		sm.metrics.ActiveSessions.Inc()
	}

	return nil
}

// closeBrowserContext closes the BrowserContext associated with the session, if any,
// and clears session.ctx and session.page. Must be called with session.mu held.
func (sm *SessionManager) closeBrowserContext(session *AccountSession) {
	if session.ctx != nil {
		session.ctx.Close() //nolint:errcheck
		session.ctx = nil
		session.page = nil

		// Track active browser sessions gauge (OBS-01).
		if sm.metrics != nil {
			sm.metrics.ActiveSessions.Dec()
		}
	}
}

// ensureHealthy ensures the account session has a valid, authenticated browser context.
//
// Four cases handled:
//  1. No context (session.ctx == nil): create a fresh context and log in.
//  2. Aged out (lastLogin older than reloginAge): close old context, create fresh, re-login.
//  3. Health check fails (IsLoggedIn returns false or error): close old context, recover.
//     If recovery fails, return a wrapped ErrSessionUnhealthy.
//  4. Healthy: no-op, return nil.
//
// Unit-test escape hatch: if sm.launcher == nil, mark healthy without a real browser.
// Must be called with session.mu held.
func (sm *SessionManager) ensureHealthy(ctx context.Context, session *AccountSession) error {
	// Unit-test escape hatch: no launcher means no real browser.
	if sm.launcher == nil {
		slog.Warn("launcher is nil, session will have no browser context", "account", session.accountID)
		session.healthy = true
		return nil
	}

	// Case 1: No context — first use of this session.
	if session.ctx == nil {
		slog.Info("new session created", "account", session.accountID)
		if err := sm.createBrowserSession(ctx, session); err != nil {
			return err
		}
		return nil
	}

	// Case 2: Session has aged out — close and re-login proactively.
	if time.Since(session.lastLogin) > sm.reloginAge {
		slog.Info("session aged out, re-logging in", "account", session.accountID)
		sm.closeBrowserContext(session)
		session.healthy = false
		if err := sm.createBrowserSession(ctx, session); err != nil {
			return err
		}
		return nil
	}

	// Case 3: Health check — verify the session cookie is still valid.
	loggedIn, err := pages.NewLoginPage(session.page).IsLoggedIn()
	if err != nil || !loggedIn {
		slog.Info("session unhealthy, recovering", "account", session.accountID)
		// OBS-03: save debug screenshot before tearing down the unhealthy session.
		if session.page != nil {
			SaveDebugScreenshot(session.page, sm.screenshotDir, session.accountID, "health-check-failure")
		}
		sm.closeBrowserContext(session)
		session.healthy = false
		if recoveryErr := sm.createBrowserSession(ctx, session); recoveryErr != nil {
			// BROWSER-09: recovery after crash failed — log with details.
			slog.Error("session recovery failed after crash", "account", session.accountID, "err", recoveryErr)
			return fmt.Errorf("recovery failed: %w", ErrSessionUnhealthy)
		}
		return nil
	}

	// Case 4: Session is healthy.
	slog.Debug("session healthy", "account", session.accountID)
	return nil
}

// ForceRelogin forces a fresh browser session for the given account, discarding any
// cached session state and immediately re-establishing authentication.
// This is useful when an external caller knows the session is stale (e.g., after a
// password rotation).
//
// ForceRelogin acquires the per-account mutex with the configured queue timeout,
// closes the existing browser context, and creates a new one with a fresh login.
func (sm *SessionManager) ForceRelogin(ctx context.Context, accountID string) error {
	session := sm.getOrCreateSession(accountID)

	acquired := make(chan struct{})
	done := make(chan struct{})

	go func() {
		session.mu.Lock()
		select {
		case acquired <- struct{}{}:
		case <-done:
			session.mu.Unlock()
		}
	}()

	select {
	case <-acquired:
		defer session.mu.Unlock()
	case <-time.After(sm.queueTimeout):
		close(done)
		return ErrQueueTimeout
	case <-ctx.Done():
		close(done)
		return ctx.Err()
	}

	opCtx, cancel := context.WithTimeout(ctx, sm.opTimeout)
	defer cancel()

	slog.Info("forcing re-login", "account", accountID)
	sm.closeBrowserContext(session)
	session.healthy = false

	if sm.launcher == nil {
		// Unit-test escape hatch.
		session.healthy = true
		return nil
	}

	return sm.createBrowserSession(opCtx, session)
}

// Close closes all browser contexts and clears the session map.
// It is safe to call after the launcher has been closed.
func (sm *SessionManager) Close() {
	sm.sessionsMu.Lock()
	defer sm.sessionsMu.Unlock()

	for id, session := range sm.sessions {
		session.mu.Lock()
		if session.ctx != nil {
			session.ctx.Close() //nolint:errcheck
		}
		session.mu.Unlock()
		slog.Debug("session closed", "account", id)
	}

	sm.sessions = make(map[string]*AccountSession)
	slog.Info("all sessions closed")
}
