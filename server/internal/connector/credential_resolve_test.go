package connector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

func TestFetchCredentialValueSignsAndParses(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	sawRequest := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/connectors/credentials/resolve" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := domain.VerifyConnectorRequest(r, body, key, time.Now(), 2*time.Minute); err != nil {
			t.Fatalf("control plane rejected Connector signature: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["connectorId"] != "connector-a" || payload["name"] != "gitlab-token" || payload["integrationId"] != "integration-1" {
			t.Fatalf("unexpected resolve payload: %v", payload)
		}
		sawRequest = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":"the-plaintext-secret"}`))
	}))
	defer server.Close()

	runtime := NewTypedRuntime(server.URL, "connector-a", "testing", "1.0.0", "kubernetes", key, nil, nil)
	runtime.connectorID = "connector-a"
	value, err := runtime.FetchCredentialValue(context.Background(), "gitlab-token", "integration-1")
	if err != nil {
		t.Fatalf("FetchCredentialValue: %v", err)
	}
	if !sawRequest {
		t.Fatal("resolve request was not sent")
	}
	if value != "the-plaintext-secret" {
		t.Fatalf("resolved value = %q", value)
	}
}

func TestFetchCredentialValuePropagatesControlPlaneError(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"CREDENTIAL_NOT_BOUND","message":"凭据未绑定到该集成"}`))
	}))
	defer server.Close()

	runtime := NewTypedRuntime(server.URL, "connector-a", "testing", "1.0.0", "kubernetes", key, nil, nil)
	runtime.connectorID = "connector-a"
	_, err := runtime.FetchCredentialValue(context.Background(), "gitlab-token", "")
	if err == nil {
		t.Fatal("expected error from control plane")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "gitlab-token") {
		t.Fatalf("error = %v", err)
	}
}

func TestFetchCredentialValueRejectsEmptyValue(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":""}`))
	}))
	defer server.Close()

	runtime := NewTypedRuntime(server.URL, "connector-a", "testing", "1.0.0", "kubernetes", key, nil, nil)
	runtime.connectorID = "connector-a"
	_, err := runtime.FetchCredentialValue(context.Background(), "gitlab-token", "")
	if err == nil || !strings.Contains(err.Error(), "empty credential value") {
		t.Fatalf("error = %v", err)
	}
}
