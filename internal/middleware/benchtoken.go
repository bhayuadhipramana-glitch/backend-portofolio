package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
)

const bearerPrefix = "bearer "

// BearerOrAdmin authorizes a request either with "Authorization: Bearer
// <token>" (CI) or, when no bearer header is present, with the admin chain
// (session cookie, trusted origin). A bearer header never falls back to the
// session: a wrong or unconfigured token is a plain 401.
//
// The token is compared in constant time. Both sides are hashed first so the
// comparison always sees equal-length inputs and does not leak the length.
// An empty token disables the bearer path entirely.
func BearerOrAdmin(token string, admin ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))

	return func(next http.Handler) http.Handler {
		adminChain := next
		for i := len(admin) - 1; i >= 0; i-- {
			adminChain = admin[i](adminChain)
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if h == "" {
				adminChain.ServeHTTP(w, r)
				return
			}

			if len(h) < len(bearerPrefix) || !strings.EqualFold(h[:len(bearerPrefix)], bearerPrefix) {
				response.Error(w, http.StatusUnauthorized, "authentication required", nil)
				return
			}
			got := sha256.Sum256([]byte(strings.TrimSpace(h[len(bearerPrefix):])))
			if token == "" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
				response.Error(w, http.StatusUnauthorized, "authentication required", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
