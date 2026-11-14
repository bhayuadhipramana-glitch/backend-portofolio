package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

type fakePublisher struct {
	got []domain.Event
	err error
}

func (f *fakePublisher) Publish(_ context.Context, ev []domain.Event) error {
	f.got = append(f.got, ev...)
	return f.err
}
func (f *fakePublisher) Ping(context.Context) error { return f.err }

func TestEventUsecaseIngest(t *testing.T) {
	pub := &fakePublisher{}
	uc, err := NewEventUsecase(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := uc.Ingest(context.Background(), []domain.Event{{EventID: "a"}, {EventID: "b"}}); err != nil {
		t.Fatal(err)
	}
	if len(pub.got) != 2 {
		t.Fatalf("published %d events, want 2", len(pub.got))
	}
}

func TestEventUsecaseMapsBusFailure(t *testing.T) {
	uc, _ := NewEventUsecase(&fakePublisher{err: errors.New("nats down")})
	err := uc.Ingest(context.Background(), []domain.Event{{EventID: "a"}})
	if !errors.Is(err, domain.ErrEventsUnavailable) {
		t.Fatalf("got %v, want ErrEventsUnavailable", err)
	}
}

func TestEventUsecaseRejectsOversizedBatchAndNilPublisher(t *testing.T) {
	uc, _ := NewEventUsecase(&fakePublisher{})
	if err := uc.Ingest(context.Background(), make([]domain.Event, domain.MaxEventBatch+1)); err == nil {
		t.Fatal("oversized batch must be rejected")
	}
	if _, err := NewEventUsecase(nil); err == nil {
		t.Fatal("nil publisher must be rejected")
	}
}
