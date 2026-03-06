package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/httprate"
	response "github.com/vnovakovic/dns-he-net-automation/internal/api/response"
)

// PerTokenRateLimit returns chi middleware that limits requests per bearer token.
// Must be registered AFTER BearerAuth middleware (needs token in Authorization header).
// Returns 429 with Retry-After header and JSON error body on limit exceeded.
func PerTokenRateLimit(requestsPerMin int) func(http.Handler) http.Handler {
	return httprate.Limit(
		requestsPerMin,
		time.Minute,
		httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
			header := r.Header.Get("Authorization")
			if strings.HasPrefix(header, "Bearer ") {
				return strings.TrimPrefix(header, "Bearer "), nil
			}
			return r.RemoteAddr, nil
		}),
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			response.WriteError(w, http.StatusTooManyRequests, "rate_limited",
				"Request rate limit exceeded. Check the Retry-After header.")
		}),
	)
}

// GlobalRateLimit returns chi middleware limiting total requests across all clients.
// Register BEFORE BearerAuth (provides DDoS protection before auth overhead).
func GlobalRateLimit(requestsPerMin int) func(http.Handler) http.Handler {
	return httprate.LimitAll(requestsPerMin, time.Minute)
}
