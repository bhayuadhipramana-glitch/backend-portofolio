package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// settleLog records how each delivery was settled.
type settleLog struct {
	mu                 sync.Mutex
	acked, naked, term []string
}

func (l *settleLog) delivery(id string, attempt int) domain.EventDelivery {
	rec := func(dst *[]string) func() error {
		return func() error {
			l.mu.Lock()
			defer l.mu.Unlock()
			*dst = append(*dst, id)
			return nil
		}
	}
	return domain.EventDelivery{
		Event:   domain.Event{EventID: id},
		Attempt: attempt,
		Ack:     rec(&l.acked),
		Nak:     rec(&l.naked),
		Term:    rec(&l.term),
	}
}

func (l *settleLog) counts() (acked, naked, term int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.acked), len(l.naked), len(l.term)
}

// scriptedSource returns the queued batches in order, then blocks until ctx ends.
type scriptedSource struct {
	mu      sync.Mutex
	batches [][]domain.EventDelivery
	errs    []error
}

func (s *scriptedSource) Fetch(ctx context.Context, _ int, _ time.Duration) ([]domain.EventDelivery, error) {
	s.mu.Lock()
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		s.mu.Unlock()
		return nil, err
	}
	if len(s.batches) > 0 {
		b := s.batches[0]
		s.batches = s.batches[1:]
		s.mu.Unlock()
		return b, nil
	}
	s.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

type fakeRepo struct {
	mu    sync.Mutex
	seen  map[string]bool
	calls int
	fail  int // number of leading calls that fail
	hook  func(ctx context.Context)
}

func (r *fakeRepo) InsertBatch(ctx context.Context, events []domain.Event) (int, error) {
	if r.hook != nil {
		r.hook(ctx)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls <= r.fail {
		return 0, errors.New("db down")
	}
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	n := 0
	for _, e := range events {
		if !r.seen[e.EventID] {
			r.seen[e.EventID] = true
			n++
		}
	}
	return n, nil
}

func newTestAggregator(t *testing.T, src domain.EventSource, repo domain.EventRepository) *Aggregator {
	t.Helper()
	a, err := NewAggregator(src, repo, quiet)
	if err != nil {
		t.Fatal(err)
	}
	a.sleep = func(context.Context, time.Duration) {} // no real backoff in tests
	return a
}

func runUntil(t *testing.T, a *Aggregator, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = a.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if !cond() {
		t.Fatal("condition not met before timeout")
	}
}

func TestNewAggregatorRejectsNil(t *testing.T) {
	if _, err := NewAggregator(nil, &fakeRepo{}, nil); err == nil {
		t.Fatal("nil source must be rejected")
	}
	if _, err := NewAggregator(&scriptedSource{}, nil, nil); err == nil {
		t.Fatal("nil repository must be rejected")
	}
}

func TestAggregatorAcksStoredBatch(t *testing.T) {
	log := &settleLog{}
	src := &scriptedSource{batches: [][]domain.EventDelivery{{log.delivery("a", 1), log.delivery("b", 1)}}}
	repo := &fakeRepo{}

	runUntil(t, newTestAggregator(t, src, repo), func() bool { a, _, _ := log.counts(); return a == 2 })

	if n, _, _ := log.counts(); n != 2 || len(repo.seen) != 2 {
		t.Fatalf("acked %d, stored %d, want 2/2", n, len(repo.seen))
	}
}

func TestAggregatorRedeliveryDoesNotDoubleStore(t *testing.T) {
	log := &settleLog{}
	// The same event arrives twice (bus redelivery after a lost ack).
	src := &scriptedSource{batches: [][]domain.EventDelivery{
		{log.delivery("a", 1)},
		{log.delivery("a", 2)},
	}}
	repo := &fakeRepo{}

	runUntil(t, newTestAggregator(t, src, repo), func() bool { a, _, _ := log.counts(); return a == 2 })

	if len(repo.seen) != 1 {
		t.Fatalf("stored %d distinct events, want 1", len(repo.seen))
	}
}

func TestAggregatorNaksOnFailureThenRecovers(t *testing.T) {
	log := &settleLog{}
	src := &scriptedSource{batches: [][]domain.EventDelivery{
		{log.delivery("a", 1)},
		{log.delivery("a", 2)}, // redelivered after the nak
	}}
	repo := &fakeRepo{fail: 1}

	runUntil(t, newTestAggregator(t, src, repo), func() bool { a, n, _ := log.counts(); return a == 1 && n == 1 })
}

func TestAggregatorTerminatesAfterMaxAttempts(t *testing.T) {
	log := &settleLog{}
	src := &scriptedSource{batches: [][]domain.EventDelivery{{log.delivery("poison", maxAttempts)}}}
	repo := &fakeRepo{fail: 100}

	runUntil(t, newTestAggregator(t, src, repo), func() bool { _, n, term := log.counts(); return term == 1 && n == 0 })
}

func TestAggregatorSurvivesFetchErrors(t *testing.T) {
	log := &settleLog{}
	src := &scriptedSource{
		errs:    []error{errors.New("nats timeout"), errors.New("nats timeout")},
		batches: [][]domain.EventDelivery{{log.delivery("a", 1)}},
	}
	runUntil(t, newTestAggregator(t, src, &fakeRepo{}), func() bool { a, _, _ := log.counts(); return a == 1 })
}

// A batch fetched just before shutdown must still be written and acked, and
// Run must not return before that is done.
func TestAggregatorDrainsInFlightBatchOnShutdown(t *testing.T) {
	log := &settleLog{}
	src := &scriptedSource{batches: [][]domain.EventDelivery{{log.delivery("a", 1), log.delivery("b", 1)}}}

	ctx, cancel := context.WithCancel(context.Background())
	var writeCtxErr atomic.Value
	repo := &fakeRepo{hook: func(wctx context.Context) {
		cancel() // shutdown arrives while the batch is being written
		time.Sleep(50 * time.Millisecond)
		writeCtxErr.Store(wctx.Err() == nil)
	}}

	a := newTestAggregator(t, src, repo)
	done := make(chan struct{})
	go func() { defer close(done); _ = a.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}

	if n, _, _ := log.counts(); n != 2 {
		t.Fatalf("acked %d events after shutdown, want 2", n)
	}
	if ok, _ := writeCtxErr.Load().(bool); !ok {
		t.Fatal("the database write context was cancelled by shutdown")
	}
}

func TestNextBackoffIsBounded(t *testing.T) {
	d := time.Duration(0)
	for i := 0; i < 20; i++ {
		d = nextBackoff(d)
	}
	if d != backoffMax {
		t.Fatalf("backoff = %v, want %v", d, backoffMax)
	}
	if nextBackoff(0) != backoffMin {
		t.Fatal("backoff must start at backoffMin")
	}
}
