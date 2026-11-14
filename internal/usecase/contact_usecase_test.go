package usecase

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

type stubContacts struct {
	got  domain.Contact
	err  error
	page domain.Page
}

func (s *stubContacts) CreateWithOutbox(_ context.Context, c *domain.Contact) error {
	s.got = *c
	return s.err
}

func (s *stubContacts) List(_ context.Context, p domain.Page) ([]domain.Contact, error) {
	s.page = p
	return nil, nil
}

func TestContactSubmitHashesClientIP(t *testing.T) {
	repo := &stubContacts{}
	uc, err := NewContactUsecase(repo)
	if err != nil {
		t.Fatal(err)
	}
	c := &domain.Contact{Name: "Ada"}
	if err := uc.Submit(context.Background(), c, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}

	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(repo.got.IPHash) {
		t.Fatalf("ip_hash = %q, want 64 hex chars", repo.got.IPHash)
	}
	if repo.got.IPHash == "203.0.113.7" {
		t.Fatal("raw IP stored")
	}
	// Known SHA-256 vector keeps the algorithm honest.
	if got := HashIP("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("HashIP(abc) = %s, not SHA-256", got)
	}
}

func TestContactSubmitPropagatesRepositoryError(t *testing.T) {
	boom := errors.New("tx failed")
	uc, _ := NewContactUsecase(&stubContacts{err: boom})
	if err := uc.Submit(context.Background(), &domain.Contact{}, "1.2.3.4"); !errors.Is(err, boom) {
		t.Fatalf("got %v, want the repository error", err)
	}
	if err := uc.Submit(context.Background(), nil, "1.2.3.4"); err == nil {
		t.Fatal("nil contact accepted")
	}
}

func TestContactListBoundsPage(t *testing.T) {
	repo := &stubContacts{}
	uc, _ := NewContactUsecase(repo)
	if _, err := uc.List(context.Background(), domain.Page{Limit: 100000, Offset: -5}); err != nil {
		t.Fatal(err)
	}
	if repo.page.Limit != domain.MaxPageLimit || repo.page.Offset != 0 {
		t.Fatalf("repository saw %+v, want a normalized page", repo.page)
	}
}

func TestNewContactUsecaseRejectsNil(t *testing.T) {
	if _, err := NewContactUsecase(nil); err == nil {
		t.Fatal("nil repository accepted")
	}
}
