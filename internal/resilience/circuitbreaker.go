package resilience

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/sony/gobreaker/v2"
)

// BreakerRegistry maintains a per-account circuit breaker map.
// It is safe for concurrent use — getOrCreate uses double-checked locking.
type BreakerRegistry struct {
	mu       sync.RWMutex
	breakers map[string]*gobreaker.CircuitBreaker[error]
	settings gobreaker.Settings
}

// NewBreakerRegistry creates a BreakerRegistry with shared base settings.
//
//   - maxFailures: consecutive failures before the breaker opens (REL-04 spec: 5)
//   - timeoutSec: seconds the breaker stays open before allowing one probe (REL-04 spec: 30)
func NewBreakerRegistry(maxFailures uint32, timeoutSec int) *BreakerRegistry {
	settings := gobreaker.Settings{
		// Name is overridden per-breaker in getOrCreate.
		MaxRequests: 1, // one probe allowed in half-open state
		Timeout:     time.Duration(timeoutSec) * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= maxFailures
		},
		// OnStateChange is assigned per-breaker in getOrCreate so we have the account name.
	}

	return &BreakerRegistry{
		breakers: make(map[string]*gobreaker.CircuitBreaker[error]),
		settings: settings,
	}
}

// getOrCreate returns the circuit breaker for accountID, creating one if none exists.
// Uses double-checked locking for safety.
func (r *BreakerRegistry) getOrCreate(accountID string) *gobreaker.CircuitBreaker[error] {
	// Fast path: check under read lock.
	r.mu.RLock()
	cb, ok := r.breakers[accountID]
	r.mu.RUnlock()
	if ok {
		return cb
	}

	// Slow path: acquire write lock, double-check, create.
	r.mu.Lock()
	defer r.mu.Unlock()

	if cb, ok = r.breakers[accountID]; ok {
		return cb
	}

	name := "account-" + accountID
	s := r.settings
	s.Name = name
	s.OnStateChange = func(_ string, from, to gobreaker.State) {
		slog.Warn("circuit breaker state change",
			"account", accountID,
			"from", from.String(),
			"to", to.String(),
		)
	}

	cb = gobreaker.NewCircuitBreaker[error](s)
	r.breakers[accountID] = cb
	return cb
}

// Execute runs op through the circuit breaker for the given accountID.
//
// Returns:
//   - a wrapped error with "circuit breaker open for account <id>" if the breaker is open
//   - gobreaker.ErrTooManyRequests if in half-open state and a probe is already in progress
//   - the op's own error on normal failure (which counts as a failure for the breaker)
//   - nil on success
func (r *BreakerRegistry) Execute(_ context.Context, accountID string, op func() error) error {
	cb := r.getOrCreate(accountID)

	_, cbErr := cb.Execute(func() (error, error) {
		opErr := op()
		return opErr, opErr
	})

	if cbErr == gobreaker.ErrOpenState {
		return fmt.Errorf("circuit breaker open for account %s", accountID)
	}
	return cbErr
}
