package observability

import (
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
)

// MetricsAccess guards GET /metrics.
//
// With a token configured, only "Authorization: Bearer <token>" passes
// (constant-time comparison over SHA-256 digests).
//
// Without a token the endpoint is internal-only: the peer must be a loopback
// or private address AND the request must carry no Cloudflare or proxy
// headers, so traffic that arrived through the tunnel is refused even though
// cloudflared connects from a private cluster IP. Production should set
// METRICS_TOKEN as well.
func MetricsAccess(token string) func(http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token != "" {
				const prefix = "bearer "
				h := r.Header.Get("Authorization")
				if len(h) >= len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
					got := sha256.Sum256([]byte(strings.TrimSpace(h[len(prefix):])))
					if subtle.ConstantTimeCompare(got[:], want[:]) == 1 {
						next.ServeHTTP(w, r)
						return
					}
				}
				response.Error(w, http.StatusUnauthorized, "authentication required", nil)
				return
			}

			if viaProxy(r) || !internalPeer(r.RemoteAddr) {
				response.Error(w, http.StatusForbidden, "forbidden", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// viaProxy reports headers that only a proxy or the Cloudflare edge adds.
func viaProxy(r *http.Request) bool {
	for _, h := range []string{"CF-Connecting-IP", "Cf-Ray", "X-Forwarded-For", "X-Real-IP", "Forwarded"} {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

func internalPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate()
}
