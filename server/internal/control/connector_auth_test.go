package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

func TestConnectorRegistrationPreservesPairedTenant(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)
	ctx := context.Background()
	key := []byte("paired-connector-key-0123456789abcdef")
	encrypted, err := h.credentialCipher.Encrypt(key)
	if err != nil {
		t.Fatal(err)
	}
	var pairedID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,tenant_id,task_key_ciphertext) VALUES ('tenant-register-test','kubernetes','DRAFT','testing','platform',$1) RETURNING id`, encrypted).Scan(&pairedID); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"name":"tenant-register-test","connectorType":"kubernetes","environment":"testing","version":"1.0.0","capabilities":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/register", bytes.NewReader(body))
	if err := domain.SignConnectorRequest(req, body, key, time.Now(), "register-tenant-test"); err != nil {
		t.Fatal(err)
	}
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("register status=%d body=%s", resp.Code, resp.Body.String())
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil || result.ID != pairedID {
		t.Fatalf("registration did not reuse paired connector: id=%q want=%q err=%v", result.ID, pairedID, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM connectors WHERE name='tenant-register-test'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("registration created duplicate connector: count=%d err=%v", count, err)
	}
	heartbeat := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/"+pairedID+"/heartbeat", nil)
	if err := domain.SignConnectorRequest(heartbeat, nil, key, time.Now(), "heartbeat-tenant-test"); err != nil {
		t.Fatal(err)
	}
	heartbeatResp := httptest.NewRecorder()
	mux.ServeHTTP(heartbeatResp, heartbeat)
	if heartbeatResp.Code != http.StatusOK {
		t.Fatalf("heartbeat status=%d body=%s", heartbeatResp.Code, heartbeatResp.Body.String())
	}
}

func TestConnectorRegistrationDisambiguatesSameNameAcrossTenantsBySignature(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)
	ctx := context.Background()
	const otherTenant = "connector-registration-other"
	if _, err := pool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ($1,$1)`, otherTenant); err != nil {
		t.Fatal(err)
	}
	keyA := []byte("tenant-a-connector-key-0123456789abcdef")
	keyB := []byte("tenant-b-connector-key-0123456789abcdef")
	encryptedA, err := h.credentialCipher.Encrypt(keyA)
	if err != nil {
		t.Fatal(err)
	}
	encryptedB, err := h.credentialCipher.Encrypt(keyB)
	if err != nil {
		t.Fatal(err)
	}
	var idA, idB string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,tenant_id,task_key_ciphertext)
		VALUES ('shared-connector-name','kubernetes','DRAFT','testing','platform',$1) RETURNING id`, encryptedA).Scan(&idA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,tenant_id,task_key_ciphertext)
		VALUES ('shared-connector-name','kubernetes','DRAFT','testing',$1,$2) RETURNING id`, otherTenant, encryptedB).Scan(&idB); err != nil {
		t.Fatal(err)
	}

	register := func(key []byte, nonce string) string {
		t.Helper()
		body := []byte(`{"name":"shared-connector-name","connectorType":"kubernetes","environment":"testing","version":"1.0.0","capabilities":[]}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/register", bytes.NewReader(body))
		if err := domain.SignConnectorRequest(req, body, key, time.Now(), nonce); err != nil {
			t.Fatal(err)
		}
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("register status=%d body=%s", resp.Code, resp.Body.String())
		}
		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.ID
	}

	if got := register(keyA, "same-name-tenant-a"); got != idA {
		t.Fatalf("tenant A registration resolved id=%q want=%q", got, idA)
	}
	if got := register(keyB, "same-name-tenant-b"); got != idB {
		t.Fatalf("tenant B registration resolved id=%q want=%q", got, idB)
	}
}

func TestLegacyConnectorKeyDisabledByDefault(t *testing.T) {
	t.Setenv("WARDEN_ENABLE_LEGACY_CONNECTOR_KEY", "")
	if legacyConnectorKeyEnabled() {
		t.Fatal("legacy connector key must be disabled by default")
	}
	t.Setenv("WARDEN_ENABLE_LEGACY_CONNECTOR_KEY", "true")
	if !legacyConnectorKeyEnabled() {
		t.Fatal("explicit legacy compatibility flag was ignored")
	}
}

func TestConnectorRequestCarriesID(t *testing.T) {
	for _, path := range []string{
		"/api/v1/resources/sync",
		"/api/v2/resources/sync",
		"/api/v1/connectors/credentials/resolve",
	} {
		if !connectorRequestCarriesID(path) {
			t.Errorf("%s did not resolve connectorId from the signed body", path)
		}
	}
	if connectorRequestCarriesID("/api/v2/unknown") {
		t.Fatal("unknown route unexpectedly accepted a body connectorId")
	}
}

func TestConnectorAuthMiddlewareRejectsReplay(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	resolve := func(context.Context, *http.Request, []byte) (string, []byte, error) {
		return "connector-1", key, nil
	}
	replay := &connectorReplayCache{}
	calls := 0
	handler := ConnectorAuthMiddleware(resolve, replay, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	})

	request := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/connectors/connector-1/tasks", nil)
		if err := domain.SignConnectorRequest(req, nil, key, time.Now(), "same-nonce"); err != nil {
			t.Fatal(err)
		}
		return req
	}
	first := httptest.NewRecorder()
	handler(first, request())
	if first.Code != http.StatusNoContent || calls != 1 {
		t.Fatalf("valid Connector request returned %d, calls=%d", first.Code, calls)
	}
	second := httptest.NewRecorder()
	handler(second, request())
	if second.Code != http.StatusUnauthorized || calls != 1 {
		t.Fatalf("replayed Connector request returned %d, calls=%d", second.Code, calls)
	}
}

func TestConnectorAuthMiddlewareRejectsUnsignedRequest(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	resolve := func(context.Context, *http.Request, []byte) (string, []byte, error) {
		return "connector-1", key, nil
	}
	handler := ConnectorAuthMiddleware(resolve, &connectorReplayCache{}, func(http.ResponseWriter, *http.Request) {
		t.Fatal("unsigned request reached Connector handler")
	})
	response := httptest.NewRecorder()
	handler(response, httptest.NewRequest(http.MethodPost, "/api/v1/connectors/connector-1/heartbeat", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned Connector request returned %d", response.Code)
	}
}

func TestRegisteredConnectorRoutesAreProtected(t *testing.T) {
	h := &Handler{}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/connectors/register"},
		{http.MethodPost, "/api/v1/connectors/connector-1/heartbeat"},
		{http.MethodGet, "/api/v1/connectors/connector-1/tasks"},
		{http.MethodPost, "/api/v1/connectors/connector-1/tasks/execution-1/result"},
		{http.MethodPost, "/api/v1/resources/sync"},
		{http.MethodPost, "/api/v2/resources/sync"},
		{http.MethodGet, "/api/v1/connector-runtime/connector-1/integrations"},
		{http.MethodPost, "/api/v1/connectors/onboard"},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s returned %d without authentication", test.method, test.path, response.Code)
		}
	}
}
