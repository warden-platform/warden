package connector

import "testing"

func TestParseMaskingPolicy(t *testing.T) {
	for _, token := range []string{"allow", "mask", "hash", "deny"} {
		if _, err := ParseMaskingPolicy(token); err != nil {
			t.Errorf("parse %q: %v", token, err)
		}
	}
	if _, err := ParseMaskingPolicy("expose"); err == nil {
		t.Error("expected unknown policy to be rejected")
	}
}

func TestMaskValue(t *testing.T) {
	if got := maskValue("alice@example.com"); got != "al***" {
		t.Errorf("long string -> %v", got)
	}
	if got := maskValue("ab"); got != "***" {
		t.Errorf("short string -> %v", got)
	}
	if got := maskValue(""); got != "***" {
		t.Errorf("empty string -> %v", got)
	}
	if got := maskValue(123.45); got != 0 {
		t.Errorf("number -> %v", got)
	}
	if got := maskValue(true); got != nil {
		t.Errorf("bool -> %v", got)
	}
	if got := maskValue([]string{"a"}); got != "[MASKED]" {
		t.Errorf("other -> %v", got)
	}
}

func TestHashValue(t *testing.T) {
	a := hashValue("secret-token")
	b := hashValue("secret-token")
	if a != b {
		t.Error("hash not deterministic")
	}
	if a == "" {
		t.Error("empty hash")
	}
}

func TestApplyMasking(t *testing.T) {
	policies := map[string]MaskingPolicy{
		"email":  MaskMask,
		"token":  MaskHash,
		"secret": MaskDeny,
	}
	row := map[string]any{
		"id":     int64(1),
		"email":  "alice@example.com",
		"token":  "abc123",
		"secret": "classified",
	}
	got, masked := applyMasking(row, policies)
	if masked != 2 {
		t.Errorf("masked count = %d, want 2", masked)
	}
	if got["id"] != int64(1) {
		t.Errorf("allow column altered: %v", got["id"])
	}
	if got["email"] != "al***" {
		t.Errorf("mask column = %v", got["email"])
	}
	if got["token"] == "abc123" {
		t.Errorf("hash column not hashed")
	}
	if _, present := got["secret"]; present {
		t.Errorf("deny column not dropped")
	}
}

func TestParseMaskingPolicies(t *testing.T) {
	policies, err := parseMaskingPolicies(map[string]any{
		"masking": map[string]any{"email": "mask", "token": "hash"},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if policies["email"] != MaskMask || policies["token"] != MaskHash {
		t.Errorf("policies = %v", policies)
	}

	if _, err := parseMaskingPolicies(map[string]any{}); err != nil {
		t.Errorf("absent masking should be nil/ok: %v", err)
	}
	if _, err := parseMaskingPolicies(map[string]any{"masking": map[string]any{"email": "bogus"}}); err == nil {
		t.Error("expected unknown policy token to be rejected")
	}
	if _, err := parseMaskingPolicies(map[string]any{"masking": "email"}); err == nil {
		t.Error("expected non-object masking to be rejected")
	}
}
