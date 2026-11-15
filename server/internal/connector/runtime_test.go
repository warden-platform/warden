package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

type resourceSyncTestRunner struct {
	resources []DiscoveredResource
	complete  bool
}

func (r *resourceSyncTestRunner) ListResources(context.Context) []DiscoveredResource {
	return r.resources
}

func (r *resourceSyncTestRunner) ListResourcesComplete(context.Context) ([]DiscoveredResource, bool) {
	return r.resources, r.complete
}

func (r *resourceSyncTestRunner) Execute(context.Context, domain.TaskEnvelope) (map[string]any, Lease, error) {
	return nil, Lease{}, nil
}

func TestResourceSyncIntervalIsStableAndBounded(t *testing.T) {
	base := 5 * time.Minute
	connectorID := "7ac337a3-c7a5-4d41-bf2c-3ceeb268f45f"

	got := resourceSyncInterval(base, connectorID)
	if got < base || got > base+maxResourceSyncJitter {
		t.Fatalf("resource sync interval = %s, want between %s and %s", got, base, base+maxResourceSyncJitter)
	}
	if repeated := resourceSyncInterval(base, connectorID); repeated != got {
		t.Fatalf("resource sync interval changed for the same connector: first=%s repeated=%s", got, repeated)
	}
}

func TestResourceSyncIntervalSpreadsConnectors(t *testing.T) {
	base := 5 * time.Minute
	first := resourceSyncInterval(base, "connector-a")
	second := resourceSyncInterval(base, "connector-b")
	if first == second {
		t.Fatalf("distinct connectors received the same test interval: %s", first)
	}
}

func TestGroupedResourceSyncUsesV2(t *testing.T) {
	var v1Calls, v2Calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v2/resources/sync":
			v2Calls++
			var payload struct {
				Groups []DiscoveryGroup `json:"groups"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Groups) != 1 || len(payload.Groups[0].Resources) != 1 {
				t.Fatalf("unexpected grouped payload: %#v", payload)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/resources/sync":
			v1Calls++
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	runner := &resourceSyncTestRunner{complete: true, resources: []DiscoveredResource{{ExternalID: "table/public/orders", Name: "orders", ResourceType: "database_table"}}}
	runtime := NewTypedRuntime(server.URL, "test", "test", "test", "test", []byte("01234567890123456789012345678901"), nil, NewMultiRunner(runner, "database_table"))
	runtime.connectorID = "connector-1"
	if err := runtime.syncResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v2Calls != 1 || v1Calls != 0 {
		t.Fatalf("unexpected sync calls: v2=%d v1=%d", v2Calls, v1Calls)
	}
}

func TestGroupedResourceSyncFallsBackToCompleteV1Snapshot(t *testing.T) {
	var legacyResources []DiscoveredResource
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v2/resources/sync" {
			http.NotFound(w, request)
			return
		}
		var payload struct {
			Resources []DiscoveredResource `json:"resources"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		legacyResources = payload.Resources
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	runner := &resourceSyncTestRunner{complete: true, resources: []DiscoveredResource{{ExternalID: "table/public/orders", Name: "orders", ResourceType: "database_table"}}}
	runtime := NewTypedRuntime(server.URL, "test", "test", "test", "test", []byte("01234567890123456789012345678901"), nil, NewMultiRunner(runner, "database_table"))
	runtime.connectorID = "connector-1"
	if err := runtime.syncResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(legacyResources) != 1 {
		t.Fatalf("legacy fallback sent %d resources, want 1", len(legacyResources))
	}
}

func TestGroupedResourceSyncRefusesIncompleteV1Snapshot(t *testing.T) {
	var v1Calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v2/resources/sync" {
			http.NotFound(w, request)
			return
		}
		v1Calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	runner := &resourceSyncTestRunner{complete: false}
	runtime := NewTypedRuntime(server.URL, "test", "test", "test", "test", []byte("01234567890123456789012345678901"), nil, NewMultiRunner(runner, "database_table"))
	runtime.connectorID = "connector-1"
	err := runtime.syncResources(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing incomplete legacy snapshot") {
		t.Fatalf("unexpected error: %v", err)
	}
	if v1Calls != 0 {
		t.Fatalf("incomplete discovery reached V1 sync %d times", v1Calls)
	}
}
