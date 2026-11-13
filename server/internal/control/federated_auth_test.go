package control

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestFederatedAssertionVerifierAcceptsShortLivedRS256Assertion(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := federatedAssertionClaims{
		EnterpriseID: "91310000TEST",
		WorkspaceID:  "workspace-1",
		ProjectID:    "project-1",
		Nonce:        "nonce-1",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://idp.example.com",
			Subject:   "user-1",
			Audience:  jwt.ClaimStrings{"pam"},
			ID:        "assertion-1",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(2 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := (&federatedAssertionVerifier{key: &key.PublicKey, issuer: claims.Issuer, audience: "pam"}).verify(raw)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verified.Subject != "user-1" || verified.ProjectID != "project-1" {
		t.Fatalf("unexpected claims: %#v", verified)
	}
}

func TestFederatedAssertionVerifierRejectsReplayUnsafeClaims(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	cases := []struct {
		name   string
		mutate func(*federatedAssertionClaims)
	}{
		{name: "missing nonce", mutate: func(c *federatedAssertionClaims) { c.Nonce = "" }},
		{name: "too long", mutate: func(c *federatedAssertionClaims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(6 * time.Minute)) }},
		{name: "future issued at", mutate: func(c *federatedAssertionClaims) { c.IssuedAt = jwt.NewNumericDate(now.Add(2 * time.Minute)) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims := federatedAssertionClaims{
				EnterpriseID: "enterprise-1", WorkspaceID: "workspace-1", Nonce: "nonce-1",
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer: "https://idp.example.com", Subject: "user-1", Audience: jwt.ClaimStrings{"pam"}, ID: tc.name,
					IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(2 * time.Minute)),
				},
			}
			tc.mutate(&claims)
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			raw, signErr := token.SignedString(key)
			if signErr != nil {
				t.Fatal(signErr)
			}
			if _, verifyErr := (&federatedAssertionVerifier{key: &key.PublicKey, issuer: claims.Issuer, audience: "pam"}).verify(raw); verifyErr == nil {
				t.Fatal("expected assertion verification failure")
			}
		})
	}
}

func TestFederatedAssertionVerifierRejectsHMACEvenWithValidClaims(t *testing.T) {
	key := []byte("not-a-federated-rsa-key")
	claims := federatedAssertionClaims{EnterpriseID: "enterprise-1", WorkspaceID: "workspace-1", Nonce: "nonce-1", RegisteredClaims: jwt.RegisteredClaims{
		Issuer: "https://idp.example.com", Subject: "user-1", Audience: jwt.ClaimStrings{"pam"}, ID: "assertion-hmac",
		IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&federatedAssertionVerifier{key: key, issuer: claims.Issuer, audience: "pam"}).verify(raw); err == nil {
		t.Fatal("expected HMAC assertion to be rejected")
	}
}
