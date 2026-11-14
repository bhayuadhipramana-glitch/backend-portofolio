package main

import (
	"context"
	"errors"
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

type fakePinger struct{ err error }

func (p fakePinger) Ping(context.Context) error { return p.err }

// memProjects is an in-memory ProjectRepository.
type memProjects struct {
	mu    sync.Mutex
	title string
	reads int
}

func (r *memProjects) Create(context.Context, *domain.Project) error { return nil }
func (r *memProjects) Delete(context.Context, string) error          { return nil }

func (r *memProjects) Update(_ context.Context, p *domain.Project) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.title = p.Title
	return nil
}

func (r *memProjects) List(context.Context, domain.Page) ([]domain.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	return []domain.Project{{ID: "00000000-0000-0000-0000-000000000001", Title: r.title}}, nil
}

func (r *memProjects) GetByID(context.Context, string) (*domain.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	return &domain.Project{ID: "00000000-0000-0000-0000-000000000001", Title: r.title}, nil
}

func newTestRouter(t *testing.T, repo domain.ProjectRepository, cache domain.ContentCache, db, cachePing pinger) (http.Handler, usecase.ProjectUsecase) {
	t.Helper()
	uc, err := usecase.NewProjectUsecase(repo, cache)
	if err != nil {
		t.Fatal(err)
	}
	return newRouter(routerDeps{
		db:             db,
		cache:          cachePing,
		projectHandler: handler.NewProjectHandler(uc),
		allowedOrigins: []string{"http://localhost:3000"},
	}), uc
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestPublicGetExposesXCacheAndInvalidatesOnAdminWrite(t *testing.T) {
	repo := &memProjects{title: "before"}
	cache := valkey.NewCache(nil, valkey.Config{}) // L1 only; L2 is covered in the valkey package
	router, uc := newTestRouter(t, repo, cache, fakePinger{}, cache)

	const path = "/api/v1/projects"

	first := get(t, router, path)
	if first.Code != http.StatusOK || first.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first GET = %d X-Cache=%q, want 200 MISS", first.Code, first.Header().Get("X-Cache"))
	}
	second := get(t, router, path)
	if second.Header().Get("X-Cache") != "HIT-L1" {
		t.Fatalf("second GET X-Cache=%q, want HIT-L1", second.Header().Get("X-Cache"))
	}
	if second.Body.String() != first.Body.String() {
		t.Errorf("cached body differs:\n%s\n%s", first.Body.String(), second.Body.String())
	}

	if err := uc.Update(context.Background(), &domain.Project{ID: "00000000-0000-0000-0000-000000000001", Title: "after"}); err != nil {
		t.Fatal(err)
	}

	third := get(t, router, path)
	if third.Header().Get("X-Cache") != "MISS" || !strings.Contains(third.Body.String(), `"after"`) {
		t.Fatalf("GET after admin update: X-Cache=%q body=%s, want MISS with the new title", third.Header().Get("X-Cache"), third.Body.String())
	}

	detail := get(t, router, path+"/00000000-0000-0000-0000-000000000001")
	if detail.Code != http.StatusOK || detail.Header().Get("X-Cache") != "MISS" {
		t.Errorf("detail GET = %d X-Cache=%q, want 200 MISS", detail.Code, detail.Header().Get("X-Cache"))
	}
}

func TestInvalidRequestCarriesNoXCache(t *testing.T) {
	cache := valkey.NewCache(nil, valkey.Config{})
	router, _ := newTestRouter(t, &memProjects{}, cache, fakePinger{}, cache)

	rec := get(t, router, "/api/v1/projects?limit=abc")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("X-Cache"); got != "" {
		t.Errorf("X-Cache = %q on a rejected request", got)
	}
}

func TestReadyz(t *testing.T) {
	tests := []struct {
		name       string
		db, cache  error
		wantCode   int
		wantSubstr string
	}{
		{"all healthy", nil, nil, http.StatusOK, "ready"},
		{"valkey down degrades but stays ready", nil, errors.New("valkey down"), http.StatusOK, "cache degraded"},
		{"database down is not ready", errors.New("db down"), nil, http.StatusServiceUnavailable, "database unavailable"},
		{"both down is not ready", errors.New("db down"), errors.New("valkey down"), http.StatusServiceUnavailable, "database unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _ := newTestRouter(t, &memProjects{}, domain.NopCache{}, fakePinger{tt.db}, fakePinger{tt.cache})
			rec := get(t, router, "/readyz")
			if rec.Code != tt.wantCode || !strings.Contains(rec.Body.String(), tt.wantSubstr) {
				t.Errorf("readyz = %d %q, want %d containing %q", rec.Code, rec.Body.String(), tt.wantCode, tt.wantSubstr)
			}
		})
	}
}
