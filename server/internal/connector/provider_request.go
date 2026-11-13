package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

// ProviderResolver is deliberately smaller than net.Resolver so tests and
// deployments with an internal DNS resolver can supply it without weakening
// the post-resolution address checks.
type ProviderResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type ProviderExecutorConfig struct {
	Integration domain.Integration
	Credential  string
	HTTPClient  *http.Client
	Resolver    ProviderResolver
	// ResolveTarget and Authenticate are Connector-owned protocol hooks. They
	// are required for providers such as Huawei Cloud where Integration.Endpoint
	// is a region and requests use per-service fixed hosts plus AK/SK signing.
	ResolveTarget func(context.Context, domain.ProviderRequest, domain.ProviderRequestBinding) (*url.URL, error)
	Authenticate  func(*http.Request) error
}

// ProviderExecutor executes the generic read contract for exactly one
// materialized Integration. Endpoint and credentials never come from the
// agent request.
type ProviderExecutor struct {
	integration   domain.Integration
	credential    string
	http          *http.Client
	resolveTarget func(context.Context, domain.ProviderRequest, domain.ProviderRequestBinding) (*url.URL, error)
	authenticate  func(*http.Request) error
	limitMu       sync.Mutex
	limitWindow   time.Time
	limitCount    int
}

func NewProviderExecutor(config ProviderExecutorConfig) (*ProviderExecutor, error) {
	if config.ResolveTarget == nil {
		if err := domain.ValidateProviderEndpoint(config.Integration.Endpoint); err != nil {
			return nil, err
		}
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	if clone.Transport == nil {
		resolver := config.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil // a process proxy could change the registered target
		transport.DialContext = providerDialContext(resolver)
		clone.Transport = transport
	}
	// Redirects are denied in v1. This is stricter than same-origin following
	// and ensures credentials are never replayed after an upstream 30x.
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return domainProviderError(domain.ProviderErrRedirectDenied, "provider redirect is not allowed")
	}
	return &ProviderExecutor{integration: config.Integration, credential: config.Credential, http: &clone,
		resolveTarget: config.ResolveTarget, authenticate: config.Authenticate}, nil
}

func providerDialContext(resolver ProviderResolver) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, domainProviderError(domain.ProviderErrEndpointUnsafe, "invalid target address")
		}
		addresses, err := resolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, domainProviderError(domain.ProviderErrUpstreamUnknown, "target DNS resolution failed")
		}
		for _, address := range addresses {
			if err := domain.ValidateProviderIP(address.IP); err != nil {
				return nil, err
			}
		}
		var last error
		for _, resolved := range addresses {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			last = dialErr
		}
		return nil, fmt.Errorf("%w: %v", domainProviderError(domain.ProviderErrUpstreamUnknown, "target connection failed"), last)
	}
}

func (e *ProviderExecutor) Execute(ctx context.Context, request domain.ProviderRequest, binding domain.ProviderRequestBinding) (*domain.ProviderResponse, error) {
	if !e.allowRequest(time.Now()) {
		return nil, domainProviderError(domain.ProviderErrLimitExceeded, "provider request rate limit exceeded")
	}
	if binding.Integration.ID != e.integration.ID || binding.Integration.Endpoint != e.integration.Endpoint {
		return nil, domainProviderError(domain.ProviderErrIntegrationMismatch, "Connector Integration binding mismatch")
	}
	// Write capabilities are validated under the write contract so the
	// executor enforces the POST/PUT/PATCH + body-schema rules independently of
	// the control plane (defense in depth).
	if binding.Capability.Write {
		if err := domain.ValidateProviderWriteCapability(binding.Capability); err != nil {
			return nil, err
		}
		if err := domain.ValidateProviderWrite(request, binding); err != nil {
			return nil, err
		}
	} else {
		if err := domain.ValidateProviderCapability(binding.Capability); err != nil {
			return nil, err
		}
		if err := domain.ValidateProviderRequest(request, binding); err != nil {
			return nil, err
		}
	}
	_, maxBytes, timeoutSeconds := domain.EffectiveProviderLimits(binding.Capability)
	requestCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	var target *url.URL
	var err error
	if e.resolveTarget != nil {
		target, err = e.resolveTarget(requestCtx, request, binding)
	} else {
		target, _ = url.Parse(e.integration.Endpoint)
		target.Path = strings.TrimRight(target.Path, "/") + request.Path
		target.RawPath = ""
		target.RawQuery = domain.NormalizeProviderQuery(request.Query)
	}
	if err != nil || target == nil || domain.ValidateProviderEndpoint(target.Scheme+"://"+target.Host) != nil {
		return nil, domainProviderError(domain.ProviderErrEndpointUnsafe, "protocol adapter returned an unsafe target")
	}
	var requestBody io.Reader
	if binding.Capability.Write {
		payload, err := json.Marshal(request.Body)
		if err != nil {
			return nil, domainProviderError(domain.ProviderErrInvalidRequest, "write request body could not be serialized")
		}
		requestBody = bytes.NewReader(payload)
	}
	upstream, err := http.NewRequestWithContext(requestCtx, strings.ToUpper(request.Method), target.String(), requestBody)
	if err != nil {
		return nil, domainProviderError(domain.ProviderErrInvalidRequest, "could not construct provider request")
	}
	for name, value := range request.Headers {
		upstream.Header.Set(name, value)
	}
	// Correlation entries are forwarded as fixed-prefix upstream headers so the
	// session trace survives the control plane -> Connector -> provider hop. The
	// key charset is already constrained by ValidateProviderRequest, so mapping
	// '_' to '-' and prefixing "X-Pam-" cannot introduce header injection.
	for key, value := range request.Correlation {
		header := http.CanonicalHeaderKey("X-Pam-" + strings.ReplaceAll(key, "_", "-"))
		upstream.Header.Set(header, value)
	}
	upstream.Header.Set("Accept", "application/json")
	if binding.Capability.Write {
		// The write payload is always validated as JSON, so the executor owns
		// the Content-Type; agents cannot smuggle a different encoding.
		upstream.Header.Set("Content-Type", "application/json")
	}
	if e.authenticate != nil {
		if err := e.authenticate(upstream); err != nil {
			return nil, domainProviderError(domain.ProviderErrIntegrationMismatch, "protocol authentication failed")
		}
	} else {
		e.injectCredential(upstream)
	}

	response, err := e.http.Do(upstream)
	if err != nil {
		var providerErr *domain.ProviderRequestError
		if errors.As(err, &providerErr) {
			return nil, providerErr
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return nil, domainProviderError(domain.ProviderErrTimeout, "provider request timed out")
		}
		return nil, domainProviderError(domain.ProviderErrUpstreamUnknown, "provider connection failed")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, domainProviderError(domain.ProviderErrRedirectDenied, "provider redirect is not allowed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, domainProviderError(domain.ProviderErrUpstreamUnknown, "provider returned HTTP "+strconv.Itoa(response.StatusCode))
	}

	result := &domain.ProviderResponse{Status: domain.ProviderResponseSucceeded,
		Resource: domain.ProviderResourceRef{Kind: binding.Resource.ResourceType,
			Name: binding.Resource.Name, ProjectID: binding.Resource.ProjectID, Environment: binding.Resource.Environment},
		Capability: binding.Capability.Key()}
	if strings.EqualFold(request.Method, http.MethodHead) || response.StatusCode == http.StatusNoContent {
		result.Status = domain.ProviderResponseNoData
		return result, nil
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, int64(maxBytes)+1))
	if err != nil {
		return nil, domainProviderError(domain.ProviderErrUpstreamUnknown, "provider response could not be read")
	}
	if len(payload) > maxBytes {
		return nil, domainProviderError(domain.ProviderErrResponseTooLarge, "provider response exceeds the capability limit")
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		result.Status = domain.ProviderResponseNoData
		return result, nil
	}
	var data any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil {
		return nil, domainProviderError(domain.ProviderErrUpstreamUnknown, "provider returned a non-JSON response")
	}
	data = sanitizeProviderData(data)
	maxItems, _, _ := domain.EffectiveProviderLimits(binding.Capability)
	if providerItemCount(data) > maxItems {
		return nil, domainProviderError(domain.ProviderErrLimitExceeded, "provider result exceeds the item limit")
	}
	result.Data = data
	if providerItemCount(data) == 0 {
		result.Status = domain.ProviderResponseNoData
	}
	return result, nil
}

// allowRequest provides a conservative per-Integration gateway ceiling. It is
// deliberately local to the Connector; distributed control-plane limits may
// further tighten this, but cannot disable this last-hop protection.
func (e *ProviderExecutor) allowRequest(now time.Time) bool {
	e.limitMu.Lock()
	defer e.limitMu.Unlock()
	if e.limitWindow.IsZero() || now.Sub(e.limitWindow) >= time.Second {
		e.limitWindow, e.limitCount = now, 0
	}
	if e.limitCount >= 10 {
		return false
	}
	e.limitCount++
	return true
}

func (e *ProviderExecutor) injectCredential(request *http.Request) {
	switch e.integration.Protocol {
	case "jenkins":
		request.SetBasicAuth(e.integration.Username, e.credential)
	case "grafana", "kubernetes":
		request.Header.Set("Authorization", "Bearer "+e.credential)
	}
}

func sanitizeProviderData(value any) any {
	if object, ok := value.(map[string]any); ok {
		return domain.SanitizeResult(object)
	}
	if list, ok := value.([]any); ok {
		return domain.SanitizeResult(map[string]any{"items": list})["items"]
	}
	return value
}

func providerItemCount(value any) int {
	switch typed := value.(type) {
	case []any:
		return len(typed)
	case map[string]any:
		for _, key := range []string{"items", "results", "data"} {
			if list, ok := typed[key].([]any); ok {
				return len(list)
			}
		}
		if len(typed) == 0 {
			return 0
		}
		return 1
	default:
		return 1
	}
}

func domainProviderError(code, message string) error {
	return &domain.ProviderRequestError{Code: code, Message: message}
}
