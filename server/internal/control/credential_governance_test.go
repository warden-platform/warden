package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testAdminAPIKey = "admin-api-key--0123456789abcdef0123456789ab"

// --- Pure function tests (no database) ---

func TestMaskSecretValue(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"a", "****"},
		{"abcdefgh", "****"},
		{"abcdefghi", "ab****fghi"},
		{"super-secret-token-123456", "su****3456"},
	}
	for _, tc := range cases {
		if got := domain.MaskSecretValue(tc.in); got != tc.want {
			t.Errorf("MaskSecretValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsControlPlaneAction(t *testing.T) {
	for _, name := range []string{"credential.get", "credential.list", "credential.create", "credential.rotate", "credential.revoke", "credential.bind", "credential.unbind"} {
		if !domain.IsControlPlaneAction(name) {
			t.Errorf("%s must be a control-plane action", name)
		}
	}
	for _, name := range []string{"deployment.get", "jenkins.job.get", "provider.request", ""} {
		if domain.IsControlPlaneAction(name) {
			t.Errorf("%s must not be a control-plane action", name)
		}
	}
}

func TestValidateCredentialName(t *testing.T) {
	valid := []string{"gitlab-token", "a", "abc123", "a.b-c.d"}
	invalid := []string{"", "Token", "sp ace", "-leading", "trailing-", "a_b", strings.Repeat("x", 254)}
	for _, name := range valid {
		if !domain.ValidateCredentialName(name) {
			t.Errorf("ValidateCredentialName(%q) = false, want true", name)
		}
	}
	for _, name := range invalid {
		if domain.ValidateCredentialName(name) {
			t.Errorf("ValidateCredentialName(%q) = true, want false", name)
		}
	}
}

func TestExtractCredentialValue(t *testing.T) {
	create := map[string]any{"name": "gitlab-token", "kind": "token", "value": "plaintext-secret-xyz"}
	secret, ok := domain.ExtractCredentialValue("credential.create", create)
	if !ok || secret != "plaintext-secret-xyz" {
		t.Fatalf("ExtractCredentialValue(create) = (%q, %v)", secret, ok)
	}
	if _, exists := create["value"]; exists {
		t.Fatal("create parameters still contain the plaintext value")
	}
	if create["value_sha256"] != domain.HashValue("plaintext-secret-xyz") {
		t.Fatalf("create value_sha256 = %v", create["value_sha256"])
	}

	rotate := map[string]any{"value": "new-rotated-secret"}
	rotated, ok := domain.ExtractCredentialValue("credential.rotate", rotate)
	if !ok || rotated != "new-rotated-secret" {
		t.Fatalf("ExtractCredentialValue(rotate) = (%q, %v)", rotated, ok)
	}
	if _, exists := rotate["value"]; exists {
		t.Fatal("rotate parameters still contain the plaintext value")
	}

	get := map[string]any{"name": "gitlab-token"}
	if _, ok := domain.ExtractCredentialValue("credential.get", get); ok {
		t.Fatal("credential.get must not extract a value")
	}
	if _, exists := get["value_sha256"]; exists {
		t.Fatal("credential.get must not inject value_sha256")
	}

	nonControl := map[string]any{"value": "should-stay"}
	if _, ok := domain.ExtractCredentialValue("deployment.get", nonControl); ok {
		t.Fatal("non control-plane action must not extract a value")
	}
	if nonControl["value"] != "should-stay" {
		t.Fatal("non control-plane parameters were modified")
	}
}

func TestPendingSecretCipherRoundTrip(t *testing.T) {
	cipher, err := newCredentialCipher([]byte("test-master-key-with-at-least-32-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	secret := "plaintext-that-never-hits-parameters"
	encrypted, err := cipher.Encrypt([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(secret)) {
		t.Fatal("pending_secret ciphertext contains plaintext")
	}
	decrypted, err := cipher.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if string(decrypted) != secret {
		t.Fatalf("pending_secret round trip = %q", decrypted)
	}
}

// --- DB-gated test helpers (WARDEN_TEST_DATABASE_URL) ---

// newTestDatabasePool resets a fresh schema and applies every migration. Each
// caller gets an isolated database so tests never share credential state.
func newTestDatabasePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("WARDEN_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("WARDEN_TEST_DATABASE_URL is not set; skipping DB-gated credential governance tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping test database: %v", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire test connection: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset test schema: %v", err)
	}
	applyTestMigrations(t, conn)
	return pool
}

func applyTestMigrations(t *testing.T, conn *pgxpool.Conn) {
	t.Helper()
	ctx := context.Background()
	entries, err := os.ReadDir(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		content, err := os.ReadFile(filepath.Join("..", "..", "migrations", entry.Name()))
		if err != nil {
			t.Fatalf("read migration %s: %v", entry.Name(), err)
		}
		if _, err := conn.Exec(ctx, string(content)); err != nil {
			t.Fatalf("apply migration %s: %v", entry.Name(), err)
		}
	}
}

func newTestDatabaseHandler(t *testing.T, pool *pgxpool.Pool) (*Handler, *http.ServeMux) {
	t.Helper()
	t.Setenv("WARDEN_TASK_SIGNING_KEY", "task-signing-key-0123456789abcdef01234567")
	t.Setenv("WARDEN_JWT_SIGNING_KEY", "jwt-signing-key--0123456789abcdef012345678")
	t.Setenv("WARDEN_CREDENTIAL_ENCRYPTION_KEY", "cred-encryption-key--0123456789abcdef0123456")
	t.Setenv("WARDEN_ADMIN_API_KEY", testAdminAPIKey)
	t.Setenv("WARDEN_ADMIN_PASSWORD", "test-admin-password-123")
	t.Setenv("WARDEN_ADMIN_USERNAME", "admin")
	h, err := NewHandler(pool)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, mux
}

func doAdminRequest(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+testAdminAPIKey)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)
	return resp
}

func createExecutionForResource(t *testing.T, mux *http.ServeMux, resourceID, action string, parameters map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"resourceId":    resourceID,
		"action":        action,
		"actionVersion": 1,
		"parameters":    parameters,
		"reason":        "credential governance test",
	})
	if err != nil {
		t.Fatalf("marshal create execution: %v", err)
	}
	return doAdminRequest(t, mux, http.MethodPost, "/api/v1/executions", string(payload))
}

func approvePendingExecution(t *testing.T, mux *http.ServeMux, pool *pgxpool.Pool, executionID string) {
	t.Helper()
	var requestID string
	if err := pool.QueryRow(context.Background(), `SELECT id FROM access_requests WHERE execution_id=$1`, executionID).Scan(&requestID); err != nil {
		t.Fatalf("find access request for execution %s: %v", executionID, err)
	}
	resp := doAdminRequest(t, mux, http.MethodPost, "/api/v1/access-requests/"+requestID+"/approve", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("approve returned %d: %s", resp.Code, resp.Body.String())
	}
}

type executionResponse struct {
	ID        string         `json:"id"`
	Status    string         `json:"status"`
	ErrorCode string         `json:"errorCode"`
	Result    map[string]any `json:"result"`
}

func parseExecutionResponse(t *testing.T, resp *httptest.ResponseRecorder) executionResponse {
	t.Helper()
	var parsed executionResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode execution response: %v (body=%s)", err, resp.Body.String())
	}
	return parsed
}

func testResourceID(t *testing.T, pool *pgxpool.Pool, query string, args ...any) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&id); err != nil {
		t.Fatalf("query resource id: %v", err)
	}
	return id
}

func assertExecutionParamsRedacted(t *testing.T, pool *pgxpool.Pool, executionID, secret string) {
	t.Helper()
	var parameters []byte
	if err := pool.QueryRow(context.Background(), `SELECT parameters::text FROM executions WHERE id=$1`, executionID).Scan(&parameters); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(parameters), secret) {
		t.Fatal("executions.parameters contains the plaintext credential value")
	}
	if !strings.Contains(string(parameters), domain.HashValue(secret)) {
		t.Fatal("executions.parameters is missing value_sha256")
	}
}

func assertExecutionSucceeded(t *testing.T, pool *pgxpool.Pool, executionID string) {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM executions WHERE id=$1`, executionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(domain.ExecSucceeded) {
		t.Fatalf("execution %s status = %q, want SUCCEEDED", executionID, status)
	}
}

func assertAuditRedacted(t *testing.T, pool *pgxpool.Pool, executionID, action, secret string) {
	t.Helper()
	var metadata []byte
	if err := pool.QueryRow(context.Background(), `SELECT metadata::text FROM audit_events WHERE execution_id=$1 AND action=$2`, executionID, action).Scan(&metadata); err != nil {
		t.Fatalf("find audit event %s: %v", action, err)
	}
	if strings.Contains(string(metadata), secret) {
		t.Fatal("audit metadata contains the plaintext credential value")
	}
	if !strings.Contains(string(metadata), domain.HashValue(secret)) {
		t.Fatal("audit metadata is missing value_sha256")
	}
	if !strings.Contains(string(metadata), "value_preview") {
		t.Fatal("audit metadata is missing value_preview")
	}
}

// --- DB-gated integration tests ---

func TestCredentialGovernanceLifecycle(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	ctx := context.Background()

	secret := "super-secret-token-123456"
	vaultID := testResourceID(t, pool, `SELECT id FROM resources WHERE resource_type='credential_vault' AND external_id='pam/control-plane' LIMIT 1`)

	// 1. credential.create requires approval; the plaintext is stripped from
	//    executions.parameters and held as pending_secret until approval.
	resp := createExecutionForResource(t, mux, vaultID, "credential.create", map[string]any{
		"name": "gitlab-token", "kind": "token", "value": secret, "purpose": "jenkins builds",
	})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("create returned %d: %s", resp.Code, resp.Body.String())
	}
	createExec := parseExecutionResponse(t, resp)
	if createExec.Status != string(domain.ExecWaitingApproval) {
		t.Fatalf("create execution status = %q", createExec.Status)
	}
	assertExecutionParamsRedacted(t, pool, createExec.ID, secret)

	approvePendingExecution(t, mux, pool, createExec.ID)
	assertExecutionSucceeded(t, pool, createExec.ID)
	var pendingSecret []byte
	if err := pool.QueryRow(ctx, `SELECT pending_secret FROM executions WHERE id=$1`, createExec.ID).Scan(&pendingSecret); err != nil {
		t.Fatal(err)
	}
	if pendingSecret != nil {
		t.Fatal("pending_secret must be cleared after control-plane completion")
	}
	var version int
	var preview string
	var status string
	if err := pool.QueryRow(ctx, `SELECT version, value_preview, status FROM credentials WHERE name='gitlab-token'`).Scan(&version, &preview, &status); err != nil {
		t.Fatal(err)
	}
	if version != 1 || status != string(domain.CredentialActive) || preview != "su****3456" {
		t.Fatalf("credential row = version %d status %s preview %q", version, status, preview)
	}
	var encrypted []byte
	if err := pool.QueryRow(ctx, `SELECT encrypted_value FROM credentials WHERE name='gitlab-token'`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(secret)) {
		t.Fatal("credentials.encrypted_value contains plaintext")
	}
	credResID := testResourceID(t, pool, `SELECT id FROM resources WHERE resource_type='credential' AND external_id='pam/credential/gitlab-token' LIMIT 1`)
	assertAuditRedacted(t, pool, createExec.ID, "credential.created", secret)

	// 2. credential.get is allowed without approval and only returns masked data.
	getResp := createExecutionForResource(t, mux, credResID, "credential.get", map[string]any{"name": "gitlab-token"})
	if getResp.Code != http.StatusOK {
		t.Fatalf("get returned %d: %s", getResp.Code, getResp.Body.String())
	}
	getExec := parseExecutionResponse(t, getResp)
	if getExec.Status != string(domain.ExecSucceeded) {
		t.Fatalf("get execution status = %q", getExec.Status)
	}
	if getExec.Result["valuePreview"] != "su****3456" {
		t.Fatalf("get valuePreview = %v", getExec.Result["valuePreview"])
	}
	if _, has := getExec.Result["value"]; has {
		t.Fatal("get result contains a plaintext value key")
	}
	if strings.Contains(getResp.Body.String(), secret) {
		t.Fatal("get response leaks plaintext")
	}

	// 3. credential.bind requires approval and persists the purpose binding.
	bindResp := createExecutionForResource(t, mux, credResID, "credential.bind", map[string]any{"targetType": "integration", "targetName": "jenkins-main"})
	if bindResp.Code != http.StatusAccepted {
		t.Fatalf("bind returned %d: %s", bindResp.Code, bindResp.Body.String())
	}
	bindExec := parseExecutionResponse(t, bindResp)
	approvePendingExecution(t, mux, pool, bindExec.ID)
	var scopeJSON []byte
	if err := pool.QueryRow(ctx, `SELECT scope::text FROM credentials WHERE name='gitlab-token'`).Scan(&scopeJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(scopeJSON), "jenkins-main") || !strings.Contains(string(scopeJSON), "integration") {
		t.Fatalf("bind scope = %s", scopeJSON)
	}

	// 4. credential.rotate bumps the version and rotates the masked preview.
	newSecret := "new-secret-value-abcdef"
	rotateResp := createExecutionForResource(t, mux, credResID, "credential.rotate", map[string]any{"value": newSecret})
	if rotateResp.Code != http.StatusAccepted {
		t.Fatalf("rotate returned %d: %s", rotateResp.Code, rotateResp.Body.String())
	}
	rotateExec := parseExecutionResponse(t, rotateResp)
	assertExecutionParamsRedacted(t, pool, rotateExec.ID, newSecret)
	approvePendingExecution(t, mux, pool, rotateExec.ID)
	if err := pool.QueryRow(ctx, `SELECT version, value_preview, status FROM credentials WHERE name='gitlab-token'`).Scan(&version, &preview, &status); err != nil {
		t.Fatal(err)
	}
	if version != 2 || status != string(domain.CredentialActive) || preview != "ne****cdef" {
		t.Fatalf("rotated credential = version %d status %s preview %q", version, status, preview)
	}

	// 5. credential.revoke requires the exact confirmName.
	revokeResp := createExecutionForResource(t, mux, credResID, "credential.revoke", map[string]any{"confirmName": "gitlab-token"})
	if revokeResp.Code != http.StatusAccepted {
		t.Fatalf("revoke returned %d: %s", revokeResp.Code, revokeResp.Body.String())
	}
	revokeExec := parseExecutionResponse(t, revokeResp)
	approvePendingExecution(t, mux, pool, revokeExec.ID)
	var revokedAtSet bool
	if err := pool.QueryRow(ctx, `SELECT status, revoked_at IS NOT NULL FROM credentials WHERE name='gitlab-token'`).Scan(&status, &revokedAtSet); err != nil {
		t.Fatal(err)
	}
	if status != string(domain.CredentialRevoked) || !revokedAtSet {
		t.Fatalf("revoked credential = status %s revoked_at=%v", status, revokedAtSet)
	}

	// 6. credential.get reflects REVOKED without leaking any plaintext.
	finalResp := createExecutionForResource(t, mux, credResID, "credential.get", map[string]any{"name": "gitlab-token"})
	finalGet := parseExecutionResponse(t, finalResp)
	if finalGet.Status != string(domain.ExecSucceeded) || finalGet.Result["status"] != string(domain.CredentialRevoked) {
		t.Fatalf("get after revoke = status %q result %v", finalGet.Status, finalGet.Result)
	}
	if strings.Contains(finalResp.Body.String(), newSecret) || strings.Contains(finalResp.Body.String(), secret) {
		t.Fatal("final get response leaks plaintext")
	}
}

func TestCredentialGovernanceDeny(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	ctx := context.Background()
	vaultID := testResourceID(t, pool, `SELECT id FROM resources WHERE resource_type='credential_vault' AND external_id='pam/control-plane' LIMIT 1`)
	if _, err := pool.Exec(ctx, `INSERT INTO policy_matrix_rules (name, resource_type, action_name, environment, principal_kind, decision, tenant_id)
		VALUES ('deny-credential-create', 'credential_vault', 'credential.create', 'testing', 'user', 'DENY', 'platform')`); err != nil {
		t.Fatalf("insert deny matrix rule: %v", err)
	}
	resp := createExecutionForResource(t, mux, vaultID, "credential.create", map[string]any{
		"name": "blocked-token", "kind": "token", "value": "blocked-secret",
	})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("denied create returned %d: %s", resp.Code, resp.Body.String())
	}
	exec := parseExecutionResponse(t, resp)
	if exec.ErrorCode != "POLICY_DENIED" {
		t.Fatalf("denied create errorCode = %q", exec.ErrorCode)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM executions WHERE status='FAILED' AND error_code='POLICY_DENIED'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("FAILED POLICY_DENIED executions = %d, want 1", count)
	}
}

func TestCredentialActionTimeoutMapsToUnknown(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")
	vaultID := testResourceID(t, pool, `SELECT id FROM resources WHERE resource_type='credential_vault' AND external_id='pam/control-plane' LIMIT 1`)
	// Timeout=0 makes the action context already expired when the executor starts.
	if _, err := pool.Exec(ctx, `UPDATE action_definitions SET timeout_seconds=0 WHERE name='credential.list' AND version=1`); err != nil {
		t.Fatal(err)
	}
	resp := createExecutionForResource(t, mux, vaultID, "credential.list", map[string]any{})
	if resp.Code != http.StatusOK {
		t.Fatalf("timeout list returned %d: %s", resp.Code, resp.Body.String())
	}
	exec := parseExecutionResponse(t, resp)
	if exec.Status != string(domain.ExecUnknown) {
		t.Fatalf("timeout execution status = %q, want UNKNOWN", exec.Status)
	}
	if exec.ErrorCode != "ACTION_TIMEOUT" {
		t.Fatalf("timeout execution errorCode = %q", exec.ErrorCode)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM executions WHERE status='UNKNOWN' AND error_code='ACTION_TIMEOUT'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("UNKNOWN timeout executions = %d, want 1", count)
	}
}

func TestExecuteCredentialActionDeadline(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, _ := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")
	vaultID := testResourceID(t, pool, `SELECT id FROM resources WHERE resource_type='credential_vault' AND external_id='pam/control-plane' LIMIT 1`)
	res, err := h.resources.GetByID(ctx, vaultID)
	if err != nil || res == nil {
		t.Fatalf("load vault resource: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	action := &domain.ActionDefinition{Name: "credential.list", TimeoutSeconds: 0}
	exec := &domain.Execution{ID: "00000000-0000-0000-0000-000000000001", PrincipalID: adminPrincipalID, TraceID: "trace-timeout"}
	if _, execErr := h.executeCredentialAction(ctx, tx, exec, action, res, ""); !errors.Is(execErr, context.DeadlineExceeded) {
		t.Fatalf("executeCredentialAction error = %v, want context.DeadlineExceeded", execErr)
	}
}

func TestCredentialResolveEndpoint(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)
	ctx := context.Background()

	connectorKey := []byte("connector-task-key-0123456789abcdef0123456789")
	encryptedKey, err := h.credentialCipher.Encrypt(connectorKey)
	if err != nil {
		t.Fatal(err)
	}
	var connectorID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors (name, connector_type, status, environment, version, capabilities, task_key_ciphertext, tenant_id)
		VALUES ('resolve-connector', 'kubernetes', 'ACTIVE', 'testing', '1.0.0', '["deployment.get@v1"]'::jsonb, $1, 'platform') RETURNING id`, encryptedKey).Scan(&connectorID); err != nil {
		t.Fatalf("insert connector: %v", err)
	}

	encryptedValue, err := h.credentialCipher.Encrypt([]byte("resolve-secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO credentials (name, kind, encrypted_value, value_preview, status, version, tenant_id)
		VALUES ('gitlab-token', 'token', $1, 're****alue', 'ACTIVE', 1, 'platform')`, encryptedValue); err != nil {
		t.Fatalf("insert credential: %v", err)
	}

	nonceCounter := 0
	signedResolve := func(connectorID string, key []byte, integrationID, name string) *httptest.ResponseRecorder {
		t.Helper()
		nonceCounter++
		payload, err := json.Marshal(map[string]any{"connectorId": connectorID, "integrationId": integrationID, "name": name})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/credentials/resolve", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if err := domain.SignConnectorRequest(req, payload, key, time.Now(), "resolve-nonce-"+connectorID+"-"+string(rune('0'+nonceCounter))); err != nil {
			t.Fatal(err)
		}
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)
		return resp
	}

	// Unbound credential: any Connector holding the matching task key may resolve.
	resp := signedResolve(connectorID, connectorKey, "", "gitlab-token")
	if resp.Code != http.StatusOK {
		t.Fatalf("unbound resolve returned %d: %s", resp.Code, resp.Body.String())
	}
	var result struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != "resolve-secret-value" {
		t.Fatalf("resolved value = %q", result.Value)
	}
	var lastUsed bool
	if err := pool.QueryRow(ctx, `SELECT last_used_at IS NOT NULL FROM credentials WHERE name='gitlab-token'`).Scan(&lastUsed); err != nil {
		t.Fatal(err)
	}
	if !lastUsed {
		t.Fatal("credential resolution did not stamp last_used_at")
	}

	// After binding, a missing integrationId is rejected.
	if _, err := pool.Exec(ctx, `UPDATE credentials SET scope='{"bindings":[{"target_type":"integration","target_name":"jenkins-main"}]}'::jsonb WHERE name='gitlab-token'`); err != nil {
		t.Fatal(err)
	}
	resp = signedResolve(connectorID, connectorKey, "", "gitlab-token")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body.String(), "CREDENTIAL_BINDING_REQUIRED") {
		t.Fatalf("bound resolve without integrationId returned %d: %s", resp.Code, resp.Body.String())
	}
	// An integrationId that is not bound is rejected.
	resp = signedResolve(connectorID, connectorKey, "other-integration", "gitlab-token")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body.String(), "CREDENTIAL_NOT_BOUND") {
		t.Fatalf("bound resolve with wrong integration returned %d: %s", resp.Code, resp.Body.String())
	}

	// A real integration owned by this Connector unblocks resolution.
	var integrationID string
	if err := pool.QueryRow(ctx, `INSERT INTO integrations (connector_id, protocol, name, endpoint, username, credential_ref, scope, status, tenant_id)
		VALUES ($1, 'jenkins', 'jenkins-main', 'https://jenkins.example.test', 'admin', 'pam:gitlab-token', '{}'::jsonb, 'ACTIVE', 'platform') RETURNING id`, connectorID).Scan(&integrationID); err != nil {
		t.Fatalf("insert integration: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE credentials SET scope=jsonb_build_object('bindings', jsonb_build_array(jsonb_build_object('target_type', 'integration', 'target_name', $1::text))) WHERE name='gitlab-token'`, integrationID); err != nil {
		t.Fatal(err)
	}
	resp = signedResolve(connectorID, connectorKey, integrationID, "gitlab-token")
	if resp.Code != http.StatusOK {
		t.Fatalf("bound resolve returned %d: %s", resp.Code, resp.Body.String())
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil || result.Value != "resolve-secret-value" {
		t.Fatalf("bound resolved value = %q (err=%v)", result.Value, err)
	}

	// credential.bind stores the integration by display name; resolution must
	// tolerate a name-based binding (matches ig.Name) as well as the UUID.
	if _, err := pool.Exec(ctx, `UPDATE credentials SET scope='{"bindings":[{"target_type":"integration","target_name":"jenkins-main"}]}'::jsonb WHERE name='gitlab-token'`); err != nil {
		t.Fatal(err)
	}
	resp = signedResolve(connectorID, connectorKey, integrationID, "gitlab-token")
	if resp.Code != http.StatusOK {
		t.Fatalf("name-bound resolve returned %d: %s", resp.Code, resp.Body.String())
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil || result.Value != "resolve-secret-value" {
		t.Fatalf("name-bound resolved value = %q (err=%v)", result.Value, err)
	}

	// A different Connector that does not own the integration is rejected.
	otherKey := []byte("other-connector-task-key-0123456789abcdef0123")
	otherEncrypted, err := h.credentialCipher.Encrypt(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	var otherConnectorID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors (name, connector_type, status, environment, version, capabilities, task_key_ciphertext, tenant_id)
		VALUES ('other-connector', 'kubernetes', 'ACTIVE', 'testing', '1.0.0', '["deployment.get@v1"]'::jsonb, $1, 'platform') RETURNING id`, otherEncrypted).Scan(&otherConnectorID); err != nil {
		t.Fatalf("insert other connector: %v", err)
	}
	resp = signedResolve(otherConnectorID, otherKey, integrationID, "gitlab-token")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body.String(), "CREDENTIAL_CONNECTOR_MISMATCH") {
		t.Fatalf("cross-connector resolve returned %d: %s", resp.Code, resp.Body.String())
	}

	// Unknown credential resolves to 404.
	resp = signedResolve(connectorID, connectorKey, "", "missing-token")
	if resp.Code != http.StatusNotFound {
		t.Fatalf("missing credential resolve returned %d: %s", resp.Code, resp.Body.String())
	}
}
