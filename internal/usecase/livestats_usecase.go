package usecase

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

const (
	// liveStatsTTL is how long one snapshot is shared between SSE clients, so
	// 100 viewers cost about four queries per second instead of 100. It adds to
	// the end-to-end latency, so it stays small (PRD G2: events under 2 s).
	liveStatsTTL = 250 * time.Millisecond
	// liveStatsTimeout bounds the aggregate query.
	liveStatsTimeout = 2 * time.Second
)

// LiveStatsUsecase builds the snapshots the SSE live feed streams.
type LiveStatsUsecase interface {
	// Snapshot returns the last 15 minutes of per-minute buckets and the
	// rolling throughput, error rate and latency. Concurrent callers share
	// one query.
	Snapshot(ctx context.Context) (*domain.LiveSnapshot, error)
}

type liveStatsUsecase struct {
	repo domain.EventStatsRepository
	now  func() time.Time

	mu       sync.Mutex // held during a refresh so concurrent callers wait for it
	snapshot *domain.LiveSnapshot
	loadedAt time.Time
}

// NewLiveStatsUsecase constructs a LiveStatsUsecase.
func NewLiveStatsUsecase(repo domain.EventStatsRepository) (LiveStatsUsecase, error) {
	return newLiveStatsUsecase(repo, time.Now)
}

func newLiveStatsUsecase(repo domain.EventStatsRepository, now func() time.Time) (*liveStatsUsecase, error) {
	if repo == nil {
		return nil, errors.New("usecase: event stats repository must not be nil")
	}
	return &liveStatsUsecase{repo: repo, now: now}, nil
}

func (u *liveStatsUsecase) Snapshot(ctx context.Context) (*domain.LiveSnapshot, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	now := u.now()
	if u.snapshot != nil && now.Sub(u.loadedAt) < liveStatsTTL {
		return u.snapshot, nil
	}

	// The shared query must not die with whichever client triggered it.
	qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), liveStatsTimeout)
	defer cancel()

	first := now.UTC().Truncate(time.Minute).Add(-(domain.LiveWindowMinutes - 1) * time.Minute)
	rows, err := u.repo.Buckets(qctx, first)
	if err != nil {
		return nil, fmt.Errorf("load live buckets: %w", err)
	}

	u.snapshot = buildSnapshot(now, first, rows)
	u.loadedAt = now
	return u.snapshot, nil
}

// buildSnapshot lays the rows out on a gap-free minute grid and derives the
// rolling figures. It is pure so it can be tested without a database.
func buildSnapshot(now, first time.Time, rows []domain.AggregateBucket) *domain.LiveSnapshot {
	now = now.UTC()
	byMinute := make(map[int64]domain.AggregateBucket, len(rows))
	for _, r := range rows {
		byMinute[r.Bucket.UTC().Truncate(time.Minute).Unix()] = r
	}

	buckets := make([]domain.AggregateBucket, 0, domain.LiveWindowMinutes)
	for i := 0; i < domain.LiveWindowMinutes; i++ {
		minute := first.Add(time.Duration(i) * time.Minute)
		b, ok := byMinute[minute.Unix()]
		if !ok {
			b = domain.AggregateBucket{}
		}
		b.Bucket = minute
		buckets = append(buckets, b)
	}

	// Rolling window: previous minute boundary up to now.
	windowStart := now.Truncate(time.Minute).Add(-time.Minute)
	roll := domain.RollingStats{WindowSeconds: now.Sub(windowStart).Seconds()}
	var errCount int64
	var latencySum float64
	for _, b := range buckets {
		if b.Bucket.Before(windowStart) {
			continue
		}
		roll.TotalCount += b.TotalCount
		errCount += b.ErrorCount
		latencySum += b.AvgLatencyMS * float64(b.TotalCount)
		roll.MaxLatencyMS = max(roll.MaxLatencyMS, b.MaxLatencyMS)
	}
	if roll.TotalCount > 0 {
		roll.RPS = float64(roll.TotalCount) / roll.WindowSeconds
		roll.ErrorRate = float64(errCount) / float64(roll.TotalCount)
		roll.AvgLatencyMS = latencySum / float64(roll.TotalCount)
	}

	return &domain.LiveSnapshot{GeneratedAt: now, Buckets: buckets, Rolling: roll}
}
