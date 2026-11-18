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
	err := r.pool.QueryRow(ctx, `SELECT id, type, name, enabled, events, tenant_id, COALESCE(open_conversation_id,''), created_at, updated_at
		FROM notification_channels WHERE type=$1 AND tenant_id=$2`, channelType, tenantID).
		Scan(&channel.ID, &channel.Type, &channel.Name, &channel.Enabled, &channel.Events,
			&channel.TenantID, &channel.OpenConversationID, &channel.CreatedAt, &channel.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &channel, nil
}

// GetByConversation resolves the tenant that bound a DingTalk group. It runs
// before any tenant context exists (coolapp login), so it deliberately reads
// across tenants; the partial unique index guarantees at most one match.
func (r *NotificationChannelRepo) GetByConversation(ctx context.Context, channelType, conversationID string) (*domain.NotificationChannel, error) {
	var channel domain.NotificationChannel
	err := r.pool.QueryRow(ctx, `SELECT id, type, name, enabled, events, tenant_id, COALESCE(open_conversation_id,''), created_at, updated_at
		FROM notification_channels WHERE type=$1 AND open_conversation_id=$2`, channelType, conversationID).
		Scan(&channel.ID, &channel.Type, &channel.Name, &channel.Enabled, &channel.Events,
			&channel.TenantID, &channel.OpenConversationID, &channel.CreatedAt, &channel.UpdatedAt)
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
	err := r.pool.QueryRow(ctx, `INSERT INTO notification_channels (type, name, enabled, events, tenant_id, open_conversation_id)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''))
		ON CONFLICT (type, tenant_id) DO UPDATE SET name=EXCLUDED.name, enabled=EXCLUDED.enabled,
			events=EXCLUDED.events, open_conversation_id=EXCLUDED.open_conversation_id, updated_at=now()
		RETURNING id, tenant_id, COALESCE(open_conversation_id,''), created_at, updated_at`,
		channel.Type, channel.Name, channel.Enabled, channel.Events, tenantID, channel.OpenConversationID).
		Scan(&channel.ID, &channel.TenantID, &channel.OpenConversationID, &channel.CreatedAt, &channel.UpdatedAt)
	return err
}
