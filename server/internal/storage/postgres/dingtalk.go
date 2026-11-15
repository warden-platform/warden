package postgres

import (
	"context"
	"errors"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DingTalkRepo struct{ pool *pgxpool.Pool }

func NewDingTalkRepo(pool *pgxpool.Pool) *DingTalkRepo { return &DingTalkRepo{pool: pool} }

func (r *DingTalkRepo) ListBindings(ctx context.Context) ([]domain.DingTalkPrincipalBinding, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT b.user_id,b.principal_id,b.display_name,p.name,b.created_at,b.updated_at
		FROM dingtalk_principal_bindings b JOIN principals p ON p.id=b.principal_id
		WHERE b.tenant_id=$1 ORDER BY b.created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.DingTalkPrincipalBinding
	for rows.Next() {
		var item domain.DingTalkPrincipalBinding
		if err := rows.Scan(&item.UserID, &item.PrincipalID, &item.DisplayName, &item.PrincipalName, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *DingTalkRepo) GetBinding(ctx context.Context, userID string) (*domain.DingTalkPrincipalBinding, error) {
	tenantID := repositoryTenant(ctx)
	var item domain.DingTalkPrincipalBinding
	err := r.pool.QueryRow(ctx, `SELECT b.user_id,b.principal_id,b.display_name,p.name,b.created_at,b.updated_at
		FROM dingtalk_principal_bindings b JOIN principals p ON p.id=b.principal_id
		WHERE b.user_id=$1 AND b.tenant_id=$2`, userID, tenantID).
		Scan(&item.UserID, &item.PrincipalID, &item.DisplayName, &item.PrincipalName, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &item, err
}

func (r *DingTalkRepo) UpsertBinding(ctx context.Context, binding *domain.DingTalkPrincipalBinding) error {
	tenantID := repositoryTenant(ctx)
	return r.pool.QueryRow(ctx, `INSERT INTO dingtalk_principal_bindings(user_id,principal_id,display_name,tenant_id)
		VALUES($1,$2,$3,$4) ON CONFLICT(user_id) DO UPDATE SET principal_id=EXCLUDED.principal_id,
		display_name=EXCLUDED.display_name,updated_at=now()
		RETURNING created_at,updated_at`, binding.UserID, binding.PrincipalID, binding.DisplayName, tenantID).
		Scan(&binding.CreatedAt, &binding.UpdatedAt)
}

func (r *DingTalkRepo) DeleteBinding(ctx context.Context, userID string) (bool, error) {
	tenantID := repositoryTenant(ctx)
	result, err := r.pool.Exec(ctx, `DELETE FROM dingtalk_principal_bindings WHERE user_id=$1 AND tenant_id=$2`, userID, tenantID)
	return err == nil && result.RowsAffected() > 0, err
}
