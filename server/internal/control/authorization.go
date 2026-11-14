package control

import (
	"net/http"
	"sort"
	"strings"
)

// Roles are the only account-level role names the control plane recognizes.
// Every producer (OIDC provisioning, JWT issuance) and every consumer
// (account administration, console gating) must reference these constants.
const (
	RoleUser          = "user"
	RoleTenantAdmin   = "tenant_admin"
	RolePlatformAdmin = "platform_admin"
)

// platformTenantSlug is the shared control-plane tenant. Platform-level
// operations (tenant lifecycle, account role assignment) are restricted to
// identities that belong to it.
const platformTenantSlug = "platform"

func validAccountRole(role string) bool {
	switch role {
	case RoleUser, RoleTenantAdmin, RolePlatformAdmin:
		return true
	default:
		return false
	}
}

// validOIDCDefaultRole is deliberately narrower than validAccountRole.
// A first-time SSO login must never create a platform administrator, even if
// an environment variable is mistyped or deliberately pointed at the highest
// role. Elevated roles are assigned by an existing administrator in the
// console, never by the identity provider.
func validOIDCDefaultRole(role string) bool {
	return role == RoleUser || role == RoleTenantAdmin
}

// Scopes are the capability names shared by backend guards (RequireScope)
// and the console (the capabilities list returned by /auth/me).
const (
	ScopeAdmin                = "pam:admin"
	ScopeTenantAdmin          = "pam:tenant:admin"
	ScopeResourcesRead        = "pam:resources:read"
	ScopeExecutionsCreate     = "pam:executions:create"
	ScopeExecutionsReadSelf   = "pam:executions:read:self"
	ScopeExecutionsCancelSelf = "pam:executions:cancel:self"
	ScopeCredentialsManage    = "pam:credentials:manage"
	ScopeApprove              = "pam:approve"
	ScopeAuditRead            = "pam:audit:read"
	ScopeRegistrationRead     = "pam:registration:read"
	ScopeRegistrationWrite    = "pam:registration:write"
	ScopeDelegationExchange   = "pam:delegation:exchange"
)

var allScopes = []string{
	ScopeAdmin, ScopeTenantAdmin, ScopeResourcesRead, ScopeExecutionsCreate,
	ScopeExecutionsReadSelf, ScopeExecutionsCancelSelf, ScopeCredentialsManage,
	ScopeApprove, ScopeAuditRead, ScopeRegistrationRead, ScopeRegistrationWrite,
	ScopeDelegationExchange,
}

func defaultUserScopes() []string {
	return []string{
		ScopeResourcesRead, ScopeExecutionsCreate,
		ScopeExecutionsReadSelf, ScopeExecutionsCancelSelf,
	}
}

func tenantAdminScopes() []string {
	return []string{
		ScopeTenantAdmin, ScopeResourcesRead, ScopeExecutionsCreate,
		ScopeExecutionsReadSelf, ScopeExecutionsCancelSelf, ScopeCredentialsManage,
		ScopeApprove, ScopeAuditRead, ScopeRegistrationRead, ScopeRegistrationWrite,
	}
}

// Capabilities normalizes an identity's scopes into the single capability
// list consumed by both the backend guards and the console. pam:admin is a
// wildcard scope, so it is expanded to every known capability; the console
// never has to infer privileges from a role name again.
func Capabilities(scopes []string) []string {
	set := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		if scope = strings.TrimSpace(scope); scope != "" {
			set[scope] = true
		}
	}
	if set[ScopeAdmin] {
		for _, scope := range allScopes {
			set[scope] = true
		}
	}
	out := make([]string, 0, len(set))
	for scope := range set {
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
}

// RequirePlatformTenant keeps platform-wide operations inside the shared
// control-plane tenant. Without this wrapper, a user in any tenant holding
// the platform_admin role could manage accounts and tenants globally.
func RequirePlatformTenant(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity := identityFromContext(r.Context())
		if identity == nil || identity.TenantID != platformTenantSlug {
			jsonError(w, http.StatusForbidden, "PLATFORM_TENANT_REQUIRED", "平台级操作仅限平台租户的超级管理员")
			return
		}
		next(w, r)
	}
}
