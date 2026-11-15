package connector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestRuntimeSignsRegistrationRequest(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	runtime := NewTypedRuntime("https://control.example.test", "connector-a", "testing", "1.0.0", "linux", key, nil, nil)
	runtime.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := domain.VerifyConnectorRequest(req, body, key, time.Now(), 2*time.Minute); err != nil {
			t.Fatalf("runtime sent an invalid Connector signature: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil || payload["name"] != "connector-a" {
			t.Fatalf("registration body changed while signing: %s, err=%v", body, err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"id":"connector-id"}`)),
			Request:    req,
		}, nil
	})
	if err := runtime.register(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.connectorID != "connector-id" {
		t.Fatalf("connector id = %q", runtime.connectorID)
	}
}
