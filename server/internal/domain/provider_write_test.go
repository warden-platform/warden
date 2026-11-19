package domain

import (
	"net/http"
	"testing"
	"time"
)

// writeCapabilityFixture returns a valid governed write capability for a
// Jenkins job plus the matching binding. All tests mutate a copy.
func writeCapabilityFixture() (ProviderRequest, ProviderRequestBinding) {
	request := ProviderRequest{
		Version: ProviderRequestVersion, ResourceID: "resource-1", Capability: "jenkins.job.create@v1",
		Method: http.MethodPost, Path: "/job/platform/build",
		Body:    map[string]any{"name": "platform", "branch": "main"},
		Headers: map[string]string{"X-Job-Branch": "main"},
	}
	binding := ProviderRequestBinding{
		Resource:    Resource{ID: "resource-1", ResourceType: "jenkins_job", ExternalID: "job/platform", Name: "platform", ConnectorID: "connector-1", IntegrationID: "integration-1", TenantID: "tenant-1", Status: ResourceActive},
		Integration: Integration{ID: "integration-1", ConnectorID: "connector-1", Protocol: "jenkins", Endpoint: "https://jenkins.example.com", TenantID: "tenant-1", Status: IntegrationActive},
		Capability: ResourceCapability{
			Name: "jenkins.job.create", Version: 1, Write: true, ResourceKinds: []string{"jenkins_job"}, Protocols: []string{"jenkins"},
			Methods: []string{http.MethodPost}, PathPatterns: []string{"/job/{job}/build"},
			AllowedHeaders: []string{"X-Job-Branch"},
			MaxItems:       1, MaxResponseBytes: 1 << 20, TimeoutSeconds: 30, MaxTimeRange: time.Hour,
			MaxBodyBytes: 64 << 10, RequestBodySchema: map[string]any{
				"type": "object", "required": []any{"name", "branch"},
				"properties": map[string]any{
					"name":   map[string]any{"type": "string"},
					"branch": map[string]any{"type": "string"},
				},
			},
		},
	}
	return request, binding
}

func TestValidateProviderWriteCapabilityAcceptsGovernedWrite(t *testing.T) {
	_, binding := writeCapabilityFixture()
	if err := ValidateProviderWriteCapability(binding.Capability); err != nil {
		t.Fatalf("ValidateProviderWriteCapability() error = %v", err)
	}
}

func TestValidateProviderWriteCapabilityRejectsReadCapability(t *testing.T) {
	_, binding := writeCapabilityFixture()
	binding.Capability.Write = false
	assertProviderErrorCode(t, ValidateProviderWriteCapability(binding.Capability), ProviderErrInvalidRequest)
}

func TestValidateProviderWriteCapabilityRejectsDelete(t *testing.T) {
	_, binding := writeCapabilityFixture()
	binding.Capability.Methods = []string{http.MethodDelete}
	assertProviderErrorCode(t, ValidateProviderWriteCapability(binding.Capability), ProviderErrMethodDenied)
}

func TestValidateProviderWriteCapabilityRejectsGet(t *testing.T) {
	_, binding := writeCapabilityFixture()
	binding.Capability.Methods = []string{http.MethodGet}
	assertProviderErrorCode(t, ValidateProviderWriteCapability(binding.Capability), ProviderErrMethodDenied)
}

func TestValidateProviderWriteCapabilityRejectsKubernetes(t *testing.T) {
	_, binding := writeCapabilityFixture()
	binding.Capability.Protocols = []string{"kubernetes"}
	assertProviderErrorCode(t, ValidateProviderWriteCapability(binding.Capability), ProviderErrMethodDenied)
}

func TestValidateProviderWriteCapabilityRejectsInvalidBodySchema(t *testing.T) {
	_, binding := writeCapabilityFixture()
	binding.Capability.RequestBodySchema = map[string]any{"type": "not-a-json-schema-type"}
	assertProviderErrorCode(t, ValidateProviderWriteCapability(binding.Capability), ProviderErrInvalidRequest)
}

func TestValidateProviderWriteAcceptsBoundWrite(t *testing.T) {
	request, binding := writeCapabilityFixture()
	if err := ValidateProviderWrite(request, binding); err != nil {
		t.Fatalf("ValidateProviderWrite() error = %v", err)
	}
}

func TestValidateProviderWriteRejectsNonWriteCapability(t *testing.T) {
	request, binding := writeCapabilityFixture()
	binding.Capability.Write = false
	assertProviderErrorCode(t, ValidateProviderWrite(request, binding), ProviderErrCapabilityMismatch)
}

func TestValidateProviderWriteRejectsGetMethod(t *testing.T) {
	request, binding := writeCapabilityFixture()
	request.Method = http.MethodGet
	assertProviderErrorCode(t, ValidateProviderWrite(request, binding), ProviderErrMethodDenied)
}

func TestValidateProviderWriteRejectsDeleteMethod(t *testing.T) {
	request, binding := writeCapabilityFixture()
	request.Method = http.MethodDelete
	assertProviderErrorCode(t, ValidateProviderWrite(request, binding), ProviderErrMethodDenied)
}

func TestValidateProviderWriteRejectsMissingBody(t *testing.T) {
	request, binding := writeCapabilityFixture()
	request.Body = nil
	assertProviderErrorCode(t, ValidateProviderWrite(request, binding), ProviderErrInvalidRequest)
}

func TestValidateProviderWriteRejectsSchemaMismatch(t *testing.T) {
	request, binding := writeCapabilityFixture()
	request.Body = map[string]any{"name": "platform"} // branch missing
	assertProviderErrorCode(t, ValidateProviderWrite(request, binding), ProviderErrInvalidRequest)
}

func TestValidateProviderWriteRejectsOversizedBody(t *testing.T) {
	request, binding := writeCapabilityFixture()
	binding.Capability.MaxBodyBytes = 8
	assertProviderErrorCode(t, ValidateProviderWrite(request, binding), ProviderErrLimitExceeded)
}

func TestValidateProviderWriteRejectsPageAndTimeRange(t *testing.T) {
	request, binding := writeCapabilityFixture()
	request.Page = &ProviderPage{Limit: 10}
	assertProviderErrorCode(t, ValidateProviderWrite(request, binding), ProviderErrInvalidRequest)
	request, binding = writeCapabilityFixture()
	request.TimeRange = &ProviderTimeRange{Start: time.Now().Add(-time.Hour), End: time.Now()}
	assertProviderErrorCode(t, ValidateProviderWrite(request, binding), ProviderErrInvalidRequest)
}

func TestValidateProviderWriteRejectsBodyOnReadRequest(t *testing.T) {
	request, binding := providerFixture()
	request.Body = map[string]any{"name": "platform"}
	assertProviderErrorCode(t, ValidateProviderRequest(request, binding), ProviderErrInvalidRequest)
}

func TestValidateProviderWriteRejectsNativeKubernetesWrite(t *testing.T) {
	request, binding := writeCapabilityFixture()
	// A resource with no Integration means native Kubernetes. Write
	// capabilities forbid the kubernetes protocol at catalog time, and the
	// executor path never constructs a kubernetes binding for a write; a
	// request that somehow reaches this state must still be rejected.
	binding.Resource.IntegrationID = ""
	binding.Integration.ID = ""
	binding.Integration.Protocol = "kubernetes"
	binding.Integration.Endpoint = "https://kubernetes.default.svc"
	if err := ValidateProviderWrite(request, binding); err == nil {
		t.Fatal("ValidateProviderWrite() accepted a native Kubernetes write")
	}
}

func TestProviderWriteMaxBodyDefaults(t *testing.T) {
	if got := providerWriteMaxBody(ResourceCapability{}); got != DefaultProviderMaxBody {
		t.Fatalf("providerWriteMaxBody(default) = %d, want %d", got, DefaultProviderMaxBody)
	}
	if got := providerWriteMaxBody(ResourceCapability{MaxBodyBytes: MaxProviderBodyBytes}); got != MaxProviderBodyBytes {
		t.Fatalf("providerWriteMaxBody(max) = %d, want %d", got, MaxProviderBodyBytes)
	}
	if got := providerWriteMaxBody(ResourceCapability{MaxBodyBytes: MaxProviderBodyBytes + 1}); got != DefaultProviderMaxBody {
		t.Fatalf("providerWriteMaxBody(over-max) = %d, want %d", got, DefaultProviderMaxBody)
	}
}
