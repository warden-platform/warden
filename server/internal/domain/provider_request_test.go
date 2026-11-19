package domain

import (
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func providerFixture() (ProviderRequest, ProviderRequestBinding) {
	request := ProviderRequest{
		Version: ProviderRequestVersion, ResourceID: "resource-1", Capability: "jenkins.job.read@v1",
		Method: http.MethodGet, Path: "/job/platform/api/json",
		Query: map[string][]string{"tree": {"name,url,color"}}, Page: &ProviderPage{Limit: 50},
		Correlation: map[string]string{"trace_id": "trace-1"},
	}
	binding := ProviderRequestBinding{
		Resource:    Resource{ID: "resource-1", ResourceType: "jenkins_job", ExternalID: "job/platform", Name: "platform", ConnectorID: "connector-1", IntegrationID: "integration-1", TenantID: "tenant-1", Status: ResourceActive},
		Integration: Integration{ID: "integration-1", ConnectorID: "connector-1", Protocol: "jenkins", Endpoint: "https://jenkins.example.com", TenantID: "tenant-1", Status: IntegrationActive},
		Capability: ResourceCapability{
			Name: "jenkins.job.read", Version: 1, ResourceKinds: []string{"jenkins_job"}, Protocols: []string{"jenkins"},
			Methods: []string{http.MethodGet}, PathPatterns: []string{"/job/{job}/api/json"},
			QueryParameters: map[string]ProviderParamRule{"tree": {MaxValues: 1, MaxLength: 128}},
			MaxItems:        100, MaxResponseBytes: 1 << 20, TimeoutSeconds: 10, MaxTimeRange: time.Hour,
		},
	}
	return request, binding
}

func TestValidateProviderRequestAcceptsBoundRead(t *testing.T) {
	request, binding := providerFixture()
	if err := ValidateProviderCapability(binding.Capability); err != nil {
		t.Fatalf("ValidateProviderCapability() error = %v", err)
	}
	if err := ValidateProviderRequest(request, binding); err != nil {
		t.Fatalf("ValidateProviderRequest() error = %v", err)
	}
}

func TestValidateProviderRequestAcceptsBoundOBSObjectWithNestedKey(t *testing.T) {
	request := ProviderRequest{
		Version: ProviderRequestVersion, ResourceID: "resource-1", Capability: "huawei.obs.object.metadata.read@v1",
		Method: http.MethodHead, Path: "/example-files/ollama/README.md",
	}
	binding := ProviderRequestBinding{
		Resource:    Resource{ID: "resource-1", ResourceType: "huawei_obs_bucket", ExternalID: "obs/example-files", Name: "example-files", ConnectorID: "connector-1", IntegrationID: "integration-1", TenantID: "tenant-1", Status: ResourceActive},
		Integration: Integration{ID: "integration-1", ConnectorID: "connector-1", Protocol: "huaweicloud", Endpoint: "cn-east-3", TenantID: "tenant-1", Status: IntegrationActive},
		Capability:  ResourceCapability{Name: "huawei.obs.object.metadata.read", Version: 1, ResourceKinds: []string{"huawei_obs_bucket"}, Protocols: []string{"huaweicloud"}, Methods: []string{http.MethodHead}, PathPatterns: []string{"/{bucket}/**"}, MaxItems: 1, MaxResponseBytes: 64 << 10, TimeoutSeconds: 15},
	}

	if err := ValidateProviderRequest(request, binding); err != nil {
		t.Fatalf("ValidateProviderRequest() error = %v", err)
	}
	request.Path = "/another-bucket/ollama/README.md"
	assertProviderErrorCode(t, ValidateProviderRequest(request, binding), ProviderErrScopeDenied)
}

func TestValidateProviderRequestRejectsBoundaryBypasses(t *testing.T) {
	tests := []struct {
		name string
		edit func(*ProviderRequest, *ProviderRequestBinding)
		code string
	}{
		{"resource substitution", func(r *ProviderRequest, _ *ProviderRequestBinding) { r.ResourceID = "other" }, ProviderErrResourceNotFound},
		{"unbound integration", func(_ *ProviderRequest, b *ProviderRequestBinding) { b.Resource.IntegrationID = "" }, ProviderErrIntegrationMismatch},
		{"connector mismatch", func(_ *ProviderRequest, b *ProviderRequestBinding) { b.Integration.ConnectorID = "other" }, ProviderErrIntegrationMismatch},
		{"tenant mismatch", func(_ *ProviderRequest, b *ProviderRequestBinding) { b.Integration.TenantID = "other" }, ProviderErrScopeDenied},
		{"inactive integration", func(_ *ProviderRequest, b *ProviderRequestBinding) { b.Integration.Status = IntegrationDisabled }, ProviderErrIntegrationInactive},
		{"capability substitution", func(r *ProviderRequest, _ *ProviderRequestBinding) { r.Capability = "jenkins.job.delete@v1" }, ProviderErrCapabilityNotFound},
		{"kind mismatch", func(_ *ProviderRequest, b *ProviderRequestBinding) { b.Resource.ResourceType = "jenkins_controller" }, ProviderErrCapabilityMismatch},
		{"write method", func(r *ProviderRequest, _ *ProviderRequestBinding) { r.Method = http.MethodPost }, ProviderErrMethodDenied},
		{"absolute URL", func(r *ProviderRequest, _ *ProviderRequestBinding) { r.Path = "https://evil.example/api" }, ProviderErrPathDenied},
		{"network path", func(r *ProviderRequest, _ *ProviderRequestBinding) { r.Path = "//evil.example/api" }, ProviderErrPathDenied},
		{"path traversal", func(r *ProviderRequest, _ *ProviderRequestBinding) { r.Path = "/job/../api/json" }, ProviderErrPathDenied},
		{"encoded separator", func(r *ProviderRequest, _ *ProviderRequestBinding) { r.Path = "/job/a%2Fb/api/json" }, ProviderErrPathDenied},
		{"path outside capability", func(r *ProviderRequest, _ *ProviderRequestBinding) {
			r.Path = "/credentials/store/system/domain/_/api/json"
		}, ProviderErrPathDenied},
		{"cross resource path", func(r *ProviderRequest, _ *ProviderRequestBinding) { r.Path = "/job/other/api/json" }, ProviderErrScopeDenied},
		{"undeclared query", func(r *ProviderRequest, _ *ProviderRequestBinding) { r.Query["depth"] = []string{"5"} }, ProviderErrQueryDenied},
		{"secret query", func(r *ProviderRequest, b *ProviderRequestBinding) {
			b.Capability.QueryParameters["api_token"] = ProviderParamRule{}
			r.Query["api_token"] = []string{"secret"}
		}, ProviderErrQueryDenied},
		{"authorization header", func(r *ProviderRequest, b *ProviderRequestBinding) {
			b.Capability.AllowedHeaders = []string{"Authorization"}
			r.Headers = map[string]string{"Authorization": "Bearer secret"}
		}, ProviderErrHeaderDenied},
		{"host header", func(r *ProviderRequest, b *ProviderRequestBinding) {
			b.Capability.AllowedHeaders = []string{"Host"}
			r.Headers = map[string]string{"Host": "evil.example"}
		}, ProviderErrHeaderDenied},
		{"page overflow", func(r *ProviderRequest, _ *ProviderRequestBinding) { r.Page.Limit = 101 }, ProviderErrLimitExceeded},
		{"metadata endpoint", func(_ *ProviderRequest, b *ProviderRequestBinding) {
			b.Integration.Endpoint = "http://169.254.169.254/latest"
		}, ProviderErrEndpointUnsafe},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, binding := providerFixture()
			tt.edit(&request, &binding)
			err := ValidateProviderRequest(request, binding)
			assertProviderErrorCode(t, err, tt.code)
		})
	}
}

func TestValidateProviderRequestTimeRange(t *testing.T) {
	request, binding := providerFixture()
	request.TimeRange = &ProviderTimeRange{Start: time.Now().Add(-2 * time.Hour), End: time.Now()}
	assertProviderErrorCode(t, ValidateProviderRequest(request, binding), ProviderErrLimitExceeded)
}

func TestValidateProviderCapabilityRejectsWriteAndUnsafeCatalogEntries(t *testing.T) {
	_, binding := providerFixture()
	tests := []struct {
		name string
		edit func(*ResourceCapability)
		code string
	}{
		{"write method", func(c *ResourceCapability) { c.Methods = []string{http.MethodDelete} }, ProviderErrMethodDenied},
		{"arbitrary wildcard", func(c *ResourceCapability) { c.PathPatterns = []string{"/**/api"} }, ProviderErrPathDenied},
		{"unsafe header", func(c *ResourceCapability) { c.AllowedHeaders = []string{"Cookie"} }, ProviderErrHeaderDenied},
		{"sensitive query", func(c *ResourceCapability) { c.QueryParameters["password"] = ProviderParamRule{} }, ProviderErrInvalidRequest},
		{"response too large", func(c *ResourceCapability) { c.MaxResponseBytes = DefaultProviderMaxBytes + 1 }, ProviderErrLimitExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capability := binding.Capability
			capability.QueryParameters = map[string]ProviderParamRule{"tree": {MaxLength: 128}}
			tt.edit(&capability)
			assertProviderErrorCode(t, ValidateProviderCapability(capability), tt.code)
		})
	}
}

func TestValidateProviderEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"https://user:pass@example.com", "file:///etc/passwd", "http://localhost:3000", "http://127.0.0.1", "http://[::1]", "http://100.100.100.200", "http://metadata.google.internal",
	} {
		t.Run(endpoint, func(t *testing.T) {
			assertProviderErrorCode(t, ValidateProviderEndpoint(endpoint), ProviderErrEndpointUnsafe)
		})
	}
	for _, endpoint := range []string{"https://grafana.example.com", "http://10.0.0.20:8080/base"} {
		if err := ValidateProviderEndpoint(endpoint); err != nil {
			t.Errorf("ValidateProviderEndpoint(%q) error = %v", endpoint, err)
		}
	}
}

func TestProviderRedirectPolicy(t *testing.T) {
	origin, _ := url.Parse("https://grafana.example.com")
	policy := ProviderRedirectPolicy(origin)
	for _, tc := range []struct {
		url  string
		want string
	}{
		{"https://grafana.example.com/login", ""},
		{"https://grafana.example.com:444/login", ProviderErrRedirectDenied},
		{"https://evil.example/login", ProviderErrRedirectDenied},
		{"http://grafana.example.com/login", ProviderErrRedirectDenied},
	} {
		target, _ := url.Parse(tc.url)
		err := policy(&http.Request{URL: target}, nil)
		if tc.want == "" && err != nil {
			t.Errorf("redirect to %s error = %v", tc.url, err)
		} else if tc.want != "" {
			assertProviderErrorCode(t, err, tc.want)
		}
	}
}

func TestNormalizeProviderQueryIsDeterministic(t *testing.T) {
	got := NormalizeProviderQuery(map[string][]string{"z": {"2", "1"}, "a": {"x y"}})
	if got != "a=x+y&z=2&z=1" {
		t.Fatalf("NormalizeProviderQuery() = %q", got)
	}
}

func TestEffectiveProviderLimits(t *testing.T) {
	got := []int{}
	a, b, c := EffectiveProviderLimits(ResourceCapability{})
	got = append(got, a, b, c)
	want := []int{DefaultProviderMaxItems, DefaultProviderMaxBytes, DefaultProviderTimeoutSec}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EffectiveProviderLimits() = %#v, want %#v", got, want)
	}
}

func TestProviderRequestErrorHTTPStatus(t *testing.T) {
	tests := map[string]int{
		ProviderErrInvalidRequest:      http.StatusBadRequest,
		ProviderErrResourceNotFound:    http.StatusNotFound,
		ProviderErrIntegrationMismatch: http.StatusForbidden,
		ProviderErrIntegrationInactive: http.StatusServiceUnavailable,
		ProviderErrLimitExceeded:       http.StatusRequestEntityTooLarge,
		ProviderErrTimeout:             http.StatusGatewayTimeout,
		ProviderErrUpstreamUnknown:     http.StatusBadGateway,
	}
	for code, want := range tests {
		if got := (&ProviderRequestError{Code: code}).HTTPStatus(); got != want {
			t.Errorf("HTTPStatus(%s) = %d, want %d", code, got, want)
		}
	}
}

func assertProviderErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", want)
	}
	var providerErr *ProviderRequestError
	if !errors.As(err, &providerErr) || providerErr.Code != want {
		t.Fatalf("error = %v, want provider code %s", err, want)
	}
}
