package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

func TestProjectUserLifecycleAndTenantIsolation(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")

	var firstProjectID, secondProjectID string
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,description,tenant_id) VALUES ('lifecycle-first','test','platform') RETURNING id`).Scan(&firstProjectID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,description,tenant_id) VALUES ('lifecycle-second','test','platform') RETURNING id`).Scan(&secondProjectID); err != nil {
		t.Fatal(err)
	}
	initialHash, err := hashPassword("initial-password-123")
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata,password_hash)
		VALUES ('user','lifecycle-user','ACTIVE','platform','{"role":"project_user"}',$1) RETURNING id`, initialHash).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO project_memberships(tenant_id,project_id,principal_id,role,status,created_by)
		VALUES ('platform',$1,$3,'VIEWER','ACTIVE',$4),('platform',$2,$3,'OPERATOR','DISABLED',$4)`,
		firstProjectID, secondProjectID, userID, adminPrincipalID); err != nil {
		t.Fatal(err)
	}

	var tenantAdminID string
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata,password_hash)
		VALUES ('user','lifecycle-tenant-admin','ACTIVE','platform','{"role":"tenant_admin"}',$1) RETURNING id`, initialHash).Scan(&tenantAdminID); err != nil {
		t.Fatal(err)
	}
	const otherTenant = "project-user-other"
	if _, err := pool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ($1,$1)`, otherTenant); err != nil {
		t.Fatal(err)
	}
	var otherUserID string
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata,password_hash)
		VALUES ('user','other-lifecycle-user','ACTIVE',$1,'{"role":"project_user"}',$2) RETURNING id`, otherTenant, initialHash).Scan(&otherUserID); err != nil {
		t.Fatal(err)
	}

	list := doAdminRequest(t, mux, http.MethodGet, "/api/v1/project-users", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list project users status=%d body=%s", list.Code, list.Body.String())
	}
	var users []domain.ProjectUser
	if err := json.Unmarshal(list.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].ID != userID || len(users[0].Memberships) != 2 || users[0].Memberships[0].ProjectName != "lifecycle-first" || users[0].Memberships[1].Role != domain.ProjectRoleOperator {
		t.Fatalf("unexpected project users: %#v", users)
	}
	for _, forbiddenID := range []string{otherUserID, tenantAdminID, adminPrincipalID} {
		resp := doAdminRequest(t, mux, http.MethodGet, "/api/v1/project-users/"+forbiddenID, "")
		if resp.Code != http.StatusNotFound {
			t.Fatalf("non-project user %s status=%d body=%s", forbiddenID, resp.Code, resp.Body.String())
		}
	}

	loginCookie := loginProjectUser(t, mux, "initial-password-123")
	disable := doAdminRequest(t, mux, http.MethodPut, "/api/v1/project-users/"+userID+"/status", `{"status":"DISABLED"}`)
	if disable.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", disable.Code, disable.Body.String())
	}
	assertCookieUnauthorized(t, mux, loginCookie)
	if loginProjectUserStatus(t, mux, "initial-password-123") != http.StatusUnauthorized {
		t.Fatal("disabled project user was able to log in")
	}

	reenable := doAdminRequest(t, mux, http.MethodPut, "/api/v1/project-users/"+userID+"/status", `{"status":"ACTIVE"}`)
	if reenable.Code != http.StatusOK {
		t.Fatalf("re-enable status=%d body=%s", reenable.Code, reenable.Body.String())
	}
	loginCookie = loginProjectUser(t, mux, "initial-password-123")

	reset := doAdminRequest(t, mux, http.MethodPost, "/api/v1/project-users/"+userID+"/reset-password", `{"password":"reset-password-456"}`)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset password status=%d body=%s", reset.Code, reset.Body.String())
	}
	assertCookieUnauthorized(t, mux, loginCookie)
	if loginProjectUserStatus(t, mux, "initial-password-123") != http.StatusUnauthorized {
		t.Fatal("old password worked after reset")
	}
	loginCookie = loginProjectUser(t, mux, "reset-password-456")

	revoke := doAdminRequest(t, mux, http.MethodPost, "/api/v1/project-users/"+userID+"/revoke-sessions", "")
	if revoke.Code != http.StatusOK {
		t.Fatalf("revoke sessions status=%d body=%s", revoke.Code, revoke.Body.String())
	}
	assertCookieUnauthorized(t, mux, loginCookie)
	loginCookie = loginProjectUser(t, mux, "reset-password-456")

	wrongCurrent := doCookieRequest(t, mux, loginCookie, http.MethodPut, "/api/v1/auth/password", `{"currentPassword":"wrong-password","newPassword":"self-password-789"}`)
	if wrongCurrent.Code != http.StatusForbidden {
		t.Fatalf("wrong current password status=%d body=%s", wrongCurrent.Code, wrongCurrent.Body.String())
	}
	change := doCookieRequest(t, mux, loginCookie, http.MethodPut, "/api/v1/auth/password", `{"currentPassword":"reset-password-456","newPassword":"self-password-789"}`)
	if change.Code != http.StatusOK {
		t.Fatalf("self password change status=%d body=%s", change.Code, change.Body.String())
	}
	assertCookieUnauthorized(t, mux, loginCookie)
	if loginProjectUserStatus(t, mux, "reset-password-456") != http.StatusUnauthorized {
		t.Fatal("reset password worked after self-service change")
	}
	_ = loginProjectUser(t, mux, "self-password-789")

	unknown := doAdminRequest(t, mux, http.MethodPut, "/api/v1/project-users/"+userID+"/status", `{"status":"ACTIVE","extra":true}`)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", unknown.Code, unknown.Body.String())
	}
	shortPassword := doAdminRequest(t, mux, http.MethodPost, "/api/v1/project-users/"+userID+"/reset-password", `{"password":"short"}`)
	if shortPassword.Code != http.StatusBadRequest {
		t.Fatalf("short password status=%d body=%s", shortPassword.Code, shortPassword.Body.String())
	}
	adminMutation := doAdminRequest(t, mux, http.MethodPut, "/api/v1/project-users/"+tenantAdminID+"/status", `{"status":"DISABLED"}`)
	if adminMutation.Code != http.StatusNotFound {
		t.Fatalf("tenant admin mutation status=%d body=%s", adminMutation.Code, adminMutation.Body.String())
	}

	for _, action := range []string{"principal.status.updated", "principal.password.reset", "user.sessions.revoked", "user.password.changed"} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id='platform' AND action::text=$1 AND target_id=$2`, action, userID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatalf("missing audit action %s", action)
		}
	}
}

func loginProjectUser(t *testing.T, mux *http.ServeMux, password string) *http.Cookie {
	t.Helper()
	response := loginProjectUserResponse(t, mux, password)
	if response.Code != http.StatusOK {
		t.Fatalf("project user login status=%d body=%s", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "pam_session" {
			return cookie
		}
	}
	t.Fatal("project user login did not return pam_session cookie")
	return nil
}

func loginProjectUserStatus(t *testing.T, mux *http.ServeMux, password string) int {
	t.Helper()
	return loginProjectUserResponse(t, mux, password).Code
}

func loginProjectUserResponse(t *testing.T, mux *http.ServeMux, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"tenant": "platform", "username": "lifecycle-user", "password": password})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func doCookieRequest(t *testing.T, mux *http.ServeMux, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.AddCookie(cookie)
	request.Header.Set("Origin", "http://localhost:4173")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func assertCookieUnauthorized(t *testing.T, mux *http.ServeMux, cookie *http.Cookie) {
	t.Helper()
	response := doCookieRequest(t, mux, cookie, http.MethodGet, "/api/v1/auth/me", "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie status=%d body=%s", response.Code, response.Body.String())
	}
}
