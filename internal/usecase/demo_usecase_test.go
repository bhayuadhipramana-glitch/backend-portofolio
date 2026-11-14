package usecase

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// recordingIngest is an EventUsecase that keeps every batch it receives.
type recordingIngest struct {
	mu      sync.Mutex
	batches [][]domain.Event
	err     error
	gate    chan struct{} // when set, Ingest blocks until it is closed
}

func (r *recordingIngest) Ingest(ctx context.Context, ev []domain.Event) error {
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, ev)
	return r.err
}

func (r *recordingIngest) all() []domain.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Event
	for _, b := range r.batches {
		out = append(out, b...)
	}
	return out
}

func newTestBurst(t *testing.T, ing EventUsecase, maxConcurrent int) *demoBurstUsecase {
	t.Helper()
	uc, err := NewDemoBurstUsecase(context.Background(), ing, maxConcurrent)
	if err != nil {
		t.Fatal(err)
	}
	d := uc.(*demoBurstUsecase)
	d.sleep = func(context.Context, time.Duration) {} // no real pacing in tests
	return d
}

func waitDone(t *testing.T, uc DemoBurstUsecase) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := uc.Wait(ctx); err != nil {
		t.Fatalf("burst did not finish: %v", err)
	}
}

func TestDemoBurstPublishesExactlyCountRealisticEvents(t *testing.T) {
	ing := &recordingIngest{}
	uc := newTestBurst(t, ing, 2)

	if err := uc.Start(137, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	waitDone(t, uc)

	events := ing.all()
	if len(events) != 137 {
		t.Fatalf("published %d events, want 137", len(events))
	}
	if len(ing.batches) < 2 {
		t.Fatalf("a 2 s burst must be spread over several publishes, got %d", len(ing.batches))
	}

	types := map[string]int{}
	ids := map[string]bool{}
	for _, b := range ing.batches {
		if len(b) > domain.MaxEventBatch {
			t.Fatalf("batch of %d exceeds the ingest limit", len(b))
		}
	}
	for _, e := range events {
		if e.Source != domain.DemoEventSource {
			t.Fatalf("source = %q", e.Source)
		}
		if !uuidV4.MatchString(e.EventID) || ids[e.EventID] {
			t.Fatalf("event id %q is not a unique v4 UUID", e.EventID)
		}
		ids[e.EventID] = true
		if e.LatencyMS <= 0 || e.LatencyMS > 200 {
			t.Fatalf("unrealistic latency %v for %s", e.LatencyMS, e.EventType)
		}
		if e.EventType == "cache_hit" && (e.LatencyMS > 2.1 || e.IsError) {
			t.Fatalf("cache_hit must be fast and error free: %+v", e)
		}
		if e.OccurredAt.IsZero() || time.Since(e.OccurredAt) > time.Minute {
			t.Fatalf("occurred_at not current: %v", e.OccurredAt)
		}
		types[e.EventType]++
	}
	for _, typ := range []string{"page_view", "api_read", "cache_hit"} {
		if types[typ] == 0 {
			t.Fatalf("no %s events in a 137 event burst: %v", typ, types)
		}
	}
	if len(types) != 3 {
		t.Fatalf("unexpected event types: %v", types)
	}
}

func TestDemoBurstClampsOutOfRangeInput(t *testing.T) {
	ing := &recordingIngest{}
	uc := newTestBurst(t, ing, 2)
	if err := uc.Start(100000, time.Hour); err != nil {
		t.Fatal(err)
	}
	waitDone(t, uc)
	if got := len(ing.all()); got != domain.DemoMaxCount {
		t.Fatalf("published %d, want the clamped %d", got, domain.DemoMaxCount)
	}
}

func TestDemoBurstGlobalSemaphore(t *testing.T) {
	gate := make(chan struct{})
	ing := &recordingIngest{gate: gate}
	uc := newTestBurst(t, ing, 2)

	if err := uc.Start(10, time.Second); err != nil {
		t.Fatalf("first burst: %v", err)
	}
	if err := uc.Start(10, time.Second); err != nil {
		t.Fatalf("second burst: %v", err)
	}
	if err := uc.Start(10, time.Second); !errors.Is(err, domain.ErrBurstBusy) {
		t.Fatalf("third concurrent burst: got %v, want ErrBurstBusy", err)
	}

	close(gate) // let the running bursts finish
	waitDone(t, uc)

	if err := uc.Start(10, time.Second); err != nil {
		t.Fatalf("a finished burst must free its slot: %v", err)
	}
	waitDone(t, uc)
}

func TestDemoBurstStopsOnPublishFailureAndFreesSlot(t *testing.T) {
	ing := &recordingIngest{err: errors.New("bus down")}
	uc := newTestBurst(t, ing, 1)
	if err := uc.Start(100, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	waitDone(t, uc)
	if len(ing.batches) != 1 {
		t.Fatalf("burst kept publishing after a failure: %d publishes", len(ing.batches))
	}
	if err := uc.Start(10, time.Second); err != nil {
		t.Fatalf("slot leaked after a failed burst: %v", err)
	}
	waitDone(t, uc)
}

func TestDemoBurstStopsWhenBaseContextIsCancelled(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	ing := &recordingIngest{}
	uc, _ := NewDemoBurstUsecase(base, ing, 1)
	d := uc.(*demoBurstUsecase)
	d.sleep = sleepContext

	if err := uc.Start(100, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitDone(t, uc)
	if got := len(ing.all()); got >= 100 {
		t.Fatalf("burst ignored cancellation and published %d events", got)
	}
}

func TestDemoBurstWaitHonoursItsContext(t *testing.T) {
	ing := &recordingIngest{gate: make(chan struct{})}
	uc := newTestBurst(t, ing, 1)
	if err := uc.Start(10, time.Second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := uc.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
	close(ing.gate)
	waitDone(t, uc)
}

func TestNewDemoBurstUsecaseValidates(t *testing.T) {
	if _, err := NewDemoBurstUsecase(context.Background(), nil, 1); err == nil {
		t.Fatal("nil ingest must be rejected")
	}
	if _, err := NewDemoBurstUsecase(context.Background(), &recordingIngest{}, 0); err == nil {
		t.Fatal("zero concurrency must be rejected")
	}
}
