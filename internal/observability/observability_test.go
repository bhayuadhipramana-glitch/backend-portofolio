package observability

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

func newProvider(t *testing.T) *Provider {
	t.Helper()
	p, err := New(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p
}

// scrape returns the Prometheus text exposition of p.
func scrape(t *testing.T, p *Provider) string {
	t.Helper()
	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d", rec.Code)
	}
	return rec.Body.String()
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("metrics output is missing %q\n%s", w, body)
		}
	}
}

func TestHTTPRecordsRouteMethodStatusAndDuration(t *testing.T) {
	p := newProvider(t)
	r := chi.NewRouter()
	r.Use(p.HTTP)
	r.Get("/items/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	r.Get("/boom", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })

	for _, path := range []string{"/items/1", "/items/2", "/boom", "/nope/abc"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	body := scrape(t, p)
	mustContain(t, body,
		// two different ids collapse into one route label: bounded cardinality.
		`portfolio_http_server_duration_seconds_count{http_method="GET",http_route="/items/{id}",http_status="200"} 2`,
		`http_status="500"`,
		`http_route="unmatched",http_status="404"`,
		`portfolio_http_server_duration_seconds_bucket`,
	)
	if strings.Contains(body, "/items/1") || strings.Contains(body, "/nope/abc") {
		t.Error("raw paths leaked into labels")
	}
}

func TestHTTPSkipsEventStreamsAndKeepsFlusher(t *testing.T) {
	p := newProvider(t)
	flushed := false
	h := p.HTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush through the metrics wrapper failed: %v", err)
		}
		flushed = true
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/stream", nil))

	if !flushed {
		t.Fatal("handler did not run")
	}
	if strings.Contains(scrape(t, p), "portfolio_http_server_duration_seconds_count") {
		t.Error("an SSE stream was recorded in the latency histogram")
	}
}

// statusCache reports a fixed cache status, like valkey.Cache does.
type statusCache struct{ statuses []domain.CacheStatus }

func (c *statusCache) Fetch(ctx context.Context, _, _ string, load func(context.Context) ([]byte, error)) ([]byte, error) {
	s := c.statuses[0]
	c.statuses = c.statuses[1:]
	domain.RecordCacheStatus(ctx, s)
	return load(ctx)
}
func (c *statusCache) Invalidate(context.Context, string) error { return nil }
func (c *statusCache) Ping(context.Context) error               { return errors.New("down") }

func TestInstrumentCacheCountsResultsAndForwardsStatus(t *testing.T) {
	p := newProvider(t)
	inner := &statusCache{statuses: []domain.CacheStatus{domain.CacheMiss, domain.CacheHitL1, domain.CacheHitL2, domain.CacheHitL1}}
	c := p.InstrumentCache(inner)
	load := func(context.Context) ([]byte, error) { return []byte("x"), nil }

	var last domain.CacheStatus
	for range 4 {
		ctx, trace := domain.WithCacheTrace(context.Background())
		if _, err := c.Fetch(ctx, domain.CacheNamespaceProjects, "k", load); err != nil {
			t.Fatal(err)
		}
		last = trace.Status()
	}
	if last != domain.CacheHitL1 {
		t.Errorf("caller trace status = %q, want HIT-L1 (X-Cache must keep working)", last)
	}
	if err := c.Ping(context.Background()); err == nil {
		t.Error("Ping must pass the inner error through")
	}

	mustContain(t, scrape(t, p),
		`portfolio_cache_requests_total{cache_namespace="projects",cache_result="HIT-L1"} 2`,
		`portfolio_cache_requests_total{cache_namespace="projects",cache_result="HIT-L2"} 1`,
		`portfolio_cache_requests_total{cache_namespace="projects",cache_result="MISS"} 1`,
	)
}

type fakePublisher struct{ err error }

func (f fakePublisher) Publish(context.Context, []domain.Event) error { return f.err }
func (fakePublisher) Ping(context.Context) error                      { return nil }

type fakeSource struct{ n int }

func (f fakeSource) Fetch(context.Context, int, time.Duration) ([]domain.EventDelivery, error) {
	return make([]domain.EventDelivery, f.n), nil
}

func TestInstrumentNATSCountsPublishedAndConsumedEvents(t *testing.T) {
	p := newProvider(t)
	evs := make([]domain.Event, 3)

	if err := p.InstrumentPublisher(fakePublisher{}).Publish(context.Background(), evs); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("nats down")
	if err := p.InstrumentPublisher(fakePublisher{err: boom}).Publish(context.Background(), evs[:2]); !errors.Is(err, boom) {
		t.Fatalf("publish error = %v, want the inner error", err)
	}
	if _, err := p.InstrumentSource(fakeSource{n: 5}).Fetch(context.Background(), 100, time.Millisecond); err != nil {
		t.Fatal(err)
	}

	mustContain(t, scrape(t, p),
		`portfolio_nats_published_events_total{outcome="ok"} 3`,
		`portfolio_nats_published_events_total{outcome="error"} 2`,
		`portfolio_nats_consumed_events_total 5`,
	)
}

// fakeOutbox calls deliver once with a message that has already failed `attempts` times.
type fakeOutbox struct{ attempts int }

func (f fakeOutbox) Dispatch(ctx context.Context, _, _ int, _ func(int) time.Duration, deliver func(context.Context, domain.OutboxMessage) error) (int, error) {
	_ = deliver(ctx, domain.OutboxMessage{Attempts: f.attempts})
	return 1, nil
}

func TestInstrumentOutboxCountsSentFailedAndDead(t *testing.T) {
	p := newProvider(t)
	ctx := context.Background()
	ok := func(context.Context, domain.OutboxMessage) error { return nil }
	bad := func(context.Context, domain.OutboxMessage) error { return errors.New("webhook 502") }

	_, _ = p.InstrumentOutbox(fakeOutbox{attempts: 0}).Dispatch(ctx, 10, 5, nil, ok)
	_, _ = p.InstrumentOutbox(fakeOutbox{attempts: 1}).Dispatch(ctx, 10, 5, nil, bad)
	_, _ = p.InstrumentOutbox(fakeOutbox{attempts: 4}).Dispatch(ctx, 10, 5, nil, bad) // 5th attempt

	mustContain(t, scrape(t, p),
		`portfolio_outbox_deliveries_total{outcome="sent"} 1`,
		`portfolio_outbox_deliveries_total{outcome="failed"} 1`,
		`portfolio_outbox_deliveries_total{outcome="dead"} 1`,
	)
}

func TestObservableGaugesReportAtScrapeTime(t *testing.T) {
	p := newProvider(t)
	active, pending := 7, int64(3)
	if err := p.ObserveActiveSSE(func() int { return active }); err != nil {
		t.Fatal(err)
	}
	failing := false
	if err := p.ObserveOutboxPending(func(context.Context) (int64, error) {
		if failing {
			return 0, errors.New("db down")
		}
		return pending, nil
	}); err != nil {
		t.Fatal(err)
	}

	mustContain(t, scrape(t, p), "portfolio_sse_connections_active 7", "portfolio_outbox_pending 3")

	active, failing = 2, true // a failing backlog query must not fail the scrape
	body := scrape(t, p)
	mustContain(t, body, "portfolio_sse_connections_active 2")
	if strings.Contains(body, "portfolio_outbox_pending 3") {
		t.Error("stale outbox sample reported after the query failed")
	}
}

func TestMetricsAccessWithToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	h := MetricsAccess(token)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "metrics") }))

	cases := []struct {
		name, auth string
		want       int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"not bearer", "Basic " + token, http.StatusUnauthorized},
		{"right token", "Bearer " + token, http.StatusOK},
		{"scheme is case-insensitive", "bearer " + token, http.StatusOK},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.RemoteAddr = "8.8.8.8:1234" // the token alone decides, not the peer
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}

func TestMetricsAccessWithoutTokenIsInternalOnly(t *testing.T) {
	h := MetricsAccess("")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "metrics") }))

	cases := []struct {
		name, remote string
		header       [2]string
		want         int
	}{
		{"loopback", "127.0.0.1:5000", [2]string{}, http.StatusOK},
		{"ipv6 loopback", "[::1]:5000", [2]string{}, http.StatusOK},
		{"cluster pod ip", "10.42.0.17:5000", [2]string{}, http.StatusOK},
		{"public ip", "203.0.113.9:5000", [2]string{}, http.StatusForbidden},
		{"tunnel (private peer, Cloudflare header)", "10.42.0.5:5000", [2]string{"CF-Connecting-IP", "203.0.113.9"}, http.StatusForbidden},
		{"tunnel (Cf-Ray)", "10.42.0.5:5000", [2]string{"Cf-Ray", "abc"}, http.StatusForbidden},
		{"forwarded by a proxy", "10.42.0.5:5000", [2]string{"X-Forwarded-For", "203.0.113.9"}, http.StatusForbidden},
		{"unparsable peer", "garbage", [2]string{}, http.StatusForbidden},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.RemoteAddr = tc.remote
		if tc.header[0] != "" {
			req.Header.Set(tc.header[0], tc.header[1])
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}

func TestNewWithOTLPEndpointEnablesTracingAndShutsDown(t *testing.T) {
	// Nothing listens here: the exporter is lazy, so New must still succeed and
	// Shutdown must return promptly instead of hanging the process exit.
	p, err := New(context.Background(), Config{OTLPEndpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.traces == nil {
		t.Fatal("tracing should be enabled when an endpoint is set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = p.Shutdown(ctx)
}

func TestNewWithoutEndpointKeepsTracingOff(t *testing.T) {
	if p := newProvider(t); p.traces != nil {
		t.Fatal("tracing must be a no-op when OTEL_EXPORTER_OTLP_ENDPOINT is unset")
	}
}
