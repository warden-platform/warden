package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"gitee.com/pam-platform/pam/server/internal/domain"
	"github.com/jackc/pgx/v5"
)

var tenantSlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,98}[a-z0-9])?$`)

type tenantQuotaInput struct {
	MaxPrincipals        int `json:"maxPrincipals"`
	MaxConnectors        int `json:"maxConnectors"`
	MaxConcurrentTasks   int `json:"maxConcurrentTasks"`
	MaxRequestsPerMinute int `json:"maxRequestsPerMinute"`
	AuditRetentionDays   int `json:"auditRetentionDays"`
}

func defaultTenantQuotaInput() tenantQuotaInput {
	return tenantQuotaInput{MaxPrincipals: 1000, MaxConnectors: 100, MaxConcurrentTasks: 100, MaxRequestsPerMinute: 600, AuditRetentionDays: 365}
}

func validTenantQuota(q tenantQuotaInput) bool {
	return q.MaxPrincipals > 0 && q.MaxConnectors > 0 && q.MaxConcurrentTasks > 0 && q.MaxRequestsPerMinute > 0 && q.AuditRetentionDays > 0
}

func validTenantIdentity(slug, name string) bool {
	return slug != "" && name != "" && len(slug) <= 100 && len(name) <= 255 && tenantSlugPattern.MatchString(slug)
}

const tenantSummaryQuery = `SELECT t.id,t.slug,t.name,t.status,t.settings,t.created_at,t.updated_at,
	q.tenant_id,q.max_principals,q.max_connectors,q.max_concurrent_tasks,q.max_requests_per_minute,q.audit_retention_days,q.updated_at,
	(SELECT count(*) FROM principals p WHERE p.tenant_id=t.slug AND p.status='ACTIVE'),
	(SELECT count(*) FROM connectors c WHERE c.tenant_id=t.slug AND c.archived_at IS NULL AND c.status<>'DISABLED'),
	(SELECT count(*) FROM projects pr WHERE pr.tenant_id=t.slug),
	(SELECT count(*) FROM executions e WHERE e.tenant_id=t.slug AND e.status IN ('WAITING_APPROVAL','QUEUED','DISPATCHED','RUNNING')),
	(SELECT count(*) FROM principals a WHERE a.tenant_id=t.slug AND a.kind='user' AND a.status='ACTIVE' AND a.metadata->>'role'='tenant_admin')
	FROM tenants t JOIN tenant_quotas q ON q.tenant_id=t.slug`

func scanTenantSummary(row interface{ Scan(...any) error }) (domain.TenantSummary, error) {
	var summary domain.TenantSummary
	err := row.Scan(&summary.ID, &summary.Slug, &summary.Name, &summary.Status, &summary.Settings, &summary.CreatedAt, &summary.UpdatedAt,
		&summary.Quota.TenantID, &summary.Quota.MaxPrincipals, &summary.Quota.MaxConnectors, &summary.Quota.MaxConcurrentTasks,
		&summary.Quota.MaxRequestsPerMinute, &summary.Quota.AuditRetentionDays, &summary.Quota.UpdatedAt,
		&summary.Usage.Principals, &summary.Usage.Connectors, &summary.Usage.Projects, &summary.Usage.InFlightTasks, &summary.Usage.Administrators)
	return summary, err
}

func (h *Handler) ListTenants(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	rows, err := h.pool.Query(r.Context(), tenantSummaryQuery+` ORDER BY t.created_at`)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询租户失败")
		return
	}
	defer rows.Close()
	result := []domain.TenantSummary{}
	for rows.Next() {
		tenant, err := scanTenantSummary(rows)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取租户失败")
			return
		}
		result = append(result, tenant)
	}
	jsonResp(w, http.StatusOK, result)
}

func (h *Handler) GetTenant(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	tenant, err := scanTenantSummary(h.pool.QueryRow(r.Context(), tenantSummaryQuery+` WHERE t.slug=$1`, slug))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			jsonError(w, http.StatusNotFound, "TENANT_NOT_FOUND", "租户不存在")
		} else {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询租户失败")
		}
		return
	}
	jsonResp(w, http.StatusOK, tenant)
}

func (h *Handler) CreateTenant(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var req struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	req.Name = strings.TrimSpace(req.Name)
	if !validTenantIdentity(req.Slug, req.Name) {
		jsonError(w, http.StatusBadRequest, "INVALID_TENANT", "slug 和 name 为必填且长度无效")
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "TENANT_CREATE_FAILED", "创建租户失败")
		return
	}
	defer tx.Rollback(r.Context())
	var tenant domain.Tenant
	err = tx.QueryRow(r.Context(), `INSERT INTO tenants (slug, name) VALUES ($1,$2)
		RETURNING id, slug, name, status, settings, created_at, updated_at`, req.Slug, req.Name).
		Scan(&tenant.ID, &tenant.Slug, &tenant.Name, &tenant.Status, &tenant.Settings, &tenant.CreatedAt, &tenant.UpdatedAt)
	if err != nil {
		jsonError(w, http.StatusConflict, "TENANT_CREATE_FAILED", "租户 slug 已存在或数据无效")
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO tenant_quotas (tenant_id) VALUES ($1)`, tenant.Slug); err != nil {
		jsonError(w, http.StatusInternalServerError, "TENANT_QUOTA_INIT_FAILED", "初始化租户配额失败")
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO projects (name, is_default, tenant_id) VALUES ('默认项目', true, $1)`, tenant.Slug); err != nil {
		jsonError(w, http.StatusInternalServerError, "TENANT_INIT_FAILED", "初始化租户项目失败")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		jsonError(w, http.StatusInternalServerError, "TENANT_CREATE_FAILED", "创建租户失败")
		return
	}
	jsonResp(w, http.StatusCreated, tenant)
}

// BootstrapTenant atomically creates the tenant boundary, its default project,
// configured quota and first tenant administrator.
func (h *Handler) BootstrapTenant(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var req struct {
		Slug          string            `json:"slug"`
		Name          string            `json:"name"`
		AdminName     string            `json:"adminName"`
		AdminPassword string            `json:"adminPassword"`
		Quota         *tenantQuotaInput `json:"quota,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	req.Name = strings.TrimSpace(req.Name)
	req.AdminName = strings.TrimSpace(req.AdminName)
	quota := defaultTenantQuotaInput()
	if req.Quota != nil {
		quota = *req.Quota
	}
	if !validTenantIdentity(req.Slug, req.Name) || req.AdminName == "" || len(req.AdminName) > 255 || len(req.AdminPassword) < 8 || len(req.AdminPassword) > 72 || !validTenantQuota(quota) {
		jsonError(w, http.StatusBadRequest, "INVALID_TENANT_BOOTSTRAP", "租户、管理员或配额格式无效")
		return
	}
	passwordHash, err := hashPassword(req.AdminPassword)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PASSWORD_HASH_FAILED", "管理员密码处理失败")
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "TENANT_CREATE_FAILED", "创建租户失败")
		return
	}
	defer tx.Rollback(r.Context())
	var tenant domain.Tenant
	err = tx.QueryRow(r.Context(), `INSERT INTO tenants(slug,name) VALUES ($1,$2)
		RETURNING id,slug,name,status,settings,created_at,updated_at`, req.Slug, req.Name).
		Scan(&tenant.ID, &tenant.Slug, &tenant.Name, &tenant.Status, &tenant.Settings, &tenant.CreatedAt, &tenant.UpdatedAt)
	if err != nil {
		jsonError(w, http.StatusConflict, "TENANT_CREATE_FAILED", "租户 slug 已存在或数据无效")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO tenant_quotas
		(tenant_id,max_principals,max_connectors,max_concurrent_tasks,max_requests_per_minute,audit_retention_days)
		VALUES ($1,$2,$3,$4,$5,$6)`, req.Slug, quota.MaxPrincipals, quota.MaxConnectors, quota.MaxConcurrentTasks, quota.MaxRequestsPerMinute, quota.AuditRetentionDays); err != nil {
		jsonError(w, http.StatusInternalServerError, "TENANT_QUOTA_INIT_FAILED", "初始化租户配额失败")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO projects(name,description,is_default,tenant_id)
		VALUES ('默认项目','未划分项目的资源自动归入默认项目',true,$1)`, req.Slug); err != nil {
		jsonError(w, http.StatusInternalServerError, "TENANT_INIT_FAILED", "初始化租户项目失败")
		return
	}
	var admin domain.Principal
	err = tx.QueryRow(r.Context(), `INSERT INTO principals(kind,name,status,tenant_id,metadata,password_hash)
		VALUES ('user',$1,'ACTIVE',$2,'{"role":"tenant_admin"}'::jsonb,$3)
		RETURNING id,kind,name,status,tenant_id,metadata,created_at,updated_at`, req.AdminName, req.Slug, passwordHash).
		Scan(&admin.ID, &admin.Kind, &admin.Name, &admin.Status, &admin.TenantID, &admin.Metadata, &admin.CreatedAt, &admin.UpdatedAt)
	if err != nil {
		jsonError(w, http.StatusConflict, "TENANT_ADMIN_CREATE_FAILED", "管理员名称已存在或数据无效")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		jsonError(w, http.StatusInternalServerError, "TENANT_CREATE_FAILED", "创建租户失败")
		return
	}
	jsonResp(w, http.StatusCreated, map[string]any{"tenant": tenant, "administrator": admin})
}
