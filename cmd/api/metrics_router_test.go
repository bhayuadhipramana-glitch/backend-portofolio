package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/handler"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/observability"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/platform/valkey"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

const testMetricsToken = "0123456789abcdef0123456789abcdef"

func newMetricsRouter(t *testing.T, token string) http.Handler {
	t.Helper()
	obs, err := observability.New(context.Background(), observability.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = obs.Shutdown(context.Background()) })

	cache := obs.InstrumentCache(valkey.NewCache(nil, valkey.Config{})) // L1 only
	uc, err := usecase.NewProjectUsecase(&memProjects{title: "p"}, cache)
	if err != nil {
		t.Fatal(err)
	}
	return newRouter(routerDeps{
		db:             fakePinger{},
		projectHandler: handler.NewProjectHandler(uc),
		allowedOrigins: []string{"http://localhost:3000"},
		httpMetrics:    obs.HTTP,
		metricsHandler: observability.MetricsAccess(token)(obs.Handler()),
	})
}

func scrapeMetrics(t *testing.T, h http.Handler, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "127.0.0.1:4000"
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMetricsEndpointCountsHTTPAndCacheOperations(t *testing.T) {
	router := newMetricsRouter(t, "")

	first := get(t, router, "/api/v1/projects")
	second := get(t, router, "/api/v1/projects")
	if first.Header().Get("X-Cache") != "MISS" || second.Header().Get("X-Cache") != "HIT-L1" {
		t.Fatalf("X-Cache = %q then %q, want MISS then HIT-L1 (the metrics decorator must not hide it)",
			first.Header().Get("X-Cache"), second.Header().Get("X-Cache"))
	}
	get(t, router, "/healthz")
	get(t, router, "/does-not-exist")

	rec := scrapeMetrics(t, router, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, body %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`portfolio_http_server_duration_seconds_count{http_method="GET",http_route="/api/v1/projects",http_status="200"} 2`,
		`http_route="/healthz"`,
		`http_route="unmatched",http_status="404"`,
		`portfolio_cache_requests_total{cache_namespace="projects",cache_result="MISS"} 1`,
		`portfolio_cache_requests_total{cache_namespace="projects",cache_result="HIT-L1"} 1`,
		"go_goroutines", // runtime collector is exposed too
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics is missing %q", want)
		}
	}

	// A second scrape sees the first one: the counters keep moving.
	body = scrapeMetrics(t, router, nil).Body.String()
	if !strings.Contains(body, `http_route="/metrics",http_status="200"`) {
		t.Error("the /metrics route itself is not recorded")
	}
}

func TestMetricsEndpointRequiresTokenWhenConfigured(t *testing.T) {
	router := newMetricsRouter(t, testMetricsToken)

	if rec := scrapeMetrics(t, router, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", rec.Code)
	}
	if rec := scrapeMetrics(t, router, func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", rec.Code)
	}
	rec := scrapeMetrics(t, router, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testMetricsToken) })
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "portfolio_http_server_duration_seconds") {
		t.Errorf("right token: status = %d", rec.Code)
	}
	// The token must not be an admin or CI credential.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/benchmarks", nil)
	req.Header.Set("Authorization", "Bearer "+testMetricsToken)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req)
	if rec2.Code == http.StatusOK || rec2.Code == http.StatusCreated || rec2.Code == http.StatusAccepted {
		t.Errorf("the metrics token unlocked another route: %d", rec2.Code)
	}
}

func TestMetricsEndpointWithoutTokenIsInternalOnly(t *testing.T) {
	router := newMetricsRouter(t, "")

	if rec := scrapeMetrics(t, router, func(r *http.Request) { r.RemoteAddr = "10.42.0.9:4000" }); rec.Code != http.StatusOK {
		t.Errorf("cluster peer: status = %d, want 200", rec.Code)
	}
	if rec := scrapeMetrics(t, router, func(r *http.Request) { r.RemoteAddr = "198.51.100.7:4000" }); rec.Code != http.StatusForbidden {
		t.Errorf("public peer: status = %d, want 403", rec.Code)
	}
	if rec := scrapeMetrics(t, router, func(r *http.Request) { r.Header.Set("CF-Connecting-IP", "198.51.100.7") }); rec.Code != http.StatusForbidden {
		t.Errorf("request through the tunnel: status = %d, want 403", rec.Code)
	}
}

func TestMetricsRouteAbsentWhenNotConfigured(t *testing.T) {
	router, _ := newTestRouter(t, &memProjects{}, valkey.NewCache(nil, valkey.Config{}), fakePinger{}, fakePinger{})
	if rec := scrapeMetrics(t, router, nil); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no metrics handler is wired", rec.Code)
	}
}
