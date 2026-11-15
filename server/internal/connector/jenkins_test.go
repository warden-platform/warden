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

	"gitee.com/pam-platform/warden/server/internal/domain"
)

func TestJenkinsRunnerDiscoversScopedResourcesWithoutSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if username, password, ok := r.BasicAuth(); !ok || username != "agent" || password != "secret-token" {
			t.Fatal("Jenkins request did not use configured basic auth")
		}
		switch r.URL.Path {
		case "/api/json":
			writeTestJSON(w, map[string]any{"jobs": []any{
				map[string]any{"name": "allowed", "buildable": true, "builds": []any{map[string]any{"number": 7, "result": "SUCCESS"}}},
				map[string]any{"name": "private", "buildable": true},
			}})
		case "/queue/api/json":
			writeTestJSON(w, map[string]any{"items": []any{map[string]any{"id": 12, "task": map[string]any{"fullName": "allowed"}}}})
		case "/computer/api/json":
			writeTestJSON(w, map[string]any{"computer": []any{map[string]any{"displayName": "built-in", "offline": false}}})
		case "/credentials/store/system/domain/_/api/json":
			writeTestJSON(w, map[string]any{"credentials": []any{map[string]any{
				"id": "deploy-key", "displayName": "Deploy key", "description": "production deploy", "typeName": "SSH Username with private key",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "secret-token", AllowedJobs: []string{"allowed"}})
	if err != nil {
		t.Fatal(err)
	}
	resources := runner.ListResources(context.Background())
	encoded, err := json.Marshal(resources)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{"jenkins_controller", "job/allowed", "job/allowed/build/7", "queue/12", "node/built-in", "credential/deploy-key"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("discovery result is missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "job/private") || strings.Contains(text, "secret-token") {
		t.Fatalf("discovery exposed an out-of-scope job or API token: %s", text)
	}
}

func TestJenkinsRunnerDiscoversStableControllerIdentity(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{
		BaseURL: server.URL + "/jenkins", Username: "agent", APIToken: "token", AllowAll: true, IntegrationID: "integration-jenkins",
	})
	if err != nil {
		t.Fatal(err)
	}
	resources := runner.ListResources(context.Background())
	if len(resources) == 0 {
		t.Fatal("controller resource was not discovered")
	}
	controller := resources[0]
	wantIdentity := strings.TrimPrefix(server.URL, "http://") + "/jenkins"
	if controller.ResourceType != "jenkins_controller" || controller.ExternalID != wantIdentity || controller.Name != wantIdentity {
		t.Fatalf("unstable controller identity: %#v", controller)
	}
	if controller.IntegrationID != "integration-jenkins" || controller.Metadata["endpoint"] != server.URL+"/jenkins" {
		t.Fatalf("controller routing metadata is incomplete: %#v", controller)
	}
}

func TestJenkinsRunnerRejectsOutOfScopeJobBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"team/*"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_job", ExternalID: "job/other/deploy", Name: "other/deploy"},
		Action:   domain.TaskAction{Name: "jenkins.job.get"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err == nil {
		t.Fatal("out-of-scope Jenkins job was accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("Jenkins was contacted before scope validation")
	}
}

func TestJenkinsRunnerSnapshotResourceReadsJobDirectly(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/job/team/job/deploy/api/json":
			writeTestJSON(w, map[string]any{"name": "deploy", "color": "blue", "buildable": true, "inQueue": false})
		case "/job/team/job/deploy/config.xml":
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected snapshot path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"team/deploy"}, IntegrationID: "ig-1"})
	if err != nil {
		t.Fatal(err)
	}
	resource, found := runner.SnapshotResource(context.Background(), domain.TaskResource{Type: "jenkins_job", ExternalID: "job/team/deploy", Name: "team/deploy", IntegrationID: "ig-1"})
	if !found || resource.ExternalID != "job/team/deploy" || resource.IntegrationID != "ig-1" || resource.Metadata["buildable"] != true {
		t.Fatalf("unexpected snapshot: found=%v resource=%#v", found, resource)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected the Job snapshot and pipeline metadata requests, got %d", calls.Load())
	}
}

func TestJenkinsRunnerSnapshotResourceReturnsAbsentOn404(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"missing"}})
	if err != nil {
		t.Fatal(err)
	}
	_, found := runner.SnapshotResource(context.Background(), domain.TaskResource{Type: "jenkins_job", ExternalID: "job/missing", Name: "missing"})
	if found {
		t.Fatal("404 Job snapshot must be absent")
	}
}

func TestJenkinsRunnerSnapshotResourceRejectsOutOfScopeBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"team/*"}})
	if err != nil {
		t.Fatal(err)
	}
	_, found := runner.SnapshotResource(context.Background(), domain.TaskResource{Type: "jenkins_job", ExternalID: "job/other/deploy", Name: "other/deploy"})
	if found || calls.Load() != 0 {
		t.Fatalf("out-of-scope snapshot must be rejected locally: found=%v calls=%d", found, calls.Load())
	}
}

func TestJenkinsRunnerRejectsCrossResourceJobParameterBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"team/deploy", "other/job"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_job", ExternalID: "job/team/deploy", Name: "team/deploy"},
		Action:   domain.TaskAction{Name: "jenkins.job.configure"}, Parameters: map[string]any{"name": "other/job", "description": "must not run"},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err == nil || !strings.Contains(err.Error(), "authorized resource") {
		t.Fatalf("cross-resource Jenkins parameter was not rejected: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("Jenkins was contacted before target identity validation")
	}
}

func TestJenkinsRunnerConfiguresOnlyStructuredJobFields(t *testing.T) {
	const original = `<?xml version="1.1" encoding="UTF-8"?><flow-definition plugin="workflow-job"><description>old</description><quietPeriod>5</quietPeriod><definition><script>echo "keep me"</script></definition></flow-definition>`
	var posted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/job/team/job/deploy/config.xml" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, original)
		case r.URL.Path == "/crumbIssuer/api/json":
			writeTestJSON(w, map[string]any{"Crumb": "crumb-value", "CrumbRequestField": "Jenkins-Crumb"})
		case r.URL.Path == "/job/team/job/deploy/config.xml" && r.Method == http.MethodPost:
			if r.Header.Get("Jenkins-Crumb") != "crumb-value" {
				t.Fatal("Jenkins mutation did not include the CSRF crumb")
			}
			data, _ := io.ReadAll(r.Body)
			posted = string(data)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "secret-token", AllowedJobs: []string{"team/deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource:   domain.TaskResource{Type: "jenkins_job", ExternalID: "job/team/deploy", Name: "team/deploy"},
		Action:     domain.TaskAction{Name: "jenkins.job.configure"},
		Parameters: map[string]any{"description": "managed by Warden", "quietPeriod": float64(30)},
		Grant:      domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if result["descriptionUpdated"] != true || !strings.Contains(posted, "managed by Warden") || !strings.Contains(posted, "<quietPeriod>30</quietPeriod>") {
		t.Fatalf("structured fields were not updated: %s", posted)
	}
	if !strings.Contains(posted, `echo &#34;keep me&#34;`) || strings.Contains(posted, "secret-token") {
		t.Fatalf("existing pipeline was not preserved or token leaked: %s", posted)
	}
}

func TestJenkinsRunnerEmptyScopeAllowsAllJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/json":
			writeTestJSON(w, map[string]any{"jobs": []any{
				map[string]any{"name": "deploy", "buildable": true},
				map[string]any{"name": "legacy", "buildable": true},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// No AllowedJobs: the runner should materialize every discovered job and
	// accept execution against any of them (empty scope = allow all).
	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowAll: true})
	if err != nil {
		t.Fatal(err)
	}
	resources := runner.ListResources(context.Background())
	encoded, _ := json.Marshal(resources)
	text := string(encoded)
	for _, expected := range []string{"job/deploy", "job/legacy"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("empty scope did not allow all jobs: missing %q in %s", expected, text)
		}
	}

	// Execution against any job must also be accepted when scope is empty.
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_job", ExternalID: "job/deploy", Name: "deploy"},
		Action:   domain.TaskAction{Name: "jenkins.job.get"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if err := runner.validateResource(task.Resource); err != nil {
		t.Fatalf("empty scope rejected an in-scope job: %v", err)
	}
}

func TestJenkinsRunnerSkipsLostFoundFolder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/json":
			writeTestJSON(w, map[string]any{"jobs": []any{
				map[string]any{"name": "real-job", "buildable": true},
				map[string]any{"name": "lost+found", "buildable": true, "_class": "com.cloudbees.hudson.plugins.folder.Folder"},
			}})
		case "/job/lost/job/found/api/json":
			// Folder recursion into lost+found should never reach here
			t.Fatalf("unexpected recursion into lost+found: %s", r.URL.Path)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowAll: true})
	if err != nil {
		t.Fatal(err)
	}
	resources := runner.ListResources(context.Background())
	encoded, _ := json.Marshal(resources)
	text := string(encoded)
	if strings.Contains(text, "lost+found") {
		t.Fatalf("lost+found jobs should be filtered out: %s", text)
	}
	if !strings.Contains(text, "real-job") {
		t.Fatalf("real job was incorrectly filtered: %s", text)
	}
}

func TestMultiRunnerRoutesByResourceType(t *testing.T) {
	primary := &recordingRunner{result: "primary"}
	jenkins := &recordingRunner{result: "jenkins"}
	runner := NewMultiRunner(primary, "deployment")
	runner.Add(jenkins, "jenkins_job")

	result, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{Resource: domain.TaskResource{Type: "jenkins_job"}})
	if err != nil {
		t.Fatal(err)
	}
	if result["runner"] != "jenkins" || primary.calls != 0 || jenkins.calls != 1 {
		t.Fatalf("unexpected route result: %#v", result)
	}
}

func TestMultiRunnerReplacesAndRemovesAdapter(t *testing.T) {
	primary := &recordingRunner{result: "primary"}
	first := &recordingRunner{result: "first"}
	second := &recordingRunner{result: "second"}
	runner := NewMultiRunner(primary, "deployment")
	runner.Set("jenkins", first, "jenkins_job")
	runner.Set("jenkins", second, "jenkins_job", "jenkins_build")

	result, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{Resource: domain.TaskResource{Type: "jenkins_job"}})
	if err != nil || result["runner"] != "second" || first.calls != 0 || second.calls != 1 {
		t.Fatalf("adapter was not replaced: result=%#v err=%v", result, err)
	}
	runner.Set("jenkins", nil, JenkinsResourceTypes...)
	if _, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{Resource: domain.TaskResource{Type: "jenkins_job"}}); err == nil {
		t.Fatal("removed adapter still accepted Jenkins resources")
	}
}

func TestMultiRunnerRoutesByIntegrationID(t *testing.T) {
	// Two integrations of the same protocol (jenkins) must coexist: each routes
	// by integration_id, so a connector can wire up multiple Jenkins instances
	// without their runners overwriting each other.
	primary := &recordingRunner{result: "primary"}
	jenkinsA := &recordingRunner{result: "jenkins-a"}
	jenkinsB := &recordingRunner{result: "jenkins-b"}
	runner := NewMultiRunner(primary, "deployment")
	runner.SetIntegration("ig-a", jenkinsA, "jenkins_job")
	runner.SetIntegration("ig-b", jenkinsB, "jenkins_job")

	taskA := domain.TaskEnvelope{Resource: domain.TaskResource{Type: "jenkins_job", IntegrationID: "ig-a"}}
	result, _, err := runner.Execute(context.Background(), taskA)
	if err != nil || result["runner"] != "jenkins-a" || jenkinsA.calls != 1 || jenkinsB.calls != 0 {
		t.Fatalf("integration A did not route correctly: %#v err=%v", result, err)
	}

	taskB := domain.TaskEnvelope{Resource: domain.TaskResource{Type: "jenkins_job", IntegrationID: "ig-b"}}
	result, _, err = runner.Execute(context.Background(), taskB)
	if err != nil || result["runner"] != "jenkins-b" || jenkinsA.calls != 1 || jenkinsB.calls != 1 {
		t.Fatalf("integration B did not route correctly: %#v err=%v", result, err)
	}

	// Native resources (no integration_id) still fall back to byType.
	native, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{Resource: domain.TaskResource{Type: "deployment"}})
	if err != nil || native["runner"] != "primary" {
		t.Fatalf("native resource did not fall back to byType: %#v err=%v", native, err)
	}

	// Removing one integration leaves the other intact.
	runner.SetIntegration("ig-a", nil, "jenkins_job")
	if _, _, err := runner.Execute(context.Background(), taskA); err == nil {
		t.Fatal("removed integration A still routed")
	}
	result, _, err = runner.Execute(context.Background(), taskB)
	if err != nil || result["runner"] != "jenkins-b" {
		t.Fatalf("integration B broke after removing A: %#v err=%v", result, err)
	}
}

type recordingRunner struct {
	result string
	calls  int
}

func (r *recordingRunner) ListResources(context.Context) []DiscoveredResource { return nil }

func (r *recordingRunner) Execute(context.Context, domain.TaskEnvelope) (map[string]any, Lease, error) {
	r.calls++

	return map[string]any{"runner": r.result}, Lease{}, nil
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func TestJenkinsRunnerGetNodeAddressesBuiltInNodeWithParens(t *testing.T) {
	// Jenkins' built-in node (displayName "Built-In Node") is addressed via
	// /computer/(master)/api/json - the parentheses are literal path characters
	// and the internal URL name stays "master" even after Jenkins 2.319+
	// renamed the displayName to "Built-In Node".
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		switch requestedPath {
		case "/computer/(master)/api/json":
			writeTestJSON(w, map[string]any{"displayName": "Built-In Node", "offline": false, "numExecutors": 2})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"test-job"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_node", ExternalID: "node/Built-In Node", Name: "Built-In Node"},
		Action:   domain.TaskAction{Name: "jenkins.node.get"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("built-in node lookup failed: %v (path=%s)", err, requestedPath)
	}
	if result["displayName"] != "Built-In Node" {
		t.Fatalf("unexpected node result: %#v", result)
	}
	if requestedPath != "/computer/(master)/api/json" {
		t.Fatalf("expected URL path /computer/(master)/api/json, got %s", requestedPath)
	}
}

func TestJenkinsRunnerGetNodeAddressesRegularNodeByName(t *testing.T) {
	// Regular agent nodes use their displayName directly - no parens.
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		switch requestedPath {
		case "/computer/agent-1/api/json":
			writeTestJSON(w, map[string]any{"displayName": "agent-1", "offline": false, "numExecutors": 4})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"test-job"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_node", ExternalID: "node/agent-1", Name: "agent-1"},
		Action:   domain.TaskAction{Name: "jenkins.node.get"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("regular node lookup failed: %v (path=%s)", err, requestedPath)
	}
	if result["displayName"] != "agent-1" {
		t.Fatalf("unexpected node result: %#v", result)
	}
	if requestedPath != "/computer/agent-1/api/json" {
		t.Fatalf("expected URL path to use node name directly, got %s", requestedPath)
	}
}

func TestJenkinsRunnerGetJobConfig(t *testing.T) {
	const configXML = `<?xml version='1.0' encoding='UTF-8'?><flow-definition><description>test</description></flow-definition>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/team/job/deploy/config.xml":
			_, _ = io.WriteString(w, configXML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"team/deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_job", ExternalID: "job/team/deploy", Name: "team/deploy"},
		Action:   domain.TaskAction{Name: "jenkins.job.config.get"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("getJobConfig failed: %v", err)
	}
	config, ok := result["config"].(string)
	if !ok || !strings.Contains(config, "<description>test</description>") {
		t.Fatalf("unexpected config result: %#v", result)
	}
}

func TestJenkinsRunnerCreateJobInline(t *testing.T) {
	var postedBody string
	var postedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/crumbIssuer/api/json":
			writeTestJSON(w, map[string]any{"Crumb": "c", "CrumbRequestField": "Jenkins-Crumb"})
		case r.URL.Path == "/createItem" && r.Method == http.MethodPost:
			postedPath = r.URL.Path + "?" + r.URL.RawQuery
			data, _ := io.ReadAll(r.Body)
			postedBody = string(data)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"test-job"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_controller", ExternalID: server.URL[7:], Name: server.URL[7:]},
		Action:   domain.TaskAction{Name: "jenkins.job.create"},
		Parameters: map[string]any{
			"name":           "test-job",
			"mode":           "inline",
			"pipelineScript": "pipeline { agent any; stages { stage('Clone') { steps { git 'https://gitee.com/repo.git' } } } }",
			"description":    "test job",
		},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("createJob inline failed: %v", err)
	}
	if result["created"] != true || result["job"] != "test-job" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if postedPath != "/createItem?name=test-job" {
		t.Fatalf("expected POST /createItem?name=test-job, got %s", postedPath)
	}
	if !strings.Contains(postedBody, "CpsFlowDefinition") || !strings.Contains(postedBody, "gitee.com/repo.git") {
		t.Fatalf("posted XML missing inline pipeline script: %s", postedBody)
	}
	if !strings.Contains(postedBody, "<sandbox>true</sandbox>") {
		t.Fatalf("sandbox must be true: %s", postedBody)
	}
}

func TestJenkinsRunnerPersistsCrumbSessionCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/crumbIssuer/api/json":
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "session-1", Path: "/"})
			writeTestJSON(w, map[string]any{"Crumb": "c", "CrumbRequestField": "Jenkins-Crumb"})
		case r.URL.Path == "/createItem" && r.Method == http.MethodPost:
			if _, err := r.Cookie("JSESSIONID"); err != nil {
				http.Error(w, "No valid crumb was included in the request", http.StatusForbidden)
				return
			}
			if r.Header.Get("Jenkins-Crumb") != "c" {
				http.Error(w, "missing crumb", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"test-job"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_controller", ExternalID: server.URL[7:], Name: server.URL[7:]},
		Action:   domain.TaskAction{Name: "jenkins.job.create"},
		Parameters: map[string]any{
			"name": "test-job", "mode": "inline", "pipelineScript": "pipeline { agent any }",
		},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err != nil {
		t.Fatalf("createJob did not preserve Jenkins session cookie: %v", err)
	}
}

func TestJenkinsRunnerCreateJobScm(t *testing.T) {
	var postedBody string
	var postedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/crumbIssuer/api/json":
			writeTestJSON(w, map[string]any{"Crumb": "c", "CrumbRequestField": "Jenkins-Crumb"})
		case r.URL.Path == "/credentials/store/system/domain/_/api/json":
			writeTestJSON(w, map[string]any{"credentials": []any{map[string]any{"id": "gitee-key"}}})
		case r.URL.Path == "/job/folder/job/sub/createItem" && r.Method == http.MethodPost:
			postedPath = r.URL.Path + "?" + r.URL.RawQuery
			data, _ := io.ReadAll(r.Body)
			postedBody = string(data)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"folder/sub/*"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_controller", ExternalID: server.URL[7:], Name: server.URL[7:]},
		Action:   domain.TaskAction{Name: "jenkins.job.create"},
		Parameters: map[string]any{
			"name": "folder/sub/new-job",
			"mode": "scm",
			"scm": map[string]any{
				"repoUrl":       "https://gitee.com/org/repo.git",
				"credentialsId": "gitee-key",
				"branch":        "*/main",
				"scriptPath":    "Jenkinsfile.test",
			},
		},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("createJob scm failed: %v", err)
	}
	if result["created"] != true || result["job"] != "folder/sub/new-job" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if postedPath != "/job/folder/job/sub/createItem?name=new-job" {
		t.Fatalf("expected nested folder createItem path, got %s", postedPath)
	}
	for _, expected := range []string{"CpsScmFlowDefinition", "https://gitee.com/org/repo.git", "gitee-key", "*/main", "Jenkinsfile.test"} {
		if !strings.Contains(postedBody, expected) {
			t.Fatalf("posted XML missing %q: %s", expected, postedBody)
		}
	}
}

func TestJenkinsRunnerCreateJobConflictsWithoutUpdatingExisting(t *testing.T) {
	var createCalled, updateCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/crumbIssuer/api/json":
			writeTestJSON(w, map[string]any{"Crumb": "c", "CrumbRequestField": "Jenkins-Crumb"})
		case r.URL.Path == "/createItem" && r.Method == http.MethodPost:
			createCalled = true
			_, _ = io.Copy(io.Discard, r.Body)
			http.Error(w, "<html><title>Bad Request</title><body>Jenkins</body></html>", http.StatusBadRequest)
		case r.URL.Path == "/job/test-job/api/json" && r.Method == http.MethodGet:
			writeTestJSON(w, map[string]any{"fullName": "test-job"})
		case r.URL.Path == "/job/test-job/config.xml" && r.Method == http.MethodPost:
			updateCalled = true
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"test-job"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_controller", ExternalID: server.URL[7:], Name: server.URL[7:]},
		Action:   domain.TaskAction{Name: "jenkins.job.create"},
		Parameters: map[string]any{
			"name":           "test-job",
			"mode":           "inline",
			"pipelineScript": "pipeline { agent any; stages { stage('Hi') { steps { echo 'hi' } } } }",
		},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	_, _, err = runner.Execute(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "JENKINS_JOB_CONFLICT") {
		t.Fatalf("expected explicit conflict, got %v", err)
	}
	if !createCalled {
		t.Fatal("createItem was not attempted first")
	}
	if updateCalled {
		t.Fatal("create conflict must not overwrite the existing config.xml")
	}
}

func TestJenkinsRunnerUpdateJobInline(t *testing.T) {
	var posted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/crumbIssuer/api/json":
			writeTestJSON(w, map[string]any{"Crumb": "c", "CrumbRequestField": "Jenkins-Crumb"})
		case r.URL.Path == "/job/team/job/deploy/config.xml" && r.Method == http.MethodPost:
			data, _ := io.ReadAll(r.Body)
			posted = string(data)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "secret-token", AllowedJobs: []string{"team/deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_job", ExternalID: "job/team/deploy", Name: "team/deploy"},
		Action:   domain.TaskAction{Name: "jenkins.job.update"},
		Parameters: map[string]any{
			"mode": "inline", "pipelineScript": "pipeline { agent any; stages { stage('Deploy') { steps { echo 'ok' } } } }",
			"description": "updated by Warden", "quietPeriod": float64(15),
		},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("update inline failed: %v", err)
	}
	if result["updated"] != true || result["job"] != "team/deploy" || result["mode"] != "inline" {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, expected := range []string{"CpsFlowDefinition", "updated by Warden", "<quietPeriod>15</quietPeriod>", "stage(&#39;Deploy&#39;)"} {
		if !strings.Contains(posted, expected) {
			t.Fatalf("updated inline XML missing %q: %s", expected, posted)
		}
	}
	if strings.Contains(posted, "secret-token") {
		t.Fatalf("API token leaked into Job config: %s", posted)
	}
}

func TestJenkinsRunnerUpdateJobScmUsesCredentialReferenceOnly(t *testing.T) {
	var posted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/crumbIssuer/api/json":
			writeTestJSON(w, map[string]any{"Crumb": "c", "CrumbRequestField": "Jenkins-Crumb"})
		case r.URL.Path == "/credentials/store/system/domain/_/api/json":
			writeTestJSON(w, map[string]any{"credentials": []any{map[string]any{"id": "deploy-key"}}})
		case r.URL.Path == "/job/deploy/config.xml" && r.Method == http.MethodPost:
			data, _ := io.ReadAll(r.Body)
			posted = string(data)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "super-secret-token", AllowedJobs: []string{"deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_job", ExternalID: "job/deploy", Name: "deploy"},
		Action:   domain.TaskAction{Name: "jenkins.job.update"},
		Parameters: map[string]any{"mode": "scm", "scm": map[string]any{
			"repoUrl": "ssh://git@example.com/team/app.git", "credentialsId": "deploy-key", "branch": "*/release", "scriptPath": "ci/Jenkinsfile",
		}},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err != nil {
		t.Fatalf("update SCM failed: %v", err)
	}
	for _, expected := range []string{"CpsScmFlowDefinition", "ssh://git@example.com/team/app.git", "deploy-key", "*/release", "ci/Jenkinsfile"} {
		if !strings.Contains(posted, expected) {
			t.Fatalf("updated SCM XML missing %q: %s", expected, posted)
		}
	}
	if strings.Contains(posted, "super-secret-token") {
		t.Fatalf("credential secret leaked into Job config: %s", posted)
	}
}

func TestJenkinsRunnerRejectsUnknownScmCredentialBeforeWrite(t *testing.T) {
	var configWrites atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/credentials/store/system/domain/_/api/json":
			writeTestJSON(w, map[string]any{"credentials": []any{map[string]any{"id": "registered"}}})
		case r.URL.Path == "/job/deploy/config.xml":
			configWrites.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource:   domain.TaskResource{Type: "jenkins_job", ExternalID: "job/deploy", Name: "deploy"},
		Action:     domain.TaskAction{Name: "jenkins.job.update"},
		Parameters: map[string]any{"mode": "scm", "scm": map[string]any{"repoUrl": "https://example.com/repo.git", "credentialsId": "missing"}},
		Grant:      domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	})
	if err == nil || !strings.Contains(err.Error(), "JENKINS_CREDENTIAL_REFERENCE_NOT_FOUND") {
		t.Fatalf("expected unknown credential rejection, got %v", err)
	}
	if configWrites.Load() != 0 {
		t.Fatal("unknown credential must be rejected before config.xml write")
	}
}

func TestJenkinsRunnerDeleteJobRequiresExactSecondConfirmation(t *testing.T) {
	var deleteCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/crumbIssuer/api/json":
			writeTestJSON(w, map[string]any{"Crumb": "c", "CrumbRequestField": "Jenkins-Crumb"})
		case r.URL.Path == "/job/team/job/deploy/doDelete" && r.Method == http.MethodPost:
			deleteCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"team/deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_job", ExternalID: "job/team/deploy", Name: "team/deploy"},
		Action:   domain.TaskAction{Name: "jenkins.job.delete"}, Parameters: map[string]any{"confirmJobName": "deploy"},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err == nil || !strings.Contains(err.Error(), "JENKINS_JOB_CONFIRMATION_MISMATCH") {
		t.Fatalf("expected confirmation mismatch, got %v", err)
	}
	if deleteCalls.Load() != 0 {
		t.Fatal("Jenkins was contacted before exact second confirmation")
	}
	task.Parameters["confirmJobName"] = "team/deploy"
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if deleteCalls.Load() != 1 || result["deleted"] != true {
		t.Fatalf("unexpected delete result: %#v", result)
	}
}

func TestJenkinsRunnerUpdateJobReturnsNotFoundCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			writeTestJSON(w, map[string]any{"Crumb": "c", "CrumbRequestField": "Jenkins-Crumb"})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"missing"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_job", ExternalID: "job/missing", Name: "missing"},
		Action:   domain.TaskAction{Name: "jenkins.job.update"}, Parameters: map[string]any{"mode": "inline", "pipelineScript": "pipeline { agent any }"},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err == nil || !strings.Contains(err.Error(), "JENKINS_JOB_NOT_FOUND") {
		t.Fatalf("expected not-found code, got %v", err)
	}
}

func TestJenkinsLifecycleMutationsHaveUnknownOutcomeOnCancellation(t *testing.T) {
	for _, action := range []string{"jenkins.job.create", "jenkins.job.update", "jenkins.job.configure", "jenkins.job.delete"} {
		if !IsJenkinsUnknownOutcome(action, context.Canceled) {
			t.Fatalf("%s must be treated as an unknown mutation outcome", action)
		}
	}
}

func TestJenkinsRunnerCreateJobRejectsOutOfScope(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token", AllowedJobs: []string{"team/*"}})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "jenkins_controller", ExternalID: server.URL[7:], Name: server.URL[7:]},
		Action:   domain.TaskAction{Name: "jenkins.job.create"},
		Parameters: map[string]any{
			"name":           "other/job",
			"mode":           "inline",
			"pipelineScript": "pipeline { agent any }",
		},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err == nil {
		t.Fatal("out-of-scope job creation was accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("Jenkins was contacted before scope validation")
	}
}

func TestJenkinsRunnerCreateJobRejectsInvalidMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource:   domain.TaskResource{Type: "jenkins_controller", ExternalID: server.URL[7:], Name: server.URL[7:]},
		Action:     domain.TaskAction{Name: "jenkins.job.create"},
		Parameters: map[string]any{"name": "test-job", "mode": "freestyle"},
		Grant:      domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err == nil {
		t.Fatal("invalid mode was accepted")
	}
}
