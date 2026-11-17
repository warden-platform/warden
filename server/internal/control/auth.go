package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitee.com/pam-platform/warden/server/internal/config"
	"gitee.com/pam-platform/warden/server/internal/domain"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// adminPrincipalID is the seeded admin user (migration 002_seed.sql).
const adminPrincipalID = "00000000-0000-0000-0000-000000000003"

const (
	jwtIssuer   = "warden-control"
	jwtAudience = "warden-admin"
)

// PrincipalStore abstracts principal persistence so auth-related handlers
// can be unit-tested without a live PostgreSQL connection.
type PrincipalStore interface {
	GetByID(ctx context.Context, id string) (*domain.Principal, error)
	GetByName(ctx context.Context, name string) (*domain.Principal, error)
	List(ctx context.Context, kind string) ([]domain.Principal, error)
	Create(ctx context.Context, p *domain.Principal) error
	UpdatePassword(ctx context.Context, id, hash string) error
	UpdateName(ctx context.Context, id, name string) error
	UpdateLastLogin(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}

// --- Password hashing (bcrypt) ---

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func verifyPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// --- JWT (HMAC-SHA256) ---

type wardenClaims struct {
	Name   string   `json:"name"`
	Tenant string   `json:"tenant"`
	Scope  []string `json:"scope"`
	jwt.RegisteredClaims
}

// jwtTTL returns the JWT lifetime. Defaults to 8 hours; overridable via
// WARDEN_JWT_TTL_HOURS (must parse as a positive integer).
func jwtTTL() time.Duration {
	if raw := os.Getenv("WARDEN_JWT_TTL_HOURS"); raw != "" {
		if hours, err := strconv.Atoi(raw); err == nil && hours > 0 {
			return time.Duration(hours) * time.Hour
		}
	}
	return 8 * time.Hour
}

func (h *Handler) issueJWT(principal *domain.Principal) (string, time.Time, error) {
	return h.issueJWTWithTTL(principal, jwtTTL())
}

// issueJWTWithTTL is used by federation exchanges so an external assertion
// cannot be traded for the normal browser session lifetime. Federated callers
// should re-assert when this short token expires; refresh tokens are
// intentionally not introduced in this phase.
func (h *Handler) issueJWTWithTTL(principal *domain.Principal, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		return "", time.Time{}, errors.New("JWT TTL must be positive")
	}
	expiresAt := time.Now().Add(ttl)
	jti, err := randomToken(24)
	if err != nil {
		return "", time.Time{}, err
	}
	// Roles map to scopes in exactly one place (authorization.go); the same
	// scope names are expanded into the console's capability list so the UI
	// and the backend guards never disagree about what a role can do.
	scopes := defaultUserScopes()
	if principal != nil && principal.ID == adminPrincipalID {
		scopes = []string{ScopeAdmin}
	} else if principal != nil && isUnaffiliated(principal.TenantID) {
		// An unaffiliated identity is authenticated but has no organization. It
		// receives only the self-profile scope (a session token must carry a
		// non-empty scope) and none of the business capabilities, so every
		// tenant-scoped endpoint is denied until a super admin assigns it.
		scopes = []string{ScopeSelfProfile}
	} else if principal != nil && principal.Metadata != nil {
		switch role, _ := principal.Metadata["role"].(string); role {
		case RolePlatformAdmin:
			// Federated administrators receive the same wildcard scope as the
			// seeded break-glass admin; IsPlatformAdmin is derived from it.
			scopes = []string{ScopeAdmin}
		case RoleTenantAdmin:
			scopes = tenantAdminScopes()
		}
	}
	claims := wardenClaims{
		Name:   principal.Name,
		Tenant: principal.TenantID,
		Scope:  scopes,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Subject:   principal.ID,
			Issuer:    jwtIssuer,
			Audience:  jwt.ClaimStrings{jwtAudience},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(h.jwtSigningKey)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

func (h *Handler) parseJWT(raw string) (*wardenClaims, error) {
	claims := &wardenClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return h.jwtSigningKey, nil
	}, jwt.WithIssuer(jwtIssuer), jwt.WithAudience(jwtAudience), jwt.WithIssuedAt(),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(claims.Subject) == "" || len(claims.Scope) == 0 {
		return nil, errors.New("JWT subject and scope are required")
	}
	return claims, nil
}

// --- Admin password seeding ---

// EnsureAdminPassword synchronizes the bootstrap administrator's login name and
// password hash from explicit startup configuration. The fixed principal ID is
// only an internal authorization anchor; deployments choose their own login.
func (h *Handler) EnsureAdminPassword(ctx context.Context) error {
	if h.principals == nil {
		return nil
	}
	username := strings.TrimSpace(os.Getenv("WARDEN_ADMIN_USERNAME"))
	if !validLoginName(username) {
		return fmt.Errorf("WARDEN_ADMIN_USERNAME is required and must be a valid login name without @")
	}
	password, err := config.Secret("WARDEN_ADMIN_PASSWORD")
	if err != nil {
		return fmt.Errorf("read WARDEN_ADMIN_PASSWORD: %w", err)
	}
	if password == "" {
		return fmt.Errorf("WARDEN_ADMIN_PASSWORD or WARDEN_ADMIN_PASSWORD_FILE is required")
	}
	if len(password) < 8 || len(password) > 72 {
		return fmt.Errorf("WARDEN_ADMIN_PASSWORD must contain 8-72 bytes")
	}
	// The platform administrator is the bootstrap identity. Its tenant is
	// deliberately fixed to the migrated platform tenant for this lookup; normal
	// business repositories must never infer a tenant this way.
	principal, err := h.principals.GetByID(domain.WithTenantID(ctx, "platform"), adminPrincipalID)
	if err != nil {
		return fmt.Errorf("query admin principal: %w", err)
	}
	if principal == nil {
		return fmt.Errorf("admin principal %s does not exist", adminPrincipalID)
	}
	if principal.Name != username {
		if err := h.principals.UpdateName(domain.WithTenantID(ctx, "platform"), adminPrincipalID, username); err != nil {
			return fmt.Errorf("save admin username: %w", err)
		}
	}
	if principal.PasswordHash != "" && verifyPassword(password, principal.PasswordHash) {
		return nil
	}
	hash, err := hashPassword(password)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	if err := h.principals.UpdatePassword(ctx, adminPrincipalID, hash); err != nil {
		return fmt.Errorf("save admin password hash: %w", err)
	}
	log.Println("启动管理员账号已同步，Web 登录已启用")
	return nil
}

// --- HTTP handlers ---

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Tenant   string `json:"tenant,omitempty"`
}

// parseLoginIdentifier resolves a RAM-style qualified login name
// "user@tenant-slug" into its parts. An explicit tenant (legacy API callers
// and the platform operations login page) takes precedence. A bare "admin"
// keeps working as the break-glass platform alias for scripts; any other bare
// name is rejected so tenant users are never asked to guess a tenant.
func parseLoginIdentifier(username, explicitTenant string) (string, string, bool) {
	name := strings.TrimSpace(username)
	if t := strings.TrimSpace(explicitTenant); t != "" {
		return name, strings.ToLower(t), true
	}
	if i := strings.LastIndex(name, "@"); i > 0 && i < len(name)-1 {
		n, t := name[:i], strings.ToLower(name[i+1:])
		if !tenantSlugPattern.MatchString(t) {
			return "", "", false
		}
		return n, t, true
	}
	if name == "admin" {
		return name, "platform", true
	}
	return "", "", false
}

// validLoginName rejects '@' so qualified login names stay unambiguous.
func validLoginName(name string) bool {
	return name != "" && len([]rune(name)) <= 255 && !strings.Contains(name, "@")
}

type loginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	User      struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"user"`
}

type loginAttempt struct {
	count int
	reset time.Time
}

var loginAttempts = struct {
	sync.Mutex
	items map[string]loginAttempt
}{items: make(map[string]loginAttempt)}

func allowLoginAttempt(remoteAddr, username string, now time.Time) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	key := host + "\x00" + strings.ToLower(strings.TrimSpace(username))
	loginAttempts.Lock()
	defer loginAttempts.Unlock()
	for candidate, item := range loginAttempts.items {
		if !item.reset.After(now) {
			delete(loginAttempts.items, candidate)
		}
	}
	attempt := loginAttempts.items[key]
	if !attempt.reset.After(now) {
		attempt = loginAttempt{reset: now.Add(5 * time.Minute)}
	}
	if attempt.count >= 10 {
		return false
	}
	if _, exists := loginAttempts.items[key]; !exists && len(loginAttempts.items) >= 10000 {
		return false
	}
	attempt.count++
	loginAttempts.items[key] = attempt
	return true
}

func requestClientAddress(r *http.Request) string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("WARDEN_TRUST_PROXY_HEADERS")), "true") && requestFromTrustedProxy(r.RemoteAddr) {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(forwarded) != nil {
			return forwarded
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(realIP) != nil {
			return realIP
		}
	}
	return r.RemoteAddr
}

func requestFromTrustedProxy(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	for _, raw := range strings.Split(os.Getenv("WARDEN_TRUSTED_PROXY_CIDRS"), ",") {
		_, network, parseErr := net.ParseCIDR(strings.TrimSpace(raw))
		if parseErr == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func clearLoginAttempts(remoteAddr, username string) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	loginAttempts.Lock()
	delete(loginAttempts.items, host+"\x00"+strings.ToLower(strings.TrimSpace(username)))
	loginAttempts.Unlock()
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		jsonError(w, http.StatusServiceUnavailable, "NO_DATABASE", "数据库未连接")
		return
	}
	if h.principals == nil {
		jsonError(w, http.StatusServiceUnavailable, "LOGIN_DISABLED", "登录服务未配置")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req loginRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || len(req.Password) < 8 || len(req.Password) > 72 {
		jsonError(w, http.StatusBadRequest, "INVALID_CREDENTIALS", "用户名或密码格式不正确")
		return
	}
	loginName, tenant, ok := parseLoginIdentifier(username, req.Tenant)
	if !ok {
		jsonError(w, http.StatusBadRequest, "INVALID_LOGIN_NAME", "请使用 用户名@组织标识 格式登录（例如 zhangsan@acme）")
		return
	}
	clientAddress := requestClientAddress(r)
	if !allowLoginAttempt(clientAddress, username, time.Now()) {
		w.Header().Set("Retry-After", "300")
		jsonError(w, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", "登录尝试过于频繁，请稍后重试")
		return
	}
	loginCtx := r.Context()
	if !h.tenantIsActive(loginCtx, tenant) {
		jsonError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "用户名或密码错误")
		return
	}
	loginCtx = WithTenantContext(loginCtx, tenant)
	principal, err := h.principals.GetByName(loginCtx, loginName)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询用户失败")
		return
	}
	if principal == nil || principal.Kind != domain.PrincipalKindUser ||
		principal.Status != domain.PrincipalStatusActive || principal.PasswordHash == "" {
		jsonError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "用户名或密码错误")
		return
	}
	if !verifyPassword(req.Password, principal.PasswordHash) {
		jsonError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "用户名或密码错误")
		return
	}
	clearLoginAttempts(clientAddress, username)
	expiresAt, err := h.establishSession(w, r.WithContext(loginCtx), principal)
	if err != nil {
		log.Printf("establish session: %v", err)
		jsonError(w, http.StatusInternalServerError, "TOKEN_ISSUE_FAILED", "签发登录令牌失败")
		return
	}
	// The JWT is deliberately not exposed to browser JavaScript; the response
	// field remains for wire compatibility but is empty.
	resp := loginResponse{ExpiresAt: expiresAt}
	resp.User.ID = principal.ID
	resp.User.Name = principal.Name
	jsonResp(w, http.StatusOK, resp)
}

// establishSession issues a Warden JWT, records the session, and sets the
// pam_session cookie. It is shared by the password login and OIDC login paths
// so both produce an identical session contract.
func (h *Handler) establishSession(w http.ResponseWriter, r *http.Request, principal *domain.Principal) (time.Time, error) {
	token, expiresAt, err := h.issueJWT(principal)
	if err != nil {
		return time.Time{}, err
	}
	claims, err := h.parseJWT(token)
	if err != nil || claims.ID == "" {
		return time.Time{}, errors.New("session token validation failed")
	}
	if _, err := h.pool.Exec(r.Context(), `INSERT INTO admin_sessions (jti,principal_id,expires_at) VALUES ($1,$2,$3)`, claims.ID, principal.ID, expiresAt); err != nil {
		return time.Time{}, err
	}
	if err := h.principals.UpdateLastLogin(r.Context(), principal.ID); err != nil {
		log.Printf("更新 last_login_at 失败: %v", err)
	}
	h.writeIdentityAudit(r.Context(), "user.login", principal.ID, "")
	http.SetCookie(w, &http.Cookie{Name: "pam_session", Value: token, Path: "/", HttpOnly: true,
		Secure: os.Getenv("WARDEN_COOKIE_SECURE") != "false", SameSite: http.SameSiteStrictMode, Expires: expiresAt})
	return expiresAt, nil
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if identity := identityFromContext(r.Context()); identity != nil && identity.Kind == "JWT" && identity.SessionID != "" && h.pool != nil {
		_, _ = h.pool.Exec(r.Context(), `UPDATE admin_sessions SET revoked_at=now() WHERE jti=$1 AND revoked_at IS NULL`, identity.SessionID)
	}
	http.SetCookie(w, &http.Cookie{Name: "pam_session", Value: "", Path: "/", HttpOnly: true,
		Secure: os.Getenv("WARDEN_COOKIE_SECURE") != "false", SameSite: http.SameSiteStrictMode, MaxAge: -1})
	jsonResp(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	identity := identityFromContext(r.Context())
	if identity == nil || identity.Principal == nil {
		jsonError(w, http.StatusUnauthorized, "UNAUTHORIZED", "未认证")
		return
	}
	role := RoleUser
	if identity.IsPlatformAdmin {
		role = RolePlatformAdmin
	} else if value, _ := identity.Principal.Metadata["role"].(string); value != "" {
		role = value
	}
	// Federated identities keep an opaque, stable principals.name
	// (federated:<hash>) because it is unique per tenant and is referenced by
	// login, session and audit records. The human-readable label is published
	// separately so the console never has to show that hash.
	displayName, _ := identity.Principal.Metadata["display_name"].(string)
	email, _ := identity.Principal.Metadata["email"].(string)
	avatarURL, _ := identity.Principal.Metadata["avatar_url"].(string)
	displayName, email = strings.TrimSpace(displayName), strings.TrimSpace(email)
	avatarURL = strings.TrimSpace(avatarURL)
	authSource, _ := identity.Principal.Metadata["source"].(string)
	if authSource == "" {
		authSource = "local"
	}
	if h.pool != nil && (displayName == "" || email == "") {
		// Fallback for profiles provisioned before display attributes were
		// stored in metadata, or by a federation path that did not set them.
		var storedName, storedEmail *string
		if err := h.pool.QueryRow(r.Context(), `
			SELECT NULLIF(display_name,''), NULLIF(email,'')
			FROM federated_identities WHERE principal_id=$1::uuid
			ORDER BY last_login_at DESC NULLS LAST LIMIT 1`, identity.Principal.ID).
			Scan(&storedName, &storedEmail); err == nil {
			if displayName == "" && storedName != nil {
				displayName = *storedName
			}
			if email == "" && storedEmail != nil {
				email = *storedEmail
			}
		}
	}
	if displayName == "" && email != "" {
		displayName = email
	}
	if displayName == "" {
		displayName = identity.Principal.Name
	}
	var tenant *domain.Tenant
	if h.pool != nil && identity.TenantID != "" && !isUnaffiliated(identity.TenantID) {
		var current domain.Tenant
		if err := h.pool.QueryRow(r.Context(), `SELECT id,slug,name,status,settings,created_at,updated_at FROM tenants WHERE slug=$1`, identity.TenantID).
			Scan(&current.ID, &current.Slug, &current.Name, &current.Status, &current.Settings, &current.CreatedAt, &current.UpdatedAt); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询当前租户失败")
			return
		}
		tenant = &current
	}
	var expiresAt *time.Time
	if h.pool != nil && identity.SessionID != "" {
		var value time.Time
		if err := h.pool.QueryRow(r.Context(), `SELECT expires_at FROM admin_sessions WHERE jti=$1`, identity.SessionID).Scan(&value); err == nil {
			expiresAt = &value
		}
	}
	jsonResp(w, http.StatusOK, map[string]any{
		"id": identity.Principal.ID, "name": identity.Principal.Name, "kind": identity.Principal.Kind,
		"display_name": displayName, "email": email, "avatar_url": avatarURL,
		"auth_source": authSource,
		"tenant":      tenant, "role": role, "scopes": append([]string(nil), identity.Scopes...),
		"unaffiliated":      isUnaffiliated(identity.TenantID),
		"capabilities":      Capabilities(identity.Scopes),
		"is_platform_admin": identity.IsPlatformAdmin, "expires_at": expiresAt,
	})
}
