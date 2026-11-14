package usecase

import (
	"context"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// recordingArticles records the arguments the usecase forwards to the repository.
type recordingArticles struct {
	domain.ArticleRepository // unused methods panic if called
	vis                      domain.Visibility
	page                     domain.Page
	created                  *domain.Article
}

func (r *recordingArticles) List(_ context.Context, vis domain.Visibility, p domain.Page) ([]domain.Article, error) {
	r.vis, r.page = vis, p
	return nil, nil
}

func (r *recordingArticles) GetByID(_ context.Context, vis domain.Visibility, _ string) (*domain.Article, error) {
	r.vis = vis
	return &domain.Article{}, nil
}

func (r *recordingArticles) GetBySlug(_ context.Context, vis domain.Visibility, _ string) (*domain.Article, error) {
	r.vis = vis
	return &domain.Article{}, nil
}

func (r *recordingArticles) Create(_ context.Context, a *domain.Article) error {
	r.created = a
	return nil
}

func TestArticleUsecaseForwardsVisibilityAndBoundsPage(t *testing.T) {
	ctx := context.Background()
	repo := &recordingArticles{}
	uc, err := NewArticleUsecase(repo, domain.NopCache{})
	if err != nil {
		t.Fatal(err)
	}

	for _, vis := range []domain.Visibility{domain.VisibilityPublic, domain.VisibilityAll} {
		if _, err := uc.List(ctx, vis, domain.Page{}); err != nil {
			t.Fatal(err)
		}
		if repo.vis != vis {
			t.Errorf("List forwarded visibility %v, want %v", repo.vis, vis)
		}
		if _, err := uc.GetByID(ctx, vis, "id"); err != nil || repo.vis != vis {
			t.Errorf("GetByID forwarded visibility %v, want %v (err=%v)", repo.vis, vis, err)
		}
		if _, err := uc.GetBySlug(ctx, vis, "slug"); err != nil || repo.vis != vis {
			t.Errorf("GetBySlug forwarded visibility %v, want %v (err=%v)", repo.vis, vis, err)
		}
	}

	bounds := []struct {
		name     string
		in, want domain.Page
	}{
		{"default limit", domain.Page{}, domain.Page{Limit: domain.DefaultPageLimit}},
		{"limit above max is capped", domain.Page{Limit: 100000}, domain.Page{Limit: domain.MaxPageLimit}},
		{"negative offset is clamped", domain.Page{Limit: 5, Offset: -9}, domain.Page{Limit: 5}},
	}
	for _, b := range bounds {
		if _, err := uc.List(ctx, domain.VisibilityPublic, b.in); err != nil {
			t.Fatal(err)
		}
		if repo.page != b.want {
			t.Errorf("%s: repository got %+v, want %+v", b.name, repo.page, b.want)
		}
	}
}

func TestArticleUsecaseStampsPublishedAt(t *testing.T) {
	repo := &recordingArticles{}
	uc, _ := NewArticleUsecase(repo, domain.NopCache{})

	pub := &domain.Article{Status: domain.ArticleStatusPublished}
	if err := uc.Create(context.Background(), pub); err != nil {
		t.Fatal(err)
	}
	if pub.PublishedAt == nil {
		t.Fatal("a published article must get a published_at, or it would sort last in the public list")
	}

	draft := &domain.Article{Status: domain.ArticleStatusDraft}
	if err := uc.Create(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	if draft.PublishedAt != nil {
		t.Fatal("a draft must not get a published_at")
	}
}

type recordingProfiles struct {
	domain.ProfileRepository
	vis  domain.Visibility
	page domain.Page
}

func (r *recordingProfiles) List(_ context.Context, vis domain.Visibility, p domain.Page) ([]domain.Profile, error) {
	r.vis, r.page = vis, p
	return nil, nil
}

func (r *recordingProfiles) GetByID(_ context.Context, vis domain.Visibility, _ string) (*domain.Profile, error) {
	r.vis = vis
	return &domain.Profile{}, nil
}

type recordingResumes struct {
	domain.ResumeRepository
	vis  domain.Visibility
	page domain.Page
}

func (r *recordingResumes) List(_ context.Context, vis domain.Visibility, p domain.Page) ([]domain.Resume, error) {
	r.vis, r.page = vis, p
	return nil, nil
}

func (r *recordingResumes) GetByID(_ context.Context, vis domain.Visibility, _ string) (*domain.Resume, error) {
	r.vis = vis
	return &domain.Resume{}, nil
}

func TestProfileAndResumeUsecasesForwardVisibilityAndBoundsPage(t *testing.T) {
	ctx := context.Background()
	pr, rr := &recordingProfiles{}, &recordingResumes{}
	pu, _ := NewProfileUsecase(pr, domain.NopCache{})
	ru, _ := NewResumeUsecase(rr, domain.NopCache{})

	for _, vis := range []domain.Visibility{domain.VisibilityPublic, domain.VisibilityAll} {
		if _, err := pu.List(ctx, vis, domain.Page{Limit: 9999}); err != nil {
			t.Fatal(err)
		}
		if pr.vis != vis || pr.page.Limit != domain.MaxPageLimit {
			t.Errorf("profile List: vis=%v page=%+v", pr.vis, pr.page)
		}
		if _, err := pu.GetByID(ctx, vis, "id"); err != nil || pr.vis != vis {
			t.Errorf("profile GetByID forwarded %v, want %v", pr.vis, vis)
		}

		if _, err := ru.List(ctx, vis, domain.Page{Limit: 9999}); err != nil {
			t.Fatal(err)
		}
		if rr.vis != vis || rr.page.Limit != domain.MaxPageLimit {
			t.Errorf("resume List: vis=%v page=%+v", rr.vis, rr.page)
		}
		if _, err := ru.GetByID(ctx, vis, "id"); err != nil || rr.vis != vis {
			t.Errorf("resume GetByID forwarded %v, want %v", rr.vis, vis)
		}
	}
}

func TestAuthenticateRejectsExpiredSession(t *testing.T) {
	ctx := context.Background()
	repo := newFakeSessions()
	uc := newAuth(t, 42, repo)

	token, _, err := uc.CompleteLogin(ctx, "code")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range repo.rows {
		s.ExpiresAt = s.ExpiresAt.AddDate(0, 0, -2)
	}
	if _, err := uc.Authenticate(ctx, token); err == nil {
		t.Fatal("an expired session must not authenticate")
	}
}
