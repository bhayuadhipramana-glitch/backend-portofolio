package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

func TestCacheStatusSetsXCacheHeader(t *testing.T) {
	tests := []struct {
		name   string
		record domain.CacheStatus
		code   int
		want   string
	}{
		{"l1", domain.CacheHitL1, http.StatusOK, "HIT-L1"},
		{"l2", domain.CacheHitL2, http.StatusOK, "HIT-L2"},
		{"miss", domain.CacheMiss, http.StatusOK, "MISS"},
		{"nothing cached", "", http.StatusOK, ""},
		{"errors never claim a cache status", domain.CacheMiss, http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := CacheStatus(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.record != "" {
					domain.RecordCacheStatus(r.Context(), tt.record)
				}
				w.WriteHeader(tt.code)
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			if got := rec.Header().Get(CacheHeaderName); got != tt.want {
				t.Errorf("X-Cache = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCacheStatusHeaderSetOnImplicitWriteHeader(t *testing.T) {
	h := CacheStatus(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		domain.RecordCacheStatus(r.Context(), domain.CacheHitL2)
		_, _ = w.Write([]byte("body")) // no explicit WriteHeader
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get(CacheHeaderName); got != "HIT-L2" {
		t.Errorf("X-Cache = %q, want HIT-L2", got)
	}
}
