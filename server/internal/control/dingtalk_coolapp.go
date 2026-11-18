package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"gitee.com/warden-platform/warden/server/internal/config"
	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/golang-jwt/jwt/v5"
)

const (
	dingTalkCookieName = "warden_dingtalk_session"
	dingTalkAudience   = "warden-dingtalk-coolapp"
)

type dingTalkCoolAppClient struct {
	appKey, appSecret   string
	http                *http.Client
	tokenEndpoint       string
	userTokenEndpoint   string
	currentUserEndpoint string
	unionUserEndpoint   string
	userDetailEndpoint  string
	mu                  sync.Mutex
	accessToken         string
	tokenExpiry         time.Time
}

func loadDingTalkCoolAppClient() (*dingTalkCoolAppClient, error) {
	appKey, err := config.Secret("WARDEN_DINGTALK_APP_KEY")
	if err != nil {
		return nil, err
	}
	appSecret, err := config.Secret("WARDEN_DINGTALK_APP_SECRET")
	if err != nil {
		return nil, err
	}
	if appKey == "" && appSecret == "" {
		return nil, nil
	}
	if appKey == "" || appSecret == "" {
		return nil, errors.New("WARDEN_DINGTALK_APP_KEY and WARDEN_DINGTALK_APP_SECRET must be configured together")
	}
	if len(appSecret) < 16 {
		return nil, errors.New("WARDEN_DINGTALK_APP_SECRET must contain at least 16 bytes")
	}
	return &dingTalkCoolAppClient{appKey: appKey, appSecret: appSecret, http: &http.Client{Timeout: 8 * time.Second},
		tokenEndpoint:       "https://api.dingtalk.com/v1.0/oauth2/accessToken",
		userTokenEndpoint:   "https://api.dingtalk.com/v1.0/oauth2/userAccessToken",
		currentUserEndpoint: "https://api.dingtalk.com/v1.0/contact/users/me",
		unionUserEndpoint:   "https://oapi.dingtalk.com/topapi/user/getbyunionid",
		userDetailEndpoint:  "https://oapi.dingtalk.com/topapi/v2/user/get"}, nil
}

func (c *dingTalkCoolAppClient) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry.Add(-2*time.Minute)) {
		return c.accessToken, nil
	}
	body, _ := json.Marshal(map[string]string{"appKey": c.appKey, "appSecret": c.appSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", errors.New("create DingTalk token request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", errors.New("DingTalk token network request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("DingTalk token HTTP %d", resp.StatusCode)
	}
	var result struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int    `json:"expireIn"`
	}
	if json.Unmarshal(data, &result) != nil || result.AccessToken == "" {
		return "", errors.New("DingTalk returned an invalid token response")
	}
	if result.ExpireIn <= 0 {
		result.ExpireIn = 7200
	}
	c.accessToken, c.tokenExpiry = result.AccessToken, time.Now().Add(time.Duration(result.ExpireIn)*time.Second)
	return c.accessToken, nil
}

// userName resolves the staff user ID carried by an authenticated DingTalk
// Stream callback. Directory lookup is enrichment rather than authorization:
// callers may safely fall back to the immutable user ID when it is unavailable.
func (c *dingTalkCoolAppClient) userName(ctx context.Context, userID string) (string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || len(userID) > 256 || strings.ContainsAny(userID, "\r\n") {
		return "", errors.New("invalid DingTalk user ID")
	}
	token, err := c.token(ctx)
	if err != nil {
		return "", err
	}
	form := url.Values{"access_token": {token}, "userid": {userID}, "language": {"zh_CN"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.userDetailEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("create DingTalk user detail request failed")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", errors.New("DingTalk user detail network request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("DingTalk user detail HTTP %d", resp.StatusCode)
	}
	var result struct {
		ErrorCode int `json:"errcode"`
		Result    struct {
			UserID string `json:"userid"`
			Name   string `json:"name"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &result) != nil {
		return "", errors.New("DingTalk returned an invalid user detail response")
	}
	if result.ErrorCode != 0 {
		return "", fmt.Errorf("DingTalk rejected user detail lookup: code %d", result.ErrorCode)
	}
	if strings.TrimSpace(result.Result.UserID) != userID || strings.TrimSpace(result.Result.Name) == "" {
		return "", errors.New("DingTalk returned an invalid user detail response")
	}
	return strings.TrimSpace(result.Result.Name), nil
}

func (c *dingTalkCoolAppClient) resolveUserID(ctx context.Context, authCode, corpID string) (string, error) {
	body, _ := json.Marshal(map[string]string{"clientId": c.appKey, "clientSecret": c.appSecret, "code": authCode, "grantType": "authorization_code"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.userTokenEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", errors.New("create DingTalk user token request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", errors.New("DingTalk user token network request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("DingTalk user token HTTP %d", resp.StatusCode)
	}
	var userTokenResult struct {
		AccessToken string `json:"accessToken"`
		CorpID      string `json:"corpId"`
	}
	if json.Unmarshal(data, &userTokenResult) != nil || strings.TrimSpace(userTokenResult.AccessToken) == "" {
		return "", errors.New("DingTalk returned an invalid user token response")
	}
	if userTokenResult.CorpID != "" && userTokenResult.CorpID != corpID {
		return "", errors.New("DingTalk user token corporation mismatch")
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, c.currentUserEndpoint, nil)
	if err != nil {
		return "", errors.New("create DingTalk current user request failed")
	}
	req.Header.Set("x-acs-dingtalk-access-token", userTokenResult.AccessToken)
	resp, err = c.http.Do(req)
	if err != nil {
		return "", errors.New("DingTalk current user network request failed")
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("DingTalk current user HTTP %d", resp.StatusCode)
	}
	var currentUser struct {
		UnionID string `json:"unionId"`
	}
	if json.Unmarshal(data, &currentUser) != nil || strings.TrimSpace(currentUser.UnionID) == "" {
		return "", errors.New("DingTalk returned an invalid current user response")
	}

	appToken, err := c.token(ctx)
	if err != nil {
		return "", err
	}
	endpoint, err := url.Parse(c.unionUserEndpoint)
	if err != nil {
		return "", errors.New("invalid DingTalk union user endpoint")
	}
	query := endpoint.Query()
	query.Set("access_token", appToken)
	endpoint.RawQuery = query.Encode()
	body, _ = json.Marshal(map[string]string{"unionid": strings.TrimSpace(currentUser.UnionID)})
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", errors.New("create DingTalk union user request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = c.http.Do(req)
	if err != nil {
		return "", errors.New("DingTalk union user network request failed")
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("DingTalk union user HTTP %d", resp.StatusCode)
	}
	var result struct {
		ErrorCode    int    `json:"errcode"`
		ErrorMessage string `json:"errmsg"`
		Result       struct {
			UserID string `json:"userid"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &result) != nil {
		return "", errors.New("DingTalk returned an invalid union user response")
	}
	if result.ErrorCode != 0 || strings.TrimSpace(result.Result.UserID) == "" {
		return "", fmt.Errorf("DingTalk rejected union user lookup: code %d", result.ErrorCode)
	}
	return strings.TrimSpace(result.Result.UserID), nil
}

func (h *Handler) DingTalkConfig(w http.ResponseWriter, _ *http.Request) {
	if h.dingTalkCoolApp == nil {
		jsonError(w, http.StatusConflict, "DINGTALK_COOLAPP_NOT_CONFIGURED", "钉钉酷应用免登凭据尚未配置")
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"client_id": h.dingTalkCoolApp.appKey})
}

type dingTalkClaims struct {
	UserID             string `json:"dingtalk_user_id"`
	DisplayName        string `json:"dingtalk_user_name,omitempty"`
	PrincipalID        string `json:"principal_id"`
	TenantID           string `json:"tenant_id"`
	CorpID             string `json:"corp_id"`
	OpenConversationID string `json:"open_conversation_id,omitempty"`
	jwt.RegisteredClaims
}

func (h *Handler) issueDingTalkSession(userID, displayName, corpID, conversationID, tenantID string) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(2 * time.Hour)
	claims := dingTalkClaims{UserID: userID, DisplayName: displayName, PrincipalID: adminPrincipalID, TenantID: tenantID, CorpID: corpID, OpenConversationID: conversationID,
		RegisteredClaims: jwt.RegisteredClaims{Subject: adminPrincipalID, Issuer: jwtIssuer, Audience: jwt.ClaimStrings{dingTalkAudience}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(expiresAt)}}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(h.jwtSigningKey)
	return signed, expiresAt, err
}

func (h *Handler) parseDingTalkSession(raw string) (*dingTalkClaims, error) {
	claims := &dingTalkClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return h.jwtSigningKey, nil
	},
		jwt.WithIssuer(jwtIssuer), jwt.WithAudience(dingTalkAudience), jwt.WithIssuedAt(), jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || claims.UserID == "" || claims.PrincipalID == "" || claims.TenantID == "" {
		// Sessions issued before tenant-level group isolation carry no tenant
		// claim; they are rejected so the user re-authenticates through a bound
		// group (2h TTL converges quickly).
		return nil, errors.New("invalid DingTalk session")
	}
	return claims, nil
}

func dingTalkRequestOriginValid(r *http.Request) bool {
	expected, err := configuredCORSOrigin()
	return err == nil && r.Header.Get("Origin") == expected
}

func (h *Handler) DingTalkAuth(w http.ResponseWriter, r *http.Request) {
	if h.dingTalkCoolApp == nil {
		jsonError(w, http.StatusConflict, "DINGTALK_COOLAPP_NOT_CONFIGURED", "钉钉酷应用免登凭据尚未配置")
		return
	}
	if h.dingTalkRepo == nil {
		databaseUnavailable(w)
		return
	}
	if !dingTalkRequestOriginValid(r) {
		jsonError(w, http.StatusForbidden, "CSRF_REJECTED", "请求来源无效")
		return
	}
	var body struct {
		AuthCode           string `json:"auth_code"`
		CorpID             string `json:"corp_id"`
		OpenConversationID string `json:"open_conversation_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体格式不正确")
		return
	}
	body.AuthCode = strings.TrimSpace(body.AuthCode)
	body.CorpID = strings.TrimSpace(body.CorpID)
	body.OpenConversationID = strings.TrimSpace(body.OpenConversationID)
	if body.AuthCode == "" || len(body.AuthCode) > 512 || body.CorpID == "" || len(body.CorpID) > 128 || len(body.OpenConversationID) > 512 {
		jsonError(w, http.StatusBadRequest, "INVALID_DINGTALK_CONTEXT", "钉钉免登或群上下文参数不正确")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	userID, err := h.dingTalkCoolApp.resolveUserID(ctx, body.AuthCode, body.CorpID)
	if err != nil {
		// The client error intentionally stays generic. The internal error only
		// contains the upstream status/error code and never the auth code, access
		// token, AppKey, or AppSecret.
		log.Printf("DingTalk auth code exchange failed: %v", err)
		jsonError(w, http.StatusUnauthorized, "DINGTALK_AUTH_FAILED", "钉钉免登失败，请从群快捷入口重新打开")
		return
	}
	// Identity binding is no longer required: any member of the configured
	// approval group may decide, and the decision is audited with the DingTalk
	// user's real name (same trust model as the interactive card buttons).
	// The group itself is the tenant anchor: each tenant binds its own group,
	// so the queue, decisions and audit stay inside that tenant's boundary.
	tenantID, err := h.resolveDingTalkTenant(ctx, body.OpenConversationID)
	if err != nil {
		log.Printf("DingTalk group tenant resolution failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "解析审批群归属失败")
		return
	}
	if tenantID == "" {
		jsonError(w, http.StatusConflict, "DINGTALK_GROUP_NOT_BOUND", "该群未绑定任何组织的审批渠道，请由租户管理员在钉钉审批设置中绑定本组织的审批群")
		return
	}
	if !h.tenantIsActive(ctx, tenantID) {
		jsonError(w, http.StatusForbidden, "DINGTALK_TENANT_DISABLED", "该群绑定的组织已停用")
		return
	}
	displayName := userID
	if resolvedName, lookupErr := h.dingTalkCoolApp.userName(ctx, userID); lookupErr != nil {
		log.Printf("DingTalk coolapp user lookup failed: user_id=%s error=%v", userID, lookupErr)
	} else {
		displayName = sanitizeDingTalkText(resolvedName)
	}
	// The built-in admin principal is a platform fixture; look it up in the
	// platform tenant while all business data stays in the bound tenant.
	principal, err := h.principals.GetByID(domain.WithTenantID(ctx, "platform"), adminPrincipalID)
	if err != nil || principal == nil || principal.Status != domain.PrincipalStatusActive || principal.Kind != domain.PrincipalKindUser {
		jsonError(w, http.StatusForbidden, "DINGTALK_PRINCIPAL_DISABLED", "Warden 审批身份不可用")
		return
	}
	token, expiresAt, err := h.issueDingTalkSession(userID, displayName, body.CorpID, body.OpenConversationID, tenantID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "SESSION_CREATE_FAILED", "创建钉钉审批会话失败")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: dingTalkCookieName, Value: token, Path: "/api/v1/dingtalk", HttpOnly: true, Secure: os.Getenv("WARDEN_COOKIE_SECURE") != "false", SameSite: http.SameSiteLaxMode, Expires: expiresAt})
	h.writeNotificationAudit(domain.WithTenantID(ctx, tenantID), "dingtalk.coolapp.login", userID, "钉钉酷应用审批用户免登成功", map[string]any{
		"dingtalk_user_id": userID, "dingtalk_user_name": displayName,
	}, principal.ID, "user")
	jsonResp(w, http.StatusOK, map[string]any{"user": map[string]string{"id": userID, "name": displayName}, "expires_at": expiresAt})
}

func (h *Handler) dingTalkIdentity(r *http.Request) (*AuthenticatedIdentity, *dingTalkClaims, error) {
	cookie, err := r.Cookie(dingTalkCookieName)
	if err != nil {
		return nil, nil, err
	}
	claims, err := h.parseDingTalkSession(cookie.Value)
	if err != nil {
		return nil, nil, err
	}
	if !h.tenantIsActive(r.Context(), claims.TenantID) {
		return nil, nil, errors.New("DingTalk tenant disabled")
	}
	principal, err := h.principals.GetByID(domain.WithTenantID(r.Context(), "platform"), adminPrincipalID)
	if err != nil || principal == nil || principal.Status != domain.PrincipalStatusActive || principal.Kind != domain.PrincipalKindUser {
		return nil, nil, errors.New("DingTalk principal unavailable")
	}
	identity := &AuthenticatedIdentity{Principal: principal, TenantID: claims.TenantID, Scopes: []string{"warden:approve"}, Kind: "DINGTALK_COOLAPP", AuditMetadata: map[string]any{
		"auth_channel": "dingtalk_coolapp", "dingtalk_user_id": claims.UserID, "dingtalk_user_name": claims.DisplayName,
	}}
	return identity, claims, nil
}

func (h *Handler) DingTalkListAccessRequests(w http.ResponseWriter, r *http.Request) {
	if h.dingTalkRepo == nil {
		databaseUnavailable(w)
		return
	}
	_, claims, err := h.dingTalkIdentity(r)
	if err != nil {
		jsonError(w, http.StatusUnauthorized, "DINGTALK_SESSION_INVALID", "钉钉审批会话已过期，请重新打开酷应用")
		return
	}
	ctx := domain.WithTenantID(r.Context(), claims.TenantID)
	requests, err := h.requests.List(ctx, string(domain.RequestPending), 50, 0)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询审批请求失败")
		return
	}
	// Batch-load display context: one query per entity kind instead of four
	// queries per request.
	uniqueIDs := func(values []string) []string {
		seen := make(map[string]bool, len(values))
		out := make([]string, 0, len(values))
		for _, id := range values {
			if id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		return out
	}
	principalIDs := make([]string, 0, len(requests))
	resourceIDs := make([]string, 0, len(requests))
	actionIDs := make([]string, 0, len(requests))
	for _, request := range requests {
		principalIDs = append(principalIDs, request.PrincipalID)
		resourceIDs = append(resourceIDs, request.ResourceID)
		actionIDs = append(actionIDs, request.ActionID)
	}
	pMap, _ := h.principals.SummariesByIDs(ctx, uniqueIDs(principalIDs))
	// Full rows (not summaries) so the effective environment inheritance is
	// preserved exactly as the previous per-request GetByID provided.
	rMap, _ := h.resources.ByIDs(ctx, uniqueIDs(resourceIDs))
	aMap, _ := h.actions.SummariesByIDs(ctx, uniqueIDs(actionIDs))
	result := make([]map[string]any, 0, len(requests))
	for _, request := range requests {
		item := map[string]any{"id": request.ID, "status": request.Status, "reason": request.Reason, "parameters": request.Parameters, "expires_at": request.ExpiresAt, "created_at": request.CreatedAt}
		if principal, ok := pMap[request.PrincipalID]; ok {
			item["applicant"] = principal.Name
		}
		if resource, ok := rMap[request.ResourceID]; ok {
			item["resource_name"] = resource.Name
			item["resource_type"] = resource.ResourceType
			item["environment"] = resource.Environment
		}
		if action, ok := aMap[request.ActionID]; ok {
			item["action_name"] = action.Name
			item["risk_level"] = action.RiskLevel
		}
		result = append(result, item)
	}
	jsonResp(w, http.StatusOK, result)
}

func (h *Handler) dingTalkDecide(w http.ResponseWriter, r *http.Request, approve bool) {
	if !dingTalkRequestOriginValid(r) {
		jsonError(w, http.StatusForbidden, "CSRF_REJECTED", "请求来源无效")
		return
	}
	actor, claims, err := h.dingTalkIdentity(r)
	if err != nil {
		jsonError(w, http.StatusUnauthorized, "DINGTALK_SESSION_INVALID", "钉钉审批会话已过期，请重新打开酷应用")
		return
	}
	ctx := domain.WithTenantID(r.Context(), claims.TenantID)
	request, err := h.decideApproval(ctx, r.PathValue("id"), approve, actor)
	if err != nil {
		status, code, message := approvalDecisionHTTPError(err)
		jsonError(w, status, code, message)
		return
	}
	event, status := "access_request.approved", "APPROVED"
	if !approve {
		event, status = "access_request.rejected", "REJECTED"
	}
	approverName := claims.DisplayName
	if approverName == "" {
		approverName = claims.UserID
	}
	h.notifyApprovalAsync(ctx, *request, event, approverName)
	jsonResp(w, http.StatusOK, map[string]string{"id": request.ID, "status": status})
}

func (h *Handler) DingTalkApproveRequest(w http.ResponseWriter, r *http.Request) {
	h.dingTalkDecide(w, r, true)
}
func (h *Handler) DingTalkRejectRequest(w http.ResponseWriter, r *http.Request) {
	h.dingTalkDecide(w, r, false)
}

func (h *Handler) DingTalkLogout(w http.ResponseWriter, r *http.Request) {
	if !dingTalkRequestOriginValid(r) {
		jsonError(w, http.StatusForbidden, "CSRF_REJECTED", "请求来源无效")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: dingTalkCookieName, Value: "", Path: "/api/v1/dingtalk", HttpOnly: true, Secure: os.Getenv("WARDEN_COOKIE_SECURE") != "false", SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}
