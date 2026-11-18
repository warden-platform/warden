package control

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

var credentialReferencePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)
var grafanaDashboardUIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,40}$`)
var huaweiCloudRegionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-\d{1,2}$`)
var aliyunSLSRegionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z0-9]+)+$`)
var aliyunSLSProjectEndpointPattern = regexp.MustCompile(`^([a-z0-9][a-z0-9-]{1,61}[a-z0-9])\.([a-z0-9-]+)\.log\.aliyuncs\.com$`)
var aliyunSLSProjectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
var h3cHostKeyFingerprintPattern = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]+={0,2}$`)
var h3cInterfaceNamePattern = regexp.MustCompile(`(?i)^(?:GE|XGE|GigabitEthernet|Ten-GigabitEthernet|BAGG|Bridge-Aggregation)[0-9/]+$`)

// databaseCredentialProfileNames is the closed set of purpose-bound database
// credential profiles. Routing is by action type, never by Agent choice.
var databaseCredentialProfileNames = map[string]bool{"query": true, "ddl": true}

// huaweiCloudServices are the P1 service scopes for the huaweicloud protocol.
var huaweiCloudServices = map[string]bool{"ecs": true, "rds": true, "elb": true, "obs": true, "lts": true, "swr": true}

// integrationInput is the API shape for create/update integration requests.
// Protocol drives validation dispatch.
type integrationInput struct {
	Protocol           string                                `json:"protocol"`
	Name               string                                `json:"name"`
	Endpoint           string                                `json:"endpoint"`
	Username           string                                `json:"username"`
	CredentialRef      string                                `json:"credentialRef"`
	CredentialProfiles []domain.IntegrationCredentialProfile `json:"credentialProfiles"`
	ProjectID          string                                `json:"projectId"`
	Environment        string                                `json:"environment"`
	Scope              map[string]any                        `json:"scope"`
	Status             string                                `json:"status"`
}

type integrationProjectScopeInput struct {
	DefaultProjectID string                          `json:"defaultProjectId"`
	Environment      string                          `json:"environment"`
	ProjectIDs       []string                        `json:"projectIds"`
	Rules            []domain.IntegrationProjectRule `json:"rules"`
}

type configError struct {
	message string
}

func (e *configError) Error() string { return e.message }

// --- Global / connector-scoped listing ---

// ListIntegrations answers "which integrations did I wire up?" across all
// connectors. Agent-facing (API key auth).
func (h *Handler) ListIntegrations(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	integrations, err := h.integrations.ListAll(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询 Integration 失败")
		return
	}
	if integrations == nil {
		integrations = []domain.Integration{}
	}
	jsonResp(w, http.StatusOK, integrations)
}

// ListConnectorIntegrations serves both the admin-scoped connector detail route
// and the HMAC-authenticated Connector runtime route.
func (h *Handler) ListConnectorIntegrations(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	connectorID := r.PathValue("id")
	integrations, err := h.integrations.ListByConnector(r.Context(), connectorID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询 Integration 失败")
		return
	}
	if integrations == nil {
		integrations = []domain.Integration{}
	}
	jsonResp(w, http.StatusOK, integrations)
}

// --- CRUD ---

func (h *Handler) CreateIntegration(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	connectorID := r.PathValue("id")
	var input integrationInput
	if err := decodeIntegration(r, &input); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_INTEGRATION", err.Error())
		return
	}
	if err := validateIntegration(input); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_INTEGRATION", err.Error())
		return
	}
	projectID, err := h.resolveIntegrationProjectID(r.Context(), input.ProjectID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PROJECT_LOOKUP_FAILED", "读取 Integration 默认项目失败")
		return
	}
	if projectID == "" {
		jsonError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "项目不存在")
		return
	}
	input.ProjectID = projectID
	ig := buildIntegration(connectorID, input)
	ig.Status = domain.IntegrationActive
	if err := h.integrations.Create(r.Context(), ig); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "创建 Integration 失败")
		return
	}
	if err := h.integrations.ReplaceCredentialProfiles(r.Context(), ig.ID, ig.CredentialProfiles); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "写入 Integration 凭证 Profile 失败")
		return
	}
	_ = h.writeAudit(r.Context(), "integration.created", nil, nil, "", "")
	jsonResp(w, http.StatusCreated, ig)
}

func (h *Handler) UpdateIntegration(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	connectorID := r.PathValue("id")
	integrationID := r.PathValue("integrationId")
	existing, err := h.integrations.GetByID(r.Context(), integrationID)
	if err != nil || existing == nil || existing.ConnectorID != connectorID {
		jsonError(w, http.StatusNotFound, "INTEGRATION_NOT_FOUND", "Integration 不存在")
		return
	}
	var input integrationInput
	if err := decodeIntegration(r, &input); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_INTEGRATION", err.Error())
		return
	}
	if err := validateIntegration(input); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_INTEGRATION", err.Error())
		return
	}
	projectID, err := h.resolveIntegrationProjectID(r.Context(), input.ProjectID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PROJECT_LOOKUP_FAILED", "读取 Integration 默认项目失败")
		return
	}
	if projectID == "" {
		jsonError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "项目不存在")
		return
	}
	input.ProjectID = projectID
	ig := buildIntegration(connectorID, input)
	ig.ID = integrationID
	ig.Status = existing.Status
	if input.Status != "" {
		ig.Status = domain.IntegrationStatus(input.Status)
	}
	if err := h.integrations.Update(r.Context(), ig); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新 Integration 失败")
		return
	}
	if err := h.integrations.ReplaceCredentialProfiles(r.Context(), integrationID, ig.CredentialProfiles); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新 Integration 凭证 Profile 失败")
		return
	}
	_ = h.writeAudit(r.Context(), "integration.updated", nil, nil, "", "")
	jsonResp(w, http.StatusOK, ig)
}

func (h *Handler) DeleteIntegration(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	connectorID := r.PathValue("id")
	integrationID := r.PathValue("integrationId")
	if err := h.integrations.Delete(r.Context(), integrationID, connectorID); err != nil {
		jsonError(w, http.StatusNotFound, "INTEGRATION_NOT_FOUND", "Integration 不存在")
		return
	}
	_ = h.writeAudit(r.Context(), "integration.deleted", nil, nil, "", "")
	jsonResp(w, http.StatusOK, map[string]string{"id": integrationID, "status": "DELETED"})
}

func (h *Handler) GetIntegrationProjectScope(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	ig, err := h.integrations.GetByID(r.Context(), r.PathValue("integrationId"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询 Integration 项目范围失败")
		return
	}
	if ig == nil {
		jsonError(w, http.StatusNotFound, "INTEGRATION_NOT_FOUND", "Integration 不存在")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{
		"default_project_id": ig.ProjectID, "environment": ig.Environment, "project_ids": ig.ProjectIDs, "rules": ig.ProjectRules,
	})
}

func (h *Handler) UpdateIntegrationProjectScope(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	ctx := r.Context()
	integrationID := r.PathValue("integrationId")
	ig, err := h.integrations.GetByID(ctx, integrationID)
	if err != nil || ig == nil {
		jsonError(w, http.StatusNotFound, "INTEGRATION_NOT_FOUND", "Integration 不存在")
		return
	}
	var input integrationProjectScopeInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_PROJECT_SCOPE", "项目范围请求体无效")
		return
	}
	if len(input.ProjectIDs) > 100 || len(input.Rules) > 200 {
		jsonError(w, http.StatusBadRequest, "INVALID_PROJECT_SCOPE", "最多允许 100 个服务项目和 200 条路由规则")
		return
	}
	input.Environment = strings.TrimSpace(input.Environment)
	if !validEnvironment(input.Environment, true) {
		jsonError(w, http.StatusBadRequest, "INVALID_ENVIRONMENT", "环境必须是开发、测试、生产或继承执行通道")
		return
	}
	defaultProjectID, err := h.resolveIntegrationProjectID(ctx, input.DefaultProjectID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PROJECT_LOOKUP_FAILED", "读取 Integration 默认项目失败")
		return
	}
	if defaultProjectID == "" {
		jsonError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "项目不存在")
		return
	}
	input.DefaultProjectID = defaultProjectID
	seenProjects := map[string]bool{}
	allProjectIDs := append([]string{}, input.ProjectIDs...)
	if input.DefaultProjectID != "" {
		allProjectIDs = append(allProjectIDs, input.DefaultProjectID)
	}
	for _, rule := range input.Rules {
		allProjectIDs = append(allProjectIDs, rule.ProjectID)
	}
	for _, projectID := range allProjectIDs {
		if projectID == "" || seenProjects[projectID] {
			continue
		}
		seenProjects[projectID] = true
		if project, err := h.projects.GetByID(ctx, projectID); err != nil || project == nil {
			jsonError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "项目不存在: "+projectID)
			return
		}
	}
	seenBindings := map[string]bool{}
	projectIDs := make([]string, 0, len(input.ProjectIDs))
	for _, projectID := range input.ProjectIDs {
		if projectID == "" || seenBindings[projectID] {
			continue
		}
		seenBindings[projectID] = true
		projectIDs = append(projectIDs, projectID)
	}
	seenRules := map[string]bool{}
	for i := range input.Rules {
		rule := &input.Rules[i]
		rule.MatcherType = strings.ToUpper(strings.TrimSpace(rule.MatcherType))
		rule.Pattern = strings.TrimPrefix(strings.TrimSpace(rule.Pattern), "job/")
		if ig.Protocol != "jenkins" || rule.MatcherType != "JENKINS_PATH_PREFIX" {
			jsonError(w, http.StatusBadRequest, "INVALID_PROJECT_RULE", "当前仅 Jenkins 支持路径前缀项目规则")
			return
		}
		if rule.Pattern == "" || len(rule.Pattern) > 500 || strings.Contains(rule.Pattern, "..") || strings.HasPrefix(rule.Pattern, "/") {
			jsonError(w, http.StatusBadRequest, "INVALID_PROJECT_RULE", "Jenkins 路径前缀不能为空，且不能包含相对路径")
			return
		}
		if rule.Priority == 0 {
			rule.Priority = 100
		}
		if rule.Priority < 1 || rule.Priority > 10000 || seenRules[rule.MatcherType+"\x00"+rule.Pattern] {
			jsonError(w, http.StatusBadRequest, "INVALID_PROJECT_RULE", "路由规则优先级或路径前缀无效/重复")
			return
		}
		seenRules[rule.MatcherType+"\x00"+rule.Pattern] = true
		if !seenBindings[rule.ProjectID] {
			jsonError(w, http.StatusBadRequest, "PROJECT_NOT_ALLOWED", "路由规则项目必须先加入允许服务项目")
			return
		}
	}
	if err := h.integrations.ReplaceProjectScope(ctx, integrationID, input.DefaultProjectID, input.Environment, projectIDs, input.Rules); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存 Integration 项目范围失败")
		return
	}
	updated, err := h.integrations.GetByID(ctx, integrationID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取更新后的项目范围失败")
		return
	}
	_ = h.writeAudit(ctx, "integration.project_scope.updated", nil, nil, "", "")
	jsonResp(w, http.StatusOK, updated)
}

// prepareHuaweiECSCreate validates the requested Warden project against the
// Integration allow-list and injects signed, server-owned task context. The
// Connector never trusts user-provided warden-* labels.
func (h *Handler) prepareHuaweiECSCreate(ctx context.Context, resource *domain.Resource, principal *domain.Principal, parameters map[string]any) (map[string]any, error) {
	if resource.IntegrationID == "" {
		return nil, &configError{"华为云账号资源缺少 Integration"}
	}
	projectID, _ := parameters["projectId"].(string)
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, &configError{"创建 ECS 必须明确选择 Warden 项目"}
	}
	region, _ := parameters["region"].(string)
	if region == "" || resource.ExternalID != "account/"+region {
		return nil, &configError{"Region 必须与选择的华为云账号资源一致"}
	}
	project, err := h.projects.GetByID(ctx, projectID)
	if err != nil || project == nil {
		return nil, &configError{"选择的 Warden 项目不存在"}
	}
	integration, err := h.integrations.GetByID(ctx, resource.IntegrationID)
	if err != nil || integration == nil {
		return nil, &configError{"华为云 Integration 不存在"}
	}
	allowed := integration.ProjectID == projectID
	for _, candidate := range integration.ProjectIDs {
		allowed = allowed || candidate == projectID
	}
	if !allowed {
		return nil, &configError{"该项目不在华为云账号的允许服务项目中"}
	}
	prepared := make(map[string]any, len(parameters)+1)
	for key, value := range parameters {
		if key != "_wardenContext" {
			prepared[key] = value
		}
	}
	prepared["_wardenContext"] = map[string]any{
		"projectId": project.ID, "projectName": project.Name, "environment": resource.Environment,
		"createdBy": principal.ID, "integrationId": integration.ID,
	}
	return prepared, nil
}

// prepareHuaweiECSMetadataUpdate derives governance labels from the resource's
// current Warden ownership. Callers may supply business tags, but never warden-* keys.
func (h *Handler) prepareHuaweiECSMetadataUpdate(ctx context.Context, resource *domain.Resource, parameters map[string]any) (map[string]any, error) {
	if resource.IntegrationID == "" {
		return nil, &configError{"华为云 ECS 资源缺少 Integration"}
	}
	projectID, err := h.resources.EffectiveProjectID(ctx, resource.ID)
	if err != nil || projectID == "" {
		return nil, &configError{"ECS 资源缺少有效的 Warden 项目归属"}
	}
	project, err := h.projects.GetByID(ctx, projectID)
	if err != nil || project == nil {
		return nil, &configError{"ECS 资源所属 Warden 项目不存在"}
	}
	integration, err := h.integrations.GetByID(ctx, resource.IntegrationID)
	if err != nil || integration == nil {
		return nil, &configError{"华为云 Integration 不存在"}
	}
	allowed := integration.ProjectID == projectID
	for _, candidate := range integration.ProjectIDs {
		allowed = allowed || candidate == projectID
	}
	if !allowed {
		return nil, &configError{"ECS 所属项目不在华为云账号的允许服务项目中"}
	}
	prepared := make(map[string]any, len(parameters)+1)
	for key, value := range parameters {
		if key != "_wardenContext" {
			prepared[key] = value
		}
	}
	prepared["_wardenContext"] = map[string]any{
		"projectId": project.ID, "projectName": project.Name, "environment": resource.Environment,
		"integrationId": integration.ID,
	}
	return prepared, nil
}

// --- Validation helpers (migrated from jenkins_adapter.go) ---

func decodeIntegration(r *http.Request, input *integrationInput) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(input)
}

func validateIntegration(input integrationInput) error {
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 255 {
		return &configError{"Integration 名称不能为空且不能超过 255 个字符"}
	}
	// A "warden:" prefix references a Warden-managed credential (resolved at runtime
	// over the connector channel); otherwise the ref is a K8s Secret name (or
	// Linux credential file) using the same DNS-1123 shape.
	if strings.HasPrefix(input.CredentialRef, "warden:") {
		if !domain.ValidateCredentialName(strings.TrimPrefix(input.CredentialRef, "warden:")) {
			return &configError{"Credential Reference 的 warden: 名称无效"}
		}
	} else if !credentialReferencePattern.MatchString(input.CredentialRef) {
		return &configError{"Credential Reference 必须是小写字母、数字、点或连字符组成的名称"}
	}
	if !validEnvironment(strings.TrimSpace(input.Environment), true) {
		return &configError{"环境必须是 development、testing、production 或留空继承执行通道"}
	}
	if len(input.CredentialProfiles) > 0 {
		protocol := strings.ToLower(strings.TrimSpace(input.Protocol))
		if protocol != "mysql" && protocol != "postgresql" {
			return &configError{"只有数据库集成支持凭证 Profile"}
		}
	}
	switch strings.ToLower(strings.TrimSpace(input.Protocol)) {
	case "jenkins":
		return validateJenkinsIntegration(input)
	case "grafana":
		return validateGrafanaIntegration(input)
	case "postgresql", "mysql":
		return validateDatabaseIntegration(input)
	case "huaweicloud":
		return validateHuaweiCloudIntegration(input)
	case "aliyun_sls":
		return validateAliyunSLSIntegration(input)
	case "h3c_comware_ssh":
		return validateH3CIntegration(input)
	default:
		return &configError{"不支持的 Integration 协议: " + input.Protocol}
	}
}

func validateH3CIntegration(input integrationInput) error {
	endpoint, err := url.Parse(strings.TrimSpace(input.Endpoint))
	if err != nil || endpoint.Scheme != "ssh" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return &configError{"H3C Endpoint 必须是 ssh://<固定设备IP>:<端口>，不能包含凭据、Query 或 Fragment"}
	}
	if ip := net.ParseIP(endpoint.Hostname()); ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
		return &configError{"H3C Endpoint 必须是固定单播 IP，不能使用任意主机名"}
	}
	port := endpoint.Port()
	if port == "" {
		return &configError{"H3C Endpoint 必须显式包含 SSH 端口"}
	}
	if strings.TrimSpace(input.Username) == "" || !regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,80}$`).MatchString(strings.TrimSpace(input.Username)) {
		return &configError{"H3C SSH 用户名格式无效"}
	}
	role, _ := input.Scope["deviceRole"].(string)
	if role != "ac" && role != "core" && role != "aggregation" && role != "poe" && role != "router" && role != "switch" {
		return &configError{"H3C deviceRole 必须是 ac、core、aggregation、poe、router 或 switch"}
	}
	fingerprint, _ := input.Scope["hostKeySHA256"].(string)
	if !h3cHostKeyFingerprintPattern.MatchString(strings.TrimSpace(fingerprint)) {
		return &configError{"H3C Integration 必须提供 SSH Host Key SHA256 指纹"}
	}
	if name, ok := input.Scope["deviceName"].(string); ok && (strings.TrimSpace(name) == "" || len(name) > 255) {
		return &configError{"H3C deviceName 不能为空且不能超过 255 个字符"}
	}
	if raw, ok := input.Scope["legacyRsa"]; ok {
		if _, ok := raw.(bool); !ok {
			return &configError{"H3C legacyRsa 必须是布尔值"}
		}
	}
	if raw, ok := input.Scope["allowInterfaceChanges"]; ok {
		if _, ok := raw.(bool); !ok {
			return &configError{"H3C allowInterfaceChanges 必须是布尔值"}
		}
	}
	if raw, ok := input.Scope["protectedInterfaces"]; ok {
		items, ok := raw.([]any)
		if !ok || len(items) > 128 {
			return &configError{"H3C protectedInterfaces 必须是最多 128 个接口名的数组"}
		}
		for _, item := range items {
			name, ok := item.(string)
			if !ok || !h3cInterfaceNamePattern.MatchString(strings.TrimSpace(name)) {
				return &configError{"H3C protectedInterfaces 包含无效接口名"}
			}
		}
	}
	return nil
}

func validateAliyunSLSIntegration(input integrationInput) error {
	value := strings.TrimSpace(input.Endpoint)
	validEndpoint := aliyunSLSRegionPattern.MatchString(value)
	if !validEndpoint {
		endpoint, err := url.Parse(value)
		if err == nil && endpoint != nil {
			matches := aliyunSLSProjectEndpointPattern.FindStringSubmatch(endpoint.Hostname())
			validEndpoint = endpoint.Scheme == "https" && len(matches) == 3 && aliyunSLSRegionPattern.MatchString(matches[2]) && endpoint.User == nil && endpoint.RawQuery == "" && endpoint.Fragment == "" && (endpoint.Path == "" || endpoint.Path == "/")
		}
	}
	if !validEndpoint {
		return &configError{"阿里云 SLS 请输入种子 Region（例如 cn-hangzhou）；现有项目级 HTTPS Endpoint 仍兼容"}
	}
	if len(input.Username) > 255 {
		return &configError{"阿里云 RAM 用户名不能超过 255 个字符"}
	}
	allowedProjects := stringSliceFromScope(input.Scope, "allowedProjects")
	if len(allowedProjects) == 0 && !boolFromScope(input.Scope, "allowAll") {
		return &configError{"阿里云 SLS 必须允许全部可访问 Project，或至少选择一个 Project"}
	}
	if len(allowedProjects) > 200 {
		return &configError{"阿里云 SLS Project 允许列表最多 200 项"}
	}
	for _, project := range allowedProjects {
		if !aliyunSLSProjectPattern.MatchString(strings.TrimSpace(project)) {
			return &configError{"阿里云 SLS Project 名称格式无效: " + project}
		}
	}
	if raw, ok := input.Scope["regionPolicy"]; ok {
		policy, ok := raw.(map[string]any)
		if !ok {
			return &configError{"阿里云 SLS Region 策略格式无效"}
		}
		mode, _ := policy["mode"].(string)
		if mode != "all" && mode != "allowlist" && mode != "single" {
			return &configError{"阿里云 SLS Region 模式仅支持 all、allowlist 或 single"}
		}
		regions := stringSliceFromScope(policy, "regions")
		denied := stringSliceFromScope(policy, "deniedRegions")
		if mode == "allowlist" && len(regions) == 0 {
			return &configError{"指定 Region 模式至少选择一个 Region"}
		}
		if len(regions) > 100 || len(denied) > 100 {
			return &configError{"Region 允许/排除列表最多各 100 项"}
		}
		for _, region := range append(regions, denied...) {
			if !aliyunSLSRegionPattern.MatchString(region) {
				return &configError{"Region 标识格式无效: " + region}
			}
		}
	}
	return nil
}

func validateJenkinsIntegration(input integrationInput) error {
	endpoint, err := url.Parse(strings.TrimSpace(input.Endpoint))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return &configError{"Jenkins Endpoint 必须是无凭据、Query 和 Fragment 的 http(s) URL"}
	}
	if strings.TrimSpace(input.Username) == "" || len(input.Username) > 255 {
		return &configError{"Jenkins 用户名不能为空且不能超过 255 个字符"}
	}
	// AllowedJobs is optional: an empty scope means "allow all jobs" and is the
	// default for newly created integrations. The admin narrows the scope from
	// the connector detail page once jobs have been discovered.
	allowedJobs := stringSliceFromScope(input.Scope, "allowedJobs")
	if len(allowedJobs) == 0 && !boolFromScope(input.Scope, "allowAll") {
		return &configError{"Jenkins 必须选择至少一个 Job，或显式启用 allowAll"}
	}
	if len(allowedJobs) > 200 {
		return &configError{"最多配置 200 个 Jenkins Job Scope"}
	}
	for _, job := range allowedJobs {
		job = strings.Trim(strings.TrimSpace(job), "/")
		if job == "" || job == "*" || strings.Contains(job, "..") {
			return &configError{"Jenkins Job Scope 不允许为空、全局通配或相对路径"}
		}
	}
	return nil
}

func validateGrafanaIntegration(input integrationInput) error {
	endpoint, err := url.Parse(strings.TrimSpace(input.Endpoint))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return &configError{"Grafana Endpoint 必须是无凭据、Query 和 Fragment 的 http(s) URL"}
	}
	// Username is optional for Grafana: auth is via Service Account Token
	// (Bearer header), not basic auth. An empty username is the default.
	if len(input.Username) > 255 {
		return &configError{"Grafana 用户名不能超过 255 个字符"}
	}
	// AllowedDashboards is optional: an empty scope means "allow all
	// dashboards" and is the default for newly created integrations.
	allowedDashboards := stringSliceFromScope(input.Scope, "allowedDashboards")
	if len(allowedDashboards) == 0 && !boolFromScope(input.Scope, "allowAll") {
		return &configError{"Grafana 必须选择至少一个 Dashboard，或显式启用 allowAll"}
	}
	if len(allowedDashboards) > 200 {
		return &configError{"最多配置 200 个 Grafana Dashboard Scope"}
	}
	for _, uid := range allowedDashboards {
		uid = strings.TrimSpace(uid)
		if uid == "" || !grafanaDashboardUIDPattern.MatchString(uid) {
			return &configError{"Grafana Dashboard Scope UID 必须匹配 ^[a-zA-Z0-9_-]{1,40}$"}
		}
	}
	return nil
}

// validateHuaweiCloudIntegration checks the huaweicloud protocol input. The
// endpoint is a region identifier (e.g. cn-north-4) rather than a URL: all
// service endpoints derive from it. Username is the IAM user name (display
// only; signing uses the AK/SK stored behind credentialRef).
func validateHuaweiCloudIntegration(input integrationInput) error {
	if !huaweiCloudRegionPattern.MatchString(strings.TrimSpace(input.Endpoint)) {
		return &configError{"华为云 Endpoint 必须是区域标识，例如 cn-north-4"}
	}
	if len(input.Username) > 255 {
		return &configError{"华为云 IAM 用户名不能超过 255 个字符"}
	}
	allowedServices := stringSliceFromScope(input.Scope, "allowedServices")
	if len(allowedServices) == 0 && !boolFromScope(input.Scope, "allowAll") {
		return &configError{"华为云必须选择至少一个服务（ecs/rds/elb/obs/lts/swr），或显式启用 allowAll"}
	}
	for _, service := range allowedServices {
		if !huaweiCloudServices[strings.ToLower(strings.TrimSpace(service))] {
			return &configError{"华为云服务范围仅支持 ecs、rds、elb、obs、lts、swr"}
		}
	}
	if raw, ok := input.Scope["regionPolicy"]; ok {
		policy, ok := raw.(map[string]any)
		if !ok {
			return &configError{"华为云 Region 策略格式无效"}
		}
		mode, _ := policy["mode"].(string)
		if mode != "all" && mode != "allowlist" && mode != "single" {
			return &configError{"华为云 Region 模式仅支持 all、allowlist 或 single"}
		}
		regions := stringSliceFromScope(policy, "regions")
		denied := stringSliceFromScope(policy, "deniedRegions")
		if mode == "allowlist" && len(regions) == 0 {
			return &configError{"指定 Region 模式至少选择一个 Region"}
		}
		if len(regions) > 100 || len(denied) > 100 {
			return &configError{"Region 允许/排除列表最多各 100 项"}
		}
		for _, region := range append(regions, denied...) {
			if !huaweiCloudRegionPattern.MatchString(region) {
				return &configError{"Region 标识格式无效: " + region}
			}
		}
	}
	return nil
}

func validateDatabaseIntegration(input integrationInput) error {
	protocol := strings.ToLower(strings.TrimSpace(input.Protocol))
	endpoint, err := url.Parse(strings.TrimSpace(input.Endpoint))
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return &configError{"数据库 Endpoint 必须是无凭据和 Fragment 的绝对 URL"}
	}
	switch protocol {
	case "postgresql":
		if endpoint.Scheme != "postgresql" && endpoint.Scheme != "postgres" {
			return &configError{"PostgreSQL Endpoint 必须使用 postgresql://"}
		}
		for key := range endpoint.Query() {
			if key != "sslmode" && key != "connect_timeout" {
				return &configError{"PostgreSQL Endpoint 仅允许 sslmode 和 connect_timeout 参数"}
			}
		}
	case "mysql":
		if endpoint.Scheme != "mysql" {
			return &configError{"MySQL Endpoint 必须使用 mysql://"}
		}
		for key := range endpoint.Query() {
			if key != "tls" {
				return &configError{"MySQL Endpoint 仅允许 tls 参数"}
			}
		}
		if tls := endpoint.Query().Get("tls"); tls != "" && tls != "true" && tls != "false" {
			return &configError{"MySQL tls 参数只能是 true 或 false"}
		}
	}
	if strings.TrimSpace(input.Username) == "" || len(input.Username) > 255 {
		return &configError{"数据库用户名不能为空且不能超过 255 个字符"}
	}
	allowedSchemas := stringSliceFromScope(input.Scope, "allowedSchemas")
	if len(allowedSchemas) == 0 && !boolFromScope(input.Scope, "allowAll") {
		return &configError{"数据库必须选择至少一个 Schema，或显式启用 allowAll"}
	}
	if len(allowedSchemas) > 200 {
		return &configError{"最多配置 200 个数据库 Schema Scope"}
	}
	for _, schema := range allowedSchemas {
		if strings.TrimSpace(schema) == "" || len(schema) > 255 {
			return &configError{"数据库 Schema Scope 不能为空且不能超过 255 个字符"}
		}
	}
	for _, profile := range input.CredentialProfiles {
		if !databaseCredentialProfileNames[strings.TrimSpace(profile.Name)] {
			return &configError{"数据库凭证 Profile 名称只能是 query 或 ddl"}
		}
		if !credentialReferencePattern.MatchString(strings.TrimSpace(profile.CredentialRef)) {
			return &configError{"凭证 Profile 的 Credential Reference 必须是合法名称"}
		}
		if len(profile.Username) > 255 {
			return &configError{"凭证 Profile 用户名不能超过 255 个字符"}
		}
	}
	return nil
}

func boolFromScope(scope map[string]any, key string) bool {
	value, _ := scope[key].(bool)
	return value
}

func stringSliceFromScope(scope map[string]any, key string) []string {
	if scope == nil {
		return nil
	}
	raw, ok := scope[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func buildIntegration(connectorID string, input integrationInput) *domain.Integration {
	protocol := strings.ToLower(strings.TrimSpace(input.Protocol))
	scope := map[string]any{}
	if boolFromScope(input.Scope, "allowAll") {
		scope["allowAll"] = true
	}
	switch protocol {
	case "jenkins":
		allowedJobs := stringSliceFromScope(input.Scope, "allowedJobs")
		if len(allowedJobs) > 0 {
			scope["allowedJobs"] = allowedJobs
		}
	case "grafana":
		allowedDashboards := stringSliceFromScope(input.Scope, "allowedDashboards")
		if len(allowedDashboards) > 0 {
			scope["allowedDashboards"] = allowedDashboards
		}
	case "postgresql", "mysql":
		allowedSchemas := stringSliceFromScope(input.Scope, "allowedSchemas")
		if len(allowedSchemas) > 0 {
			scope["allowedSchemas"] = allowedSchemas
		}
	case "huaweicloud":
		allowedServices := stringSliceFromScope(input.Scope, "allowedServices")
		if len(allowedServices) > 0 {
			normalized := make([]string, 0, len(allowedServices))
			for _, service := range allowedServices {
				normalized = append(normalized, strings.ToLower(strings.TrimSpace(service)))
			}
			scope["allowedServices"] = normalized
		}
		if policy, ok := input.Scope["regionPolicy"].(map[string]any); ok {
			scope["regionPolicy"] = map[string]any{
				"mode":          policy["mode"],
				"regions":       stringSliceFromScope(policy, "regions"),
				"deniedRegions": stringSliceFromScope(policy, "deniedRegions"),
			}
		}
	case "aliyun_sls":
		allowedProjects := stringSliceFromScope(input.Scope, "allowedProjects")
		if len(allowedProjects) > 0 {
			scope["allowedProjects"] = allowedProjects
		}
		if policy, ok := input.Scope["regionPolicy"].(map[string]any); ok {
			scope["regionPolicy"] = map[string]any{
				"mode": policy["mode"], "regions": stringSliceFromScope(policy, "regions"), "deniedRegions": stringSliceFromScope(policy, "deniedRegions"),
			}
		}
	case "h3c_comware_ssh":
		for _, key := range []string{"deviceRole", "deviceName", "hostKeySHA256"} {
			if value, ok := input.Scope[key].(string); ok && strings.TrimSpace(value) != "" {
				scope[key] = strings.TrimSpace(value)
			}
		}
		if value, ok := input.Scope["legacyRsa"].(bool); ok {
			scope["legacyRsa"] = value
		}
		if value, ok := input.Scope["allowInterfaceChanges"].(bool); ok {
			scope["allowInterfaceChanges"] = value
		}
		if protected := stringSliceFromScope(input.Scope, "protectedInterfaces"); len(protected) > 0 {
			normalized := make([]string, 0, len(protected))
			for _, interfaceName := range protected {
				normalized = append(normalized, strings.TrimSpace(interfaceName))
			}
			scope["protectedInterfaces"] = normalized
		}
	}
	profiles := make([]domain.IntegrationCredentialProfile, 0, len(input.CredentialProfiles))
	for _, profile := range input.CredentialProfiles {
		profiles = append(profiles, domain.IntegrationCredentialProfile{
			Name:          strings.TrimSpace(profile.Name),
			Username:      strings.TrimSpace(profile.Username),
			CredentialRef: strings.TrimSpace(profile.CredentialRef),
		})
	}
	return &domain.Integration{
		ConnectorID:        connectorID,
		Protocol:           protocol,
		Name:               strings.TrimSpace(input.Name),
		Endpoint:           strings.TrimRight(strings.TrimSpace(input.Endpoint), "/"),
		Username:           strings.TrimSpace(input.Username),
		CredentialRef:      strings.TrimSpace(input.CredentialRef),
		ProjectID:          strings.TrimSpace(input.ProjectID),
		Environment:        strings.TrimSpace(input.Environment),
		CredentialProfiles: profiles,
		Scope:              scope,
	}
}

func validEnvironment(value string, allowEmpty bool) bool {
	return (allowEmpty && value == "") || value == "development" || value == "testing" || value == "production"
}

// resolveIntegrationProjectID deliberately does not consult the Connector.
// Middleware ownership is a governance decision independent from where its
// requests happen to execute. An omitted project means the tenant default.
func (h *Handler) resolveIntegrationProjectID(ctx context.Context, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		project, err := h.projects.GetByID(ctx, requested)
		if err != nil || project == nil {
			return "", err
		}
		return project.ID, nil
	}
	project, err := h.projects.GetDefault(ctx)
	if err != nil || project == nil {
		return "", err
	}
	return project.ID, nil
}
