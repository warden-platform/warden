package control

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitee.com/pam-platform/pam/server/internal/storage/postgres"
	"github.com/golang-jwt/jwt/v5"
)

// These tests use a self-contained fake OIDC provider. They need no real IdP,
// no client credentials and no database, so the relying-party flow can be
// verified before any external integration is configured.

const (
	testOIDCClientID    = "pam-local"
	testOIDCSecret      = "test-client-secret"
	testOIDCRedirectURL = "http://localhost:4173/api/v1/auth/oidc/callback"
	testOIDCTenant      = "platform"
)

var oidcEnvKeys = []string{
	"PAM_OIDC_ISSUER", "PAM_OIDC_CLIENT_ID", "PAM_OIDC_CLIENT_SECRET",
	"PAM_OIDC_CLIENT_SECRET_FILE", "PAM_OIDC_REDIRECT_URL", "PAM_OIDC_DEFAULT_TENANT_ID",
	"PAM_OIDC_DEFAULT_ROLE",
}

// setOIDCEnv sets (or, with an empty value, unsets) an env var and restores the
// original state when the test ends. t.Setenv cannot express "unset but was set".
func setOIDCEnv(t *testing.T, key, value string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	var err error
	if value == "" {
		err = os.Unsetenv(key)
	} else {
		err = os.Setenv(key, value)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func resetOIDCEnv(t *testing.T) {
	t.Helper()
	for _, key := range oidcEnvKeys {
		setOIDCEnv(t, key, "")
	}
}

func fullOIDCEnv(t *testing.T, issuer string) {
	t.Helper()
	resetOIDCEnv(t)
	setOIDCEnv(t, "PAM_OIDC_ISSUER", issuer)
	setOIDCEnv(t, "PAM_OIDC_CLIENT_ID", testOIDCClientID)
	setOIDCEnv(t, "PAM_OIDC_CLIENT_SECRET", testOIDCSecret)
	setOIDCEnv(t, "PAM_OIDC_REDIRECT_URL", testOIDCRedirectURL)
	setOIDCEnv(t, "PAM_OIDC_DEFAULT_TENANT_ID", testOIDCTenant)
}

// A role typo must fail startup instead of silently granting elevated scopes
// to every federated user. Unset means the least-privilege default.
func TestLoadOIDCConfigValidatesDefaultRole(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{"unset uses least privilege", "", "user", false},
		{"tenant admin", "tenant_admin", "tenant_admin", false},
		{"platform admin must be assigned manually", "platform_admin", "", true},
		{"unknown role rejected", "superuser", "", true},
		{"case sensitive", "Platform_Admin", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fullOIDCEnv(t, "https://idp.example.com")
			setOIDCEnv(t, "PAM_OIDC_DEFAULT_ROLE", tc.value)
			cfg, err := loadOIDCConfig()
			if tc.wantErr {
				if err == nil {
					t.Fatal("loadOIDCConfig accepted an invalid default role")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DefaultRole != tc.want {
				t.Fatalf("DefaultRole = %q, want %q", cfg.DefaultRole, tc.want)
			}
		})
	}
}

func TestLoadOIDCConfigDisabledWithoutIssuer(t *testing.T) {
	resetOIDCEnv(t)
	cfg, err := loadOIDCConfig()
	if err != nil {
		t.Fatalf("loadOIDCConfig: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config when issuer is unset, got %#v", cfg)
	}
}

func TestLoadOIDCConfigRejectsIncompleteSettings(t *testing.T) {
	cases := []struct{ name, missing string }{
		{"client id", "PAM_OIDC_CLIENT_ID"},
		{"client secret", "PAM_OIDC_CLIENT_SECRET"},
		{"redirect url", "PAM_OIDC_REDIRECT_URL"},
		{"tenant", "PAM_OIDC_DEFAULT_TENANT_ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fullOIDCEnv(t, "https://idp.example.com")
			setOIDCEnv(t, tc.missing, "")
			if _, err := loadOIDCConfig(); err == nil {
				t.Fatal("expected a configuration error")
			}
		})
	}
}

func TestLoadOIDCConfigReadsSecretFile(t *testing.T) {
	fullOIDCEnv(t, "https://idp.example.com")
	setOIDCEnv(t, "PAM_OIDC_CLIENT_SECRET", "")
	secretPath := filepath.Join(t.TempDir(), "oidc_client_secret")
	if err := os.WriteFile(secretPath, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setOIDCEnv(t, "PAM_OIDC_CLIENT_SECRET_FILE", secretPath)
	cfg, err := loadOIDCConfig()
	if err != nil {
		t.Fatalf("loadOIDCConfig: %v", err)
	}
	if cfg.ClientSecret != "file-secret" {
		t.Fatalf("client secret = %q, want file-secret", cfg.ClientSecret)
	}
	if cfg.DefaultTenantID != testOIDCTenant || cfg.RedirectURL != testOIDCRedirectURL {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadOIDCConfigRejectsConflictingSecretSources(t *testing.T) {
	fullOIDCEnv(t, "https://idp.example.com")
	secretPath := filepath.Join(t.TempDir(), "oidc_client_secret")
	if err := os.WriteFile(secretPath, []byte("file-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	setOIDCEnv(t, "PAM_OIDC_CLIENT_SECRET_FILE", secretPath)
	if _, err := loadOIDCConfig(); err == nil {
		t.Fatal("expected an error when both secret and _FILE are set")
	}
}

func TestOIDCConfigEndpointReportsEnabledState(t *testing.T) {
	h := newTestHandler()
	for _, tc := range []struct {
		name    string
		state   *oidcState
		enabled bool
	}{
		{"disabled", nil, false},
		{"enabled", &oidcState{config: &oidcConfig{RedirectURL: "http://127.0.0.1:8081/api/v1/auth/oidc/callback"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.oidc = tc.state
			rec := httptest.NewRecorder()
			h.OIDCConfig(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/config", nil))
			var body struct {
				Enabled   bool   `json:"enabled"`
				LoginURL  string `json:"login_url"`
				LogoutURL string `json:"logout_url"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Enabled != tc.enabled {
				t.Fatalf("enabled = %t, want %t", body.Enabled, tc.enabled)
			}
			wantURL := ""
			wantLogout := ""
			if tc.enabled {
				wantURL = "http://127.0.0.1:8081/api/v1/auth/oidc/start"
				wantLogout = "http://127.0.0.1:8081/api/v1/auth/oidc/logout"
			}
			if body.LoginURL != wantURL {
				t.Fatalf("login_url = %q, want %q", body.LoginURL, wantURL)
			}
			if body.LogoutURL != wantLogout {
				t.Fatalf("logout_url = %q, want %q", body.LogoutURL, wantLogout)
			}
		})
	}
}

// The login origin must match the registered redirect URI byte-for-byte at
// the origin level; otherwise host-only state cookies never reach the callback.
func TestOIDCLoginOrigin(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://127.0.0.1:8081/api/v1/auth/oidc/callback", "http://127.0.0.1:8081"},
		{"https://pam.example.com/api/v1/auth/oidc/callback?x=1", "https://pam.example.com"},
		{"", ""},
		{"://broken", ""},
	}
	for _, tc := range cases {
		if got := oidcLoginOrigin(tc.in); got != tc.want {
			t.Errorf("oidcLoginOrigin(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// fakeOIDCProvider implements the subset of an OIDC provider that pam-control
// consumes: discovery, JWKS and the token endpoint. All keys and credentials
// are generated in-process, so no external service is involved.
type fakeOIDCProvider struct {
	server      *httptest.Server
	key         *rsa.PrivateKey
	signingKey  *rsa.PrivateKey
	clientID    string
	nonce       string
	omitSubject bool
}

func newFakeOIDCProvider(t *testing.T) *fakeOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeOIDCProvider{key: key, signingKey: key, clientID: testOIDCClientID}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", f.handleDiscovery)
	mux.HandleFunc("/jwks", f.handleJWKS)
	mux.HandleFunc("/token", f.handleToken)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeOIDCProvider) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                f.server.URL,
		"authorization_endpoint":                f.server.URL + "/authorize",
		"token_endpoint":                        f.server.URL + "/token",
		"jwks_uri":                              f.server.URL + "/jwks",
		"end_session_endpoint":                  f.server.URL + "/end_session",
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
	})
}

func (f *fakeOIDCProvider) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	pub := &f.key.PublicKey
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"kid": "test-kid",
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
}

func (f *fakeOIDCProvider) handleToken(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   f.server.URL,
		"aud":   f.clientID,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
		"email": "tester@example.com",
		"name":  "Tester",
	}
	if !f.omitSubject {
		claims["sub"] = "feishu-user-1"
	}
	if f.nonce != "" {
		claims["nonce"] = f.nonce
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-kid"
	raw, err := token.SignedString(f.signingKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "fake-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     raw,
	})
}

func newOIDCTestHandler(f *fakeOIDCProvider) *Handler {
	h := newTestHandler()
	h.oidc = &oidcState{config: &oidcConfig{
		Issuer:          f.server.URL,
		ClientID:        testOIDCClientID,
		ClientSecret:    testOIDCSecret,
		RedirectURL:     testOIDCRedirectURL,
		DefaultTenantID: testOIDCTenant,
	}}
	return h
}

func TestOIDCLoginStartSetsStateAndNonceCookies(t *testing.T) {
	f := newFakeOIDCProvider(t)
	h := newOIDCTestHandler(f)
	rec := httptest.NewRecorder()
	h.OIDCLoginStart(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/start", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body %s)", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Scheme+"://"+loc.Host != f.server.URL {
		t.Fatalf("redirect host = %q, want %q", loc.Scheme+"://"+loc.Host, f.server.URL)
	}
	query := loc.Query()
	if query.Get("client_id") != testOIDCClientID || query.Get("redirect_uri") != testOIDCRedirectURL {
		t.Fatalf("unexpected authorize request: %v", query)
	}
	if query.Get("response_type") != "code" || !strings.Contains(query.Get("scope"), "openid") {
		t.Fatalf("missing code flow parameters: %v", query)
	}
	state, nonce := query.Get("state"), query.Get("nonce")
	if state == "" || nonce == "" || state == nonce {
		t.Fatalf("state/nonce not generated correctly: %v", query)
	}
	cookies := map[string]*http.Cookie{}
	for _, cookie := range rec.Result().Cookies() {
		cookies[cookie.Name] = cookie
	}
	for _, name := range []string{"oidc_state", "oidc_nonce", "oidc_verifier"} {
		cookie := cookies[name]
		if cookie == nil {
			t.Fatalf("missing %s cookie", name)
		}
		if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 600 {
			t.Fatalf("%s cookie attributes are unsafe: %#v", name, cookie)
		}
	}
	if cookies["oidc_state"].Value != state || cookies["oidc_nonce"].Value != nonce {
		t.Fatal("cookie values do not match the authorize URL parameters")
	}

	// PKCE S256: the verifier is never sent to the IdP, only its SHA-256 hash.
	challenge := query.Get("code_challenge")
	if challenge == "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("missing PKCE S256 parameters: %v", query)
	}
	verifier := cookies["oidc_verifier"].Value
	sum := sha256.Sum256([]byte(verifier))
	if challenge != base64.RawURLEncoding.EncodeToString(sum[:]) || challenge == verifier {
		t.Fatal("code_challenge is not S256(oidc_verifier)")
	}
	if query.Get("code_verifier") != "" {
		t.Fatal("code_verifier must never appear in the authorize URL")
	}
}

func TestOIDCLoginStartDisabled(t *testing.T) {
	h := newTestHandler()
	rec := httptest.NewRecorder()
	h.OIDCLoginStart(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/start", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "OIDC_NOT_CONFIGURED") {
		t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestOIDCLoginCallbackDisabledAndNoDatabase(t *testing.T) {
	req := func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?state=s&code=c", nil)
	}
	disabled := newTestHandler()
	rec := httptest.NewRecorder()
	disabled.OIDCLoginCallback(rec, req())
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "OIDC_NOT_CONFIGURED") {
		t.Fatalf("disabled: %d %s", rec.Code, rec.Body.String())
	}

	noDB := newTestHandler()
	noDB.oidc = &oidcState{config: &oidcConfig{}}
	rec = httptest.NewRecorder()
	noDB.OIDCLoginCallback(rec, req())
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "NO_DATABASE") {
		t.Fatalf("no database: %d %s", rec.Code, rec.Body.String())
	}
}

func TestOIDCLoginCallbackRejectsInvalidRequests(t *testing.T) {
	f := newFakeOIDCProvider(t)
	cases := []struct {
		name       string
		query      string
		cookies    map[string]string
		wantStatus int
		wantCode   string
	}{
		{"provider error", "error=access_denied&error_description=nope", nil, http.StatusBadRequest, "OIDC_ERROR"},
		{"state mismatch", "state=expected&code=abc", map[string]string{"oidc_state": "other", "oidc_nonce": "n"}, http.StatusBadRequest, "INVALID_OIDC_STATE"},
		{"missing state cookie", "state=expected&code=abc", map[string]string{"oidc_nonce": "n"}, http.StatusBadRequest, "INVALID_OIDC_STATE"},
		{"missing code", "state=expected", map[string]string{"oidc_state": "expected", "oidc_nonce": "n"}, http.StatusBadRequest, "OIDC_NO_CODE"},
		{"missing PKCE verifier", "state=expected&code=abc", map[string]string{"oidc_state": "expected", "oidc_nonce": "n"}, http.StatusBadRequest, "OIDC_PKCE_MISSING"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newOIDCTestHandler(f)
			h.federated = postgres.NewFederatedRepo(nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?"+tc.query, nil)
			for name, value := range tc.cookies {
				req.AddCookie(&http.Cookie{Name: name, Value: value})
			}
			rec := httptest.NewRecorder()
			h.OIDCLoginCallback(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantCode) {
				t.Fatalf("body = %s, want error code %s", rec.Body.String(), tc.wantCode)
			}
			// A failed callback must leave the state/nonce cookies intact so a
			// retry (or a duplicate request) can still complete the flow.
			for _, c := range rec.Result().Cookies() {
				if (c.Name == "oidc_state" || c.Name == "oidc_nonce" || c.Name == "oidc_verifier") && c.MaxAge < 0 {
					t.Fatalf("failed callback cleared %s, which would break a retry", c.Name)
				}
			}
		})
	}
}

// An identity broker in front of Feishu may drop the nonce claim. This test
// drives the full discovery -> exchange -> signature verification path and
// confirms pam-control rejects such a token rather than minting a session.
func TestOIDCLoginCallbackRejectsIdTokenWithoutNonce(t *testing.T) {
	f := newFakeOIDCProvider(t) // f.nonce is empty: no nonce claim in id_token
	h := newOIDCTestHandler(f)
	h.federated = postgres.NewFederatedRepo(nil)
	const state, nonce = "the-state", "the-nonce"
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/oidc/callback?state="+state+"&code=fake-code", nil)
	req.AddCookie(&http.Cookie{Name: "oidc_state", Value: state})
	req.AddCookie(&http.Cookie{Name: "oidc_nonce", Value: nonce})
	req.AddCookie(&http.Cookie{Name: "oidc_verifier", Value: "the-verifier"})
	rec := httptest.NewRecorder()
	h.OIDCLoginCallback(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "OIDC_NONCE_MISMATCH") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestOIDCLoginCallbackRejectsInvalidSignature(t *testing.T) {
	f := newFakeOIDCProvider(t)
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f.signingKey = otherKey // JWKS still publishes f.key, so verification must fail
	h := newOIDCTestHandler(f)
	h.federated = postgres.NewFederatedRepo(nil)
	const state, nonce = "the-state", "the-nonce"
	f.nonce = nonce
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/oidc/callback?state="+state+"&code=fake-code", nil)
	req.AddCookie(&http.Cookie{Name: "oidc_state", Value: state})
	req.AddCookie(&http.Cookie{Name: "oidc_nonce", Value: nonce})
	req.AddCookie(&http.Cookie{Name: "oidc_verifier", Value: "the-verifier"})
	rec := httptest.NewRecorder()
	h.OIDCLoginCallback(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "OIDC_VERIFY_FAILED") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

// A provider that omits sub would otherwise map every federated user onto the
// same (issuer, subject) row. pam-control must fail closed before provisioning.
func TestOIDCLoginCallbackRejectsIdTokenWithoutSubject(t *testing.T) {
	f := newFakeOIDCProvider(t)
	f.omitSubject = true
	h := newOIDCTestHandler(f)
	h.federated = postgres.NewFederatedRepo(nil) // must not be reached
	const state, nonce = "the-state", "the-nonce"
	f.nonce = nonce
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/oidc/callback?state="+state+"&code=fake-code", nil)
	req.AddCookie(&http.Cookie{Name: "oidc_state", Value: state})
	req.AddCookie(&http.Cookie{Name: "oidc_nonce", Value: nonce})
	req.AddCookie(&http.Cookie{Name: "oidc_verifier", Value: "the-verifier"})
	rec := httptest.NewRecorder()
	h.OIDCLoginCallback(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "OIDC_INVALID_SUBJECT") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

// RP-initiated logout must forward to the IdP end_session endpoint and clear
// both the PAM session and every flow cookie; otherwise the IdP silently
// signs the user straight back in on the next SSO click.
func TestOIDCLogoutRedirectsToIdPEndSession(t *testing.T) {
	f := newFakeOIDCProvider(t)
	h := newOIDCTestHandler(f)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/logout", nil)
	req.AddCookie(&http.Cookie{Name: "oidc_state", Value: "s"})
	rec := httptest.NewRecorder()
	h.OIDCLogout(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Scheme+"://"+loc.Host != f.server.URL || loc.Path != "/end_session" {
		t.Fatalf("logout target = %q, want IdP /end_session", loc.String())
	}
	q := loc.Query()
	if q.Get("client_id") != testOIDCClientID ||
		q.Get("post_logout_redirect_uri") != "http://localhost:4173/" {
		t.Fatalf("unexpected logout parameters: %v", q)
	}
	cleared := map[string]bool{}
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			cleared[c.Name] = true
		}
	}
	for _, name := range []string{"pam_session", "oidc_state", "oidc_nonce", "oidc_verifier"} {
		if !cleared[name] {
			t.Fatalf("logout did not clear %s", name)
		}
	}
}

func TestOIDCLogoutDisabledReturnsToLoginPage(t *testing.T) {
	h := newTestHandler()
	rec := httptest.NewRecorder()
	h.OIDCLogout(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/logout", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
		t.Fatalf("status = %d location = %q", rec.Code, rec.Header().Get("Location"))
	}
}
