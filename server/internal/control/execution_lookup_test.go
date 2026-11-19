package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- DB-gated integration tests (WARDEN_TEST_DATABASE_URL) ---
//
// Regression: GetExecution / CancelExecution must distinguish a repository
// failure (500 DB_ERROR, no internal details) from a genuinely missing row
// (404 NOT_FOUND). The previous `if err != nil || e == nil` branch masked
// database outages as "执行记录不存在".

func TestExecutionLookupSeparatesDBErrorFromNotFound(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)

	const missingID = "00000000-0000-0000-0000-000000000000"

	assertError := func(resp *httptest.ResponseRecorder, wantStatus int, wantCode string) {
		t.Helper()
		if resp.Code != wantStatus {
			t.Fatalf("status = %d, want %d, body=%s", resp.Code, wantStatus, resp.Body.String())
		}
		var body struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		if body.Error != wantCode {
			t.Fatalf("error code = %q, want %q", body.Error, wantCode)
		}
	}

	// Missing row → 404 on both endpoints.
	assertError(doAdminRequest(t, mux, http.MethodGet, "/api/v1/executions/"+missingID, ""),
		http.StatusNotFound, "NOT_FOUND")
	assertError(doAdminRequest(t, mux, http.MethodPost, "/api/v1/executions/"+missingID+"/cancel", ""),
		http.StatusNotFound, "NOT_FOUND")

	// Force a real repository failure: remove the executions table so
	// GetByID returns a pgx error instead of an empty result.
	if _, err := pool.Exec(context.Background(), `DROP TABLE executions CASCADE`); err != nil {
		t.Fatalf("drop executions table: %v", err)
	}

	for _, probe := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/executions/" + missingID},
		{http.MethodPost, "/api/v1/executions/" + missingID + "/cancel"},
	} {
		resp := doAdminRequest(t, mux, probe.method, probe.path, "")
		assertError(resp, http.StatusInternalServerError, "DB_ERROR")
		// The raw driver error (SQL state, table names) must not leak.
		if body := resp.Body.String(); strings.Contains(body, "SQLSTATE") || strings.Contains(body, "pgx") || strings.Contains(body, "executions") {
			t.Fatalf("internal error detail leaked to client: %s", body)
		}
	}
}