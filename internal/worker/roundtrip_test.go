package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	natsbus "github.com/bhayuadhipramana-glicth/backend-portofolio/internal/platform/nats"
)

func uuid(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// syncRepo is an idempotent in-memory repository safe for the worker goroutine.
type syncRepo struct {
	mu       sync.Mutex
	stored   map[string]domain.Event
	inserted int
}

func (r *syncRepo) InsertBatch(_ context.Context, events []domain.Event) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stored == nil {
		r.stored = map[string]domain.Event{}
	}
	n := 0
	for _, e := range events {
		if _, dup := r.stored[e.EventID]; !dup {
			r.stored[e.EventID] = e
			n++
		}
	}
	r.inserted += n
	return n, nil
}

func (r *syncRepo) snapshot() (distinct, inserted int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.stored), r.inserted
}

// TestJetStreamRoundTrip publishes through the real publisher into a private
// JetStream stream and consumes it with the real durable consumer and worker.
// It is skipped when NATS is not reachable (docker compose up -d nats).
func TestJetStreamRoundTrip(t *testing.T) {
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = "nats://127.0.0.1:4222"
	}
	suffix := uuid(t)[:8]
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, err := natsbus.ConnectStream(ctx, url, "TEST_EVENTS_"+suffix, "test.events."+suffix)
	if err != nil {
		t.Skipf("NATS not reachable at %s: %v", url, err)
	}
	defer client.Close()
	defer func() { _ = client.DeleteStream(context.Background()) }()

	source, err := client.NewEventSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repo := &syncRepo{}
	agg, err := NewAggregator(source, repo, quiet)
	if err != nil {
		t.Fatal(err)
	}

	runCtx, stopWorker := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = agg.Run(runCtx) }()

	now := time.Now().UTC().Truncate(time.Millisecond)
	e1 := domain.Event{EventID: uuid(t), EventType: "page_view", Source: "web", LatencyMS: 12, OccurredAt: now}
	e2 := domain.Event{EventID: uuid(t), EventType: "click", Source: "web", LatencyMS: 3, IsError: true, OccurredAt: now}

	pub := natsbus.NewPublisher(client)
	// e1 appears twice in the batch and again in a retried request.
	if err := pub.Publish(ctx, []domain.Event{e1, e2, e1}); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(ctx, []domain.Event{e1}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d, _ := repo.snapshot(); d == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	stopWorker()
	<-done

	distinct, inserted := repo.snapshot()
	if distinct != 2 || inserted != 2 {
		t.Fatalf("stored %d distinct / %d inserts, want 2/2", distinct, inserted)
	}
	got := repo.stored[e2.EventID]
	if got.EventType != "click" || !got.IsError || got.LatencyMS != 3 || !got.OccurredAt.Equal(now) {
		t.Fatalf("event did not survive the round trip: %+v", got)
	}
}
