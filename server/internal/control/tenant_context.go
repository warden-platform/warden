package control

import (
	"context"
	"errors"
	"strings"

	"gitee.com/pam-platform/pam/server/internal/domain"
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
