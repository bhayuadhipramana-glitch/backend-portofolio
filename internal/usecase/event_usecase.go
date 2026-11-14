package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// publishTimeout bounds one publish round trip to the event bus.
const publishTimeout = 3 * time.Second

// EventUsecase accepts telemetry events for asynchronous aggregation.
type EventUsecase interface {
	// Ingest publishes validated events to the bus. It returns
	// domain.ErrEventsUnavailable when the bus did not accept them.
	Ingest(ctx context.Context, events []domain.Event) error
}

type eventUsecase struct {
	publisher domain.EventPublisher
}

// NewEventUsecase constructs an EventUsecase.
func NewEventUsecase(publisher domain.EventPublisher) (EventUsecase, error) {
	if publisher == nil {
		return nil, errors.New("usecase: event publisher must not be nil")
	}
	return &eventUsecase{publisher: publisher}, nil
}

func (u *eventUsecase) Ingest(ctx context.Context, events []domain.Event) error {
	if len(events) == 0 {
		return nil
	}
	if len(events) > domain.MaxEventBatch {
		return fmt.Errorf("usecase: batch of %d exceeds %d events", len(events), domain.MaxEventBatch)
	}

	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	if err := u.publisher.Publish(ctx, events); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrEventsUnavailable, err)
	}
	return nil
}
