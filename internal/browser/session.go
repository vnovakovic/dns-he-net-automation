package browser

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	playwright "github.com/playwright-community/playwright-go"
	"github.com/vnovakov/dns-he-net-automation/internal/credential"
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
	launcher     *Launcher
	credProvider credential.Provider
	sessions     map[string]*AccountSession
	sessionsMu   sync.RWMutex // protects the sessions map (NOT per-account mutex)
	queueTimeout time.Duration
	opTimeout    time.Duration
	reloginAge   time.Duration
	minOpDelay   time.Duration
}

// NewSessionManager creates a SessionManager.
//   - launcher: manages browser context creation
//   - credProvider: retrieves credentials for ensureHealthy (used in plan 01-03)
//   - queueTimeout: max wait time to acquire per-account mutex; returns ErrQueueTimeout
//   - opTimeout: context timeout for the operation function
//   - reloginAge: max session age before proactive re-login (used in plan 01-03)
//   - minOpDelay: minimum delay between operations on the same account
func NewSessionManager(
	launcher *Launcher,
	credProvider credential.Provider,
	queueTimeout, opTimeout, reloginAge, minOpDelay time.Duration,
) *SessionManager {
	return &SessionManager{
		launcher:     launcher,
		credProvider: credProvider,
		sessions:     make(map[string]*AccountSession),
		queueTimeout: queueTimeout,
		opTimeout:    opTimeout,
		reloginAge:   reloginAge,
		minOpDelay:   minOpDelay,
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
// Returns:
//   - ErrQueueTimeout if the mutex is not acquired within queueTimeout
//   - ctx.Err() if the caller's context is cancelled while waiting
//   - A wrapped ErrSessionUnhealthy if session recovery fails
//   - The result of op(page)
func (sm *SessionManager) WithAccount(ctx context.Context, accountID string, op func(playwright.Page) error) error {
	session := sm.getOrCreateSession(accountID)

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
		// Successfully acquired the lock.
		defer session.mu.Unlock()

	case <-time.After(sm.queueTimeout):
		// Timed out. Signal goroutine to release the lock when it acquires it.
		close(done)
		return ErrQueueTimeout

	case <-ctx.Done():
		// Caller cancelled. Signal goroutine to release the lock when it acquires it.
		close(done)
		return ctx.Err()
	}

	// Wrap the operation in an independent timeout.
	opCtx, cancel := context.WithTimeout(ctx, sm.opTimeout)
	defer cancel()

	// Ensure the session is healthy (stub in 01-02; login logic added in 01-03).
	if err := sm.ensureHealthy(opCtx, session); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnhealthy, err)
	}

	// Enforce minimum inter-operation delay to rate-limit dns.he.net interactions.
	if !session.lastOp.IsZero() {
		elapsed := time.Since(session.lastOp)
		if elapsed < sm.minOpDelay {
			select {
			case <-time.After(sm.minOpDelay - elapsed):
			case <-opCtx.Done():
				return opCtx.Err()
			}
		}
	}
	session.lastOp = time.Now()

	return op(session.page)
}

// ensureHealthy ensures the account session has a valid browser context and page.
//
// STUB (Plan 01-02): Creates a new context+page if ctx or page is nil/unhealthy.
// Actual health check logic (login verification, re-login) is added in Plan 01-03
// when page objects exist.
//
// Must be called with session.mu held.
func (sm *SessionManager) ensureHealthy(ctx context.Context, session *AccountSession) error {
	if session.ctx == nil || !session.healthy {
		slog.Info("creating new session", "account", session.accountID)

		if sm.launcher == nil {
			// Allow unit tests to run without a real launcher.
			// In production this should not happen; the launcher is always set.
			slog.Warn("launcher is nil, session will have no browser context", "account", session.accountID)
			session.healthy = true
			return nil
		}

		// Create an isolated browser context for this account.
		opTimeoutMs := float64(sm.opTimeout.Milliseconds())
		browserCtx, err := sm.launcher.NewAccountContext(opTimeoutMs)
		if err != nil {
			return fmt.Errorf("create browser context for account %q: %w", session.accountID, err)
		}

		// Create the active page within this context.
		page, err := browserCtx.NewPage()
		if err != nil {
			browserCtx.Close() //nolint:errcheck
			return fmt.Errorf("create page for account %q: %w", session.accountID, err)
		}

		session.ctx = browserCtx
		session.page = page
		session.healthy = true
		return nil
	}

	slog.Debug("session healthy", "account", session.accountID)
	return nil
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
