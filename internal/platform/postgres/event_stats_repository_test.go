package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

func TestEventStatsRepositorySumsTypesPerMinuteAndFiltersByTime(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	events := NewEventRepository(pool)
	stats := NewEventStatsRepository(pool)

	m0 := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)
	m1 := m0.Add(time.Minute)
	if _, err := events.InsertBatch(ctx, []domain.Event{
		{EventID: "00000000-0000-4000-8000-000000000a01", EventType: "page_view", Source: "s", LatencyMS: 10, OccurredAt: m0},
		{EventID: "00000000-0000-4000-8000-000000000a02", EventType: "api_read", Source: "s", LatencyMS: 30, IsError: true, OccurredAt: m0.Add(time.Second)},
		{EventID: "00000000-0000-4000-8000-000000000a03", EventType: "page_view", Source: "s", LatencyMS: 5, OccurredAt: m1},
		{EventID: "00000000-0000-4000-8000-000000000a04", EventType: "page_view", Source: "s", LatencyMS: 99, OccurredAt: m0.Add(-time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := stats.Buckets(ctx, m0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d buckets, want 2 (the older one is filtered out): %+v", len(got), got)
	}
	if !got[0].Bucket.Equal(m0) || got[0].TotalCount != 2 || got[0].ErrorCount != 1 || got[0].AvgLatencyMS != 20 || got[0].MaxLatencyMS != 30 {
		t.Fatalf("minute 0 = %+v", got[0])
	}
	if !got[1].Bucket.Equal(m1) || got[1].TotalCount != 1 || got[1].MaxLatencyMS != 5 {
		t.Fatalf("minute 1 = %+v", got[1])
	}

	empty, err := stats.Buckets(ctx, m1.Add(time.Hour))
	if err != nil || len(empty) != 0 {
		t.Fatalf("future window: %d rows, err %v", len(empty), err)
	}
}
