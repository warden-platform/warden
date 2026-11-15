package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrFederatedTenantNotFound        = errors.New("federated tenant binding not found")
	ErrFederatedTenantConflict        = errors.New("federated tenant binding conflicts with another tenant")
	ErrFederatedIdentityTenant        = errors.New("federated identity belongs to another tenant")
	ErrFederatedAssertionReplay       = errors.New("federated assertion has already been consumed")
	ErrExternalProjectBindingNotFound = errors.New("external project binding not found")
)

// FederatedRepo persists external enterprise mappings, federated users, and
// explicit external-project bindings. It intentionally lives beside the
// existing external-identity repository so the delegation lifecycle remains
// unchanged.
type FederatedRepo struct{ pool *pgxpool.Pool }

func NewFederatedRepo(pool *pgxpool.Pool) *FederatedRepo { return &FederatedRepo{pool: pool} }

func (r *FederatedRepo) BindTenant(ctx context.Context, binding *domain.ExternalTenantBinding) error {
	tenantID := repositoryTenant(ctx)
	if binding.TenantID != "" && binding.TenantID != tenantID {
		return ErrFederatedTenantConflict
	}
	binding.TenantID = tenantID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO external_tenant_bindings
			(tenant_id,source,external_enterprise_id,external_workspace_id,status,created_by)
		VALUES ($1,$2,$3,$4,'ACTIVE',NULLIF($5,'')::uuid)
		ON CONFLICT (source,external_enterprise_id) WHERE status='ACTIVE'
		DO UPDATE SET tenant_id=EXCLUDED.tenant_id,
		              external_workspace_id=EXCLUDED.external_workspace_id,
		              status='ACTIVE',revoked_at=NULL,updated_at=now(),created_by=EXCLUDED.created_by
		WHERE external_tenant_bindings.tenant_id=EXCLUDED.tenant_id
		RETURNING id,created_at,updated_at,COALESCE(created_by::text,''),revoked_at`,
		tenantID, binding.Source, binding.ExternalEnterpriseID, binding.ExternalWorkspaceID, binding.CreatedBy).
		Scan(&binding.ID, &binding.CreatedAt, &binding.UpdatedAt, &binding.CreatedBy, &binding.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The enterprise is already bound to a different tenant. The guarded
		// upsert above deliberately refuses to transfer ownership.
		return ErrFederatedTenantConflict
	}
	return err
}

func (r *FederatedRepo) ResolveTenant(ctx context.Context, source, enterpriseID, workspaceID string) (*domain.ExternalTenantBinding, error) {
	var binding domain.ExternalTenantBinding
	err := r.pool.QueryRow(ctx, `
		SELECT id,tenant_id,source,external_enterprise_id,external_workspace_id,status,
		       COALESCE(created_by::text,''),created_at,updated_at,revoked_at
		FROM external_tenant_bindings
		WHERE source=$1 AND external_enterprise_id=$2 AND external_workspace_id=$3 AND status='ACTIVE'`,
		source, enterpriseID, workspaceID).Scan(
		&binding.ID, &binding.TenantID, &binding.Source, &binding.ExternalEnterpriseID,
		&binding.ExternalWorkspaceID, &binding.Status, &binding.CreatedBy,
		&binding.CreatedAt, &binding.UpdatedAt, &binding.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrFederatedTenantNotFound
	}
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

func (r *FederatedRepo) ListTenants(ctx context.Context) ([]domain.ExternalTenantBinding, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id,tenant_id,source,external_enterprise_id,external_workspace_id,status,
		       COALESCE(created_by::text,''),created_at,updated_at,revoked_at
		FROM external_tenant_bindings WHERE tenant_id=$1 AND status='ACTIVE'
		ORDER BY external_enterprise_id`, repositoryTenant(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ExternalTenantBinding
	for rows.Next() {
		var binding domain.ExternalTenantBinding
		if err := rows.Scan(&binding.ID, &binding.TenantID, &binding.Source, &binding.ExternalEnterpriseID,
			&binding.ExternalWorkspaceID, &binding.Status, &binding.CreatedBy, &binding.CreatedAt,
			&binding.UpdatedAt, &binding.RevokedAt); err != nil {
			return nil, err
		}
		result = append(result, binding)
	}
	return result, rows.Err()
}

func federatedPrincipalName(issuer, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + subject))
	return "federated:" + hex.EncodeToString(sum[:])[:32]
}

// ConsumeAssertion records a jti before provisioning. A duplicate key means
// the assertion was replayed and must not be allowed to mint another session.
func (r *FederatedRepo) ConsumeAssertion(ctx context.Context, issuer, assertionID string, expiresAt any) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO federated_assertion_replays(issuer,assertion_id,expires_at) VALUES($1,$2,$3)`, issuer, assertionID, expiresAt)
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrFederatedAssertionReplay
	}
	return err
}

// ProvisionIdentity creates a passwordless Warden user on first login and
// refreshes its display attributes on subsequent logins. No project
// membership is created here; a Warden tenant/project administrator must assign
// it explicitly.
//
// defaultRole is applied whenever the principal has no role yet. An existing
// role always wins, so a later role change made by an administrator in Warden is
// never silently overwritten by the next IdP login.
func (r *FederatedRepo) ProvisionIdentity(ctx context.Context, issuer, subject, email, displayName, tenantID, defaultRole string, metadata map[string]any) (*domain.Principal, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var existingPrincipalID string
	var existingTenant string
	// Keep the lookup error in its own variable. Reusing err for json.Marshal
	// below would overwrite pgx.ErrNoRows and make every first-time federated
	// login take the "update existing principal" branch with an empty UUID.
	lookupErr := tx.QueryRow(ctx, `SELECT principal_id::text,tenant_id FROM federated_identities WHERE issuer=$1 AND subject=$2 FOR UPDATE`, issuer, subject).Scan(&existingPrincipalID, &existingTenant)
	if lookupErr == nil && existingTenant != tenantID {
		return nil, ErrFederatedIdentityTenant
	}
	if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
		return nil, lookupErr
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	// Passing JSON as text lets the SQL expressions below cast it explicitly;
	// pgx would otherwise encode []byte as bytea, which is not castable here.
	metadataText := string(metadataJSON)
	const returning = `RETURNING id::text,kind,name,status,tenant_id,metadata,COALESCE(password_hash,''),last_login_at,created_at,updated_at`

	var principal domain.Principal
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		name := federatedPrincipalName(issuer, subject)
		err = tx.QueryRow(ctx, `
			INSERT INTO principals(kind,name,status,tenant_id,metadata)
			VALUES('user',$1,'ACTIVE',$2,
				jsonb_strip_nulls($3::jsonb || jsonb_build_object('role', NULLIF($4::text,''))))
			`+returning,
			name, tenantID, metadataText, defaultRole).Scan(&principal.ID, &principal.Kind, &principal.Name, &principal.Status,
			&principal.TenantID, &principal.Metadata, &principal.PasswordHash, &principal.LastLoginAt,
			&principal.CreatedAt, &principal.UpdatedAt)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO federated_identities(tenant_id,issuer,subject,principal_id,email,display_name,last_login_at) VALUES($1,$2,$3,$4::uuid,NULLIF($5,''),NULLIF($6,''),now())`, tenantID, issuer, subject, principal.ID, email, displayName)
	} else {
		// Preserve an administrator-assigned role across IdP logins. Every
		// other metadata field (display_name, email, avatar) is refreshed from
		// the latest id_token.
		err = tx.QueryRow(ctx, `
			UPDATE principals SET status='ACTIVE',
				metadata = jsonb_strip_nulls($2::jsonb || jsonb_build_object('role',
					COALESCE(NULLIF(principals.metadata->>'role',''), NULLIF($4::text,'')))),
				updated_at=now()
			WHERE id=$1::uuid AND tenant_id=$3
			`+returning,
			existingPrincipalID, metadataText, tenantID, defaultRole).Scan(&principal.ID, &principal.Kind, &principal.Name, &principal.Status,
			&principal.TenantID, &principal.Metadata, &principal.PasswordHash, &principal.LastLoginAt,
			&principal.CreatedAt, &principal.UpdatedAt)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE federated_identities SET email=NULLIF($3,''),display_name=NULLIF($4,''),status='ACTIVE',last_login_at=now(),updated_at=now() WHERE issuer=$1 AND subject=$2`, issuer, subject, email, displayName)
		}
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &principal, nil
}

func (r *FederatedRepo) BindProject(ctx context.Context, binding *domain.ExternalProjectBinding) error {
	tenantID := repositoryTenant(ctx)
	if binding.TenantID != "" && binding.TenantID != tenantID {
		return ErrFederatedTenantConflict
	}
	binding.TenantID = tenantID
	return r.pool.QueryRow(ctx, `
		INSERT INTO external_project_bindings
			(tenant_id,source,external_workspace_id,external_project_id,external_project_name,project_id,status,created_by)
		SELECT $1,$2,$3,$4,NULLIF($5,''),$6::uuid,'ACTIVE',NULLIF($7,'')::uuid
		WHERE EXISTS (SELECT 1 FROM projects WHERE id=$6::uuid AND tenant_id=$1)
		  AND EXISTS (SELECT 1 FROM external_tenant_bindings
		              WHERE tenant_id=$1 AND source=$2 AND external_workspace_id=$3 AND status='ACTIVE')
		ON CONFLICT (tenant_id,source,external_workspace_id,external_project_id) WHERE status='ACTIVE'
		DO UPDATE SET external_project_name=EXCLUDED.external_project_name,project_id=EXCLUDED.project_id,
		              status='ACTIVE',revoked_at=NULL,updated_at=now(),created_by=EXCLUDED.created_by
		RETURNING id,created_at,updated_at,COALESCE(created_by::text,''),revoked_at`,
		tenantID, binding.Source, binding.ExternalWorkspaceID, binding.ExternalProjectID,
		binding.ExternalProjectName, binding.ProjectID, binding.CreatedBy).
		Scan(&binding.ID, &binding.CreatedAt, &binding.UpdatedAt, &binding.CreatedBy, &binding.RevokedAt)
}

func (r *FederatedRepo) ResolveProject(ctx context.Context, source, workspaceID, externalProjectID string) (*domain.ExternalProjectBinding, error) {
	var binding domain.ExternalProjectBinding
	err := r.pool.QueryRow(ctx, `
		SELECT id,tenant_id,source,external_workspace_id,external_project_id,COALESCE(external_project_name,''),project_id::text,status,COALESCE(created_by::text,''),created_at,updated_at,revoked_at
		FROM external_project_bindings WHERE tenant_id=$1 AND source=$2 AND external_workspace_id=$3 AND external_project_id=$4 AND status='ACTIVE'`,
		repositoryTenant(ctx), source, workspaceID, externalProjectID).Scan(
		&binding.ID, &binding.TenantID, &binding.Source, &binding.ExternalWorkspaceID,
		&binding.ExternalProjectID, &binding.ExternalProjectName, &binding.ProjectID,
		&binding.Status, &binding.CreatedBy, &binding.CreatedAt, &binding.UpdatedAt, &binding.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrExternalProjectBindingNotFound
	}
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

func (r *FederatedRepo) ListProjects(ctx context.Context) ([]domain.ExternalProjectBinding, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id,tenant_id,source,external_workspace_id,external_project_id,COALESCE(external_project_name,''),project_id::text,status,COALESCE(created_by::text,''),created_at,updated_at,revoked_at
		FROM external_project_bindings WHERE tenant_id=$1 AND status='ACTIVE' ORDER BY external_workspace_id,external_project_name,external_project_id`, repositoryTenant(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ExternalProjectBinding
	for rows.Next() {
		var binding domain.ExternalProjectBinding
		if err := rows.Scan(&binding.ID, &binding.TenantID, &binding.Source, &binding.ExternalWorkspaceID,
			&binding.ExternalProjectID, &binding.ExternalProjectName, &binding.ProjectID,
			&binding.Status, &binding.CreatedBy, &binding.CreatedAt, &binding.UpdatedAt, &binding.RevokedAt); err != nil {
			return nil, err
		}
		result = append(result, binding)
	}
	return result, rows.Err()
}

func (r *FederatedRepo) RevokeProject(ctx context.Context, id string) (bool, error) {
	result, err := r.pool.Exec(ctx, `UPDATE external_project_bindings SET status='REVOKED',revoked_at=now(),updated_at=now() WHERE id=$1 AND tenant_id=$2 AND status='ACTIVE'`, id, repositoryTenant(ctx))
	return result.RowsAffected() > 0, err
}

func (r *FederatedRepo) CleanupReplays(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM federated_assertion_replays WHERE expires_at < now()`)
	return err
}

func (r *FederatedRepo) IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
