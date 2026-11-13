package postgres

import (
	"context"
	"errors"

	"gitee.com/pam-platform/pam/server/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrProjectUserNotFound = errors.New("project user not found")

type ProjectUserRepo struct {
	pool *pgxpool.Pool
}

func NewProjectUserRepo(pool *pgxpool.Pool) *ProjectUserRepo {
	return &ProjectUserRepo{pool: pool}
}

func (r *ProjectUserRepo) List(ctx context.Context) ([]domain.ProjectUser, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT id,name,status,tenant_id,last_login_at,created_at,updated_at
		FROM principals
		WHERE tenant_id=$1 AND kind='user' AND metadata->>'role'='project_user'
		ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]domain.ProjectUser, 0)
	for rows.Next() {
		var user domain.ProjectUser
		if err := rows.Scan(&user.ID, &user.Name, &user.Status, &user.TenantID, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, err
		}
		user.Memberships = []domain.ProjectMembership{}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := r.attachMemberships(ctx, users); err != nil {
		return nil, err
	}
	return users, nil
}

func (r *ProjectUserRepo) GetByID(ctx context.Context, id string) (*domain.ProjectUser, error) {
	tenantID := repositoryTenant(ctx)
	var user domain.ProjectUser
	err := r.pool.QueryRow(ctx, `SELECT id,name,status,tenant_id,last_login_at,created_at,updated_at
		FROM principals
		WHERE tenant_id=$1 AND id=$2 AND kind='user' AND metadata->>'role'='project_user'`, tenantID, id).
		Scan(&user.ID, &user.Name, &user.Status, &user.TenantID, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	users := []domain.ProjectUser{user}
	users[0].Memberships = []domain.ProjectMembership{}
	if err := r.attachMemberships(ctx, users); err != nil {
		return nil, err
	}
	return &users[0], nil
}

func (r *ProjectUserRepo) attachMemberships(ctx context.Context, users []domain.ProjectUser) error {
	if len(users) == 0 {
		return nil
	}
	byID := make(map[string]*domain.ProjectUser, len(users))
	ids := make([]string, 0, len(users))
	for i := range users {
		byID[users[i].ID] = &users[i]
		ids = append(ids, users[i].ID)
	}
	rows, err := r.pool.Query(ctx, `SELECT pm.tenant_id,pm.project_id,p.name,pm.principal_id,pr.name,pr.kind,
		pm.role,pm.status,COALESCE(pm.created_by::text,''),pm.created_at,pm.updated_at
		FROM project_memberships pm
		JOIN projects p ON p.id=pm.project_id AND p.tenant_id=pm.tenant_id
		JOIN principals pr ON pr.id=pm.principal_id AND pr.tenant_id=pm.tenant_id
		WHERE pm.tenant_id=$1 AND pm.principal_id=ANY($2::uuid[])
		ORDER BY p.name,pm.created_at`, repositoryTenant(ctx), ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var membership domain.ProjectMembership
		if err := rows.Scan(&membership.TenantID, &membership.ProjectID, &membership.ProjectName,
			&membership.PrincipalID, &membership.PrincipalName, &membership.PrincipalKind,
			&membership.Role, &membership.Status, &membership.CreatedBy,
			&membership.CreatedAt, &membership.UpdatedAt); err != nil {
			return err
		}
		if user := byID[membership.PrincipalID]; user != nil {
			user.Memberships = append(user.Memberships, membership)
		}
	}
	return rows.Err()
}

func (r *ProjectUserRepo) UpdateStatusTx(ctx context.Context, tx pgx.Tx, id string, status domain.PrincipalStatus) (int64, error) {
	tenantID := repositoryTenant(ctx)
	command, err := tx.Exec(ctx, `UPDATE principals SET status=$3,updated_at=now()
		WHERE tenant_id=$1 AND id=$2 AND kind='user' AND metadata->>'role'='project_user'`, tenantID, id, status)
	if err != nil {
		return 0, err
	}
	if command.RowsAffected() != 1 {
		return 0, ErrProjectUserNotFound
	}
	if status != domain.PrincipalStatusDisabled {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE delegation_tokens SET revoked_at=now()
		WHERE tenant_id=$1 AND revoked_at IS NULL
		AND (on_behalf_of_principal_id=$2 OR accountable_principal_id=$2)`, tenantID, id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE external_identity_bindings SET status='REVOKED',revoked_at=now(),updated_at=now()
		WHERE tenant_id=$1 AND principal_id=$2 AND identity_type='USER' AND status='ACTIVE'`, tenantID, id); err != nil {
		return 0, err
	}
	return revokeProjectUserSessions(ctx, tx, tenantID, id)
}

func (r *ProjectUserRepo) UpdatePasswordTx(ctx context.Context, tx pgx.Tx, id, passwordHash string) (int64, error) {
	tenantID := repositoryTenant(ctx)
	command, err := tx.Exec(ctx, `UPDATE principals SET password_hash=$3,updated_at=now()
		WHERE tenant_id=$1 AND id=$2 AND kind='user' AND metadata->>'role'='project_user'`, tenantID, id, passwordHash)
	if err != nil {
		return 0, err
	}
	if command.RowsAffected() != 1 {
		return 0, ErrProjectUserNotFound
	}
	return revokeProjectUserSessions(ctx, tx, tenantID, id)
}

func (r *ProjectUserRepo) RevokeSessionsTx(ctx context.Context, tx pgx.Tx, id string) (int64, error) {
	tenantID := repositoryTenant(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM principals
		WHERE tenant_id=$1 AND id=$2 AND kind='user' AND metadata->>'role'='project_user')`, tenantID, id).Scan(&exists); err != nil {
		return 0, err
	}
	if !exists {
		return 0, ErrProjectUserNotFound
	}
	return revokeProjectUserSessions(ctx, tx, tenantID, id)
}

func (r *ProjectUserRepo) UpdateOwnPasswordTx(ctx context.Context, tx pgx.Tx, id, currentHash, passwordHash string) (int64, error) {
	tenantID := repositoryTenant(ctx)
	command, err := tx.Exec(ctx, `UPDATE principals SET password_hash=$4,updated_at=now()
		WHERE tenant_id=$1 AND id=$2 AND kind='user' AND password_hash=$3`, tenantID, id, currentHash, passwordHash)
	if err != nil {
		return 0, err
	}
	if command.RowsAffected() != 1 {
		return 0, ErrProjectUserNotFound
	}
	return revokeProjectUserSessions(ctx, tx, tenantID, id)
}

func revokeProjectUserSessions(ctx context.Context, tx pgx.Tx, tenantID, principalID string) (int64, error) {
	command, err := tx.Exec(ctx, `UPDATE admin_sessions s SET revoked_at=now()
		FROM principals p
		WHERE s.principal_id=p.id AND p.tenant_id=$1 AND p.id=$2 AND s.revoked_at IS NULL`, tenantID, principalID)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}
