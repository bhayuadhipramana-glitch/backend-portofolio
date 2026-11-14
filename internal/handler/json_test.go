package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

type payload struct {
	Name string `json:"name"`
}

func decode(t *testing.T, body string) (bool, *httptest.ResponseRecorder, payload) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var p payload
	ok := decodeJSON(rec, req, &p)
	return ok, rec, p
}

func TestDecodeJSON(t *testing.T) {
	t.Run("valid body", func(t *testing.T) {
		ok, _, p := decode(t, `{"name":"a"}`)
		if !ok || p.Name != "a" {
			t.Fatalf("ok=%v payload=%+v", ok, p)
		}
	})

	t.Run("unknown field is rejected", func(t *testing.T) {
		ok, rec, _ := decode(t, `{"name":"a","admin":true}`)
		if ok || rec.Code != http.StatusBadRequest {
			t.Fatalf("ok=%v status=%d", ok, rec.Code)
		}
	})

	t.Run("trailing value is rejected", func(t *testing.T) {
		ok, rec, _ := decode(t, `{"name":"a"} {"name":"b"}`)
		if ok || rec.Code != http.StatusBadRequest {
			t.Fatalf("ok=%v status=%d", ok, rec.Code)
		}
	})

	t.Run("malformed JSON is rejected", func(t *testing.T) {
		ok, rec, _ := decode(t, `{"name":`)
		if ok || rec.Code != http.StatusBadRequest {
			t.Fatalf("ok=%v status=%d", ok, rec.Code)
		}
	})

	t.Run("oversized body returns 413", func(t *testing.T) {
		big := `{"name":"` + strings.Repeat("a", maxBodyBytes) + `"}`
		ok, rec, _ := decode(t, big)
		if ok || rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("ok=%v status=%d", ok, rec.Code)
		}
	})
}

func TestParsePage(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		want     domain.Page
		wantOK   bool
		wantCode int
	}{
		{"defaults", "", domain.Page{Limit: domain.DefaultPageLimit}, true, 0},
		{"explicit values", "limit=5&offset=10", domain.Page{Limit: 5, Offset: 10}, true, 0},
		{"limit is capped", "limit=500", domain.Page{Limit: domain.MaxPageLimit}, true, 0},
		{"zero limit rejected", "limit=0", domain.Page{}, false, http.StatusBadRequest},
		{"non-numeric limit rejected", "limit=abc", domain.Page{}, false, http.StatusBadRequest},
		{"negative offset rejected", "offset=-1", domain.Page{}, false, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/?"+tt.query, nil)
			got, ok := parsePage(rec, req)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("got (%+v, %v), want (%+v, %v)", got, ok, tt.want, tt.wantOK)
			}
			if !ok && rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
		})
	}
}
