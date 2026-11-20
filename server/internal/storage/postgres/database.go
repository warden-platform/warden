package postgres

import (
	"context"
	"fmt"

	"gitee.com/warden-platform/warden/server/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConnectPool 建立应用运行时连接池。生产形态下 WARDEN_DATABASE_URL 指向
// 普通角色 warden_app（非 owner、非 superuser，受 FORCE RLS 约束）；
// 开发/测试可仍用 warden superuser（RLS 对其无效，行为不变）。
func ConnectPool(ctx context.Context) (*pgxpool.Pool, error) {
	dsn, err := config.RequiredSecret("WARDEN_DATABASE_URL", 1)
	if err != nil {
		return nil, err
	}
	return connectPoolFromDSN(ctx, dsn)
}

// ConnectAdminPool 建立合法跨租户旁路连接池（认证引导/超管全局/后台 janitor/
// tenants 管理）。生产形态下 WARDEN_PRIVILEGED_DATABASE_URL 指向 BYPASSRLS
// 特权角色 warden_privileged。未配置时回退主 DSN——单 DSN 的开发/测试形态
// 旁路与主路共用连接（superuser 本就绕过 RLS），行为不变。
func ConnectAdminPool(ctx context.Context) (*pgxpool.Pool, error) {
	dsn, err := config.Secret("WARDEN_PRIVILEGED_DATABASE_URL")
	if err != nil {
		return nil, err
	}
	if dsn == "" {
		dsn, err = config.RequiredSecret("WARDEN_DATABASE_URL", 1)
		if err != nil {
			return nil, err
		}
	}
	return connectPoolFromDSN(ctx, dsn)
}

func connectPoolFromDSN(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
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