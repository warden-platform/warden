package connector

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

const aliyunSLSMaxResponse = 4 << 20

var aliyunSLSRegionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z0-9]+)+$`)
var aliyunSLSProjectHostPattern = regexp.MustCompile(`^([a-z0-9][a-z0-9-]{1,61}[a-z0-9])\.([a-z0-9-]+)\.log\.aliyuncs\.com$`)
var aliyunSLSProjectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
var aliyunSLSLogstorePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)

// SLS has no global ListProject endpoint. Account-wide discovery probes the
// official public endpoint of each supported region and keeps only projects
// the RAM identity can list. Keep this catalog explicit and reviewable.
var aliyunSLSRegions = []string{
	"cn-qingdao", "cn-beijing", "cn-zhangjiakou", "cn-huhehaote", "cn-wulanchabu",
	"cn-hangzhou", "cn-shanghai", "cn-nanjing", "cn-fuzhou", "cn-shenzhen",
	"cn-heyuan", "cn-guangzhou", "cn-chengdu", "cn-hongkong",
	"ap-northeast-1", "ap-southeast-1", "ap-southeast-2", "ap-southeast-3",
	"ap-southeast-5", "ap-southeast-6", "ap-southeast-7", "us-west-1", "us-east-1",
	"eu-central-1", "eu-west-1", "me-east-1", "me-central-1",
}

var AliyunSLSResourceTypes = []string{"aliyun_sls_account", "aliyun_sls_logstore"}
var AliyunSLSCapabilities = []string{"aliyun.sls.account.get@v1", "aliyun.sls.logs.query@v1"}

type AliyunSLSConfig struct {
	Endpoint, AccessKey, SecretKey, IntegrationID string
	RegionMode                                    string
	Regions, DeniedRegions, AllowedProjects       []string
	AllowAll                                      bool
	HTTPClient                                    *http.Client
}

type AliyunSLSRunner struct {
	seedRegion, legacyProject           string
	regionMode                          string
	regions                             []string
	deniedRegions, allowedProjects      map[string]bool
	allowAll                            bool
	accessKey, secretKey, integrationID string
	http                                *http.Client
	cacheMu                             sync.Mutex
	cachedRegions                       []string
	regionCacheAt                       time.Time
}

func ParseAliyunSLSCredential(raw string) (string, string, error) {
	var value struct {
		AccessKey string `json:"accessKey"`
		SecretKey string `json:"secretKey"`
	}
	if json.Unmarshal([]byte(raw), &value) == nil && strings.TrimSpace(value.AccessKey) != "" && strings.TrimSpace(value.SecretKey) != "" {
		return strings.TrimSpace(value.AccessKey), strings.TrimSpace(value.SecretKey), nil
	}
	accessKey, secretKey, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok || strings.TrimSpace(accessKey) == "" || strings.TrimSpace(secretKey) == "" {
		return "", "", fmt.Errorf("Aliyun SLS credential must be AK:SK or JSON")
	}
	return strings.TrimSpace(accessKey), strings.TrimSpace(secretKey), nil
}

func NewAliyunSLSRunner(config AliyunSLSConfig) (*AliyunSLSRunner, error) {
	seedRegion, legacyProject, err := parseAliyunSLSEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.AccessKey) == "" || strings.TrimSpace(config.SecretKey) == "" {
		return nil, fmt.Errorf("Aliyun SLS AK/SK is required")
	}
	regionMode := strings.ToLower(strings.TrimSpace(config.RegionMode))
	if regionMode == "" {
		regionMode = "single"
	}
	if regionMode != "single" && regionMode != "all" && regionMode != "allowlist" {
		return nil, fmt.Errorf("Aliyun SLS region mode must be single, all, or allowlist")
	}
	regions := normalizeAliyunRegions(config.Regions)
	if regionMode == "allowlist" && len(regions) == 0 {
		return nil, fmt.Errorf("Aliyun SLS allowlist region mode requires at least one region")
	}
	denied := make(map[string]bool, len(config.DeniedRegions))
	for _, region := range normalizeAliyunRegions(config.DeniedRegions) {
		denied[region] = true
	}
	projects := make(map[string]bool, len(config.AllowedProjects))
	for _, project := range config.AllowedProjects {
		project = strings.TrimSpace(project)
		if !aliyunSLSProjectPattern.MatchString(project) {
			return nil, fmt.Errorf("invalid Aliyun SLS project scope %q", project)
		}
		projects[project] = true
	}
	allowAll := config.AllowAll
	if !allowAll && len(projects) == 0 {
		if legacyProject == "" {
			return nil, fmt.Errorf("Aliyun SLS requires allowAll or an allowed project scope")
		}
		// Legacy project-endpoint integrations predate account-level scope.
		allowAll = true
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &AliyunSLSRunner{
		seedRegion: seedRegion, legacyProject: legacyProject, regionMode: regionMode, regions: regions,
		deniedRegions: denied, allowedProjects: projects, allowAll: allowAll,
		accessKey: config.AccessKey, secretKey: config.SecretKey, integrationID: config.IntegrationID, http: client,
	}, nil
}

func parseAliyunSLSEndpoint(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if aliyunSLSRegionPattern.MatchString(value) {
		return value, "", nil
	}
	endpoint, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return "", "", fmt.Errorf("Aliyun SLS endpoint must be a seed region or official project HTTPS endpoint")
	}
	matches := aliyunSLSProjectHostPattern.FindStringSubmatch(endpoint.Hostname())
	if len(matches) != 3 || !aliyunSLSRegionPattern.MatchString(matches[2]) {
		return "", "", fmt.Errorf("Aliyun SLS endpoint must be a seed region or official project HTTPS endpoint")
	}
	return matches[2], matches[1], nil
}

func normalizeAliyunRegions(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, region := range values {
		region = strings.TrimSpace(region)
		if !aliyunSLSRegionPattern.MatchString(region) || seen[region] {
			continue
		}
		seen[region] = true
		result = append(result, region)
	}
	sort.Strings(result)
	return result
}

func (r *AliyunSLSRunner) ListResources(ctx context.Context) []DiscoveredResource {
	regions, refreshRegionCache := r.discoveryRegions()
	var mu sync.Mutex
	var resources []DiscoveredResource
	var successfulRegions []string
	jobs := make(chan string)
	var workers sync.WaitGroup
	workerCount := 6
	if len(regions) < workerCount {
		workerCount = len(regions)
	}
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for region := range jobs {
				items, reachable := r.discoverRegion(ctx, region)
				mu.Lock()
				resources = append(resources, items...)
				if reachable {
					successfulRegions = append(successfulRegions, region)
				}
				mu.Unlock()
			}
		}()
	}
	for _, region := range regions {
		jobs <- region
	}
	close(jobs)
	workers.Wait()
	if refreshRegionCache {
		sort.Strings(successfulRegions)
		r.cacheMu.Lock()
		r.cachedRegions = append([]string(nil), successfulRegions...)
		r.regionCacheAt = time.Now()
		r.cacheMu.Unlock()
	}
	for i := range resources {
		resources[i].IntegrationID = r.integrationID
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].ExternalID < resources[j].ExternalID })
	return resources
}

func (r *AliyunSLSRunner) discoveryRegions() ([]string, bool) {
	if r.regionMode == "single" {
		return []string{r.seedRegion}, false
	}
	candidates := r.regions
	refreshCache := false
	if r.regionMode == "all" {
		r.cacheMu.Lock()
		if !r.regionCacheAt.IsZero() && time.Since(r.regionCacheAt) < 10*time.Minute {
			candidates = append([]string(nil), r.cachedRegions...)
		} else {
			candidates = aliyunSLSRegions
			refreshCache = true
		}
		r.cacheMu.Unlock()
	}
	result := make([]string, 0, len(candidates))
	for _, region := range candidates {
		if !r.deniedRegions[region] {
			result = append(result, region)
		}
	}
	return result, refreshCache
}

func (r *AliyunSLSRunner) discoverRegion(ctx context.Context, region string) ([]DiscoveredResource, bool) {
	projects := []string{}
	legacyIdentity := r.regionMode == "single" && r.legacyProject != ""
	// Existing project-level integrations remain exactly scoped until an admin
	// explicitly changes their Region policy.
	if legacyIdentity {
		projects = append(projects, r.legacyProject)
	} else {
		var reachable bool
		projects, reachable = r.listProjects(ctx, region)
		if !reachable {
			return nil, false
		}
	}
	resources := []DiscoveredResource{}
	for _, project := range projects {
		if !r.projectAllowed(project) {
			continue
		}
		endpoint := aliyunProjectEndpoint(region, project)
		projectExternalID := "account/" + region + "/" + project
		if legacyIdentity {
			projectExternalID = "account/" + project
		}
		projectResource := DiscoveredResource{
			ExternalID: projectExternalID, Name: project, ResourceType: "aliyun_sls_account",
			Labels: map[string]any{"region": region}, Annotations: map[string]any{},
			Metadata: map[string]any{"region": region, "project": project, "endpoint": endpoint.Hostname()},
		}
		logstores, err := r.listLogstores(ctx, endpoint)
		if err != nil {
			continue
		}
		resources = append(resources, projectResource)
		for _, name := range logstores {
			logstoreExternalID := "logstore/" + region + "/" + project + "/" + name
			if legacyIdentity {
				logstoreExternalID = "logstore/" + name
			}
			resources = append(resources, DiscoveredResource{
				ExternalID: logstoreExternalID, Name: name, ResourceType: "aliyun_sls_logstore",
				Labels: map[string]any{"region": region, "project": project}, Annotations: map[string]any{},
				Metadata: map[string]any{"region": region, "project": project, "logstore": name},
			})
		}
	}
	return resources, true
}

func (r *AliyunSLSRunner) listProjects(ctx context.Context, region string) ([]string, bool) {
	endpoint := aliyunRegionEndpoint(region)
	result := []string{}
	for offset := 0; offset < 1000; offset += 100 {
		var page struct {
			Projects []json.RawMessage `json:"projects"`
			Total    int               `json:"total"`
		}
		if err := r.requestJSONAt(ctx, endpoint, http.MethodGet, "/", url.Values{"offset": {fmt.Sprint(offset)}, "size": {"100"}}, &page); err != nil {
			return nil, false
		}
		for _, raw := range page.Projects {
			var object struct {
				ProjectName string `json:"projectName"`
				Name        string `json:"name"`
			}
			var name string
			if json.Unmarshal(raw, &object) == nil {
				name = object.ProjectName
				if name == "" {
					name = object.Name
				}
			} else {
				_ = json.Unmarshal(raw, &name)
			}
			if aliyunSLSProjectPattern.MatchString(name) {
				result = append(result, name)
			}
		}
		if len(page.Projects) < 100 || (page.Total > 0 && offset+len(page.Projects) >= page.Total) {
			break
		}
	}
	sort.Strings(result)
	return result, true
}

func (r *AliyunSLSRunner) listLogstores(ctx context.Context, endpoint *url.URL) ([]string, error) {
	result := []string{}
	for offset := 0; offset < 1000; offset += 100 {
		var page struct {
			Logstores []string `json:"logstores"`
			Total     int      `json:"total"`
		}
		if err := r.requestJSONAt(ctx, endpoint, http.MethodGet, "/logstores", url.Values{"offset": {fmt.Sprint(offset)}, "size": {"100"}}, &page); err != nil {
			return nil, err
		}
		for _, name := range page.Logstores {
			if aliyunSLSLogstorePattern.MatchString(name) {
				result = append(result, name)
			}
		}
		if len(page.Logstores) < 100 || (page.Total > 0 && offset+len(page.Logstores) >= page.Total) {
			break
		}
	}
	sort.Strings(result)
	return result, nil
}

func (r *AliyunSLSRunner) projectAllowed(project string) bool {
	return r.allowAll || r.allowedProjects[project]
}

func (r *AliyunSLSRunner) Execute(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	lease := r.lease(task.Grant.ExpiresAt)
	region, project, logstore, err := r.resourceIdentity(task.Resource)
	if err != nil {
		return nil, lease, err
	}
	if !r.regionAllowed(region) || !r.projectAllowed(project) {
		return nil, lease, fmt.Errorf("Aliyun SLS resource is outside the Integration scope")
	}
	endpoint := aliyunProjectEndpoint(region, project)
	switch task.Action.Name {
	case "aliyun.sls.account.get":
		if task.Resource.Type != "aliyun_sls_account" || logstore != "" {
			return nil, lease, fmt.Errorf("action requires an Aliyun SLS project resource")
		}
		var result map[string]any
		if err := r.requestJSONAt(ctx, endpoint, http.MethodGet, "/logstores", url.Values{"offset": {"0"}, "size": {"100"}}, &result); err != nil {
			return nil, lease, err
		}
		return map[string]any{"region": region, "project": project, "result": result}, lease, nil
	case "aliyun.sls.logs.query":
		if task.Resource.Type != "aliyun_sls_logstore" || logstore == "" {
			return nil, lease, fmt.Errorf("action requires an Aliyun SLS logstore resource")
		}
		return r.queryLogs(ctx, endpoint, region, project, logstore, task.Parameters, lease)
	default:
		return nil, lease, fmt.Errorf("unsupported Aliyun SLS action %q", task.Action.Name)
	}
}

func (r *AliyunSLSRunner) resourceIdentity(resource domain.TaskResource) (string, string, string, error) {
	parts := strings.Split(resource.ExternalID, "/")
	if resource.Type == "aliyun_sls_account" && len(parts) == 3 && parts[0] == "account" && aliyunSLSRegionPattern.MatchString(parts[1]) && aliyunSLSProjectPattern.MatchString(parts[2]) && resource.Name == parts[2] {
		return parts[1], parts[2], "", nil
	}
	if resource.Type == "aliyun_sls_logstore" && len(parts) == 4 && parts[0] == "logstore" && aliyunSLSRegionPattern.MatchString(parts[1]) && aliyunSLSProjectPattern.MatchString(parts[2]) && aliyunSLSLogstorePattern.MatchString(parts[3]) && resource.Name == parts[3] {
		return parts[1], parts[2], parts[3], nil
	}
	// Backward-compatible identities for an existing project-endpoint runner.
	if r.legacyProject != "" && resource.Type == "aliyun_sls_account" && len(parts) == 2 && parts[0] == "account" && parts[1] == r.legacyProject && resource.Name == parts[1] {
		return r.seedRegion, r.legacyProject, "", nil
	}
	if r.legacyProject != "" && resource.Type == "aliyun_sls_logstore" && len(parts) == 2 && parts[0] == "logstore" && aliyunSLSLogstorePattern.MatchString(parts[1]) && resource.Name == parts[1] {
		return r.seedRegion, r.legacyProject, parts[1], nil
	}
	return "", "", "", fmt.Errorf("Aliyun SLS resource identity does not match its authorized scope")
}

func (r *AliyunSLSRunner) regionAllowed(region string) bool {
	if r.deniedRegions[region] {
		return false
	}
	if r.regionMode == "single" {
		return region == r.seedRegion
	}
	if r.regionMode == "allowlist" {
		for _, allowed := range r.regions {
			if region == allowed {
				return true
			}
		}
		return false
	}
	for _, supported := range aliyunSLSRegions {
		if region == supported {
			return true
		}
	}
	return false
}

func (r *AliyunSLSRunner) queryLogs(ctx context.Context, endpoint *url.URL, region, project, logstore string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	from, fromOK := parameters["from"].(float64)
	to, toOK := parameters["to"].(float64)
	if !fromOK || !toOK || from <= 0 || to <= from || int64(to) > time.Now().Add(5*time.Minute).Unix() {
		return nil, lease, fmt.Errorf("from/to must be valid Unix seconds and to must be after from")
	}
	query, _ := parameters["query"].(string)
	if len(query) > 2048 {
		return nil, lease, fmt.Errorf("query cannot exceed 2048 bytes")
	}
	line := 100
	if value, ok := parameters["line"].(float64); ok {
		line = int(value)
	}
	if line < 1 || line > 500 {
		return nil, lease, fmt.Errorf("line must be between 1 and 500")
	}
	values := url.Values{"type": {"log"}, "from": {fmt.Sprintf("%d", int64(from))}, "to": {fmt.Sprintf("%d", int64(to))}, "line": {fmt.Sprintf("%d", line)}, "offset": {"0"}, "reverse": {"true"}}
	if query != "" {
		values.Set("query", query)
	}
	var result any
	if err := r.requestJSONAt(ctx, endpoint, http.MethodGet, "/logstores/"+url.PathEscape(logstore), values, &result); err != nil {
		return nil, lease, err
	}
	return map[string]any{"region": region, "project": project, "logstore": logstore, "from": int64(from), "to": int64(to), "result": result}, lease, nil
}

func (r *AliyunSLSRunner) requestJSONAt(ctx context.Context, endpoint *url.URL, method, path string, query url.Values, output any) error {
	target := *endpoint
	target.Path = path
	target.RawQuery = query.Encode()
	now := time.Now().UTC().Format(http.TimeFormat)
	logHeaders := map[string]string{"x-log-apiversion": "0.6.0", "x-log-signaturemethod": "hmac-sha1", "x-log-bodyrawsize": "0"}
	keys := make([]string, 0, len(logHeaders))
	for key := range logHeaders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var canonical strings.Builder
	for _, key := range keys {
		canonical.WriteString(key + ":" + logHeaders[key] + "\n")
	}
	canonicalResource := canonicalizeAliyunSLSResource(&target)
	stringToSign := method + "\n\n\n" + now + "\n" + canonical.String() + canonicalResource
	mac := hmac.New(sha1.New, []byte(r.secretKey))
	_, _ = mac.Write([]byte(stringToSign))
	req, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Date", now)
	req.Header.Set("Authorization", "LOG "+r.accessKey+":"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	for key, value := range logHeaders {
		req.Header.Set(key, value)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, aliyunSLSMaxResponse+1))
	if err != nil {
		return err
	}
	if len(data) > aliyunSLSMaxResponse {
		return fmt.Errorf("Aliyun SLS response exceeds 4 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Aliyun SLS returned %d: %s", resp.StatusCode, domain.SanitizeErrorMessage(string(data)))
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, output)
}

// SLS signs decoded query values, while the HTTP URL retains percent encoding.
// This mirrors the official SDK's SignerV1 CanonicalizedResource algorithm.
func canonicalizeAliyunSLSResource(target *url.URL) string {
	resource := target.EscapedPath()
	if target.RawQuery == "" {
		return resource
	}
	values := target.Query()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	resource += "?"
	for i, key := range keys {
		if i > 0 {
			resource += "&"
		}
		for _, value := range values[key] {
			resource += key + "=" + value
		}
	}
	return resource
}

func aliyunRegionEndpoint(region string) *url.URL {
	endpoint, _ := url.Parse("https://" + region + ".log.aliyuncs.com")
	return endpoint
}

func aliyunProjectEndpoint(region, project string) *url.URL {
	endpoint, _ := url.Parse("https://" + project + "." + region + ".log.aliyuncs.com")
	return endpoint
}

func (r *AliyunSLSRunner) lease(expiresAt time.Time) Lease {
	fingerprint := sha256.Sum256([]byte(r.accessKey))
	return Lease{Provider: "ALIYUN_SLS_CREDENTIAL_REFERENCE", Fingerprint: hex.EncodeToString(fingerprint[:8]), ExpiresAt: expiresAt}
}
