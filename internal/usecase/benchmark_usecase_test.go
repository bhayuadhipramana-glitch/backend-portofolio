package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

type stubBenchmarks struct {
	rows  []domain.Benchmark
	lists int
	err   error
}

func (s *stubBenchmarks) Create(_ context.Context, b *domain.Benchmark) error {
	if s.err != nil {
		return s.err
	}
	s.rows = append([]domain.Benchmark{*b}, s.rows...)
	return nil
}

func (s *stubBenchmarks) List(context.Context, domain.Page) ([]domain.Benchmark, error) {
	s.lists++
	return append([]domain.Benchmark(nil), s.rows...), nil
}

func TestBenchmarkListIsCachedAndCreateInvalidates(t *testing.T) {
	ctx := context.Background()
	repo, cache := &stubBenchmarks{}, newMemCache()
	uc, err := NewBenchmarkUsecase(repo, cache)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, err := uc.List(ctx, domain.Page{}); err != nil {
			t.Fatal(err)
		}
	}
	if repo.lists != 1 {
		t.Fatalf("repository hit %d times for 3 reads, want 1 (cache-aside)", repo.lists)
	}

	if err := uc.Create(ctx, &domain.Benchmark{Scenario: "f1"}); err != nil {
		t.Fatal(err)
	}
	if len(cache.invalidated) != 1 || cache.invalidated[0] != domain.CacheNamespaceBenchmarks {
		t.Fatalf("invalidated %v, want [benchmarks]", cache.invalidated)
	}
	got, err := uc.List(ctx, domain.Page{})
	if err != nil || len(got) != 1 || repo.lists != 2 {
		t.Fatalf("after ingest: %d rows, %d repo reads, err %v; want the fresh row via a reload", len(got), repo.lists, err)
	}
}

func TestBenchmarkCreateFailureDoesNotInvalidate(t *testing.T) {
	cache := newMemCache()
	uc, _ := NewBenchmarkUsecase(&stubBenchmarks{err: errors.New("db down")}, cache)
	if err := uc.Create(context.Background(), &domain.Benchmark{}); err == nil {
		t.Fatal("expected an error")
	}
	if len(cache.invalidated) != 0 {
		t.Fatalf("invalidated %v after a failed write", cache.invalidated)
	}
}

func TestNewBenchmarkUsecaseRejectsNil(t *testing.T) {
	if _, err := NewBenchmarkUsecase(nil, domain.NopCache{}); err == nil {
		t.Error("nil repository accepted")
	}
	if _, err := NewBenchmarkUsecase(&stubBenchmarks{}, nil); err == nil {
		t.Error("nil cache accepted")
	}
}
