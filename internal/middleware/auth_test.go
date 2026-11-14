package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

type fakeAuthenticator struct {
	session  *domain.Session
	err      error
	gotToken string
}

func (f *fakeAuthenticator) Authenticate(_ context.Context, token string) (*domain.Session, error) {
	f.gotToken = token
	return f.session, f.err
}

func okHandler(t *testing.T, sawSession *bool) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s, ok := SessionFromContext(r.Context()); ok && s != nil {
			*sawSession = true
		}
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireAdmin(t *testing.T) {
	admin := &domain.Session{GithubID: 42, Login: "admin"}

	tests := []struct {
		name       string
		env        string
		cookie     *http.Cookie
		proto      string
		auth       *fakeAuthenticator
		wantStatus int
		wantToken  string
	}{
		{
			name:       "missing cookie is 401",
			env:        "local",
			auth:       &fakeAuthenticator{session: admin},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "unknown, expired, revoked or non-admin session is 401",
			env:        "local",
			cookie:     &http.Cookie{Name: "session", Value: "tok"},
			auth:       &fakeAuthenticator{err: domain.ErrUnauthorized},
			wantStatus: http.StatusUnauthorized,
			wantToken:  "tok",
		},
		{
			name:       "backend failure is 500, not a silent pass",
			env:        "local",
			cookie:     &http.Cookie{Name: "session", Value: "tok"},
			auth:       &fakeAuthenticator{err: errors.New("db down")},
			wantStatus: http.StatusInternalServerError,
			wantToken:  "tok",
		},
		{
			name:       "valid admin session passes",
			env:        "local",
			cookie:     &http.Cookie{Name: "session", Value: "tok"},
			auth:       &fakeAuthenticator{session: admin},
			wantStatus: http.StatusOK,
			wantToken:  "tok",
		},
		{
			name:       "production reads the __Host- cookie",
			env:        "production",
			cookie:     &http.Cookie{Name: "__Host-session", Value: "tok"},
			auth:       &fakeAuthenticator{session: admin},
			wantStatus: http.StatusOK,
			wantToken:  "tok",
		},
		{
			name:       "production ignores the plain cookie name",
			env:        "production",
			cookie:     &http.Cookie{Name: "session", Value: "tok"},
			auth:       &fakeAuthenticator{session: admin},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "local behind https proxy reads the __Host- cookie",
			env:        "local",
			proto:      "https",
			cookie:     &http.Cookie{Name: "__Host-session", Value: "tok"},
			auth:       &fakeAuthenticator{session: admin},
			wantStatus: http.StatusOK,
			wantToken:  "tok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sawSession bool
			h := RequireAdmin(tt.auth, tt.env)(okHandler(t, &sawSession))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}
			if tt.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.proto)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.auth.gotToken != tt.wantToken {
				t.Fatalf("authenticated token %q, want %q", tt.auth.gotToken, tt.wantToken)
			}
			if wantSession := tt.wantStatus == http.StatusOK; sawSession != wantSession {
				t.Fatalf("handler saw session = %v, want %v", sawSession, wantSession)
			}
		})
	}
}

func TestTrustedOrigin(t *testing.T) {
	allowed := []string{"https://app.example.com"}
	h := TrustedOrigin(allowed)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name   string
		method string
		origin string
		want   int
	}{
		{"POST from untrusted origin", http.MethodPost, "https://evil.example", http.StatusForbidden},
		{"PUT from untrusted origin", http.MethodPut, "https://evil.example", http.StatusForbidden},
		{"DELETE from untrusted origin", http.MethodDelete, "https://evil.example", http.StatusForbidden},
		{"opaque null origin", http.MethodPost, "null", http.StatusForbidden},
		{"origin differing only by scheme", http.MethodPost, "http://app.example.com", http.StatusForbidden},
		{"origin with extra path-like suffix", http.MethodPost, "https://app.example.com.evil.example", http.StatusForbidden},
		{"POST from trusted origin", http.MethodPost, "https://app.example.com", http.StatusOK},
		{"PUT from trusted origin", http.MethodPut, "https://app.example.com", http.StatusOK},
		{"no Origin header (curl, server-to-server)", http.MethodPost, "", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}
