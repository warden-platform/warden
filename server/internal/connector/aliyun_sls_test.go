package connector

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestAliyunSLSRunnerDiscoversAndQueriesBoundLogstore(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.Header.Get("Authorization"), "LOG test-ak:") {
			t.Fatalf("request was not signed: %s", request.Header.Get("Authorization"))
		}
		body := `{"logstores":["backend","frontend"],"count":2,"total":2}`
		if strings.HasPrefix(request.URL.Path, "/logstores/backend") {
			body = `[{"__time__":1,"level":"ERROR"}]`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	runner, err := NewAliyunSLSRunner(AliyunSLSConfig{Endpoint: "https://aipc-log.cn-hangzhou.log.aliyuncs.com", AccessKey: "test-ak", SecretKey: "test-sk", IntegrationID: "ig-sls", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	resources := runner.ListResources(context.Background())
	if len(resources) != 3 || resources[1].ExternalID != "logstore/backend" {
		t.Fatalf("unexpected discovery: %#v", resources)
	}
	now := time.Now().Unix()
	task := domain.TaskEnvelope{Resource: domain.TaskResource{Type: "aliyun_sls_logstore", ExternalID: "logstore/backend", Name: "backend", IntegrationID: "ig-sls"}, Action: domain.TaskAction{Name: "aliyun.sls.logs.query"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)}, Parameters: map[string]any{"from": float64(now - 300), "to": float64(now), "query": "level:ERROR", "line": float64(20)}}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if result["logstore"] != "backend" {
		t.Fatalf("unexpected query result: %#v", result)
	}
}

func TestAliyunSLSRunnerSignsDecodedCanonicalQueryValues(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{name: "colon", query: "level:ERROR"},
		{name: "spaces and operators", query: `ERROR OR Exception OR status >= 500`},
		{name: "quotes parentheses unicode and plus", query: `(message:"请求失败" OR path:/v1/a+b)`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				expected := aliyunSLSAuthorizationForTest(request, "test-ak", "test-sk")
				if actual := request.Header.Get("Authorization"); actual != expected {
					t.Fatalf("signature mismatch for %q: got %q, want %q", test.query, actual, expected)
				}
				if got := request.URL.Query().Get("query"); got != test.query {
					t.Fatalf("query changed in transit: got %q, want %q", got, test.query)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`))}, nil
			})}
			runner, err := NewAliyunSLSRunner(AliyunSLSConfig{
				Endpoint: "https://aipc-log.cn-hangzhou.log.aliyuncs.com", AccessKey: "test-ak", SecretKey: "test-sk",
				IntegrationID: "ig-sls", HTTPClient: client,
			})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().Unix()
			_, _, err = runner.Execute(context.Background(), domain.TaskEnvelope{
				Resource: domain.TaskResource{Type: "aliyun_sls_logstore", ExternalID: "logstore/backend", Name: "backend", IntegrationID: "ig-sls"},
				Action:   domain.TaskAction{Name: "aliyun.sls.logs.query"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
				Parameters: map[string]any{"from": float64(now - 300), "to": float64(now), "query": test.query, "line": float64(20)},
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// aliyunSLSAuthorizationForTest independently follows the official SDK's
// SignerV1 algorithm so the test does not reuse production canonicalization.
func aliyunSLSAuthorizationForTest(request *http.Request, accessKey, secretKey string) string {
	keys := make([]string, 0)
	headers := make(map[string]string)
	for key, values := range request.Header {
		key = strings.ToLower(strings.TrimSpace(key))
		if strings.HasPrefix(key, "x-log-") || strings.HasPrefix(key, "x-acs-") {
			headers[key] = strings.TrimSpace(strings.Join(values, ","))
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	canonicalHeaders := make([]string, 0, len(keys))
	for _, key := range keys {
		canonicalHeaders = append(canonicalHeaders, key+":"+headers[key])
	}
	canonicalResource := request.URL.EscapedPath()
	query := request.URL.Query()
	if len(query) > 0 {
		queryKeys := make([]string, 0, len(query))
		for key := range query {
			queryKeys = append(queryKeys, key)
		}
		sort.Strings(queryKeys)
		parts := make([]string, 0, len(queryKeys))
		for _, key := range queryKeys {
			var part strings.Builder
			for _, value := range query[key] {
				part.WriteString(key + "=" + value)
			}
			parts = append(parts, part.String())
		}
		canonicalResource += "?" + strings.Join(parts, "&")
	}
	stringToSign := request.Method + "\n\n\n" + request.Header.Get("Date") + "\n" + strings.Join(canonicalHeaders, "\n") + "\n" + canonicalResource
	mac := hmac.New(sha1.New, []byte(secretKey))
	_, _ = mac.Write([]byte(stringToSign))
	return "LOG " + accessKey + ":" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestAliyunSLSRunnerDiscoversAuthorizedProjectsAcrossRegions(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"projects":[],"count":0,"total":0}`
		switch request.URL.Hostname() {
		case "cn-hangzhou.log.aliyuncs.com":
			body = `{"projects":[{"projectName":"aipc-log"},{"projectName":"test-aipc-log"}],"count":2,"total":2}`
		case "cn-shanghai.log.aliyuncs.com":
			body = `{"projects":[{"projectName":"other-log"}],"count":1,"total":1}`
		default:
			body = `{"logstores":["backend"],"count":1,"total":1}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	runner, err := NewAliyunSLSRunner(AliyunSLSConfig{
		Endpoint: "cn-hangzhou", RegionMode: "allowlist", Regions: []string{"cn-hangzhou", "cn-shanghai"},
		AccessKey: "test-ak", SecretKey: "test-sk", IntegrationID: "ig-account", AllowAll: true, HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	resources := runner.ListResources(context.Background())
	if len(resources) != 6 {
		t.Fatalf("expected three projects and three logstores, got %#v", resources)
	}
	found := false
	for _, resource := range resources {
		if resource.ExternalID == "logstore/cn-shanghai/other-log/backend" && resource.Metadata["region"] == "cn-shanghai" {
			found = true
		}
	}
	if !found {
		t.Fatalf("multi-region Logstore missing from discovery: %#v", resources)
	}
	now := time.Now().Unix()
	result, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "aliyun_sls_logstore", ExternalID: "logstore/cn-shanghai/other-log/backend", Name: "backend", IntegrationID: "ig-account"},
		Action:   domain.TaskAction{Name: "aliyun.sls.logs.query"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
		Parameters: map[string]any{"from": float64(now - 300), "to": float64(now), "line": float64(20)},
	})
	if err != nil || result["region"] != "cn-shanghai" || result["project"] != "other-log" {
		t.Fatalf("multi-region query was not bound to its discovered project: result=%#v err=%v", result, err)
	}
}

func TestAliyunSLSRunnerNarrowsProjectAndRegionScope(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"projects":[{"projectName":"aipc-log"},{"projectName":"test-aipc-log"}],"count":2,"total":2}`
		if strings.Contains(request.URL.Hostname(), "test-aipc-log.") {
			body = `{"logstores":["backend"],"count":1,"total":1}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	runner, err := NewAliyunSLSRunner(AliyunSLSConfig{
		Endpoint: "cn-hangzhou", RegionMode: "allowlist", Regions: []string{"cn-hangzhou", "cn-shanghai"}, DeniedRegions: []string{"cn-shanghai"},
		AllowedProjects: []string{"test-aipc-log"}, AccessKey: "test-ak", SecretKey: "test-sk", IntegrationID: "ig-scoped", HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	resources := runner.ListResources(context.Background())
	if len(resources) != 2 || resources[0].Name != "test-aipc-log" || resources[1].ExternalID != "logstore/cn-hangzhou/test-aipc-log/backend" {
		t.Fatalf("project/region scope was not enforced: %#v", resources)
	}
}

func TestAliyunSLSRunnerCachesReachableRegionsAndRefreshesCatalog(t *testing.T) {
	var regionalRequests atomic.Int64
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		host := request.URL.Hostname()
		if host == "aipc-log.cn-hangzhou.log.aliyuncs.com" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"logstores":["backend"],"total":1}`))}, nil
		}
		regionalRequests.Add(1)
		if host == "cn-hangzhou.log.aliyuncs.com" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"projects":[{"projectName":"aipc-log"}],"total":1}`))}, nil
		}
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"errorCode":"Unauthorized"}`))}, nil
	})}
	runner, err := NewAliyunSLSRunner(AliyunSLSConfig{Endpoint: "cn-hangzhou", RegionMode: "all", AllowAll: true, AccessKey: "test-ak", SecretKey: "test-sk", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	runner.ListResources(context.Background())
	first := regionalRequests.Load()
	runner.ListResources(context.Background())
	if got := regionalRequests.Load() - first; got != 1 {
		t.Fatalf("expected cached discovery to probe one reachable region, got %d", got)
	}
	runner.cacheMu.Lock()
	runner.regionCacheAt = time.Now().Add(-11 * time.Minute)
	runner.cacheMu.Unlock()
	beforeRefresh := regionalRequests.Load()
	runner.ListResources(context.Background())
	if got := regionalRequests.Load() - beforeRefresh; got != int64(len(aliyunSLSRegions)) {
		t.Fatalf("expected expired cache to rescan %d regions, got %d", len(aliyunSLSRegions), got)
	}
}
