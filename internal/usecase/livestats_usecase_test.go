package usecase

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

type fakeStatsRepo struct {
	rows  []domain.AggregateBucket
	err   error
	calls int
	since time.Time
}

func (f *fakeStatsRepo) Buckets(_ context.Context, since time.Time) ([]domain.AggregateBucket, error) {
	f.calls++
	f.since = since
	return f.rows, f.err
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestLiveSnapshotFillsGapsAndComputesRolling(t *testing.T) {
	// 12:30:30 UTC: window grid is 12:16..12:30; rolling starts at 12:29:00 (90 s wide).
	now := time.Date(2026, 5, 1, 12, 30, 30, 0, time.UTC)
	repo := &fakeStatsRepo{rows: []domain.AggregateBucket{
		{Bucket: time.Date(2026, 5, 1, 12, 20, 0, 0, time.UTC), TotalCount: 5, AvgLatencyMS: 5, MaxLatencyMS: 9},
		{Bucket: time.Date(2026, 5, 1, 12, 29, 0, 0, time.UTC), TotalCount: 60, ErrorCount: 6, AvgLatencyMS: 10, MaxLatencyMS: 40},
		{Bucket: time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC), TotalCount: 30, ErrorCount: 0, AvgLatencyMS: 20, MaxLatencyMS: 90},
	}}
	uc, err := newLiveStatsUsecase(repo, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	snap, err := uc.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if want := time.Date(2026, 5, 1, 12, 16, 0, 0, time.UTC); !repo.since.Equal(want) {
		t.Fatalf("queried since %v, want %v", repo.since, want)
	}
	if len(snap.Buckets) != domain.LiveWindowMinutes {
		t.Fatalf("got %d buckets, want %d", len(snap.Buckets), domain.LiveWindowMinutes)
	}
	for i := 1; i < len(snap.Buckets); i++ {
		if d := snap.Buckets[i].Bucket.Sub(snap.Buckets[i-1].Bucket); d != time.Minute {
			t.Fatalf("buckets %d and %d are %v apart, want 1m", i-1, i, d)
		}
	}
	if snap.Buckets[0].TotalCount != 0 || snap.Buckets[4].TotalCount != 5 {
		t.Fatalf("gap fill wrong: %+v", snap.Buckets[:5])
	}

	r := snap.Rolling
	if r.TotalCount != 90 || !near(r.WindowSeconds, 90) || !near(r.RPS, 1) {
		t.Fatalf("rolling totals wrong: %+v", r)
	}
	if !near(r.ErrorRate, 6.0/90) || !near(r.AvgLatencyMS, (60*10+30*20)/90.0) || r.MaxLatencyMS != 90 {
		t.Fatalf("rolling latency/error wrong: %+v", r)
	}
}

func TestLiveSnapshotEmptyIsZeroNotNaN(t *testing.T) {
	uc, _ := newLiveStatsUsecase(&fakeStatsRepo{}, time.Now)
	snap, err := uc.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := snap.Rolling
	if r.RPS != 0 || r.ErrorRate != 0 || r.AvgLatencyMS != 0 || math.IsNaN(r.AvgLatencyMS) {
		t.Fatalf("empty window must be all zero: %+v", r)
	}
}

func TestLiveSnapshotSharedWithinTTL(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 30, 30, 0, time.UTC)
	repo := &fakeStatsRepo{}
	uc, _ := newLiveStatsUsecase(repo, func() time.Time { return now })

	for i := 0; i < 50; i++ {
		if _, err := uc.Snapshot(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if repo.calls != 1 {
		t.Fatalf("50 snapshots within the TTL ran %d queries, want 1", repo.calls)
	}

	now = now.Add(liveStatsTTL + time.Millisecond)
	if _, err := uc.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.calls != 2 {
		t.Fatalf("expired snapshot did not reload: %d queries", repo.calls)
	}
}

func TestLiveSnapshotErrorsAreNotCached(t *testing.T) {
	repo := &fakeStatsRepo{err: errors.New("db down")}
	uc, _ := newLiveStatsUsecase(repo, time.Now)
	if _, err := uc.Snapshot(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	repo.err = nil
	if _, err := uc.Snapshot(context.Background()); err != nil {
		t.Fatalf("a failure must not poison the next call: %v", err)
	}
	if _, err := NewLiveStatsUsecase(nil); err == nil {
		t.Fatal("nil repository must be rejected")
	}
}
