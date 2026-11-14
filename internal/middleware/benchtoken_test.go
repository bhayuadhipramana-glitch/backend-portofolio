package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBearerOrAdmin(t *testing.T) {
	const token = "s3cret-ci-token-with-enough-length-1234"

	adminCalls := 0
	admin := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			adminCalls++
			if r.Header.Get("X-Admin") != "yes" {
				http.Error(w, "no session", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	tests := []struct {
		name       string
		configured string
		header     string
		admin      bool
		want       int
		wantAdmin  int
	}{
		{"valid bearer", token, "Bearer " + token, false, http.StatusOK, 0},
		{"scheme is case-insensitive", token, "bearer " + token, false, http.StatusOK, 0},
		{"surrounding space tolerated", token, "Bearer  " + token + " ", false, http.StatusOK, 0},
		{"wrong token", token, "Bearer " + token + "0", false, http.StatusUnauthorized, 0},
		{"shorter token", token, "Bearer s3cret", false, http.StatusUnauthorized, 0},
		{"empty bearer", token, "Bearer ", false, http.StatusUnauthorized, 0},
		{"other scheme", token, "Basic " + token, false, http.StatusUnauthorized, 0},
		{"bare token", token, token, false, http.StatusUnauthorized, 0},
		{"bad bearer ignores admin session", token, "Bearer nope", true, http.StatusUnauthorized, 0},
		{"no header uses admin chain (ok)", token, "", true, http.StatusOK, 1},
		{"no header uses admin chain (denied)", token, "", false, http.StatusUnauthorized, 1},
		{"unconfigured token rejects any bearer", "", "Bearer anything", false, http.StatusUnauthorized, 0},
		{"unconfigured token rejects empty bearer", "", "Bearer ", false, http.StatusUnauthorized, 0},
		{"unconfigured token still allows admin", "", "", true, http.StatusOK, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminCalls = 0
			h := BearerOrAdmin(tt.configured, admin)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			if tt.admin {
				req.Header.Set("X-Admin", "yes")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.want || adminCalls != tt.wantAdmin {
				t.Fatalf("got %d (admin chain calls %d), want %d (%d)", rec.Code, adminCalls, tt.want, tt.wantAdmin)
			}
		})
	}
}
