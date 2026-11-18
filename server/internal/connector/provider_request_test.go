package connector

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

func providerExecutorFixture(t *testing.T, handler http.HandlerFunc) (*ProviderExecutor, domain.ProviderRequest, domain.ProviderRequestBinding) {
	t.Helper()
	integration := domain.Integration{ID: "integration-1", ConnectorID: "connector-1", Protocol: "grafana",
		Endpoint: "https://grafana.example.com", TenantID: "tenant-1", Status: domain.IntegrationActive}
	resource := domain.Resource{ID: "resource-1", ResourceType: "grafana_dashboard", ConnectorID: "connector-1",
		IntegrationID: integration.ID, TenantID: "tenant-1", Status: domain.ResourceActive, ExternalID: "dashboard/payments", Name: "payments", ProjectID: "project-1", Environment: "production"}
	capability := domain.ResourceCapability{Name: "grafana.dashboard.read", Version: 1,
		ResourceKinds: []string{"grafana_dashboard"}, Protocols: []string{"grafana"}, Methods: []string{"GET"},
		PathPatterns: []string{"/api/dashboards/uid/{uid}"}, MaxItems: 2, MaxResponseBytes: 1024, TimeoutSeconds: 1}
	executor, err := NewProviderExecutor(ProviderExecutorConfig{Integration: integration, Credential: "connector-secret",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler(recorder, request)
			response := recorder.Result()
			response.Request = request
			return response, nil
		})}})
	if err != nil {
		t.Fatal(err)
	}
	request := domain.ProviderRequest{Version: 1, ResourceID: resource.ID, Capability: capability.Key(), Method: "GET", Path: "/api/dashboards/uid/payments"}
	return executor, request, domain.ProviderRequestBinding{Resource: resource, Integration: integration, Capability: capability}
}

func TestProviderExecutorInjectsCredentialAndSanitizesJSON(t *testing.T) {
	executor, request, binding := providerExecutorFixture(t, func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer connector-secret" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"payments","apiToken":"upstream-secret","nested":{"password":"hidden"}}`))
	})
	response, err := executor.Execute(context.Background(), request, binding)
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data.(map[string]any)
	if data["apiToken"] != "[REDACTED]" || data["nested"].(map[string]any)["password"] != "[REDACTED]" {
		t.Fatalf("response was not sanitized: %#v", data)
	}
	if response.Resource.ID != "" || response.Capability != "grafana.dashboard.read@v1" {
		t.Fatalf("unexpected normalized response: %#v", response)
	}
}

func TestProviderExecutorForwardsCorrelationHeaders(t *testing.T) {
	executor, request, binding := providerExecutorFixture(t, func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("X-Warden-Trace-Id"); got != "trace-abc-123" {
			t.Errorf("X-Warden-Trace-Id = %q, want trace-abc-123", got)
		}
		if got := request.Header.Get("X-Warden-Session-Key"); got != "session-1" {
			t.Errorf("X-Warden-Session-Key = %q, want session-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	request.Correlation = map[string]string{"trace_id": "trace-abc-123", "session_key": "session-1"}
	if _, err := executor.Execute(context.Background(), request, binding); err != nil {
		t.Fatal(err)
	}
}

func TestProviderExecutorEnforcesResponseAndItemLimits(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		edit func(*domain.ResourceCapability)
		code string
	}{
		{"bytes", strings.Repeat("x", 40), func(c *domain.ResourceCapability) { c.MaxResponseBytes = 16 }, domain.ProviderErrResponseTooLarge},
		{"items", `[{"id":1},{"id":2},{"id":3}]`, func(c *domain.ResourceCapability) { c.MaxItems = 2 }, domain.ProviderErrLimitExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor, request, binding := providerExecutorFixture(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(test.body)) })
			test.edit(&binding.Capability)
			_, err := executor.Execute(context.Background(), request, binding)
			assertProviderExecutorError(t, err, test.code)
		})
	}
}

func TestProviderExecutorRejectsRedirectAndTimeout(t *testing.T) {
	t.Run("redirect", func(t *testing.T) {
		executor, request, binding := providerExecutorFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "https://other.example/")
			w.WriteHeader(http.StatusFound)
		})
		_, err := executor.Execute(context.Background(), request, binding)
		assertProviderExecutorError(t, err, domain.ProviderErrRedirectDenied)
	})
	t.Run("timeout", func(t *testing.T) {
		executor, request, binding := providerExecutorFixture(t, func(http.ResponseWriter, *http.Request) {})
		executor.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})
		binding.Capability.TimeoutSeconds = 1
		_, err := executor.Execute(context.Background(), request, binding)
		assertProviderExecutorError(t, err, domain.ProviderErrTimeout)
	})
}

type staticProviderResolver []net.IPAddr

func (r staticProviderResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r, nil
}

func TestProviderDialRejectsUnsafeResolvedAddressBeforeConnect(t *testing.T) {
	dial := providerDialContext(staticProviderResolver{{IP: net.ParseIP("169.254.169.254")}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := dial(ctx, "tcp", "registered.example:80")
	assertProviderExecutorError(t, err, domain.ProviderErrEndpointUnsafe)
}

func TestProviderExecutorRateLimit(t *testing.T) {
	executor, request, binding := providerExecutorFixture(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) })
	for i := 0; i < 10; i++ {
		if _, err := executor.Execute(context.Background(), request, binding); err != nil {
			t.Fatalf("request %d failed: %v", i+1, err)
		}
	}
	_, err := executor.Execute(context.Background(), request, binding)
	assertProviderExecutorError(t, err, domain.ProviderErrLimitExceeded)
}

func assertProviderExecutorError(t *testing.T, err error, code string) {
	t.Helper()
	var providerErr *domain.ProviderRequestError
	if !errors.As(err, &providerErr) || providerErr.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

// writeProviderExecutorFixture returns an executor bound to a governed write
// capability for a Jenkins build trigger. The body schema requires {job, branch}.
func writeProviderExecutorFixture(t *testing.T, handler http.HandlerFunc) (*ProviderExecutor, domain.ProviderRequest, domain.ProviderRequestBinding) {
	t.Helper()
	integration := domain.Integration{ID: "integration-1", ConnectorID: "connector-1", Protocol: "jenkins",
		Endpoint: "https://jenkins.example.com", TenantID: "tenant-1", Status: domain.IntegrationActive}
	resource := domain.Resource{ID: "resource-1", ResourceType: "jenkins_job", ConnectorID: "connector-1",
		IntegrationID: integration.ID, TenantID: "tenant-1", Status: domain.ResourceActive, ExternalID: "job/platform", Name: "platform"}
	capability := domain.ResourceCapability{Name: "jenkins.job.trigger", Version: 1, Write: true,
		ResourceKinds: []string{"jenkins_job"}, Protocols: []string{"jenkins"}, Methods: []string{"POST"},
		PathPatterns: []string{"/job/{job}/build"}, MaxItems: 2, MaxResponseBytes: 1024, TimeoutSeconds: 2,
		MaxBodyBytes: 4096, RequestBodySchema: map[string]any{
			"type": "object", "required": []any{"branch"},
			"properties": map[string]any{"branch": map[string]any{"type": "string"}},
		}}
	executor, err := NewProviderExecutor(ProviderExecutorConfig{Integration: integration, Credential: "connector-secret",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler(recorder, request)
			response := recorder.Result()
			response.Request = request
			return response, nil
		})}})
	if err != nil {
		t.Fatal(err)
	}
	request := domain.ProviderRequest{Version: 1, ResourceID: resource.ID, Capability: capability.Key(),
		Method: "POST", Path: "/job/platform/build", Body: map[string]any{"branch": "main"}}
	return executor, request, domain.ProviderRequestBinding{Resource: resource, Integration: integration, Capability: capability}
}

func TestProviderWriteExecutorSendsBodyAndContentType(t *testing.T) {
	var capturedBody string
	executor, request, binding := writeProviderExecutorFixture(t, func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := request.Header.Get("Authorization"); got != "Basic OmNvbm5lY3Rvci1zZWNyZXQ=" {
			t.Errorf("Authorization = %q", got)
		}
		raw := make([]byte, 4096)
		n, _ := request.Body.Read(raw)
		capturedBody = strings.TrimSpace(string(raw[:n]))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"queued"}`))
	})
	response, err := executor.Execute(context.Background(), request, binding)
	if err != nil {
		t.Fatal(err)
	}
	if capturedBody != `{"branch":"main"}` {
		t.Fatalf("upstream received body = %q", capturedBody)
	}
	if response.Capability != "jenkins.job.trigger@v1" {
		t.Fatalf("unexpected capability: %#v", response)
	}
}

func TestProviderWriteExecutorRejectsSchemaMismatchBeforeSend(t *testing.T) {
	executor, request, binding := writeProviderExecutorFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream must not be called for a rejected body")
	})
	request.Body = map[string]any{"branch": 42} // schema requires string
	_, err := executor.Execute(context.Background(), request, binding)
	assertProviderExecutorError(t, err, domain.ProviderErrInvalidRequest)
}

func TestProviderWriteExecutorRejectsOversizedBodyBeforeSend(t *testing.T) {
	executor, request, binding := writeProviderExecutorFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream must not be called for an oversized body")
	})
	binding.Capability.MaxBodyBytes = 8
	_, err := executor.Execute(context.Background(), request, binding)
	assertProviderExecutorError(t, err, domain.ProviderErrLimitExceeded)
}

func TestIsProviderTimeout(t *testing.T) {
	if !isProviderTimeout(domainProviderError(domain.ProviderErrTimeout, "timed out")) {
		t.Fatal("isProviderTimeout() = false for ProviderErrTimeout")
	}
	if isProviderTimeout(domainProviderError(domain.ProviderErrUpstreamUnknown, "boom")) {
		t.Fatal("isProviderTimeout() = true for a non-timeout error")
	}
	if isProviderTimeout(errors.New("boom")) {
		t.Fatal("isProviderTimeout() = true for a plain error")
	}
}

func TestProviderWriteExecutorRejectsReadCapability(t *testing.T) {
	executor, request, binding := writeProviderExecutorFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream must not be called for a read-only capability")
	})
	binding.Capability.Write = false
	// A non-write capability with POST methods is itself invalid under the read
	// contract, so the executor must refuse before constructing a request.
	_, err := executor.Execute(context.Background(), request, binding)
	if err == nil {
		t.Fatal("Execute() accepted a read capability for a write request")
	}
}
