package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/handler"
	authmw "github.com/bhayuadhipramana-glicth/backend-portofolio/internal/middleware"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

// blockingIngest holds every burst open until released, so the test can fill
// the server-wide burst slots.
type blockingIngest struct{ release chan struct{} }

func (b blockingIngest) Ingest(ctx context.Context, _ []domain.Event) error {
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newDemoRouter(t *testing.T, ing usecase.EventUsecase, perIP int) (http.Handler, usecase.DemoBurstUsecase) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	uc, err := usecase.NewDemoBurstUsecase(ctx, ing, domain.DemoMaxConcurrency)
	if err != nil {
		t.Fatal(err)
	}
	return newRouter(routerDeps{
		db:             fakePinger{},
		cache:          fakePinger{},
		demoHandler:    handler.NewDemoHandler(uc),
		demoLimiter:    authmw.NewWindowLimiter(perIP, time.Minute).Middleware,
		allowedOrigins: []string{"http://localhost:3000"},
	}), uc
}

func postBurstFrom(router http.Handler, addr, body string) int {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/demo/burst", strings.NewReader(body))
	req.RemoteAddr = addr
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec.Code
}

func TestDemoBurstPerIPRateLimit(t *testing.T) {
	release := make(chan struct{})
	close(release) // bursts complete immediately, so only the IP limit matters
	router, uc := newDemoRouter(t, blockingIngest{release}, 2)

	for i := 0; i < 2; i++ {
		if code := postBurstFrom(router, "198.51.100.7:1000", `{"count":10,"duration_ms":500}`); code != http.StatusAccepted {
			t.Fatalf("burst %d: got %d, want 202", i, code)
		}
	}
	if code := postBurstFrom(router, "198.51.100.7:2000", ``); code != http.StatusTooManyRequests {
		t.Fatalf("third burst within a minute: got %d, want 429", code)
	}
	// The first two bursts still hold both server-wide slots; let them end.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := uc.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if code := postBurstFrom(router, "198.51.100.8:1000", ``); code != http.StatusAccepted {
		t.Fatalf("another IP: got %d, want 202", code)
	}
}

func TestDemoBurstServerWideConcurrencyCap(t *testing.T) {
	release := make(chan struct{})
	router, uc := newDemoRouter(t, blockingIngest{release}, 100)

	// Two different visitors fill both server-wide slots.
	if code := postBurstFrom(router, "198.51.100.1:1", ``); code != http.StatusAccepted {
		t.Fatalf("first burst: %d", code)
	}
	if code := postBurstFrom(router, "198.51.100.2:1", ``); code != http.StatusAccepted {
		t.Fatalf("second burst: %d", code)
	}
	// A third visitor, well within their own IP limit, is turned away.
	if code := postBurstFrom(router, "198.51.100.3:1", ``); code != http.StatusServiceUnavailable {
		t.Fatalf("third concurrent burst: got %d, want 503", code)
	}

	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := uc.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if code := postBurstFrom(router, "198.51.100.3:2", ``); code != http.StatusAccepted {
		t.Fatalf("after the bursts ended: got %d, want 202", code)
	}
}
