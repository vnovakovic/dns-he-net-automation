// Package browser provides Playwright lifecycle management and per-account session
// management for dns.he.net browser automation.
package browser

import "errors"

// ErrQueueTimeout is returned when a request to acquire the per-account mutex
// exceeds the configured queue timeout. Callers should map this to HTTP 429.
var ErrQueueTimeout = errors.New("operation queue timeout")

// ErrSessionUnhealthy is returned when session recovery fails after detecting
// an unhealthy browser session. Callers should map this to HTTP 503.
var ErrSessionUnhealthy = errors.New("session recovery failed")
