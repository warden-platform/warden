package control

import "testing"

func TestNormalizeRegistrationAudience(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: "https://PAM.EXAMPLE.test/", want: "https://pam.example.test/api/v1"},
		{input: "https://pam.example.test/api/v1/", want: "https://pam.example.test/api/v1"},
		{input: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080/api/v1"},
	} {
		got, err := normalizeRegistrationAudience(tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("normalizeRegistrationAudience(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}
	for _, input := range []string{"", "https://user@example.test", "https://example.test/other", "https://example.test/api/v1?tenant=x"} {
		if got, err := normalizeRegistrationAudience(input); err == nil {
			t.Fatalf("invalid audience %q accepted as %q", input, got)
		}
	}
}

func TestConfiguredRegistrationAudienceRequiresEnvironmentConsistency(t *testing.T) {
	t.Setenv("PAM_PUBLIC_CONTROL_URL", "https://pam.example.test")
	t.Setenv("PAM_REGISTRATION_AUDIENCE", "")
	if got, err := configuredRegistrationAudience(); err != nil || got != "https://pam.example.test/api/v1" {
		t.Fatalf("public URL audience = %q, %v", got, err)
	}
	t.Setenv("PAM_REGISTRATION_AUDIENCE", "https://other.example.test/api/v1")
	if _, err := configuredRegistrationAudience(); err == nil {
		t.Fatal("mismatched explicit registration audience was accepted")
	}
}
