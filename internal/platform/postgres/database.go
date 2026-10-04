package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Setup menginisialisasi dan memvalidasi koneksi ke PostgreSQL
func Setup(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("gagal membedah DSN: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("gagal membuat kolam koneksi: %w", err)
	}

	// Uji denyut (ping) untuk memastikan database benar-benar hidup
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("gagal melakukan ping ke database: %w", err)
	}

	return pool, nil
}
