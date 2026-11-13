package connector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

func TestGrafanaRunnerDiscoversScopedResourcesWithoutTokenLeak(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer grafana-token" {
			t.Fatal("Grafana request did not use bearer auth")
		}
		switch r.URL.Path {
		case "/api/folders":
			writeTestJSON(w, []any{map[string]any{"uid": "folder-1", "title": "Folder 1", "id": 1}})
		case "/api/search":
			writeTestJSON(w, []any{
				map[string]any{"uid": "allowed-dash", "title": "Allowed", "uri": "db/allowed", "folderTitle": "Folder 1", "folderUid": "folder-1"},
				map[string]any{"uid": "private-dash", "title": "Private", "uri": "db/private", "folderTitle": "", "folderUid": ""},
			})
		case "/api/datasources":
			writeTestJSON(w, []any{map[string]any{"uid": "prometheus", "name": "Prometheus", "type": "prometheus"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewGrafanaRunner(GrafanaConfig{BaseURL: server.URL, APIToken: "grafana-token", AllowedDashboards: []string{"allowed-dash"}})
	if err != nil {
		t.Fatal(err)
	}
	resources := runner.ListResources(context.Background())
	encoded, err := json.Marshal(resources)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{"grafana_instance", "folder/folder-1", "dashboard/allowed-dash", "datasource/prometheus"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("discovery result is missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "private-dash") || strings.Contains(text, "grafana-token") {
		t.Fatalf("discovery exposed an out-of-scope dashboard or API token: %s", text)
	}
}

func TestGrafanaRunnerRejectsOutOfScopeDashboardBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	runner, err := NewGrafanaRunner(GrafanaConfig{BaseURL: server.URL, APIToken: "token", AllowedDashboards: []string{"allowed-dash"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "grafana_dashboard", ExternalID: "dashboard/private-dash", Name: "Private"},
		Action:   domain.TaskAction{Name: "grafana.dashboard.get"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err == nil {
		t.Fatal("out-of-scope Grafana dashboard was accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("Grafana was contacted before scope validation")
	}
}

func TestGrafanaRunnerEmptyScopeAllowsAllDashboards(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/search":
			writeTestJSON(w, []any{
				map[string]any{"uid": "dash-a", "title": "Dashboard A", "uri": "db/a"},
				map[string]any{"uid": "dash-b", "title": "Dashboard B", "uri": "db/b"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// No AllowedDashboards: the runner should materialize every discovered
	// dashboard and accept execution against any of them (empty scope = allow all).
	runner, err := NewGrafanaRunner(GrafanaConfig{BaseURL: server.URL, APIToken: "token", AllowAll: true})
	if err != nil {
		t.Fatal(err)
	}
	resources := runner.ListResources(context.Background())
	encoded, _ := json.Marshal(resources)
	text := string(encoded)
	for _, expected := range []string{"dashboard/dash-a", "dashboard/dash-b"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("empty scope did not allow all dashboards: missing %q in %s", expected, text)
		}
	}

	// Execution against any dashboard must also be accepted when scope is empty.
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "grafana_dashboard", ExternalID: "dashboard/dash-a", Name: "Dashboard A"},
		Action:   domain.TaskAction{Name: "grafana.dashboard.get"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if err := runner.validateResource(task.Resource); err != nil {
		t.Fatalf("empty scope rejected an in-scope dashboard: %v", err)
	}
}

func TestGrafanaRunnerCreatesDashboard(t *testing.T) {
	var posted string
	var method string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer grafana-token" {
			t.Fatal("Grafana create did not use bearer auth")
		}
		if r.URL.Path == "/api/dashboards/db" && r.Method == http.MethodPost {
			method = r.Method
			data, _ := io.ReadAll(r.Body)
			posted = string(data)
			writeTestJSON(w, map[string]any{"id": 42, "uid": "new-dash", "url": "/d/new-dash/new-dash", "status": "success", "version": 1})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	runner, err := NewGrafanaRunner(GrafanaConfig{BaseURL: server.URL, APIToken: "grafana-token"})
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(server.URL, "http://")
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "grafana_instance", ExternalID: "instance/" + host, Name: host},
		Action:   domain.TaskAction{Name: "grafana.dashboard.create"},
		Parameters: map[string]any{
			"dashboard": map[string]any{"title": "New Dash", "panels": []any{}},
			"folderUid": "folder-1",
			"message":   "initial create",
		},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost {
		t.Fatalf("expected POST to /api/dashboards/db, got %s", method)
	}
	if !strings.Contains(posted, `"title":"New Dash"`) || !strings.Contains(posted, `"folderUid":"folder-1"`) || !strings.Contains(posted, `"message":"initial create"`) {
		t.Fatalf("create body missing expected fields: %s", posted)
	}
	if result["uid"] != "new-dash" {
		t.Fatalf("unexpected create result: %#v", result)
	}
	if strings.Contains(posted, "grafana-token") {
		t.Fatalf("API token leaked into request body: %s", posted)
	}
}

func TestGrafanaRunnerCreatesDatasourceWithoutSecretFields(t *testing.T) {
	var posted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/datasources" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		data, _ := io.ReadAll(r.Body)
		posted = string(data)
		writeTestJSON(w, map[string]any{"datasource": map[string]any{"uid": "prom-main"}, "message": "Datasource added"})
	}))
	defer server.Close()
	runner, err := NewGrafanaRunner(GrafanaConfig{BaseURL: server.URL, APIToken: "token", AllowAll: true})
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(server.URL, "http://")
	task := domain.TaskEnvelope{Resource: domain.TaskResource{Type: "grafana_instance", ExternalID: "instance/" + host, Name: host}, Action: domain.TaskAction{Name: "grafana.datasource.create"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)}, Parameters: map[string]any{"datasourceName": "Prometheus", "datasourceUid": "prom-main", "type": "prometheus", "url": "https://prometheus.example.com", "access": "proxy"}}
	if _, _, err := runner.Execute(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(posted, `"uid":"prom-main"`) || strings.Contains(strings.ToLower(posted), "securejsondata") {
		t.Fatalf("unexpected datasource payload: %s", posted)
	}
}

func TestGrafanaRunnerExportDashboardStripsMetaEnvelope(t *testing.T) {
	// Grafana has no /api/dashboards/uid/{uid}/export endpoint (returns 404).
	// The exportable dashboard model is the "dashboard" field of the response
	// from GET /api/dashboards/uid/{uid}. exportDashboard must call that
	// endpoint and strip the "meta" wrapper.
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		switch requestedPath {
		case "/api/dashboards/uid/abc123":
			writeTestJSON(w, map[string]any{
				"meta":      map[string]any{"version": 5, "folderTitle": "General"},
				"dashboard": map[string]any{"uid": "abc123", "title": "Overview", "panels": []any{map[string]any{"id": 1}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewGrafanaRunner(GrafanaConfig{BaseURL: server.URL, APIToken: "token", AllowedDashboards: []string{"abc123"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "grafana_dashboard", ExternalID: "dashboard/abc123", Name: "Overview"},
		Action:   domain.TaskAction{Name: "grafana.dashboard.export"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("dashboard export failed: %v (path=%s)", err, requestedPath)
	}
	if requestedPath != "/api/dashboards/uid/abc123" {
		t.Fatalf("expected export to call /api/dashboards/uid/abc123, got %s", requestedPath)
	}
	dashboard, ok := result["dashboard"].(map[string]any)
	if !ok || dashboard["uid"] != "abc123" || dashboard["title"] != "Overview" {
		t.Fatalf("export did not return the stripped dashboard model: %#v", result)
	}
	if _, leaked := result["meta"]; leaked {
		t.Fatalf("export leaked the meta envelope: %#v", result)
	}
}

func TestGrafanaRunnerUpdatesFolderWithPinnedUID(t *testing.T) {
	var requestedPath, posted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		posted = string(data)
		if r.Method != http.MethodPut || r.URL.Path != "/api/folders/folder-1" {
			http.NotFound(w, r)
			return
		}
		writeTestJSON(w, map[string]any{"uid": "folder-1", "title": "Renamed", "version": 8})
	}))
	defer server.Close()
	runner, err := NewGrafanaRunner(GrafanaConfig{BaseURL: server.URL, APIToken: "token", AllowAll: true})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "grafana_folder", ExternalID: "folder/folder-1", Name: "Old"},
		Action:   domain.TaskAction{Name: "grafana.folder.update"}, Parameters: map[string]any{"title": "Renamed", "version": float64(7)},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if requestedPath != "/api/folders/folder-1" || !strings.Contains(posted, `"title":"Renamed"`) || !strings.Contains(posted, `"version":7`) || !strings.Contains(posted, `"overwrite":false`) || result["uid"] != "folder-1" {
		t.Fatalf("folder update was not pinned: path=%s body=%s result=%#v", requestedPath, posted, result)
	}
}

func TestGrafanaRunnerRejectsFolderUpdateWithoutVersion(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	runner, err := NewGrafanaRunner(GrafanaConfig{BaseURL: server.URL, APIToken: "token", AllowAll: true})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "grafana_folder", ExternalID: "folder/folder-1", Name: "Old"},
		Action:   domain.TaskAction{Name: "grafana.folder.update"}, Parameters: map[string]any{"title": "Renamed"},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	})
	if err == nil || !strings.Contains(err.Error(), "version") || calls.Load() != 0 {
		t.Fatalf("missing version was not rejected before request: err=%v calls=%d", err, calls.Load())
	}
}

func TestGrafanaRunnerRejectsDashboardCrossUIDAndSecretsBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	runner, err := NewGrafanaRunner(GrafanaConfig{BaseURL: server.URL, APIToken: "token", AllowedDashboards: []string{"allowed-dash"}})
	if err != nil {
		t.Fatal(err)
	}
	base := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "grafana_dashboard", ExternalID: "dashboard/allowed-dash", Name: "Allowed"},
		Action:   domain.TaskAction{Name: "grafana.dashboard.update"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	base.Parameters = map[string]any{"dashboard": map[string]any{"uid": "other-dash", "title": "Hijack"}}
	if _, _, err := runner.Execute(context.Background(), base); err == nil || !strings.Contains(err.Error(), "uid is fixed") {
		t.Fatalf("cross-UID dashboard update was not rejected: %v", err)
	}
	base.Parameters = map[string]any{"dashboard": map[string]any{"uid": "allowed-dash", "title": "Safe", "secureJsonData": map[string]any{"token": "secret"}}}
	if _, _, err := runner.Execute(context.Background(), base); err == nil || !strings.Contains(err.Error(), "secret field") {
		t.Fatalf("secret-bearing dashboard update was not rejected: %v", err)
	}
	base.Parameters = map[string]any{"dashboard": map[string]any{"uid": "allowed-dash", "title": "Safe"}, "authorization": "secret"}
	if _, _, err := runner.Execute(context.Background(), base); err == nil || !strings.Contains(err.Error(), "secret field") {
		t.Fatalf("top-level secret-bearing dashboard update was not rejected: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("Grafana was contacted before dashboard safety validation")
	}
}

func TestGrafanaRunnerDatasourceGetStripsSecretsAndUpdateRejectsUnsafeFields(t *testing.T) {
	var mutationCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/datasources/uid/prom-main" {
			writeTestJSON(w, map[string]any{
				"uid": "prom-main", "name": "Prometheus", "type": "prometheus", "url": "https://prometheus.example.com", "access": "proxy", "isDefault": true,
				"jsonData":       map[string]any{"httpMethod": "POST", "nested": map[string]any{"token": "must-not-return"}},
				"secureJsonData": map[string]any{"password": "must-not-return"}, "secureJsonFields": map[string]any{"password": true},
			})
			return
		}
		mutationCalls.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	runner, err := NewGrafanaRunner(GrafanaConfig{BaseURL: server.URL, APIToken: "token", AllowAll: true})
	if err != nil {
		t.Fatal(err)
	}
	resource := domain.TaskResource{Type: "grafana_datasource", ExternalID: "datasource/prom-main", Name: "Prometheus"}
	result, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: resource, Action: domain.TaskAction{Name: "grafana.datasource.get"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	lower := strings.ToLower(string(encoded))
	if strings.Contains(lower, "must-not-return") || strings.Contains(lower, "securejson") || strings.Contains(lower, `"token"`) {
		t.Fatalf("datasource read leaked secret configuration: %s", encoded)
	}
	datasource := result["datasource"].(map[string]any)
	if datasource["name"] != "Prometheus" || datasource["url"] != "https://prometheus.example.com" {
		t.Fatalf("safe datasource fields missing: %#v", datasource)
	}
	for _, parameters := range []map[string]any{
		{"datasourceUid": "other", "datasourceName": "Prometheus", "type": "prometheus", "url": "https://prometheus.example.com"},
		{"datasourceName": "Prometheus", "type": "prometheus", "url": "https://prometheus.example.com", "jsonData": map[string]any{"password": "secret"}},
	} {
		_, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
			Resource: resource, Action: domain.TaskAction{Name: "grafana.datasource.update"}, Parameters: parameters,
			Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
		})
		if err == nil {
			t.Fatalf("unsafe datasource update was accepted: %#v", parameters)
		}
	}
	if mutationCalls.Load() != 0 {
		t.Fatal("Grafana was contacted before datasource safety validation")
	}
}
