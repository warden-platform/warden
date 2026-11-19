package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMain prepares the shared test database once for all DB-gated storage
// tests. The control package's tests reset the same schema, so `make
// server-test` runs packages serially (-p 1); this setup additionally makes
// the storage package independent of package execution order.
func TestMain(m *testing.M) {
	if dsn := os.Getenv("WARDEN_TEST_DATABASE_URL"); dsn != "" {
		if err := prepareTestSchema(dsn); err != nil {
			fmt.Fprintf(os.Stderr, "prepare test schema: %v\n", err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func prepareTestSchema(dsn string) error {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		content, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", entry.Name()))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		if _, err := conn.Exec(ctx, string(content)); err != nil {
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}