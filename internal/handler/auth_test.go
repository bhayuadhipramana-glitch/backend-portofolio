package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

type fakeAuthUsecase struct {
	token   string
	session *domain.Session
	err     error
}

func (fakeAuthUsecase) LoginURL(state string) string {
	return "https://github.test/auth?state=" + state
}
func (f fakeAuthUsecase) CompleteLogin(context.Context, string) (string, *domain.Session, error) {
	return f.token, f.session, f.err
}
func (fakeAuthUsecase) Authenticate(context.Context, string) (*domain.Session, error) {
	return nil, domain.ErrUnauthorized
}
func (fakeAuthUsecase) Logout(context.Context, string) error { return nil }

func findCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func callback(t *testing.T, appEnv, proto string, uc fakeAuthUsecase) *httptest.ResponseRecorder {
	t.Helper()
	h := NewAuthHandler(uc, appEnv, "", time.Hour)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback?state=abc&code=xyz", nil)
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "abc"})
	if proto != "" {
		req.Header.Set("X-Forwarded-Proto", proto)
	}
	rec := httptest.NewRecorder()
	h.Callback(rec, req)
	return rec
}

func TestCallbackSessionCookie(t *testing.T) {
	uc := fakeAuthUsecase{token: "tok", session: &domain.Session{GithubID: 42, ExpiresAt: time.Now().Add(time.Hour)}}

	tests := []struct {
		name       string
		env, proto string
		wantName   string
		wantSecure bool
	}{
		{"local over plain http", "local", "", "session", false},
		{"local behind https proxy", "local", "https", "__Host-session", true},
		{"production without proxy header", "production", "", "__Host-session", true},
		{"production behind https proxy", "production", "https", "__Host-session", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := callback(t, tt.env, tt.proto, uc)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
			}

			c := findCookie(t, rec, tt.wantName)
			if c == nil {
				t.Fatalf("cookie %q not set; got %v", tt.wantName, rec.Result().Cookies())
			}
			if c.Value != "tok" || !c.HttpOnly || c.Secure != tt.wantSecure ||
				c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
				t.Fatalf("unexpected cookie attributes: %+v", c)
			}

			// The one-shot state cookie must be cleared and carry the same Secure flag.
			st := findCookie(t, rec, stateCookieName)
			if st == nil || st.MaxAge >= 0 || st.Secure != tt.wantSecure {
				t.Fatalf("state cookie not cleared correctly: %+v", st)
			}
		})
	}
}

func TestCallbackRejectsBadState(t *testing.T) {
	h := NewAuthHandler(fakeAuthUsecase{}, "local", "", time.Hour)

	tests := []struct {
		name   string
		cookie *http.Cookie
		query  string
	}{
		{"no state cookie", nil, "state=abc&code=x"},
		{"mismatched state", &http.Cookie{Name: stateCookieName, Value: "other"}, "state=abc&code=x"},
		{"empty state", &http.Cookie{Name: stateCookieName, Value: ""}, "code=x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/cb?"+tt.query, nil)
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}
			rec := httptest.NewRecorder()
			h.Callback(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if findCookie(t, rec, "session") != nil || findCookie(t, rec, "__Host-session") != nil {
				t.Fatal("no session cookie may be issued on a state mismatch")
			}
		})
	}
}

func TestCallbackNonAdminIsForbidden(t *testing.T) {
	rec := callback(t, "local", "", fakeAuthUsecase{err: domain.ErrForbidden})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if findCookie(t, rec, "session") != nil {
		t.Fatal("no session cookie may be issued to a non-admin")
	}
}

func TestLoginStateCookie(t *testing.T) {
	tests := []struct {
		name       string
		env, proto string
		wantSecure bool
	}{
		{"local http", "local", "", false},
		{"local https proxy", "local", "https", true},
		{"production", "production", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewAuthHandler(fakeAuthUsecase{}, tt.env, "", time.Hour)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/login", nil)
			if tt.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.proto)
			}
			rec := httptest.NewRecorder()
			h.Login(rec, req)

			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302", rec.Code)
			}
			c := findCookie(t, rec, stateCookieName)
			if c == nil || c.Value == "" || !c.HttpOnly || c.Secure != tt.wantSecure || c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("unexpected state cookie: %+v", c)
			}
			if loc := rec.Header().Get("Location"); loc != "https://github.test/auth?state="+c.Value {
				t.Fatalf("redirect %q does not carry the cookie state", loc)
			}
		})
	}
}
