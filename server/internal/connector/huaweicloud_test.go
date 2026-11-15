package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

func newHuaweiTestServer(t *testing.T, rdsStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		if r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/bucket-a") {
			if !strings.HasPrefix(authorization, "OBS test-ak:") {
				t.Fatalf("OBS request is not signed with the OBS scheme: %q", authorization)
			}
		} else if !strings.HasPrefix(authorization, "SDK-HMAC-SHA256 Access=test-ak,") {
			t.Fatalf("API request is not signed with SDK-HMAC-SHA256: %q", authorization)
		}
		switch {
		case r.URL.Path == "/v3/projects":
			writeTestJSON(w, map[string]any{"projects": []any{map[string]any{"id": "proj-123", "name": "cn-north-4"}}})
		case r.URL.Path == "/v1/proj-123/cloudservers/detail":
			writeTestJSON(w, map[string]any{"servers": []any{map[string]any{
				"id": "ecs-uuid-1", "name": "web-01", "status": "ACTIVE",
				"flavor": map[string]any{"id": "s6.large.2"}, "OS-EXT-AZ:availability_zone": "cn-north-4a",
			}}})
		case r.URL.Path == "/v1/proj-123/cloudservers/ecs-uuid-1":
			writeTestJSON(w, map[string]any{"server": map[string]any{"id": "ecs-uuid-1", "name": "web-01", "status": "ACTIVE"}})
		case r.URL.Path == "/v3/proj-123/instances":
			if rdsStatus != http.StatusOK {
				http.Error(w, `{"error_code":"RDS.0003","error_msg":"permission denied"}`, rdsStatus)
				return
			}
			writeTestJSON(w, map[string]any{"instances": []any{map[string]any{
				"id": "rds-uuid-1", "name": "orders-db", "status": "ACTIVE",
				"datastore": map[string]any{"type": "MySQL", "version": "8.0"},
			}}})
		case r.URL.Path == "/v3/proj-123/elb/loadbalancers":
			writeTestJSON(w, map[string]any{"loadbalancers": []any{map[string]any{
				"id": "elb-uuid-1", "name": "ingress-lb", "provisioning_status": "ACTIVE", "vip_address": "10.0.0.10",
			}}})
		case r.URL.Path == "/v2/proj-123/groups":
			writeTestJSON(w, map[string]any{"log_groups": []any{map[string]any{"log_group_id": "group-1", "log_group_name": "application", "ttl_in_days": 30}}})
		case r.URL.Path == "/v2/proj-123/groups/group-1/streams":
			writeTestJSON(w, map[string]any{"log_streams": []any{map[string]any{"log_stream_id": "stream-1", "log_stream_name": "api"}}})
		case r.URL.Path == "/v2/proj-123/groups/group-1/streams/stream-1/content/query":
			writeTestJSON(w, map[string]any{"logs": []any{map[string]any{"content": "error sample"}}})
		case r.URL.Path == "/v2/proj-123/instances":
			writeTestJSON(w, map[string]any{"instances": []any{map[string]any{
				"id": "swr-instance-1", "name": "registry-prod", "status": "Running", "spec": "swr.enterprise.x1",
				"version": "v2.0", "access_address": "swr.cn-north-4.myhuaweicloud.com", "vpc_id": "vpc-1", "subnet_id": "subnet-1",
			}}, "total": 1})
		case r.URL.Path == "/v2/proj-123/instances/swr-instance-1":
			writeTestJSON(w, map[string]any{"id": "swr-instance-1", "name": "registry-prod", "status": "Running", "access_address": "swr.cn-north-4.myhuaweicloud.com"})
		case r.URL.Path == "/v2/proj-123/instances/swr-instance-1/namespaces":
			writeTestJSON(w, map[string]any{"namespaces": []any{map[string]any{
				"name": "team-a", "namespace_id": 11, "repo_count": 1, "metadata": map[string]any{"public": "false"},
			}}, "total": 1})
		case r.URL.Path == "/v2/proj-123/instances/swr-instance-1/namespaces/team-a":
			writeTestJSON(w, map[string]any{"name": "team-a", "namespace_id": 11, "repo_count": 1, "metadata": map[string]any{"public": "false"}})
		case r.URL.Path == "/v2/proj-123/instances/swr-instance-1/namespaces/team-a/repositories":
			writeTestJSON(w, map[string]any{"repositories": []any{map[string]any{
				"id": 21, "name": "api", "namespace_name": "team-a", "namespace_id": 11, "tag_count": 1, "artifact_count": 1, "pull_count": 7,
			}}, "total": 1})
		case r.URL.Path == "/v2/proj-123/instances/swr-instance-1/namespaces/team-a/repositories/api":
			writeTestJSON(w, map[string]any{"id": 21, "name": "api", "namespace_name": "team-a", "namespace_id": 11, "tag_count": 1, "artifact_count": 1, "pull_count": 7})
		case r.URL.Path == "/v2/instances/swr-instance-1/namespaces/team-a/repositories/api/tags":
			writeTestJSON(w, map[string]any{"tags": []any{map[string]any{
				"name": "v1.2.3", "id": 31, "artifact_id": 41, "digest": "sha256:abcdef0123456789", "size": 1024, "type": "IMAGE",
				"push_time": "2026-01-01T00:00:00Z",
			}}, "total": 1})
		case r.URL.Path == "/v2/proj-123/instances/swr-instance-1/namespaces/team-a/repositories/api/artifacts/sha256:abcdef0123456789":
			writeTestJSON(w, map[string]any{"name": "v1.2.3", "digest": "sha256:abcdef0123456789", "size": 1024})
		case r.URL.Path == "/":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult><Owner><ID>owner</ID></Owner><Buckets>
<Bucket><Name>bucket-a</Name><CreationDate>2026-01-01T00:00:00.000Z</CreationDate><Location>cn-north-4</Location></Bucket>
</Buckets></ListAllMyBucketsResult>`))
		case r.URL.Path == "/bucket-a" && r.URL.RawQuery == "storageinfo":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<GetBucketStorageInfoResult><Size>2048</Size><ObjectNumber>7</ObjectNumber></GetBucketStorageInfoResult>`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func newHuaweiTestRunner(t *testing.T, server *httptest.Server, config HuaweiCloudConfig) *HuaweiCloudRunner {
	t.Helper()
	config.Region = "cn-north-4"
	config.AccessKey = "test-ak"
	config.SecretKey = "test-sk"
	config.IntegrationID = "ig-1"
	config.EndpointOverrides = map[string]string{
		"iam": server.URL, "ecs": server.URL, "rds": server.URL, "elb": server.URL, "ces": server.URL, "obs": server.URL, "lts": server.URL, "swr": server.URL,
	}
	runner, err := NewHuaweiCloudRunner(config)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestHuaweiCloudRunnerUsesEUWest101Endpoints(t *testing.T) {
	runner, err := NewHuaweiCloudRunner(HuaweiCloudConfig{
		Region: "eu-west-101", AccessKey: "test-ak", SecretKey: "test-sk", AllowAll: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := runner.endpoint("iam"); got != "https://iam.myhuaweicloud.eu" {
		t.Fatalf("unexpected eu-west-101 IAM endpoint: %s", got)
	}
	if got := runner.endpoint("swr"); got != "https://swr-api.eu-west-101.myhuaweicloud.eu" {
		t.Fatalf("unexpected eu-west-101 SWR endpoint: %s", got)
	}
}

func TestHuaweiProviderECSTargetResolvesProjectServerSide(t *testing.T) {
	server := newHuaweiTestServer(t, http.StatusOK)
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowAll: true})
	request := domain.ProviderRequest{Path: "/servers/ecs-uuid-1"}
	binding := domain.ProviderRequestBinding{Capability: domain.ResourceCapability{Name: "huawei.ecs.read"}}

	target, err := runner.providerTarget(context.Background(), request, binding)
	if err != nil {
		t.Fatal(err)
	}
	if target.Path != "/v1/proj-123/cloudservers/ecs-uuid-1" {
		t.Fatalf("unexpected ECS target path %q", target.Path)
	}
}

func TestHuaweiProviderTargetUsesResourceRegionAndOBSVirtualHost(t *testing.T) {
	runner, err := NewHuaweiCloudRunner(HuaweiCloudConfig{
		Region: "cn-north-4", RegionMode: "allowlist", Regions: []string{"cn-east-3"},
		AccessKey: "test-ak", SecretKey: "test-sk", AllowAll: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := domain.ProviderRequest{Path: "/example-files/ollama/README.md"}
	binding := domain.ProviderRequestBinding{
		Resource:   domain.Resource{ExternalID: "obs/cn-east-3/example-files"},
		Capability: domain.ResourceCapability{Name: "huawei.obs.object.metadata.read"},
	}

	target, err := runner.providerTarget(context.Background(), request, binding)
	if err != nil {
		t.Fatal(err)
	}
	if target.String() != "https://example-files.obs.cn-east-3.myhuaweicloud.com/ollama/README.md" {
		t.Fatalf("unexpected OBS target %q", target.String())
	}
	req, err := http.NewRequest(http.MethodHead, target.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.authenticateProviderRequest(req); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(req.Header.Get("Authorization"), "OBS test-ak:") {
		t.Fatalf("OBS request was not signed with OBS authentication")
	}
}

func TestHuaweiCloudRunnerDiscoversResourcesAndProbesPermissions(t *testing.T) {
	// RDS returns 403: the IAM user has no RDS permission, so discovery must
	// surface no RDS resources and mark the service UNAVAILABLE on the account.
	server := newHuaweiTestServer(t, http.StatusForbidden)
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowAll: true})

	resources, complete := runner.ListResourcesComplete(context.Background())
	if complete {
		t.Fatal("discovery with a failed RDS probe must be marked incomplete")
	}
	encoded, err := json.Marshal(resources)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{
		"account/cn-north-4", "huawei_cloud_account",
		"ecs/ecs-uuid-1", "huawei_ecs_instance",
		"elb/elb-uuid-1", "huawei_elb_loadbalancer",
		"obs/bucket-a", "huawei_obs_bucket",
		"lts-group/cn-north-4/group-1", "huawei_lts_log_group",
		"lts-stream/cn-north-4/group-1/stream-1", "huawei_lts_log_stream",
		"swr-instance/swr-instance-1", "huawei_swr_instance",
		"swr-namespace/swr-instance-1/team-a", "huawei_swr_namespace",
		"swr-repository/swr-instance-1/team-a/api", "huawei_swr_repository",
		"swr-tag/swr-instance-1/team-a/api/v1.2.3", "huawei_swr_tag",
		`"rds":"UNAVAILABLE"`, `"ecs":"ACTIVE"`, `"swr":"ACTIVE"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("discovery result is missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "rds-uuid-1") {
		t.Fatalf("discovery surfaced a resource from a denied service: %s", text)
	}
	if strings.Contains(text, "test-sk") {
		t.Fatalf("discovery leaked the secret key: %s", text)
	}
	for _, resource := range resources {
		if resource.IntegrationID != "ig-1" {
			t.Fatalf("resource %s missing integration id", resource.ExternalID)
		}
	}
}

func TestHuaweiCloudRunnerReportsCompleteDiscovery(t *testing.T) {
	server := newHuaweiTestServer(t, http.StatusOK)
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowAll: true})

	resources, complete := runner.ListResourcesComplete(context.Background())
	if !complete {
		t.Fatal("successful Huawei Cloud discovery was marked incomplete")
	}
	if len(resources) == 0 {
		t.Fatal("successful Huawei Cloud discovery returned no resources")
	}
}

func TestHuaweiCloudRunnerScopeSkipsOutOfScopeServices(t *testing.T) {
	var calls atomic.Int32
	server := newHuaweiTestServer(t, http.StatusOK)
	defer server.Close()
	proxied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "out-of-scope service was contacted", http.StatusInternalServerError)
	}))
	defer proxied.Close()

	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"ecs"}})
	// Point the out-of-scope services at a server that fails the test if hit.
	runner.overrides["rds"] = proxied.URL
	runner.overrides["elb"] = proxied.URL
	runner.overrides["obs"] = proxied.URL

	resources := runner.ListResources(context.Background())
	encoded, _ := json.Marshal(resources)
	text := string(encoded)
	if !strings.Contains(text, "ecs/ecs-uuid-1") || !strings.Contains(text, `"rds":"OUT_OF_SCOPE"`) {
		t.Fatalf("scoped discovery result unexpected: %s", text)
	}
	if calls.Load() != 0 {
		t.Fatal("an out-of-scope service was contacted during discovery")
	}
}

func TestHuaweiCloudRunnerQueriesLTSWithinServiceScope(t *testing.T) {
	server := newHuaweiTestServer(t, http.StatusOK)
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"lts"}})
	now := time.Now().UnixMilli()
	task := domain.TaskEnvelope{Resource: domain.TaskResource{Type: "huawei_lts_log_stream", ExternalID: "lts-stream/cn-north-4/group-1/stream-1", Name: "api"}, Action: domain.TaskAction{Name: "huawei.lts.logs.query"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)}, Parameters: map[string]any{"name": "api", "startTime": float64(now - 86400000*30), "endTime": float64(now), "keywords": "ERROR", "limit": float64(100)}}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	resultBody, ok := result["result"].(map[string]any)
	if !ok || resultBody["logs"] == nil || result["logGroupId"] != "group-1" || result["logStreamId"] != "stream-1" {
		t.Fatalf("unexpected LTS result: %#v", result)
	}
	runner.allowedServices = map[string]bool{"ecs": true}
	if _, _, err := runner.Execute(context.Background(), task); err == nil {
		t.Fatal("LTS action bypassed Integration service scope")
	}
}

func TestHuaweiCloudRunnerExecutesReadOnlyActions(t *testing.T) {
	server := newHuaweiTestServer(t, http.StatusOK)
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowAll: true})
	grant := domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)}

	result, lease, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_ecs_instance", ExternalID: "ecs/ecs-uuid-1", Name: "web-01"},
		Action:   domain.TaskAction{Name: "huawei.ecs.get"}, Grant: grant,
	})
	if err != nil {
		t.Fatalf("huawei.ecs.get failed: %v", err)
	}
	if server, ok := result["server"].(map[string]any); !ok || server["id"] != "ecs-uuid-1" {
		t.Fatalf("unexpected ecs.get result: %#v", result)
	}
	if lease.Provider != "HUAWEICLOUD_CREDENTIAL_REFERENCE" {
		t.Fatalf("unexpected lease provider: %s", lease.Provider)
	}

	result, _, err = runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_obs_bucket", ExternalID: "obs/bucket-a", Name: "bucket-a"},
		Action:   domain.TaskAction{Name: "huawei.obs.bucket.get"}, Grant: grant,
	})
	if err != nil {
		t.Fatalf("huawei.obs.bucket.get failed: %v", err)
	}
	if result["sizeBytes"] != int64(2048) || result["objectCount"] != int64(7) {
		t.Fatalf("unexpected obs storage info: %#v", result)
	}

	result, _, err = runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_cloud_account", ExternalID: "account/cn-north-4", Name: "huaweicloud/cn-north-4"},
		Action:   domain.TaskAction{Name: "huawei.account.get"}, Grant: grant,
	})
	if err != nil {
		t.Fatalf("huawei.account.get failed: %v", err)
	}
	services, ok := result["services"].(map[string]any)
	if !ok || services["ecs"] != "ACTIVE" || services["rds"] != "ACTIVE" {
		t.Fatalf("unexpected account probe result: %#v", result)
	}
}

func TestHuaweiCloudRunnerExecutesSWRReadOnlyActions(t *testing.T) {
	server := newHuaweiTestServer(t, http.StatusOK)
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"swr"}})
	grant := domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)}

	tests := []struct {
		name        string
		resource    domain.TaskResource
		action      string
		expectedKey string
		expected    string
	}{
		{
			name: "instance", resource: domain.TaskResource{Type: "huawei_swr_instance", ExternalID: "swr-instance/swr-instance-1"},
			action: "huawei.swr.instance.get", expectedKey: "id", expected: "swr-instance-1",
		},
		{
			name: "namespace", resource: domain.TaskResource{Type: "huawei_swr_namespace", ExternalID: "swr-namespace/swr-instance-1/team-a"},
			action: "huawei.swr.namespace.get", expectedKey: "name", expected: "team-a",
		},
		{
			name: "repository", resource: domain.TaskResource{Type: "huawei_swr_repository", ExternalID: "swr-repository/swr-instance-1/team-a/api"},
			action: "huawei.swr.repository.get", expectedKey: "name", expected: "api",
		},
		{
			name: "tag", resource: domain.TaskResource{Type: "huawei_swr_tag", ExternalID: "swr-tag/swr-instance-1/team-a/api/v1.2.3"},
			action: "huawei.swr.tag.get", expectedKey: "digest", expected: "sha256:abcdef0123456789",
		},
		{
			name: "tag-region-scoped", resource: domain.TaskResource{Type: "huawei_swr_tag", ExternalID: "swr-tag/cn-north-4/swr-instance-1/team-a/api/v1.2.3"},
			action: "huawei.swr.tag.get", expectedKey: "digest", expected: "sha256:abcdef0123456789",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
				Resource: test.resource, Action: domain.TaskAction{Name: test.action}, Grant: grant,
			})
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%v", result[test.expectedKey]) != test.expected {
				t.Fatalf("unexpected SWR %s result: %#v", test.name, result)
			}
		})
	}

	runner.allowedServices = map[string]bool{"ecs": true}
	if _, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: tests[0].resource, Action: domain.TaskAction{Name: tests[0].action}, Grant: grant,
	}); err == nil || !strings.Contains(err.Error(), "outside the Integration service scope") {
		t.Fatalf("SWR action bypassed the Integration service scope: %v", err)
	}
}

func TestHuaweiCloudRunnerEscapesSWRRepositoryNamesAndUsesArtifactDigest(t *testing.T) {
	const escapedRepository = "apps%2Fapi"
	const digest = "sha256:abcdef0123456789"
	var listTagsPath, artifactPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		escapedPath := req.URL.EscapedPath()
		switch {
		case req.URL.Path == "/v3/projects":
			writeTestJSON(w, map[string]any{"projects": []any{map[string]any{"id": "proj-123", "name": "cn-north-4"}}})
		case req.URL.Path == "/v2/proj-123/instances":
			writeTestJSON(w, map[string]any{"instances": []any{map[string]any{"id": "swr-instance-1", "name": "registry-prod"}}, "total": 1})
		case req.URL.Path == "/v2/proj-123/instances/swr-instance-1/namespaces":
			writeTestJSON(w, map[string]any{"namespaces": []any{map[string]any{"name": "team-a", "namespace_id": 11}}, "total": 1})
		case req.URL.Path == "/v2/proj-123/instances/swr-instance-1/namespaces/team-a/repositories":
			writeTestJSON(w, map[string]any{"repositories": []any{map[string]any{"id": 21, "name": "apps/api", "namespace_name": "team-a", "tag_count": 1}}, "total": 1})
		case escapedPath == "/v2/instances/swr-instance-1/namespaces/team-a/repositories/"+escapedRepository+"/tags":
			listTagsPath = escapedPath
			writeTestJSON(w, map[string]any{"tags": []any{map[string]any{"name": "v1.2.3", "id": 31, "artifact_id": 41, "digest": digest}}, "total": 1})
		case escapedPath == "/v2/proj-123/instances/swr-instance-1/namespaces/team-a/repositories/"+escapedRepository+"/artifacts/"+url.PathEscape(digest):
			artifactPath = escapedPath
			writeTestJSON(w, map[string]any{"name": "v1.2.3", "digest": digest})
		case escapedPath == "/v2/proj-123/instances/swr-instance-1/namespaces/team-a/repositories/"+escapedRepository && req.Method == http.MethodGet:
			writeTestJSON(w, map[string]any{"id": 21, "name": "apps/api"})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"swr"}})
	resources, err := runner.discoverSWR(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	repositoryExternalID := "swr-repository/swr-instance-1/team-a/" + escapedRepository
	tagExternalID := "swr-tag/swr-instance-1/team-a/" + escapedRepository + "/v1.2.3"
	foundRepository, foundTag := false, false
	for _, resource := range resources {
		foundRepository = foundRepository || resource.ExternalID == repositoryExternalID
		foundTag = foundTag || resource.ExternalID == tagExternalID
	}
	if !foundRepository || !foundTag {
		t.Fatalf("escaped SWR identities were not discovered: repository=%v tag=%v", foundRepository, foundTag)
	}
	grant := domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)}
	if _, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_swr_repository", ExternalID: repositoryExternalID},
		Action:   domain.TaskAction{Name: "huawei.swr.repository.get"}, Grant: grant,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_swr_tag", ExternalID: tagExternalID},
		Action:   domain.TaskAction{Name: "huawei.swr.tag.get"}, Grant: grant,
	}); err != nil {
		t.Fatal(err)
	}
	if want := "/v2/instances/swr-instance-1/namespaces/team-a/repositories/" + escapedRepository + "/tags"; listTagsPath != want {
		t.Fatalf("unexpected list tags path: got %q want %q", listTagsPath, want)
	}
	if strings.Contains(listTagsPath, "proj-123") {
		t.Fatalf("ListInstanceTags must not include project_id: %q", listTagsPath)
	}
	if want := "/v2/proj-123/instances/swr-instance-1/namespaces/team-a/repositories/" + escapedRepository + "/artifacts/" + url.PathEscape(digest); artifactPath != want {
		t.Fatalf("ShowInstanceArtifact must use the digest reference: got %q want %q", artifactPath, want)
	}
	if strings.Contains(artifactPath, "v1.2.3") {
		t.Fatalf("ShowInstanceArtifact used the tag instead of the digest: %q", artifactPath)
	}
}

func TestHuaweiCloudRunnerRejectsMismatchedActionAndBadIdentity(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowAll: true})
	grant := domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)}

	// Action/resource type mismatch must fail before any API call.
	if _, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_rds_instance", ExternalID: "rds/rds-uuid-1"},
		Action:   domain.TaskAction{Name: "huawei.ecs.get"}, Grant: grant,
	}); err == nil {
		t.Fatal("mismatched action/resource was accepted")
	}
	// Path-injection attempts in the external id must be rejected.
	if _, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_ecs_instance", ExternalID: "ecs/../admin"},
		Action:   domain.TaskAction{Name: "huawei.ecs.get"}, Grant: grant,
	}); err == nil {
		t.Fatal("path-injection external id was accepted")
	}
	if _, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_swr_repository", ExternalID: "swr-repository/swr-instance-1/team-a/../admin"},
		Action:   domain.TaskAction{Name: "huawei.swr.repository.get"}, Grant: grant,
	}); err == nil {
		t.Fatal("SWR path-injection external id was accepted")
	}
	if _, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_swr_repository", ExternalID: "swr-repository/swr-instance-1/team-a/%2e%2e%2fadmin"},
		Action:   domain.TaskAction{Name: "huawei.swr.repository.get"}, Grant: grant,
	}); err == nil {
		t.Fatal("SWR percent-encoded path-injection external id was accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("Huawei Cloud was contacted before validation")
	}
}

func TestHuaweiCloudRunnerDiscoversAllRegionsAndAppliesDenyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.URL.Path == "/v3/projects" && req.URL.Query().Get("name") == "":
			_, _ = w.Write([]byte(`{"projects":[{"id":"p-east","name":"cn-east-3"},{"id":"p-south","name":"cn-south-1"},{"id":"domain","name":"default"}]}`))
		case req.URL.Path == "/v3/projects":
			region := req.URL.Query().Get("name")
			_, _ = w.Write([]byte(`{"projects":[{"id":"project-` + region + `","name":"` + region + `"}]}`))
		case strings.Contains(req.URL.Path, "/cloudservers/detail"):
			_, _ = w.Write([]byte(`{"servers":[{"id":"ecs-1","name":"web","status":"ACTIVE"}]}`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{
		AllowedServices: []string{"ecs"}, RegionMode: "all", DeniedRegions: []string{"cn-south-1"},
	})
	resources := runner.ListResources(context.Background())
	encoded, _ := json.Marshal(resources)
	text := string(encoded)
	if !strings.Contains(text, "account/cn-east-3") || !strings.Contains(text, "ecs/cn-east-3/ecs-1") {
		t.Fatalf("multi-region discovery did not produce region-scoped resources: %s", text)
	}
	if strings.Contains(text, "cn-south-1") {
		t.Fatalf("denied region was discovered: %s", text)
	}
}

func TestHuaweiCloudRunnerCreatesECSWithReservedTagsAndIdempotency(t *testing.T) {
	var requestBody map[string]any
	var tagBody map[string]any
	var clientToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/v3/projects" {
			_, _ = w.Write([]byte(`{"projects":[{"id":"cloud-project","name":"cn-north-4"}]}`))
			return
		}
		if req.Method == http.MethodPost && req.URL.Path == "/v1/cloud-project/cloudservers" {
			clientToken = req.URL.Query().Get("client_token")
			if err := json.NewDecoder(req.Body).Decode(&requestBody); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"job_id":"job-1"}`))
			return
		}
		if req.Method == http.MethodGet && req.URL.Path == "/v1/cloud-project/jobs/job-1" {
			writeTestJSON(w, map[string]any{"status": "SUCCESS"})
			return
		}
		if req.Method == http.MethodPost && req.URL.Path == "/v1/cloud-project/cloudservers/ecs-1/tags/action" {
			if err := json.NewDecoder(req.Body).Decode(&tagBody); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if req.Method == http.MethodGet && req.URL.Path == "/v1/cloud-project/cloudservers/detail" {
			if req.URL.Query().Get("name") != "order-api-01" {
				t.Fatalf("created ECS lookup did not use the exact requested name: %s", req.URL.RawQuery)
			}
			writeTestJSON(w, map[string]any{"servers": []any{map[string]any{"id": "ecs-1", "name": "order-api-01", "status": "ACTIVE"}}})
			return
		}
		if req.Method == http.MethodGet && req.URL.Path == "/v1/cloud-project/cloudservers/ecs-1" {
			writeTestJSON(w, map[string]any{"server": map[string]any{"id": "ecs-1", "name": "order-api-01", "status": "ACTIVE"}})
			return
		}
		http.NotFound(w, req)
	}))
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"ecs"}})
	projectID := "11111111-1111-4111-8111-111111111111"
	task := domain.TaskEnvelope{
		ExecutionID: "22222222-2222-4222-8222-222222222222",
		Resource:    domain.TaskResource{Type: "huawei_cloud_account", ExternalID: "account/cn-north-4"},
		Action:      domain.TaskAction{Name: "huawei.ecs.create", Version: 1},
		Parameters: map[string]any{
			"projectId": projectID, "region": "cn-north-4", "name": "order-api-01", "flavor": "s6.large.2",
			"imageId": "image-1", "vpcId": "vpc-1", "subnetId": "subnet-1", "systemDiskSize": float64(80),
			"securityGroupIds": []any{"sg-1"}, "tags": map[string]any{"service": "order-api"},
			"_pamContext": map[string]any{"projectId": projectID, "projectName": "订单", "environment": "production", "createdBy": "agent-1", "integrationId": "ig-1"},
		},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if result["job_id"] != "job-1" || clientToken != task.ExecutionID {
		t.Fatalf("unexpected create result/token: %#v %q", result, clientToken)
	}
	encoded, _ := json.Marshal(tagBody)
	bodyText := string(encoded)
	for _, expected := range []string{"pam-managed-by", "pam-project-id", projectID, "pam-execution-id", task.ExecutionID, "service", "order-api"} {
		if !strings.Contains(bodyText, expected) {
			t.Fatalf("create request is missing %q: %s", expected, bodyText)
		}
	}
	serverPayload, ok := requestBody["server"].(map[string]any)
	if !ok {
		t.Fatalf("create request is missing server payload: %#v", requestBody)
	}
	if _, exists := serverPayload["tags"]; exists {
		t.Fatalf("create request must attach governance tags after the server job succeeds: %#v", serverPayload["tags"])
	}
	rawTags, ok := tagBody["tags"].([]any)
	if !ok || len(rawTags) == 0 {
		t.Fatalf("create tags must be a non-empty object array: %#v", tagBody["tags"])
	}
	for _, rawTag := range rawTags {
		tag, ok := rawTag.(map[string]any)
		if !ok || tag["key"] == nil || tag["value"] == nil {
			t.Fatalf("create tag is not a {key,value} object: %#v", rawTag)
		}
	}

	task.Parameters["tags"] = map[string]any{"pam-project-id": "spoofed"}
	if _, _, err := runner.Execute(context.Background(), task); err == nil || !strings.Contains(err.Error(), "reserved pam-*") {
		t.Fatalf("reserved tag override was not rejected: %v", err)
	}
}

func TestHuaweiCloudRunnerExecutesECSLifecycleAndVerifiesFinalState(t *testing.T) {
	for _, tc := range []struct {
		action, operation, status string
	}{
		{"huawei.ecs.start", "os-start", "ACTIVE"},
		{"huawei.ecs.stop", "os-stop", "SHUTOFF"},
		{"huawei.ecs.reboot", "reboot", "ACTIVE"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			var actionBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case req.URL.Path == "/v3/projects":
					_, _ = w.Write([]byte(`{"projects":[{"id":"cloud-project","name":"cn-north-4"}]}`))
				case req.Method == http.MethodPost && req.URL.Path == "/v1/cloud-project/cloudservers/action":
					if err := json.NewDecoder(req.Body).Decode(&actionBody); err != nil {
						t.Fatal(err)
					}
					_, _ = w.Write([]byte(`{"job_id":"job-1"}`))
				case req.Method == http.MethodGet && req.URL.Path == "/v1/cloud-project/jobs/job-1":
					writeTestJSON(w, map[string]any{"status": "SUCCESS"})
				case req.Method == http.MethodGet && req.URL.Path == "/v1/cloud-project/cloudservers/ecs-1":
					writeTestJSON(w, map[string]any{"server": map[string]any{"id": "ecs-1", "status": tc.status}})
				default:
					http.NotFound(w, req)
				}
			}))
			defer server.Close()
			runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"ecs"}})
			result, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
				Resource: domain.TaskResource{Type: "huawei_ecs_instance", ExternalID: "ecs/ecs-1"},
				Action:   domain.TaskAction{Name: tc.action}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
			})
			if err != nil {
				t.Fatal(err)
			}
			if actionBody[tc.operation] == nil || result["targetStatus"] != tc.status {
				t.Fatalf("unexpected lifecycle request/result: %#v %#v", actionBody, result)
			}
		})
	}
}

func TestHuaweiCloudRunnerUpdatesECSMetadataWithoutAllowingReservedTagOverrides(t *testing.T) {
	var tagBody, descriptionBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.URL.Path == "/v3/projects":
			_, _ = w.Write([]byte(`{"projects":[{"id":"cloud-project","name":"cn-north-4"}]}`))
		case req.Method == http.MethodPost && req.URL.Path == "/v1/cloud-project/cloudservers/ecs-1/tags/action":
			if err := json.NewDecoder(req.Body).Decode(&tagBody); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodPut && req.URL.Path == "/v1/cloud-project/cloudservers/ecs-1":
			if err := json.NewDecoder(req.Body).Decode(&descriptionBody); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodGet && req.URL.Path == "/v1/cloud-project/cloudservers/ecs-1":
			writeTestJSON(w, map[string]any{"server": map[string]any{"id": "ecs-1", "description": "订单服务"}})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"ecs"}})
	projectID := "11111111-1111-4111-8111-111111111111"
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_ecs_instance", ExternalID: "ecs/ecs-1"},
		Action:   domain.TaskAction{Name: "huawei.ecs.update_metadata"},
		Parameters: map[string]any{
			"description": "订单服务", "tags": map[string]any{"service": "orders"},
			"_pamContext": map[string]any{"projectId": projectID, "projectName": "订单", "environment": "production", "integrationId": "ig-1"},
		},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(tagBody)
	text := string(encoded)
	for _, expected := range []string{"pam-managed-by", "pam-project-id", projectID, "pam-project-name", "service", "orders"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("tag update is missing %q: %s", expected, text)
		}
	}
	if descriptionBody["server"] == nil {
		t.Fatalf("description update missing server body: %#v", descriptionBody)
	}

	task.Parameters["tags"] = map[string]any{"pam-project-id": "spoofed"}
	if _, _, err := runner.Execute(context.Background(), task); err == nil || !strings.Contains(err.Error(), "reserved pam-*") {
		t.Fatalf("reserved tag override was not rejected: %v", err)
	}
}

func TestHuaweiCloudRunnerRejectsCrossRegionECSLifecycleTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v3/projects" {
			writeTestJSON(w, map[string]any{"projects": []any{map[string]any{"id": "north", "name": "cn-north-4"}}})
			return
		}
		http.Error(w, "unexpected cloud request", http.StatusInternalServerError)
	}))
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"ecs"}, RegionMode: "allowlist", Regions: []string{"cn-north-4"}})
	_, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_ecs_instance", ExternalID: "ecs/cn-east-3/ecs-1"},
		Action:   domain.TaskAction{Name: "huawei.ecs.stop"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	})
	if err == nil || !strings.Contains(err.Error(), "outside the Integration scope") {
		t.Fatalf("cross-region target returned unexpected error: %v", err)
	}
	singleRunner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"ecs"}})
	_, _, err = singleRunner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_ecs_instance", ExternalID: "ecs/cn-east-3/ecs-1"},
		Action:   domain.TaskAction{Name: "huawei.ecs.start"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	})
	if err == nil || !strings.Contains(err.Error(), "not authorized region cn-north-4") {
		t.Fatalf("single-region target returned unexpected error: %v", err)
	}
}

func TestHuaweiCloudRunnerSurfacesLifecycleIAMDenial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v3/projects" {
			writeTestJSON(w, map[string]any{"projects": []any{map[string]any{"id": "cloud-project", "name": "cn-north-4"}}})
			return
		}
		if req.URL.Path == "/v1/cloud-project/cloudservers/action" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error_code":"Ecs.0001","error_msg":"permission denied"}`))
			return
		}
		http.NotFound(w, req)
	}))
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"ecs"}})
	_, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_ecs_instance", ExternalID: "ecs/ecs-1"},
		Action:   domain.TaskAction{Name: "huawei.ecs.stop"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	})
	if err == nil || !strings.Contains(err.Error(), "Huawei Cloud returned 403") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("IAM denial was not surfaced clearly: %v", err)
	}
}

func TestHuaweiCloudRunnerDeletesOnlyConfirmedECSAndVerifiesRemoval(t *testing.T) {
	var deleteBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.URL.Path == "/v3/projects":
			writeTestJSON(w, map[string]any{"projects": []any{map[string]any{"id": "cloud-project", "name": "cn-north-4"}}})
		case req.Method == http.MethodPost && req.URL.Path == "/v1/cloud-project/cloudservers/delete":
			if err := json.NewDecoder(req.Body).Decode(&deleteBody); err != nil {
				t.Fatalf("decode delete request: %v", err)
			}
			writeTestJSON(w, map[string]any{"job_id": "delete-job-1"})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"ecs"}})
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "huawei_ecs_instance", ExternalID: "ecs/ecs-1", Name: "warden-e2e-7211"},
		Action:   domain.TaskAction{Name: "huawei.ecs.delete"},
		Parameters: map[string]any{
			"confirmServerName": "wrong-name", "deletePublicIP": true, "deleteVolumes": true,
		},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err == nil || !strings.Contains(err.Error(), "exactly match") {
		t.Fatalf("wrong delete confirmation was accepted: %v", err)
	}
	task.Parameters["confirmServerName"] = task.Resource.Name
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	servers, _ := deleteBody["servers"].([]any)
	serverEntry, _ := servers[0].(map[string]any)
	if result["deletionSubmitted"] != true || deleteBody["delete_publicip"] != true || deleteBody["delete_volume"] != true || serverEntry["id"] != "ecs-1" {
		t.Fatalf("unexpected delete result/body: %#v %#v", result, deleteBody)
	}
}

func TestParseHuaweiCloudCredential(t *testing.T) {
	if !IsUnknownOutcome("huawei.ecs.create", errHuaweiCreatePartial) {
		t.Fatal("partially completed Huawei ECS creation must be treated as UNKNOWN to prevent automatic retry")
	}
	ak, sk, err := ParseHuaweiCloudCredential(`{"accessKey":"AKIA","secretKey":"SECRET"}`)
	if err != nil || ak != "AKIA" || sk != "SECRET" {
		t.Fatalf("JSON credential parse failed: %v %s %s", err, ak, sk)
	}
	ak, sk, err = ParseHuaweiCloudCredential("AKIA:SECRET:WITH:COLONS")
	if err != nil || ak != "AKIA" || sk != "SECRET:WITH:COLONS" {
		t.Fatalf("compact credential parse failed: %v %s %s", err, ak, sk)
	}
	if _, _, err := ParseHuaweiCloudCredential("no-separator"); err == nil {
		t.Fatal("invalid credential was accepted")
	}
}

func TestHuaweiCloudOBSObjectListAndMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case req.URL.Path == "/bucket-a" && req.Method == http.MethodGet && strings.Contains(req.URL.RawQuery, "max-keys"):
			if !strings.Contains(req.URL.RawQuery, "prefix=logs") {
				t.Fatalf("object list missing prefix param: %q", req.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult><Name>bucket-a</Name><IsTruncated>false</IsTruncated>
<Contents><Key>logs/app.log</Key><LastModified>2026-01-01T00:00:00.000Z</LastModified><ETag>"abc123"</ETag><Size>1024</Size></Contents>
<Contents><Key>logs/error.log</Key><LastModified>2026-01-02T00:00:00.000Z</LastModified><ETag>"def456"</ETag><Size>2048</Size></Contents>
</ListBucketResult>`))
		case req.URL.Path == "/bucket-a/logs/app.log" && req.Method == http.MethodHead:
			w.Header().Set("Content-Length", "1024")
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Last-Modified", "Wed, 01 Jan 2026 00:00:00 GMT")
			w.Header().Set("ETag", `"abc123"`)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"obs"}})

	listResult, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource:   domain.TaskResource{Type: "huawei_obs_bucket", ExternalID: "obs/bucket-a", Name: "bucket-a"},
		Action:     domain.TaskAction{Name: "huawei.obs.object.list"},
		Parameters: map[string]any{"prefix": "logs", "maxKeys": float64(100)},
		Grant:      domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	objects, _ := listResult["objects"].([]map[string]any)
	if fmt.Sprintf("%v", listResult["count"]) != "2" || len(objects) != 2 || objects[0]["key"] != "logs/app.log" || fmt.Sprintf("%v", objects[1]["size"]) != "2048" {
		t.Fatalf("unexpected OBS object list result: %#v", listResult)
	}

	metaResult, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource:   domain.TaskResource{Type: "huawei_obs_bucket", ExternalID: "obs/bucket-a", Name: "bucket-a"},
		Action:     domain.TaskAction{Name: "huawei.obs.object.metadata.get"},
		Parameters: map[string]any{"objectKey": "logs/app.log"},
		Grant:      domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if metaResult["objectKey"] != "logs/app.log" || metaResult["size"] != "1024" || metaResult["contentType"] != "text/plain" || metaResult["etag"] != "abc123" {
		t.Fatalf("unexpected OBS object metadata result: %#v", metaResult)
	}
}

func TestHuaweiCloudOBSObjectDeleteRequiresExactConfirmation(t *testing.T) {
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/bucket-a/logs/app.log" && req.Method == http.MethodDelete {
			deleted = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, req)
	}))
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"obs"}})
	task := domain.TaskEnvelope{
		Resource:   domain.TaskResource{Type: "huawei_obs_bucket", ExternalID: "obs/bucket-a", Name: "bucket-a"},
		Action:     domain.TaskAction{Name: "huawei.obs.object.delete"},
		Parameters: map[string]any{"objectKey": "logs/app.log", "confirmObjectKey": "logs/wrong"},
		Grant:      domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err == nil || !strings.Contains(err.Error(), "exact objectKey confirmation") {
		t.Fatalf("wrong object key confirmation was accepted: %v", err)
	}
	if deleted {
		t.Fatal("object was deleted without matching confirmation")
	}
	task.Parameters["confirmObjectKey"] = "logs/app.log"
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if !deleted || result["deletedObjectKey"] != "logs/app.log" {
		t.Fatalf("unexpected OBS object delete result: %#v deleted=%v", result, deleted)
	}
}

func TestHuaweiCloudSnapshotResourceOBSBucket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if req.URL.Path == "/bucket-a" && req.URL.RawQuery == "storageinfo" {
			_, _ = w.Write([]byte(`<GetBucketStorageInfoResult><Size>2048</Size><ObjectNumber>7</ObjectNumber></GetBucketStorageInfoResult>`))
			return
		}
		http.NotFound(w, req)
	}))
	defer server.Close()
	runner := newHuaweiTestRunner(t, server, HuaweiCloudConfig{AllowedServices: []string{"obs"}})

	found, ok := runner.SnapshotResource(context.Background(), domain.TaskResource{Type: "huawei_obs_bucket", ExternalID: "obs/bucket-a", Name: "bucket-a"})
	if !ok || found.ResourceType != "huawei_obs_bucket" || found.Name != "bucket-a" {
		t.Fatalf("OBS bucket snapshot failed: %#v ok=%v", found, ok)
	}
	if _, ok := runner.SnapshotResource(context.Background(), domain.TaskResource{Type: "huawei_obs_bucket", ExternalID: "obs/missing-bucket", Name: "missing"}); ok {
		t.Fatalf("missing OBS bucket snapshot should not be found")
	}
}
