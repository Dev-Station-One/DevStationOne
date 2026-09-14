package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/time/rate"

	"devstationone/internal/auth"
	"devstationone/internal/database"
)

// wsAcceptOptions is shared by both WebSocket upgrade points (runtime
// connect, terminal stream). Compression is disabled: these connections
// carry the runtime's bearer-token/session-cookie-authenticated control
// and terminal-byte traffic, and per-message deflate compression on a
// connection where some content can be attacker-influenced opens a
// theoretical compression-oracle side channel (CRIME/BREACH-family) for no
// real benefit here — terminal I/O and small JSON control envelopes are
// not bandwidth-constrained. Origin validation is coder/websocket's
// default behavior (rejects a cross-origin Accept unless OriginPatterns is
// set), already satisfying docs/SECURITY.md's WebSocket origin check.
var wsAcceptOptions = &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled}

type contextKey int

const userContextKey contextKey = 0

// userFromContext returns the authenticated user attached by requireSession.
func userFromContext(ctx context.Context) (auth.User, bool) {
	u, ok := ctx.Value(userContextKey).(auth.User)
	return u, ok
}

// requireSession authenticates the request via the session cookie and rejects
// it with 401 if there is no valid session.
func requireSession(db *database.DB, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.CookieName)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}

		_, user, err := auth.SessionAndUser(r.Context(), db.Pool, cookie.Value)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next(w, r.WithContext(ctx))
	}
}

// requireAdmin builds on requireSession, additionally rejecting the request
// with 403 if the authenticated user is not an administrator.
func requireAdmin(db *database.DB, next http.HandlerFunc) http.HandlerFunc {
	return requireSession(db, func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		if !user.IsAdmin {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "administrator access required"})
			return
		}
		next(w, r)
	})
}

// securityHeaders sets standard hardening headers on every response
// (docs/SECURITY.md "security headers"). The Content-Security-Policy keeps
// 'unsafe-inline' for script-src/style-src: every page in web/static uses
// inline <script>/<style> blocks by design (no bundler/build step — see
// docs/DECISIONS.md), so a stricter policy would break the app; every
// resource is still same-origin only, which is the meaningful boundary
// this policy enforces.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self' 'unsafe-inline'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"font-src 'self'; " +
		"connect-src 'self' ws: wss:; " +
		"frame-ancestors 'none'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
		h.Set("Content-Security-Policy", csp)
		if requestIsSecure(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// noCache forces the browser to revalidate every static file request rather
// than trust a heuristically-cached copy (RFC 7234 §4.2.2). There is no
// build step or hashed filenames in web/static (docs/DECISIONS.md), so
// without this a changed file can silently keep serving a stale cached
// version indefinitely. `no-cache` still allows a conditional GET (cheap
// 304 in the common case) rather than forcing a full re-download.
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

// mutatingMethods are the HTTP methods csrfProtect checks — state-changing
// requests, where a forged cross-site request could do damage. GET/HEAD are
// exempt: they must not have side effects (and are how top-level navigation
// works at all).
var mutatingMethods = map[string]bool{
	http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
}

// csrfProtect is defense-in-depth against CSRF alongside the session
// cookie's SameSite=Lax attribute (docs/DECISIONS.md ADR-026): Lax already
// keeps the cookie off a cross-site POST/fetch, so this only rejects the
// request when a browser-sent Origin header positively disagrees with the
// request's own Host — it never blocks a request with no Origin header
// (a bearer-token-authenticated runtime connection, or a same-site request
// from a browser old enough to omit it).
func csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mutatingMethods[r.Method] {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin request rejected"})
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ipLimiter is a per-client-IP token-bucket rate limiter.
//
// ponytail: limiters never expire, so a very large number of distinct
// client IPs grows this map without bound. Fine at MVP/self-hosted scale
// (docs/PRD.md — one customer's own infrastructure, not a public multi-
// tenant service); add TTL eviction if that stops being true.
type ipLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	b        int
}

func newIPLimiter(r rate.Limit, b int) *ipLimiter {
	return &ipLimiter{limiters: make(map[string]*rate.Limiter), r: r, b: b}
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, ok := l.limiters[ip]
	if !ok {
		lim = rate.NewLimiter(l.r, l.b)
		l.limiters[ip] = lim
	}
	return lim.Allow()
}

// rateLimit rejects a request with 429 once ip's bucket is empty.
func rateLimit(limiter *ipLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !limiter.allow(clientIP(r)) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
			return
		}
		next(w, r)
	}
}

// rateLimitAPI applies limiter to mutating (docs/SECURITY.md "rate
// limits") /api/v1/... requests only. GETs are exempt on purpose: the UI
// polls read endpoints frequently by design (terminal/service status,
// runtime online state), and the actual abuse surface — brute-forcing
// login, mass resource creation/deletion — is entirely POST/PUT/PATCH/
// DELETE.
func rateLimitAPI(limiter *ipLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mutatingMethods[r.Method] && strings.HasPrefix(r.URL.Path, "/api/v1/") && !limiter.allow(clientIP(r)) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// newRequestID returns a short random hex string for correlating one
// request's log line and response header (X-Request-Id) — not a security
// token, just a debugging correlation ID (docs/API.md "Errors").
func newRequestID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// statusRecorder captures the status code a handler writes, since
// http.ResponseWriter doesn't expose it after the fact. Unwrap lets
// http.ResponseController (used by websocket.Accept to hijack the
// connection for the WebSocket upgrade — runtimeConnectHandler,
// termSessionStreamHandler) see through this wrapper to the real
// http.Hijacker; without it every WebSocket upgrade would fail once this
// middleware sits in front of them.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// withRequestLogging logs one structured line per request (method, path,
// status, duration, request ID, client IP) and stamps the response with
// X-Request-Id (docs/SECURITY.md "structured observability").
func withRequestLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		start := time.Now()
		next.ServeHTTP(rec, r)
		duration := time.Since(start)

		recordRequestMetrics(rec.status)
		logger.Info("request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration_ms", duration.Milliseconds(), "request_id", id, "remote_addr", clientIP(r),
		)
	})
}
