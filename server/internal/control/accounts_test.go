package control

import "testing"

func TestValidAccountRole(t *testing.T) {
	for _, role := range []string{RoleUser, RoleTenantAdmin, RolePlatformAdmin} {
		if !validAccountRole(role) {
			t.Errorf("expected %q to be a valid account role", role)
		}
	}
	for _, role := range []string{"", "admin", "PLATFORM_ADMIN", "root", "user "} {
		if validAccountRole(role) {
			t.Errorf("expected %q to be rejected", role)
		}
	}
}

func TestValidateRoleChange(t *testing.T) {
	const other = "11111111-1111-1111-1111-111111111111"
	const self = "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name     string
		target   string
		actor    string
		current  string
		next     string
		others   int
		wantOK   bool
		wantCode string
	}{
		{"promote federated user", other, self, RoleUser, RolePlatformAdmin, 0, true, ""},
		{"assign tenant admin", other, self, RoleUser, RoleTenantAdmin, 0, true, ""},
		{"no-op role assignment", other, self, RoleUser, RoleUser, 0, true, ""},
		{"reject unknown role", other, self, RoleUser, "root", 0, false, "INVALID_ACCOUNT_ROLE"},
		{"builtin admin immutable", adminPrincipalID, self, RolePlatformAdmin, RoleUser, 3, false, "BUILTIN_ADMIN_PROTECTED"},
		{"cannot demote self", self, self, RolePlatformAdmin, RoleUser, 2, false, "CANNOT_DEMOTE_SELF"},
		{"keep last federated admin", other, self, RolePlatformAdmin, RoleTenantAdmin, 0, false, "LAST_PLATFORM_ADMIN"},
		{"demote when another admin remains", other, self, RolePlatformAdmin, RoleTenantAdmin, 1, true, ""},
		{"keep platform admin role", other, self, RolePlatformAdmin, RolePlatformAdmin, 0, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, ok := validateRoleChange(tc.target, tc.actor, tc.current, tc.next, tc.others)
			if ok != tc.wantOK || code != tc.wantCode {
				t.Fatalf("got (%q, %v), want (%q, %v)", code, ok, tc.wantCode, tc.wantOK)
			}
		})
	}
}
