package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"
)

const (
	ProviderRequestVersion    = 1
	DefaultProviderMaxItems   = 500
	DefaultProviderMaxBytes   = 4 << 20
	DefaultProviderTimeoutSec = 30
	DefaultProviderMaxBody    = 1 << 20 // default write payload limit
	MaxProviderBodyBytes      = 4 << 20 // absolute write payload ceiling
)

// ProviderRequest is the agent-facing contract for a governed read request.
// It deliberately has no URL, host, credential, body, or arbitrary headers.
// The control plane resolves all of those from the bound resource and
// Integration after ValidateProviderRequest succeeds.
type ProviderRequest struct {
	Version     int                 `json:"version"`
	ResourceID  string              `json:"resourceId"`
	Capability  string              `json:"capability"`
	Method      string              `json:"method"`
	Path        string              `json:"path"`
	Query       map[string][]string `json:"query,omitempty"`
	Headers     map[string]string   `json:"headers,omitempty"`
	Body        any                 `json:"body,omitempty"` // structured write payload (provider.write)
	Page        *ProviderPage       `json:"page,omitempty"`
	TimeRange   *ProviderTimeRange  `json:"timeRange,omitempty"`
	Correlation map[string]string   `json:"correlation,omitempty"`
}

type ProviderPage struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type ProviderTimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// ResourceCapability is an administrator-authored catalog entry. Path
// patterns use literal segments, {name} for one segment, or a terminal **.
// The first implementation only admits GET/HEAD, even if another method is
// accidentally placed in Methods.
type ResourceCapability struct {
	ID                string                       `json:"id,omitempty"`
	Name              string                       `json:"name"`
	Version           int                          `json:"version"`
	ResourceKinds     []string                     `json:"resourceKinds"`
	Protocols         []string                     `json:"protocols"`
	Methods           []string                     `json:"methods"`
	PathPatterns      []string                     `json:"pathPatterns"`
	QueryParameters   map[string]ProviderParamRule `json:"queryParameters,omitempty"`
	AllowedHeaders    []string                     `json:"allowedHeaders,omitempty"`
	MaxItems          int                          `json:"maxItems,omitempty"`
	MaxResponseBytes  int                          `json:"maxResponseBytes,omitempty"`
	TimeoutSeconds    int                          `json:"timeoutSeconds,omitempty"`
	MaxTimeRange      time.Duration                `json:"-"`
	MaxTimeRangeSecs  int64                        `json:"maxTimeRangeSeconds,omitempty"`
	Description       string                       `json:"description,omitempty"`
	Write             bool                         `json:"write,omitempty"`             // governed write capability (provider.write)
	MaxBodyBytes      int                          `json:"maxBodyBytes,omitempty"`      // write payload ceiling
	RequestBodySchema map[string]any               `json:"requestBodySchema,omitempty"` // optional JSON Schema for the write payload
}

func (c ResourceCapability) Key() string { return RequiredCapability(c.Name, c.Version) }

type ProviderParamRule struct {
	Required  bool   `json:"required,omitempty"`
	MaxValues int    `json:"maxValues,omitempty"`
	MaxLength int    `json:"maxLength,omitempty"`
	Pattern   string `json:"pattern,omitempty"`
}

// ProviderRequestBinding is resolved server-side. Supplying these fields is
// not part of ProviderRequest, which prevents an agent from selecting another
// endpoint or credential while retaining a valid resource id.
type ProviderRequestBinding struct {
	Resource    Resource           `json:"resource"`
	Integration Integration        `json:"integration"`
	Capability  ResourceCapability `json:"capability"`
}

type ProviderResponse struct {
	Status     ProviderResponseStatus `json:"status"`
	Resource   ProviderResourceRef    `json:"resource"`
	Capability string                 `json:"capability"`
	Data       any                    `json:"data,omitempty"`
	Page       *ProviderResponsePage  `json:"page,omitempty"`
	Truncated  bool                   `json:"truncated,omitempty"`
	Warnings   []string               `json:"warnings,omitempty"`
}

type ProviderResponseStatus string

const (
	ProviderResponseSucceeded ProviderResponseStatus = "SUCCEEDED"
	ProviderResponseNoData    ProviderResponseStatus = "NO_DATA"
	ProviderResponsePartial   ProviderResponseStatus = "PARTIAL"
	ProviderResponseFailed    ProviderResponseStatus = "FAILED"
	ProviderResponseUnknown   ProviderResponseStatus = "UNKNOWN"
)

type ProviderResourceRef struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	ProjectID   string `json:"projectId,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type ProviderResponsePage struct {
	NextCursor string `json:"nextCursor,omitempty"`
	HasMore    bool   `json:"hasMore"`
}

type ProviderRequestError struct {
	Code    string
	Message string
}

func (e *ProviderRequestError) Error() string { return e.Code + ": " + e.Message }

const (
	ProviderErrInvalidRequest      = "PROVIDER_INVALID_REQUEST"
	ProviderErrResourceNotFound    = "PROVIDER_RESOURCE_NOT_FOUND"
	ProviderErrResourceUnavailable = "PROVIDER_RESOURCE_UNAVAILABLE"
	ProviderErrIntegrationMismatch = "PROVIDER_INTEGRATION_MISMATCH"
	ProviderErrIntegrationInactive = "PROVIDER_INTEGRATION_INACTIVE"
	ProviderErrCapabilityNotFound  = "PROVIDER_CAPABILITY_NOT_FOUND"
	ProviderErrCapabilityMismatch  = "PROVIDER_CAPABILITY_MISMATCH"
	ProviderErrMethodDenied        = "PROVIDER_METHOD_DENIED"
	ProviderErrPathDenied          = "PROVIDER_PATH_DENIED"
	ProviderErrQueryDenied         = "PROVIDER_QUERY_DENIED"
	ProviderErrHeaderDenied        = "PROVIDER_HEADER_DENIED"
	ProviderErrScopeDenied         = "PROVIDER_SCOPE_DENIED"
	ProviderErrLimitExceeded       = "PROVIDER_LIMIT_EXCEEDED"
	ProviderErrEndpointUnsafe      = "PROVIDER_ENDPOINT_UNSAFE"
	ProviderErrRedirectDenied      = "PROVIDER_REDIRECT_DENIED"
	ProviderErrResponseTooLarge    = "PROVIDER_RESPONSE_TOO_LARGE"
	ProviderErrTimeout             = "PROVIDER_TIMEOUT"
	ProviderErrUpstreamUnknown     = "PROVIDER_UPSTREAM_UNKNOWN"
)

var providerCorrelationKey = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// ValidateProviderRequest validates the complete control-plane boundary. It
// performs no I/O and returns normalized limits for the OPE-7519 executor.
func ValidateProviderRequest(request ProviderRequest, binding ProviderRequestBinding) error {
	if request.Version != ProviderRequestVersion {
		return providerError(ProviderErrInvalidRequest, "unsupported provider request version")
	}
	if strings.TrimSpace(request.ResourceID) == "" || request.ResourceID != binding.Resource.ID {
		return providerError(ProviderErrResourceNotFound, "request resource is not the resolved resource")
	}
	if binding.Resource.Status != ResourceActive && binding.Resource.Status != ResourceDiscovered {
		return providerError(ProviderErrResourceUnavailable, "resource is not available")
	}
	nativeKubernetes := binding.Integration.Protocol == "kubernetes" && binding.Resource.IntegrationID == "" && binding.Integration.ID == ""
	if !nativeKubernetes && (binding.Resource.IntegrationID == "" || binding.Resource.IntegrationID != binding.Integration.ID) {
		return providerError(ProviderErrIntegrationMismatch, "resource is not bound to the resolved Integration")
	}
	if binding.Resource.ConnectorID != binding.Integration.ConnectorID {
		return providerError(ProviderErrIntegrationMismatch, "resource and Integration use different connectors")
	}
	if binding.Resource.TenantID != binding.Integration.TenantID {
		return providerError(ProviderErrScopeDenied, "resource and Integration use different tenants")
	}
	if binding.Integration.Status != IntegrationActive {
		return providerError(ProviderErrIntegrationInactive, "Integration is not active")
	}
	if err := ValidateProviderIntegrationEndpoint(binding.Integration.Protocol, binding.Integration.Endpoint); err != nil {
		return err
	}
	if request.Capability == "" || request.Capability != binding.Capability.Key() {
		return providerError(ProviderErrCapabilityNotFound, "capability is not the resolved catalog entry")
	}
	if !contains(binding.Capability.ResourceKinds, binding.Resource.ResourceType) ||
		!contains(binding.Capability.Protocols, binding.Integration.Protocol) {
		return providerError(ProviderErrCapabilityMismatch, "capability does not apply to this resource or protocol")
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method != http.MethodGet && method != http.MethodHead {
		return providerError(ProviderErrMethodDenied, "provider.request only permits GET and HEAD")
	}
	if !containsFold(binding.Capability.Methods, method) {
		return providerError(ProviderErrMethodDenied, "method is not declared by the capability")
	}
	if request.Body != nil {
		return providerError(ProviderErrInvalidRequest, "read requests may not carry a body")
	}
	normalizedPath, err := validateProviderPath(request.Path)
	if err != nil {
		return err
	}
	matched := false
	for _, pattern := range binding.Capability.PathPatterns {
		if matchProviderPath(pattern, normalizedPath) {
			if err := validateProviderResourceIdentity(pattern, normalizedPath, binding.Resource); err != nil {
				return err
			}
			matched = true
			break
		}
	}
	if !matched {
		return providerError(ProviderErrPathDenied, "path is outside the capability allowlist")
	}
	if err := validateProviderQuery(request.Query, binding.Capability.QueryParameters); err != nil {
		return err
	}
	if err := validateProviderHeaders(request.Headers, binding.Capability.AllowedHeaders); err != nil {
		return err
	}
	maxItems := binding.Capability.MaxItems
	if maxItems <= 0 || maxItems > DefaultProviderMaxItems {
		maxItems = DefaultProviderMaxItems
	}
	if request.Page != nil && (request.Page.Limit < 0 || request.Page.Limit > maxItems) {
		return providerError(ProviderErrLimitExceeded, "page limit exceeds the capability limit")
	}
	maxRange := binding.Capability.MaxTimeRange
	if maxRange <= 0 && binding.Capability.MaxTimeRangeSecs > 0 {
		maxRange = time.Duration(binding.Capability.MaxTimeRangeSecs) * time.Second
	}
	if request.TimeRange != nil {
		if request.TimeRange.Start.IsZero() || request.TimeRange.End.IsZero() || !request.TimeRange.Start.Before(request.TimeRange.End) {
			return providerError(ProviderErrInvalidRequest, "time range must have start before end")
		}
		if maxRange > 0 && request.TimeRange.End.Sub(request.TimeRange.Start) > maxRange {
			return providerError(ProviderErrLimitExceeded, "time range exceeds the capability limit")
		}
	}
	for key, value := range request.Correlation {
		if !providerCorrelationKey.MatchString(key) || len(value) > 256 || sensitiveResultKey(key) {
			return providerError(ProviderErrInvalidRequest, "invalid correlation field")
		}
	}
	return nil
}

var providerCloudRegion = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-[0-9]{1,2}$`)

// ValidateProviderIntegrationEndpoint accounts for adapters whose registered
// endpoint is a fixed region rather than a caller-selectable URL. The adapter
// still owns the service hostname; agents cannot provide it.
func ValidateProviderIntegrationEndpoint(protocol, endpoint string) error {
	if protocol == "huaweicloud" {
		if !providerCloudRegion.MatchString(strings.TrimSpace(endpoint)) {
			return providerError(ProviderErrEndpointUnsafe, "Huawei Cloud Integration must contain a valid fixed region")
		}
		return nil
	}
	return ValidateProviderEndpoint(endpoint)
}

func validateProviderResourceIdentity(pattern, path string, resource Resource) error {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	values := map[string]string{}
	for index, part := range patternParts {
		if index >= len(pathParts) {
			break
		}
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			values[part[1:len(part)-1]] = pathParts[index]
		}
	}
	externalTail := resource.ExternalID
	if index := strings.LastIndex(externalTail, "/"); index >= 0 {
		externalTail = externalTail[index+1:]
	}
	switch resource.ResourceType {
	case "jenkins_job":
		if values["job"] != resource.Name && values["job"] != externalTail {
			return providerError(ProviderErrScopeDenied, "path targets a different Jenkins job")
		}
	case "grafana_dashboard":
		if values["uid"] != externalTail {
			return providerError(ProviderErrScopeDenied, "path targets a different Grafana dashboard")
		}
	case "huawei_ecs_instance":
		if values["server_id"] != externalTail {
			return providerError(ProviderErrScopeDenied, "path targets a different ECS instance")
		}
	case "huawei_obs_bucket":
		if values["bucket"] != resource.Name && values["bucket"] != externalTail {
			return providerError(ProviderErrScopeDenied, "path targets a different OBS bucket")
		}
	case "deployment", "statefulset", "daemonset", "replicaset", "job", "cronjob", "service", "endpoints", "ingress", "configmap", "pod", "kubernetes_resource":
		if values["name"] != "" && values["name"] != resource.Name && values["name"] != externalTail {
			return providerError(ProviderErrScopeDenied, "path targets a different Kubernetes resource")
		}
		if values["namespace"] != "" && values["namespace"] != resource.Namespace {
			return providerError(ProviderErrScopeDenied, "path targets a different Kubernetes namespace")
		}
	}
	return nil
}

// ValidateProviderCapability rejects catalog entries that could accidentally
// widen the executor into a generic write or arbitrary URL proxy.
func ValidateProviderCapability(capability ResourceCapability) error {
	if capability.Version < 1 || !capabilityPattern.MatchString(capability.Key()) {
		return providerError(ProviderErrInvalidRequest, "invalid capability name or version")
	}
	if len(capability.ResourceKinds) == 0 || len(capability.Protocols) == 0 || len(capability.Methods) == 0 || len(capability.PathPatterns) == 0 {
		return providerError(ProviderErrInvalidRequest, "capability bindings, methods, and paths are required")
	}
	for _, method := range capability.Methods {
		if !strings.EqualFold(method, http.MethodGet) && !strings.EqualFold(method, http.MethodHead) {
			return providerError(ProviderErrMethodDenied, "read capabilities may only declare GET or HEAD")
		}
	}
	for _, pattern := range capability.PathPatterns {
		if err := validateProviderPathPattern(pattern); err != nil {
			return err
		}
	}
	for name, rule := range capability.QueryParameters {
		if !validProviderParameterName(name) || sensitiveResultKey(name) || rule.MaxValues < 0 || rule.MaxLength < 0 {
			return providerError(ProviderErrInvalidRequest, "invalid query parameter rule")
		}
		if rule.Pattern != "" {
			if _, err := regexp.Compile(rule.Pattern); err != nil {
				return providerError(ProviderErrInvalidRequest, "invalid query parameter pattern")
			}
		}
	}
	for _, name := range capability.AllowedHeaders {
		if !validProviderHeader(name) || forbiddenProviderHeader(name) {
			return providerError(ProviderErrHeaderDenied, "unsafe header in capability allowlist")
		}
	}
	if capability.MaxItems < 0 || capability.MaxItems > DefaultProviderMaxItems ||
		capability.MaxResponseBytes < 0 || capability.MaxResponseBytes > DefaultProviderMaxBytes ||
		capability.TimeoutSeconds < 0 || capability.TimeoutSeconds > DefaultProviderTimeoutSec ||
		capability.MaxTimeRangeSecs < 0 {
		return providerError(ProviderErrLimitExceeded, "capability limit exceeds the platform maximum")
	}
	return nil
}

// ValidateProviderWriteCapability rejects write catalog entries that could
// widen the executor into arbitrary mutation or arbitrary URL proxying. Write
// capabilities may only declare POST/PUT/PATCH (never DELETE — destructive
// changes stay on dedicated approved actions) and never target native
// Kubernetes (which has its own dry-run generic CRUD).
func ValidateProviderWriteCapability(capability ResourceCapability) error {
	if !capability.Write {
		return providerError(ProviderErrInvalidRequest, "write capability must set write=true")
	}
	if capability.Version < 1 || !capabilityPattern.MatchString(capability.Key()) {
		return providerError(ProviderErrInvalidRequest, "invalid capability name or version")
	}
	if len(capability.ResourceKinds) == 0 || len(capability.Protocols) == 0 || len(capability.Methods) == 0 || len(capability.PathPatterns) == 0 {
		return providerError(ProviderErrInvalidRequest, "capability bindings, methods, and paths are required")
	}
	if containsFold(capability.Protocols, "kubernetes") {
		return providerError(ProviderErrMethodDenied, "Kubernetes writes must use the dedicated dry-run generic CRUD")
	}
	for _, method := range capability.Methods {
		upper := strings.ToUpper(method)
		if upper != http.MethodPost && upper != http.MethodPut && upper != http.MethodPatch {
			return providerError(ProviderErrMethodDenied, "write capabilities may only declare POST, PUT, or PATCH (never DELETE)")
		}
	}
	for _, pattern := range capability.PathPatterns {
		if err := validateProviderPathPattern(pattern); err != nil {
			return err
		}
	}
	for name, rule := range capability.QueryParameters {
		if !validProviderParameterName(name) || sensitiveResultKey(name) || rule.MaxValues < 0 || rule.MaxLength < 0 {
			return providerError(ProviderErrInvalidRequest, "invalid query parameter rule")
		}
		if rule.Pattern != "" {
			if _, err := regexp.Compile(rule.Pattern); err != nil {
				return providerError(ProviderErrInvalidRequest, "invalid query parameter pattern")
			}
		}
	}
	for _, name := range capability.AllowedHeaders {
		if !validProviderHeader(name) || forbiddenProviderHeader(name) {
			return providerError(ProviderErrHeaderDenied, "unsafe header in capability allowlist")
		}
	}
	if capability.MaxItems < 0 || capability.MaxItems > DefaultProviderMaxItems ||
		capability.MaxResponseBytes < 0 || capability.MaxResponseBytes > DefaultProviderMaxBytes ||
		capability.TimeoutSeconds < 0 || capability.TimeoutSeconds > DefaultProviderTimeoutSec ||
		capability.MaxTimeRangeSecs < 0 ||
		capability.MaxBodyBytes < 0 || capability.MaxBodyBytes > MaxProviderBodyBytes {
		return providerError(ProviderErrLimitExceeded, "capability limit exceeds the platform maximum")
	}
	if len(capability.RequestBodySchema) > 0 {
		if _, err := compileProviderBodySchema(capability.RequestBodySchema); err != nil {
			return providerError(ProviderErrInvalidRequest, "invalid request body schema: "+err.Error())
		}
	}
	return nil
}

// ValidateProviderWrite validates the complete control-plane boundary for a
// governed structured write. It performs no I/O and rejects payloads that do
// not match the capability's declared body schema.
func ValidateProviderWrite(request ProviderRequest, binding ProviderRequestBinding) error {
	if request.Version != ProviderRequestVersion {
		return providerError(ProviderErrInvalidRequest, "unsupported provider write version")
	}
	if strings.TrimSpace(request.ResourceID) == "" || request.ResourceID != binding.Resource.ID {
		return providerError(ProviderErrResourceNotFound, "request resource is not the resolved resource")
	}
	if binding.Resource.Status != ResourceActive && binding.Resource.Status != ResourceDiscovered {
		return providerError(ProviderErrResourceUnavailable, "resource is not available")
	}
	nativeKubernetes := binding.Integration.Protocol == "kubernetes" && binding.Resource.IntegrationID == "" && binding.Integration.ID == ""
	if !nativeKubernetes && (binding.Resource.IntegrationID == "" || binding.Resource.IntegrationID != binding.Integration.ID) {
		return providerError(ProviderErrIntegrationMismatch, "resource is not bound to the resolved Integration")
	}
	if binding.Resource.ConnectorID != binding.Integration.ConnectorID {
		return providerError(ProviderErrIntegrationMismatch, "resource and Integration use different connectors")
	}
	if binding.Resource.TenantID != binding.Integration.TenantID {
		return providerError(ProviderErrScopeDenied, "resource and Integration use different tenants")
	}
	if binding.Integration.Status != IntegrationActive {
		return providerError(ProviderErrIntegrationInactive, "Integration is not active")
	}
	if err := ValidateProviderIntegrationEndpoint(binding.Integration.Protocol, binding.Integration.Endpoint); err != nil {
		return err
	}
	if request.Capability == "" || request.Capability != binding.Capability.Key() {
		return providerError(ProviderErrCapabilityNotFound, "capability is not the resolved catalog entry")
	}
	if !binding.Capability.Write {
		return providerError(ProviderErrCapabilityMismatch, "capability is not a write capability")
	}
	if !contains(binding.Capability.ResourceKinds, binding.Resource.ResourceType) ||
		!contains(binding.Capability.Protocols, binding.Integration.Protocol) {
		return providerError(ProviderErrCapabilityMismatch, "capability does not apply to this resource or protocol")
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
		return providerError(ProviderErrMethodDenied, "provider.write only permits POST, PUT, and PATCH")
	}
	if !containsFold(binding.Capability.Methods, method) {
		return providerError(ProviderErrMethodDenied, "method is not declared by the capability")
	}
	normalizedPath, err := validateProviderPath(request.Path)
	if err != nil {
		return err
	}
	matched := false
	for _, pattern := range binding.Capability.PathPatterns {
		if matchProviderPath(pattern, normalizedPath) {
			if err := validateProviderResourceIdentity(pattern, normalizedPath, binding.Resource); err != nil {
				return err
			}
			matched = true
			break
		}
	}
	if !matched {
		return providerError(ProviderErrPathDenied, "path is outside the capability allowlist")
	}
	if err := validateProviderQuery(request.Query, binding.Capability.QueryParameters); err != nil {
		return err
	}
	if err := validateProviderHeaders(request.Headers, binding.Capability.AllowedHeaders); err != nil {
		return err
	}
	if request.Page != nil || request.TimeRange != nil {
		return providerError(ProviderErrInvalidRequest, "write requests may not carry page or timeRange")
	}
	if err := validateProviderBody(request.Body, binding.Capability.RequestBodySchema, providerWriteMaxBody(binding.Capability)); err != nil {
		return err
	}
	for key, value := range request.Correlation {
		if !providerCorrelationKey.MatchString(key) || len(value) > 256 || sensitiveResultKey(key) {
			return providerError(ProviderErrInvalidRequest, "invalid correlation field")
		}
	}
	return nil
}

func providerWriteMaxBody(capability ResourceCapability) int {
	if capability.MaxBodyBytes <= 0 || capability.MaxBodyBytes > MaxProviderBodyBytes {
		return DefaultProviderMaxBody
	}
	return capability.MaxBodyBytes
}

func validateProviderBody(body any, schema map[string]any, maxBodyBytes int) error {
	if body == nil {
		return providerError(ProviderErrInvalidRequest, "write request requires a structured body")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return providerError(ProviderErrInvalidRequest, "write request body is not valid JSON")
	}
	if len(payload) > maxBodyBytes {
		return providerError(ProviderErrLimitExceeded, "write request body exceeds the capability limit")
	}
	if len(schema) > 0 {
		compiled, err := compileProviderBodySchema(schema)
		if err != nil {
			return providerError(ProviderErrInvalidRequest, "invalid request body schema")
		}
		if err := compiled.Validate(body); err != nil {
			return providerError(ProviderErrInvalidRequest, "write request body does not match the capability schema: "+err.Error())
		}
	}
	return nil
}

func compileProviderBodySchema(schema map[string]any) (*jsonschema.Schema, error) {
	payload, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("provider-write-body.json", bytes.NewReader(payload)); err != nil {
		return nil, err
	}
	return compiler.Compile("provider-write-body.json")
}

// ValidateProviderEndpoint validates only administrator-registered targets.
// Private RFC1918 endpoints are intentional for internal infrastructure, but
// loopback, link-local, unspecified, multicast, userinfo, and metadata targets
// are never valid. The executor must also resolve DNS and apply the same checks
// to every resolved address before connecting.
func ValidateProviderEndpoint(raw string) error {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" ||
		(endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return providerError(ProviderErrEndpointUnsafe, "Integration endpoint must be an absolute HTTP(S) origin without credentials, query, or fragment")
	}
	host := strings.TrimSuffix(strings.ToLower(endpoint.Hostname()), ".")
	if host == "localhost" || host == "metadata" || host == "metadata.google.internal" || host == "169.254.169.254" || host == "100.100.100.200" {
		return providerError(ProviderErrEndpointUnsafe, "Integration endpoint targets a local or metadata service")
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := ValidateProviderIP(ip); err != nil {
			return err
		}
	}
	return nil
}

// ValidateProviderIP is shared by endpoint validation and the Connector's
// post-DNS-resolution dialer. Keeping the checks in one place prevents a DNS
// name from bypassing the literal-IP restrictions (including DNS rebinding).
func ValidateProviderIP(ip net.IP) error {
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.String() == "100.100.100.200" {
		return providerError(ProviderErrEndpointUnsafe, "Integration endpoint resolved to an unsafe address")
	}
	return nil
}

// ProviderRedirectPolicy returns a CheckRedirect callback which prevents an
// upstream from moving a credentialed request to another origin.
func ProviderRedirectPolicy(origin *url.URL) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, _ []*http.Request) error {
		if origin == nil || !sameProviderOrigin(origin, req.URL) {
			return providerError(ProviderErrRedirectDenied, "cross-origin redirect is not allowed")
		}
		return nil
	}
}

func sameProviderOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && providerPort(a) == providerPort(b)
}

func providerPort(value *url.URL) string {
	if value.Port() != "" {
		return value.Port()
	}
	if value.Scheme == "https" {
		return "443"
	}
	return "80"
}

func validateProviderPath(raw string) (string, error) {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, "\\") || strings.ContainsAny(raw, "?#\r\n\x00") {
		return "", providerError(ProviderErrPathDenied, "path must be an absolute-path reference without query or fragment")
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" {
		return "", providerError(ProviderErrPathDenied, "path is not a valid relative API path")
	}
	decoded, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil || strings.Contains(decoded, "\\") || strings.Contains(strings.ToLower(parsed.EscapedPath()), "%2f") || strings.Contains(strings.ToLower(parsed.EscapedPath()), "%5c") {
		return "", providerError(ProviderErrPathDenied, "encoded path separators are not allowed")
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." {
			return "", providerError(ProviderErrPathDenied, "path traversal is not allowed")
		}
	}
	return decoded, nil
}

func validateProviderPathPattern(pattern string) error {
	if pattern == "" || !strings.HasPrefix(pattern, "/") || strings.ContainsAny(pattern, "?#\\\r\n") {
		return providerError(ProviderErrPathDenied, "invalid capability path pattern")
	}
	parts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	for i, part := range parts {
		if part == "**" {
			if i != len(parts)-1 {
				return providerError(ProviderErrPathDenied, "** is only allowed as the final path segment")
			}
			continue
		}
		if strings.HasPrefix(part, "{") || strings.HasSuffix(part, "}") {
			if len(part) < 3 || part[0] != '{' || part[len(part)-1] != '}' || !providerCorrelationKey.MatchString(part[1:len(part)-1]) {
				return providerError(ProviderErrPathDenied, "invalid path variable")
			}
		}
		if part == "." || part == ".." || part == "*" {
			return providerError(ProviderErrPathDenied, "invalid capability path segment")
		}
	}
	return nil
}

func matchProviderPath(pattern, path string) bool {
	if validateProviderPathPattern(pattern) != nil {
		return false
	}
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	for i, part := range patternParts {
		if part == "**" {
			return true
		}
		if i >= len(pathParts) {
			return false
		}
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			if pathParts[i] == "" {
				return false
			}
			continue
		}
		if part != pathParts[i] {
			return false
		}
	}
	return len(patternParts) == len(pathParts)
}

func validateProviderQuery(query map[string][]string, rules map[string]ProviderParamRule) error {
	for name, values := range query {
		rule, ok := rules[name]
		if !ok || !validProviderParameterName(name) || sensitiveResultKey(name) {
			return providerError(ProviderErrQueryDenied, "query parameter is not allowed")
		}
		maxValues := rule.MaxValues
		if maxValues <= 0 {
			maxValues = 1
		}
		if len(values) == 0 || len(values) > maxValues {
			return providerError(ProviderErrQueryDenied, "query parameter value count is invalid")
		}
		var expression *regexp.Regexp
		if rule.Pattern != "" {
			expression, _ = regexp.Compile(rule.Pattern)
		}
		maxLength := rule.MaxLength
		if maxLength <= 0 {
			maxLength = 1024
		}
		for _, value := range values {
			if len(value) > maxLength || strings.ContainsAny(value, "\r\n\x00") || (expression != nil && !expression.MatchString(value)) {
				return providerError(ProviderErrQueryDenied, "query parameter value is invalid")
			}
		}
	}
	for name, rule := range rules {
		if rule.Required && len(query[name]) == 0 {
			return providerError(ProviderErrQueryDenied, "required query parameter is missing")
		}
	}
	return nil
}

func validateProviderHeaders(headers map[string]string, allowed []string) error {
	allow := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allow[strings.ToLower(name)] = struct{}{}
	}
	for name, value := range headers {
		lower := strings.ToLower(name)
		if _, ok := allow[lower]; !ok || forbiddenProviderHeader(name) || !validProviderHeader(name) || len(value) > 1024 || strings.ContainsAny(value, "\r\n\x00") {
			return providerError(ProviderErrHeaderDenied, "header is not allowed")
		}
	}
	return nil
}

func forbiddenProviderHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return sensitiveResultKey(lower) || lower == "host" || lower == "connection" || lower == "content-length" || lower == "transfer-encoding" || strings.HasPrefix(lower, "proxy-") || strings.HasPrefix(lower, "x-forwarded-")
}

func validProviderParameterName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("._-[]", ch)) {
			return false
		}
	}
	return true
}

func validProviderHeader(name string) bool {
	return name != "" && http.CanonicalHeaderKey(name) != "" && !strings.ContainsAny(name, " \t\r\n:")
}

func providerError(code, message string) error {
	return &ProviderRequestError{Code: code, Message: message}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

// NormalizeProviderQuery provides deterministic audit material without
// including endpoint or credentials.
func NormalizeProviderQuery(query map[string][]string) string {
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := url.Values{}
	for _, key := range keys {
		for _, value := range query[key] {
			values.Add(key, value)
		}
	}
	return values.Encode()
}

func EffectiveProviderLimits(capability ResourceCapability) (maxItems, maxBytes, timeoutSeconds int) {
	maxItems = capability.MaxItems
	if maxItems <= 0 || maxItems > DefaultProviderMaxItems {
		maxItems = DefaultProviderMaxItems
	}
	maxBytes = capability.MaxResponseBytes
	if maxBytes <= 0 || maxBytes > DefaultProviderMaxBytes {
		maxBytes = DefaultProviderMaxBytes
	}
	timeoutSeconds = capability.TimeoutSeconds
	if timeoutSeconds <= 0 || timeoutSeconds > DefaultProviderTimeoutSec {
		timeoutSeconds = DefaultProviderTimeoutSec
	}
	return
}

func (e *ProviderRequestError) HTTPStatus() int {
	switch e.Code {
	case ProviderErrResourceNotFound, ProviderErrCapabilityNotFound:
		return http.StatusNotFound
	case ProviderErrResourceUnavailable, ProviderErrIntegrationInactive:
		return http.StatusServiceUnavailable
	case ProviderErrIntegrationMismatch, ProviderErrCapabilityMismatch, ProviderErrMethodDenied,
		ProviderErrPathDenied, ProviderErrQueryDenied, ProviderErrHeaderDenied, ProviderErrScopeDenied,
		ProviderErrEndpointUnsafe, ProviderErrRedirectDenied:
		return http.StatusForbidden
	case ProviderErrLimitExceeded, ProviderErrResponseTooLarge:
		return http.StatusRequestEntityTooLarge
	case ProviderErrTimeout:
		return http.StatusGatewayTimeout
	case ProviderErrUpstreamUnknown:
		return http.StatusBadGateway
	default:
		return http.StatusBadRequest
	}
}

func (c ResourceCapability) String() string {
	return c.Key() + "[" + strings.Join(c.ResourceKinds, ",") + "]"
}

func ProviderLimitWarning(capability ResourceCapability) string {
	items, bytes, timeout := EffectiveProviderLimits(capability)
	return fmt.Sprintf("maxItems=%s maxResponseBytes=%d timeoutSeconds=%d", strconv.Itoa(items), bytes, timeout)
}
