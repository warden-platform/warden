package control

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gitee.com/pam-platform/pam/server/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// --- DB-gated integration tests (PAM_TEST_DATABASE_URL) ---

func TestProviderCapabilityCatalogCRUD(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)

	// Seed from migration 065.
	resp := doAdminRequest(t, mux, http.MethodGet, "/api/v1/provider-capabilities", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("list seed returned %d: %s", resp.Code, resp.Body.String())
	}
	seed := catalogResponse{}
	if err := json.Unmarshal(resp.Body.Bytes(), &seed); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	if len(seed.Capabilities) != len(domain.BuiltinProviderCapabilities) {
		t.Fatalf("seeded capabilities = %d, want %d", len(seed.Capabilities), len(domain.BuiltinProviderCapabilities))
	}

	// Create a new capability (admin-registered, passes validation).
	created := doAdminRequest(t, mux, http.MethodPost, "/api/v1/provider-capabilities", `{
		"name":"jenkins.build.read","version":1,
		"resourceKinds":["jenkins_job"],"protocols":["jenkins"],"methods":["GET"],
		"pathPatterns":["/job/{job}/api/json"],"maxItems":10,"maxResponseBytes":1048576,"timeoutSeconds":10,
		"description":"读取 Jenkins 构建状态"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create returned %d: %s", created.Code, created.Body.String())
	}
	var stored domain.ResourceCapability
	if err := json.Unmarshal(created.Body.Bytes(), &stored); err != nil {
		t.Fatalf("decode created capability: %v", err)
	}
	if stored.ID == "" || stored.Name != "jenkins.build.read" {
		t.Fatalf("created capability = %+v", stored)
	}

	// Invalid entries are rejected by ValidateProviderCapability.
	invalid := doAdminRequest(t, mux, http.MethodPost, "/api/v1/provider-capabilities", `{
		"name":"bad.write","version":1,"resourceKinds":["jenkins_job"],"protocols":["jenkins"],
		"methods":["POST"],"pathPatterns":["/job/{job}/api/json"]}`)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "PROVIDER_METHOD_DENIED") {
		t.Fatalf("invalid create returned %d: %s", invalid.Code, invalid.Body.String())
	}

	// Duplicate (name, version) is rejected.
	duplicate := doAdminRequest(t, mux, http.MethodPost, "/api/v1/provider-capabilities", `{
		"name":"jenkins.build.read","version":1,"resourceKinds":["jenkins_job"],"protocols":["jenkins"],
		"methods":["GET"],"pathPatterns":["/job/{job}/api/json"]}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate create returned %d: %s", duplicate.Code, duplicate.Body.String())
	}

	// Update in place.
	updated := doAdminRequest(t, mux, http.MethodPut, "/api/v1/provider-capabilities/"+stored.ID, `{
		"name":"jenkins.build.read","version":2,"resourceKinds":["jenkins_job"],"protocols":["jenkins"],
		"methods":["GET"],"pathPatterns":["/job/{job}/api/json"],"description":"updated"}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("update returned %d: %s", updated.Code, updated.Body.String())
	}

	// Soft delete; second delete is 404.
	firstDelete := doAdminRequest(t, mux, http.MethodDelete, "/api/v1/provider-capabilities/"+stored.ID, "")
	if firstDelete.Code != http.StatusNoContent {
		t.Fatalf("delete returned %d: %s", firstDelete.Code, firstDelete.Body.String())
	}
	secondDelete := doAdminRequest(t, mux, http.MethodDelete, "/api/v1/provider-capabilities/"+stored.ID, "")
	if secondDelete.Code != http.StatusNotFound {
		t.Fatalf("second delete returned %d", secondDelete.Code)
	}

	// Catalog is back to the seed.
	resp = doAdminRequest(t, mux, http.MethodGet, "/api/v1/provider-capabilities", "")
	if err := json.Unmarshal(resp.Body.Bytes(), &seed); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	if len(seed.Capabilities) != len(domain.BuiltinProviderCapabilities) {
		t.Fatalf("capabilities after delete = %d, want %d", len(seed.Capabilities), len(domain.BuiltinProviderCapabilities))
	}
}

// TestProviderCapabilityResolutionUsesRegistry proves the per-resource
// capability list is fed by the database registry, not the built-in slice:
// deleting a seeded capability removes it from resource resolution.
func TestProviderCapabilityResolutionUsesRegistry(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)

	resourceID := insertKubernetesResource(t, pool)

	before := doAdminRequest(t, mux, http.MethodGet, "/api/v1/resources/"+resourceID+"/provider-capabilities", "")
	if before.Code != http.StatusOK {
		t.Fatalf("list resource capabilities returned %d: %s", before.Code, before.Body.String())
	}
	if !strings.Contains(before.Body.String(), `"name":"kubernetes.resource.read"`) {
		t.Fatalf("kubernetes.resource.read not resolvable before delete: %s", before.Body.String())
	}

	// Disable the seeded kubernetes.resource.read capability.
	var capabilityID string
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM provider_capabilities WHERE tenant_id='platform' AND name='kubernetes.resource.read' AND version=1`).Scan(&capabilityID); err != nil {
		t.Fatalf("find capability id: %v", err)
	}
	deleted := doAdminRequest(t, mux, http.MethodDelete, "/api/v1/provider-capabilities/"+capabilityID, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete capability returned %d: %s", deleted.Code, deleted.Body.String())
	}

	after := doAdminRequest(t, mux, http.MethodGet, "/api/v1/resources/"+resourceID+"/provider-capabilities", "")
	if after.Code != http.StatusOK {
		t.Fatalf("list resource capabilities returned %d: %s", after.Code, after.Body.String())
	}
	var list []domain.ResourceCapability
	if err := json.Unmarshal(after.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode resource capabilities: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("disabled capability still resolvable: %v", list)
	}

	// The registry still answers the global catalog list (resolution for other
	// kinds is unaffected).
	if _, err := h.providerCapabilities.List(domain.WithTenantID(context.Background(), "platform")); err != nil {
		t.Fatalf("list registry: %v", err)
	}
}

func insertKubernetesResource(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var connectorID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors (name, connector_type, status, environment, version, capabilities, tenant_id)
		VALUES ('test-k8s-connector', 'kubernetes', 'ACTIVE', 'testing', '1.0.0', '["provider.request@v1","pam.change-safety@v1"]'::jsonb, 'platform')
		ON CONFLICT (name, tenant_id) DO UPDATE SET status='ACTIVE', capabilities=EXCLUDED.capabilities
		RETURNING id`).Scan(&connectorID); err != nil {
		t.Fatalf("insert connector: %v", err)
	}
	var resourceID string
	if err := pool.QueryRow(ctx, `INSERT INTO resources (resource_type, external_id, name, namespace, connector_id, environment, status, tenant_id)
		VALUES ('deployment', 'test-ns/app', 'app', 'test-ns', $1, 'testing', 'ACTIVE', 'platform')
		RETURNING id`, connectorID).Scan(&resourceID); err != nil {
		t.Fatalf("insert resource: %v", err)
	}
	return resourceID
}

type catalogResponse struct {
	Capabilities []domain.ResourceCapability `json:"capabilities"`
}
