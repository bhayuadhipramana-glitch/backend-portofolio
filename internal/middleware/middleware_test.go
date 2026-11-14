package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	const direct = "10.0.0.5:1234"

	run := func(trust bool, header, value string) string {
		var got string
		h := ClientIP(trust)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = r.RemoteAddr
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = direct
		if header != "" {
			req.Header.Set(header, value)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		return got
	}

	if got := run(true, "CF-Connecting-IP", "203.0.113.7"); got != "203.0.113.7" {
		t.Errorf("trusted tunnel: got %q, want 203.0.113.7", got)
	}
	if got := run(false, "CF-Connecting-IP", "203.0.113.7"); got != direct {
		t.Errorf("tunnel not trusted must ignore the header: got %q", got)
	}
	if got := run(true, "X-Forwarded-For", "203.0.113.7"); got != direct {
		t.Errorf("X-Forwarded-For must never be trusted: got %q", got)
	}
	if got := run(true, "CF-Connecting-IP", "not-an-ip"); got != direct {
		t.Errorf("malformed header must be ignored: got %q", got)
	}
}

func TestIsSecureRequestAndCookieName(t *testing.T) {
	req := func(proto string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		return r
	}

	tests := []struct {
		name       string
		env, proto string
		want       bool
	}{
		{"local over http", "local", "", false},
		{"local behind https proxy", "local", "https", true},
		{"local with uppercase proto", "local", "HTTPS", true},
		{"local with http proto", "local", "http", false},
		{"production is always secure", "production", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSecureRequest(tt.env, req(tt.proto)); got != tt.want {
				t.Fatalf("IsSecureRequest(%q, %q) = %v, want %v", tt.env, tt.proto, got, tt.want)
			}
		})
	}

	if SessionCookieName(true) != "__Host-session" || SessionCookieName(false) != "session" {
		t.Fatal("unexpected session cookie names")
	}
}
