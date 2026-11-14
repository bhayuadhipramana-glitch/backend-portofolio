package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

type aggRow struct {
	total, errs int64
	sum, max    float64
}

func readAgg(t *testing.T, pool *pgxpool.Pool, bucket time.Time, typ string) aggRow {
	t.Helper()
	var r aggRow
	err := pool.QueryRow(context.Background(),
		`SELECT total_count, error_count, sum_latency_ms, max_latency_ms FROM event_aggregates WHERE bucket = $1 AND event_type = $2`,
		bucket, typ).Scan(&r.total, &r.errs, &r.sum, &r.max)
	if err != nil {
		t.Fatalf("read aggregate %s@%s: %v", typ, bucket, err)
	}
	return r
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEventRepositoryAggregatesPerMinute(t *testing.T) {
	pool := newTestPool(t)
	repo := NewEventRepository(pool)
	ctx := context.Background()

	minute := time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC)
	next := minute.Add(time.Minute)
	events := []domain.Event{
		{EventID: "00000000-0000-4000-8000-000000000001", EventType: "page_view", Source: "web", LatencyMS: 10, OccurredAt: minute.Add(5 * time.Second)},
		{EventID: "00000000-0000-4000-8000-000000000002", EventType: "page_view", Source: "web", LatencyMS: 30, IsError: true, OccurredAt: minute.Add(50 * time.Second)},
		{EventID: "00000000-0000-4000-8000-000000000003", EventType: "page_view", Source: "web", LatencyMS: 5, OccurredAt: next.Add(time.Second)},
		{EventID: "00000000-0000-4000-8000-000000000004", EventType: "click", Source: "web", LatencyMS: 1, OccurredAt: minute},
	}
	n, err := repo.InsertBatch(ctx, events)
	if err != nil || n != 4 {
		t.Fatalf("inserted %d, err %v, want 4", n, err)
	}

	if got := readAgg(t, pool, minute, "page_view"); got != (aggRow{2, 1, 40, 30}) {
		t.Fatalf("page_view minute 1 = %+v", got)
	}
	if got := readAgg(t, pool, next, "page_view"); got != (aggRow{1, 0, 5, 5}) {
		t.Fatalf("page_view minute 2 = %+v", got)
	}
	if got := readAgg(t, pool, minute, "click"); got != (aggRow{1, 0, 1, 1}) {
		t.Fatalf("click = %+v", got)
	}
}

func TestEventRepositoryIsIdempotentPerEventID(t *testing.T) {
	pool := newTestPool(t)
	repo := NewEventRepository(pool)
	ctx := context.Background()

	minute := time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC)
	a := domain.Event{EventID: "00000000-0000-4000-8000-0000000000aa", EventType: "page_view", Source: "web", LatencyMS: 10, IsError: true, OccurredAt: minute}
	b := domain.Event{EventID: "00000000-0000-4000-8000-0000000000bb", EventType: "page_view", Source: "web", LatencyMS: 20, OccurredAt: minute}

	// A duplicate inside one batch counts once.
	if n, err := repo.InsertBatch(ctx, []domain.Event{a, a, b}); err != nil || n != 2 {
		t.Fatalf("first batch inserted %d, err %v, want 2", n, err)
	}
	// Redelivery of the same events counts nothing.
	if n, err := repo.InsertBatch(ctx, []domain.Event{a, b}); err != nil || n != 0 {
		t.Fatalf("redelivery inserted %d, err %v, want 0", n, err)
	}
	// A redelivered event next to a new one only counts the new one.
	c := domain.Event{EventID: "00000000-0000-4000-8000-0000000000cc", EventType: "page_view", Source: "web", LatencyMS: 50, OccurredAt: minute}
	if n, err := repo.InsertBatch(ctx, []domain.Event{a, c}); err != nil || n != 1 {
		t.Fatalf("mixed batch inserted %d, err %v, want 1", n, err)
	}

	if got := countRows(t, pool, "events"); got != 3 {
		t.Fatalf("events rows = %d, want 3", got)
	}
	if got := readAgg(t, pool, minute, "page_view"); got != (aggRow{3, 1, 80, 50}) {
		t.Fatalf("aggregate = %+v, want 3 events, 1 error, sum 80, max 50", got)
	}
}

func TestEventRepositoryStoresPayloadAndRejectsNegativeLatency(t *testing.T) {
	pool := newTestPool(t)
	repo := NewEventRepository(pool)
	ctx := context.Background()
	at := time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC)

	ev := domain.Event{EventID: "00000000-0000-4000-8000-0000000000dd", EventType: "t", Source: "s", Payload: []byte(`{"k":"v"}`), OccurredAt: at}
	if _, err := repo.InsertBatch(ctx, []domain.Event{ev}); err != nil {
		t.Fatal(err)
	}
	var k string
	if err := pool.QueryRow(ctx, `SELECT payload->>'k' FROM events WHERE event_id = $1`, ev.EventID).Scan(&k); err != nil || k != "v" {
		t.Fatalf("payload round trip: %q, %v", k, err)
	}

	// The CHECK constraint fails the whole statement: nothing is half written.
	bad := domain.Event{EventID: "00000000-0000-4000-8000-0000000000ee", EventType: "t", Source: "s", LatencyMS: -1, OccurredAt: at}
	good := domain.Event{EventID: "00000000-0000-4000-8000-0000000000ff", EventType: "t", Source: "s", LatencyMS: 1, OccurredAt: at}
	if _, err := repo.InsertBatch(ctx, []domain.Event{good, bad}); err == nil {
		t.Fatal("negative latency must be rejected")
	}
	if got := countRows(t, pool, "events"); got != 1 {
		t.Fatalf("events rows = %d, want 1 (failed batch must roll back)", got)
	}
	if got := readAgg(t, pool, at, "t"); got.total != 1 {
		t.Fatalf("aggregate total = %d, want 1 (failed batch must not be counted)", got.total)
	}
}

func TestEventRepositoryEmptyBatch(t *testing.T) {
	pool := newTestPool(t)
	if n, err := NewEventRepository(pool).InsertBatch(context.Background(), nil); err != nil || n != 0 {
		t.Fatalf("got %d, %v", n, err)
	}
}
