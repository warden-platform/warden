package control

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGzipMiddlewareCompressesAPIResponses(t *testing.T) {
	handler := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":["one","two"]}`))
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources", nil)
	request.Header.Set("Accept-Encoding", "br, gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if got := response.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q", got)
	}
	if !strings.Contains(response.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Vary = %q", response.Header().Get("Vary"))
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != `{"items":["one","two"]}` {
		t.Fatalf("decompressed body = %q", got)
	}
}

func TestGzipMiddlewareLeavesUnsupportedResponsesUncompressed(t *testing.T) {
	handler := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("plain"))
	}))
	for _, test := range []struct {
		path, encoding string
	}{{"/api/v1/resources", "br"}, {"/healthz", "gzip"}} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Header.Set("Accept-Encoding", test.encoding)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if got := response.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("%s Content-Encoding = %q", test.path, got)
		}
		if got := response.Body.String(); got != "plain" {
			t.Fatalf("%s body = %q", test.path, got)
		}
	}
}

func TestGzipMiddlewareSkipsNoContentAndDisabledEncoding(t *testing.T) {
	handler := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/no-content" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	for _, test := range []struct {
		path, encoding string
	}{{"/api/v1/no-content", "gzip"}, {"/api/v1/resources", "gzip;q=0, br"}} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Header.Set("Accept-Encoding", test.encoding)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if got := response.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("%s Content-Encoding = %q", test.path, got)
		}
	}
}

func TestAPIKeyAuthProtectsConnectorAdministration(t *testing.T) {
	handler := APIKeyAuthMiddleware("agent-key", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/connector-id/integrations", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated Connector administration returned %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/connectors/connector-id/integrations", nil)
	request.Header.Set("Authorization", "Bearer agent-key")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("authenticated Connector administration returned %d", response.Code)
	}
}

func TestConfiguredCORSOriginRejectsNonOriginValues(t *testing.T) {
	for _, value := range []string{"*", "https://user@example.test", "https://example.test/path", "javascript:alert(1)"} {
		t.Setenv("WARDEN_CORS_ORIGIN", value)
		if _, err := configuredCORSOrigin(); err == nil {
			t.Fatalf("invalid CORS origin accepted: %q", value)
		}
	}
	t.Setenv("WARDEN_CORS_ORIGIN", "https://warden.example.test/")
	if got, err := configuredCORSOrigin(); err != nil || got != "https://warden.example.test" {
		t.Fatalf("valid CORS origin = %q, %v", got, err)
	}
}
