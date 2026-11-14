package middleware

import (
	"net/http"
	"net/netip"
	"strings"
)

// cfConnectingIP is the header Cloudflare sets to the real visitor address.
const cfConnectingIP = "CF-Connecting-IP"

// ClientIP replaces r.RemoteAddr with the visitor address from CF-Connecting-IP,
// but only when trustCloudflare is true, meaning the API is reachable solely
// through Cloudflare Tunnel. X-Forwarded-For and X-Real-IP are never trusted:
// any direct client can forge them, which would defeat per-IP rate limiting.
// An absent or malformed header leaves RemoteAddr untouched.
func ClientIP(trustCloudflare bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if !trustCloudflare {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if raw := strings.TrimSpace(r.Header.Get(cfConnectingIP)); raw != "" {
				if ip, err := netip.ParseAddr(raw); err == nil {
					r.RemoteAddr = ip.String()
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
