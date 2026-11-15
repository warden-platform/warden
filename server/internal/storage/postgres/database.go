package postgres

import (
	"context"
	"fmt"

	"gitee.com/warden-platform/warden/server/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ConnectPool(ctx context.Context) (*pgxpool.Pool, error) {
	dsn, err := config.RequiredSecret("WARDEN_DATABASE_URL", 1)
	if err != nil {
		return nil, err
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}

	cfg.MaxConns = 20
	cfg.MinConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
