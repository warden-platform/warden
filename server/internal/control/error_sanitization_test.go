package control

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// --- DB-gated integration tests (WARDEN_TEST_DATABASE_URL) ---
//
// Regression: internal failures (database, filesystem) must not leak raw
// driver errors or server paths to clients. Input-validation messages stay
// verbatim; everything else becomes a generic domain error code.

func TestCreateAPIKeyErrorMapping(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)

	var principalID string
	if err := pool.QueryRow(context.Background(), `INSERT INTO principals(kind,name,status,tenant_id,metadata)
		VALUES ('agent_instance','error-mapping-agent','ACTIVE','platform','{}') RETURNING id`).Scan(&principalID); err != nil {
		t.Fatalf("seed agent principal: %v", err)
	}
	path := "/api/v1/principals/" + principalID + "/api-keys"

	decode := func(body []byte) (string, string) {
		var parsed struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		return parsed.Error, parsed.Message
	}

	// Input error → 400 with the exact validation text.
	resp := doAdminRequest(t, mux, http.MethodPost, path, `{"scopes":["warden:nonexistent"]}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("validation status = %d, want 400, body=%s", resp.Code, resp.Body.String())
	}
	if code, message := decode(resp.Body.Bytes()); code != "CREDENTIAL_CREATE_FAILED" || message != "invalid agent scopes" {
		t.Fatalf("validation error = %s/%s", code, message)
	}

	// Internal error → 500 DB_ERROR without driver internals.
	if _, err := pool.Exec(context.Background(), `DROP TABLE principal_api_keys CASCADE`); err != nil {
		t.Fatalf("drop principal_api_keys: %v", err)
	}
	resp = doAdminRequest(t, mux, http.MethodPost, path, `{}`)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("internal status = %d, want 500, body=%s", resp.Code, resp.Body.String())
	}
	if code, _ := decode(resp.Body.Bytes()); code != "DB_ERROR" {
		t.Fatalf("internal error code = %s, want DB_ERROR", code)
	}
	if body := resp.Body.String(); strings.Contains(body, "SQLSTATE") || strings.Contains(body, "pgx") || strings.Contains(body, "principal_api_keys") {
		t.Fatalf("internal error detail leaked: %s", body)
	}
}

func TestInstallArtifactsDoNotLeakServerPaths(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	emptyRoot := t.TempDir()
	t.Setenv("WARDEN_ARTIFACT_ROOT", emptyRoot)
	for _, name := range []string{"WARDEN_RELEASE_VERSION", "WARDEN_OPERATOR_IMAGE_REPOSITORY", "WARDEN_OPERATOR_IMAGE_TAG", "WARDEN_CONNECTOR_IMAGE"} {
		t.Setenv(name, "")
	}

	resp := doAdminRequest(t, mux, http.MethodGet, "/api/v1/install-config", "")
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("install-config status = %d, want 503, body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); strings.Contains(body, emptyRoot) || strings.Contains(body, "Chart.yaml") || strings.Contains(body, "no such file") {
		t.Fatalf("install-config leaked filesystem detail: %s", body)
	}

	resp = doAdminRequest(t, mux, http.MethodGet, "/downloads/warden-operator-chart.tgz", "")
	if resp.Code != http.StatusNotFound {
		t.Fatalf("chart download status = %d, want 404, body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); strings.Contains(body, emptyRoot) || strings.Contains(body, "no such file") {
		t.Fatalf("chart download leaked filesystem detail: %s", body)
	}
}