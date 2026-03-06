// Package api provides the chi HTTP router for the dns-he-net-automation API.
package api

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/vnovakov/dns-he-net-automation/internal/api/admin"
	"github.com/vnovakov/dns-he-net-automation/internal/api/handlers"
	"github.com/vnovakov/dns-he-net-automation/internal/api/middleware"
	"github.com/vnovakov/dns-he-net-automation/internal/api/response"
	"github.com/vnovakov/dns-he-net-automation/internal/browser"
	"github.com/vnovakov/dns-he-net-automation/internal/metrics"
	"github.com/vnovakov/dns-he-net-automation/internal/resilience"
	"github.com/vnovakov/dns-he-net-automation/internal/token"
)

// NewRouter builds and returns the chi HTTP router with all middleware and route registrations.
//
// Route structure:
//   - GET /healthz — unauthenticated health check (OPS-01)
//   - GET /metrics — Prometheus metrics endpoint, unauthenticated (OBS-01)
//   - /api/v1/* — all require JWT bearer authentication (BearerAuth middleware)
//   - POST/DELETE mutations additionally require admin role (RequireAdmin middleware)
//   - GET /{zoneID}/export — BIND zone file export (admin only, BIND-01)
//   - POST /{zoneID}/import — BIND zone file import, additive-only (admin only, BIND-02/03)
//   - /admin/* — admin UI with Basic Auth + session cookie (UI-01, UI-04)
//
// Middleware order:
//   - GlobalRateLimit applied before BearerAuth (DDoS protection layer — research Pitfall 4)
//   - PrometheusMiddleware applied globally to all routes after panic recovery
//   - PerTokenRateLimit applied after BearerAuth (needs token identity from auth)
//
// Admin UI parameters are passed through to RegisterAdminRoutes. This function is updated
// exactly ONCE in plan 02 — plans 03 and 04 do not change this signature. (Checker issue 5 fix)
func NewRouter(db *sql.DB, sm *browser.SessionManager, launcher *browser.Launcher,
	secret []byte, breakers *resilience.BreakerRegistry,
	globalRPM, perTokenRPM int,
	vaultHealthFn func() string,
	reg *metrics.Registry,
	adminUsername, adminPassword, adminSessionKey string,
	tokenRecoveryEnabled bool,
	version string) http.Handler {
	r := chi.NewRouter()

	// Derive the AES-256 recovery key from the JWT secret when recovery is enabled.
	// nil means "feature off" — IssueToken and ListTokens both accept nil safely.
	// WHY derive here (not in config): the key is computed once per router init and
	// threaded into handlers as a [32]byte pointer — no repeated derivation per request.
	var recoveryKey *[32]byte
	if tokenRecoveryEnabled {
		k := token.RecoveryKey(secret)
		recoveryKey = &k
	}

	// Global rate limit — registered first, before auth, for DDoS protection (RES-03).
	// Limits total requests per minute across all clients (research Pitfall 4).
	r.Use(middleware.GlobalRateLimit(globalRPM))

	// Global middleware — applied to all routes.
	r.Use(chiMiddleware.RequestID) // X-Request-Id header; accessible via GetReqID(ctx)
	r.Use(chiMiddleware.RealIP)   // Populates RemoteAddr from X-Real-IP / X-Forwarded-For

	// Custom panic recovery middleware: returns JSON error response instead of chi's plain text 500.
	// This replaces chiMiddleware.Recoverer to satisfy the JSON error contract (API-04).
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					slog.ErrorContext(r.Context(), "panic recovered",
						"request_id", chiMiddleware.GetReqID(r.Context()),
						"panic", fmt.Sprintf("%v", rec),
					)
					response.WriteError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
				}
			}()
			next.ServeHTTP(w, r)
		})
	})

	// Prometheus metrics middleware — records HTTP request counts and durations.
	// Uses chi route patterns (not URL paths) to avoid cardinality explosion (OBS-01).
	// Applied after panic recovery so panics are captured before metrics are recorded.
	r.Use(PrometheusMiddleware(reg))

	// NOTE: Do NOT use chiMiddleware.Logger here — it uses log.Printf, not slog (research anti-pattern).
	// Structured request logging is handled in the BearerAuth middleware via slog.InfoContext.

	// Custom 404 and 405 handlers so unmatched routes follow the JSON error contract (API-04).
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		response.WriteError(w, http.StatusNotFound, "not_found", "The requested resource does not exist")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		response.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "HTTP method not allowed for this endpoint")
	})

	// Redirect root to the admin UI — no route is registered for "/" so without this
	// the NotFound handler returns a JSON 404, which is confusing for browser users.
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusFound)
	})

	// Health check — no authentication required (OPS-01).
	// vaultHealthFn returns "ok", "degraded: <reason>", or "disabled" (VAULT-04).
	r.Get("/healthz", handlers.HealthHandler(db, launcher, vaultHealthFn))

	// Prometheus metrics endpoint — unauthenticated, at root level (OBS-01).
	// MUST NOT be inside the /api/v1 group (which has BearerAuth middleware).
	// Prometheus scrapers must not be behind auth (research Pitfall 4 / 05-01 key decision).
	r.Get("/metrics", reg.Handler().ServeHTTP)

	// All /api/v1/* routes require authentication.
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.BearerAuth(db, secret))
		// Per-token rate limit — registered after BearerAuth (needs token identity).
		// Limits requests per unique bearer token per minute (RES-03).
		r.Use(middleware.PerTokenRateLimit(perTokenRPM))

		r.Route("/accounts", func(r chi.Router) {
			r.Get("/", handlers.ListAccounts(db))
			r.With(middleware.RequireAdmin).Post("/", handlers.CreateAccount(db))

			r.Route("/{accountID}", func(r chi.Router) {
				r.Get("/", handlers.GetAccount(db))
				r.With(middleware.RequireAdmin).Delete("/", handlers.DeleteAccount(db, sm))

				r.Route("/tokens", func(r chi.Router) {
					r.Get("/", handlers.ListTokens(db, tokenRecoveryEnabled))
					r.With(middleware.RequireAdmin).Post("/", handlers.IssueToken(db, secret, recoveryKey))
					r.With(middleware.RequireAdmin).Delete("/{tokenID}", handlers.RevokeToken(db))
				})
			})
		})

		r.Route("/zones", func(r chi.Router) {
			r.Get("/", handlers.ListZones(db, sm, breakers))
			r.With(middleware.RequireAdmin).Post("/", handlers.CreateZone(db, sm, breakers))
			r.With(middleware.RequireAdmin).Delete("/{zoneID}", handlers.DeleteZone(db, sm, breakers))

			r.Route("/{zoneID}", func(r chi.Router) {
				// Zone-scoped token enforcement: reject tokens bound to a different zone.
				// Account-wide tokens (ZoneID=="") pass through without restriction.
				// DEPENDENCY: must come after BearerAuth (needs claims in context).
				r.Use(middleware.RequireZoneAccess)

				r.With(middleware.RequireAdmin).Post("/sync", handlers.SyncRecords(db, sm, breakers, reg))
				// BIND export/import routes (BIND-01, BIND-02, BIND-03).
				// GET /export: scrapes live records and returns BIND zone file (text/plain, attachment).
				// POST /import: accepts BIND zone file, applies additive-only sync (plan.Delete = nil).
				// Both require admin role — they trigger browser automation against dns.he.net.
				r.With(middleware.RequireAdmin).Get("/export", handlers.ExportZone(db, sm, breakers))
				r.With(middleware.RequireAdmin).Post("/import", handlers.ImportZone(db, sm, breakers))

				r.Route("/records", func(r chi.Router) {
					r.Get("/", handlers.ListRecords(db, sm, breakers))
					r.With(middleware.RequireAdmin).Post("/", handlers.CreateRecord(db, sm, breakers))
					// WHY DELETE "/" separate from DELETE "/{recordID}":
					//   chi routes DELETE / and DELETE /{recordID} independently — no ambiguity.
					//   ?name= on the collection endpoint avoids forcing callers to discover the
					//   numeric record ID first, reducing a 2-step flow to a single API call.
					r.With(middleware.RequireAdmin).Delete("/", handlers.DeleteRecordByName(db, sm, breakers))
					r.Route("/{recordID}", func(r chi.Router) {
						r.Get("/", handlers.GetRecord(db, sm, breakers))
						r.With(middleware.RequireAdmin).Put("/", handlers.UpdateRecord(db, sm, breakers))
						r.With(middleware.RequireAdmin).Delete("/", handlers.DeleteRecord(db, sm, breakers))
					})
				})
			})
		})
	})

	// Admin UI — mounted at /admin (UI-01).
	// Auth (Basic Auth + HMAC-SHA256 session cookie) is handled inside RegisterAdminRoutes
	// via the AdminAuth middleware. The full dependency set is passed here; stub handlers
	// ignore unused params until plan 03/04 fills them in.
	admin.RegisterAdminRoutes(r, db, sm, breakers, secret, adminUsername, adminPassword, adminSessionKey, tokenRecoveryEnabled, version)

	return r
}

// PrometheusMiddleware returns a chi middleware that instruments every HTTP request
// using the provided metrics registry.
//
// Labels use chi route patterns (e.g. "/api/v1/zones/{zoneID}") not URL paths
// (e.g. "/api/v1/zones/12345") to avoid cardinality explosion when zone IDs and
// record IDs are embedded in the path (OBS-01, research anti-pattern).
//
// If reg is nil, the middleware is a no-op passthrough — safe for unit tests.
func PrometheusMiddleware(reg *metrics.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if reg == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Wrap the ResponseWriter to capture the status code after the handler runs.
			ww := chiMiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			next.ServeHTTP(ww, r)

			// Use the chi route pattern — NOT r.URL.Path — to prevent cardinality explosion.
			// chi.RouteContext is populated by chi after routing, so RoutePattern() returns
			// the pattern string (e.g. "/api/v1/zones/{zoneID}/records/{recordID}").
			routePattern := chi.RouteContext(r.Context()).RoutePattern()
			if routePattern == "" {
				routePattern = "unknown"
			}

			reg.HTTPRequestsTotal.WithLabelValues(r.Method, routePattern, strconv.Itoa(ww.Status())).Inc()
			reg.HTTPRequestDuration.WithLabelValues(r.Method, routePattern).Observe(time.Since(start).Seconds())
		})
	}
}
