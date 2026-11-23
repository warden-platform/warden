package postgres

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAssertRLSRuntime 验证启动探针对三类角色的判定：superuser 开发形态告警放行、
// 受 RLS 约束角色要求特权 DSN 已配、BYPASSRLS 非 superuser 拒绝。
func TestAssertRLSRuntime(t *testing.T) {
	ownerURL := os.Getenv("WARDEN_TEST_DATABASE_URL")
	if ownerURL == "" {
		t.Skip("WARDEN_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	ownerPool, err := pgxpool.New(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer ownerPool.Close()

	rolesSQL, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "db", "roles.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownerPool.Exec(ctx, string(rolesSQL)); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerPool.Exec(ctx, `ALTER ROLE warden_app LOGIN PASSWORD 'testapp'; ALTER ROLE warden_privileged LOGIN PASSWORD 'testpriv';`); err != nil {
		t.Fatal(err)
	}

	appPool, err := connectPoolFromDSN(ctx, roleURL(t, ownerURL, "warden_app", "testapp"))
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	defer appPool.Close()

	adminPool, err := connectPoolFromDSN(ctx, roleURL(t, ownerURL, "warden_privileged", "testpriv"))
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer adminPool.Close()

	t.Run("superuser dev 形态放行", func(t *testing.T) {
		if err := AssertRLSRuntime(ctx, ownerPool); err != nil {
			t.Errorf("superuser 应放行（仅告警），却返回错误: %v", err)
		}
	})

	t.Run("受 RLS 约束但漏配特权 DSN 应拒绝", func(t *testing.T) {
		t.Setenv("WARDEN_PRIVILEGED_DATABASE_URL", "")
		if err := AssertRLSRuntime(ctx, appPool); err == nil {
			t.Error("warden_app 受 RLS 约束却未配特权 DSN，应拒绝启动，却未报错")
		}
	})

	t.Run("受 RLS 约束且特权 DSN 已配应通过", func(t *testing.T) {
		t.Setenv("WARDEN_PRIVILEGED_DATABASE_URL", "postgres://warden_privileged:testpriv@postgresql:5432/warden")
		if err := AssertRLSRuntime(ctx, appPool); err != nil {
			t.Errorf("warden_app + 特权 DSN 已配应通过，却返回错误: %v", err)
		}
	})

	t.Run("BYPASSRLS 非 superuser 应拒绝", func(t *testing.T) {
		t.Setenv("WARDEN_PRIVILEGED_DATABASE_URL", "postgres://warden_privileged:testpriv@postgresql:5432/warden")
		if err := AssertRLSRuntime(ctx, adminPool); err == nil {
			t.Error("非 superuser 的 BYPASSRLS 角色作主池应拒绝，却未报错")
		}
	})
}