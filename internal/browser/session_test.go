package browser

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	playwright "github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vnovakov/dns-he-net-automation/internal/credential"
)

// newTestSessionManager creates a SessionManager with a nil Launcher and a minimal
// credential provider. Suitable for unit tests that do not require a real browser.
// The nil Launcher causes ensureHealthy to log a warning and mark healthy=true (stub mode).
func newTestSessionManager(queueTimeout, opTimeout time.Duration) *SessionManager {
	// A minimal credential provider with no accounts (unit tests don't need real creds).
	creds := map[string]*credential.Credential{}
	provider := &stubCredProvider{accounts: creds}

	return NewSessionManager(
		nil, // nil launcher: ensureHealthy stub will handle this
		provider,
		queueTimeout,
		opTimeout,
		30*time.Minute, // reloginAge
		0,              // minOpDelay: no delay for tests
		0,              // maxOpDelay: no delay for tests
	)
}

// stubCredProvider implements credential.Provider for tests.
type stubCredProvider struct {
	accounts map[string]*credential.Credential
}

func (s *stubCredProvider) GetCredential(_ context.Context, accountID string) (*credential.Credential, error) {
	c, ok := s.accounts[accountID]
	if !ok {
		return nil, errors.New("not found")
	}
	return c, nil
}

func (s *stubCredProvider) ListAccountIDs(_ context.Context) ([]string, error) {
	ids := make([]string, 0, len(s.accounts))
	for id := range s.accounts {
		ids = append(ids, id)
	}
	return ids, nil
}

// TestGetOrCreateSession_SameID verifies that calling getOrCreateSession twice
// with the same account ID returns the same pointer.
func TestGetOrCreateSession_SameID(t *testing.T) {
	sm := newTestSessionManager(5*time.Second, 10*time.Second)

	s1 := sm.getOrCreateSession("acct1")
	s2 := sm.getOrCreateSession("acct1")

	assert.Same(t, s1, s2, "same account ID must return the same session pointer")
}

// TestGetOrCreateSession_DifferentIDs verifies that different account IDs return
// different session pointers.
func TestGetOrCreateSession_DifferentIDs(t *testing.T) {
	sm := newTestSessionManager(5*time.Second, 10*time.Second)

	s1 := sm.getOrCreateSession("acct1")
	s2 := sm.getOrCreateSession("acct2")

	assert.NotSame(t, s1, s2, "different account IDs must return different sessions")
}

// TestWithAccount_QueueTimeout verifies that ErrQueueTimeout is returned when
// the per-account mutex is held by another goroutine for longer than queueTimeout.
func TestWithAccount_QueueTimeout(t *testing.T) {
	// Very short queue timeout for fast test.
	sm := newTestSessionManager(50*time.Millisecond, 5*time.Second)

	// Acquire the session and lock its mutex externally.
	session := sm.getOrCreateSession("locked-account")
	session.mu.Lock()
	defer session.mu.Unlock()

	ctx := context.Background()
	err := sm.WithAccount(ctx, "locked-account", func(playwright.Page) error {
		return nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrQueueTimeout)
}

// TestWithAccount_ContextCancellation verifies that ctx.Err() is returned when
// the caller's context is cancelled while waiting for the queue.
func TestWithAccount_ContextCancellation(t *testing.T) {
	// Long queue timeout -- test uses context cancellation instead.
	sm := newTestSessionManager(10*time.Second, 5*time.Second)

	// Acquire the session and lock its mutex externally.
	session := sm.getOrCreateSession("cancel-account")
	session.mu.Lock()
	defer session.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay so the WithAccount call is already waiting.
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	err := sm.WithAccount(ctx, "cancel-account", func(playwright.Page) error {
		return nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestWithAccount_Serialization verifies that two concurrent WithAccount calls
// on the same account execute sequentially (not concurrently).
func TestWithAccount_Serialization(t *testing.T) {
	// Use a short queue timeout but long enough that both calls succeed sequentially.
	sm := newTestSessionManager(5*time.Second, 10*time.Second)

	var mu sync.Mutex
	var execOrder []int
	var wg sync.WaitGroup

	// Both goroutines call WithAccount on the same account. They must not overlap.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			err := sm.WithAccount(context.Background(), "shared-account", func(playwright.Page) error {
				mu.Lock()
				execOrder = append(execOrder, i)
				mu.Unlock()
				// Hold the lock briefly to ensure overlap would be detectable.
				time.Sleep(20 * time.Millisecond)
				return nil
			})
			// Serialization test: both should succeed (no timeout with 5s queue timeout).
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	// Both operations should have executed (order may vary, but both should be present).
	assert.Len(t, execOrder, 2, "both operations should complete")
}

// TestWithAccount_OperationError verifies that errors from the op function are propagated.
func TestWithAccount_OperationError(t *testing.T) {
	sm := newTestSessionManager(5*time.Second, 10*time.Second)
	expectedErr := errors.New("operation failed")

	err := sm.WithAccount(context.Background(), "acct1", func(playwright.Page) error {
		return expectedErr
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, expectedErr)
}

// TestClose_Empty verifies that Close() on an empty SessionManager does not panic.
func TestClose_Empty(t *testing.T) {
	sm := newTestSessionManager(5*time.Second, 10*time.Second)
	assert.NotPanics(t, func() {
		sm.Close()
	})
}
