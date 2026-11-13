package postgres

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errTenantContextMissing = errors.New("tenant context is missing")

type OutboxRepo struct {
	pool    *pgxpool.Pool
	enabled bool
}

func (r *OutboxRepo) PublishTx(ctx context.Context, tx pgx.Tx, eventType, aggregateType, aggregateID string, payload map[string]any) error {
	if !r.enabled {
		return nil
	}
	tenantID := repositoryTenant(ctx)
	if tenantID == "" {
		return errTenantContextMissing
	}
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events (tenant_id, event_type, aggregate_type, aggregate_id, payload) VALUES ($1,$2,$3,$4,$5)`, tenantID, eventType, aggregateType, aggregateID, payload)
	return err
}

func NewOutboxRepo(pool *pgxpool.Pool) *OutboxRepo {
	return &OutboxRepo{pool: pool, enabled: strings.EqualFold(strings.TrimSpace(os.Getenv("PAM_OUTBOX_ENABLED")), "true")}
}

func (r *OutboxRepo) Publish(ctx context.Context, eventType, aggregateType, aggregateID string, payload map[string]any) error {
	if !r.enabled {
		return nil
	}
	tenantID := repositoryTenant(ctx)
	if tenantID == "" {
		return errTenantContextMissing
	}
	query := `INSERT INTO outbox_events (tenant_id, event_type, aggregate_type, aggregate_id, payload)
		VALUES ($1, $2, $3, $4, $5)`
	_, err := r.pool.Exec(ctx, query, tenantID, eventType, aggregateType, aggregateID, payload)
	return err
}
