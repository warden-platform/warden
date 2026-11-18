package postgres

import (
	"context"
	"errors"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProjectMembershipRepo persists the tenant-scoped project RBAC relationship.
// Every query carries tenant_id, including the foreign-key-safe writes, so a
// caller can never use a project or principal from another tenant.
type ProjectMembershipRepo struct{ pool *pgxpool.Pool }

func NewProjectMembershipRepo(pool *pgxpool.Pool) *ProjectMembershipRepo {
	return &ProjectMembershipRepo{pool: pool}
}

func (r *ProjectMembershipRepo) List(ctx context.Context, projectID string) ([]domain.ProjectMembership, error) {
	tenantID := projectTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT m.tenant_id,m.project_id::text,p.name,m.principal_id::text,pr.name,pr.kind,m.role,m.status,COALESCE(m.created_by::text,''),m.created_at,m.updated_at
		FROM project_memberships m JOIN projects p ON p.id=m.project_id AND p.tenant_id=m.tenant_id
		JOIN principals pr ON pr.id=m.principal_id AND pr.tenant_id=m.tenant_id
		WHERE m.tenant_id=$1 AND m.project_id=$2 ORDER BY pr.name`, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ProjectMembership
	for rows.Next() {
		var m domain.ProjectMembership
		if err := rows.Scan(&m.TenantID, &m.ProjectID, &m.ProjectName, &m.PrincipalID, &m.PrincipalName, &m.PrincipalKind, &m.Role, &m.Status, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *ProjectMembershipRepo) Candidates(ctx context.Context, projectID string) ([]domain.Principal, error) {
	tenantID := projectTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT pr.id,pr.kind,pr.name,pr.status,pr.tenant_id,pr.metadata,pr.created_at,pr.updated_at
		FROM principals pr WHERE pr.tenant_id=$1 AND pr.status='ACTIVE'
		AND NOT EXISTS (SELECT 1 FROM project_memberships m WHERE m.tenant_id=$1 AND m.project_id=$2 AND m.principal_id=pr.id AND m.status='ACTIVE')
		ORDER BY pr.name`, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Principal
	for rows.Next() {
		var p domain.Principal
		if err := rows.Scan(&p.ID, &p.Kind, &p.Name, &p.Status, &p.TenantID, &p.Metadata, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *ProjectMembershipRepo) Upsert(ctx context.Context, projectID, principalID string, role domain.ProjectRole, status domain.ProjectMembershipStatus, createdBy string) (*domain.ProjectMembership, error) {
	tenantID := projectTenant(ctx)
	var m domain.ProjectMembership
	err := r.pool.QueryRow(ctx, `INSERT INTO project_memberships(tenant_id,project_id,principal_id,role,status,created_by)
		SELECT $1::varchar,$2::uuid,$3::uuid,$4::varchar,$5::varchar,nullif($6::text,'')::uuid
		WHERE EXISTS (SELECT 1 FROM projects WHERE id=$2::uuid AND tenant_id=$1::varchar)
		  AND EXISTS (SELECT 1 FROM principals WHERE id=$3::uuid AND tenant_id=$1::varchar)
		ON CONFLICT (project_id,principal_id) DO UPDATE SET role=EXCLUDED.role,status=EXCLUDED.status,created_by=COALESCE(EXCLUDED.created_by,project_memberships.created_by),updated_at=now()
		RETURNING tenant_id,project_id::text,(SELECT name FROM projects WHERE id=project_memberships.project_id AND tenant_id=project_memberships.tenant_id),principal_id::text,(SELECT name FROM principals WHERE id=project_memberships.principal_id AND tenant_id=project_memberships.tenant_id),(SELECT kind FROM principals WHERE id=project_memberships.principal_id AND tenant_id=project_memberships.tenant_id),role,status,COALESCE(created_by::text,''),created_at,updated_at`, tenantID, projectID, principalID, role, status, createdBy).
		Scan(&m.TenantID, &m.ProjectID, &m.ProjectName, &m.PrincipalID, &m.PrincipalName, &m.PrincipalKind, &m.Role, &m.Status, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidProjectMembership
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

var ErrInvalidProjectMembership = errors.New("project membership project or principal is invalid")

func (r *ProjectMembershipRepo) Disable(ctx context.Context, projectID, principalID string) (bool, error) {
	tenantID := projectTenant(ctx)
	command, err := r.pool.Exec(ctx, `UPDATE project_memberships SET status='DISABLED',updated_at=now() WHERE tenant_id=$1 AND project_id=$2 AND principal_id=$3`, tenantID, projectID, principalID)
	return command.RowsAffected() > 0, err
}

func (r *ProjectMembershipRepo) Role(ctx context.Context, projectID, principalID string) (domain.ProjectRole, domain.ProjectMembershipStatus, error) {
	tenantID := projectTenant(ctx)
	var role domain.ProjectRole
	var status domain.ProjectMembershipStatus
	err := r.pool.QueryRow(ctx, `SELECT role,status FROM project_memberships WHERE tenant_id=$1 AND project_id=$2 AND principal_id=$3`, tenantID, projectID, principalID).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	return role, status, err
}

func (r *ProjectMembershipRepo) ProjectIDs(ctx context.Context, principalID string) ([]string, error) {
	tenantID := projectTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT project_id::text FROM project_memberships WHERE tenant_id=$1 AND principal_id=$2 AND status='ACTIVE' ORDER BY project_id`, tenantID, principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RolesForPrincipal maps project ID to the principal's active membership role,
// used by the console to explain "what can I do in each project".
func (r *ProjectMembershipRepo) RolesForPrincipal(ctx context.Context, principalID string) (map[string]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT project_id::text, role FROM project_memberships
		WHERE tenant_id=$1 AND principal_id=$2 AND status='ACTIVE'`, projectTenant(ctx), principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var projectID, role string
		if err := rows.Scan(&projectID, &role); err != nil {
			return nil, err
		}
		out[projectID] = role
	}
	return out, rows.Err()
}

func (r *ProjectMembershipRepo) ListForPrincipal(ctx context.Context, principalID string) ([]domain.Project, error) {
	tenantID := projectTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT p.id::text,p.name,p.description,p.is_default,p.tenant_id,p.created_at,p.updated_at
		FROM projects p JOIN project_memberships m ON m.project_id=p.id AND m.tenant_id=p.tenant_id
		WHERE p.tenant_id=$1 AND m.principal_id=$2 AND m.status='ACTIVE' ORDER BY p.is_default DESC,p.name`, tenantID, principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Project
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.IsDefault, &p.TenantID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
