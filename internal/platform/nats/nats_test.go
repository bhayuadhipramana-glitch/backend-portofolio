package nats

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// newTestClient connects to a real NATS (NATS_URL, default
// nats://127.0.0.1:4222; start it with `docker compose up -d nats`) and skips
// the test when none is reachable.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = "nats://127.0.0.1:4222"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := Connect(ctx, url)
	if err != nil {
		t.Skipf("NATS not reachable at %s: %v", url, err)
	}
	t.Cleanup(c.Close)
	return c
}

func newUUID(t *testing.T) string {
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

func TestConnectEnsuresBoundedStream(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	stream, err := c.js.Stream(ctx, domain.EventStreamName)
	if err != nil {
		t.Fatal(err)
	}
	cfg := stream.CachedInfo().Config
	if cfg.MaxMsgs != streamMaxMsgs || cfg.MaxBytes != streamMaxBytes {
		t.Fatalf("stream is not bounded: %+v", cfg)
	}
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestPublishDeduplicatesByEventID(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pub := NewPublisher(c)
	ev := domain.Event{
		EventID: newUUID(t), EventType: "dedupe_test", Source: "test",
		LatencyMS: 1, OccurredAt: time.Now().UTC(),
	}

	stream, err := c.js.Stream(ctx, domain.EventStreamName)
	if err != nil {
		t.Fatal(err)
	}
	before, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Same event_id twice in one batch and again in a retry.
	if err := pub.Publish(ctx, []domain.Event{ev, ev}); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(ctx, []domain.Event{ev}); err != nil {
		t.Fatal(err)
	}

	// Remove the test message so a running worker never aggregates it.
	t.Cleanup(func() {
		raw, err := stream.GetLastMsgForSubject(context.Background(), domain.EventSubject)
		if err == nil && raw.Header.Get(jetstream.MsgIDHeader) == ev.EventID {
			_ = stream.DeleteMsg(context.Background(), raw.Sequence)
		}
	})

	after, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := after.State.Msgs - before.State.Msgs; got != 1 {
		t.Fatalf("stream grew by %d messages, want 1 (Nats-Msg-Id dedup)", got)
	}
}
