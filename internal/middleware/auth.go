// Package middleware holds HTTP middlewares that guard the admin API.
package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
)

const (
	// localSessionCookie carries the raw admin session token over plain http (APP_ENV=local).
	localSessionCookie = "session"
	// secureSessionCookie is the __Host- prefixed name used whenever the cookie is Secure:
	// browsers then guarantee it is HTTPS-only, host-bound and set with Path=/.
	secureSessionCookie = "__Host-session"

	// EnvLocal is the only APP_ENV value that permits plain-http cookies.
	EnvLocal = "local"
)

// IsSecureRequest reports whether cookies for this request must be Secure:
// always outside the local environment, and in local mode whenever the request
// arrived over HTTPS according to the proxy (Cloudflare sets X-Forwarded-Proto).
func IsSecureRequest(appEnv string, r *http.Request) bool {
	return appEnv != EnvLocal || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// SessionCookieName returns the session cookie name for the given Secure flag.
func SessionCookieName(secure bool) string {
	if secure {
		return secureSessionCookie
	}
	return localSessionCookie
}

// Authenticator resolves a raw session token into a session.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (*domain.Session, error)
}

type ctxKey struct{}

// SessionFromContext returns the admin session placed by RequireAdmin.
func SessionFromContext(ctx context.Context) (*domain.Session, bool) {
	s, ok := ctx.Value(ctxKey{}).(*domain.Session)
	return s, ok
}

// RequireAdmin rejects requests without a valid admin session (401) and
// stores the session in the request context otherwise.
func RequireAdmin(auth Authenticator, appEnv string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(SessionCookieName(IsSecureRequest(appEnv, r)))
			if err != nil {
				response.Error(w, http.StatusUnauthorized, "authentication required", nil)
				return
			}

			s, err := auth.Authenticate(r.Context(), c.Value)
			if err != nil {
				if errors.Is(err, domain.ErrUnauthorized) {
					response.Error(w, http.StatusUnauthorized, "authentication required", nil)
					return
				}
				slog.ErrorContext(r.Context(), "middleware: authenticate failed", "error", err)
				response.Error(w, http.StatusInternalServerError, "failed to verify session", nil)
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, s)))
		})
	}
}

// TrustedOrigin is a CSRF defence-in-depth for cookie-authenticated mutations:
// when a request carries an Origin header it must match one of allowed.
// Requests without Origin (curl, server-to-server) pass; they still need a
// valid session cookie.
func TrustedOrigin(allowed []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin := r.Header.Get("Origin"); origin != "" && !slices.Contains(allowed, origin) {
				response.Error(w, http.StatusForbidden, "origin not allowed", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
