package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// memCache is a ContentCache that keeps bytes until their namespace is
// invalidated and records which namespaces were invalidated.
type memCache struct {
	mu          sync.Mutex
	data        map[string][]byte
	fetches     int
	invalidated []string
	failInval   bool
}

func newMemCache() *memCache { return &memCache{data: map[string][]byte{}} }

func (m *memCache) Fetch(ctx context.Context, ns, key string, load func(context.Context) ([]byte, error)) ([]byte, error) {
	m.mu.Lock()
	m.fetches++
	v, ok := m.data[ns+"|"+key]
	m.mu.Unlock()
	if ok {
		domain.RecordCacheStatus(ctx, domain.CacheHitL1)
		return v, nil
	}
	v, err := load(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.data[ns+"|"+key] = v
	m.mu.Unlock()
	domain.RecordCacheStatus(ctx, domain.CacheMiss)
	return v, nil
}

func (m *memCache) Invalidate(_ context.Context, ns string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invalidated = append(m.invalidated, ns)
	for k := range m.data {
		if strings.HasPrefix(k, ns+"|") {
			delete(m.data, k)
		}
	}
	if m.failInval {
		return errors.New("valkey down")
	}
	return nil
}

func (m *memCache) Ping(context.Context) error { return nil }

// countingProjects is an in-memory ProjectRepository that counts reads.
type countingProjects struct {
	mu    sync.Mutex
	rows  map[string]domain.Project
	lists int
	gets  int
}

func newCountingProjects() *countingProjects {
	return &countingProjects{rows: map[string]domain.Project{"p1": {ID: "p1", Title: "one"}}}
}

func (r *countingProjects) Create(_ context.Context, p *domain.Project) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.ID = "p2"
	r.rows[p.ID] = *p
	return nil
}

func (r *countingProjects) List(context.Context, domain.Page) ([]domain.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lists++
	out := make([]domain.Project, 0, len(r.rows))
	for _, p := range r.rows {
		out = append(out, p)
	}
	return out, nil
}

func (r *countingProjects) GetByID(_ context.Context, id string) (*domain.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gets++
	p, ok := r.rows[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &p, nil
}

func (r *countingProjects) Update(_ context.Context, p *domain.Project) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rows[p.ID]; !ok {
		return domain.ErrNotFound
	}
	r.rows[p.ID] = *p
	return nil
}

func (r *countingProjects) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rows[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.rows, id)
	return nil
}

func TestProjectReadsAreServedFromCache(t *testing.T) {
	ctx := context.Background()
	repo, cache := newCountingProjects(), newMemCache()
	uc, err := NewProjectUsecase(repo, cache)
	if err != nil {
		t.Fatal(err)
	}

	for range 5 {
		if _, err := uc.List(ctx, domain.Page{}); err != nil {
			t.Fatal(err)
		}
		if p, err := uc.GetByID(ctx, "p1"); err != nil || p.Title != "one" {
			t.Fatalf("GetByID = %+v, %v", p, err)
		}
	}
	if repo.lists != 1 || repo.gets != 1 {
		t.Errorf("repository hit %d list / %d get times, want 1 / 1", repo.lists, repo.gets)
	}
}

func TestDifferentPagesUseDifferentKeys(t *testing.T) {
	ctx := context.Background()
	repo, cache := newCountingProjects(), newMemCache()
	uc, _ := NewProjectUsecase(repo, cache)

	_, _ = uc.List(ctx, domain.Page{Limit: 10})
	_, _ = uc.List(ctx, domain.Page{Limit: 10, Offset: 10})
	_, _ = uc.List(ctx, domain.Page{Limit: 10}) // repeat of the first page
	if repo.lists != 2 {
		t.Errorf("repository listed %d times, want 2", repo.lists)
	}
}

func TestNotFoundIsReturnedAndNotCached(t *testing.T) {
	ctx := context.Background()
	repo, cache := newCountingProjects(), newMemCache()
	uc, _ := NewProjectUsecase(repo, cache)

	for range 2 {
		if _, err := uc.GetByID(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}
	if repo.gets != 2 {
		t.Errorf("a miss must not be cached: repository saw %d gets, want 2", repo.gets)
	}
}

func TestEveryProjectMutationInvalidatesNamespace(t *testing.T) {
	ctx := context.Background()
	mutations := map[string]func(uc ProjectUsecase) error{
		"create": func(uc ProjectUsecase) error { return uc.Create(ctx, &domain.Project{Title: "two"}) },
		"update": func(uc ProjectUsecase) error { return uc.Update(ctx, &domain.Project{ID: "p1", Title: "changed"}) },
		"delete": func(uc ProjectUsecase) error { return uc.Delete(ctx, "p1") },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			repo, cache := newCountingProjects(), newMemCache()
			uc, _ := NewProjectUsecase(repo, cache)

			before, _ := uc.List(ctx, domain.Page{})
			if err := mutate(uc); err != nil {
				t.Fatal(err)
			}
			if len(cache.invalidated) != 1 || cache.invalidated[0] != domain.CacheNamespaceProjects {
				t.Fatalf("invalidated %v, want [projects]", cache.invalidated)
			}
			after, _ := uc.List(ctx, domain.Page{})
			if repo.lists != 2 {
				t.Errorf("list after %s came from the cache (repository lists = %d)", name, repo.lists)
			}
			if name != "update" && len(after) == len(before) {
				t.Errorf("list after %s is stale: %d rows before and after", name, len(before))
			}
		})
	}
}

func TestFailedMutationDoesNotInvalidate(t *testing.T) {
	ctx := context.Background()
	repo, cache := newCountingProjects(), newMemCache()
	uc, _ := NewProjectUsecase(repo, cache)

	if err := uc.Delete(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if len(cache.invalidated) != 0 {
		t.Errorf("a failed write invalidated %v", cache.invalidated)
	}
}

func TestInvalidationFailureDoesNotFailTheWrite(t *testing.T) {
	repo, cache := newCountingProjects(), newMemCache()
	cache.failInval = true
	uc, _ := NewProjectUsecase(repo, cache)

	if err := uc.Update(context.Background(), &domain.Project{ID: "p1", Title: "x"}); err != nil {
		t.Fatalf("the write is committed; the error must not surface: %v", err)
	}
}

func TestPublicReadsCachedAdminReadsNeverAre(t *testing.T) {
	ctx := context.Background()
	cache := newMemCache()
	ar, pr, rr := &countingArticles{}, &countingProfiles{}, &countingResumes{}
	au, _ := NewArticleUsecase(ar, cache)
	pu, _ := NewProfileUsecase(pr, cache)
	ru, _ := NewResumeUsecase(rr, cache)

	readAll := func(vis domain.Visibility) {
		_, _ = au.List(ctx, vis, domain.Page{})
		_, _ = au.GetByID(ctx, vis, "a1")
		_, _ = au.GetBySlug(ctx, vis, "hello")
		_, _ = pu.List(ctx, vis, domain.Page{})
		_, _ = pu.GetByID(ctx, vis, "p1")
		_, _ = ru.List(ctx, vis, domain.Page{})
		_, _ = ru.GetByID(ctx, vis, "r1")
	}

	for range 3 {
		readAll(domain.VisibilityPublic)
	}
	if ar.reads != 3 || pr.reads != 2 || rr.reads != 2 {
		t.Errorf("public reads reached the repositories articles=%d profiles=%d resumes=%d, want 3/2/2", ar.reads, pr.reads, rr.reads)
	}

	// Admin reads must bypass the cache entirely.
	ar.reads, pr.reads, rr.reads = 0, 0, 0
	fetchesBefore := cache.fetches
	for range 3 {
		readAll(domain.VisibilityAll)
	}
	if ar.reads != 9 || pr.reads != 6 || rr.reads != 6 {
		t.Errorf("admin reads were cached: articles=%d profiles=%d resumes=%d, want 9/6/6", ar.reads, pr.reads, rr.reads)
	}
	if cache.fetches != fetchesBefore {
		t.Error("admin reads must not touch the cache")
	}
}

func TestMutationsInvalidateTheirOwnNamespace(t *testing.T) {
	ctx := context.Background()
	cache := newMemCache()
	au, _ := NewArticleUsecase(&countingArticles{}, cache)
	pu, _ := NewProfileUsecase(&countingProfiles{}, cache)
	ru, _ := NewResumeUsecase(&countingResumes{}, cache)

	steps := []struct {
		name string
		run  func() error
		want string
	}{
		{"article create", func() error { return au.Create(ctx, &domain.Article{Title: "t"}) }, domain.CacheNamespaceArticles},
		{"article update", func() error { return au.Update(ctx, &domain.Article{ID: "a1"}) }, domain.CacheNamespaceArticles},
		{"article delete", func() error { return au.Delete(ctx, "a1") }, domain.CacheNamespaceArticles},
		{"profile create", func() error { return pu.Create(ctx, &domain.Profile{Name: "n"}) }, domain.CacheNamespaceProfiles},
		{"profile set active", func() error { return pu.Update(ctx, &domain.Profile{ID: "p1", IsActive: true}) }, domain.CacheNamespaceProfiles},
		{"profile delete", func() error { return pu.Delete(ctx, "p1") }, domain.CacheNamespaceProfiles},
		{"resume create", func() error { return ru.Create(ctx, &domain.Resume{Name: "n"}) }, domain.CacheNamespaceResumes},
		{"resume set active", func() error { return ru.Update(ctx, &domain.Resume{ID: "r1", IsActive: true}) }, domain.CacheNamespaceResumes},
		{"resume delete", func() error { return ru.Delete(ctx, "r1") }, domain.CacheNamespaceResumes},
	}
	for _, s := range steps {
		cache.invalidated = nil
		if err := s.run(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if len(cache.invalidated) != 1 || cache.invalidated[0] != s.want {
			t.Errorf("%s invalidated %v, want [%s]", s.name, cache.invalidated, s.want)
		}
	}
}

func TestActiveSwitchIsNeverServedStale(t *testing.T) {
	ctx := context.Background()
	cache := newMemCache()
	repo := &switchingProfiles{active: "p1"}
	pu, _ := NewProfileUsecase(repo, cache)

	list := func() string {
		ps, err := pu.List(ctx, domain.VisibilityPublic, domain.Page{})
		if err != nil || len(ps) != 1 {
			t.Fatalf("List = %v, %v", ps, err)
		}
		return ps[0].ID
	}
	if got := list(); got != "p1" {
		t.Fatalf("active = %s, want p1", got)
	}
	if err := pu.Update(ctx, &domain.Profile{ID: "p2", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if got := list(); got != "p2" {
		t.Errorf("public list still serves %s after switching the active profile to p2", got)
	}
}

func TestConstructorsRejectNilCache(t *testing.T) {
	if _, err := NewProjectUsecase(newCountingProjects(), nil); err == nil {
		t.Error("project usecase accepted a nil cache")
	}
	if _, err := NewArticleUsecase(&countingArticles{}, nil); err == nil {
		t.Error("article usecase accepted a nil cache")
	}
	if _, err := NewProfileUsecase(&countingProfiles{}, nil); err == nil {
		t.Error("profile usecase accepted a nil cache")
	}
	if _, err := NewResumeUsecase(&countingResumes{}, nil); err == nil {
		t.Error("resume usecase accepted a nil cache")
	}
}

func TestOversizedKeyBypassesCache(t *testing.T) {
	ctx := context.Background()
	cache := newMemCache()
	repo := &countingArticles{}
	uc, _ := NewArticleUsecase(repo, cache)

	slug := strings.Repeat("a", maxCacheKeyLen+1)
	for range 2 {
		_, _ = uc.GetBySlug(ctx, domain.VisibilityPublic, slug)
	}
	if repo.reads != 2 || cache.fetches != 0 {
		t.Errorf("oversized key: repo reads=%d cache fetches=%d, want 2 / 0", repo.reads, cache.fetches)
	}
}

func TestCorruptCacheEntryFallsBackToLoader(t *testing.T) {
	cache := newMemCache()
	cache.data[domain.CacheNamespaceProjects+"|id:p1"] = []byte("{not json")
	uc, _ := NewProjectUsecase(newCountingProjects(), cache)

	p, err := uc.GetByID(context.Background(), "p1")
	if err != nil || p.Title != "one" {
		t.Fatalf("GetByID = %+v, %v", p, err)
	}
}

func TestCachedValuesRoundTripThroughJSON(t *testing.T) {
	ctx := context.Background()
	repo := &countingProfiles{social: json.RawMessage(`{"github":"x"}`)}
	uc, _ := NewProfileUsecase(repo, newMemCache())

	if _, err := uc.GetByID(ctx, domain.VisibilityPublic, "p1"); err != nil {
		t.Fatal(err)
	}
	p, err := uc.GetByID(ctx, domain.VisibilityPublic, "p1") // decoded from the cache
	if err != nil {
		t.Fatal(err)
	}
	if string(p.SocialLinks) != `{"github":"x"}` {
		t.Errorf("social links = %s", p.SocialLinks)
	}
}

// ---- repository fakes -----------------------------------------------------

type countingArticles struct{ reads int }

func (r *countingArticles) List(context.Context, domain.Visibility, domain.Page) ([]domain.Article, error) {
	r.reads++
	return []domain.Article{{ID: "a1"}}, nil
}

func (r *countingArticles) GetByID(context.Context, domain.Visibility, string) (*domain.Article, error) {
	r.reads++
	return &domain.Article{ID: "a1"}, nil
}

func (r *countingArticles) GetBySlug(context.Context, domain.Visibility, string) (*domain.Article, error) {
	r.reads++
	return &domain.Article{ID: "a1"}, nil
}

func (r *countingArticles) Create(context.Context, *domain.Article) error { return nil }
func (r *countingArticles) Update(context.Context, *domain.Article) error { return nil }
func (r *countingArticles) Delete(context.Context, string) error          { return nil }

type countingProfiles struct {
	reads  int
	social json.RawMessage
}

func (r *countingProfiles) List(context.Context, domain.Visibility, domain.Page) ([]domain.Profile, error) {
	r.reads++
	return []domain.Profile{{ID: "p1", SocialLinks: r.social}}, nil
}

func (r *countingProfiles) GetByID(context.Context, domain.Visibility, string) (*domain.Profile, error) {
	r.reads++
	return &domain.Profile{ID: "p1", SocialLinks: r.social}, nil
}

func (r *countingProfiles) Create(context.Context, *domain.Profile) error { return nil }
func (r *countingProfiles) Update(context.Context, *domain.Profile) error { return nil }
func (r *countingProfiles) Delete(context.Context, string) error          { return nil }

type countingResumes struct{ reads int }

func (r *countingResumes) List(context.Context, domain.Visibility, domain.Page) ([]domain.Resume, error) {
	r.reads++
	return []domain.Resume{{ID: "r1"}}, nil
}

func (r *countingResumes) GetByID(context.Context, domain.Visibility, string) (*domain.Resume, error) {
	r.reads++
	return &domain.Resume{ID: "r1"}, nil
}

func (r *countingResumes) Create(context.Context, *domain.Resume) error { return nil }
func (r *countingResumes) Update(context.Context, *domain.Resume) error { return nil }
func (r *countingResumes) Delete(context.Context, string) error         { return nil }

// switchingProfiles keeps exactly one active profile, like the real repository.
type switchingProfiles struct{ active string }

func (r *switchingProfiles) List(context.Context, domain.Visibility, domain.Page) ([]domain.Profile, error) {
	return []domain.Profile{{ID: r.active, IsActive: true}}, nil
}

func (r *switchingProfiles) GetByID(context.Context, domain.Visibility, string) (*domain.Profile, error) {
	return &domain.Profile{ID: r.active, IsActive: true}, nil
}

func (r *switchingProfiles) Create(context.Context, *domain.Profile) error { return nil }

func (r *switchingProfiles) Update(_ context.Context, p *domain.Profile) error {
	if p.IsActive {
		r.active = p.ID
	}
	return nil
}

func (r *switchingProfiles) Delete(context.Context, string) error { return nil }
