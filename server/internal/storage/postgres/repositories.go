package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PrincipalRepo struct {
	pool      *pgxpool.Pool
	adminPool *pgxpool.Pool
}

func NewPrincipalRepo(pool *pgxpool.Pool) *PrincipalRepo {
	return &PrincipalRepo{pool: pool}
}

// SetAdminPool 配置旁路池（BYPASSRLS）；未设回退主池。
func (r *PrincipalRepo) SetAdminPool(p *pgxpool.Pool) { r.adminPool = p }
func (r *PrincipalRepo) admin() *pgxpool.Pool {
	if r.adminPool != nil {
		return r.adminPool
	}
	return r.pool
}

func principalTenant(ctx context.Context) string {
	if tenantID := domain.TenantIDFromContext(ctx); tenantID != "" {
		return tenantID
	}
	return ""
}

func repositoryTenant(ctx context.Context) string {
	if tenantID := domain.TenantIDFromContext(ctx); tenantID != "" {
		return tenantID
	}
	return ""
}

func (r *PrincipalRepo) GetByID(ctx context.Context, id string) (*domain.Principal, error) {
	tenantID := principalTenant(ctx)
	query := `SELECT id, kind, name, status, tenant_id, metadata, COALESCE(password_hash,''), last_login_at, created_at, updated_at
		FROM principals WHERE id = $1 AND tenant_id=$2`
	row := r.pool.QueryRow(ctx, query, id, tenantID)

	var p domain.Principal
	err := row.Scan(&p.ID, &p.Kind, &p.Name, &p.Status, &p.TenantID, &p.Metadata, &p.PasswordHash, &p.LastLoginAt, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// GetByName looks up a kind='user' principal by name. Used by the Web login
// flow; agent_instance/workload principals are not name-addressable here.
func (r *PrincipalRepo) GetByName(ctx context.Context, name string) (*domain.Principal, error) {
	tenantID := principalTenant(ctx)
	query := `SELECT id, kind, name, status, tenant_id, metadata, COALESCE(password_hash,''), last_login_at, created_at, updated_at
		FROM principals WHERE kind = 'user' AND name = $1 AND tenant_id=$2`
	row := r.pool.QueryRow(ctx, query, name, tenantID)

	var p domain.Principal
	err := row.Scan(&p.ID, &p.Kind, &p.Name, &p.Status, &p.TenantID, &p.Metadata, &p.PasswordHash, &p.LastLoginAt, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// UpdatePassword stores a bcrypt password hash for the given principal.
func (r *PrincipalRepo) UpdatePassword(ctx context.Context, id, hash string) error {
	_, err := r.admin().Exec(ctx, `UPDATE principals SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	return err
}

func (r *PrincipalRepo) UpdateName(ctx context.Context, id, name string) error {
	_, err := r.pool.Exec(ctx, `UPDATE principals SET name=$2,updated_at=now() WHERE id=$1 AND tenant_id=$3`, id, name, principalTenant(ctx))
	return err
}

// UpdateLastLogin stamps last_login_at to now() for the given principal.
func (r *PrincipalRepo) UpdateLastLogin(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE principals SET last_login_at = now() WHERE id = $1`, id)
	return err
}

// Delete soft-deletes a principal by setting status to DISABLED and revoking
// all active API keys in a single transaction. This ensures no window exists
// where a principal is disabled but its keys remain usable.
func (r *PrincipalRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tenantID := repositoryTenant(ctx)

	cmd, err := tx.Exec(ctx, `UPDATE principals SET status='DISABLED', updated_at=now() WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	if err != nil {
		return err
	}
	// 首条未命中（id 不属于本租户）即停止，避免级联撤销/取消他租户的
	// grant/request/execution。下面级联亦补 tenant_id 过滤作纵深防御。
	if cmd.RowsAffected() == 0 {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE principal_api_keys SET status='REVOKED', revoked_at=now() WHERE principal_id=$1 AND status='ACTIVE'`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE delegation_tokens SET revoked_at=now()
		WHERE tenant_id=$2 AND revoked_at IS NULL
		AND (principal_id=$1 OR on_behalf_of_principal_id=$1 OR accountable_principal_id=$1)`, id, tenantID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE external_identity_bindings SET status='REVOKED', revoked_at=now(), updated_at=now()
		WHERE tenant_id=$2 AND principal_id=$1 AND status='ACTIVE'`, id, tenantID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE access_grants SET status='REVOKED' WHERE tenant_id=$2 AND principal_id=$1 AND status='ACTIVE'`, id, tenantID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE access_requests SET status='CANCELLED', updated_at=now()
		WHERE tenant_id=$2 AND principal_id=$1 AND status='PENDING'`, id, tenantID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE executions SET status='CANCELLED', error_code='PRINCIPAL_DISABLED',
		error_message='请求身份已停用',updated_at=now()
		WHERE tenant_id=$2 AND principal_id=$1 AND status IN ('WAITING_APPROVAL','QUEUED')`, id, tenantID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE executions SET status='UNKNOWN', error_code='PRINCIPAL_DISABLED_DURING_DISPATCH',
		error_message='任务分发后请求身份被停用，目标系统可能已经执行',dispatch_token_hash=NULL,dispatch_expires_at=NULL,updated_at=now()
		WHERE tenant_id=$2 AND principal_id=$1 AND status IN ('DISPATCHED','RUNNING')`, id, tenantID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PrincipalRepo) List(ctx context.Context, kind string) ([]domain.Principal, error) {
	tenantID := principalTenant(ctx)
	query := `SELECT id, kind, name, status, tenant_id, metadata, created_at, updated_at
		FROM principals WHERE tenant_id=$2 AND status='ACTIVE'
		AND ($1 = '' OR kind = $1::principal_kind) ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, query, kind, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.Principal
	for rows.Next() {
		var p domain.Principal
		if err := rows.Scan(&p.ID, &p.Kind, &p.Name, &p.Status, &p.TenantID, &p.Metadata, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// SummariesByIDs batch-loads display fields (name/kind/status) for the given
// principal ids within the tenant boundary, replacing per-id GetByID loops in
// list enrichment paths.
func (r *PrincipalRepo) SummariesByIDs(ctx context.Context, ids []string) (map[string]domain.Principal, error) {
	out := make(map[string]domain.Principal, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	tenantID := principalTenant(ctx)
	rows, err := r.pool.Query(ctx,
		`SELECT id, kind, name, status, tenant_id FROM principals WHERE id = ANY($1) AND tenant_id=$2`, ids, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p domain.Principal
		if err := rows.Scan(&p.ID, &p.Kind, &p.Name, &p.Status, &p.TenantID); err != nil {
			return nil, err
		}
		out[p.ID] = p
	}
	return out, rows.Err()
}

func (r *PrincipalRepo) Create(ctx context.Context, p *domain.Principal) error {
	if p.Kind == "" {
		p.Kind = domain.PrincipalKindAgentInstance
	}
	if p.Status == "" {
		p.Status = domain.PrincipalStatusActive
	}
	if p.TenantID == "" {
		p.TenantID = repositoryTenant(ctx)
	}
	if p.TenantID == "" {
		return fmt.Errorf("principal create requires a tenant (set TenantID or tenant context)")
	}
	if p.Metadata == nil {
		p.Metadata = map[string]any{}
	}
	return r.pool.QueryRow(ctx, `INSERT INTO principals (kind,name,status,tenant_id,metadata)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (kind,name,tenant_id) DO UPDATE SET status='ACTIVE',
			metadata=EXCLUDED.metadata,updated_at=now()
			WHERE principals.status='DISABLED'
		RETURNING id,created_at,updated_at`,
		p.Kind, p.Name, p.Status, p.TenantID, p.Metadata).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

type APIKeyRepo struct {
	pool      *pgxpool.Pool
	adminPool *pgxpool.Pool
}

func NewAPIKeyRepo(pool *pgxpool.Pool) *APIKeyRepo { return &APIKeyRepo{pool: pool} }

// SetAdminPool 配置旁路池（BYPASSRLS）；未设回退主池。
func (r *APIKeyRepo) SetAdminPool(p *pgxpool.Pool) { r.adminPool = p }
func (r *APIKeyRepo) admin() *pgxpool.Pool {
	if r.adminPool != nil {
		return r.adminPool
	}
	return r.pool
}

func (r *APIKeyRepo) Create(ctx context.Context, key *domain.PrincipalAPIKey) error {
	return r.pool.QueryRow(ctx, `INSERT INTO principal_api_keys
		(id,principal_id,name,key_prefix,key_hash,status,purpose,audience,scopes,expires_at,created_by,rotation_parent_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,nullif($8,''),$9,$10,nullif($11,'')::uuid,nullif($12,'')::uuid)
		RETURNING id,created_at`, key.ID, key.PrincipalID, key.Name, key.KeyPrefix, key.KeyHash,
		key.Status, key.Purpose, key.Audience, key.Scopes, key.ExpiresAt, key.CreatedBy, key.RotationParentID).
		Scan(&key.ID, &key.CreatedAt)
}

func (r *APIKeyRepo) GetForAuthentication(ctx context.Context, id string) (*domain.PrincipalAPIKey, *domain.Principal, error) {
	row := r.admin().QueryRow(ctx, `SELECT k.id,k.principal_id,k.name,k.key_prefix,k.key_hash,k.status,k.purpose,COALESCE(k.audience,''),k.scopes,
		k.expires_at,k.last_used_at,COALESCE(host(k.last_used_ip),''),COALESCE(k.created_by::text,''),k.created_at,
		k.revoked_at,COALESCE(k.revoked_by::text,''),COALESCE(k.rotation_parent_id::text,''),
		p.id,p.kind,p.name,p.status,p.tenant_id,p.metadata,p.created_at,p.updated_at
		FROM principal_api_keys k JOIN principals p ON p.id=k.principal_id WHERE k.id=$1`, id)
	var key domain.PrincipalAPIKey
	var p domain.Principal
	err := row.Scan(&key.ID, &key.PrincipalID, &key.Name, &key.KeyPrefix, &key.KeyHash, &key.Status, &key.Purpose, &key.Audience, &key.Scopes,
		&key.ExpiresAt, &key.LastUsedAt, &key.LastUsedIP, &key.CreatedBy, &key.CreatedAt, &key.RevokedAt, &key.RevokedBy,
		&key.RotationParentID, &p.ID, &p.Kind, &p.Name, &p.Status, &p.TenantID, &p.Metadata, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &key, &p, nil
}

func (r *APIKeyRepo) ListByPrincipal(ctx context.Context, principalID string) ([]domain.PrincipalAPIKey, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,principal_id,name,key_prefix,status,purpose,COALESCE(audience,''),scopes,expires_at,last_used_at,
		COALESCE(host(last_used_ip),''),COALESCE(created_by::text,''),created_at,revoked_at,
		COALESCE(revoked_by::text,''),COALESCE(rotation_parent_id::text,'')
		FROM principal_api_keys WHERE principal_id=$1 ORDER BY created_at DESC`, principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.PrincipalAPIKey
	for rows.Next() {
		var key domain.PrincipalAPIKey
		if err := rows.Scan(&key.ID, &key.PrincipalID, &key.Name, &key.KeyPrefix, &key.Status, &key.Purpose, &key.Audience, &key.Scopes,
			&key.ExpiresAt, &key.LastUsedAt, &key.LastUsedIP, &key.CreatedBy, &key.CreatedAt, &key.RevokedAt,
			&key.RevokedBy, &key.RotationParentID); err != nil {
			return nil, err
		}
		result = append(result, key)
	}
	return result, rows.Err()
}

func (r *APIKeyRepo) GetByID(ctx context.Context, id string) (*domain.PrincipalAPIKey, error) {
	row := r.pool.QueryRow(ctx, `SELECT id,principal_id,name,key_prefix,key_hash,status,purpose,COALESCE(audience,''),scopes,expires_at,last_used_at,
		COALESCE(host(last_used_ip),''),COALESCE(created_by::text,''),created_at,revoked_at,
		COALESCE(revoked_by::text,''),COALESCE(rotation_parent_id::text,'') FROM principal_api_keys WHERE id=$1`, id)
	var key domain.PrincipalAPIKey
	err := row.Scan(&key.ID, &key.PrincipalID, &key.Name, &key.KeyPrefix, &key.KeyHash, &key.Status, &key.Purpose, &key.Audience, &key.Scopes,
		&key.ExpiresAt, &key.LastUsedAt, &key.LastUsedIP, &key.CreatedBy, &key.CreatedAt, &key.RevokedAt,
		&key.RevokedBy, &key.RotationParentID)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &key, nil
}

// NamesByIDs batch-loads key display names for list enrichment. API keys have
// no tenant column of their own; the owning principal enforces the boundary.
func (r *APIKeyRepo) NamesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx,
		`SELECT k.id, k.name FROM principal_api_keys k
		JOIN principals p ON p.id = k.principal_id
		WHERE k.id = ANY($1) AND p.tenant_id=$2`, ids, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

func (r *APIKeyRepo) MarkUsed(ctx context.Context, id, remoteIP string) error {
	_, err := r.pool.Exec(ctx, `UPDATE principal_api_keys SET last_used_at=now(),last_used_ip=nullif($2,'')::inet WHERE id=$1`, id, remoteIP)
	return err
}

func (r *APIKeyRepo) Revoke(ctx context.Context, id, actorID string) (bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE principal_api_keys SET status='REVOKED',revoked_at=now(),revoked_by=nullif($2,'')::uuid
		WHERE id=$1 AND status='ACTIVE'`, id, actorID)
	if err != nil || result.RowsAffected() != 1 {
		return result.RowsAffected() == 1, err
	}
	if _, err := tx.Exec(ctx, `UPDATE delegation_tokens SET revoked_at=now()
		WHERE parent_credential_id=$1 AND revoked_at IS NULL`, id); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

type ResourceRepo struct {
	pool *pgxpool.Pool
}

var ErrResourceSyncInProgress = errors.New("resource sync already in progress")

type ResourceSyncStats struct {
	Staged   int
	Upserted int64
	Orphaned int64
}

func NewResourceRepo(pool *pgxpool.Pool) *ResourceRepo {
	return &ResourceRepo{pool: pool}
}

func (r *ResourceRepo) GetByID(ctx context.Context, id string) (*domain.Resource, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT res.id, res.resource_type, res.external_id, res.name, COALESCE(res.namespace,''), COALESCE(res.connector_id::text,''), res.management_type,
		COALESCE(res.integration_id::text,''), COALESCE(res.owner_team,''),
		COALESCE(reo.environment, sreo.environment, ireo.environment, NULLIF(ig.environment,''), c.environment, res.environment),
		CASE WHEN reo.resource_id IS NOT NULL THEN 'RESOURCE'
			WHEN sreo.resource_id IS NOT NULL THEN 'DATABASE_SCHEMA'
			WHEN ireo.resource_id IS NOT NULL THEN 'DATABASE_INSTANCE'
			WHEN NULLIF(ig.environment,'') IS NOT NULL THEN 'INTEGRATION' ELSE 'CONNECTOR' END,
		res.status, res.labels, res.annotations, res.metadata, res.tenant_id, res.created_at, res.updated_at
		FROM resources res LEFT JOIN connectors c ON c.id=res.connector_id
		LEFT JOIN integrations ig ON ig.id=res.integration_id
		LEFT JOIN resource_environment_overrides reo ON reo.resource_id=res.id
		LEFT JOIN resources schema_parent ON res.resource_type='database_table'
			AND schema_parent.integration_id=res.integration_id AND schema_parent.resource_type='database_schema'
			AND schema_parent.name=COALESCE(res.metadata->>'schema',res.namespace) AND schema_parent.status<>'ORPHANED'
		LEFT JOIN resource_environment_overrides sreo ON sreo.resource_id=schema_parent.id
		LEFT JOIN resources instance_parent ON res.resource_type IN ('database_schema','database_table')
			AND instance_parent.integration_id=res.integration_id AND instance_parent.resource_type='database_instance'
			AND instance_parent.status<>'ORPHANED'
		LEFT JOIN resource_environment_overrides ireo ON ireo.resource_id=instance_parent.id
		WHERE res.id = $1 AND res.tenant_id=$2`
	row := r.pool.QueryRow(ctx, query, id, tenantID)

	var res domain.Resource
	err := row.Scan(&res.ID, &res.ResourceType, &res.ExternalID, &res.Name, &res.Namespace,
		&res.ConnectorID, &res.ManagementType, &res.IntegrationID, &res.OwnerTeam, &res.Environment, &res.EnvironmentSource, &res.Status,
		&res.Labels, &res.Annotations, &res.Metadata, &res.TenantID, &res.CreatedAt, &res.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &res, nil
}

// ActiveDatabaseTableExternalIDs returns the governed table identifiers that
// are currently ACTIVE under one database integration. Complex query ASTs use
// these identifiers for every FROM/JOIN source; resolving them here prevents a
// caller from substituting an unregistered or cross-integration table name.
func (r *ResourceRepo) ActiveDatabaseTableExternalIDs(ctx context.Context, integrationID string, externalIDs []string) (map[string]bool, error) {
	result := make(map[string]bool, len(externalIDs))
	if integrationID == "" || len(externalIDs) == 0 {
		return result, nil
	}
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT external_id FROM resources
		WHERE tenant_id=$3 AND resource_type='database_table' AND integration_id=$1
			AND status='ACTIVE' AND external_id=ANY($2)`, integrationID, externalIDs, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var externalID string
		if err := rows.Scan(&externalID); err != nil {
			return nil, err
		}
		result[externalID] = true
	}
	return result, rows.Err()
}

// EffectiveProjectID resolves the same inheritance chain used by the resource
// catalog without loading the full catalog: resource override > integration
// route/cloud tag > namespace > integration > connector (native resources
// only) > default. Integration-backed resources never inherit the channel.
func (r *ResourceRepo) EffectiveProjectID(ctx context.Context, resourceID string) (string, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT COALESCE(op.id::text, rr.project_id::text, tp.id::text, rp.id::text,
		ip.id::text, cp.id::text, dp.id::text, '')
		FROM resources res LEFT JOIN connectors c ON c.id=res.connector_id
		LEFT JOIN resource_project_overrides rpo ON rpo.resource_id=res.id
		LEFT JOIN projects op ON op.id=rpo.project_id
		LEFT JOIN LATERAL (
			SELECT rule.project_id FROM integration_project_rules rule
			WHERE rule.integration_id=res.integration_id AND res.resource_type IN ('jenkins_job','jenkins_build','jenkins_queue_item')
				AND left(CASE WHEN res.resource_type='jenkins_job' THEN regexp_replace(res.external_id, '^job/', '')
					ELSE res.namespace END, length(rule.pattern))=rule.pattern
			ORDER BY rule.priority ASC, length(rule.pattern) DESC, rule.id ASC LIMIT 1
		) rr ON true
		LEFT JOIN integrations ig ON ig.id=res.integration_id
		LEFT JOIN projects tp ON tp.id=CASE
			WHEN res.integration_id IS NOT NULL AND COALESCE(res.labels->>'warden-project-id',res.metadata->>'warden-project-id','')
				~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
			THEN COALESCE(res.labels->>'warden-project-id',res.metadata->>'warden-project-id')::uuid ELSE NULL END
			AND (tp.id=ig.project_id OR EXISTS (SELECT 1 FROM integration_project_bindings binding
				WHERE binding.integration_id=ig.id AND binding.project_id=tp.id))
		LEFT JOIN project_namespace_rules pnr ON res.integration_id IS NULL
			AND c.connector_type='kubernetes' AND pnr.connector_id=res.connector_id AND pnr.namespace=res.namespace
		LEFT JOIN projects rp ON rp.id=pnr.project_id
		LEFT JOIN projects ip ON ip.id=ig.project_id
		LEFT JOIN projects cp ON cp.id=c.project_id AND res.integration_id IS NULL
		LEFT JOIN projects dp ON dp.is_default AND dp.tenant_id=$2
		WHERE res.id=$1 AND res.tenant_id=$2`
	var projectID string
	err := r.pool.QueryRow(ctx, query, resourceID, tenantID).Scan(&projectID)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return projectID, err
}

// EnrichProjectScopes expands each resource's tenant-level project exposure.
// The existing ProjectID remains the inherited primary project used by legacy
// routing; ProjectIDs is the authoritative visibility/execution scope.
func (r *ResourceRepo) EnrichProjectScopes(ctx context.Context, resources []domain.Resource) error {
	if len(resources) == 0 {
		return nil
	}
	tenantID := repositoryTenant(ctx)
	ids := make([]string, 0, len(resources))
	index := make(map[string]int, len(resources))
	for i := range resources {
		ids = append(ids, resources[i].ID)
		index[resources[i].ID] = i
		resources[i].ProjectIDs = []string{}
		resources[i].ProjectNames = []string{}
	}
	rows, err := r.pool.Query(ctx, `SELECT id::text,project_scope_mode FROM resources WHERE tenant_id=$1 AND id=ANY($2)`, tenantID, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var mode domain.ProjectScopeMode
		if err := rows.Scan(&id, &mode); err != nil {
			rows.Close()
			return err
		}
		if i, ok := index[id]; ok {
			resources[i].ProjectScopeMode = mode
			if mode == domain.ProjectScopeInherited && resources[i].ProjectID != "" {
				resources[i].ProjectIDs = append(resources[i].ProjectIDs, resources[i].ProjectID)
				resources[i].ProjectNames = append(resources[i].ProjectNames, resources[i].ProjectName)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	bindingRows, err := r.pool.Query(ctx, `SELECT binding.resource_id::text,binding.project_id::text,project.name
		FROM resource_project_bindings binding
		JOIN projects project ON project.id=binding.project_id AND project.tenant_id=binding.tenant_id
		JOIN resources resource ON resource.id=binding.resource_id AND resource.tenant_id=binding.tenant_id
		WHERE binding.tenant_id=$1 AND binding.resource_id=ANY($2)
		ORDER BY project.is_default DESC,project.name`, tenantID, ids)
	if err != nil {
		return err
	}
	defer bindingRows.Close()
	for bindingRows.Next() {
		var resourceID, projectID, projectName string
		if err := bindingRows.Scan(&resourceID, &projectID, &projectName); err != nil {
			return err
		}
		i, ok := index[resourceID]
		if !ok || resources[i].ProjectScopeMode == domain.ProjectScopeUnassigned || resources[i].ProjectScopeMode == domain.ProjectScopeAll {
			continue
		}
		duplicate := false
		for _, existing := range resources[i].ProjectIDs {
			if existing == projectID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			resources[i].ProjectIDs = append(resources[i].ProjectIDs, projectID)
			resources[i].ProjectNames = append(resources[i].ProjectNames, projectName)
		}
	}
	if err := bindingRows.Err(); err != nil {
		return err
	}

	needsAll := false
	for i := range resources {
		if resources[i].ProjectScopeMode == domain.ProjectScopeAll {
			needsAll = true
			break
		}
	}
	if !needsAll {
		return nil
	}
	projectRows, err := r.pool.Query(ctx, `SELECT id::text,name FROM projects WHERE tenant_id=$1 ORDER BY is_default DESC,name`, tenantID)
	if err != nil {
		return err
	}
	defer projectRows.Close()
	var allIDs, allNames []string
	for projectRows.Next() {
		var id, name string
		if err := projectRows.Scan(&id, &name); err != nil {
			return err
		}
		allIDs = append(allIDs, id)
		allNames = append(allNames, name)
	}
	if err := projectRows.Err(); err != nil {
		return err
	}
	for i := range resources {
		if resources[i].ProjectScopeMode == domain.ProjectScopeAll {
			resources[i].ProjectIDs = append([]string(nil), allIDs...)
			resources[i].ProjectNames = append([]string(nil), allNames...)
		}
	}
	return nil
}

// ResourceSummary is a lightweight projection used to enrich executions and
// access requests with resource display info without loading full rows.
type ResourceSummary struct {
	ID           string
	Name         string
	ResourceType string
	Status       string
}

// SummariesByIDs batch-loads name/type/status for the given resource ids.
func (r *ResourceRepo) SummariesByIDs(ctx context.Context, ids []string) (map[string]ResourceSummary, error) {
	out := make(map[string]ResourceSummary, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, resource_type, status FROM resources WHERE id = ANY($1) AND tenant_id=$2`, ids, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s ResourceSummary
		if err := rows.Scan(&s.ID, &s.Name, &s.ResourceType, &s.Status); err != nil {
			return nil, err
		}
		out[s.ID] = s
	}
	return out, rows.Err()
}

// Stats aggregates resource counts by type and status so list pages can show
// overview numbers without downloading the entire resource catalog.
func (r *ResourceRepo) Stats(ctx context.Context) (map[string]any, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx,
		`SELECT res.resource_type, res.status, count(*)
		FROM resources res LEFT JOIN connectors c ON c.id=res.connector_id
		WHERE res.tenant_id=$1 AND (res.management_type='control_plane' OR (c.tenant_id=$1 AND c.archived_at IS NULL))
			AND res.metadata->>'integrationDeleted' IS DISTINCT FROM 'true'
			AND res.metadata->>'connectorDeleted' IS DISTINCT FROM 'true'
			AND (res.resource_type !~ '^(jenkins_|grafana_|database_|huawei_|aliyun_sls_)' OR res.integration_id IS NOT NULL)
		GROUP BY 1, 2`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	total := 0
	active := 0
	current := 0
	history := 0
	abnormal := 0
	byType := map[string]int{}
	byStatus := map[string]int{}
	for rows.Next() {
		var resourceType, status string
		var count int
		if err := rows.Scan(&resourceType, &status, &count); err != nil {
			return nil, err
		}
		total += count
		if status == "ACTIVE" {
			active += count
		}
		if status == "ORPHANED" {
			history += count
		} else {
			current += count
			if status != "ACTIVE" {
				abnormal += count
			}
		}
		byType[resourceType] += count
		byStatus[status] += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	// The resource catalog only renders Jenkins controllers, nodes and jobs;
	// builds are loaded per job, while queue and credential references remain
	// API-only. Aggregate the visible catalog by its effective project so the UI
	// never derives cross-domain counts from one domain's partial list.
	catalogRows, err := r.pool.Query(ctx, `SELECT
		scope.project_id::text,
		CASE
			WHEN res.resource_type IN ('pod','deployment','statefulset','daemonset','replicaset','job','cronjob','service','endpoints','ingress','configmap','secret','event','node','namespace') THEN 'kubernetes'
			WHEN res.resource_type LIKE 'jenkins%' THEN 'jenkins'
			WHEN res.resource_type LIKE 'grafana%' THEN 'grafana'
			WHEN res.resource_type LIKE 'database%' THEN 'database'
			WHEN res.resource_type LIKE 'huawei%' OR res.resource_type LIKE 'aliyun_sls%' THEN 'huawei'
			WHEN res.resource_type LIKE 'network_%' THEN 'network'
			ELSE 'linux' END,
		res.status::text, count(*)
		FROM resources res LEFT JOIN connectors c ON c.id=res.connector_id
		LEFT JOIN resource_project_overrides rpo ON rpo.resource_id=res.id
		LEFT JOIN projects op ON op.id=rpo.project_id
		LEFT JOIN LATERAL (
			SELECT rule.project_id FROM integration_project_rules rule
			WHERE rule.integration_id=res.integration_id AND res.resource_type='jenkins_job'
				AND left(regexp_replace(res.external_id, '^job/', ''), length(rule.pattern))=rule.pattern
			ORDER BY rule.priority ASC, length(rule.pattern) DESC, rule.id ASC LIMIT 1
		) rr ON true
		LEFT JOIN integrations ig ON ig.id=res.integration_id
		LEFT JOIN projects tp ON tp.id=CASE
			WHEN res.integration_id IS NOT NULL AND COALESCE(res.labels->>'warden-project-id',res.metadata->>'warden-project-id','')
				~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
			THEN COALESCE(res.labels->>'warden-project-id',res.metadata->>'warden-project-id')::uuid ELSE NULL END
			AND (tp.id=ig.project_id OR EXISTS (SELECT 1 FROM integration_project_bindings binding
				WHERE binding.integration_id=ig.id AND binding.project_id=tp.id))
		LEFT JOIN project_namespace_rules pnr ON res.integration_id IS NULL
			AND c.connector_type='kubernetes' AND pnr.connector_id=res.connector_id AND pnr.namespace=res.namespace
		LEFT JOIN projects rp ON rp.id=pnr.project_id
		LEFT JOIN projects ip ON ip.id=ig.project_id
		LEFT JOIN projects cp ON cp.id=c.project_id AND res.integration_id IS NULL
		LEFT JOIN projects dp ON dp.is_default AND dp.tenant_id=$1
		JOIN LATERAL (
			SELECT candidate.project_id FROM (
				SELECT binding.project_id
				FROM resource_project_bindings binding
				WHERE binding.resource_id=res.id AND binding.tenant_id=res.tenant_id
					AND res.project_scope_mode IN ('INHERITED','SELECTED_PROJECTS')
				UNION
				SELECT project.id
				FROM projects project
				WHERE project.tenant_id=res.tenant_id AND res.project_scope_mode='ALL_PROJECTS'
				UNION
				SELECT COALESCE(op.id, rr.project_id, tp.id, rp.id, ip.id, cp.id, dp.id)
				WHERE res.project_scope_mode='INHERITED'
			) candidate
			WHERE candidate.project_id IS NOT NULL
		) scope ON true
		WHERE res.tenant_id=$1 AND (res.management_type='control_plane' OR (c.tenant_id=$1 AND c.archived_at IS NULL))
			AND res.metadata->>'integrationDeleted' IS DISTINCT FROM 'true'
			AND res.metadata->>'connectorDeleted' IS DISTINCT FROM 'true'
			AND (res.resource_type !~ '^(jenkins_|grafana_|database_|huawei_|aliyun_sls_)' OR res.integration_id IS NOT NULL)
			AND (res.resource_type NOT LIKE 'jenkins%' OR res.resource_type IN ('jenkins_controller','jenkins_node','jenkins_job'))
		GROUP BY 1, 2, 3`, repositoryTenant(ctx))
	if err != nil {
		return nil, err
	}
	defer catalogRows.Close()
	catalogByProjectDomainStatus := map[string]map[string]map[string]int{}
	for catalogRows.Next() {
		var projectID, resourceDomain, status string
		var count int
		if err := catalogRows.Scan(&projectID, &resourceDomain, &status, &count); err != nil {
			return nil, err
		}
		if catalogByProjectDomainStatus[projectID] == nil {
			catalogByProjectDomainStatus[projectID] = map[string]map[string]int{}
		}
		if catalogByProjectDomainStatus[projectID][resourceDomain] == nil {
			catalogByProjectDomainStatus[projectID][resourceDomain] = map[string]int{}
		}
		catalogByProjectDomainStatus[projectID][resourceDomain][status] += count
	}
	if err := catalogRows.Err(); err != nil {
		return nil, err
	}
	catalogRows.Close()

	// Keep the all-project totals unique. A resource exposed to three projects
	// contributes to each project's count above, but only once in this view.
	overallRows, err := r.pool.Query(ctx, `SELECT
		CASE
			WHEN res.resource_type IN ('pod','deployment','statefulset','daemonset','replicaset','job','cronjob','service','endpoints','ingress','configmap','secret','event','node','namespace') THEN 'kubernetes'
			WHEN res.resource_type LIKE 'jenkins%' THEN 'jenkins'
			WHEN res.resource_type LIKE 'grafana%' THEN 'grafana'
			WHEN res.resource_type LIKE 'database%' THEN 'database'
			WHEN res.resource_type LIKE 'huawei%' OR res.resource_type LIKE 'aliyun_sls%' THEN 'huawei'
			WHEN res.resource_type LIKE 'network_%' THEN 'network'
			ELSE 'linux' END,
		res.status::text, count(*)
		FROM resources res LEFT JOIN connectors c ON c.id=res.connector_id
		WHERE res.tenant_id=$1 AND (res.management_type='control_plane' OR (c.tenant_id=$1 AND c.archived_at IS NULL))
			AND res.metadata->>'integrationDeleted' IS DISTINCT FROM 'true'
			AND res.metadata->>'connectorDeleted' IS DISTINCT FROM 'true'
			AND (res.resource_type !~ '^(jenkins_|grafana_|database_|huawei_|aliyun_sls_)' OR res.integration_id IS NOT NULL)
			AND (res.resource_type NOT LIKE 'jenkins%' OR res.resource_type IN ('jenkins_controller','jenkins_node','jenkins_job'))
		GROUP BY 1, 2`, tenantID)
	if err != nil {
		return nil, err
	}
	defer overallRows.Close()
	catalogByDomainStatus := map[string]map[string]int{}
	for overallRows.Next() {
		var resourceDomain, status string
		var count int
		if err := overallRows.Scan(&resourceDomain, &status, &count); err != nil {
			return nil, err
		}
		if catalogByDomainStatus[resourceDomain] == nil {
			catalogByDomainStatus[resourceDomain] = map[string]int{}
		}
		catalogByDomainStatus[resourceDomain][status] = count
	}
	if err := overallRows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"total": total, "current": current, "active": active, "abnormal": abnormal, "history": history,
		"byType": byType, "byStatus": byStatus, "catalogByDomainStatus": catalogByDomainStatus,
		"catalogByProjectDomainStatus": catalogByProjectDomainStatus,
	}, nil
}

// resourceSelectWithProjects is the shared SELECT + join chain resolving the
// effective project (resource override > integration route/cloud tag >
// namespace rule > integration > connector > default) and environment for a
// resource row. $7 is always the tenant slug. List and ByIDs append their own
// WHERE/ORDER clauses; keep the parameter positions stable.
const resourceSelectWithProjects = `SELECT res.id, res.resource_type, res.external_id, res.name, COALESCE(res.namespace,''), COALESCE(res.connector_id::text,''), res.management_type,
	COALESCE(res.integration_id::text,''), COALESCE(res.owner_team,''),
	COALESCE(reo.environment, sreo.environment, ireo.environment, NULLIF(ig.environment,''), c.environment, res.environment), res.status,
	res.labels, res.annotations, res.metadata, res.tenant_id, res.created_at, res.updated_at,
	COALESCE(op.id::text, rr.project_id::text, tp.id::text, rp.id::text, ip.id::text, cp.id::text, dp.id::text, ''),
	COALESCE(op.name, rrp.name, tp.name, rp.name, ip.name, cp.name, dp.name, ''),
	CASE WHEN op.id IS NOT NULL THEN 'RESOURCE' WHEN rr.project_id IS NOT NULL THEN 'INTEGRATION_RULE'
		WHEN tp.id IS NOT NULL THEN 'CLOUD_TAG' WHEN rp.id IS NOT NULL THEN 'NAMESPACE'
		WHEN ip.id IS NOT NULL THEN 'INTEGRATION' WHEN cp.id IS NOT NULL THEN 'CONNECTOR' ELSE 'DEFAULT' END,
	CASE WHEN reo.resource_id IS NOT NULL THEN 'RESOURCE'
		WHEN sreo.resource_id IS NOT NULL THEN 'DATABASE_SCHEMA'
		WHEN ireo.resource_id IS NOT NULL THEN 'DATABASE_INSTANCE'
		WHEN NULLIF(ig.environment,'') IS NOT NULL THEN 'INTEGRATION' ELSE 'CONNECTOR' END
	FROM resources res LEFT JOIN connectors c ON c.id=res.connector_id
	LEFT JOIN resource_project_overrides rpo ON rpo.resource_id=res.id
	LEFT JOIN projects op ON op.id=rpo.project_id
	LEFT JOIN LATERAL (
		SELECT rule.project_id FROM integration_project_rules rule
		WHERE rule.integration_id=res.integration_id AND res.resource_type IN ('jenkins_job','jenkins_build','jenkins_queue_item')
			AND left(CASE WHEN res.resource_type='jenkins_job' THEN regexp_replace(res.external_id, '^job/', '')
				ELSE res.namespace END, length(rule.pattern)) = rule.pattern
		ORDER BY rule.priority ASC, length(rule.pattern) DESC, rule.id ASC LIMIT 1
	) rr ON true
	LEFT JOIN projects rrp ON rrp.id=rr.project_id
	LEFT JOIN integrations ig ON ig.id=res.integration_id
	LEFT JOIN resource_environment_overrides reo ON reo.resource_id=res.id
	LEFT JOIN resources schema_parent ON res.resource_type='database_table'
		AND schema_parent.integration_id=res.integration_id AND schema_parent.resource_type='database_schema'
		AND schema_parent.name=COALESCE(res.metadata->>'schema',res.namespace) AND schema_parent.status<>'ORPHANED'
	LEFT JOIN resource_environment_overrides sreo ON sreo.resource_id=schema_parent.id
	LEFT JOIN resources instance_parent ON res.resource_type IN ('database_schema','database_table')
		AND instance_parent.integration_id=res.integration_id AND instance_parent.resource_type='database_instance'
		AND instance_parent.status<>'ORPHANED'
	LEFT JOIN resource_environment_overrides ireo ON ireo.resource_id=instance_parent.id
	LEFT JOIN projects tp ON tp.id=CASE
		WHEN res.integration_id IS NOT NULL AND COALESCE(res.labels->>'warden-project-id', res.metadata->>'warden-project-id', '')
			~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
		THEN COALESCE(res.labels->>'warden-project-id', res.metadata->>'warden-project-id')::uuid ELSE NULL END
		AND (tp.id=ig.project_id OR EXISTS (SELECT 1 FROM integration_project_bindings binding
			WHERE binding.integration_id=ig.id AND binding.project_id=tp.id))
	LEFT JOIN project_namespace_rules pnr ON res.integration_id IS NULL
		AND c.connector_type='kubernetes' AND pnr.connector_id=res.connector_id AND pnr.namespace=res.namespace
	LEFT JOIN projects rp ON rp.id=pnr.project_id
	LEFT JOIN projects ip ON ip.id=ig.project_id
	LEFT JOIN projects cp ON cp.id=c.project_id AND res.integration_id IS NULL
	LEFT JOIN projects dp ON dp.is_default AND dp.tenant_id=$7`

func scanResourceWithProjects(rows pgx.Rows) (domain.Resource, error) {
	var res domain.Resource
	err := rows.Scan(&res.ID, &res.ResourceType, &res.ExternalID, &res.Name, &res.Namespace,
		&res.ConnectorID, &res.ManagementType, &res.IntegrationID, &res.OwnerTeam, &res.Environment, &res.Status,
		&res.Labels, &res.Annotations, &res.Metadata, &res.TenantID, &res.CreatedAt, &res.UpdatedAt,
		&res.ProjectID, &res.ProjectName, &res.ProjectSource, &res.EnvironmentSource)
	return res, err
}

func (r *ResourceRepo) List(ctx context.Context, resourceType, domainFilter, excludeType, namespace, integrationID string, limit, offset int) ([]domain.Resource, error) {
	tenantID := repositoryTenant(ctx)
	query := resourceSelectWithProjects + `
		WHERE res.tenant_id=$7 AND (res.management_type='control_plane' OR (c.tenant_id=$7 AND c.archived_at IS NULL)) AND ($1 = '' OR res.resource_type = $1)
		AND ($2 = '' OR CASE
			WHEN res.resource_type IN ('pod','deployment','statefulset','daemonset','replicaset','job','cronjob','service','endpoints','ingress','configmap','secret','event','node','namespace') THEN 'kubernetes'
			WHEN res.resource_type LIKE 'jenkins%' THEN 'jenkins'
			WHEN res.resource_type LIKE 'grafana%' THEN 'grafana'
			WHEN res.resource_type LIKE 'database%' THEN 'database'
			WHEN res.resource_type LIKE 'huawei%' OR res.resource_type LIKE 'aliyun_sls%' THEN 'huawei'
			WHEN res.resource_type LIKE 'network_%' THEN 'network'
			ELSE 'linux' END = $2)
		AND ($3 = '' OR res.resource_type <> $3)
		AND ($4 = '' OR res.namespace = $4)
		AND ($5 = '' OR COALESCE(res.integration_id::text,'') = $5)
		AND res.metadata->>'integrationDeleted' IS DISTINCT FROM 'true'
		AND res.metadata->>'connectorDeleted' IS DISTINCT FROM 'true'
		AND (res.resource_type !~ '^(jenkins_|grafana_|database_|huawei_|aliyun_sls_)' OR res.integration_id IS NOT NULL)
		ORDER BY CASE WHEN res.resource_type = 'jenkins_build' AND res.metadata->>'number' ~ '^[0-9]+$'
			THEN (res.metadata->>'number')::bigint END DESC NULLS LAST,
			res.created_at DESC LIMIT NULLIF($6, 0) OFFSET $8`
	rows, err := r.pool.Query(ctx, query, resourceType, domainFilter, excludeType, namespace, integrationID, limit, tenantID, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.Resource
	for rows.Next() {
		res, err := scanResourceWithProjects(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, res)
	}
	return result, rows.Err()
}

// ByIDs batch-loads full resource rows (including the effective primary
// project) for the given ids within the tenant boundary. Callers that need
// ProjectIDs should run EnrichProjectScopes on the values afterwards.
func (r *ResourceRepo) ByIDs(ctx context.Context, ids []string) (map[string]domain.Resource, error) {
	out := make(map[string]domain.Resource, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	tenantID := repositoryTenant(ctx)
	// The shared select chain references the tenant as $7 (List's parameter
	// layout); remap it to $2 so this query only carries used parameters.
	query := strings.ReplaceAll(resourceSelectWithProjects, "$7", "$2") + `
		WHERE res.tenant_id=$2 AND res.id = ANY($1)
		AND (res.management_type='control_plane' OR (c.tenant_id=$2 AND c.archived_at IS NULL))`
	rows, err := r.pool.Query(ctx, query, ids, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		res, err := scanResourceWithProjects(rows)
		if err != nil {
			return nil, err
		}
		out[res.ID] = res
	}
	return out, rows.Err()
}

// ListTopology returns one compact representative per parent/type/status group.
// The aggregate count is carried in metadata so the topology can render the
// same folded graph without downloading every resource row.
func (r *ResourceRepo) ListTopology(ctx context.Context) ([]domain.Resource, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT COALESCE(res.integration_id::text,''), COALESCE(res.connector_id::text,''),
		res.resource_type, res.status, count(*)
		FROM resources res LEFT JOIN connectors c ON c.id=res.connector_id
		WHERE res.tenant_id=$1 AND res.status<>'ORPHANED'
		  AND res.management_type='connector' AND c.tenant_id=$1 AND c.archived_at IS NULL
		  AND res.metadata->>'integrationDeleted' IS DISTINCT FROM 'true'
		  AND res.metadata->>'connectorDeleted' IS DISTINCT FROM 'true'
		  AND (res.resource_type !~ '^(jenkins_|grafana_|database_|huawei_|aliyun_sls_)' OR res.integration_id IS NOT NULL)
	GROUP BY 1,2,3,4 ORDER BY 1,2,3,4`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Resource
	for rows.Next() {
		var res domain.Resource
		var count int
		if err := rows.Scan(&res.IntegrationID, &res.ConnectorID, &res.ResourceType, &res.Status, &count); err != nil {
			return nil, err
		}
		res.ID = "topology:" + res.IntegrationID + ":" + res.ConnectorID + ":" + res.ResourceType + ":" + string(res.Status)
		res.Name = res.ResourceType
		res.Metadata = map[string]any{"topologyCount": count}
		result = append(result, res)
	}
	return result, rows.Err()
}

// TopologySample is one concrete resource surfaced under a topology group.
type TopologySample struct {
	Name      string
	Namespace string
	Status    string
}

// TopologyGroupStat is one folded resource type under a connector/integration.
// It carries the sample resources so the drill-down view can show real names
// without downloading the whole catalog.
type TopologyGroupStat struct {
	IntegrationID string
	ConnectorID   string
	ResourceType  string
	Count         int
	ActiveCount   int
	Samples       []TopologySample
}

// ListTopologyGroups aggregates resources by parent + resource_type. The
// filter matches ListTopology so the drill-down tree and the legacy folded
// graph describe the same resource set.
func (r *ResourceRepo) ListTopologyGroups(ctx context.Context) ([]TopologyGroupStat, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT COALESCE(res.integration_id::text,''), res.connector_id::text, res.resource_type,
		count(*)::int,
		count(*) FILTER (WHERE res.status='ACTIVE')::int,
		(array_agg(res.name ORDER BY (res.status='ACTIVE') DESC, res.created_at DESC))[1:6],
		(array_agg(COALESCE(res.namespace,'') ORDER BY (res.status='ACTIVE') DESC, res.created_at DESC))[1:6],
		(array_agg(res.status::text ORDER BY (res.status='ACTIVE') DESC, res.created_at DESC))[1:6]
		FROM resources res LEFT JOIN connectors c ON c.id=res.connector_id
		WHERE res.tenant_id=$1 AND res.status<>'ORPHANED'
		  AND res.management_type='connector' AND c.tenant_id=$1 AND c.archived_at IS NULL
		  AND res.metadata->>'integrationDeleted' IS DISTINCT FROM 'true'
		  AND res.metadata->>'connectorDeleted' IS DISTINCT FROM 'true'
		  AND (res.resource_type !~ '^(jenkins_|grafana_|database_|huawei_|aliyun_sls_)' OR res.integration_id IS NOT NULL)
		GROUP BY 1,2,3 ORDER BY 1,2,3`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []TopologyGroupStat
	for rows.Next() {
		var stat TopologyGroupStat
		var names, namespaces, statuses []string
		if err := rows.Scan(&stat.IntegrationID, &stat.ConnectorID, &stat.ResourceType,
			&stat.Count, &stat.ActiveCount, &names, &namespaces, &statuses); err != nil {
			return nil, err
		}
		for i := range names {
			sample := TopologySample{Name: names[i]}
			if i < len(namespaces) {
				sample.Namespace = namespaces[i]
			}
			if i < len(statuses) {
				sample.Status = statuses[i]
			}
			stat.Samples = append(stat.Samples, sample)
		}
		result = append(result, stat)
	}
	return result, rows.Err()
}

// ListTopologyLiveGroups returns the connector/integration/type keys that have
// an in-flight execution. The key format is "connector|integration|type".
func (r *ResourceRepo) ListTopologyLiveGroups(ctx context.Context) (map[string]struct{}, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT res.connector_id::text, COALESCE(res.integration_id::text,''), res.resource_type
		FROM executions e JOIN resources res ON res.id=e.resource_id
		WHERE e.tenant_id=$1 AND e.status IN ('QUEUED','DISPATCHED','RUNNING','WAITING_APPROVAL')`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]struct{}{}
	for rows.Next() {
		var connectorID, integrationID, resourceType string
		if err := rows.Scan(&connectorID, &integrationID, &resourceType); err != nil {
			return nil, err
		}
		result[topologyLiveKey(connectorID, integrationID, resourceType)] = struct{}{}
	}
	return result, rows.Err()
}

func topologyLiveKey(connectorID, integrationID, resourceType string) string {
	return connectorID + "|" + integrationID + "|" + resourceType
}

func (r *ResourceRepo) ListAvailable(ctx context.Context, resourceType string) ([]domain.Resource, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT res.id, res.resource_type, res.external_id, res.name, COALESCE(res.namespace,''), COALESCE(res.connector_id::text,''), res.management_type,
		COALESCE(res.integration_id::text,''), COALESCE(res.owner_team,''),
		COALESCE(reo.environment, sreo.environment, ireo.environment, NULLIF(ig.environment,''), c.environment, res.environment),
		CASE WHEN reo.resource_id IS NOT NULL THEN 'RESOURCE'
			WHEN sreo.resource_id IS NOT NULL THEN 'DATABASE_SCHEMA'
			WHEN ireo.resource_id IS NOT NULL THEN 'DATABASE_INSTANCE'
			WHEN NULLIF(ig.environment,'') IS NOT NULL THEN 'INTEGRATION' ELSE 'CONNECTOR' END,
		res.status,
		res.labels, res.annotations, res.metadata, res.tenant_id, res.created_at, res.updated_at,
		COALESCE(op.id::text, rr.project_id::text, tp.id::text, rp.id::text, ip.id::text, cp.id::text, dp.id::text, ''),
		COALESCE(op.name, rrp.name, tp.name, rp.name, ip.name, cp.name, dp.name, ''),
		CASE WHEN op.id IS NOT NULL THEN 'RESOURCE' WHEN rr.project_id IS NOT NULL THEN 'INTEGRATION_RULE'
			WHEN tp.id IS NOT NULL THEN 'CLOUD_TAG' WHEN rp.id IS NOT NULL THEN 'NAMESPACE'
			WHEN ip.id IS NOT NULL THEN 'INTEGRATION' WHEN cp.id IS NOT NULL THEN 'CONNECTOR' ELSE 'DEFAULT' END
		FROM resources res LEFT JOIN connectors c ON c.id=res.connector_id
		LEFT JOIN resource_project_overrides rpo ON rpo.resource_id=res.id
		LEFT JOIN projects op ON op.id=rpo.project_id
		LEFT JOIN LATERAL (
			SELECT rule.project_id FROM integration_project_rules rule
			WHERE rule.integration_id=res.integration_id AND res.resource_type IN ('jenkins_job','jenkins_build','jenkins_queue_item')
				AND left(CASE WHEN res.resource_type='jenkins_job' THEN regexp_replace(res.external_id, '^job/', '')
					ELSE res.namespace END, length(rule.pattern))=rule.pattern
			ORDER BY rule.priority ASC, length(rule.pattern) DESC, rule.id ASC LIMIT 1
		) rr ON true
		LEFT JOIN projects rrp ON rrp.id=rr.project_id
		LEFT JOIN integrations ig ON ig.id=res.integration_id
		LEFT JOIN resource_environment_overrides reo ON reo.resource_id=res.id
		LEFT JOIN resources schema_parent ON res.resource_type='database_table'
			AND schema_parent.integration_id=res.integration_id AND schema_parent.resource_type='database_schema'
			AND schema_parent.name=COALESCE(res.metadata->>'schema',res.namespace) AND schema_parent.status<>'ORPHANED'
		LEFT JOIN resource_environment_overrides sreo ON sreo.resource_id=schema_parent.id
		LEFT JOIN resources instance_parent ON res.resource_type IN ('database_schema','database_table')
			AND instance_parent.integration_id=res.integration_id AND instance_parent.resource_type='database_instance'
			AND instance_parent.status<>'ORPHANED'
		LEFT JOIN resource_environment_overrides ireo ON ireo.resource_id=instance_parent.id
		LEFT JOIN projects tp ON tp.id=CASE
			WHEN res.integration_id IS NOT NULL AND COALESCE(res.labels->>'warden-project-id',res.metadata->>'warden-project-id','')
				~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
			THEN COALESCE(res.labels->>'warden-project-id',res.metadata->>'warden-project-id')::uuid ELSE NULL END
			AND (tp.id=ig.project_id OR EXISTS (SELECT 1 FROM integration_project_bindings binding
				WHERE binding.integration_id=ig.id AND binding.project_id=tp.id))
		LEFT JOIN project_namespace_rules pnr ON res.integration_id IS NULL
			AND c.connector_type='kubernetes' AND pnr.connector_id=res.connector_id AND pnr.namespace=res.namespace
		LEFT JOIN projects rp ON rp.id=pnr.project_id
		LEFT JOIN projects ip ON ip.id=ig.project_id
		LEFT JOIN projects cp ON cp.id=c.project_id AND res.integration_id IS NULL
		LEFT JOIN projects dp ON dp.is_default AND dp.tenant_id=$2
		WHERE res.tenant_id=$2 AND res.status='ACTIVE'
		AND (res.management_type='control_plane' OR (c.tenant_id=$2 AND c.status='ACTIVE' AND c.archived_at IS NULL
			AND c.last_heartbeat IS NOT NULL AND c.last_heartbeat > now() - interval '90 seconds'))
		AND ($1='' OR res.resource_type=$1)
		AND (res.integration_id IS NULL OR ig.status='ACTIVE')
		AND (res.resource_type !~ '^(jenkins_|grafana_|database_|huawei_|aliyun_sls_)' OR res.integration_id IS NOT NULL)
		ORDER BY c.name, res.namespace, res.resource_type, res.name`
	rows, err := r.pool.Query(ctx, query, resourceType, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Resource
	for rows.Next() {
		var res domain.Resource
		if err := rows.Scan(&res.ID, &res.ResourceType, &res.ExternalID, &res.Name, &res.Namespace,
			&res.ConnectorID, &res.ManagementType, &res.IntegrationID, &res.OwnerTeam, &res.Environment, &res.EnvironmentSource, &res.Status, &res.Labels, &res.Annotations,
			&res.Metadata, &res.TenantID, &res.CreatedAt, &res.UpdatedAt, &res.ProjectID, &res.ProjectName, &res.ProjectSource); err != nil {
			return nil, err
		}
		result = append(result, res)
	}
	return result, rows.Err()
}

func (r *ResourceRepo) Upsert(ctx context.Context, res *domain.Resource) error {
	return upsertResource(ctx, r.pool, res)
}

// UpsertTx is the transaction-scoped variant of Upsert. It is used by the
// control-plane credential executor to register the "credential" catalog
// resource atomically with the credential row it represents.
func (r *ResourceRepo) UpsertTx(ctx context.Context, tx pgx.Tx, res *domain.Resource) error {
	return upsertResource(ctx, tx, res)
}

func upsertResource(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, res *domain.Resource) error {
	tenantID := repositoryTenant(ctx)
	if res.ManagementType == domain.ManagementControlPlane {
		query := `INSERT INTO resources (resource_type, external_id, name, namespace, connector_id, management_type, owner_team,
			environment, status, labels, annotations, metadata, tenant_id)
			VALUES ($1,$2,$3,$4,NULL,'control_plane',$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (tenant_id, resource_type, external_id) WHERE management_type='control_plane'
			DO UPDATE SET name=EXCLUDED.name, namespace=EXCLUDED.namespace, status=EXCLUDED.status,
				labels=EXCLUDED.labels, annotations=EXCLUDED.annotations, metadata=EXCLUDED.metadata,
				updated_at=now()
			RETURNING id, created_at, updated_at`
		return db.QueryRow(ctx, query,
			res.ResourceType, res.ExternalID, res.Name, res.Namespace, res.OwnerTeam,
			res.Environment, res.Status, res.Labels, res.Annotations, res.Metadata, tenantID).
			Scan(&res.ID, &res.CreatedAt, &res.UpdatedAt)
	}
	res.ManagementType = domain.ManagementConnector
	query := `INSERT INTO resources (resource_type, external_id, name, namespace, connector_id, owner_team,
		environment, status, labels, annotations, metadata, tenant_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (connector_id, resource_type, external_id)
		DO UPDATE SET name=EXCLUDED.name, namespace=EXCLUDED.namespace, status=EXCLUDED.status,
			labels=EXCLUDED.labels, annotations=EXCLUDED.annotations, metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING id, created_at, updated_at`
	return db.QueryRow(ctx, query,
		res.ResourceType, res.ExternalID, res.Name, res.Namespace, res.ConnectorID,
		res.OwnerTeam, res.Environment, res.Status, res.Labels, res.Annotations,
		res.Metadata, tenantID).Scan(&res.ID, &res.CreatedAt, &res.UpdatedAt)
}

// UpsertFromSync is used by the connector resource sync path. Unlike Upsert it
// does NOT overwrite status or owner_team: admin-set DISABLED resources stay
// disabled, and owner_team assignments survive sync. Only discovery-time fields
// (external_id, labels, annotations, metadata) are refreshed.
func (r *ResourceRepo) UpsertFromSync(ctx context.Context, res *domain.Resource) error {
	tenantID := repositoryTenant(ctx)
	query := `INSERT INTO resources (resource_type, external_id, name, namespace, connector_id, owner_team,
		environment, status, labels, annotations, metadata, tenant_id, last_seen_at, sync_generation, integration_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now(),$13,nullif($14,'')::uuid)
		ON CONFLICT (connector_id, resource_type, external_id)
		DO UPDATE SET name=EXCLUDED.name, namespace=EXCLUDED.namespace,
			labels=EXCLUDED.labels, annotations=EXCLUDED.annotations, metadata=EXCLUDED.metadata,
			status=CASE
				WHEN resources.status='DISABLED' AND resources.metadata->>'connectorDeleted' IS DISTINCT FROM 'true'
				THEN resources.status ELSE 'ACTIVE'::resource_status END,
			tenant_id=EXCLUDED.tenant_id,
			integration_id=COALESCE(EXCLUDED.integration_id, resources.integration_id),
			last_seen_at=now(), sync_generation=EXCLUDED.sync_generation, updated_at=now()
		RETURNING id, created_at, updated_at`
	return r.pool.QueryRow(ctx, query,
		res.ResourceType, res.ExternalID, res.Name, res.Namespace, res.ConnectorID,
		res.OwnerTeam, res.Environment, res.Status, res.Labels, res.Annotations,
		res.Metadata, tenantID, res.SyncGeneration, res.IntegrationID).Scan(&res.ID, &res.CreatedAt, &res.UpdatedAt)
}

// SyncSnapshot applies one connector discovery snapshot atomically. COPY keeps
// large catalogs out of the per-resource query path, while the transaction
// prevents a partial snapshot from orphaning resources that failed to write.
func (r *ResourceRepo) SyncSnapshot(ctx context.Context, connectorID, generation string, resources []domain.Resource) (ResourceSyncStats, error) {
	tenantID := repositoryTenant(ctx)
	stats := ResourceSyncStats{}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return stats, err
	}
	defer tx.Rollback(ctx)

	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))`, connectorID).Scan(&locked); err != nil {
		return stats, err
	}
	if !locked {
		return stats, ErrResourceSyncInProgress
	}
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '2s'`); err != nil {
		return stats, err
	}
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '30s'`); err != nil {
		return stats, err
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE resource_sync_stage (
		resource_type text NOT NULL, external_id text NOT NULL, name text NOT NULL, namespace text NOT NULL,
		connector_id text NOT NULL, owner_team text NOT NULL, environment text NOT NULL, status text NOT NULL,
		labels text NOT NULL, annotations text NOT NULL, metadata text NOT NULL, tenant_id text NOT NULL,
		sync_generation text NOT NULL, integration_id text NOT NULL
	) ON COMMIT DROP`); err != nil {
		return stats, err
	}

	deduplicated := make([]domain.Resource, 0, len(resources))
	positions := make(map[string]int, len(resources))
	for _, resource := range resources {
		key := resource.ResourceType + "\x00" + resource.ExternalID
		if position, exists := positions[key]; exists {
			deduplicated[position] = resource
			continue
		}
		positions[key] = len(deduplicated)
		deduplicated = append(deduplicated, resource)
	}
	rows := make([][]any, 0, len(deduplicated))
	for _, resource := range deduplicated {
		labels, err := json.Marshal(resource.Labels)
		if err != nil {
			return stats, fmt.Errorf("marshal resource labels: %w", err)
		}
		annotations, err := json.Marshal(resource.Annotations)
		if err != nil {
			return stats, fmt.Errorf("marshal resource annotations: %w", err)
		}
		metadata, err := json.Marshal(resource.Metadata)
		if err != nil {
			return stats, fmt.Errorf("marshal resource metadata: %w", err)
		}
		rows = append(rows, []any{
			resource.ResourceType, resource.ExternalID, resource.Name, resource.Namespace,
			connectorID, resource.OwnerTeam, resource.Environment, string(resource.Status),
			string(labels), string(annotations), string(metadata), tenantID, generation, resource.IntegrationID,
		})
	}
	if len(rows) > 0 {
		copied, err := tx.CopyFrom(ctx, pgx.Identifier{"resource_sync_stage"}, []string{
			"resource_type", "external_id", "name", "namespace", "connector_id", "owner_team", "environment", "status",
			"labels", "annotations", "metadata", "tenant_id", "sync_generation", "integration_id",
		}, pgx.CopyFromRows(rows))
		if err != nil {
			return stats, err
		}
		stats.Staged = int(copied)
	}

	command, err := tx.Exec(ctx, `INSERT INTO resources (resource_type, external_id, name, namespace, connector_id, owner_team,
		environment, status, labels, annotations, metadata, tenant_id, last_seen_at, sync_generation, integration_id)
		SELECT resource_type, external_id, name, namespace, connector_id::uuid, owner_team,
			environment, status::resource_status, labels::jsonb, annotations::jsonb, metadata::jsonb,
			tenant_id, now(), sync_generation, nullif(integration_id, '')::uuid
		FROM resource_sync_stage
		ON CONFLICT (connector_id, resource_type, external_id)
		DO UPDATE SET name=EXCLUDED.name, namespace=EXCLUDED.namespace,
			labels=EXCLUDED.labels, annotations=EXCLUDED.annotations, metadata=EXCLUDED.metadata,
			status=CASE
				WHEN resources.status='DISABLED' AND resources.metadata->>'connectorDeleted' IS DISTINCT FROM 'true'
				THEN resources.status ELSE 'ACTIVE'::resource_status END,
			tenant_id=EXCLUDED.tenant_id,
			integration_id=COALESCE(EXCLUDED.integration_id, resources.integration_id),
			last_seen_at=now(), sync_generation=EXCLUDED.sync_generation, updated_at=now()`)
	if err != nil {
		return stats, err
	}
	stats.Upserted = command.RowsAffected()

	command, err = tx.Exec(ctx, `UPDATE resources SET status='ORPHANED', updated_at=now()
		WHERE connector_id=$1 AND status='ACTIVE' AND sync_generation IS DISTINCT FROM $2`, connectorID, generation)
	if err != nil {
		return stats, err
	}
	stats.Orphaned = command.RowsAffected()
	if err := tx.Commit(ctx); err != nil {
		return stats, err
	}
	return stats, nil
}

func (r *ResourceRepo) MarkMissingFromSync(ctx context.Context, connectorID, generation string) error {
	_, err := r.pool.Exec(ctx, `UPDATE resources SET status='ORPHANED', updated_at=now()
		WHERE connector_id=$1 AND status='ACTIVE' AND sync_generation<>$2`, connectorID, generation)
	return err
}

// MarkMissingFromSyncIntegration orphans ACTIVE resources of a single
// integration (or native/connector-owned resources when integrationID is empty)
// that were not observed in the latest complete sync for that scope. Scoping by
// integration lets one integration's incomplete discovery pass avoid orphaning
// another integration's resources.
func (r *ResourceRepo) MarkMissingFromSyncIntegration(ctx context.Context, connectorID, integrationID, generation string) error {
	var err error
	if integrationID == "" {
		_, err = r.pool.Exec(ctx, `UPDATE resources SET status='ORPHANED', updated_at=now()
			WHERE connector_id=$1 AND integration_id IS NULL AND status='ACTIVE' AND sync_generation<>$2`,
			connectorID, generation)
	} else {
		_, err = r.pool.Exec(ctx, `UPDATE resources SET status='ORPHANED', updated_at=now()
			WHERE connector_id=$1 AND integration_id=$2 AND status='ACTIVE' AND sync_generation<>$3`,
			connectorID, integrationID, generation)
	}
	return err
}

// PurgeExpiredHistory removes unreferenced discovery history after a grace
// period. High-churn Kubernetes objects retain seven days; stable resource
// types retain thirty days. Rows used by policy, approval, or execution
// history are preserved so audit relationships remain intact.
func (r *ResourceRepo) PurgeExpiredHistory(ctx context.Context, connectorID string) (int64, error) {
	command, err := r.pool.Exec(ctx, `DELETE FROM resources res
		WHERE res.connector_id=$1 AND res.status='ORPHANED'
			AND ((res.resource_type IN ('pod','replicaset','event') AND res.updated_at < now() - interval '7 days')
				OR (res.resource_type NOT IN ('pod','replicaset','event') AND res.updated_at < now() - interval '30 days'))
			AND NOT EXISTS (SELECT 1 FROM policy_decisions pd WHERE pd.resource_id=res.id)
			AND NOT EXISTS (SELECT 1 FROM access_requests ar WHERE ar.resource_id=res.id)
			AND NOT EXISTS (SELECT 1 FROM executions ex WHERE ex.resource_id=res.id)`, connectorID)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

type ActionRepo struct {
	pool *pgxpool.Pool
}

func NewActionRepo(pool *pgxpool.Pool) *ActionRepo {
	return &ActionRepo{pool: pool}
}

func (r *ActionRepo) GetByName(ctx context.Context, name string) (*domain.ActionDefinition, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT id, name, version, COALESCE(display_name,''), COALESCE(description,''),
		resource_type, risk_level, execution_mode, executor_type, parameter_schema, timeout_seconds, tenant_id, created_at, updated_at
		FROM action_definitions WHERE name = $1 AND (tenant_id='platform' OR tenant_id=$2) ORDER BY version DESC LIMIT 1`
	row := r.pool.QueryRow(ctx, query, name, tenantID)

	var a domain.ActionDefinition
	err := row.Scan(&a.ID, &a.Name, &a.Version, &a.DisplayName, &a.Description,
		&a.ResourceType, &a.RiskLevel, &a.ExecutionMode, &a.ExecutorType, &a.ParameterSchema, &a.TimeoutSeconds,
		&a.TenantID, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (r *ActionRepo) GetByResourceType(ctx context.Context, resourceType string) ([]domain.ActionDefinition, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT DISTINCT ON (a.name) a.id, a.name, a.version, COALESCE(a.display_name,''), COALESCE(a.description,''),
		a.resource_type, a.risk_level, a.execution_mode, a.executor_type, a.parameter_schema, a.timeout_seconds, a.tenant_id, a.created_at, a.updated_at
		FROM action_definitions a
		WHERE a.resource_type = $1 AND (a.tenant_id='platform' OR a.tenant_id=$2)
		ORDER BY a.name, a.version DESC`
	rows, err := r.pool.Query(ctx, query, resourceType, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.ActionDefinition
	for rows.Next() {
		var a domain.ActionDefinition
		if err := rows.Scan(&a.ID, &a.Name, &a.Version, &a.DisplayName, &a.Description,
			&a.ResourceType, &a.RiskLevel, &a.ExecutionMode, &a.ExecutorType, &a.ParameterSchema, &a.TimeoutSeconds,
			&a.TenantID, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (r *ActionRepo) ListAvailable(ctx context.Context) ([]domain.ActionDefinition, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT DISTINCT ON (a.name) a.id, a.name, a.version, COALESCE(a.display_name,''), COALESCE(a.description,''),
		a.resource_type, a.risk_level, a.execution_mode, a.executor_type, a.parameter_schema, a.timeout_seconds,
		a.tenant_id, a.created_at, a.updated_at
		FROM action_definitions a
		WHERE (a.tenant_id='platform' OR a.tenant_id=$1)
		AND (
			(a.executor_type='control_plane' AND EXISTS (
				SELECT 1 FROM resources res
				WHERE res.resource_type=a.resource_type AND res.management_type='control_plane'
				  AND res.status='ACTIVE' AND res.tenant_id=$1))
			OR
			-- Concrete resource-backed actions are visible through the
			-- connector that owns at least one matching ACTIVE resource.
			(a.executor_type='connector' AND a.resource_type NOT IN ('provider_resource','kubernetes_resource')
			 AND EXISTS (
				SELECT 1 FROM resources res
				JOIN connectors c ON c.id=res.connector_id AND c.status='ACTIVE' AND c.archived_at IS NULL AND c.tenant_id=$1
				LEFT JOIN integrations ig ON ig.id=res.integration_id
				WHERE res.resource_type=a.resource_type AND res.status='ACTIVE' AND res.tenant_id=$1
				  AND (res.integration_id IS NULL OR ig.status='ACTIVE')
				  AND c.last_heartbeat IS NOT NULL AND c.last_heartbeat > now() - interval '90 seconds'
				  AND (c.capabilities ? a.name OR c.capabilities ? (a.name || '@v' || a.version::text))
				  AND (a.risk_level='low' OR c.capabilities ? 'warden.change-safety@v1')))
			OR
			-- The provider_resource action is synthetic and never owns real
			-- resources. It is advertised once any active connector declares
			-- the matching capability (provider.request/provider.write), so
			-- agents can discover it from the global catalog before selecting
			-- a concrete resource. High-risk writes additionally require the
			-- change-safety capability.
			(a.executor_type='connector' AND a.resource_type='provider_resource'
			 AND EXISTS (
				SELECT 1 FROM connectors pc
				WHERE pc.status='ACTIVE' AND pc.archived_at IS NULL AND pc.tenant_id=$1
				  AND pc.last_heartbeat IS NOT NULL AND pc.last_heartbeat > now() - interval '90 seconds'
				  AND (pc.capabilities ? a.name OR pc.capabilities ? (a.name || '@v' || a.version::text))
				  AND (a.risk_level='low' OR pc.capabilities ? 'warden.change-safety@v1')))
		)
		ORDER BY a.name, a.version DESC`
	rows, err := r.pool.Query(ctx, query, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ActionDefinition
	for rows.Next() {
		var a domain.ActionDefinition
		if err := rows.Scan(&a.ID, &a.Name, &a.Version, &a.DisplayName, &a.Description,
			&a.ResourceType, &a.RiskLevel, &a.ExecutionMode, &a.ExecutorType, &a.ParameterSchema, &a.TimeoutSeconds,
			&a.TenantID, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (r *ActionRepo) ListAll(ctx context.Context) ([]domain.ActionDefinition, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT DISTINCT ON (a.name) a.id, a.name, a.version, COALESCE(a.display_name,''), COALESCE(a.description,''),
		a.resource_type, a.risk_level, a.execution_mode, a.executor_type, a.parameter_schema, a.timeout_seconds,
		a.tenant_id, a.created_at, a.updated_at
		FROM action_definitions a WHERE (a.tenant_id='platform' OR a.tenant_id=$1)
		ORDER BY a.name, a.version DESC`
	rows, err := r.pool.Query(ctx, query, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ActionDefinition
	for rows.Next() {
		var a domain.ActionDefinition
		if err := rows.Scan(&a.ID, &a.Name, &a.Version, &a.DisplayName, &a.Description,
			&a.ResourceType, &a.RiskLevel, &a.ExecutionMode, &a.ExecutorType, &a.ParameterSchema, &a.TimeoutSeconds,
			&a.TenantID, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// SummariesByIDs batch-loads the display fields needed by execution and
// approval lists without forcing clients to download the full action catalog.
func (r *ActionRepo) SummariesByIDs(ctx context.Context, ids []string) (map[string]domain.ActionDefinition, error) {
	result := make(map[string]domain.ActionDefinition, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT id, name, COALESCE(display_name,''), risk_level
		FROM action_definitions WHERE id = ANY($1) AND (tenant_id='platform' OR tenant_id=$2)`, ids, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var action domain.ActionDefinition
		if err := rows.Scan(&action.ID, &action.Name, &action.DisplayName, &action.RiskLevel); err != nil {
			return nil, err
		}
		result[action.ID] = action
	}
	return result, rows.Err()
}

type ExecutionRepo struct {
	pool      *pgxpool.Pool
	adminPool *pgxpool.Pool
}

// SetAdminPool 配置合法跨租户旁路池（BYPASSRLS）。未设置时旁路回退主池。
func (r *ExecutionRepo) SetAdminPool(p *pgxpool.Pool) { r.adminPool = p }

// admin 返回旁路池：超管全局查询与后台 janitor 全表扫描须绕过 RLS。
func (r *ExecutionRepo) admin() *pgxpool.Pool {
	if r.adminPool != nil {
		return r.adminPool
	}
	return r.pool
}

type DecisionRepo struct {
	pool *pgxpool.Pool
}

func NewDecisionRepo(pool *pgxpool.Pool) *DecisionRepo {
	return &DecisionRepo{pool: pool}
}

func (r *DecisionRepo) Create(ctx context.Context, decision *domain.PolicyDecision) error {
	return createDecision(ctx, r.pool, decision)
}

func (r *DecisionRepo) CreateTx(ctx context.Context, tx pgx.Tx, decision *domain.PolicyDecision) error {
	return createDecision(ctx, tx, decision)
}

func createDecision(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, decision *domain.PolicyDecision) error {
	tenantID := repositoryTenant(ctx)
	return db.QueryRow(ctx, `INSERT INTO policy_decisions
		(tenant_id, principal_id, resource_id, action_id, acting_project_id, opa_input, opa_output, result,
		 bundle_hash, decision_seconds)
		VALUES ($1,$2,$3,$4,nullif($5,'')::uuid,$6,$7,$8,$9,$10) RETURNING id, created_at`,
		tenantID, decision.PrincipalID, decision.ResourceID, decision.ActionID,
		decision.ActingProjectID, decision.OPAInput, decision.OPAOutput, decision.Result,
		decision.BundleHash, decision.DecisionSeconds).Scan(&decision.ID, &decision.CreatedAt)
}

func (r *DecisionRepo) LinkExecution(ctx context.Context, id, executionID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE policy_decisions SET execution_id = $2
		WHERE id = $1 AND execution_id IS NULL`, id, executionID)
	return err
}

func (r *DecisionRepo) LinkExecutionTx(ctx context.Context, tx pgx.Tx, id, executionID string) error {
	_, err := tx.Exec(ctx, `UPDATE policy_decisions SET execution_id=$2 WHERE id=$1 AND execution_id IS NULL`, id, executionID)
	return err
}

func (r *DecisionRepo) OutputsByIDs(ctx context.Context, ids []string) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT id, opa_output FROM policy_decisions WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var output map[string]any
		if err := rows.Scan(&id, &output); err != nil {
			return nil, err
		}
		result[id] = output
	}
	return result, rows.Err()
}

func NewExecutionRepo(pool *pgxpool.Pool) *ExecutionRepo {
	return &ExecutionRepo{pool: pool}
}

func (r *ExecutionRepo) Create(ctx context.Context, e *domain.Execution) error {
	return createExecution(ctx, r.pool, e)
}

func (r *ExecutionRepo) CreateTx(ctx context.Context, tx pgx.Tx, e *domain.Execution) error {
	return createExecution(ctx, tx, e)
}

// ErrDuplicateIdempotency 表示并发下相同 (principal_id, idempotency_key) 已存在，
// createExecution 命中部分唯一索引后不插入（DO NOTHING）。调用方应回查幂等键
// 返回已存在的执行，而非报 500。
var ErrDuplicateIdempotency = errors.New("duplicate idempotency key")

func createExecution(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, e *domain.Execution) error {
	tenantID := repositoryTenant(ctx)
	query := `INSERT INTO executions (tenant_id, grant_id, principal_id, acting_project_id, external_context,
		delegation_token_id, on_behalf_of_principal_id, accountable_principal_id, credential_id, resource_id, action_id,
		parameters, parameter_hash, status, decision_id, idempotency_key, task_id,
		trace_id, reason, timeout_seconds, expires_at, connector_id, error_code, error_message,
		executor_type, pending_secret)
		VALUES ($1,$2,$3,nullif($4,'')::uuid,$5,nullif($6,'')::uuid,nullif($7,'')::uuid,nullif($8,'')::uuid,
		nullif($9,'')::uuid,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)
		ON CONFLICT (principal_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		RETURNING id, created_at, updated_at`
	err := db.QueryRow(ctx, query,
		tenantID, nullIf(e.GrantID), e.PrincipalID, e.ActingProjectID, e.ExternalContext,
		e.DelegationTokenID, e.OnBehalfOfPrincipalID, e.AccountablePrincipalID, e.CredentialID, e.ResourceID, e.ActionID,
		e.Parameters, e.ParameterHash, e.Status, nullIf(e.DecisionID),
		nullIf(e.IdempotencyKey), nullIf(e.TaskID), e.TraceID, e.Reason,
		e.TimeoutSeconds, e.ExpiresAt, nullIf(e.ConnectorID), nullIf(e.ErrorCode), nullIf(e.ErrorMessage),
		e.ExecutorType, e.PendingSecret).
		Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt)
	if err == pgx.ErrNoRows && e.IdempotencyKey != "" {
		return ErrDuplicateIdempotency
	}
	return err
}

func (r *ExecutionRepo) AttachGrantTx(ctx context.Context, tx pgx.Tx, id, grantID string, expiresAt time.Time) error {
	command, err := tx.Exec(ctx, `UPDATE executions SET grant_id=$2, expires_at=$3, updated_at=now()
		WHERE id=$1 AND tenant_id=$4 AND status='QUEUED' AND grant_id IS NULL`, id, grantID, expiresAt, repositoryTenant(ctx))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("execution %s cannot attach grant", id)
	}
	return nil
}

func (r *ExecutionRepo) GetByID(ctx context.Context, id string) (*domain.Execution, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT id, COALESCE(grant_id::text,''), principal_id, COALESCE(acting_project_id::text,''), external_context,
		COALESCE(delegation_token_id::text,''),COALESCE(on_behalf_of_principal_id::text,''),COALESCE(accountable_principal_id::text,''),
		COALESCE(credential_id::text,''), resource_id, action_id,
		parameters, parameter_hash, status, COALESCE(decision_id::text,''),
		COALESCE(idempotency_key,''), COALESCE(task_id,''), trace_id,
		COALESCE(reason,''), timeout_seconds, expires_at, COALESCE(connector_id::text,''),
		result, COALESCE(error_code,''), COALESCE(error_message,''),
		executor_type, pending_secret,
		created_at, updated_at
		FROM executions WHERE id = $1 AND tenant_id = $2`
	row := r.pool.QueryRow(ctx, query, id, tenantID)

	var e domain.Execution
	err := row.Scan(&e.ID, &e.GrantID, &e.PrincipalID, &e.ActingProjectID, &e.ExternalContext,
		&e.DelegationTokenID, &e.OnBehalfOfPrincipalID, &e.AccountablePrincipalID, &e.CredentialID, &e.ResourceID, &e.ActionID,
		&e.Parameters, &e.ParameterHash, &e.Status, &e.DecisionID,
		&e.IdempotencyKey, &e.TaskID, &e.TraceID,
		&e.Reason, &e.TimeoutSeconds, &e.ExpiresAt, &e.ConnectorID,
		&e.Result, &e.ErrorCode, &e.ErrorMessage,
		&e.ExecutorType, &e.PendingSecret,
		&e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

func (r *ExecutionRepo) GetByIdempotencyKey(ctx context.Context, principalID, key string) (*domain.Execution, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT id, COALESCE(grant_id::text,''), principal_id, COALESCE(acting_project_id::text,''), external_context,
		COALESCE(delegation_token_id::text,''),COALESCE(on_behalf_of_principal_id::text,''),COALESCE(accountable_principal_id::text,''),
		COALESCE(credential_id::text,''), resource_id, action_id,
		parameters, parameter_hash, status, COALESCE(decision_id::text,''),
		COALESCE(idempotency_key,''), COALESCE(task_id,''), trace_id,
		COALESCE(reason,''), timeout_seconds, expires_at, COALESCE(connector_id::text,''),
		result, COALESCE(error_code,''), COALESCE(error_message,''),
		executor_type, pending_secret,
		created_at, updated_at
		FROM executions WHERE principal_id = $1 AND idempotency_key = $2 AND tenant_id=$3 LIMIT 1`
	row := r.pool.QueryRow(ctx, query, principalID, key, tenantID)

	var e domain.Execution
	err := row.Scan(&e.ID, &e.GrantID, &e.PrincipalID, &e.ActingProjectID, &e.ExternalContext,
		&e.DelegationTokenID, &e.OnBehalfOfPrincipalID, &e.AccountablePrincipalID, &e.CredentialID, &e.ResourceID, &e.ActionID,
		&e.Parameters, &e.ParameterHash, &e.Status, &e.DecisionID,
		&e.IdempotencyKey, &e.TaskID, &e.TraceID,
		&e.Reason, &e.TimeoutSeconds, &e.ExpiresAt, &e.ConnectorID,
		&e.Result, &e.ErrorCode, &e.ErrorMessage,
		&e.ExecutorType, &e.PendingSecret,
		&e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// DatabaseQueryPreview is the trusted cost decision emitted by a successful
// Connector preview. The control plane scopes lookups to the same principal,
// resource and binding-aware preview hash before it can influence execution.
type DatabaseQueryPreview struct {
	ExecutionID string
	Verdict     string
	PreviewHash string
	Result      map[string]any
	UpdatedAt   time.Time
}

// GetRecentDatabaseQueryPreview returns the newest successful ad-hoc query
// preview matching the exact principal/resource/fingerprint tuple. Preview
// results are Connector-authenticated and immutable after finalization.
func (r *ExecutionRepo) GetRecentDatabaseQueryPreview(ctx context.Context, principalID, resourceID, actingProjectID, previewHash string, maxAge time.Duration) (*DatabaseQueryPreview, error) {
	row := r.pool.QueryRow(ctx, `SELECT e.id, e.result->>'verdict', e.result->>'previewHash', e.result, e.updated_at
		FROM executions e
		JOIN action_definitions a ON a.id=e.action_id
		WHERE e.tenant_id=$6 AND e.principal_id=$1 AND e.resource_id=$2 AND e.acting_project_id=$3
			AND a.name='database.query.preview' AND a.version=1
			AND e.status='SUCCEEDED'
			AND e.result->>'previewHash'=$4
			AND e.updated_at >= now() - ($5 * interval '1 second')
		ORDER BY e.updated_at DESC LIMIT 1`, principalID, resourceID, actingProjectID, previewHash, maxAge.Seconds(), repositoryTenant(ctx))
	var preview DatabaseQueryPreview
	if err := row.Scan(&preview.ExecutionID, &preview.Verdict, &preview.PreviewHash, &preview.Result, &preview.UpdatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &preview, nil
}

// TransitionStatus applies a compare-and-set state transition. Callers must
// name the expected source state so concurrent terminal updates cannot overwrite
// one another.
func (r *ExecutionRepo) TransitionStatus(ctx context.Context, id string, from, to domain.ExecutionStatus, errorCode, errorMessage string) (bool, error) {
	command, err := r.pool.Exec(ctx, `UPDATE executions
		SET status=$3, error_code=$4, error_message=$5, updated_at=now()
		WHERE id=$1 AND tenant_id=$6 AND status=$2`, id, from, to, nullIf(errorCode), nullIf(errorMessage), repositoryTenant(ctx))
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

func (r *ExecutionRepo) TransitionStatusTx(ctx context.Context, tx pgx.Tx, id string, from, to domain.ExecutionStatus, errorCode, errorMessage string) (bool, error) {
	command, err := tx.Exec(ctx, `UPDATE executions SET status=$3,error_code=$4,error_message=$5,updated_at=now()
		WHERE id=$1 AND tenant_id=$6 AND status=$2`, id, from, to, nullIf(errorCode), nullIf(errorMessage), repositoryTenant(ctx))
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

// CompleteControlPlaneTx atomically finalizes a control-plane inline execution
// (credential.*). It requires the execution to be flagged control_plane and to
// be in the expected source state, and it clears pending_secret, grant_id and
// expires_at so the approval-time plaintext ciphertext is wiped on completion.
func (r *ExecutionRepo) CompleteControlPlaneTx(ctx context.Context, tx pgx.Tx, id string, from, to domain.ExecutionStatus, result map[string]any, errorCode, errorMessage string) (bool, error) {
	command, err := tx.Exec(ctx, `UPDATE executions
		SET status=$2, result=$3, error_code=$4, error_message=$5,
			pending_secret=NULL, grant_id=NULL, expires_at=NULL, updated_at=now()
		WHERE id=$1 AND tenant_id=$7 AND executor_type='control_plane' AND status=$6`,
		id, to, result, nullIf(errorCode), nullIf(errorMessage), from, repositoryTenant(ctx))
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

// Finalize atomically stores a Connector result and moves a DISPATCHED
// execution to a terminal state. This is the only result-writing path.
func (r *ExecutionRepo) Finalize(ctx context.Context, id, dispatchToken string, status domain.ExecutionStatus, result map[string]any, errorCode, errorMessage string) (bool, error) {
	tenantID := repositoryTenant(ctx)
	command, err := r.pool.Exec(ctx, `UPDATE executions
		SET status=$3, result=$4, error_code=$5, error_message=$6,
			dispatch_token_hash=NULL, dispatch_expires_at=NULL, updated_at=now()
		WHERE id=$1 AND dispatch_token_hash=$2 AND status='DISPATCHED'
			AND tenant_id=$7 AND dispatch_expires_at >= now()`, id, domain.HashDispatchToken(dispatchToken), status, result, nullIf(errorCode), nullIf(errorMessage), tenantID)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

// FinalizeWithLease atomically persists a terminal result and its credential
// lease. It is used for Connector responses so a successful action cannot be
// recorded without the lease needed for audit and revocation.
func (r *ExecutionRepo) FinalizeWithLease(ctx context.Context, id, dispatchToken string, status domain.ExecutionStatus, result map[string]any, errorCode, errorMessage string, provider, fingerprint string, leaseExpiresAt *time.Time, audit *domain.AuditEvent) (bool, error) {
	tenantID := repositoryTenant(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE executions
		SET status=$3, result=$4, error_code=$5, error_message=$6,
			dispatch_token_hash=NULL, dispatch_expires_at=NULL, updated_at=now()
		WHERE id=$1 AND dispatch_token_hash=$2 AND status='DISPATCHED'
			AND tenant_id=$7 AND dispatch_expires_at >= now()`, id, domain.HashDispatchToken(dispatchToken), status, result, nullIf(errorCode), nullIf(errorMessage), tenantID)
	if err != nil || command.RowsAffected() != 1 {
		return command.RowsAffected() == 1, err
	}
	if provider != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO credential_leases
			(execution_id, provider, role_name, fingerprint, status, issued_at, expires_at)
			VALUES ($1,$2,$3,$4,'ACTIVE',now(),$5)`, id, provider, "warden-executor", fingerprint, leaseExpiresAt); err != nil {
			return false, err
		}
	}
	if audit != nil {
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, action, actor_id, actor_type, acting_project_id, credential_id, resource_id, execution_id,
			grant_id, decision_id, connector_id, trace_id, target_id, summary, metadata)
			VALUES ($1,$2,$3,$4,nullif($5,'')::uuid,nullif($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, tenantID, audit.Action,
			nullIf(audit.ActorID), nullIf(audit.ActorType), audit.ActingProjectID, audit.CredentialID, nullIf(audit.ResourceID),
			nullIf(audit.ExecutionID), nullIf(audit.GrantID), nullIf(audit.DecisionID), nullIf(audit.ConnectorID),
			nullIf(audit.TraceID), nullIf(audit.TargetID), audit.Summary, audit.Metadata); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (r *ExecutionRepo) UpdateResult(ctx context.Context, id string, result map[string]any) error {
	query := `UPDATE executions SET result = $2, updated_at = now() WHERE id = $1 AND tenant_id=$3`
	_, err := r.pool.Exec(ctx, query, id, result, repositoryTenant(ctx))
	return err
}

// PurgeTransientLogResults removes raw cloud-log and database-row payloads
// after the Agent had enough time to collect them. Execution parameters,
// status, identity, query target and audit records remain, so Warden retains
// accountability without becoming a secondary log or business-data archive.
func (r *ExecutionRepo) PurgeTransientLogResults(ctx context.Context, olderThan time.Duration) (int64, error) {
	command, err := r.admin().Exec(ctx, `UPDATE executions e
		SET result = jsonb_build_object('purged', true, 'reason', 'transient_query_result_expired')
		FROM action_definitions a
		WHERE e.action_id=a.id AND a.name IN ('huawei.lts.logs.query','aliyun.sls.logs.query','database.table.rows.query','database.query.execute','database.query_template.execute')
			AND e.status IN ('SUCCEEDED','FAILED','UNKNOWN')
			AND e.result IS NOT NULL AND NOT (e.result ? 'purged')
			AND e.updated_at < now() - ($1 * interval '1 second')`, olderThan.Seconds())
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

func (r *ExecutionRepo) Authorize(ctx context.Context, id, grantID string, expiresAt time.Time) error {
	command, err := r.pool.Exec(ctx, `UPDATE executions
		SET grant_id = $2, status = 'QUEUED', expires_at = $3, error_code = NULL,
			error_message = NULL, updated_at = now()
		WHERE id = $1 AND tenant_id=$4 AND status = 'WAITING_APPROVAL'`, id, grantID, expiresAt, repositoryTenant(ctx))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("execution %s is not waiting for approval", id)
	}
	return nil
}

func (r *ExecutionRepo) AuthorizeTx(ctx context.Context, tx pgx.Tx, id, grantID string, expiresAt time.Time) error {
	command, err := tx.Exec(ctx, `UPDATE executions SET grant_id=$2,status='QUEUED',expires_at=$3,
		error_code=NULL,error_message=NULL,updated_at=now() WHERE id=$1 AND tenant_id=$4 AND status='WAITING_APPROVAL'`, id, grantID, expiresAt, repositoryTenant(ctx))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("execution %s is not waiting for approval", id)
	}
	return nil
}

func (r *ExecutionRepo) AttachGrant(ctx context.Context, id, grantID string, expiresAt time.Time) error {
	command, err := r.pool.Exec(ctx, `UPDATE executions
		SET grant_id = $2, expires_at = $3, updated_at = now()
		WHERE id = $1 AND tenant_id=$4 AND status = 'QUEUED' AND grant_id IS NULL`, id, grantID, expiresAt, repositoryTenant(ctx))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("execution %s cannot attach grant", id)
	}
	return nil
}

func (r *ExecutionRepo) List(ctx context.Context, principalID, status string, limit, offset int) ([]domain.Execution, error) {
	tenantID := repositoryTenant(ctx)
	var query string
	var args []any
	if status != "" {
		query = `SELECT id, COALESCE(grant_id::text,''), principal_id, COALESCE(acting_project_id::text,''), external_context,
			COALESCE(delegation_token_id::text,''), COALESCE(on_behalf_of_principal_id::text,''), COALESCE(accountable_principal_id::text,''), COALESCE(credential_id::text,''), resource_id, action_id,
			parameters, parameter_hash, status, COALESCE(decision_id::text,''),
			COALESCE(idempotency_key,''), COALESCE(task_id,''), trace_id,
			COALESCE(reason,''), timeout_seconds, expires_at, COALESCE(connector_id::text,''),
			result, COALESCE(error_code,''), COALESCE(error_message,''),
			executor_type, pending_secret,
			created_at, updated_at
			FROM executions WHERE tenant_id=$1 AND ($2='' OR principal_id=$2::uuid) AND status = $3
			ORDER BY created_at DESC LIMIT $4 OFFSET $5`
		args = []any{tenantID, principalID, status, limit, offset}
	} else {
		query = `SELECT id, COALESCE(grant_id::text,''), principal_id, COALESCE(acting_project_id::text,''), external_context,
			COALESCE(delegation_token_id::text,''), COALESCE(on_behalf_of_principal_id::text,''), COALESCE(accountable_principal_id::text,''), COALESCE(credential_id::text,''), resource_id, action_id,
			parameters, parameter_hash, status, COALESCE(decision_id::text,''),
			COALESCE(idempotency_key,''), COALESCE(task_id,''), trace_id,
			COALESCE(reason,''), timeout_seconds, expires_at, COALESCE(connector_id::text,''),
			result, COALESCE(error_code,''), COALESCE(error_message,''),
			executor_type, pending_secret,
			created_at, updated_at
			FROM executions WHERE tenant_id=$1 AND ($2='' OR principal_id=$2::uuid)
			ORDER BY created_at DESC LIMIT $3 OFFSET $4`
		args = []any{tenantID, principalID, limit, offset}
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.Execution
	for rows.Next() {
		var e domain.Execution
		if err := rows.Scan(&e.ID, &e.GrantID, &e.PrincipalID, &e.ActingProjectID, &e.ExternalContext,
			&e.DelegationTokenID, &e.OnBehalfOfPrincipalID, &e.AccountablePrincipalID, &e.CredentialID, &e.ResourceID, &e.ActionID,
			&e.Parameters, &e.ParameterHash, &e.Status, &e.DecisionID,
			&e.IdempotencyKey, &e.TaskID, &e.TraceID,
			&e.Reason, &e.TimeoutSeconds, &e.ExpiresAt, &e.ConnectorID,
			&e.Result, &e.ErrorCode, &e.ErrorMessage,
			&e.ExecutorType, &e.PendingSecret,
			&e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// ListAllTenants is the super-admin visibility path: every execution across
// every tenant, each row carrying its tenant_id so the console can label the
// owning organization. Never reachable by tenant-scoped identities.
func (r *ExecutionRepo) ListAllTenants(ctx context.Context, status string, limit, offset int) ([]domain.Execution, error) {
	query := `SELECT tenant_id, id, COALESCE(grant_id::text,''), principal_id, COALESCE(acting_project_id::text,''), external_context,
		COALESCE(delegation_token_id::text,''), COALESCE(on_behalf_of_principal_id::text,''), COALESCE(accountable_principal_id::text,''), COALESCE(credential_id::text,''), resource_id, action_id,
		parameters, parameter_hash, status, COALESCE(decision_id::text,''),
		COALESCE(idempotency_key,''), COALESCE(task_id,''), trace_id,
		COALESCE(reason,''), timeout_seconds, expires_at, COALESCE(connector_id::text,''),
		result, COALESCE(error_code,''), COALESCE(error_message,''),
		executor_type, pending_secret,
		created_at, updated_at
		FROM executions`
	args := []any{}
	if status != "" {
		query += ` WHERE status=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		args = append(args, status, limit, offset)
	} else {
		query += ` ORDER BY created_at DESC LIMIT $1 OFFSET $2`
		args = append(args, limit, offset)
	}
	rows, err := r.admin().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Execution
	for rows.Next() {
		var e domain.Execution
		if err := rows.Scan(&e.TenantID, &e.ID, &e.GrantID, &e.PrincipalID, &e.ActingProjectID, &e.ExternalContext,
			&e.DelegationTokenID, &e.OnBehalfOfPrincipalID, &e.AccountablePrincipalID, &e.CredentialID, &e.ResourceID, &e.ActionID,
			&e.Parameters, &e.ParameterHash, &e.Status, &e.DecisionID,
			&e.IdempotencyKey, &e.TaskID, &e.TraceID,
			&e.Reason, &e.TimeoutSeconds, &e.ExpiresAt, &e.ConnectorID,
			&e.Result, &e.ErrorCode, &e.ErrorMessage,
			&e.ExecutorType, &e.PendingSecret,
			&e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// TraceExecution is a diagnostic projection of one execution within a session
// trace. It deliberately omits every internal identifier (execution, action,
// grant, decision, credential) so the aggregation view never leaks them.
type TraceExecution struct {
	PrincipalID  string
	Status       domain.ExecutionStatus
	ErrorCode    string
	Result       map[string]any
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ResourceName string
	ResourceType string
	ProjectName  string
	Environment  string
}

// ListByTraceID returns every execution sharing a session trace, ordered by
// creation time, joined with the resource's business identifiers (name, type,
// project, environment). It never filters by resource or connector liveness so
// historical traces remain viewable after a connector is archived.
func (r *ExecutionRepo) ListByTraceID(ctx context.Context, traceID string) ([]TraceExecution, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT e.principal_id::text,e.status, COALESCE(e.error_code,''), e.result, e.created_at, e.updated_at,
		r.name, r.resource_type,
		COALESCE(op.name, tp.name, rp.name, ip.name, cp.name, dp.name, ''),
		COALESCE(reo.environment, NULLIF(ig.environment,''), c.environment)
		FROM executions e
		JOIN resources r ON r.id = e.resource_id
		JOIN connectors c ON c.id = r.connector_id
		LEFT JOIN resource_project_overrides rpo ON rpo.resource_id = r.id
		LEFT JOIN projects op ON op.id = rpo.project_id
		LEFT JOIN integrations ig ON ig.id = r.integration_id
		LEFT JOIN projects tp ON tp.id = CASE
			WHEN r.integration_id IS NOT NULL AND COALESCE(r.labels->>'warden-project-id', r.metadata->>'warden-project-id','')
				~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
			THEN COALESCE(r.labels->>'warden-project-id', r.metadata->>'warden-project-id')::uuid ELSE NULL END
			AND (tp.id=ig.project_id OR EXISTS (SELECT 1 FROM integration_project_bindings binding
				WHERE binding.integration_id=ig.id AND binding.project_id=tp.id))
		LEFT JOIN project_namespace_rules pnr ON r.integration_id IS NULL
			AND c.connector_type='kubernetes' AND pnr.connector_id=r.connector_id AND pnr.namespace=r.namespace
		LEFT JOIN projects rp ON rp.id=pnr.project_id
		LEFT JOIN projects ip ON ip.id=ig.project_id
		LEFT JOIN projects cp ON cp.id=c.project_id AND r.integration_id IS NULL
		LEFT JOIN projects dp ON dp.is_default AND dp.tenant_id=$2
		LEFT JOIN resource_environment_overrides reo ON reo.resource_id = r.id
		WHERE e.trace_id = $1 AND e.tenant_id=$2 AND r.tenant_id=$2 AND c.tenant_id=$2
		ORDER BY e.created_at ASC`
	rows, err := r.pool.Query(ctx, query, traceID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []TraceExecution
	for rows.Next() {
		var item TraceExecution
		if err := rows.Scan(&item.PrincipalID, &item.Status, &item.ErrorCode, &item.Result, &item.CreatedAt, &item.UpdatedAt,
			&item.ResourceName, &item.ResourceType, &item.ProjectName, &item.Environment); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *ExecutionRepo) ClaimTasks(ctx context.Context, connectorID string, limit int) ([]domain.TaskEnvelope, error) {
	tenantID := repositoryTenant(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	query := `WITH claimed AS (
		SELECT e.id FROM executions e
		JOIN access_grants g ON g.id = e.grant_id
		JOIN principals p ON p.id=e.principal_id
		JOIN resources claim_res ON claim_res.id=e.resource_id
		LEFT JOIN principal_api_keys k ON k.id=e.credential_id
		WHERE e.connector_id = $1 AND e.tenant_id=$2 AND e.status = 'QUEUED' AND e.executor_type = 'connector'
			AND g.tenant_id=$2 AND p.tenant_id=$2 AND claim_res.tenant_id=$2
			AND g.acting_project_id IS NOT DISTINCT FROM e.acting_project_id
			AND g.status = 'ACTIVE' AND g.expires_at > now() AND g.use_count < g.max_uses
			AND p.status='ACTIVE'
			AND (e.credential_id IS NULL OR (k.status='ACTIVE' AND (k.expires_at IS NULL OR k.expires_at>now())))
			AND claim_res.status='ACTIVE' AND claim_res.connector_id=e.connector_id
			AND claim_res.metadata->>'integrationDeleted' IS DISTINCT FROM 'true'
			AND claim_res.metadata->>'connectorDeleted' IS DISTINCT FROM 'true'
		ORDER BY e.created_at
		FOR UPDATE OF e SKIP LOCKED LIMIT $3
	)
		UPDATE executions e SET status = 'DISPATCHED', dispatch_token=gen_random_uuid(),
			dispatch_expires_at=g.expires_at,
			dispatched_at=now(), attempt=e.attempt+1, updated_at = now()
	FROM claimed c, resources res, action_definitions act, access_grants g
	WHERE e.id = c.id AND e.tenant_id=$2 AND res.id = e.resource_id AND res.tenant_id=$2
		AND act.id = e.action_id AND (act.tenant_id=$2 OR act.tenant_id='platform' OR act.tenant_id='') AND g.id = e.grant_id
	RETURNING e.id, e.connector_id, res.id, res.external_id, res.resource_type, res.name,
		COALESCE(res.namespace,''), COALESCE(res.integration_id::text,''), act.name, act.version, act.risk_level,
			e.parameters, e.parameter_hash, g.id, COALESCE(g.request_id::text,''), g.scope_hash, g.expires_at, e.trace_id,
			e.dispatch_token::text, e.attempt`
	rows, err := tx.Query(ctx, query, connectorID, tenantID, limit)
	if err != nil {
		return nil, err
	}
	var tasks []domain.TaskEnvelope
	var grantIDs []string
	for rows.Next() {
		var task domain.TaskEnvelope
		var risk domain.RiskLevel
		var approvalRequestID string
		task.Version = 1
		task.IssuedAt = time.Now().UTC()
		if err := rows.Scan(&task.ExecutionID, &task.ConnectorID,
			&task.Resource.ID, &task.Resource.ExternalID, &task.Resource.Type,
			&task.Resource.Name, &task.Resource.Namespace, &task.Resource.IntegrationID,
			&task.Action.Name, &task.Action.Version, &risk, &task.Parameters, &task.ParameterHash,
			&task.Grant.ID, &approvalRequestID, &task.Grant.ScopeHash, &task.Grant.ExpiresAt, &task.TraceID,
			&task.DispatchToken, &task.Attempt); err != nil {
			rows.Close()
			return nil, err
		}
		contract := domain.ActionSafetyContract(task.Action.Name, risk)
		if contract.Change {
			task.Safety = &contract
		}
		if task.Action.Name == "database.query.execute" && approvalRequestID != "" {
			if task.Parameters == nil {
				task.Parameters = make(map[string]any)
			}
			task.Parameters[domain.QueryApprovalContextParameter] = map[string]any{"requestId": approvalRequestID}
			task.ParameterHash = domain.HashParameters(task.Parameters)
		}
		if task.Action.Name == "provider.request" || task.Action.Name == "provider.write" {
			requestPayload, requestOK := task.Parameters["providerRequest"]
			bindingPayload, bindingOK := task.Parameters["providerBinding"]
			if !requestOK || !bindingOK {
				rows.Close()
				return nil, fmt.Errorf("provider execution %s is missing its signed binding", task.ExecutionID)
			}
			payload, marshalErr := json.Marshal(map[string]any{"request": requestPayload, "binding": bindingPayload})
			if marshalErr != nil || json.Unmarshal(payload, &task.Provider) != nil {
				rows.Close()
				return nil, fmt.Errorf("provider execution %s has an invalid signed binding", task.ExecutionID)
			}
		}
		tasks = append(tasks, task)
		grantIDs = append(grantIDs, task.Grant.ID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// 令牌明文仅当次下发，落库存哈希防库只读者可回放（rows 已关闭，连接空闲可安全写）。
	for _, task := range tasks {
		if _, err := tx.Exec(ctx, `UPDATE executions SET dispatch_token_hash=$1, dispatch_token=NULL WHERE id=$2`,
			domain.HashDispatchToken(task.DispatchToken), task.ExecutionID); err != nil {
			return nil, err
		}
	}
	for _, grantID := range grantIDs {
		if _, err := tx.Exec(ctx, `UPDATE access_grants SET use_count = use_count + 1,
			status = CASE WHEN use_count + 1 >= max_uses THEN 'USED' ELSE status END
			WHERE id = $1 AND tenant_id=$2`, grantID, tenantID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return tasks, nil
}

// ExpireDispatchLeases resolves tasks whose delivery or result acknowledgement
// was lost. UNKNOWN is fail-safe because the target may already have observed
// a side effect; an operator can inspect it without an unsafe automatic retry.
func (r *ExecutionRepo) ExpireDispatchLeases(ctx context.Context) (int64, error) {
	command, err := r.admin().Exec(ctx, `UPDATE executions
		SET status='UNKNOWN', error_code='DISPATCH_LEASE_EXPIRED',
			error_message='任务分发租约过期，目标系统可能已经执行',
			dispatch_token_hash=NULL, dispatch_expires_at=NULL, updated_at=now()
		WHERE status='DISPATCHED' AND (dispatch_expires_at < now()
			OR (dispatch_expires_at IS NULL AND updated_at < now()-interval '5 minutes'))`)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

// ExpireQueuedExecutions prevents tasks with expired or revoked grants from
// remaining QUEUED forever after a Connector goes offline.
func (r *ExecutionRepo) ExpireQueuedExecutions(ctx context.Context) (int64, error) {
	tx, err := r.admin().Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE executions e
		SET status='EXPIRED',error_code='EXECUTION_GRANT_EXPIRED',
			error_message='执行授权已过期或被撤销',updated_at=now()
		FROM access_grants g
		WHERE e.grant_id=g.id AND e.status='QUEUED'
			AND (e.expires_at<=now() OR g.expires_at<=now() OR g.status<>'ACTIVE')`)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE access_grants SET status='EXPIRED'
		WHERE status='ACTIVE' AND expires_at<=now()`); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

type GrantRepo struct {
	pool *pgxpool.Pool
}

func NewGrantRepo(pool *pgxpool.Pool) *GrantRepo {
	return &GrantRepo{pool: pool}
}

func (r *GrantRepo) Create(ctx context.Context, g *domain.AccessGrant) error {
	return createGrant(ctx, r.pool, g)
}

func (r *GrantRepo) CreateTx(ctx context.Context, tx pgx.Tx, g *domain.AccessGrant) error {
	return createGrant(ctx, tx, g)
}

func createGrant(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, g *domain.AccessGrant) error {
	tenantID := repositoryTenant(ctx)
	query := `INSERT INTO access_grants (tenant_id, request_id, principal_id, acting_project_id, resource_scope, action_scope,
		parameter_hash, scope_hash, max_uses, ttl_seconds, expires_at, status)
		VALUES ($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, use_count, created_at`
	return db.QueryRow(ctx, query,
		tenantID, nullIf(g.RequestID), g.PrincipalID, g.ActingProjectID, g.ResourceScope, g.ActionScope,
		g.ParameterHash, g.ScopeHash, g.MaxUses, g.TTLSeconds, g.ExpiresAt, g.Status).
		Scan(&g.ID, &g.UseCount, &g.CreatedAt)
}

func (r *GrantRepo) GetByID(ctx context.Context, id string) (*domain.AccessGrant, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT id, COALESCE(request_id::text,''), principal_id, COALESCE(acting_project_id::text,''), resource_scope, action_scope,
		parameter_hash, scope_hash, max_uses, use_count, ttl_seconds, expires_at, status, created_at
		FROM access_grants WHERE id = $1 AND tenant_id = $2`
	row := r.pool.QueryRow(ctx, query, id, tenantID)

	var g domain.AccessGrant
	err := row.Scan(&g.ID, &g.RequestID, &g.PrincipalID, &g.ActingProjectID, &g.ResourceScope, &g.ActionScope,
		&g.ParameterHash, &g.ScopeHash, &g.MaxUses, &g.UseCount, &g.TTLSeconds, &g.ExpiresAt, &g.Status, &g.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &g, nil
}

func (r *GrantRepo) UpdateStatus(ctx context.Context, id string, status domain.GrantStatus) error {
	_, err := r.pool.Exec(ctx, `UPDATE access_grants SET status = $2 WHERE id = $1 AND tenant_id=$3`, id, status, repositoryTenant(ctx))
	return err
}

type AuditRepo struct {
	pool *pgxpool.Pool
}

func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo {
	return &AuditRepo{pool: pool}
}

func (r *AuditRepo) Create(ctx context.Context, e *domain.AuditEvent) error {
	return createAudit(ctx, r.pool, e)
}

func (r *AuditRepo) CreateTx(ctx context.Context, tx pgx.Tx, e *domain.AuditEvent) error {
	return createAudit(ctx, tx, e)
}

func createAudit(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, e *domain.AuditEvent) error {
	tenantID := repositoryTenant(ctx)
	query := `INSERT INTO audit_events (tenant_id, action, actor_id, actor_type, acting_project_id, credential_id, resource_id, execution_id,
		grant_id, decision_id, connector_id, trace_id, target_id, summary, metadata)
		VALUES ($1,$2,$3,$4,nullif($5,'')::uuid,nullif($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id, created_at`
	return db.QueryRow(ctx, query, tenantID, e.Action,
		nullIf(e.ActorID), nullIf(e.ActorType), e.ActingProjectID, e.CredentialID, nullIf(e.ResourceID),
		nullIf(e.ExecutionID), nullIf(e.GrantID), nullIf(e.DecisionID),
		nullIf(e.ConnectorID), nullIf(e.TraceID), nullIf(e.TargetID),
		e.Summary, e.Metadata).Scan(&e.ID, &e.CreatedAt)
}

type AuditListFilter struct {
	Action                 string
	Category               string
	ExecutionID            string
	TraceID                string
	ConnectorID            string
	Query                  string
	ExternalSource         string
	ExternalWorkspaceID    string
	ExternalProjectID      string
	ExternalAgentID        string
	ExternalTaskID         string
	ExternalIssueID        string
	OriginatorUserID       string
	DelegationTokenID      string
	OnBehalfOfPrincipalID  string
	AccountablePrincipalID string
}

func (r *AuditRepo) List(ctx context.Context, filter AuditListFilter, limit, offset int) ([]domain.AuditEvent, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT id, action, COALESCE(actor_id::text,''), COALESCE(actor_type,''), COALESCE(acting_project_id::text,''), COALESCE(credential_id::text,''),
		COALESCE(resource_id::text,''), COALESCE(execution_id::text,''), COALESCE(grant_id::text,''),
		COALESCE(decision_id::text,''), COALESCE(connector_id::text,''), COALESCE(trace_id,''),
		COALESCE(target_id,''), COALESCE(summary,''), metadata, created_at
		FROM audit_events
		WHERE tenant_id=$1
		  AND ($2='' OR action::text=$2)
		  AND ($3='' OR ($3='system' AND split_part(action::text,'.',1) NOT IN
		       ('execution','access_request','grant','connector','policy','permission_drift'))
		       OR ($3<>'system' AND action::text LIKE $3 || '.%'))
		  AND ($4='' OR execution_id::text=$4 OR execution_id::text LIKE $4 || '%')
		  AND ($5='' OR trace_id=$5 OR trace_id LIKE $5 || '%')
		  AND ($6='' OR connector_id::text=$6 OR connector_id::text LIKE $6 || '%')
		  AND ($7='' OR action::text ILIKE '%' || $7 || '%' OR COALESCE(summary,'') ILIKE '%' || $7 || '%'
		       OR COALESCE(target_id,'') ILIKE '%' || $7 || '%' OR COALESCE(trace_id,'') ILIKE '%' || $7 || '%'
		       OR COALESCE(execution_id::text,'') ILIKE '%' || $7 || '%'
		       OR metadata::text ILIKE '%' || $7 || '%')
		  AND ($8='' OR metadata->'external_context'->>'source'=$8)
		  AND ($9='' OR metadata->'external_context'->>'workspaceId'=$9)
		  AND ($10='' OR metadata->'external_context'->>'projectId'=$10)
		  AND ($11='' OR metadata->'external_context'->>'agentId'=$11)
		  AND ($12='' OR metadata->'external_context'->>'taskId'=$12)
		  AND ($13='' OR metadata->'external_context'->>'issueId'=$13)
		  AND ($14='' OR metadata->'external_context'->>'originatorUserId'=$14)
		  AND ($15='' OR metadata->>'delegation_token_id'=$15)
		  AND ($16='' OR metadata->>'on_behalf_of_principal_id'=$16)
		  AND ($17='' OR metadata->>'accountable_principal_id'=$17)
		ORDER BY created_at DESC LIMIT $18 OFFSET $19`
	rows, err := r.pool.Query(ctx, query, tenantID, filter.Action, filter.Category, filter.ExecutionID,
		filter.TraceID, filter.ConnectorID, filter.Query, filter.ExternalSource, filter.ExternalWorkspaceID,
		filter.ExternalProjectID, filter.ExternalAgentID, filter.ExternalTaskID, filter.ExternalIssueID,
		filter.OriginatorUserID, filter.DelegationTokenID, filter.OnBehalfOfPrincipalID,
		filter.AccountablePrincipalID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.AuditEvent
	for rows.Next() {
		var e domain.AuditEvent
		if err := rows.Scan(&e.ID, &e.Action, &e.ActorID, &e.ActorType, &e.ActingProjectID, &e.CredentialID,
			&e.ResourceID, &e.ExecutionID, &e.GrantID, &e.DecisionID,
			&e.ConnectorID, &e.TraceID, &e.TargetID, &e.Summary,
			&e.Metadata, &e.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

type RequestRepo struct {
	pool      *pgxpool.Pool
	adminPool *pgxpool.Pool
}

// SetAdminPool 配置合法跨租户旁路池（BYPASSRLS）。未设置时旁路回退主池。
func (r *RequestRepo) SetAdminPool(p *pgxpool.Pool) { r.adminPool = p }

// admin 返回旁路池：钉钉回调按 ID 跨租户取请求、后台过期扫描须绕过 RLS。
func (r *RequestRepo) admin() *pgxpool.Pool {
	if r.adminPool != nil {
		return r.adminPool
	}
	return r.pool
}

func NewRequestRepo(pool *pgxpool.Pool) *RequestRepo {
	return &RequestRepo{pool: pool}
}

func (r *RequestRepo) Create(ctx context.Context, req *domain.AccessRequest) error {
	return createRequest(ctx, r.pool, req)
}

func (r *RequestRepo) CreateTx(ctx context.Context, tx pgx.Tx, req *domain.AccessRequest) error {
	return createRequest(ctx, tx, req)
}

func createRequest(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, req *domain.AccessRequest) error {
	tenantID := repositoryTenant(ctx)
	query := `INSERT INTO access_requests (tenant_id, execution_id, principal_id, resource_id, action_id, acting_project_id, external_context,
		delegation_token_id, on_behalf_of_principal_id, accountable_principal_id, parameters, parameter_hash, reason, status, decision_id, expires_at)
		VALUES ($1,$2,$3,$4,$5,nullif($6,'')::uuid,$7,nullif($8,'')::uuid,nullif($9,'')::uuid,nullif($10,'')::uuid,$11,$12,$13,$14,$15,$16)
		RETURNING id, created_at, updated_at`
	return db.QueryRow(ctx, query,
		tenantID, nullIf(req.ExecutionID), req.PrincipalID, req.ResourceID, req.ActionID, req.ActingProjectID,
		req.ExternalContext, nullIf(req.DelegationTokenID), nullIf(req.OnBehalfOfPrincipalID), nullIf(req.AccountablePrincipalID), req.Parameters, req.ParameterHash, req.Reason, req.Status,
		nullIf(req.DecisionID), req.ExpiresAt).
		Scan(&req.ID, &req.CreatedAt, &req.UpdatedAt)
}

func (r *RequestRepo) GetByID(ctx context.Context, id string) (*domain.AccessRequest, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT id, COALESCE(execution_id::text,''), principal_id, resource_id, action_id, COALESCE(acting_project_id::text,''), external_context,
		COALESCE(delegation_token_id::text,''), COALESCE(on_behalf_of_principal_id::text,''), COALESCE(accountable_principal_id::text,''), parameters, parameter_hash,
		COALESCE(reason,''), status, COALESCE(decision_id::text,''), COALESCE(approved_by::text,''),
		approved_at, expires_at, created_at, updated_at
		FROM access_requests WHERE id = $1 AND tenant_id=$2`
	row := r.pool.QueryRow(ctx, query, id, tenantID)

	var ar domain.AccessRequest
	err := row.Scan(&ar.ID, &ar.ExecutionID, &ar.PrincipalID, &ar.ResourceID, &ar.ActionID, &ar.ActingProjectID, &ar.ExternalContext,
		&ar.DelegationTokenID, &ar.OnBehalfOfPrincipalID, &ar.AccountablePrincipalID, &ar.Parameters, &ar.ParameterHash,
		&ar.Reason, &ar.Status, &ar.DecisionID, &ar.ApprovedBy,
		&ar.ApprovedAt, &ar.ExpiresAt, &ar.CreatedAt, &ar.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &ar, nil
}

// GetByIDAnyTenant resolves a request across tenant boundaries and returns its
// owning tenant. It exists solely for the DingTalk card Stream callback, which
// is authenticated by the app-level Stream signature plus a per-tenant group
// allowlist before this lookup runs; every other path must use GetByID.
func (r *RequestRepo) GetByIDAnyTenant(ctx context.Context, id string) (*domain.AccessRequest, error) {
	query := `SELECT id, tenant_id, COALESCE(execution_id::text,''), principal_id, resource_id, action_id, COALESCE(acting_project_id::text,''), external_context,
		COALESCE(delegation_token_id::text,''), COALESCE(on_behalf_of_principal_id::text,''), COALESCE(accountable_principal_id::text,''), parameters, parameter_hash,
		COALESCE(reason,''), status, COALESCE(decision_id::text,''), COALESCE(approved_by::text,''),
		approved_at, expires_at, created_at, updated_at
		FROM access_requests WHERE id = $1`
	var ar domain.AccessRequest
	err := r.admin().QueryRow(ctx, query, id).Scan(&ar.ID, &ar.TenantID, &ar.ExecutionID, &ar.PrincipalID, &ar.ResourceID, &ar.ActionID, &ar.ActingProjectID, &ar.ExternalContext,
		&ar.DelegationTokenID, &ar.OnBehalfOfPrincipalID, &ar.AccountablePrincipalID, &ar.Parameters, &ar.ParameterHash,
		&ar.Reason, &ar.Status, &ar.DecisionID, &ar.ApprovedBy,
		&ar.ApprovedAt, &ar.ExpiresAt, &ar.CreatedAt, &ar.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &ar, nil
}

func (r *RequestRepo) GetPendingByExecutionID(ctx context.Context, executionID string) (*domain.AccessRequest, error) {
	query := `SELECT id, COALESCE(execution_id::text,''), principal_id, resource_id, action_id, COALESCE(acting_project_id::text,''), external_context,
		COALESCE(delegation_token_id::text,''), COALESCE(on_behalf_of_principal_id::text,''), COALESCE(accountable_principal_id::text,''), parameters, parameter_hash,
		COALESCE(reason,''), status, COALESCE(decision_id::text,''), COALESCE(approved_by::text,''),
		approved_at, expires_at, created_at, updated_at
		FROM access_requests WHERE execution_id=$1 AND tenant_id=$2 AND status='PENDING'`
	var request domain.AccessRequest
	err := r.pool.QueryRow(ctx, query, executionID, repositoryTenant(ctx)).Scan(&request.ID, &request.ExecutionID, &request.PrincipalID,
		&request.ResourceID, &request.ActionID, &request.ActingProjectID, &request.ExternalContext, &request.DelegationTokenID, &request.OnBehalfOfPrincipalID, &request.AccountablePrincipalID,
		&request.Parameters, &request.ParameterHash, &request.Reason,
		&request.Status, &request.DecisionID, &request.ApprovedBy, &request.ApprovedAt, &request.ExpiresAt,
		&request.CreatedAt, &request.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &request, nil
}

func (r *RequestRepo) GetByExecutionID(ctx context.Context, executionID string) (*domain.AccessRequest, error) {
	query := `SELECT id, COALESCE(execution_id::text,''), principal_id, resource_id, action_id, COALESCE(acting_project_id::text,''), external_context,
		COALESCE(delegation_token_id::text,''), COALESCE(on_behalf_of_principal_id::text,''), COALESCE(accountable_principal_id::text,''), parameters, parameter_hash,
		COALESCE(reason,''), status, COALESCE(decision_id::text,''), COALESCE(approved_by::text,''),
		approved_at, expires_at, created_at, updated_at
		FROM access_requests WHERE execution_id=$1 AND tenant_id=$2 ORDER BY created_at DESC LIMIT 1`
	var request domain.AccessRequest
	err := r.pool.QueryRow(ctx, query, executionID, repositoryTenant(ctx)).Scan(&request.ID, &request.ExecutionID, &request.PrincipalID,
		&request.ResourceID, &request.ActionID, &request.ActingProjectID, &request.ExternalContext, &request.DelegationTokenID, &request.OnBehalfOfPrincipalID, &request.AccountablePrincipalID,
		&request.Parameters, &request.ParameterHash, &request.Reason,
		&request.Status, &request.DecisionID, &request.ApprovedBy, &request.ApprovedAt, &request.ExpiresAt,
		&request.CreatedAt, &request.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &request, nil
}

func (r *RequestRepo) UpdateStatus(ctx context.Context, id, status, approvedBy string, approvedAt *time.Time) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE access_requests SET status = $2, approved_by = $3, approved_at = $4, updated_at = now() WHERE id = $1 AND tenant_id=$5`,
		id, status, nullIf(approvedBy), approvedAt, repositoryTenant(ctx))
	return err
}

// ErrRequestStateChanged signals that a conditional status update matched no
// rows because another writer already transitioned the request. Callers can
// use errors.Is to tell this benign race apart from a real database failure.
var ErrRequestStateChanged = errors.New("access request state changed")

func (r *RequestRepo) UpdateStatusTx(ctx context.Context, tx pgx.Tx, id, from, status, approvedBy string, approvedAt *time.Time) error {
	command, err := tx.Exec(ctx, `UPDATE access_requests SET status=$3,approved_by=$4,approved_at=$5,updated_at=now()
		WHERE id=$1 AND tenant_id=$6 AND status=$2::request_status`, id, from, status, nullIf(approvedBy), approvedAt, repositoryTenant(ctx))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("access request %s: %w", id, ErrRequestStateChanged)
	}
	return nil
}

func (r *RequestRepo) List(ctx context.Context, status string, limit, offset int) ([]domain.AccessRequest, error) {
	var query string
	var args []any
	if status != "" {
		query = `SELECT id, COALESCE(execution_id::text,''), principal_id, resource_id, action_id, COALESCE(acting_project_id::text,''), external_context,
			COALESCE(delegation_token_id::text,''), COALESCE(on_behalf_of_principal_id::text,''), COALESCE(accountable_principal_id::text,''), parameters, parameter_hash,
			COALESCE(reason,''), status, COALESCE(decision_id::text,''), COALESCE(approved_by::text,''),
			approved_at, expires_at, created_at, updated_at
			FROM access_requests WHERE tenant_id=$1 AND status = $2
			ORDER BY created_at DESC LIMIT $3 OFFSET $4`
		args = []any{repositoryTenant(ctx), status, limit, offset}
	} else {
		query = `SELECT id, COALESCE(execution_id::text,''), principal_id, resource_id, action_id, COALESCE(acting_project_id::text,''), external_context,
			COALESCE(delegation_token_id::text,''), COALESCE(on_behalf_of_principal_id::text,''), COALESCE(accountable_principal_id::text,''), parameters, parameter_hash,
			COALESCE(reason,''), status, COALESCE(decision_id::text,''), COALESCE(approved_by::text,''),
			approved_at, expires_at, created_at, updated_at
			FROM access_requests WHERE tenant_id=$1
			ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		args = []any{repositoryTenant(ctx), limit, offset}
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.AccessRequest
	for rows.Next() {
		var ar domain.AccessRequest
		if err := rows.Scan(&ar.ID, &ar.ExecutionID, &ar.PrincipalID, &ar.ResourceID, &ar.ActionID, &ar.ActingProjectID, &ar.ExternalContext,
			&ar.DelegationTokenID, &ar.OnBehalfOfPrincipalID, &ar.AccountablePrincipalID, &ar.Parameters, &ar.ParameterHash,
			&ar.Reason, &ar.Status, &ar.DecisionID, &ar.ApprovedBy,
			&ar.ApprovedAt, &ar.ExpiresAt, &ar.CreatedAt, &ar.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, ar)
	}
	return result, rows.Err()
}

// ListByPrincipal returns the access requests initiated by one principal
// within the tenant boundary, powering the member-facing "my requests" view.
func (r *RequestRepo) ListByPrincipal(ctx context.Context, principalID, status string, limit, offset int) ([]domain.AccessRequest, error) {
	query := `SELECT id, COALESCE(execution_id::text,''), principal_id, resource_id, action_id, COALESCE(acting_project_id::text,''), external_context,
		COALESCE(delegation_token_id::text,''), COALESCE(on_behalf_of_principal_id::text,''), COALESCE(accountable_principal_id::text,''), parameters, parameter_hash,
		COALESCE(reason,''), status, COALESCE(decision_id::text,''), COALESCE(approved_by::text,''),
		approved_at, expires_at, created_at, updated_at
		FROM access_requests WHERE tenant_id=$1 AND principal_id=$2`
	args := []any{repositoryTenant(ctx), principalID}
	if status != "" {
		query += ` AND status=$3 ORDER BY created_at DESC LIMIT $4 OFFSET $5`
		args = append(args, status, limit, offset)
	} else {
		query += ` ORDER BY created_at DESC LIMIT $3 OFFSET $4`
		args = append(args, limit, offset)
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.AccessRequest
	for rows.Next() {
		var ar domain.AccessRequest
		if err := rows.Scan(&ar.ID, &ar.ExecutionID, &ar.PrincipalID, &ar.ResourceID, &ar.ActionID, &ar.ActingProjectID, &ar.ExternalContext,
			&ar.DelegationTokenID, &ar.OnBehalfOfPrincipalID, &ar.AccountablePrincipalID, &ar.Parameters, &ar.ParameterHash,
			&ar.Reason, &ar.Status, &ar.DecisionID, &ar.ApprovedBy,
			&ar.ApprovedAt, &ar.ExpiresAt, &ar.CreatedAt, &ar.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, ar)
	}
	return result, rows.Err()
}

func (r *RequestRepo) ListExpiredPending(ctx context.Context, limit int) ([]domain.AccessRequest, error) {
	rows, err := r.admin().Query(ctx, `SELECT id, tenant_id, COALESCE(execution_id::text,''), principal_id, resource_id, action_id,
		COALESCE(acting_project_id::text,''), external_context, COALESCE(delegation_token_id::text,''), COALESCE(on_behalf_of_principal_id::text,''), COALESCE(accountable_principal_id::text,''), parameters, parameter_hash, COALESCE(reason,''), status, COALESCE(decision_id::text,''),
		COALESCE(approved_by::text,''), approved_at, expires_at, created_at, updated_at
		FROM access_requests WHERE status='PENDING' AND expires_at IS NOT NULL AND expires_at <= now()
		ORDER BY expires_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.AccessRequest
	for rows.Next() {
		var request domain.AccessRequest
		if err := rows.Scan(&request.ID, &request.TenantID, &request.ExecutionID, &request.PrincipalID, &request.ResourceID,
			&request.ActionID, &request.ActingProjectID, &request.ExternalContext, &request.DelegationTokenID, &request.OnBehalfOfPrincipalID, &request.AccountablePrincipalID, &request.Parameters, &request.ParameterHash, &request.Reason, &request.Status,
			&request.DecisionID, &request.ApprovedBy, &request.ApprovedAt, &request.ExpiresAt,
			&request.CreatedAt, &request.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, request)
	}
	return result, rows.Err()
}

type ConnectorRepo struct {
	pool *pgxpool.Pool
}

func NewConnectorRepo(pool *pgxpool.Pool) *ConnectorRepo {
	return &ConnectorRepo{pool: pool}
}

func (r *ConnectorRepo) GetByID(ctx context.Context, id string) (*domain.Connector, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT id, name, connector_type, status, environment, COALESCE(target_host,''),
		version, capabilities, permissions, config, COALESCE(project_id::text,''), last_heartbeat, tenant_id, created_at, updated_at
		FROM connectors WHERE id = $1 AND tenant_id=$2`
	row := r.pool.QueryRow(ctx, query, id, tenantID)

	var c domain.Connector
	err := row.Scan(&c.ID, &c.Name, &c.ConnectorType, &c.Status, &c.Environment,
		&c.TargetHost, &c.Version, &c.Capabilities, &c.Permissions, &c.Config, &c.ProjectID,
		&c.LastHeartbeat, &c.TenantID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (r *ConnectorRepo) Heartbeat(ctx context.Context, id string) error {
	tenantID := repositoryTenant(ctx)
	// 停用（DISABLED）/隔离（QUARANTINED）是非健康态，不应被一次心跳自动复活；
	// DELETE_REQUESTED 显式转为 DISABLED；其余（ACTIVE/DEGRADED/TESTING/DRAFT）
	// 心跳恢复 ACTIVE。
	_, err := r.pool.Exec(ctx, `UPDATE connectors SET last_heartbeat = now(),
		status=CASE
			WHEN EXISTS (
				SELECT 1 FROM cluster_pairings WHERE connector_id=$1 AND status='DELETE_REQUESTED'
			) THEN 'DISABLED'::connector_status
			WHEN status IN ('DISABLED','QUARANTINED') THEN status
			ELSE 'ACTIVE'::connector_status
		END
		WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (r *ConnectorRepo) List(ctx context.Context) ([]domain.Connector, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, connector_type, status, environment, COALESCE(target_host,''),
		version, capabilities, permissions, config, COALESCE(project_id::text,''), last_heartbeat, tenant_id, created_at, updated_at,
		COALESCE((SELECT CASE WHEN status IN ('DELETE_REQUESTED','DELETED') THEN status::text ELSE '' END
			FROM cluster_pairings WHERE connector_id=connectors.id ORDER BY created_at DESC LIMIT 1),''),
		COALESCE((SELECT available_namespaces FROM cluster_pairings WHERE connector_id=connectors.id ORDER BY created_at DESC LIMIT 1),'[]'::jsonb),
		COALESCE((SELECT to_jsonb(available_services) FROM cluster_pairings WHERE connector_id=connectors.id ORDER BY created_at DESC LIMIT 1),'[]'::jsonb),
		COALESCE((SELECT to_jsonb(available_containers) FROM cluster_pairings WHERE connector_id=connectors.id ORDER BY created_at DESC LIMIT 1),'[]'::jsonb)
			FROM connectors WHERE tenant_id=$1 AND archived_at IS NULL ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.Connector
	for rows.Next() {
		var c domain.Connector
		if err := rows.Scan(&c.ID, &c.Name, &c.ConnectorType, &c.Status, &c.Environment,
			&c.TargetHost, &c.Version, &c.Capabilities, &c.Permissions, &c.Config, &c.ProjectID,
			&c.LastHeartbeat, &c.TenantID, &c.CreatedAt, &c.UpdatedAt, &c.DeletionStatus, &c.AvailableNamespaces, &c.AvailableServices, &c.AvailableContainers); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// --- Helpers ---

type IntegrationRepo struct {
	pool *pgxpool.Pool
}

func NewIntegrationRepo(pool *pgxpool.Pool) *IntegrationRepo {
	return &IntegrationRepo{pool: pool}
}

func (r *IntegrationRepo) scanIntegration(row pgx.Row) (*domain.Integration, error) {
	var ig domain.Integration
	err := row.Scan(&ig.ID, &ig.ConnectorID, &ig.Protocol, &ig.Name, &ig.Endpoint,
		&ig.Username, &ig.CredentialRef, &ig.ProjectID, &ig.Environment, &ig.Scope, &ig.Status, &ig.LastProbeAt,
		&ig.LastError, &ig.TenantID, &ig.CreatedAt, &ig.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &ig, nil
}

const integrationSelectColumns = `id, connector_id, protocol, name, endpoint, COALESCE(username,''),
	credential_ref, COALESCE(project_id::text,''), COALESCE(environment,''), scope, status, last_probe_at, COALESCE(last_error,''), tenant_id, created_at, updated_at`

func (r *IntegrationRepo) GetByID(ctx context.Context, id string) (*domain.Integration, error) {
	tenantID := repositoryTenant(ctx)
	ig, err := r.scanIntegration(r.pool.QueryRow(ctx,
		`SELECT `+integrationSelectColumns+` FROM integrations WHERE id = $1 AND tenant_id=$2`, id, tenantID))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if err := r.loadProjectScope(ctx, ig); err != nil {
		return nil, err
	}
	if err := r.loadCredentialProfiles(ctx, ig); err != nil {
		return nil, err
	}
	return ig, nil
}

func (r *IntegrationRepo) ListByConnector(ctx context.Context, connectorID string) ([]domain.Integration, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx,
		`SELECT `+integrationSelectColumns+` FROM integrations WHERE connector_id = $1 AND tenant_id=$2 ORDER BY created_at ASC`, connectorID, tenantID)
	if err != nil {
		return nil, err
	}
	var result []domain.Integration
	for rows.Next() {
		ig, err := r.scanIntegration(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *ig)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := r.loadProjectScopes(ctx, result); err != nil {
		return nil, err
	}
	if err := r.loadCredentialProfilesBatch(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *IntegrationRepo) ListAll(ctx context.Context) ([]domain.Integration, error) {
	tenantID := repositoryTenant(ctx)
	// Join connectors so integrations bound to archived (removed) connectors do
	// not surface as phantom "running" entries in the management UI.
	rows, err := r.pool.Query(ctx,
		`SELECT ig.id, ig.connector_id, ig.protocol, ig.name, ig.endpoint, COALESCE(ig.username,''),
			ig.credential_ref, COALESCE(ig.project_id::text,''), COALESCE(ig.environment,''), ig.scope, ig.status, ig.last_probe_at, COALESCE(ig.last_error,''), ig.tenant_id, ig.created_at, ig.updated_at
			FROM integrations ig
			JOIN connectors c ON c.id = ig.connector_id AND c.archived_at IS NULL
			WHERE ig.tenant_id=$1 ORDER BY ig.created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	var result []domain.Integration
	for rows.Next() {
		ig, err := r.scanIntegration(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *ig)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := r.loadProjectScopes(ctx, result); err != nil {
		return nil, err
	}
	if err := r.loadCredentialProfilesBatch(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *IntegrationRepo) loadProjectScope(ctx context.Context, ig *domain.Integration) error {
	ig.ProjectIDs = []string{}
	ig.ProjectRules = []domain.IntegrationProjectRule{}
	rows, err := r.pool.Query(ctx, `SELECT project_id::text FROM integration_project_bindings
		WHERE integration_id=$1 ORDER BY created_at, project_id`, ig.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ig.ProjectIDs = append(ig.ProjectIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	ruleRows, err := r.pool.Query(ctx, `SELECT id::text, integration_id::text, project_id::text,
		matcher_type, pattern, priority, created_at, updated_at FROM integration_project_rules
		WHERE integration_id=$1 ORDER BY priority, pattern, id`, ig.ID)
	if err != nil {
		return err
	}
	defer ruleRows.Close()
	for ruleRows.Next() {
		var rule domain.IntegrationProjectRule
		if err := ruleRows.Scan(&rule.ID, &rule.IntegrationID, &rule.ProjectID, &rule.MatcherType,
			&rule.Pattern, &rule.Priority, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
			return err
		}
		ig.ProjectRules = append(ig.ProjectRules, rule)
	}
	return ruleRows.Err()
}

// isUndefinedTable reports whether err is a PostgreSQL undefined_table error
// (SQLSTATE 42P01), which happens when a migration introducing an optional
// table has not been applied yet. Callers tolerate this so a missing auxiliary
// table does not fail the entire integration listing.
func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

func (r *IntegrationRepo) loadCredentialProfiles(ctx context.Context, ig *domain.Integration) error {
	ig.CredentialProfiles = []domain.IntegrationCredentialProfile{}
	rows, err := r.pool.Query(ctx, `SELECT id::text, name, COALESCE(username,''), credential_ref, created_at, updated_at
		FROM integration_credential_profiles WHERE integration_id=$1 ORDER BY created_at, name`, ig.ID)
	if err != nil {
		if isUndefinedTable(err) {
			return nil
		}
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var profile domain.IntegrationCredentialProfile
		if err := rows.Scan(&profile.ID, &profile.Name, &profile.Username, &profile.CredentialRef, &profile.CreatedAt, &profile.UpdatedAt); err != nil {
			return err
		}
		profile.IntegrationID = ig.ID
		ig.CredentialProfiles = append(ig.CredentialProfiles, profile)
	}
	return rows.Err()
}

// loadProjectScopes is the batch form of loadProjectScope: two queries total
// for the whole list instead of two per integration. Per-integration ordering
// matches the single-row loader because the global ORDER BY keys are the same.
func (r *IntegrationRepo) loadProjectScopes(ctx context.Context, integrations []domain.Integration) error {
	if len(integrations) == 0 {
		return nil
	}
	ids := make([]string, 0, len(integrations))
	index := make(map[string]int, len(integrations))
	for i := range integrations {
		ids = append(ids, integrations[i].ID)
		index[integrations[i].ID] = i
		integrations[i].ProjectIDs = []string{}
		integrations[i].ProjectRules = []domain.IntegrationProjectRule{}
	}
	bindingRows, err := r.pool.Query(ctx, `SELECT integration_id::text, project_id::text FROM integration_project_bindings
		WHERE integration_id=ANY($1) ORDER BY created_at, project_id`, ids)
	if err != nil {
		return err
	}
	for bindingRows.Next() {
		var integrationID, projectID string
		if err := bindingRows.Scan(&integrationID, &projectID); err != nil {
			bindingRows.Close()
			return err
		}
		if i, ok := index[integrationID]; ok {
			integrations[i].ProjectIDs = append(integrations[i].ProjectIDs, projectID)
		}
	}
	if err := bindingRows.Err(); err != nil {
		bindingRows.Close()
		return err
	}
	bindingRows.Close()

	ruleRows, err := r.pool.Query(ctx, `SELECT id::text, integration_id::text, project_id::text,
		matcher_type, pattern, priority, created_at, updated_at FROM integration_project_rules
		WHERE integration_id=ANY($1) ORDER BY priority, pattern, id`, ids)
	if err != nil {
		return err
	}
	defer ruleRows.Close()
	for ruleRows.Next() {
		var rule domain.IntegrationProjectRule
		if err := ruleRows.Scan(&rule.ID, &rule.IntegrationID, &rule.ProjectID, &rule.MatcherType,
			&rule.Pattern, &rule.Priority, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
			return err
		}
		if i, ok := index[rule.IntegrationID]; ok {
			integrations[i].ProjectRules = append(integrations[i].ProjectRules, rule)
		}
	}
	return ruleRows.Err()
}

// loadCredentialProfilesBatch is the batch form of loadCredentialProfiles:
// one query for the whole list, tolerating a not-yet-migrated auxiliary table.
func (r *IntegrationRepo) loadCredentialProfilesBatch(ctx context.Context, integrations []domain.Integration) error {
	if len(integrations) == 0 {
		return nil
	}
	ids := make([]string, 0, len(integrations))
	index := make(map[string]int, len(integrations))
	for i := range integrations {
		ids = append(ids, integrations[i].ID)
		index[integrations[i].ID] = i
		integrations[i].CredentialProfiles = []domain.IntegrationCredentialProfile{}
	}
	rows, err := r.pool.Query(ctx, `SELECT id::text, integration_id::text, name, COALESCE(username,''), credential_ref, created_at, updated_at
		FROM integration_credential_profiles WHERE integration_id=ANY($1) ORDER BY created_at, name`, ids)
	if err != nil {
		if isUndefinedTable(err) {
			return nil
		}
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var profile domain.IntegrationCredentialProfile
		if err := rows.Scan(&profile.ID, &profile.IntegrationID, &profile.Name, &profile.Username, &profile.CredentialRef, &profile.CreatedAt, &profile.UpdatedAt); err != nil {
			return err
		}
		if i, ok := index[profile.IntegrationID]; ok {
			integrations[i].CredentialProfiles = append(integrations[i].CredentialProfiles, profile)
		}
	}
	return rows.Err()
}

// ReplaceCredentialProfiles atomically replaces an Integration's purpose-bound
// credential profiles. Empty profiles clear the set.
func (r *IntegrationRepo) ReplaceCredentialProfiles(ctx context.Context, integrationID string, profiles []domain.IntegrationCredentialProfile) error {
	tenantID := repositoryTenant(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM integration_credential_profiles WHERE integration_id=$1`, integrationID); err != nil {
		return err
	}
	for _, profile := range profiles {
		if _, err := tx.Exec(ctx, `INSERT INTO integration_credential_profiles(integration_id, name, username, credential_ref, tenant_id)
			VALUES ($1,$2,nullif($3,''),$4,$5)`, integrationID, profile.Name, profile.Username, profile.CredentialRef, tenantID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ReplaceProjectScope atomically replaces the projects an Integration may
// serve and its deterministic primary-project routing rules.
func (r *IntegrationRepo) ReplaceProjectScope(ctx context.Context, integrationID, defaultProjectID, environment string, projectIDs []string, rules []domain.IntegrationProjectRule) error {
	tenantID := repositoryTenant(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE integrations SET project_id=nullif($2,'')::uuid,
		environment=nullif($3,''), updated_at=now()
		WHERE id=$1 AND tenant_id=$4`, integrationID, defaultProjectID, environment, tenantID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("integration %s not found", integrationID)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM integration_project_bindings WHERE integration_id=$1`, integrationID); err != nil {
		return err
	}
	for _, projectID := range projectIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO integration_project_bindings(integration_id, project_id) VALUES ($1,$2)`, integrationID, projectID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM resource_project_consumers rpc USING resources res
		WHERE rpc.resource_id=res.id AND res.integration_id=$1
			AND rpc.project_id IS DISTINCT FROM nullif($2,'')::uuid
			AND NOT EXISTS (SELECT 1 FROM integration_project_bindings binding
				WHERE binding.integration_id=$1 AND binding.project_id=rpc.project_id)`, integrationID, defaultProjectID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM integration_project_rules WHERE integration_id=$1`, integrationID); err != nil {
		return err
	}
	for _, rule := range rules {
		if _, err := tx.Exec(ctx, `INSERT INTO integration_project_rules(integration_id, project_id, matcher_type, pattern, priority)
			VALUES ($1,$2,$3,$4,$5)`, integrationID, rule.ProjectID, rule.MatcherType, rule.Pattern, rule.Priority); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *IntegrationRepo) Create(ctx context.Context, ig *domain.Integration) error {
	tenantID := repositoryTenant(ctx)
	if tenantID == "" {
		return errors.New("tenant context is missing")
	}
	return r.pool.QueryRow(ctx, `INSERT INTO integrations
		(connector_id, protocol, name, endpoint, username, credential_ref, project_id, environment, scope, status, tenant_id)
		VALUES ($1,$2,$3,$4,nullif($5,''),$6,nullif($7,'')::uuid,nullif($8,''),$9,$10,$11)
		RETURNING id, created_at, updated_at`,
		ig.ConnectorID, ig.Protocol, ig.Name, ig.Endpoint, ig.Username,
		ig.CredentialRef, ig.ProjectID, ig.Environment, ig.Scope, ig.Status, tenantID).Scan(&ig.ID, &ig.CreatedAt, &ig.UpdatedAt)
}

func (r *IntegrationRepo) Update(ctx context.Context, ig *domain.Integration) error {
	command, err := r.pool.Exec(ctx, `UPDATE integrations
		SET name=$3, endpoint=$4, username=nullif($5,''), credential_ref=$6, project_id=nullif($7,'')::uuid,
			environment=nullif($8,''), scope=$9, status=$10, updated_at=now()
		WHERE id=$1 AND connector_id=$2 AND tenant_id=$11`,
		ig.ID, ig.ConnectorID, ig.Name, ig.Endpoint, ig.Username,
		ig.CredentialRef, ig.ProjectID, ig.Environment, ig.Scope, ig.Status, repositoryTenant(ctx))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("integration %s not found", ig.ID)
	}
	return nil
}

func (r *IntegrationRepo) UpdateStatus(ctx context.Context, id string, status domain.IntegrationStatus, lastError string) error {
	_, err := r.pool.Exec(ctx, `UPDATE integrations SET status=$2, last_error=nullif($3,''),
		last_probe_at=now(), updated_at=now() WHERE id=$1 AND tenant_id=$4`, id, status, lastError, repositoryTenant(ctx))
	return err
}

// ReportHealth records a connector-side probe result without touching status
// or updated_at, so the connector reconciler does not rebuild the runner and
// the integration state machine stays user-driven.
func (r *IntegrationRepo) ReportHealth(ctx context.Context, connectorID, id, lastError string) error {
	_, err := r.pool.Exec(ctx, `UPDATE integrations SET last_error=nullif($3,''), last_probe_at=now()
		WHERE id=$1 AND connector_id=$2 AND tenant_id=$4`, id, connectorID, lastError, repositoryTenant(ctx))
	return err
}

func (r *IntegrationRepo) Delete(ctx context.Context, id, connectorID string) error {
	tenantID := repositoryTenant(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Preserve resource rows referenced by executions/audit history, but make
	// them unambiguously belong to a deleted integration. The FK will clear
	// integration_id when the integration row is deleted; the metadata marker
	// keeps these rows out of the live catalog until a future sync revives them.
	if _, err := tx.Exec(ctx, `UPDATE resources SET status='ORPHANED',
		metadata=metadata || jsonb_build_object(
			'integrationDeleted', true,
			'deletedIntegrationId', $1::text,
			'integrationDeletedAt', now()
		), updated_at=now()
		WHERE integration_id=$1 AND connector_id=$2 AND tenant_id=$3`, id, connectorID, tenantID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `DELETE FROM integrations WHERE id=$1 AND connector_id=$2 AND tenant_id=$3`, id, connectorID, tenantID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("integration %s not found", id)
	}
	return tx.Commit(ctx)
}

func nullIf(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- ApprovalRuleRepo ---

type ApprovalRuleRepo struct {
	pool *pgxpool.Pool
}

func NewApprovalRuleRepo(pool *pgxpool.Pool) *ApprovalRuleRepo {
	return &ApprovalRuleRepo{pool: pool}
}

func (r *ApprovalRuleRepo) List(ctx context.Context) ([]domain.ApprovalRule, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT id, risk_level, environment, requires_approval, priority, COALESCE(description,''), created_at, updated_at FROM approval_rules WHERE tenant_id=$1 ORDER BY priority DESC, risk_level, environment`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []domain.ApprovalRule
	for rows.Next() {
		var rule domain.ApprovalRule
		if err := rows.Scan(&rule.ID, &rule.RiskLevel, &rule.Environment, &rule.RequiresApproval, &rule.Priority, &rule.Description, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

// GetApplicable returns the highest-priority rule matching (riskLevel, env).
// It first tries an exact (risk_level, environment) match, then falls back to
// the (risk_level, '*') wildcard. Returns nil if no rule matches.
func (r *ApprovalRuleRepo) GetApplicable(ctx context.Context, riskLevel domain.RiskLevel, environment string) (*domain.ApprovalRule, error) {
	tenantID := repositoryTenant(ctx)
	row := r.pool.QueryRow(ctx, `
		SELECT id, risk_level, environment, requires_approval, priority, COALESCE(description,''), created_at, updated_at
		FROM approval_rules
		WHERE tenant_id=$3 AND risk_level = $1 AND (environment = $2 OR environment = '*')
		ORDER BY priority DESC, environment DESC
		LIMIT 1`, string(riskLevel), environment, tenantID)
	var rule domain.ApprovalRule
	err := row.Scan(&rule.ID, &rule.RiskLevel, &rule.Environment, &rule.RequiresApproval, &rule.Priority, &rule.Description, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &rule, nil
}

func (r *ApprovalRuleRepo) Upsert(ctx context.Context, rule domain.ApprovalRule) error {
	tenantID := repositoryTenant(ctx)
	if tenantID == "" {
		return errors.New("tenant context is missing")
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO approval_rules (id, risk_level, environment, requires_approval, priority, description, tenant_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (risk_level, environment) DO UPDATE SET
			requires_approval = EXCLUDED.requires_approval,
			priority = EXCLUDED.priority,
			description = EXCLUDED.description,
			updated_at = now()
			WHERE approval_rules.tenant_id = EXCLUDED.tenant_id`,
		rule.ID, string(rule.RiskLevel), rule.Environment, rule.RequiresApproval, rule.Priority, rule.Description, tenantID)
	return err
}

// --- Policy Matrix Rules ---

type PolicyMatrixRuleRepo struct {
	pool *pgxpool.Pool
}

func NewPolicyMatrixRuleRepo(pool *pgxpool.Pool) *PolicyMatrixRuleRepo {
	return &PolicyMatrixRuleRepo{pool: pool}
}

const policyMatrixRuleColumns = `r.id::text, r.name, COALESCE(r.project_id::text,''), COALESCE(p.name,''),
	r.resource_type, r.action_name, r.environment, COALESCE(r.risk_level::text,''),
	COALESCE(r.principal_kind::text,''), r.decision, r.priority, r.enabled,
	COALESCE(r.description,''), r.hit_count, r.last_hit_at,
	COALESCE(r.legacy_approval_rule_id::text,''), r.created_at, r.updated_at`

func scanPolicyMatrixRule(row pgx.Row) (*domain.PolicyMatrixRule, error) {
	var rule domain.PolicyMatrixRule
	err := row.Scan(&rule.ID, &rule.Name, &rule.ProjectID, &rule.ProjectName,
		&rule.ResourceType, &rule.ActionName, &rule.Environment, &rule.RiskLevel,
		&rule.PrincipalKind, &rule.Decision, &rule.Priority, &rule.Enabled,
		&rule.Description, &rule.HitCount, &rule.LastHitAt,
		&rule.LegacyApprovalRuleID, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

func (r *PolicyMatrixRuleRepo) List(ctx context.Context) ([]domain.PolicyMatrixRule, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT `+policyMatrixRuleColumns+`
		FROM policy_matrix_rules r LEFT JOIN projects p ON p.id=r.project_id
		WHERE r.tenant_id=$1 ORDER BY r.priority DESC, r.created_at, r.id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.PolicyMatrixRule
	for rows.Next() {
		rule, err := scanPolicyMatrixRule(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *rule)
	}
	return result, rows.Err()
}

func (r *PolicyMatrixRuleRepo) GetByID(ctx context.Context, id string) (*domain.PolicyMatrixRule, error) {
	tenantID := repositoryTenant(ctx)
	rule, err := scanPolicyMatrixRule(r.pool.QueryRow(ctx, `SELECT `+policyMatrixRuleColumns+`
		FROM policy_matrix_rules r LEFT JOIN projects p ON p.id=r.project_id WHERE r.id=$1 AND r.tenant_id=$2`, id, tenantID))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return rule, err
}

func (r *PolicyMatrixRuleRepo) Create(ctx context.Context, rule *domain.PolicyMatrixRule) error {
	tenantID := repositoryTenant(ctx)
	if tenantID == "" {
		return errors.New("tenant context is missing")
	}
	err := r.pool.QueryRow(ctx, `INSERT INTO policy_matrix_rules
		(name, project_id, resource_type, action_name, environment, risk_level, principal_kind,
		 decision, priority, enabled, description, tenant_id)
		VALUES ($1,nullif($2,'')::uuid,$3,$4,$5,nullif($6,'')::risk_level,
		 nullif($7,'')::principal_kind,$8,$9,$10,$11,$12) RETURNING id::text`,
		rule.Name, rule.ProjectID, rule.ResourceType, rule.ActionName, rule.Environment,
		rule.RiskLevel, rule.PrincipalKind, rule.Decision, rule.Priority, rule.Enabled, rule.Description, tenantID).Scan(&rule.ID)
	if err != nil {
		return err
	}
	created, err := r.GetByID(ctx, rule.ID)
	if err != nil {
		return err
	}
	*rule = *created
	return nil
}

func (r *PolicyMatrixRuleRepo) Update(ctx context.Context, rule *domain.PolicyMatrixRule) error {
	command, err := r.pool.Exec(ctx, `UPDATE policy_matrix_rules SET
		name=$2, project_id=nullif($3,'')::uuid, resource_type=$4, action_name=$5,
		environment=$6, risk_level=nullif($7,'')::risk_level,
		principal_kind=nullif($8,'')::principal_kind, decision=$9, priority=$10,
		enabled=$11, description=$12, updated_at=now() WHERE id=$1 AND tenant_id=$13`,
		rule.ID, rule.Name, rule.ProjectID, rule.ResourceType, rule.ActionName,
		rule.Environment, rule.RiskLevel, rule.PrincipalKind, rule.Decision,
		rule.Priority, rule.Enabled, rule.Description, repositoryTenant(ctx))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	updated, err := r.GetByID(ctx, rule.ID)
	if err != nil {
		return err
	}
	*rule = *updated
	return nil
}

func (r *PolicyMatrixRuleRepo) Delete(ctx context.Context, id string) (bool, error) {
	command, err := r.pool.Exec(ctx, `DELETE FROM policy_matrix_rules WHERE id=$1 AND tenant_id=$2`, id, repositoryTenant(ctx))
	return err == nil && command.RowsAffected() == 1, err
}

func (r *PolicyMatrixRuleRepo) Match(ctx context.Context, input domain.PolicyMatchInput) (*domain.PolicyMatrixRule, error) {
	tenantID := repositoryTenant(ctx)
	row := r.pool.QueryRow(ctx, `SELECT `+policyMatrixRuleColumns+`
		FROM policy_matrix_rules r LEFT JOIN projects p ON p.id=r.project_id
		WHERE r.tenant_id=$7 AND r.enabled
		  AND (r.project_id IS NULL OR r.project_id=nullif($1,'')::uuid)
		  AND (r.resource_type='*' OR r.resource_type=$2)
		  AND (r.action_name='*' OR r.action_name=$3)
		  AND (r.environment='*' OR r.environment=$4)
		  AND (r.risk_level IS NULL OR r.risk_level::text=$5)
		  AND (r.principal_kind IS NULL OR r.principal_kind::text=$6)
		ORDER BY r.priority DESC,
		  ((r.project_id IS NOT NULL)::int + (r.resource_type<>'*')::int +
		   (r.action_name<>'*')::int + (r.environment<>'*')::int +
		   (r.risk_level IS NOT NULL)::int + (r.principal_kind IS NOT NULL)::int) DESC,
		  r.created_at, r.id LIMIT 1`, input.ProjectID, input.ResourceType, input.ActionName,
		input.Environment, input.RiskLevel, input.PrincipalKind, tenantID)
	rule, err := scanPolicyMatrixRule(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return rule, err
}

func (r *PolicyMatrixRuleRepo) RecordHit(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE policy_matrix_rules
		SET hit_count=hit_count+1,last_hit_at=now() WHERE id=$1 AND tenant_id=$2`, id, repositoryTenant(ctx))
	return err
}

// --- CredentialRepo ---

type CredentialRepo struct {
	pool *pgxpool.Pool
}

func NewCredentialRepo(pool *pgxpool.Pool) *CredentialRepo {
	return &CredentialRepo{pool: pool}
}

const credentialMetadataColumns = `id, tenant_id, name, kind, COALESCE(username,''), value_preview,
	COALESCE(purpose,''), scope, status, version, expires_at, last_used_at, revoked_at,
	COALESCE(created_by::text,''), created_at, updated_at`

const credentialWithValueColumns = `id, tenant_id, name, kind, COALESCE(username,''), encrypted_value, value_preview,
	COALESCE(purpose,''), scope, status, version, expires_at, last_used_at, revoked_at,
	COALESCE(created_by::text,''), created_at, updated_at`

// CreateTx inserts a credential inside the caller's transaction. The plaintext
// value is already AES-GCM encrypted by the control plane before this call.
func (r *CredentialRepo) CreateTx(ctx context.Context, tx pgx.Tx, c *domain.Credential) error {
	tenantID := repositoryTenant(ctx)
	query := `INSERT INTO credentials (tenant_id, name, kind, username, encrypted_value, value_preview,
		purpose, scope, status, version, expires_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, created_at, updated_at`
	return tx.QueryRow(ctx, query, tenantID, c.Name, c.Kind, c.Username, c.EncryptedValue, c.ValuePreview,
		c.Purpose, c.Scope, c.Status, c.Version, c.ExpiresAt, nullIf(c.CreatedBy)).
		Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
}

// GetByName loads credential metadata WITHOUT the encrypted value. Used by the
// control-plane executor and the Web so a leaked ciphertext can never reach a
// response or an audit record.
func (r *CredentialRepo) GetByName(ctx context.Context, name string) (*domain.Credential, error) {
	tenantID := repositoryTenant(ctx)
	row := r.pool.QueryRow(ctx, `SELECT `+credentialMetadataColumns+`
		FROM credentials WHERE name=$1 AND tenant_id=$2`, name, tenantID)
	cred, err := scanCredentialMetadata(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return cred, err
}

// GetSecretByName loads a credential including its encrypted value. This is
// reserved for the warden: reference resolution path where a Connector holding a
// task key is authenticated to receive the plaintext over the signed channel.
func (r *CredentialRepo) GetSecretByName(ctx context.Context, name string) (*domain.Credential, error) {
	tenantID := repositoryTenant(ctx)
	row := r.pool.QueryRow(ctx, `SELECT `+credentialWithValueColumns+`
		FROM credentials WHERE name=$1 AND tenant_id=$2`, name, tenantID)
	cred, err := scanCredential(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return cred, err
}

// List returns credential metadata only (never encrypted_value).
func (r *CredentialRepo) List(ctx context.Context) ([]domain.Credential, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT `+credentialMetadataColumns+`
		FROM credentials WHERE tenant_id=$1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Credential
	for rows.Next() {
		cred, err := scanCredentialMetadata(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *cred)
	}
	return result, rows.Err()
}

// ListExpired returns ACTIVE credentials whose expires_at has passed. The
// control plane marks them EXPIRED so warden: resolution refuses them.
func (r *CredentialRepo) ListExpired(ctx context.Context) ([]domain.Credential, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT `+credentialMetadataColumns+`
		FROM credentials WHERE tenant_id=$1 AND status='ACTIVE'
		AND expires_at IS NOT NULL AND expires_at < now()`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Credential
	for rows.Next() {
		cred, err := scanCredentialMetadata(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *cred)
	}
	return result, rows.Err()
}

// RotateTx re-encrypts an ACTIVE credential with a new value, bumps its
// version and returns the new version.
func (r *CredentialRepo) RotateTx(ctx context.Context, tx pgx.Tx, id string, encrypted []byte, preview string) (int, error) {
	var version int
	err := tx.QueryRow(ctx, `UPDATE credentials
		SET encrypted_value=$2, value_preview=$3, version=version+1, updated_at=now()
		WHERE id=$1 AND tenant_id=$4 AND status='ACTIVE' RETURNING version`, id, encrypted, preview, repositoryTenant(ctx)).Scan(&version)
	return version, err
}

// RevokeTx marks an ACTIVE credential REVOKED, refusing a mismatched
// confirmName (returns rows affected 0).
func (r *CredentialRepo) RevokeTx(ctx context.Context, tx pgx.Tx, id, confirmName string) (bool, error) {
	command, err := tx.Exec(ctx, `UPDATE credentials
		SET status='REVOKED', revoked_at=now(), updated_at=now()
		WHERE id=$1 AND tenant_id=$3 AND status='ACTIVE' AND name=$2`, id, confirmName, repositoryTenant(ctx))
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

// SetScopeTx persists the purpose bindings scope for a credential.
func (r *CredentialRepo) SetScopeTx(ctx context.Context, tx pgx.Tx, id string, scope map[string]any) error {
	_, err := tx.Exec(ctx, `UPDATE credentials SET scope=$2, updated_at=now() WHERE id=$1 AND tenant_id=$3`, id, scope, repositoryTenant(ctx))
	return err
}

// MarkUsed stamps last_used_at=now() after a successful warden: reference
// resolution so operators can see which credentials are actually consumed.
func (r *CredentialRepo) MarkUsed(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE credentials SET last_used_at=now(), updated_at=now() WHERE id=$1 AND tenant_id=$2`, id, repositoryTenant(ctx))
	return err
}

func scanCredentialMetadata(row interface{ Scan(...any) error }) (*domain.Credential, error) {
	var c domain.Credential
	err := row.Scan(&c.ID, &c.TenantID, &c.Name, &c.Kind, &c.Username, &c.ValuePreview,
		&c.Purpose, &c.Scope, &c.Status, &c.Version, &c.ExpiresAt, &c.LastUsedAt, &c.RevokedAt,
		&c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func scanCredential(row interface{ Scan(...any) error }) (*domain.Credential, error) {
	var c domain.Credential
	err := row.Scan(&c.ID, &c.TenantID, &c.Name, &c.Kind, &c.Username, &c.EncryptedValue, &c.ValuePreview,
		&c.Purpose, &c.Scope, &c.Status, &c.Version, &c.ExpiresAt, &c.LastUsedAt, &c.RevokedAt,
		&c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ProviderCapabilityRepo is the database-backed provider capability catalog
// (OPE-7516 Phase 1). All entries pass domain.ValidateProviderCapability
// before they are stored; lookups return only ACTIVE entries.
type ProviderCapabilityRepo struct {
	pool *pgxpool.Pool
}

func NewProviderCapabilityRepo(pool *pgxpool.Pool) *ProviderCapabilityRepo {
	return &ProviderCapabilityRepo{pool: pool}
}

const providerCapabilityColumns = `id, name, version, resource_kinds, protocols, methods,
	path_patterns, query_parameters, allowed_headers, max_items, max_response_bytes, timeout_seconds,
	max_time_range_seconds, COALESCE(description,''), write, max_body_bytes, request_body_schema`

func scanProviderCapability(row interface{ Scan(...any) error }) (*domain.ResourceCapability, error) {
	var c domain.ResourceCapability
	err := row.Scan(&c.ID, &c.Name, &c.Version, &c.ResourceKinds, &c.Protocols, &c.Methods,
		&c.PathPatterns, &c.QueryParameters, &c.AllowedHeaders, &c.MaxItems, &c.MaxResponseBytes,
		&c.TimeoutSeconds, &c.MaxTimeRangeSecs, &c.Description, &c.Write, &c.MaxBodyBytes,
		&c.RequestBodySchema)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// List returns all ACTIVE registered capabilities with their IDs. The caller
// filters by resource kind + protocol for the per-request resolution path.
func (r *ProviderCapabilityRepo) List(ctx context.Context) ([]domain.ResourceCapability, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT `+providerCapabilityColumns+`
		FROM provider_capabilities WHERE tenant_id=$1 AND status='ACTIVE'
		ORDER BY name, version DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ResourceCapability
	for rows.Next() {
		capability, err := scanProviderCapability(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *capability)
	}
	return result, rows.Err()
}

// normalizeCapabilityJSONB coerces nil maps/slices to empty JSONB values so
// the NOT NULL DEFAULT '{}'/'[]' columns never receive a NULL (pgx encodes a
// nil map/slice as JSON NULL).
func normalizeCapabilityJSONB(c *domain.ResourceCapability) {
	if c.QueryParameters == nil {
		c.QueryParameters = map[string]domain.ProviderParamRule{}
	}
	if c.AllowedHeaders == nil {
		c.AllowedHeaders = []string{}
	}
	if c.ResourceKinds == nil {
		c.ResourceKinds = []string{}
	}
	if c.Protocols == nil {
		c.Protocols = []string{}
	}
	if c.Methods == nil {
		c.Methods = []string{}
	}
	if c.PathPatterns == nil {
		c.PathPatterns = []string{}
	}
	if c.RequestBodySchema == nil {
		c.RequestBodySchema = map[string]any{}
	}
}

// Create registers a new capability. The caller validates the entry first via
// domain.ValidateProviderCapability. A duplicate (tenant_id, name, version) is
// rejected by the UNIQUE constraint.
func (r *ProviderCapabilityRepo) Create(ctx context.Context, c *domain.ResourceCapability, createdBy string) (string, error) {
	normalizeCapabilityJSONB(c)
	tenantID := repositoryTenant(ctx)
	if tenantID == "" {
		return "", errors.New("tenant context is missing")
	}
	var id string
	err := r.pool.QueryRow(ctx, `INSERT INTO provider_capabilities
		(tenant_id, name, version, resource_kinds, protocols, methods, path_patterns,
		 query_parameters, allowed_headers, max_items, max_response_bytes, timeout_seconds,
		 max_time_range_seconds, status, description, created_by, write, max_body_bytes,
		 request_body_schema)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'ACTIVE',$14,$15,$16,$17,$18)
		RETURNING id`,
		tenantID, c.Name, c.Version, c.ResourceKinds, c.Protocols, c.Methods, c.PathPatterns,
		c.QueryParameters, c.AllowedHeaders, c.MaxItems, c.MaxResponseBytes, c.TimeoutSeconds,
		c.MaxTimeRangeSecs, c.Description, nullIf(createdBy), c.Write, c.MaxBodyBytes,
		c.RequestBodySchema).Scan(&id)
	return id, err
}

// Update replaces an ACTIVE capability in place, preserving its identity.
func (r *ProviderCapabilityRepo) Update(ctx context.Context, id string, c *domain.ResourceCapability) error {
	normalizeCapabilityJSONB(c)
	tenantID := repositoryTenant(ctx)
	if tenantID == "" {
		return errors.New("tenant context is missing")
	}
	command, err := r.pool.Exec(ctx, `UPDATE provider_capabilities
		SET name=$2, version=$3, resource_kinds=$4, protocols=$5, methods=$6, path_patterns=$7,
		    query_parameters=$8, allowed_headers=$9, max_items=$10, max_response_bytes=$11,
		    timeout_seconds=$12, max_time_range_seconds=$13, description=$14,
		    write=$15, max_body_bytes=$16, request_body_schema=$17, updated_at=now()
		WHERE id=$1 AND tenant_id=$18 AND status='ACTIVE'`,
		id, c.Name, c.Version, c.ResourceKinds, c.Protocols, c.Methods, c.PathPatterns,
		c.QueryParameters, c.AllowedHeaders, c.MaxItems, c.MaxResponseBytes, c.TimeoutSeconds,
		c.MaxTimeRangeSecs, c.Description, c.Write, c.MaxBodyBytes, c.RequestBodySchema, tenantID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

// Delete soft-disables a capability so future resolution refuses it while the
// row (and any audit trail) is preserved.
func (r *ProviderCapabilityRepo) Delete(ctx context.Context, id string) (bool, error) {
	tenantID := repositoryTenant(ctx)
	if tenantID == "" {
		return false, errors.New("tenant context is missing")
	}
	command, err := r.pool.Exec(ctx, `UPDATE provider_capabilities
		SET status='DISABLED', updated_at=now() WHERE id=$1 AND tenant_id=$2 AND status='ACTIVE'`, id, tenantID)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}
