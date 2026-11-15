package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

type fakeAPIKeyStore struct {
	key       *domain.PrincipalAPIKey
	principal *domain.Principal
	created   *domain.PrincipalAPIKey
	used      bool
}

func (f *fakeAPIKeyStore) Create(_ context.Context, key *domain.PrincipalAPIKey) error {
	f.created = key
	return nil
}
func (f *fakeAPIKeyStore) GetForAuthentication(_ context.Context, _ string) (*domain.PrincipalAPIKey, *domain.Principal, error) {
	return f.key, f.principal, nil
}
func (f *fakeAPIKeyStore) ListByPrincipal(context.Context, string) ([]domain.PrincipalAPIKey, error) {
	return nil, nil
}
func (f *fakeAPIKeyStore) GetByID(context.Context, string) (*domain.PrincipalAPIKey, error) {
	return f.key, nil
}
func (f *fakeAPIKeyStore) MarkUsed(context.Context, string, string) error {
	f.used = true
	return nil
}
func (f *fakeAPIKeyStore) Revoke(context.Context, string, string) (bool, error) { return true, nil }

func TestGeneratedAPIKeyCanBeParsedAndVerified(t *testing.T) {
	id, plaintext, storedHash, err := generateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	parsedID, secret, ok := parseAPIKey(plaintext)
	if !ok || parsedID != id {
		t.Fatalf("generated API key did not round trip: %q %t", parsedID, ok)
	}
	digest := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(digest[:], storedHash) != 1 {
		t.Fatal("stored hash does not verify generated secret")
	}
	if _, _, ok := parseAPIKey("not-a-warden-key"); ok {
		t.Fatal("invalid API key was accepted")
	}
}

func TestGeneratedRegistrationAPIKeyHasDistinctPurpose(t *testing.T) {
	id, plaintext, storedHash, err := generatePurposeAPIKey(domain.APIKeyPurposeRegistration)
	if err != nil {
		t.Fatal(err)
	}
	parsedID, secret, purpose, ok := parsePurposeAPIKey(plaintext)
	if !ok || parsedID != id || purpose != domain.APIKeyPurposeRegistration {
		t.Fatalf("registration API key did not round trip: %q %q %t", parsedID, purpose, ok)
	}
	if _, _, ok := parseAPIKey(plaintext); ok {
		t.Fatal("registration credential was accepted as an execution Agent key")
	}
	digest := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(digest[:], storedHash) != 1 {
		t.Fatal("stored registration hash does not verify generated secret")
	}
}

func TestGeneratePurposeAPIKeyRejectsUnknownPurpose(t *testing.T) {
	if _, _, _, err := generatePurposeAPIKey(domain.APIKeyPurpose("UNKNOWN")); err == nil {
		t.Fatal("unknown API key purpose was accepted")
	}
}

func TestRegistrationCredentialAuthenticationRequiresAudienceAndWorkload(t *testing.T) {
	_, plaintext, storedHash, err := generatePurposeAPIKey(domain.APIKeyPurposeRegistration)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	store := &fakeAPIKeyStore{
		key: &domain.PrincipalAPIKey{ID: "11111111-1111-4111-8111-111111111111", KeyHash: storedHash,
			Status: domain.APIKeyActive, Purpose: domain.APIKeyPurposeRegistration,
			Audience: "https://warden.example.test/api/v1", Scopes: registrationCredentialScopes, ExpiresAt: &expires},
		principal: &domain.Principal{ID: "22222222-2222-4222-8222-222222222222", Kind: domain.PrincipalKindWorkload, Status: domain.PrincipalStatusActive},
	}
	h := &Handler{apiKeys: store, registrationAudience: "https://warden.example.test/api/v1"}
	identity, err := h.authenticate(context.Background(), plaintext, "192.0.2.10:1234")
	if err != nil || identity == nil || identity.Kind != "REGISTRATION" || !identity.HasScope("pam:registration:write") || !store.used {
		t.Fatalf("registration authentication = %#v err=%v used=%v", identity, err, store.used)
	}

	store.used = false
	h.registrationAudience = "https://other.example.test/api/v1"
	identity, err = h.authenticate(context.Background(), plaintext, "192.0.2.10:1234")
	if err != nil || identity != nil || store.used {
		t.Fatalf("cross-environment credential accepted: identity=%#v err=%v used=%v", identity, err, store.used)
	}
}

func TestCreateRegistrationCredentialUsesFixedScopesAndPurpose(t *testing.T) {
	principals := newFakePrincipalStore()
	principal := &domain.Principal{ID: "33333333-3333-4333-8333-333333333333", Name: "registration-bot",
		Kind: domain.PrincipalKindWorkload, Status: domain.PrincipalStatusActive, Metadata: map[string]any{"purpose": "pam_resource_registration"}}
	principals.byID[principal.ID] = principal
	store := &fakeAPIKeyStore{}
	h := &Handler{principals: principals, apiKeys: store, registrationAudience: "https://warden.example.test/api/v1"}
	expires := time.Now().Add(24 * time.Hour)
	actor := &AuthenticatedIdentity{Principal: &domain.Principal{ID: "00000000-0000-0000-0000-000000000003"}}
	key, plaintext, err := h.createCredential(context.Background(), principal.ID, createAPIKeyRequest{
		Purpose: domain.APIKeyPurposeRegistration, Audience: h.registrationAudience,
		Scopes: append([]string(nil), registrationCredentialScopes...), ExpiresAt: &expires,
	}, "", actor)
	if err != nil {
		t.Fatal(err)
	}
	if key.Purpose != domain.APIKeyPurposeRegistration || key.Audience != h.registrationAudience ||
		!sameScopes(key.Scopes, registrationCredentialScopes) || !strings.HasPrefix(plaintext, "pam_rgk_") || store.created != key {
		t.Fatalf("registration credential = %#v plaintext prefix=%q", key, plaintext[:8])
	}
}

func TestSameScopesRejectsDuplicates(t *testing.T) {
	if sameScopes([]string{"pam:registration:read", "pam:registration:read"}, registrationCredentialScopes) {
		t.Fatal("duplicate registration scope was accepted as the fixed scope set")
	}
}

func TestCreateRegistrationAPIKeyDefaultsToBoundThirtyDayCredential(t *testing.T) {
	principals := newFakePrincipalStore()
	principal := &domain.Principal{ID: "44444444-4444-4444-8444-444444444444", Name: "registration-bot",
		Kind: domain.PrincipalKindWorkload, Status: domain.PrincipalStatusActive, Metadata: map[string]any{"purpose": "pam_resource_registration"}}
	principals.byID[principal.ID] = principal
	store := &fakeAPIKeyStore{}
	h := &Handler{principals: principals, apiKeys: store, registrationAudience: "https://warden.example.test/api/v1"}
	body := bytes.NewBufferString(`{"name":"registration-test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/principals/"+principal.ID+"/registration-api-keys", body)
	req.SetPathValue("id", principal.ID)
	req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, &AuthenticatedIdentity{
		Principal: &domain.Principal{ID: "00000000-0000-0000-0000-000000000003", Kind: domain.PrincipalKindUser}, Scopes: []string{"pam:admin"},
	}))
	resp := httptest.NewRecorder()
	h.CreateRegistrationAPIKey(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("CreateRegistrationAPIKey() = %d body=%s", resp.Code, resp.Body.String())
	}
	var response struct {
		Credential domain.PrincipalAPIKey `json:"credential"`
		APIKey     string                 `json:"apiKey"`
		Endpoint   string                 `json:"apiEndpoint"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Credential.Purpose != domain.APIKeyPurposeRegistration || response.Endpoint != h.registrationAudience ||
		!strings.HasPrefix(response.APIKey, "pam_rgk_") || response.Credential.ExpiresAt == nil {
		t.Fatalf("response = %#v", response)
	}
	remaining := time.Until(*response.Credential.ExpiresAt)
	if remaining < 29*24*time.Hour || remaining > 31*24*time.Hour {
		t.Fatalf("default expiry remaining = %s", remaining)
	}
}

func TestRequireScopeAllowsAdminAndRejectsMissingScope(t *testing.T) {
	next := RequireScope("pam:approve", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	for _, tc := range []struct {
		name   string
		scopes []string
		want   int
	}{
		{name: "exact scope", scopes: []string{"pam:approve"}, want: http.StatusNoContent},
		{name: "admin wildcard", scopes: []string{"pam:admin"}, want: http.StatusNoContent},
		{name: "agent denied", scopes: defaultAgentScopes, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			identity := &AuthenticatedIdentity{Principal: &domain.Principal{ID: "p"}, Scopes: tc.scopes}
			req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, identity))
			resp := httptest.NewRecorder()
			next(resp, req)
			if resp.Code != tc.want {
				t.Fatalf("got %d want %d", resp.Code, tc.want)
			}
		})
	}
}

func TestRegistrationScopesDoNotGrantExecutionApprovalOrDestructiveAdmin(t *testing.T) {
	identity := &AuthenticatedIdentity{Principal: &domain.Principal{ID: "registration"}, Scopes: registrationCredentialScopes}
	for _, tc := range []struct {
		name   string
		scopes []string
		want   int
	}{
		{name: "registration read", scopes: []string{"pam:registration:read"}, want: http.StatusNoContent},
		{name: "registration write", scopes: []string{"pam:registration:write"}, want: http.StatusNoContent},
		{name: "execution denied", scopes: []string{"pam:executions:create"}, want: http.StatusForbidden},
		{name: "approval denied", scopes: []string{"pam:approve"}, want: http.StatusForbidden},
		{name: "admin delete denied", scopes: []string{"pam:admin"}, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := RequireAnyScope(tc.scopes, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, identity))
			resp := httptest.NewRecorder()
			handler(resp, req)
			if resp.Code != tc.want {
				t.Fatalf("got %d want %d body=%s", resp.Code, tc.want, resp.Body.String())
			}
		})
	}
}

func TestProjectCatalogAllowsRegistrationAndResourceReaders(t *testing.T) {
	handler := RequireAnyScope([]string{"pam:registration:read", "pam:resources:read"}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	for _, tc := range []struct {
		name   string
		scopes []string
		want   int
	}{
		{name: "registration reader", scopes: []string{"pam:registration:read"}, want: http.StatusNoContent},
		{name: "resource reader", scopes: []string{"pam:resources:read"}, want: http.StatusNoContent},
		{name: "execution only denied", scopes: []string{"pam:executions:create"}, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
			req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, &AuthenticatedIdentity{
				Principal: &domain.Principal{ID: "principal"}, Scopes: tc.scopes,
			}))
			resp := httptest.NewRecorder()
			handler(resp, req)
			if resp.Code != tc.want {
				t.Fatalf("got %d want %d body=%s", resp.Code, tc.want, resp.Body.String())
			}
		})
	}
}

func TestIdentityAuthMiddlewareAddsAuthenticatedIdentity(t *testing.T) {
	auth := func(_ context.Context, token, _ string) (*AuthenticatedIdentity, error) {
		if token != "valid" {
			return nil, nil
		}
		return &AuthenticatedIdentity{Principal: &domain.Principal{ID: "agent-a"}, Scopes: defaultAgentScopes}, nil
	}
	handler := IdentityAuthMiddleware(auth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := identityFromContext(r.Context()); got == nil || got.Principal.ID != "agent-a" {
			t.Fatal("identity missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer valid")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("got %d", resp.Code)
	}
}
