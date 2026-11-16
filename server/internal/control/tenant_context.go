package control

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
)

var ErrTenantContextMissing = errors.New("tenant context is missing")

type tenantContextKey struct{}

// WithTenantContext is used by authenticated HTTP handlers and runtime
// adapters.  Tenant identity is derived from the credential, never from a
// request body supplied by an agent.
func WithTenantContext(ctx context.Context, tenantID string) context.Context {
	tenantID = strings.TrimSpace(tenantID)
	return domain.WithTenantID(context.WithValue(ctx, tenantContextKey{}, tenantID), tenantID)
}

func TenantIDFromContext(ctx context.Context) (string, error) {
	tenantID, _ := ctx.Value(tenantContextKey{}).(string)
	if strings.TrimSpace(tenantID) == "" {
		return "", ErrTenantContextMissing
	}
	return tenantID, nil
}

// tenantOverride implements the super-admin organization switcher: when the
// single super administrator sends X-Warden-Tenant, the whole business console
// operates in that organization's context. The identity itself stays in the
// platform management domain, so platform-level guards keep working; only the
// repository tenant scope changes. The target must be a real, active business
// organization — never the unaffiliated hall. Non-super-admin headers are
// ignored entirely (tenant identity always derives from the credential).
func (h *Handler) tenantOverride(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := identityFromContext(r.Context())
		override := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Warden-Tenant")))
		if identity == nil || !identity.IsPlatformAdmin || override == "" || override == identity.TenantID {
			next.ServeHTTP(w, r)
			return
		}
		if isUnaffiliated(override) || strings.ContainsAny(override, "/ \\\"' ") {
			jsonError(w, http.StatusBadRequest, "INVALID_TENANT_OVERRIDE", "无效的组织标识")
			return
		}
		if h.pool != nil {
			var active bool
			err := h.pool.QueryRow(r.Context(), `SELECT status='ACTIVE' FROM tenants WHERE slug=$1`, override).Scan(&active)
			if errors.Is(err, pgx.ErrNoRows) {
				jsonError(w, http.StatusNotFound, "TENANT_NOT_FOUND", "目标组织不存在")
				return
			}
			if err != nil {
				jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询目标组织失败")
				return
			}
			if !active {
				jsonError(w, http.StatusConflict, "TENANT_DISABLED", "目标组织已停用")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(WithTenantContext(r.Context(), override)))
	})
}
