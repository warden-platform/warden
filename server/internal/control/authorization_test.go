package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCapabilitiesExpandsAdminWildcard(t *testing.T) {
	admin := Capabilities([]string{ScopeAdmin})
	if len(admin) != len(allScopes) {
		t.Fatalf("warden:admin expanded to %d capabilities, want %d", len(admin), len(allScopes))
	}
	for _, scope := range allScopes {
		found := false
		for _, got := range admin {
			if got == scope {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expanded capabilities missing %q", scope)
		}
	}

	user := Capabilities(defaultUserScopes())
	if len(user) != 4 {
		t.Fatalf("least-privilege capabilities = %d, want 4", len(user))
	}
	for _, got := range user {
		if got == ScopeAdmin || got == ScopeTenantAdmin {
			t.Errorf("least-privilege capability list unexpectedly contains %q", got)
		}
	}

	tenantAdmin := Capabilities(tenantAdminScopes())
	for _, required := range []string{ScopeTenantAdmin, ScopeApprove, ScopeAuditRead} {
		found := false
		for _, got := range tenantAdmin {
			if got == required {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tenant admin capability list missing %q", required)
		}
	}

	deduped := Capabilities([]string{"", "  ", ScopeApprove, ScopeApprove})
	if len(deduped) != 1 || deduped[0] != ScopeApprove {
		t.Fatalf("Capabilities did not normalize/dedupe scopes: %v", deduped)
	}
}

func TestValidOIDCDefaultRoleNeverGrantsPlatformAdmin(t *testing.T) {
	for _, tc := range []struct {
		role string
		want bool
	}{
		{RoleUser, true},
		{RoleTenantAdmin, true},
		{RolePlatformAdmin, false},
		{"", false},
		{"root", false},
		{"Platform_Admin", false},
	} {
		if got := validOIDCDefaultRole(tc.role); got != tc.want {
			t.Errorf("validOIDCDefaultRole(%q) = %v, want %v", tc.role, got, tc.want)
		}
	}
}

func TestRequirePlatformTenant(t *testing.T) {
	next := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	handler := RequirePlatformTenant(next)
	for _, tc := range []struct {
		name     string
		identity *AuthenticatedIdentity
		want     int
	}{
		{"platform tenant", &AuthenticatedIdentity{TenantID: platformTenantSlug, Scopes: []string{ScopeAdmin}}, http.StatusNoContent},
		{"other tenant holding admin role", &AuthenticatedIdentity{TenantID: "acme", Scopes: []string{ScopeAdmin}}, http.StatusForbidden},
		{"missing tenant", &AuthenticatedIdentity{Scopes: []string{ScopeAdmin}}, http.StatusForbidden},
		{"unauthenticated", nil, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, tc.identity))
			resp := httptest.NewRecorder()
			handler(resp, req)
			if resp.Code != tc.want {
				t.Fatalf("got %d want %d", resp.Code, tc.want)
			}
		})
	}
}
