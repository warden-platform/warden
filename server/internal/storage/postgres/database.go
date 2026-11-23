package postgres

import (
	"context"
	"fmt"
	"log"

	"gitee.com/warden-platform/warden/server/internal/config"
	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
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

	// RLS 租户 GUC 注入：每次从池取连接时，把 ctx 里的租户写入会话级
	// app.tenant_id，供 RLS 策略 current_setting('app.tenant_id') 使用。
	// 现有 SQL 无需改写（租户过滤仍手写，RLS 作数据库层兜底）。空租户置 ''
	// → 策略匹配不到任何行（fail-closed）。连接归还时复位，防复用串租户。
	// 特权池（BYPASSRLS）同样注入但 RLS 对其无效，旁路语义不变。
	cfg.BeforeAcquire = func(ctx context.Context, conn *pgx.Conn) bool {
		tenant := domain.TenantIDFromContext(ctx)
		_, err := conn.Exec(ctx, "SELECT set_config('app.tenant_id', $1, false)", tenant)
		return err == nil
	}
	cfg.AfterRelease = func(conn *pgx.Conn) bool {
		_, _ = conn.Exec(context.Background(), "SELECT set_config('app.tenant_id', '', false)")
		return true
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}

// AssertRLSRuntime 校验 RLS 运行时配置，防止整套租户隔离因一条 DSN 配置漂移
// 而静默失效。返回 error 表示应拒绝启动。
//
// 判断：
//   - 主池角色受 RLS 约束（非 superuser 且非 BYPASSRLS）→ 正常；此时特权 DSN
//     必须已显式配置，否则 ConnectAdminPool 会回退主池，超管全局视图与后台
//     janitor（租约回收/审批过期等）将匹配 0 行而静默停摆。
//   - 主池角色是 superuser → 开发/测试形态，RLS 不在数据库层生效；打警告放行。
//   - 主池角色非 superuser 却带 BYPASSRLS → 配置错误（业务走了旁路角色，等于
//     无隔离且无任何告警），拒绝启动。
func AssertRLSRuntime(ctx context.Context, pool *pgxpool.Pool) error {
	var rolsuper, rolbypassrls bool
	err := pool.QueryRow(ctx,
		"SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user",
	).Scan(&rolsuper, &rolbypassrls)
	if err != nil {
		return fmt.Errorf("探测数据库角色属性: %w", err)
	}

	rlsEffective := !rolsuper && !rolbypassrls

	switch {
	case rlsEffective:
		privileged, err := config.Secret("WARDEN_PRIVILEGED_DATABASE_URL")
		if err != nil {
			return fmt.Errorf("读取 WARDEN_PRIVILEGED_DATABASE_URL: %w", err)
		}
		if privileged == "" {
			return fmt.Errorf("主数据库角色受 RLS 约束，但未配置 WARDEN_PRIVILEGED_DATABASE_URL：" +
				"超管全局视图与后台任务（租约回收/审批过期）将静默失效，拒绝启动")
		}
		return nil

	case rolsuper:
		log.Print("警告：主数据库角色为 superuser，RLS 租户隔离未在数据库层生效（仅限开发/测试环境）")
		return nil

	default:
		return fmt.Errorf("主数据库角色具备 BYPASSRLS 但非 superuser：应为受 RLS 约束的普通角色 " +
			"warden_app，当前配置会绕过租户隔离，拒绝启动")
	}
}