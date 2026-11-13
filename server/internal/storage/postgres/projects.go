package postgres

import (
	"context"
	"errors"

	"gitee.com/pam-platform/pam/server/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProjectRepo manages resource-catalog projects and namespace override rules.
type ProjectRepo struct {
	pool *pgxpool.Pool
}

func NewProjectRepo(pool *pgxpool.Pool) *ProjectRepo {
	return &ProjectRepo{pool: pool}
}

var ErrDefaultProjectProtected = errors.New("default project cannot be modified")

var ErrInvalidProjectScope = errors.New("resource project scope is invalid")

var ErrProjectInUse = errors.New("project is referenced by execution or audit history")

func projectTenant(ctx context.Context) string {
	if tenantID := domain.TenantIDFromContext(ctx); tenantID != "" {
		return tenantID
	}
	// Compatibility for migrations, background jobs, and existing tests that
	// predate the request tenant context.
	return "platform"
}

func (r *ProjectRepo) List(ctx context.Context) ([]domain.Project, error) {
	tenantID := projectTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT id, name, description, is_default, tenant_id, created_at, updated_at
		FROM projects WHERE tenant_id=$1 ORDER BY is_default DESC, name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Project
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.IsDefault, &p.TenantID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (r *ProjectRepo) GetByID(ctx context.Context, id string) (*domain.Project, error) {
	tenantID := projectTenant(ctx)
	var p domain.Project
	err := r.pool.QueryRow(ctx, `SELECT id, name, description, is_default, tenant_id, created_at, updated_at
		FROM projects WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&p.ID, &p.Name, &p.Description, &p.IsDefault, &p.TenantID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (r *ProjectRepo) GetDefault(ctx context.Context) (*domain.Project, error) {
	tenantID := projectTenant(ctx)
	var p domain.Project
	err := r.pool.QueryRow(ctx, `SELECT id, name, description, is_default, tenant_id, created_at, updated_at
		FROM projects WHERE tenant_id=$1 AND is_default`, tenantID).
		Scan(&p.ID, &p.Name, &p.Description, &p.IsDefault, &p.TenantID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (r *ProjectRepo) Create(ctx context.Context, p *domain.Project) error {
	tenantID := projectTenant(ctx)
	return r.pool.QueryRow(ctx, `INSERT INTO projects (name, description, tenant_id)
		VALUES ($1,$2,$3) RETURNING id, is_default, tenant_id, created_at, updated_at`,
		p.Name, p.Description, tenantID).Scan(&p.ID, &p.IsDefault, &p.TenantID, &p.CreatedAt, &p.UpdatedAt)
}

func (r *ProjectRepo) Update(ctx context.Context, p *domain.Project) error {
	tenantID := projectTenant(ctx)
	err := r.pool.QueryRow(ctx, `UPDATE projects SET name=$2, description=$3, updated_at=now()
		WHERE id=$1 AND tenant_id=$4 AND NOT is_default
		RETURNING is_default, tenant_id, created_at, updated_at`, p.ID, p.Name, p.Description, tenantID).
		Scan(&p.IsDefault, &p.TenantID, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDefaultProjectProtected
	}
	return err
}

// Delete removes a non-default project. Connectors referencing it fall back to
// the default project via ON DELETE SET NULL; namespace rules cascade away.
func (r *ProjectRepo) Delete(ctx context.Context, id string) (bool, error) {
	tenantID := projectTenant(ctx)
	command, err := r.pool.Exec(ctx, `DELETE FROM projects WHERE id=$1 AND tenant_id=$2 AND NOT is_default`, id, tenantID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return false, ErrProjectInUse
		}
		return false, err
	}
	return command.RowsAffected() > 0, nil
}

func (r *ProjectRepo) SetConnectorProject(ctx context.Context, connectorID, projectID string) (bool, error) {
	tenantID := projectTenant(ctx)
	command, err := r.pool.Exec(ctx, `UPDATE connectors SET project_id=nullif($2,'')::uuid, updated_at=now()
		WHERE id=$1 AND tenant_id=$3 AND archived_at IS NULL`, connectorID, projectID, tenantID)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() > 0, nil
}

func (r *ProjectRepo) SetConnectorEnvironment(ctx context.Context, connectorID, environment string) (bool, error) {
	tenantID := projectTenant(ctx)
	command, err := r.pool.Exec(ctx, `UPDATE connectors SET environment=$2::text, config=jsonb_set(config,'{environment}',to_jsonb($2::text),true), updated_at=now()
		WHERE id=$1 AND tenant_id=$3 AND archived_at IS NULL`, connectorID, environment, tenantID)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() > 0, nil
}

func (r *ProjectRepo) SetResourceProject(ctx context.Context, resourceID, projectID, actorID, reason string) error {
	tenantID := projectTenant(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `INSERT INTO resource_project_overrides (resource_id, project_id, created_by, reason)
		SELECT resource.id,$2,nullif($3,'')::uuid,$4 FROM resources resource
		JOIN projects project ON project.id=$2 AND project.tenant_id=resource.tenant_id
		WHERE resource.id=$1 AND resource.tenant_id=$5
		ON CONFLICT (resource_id) DO UPDATE SET project_id=EXCLUDED.project_id, created_by=EXCLUDED.created_by,
			reason=EXCLUDED.reason, updated_at=now()`, resourceID, projectID, actorID, reason, tenantID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrInvalidProjectScope
	}
	if _, err := tx.Exec(ctx, `UPDATE resources SET project_scope_mode='INHERITED',updated_at=now() WHERE id=$1 AND tenant_id=$2`, resourceID, tenantID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ProjectRepo) DeleteResourceProject(ctx context.Context, resourceID string) (bool, error) {
	tenantID := projectTenant(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `DELETE FROM resource_project_overrides override
		USING resources resource WHERE override.resource_id=$1 AND resource.id=override.resource_id AND resource.tenant_id=$2`, resourceID, tenantID)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE resources SET project_scope_mode='INHERITED',updated_at=now() WHERE id=$1 AND tenant_id=$2`, resourceID, tenantID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return command.RowsAffected() > 0, nil
}

func (r *ProjectRepo) SetResourceEnvironment(ctx context.Context, resourceID, environment, actorID, reason string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO resource_environment_overrides (resource_id, environment, created_by, reason)
		VALUES ($1,$2,nullif($3,'')::uuid,$4)
		ON CONFLICT (resource_id) DO UPDATE SET environment=EXCLUDED.environment, created_by=EXCLUDED.created_by,
			reason=EXCLUDED.reason, updated_at=now()`, resourceID, environment, actorID, reason)
	return err
}

func (r *ProjectRepo) DeleteResourceEnvironment(ctx context.Context, resourceID string) (bool, error) {
	command, err := r.pool.Exec(ctx, `DELETE FROM resource_environment_overrides WHERE resource_id=$1`, resourceID)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() > 0, nil
}

func (r *ProjectRepo) ReplaceResourceConsumers(ctx context.Context, resourceID string, projectIDs []string) error {
	tenantID := projectTenant(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var resourceExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM resources WHERE id=$1 AND tenant_id=$2)`, resourceID, tenantID).Scan(&resourceExists); err != nil {
		return err
	}
	if !resourceExists {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `DELETE FROM resource_project_consumers WHERE resource_id=$1`, resourceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM resource_project_bindings WHERE resource_id=$1 AND tenant_id=$2 AND source='LEGACY_CONSUMER'`, resourceID, tenantID); err != nil {
		return err
	}
	for _, projectID := range projectIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO resource_project_consumers(resource_id, project_id) VALUES($1,$2)`, resourceID, projectID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO resource_project_bindings(tenant_id,resource_id,project_id,source)
			VALUES($1,$2,$3,'LEGACY_CONSUMER') ON CONFLICT (resource_id,project_id) DO NOTHING`, tenantID, resourceID, projectID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *ProjectRepo) ListResourceConsumers(ctx context.Context, resourceID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT project_id::text FROM resource_project_consumers WHERE resource_id=$1 ORDER BY created_at, project_id`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func (r *ProjectRepo) GetResourceScope(ctx context.Context, resourceID string) (*domain.ResourceProjectScope, error) {
	tenantID := projectTenant(ctx)
	var mode domain.ProjectScopeMode
	if err := r.pool.QueryRow(ctx, `SELECT project_scope_mode FROM resources WHERE id=$1 AND tenant_id=$2`, resourceID, tenantID).Scan(&mode); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	scope := &domain.ResourceProjectScope{Mode: mode, ProjectIDs: []string{}, ProjectNames: []string{}}
	if mode == domain.ProjectScopeUnassigned {
		return scope, nil
	}
	if mode == domain.ProjectScopeAll {
		rows, err := r.pool.Query(ctx, `SELECT id::text,name FROM projects WHERE tenant_id=$1 ORDER BY is_default DESC,name`, tenantID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err != nil {
				return nil, err
			}
			scope.ProjectIDs = append(scope.ProjectIDs, id)
			scope.ProjectNames = append(scope.ProjectNames, name)
		}
		return scope, rows.Err()
	}
	if mode == domain.ProjectScopeInherited {
		projectID, err := NewResourceRepo(r.pool).EffectiveProjectID(ctx, resourceID)
		if err != nil {
			return nil, err
		}
		if projectID != "" {
			var name string
			if err := r.pool.QueryRow(ctx, `SELECT name FROM projects WHERE id=$1 AND tenant_id=$2`, projectID, tenantID).Scan(&name); err == nil {
				scope.ProjectIDs = append(scope.ProjectIDs, projectID)
				scope.ProjectNames = append(scope.ProjectNames, name)
			}
		}
	}
	rows, err := r.pool.Query(ctx, `SELECT binding.project_id::text,project.name
		FROM resource_project_bindings binding
		JOIN projects project ON project.id=binding.project_id AND project.tenant_id=binding.tenant_id
		WHERE binding.resource_id=$1 AND binding.tenant_id=$2 ORDER BY project.is_default DESC,project.name`, resourceID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := make(map[string]bool, len(scope.ProjectIDs))
	for _, id := range scope.ProjectIDs {
		seen[id] = true
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if !seen[id] {
			scope.ProjectIDs = append(scope.ProjectIDs, id)
			scope.ProjectNames = append(scope.ProjectNames, name)
			seen[id] = true
		}
	}
	return scope, rows.Err()
}

func (r *ProjectRepo) ReplaceResourceScope(ctx context.Context, resourceID string, mode domain.ProjectScopeMode, projectIDs []string, actorID string) error {
	tenantID := projectTenant(ctx)
	if mode != domain.ProjectScopeInherited && mode != domain.ProjectScopeSelected && mode != domain.ProjectScopeAll && mode != domain.ProjectScopeUnassigned {
		return ErrInvalidProjectScope
	}
	if mode == domain.ProjectScopeSelected && len(projectIDs) == 0 {
		return ErrInvalidProjectScope
	}
	if mode != domain.ProjectScopeSelected && len(projectIDs) != 0 {
		return ErrInvalidProjectScope
	}
	uniqueIDs := make([]string, 0, len(projectIDs))
	seen := map[string]bool{}
	for _, id := range projectIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		uniqueIDs = append(uniqueIDs, id)
	}
	if mode == domain.ProjectScopeSelected && len(uniqueIDs) == 0 {
		return ErrInvalidProjectScope
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE resources SET project_scope_mode=$3,updated_at=now() WHERE id=$1 AND tenant_id=$2`, resourceID, tenantID, mode)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `DELETE FROM resource_project_bindings WHERE resource_id=$1 AND tenant_id=$2`, resourceID, tenantID); err != nil {
		return err
	}
	for _, projectID := range uniqueIDs {
		command, err := tx.Exec(ctx, `INSERT INTO resource_project_bindings(tenant_id,resource_id,project_id,created_by)
			SELECT $1::varchar,$2::uuid,project.id,nullif($4,'')::uuid FROM projects project
			WHERE project.id=$3::uuid AND project.tenant_id=$1::varchar`, tenantID, resourceID, projectID, actorID)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrInvalidProjectScope
		}
	}
	return tx.Commit(ctx)
}

func (r *ProjectRepo) ListNamespaceRules(ctx context.Context) ([]domain.ProjectNamespaceRule, error) {
	tenantID := projectTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT r.id, r.project_id, r.connector_id, r.namespace, r.created_at
		FROM project_namespace_rules r JOIN projects p ON p.id=r.project_id
		WHERE p.tenant_id=$1 ORDER BY r.namespace`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ProjectNamespaceRule
	for rows.Next() {
		var rule domain.ProjectNamespaceRule
		if err := rows.Scan(&rule.ID, &rule.ProjectID, &rule.ConnectorID, &rule.Namespace, &rule.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, rule)
	}
	return result, rows.Err()
}

func (r *ProjectRepo) UpsertNamespaceRule(ctx context.Context, rule *domain.ProjectNamespaceRule) error {
	return r.pool.QueryRow(ctx, `INSERT INTO project_namespace_rules (project_id, connector_id, namespace)
		VALUES ($1,$2,$3)
		ON CONFLICT (connector_id, namespace) DO UPDATE SET project_id=EXCLUDED.project_id
		RETURNING id, created_at`, rule.ProjectID, rule.ConnectorID, rule.Namespace).
		Scan(&rule.ID, &rule.CreatedAt)
}

func (r *ProjectRepo) DeleteNamespaceRule(ctx context.Context, id string) (bool, error) {
	tenantID := projectTenant(ctx)
	command, err := r.pool.Exec(ctx, `DELETE FROM project_namespace_rules r USING projects p
		WHERE r.id=$1 AND p.id=r.project_id AND p.tenant_id=$2`, id, tenantID)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() > 0, nil
}
