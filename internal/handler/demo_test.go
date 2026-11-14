package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

type fakeBurst struct {
	count    int
	duration time.Duration
	calls    int
	err      error
}

func (f *fakeBurst) Start(count int, d time.Duration) error {
	f.calls++
	f.count, f.duration = count, d
	return f.err
}
func (f *fakeBurst) Wait(context.Context) error { return nil }

func postBurst(h *DemoHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/demo/burst", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Burst(rec, req)
	return rec
}

func TestDemoBurstDefaultsClampingAndResponse(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		count    int
		duration time.Duration
	}{
		{"empty body", ``, 100, 2 * time.Second},
		{"whitespace body", "  \n", 100, 2 * time.Second},
		{"empty object", `{}`, 100, 2 * time.Second},
		{"in range", `{"count":50,"duration_ms":1000}`, 50, time.Second},
		{"count too high", `{"count":9999}`, 200, 2 * time.Second},
		{"count too low", `{"count":1}`, 10, 2 * time.Second},
		{"duration too high", `{"duration_ms":99999}`, 100, 5 * time.Second},
		{"duration too low", `{"duration_ms":5}`, 100, 500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fb := &fakeBurst{}
			rec := postBurst(NewDemoHandler(fb), tt.body)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("got %d: %s", rec.Code, rec.Body)
			}
			if fb.count != tt.count || fb.duration != tt.duration {
				t.Fatalf("started %d / %v, want %d / %v", fb.count, fb.duration, tt.count, tt.duration)
			}
			var out struct {
				Data map[string]int64 `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Data["count"] != int64(tt.count) || out.Data["duration_ms"] != tt.duration.Milliseconds() {
				t.Fatalf("response reports %v, want the clamped values", out.Data)
			}
		})
	}
}

func TestDemoBurstRejectsBadBodies(t *testing.T) {
	tests := map[string]struct {
		body string
		want int
	}{
		"unknown field": {`{"count":10,"admin":true}`, http.StatusBadRequest},
		"wrong type":    {`{"count":"many"}`, http.StatusBadRequest},
		"not json":      {`burst please`, http.StatusBadRequest},
		"trailing":      {`{} {}`, http.StatusBadRequest},
		"oversized":     {`{"count":10,"pad":"` + strings.Repeat("a", maxDemoBodyBytes) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fb := &fakeBurst{}
			if rec := postBurst(NewDemoHandler(fb), tt.body); rec.Code != tt.want {
				t.Fatalf("got %d, want %d: %s", rec.Code, tt.want, rec.Body)
			}
			if fb.calls != 0 {
				t.Fatal("a rejected request started a burst")
			}
		})
	}
}

func TestDemoBurstBusyIs503AndErrorsAreHidden(t *testing.T) {
	rec := postBurst(NewDemoHandler(&fakeBurst{err: domain.ErrBurstBusy}), `{}`)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d / Retry-After %q, want 503 with a hint", rec.Code, rec.Header().Get("Retry-After"))
	}

	rec = postBurst(NewDemoHandler(&fakeBurst{err: errBoom{}}), `{}`)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "pq:") {
		t.Fatalf("got %d %q", rec.Code, rec.Body)
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "pq: internal detail" }
