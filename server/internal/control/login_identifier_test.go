package control

import "testing"

// RAM-style qualified login names: user@tenant-slug. The tenant page sends a
// qualified name; the platform operations page sends an explicit tenant; bare
// "admin" remains a break-glass API alias. Everything else bare is rejected.
func TestParseLoginIdentifier(t *testing.T) {
	cases := []struct {
		name     string
		username string
		tenant   string
		wantName string
		wantTenant string
		wantOK   bool
	}{
		{"qualified", "zhangsan@acme", "", "zhangsan", "acme", true},
		{"tenant lowercased", "zhangsan@ACME", "", "zhangsan", "acme", true},
		{"name with dots", "zhang.san@acme", "", "zhang.san", "acme", true},
		{"last at wins", "a@b@acme", "", "a@b", "acme", true},
		{"explicit tenant wins", "zhangsan@acme", "beta", "zhangsan@acme", "beta", true},
		{"platform page contract", "admin", "platform", "admin", "platform", true},
		{"bare admin alias", "admin", "", "admin", "platform", true},
		{"bare non-admin rejected", "zhangsan", "", "", "", false},
		{"leading at rejected", "@acme", "", "", "", false},
		{"trailing at rejected", "zhangsan@", "", "", "", false},
		{"invalid slug rejected", "zhangsan@acme!", "", "", "", false},
		{"empty rejected", "", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, tenant, ok := parseLoginIdentifier(tc.username, tc.tenant)
			if ok != tc.wantOK || name != tc.wantName || tenant != tc.wantTenant {
				t.Fatalf("parseLoginIdentifier(%q, %q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.username, tc.tenant, name, tenant, ok, tc.wantName, tc.wantTenant, tc.wantOK)
			}
		})
	}
}

func TestValidLoginName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"zhangsan", true},
		{"zhang.san", true},
		{"zhangsan@acme", false},
		{"@", false},
		{"", false},
	} {
		if got := validLoginName(tc.name); got != tc.want {
			t.Fatalf("validLoginName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEmailDomainOf(t *testing.T) {
	cases := []struct{ email, want string }{
		{"zhangsan@acme.com", "acme.com"},
		{"zhangsan@ACME.COM", "acme.com"},
		{"a@b@acme.com", "acme.com"},
		{"zhangsan@acme", ""},
		{"@acme.com", ""},
		{"zhangsan@", ""},
		{"zhangsan@acme..com", ""},
		{"zhangsan@-acme.com", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := emailDomainOf(tc.email); got != tc.want {
			t.Fatalf("emailDomainOf(%q) = %q, want %q", tc.email, got, tc.want)
		}
	}
}

func TestNormalizeEmailDomains(t *testing.T) {
	got, err := normalizeEmailDomains([]string{" Acme.com ", "acme.com", "acme.cn", ""})
	if err != nil {
		t.Fatalf("normalizeEmailDomains: %v", err)
	}
	if len(got) != 2 || got[0] != "acme.com" || got[1] != "acme.cn" {
		t.Fatalf("domains = %v, want [acme.com acme.cn]", got)
	}
	if _, err := normalizeEmailDomains([]string{"not a domain"}); err == nil {
		t.Fatal("expected invalid domain error")
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = "d" + string(rune('a'+i)) + ".com"
	}
	if _, err := normalizeEmailDomains(many); err == nil {
		t.Fatal("expected too-many-domains error")
	}
}