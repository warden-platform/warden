package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"gitee.com/pam-platform/warden/server/internal/storage/postgres"
)

func TestValidateProjectMemberInput(t *testing.T) {
	if !validateProjectMemberInput(projectMemberInput{PrincipalID: "p", Role: domain.ProjectRoleOperator}, true) {
		t.Fatal("valid operator rejected")
	}
	if validateProjectMemberInput(projectMemberInput{PrincipalID: "", Role: domain.ProjectRoleOperator}, true) {
		t.Fatal("missing principal accepted")
	}
	if validateProjectMemberInput(projectMemberInput{PrincipalID: "p", Role: "OWNER"}, true) {
		t.Fatal("unknown role accepted")
	}
	if validateProjectMemberInput(projectMemberInput{PrincipalID: "p", Role: domain.ProjectRoleViewer, Status: "DELETED"}, true) {
		t.Fatal("unknown status accepted")
	}
}

func TestProjectMembershipRolesAndTenantIsolation(t *testing.T) {
	pool := newTestDatabasePool(t)
	ctx := domain.WithTenantID(context.Background(), "platform")
	repo := postgres.NewProjectMembershipRepo(pool)
	projects := postgres.NewProjectRepo(pool)
	var projectID, secondProjectID, principalID string
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,description,tenant_id) VALUES ('membership-project','test','platform') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,description,tenant_id) VALUES ('membership-second','test','platform') RETURNING id`).Scan(&secondProjectID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata) VALUES ('user','membership-user','ACTIVE','platform','{}') RETURNING id`).Scan(&principalID); err != nil {
		t.Fatal(err)
	}
	member, err := repo.Upsert(ctx, projectID, principalID, domain.ProjectRoleViewer, domain.ProjectMembershipActive, adminPrincipalID)
	if err != nil || member.Role != domain.ProjectRoleViewer {
		t.Fatalf("create membership=%#v err=%v", member, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tenants SET settings=jsonb_set(settings,'{projectMembershipEnforcement}','true'::jsonb,true) WHERE slug='platform'`); err != nil {
		t.Fatal(err)
	}
	h := &Handler{pool: pool, projects: projects, resources: postgres.NewResourceRepo(pool), projectMemberships: repo}
	identity := &AuthenticatedIdentity{Principal: &domain.Principal{ID: principalID, TenantID: "platform"}, TenantID: "platform"}
	memberCtx := context.WithValue(ctx, identityContextKey{}, identity)
	if err := h.projectAccess(memberCtx, projectID, false); err != nil {
		t.Fatalf("viewer read denied: %v", err)
	}
	filtered, err := h.filterResourcesByProjectMembership(memberCtx, []domain.Resource{{ID: "resource", ProjectScopeMode: domain.ProjectScopeSelected, ProjectID: secondProjectID, ProjectName: "membership-second", ProjectIDs: []string{projectID, secondProjectID}, ProjectNames: []string{"membership-project", "membership-second"}}})
	if err != nil || len(filtered) != 1 || len(filtered[0].ProjectIDs) != 1 || filtered[0].ProjectIDs[0] != projectID || filtered[0].ProjectID != projectID {
		t.Fatalf("filtered resources=%#v err=%v", filtered, err)
	}
	var accessErr *projectMembershipAccessError
	if err := h.projectAccess(memberCtx, projectID, true); !errors.As(err, &accessErr) || accessErr.Code != "PROJECT_ROLE_DENIED" {
		t.Fatalf("viewer execute error=%v", err)
	}
	if _, err := repo.Upsert(ctx, projectID, principalID, domain.ProjectRoleOperator, domain.ProjectMembershipActive, adminPrincipalID); err != nil {
		t.Fatal(err)
	}
	if err := h.projectAccess(memberCtx, projectID, true); err != nil {
		t.Fatalf("operator execute denied: %v", err)
	}
	if ok, err := repo.Disable(ctx, projectID, principalID); err != nil || !ok {
		t.Fatalf("disable ok=%v err=%v", ok, err)
	}
	if err := h.projectAccess(memberCtx, projectID, false); !errors.As(err, &accessErr) || accessErr.Code != "PROJECT_ACCESS_DENIED" {
		t.Fatalf("disabled membership error=%v", err)
	}

	const otherTenant = "membership-other"
	if _, err := pool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ($1,$1)`, otherTenant); err != nil {
		t.Fatal(err)
	}
	var otherPrincipalID string
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata) VALUES ('user','other-member','ACTIVE',$1,'{}') RETURNING id`, otherTenant).Scan(&otherPrincipalID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Upsert(ctx, projectID, otherPrincipalID, domain.ProjectRoleOperator, domain.ProjectMembershipActive, adminPrincipalID); !errors.Is(err, postgres.ErrInvalidProjectMembership) {
		t.Fatalf("cross-tenant membership error=%v", err)
	}
}

func TestCreateProjectUserHTTPBindsMembership(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")
	var projectID string
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,description,tenant_id) VALUES ('http-user-project','test','platform') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"name": "http-project-viewer", "password": "viewer-password-123", "role": "VIEWER"})
	if err != nil {
		t.Fatal(err)
	}
	resp := doAdminRequest(t, mux, http.MethodPost, "/api/v1/projects/"+projectID+"/users", string(body))
	if resp.Code != http.StatusCreated {
		t.Fatalf("create project user status=%d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		User       domain.Principal         `json:"user"`
		Membership domain.ProjectMembership `json:"membership"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.User.Name != "http-project-viewer" || payload.User.Kind != domain.PrincipalKindUser || payload.Membership.Role != domain.ProjectRoleViewer || payload.Membership.ProjectID != projectID {
		t.Fatalf("unexpected create response: %#v", payload)
	}
	if payload.User.PasswordHash != "" {
		t.Fatal("create response exposed password hash")
	}
	var passwordHash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM principals WHERE id=$1 AND tenant_id='platform'`, payload.User.ID).Scan(&passwordHash); err != nil {
		t.Fatal(err)
	}
	if !verifyPassword("viewer-password-123", passwordHash) {
		t.Fatal("stored project user password does not verify")
	}
	var role string
	if err := pool.QueryRow(ctx, `SELECT role FROM project_memberships WHERE project_id=$1 AND principal_id=$2`, projectID, payload.User.ID).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != string(domain.ProjectRoleViewer) {
		t.Fatalf("stored membership role=%q", role)
	}
	loginBody, err := json.Marshal(map[string]string{"tenant": "platform", "username": "http-project-viewer", "password": "viewer-password-123"})
	if err != nil {
		t.Fatal(err)
	}
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	mux.ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("project user login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
}
