package usecase

import (
	"context"
	"errors"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// BenchmarkUsecase serves the public benchmark history (F8) and ingests new runs.
type BenchmarkUsecase interface {
	Create(ctx context.Context, b *domain.Benchmark) error
	List(ctx context.Context, page domain.Page) ([]domain.Benchmark, error)
}

type benchmarkUsecase struct {
	repo  domain.BenchmarkRepository
	cache domain.ContentCache
}

// NewBenchmarkUsecase constructs a BenchmarkUsecase.
func NewBenchmarkUsecase(repo domain.BenchmarkRepository, cache domain.ContentCache) (BenchmarkUsecase, error) {
	if repo == nil {
		return nil, errors.New("usecase: benchmark repository must not be nil")
	}
	if cache == nil {
		return nil, errors.New("usecase: benchmark cache must not be nil")
	}
	return &benchmarkUsecase{repo: repo, cache: cache}, nil
}

func (u *benchmarkUsecase) Create(ctx context.Context, b *domain.Benchmark) error {
	if b == nil {
		return errors.New("usecase: benchmark must not be nil")
	}
	if err := u.repo.Create(ctx, b); err != nil {
		return err
	}
	invalidate(ctx, u.cache, domain.CacheNamespaceBenchmarks)
	return nil
}

func (u *benchmarkUsecase) List(ctx context.Context, page domain.Page) ([]domain.Benchmark, error) {
	page = page.Normalize()
	return cached(ctx, u.cache, domain.CacheNamespaceBenchmarks, listKey(page), func(ctx context.Context) ([]domain.Benchmark, error) {
		return u.repo.List(ctx, page)
	})
}
