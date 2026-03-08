// Package resilience provides retry/backoff and circuit breaker primitives for
// browser operations against dns.he.net.
package resilience

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sethvargo/go-retry"
	"github.com/vnovakovic/dns-he-net-automation/internal/browser"
)

// WithRetry wraps a browser operation with exponential backoff and jitter.
// Only transient errors are retried (timeout, session unhealthy, deadline exceeded).
// Permanent errors (e.g., validation failures, zone-not-found) return immediately.
// Max 3 attempts: initial + 2 retries. Backoff: 500ms base, 200ms jitter.
func WithRetry(ctx context.Context, op func(context.Context) error) error {
	b := retry.NewExponential(500 * time.Millisecond)
	b = retry.WithJitter(200*time.Millisecond, b)
	b = retry.WithMaxRetries(3, b)

	return retry.Do(ctx, b, func(ctx context.Context) error {
		err := op(ctx)
		if err != nil && isTransientBrowserError(err) {
			return retry.RetryableError(err)
		}
		return err
	})
}

// isTransientBrowserError reports whether err is a transient browser failure that
// should be retried. Returns false for logic errors, validation errors, and not-found.
//
// Transient conditions:
//   - browser.ErrSessionUnhealthy — session recovery failure (may succeed on retry)
//   - context.DeadlineExceeded — operation timeout
//   - strings containing "timeout" — playwright timeout strings
//   - strings containing "Target closed" — Chromium crash or tab closure
//
// Vault credential errors are NOT retried here — they are handled separately
// by the stale cache mechanism (research Pitfall 5).
func isTransientBrowserError(err error) bool {
	if errors.Is(err, browser.ErrSessionUnhealthy) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := err.Error()
	if strings.Contains(msg, "timeout") {
		return true
	}
	if strings.Contains(msg, "Target closed") {
		return true
	}
	return false
}
