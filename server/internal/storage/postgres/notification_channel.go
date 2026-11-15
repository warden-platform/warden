package postgres

import (
	"context"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type NotificationChannelRepo struct {
	pool *pgxpool.Pool
}

func NewNotificationChannelRepo(pool *pgxpool.Pool) *NotificationChannelRepo {
	return &NotificationChannelRepo{pool: pool}
}

func (r *NotificationChannelRepo) Get(ctx context.Context, channelType string) (*domain.NotificationChannel, error) {
	tenantID := repositoryTenant(ctx)
	var channel domain.NotificationChannel
	err := r.pool.QueryRow(ctx, `SELECT id, type, name, enabled, events, tenant_id, created_at, updated_at
		FROM notification_channels WHERE type=$1 AND tenant_id=$2`, channelType, tenantID).
		Scan(&channel.ID, &channel.Type, &channel.Name, &channel.Enabled, &channel.Events,
			&channel.TenantID, &channel.CreatedAt, &channel.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &channel, nil
}

func (r *NotificationChannelRepo) Upsert(ctx context.Context, channel *domain.NotificationChannel) error {
	tenantID := repositoryTenant(ctx)
	return r.pool.QueryRow(ctx, `INSERT INTO notification_channels (type, name, enabled, events, tenant_id)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (type, tenant_id) DO UPDATE SET name=EXCLUDED.name, enabled=EXCLUDED.enabled,
			events=EXCLUDED.events, updated_at=now()
		RETURNING id, tenant_id, created_at, updated_at`, channel.Type, channel.Name, channel.Enabled, channel.Events, tenantID).
		Scan(&channel.ID, &channel.TenantID, &channel.CreatedAt, &channel.UpdatedAt)
}
