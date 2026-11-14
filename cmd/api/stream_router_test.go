package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/handler"
	authmw "github.com/bhayuadhipramana-glicth/backend-portofolio/internal/middleware"
)

type staticStats struct{}

func (staticStats) Snapshot(context.Context) (*domain.LiveSnapshot, error) {
	return &domain.LiveSnapshot{GeneratedAt: time.Now().UTC()}, nil
}

// The live feed is routed on the root router (outside middleware.Timeout) and
// must still pass through CORS, request logging and the client IP middleware.
func TestStreamRouteServesSSEThroughTheFullMiddlewareChain(t *testing.T) {
	router := newRouter(routerDeps{
		db:             fakePinger{},
		cache:          fakePinger{},
		streamHandler:  handler.NewStreamHandler(staticStats{}, authmw.NewConnLimiter(5, 5)),
		allowedOrigins: []string{"http://localhost:3000"},
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/events/stream", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("got %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatal("the stream must be served with CORS headers")
	}

	// Read until the first snapshot arrives; the logger wrapper must not buffer it.
	found := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "event: snapshot") {
				found <- true
				return
			}
		}
		found <- false
	}()
	select {
	case ok := <-found:
		if !ok {
			t.Fatal("stream ended without a snapshot")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no snapshot flushed through the middleware chain")
	}
}

func TestOtherRoutesStillWorkBesideTheStreamRoute(t *testing.T) {
	router := newRouter(routerDeps{
		db:             fakePinger{},
		cache:          fakePinger{},
		streamHandler:  handler.NewStreamHandler(staticStats{}, authmw.NewConnLimiter(5, 5)),
		allowedOrigins: []string{"http://localhost:3000"},
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/events/unknown", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown events path = %d, want 404", rec.Code)
	}
}
