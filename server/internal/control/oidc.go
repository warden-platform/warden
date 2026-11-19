package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"gitee.com/warden-platform/warden/server/internal/config"
	"gitee.com/warden-platform/warden/server/internal/domain"
	"gitee.com/warden-platform/warden/server/internal/storage/postgres"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// oidcConfig is the static OIDC relying-party configuration. A nil *oidcConfig
// (or an empty issuer) means OIDC login is disabled.
type oidcConfig struct {
	Issuer          string
	ClientID        string
	ClientSecret    string
	RedirectURL     string
	DefaultTenantID string
	DefaultRole     string
}

// A first-time SSO login only ever creates a least-privilege (or, if an
// operator explicitly chooses it, tenant-scoped) account. Platform
// administrators are assigned by an existing administrator in the console.

// oidcState lazily resolves the provider on first use so warden-control startup
// does not depend on the identity provider being reachable.
type oidcState struct {
	config   *oidcConfig
	mu       sync.Mutex
	provider *oidc.Provider
	oauth2   oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// loadOIDCConfig resolves the SSO configuration.
//
// Master switch: WARDEN_OIDC_ENABLED=true enables SSO explicitly; =false
// forces it off even when an issuer is configured. When the switch is unset,
// the legacy contract applies (a configured issuer enables SSO), so existing
// deployments keep working unchanged.
//
// Only the three IdP-issued values are truly required (issuer, client id,
// client secret). The redirect URL defaults to
// <WARDEN_PUBLIC_CONTROL_URL>/api/v1/auth/oidc/callback and the default tenant
// to "platform", so adopters can enable SSO with four lines of configuration.
func loadOIDCConfig() (*oidcConfig, error) {
	explicit := false
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WARDEN_OIDC_ENABLED"))) {
	case "":
		// Legacy mode: decided by issuer presence below.
	case "true", "1", "yes", "on":
		explicit = true
	case "false", "0", "no", "off":
		log.Printf("SSO disabled: WARDEN_OIDC_ENABLED=false")
		return nil, nil
	default:
		return nil, errors.New("WARDEN_OIDC_ENABLED must be true or false")
	}
	issuer := strings.TrimSpace(os.Getenv("WARDEN_OIDC_ISSUER"))
	if issuer == "" {
		if explicit {
			return nil, errors.New("WARDEN_OIDC_ENABLED=true requires WARDEN_OIDC_ISSUER (the IdP issuer URL)")
		}
		return nil, nil
	}
	clientID := strings.TrimSpace(os.Getenv("WARDEN_OIDC_CLIENT_ID"))
	clientSecret, err := config.Secret("WARDEN_OIDC_CLIENT_SECRET")
	if err != nil {
		return nil, err
	}
	redirectURL := strings.TrimSpace(os.Getenv("WARDEN_OIDC_REDIRECT_URL"))
	if redirectURL == "" {
		if base := strings.TrimSpace(os.Getenv("WARDEN_PUBLIC_CONTROL_URL")); base != "" {
			redirectURL = strings.TrimRight(base, "/") + "/api/v1/auth/oidc/callback"
		}
	}
	// An unset default tenant means a new federated identity that matches no
	// email-domain rule lands in the unaffiliated state rather than any real
	// organization. Operators who want the legacy "everything falls into one
	// tenant" behavior may still set WARDEN_OIDC_DEFAULT_TENANT_ID explicitly.
	defaultTenantID := strings.TrimSpace(os.Getenv("WARDEN_OIDC_DEFAULT_TENANT_ID"))
	var missing []string
	if clientID == "" {
		missing = append(missing, "WARDEN_OIDC_CLIENT_ID")
	}
	if clientSecret == "" {
		missing = append(missing, "WARDEN_OIDC_CLIENT_SECRET (or WARDEN_OIDC_CLIENT_SECRET_FILE)")
	}
	if redirectURL == "" {
		missing = append(missing, "WARDEN_OIDC_REDIRECT_URL (or WARDEN_PUBLIC_CONTROL_URL to derive it)")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("OIDC is enabled but required configuration is missing: %s", strings.Join(missing, ", "))
	}
	defaultRole := strings.TrimSpace(os.Getenv("WARDEN_OIDC_DEFAULT_ROLE"))
	if defaultRole == "" {
		defaultRole = "user"
	}
	if !validOIDCDefaultRole(defaultRole) {
		return nil, errors.New("WARDEN_OIDC_DEFAULT_ROLE must be user or tenant_admin; platform_admin must be assigned manually by an administrator")
	}
	cfg := &oidcConfig{
		Issuer:          issuer,
		ClientID:        clientID,
		ClientSecret:    clientSecret,
		RedirectURL:     redirectURL,
		DefaultTenantID: defaultTenantID,
		DefaultRole:     defaultRole,
	}
	// Never log the client secret. The remaining fields make it immediately
	// obvious which relying-party configuration is active during local testing.
	log.Printf("SSO enabled: issuer=%s client_id=%s redirect=%s default_tenant=%s default_role=%s",
		cfg.Issuer, cfg.ClientID, cfg.RedirectURL, oidcDefaultTenantLabel(cfg.DefaultTenantID), cfg.DefaultRole)
	return cfg, nil
}

// oidcDefaultTenantLabel renders the startup log: an empty default tenant means
// newcomers without an email-domain match stay unaffiliated.
func oidcDefaultTenantLabel(defaultTenantID string) string {
	if defaultTenantID == "" {
		return "unaffiliated"
	}
	return defaultTenantID
}

// resolve returns the resolved OIDC provider, verifier, and OAuth2 config.
// Resolution is cached; a failed resolution is not cached so a transient IdP
// outage can be retried on the next request.
func (s *oidcState) resolve(ctx context.Context) (*oidc.Provider, *oidc.IDTokenVerifier, oauth2.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider != nil {
		return s.provider, s.verifier, s.oauth2, nil
	}
	provider, err := oidc.NewProvider(ctx, s.config.Issuer)
	if err != nil {
		log.Printf("OIDC discovery failed: issuer=%s err=%v", s.config.Issuer, err)
		return nil, nil, oauth2.Config{}, err
	}
	s.provider = provider
	s.oauth2 = oauth2.Config{
		ClientID:     s.config.ClientID,
		ClientSecret: s.config.ClientSecret,
		RedirectURL:  s.config.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
	s.verifier = provider.Verifier(&oidc.Config{ClientID: s.config.ClientID})
	return s.provider, s.verifier, s.oauth2, nil
}

// oidcLoginOrigin derives the browser origin that owns the registered
// redirect URI. The state/nonce cookies are host-only, so the whole flow must
// be started from exactly this origin: opening the console on a different host
// (for example localhost instead of 127.0.0.1) writes the cookies to a host
// the callback will never see, which surfaces as INVALID_OIDC_STATE.
func oidcLoginOrigin(redirectURL string) string {
	u, err := url.Parse(redirectURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

var emailDomainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// emailDomainOf extracts the lowercase domain from an email address, or ""
// when the address is malformed.
func emailDomainOf(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	domainPart := strings.ToLower(strings.TrimSpace(email[at+1:]))
	if !emailDomainPattern.MatchString(domainPart) {
		return ""
	}
	return domainPart
}

// routeTenantByEmail maps a trusted (IdP-asserted) email to a tenant via the
// admin-configured emailDomains list in tenants.settings. Exactly one active
// tenant must claim the domain; zero or ambiguous matches fall back to the
// configured default. Only verified id_token claims may reach this function.
func (h *Handler) routeTenantByEmail(ctx context.Context, email string) string {
	domainPart := emailDomainOf(email)
	if domainPart == "" {
		return ""
	}
	param, err := json.Marshal([]string{domainPart})
	if err != nil {
		return ""
	}
	rows, err := h.pool.Query(ctx, `SELECT slug FROM tenants WHERE status='ACTIVE' AND settings->'emailDomains' @> $1::jsonb LIMIT 2`, string(param))
	if err != nil {
		log.Printf("OIDC tenant routing query failed: %v", err)
		return ""
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			slugs = append(slugs, s)
		}
	}
	if len(slugs) == 1 {
		return slugs[0]
	}
	if len(slugs) > 1 {
		log.Printf("OIDC tenant routing ambiguous: domain=%s matches=%d, using default", domainPart, len(slugs))
	}
	return ""
}

// OIDCConfig tells the SPA whether SSO is available and, when it is, the
// canonical absolute URLs it must navigate to. Using the server-provided URLs
// keeps the login origin in sync with the IdP's exact redirect-URI whitelist.
func (h *Handler) OIDCConfig(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"enabled": h.oidc != nil}
	if h.oidc != nil {
		if origin := oidcLoginOrigin(h.oidc.config.RedirectURL); origin != "" {
			resp["login_url"] = origin + "/api/v1/auth/oidc/start"
			resp["logout_url"] = origin + "/api/v1/auth/oidc/logout"
		}
	}
	jsonResp(w, http.StatusOK, resp)
}

// OIDCLoginStart begins the authorization-code flow with PKCE S256. It stores
// the anti-CSRF state, the nonce and the PKCE verifier in HttpOnly cookies,
// then redirects to the identity provider's authorize endpoint.
func (h *Handler) OIDCLoginStart(w http.ResponseWriter, r *http.Request) {
	if h.oidc == nil {
		jsonError(w, http.StatusNotFound, "OIDC_NOT_CONFIGURED", "OIDC 登录未配置")
		return
	}
	_, _, oauth2Conf, err := h.oidc.resolve(r.Context())
	if err != nil {
		jsonError(w, http.StatusServiceUnavailable, "OIDC_PROVIDER_ERROR", "OIDC 身份提供方暂不可用")
		return
	}
	state, err := randomToken(24)
	if err != nil {
		log.Printf("OIDC state generation failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "OIDC_STATE_ERROR", "生成 OIDC state 失败")
		return
	}
	nonce, err := randomToken(24)
	if err != nil {
		log.Printf("OIDC nonce generation failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "OIDC_NONCE_ERROR", "生成 OIDC nonce 失败")
		return
	}
	codeVerifier, err := randomToken(32)
	if err != nil {
		log.Printf("OIDC PKCE verifier generation failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "OIDC_PKCE_ERROR", "生成 OIDC PKCE verifier 失败")
		return
	}
	challengeSum := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(challengeSum[:])

	secure := os.Getenv("WARDEN_COOKIE_SECURE") != "false"
	for name, value := range map[string]string{
		"oidc_state":    state,
		"oidc_nonce":    nonce,
		"oidc_verifier": codeVerifier,
	} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true,
			Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	}
	http.Redirect(w, r, oauth2Conf.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	), http.StatusFound)
}

// OIDCLoginCallback completes the authorization-code flow: it validates the
// state, nonce and PKCE verifier, exchanges the code, verifies the id_token,
// provisions a passwordless Warden user via the existing federated identity
// mechanism, then establishes a normal warden_session cookie and redirects to
// the console.
func (h *Handler) OIDCLoginCallback(w http.ResponseWriter, r *http.Request) {
	if h.oidc == nil {
		jsonError(w, http.StatusNotFound, "OIDC_NOT_CONFIGURED", "OIDC 登录未配置")
		return
	}
	if h.federated == nil {
		databaseUnavailable(w)
		return
	}
	// State cookies are only cleared on a successful login. Clearing them at
	// the top of the callback used to destroy the flow on any duplicate or
	// retried request (browser prefetch, back/refresh, double-click): the
	// second request then arrived with no cookie and reported a misleading
	// INVALID_OIDC_STATE even though the IdP had returned a valid state.

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		log.Printf("OIDC provider returned error=%.200s description=%.200s",
			errParam, r.URL.Query().Get("error_description"))
		// The IdP-controlled parameter is reflected to the client; sanitize and
		// bound it like any other external error text.
		jsonError(w, http.StatusBadRequest, "OIDC_ERROR", "OIDC 登录失败: "+domain.SanitizeErrorMessage(errParam))
		return
	}
	stateCookie, _ := r.Cookie("oidc_state")
	state := ""
	if stateCookie != nil {
		state = stateCookie.Value
	}
	if state == "" || state != r.URL.Query().Get("state") {
		// Do not log the values. The extra fields distinguish a SameSite /
		// Secure / wrong-host cookie problem from an IdP that dropped the state.
		_, sessionErr := r.Cookie("warden_session")
		log.Printf("OIDC state mismatch: cookie_present=%t query_present=%t host=%s referer=%.120s secure_cookie=%t session_cookie=%t expected_origin=%s",
			state != "", r.URL.Query().Get("state") != "", r.Host, r.Referer(),
			os.Getenv("WARDEN_COOKIE_SECURE") != "false", sessionErr == nil,
			oidcLoginOrigin(h.oidc.config.RedirectURL))
		hint := ""
		if origin := oidcLoginOrigin(h.oidc.config.RedirectURL); origin != "" {
			hint = "；请从 " + origin + "/login 重新发起登录，不要刷新回调页"
		}
		jsonError(w, http.StatusBadRequest, "INVALID_OIDC_STATE", "OIDC state 校验失败"+hint)
		return
	}
	nonce := ""
	if nonceCookie, err := r.Cookie("oidc_nonce"); err == nil {
		nonce = nonceCookie.Value
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		log.Print("OIDC callback has no authorization code")
		jsonError(w, http.StatusBadRequest, "OIDC_NO_CODE", "OIDC 回调缺少授权码")
		return
	}
	verifierCookie, verifierErr := r.Cookie("oidc_verifier")
	if verifierErr != nil || verifierCookie == nil || verifierCookie.Value == "" {
		// Refusing to fall back to a non-PKCE exchange prevents a downgrade
		// attack where an attacker strips the challenge from the authorize URL.
		log.Print("OIDC callback missing PKCE verifier cookie")
		jsonError(w, http.StatusBadRequest, "OIDC_PKCE_MISSING", "OIDC PKCE verifier 缺失，请重新发起登录")
		return
	}
	ctx := r.Context()
	_, idTokenVerifier, oauth2Conf, err := h.oidc.resolve(ctx)
	if err != nil {
		jsonError(w, http.StatusServiceUnavailable, "OIDC_PROVIDER_ERROR", "OIDC 身份提供方暂不可用")
		return
	}
	oauth2Token, err := oauth2Conf.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", verifierCookie.Value))
	if err != nil {
		// The error text carries the token endpoint response (invalid_client,
		// invalid_grant, redirect_uri mismatch), which is exactly what is needed
		// to diagnose IdP registration problems. It never contains the secret.
		log.Printf("OIDC token exchange failed: %v", err)
		jsonError(w, http.StatusUnauthorized, "OIDC_EXCHANGE_FAILED", "OIDC 授权码兑换失败")
		return
	}
	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		log.Print("OIDC token response has no id_token")
		jsonError(w, http.StatusUnauthorized, "OIDC_NO_ID_TOKEN", "OIDC 响应缺少 id_token")
		return
	}
	idToken, err := idTokenVerifier.Verify(ctx, rawIDToken)
	if err != nil {
		log.Printf("OIDC id_token verification failed: %v", err)
		jsonError(w, http.StatusUnauthorized, "OIDC_VERIFY_FAILED", "OIDC id_token 校验失败")
		return
	}
	if nonce == "" || idToken.Nonce != nonce {
		// Some identity brokers do not echo the nonce claim. This flag makes
		// that failure mode obvious without logging the nonce itself.
		log.Printf("OIDC nonce mismatch: cookie_present=%t token_has_nonce_claim=%t",
			nonce != "", idToken.Nonce != "")
		jsonError(w, http.StatusUnauthorized, "OIDC_NONCE_MISMATCH", "OIDC nonce 校验失败")
		return
	}
	var claims struct {
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		log.Printf("OIDC claims decode failed: %v", err)
		jsonError(w, http.StatusUnauthorized, "OIDC_CLAIMS_FAILED", "解析 OIDC 用户信息失败")
		return
	}
	issuer := idToken.Issuer
	subject := idToken.Subject
	// A missing subject would map every federated user onto the same
	// (issuer, subject) row, silently sharing one Warden principal. Fail closed
	// instead of provisioning a shared identity.
	if issuer == "" || subject == "" {
		log.Printf("OIDC id_token missing subject: issuer_present=%t subject_present=%t",
			issuer != "", subject != "")
		jsonError(w, http.StatusUnauthorized, "OIDC_INVALID_SUBJECT", "OIDC id_token 缺少用户唯一标识")
		return
	}
	// A brand-new federated identity that matches no configured email-domain
	// rule lands in the unaffiliated sentinel, NOT in the platform tenant. It
	// is authenticated but belongs to no organization until a super admin
	// assigns it or a tenant invitation is accepted. Returning identities keep
	// their original binding; a trusted IdP email may still route a newcomer
	// straight into the matching tenant. Self-asserted emails never apply.
	tenantID := unaffiliatedTenant
	if h.oidc.config.DefaultTenantID != "" {
		// Legacy fallback retained only for operators who explicitly configure a
		// default tenant; a newcomer then lands there instead of unaffiliated.
		tenantID = h.oidc.config.DefaultTenantID
	}
	if bound, found, err := h.federated.IdentityTenant(ctx, issuer, subject); err != nil {
		log.Printf("OIDC identity binding lookup failed: %v", err)
	} else if found {
		tenantID = bound
	} else if claims.Email != "" {
		if routed := h.routeTenantByEmail(ctx, claims.Email); routed != "" {
			tenantID = routed
			log.Printf("OIDC tenant routing: trusted email domain matched tenant=%s", routed)
		} else if h.oidc.config.DefaultTenantID == "" {
			log.Printf("OIDC new identity unaffiliated: no email-domain rule matched")
		}
	}
	metadata := map[string]any{
		"oidc":    true,
		"issuer":  issuer,
		"subject": subject,
		"source":  "oidc",
	}
	if claims.Email != "" {
		metadata["email"] = claims.Email
	}
	if claims.Name != "" {
		metadata["display_name"] = claims.Name
	}
	if claims.Picture != "" {
		metadata["avatar_url"] = claims.Picture
	}
	provisionCtx := domain.WithTenantID(ctx, tenantID)
	principal, err := h.federated.ProvisionIdentity(provisionCtx, issuer, subject,
		claims.Email, claims.Name, tenantID, h.oidc.config.DefaultRole, metadata)
	if err != nil {
		log.Printf("OIDC identity provisioning failed: issuer=%s tenant=%s err=%v", issuer, tenantID, err)
		if errors.Is(err, postgres.ErrFederatedIdentityTenant) {
			jsonError(w, http.StatusForbidden, "OIDC_IDENTITY_TENANT_MISMATCH", "OIDC 用户已绑定其他 Warden Tenant")
			return
		}
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "Provision Warden 用户失败")
		return
	}
	// Carry the tenant into session establishment so the login audit event is
	// written with the correct tenant_id and stays visible in the audit page.
	sessionCtx := WithTenantContext(r.Context(), tenantID)
	if _, err := h.establishSession(w, r.WithContext(sessionCtx), principal); err != nil {
		log.Printf("OIDC session establishment failed: principal=%s err=%v", principal.ID, err)
		jsonError(w, http.StatusInternalServerError, "TOKEN_ISSUE_FAILED", "签发登录令牌失败")
		return
	}
	// Only non-PII identifiers are logged; subject and email stay out of logs.
	log.Printf("OIDC login succeeded: principal=%s tenant=%s issuer=%s", principal.ID, tenantID, issuer)
	clearOIDCCookies(w)
	http.Redirect(w, r, "/", http.StatusFound)
}

// OIDCLogout performs RP-initiated logout: it revokes the Warden session and
// forwards the browser to the IdP end_session endpoint when the provider
// advertises one. Without this step the IdP session stays alive and clicking
// SSO immediately signs the user back in.
func (h *Handler) OIDCLogout(w http.ResponseWriter, r *http.Request) {
	if h.oidc == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if sessionCookie, err := r.Cookie("warden_session"); err == nil && h.pool != nil {
		if claims, err := h.parseJWT(sessionCookie.Value); err == nil && claims != nil && claims.ID != "" {
			_, _ = h.pool.Exec(r.Context(),
				`UPDATE admin_sessions SET revoked_at=now() WHERE jti=$1 AND revoked_at IS NULL`, claims.ID)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "warden_session", Value: "", Path: "/", HttpOnly: true,
		Secure: os.Getenv("WARDEN_COOKIE_SECURE") != "false", SameSite: http.SameSiteStrictMode, MaxAge: -1})
	clearOIDCCookies(w)

	origin := oidcLoginOrigin(h.oidc.config.RedirectURL)
	target := "/login"
	if provider, _, _, err := h.oidc.resolve(r.Context()); err == nil {
		var discovery struct {
			EndSessionEndpoint string `json:"end_session_endpoint"`
		}
		if err := provider.Claims(&discovery); err == nil && discovery.EndSessionEndpoint != "" {
			if endpoint, err := url.Parse(discovery.EndSessionEndpoint); err == nil {
				query := endpoint.Query()
				query.Set("client_id", h.oidc.config.ClientID)
				if origin != "" {
					// Must match one of the IdP's registered
					// post_logout_redirect_uris exactly.
					query.Set("post_logout_redirect_uri", origin+"/")
				}
				endpoint.RawQuery = query.Encode()
				target = endpoint.String()
			}
		}
	} else {
		log.Printf("OIDC logout discovery failed, falling back to local logout: %v", err)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func clearOIDCCookies(w http.ResponseWriter) {
	// Deletion must mirror the attributes used when the cookies were set.
	secure := os.Getenv("WARDEN_COOKIE_SECURE") != "false"
	for _, name := range []string{"oidc_state", "oidc_nonce", "oidc_verifier"} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true,
			Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	}
}
