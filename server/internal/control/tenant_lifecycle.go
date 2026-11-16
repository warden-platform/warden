package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (h *Handler) UpdateTenantQuota(w http.ResponseWriter, r *http.Request) {
	var q tenantQuotaInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&q); err != nil {
		jsonError(w, 400, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	if !validTenantQuota(q) {
		jsonError(w, 400, "INVALID_CONCURRENCY_LIMIT", "并发保护阈值必须为正数")
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	result, err := h.pool.Exec(r.Context(), `UPDATE tenant_quotas q SET max_concurrent_tasks=$2,updated_at=now() WHERE tenant_id=$1`, slug, q.MaxConcurrentTasks)
	if err != nil || result.RowsAffected() != 1 {
		jsonError(w, 404, "TENANT_NOT_FOUND", "租户不存在")
		return
	}
	jsonResp(w, 200, q)
}

// normalizeEmailDomains lowercases, validates and deduplicates tenant SSO
// routing domains. Empty entries are dropped; more than 20 is rejected.
func normalizeEmailDomains(input []string) ([]string, error) {
	if len(input) > 20 {
		return nil, errors.New("邮箱域名最多 20 个")
	}
	seen := make(map[string]bool, len(input))
	domains := make([]string, 0, len(input))
	for _, d := range input {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if !emailDomainPattern.MatchString(d) {
			return nil, errors.New("邮箱域名格式无效：" + d)
		}
		if !seen[d] {
			seen[d] = true
			domains = append(domains, d)
		}
	}
	return domains, nil
}

// UpdateTenantSettings manages tenant-level routing settings. emailDomains
// claims email domains for trusted-email SSO routing: a new OIDC identity
// whose IdP-asserted email matches exactly one active tenant's domain list is
// provisioned into that tenant. Domains are lowercase; duplicates are removed.
func (h *Handler) UpdateTenantSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EmailDomains []string `json:"emailDomains"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		jsonError(w, 400, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	domains, err := normalizeEmailDomains(req.EmailDomains)
	if err != nil {
		jsonError(w, 400, "INVALID_EMAIL_DOMAINS", err.Error())
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	domainsJSON, err := json.Marshal(domains)
	if err != nil {
		jsonError(w, 500, "TENANT_SETTINGS_FAILED", "序列化域名失败")
		return
	}
	result, err := h.pool.Exec(r.Context(),
		`UPDATE tenants SET settings = settings || jsonb_build_object('emailDomains', $2::jsonb), updated_at=now() WHERE slug=$1`,
		slug, string(domainsJSON))
	if err != nil || result.RowsAffected() != 1 {
		jsonError(w, 404, "TENANT_NOT_FOUND", "租户不存在")
		return
	}
	jsonResp(w, 200, map[string]any{"slug": slug, "emailDomains": domains})
}

func (h *Handler) DisableTenant(w http.ResponseWriter, r *http.Request) {
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	if slug == "platform" {
		jsonError(w, 409, "TENANT_PROTECTED", "初始租户不能停用")
		return
	}
	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		jsonError(w, 500, "TENANT_DISABLE_FAILED", "停用租户失败")
		return
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE tenants SET status='DISABLED',updated_at=now() WHERE slug=$1 AND status='ACTIVE'`, slug)
	if err != nil || result.RowsAffected() != 1 {
		jsonError(w, 404, "TENANT_NOT_FOUND", "租户不存在或已停用")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE principals SET status='DISABLED',updated_at=now() WHERE tenant_id=$1`, slug); err != nil {
		jsonError(w, 500, "TENANT_DISABLE_FAILED", "停用租户身份失败")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE principal_api_keys SET status='REVOKED',revoked_at=now() WHERE principal_id IN (SELECT id FROM principals WHERE tenant_id=$1) AND status='ACTIVE'`, slug); err != nil {
		jsonError(w, 500, "TENANT_DISABLE_FAILED", "停用租户凭据失败")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE delegation_tokens SET revoked_at=now() WHERE tenant_id=$1 AND revoked_at IS NULL`, slug); err != nil {
		jsonError(w, 500, "TENANT_DISABLE_FAILED", "停用租户委托 Token 失败")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE external_identity_bindings SET status='REVOKED',revoked_at=now(),updated_at=now() WHERE tenant_id=$1 AND status='ACTIVE'`, slug); err != nil {
		jsonError(w, 500, "TENANT_DISABLE_FAILED", "停用租户外部身份失败")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE connectors SET status='DISABLED',updated_at=now() WHERE tenant_id=$1`, slug); err != nil {
		jsonError(w, 500, "TENANT_DISABLE_FAILED", "停用租户 Connector 失败")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		jsonError(w, 500, "TENANT_DISABLE_FAILED", "停用租户失败")
		return
	}
	jsonResp(w, 200, map[string]string{"slug": slug, "status": "DISABLED"})
}
