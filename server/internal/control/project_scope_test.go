package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

func TestPrincipalProjectScope(t *testing.T) {
	tests := []struct {
		name       string
		identity   *AuthenticatedIdentity
		scoped     bool
		projects   int
		shouldFail bool
	}{
		{name: "missing scope remains compatible", identity: &AuthenticatedIdentity{Principal: &domain.Principal{Metadata: map[string]any{}}}},
		{name: "explicit empty denies all", identity: &AuthenticatedIdentity{Principal: &domain.Principal{Metadata: map[string]any{principalProjectIDsMetadataKey: []any{}}}}, scoped: true},
		{name: "project list", identity: &AuthenticatedIdentity{Principal: &domain.Principal{Metadata: map[string]any{principalProjectIDsMetadataKey: []any{"project-a", "project-b"}}}}, scoped: true, projects: 2},
		{name: "admin bypass", identity: &AuthenticatedIdentity{Principal: &domain.Principal{Metadata: map[string]any{principalProjectIDsMetadataKey: []any{"project-a"}}}, Scopes: []string{"warden:admin"}}},
		{name: "invalid shape", identity: &AuthenticatedIdentity{Principal: &domain.Principal{Metadata: map[string]any{principalProjectIDsMetadataKey: "project-a"}}}, scoped: true, shouldFail: true},
		{name: "invalid element", identity: &AuthenticatedIdentity{Principal: &domain.Principal{Metadata: map[string]any{principalProjectIDsMetadataKey: []any{42}}}}, scoped: true, shouldFail: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projects, scoped, err := principalProjectScope(test.identity)
			if (err != nil) != test.shouldFail {
				t.Fatalf("principalProjectScope() error = %v, want failure %v", err, test.shouldFail)
			}
			if scoped != test.scoped {
				t.Fatalf("principalProjectScope() scoped = %v, want %v", scoped, test.scoped)
			}
			if len(projects) != test.projects {
				t.Fatalf("principalProjectScope() project count = %d, want %d", len(projects), test.projects)
			}
		})
	}
}

func TestConsumerProjectCanRunAction(t *testing.T) {
	for _, action := range []string{"jenkins.job.get", "jenkins.job.build"} {
		if !consumerProjectCanRunAction(action) {
			t.Fatalf("expected consumer project action %q to be allowed", action)
		}
	}
	for _, action := range []string{"jenkins.job.configure", "jenkins.job.disable", "jenkins.job.delete", "provider.write"} {
		if consumerProjectCanRunAction(action) {
			t.Fatalf("expected governance action %q to be denied for a consumer project", action)
		}
	}
}

func TestResourceStatsForResources(t *testing.T) {
	stats := resourceStatsForResources([]domain.Resource{
		{ResourceType: "jenkins_job", Status: domain.ResourceActive},
		{ResourceType: "deployment", Status: domain.ResourceDisabled},
		{ResourceType: "deployment", Status: domain.ResourceOrphaned},
	})
	if stats["total"] != 3 || stats["current"] != 2 || stats["active"] != 1 || stats["abnormal"] != 1 || stats["history"] != 1 {
		t.Fatalf("unexpected resource stats: %#v", stats)
	}
}

func TestFilterProjectsForIdentity(t *testing.T) {
	identity := &AuthenticatedIdentity{Principal: &domain.Principal{Metadata: map[string]any{principalProjectIDsMetadataKey: []any{"project-b"}}}}
	projects, err := filterProjectsForIdentity(identity, []domain.Project{{ID: "project-a"}, {ID: "project-b"}})
	if err != nil || len(projects) != 1 || projects[0].ID != "project-b" {
		t.Fatalf("unexpected project scope result: %#v (%v)", projects, err)
	}
}

func TestIdentityResourceProjectAccessUsesRequestedProject(t *testing.T) {
	h := &Handler{}
	identity := &AuthenticatedIdentity{Principal: &domain.Principal{Metadata: map[string]any{}}}

	access, err := h.identityResourceProjectAccess(t.Context(), identity, &domain.Resource{ID: "resource-a", ProjectID: "project-a", ResourceType: "deployment"}, "project-a")
	if err != nil || access != resourceProjectAccessPrimary {
		t.Fatalf("matching project access = %v, %v; want primary", access, err)
	}

	access, err = h.identityResourceProjectAccess(t.Context(), identity, &domain.Resource{ID: "resource-b", ProjectID: "project-b", ResourceType: "deployment"}, "project-a")
	if err != nil || access != resourceProjectAccessNone {
		t.Fatalf("cross-project access = %v, %v; want none", access, err)
	}

	scopedIdentity := &AuthenticatedIdentity{Principal: &domain.Principal{Metadata: map[string]any{principalProjectIDsMetadataKey: []any{"project-b"}}}}
	access, err = h.identityResourceProjectAccess(t.Context(), scopedIdentity, &domain.Resource{
		ID: "resource-shared", ProjectID: "project-a", ProjectIDs: []string{"project-a", "project-b"}, ResourceType: "deployment",
	}, "")
	if err != nil || access != resourceProjectAccessPrimary {
		t.Fatalf("shared project access = %v, %v; want primary", access, err)
	}

	emptyScope := &AuthenticatedIdentity{Principal: &domain.Principal{Metadata: map[string]any{principalProjectIDsMetadataKey: []any{}}}}
	access, err = h.identityResourceProjectAccess(t.Context(), emptyScope, &domain.Resource{
		ID: "resource-a", ProjectID: "project-a", ProjectIDs: []string{"project-a"}, ResourceType: "deployment",
	}, "")
	if err != nil || access != resourceProjectAccessNone {
		t.Fatalf("empty principal scope access = %v, %v; want none", access, err)
	}
}

func TestIdentityResourceProjectAccessClassifiesJenkinsConsumer(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, _ := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")

	var primaryProjectID, consumerProjectID, connectorID, resourceID string
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,tenant_id) VALUES ('jenkins-primary','platform') RETURNING id`).Scan(&primaryProjectID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,tenant_id) VALUES ('jenkins-consumer','platform') RETURNING id`).Scan(&consumerProjectID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,tenant_id,project_id,last_heartbeat)
		VALUES ('jenkins-scope-connector','jenkins','ACTIVE','testing','platform',$1,now()) RETURNING id`, primaryProjectID).Scan(&connectorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,connector_id,status,tenant_id)
		VALUES ('jenkins_job','job/scope-test','scope-test',$1,'ACTIVE','platform') RETURNING id`, connectorID).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	if err := h.projects.ReplaceResourceConsumers(ctx, resourceID, []string{consumerProjectID}); err != nil {
		t.Fatal(err)
	}
	resource, err := h.resources.GetByID(ctx, resourceID)
	if err != nil || resource == nil {
		t.Fatalf("load Jenkins resource: %#v, %v", resource, err)
	}
	identity := &AuthenticatedIdentity{Principal: &domain.Principal{Metadata: map[string]any{principalProjectIDsMetadataKey: []any{consumerProjectID}}}}
	access, err := h.identityResourceProjectAccess(ctx, identity, resource, consumerProjectID)
	if err != nil || access != resourceProjectAccessConsumer {
		t.Fatalf("Jenkins consumer access = %v, %v; want consumer", access, err)
	}
	if !consumerProjectCanRunAction("jenkins.job.build") || consumerProjectCanRunAction("jenkins.job.configure") {
		t.Fatal("Jenkins consumer action boundary is incorrect")
	}
}

func TestListResourcesFiltersRequestedProjectForAdmin(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)
	ctx := context.Background()

	var projectA, projectB string
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,tenant_id) VALUES ('scope-project-a','platform') RETURNING id`).Scan(&projectA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,tenant_id) VALUES ('scope-project-b','platform') RETURNING id`).Scan(&projectB); err != nil {
		t.Fatal(err)
	}

	var connectorA, connectorB string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,tenant_id,project_id,last_heartbeat)
		VALUES ('scope-connector-a','kubernetes','ACTIVE','testing','platform',$1,now()) RETURNING id`, projectA).Scan(&connectorA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,tenant_id,project_id,last_heartbeat)
		VALUES ('scope-connector-b','kubernetes','ACTIVE','testing','platform',$1,now()) RETURNING id`, projectB).Scan(&connectorB); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO resources(resource_type,external_id,name,connector_id,status,tenant_id)
		VALUES ('deployment','scope-resource-a','scope-resource-a',$1,'ACTIVE','platform'),
		       ('deployment','scope-resource-b','scope-resource-b',$2,'ACTIVE','platform')`, connectorA, connectorB); err != nil {
		t.Fatal(err)
	}

	response := doAdminRequest(t, mux, http.MethodGet, "/api/v1/resources?availableForExecution=true&projectId="+projectA, "")
	if response.Code != http.StatusOK {
		t.Fatalf("list resources status=%d body=%s", response.Code, response.Body.String())
	}
	var resources []domain.Resource
	if err := json.Unmarshal(response.Body.Bytes(), &resources); err != nil {
		t.Fatalf("decode resources: %v", err)
	}
	if len(resources) != 1 || resources[0].Name != "scope-resource-a" || resources[0].ProjectID != projectA {
		t.Fatalf("project filtered resources = %#v, want only scope-resource-a", resources)
	}

	identity := &AuthenticatedIdentity{
		Principal: &domain.Principal{TenantID: "platform", Metadata: map[string]any{principalProjectIDsMetadataKey: []any{projectB}}},
		TenantID:  "platform",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/resources?availableForExecution=true", nil)
	requestContext := domain.WithTenantID(req.Context(), "platform")
	requestContext = context.WithValue(requestContext, identityContextKey{}, identity)
	req = req.WithContext(requestContext)
	response = httptest.NewRecorder()
	h.ListResources(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("scoped resource list status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &resources); err != nil {
		t.Fatalf("decode scoped resources: %v", err)
	}
	if len(resources) != 1 || resources[0].Name != "scope-resource-b" {
		t.Fatalf("principal scoped resources = %#v, want only scope-resource-b", resources)
	}
}
