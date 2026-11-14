package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/handler"
	authmw "github.com/bhayuadhipramana-glicth/backend-portofolio/internal/middleware"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

type memPublisher struct{ got []domain.Event }

func (p *memPublisher) Publish(_ context.Context, ev []domain.Event) error {
	p.got = append(p.got, ev...)
	return nil
}
func (p *memPublisher) Ping(context.Context) error { return nil }

const routerEvent = `{"event_id":"3f2b8c1e-5d4a-4b6e-9c7d-1a2b3c4d5e6f","event_type":"page_view","source":"web","latency_ms":1,"occurred_at":"2026-01-02T15:04:05Z"}`

func TestEventsEndpointRateLimitedPerIP(t *testing.T) {
	pub := &memPublisher{}
	uc, err := usecase.NewEventUsecase(pub)
	if err != nil {
		t.Fatal(err)
	}
	router := newRouter(routerDeps{
		db:             fakePinger{},
		cache:          fakePinger{},
		bus:            fakePinger{},
		eventHandler:   handler.NewEventHandler(uc),
		eventLimiter:   authmw.NewRateLimiter(0.001, 2).Middleware,
		allowedOrigins: []string{"http://localhost:3000"},
	})

	post := func(addr string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(routerEvent))
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 0; i < 2; i++ {
		if code := post("198.51.100.9:1111"); code != http.StatusAccepted {
			t.Fatalf("request %d: got %d, want 202", i, code)
		}
	}
	if code := post("198.51.100.9:2222"); code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429", code)
	}
	if code := post("198.51.100.10:1111"); code != http.StatusAccepted {
		t.Fatalf("another IP got %d, want 202", code)
	}
	if len(pub.got) != 3 {
		t.Fatalf("published %d events, want 3 (rejected requests must not publish)", len(pub.got))
	}
}

func TestReadyzReportsNATSDegradation(t *testing.T) {
	tests := []struct {
		name       string
		cache, bus error
		want       string
	}{
		{"all up", nil, nil, "ready"},
		{"nats down", nil, errors.New("down"), "ready (events degraded)"},
		{"cache down", errors.New("down"), nil, "ready (cache degraded)"},
		{"both down", errors.New("down"), errors.New("down"), "ready (cache, events degraded)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := handleReadyz(fakePinger{}, fakePinger{err: tt.cache}, fakePinger{err: tt.bus})
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != http.StatusOK || rec.Body.String() != tt.want {
				t.Fatalf("got %d %q, want 200 %q", rec.Code, rec.Body.String(), tt.want)
			}
		})
	}

	h := handleReadyz(fakePinger{err: errors.New("db down")}, fakePinger{}, fakePinger{})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("database down must fail readiness, got %d", rec.Code)
	}
}
