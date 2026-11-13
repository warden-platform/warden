package domain

import (
	"context"
	"strings"
	"time"
)

type tenantContextKey struct{}

func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, strings.TrimSpace(tenantID))
}

func TenantIDFromContext(ctx context.Context) string {
	tenantID, _ := ctx.Value(tenantContextKey{}).(string)
	return strings.TrimSpace(tenantID)
}

// Tenant is an isolated customer boundary in the shared control plane.
type Tenant struct {
	ID        string         `json:"id"`
	Slug      string         `json:"slug"`
	Name      string         `json:"name"`
	Status    string         `json:"status"`
	Settings  map[string]any `json:"settings"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type TenantQuota struct {
	TenantID             string    `json:"tenant_id"`
	MaxPrincipals        int       `json:"max_principals"`
	MaxConnectors        int       `json:"max_connectors"`
	MaxConcurrentTasks   int       `json:"max_concurrent_tasks"`
	MaxRequestsPerMinute int       `json:"max_requests_per_minute"`
	AuditRetentionDays   int       `json:"audit_retention_days"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// TenantUsage is a current control-plane snapshot used by platform
// administrators to compare tenant consumption with configured quotas.
type TenantUsage struct {
	Principals     int `json:"principals"`
	Connectors     int `json:"connectors"`
	Projects       int `json:"projects"`
	InFlightTasks  int `json:"in_flight_tasks"`
	Administrators int `json:"administrators"`
}

type TenantSummary struct {
	Tenant
	Quota TenantQuota `json:"quota"`
	Usage TenantUsage `json:"usage"`
}
