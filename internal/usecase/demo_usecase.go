package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"sync"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

const (
	// burstStep is the pacing of a burst: events are released in slices this far apart.
	burstStep = 100 * time.Millisecond
	// burstGrace is added to the burst duration to bound its context.
	burstGrace = 10 * time.Second
)

// DemoBurstUsecase generates a short, bounded burst of synthetic events (F7).
type DemoBurstUsecase interface {
	// Start begins a burst of count events spread over duration and returns
	// at once; the events are published in the background through the F3
	// ingest path. It returns domain.ErrBurstBusy when the server-wide limit of
	// concurrent bursts is reached.
	Start(count int, duration time.Duration) error
	// Wait blocks until every running burst has finished or ctx ends. Call it
	// during shutdown, before the event bus is closed.
	Wait(ctx context.Context) error
}

type demoBurstUsecase struct {
	ingest EventUsecase
	base   context.Context // cancelled at shutdown; bursts must not outlive it
	sem    chan struct{}   // one slot per concurrent burst, server-wide
	wg     sync.WaitGroup
	step   time.Duration
	sleep  func(ctx context.Context, d time.Duration)
	now    func() time.Time
}

// NewDemoBurstUsecase constructs a DemoBurstUsecase that publishes through
// ingest. base bounds the lifetime of every burst; maxConcurrent is the
// server-wide cap.
func NewDemoBurstUsecase(base context.Context, ingest EventUsecase, maxConcurrent int) (DemoBurstUsecase, error) {
	if ingest == nil {
		return nil, errors.New("usecase: event usecase must not be nil")
	}
	if base == nil || maxConcurrent < 1 {
		return nil, errors.New("usecase: demo burst needs a base context and a positive concurrency cap")
	}
	return &demoBurstUsecase{
		ingest: ingest,
		base:   base,
		sem:    make(chan struct{}, maxConcurrent),
		step:   burstStep,
		sleep:  sleepContext,
		now:    time.Now,
	}, nil
}

func (u *demoBurstUsecase) Start(count int, duration time.Duration) error {
	count, duration = domain.ClampDemoBurst(count, int(duration/time.Millisecond))

	select {
	case u.sem <- struct{}{}:
	default:
		return domain.ErrBurstBusy
	}

	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		defer func() { <-u.sem }()
		u.run(count, duration)
	}()
	return nil
}

func (u *demoBurstUsecase) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() { u.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run publishes count events in slices over duration.
func (u *demoBurstUsecase) run(count int, duration time.Duration) {
	ctx, cancel := context.WithTimeout(u.base, duration+burstGrace)
	defer cancel()

	steps := max(1, int(duration/u.step))
	burstID := newBurstID()
	sent := 0
	for i := 0; i < steps; i++ {
		// Spread the remainder so the slices sum to exactly count.
		n := count*(i+1)/steps - count*i/steps
		if n > 0 {
			events := make([]domain.Event, n)
			now := u.now().UTC()
			for j := range events {
				events[j] = syntheticEvent(now, burstID)
			}
			if err := u.ingest.Ingest(ctx, events); err != nil {
				slog.Error("demo burst: publish failed, stopping burst", "burst", burstID, "sent", sent, "error", err)
				return
			}
			sent += n
		}
		if i < steps-1 {
			u.sleep(ctx, u.step)
			if ctx.Err() != nil {
				return
			}
		}
	}
	slog.Info("demo burst finished", "burst", burstID, "events", sent, "duration", duration.String())
}

// syntheticEvent builds one realistic event: mostly page views, then API reads
// and cache hits, each with a latency range and error odds that fit its type.
func syntheticEvent(now time.Time, burstID string) domain.Event {
	var (
		typ      string
		latency  float64
		errorOdd float64
	)
	switch r := mrand.Float64(); {
	case r < 0.5:
		typ, latency, errorOdd = "page_view", 20+mrand.Float64()*100, 0.01
	case r < 0.8:
		typ, latency, errorOdd = "api_read", 5+mrand.Float64()*45, 0.02
	default:
		typ, latency, errorOdd = "cache_hit", 0.2+mrand.Float64()*1.8, 0
	}
	return domain.Event{
		EventID:    newEventID(),
		EventType:  typ,
		Source:     domain.DemoEventSource,
		LatencyMS:  float64(int(latency*10)) / 10,
		IsError:    mrand.Float64() < errorOdd,
		Payload:    []byte(fmt.Sprintf(`{"demo":true,"burst":%q}`, burstID)),
		OccurredAt: now,
	}
}

// newEventID returns a random RFC 4122 version 4 UUID.
func newEventID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func newBurstID() string { return newEventID()[:8] }

func sleepContext(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
