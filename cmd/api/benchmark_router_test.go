package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/handler"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/platform/valkey"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

type memBenchmarks struct {
	mu   sync.Mutex
	rows []domain.Benchmark
}

func (m *memBenchmarks) Create(_ context.Context, b *domain.Benchmark) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b.ID = "00000000-0000-4000-8000-0000000000b1"
	m.rows = append([]domain.Benchmark{*b}, m.rows...)
	return nil
}

func (m *memBenchmarks) List(context.Context, domain.Page) ([]domain.Benchmark, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]domain.Benchmark(nil), m.rows...), nil
}

const ciToken = "0123456789abcdef0123456789abcdef-ci-token"

type f8Env struct {
	routerEnv
	benchmarks *memBenchmarks
}

func newF8Router(t *testing.T) f8Env {
	t.Helper()
	benchmarks := &memBenchmarks{}

	buc, err := usecase.NewBenchmarkUsecase(benchmarks, valkey.NewCache(nil, valkey.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	router := newRouter(routerDeps{
		db:               fakePinger{},
		appEnv:           "local",
		benchmarkHandler: handler.NewBenchmarkHandler(buc),
		benchmarkToken:   ciToken,
		authenticator:    cookieAuth{},
		allowedOrigins:   []string{"http://localhost:3000"},
	})
	return f8Env{routerEnv: routerEnv{router: router}, benchmarks: benchmarks}
}

const benchBody = `{"commit_sha":"6e7b16f1","scenario":"f1_read_p99","target_rps":5000,"achieved_rps":5012.5,"p50_ms":3.1,"p95_ms":9.8,"p99_ms":21.4,"error_rate":0.0001,"raw_summary":{"vus":200}}`

func TestBenchmarkPostAuthorization(t *testing.T) {
	bearer := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
	}
	tests := []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{"no credentials", nil, http.StatusUnauthorized},
		{"valid CI token", bearer(ciToken), http.StatusCreated},
		{"wrong CI token", bearer(ciToken + "x"), http.StatusUnauthorized},
		{"prefix of the token", bearer(ciToken[:10]), http.StatusUnauthorized},
		{"empty bearer", bearer(""), http.StatusUnauthorized},
		{"basic scheme", func(r *http.Request) { r.Header.Set("Authorization", "Basic "+ciToken) }, http.StatusUnauthorized},
		{"admin session", asAdmin, http.StatusCreated},
		{"wrong session", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session", Value: "nope"}) }, http.StatusUnauthorized},
		{"bad bearer does not fall back to session", func(r *http.Request) {
			asAdmin(r)
			bearer("wrong")(r)
		}, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newF8Router(t)
			rec := e.do(http.MethodPost, "/api/v1/benchmarks", benchBody, "", tt.mutate)
			if rec.Code != tt.want {
				t.Fatalf("got %d (%s), want %d", rec.Code, rec.Body.String(), tt.want)
			}
		})
	}
}

func TestBenchmarkIngestInvalidatesPublicCache(t *testing.T) {
	e := newF8Router(t)

	first := e.do(http.MethodGet, "/api/v1/benchmarks", "", "", nil)
	if first.Code != http.StatusOK || first.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first GET = %d X-Cache=%q, want 200 MISS", first.Code, first.Header().Get("X-Cache"))
	}
	if second := e.do(http.MethodGet, "/api/v1/benchmarks", "", "", nil); second.Header().Get("X-Cache") != "HIT-L1" {
		t.Fatalf("second GET X-Cache=%q, want HIT-L1", second.Header().Get("X-Cache"))
	}

	post := e.do(http.MethodPost, "/api/v1/benchmarks", benchBody, "", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+ciToken)
	})
	if post.Code != http.StatusCreated {
		t.Fatalf("POST = %d %s", post.Code, post.Body.String())
	}

	after := e.do(http.MethodGet, "/api/v1/benchmarks", "", "", nil)
	if after.Header().Get("X-Cache") != "MISS" || !strings.Contains(after.Body.String(), "f1_read_p99") {
		t.Fatalf("after ingest X-Cache=%q body=%s, want a MISS holding the new run", after.Header().Get("X-Cache"), after.Body.String())
	}
}

func TestBenchmarkBearerDisabledWithoutConfiguredToken(t *testing.T) {
	uc, _ := usecase.NewBenchmarkUsecase(&memBenchmarks{}, domain.NopCache{})
	router := newRouter(routerDeps{
		db:               fakePinger{},
		appEnv:           "local",
		benchmarkHandler: handler.NewBenchmarkHandler(uc),
		authenticator:    cookieAuth{},
		allowedOrigins:   []string{"http://localhost:3000"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/benchmarks", strings.NewReader(benchBody))
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("empty bearer with no token configured = %d, want 401", rec.Code)
	}
}

func TestBenchmarkValidation(t *testing.T) {
	e := newF8Router(t)
	bad := `{"commit_sha":"zz","scenario":"x","target_rps":1,"achieved_rps":1,"p50_ms":1,"p95_ms":1,"p99_ms":1,"error_rate":2}`
	rec := e.do(http.MethodPost, "/api/v1/benchmarks", bad, "", asAdmin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 for a bad sha and error_rate > 1", rec.Code)
	}
}

func TestPprofIsAdminOnly(t *testing.T) {
	e := newF8Router(t)

	for _, path := range []string{
		"/api/v1/admin/debug/pprof/",
		"/api/v1/admin/debug/pprof/heap",
		"/api/v1/admin/debug/pprof/goroutine?debug=1",
		"/api/v1/admin/debug/pprof/cmdline",
	} {
		if rec := e.do(http.MethodGet, path, "", "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s = %d, want 401", path, rec.Code)
		}
		if rec := e.do(http.MethodGet, path, "", "", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+ciToken) // the CI token must not unlock pprof
		}); rec.Code != http.StatusUnauthorized {
			t.Errorf("CI token GET %s = %d, want 401", path, rec.Code)
		}
	}

	if rec := e.do(http.MethodGet, "/api/v1/admin/debug/pprof/goroutine?debug=1", "", "", asAdmin); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "goroutine") {
		t.Fatalf("admin goroutine profile = %d, want 200 with a profile", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/api/v1/admin/debug/pprof/", "", "", asAdmin); rec.Code != http.StatusOK {
		t.Fatalf("admin pprof index = %d, want 200", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/api/v1/admin/debug/pprof/nosuchprofile", "", "", asAdmin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown profile = %d, want 404", rec.Code)
	}

	// Nothing may be reachable outside the admin prefix.
	for _, path := range []string{"/debug/pprof/", "/api/v1/debug/pprof/heap"} {
		if rec := e.do(http.MethodGet, path, "", "", asAdmin); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}
