package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// newTestPool returns a pool whose search_path points at a fresh, private
// schema with every migration applied, so tests never touch real tables and
// can run in parallel with a developer's data. The test is skipped when
// DATABASE_URL is unset.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set; skipping PostgreSQL integration test")
	}
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping: %v", err)
	}

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "it_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})

	for _, f := range migrationFiles(t, ".up.sql") {
		applyFile(t, pool, f)
	}
	return pool
}

// migrationFiles returns the migrations/*<suffix> files in version order.
func migrationFiles(t *testing.T, suffix string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "*"+suffix))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found (%v)", err)
	}
	sort.Strings(files)
	return files
}

func applyFile(t *testing.T, pool *pgxpool.Pool, file string) {
	t.Helper()
	sql, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// No arguments: pgx uses the simple protocol, which allows many statements.
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatalf("apply %s: %v", filepath.Base(file), err)
	}
}

func TestServerIsPostgres17(t *testing.T) {
	pool := newTestPool(t)
	var v string
	if err := pool.QueryRow(context.Background(), `SHOW server_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v, "17") {
		t.Fatalf("server_version = %s, want 17.x", v)
	}
}

func TestMigrationsDownThenUp(t *testing.T) {
	pool := newTestPool(t)

	// Roll back the newest migration and re-apply it: both directions must be clean.
	downs := migrationFiles(t, ".down.sql")
	var latestDown string
	for _, f := range downs {
		if strings.Contains(f, "000004") {
			latestDown = f
		}
	}
	if latestDown == "" {
		t.Fatal("000004 down migration not found")
	}
	applyFile(t, pool, latestDown)
	for _, f := range migrationFiles(t, ".up.sql") {
		if strings.Contains(f, "000004") {
			applyFile(t, pool, f)
		}
	}

	// Re-running 000001 must be idempotent (CREATE OR REPLACE / DROP TRIGGER IF EXISTS).
	for _, f := range migrationFiles(t, ".up.sql") {
		if strings.Contains(f, "000001") {
			applyFile(t, pool, f)
		}
	}
}

func TestProjectCRUD(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewProjectRepository(pool)

	p := &domain.Project{Title: "First", Description: "d", TechStack: []string{"go", "pg"}}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !uuidRe.MatchString(p.ID) {
		t.Fatalf("id %q is not a UUID", p.ID)
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		t.Fatal("timestamps not populated")
	}

	got, err := repo.GetByID(ctx, p.ID)
	if err != nil || got.Title != "First" || len(got.TechStack) != 2 {
		t.Fatalf("get: %+v, %v", got, err)
	}

	p.Title = "Renamed"
	p.TechStack = nil // must be stored as {} rather than NULL
	time.Sleep(5 * time.Millisecond)
	if err := repo.Update(ctx, p); err != nil {
		t.Fatalf("update: %v", err)
	}
	if p.Title != "Renamed" || p.TechStack == nil || !p.UpdatedAt.After(p.CreatedAt) {
		t.Fatalf("update result: %+v", p)
	}

	if err := repo.Delete(ctx, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
	missing := "00000000-0000-0000-0000-000000000000"
	if err := repo.Update(ctx, &domain.Project{ID: missing, Title: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing = %v, want ErrNotFound", err)
	}
}

func TestProjectListPagination(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewProjectRepository(pool)

	for i := 0; i < 5; i++ {
		if err := repo.Create(ctx, &domain.Project{Title: fmt.Sprintf("p%d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := repo.List(ctx, domain.Page{Limit: 100})
	if err != nil || len(all) != 5 {
		t.Fatalf("list all: len=%d err=%v", len(all), err)
	}

	first, _ := repo.List(ctx, domain.Page{Limit: 2})
	second, _ := repo.List(ctx, domain.Page{Limit: 2, Offset: 2})
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("page sizes: %d, %d", len(first), len(second))
	}
	if first[0].ID != all[0].ID || second[0].ID != all[2].ID {
		t.Fatal("pages must be consecutive slices of one deterministic ordering")
	}

	// A zero page must not become an unbounded query.
	def, _ := repo.List(ctx, domain.Page{})
	if len(def) != 5 {
		t.Fatalf("default page returned %d rows", len(def))
	}
	if empty, err := repo.List(ctx, domain.Page{Limit: 10, Offset: 50}); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("offset past the end: %v, %v", empty, err)
	}
}

func TestArticleVisibility(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewArticleRepository(pool)

	now := time.Now()
	mk := func(slug, status string, publishedAt *time.Time) *domain.Article {
		a := &domain.Article{Slug: slug, Title: slug, Content: "c", Status: status, PublishedAt: publishedAt}
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		if !uuidRe.MatchString(a.ID) {
			t.Fatalf("id %q is not a UUID", a.ID)
		}
		return a
	}
	older, newer := now.Add(-time.Hour), now
	pubOld := mk("old", domain.ArticleStatusPublished, &older)
	pubNew := mk("new", domain.ArticleStatusPublished, &newer)
	draft := mk("draft", domain.ArticleStatusDraft, nil)
	archived := mk("archived", domain.ArticleStatusArchived, &older)

	public, err := repo.List(ctx, domain.VisibilityPublic, domain.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(public) != 2 || public[0].ID != pubNew.ID || public[1].ID != pubOld.ID {
		t.Fatalf("public list must be only published, newest first: %+v", slugs(public))
	}

	admin, _ := repo.List(ctx, domain.VisibilityAll, domain.Page{Limit: 100})
	if len(admin) != 4 {
		t.Fatalf("admin list = %v, want all 4 statuses", slugs(admin))
	}

	for _, hidden := range []*domain.Article{draft, archived} {
		if _, err := repo.GetByID(ctx, domain.VisibilityPublic, hidden.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("public GetByID(%s) = %v, want ErrNotFound", hidden.Slug, err)
		}
		if _, err := repo.GetBySlug(ctx, domain.VisibilityPublic, hidden.Slug); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("public GetBySlug(%s) = %v, want ErrNotFound", hidden.Slug, err)
		}
		if got, err := repo.GetByID(ctx, domain.VisibilityAll, hidden.ID); err != nil || got.ID != hidden.ID {
			t.Errorf("admin GetByID(%s) = %v, %v", hidden.Slug, got, err)
		}
		if got, err := repo.GetBySlug(ctx, domain.VisibilityAll, hidden.Slug); err != nil || got.ID != hidden.ID {
			t.Errorf("admin GetBySlug(%s) = %v, %v", hidden.Slug, got, err)
		}
	}
	if got, err := repo.GetBySlug(ctx, domain.VisibilityPublic, "new"); err != nil || got.ID != pubNew.ID {
		t.Fatalf("public GetBySlug(published) = %v, %v", got, err)
	}

	// Publishing a draft makes it visible; un-publishing hides it again.
	draft.Status, draft.PublishedAt = domain.ArticleStatusPublished, &now
	if err := repo.Update(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByID(ctx, domain.VisibilityPublic, draft.ID); err != nil {
		t.Fatalf("published draft should be public: %v", err)
	}

	// Public pagination is bounded by LIMIT.
	limited, _ := repo.List(ctx, domain.VisibilityPublic, domain.Page{Limit: 1})
	if len(limited) != 1 {
		t.Fatalf("limit=1 returned %d rows", len(limited))
	}
}

func TestArticleDuplicateSlugIsConflict(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewArticleRepository(pool)

	a := &domain.Article{Slug: "dup", Title: "a", Status: domain.ArticleStatusDraft}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &domain.Article{Slug: "dup", Title: "b", Status: domain.ArticleStatusDraft}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate slug = %v, want ErrConflict", err)
	}
	b := &domain.Article{Slug: "other", Title: "b", Status: domain.ArticleStatusDraft}
	if err := repo.Create(ctx, b); err != nil {
		t.Fatal(err)
	}
	b.Slug = "dup"
	if err := repo.Update(ctx, b); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("update to duplicate slug = %v, want ErrConflict", err)
	}
}

func slugs(as []domain.Article) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Slug
	}
	return out
}

func TestProfileVisibilityAndAtomicSwitch(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewProfileRepository(pool)

	activeIDs := func(vis domain.Visibility) []string {
		ps, err := repo.List(ctx, vis, domain.Page{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, p := range ps {
			if vis == domain.VisibilityAll && !p.IsActive {
				continue
			}
			ids = append(ids, p.ID)
		}
		return ids
	}

	a := &domain.Profile{Name: "A", IsActive: true}
	b := &domain.Profile{Name: "B"}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("creating an inactive profile: %v", err)
	}
	if !uuidRe.MatchString(a.ID) || string(a.SocialLinks) != "{}" {
		t.Fatalf("unexpected defaults: %+v", a)
	}

	if ids := activeIDs(domain.VisibilityPublic); len(ids) != 1 || ids[0] != a.ID {
		t.Fatalf("public list = %v, want only A", ids)
	}
	if all, _ := repo.List(ctx, domain.VisibilityAll, domain.Page{Limit: 100}); len(all) != 2 {
		t.Fatalf("admin list has %d profiles, want 2", len(all))
	}
	if _, err := repo.GetByID(ctx, domain.VisibilityPublic, b.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("public GetByID(inactive) = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetByID(ctx, domain.VisibilityAll, b.ID); err != nil {
		t.Fatalf("admin GetByID(inactive) = %v", err)
	}

	// Switch the active profile via Update: no conflict, A is deactivated.
	b.IsActive = true
	if err := repo.Update(ctx, b); err != nil {
		t.Fatalf("switching active profile: %v", err)
	}
	if ids := activeIDs(domain.VisibilityPublic); len(ids) != 1 || ids[0] != b.ID {
		t.Fatalf("after switch public list = %v, want only B", ids)
	}
	if _, err := repo.GetByID(ctx, domain.VisibilityPublic, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("old active profile still public: %v", err)
	}

	// Switch via Create.
	c := &domain.Profile{Name: "C", IsActive: true}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("creating a new active profile: %v", err)
	}
	if ids := activeIDs(domain.VisibilityAll); len(ids) != 1 || ids[0] != c.ID {
		t.Fatalf("active after create = %v, want only C", ids)
	}

	// A failed update (missing id) must roll back the deactivation.
	ghost := &domain.Profile{ID: "00000000-0000-0000-0000-000000000000", Name: "ghost", IsActive: true}
	if err := repo.Update(ctx, ghost); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing = %v, want ErrNotFound", err)
	}
	if ids := activeIDs(domain.VisibilityAll); len(ids) != 1 || ids[0] != c.ID {
		t.Fatalf("rollback failed, active = %v, want only C", ids)
	}
}

func TestConcurrentActivationNeverConflicts(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	profiles := NewProfileRepository(pool)
	resumes := NewResumeRepository(pool)

	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs <- profiles.Create(ctx, &domain.Profile{Name: fmt.Sprintf("p%d", i), IsActive: true})
		}()
		go func() {
			defer wg.Done()
			errs <- resumes.Create(ctx, &domain.Resume{Name: fmt.Sprintf("r%d", i), IsActive: true})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent activation failed (a 409 would surface as this): %v", err)
		}
	}

	for _, table := range []string{"profiles", "resumes"} {
		var active, total int
		q := fmt.Sprintf(`SELECT count(*) FILTER (WHERE is_active), count(*) FROM %s`, table)
		if err := pool.QueryRow(ctx, q).Scan(&active, &total); err != nil {
			t.Fatal(err)
		}
		if active != 1 || total != n {
			t.Fatalf("%s: active=%d total=%d, want 1 active of %d", table, active, total, n)
		}
	}
}

func TestResumeVisibilityAndAtomicSwitch(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewResumeRepository(pool)

	a := &domain.Resume{Name: "A", IsActive: true}
	b := &domain.Resume{Name: "B", Content: []byte(`{"k":1}`)}
	for _, r := range []*domain.Resume{a, b} {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
		if !uuidRe.MatchString(r.ID) {
			t.Fatalf("id %q is not a UUID", r.ID)
		}
	}

	public, err := repo.List(ctx, domain.VisibilityPublic, domain.Page{Limit: 100})
	if err != nil || len(public) != 1 || public[0].ID != a.ID {
		t.Fatalf("public list = %+v, %v; want only A", public, err)
	}
	if _, err := repo.GetByID(ctx, domain.VisibilityPublic, b.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("public GetByID(inactive) = %v, want ErrNotFound", err)
	}
	if got, err := repo.GetByID(ctx, domain.VisibilityAll, b.ID); err != nil || string(got.Content) != `{"k": 1}` {
		t.Fatalf("admin GetByID(inactive) = %+v, %v", got, err)
	}

	b.IsActive = true
	if err := repo.Update(ctx, b); err != nil {
		t.Fatalf("switching active resume: %v", err)
	}
	public, _ = repo.List(ctx, domain.VisibilityPublic, domain.Page{Limit: 100})
	if len(public) != 1 || public[0].ID != b.ID {
		t.Fatalf("after switch public list = %+v, want only B", public)
	}
	if _, err := repo.GetByID(ctx, domain.VisibilityPublic, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("old active resume still public: %v", err)
	}

	if err := repo.Delete(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if public, _ = repo.List(ctx, domain.VisibilityPublic, domain.Page{Limit: 100}); len(public) != 0 {
		t.Fatalf("no active resume expected after deleting it, got %d", len(public))
	}
}

func TestSessionLifecycle(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewSessionRepository(pool)

	hash := strings.Repeat("a", 64)
	s := &domain.Session{TokenHash: hash, GithubID: 42, Login: "admin", ExpiresAt: time.Now().Add(time.Hour)}
	if err := repo.Create(ctx, s); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByTokenHash(ctx, hash)
	if err != nil {
		t.Fatalf("get live session: %v", err)
	}
	if got.GithubID != 42 || got.RevokedAt != nil || got.LastSeenAt == nil {
		t.Fatalf("unexpected session: %+v", got)
	}

	if err := repo.Revoke(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByTokenHash(ctx, hash); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked session = %v, want ErrNotFound", err)
	}
	if err := repo.Revoke(ctx, hash); err != nil {
		t.Fatalf("revoking twice must not fail: %v", err)
	}
	if err := repo.Revoke(ctx, strings.Repeat("f", 64)); err != nil {
		t.Fatalf("revoking an unknown session must not fail: %v", err)
	}

	// Expired sessions are invisible and are removed by DeleteExpired.
	expired := &domain.Session{TokenHash: strings.Repeat("b", 64), GithubID: 42, Login: "admin", ExpiresAt: time.Now().Add(-time.Minute)}
	if err := repo.Create(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByTokenHash(ctx, expired.TokenHash); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired session = %v, want ErrNotFound", err)
	}
	if err := repo.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("sessions left = %d (%v), want only the revoked, unexpired one", n, err)
	}
}
