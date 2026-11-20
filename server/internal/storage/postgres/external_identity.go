package postgres

import (
	"context"
	"fmt"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ExternalIdentityRepo struct {
	pool *pgxpool.Pool
}

func NewExternalIdentityRepo(pool *pgxpool.Pool) *ExternalIdentityRepo {
	return &ExternalIdentityRepo{pool: pool}
}

func (r *ExternalIdentityRepo) Create(ctx context.Context, binding *domain.ExternalIdentityBinding) error {
	return r.pool.QueryRow(ctx, `INSERT INTO external_identity_bindings
		(tenant_id,source,workspace_id,external_project_id,identity_type,external_id,principal_id,project_id,status,created_by)
		VALUES ($1,$2,$3,nullif($4,''),$5,$6,$7,nullif($8,'')::uuid,$9,nullif($10,'')::uuid)
		RETURNING id,created_at,updated_at`, repositoryTenant(ctx), binding.Source, binding.WorkspaceID,
		binding.ExternalProjectID, binding.IdentityType, binding.ExternalID, binding.PrincipalID,
		binding.ProjectID, binding.Status, binding.CreatedBy).
		Scan(&binding.ID, &binding.CreatedAt, &binding.UpdatedAt)
}

func scanExternalBinding(row pgx.Row) (*domain.ExternalIdentityBinding, error) {
	var binding domain.ExternalIdentityBinding
	err := row.Scan(&binding.ID, &binding.TenantID, &binding.Source, &binding.WorkspaceID,
		&binding.ExternalProjectID, &binding.IdentityType, &binding.ExternalID, &binding.PrincipalID,
		&binding.ProjectID, &binding.Status, &binding.CreatedBy, &binding.CreatedAt,
		&binding.UpdatedAt, &binding.RevokedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

const externalBindingColumns = `id,tenant_id,source,workspace_id,COALESCE(external_project_id,''),identity_type,external_id,principal_id,COALESCE(project_id::text,''),status,COALESCE(created_by::text,''),created_at,updated_at,revoked_at`

func (r *ExternalIdentityRepo) GetAgent(ctx context.Context, source, workspaceID, externalProjectID, externalID string) (*domain.ExternalIdentityBinding, error) {
	return scanExternalBinding(r.pool.QueryRow(ctx, `SELECT `+externalBindingColumns+` FROM external_identity_bindings
		WHERE tenant_id=$1 AND source=$2 AND workspace_id=$3 AND external_project_id IS NOT DISTINCT FROM nullif($4,'')
		AND identity_type='AGENT' AND external_id=$5 AND status='ACTIVE' LIMIT 1`, repositoryTenant(ctx), source, workspaceID, externalProjectID, externalID))
}

func (r *ExternalIdentityRepo) GetUser(ctx context.Context, source, workspaceID, externalProjectID, externalID string) (*domain.ExternalIdentityBinding, error) {
	return scanExternalBinding(r.pool.QueryRow(ctx, `SELECT `+externalBindingColumns+` FROM external_identity_bindings
		WHERE tenant_id=$1 AND source=$2 AND workspace_id=$3
		AND (external_project_id IS NULL OR external_project_id IS NOT DISTINCT FROM nullif($4,''))
		AND identity_type='USER' AND external_id=$5 AND status='ACTIVE' LIMIT 1`, repositoryTenant(ctx), source, workspaceID, externalProjectID, externalID))
}

func (r *ExternalIdentityRepo) List(ctx context.Context) ([]domain.ExternalIdentityBinding, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+externalBindingColumns+` FROM external_identity_bindings
		WHERE tenant_id=$1 AND status='ACTIVE' ORDER BY created_at DESC`, repositoryTenant(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ExternalIdentityBinding
	for rows.Next() {
		var binding domain.ExternalIdentityBinding
		if err := rows.Scan(&binding.ID, &binding.TenantID, &binding.Source, &binding.WorkspaceID,
			&binding.ExternalProjectID, &binding.IdentityType, &binding.ExternalID, &binding.PrincipalID,
			&binding.ProjectID, &binding.Status, &binding.CreatedBy, &binding.CreatedAt,
			&binding.UpdatedAt, &binding.RevokedAt); err != nil {
			return nil, err
		}
		result = append(result, binding)
	}
	return result, rows.Err()
}

func (r *ExternalIdentityRepo) Revoke(ctx context.Context, id, actorID string) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE external_identity_bindings
		SET status='REVOKED',revoked_at=now(),updated_at=now()
		WHERE id=$1 AND tenant_id=$2 AND status='ACTIVE'`, id, repositoryTenant(ctx))
	if err != nil {
		return false, err
	}
	if result.RowsAffected() != 1 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE delegation_tokens SET revoked_at=now()
		WHERE tenant_id=$1 AND revoked_at IS NULL AND EXISTS (
			SELECT 1 FROM external_identity_bindings b
			WHERE b.id=$2 AND b.tenant_id=$1
			AND b.source=external_context->>'source'
			AND b.workspace_id=external_context->>'workspaceId'
			AND (b.identity_type='AGENT' AND b.external_id=external_context->>'agentId'
				AND external_context->>'projectId' IS NOT DISTINCT FROM nullif(b.external_project_id,'')
				OR b.identity_type='USER' AND b.external_id IN (external_context->>'originatorUserId', external_context->>'accountableUserId')
				AND (b.external_project_id IS NULL OR b.external_project_id=external_context->>'projectId'))
		)`, repositoryTenant(ctx), id); err != nil {
		return false, err
	}
	_ = actorID
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// RevokeWorkspace disables every active external binding and delegation token
// issued for a workspace. It is used when the external platform deletes a
// workspace or when an operator needs an emergency workspace-wide cutoff.
func (r *ExternalIdentityRepo) RevokeWorkspace(ctx context.Context, source, workspaceID string) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var revoked int64
	if err := tx.QueryRow(ctx, `WITH changed AS (
		UPDATE external_identity_bindings SET status='REVOKED',revoked_at=now(),updated_at=now()
		WHERE tenant_id=$1 AND source=$2 AND workspace_id=$3 AND status='ACTIVE'
		RETURNING id
	) SELECT count(*) FROM changed`, repositoryTenant(ctx), source, workspaceID).Scan(&revoked); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE delegation_tokens SET revoked_at=now()
		WHERE tenant_id=$1 AND revoked_at IS NULL
		AND external_context->>'source'=$2 AND external_context->>'workspaceId'=$3`, repositoryTenant(ctx), source, workspaceID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return revoked, nil
}

type DelegationTokenRepo struct {
	pool      *pgxpool.Pool
	adminPool *pgxpool.Pool
}

func NewDelegationTokenRepo(pool *pgxpool.Pool) *DelegationTokenRepo {
	return &DelegationTokenRepo{pool: pool}
}

// SetAdminPool 配置旁路池（BYPASSRLS）；未设回退主池。
func (r *DelegationTokenRepo) SetAdminPool(p *pgxpool.Pool) { r.adminPool = p }
func (r *DelegationTokenRepo) admin() *pgxpool.Pool {
	if r.adminPool != nil {
		return r.adminPool
	}
	return r.pool
}

func (r *DelegationTokenRepo) Create(ctx context.Context, token *domain.DelegationToken) error {
	return r.pool.QueryRow(ctx, `INSERT INTO delegation_tokens
		(id,tenant_id,parent_credential_id,principal_id,on_behalf_of_principal_id,accountable_principal_id,
		token_hash,scopes,external_context,acting_project_id,issued_at,expires_at)
		VALUES ($1,$2,$3,$4,nullif($5,'')::uuid,nullif($6,'')::uuid,$7,$8,$9,nullif($10,'')::uuid,$11,$12)
		RETURNING id`, token.ID, repositoryTenant(ctx), token.ParentCredentialID, token.PrincipalID,
		token.OnBehalfOfPrincipalID, token.AccountablePrincipalID, token.TokenHash,
		token.Scopes, token.ExternalContext, token.ActingProjectID, token.IssuedAt, token.ExpiresAt).Scan(&token.ID)
}

func (r *DelegationTokenRepo) GetForAuthentication(ctx context.Context, id string) (*domain.DelegationToken, *domain.Principal, error) {
	row := r.admin().QueryRow(ctx, `SELECT t.id,t.tenant_id,t.parent_credential_id,t.principal_id,
		COALESCE(t.on_behalf_of_principal_id::text,''),COALESCE(t.accountable_principal_id::text,''),t.token_hash,t.scopes,
		t.external_context,COALESCE(t.acting_project_id::text,''),t.issued_at,t.expires_at,t.last_used_at,t.revoked_at,
		p.id,p.kind,p.name,p.status,p.tenant_id,p.metadata,p.created_at,p.updated_at
		FROM delegation_tokens t JOIN principals p ON p.id=t.principal_id AND p.tenant_id=t.tenant_id
		JOIN principal_api_keys k ON k.id=t.parent_credential_id AND k.principal_id=t.principal_id
		WHERE t.id=$1 AND t.revoked_at IS NULL AND t.expires_at>now()
		AND p.status='ACTIVE' AND k.status='ACTIVE' AND (k.expires_at IS NULL OR k.expires_at>now())`, id)
	var token domain.DelegationToken
	var principal domain.Principal
	err := row.Scan(&token.ID, &token.TenantID, &token.ParentCredentialID, &token.PrincipalID,
		&token.OnBehalfOfPrincipalID, &token.AccountablePrincipalID, &token.TokenHash,
		&token.Scopes, &token.ExternalContext, &token.ActingProjectID, &token.IssuedAt, &token.ExpiresAt,
		&token.LastUsedAt, &token.RevokedAt, &principal.ID, &principal.Kind, &principal.Name, &principal.Status,
		&principal.TenantID, &principal.Metadata, &principal.CreatedAt, &principal.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &token, &principal, nil
}

func (r *DelegationTokenRepo) MarkUsed(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE delegation_tokens SET last_used_at=now()
		WHERE id=$1 AND tenant_id=$2 AND revoked_at IS NULL`, id, repositoryTenant(ctx))
	return err
}

func (r *DelegationTokenRepo) RevokeByBinding(ctx context.Context, source, workspaceID, externalProjectID, externalID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE delegation_tokens SET revoked_at=now()
		WHERE tenant_id=$1 AND revoked_at IS NULL AND external_context->>'source'=$2
		AND external_context->>'workspaceId'=$3 AND external_context->>'agentId'=$4
		AND external_context->>'projectId' IS NOT DISTINCT FROM nullif($5,'')`, repositoryTenant(ctx), source, workspaceID, externalID, externalProjectID)
	return err
}

func (r *DelegationTokenRepo) Revoke(ctx context.Context, id string) error {
	command, err := r.pool.Exec(ctx, `UPDATE delegation_tokens SET revoked_at=now()
		WHERE id=$1 AND tenant_id=$2 AND revoked_at IS NULL`, id, repositoryTenant(ctx))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("delegation token %s is not active", id)
	}
	return nil
}
