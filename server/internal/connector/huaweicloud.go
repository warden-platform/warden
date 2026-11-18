package connector

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

const huaweiMaxResponse = 4 << 20

// huaweiRegionPattern matches Huawei Cloud region identifiers (cn-north-4).
var huaweiRegionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-\d{1,3}$`)

// huaweiResourceIDPattern matches ECS/RDS/ELB resource ids (UUID-like slugs).
// Kept conservative so ids are always safe to interpolate into API paths.
var huaweiResourceIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// huaweiBucketPattern matches OBS bucket names (DNS-compatible).
var huaweiBucketPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{1,61})[a-z0-9]$`)

// huaweiOBSObjectKeyPattern matches OBS object keys (letters, digits, dots,
// underscores, slashes, hyphens). The 1024-byte length limit is enforced
// separately in requireOBSObjectKey because RE2 caps repeat counts at 1000.
var huaweiOBSObjectKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/\-]*$`)

// huaweiMetricPattern matches CES metric names such as cpu_util.
var huaweiMetricPattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
var huaweiECSNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
var huaweiCloudIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,128}$`)
var huaweiSWRInstancePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
var huaweiSWRNamespacePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)
var huaweiSWRRepositoryPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9._-]{0,126}[a-zA-Z0-9])?(?:/[a-zA-Z0-9](?:[a-zA-Z0-9._-]{0,126}[a-zA-Z0-9])?)*$`)
var huaweiSWRTagPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)
var wardenProjectIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var HuaweiCloudResourceTypes = []string{
	"huawei_cloud_account", "huawei_ecs_instance", "huawei_rds_instance", "huawei_elb_loadbalancer", "huawei_obs_bucket",
	"huawei_lts_log_group", "huawei_lts_log_stream", "huawei_swr_instance", "huawei_swr_namespace",
	"huawei_swr_repository", "huawei_swr_tag",
}

func (r *HuaweiCloudRunner) providerTarget(ctx context.Context, request domain.ProviderRequest, binding domain.ProviderRequestBinding) (*url.URL, error) {
	runner := r
	if r.regionMode != "single" {
		region, err := huaweiProviderRegion(binding.Resource)
		if err != nil {
			return nil, err
		}
		allowed, err := r.resolveRegions(ctx)
		if err != nil {
			return nil, err
		}
		if !containsString(allowed, region) {
			return nil, fmt.Errorf("Huawei Cloud region %s is outside the Integration scope", region)
		}
		runner = r.forRegion(region)
	}
	service := ""
	path := request.Path
	switch binding.Capability.Name {
	case "huawei.ecs.read":
		service = "ecs"
		projectID, err := runner.resolveProjectID(ctx)
		if err != nil {
			return nil, err
		}
		serverID := strings.TrimPrefix(request.Path, "/servers/")
		if serverID == request.Path || serverID == "" || strings.Contains(serverID, "/") {
			return nil, fmt.Errorf("invalid Huawei ECS provider path")
		}
		path = "/v1/" + projectID + "/cloudservers/" + serverID
	case "huawei.obs.object.metadata.read":
		service = "obs"
	default:
		return nil, fmt.Errorf("unsupported Huawei provider capability")
	}
	target, err := url.Parse(runner.endpoint(service))
	if err != nil {
		return nil, err
	}
	target.Path = path
	if service == "obs" && runner.overrides["obs"] == "" {
		parts := strings.SplitN(strings.TrimPrefix(request.Path, "/"), "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid Huawei OBS provider path")
		}
		target.Host = parts[0] + "." + target.Host
		target.Path = "/" + parts[1]
	}
	target.RawQuery = domain.NormalizeProviderQuery(request.Query)
	return target, nil
}

func (r *HuaweiCloudRunner) authenticateProviderRequest(request *http.Request) error {
	if strings.Contains(request.URL.Hostname(), ".obs.") || strings.HasPrefix(request.URL.Hostname(), "obs.") {
		date := time.Now().UTC().Format(http.TimeFormat)
		request.Header.Set("Date", date)
		canonical := request.URL.EscapedPath()
		if bucket, _, ok := strings.Cut(request.URL.Hostname(), ".obs."); ok && bucket != "" {
			canonical = "/" + bucket + canonical
		}
		if request.URL.RawQuery != "" {
			canonical += "?" + request.URL.RawQuery
		}
		mac := hmac.New(sha1.New, []byte(r.secretKey))
		mac.Write([]byte(request.Method + "\n\n\n" + date + "\n" + canonical))
		request.Header.Set("Authorization", "OBS "+r.accessKey+":"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		return nil
	}
	date := time.Now().UTC().Format("20060102T150405Z")
	request.Header.Set("X-Sdk-Date", date)
	request.Header.Set("Content-Type", "application/json")
	canonicalURI := request.URL.EscapedPath()
	if !strings.HasSuffix(canonicalURI, "/") {
		canonicalURI += "/"
	}
	headers := map[string]string{"host": request.URL.Host, "x-sdk-date": date, "content-type": "application/json"}
	names := []string{"content-type", "host", "x-sdk-date"}
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + headers[name] + "\n")
	}
	signedHeaders := strings.Join(names, ";")
	bodyHash := sha256.Sum256(nil)
	canonicalRequest := strings.Join([]string{request.Method, canonicalURI, request.URL.RawQuery, canonicalHeaders.String(), signedHeaders, hex.EncodeToString(bodyHash[:])}, "\n")
	requestHash := sha256.Sum256([]byte(canonicalRequest))
	mac := hmac.New(sha256.New, []byte(r.secretKey))
	mac.Write([]byte("SDK-HMAC-SHA256\n" + date + "\n" + hex.EncodeToString(requestHash[:])))
	request.Header.Set("Authorization", "SDK-HMAC-SHA256 Access="+r.accessKey+", SignedHeaders="+signedHeaders+", Signature="+hex.EncodeToString(mac.Sum(nil)))
	return nil
}

func huaweiProviderRegion(resource domain.Resource) (string, error) {
	parts := strings.Split(resource.ExternalID, "/")
	if len(parts) >= 3 && huaweiRegionPattern.MatchString(parts[1]) {
		return parts[1], nil
	}
	return "", fmt.Errorf("Huawei Cloud resource identity does not include its region")
}

var HuaweiCloudCapabilities = []string{
	"huawei.account.get@v1", "huawei.ecs.create@v1", "huawei.ecs.get@v1", "huawei.ecs.metrics@v1",
	"huawei.ecs.start@v1", "huawei.ecs.stop@v1", "huawei.ecs.reboot@v1", "huawei.ecs.update_metadata@v1",
	"huawei.ecs.delete@v1",
	"huawei.rds.get@v1", "huawei.elb.get@v1", "huawei.obs.bucket.get@v1",
	"huawei.obs.object.list@v1", "huawei.obs.object.metadata.get@v1", "huawei.obs.object.delete@v1",
	"huawei.lts.logs.query@v1",
	"huawei.swr.instance.get@v1", "huawei.swr.namespace.get@v1",
	"huawei.swr.repository.get@v1", "huawei.swr.tag.get@v1",
}

// huaweiCloudServiceOrder is the fixed probe/discovery order for P1 services.
var huaweiCloudServiceOrder = []string{"ecs", "rds", "elb", "obs", "lts", "swr"}

type HuaweiCloudConfig struct {
	Region          string
	RegionMode      string
	Regions         []string
	DeniedRegions   []string
	AccessKey       string
	SecretKey       string
	AllowedServices []string
	AllowAll        bool
	IntegrationID   string
	HTTPClient      *http.Client
	// EndpointOverrides maps a service key (iam/ecs/rds/elb/ces/obs/swr) to a base
	// URL. Used by tests to point the runner at mock servers.
	EndpointOverrides map[string]string
}

// HuaweiCloudRunner is the huaweicloud protocol adapter. All requests are
// signed with the integration's IAM user AK/SK, so the cloud-side IAM policy
// is the hard permission ceiling: discovery only surfaces services the
// account can actually list, and actions the account cannot perform are
// rejected by the cloud API itself.
type HuaweiCloudRunner struct {
	region          string
	regionMode      string
	regions         []string
	deniedRegions   map[string]bool
	regionScopedIDs bool
	accessKey       string
	secretKey       string
	allowedServices map[string]bool
	allowAll        bool
	integrationID   string
	http            *http.Client
	overrides       map[string]string

	mu              sync.Mutex
	projectID       string
	resolvedRegions []string
}

// ParseHuaweiCloudCredential accepts either a JSON object
// {"accessKey":"...","secretKey":"..."} or the compact "AK:SK" form stored
// behind the integration's credential reference.
func ParseHuaweiCloudCredential(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		var parsed struct {
			AccessKey string `json:"accessKey"`
			SecretKey string `json:"secretKey"`
		}
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return "", "", fmt.Errorf("parse Huawei Cloud credential JSON: %w", err)
		}
		if parsed.AccessKey == "" || parsed.SecretKey == "" {
			return "", "", fmt.Errorf("Huawei Cloud credential JSON requires accessKey and secretKey")
		}
		return parsed.AccessKey, parsed.SecretKey, nil
	}
	accessKey, secretKey, found := strings.Cut(raw, ":")
	if !found || accessKey == "" || secretKey == "" {
		return "", "", fmt.Errorf("Huawei Cloud credential must be JSON {accessKey, secretKey} or AK:SK")
	}
	return strings.TrimSpace(accessKey), strings.TrimSpace(secretKey), nil
}

func NewHuaweiCloudRunner(config HuaweiCloudConfig) (*HuaweiCloudRunner, error) {
	region := strings.TrimSpace(config.Region)
	if !huaweiRegionPattern.MatchString(region) {
		return nil, fmt.Errorf("Huawei Cloud region must look like cn-north-4")
	}
	if strings.TrimSpace(config.AccessKey) == "" || strings.TrimSpace(config.SecretKey) == "" {
		return nil, fmt.Errorf("Huawei Cloud AK/SK is required")
	}
	allowed := make(map[string]bool, len(config.AllowedServices))
	for _, service := range config.AllowedServices {
		service = strings.ToLower(strings.TrimSpace(service))
		switch service {
		case "ecs", "rds", "elb", "obs", "lts", "swr":
			allowed[service] = true
		default:
			return nil, fmt.Errorf("unsupported Huawei Cloud service scope %q", service)
		}
	}
	regionMode := strings.ToLower(strings.TrimSpace(config.RegionMode))
	if regionMode == "" {
		regionMode = "single"
	}
	if regionMode != "single" && regionMode != "all" && regionMode != "allowlist" {
		return nil, fmt.Errorf("Huawei Cloud region mode must be single, all, or allowlist")
	}
	regions := make([]string, 0, len(config.Regions))
	seenRegions := map[string]bool{}
	for _, candidate := range config.Regions {
		candidate = strings.TrimSpace(candidate)
		if !huaweiRegionPattern.MatchString(candidate) {
			return nil, fmt.Errorf("invalid Huawei Cloud region %q", candidate)
		}
		if !seenRegions[candidate] {
			seenRegions[candidate] = true
			regions = append(regions, candidate)
		}
	}
	if regionMode == "allowlist" && len(regions) == 0 {
		return nil, fmt.Errorf("Huawei Cloud allowlist region mode requires at least one region")
	}
	denied := map[string]bool{}
	for _, candidate := range config.DeniedRegions {
		candidate = strings.TrimSpace(candidate)
		if !huaweiRegionPattern.MatchString(candidate) {
			return nil, fmt.Errorf("invalid denied Huawei Cloud region %q", candidate)
		}
		denied[candidate] = true
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	return &HuaweiCloudRunner{
		region: region, accessKey: strings.TrimSpace(config.AccessKey), secretKey: strings.TrimSpace(config.SecretKey),
		regionMode: regionMode, regions: regions, deniedRegions: denied, regionScopedIDs: regionMode != "single",
		allowedServices: allowed, allowAll: config.AllowAll, integrationID: config.IntegrationID,
		http: client, overrides: config.EndpointOverrides,
	}, nil
}

// serviceAllowed reports whether a service is in the integration scope. An
// empty allowedServices list means "allow all" (mirrors the other adapters).
func (r *HuaweiCloudRunner) serviceAllowed(service string) bool {
	if len(r.allowedServices) == 0 {
		return r.allowAll
	}
	return r.allowedServices[service]
}

func (r *HuaweiCloudRunner) endpoint(service string) string {
	if base, ok := r.overrides[service]; ok && base != "" {
		return strings.TrimRight(base, "/")
	}
	if service == "iam" {
		if r.region == "eu-west-101" {
			return "https://iam.myhuaweicloud.eu"
		}
		return "https://iam.myhuaweicloud.com"
	}
	if service == "swr" {
		if r.region == "eu-west-101" {
			return "https://swr-api.eu-west-101.myhuaweicloud.eu"
		}
		return "https://swr-api." + r.region + ".myhuaweicloud.com"
	}
	return "https://" + service + "." + r.region + ".myhuaweicloud.com"
}

// --- Discovery ---

func (r *HuaweiCloudRunner) ListResources(ctx context.Context) []DiscoveredResource {
	resources, _ := r.discoverResources(ctx)
	return resources
}

// ListResourcesComplete reports whether every in-scope Huawei Cloud service
// and region was discovered successfully. Returning partial resources with a
// false completeness flag lets the control plane refresh what was observed
// without orphaning resources hidden by a transient API or network failure.
func (r *HuaweiCloudRunner) ListResourcesComplete(ctx context.Context) ([]DiscoveredResource, bool) {
	return r.discoverResources(ctx)
}

func (r *HuaweiCloudRunner) discoverResources(ctx context.Context) ([]DiscoveredResource, bool) {
	if r.regionMode != "single" {
		regions, err := r.resolveRegions(ctx)
		if err != nil {
			fmt.Printf("huaweicloud integration %s: discover regions: %v\n", r.integrationID, err)
			return nil, false
		}
		resources := []DiscoveredResource{}
		complete := true
		for _, region := range regions {
			discovered, regionComplete := r.forRegion(region).listRegionResources(ctx)
			resources = append(resources, discovered...)
			complete = complete && regionComplete
		}
		return resources, complete
	}
	return r.listRegionResources(ctx)
}

// SnapshotResource performs a targeted reread for ECS mutations instead of
// rediscovering every enabled Huawei Cloud service in every Region.
func (r *HuaweiCloudRunner) SnapshotResource(ctx context.Context, resource domain.TaskResource) (DiscoveredResource, bool) {
	runner := r
	if r.regionMode != "single" {
		region, err := huaweiTaskRegion(resource)
		if err != nil {
			return DiscoveredResource{}, false
		}
		allowed, err := r.resolveRegions(ctx)
		if err != nil || !containsString(allowed, region) {
			return DiscoveredResource{}, false
		}
		runner = r.forRegion(region)
	}
	if resource.Type == "huawei_cloud_account" {
		if resource.ExternalID != "account/"+runner.region {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: resource.ExternalID, Name: resource.Name, ResourceType: resource.Type, IntegrationID: r.integrationID,
			Labels: map[string]any{"region": runner.region}, Annotations: map[string]any{}, Metadata: map[string]any{"region": runner.region},
		}, true
	}
	if resource.Type == "huawei_obs_bucket" {
		if !runner.serviceAllowed("obs") {
			return DiscoveredResource{}, false
		}
		bucket, err := huaweiOBSBucketName(resource)
		if err != nil {
			return DiscoveredResource{}, false
		}
		if _, err := runner.obsRequest(ctx, bucket, "storageinfo"); err != nil {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: resource.ExternalID, Name: bucket, ResourceType: resource.Type, IntegrationID: r.integrationID,
			Labels: map[string]any{"region": runner.region}, Annotations: map[string]any{},
			Metadata: map[string]any{"name": bucket, "region": runner.region},
		}, true
	}
	if resource.Type != "huawei_ecs_instance" || !runner.serviceAllowed("ecs") {
		return DiscoveredResource{}, false
	}
	id, err := huaweiResourceID(resource, "ecs", runner.region)
	if err != nil {
		return DiscoveredResource{}, false
	}
	var detail struct {
		Server struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
			Flavor struct {
				ID string `json:"id"`
			} `json:"flavor"`
			AvailabilityZone string   `json:"OS-EXT-AZ:availability_zone"`
			Tags             []string `json:"tags"`
		} `json:"server"`
	}
	if err := runner.getSignedJSON(ctx, "ecs", "/v1/{project_id}/cloudservers/"+id, nil, &detail); err != nil || detail.Server.ID == "" {
		return DiscoveredResource{}, false
	}
	labels := map[string]any{"status": detail.Server.Status}
	for _, tag := range detail.Server.Tags {
		if key, value, ok := strings.Cut(tag, "="); ok && key != "" {
			labels[key] = value
		}
	}
	return DiscoveredResource{
		ExternalID: resource.ExternalID, Name: detail.Server.Name, ResourceType: resource.Type, IntegrationID: r.integrationID,
		Labels: labels, Annotations: map[string]any{}, Metadata: map[string]any{
			"id": detail.Server.ID, "status": detail.Server.Status, "flavor": detail.Server.Flavor.ID,
			"availabilityZone": detail.Server.AvailabilityZone, "region": runner.region,
		},
	}, true
}

func (r *HuaweiCloudRunner) listRegionResources(ctx context.Context) ([]DiscoveredResource, bool) {
	serviceStatus := map[string]any{}
	var resources []DiscoveredResource
	complete := true
	for _, service := range huaweiCloudServiceOrder {
		if !r.serviceAllowed(service) {
			serviceStatus[service] = "OUT_OF_SCOPE"
			continue
		}
		var discovered []DiscoveredResource
		var err error
		switch service {
		case "ecs":
			discovered, err = r.discoverECS(ctx)
		case "rds":
			discovered, err = r.discoverRDS(ctx)
		case "elb":
			discovered, err = r.discoverELB(ctx)
		case "obs":
			discovered, err = r.discoverOBS(ctx)
		case "lts":
			discovered, err = r.discoverLTS(ctx)
		case "swr":
			discovered, err = r.discoverSWR(ctx)
		}
		if err != nil {
			// Capability probing: a service the IAM user cannot list surfaces
			// no resources (and therefore no actions in the catalog).
			serviceStatus[service] = "UNAVAILABLE"
			complete = false
			fmt.Printf("huaweicloud integration %s: discover %s: %v\n", r.integrationID, service, err)
			continue
		}
		serviceStatus[service] = "ACTIVE"
		resources = append(resources, discovered...)
	}
	account := DiscoveredResource{
		ExternalID: "account/" + r.region, Name: "huaweicloud/" + r.region, ResourceType: "huawei_cloud_account",
		Labels: map[string]any{"region": r.region}, Annotations: map[string]any{},
		Metadata: map[string]any{"region": r.region, "services": serviceStatus},
	}
	resources = append([]DiscoveredResource{account}, resources...)
	if r.integrationID != "" {
		for i := range resources {
			resources[i].IntegrationID = r.integrationID
		}
	}
	return resources, complete
}

func (r *HuaweiCloudRunner) discoverECS(ctx context.Context) ([]DiscoveredResource, error) {
	var out struct {
		Servers []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
			Flavor struct {
				ID string `json:"id"`
			} `json:"flavor"`
			AvailabilityZone string   `json:"OS-EXT-AZ:availability_zone"`
			Tags             []string `json:"tags"`
		} `json:"servers"`
	}
	if err := r.getSignedJSON(ctx, "ecs", "/v1/{project_id}/cloudservers/detail", [][2]string{{"limit", "100"}}, &out); err != nil {
		return nil, err
	}
	resources := make([]DiscoveredResource, 0, len(out.Servers))
	for _, server := range out.Servers {
		if server.ID == "" {
			continue
		}
		labels := map[string]any{"status": server.Status}
		for _, tag := range server.Tags {
			if key, value, ok := strings.Cut(tag, "="); ok && key != "" {
				labels[key] = value
			}
		}
		resources = append(resources, DiscoveredResource{
			ExternalID: r.scopedExternalID("ecs", server.ID), Name: server.Name, ResourceType: "huawei_ecs_instance",
			Labels: labels, Annotations: map[string]any{},
			Metadata: map[string]any{"id": server.ID, "status": server.Status, "flavor": server.Flavor.ID, "availabilityZone": server.AvailabilityZone, "region": r.region},
		})
	}
	return resources, nil
}

func (r *HuaweiCloudRunner) discoverRDS(ctx context.Context) ([]DiscoveredResource, error) {
	var out struct {
		Instances []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			Datastore struct {
				Type    string `json:"type"`
				Version string `json:"version"`
			} `json:"datastore"`
		} `json:"instances"`
	}
	if err := r.getSignedJSON(ctx, "rds", "/v3/{project_id}/instances", [][2]string{{"limit", "100"}}, &out); err != nil {
		return nil, err
	}
	resources := make([]DiscoveredResource, 0, len(out.Instances))
	for _, instance := range out.Instances {
		if instance.ID == "" {
			continue
		}
		resources = append(resources, DiscoveredResource{
			ExternalID: r.scopedExternalID("rds", instance.ID), Name: instance.Name, ResourceType: "huawei_rds_instance",
			Labels: map[string]any{"status": instance.Status, "engine": instance.Datastore.Type}, Annotations: map[string]any{},
			Metadata: map[string]any{"id": instance.ID, "status": instance.Status, "engine": instance.Datastore.Type, "engineVersion": instance.Datastore.Version, "region": r.region},
		})
	}
	return resources, nil
}

func (r *HuaweiCloudRunner) discoverELB(ctx context.Context) ([]DiscoveredResource, error) {
	var out struct {
		LoadBalancers []struct {
			ID                 string `json:"id"`
			Name               string `json:"name"`
			ProvisioningStatus string `json:"provisioning_status"`
			VIPAddress         string `json:"vip_address"`
		} `json:"loadbalancers"`
	}
	if err := r.getSignedJSON(ctx, "elb", "/v3/{project_id}/elb/loadbalancers", [][2]string{{"limit", "100"}}, &out); err != nil {
		return nil, err
	}
	resources := make([]DiscoveredResource, 0, len(out.LoadBalancers))
	for _, lb := range out.LoadBalancers {
		if lb.ID == "" {
			continue
		}
		resources = append(resources, DiscoveredResource{
			ExternalID: r.scopedExternalID("elb", lb.ID), Name: lb.Name, ResourceType: "huawei_elb_loadbalancer",
			Labels: map[string]any{"status": lb.ProvisioningStatus}, Annotations: map[string]any{},
			Metadata: map[string]any{"id": lb.ID, "status": lb.ProvisioningStatus, "vipAddress": lb.VIPAddress, "region": r.region},
		})
	}
	return resources, nil
}

type obsListBucketsResult struct {
	Buckets []struct {
		Name         string `xml:"Name"`
		CreationDate string `xml:"CreationDate"`
		Location     string `xml:"Location"`
	} `xml:"Buckets>Bucket"`
}

type obsListObjectsResult struct {
	Name        string `xml:"Name"`
	IsTruncated bool   `xml:"IsTruncated"`
	Contents    []struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
	} `xml:"Contents"`
}

// huaweiOBSBucketName extracts the bucket name from a resource ExternalID,
// handling both plain (obs/bucket-a) and region-scoped (obs/cn-north-4/bucket-a) forms.
func huaweiOBSBucketName(resource domain.TaskResource) (string, error) {
	rest := strings.TrimPrefix(resource.ExternalID, "obs/")
	if rest == resource.ExternalID {
		return "", fmt.Errorf("invalid OBS bucket identity")
	}
	if parts := strings.SplitN(rest, "/", 2); len(parts) == 2 && huaweiRegionPattern.MatchString(parts[0]) {
		rest = parts[1]
	}
	if !huaweiBucketPattern.MatchString(rest) {
		return "", fmt.Errorf("invalid OBS bucket identity")
	}
	return rest, nil
}

// requireOBSObjectKey validates and returns the objectKey parameter.
func requireOBSObjectKey(parameters map[string]any) (string, error) {
	objectKey, _ := parameters["objectKey"].(string)
	if len(objectKey) == 0 || len(objectKey) > 1024 || !huaweiOBSObjectKeyPattern.MatchString(objectKey) {
		return "", fmt.Errorf("invalid OBS object key")
	}
	return objectKey, nil
}

func (r *HuaweiCloudRunner) discoverOBS(ctx context.Context) ([]DiscoveredResource, error) {
	data, err := r.obsRequest(ctx, "", "")
	if err != nil {
		return nil, err
	}
	var out obsListBucketsResult
	if err := xml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse OBS bucket list: %w", err)
	}
	resources := make([]DiscoveredResource, 0, len(out.Buckets))
	for _, bucket := range out.Buckets {
		if bucket.Name == "" {
			continue
		}
		if r.regionScopedIDs && bucket.Location != "" && bucket.Location != r.region {
			continue
		}
		resources = append(resources, DiscoveredResource{
			ExternalID: r.scopedExternalID("obs", bucket.Name), Name: bucket.Name, ResourceType: "huawei_obs_bucket",
			Labels: map[string]any{"location": bucket.Location}, Annotations: map[string]any{},
			Metadata: map[string]any{"name": bucket.Name, "location": bucket.Location, "createdAt": bucket.CreationDate, "region": r.region},
		})
	}
	return resources, nil
}

func (r *HuaweiCloudRunner) discoverLTS(ctx context.Context) ([]DiscoveredResource, error) {
	var groups struct {
		LogGroups []struct {
			ID           string `json:"log_group_id"`
			Name         string `json:"log_group_name"`
			TTLInDays    int    `json:"ttl_in_days"`
			CreationTime int64  `json:"creation_time"`
		} `json:"log_groups"`
	}
	if err := r.getSignedJSON(ctx, "lts", "/v2/{project_id}/groups", [][2]string{{"limit", "100"}}, &groups); err != nil {
		return nil, err
	}
	resources := make([]DiscoveredResource, 0, len(groups.LogGroups))
	for _, group := range groups.LogGroups {
		if !huaweiCloudIDPattern.MatchString(group.ID) {
			continue
		}
		groupName := strings.TrimSpace(group.Name)
		if groupName == "" {
			groupName = group.ID
		}
		resources = append(resources, DiscoveredResource{
			ExternalID: r.scopedLTSGroupID(group.ID), Name: groupName, ResourceType: "huawei_lts_log_group",
			Labels: map[string]any{"region": r.region}, Annotations: map[string]any{},
			Metadata: map[string]any{"id": group.ID, "region": r.region, "ttlInDays": group.TTLInDays, "createdAt": group.CreationTime},
		})
		var streams struct {
			LogStreams []struct {
				ID           string `json:"log_stream_id"`
				Name         string `json:"log_stream_name"`
				CreationTime int64  `json:"creation_time"`
			} `json:"log_streams"`
		}
		path := "/v2/{project_id}/groups/" + group.ID + "/streams"
		if err := r.getSignedJSON(ctx, "lts", path, [][2]string{{"limit", "100"}}, &streams); err != nil {
			return nil, fmt.Errorf("list LTS streams for group %s: %w", group.ID, err)
		}
		for _, stream := range streams.LogStreams {
			if !huaweiCloudIDPattern.MatchString(stream.ID) {
				continue
			}
			streamName := strings.TrimSpace(stream.Name)
			if streamName == "" {
				streamName = stream.ID
			}
			resources = append(resources, DiscoveredResource{
				ExternalID: r.scopedLTSStreamID(group.ID, stream.ID), Name: streamName, ResourceType: "huawei_lts_log_stream",
				Labels: map[string]any{"region": r.region, "logGroup": groupName}, Annotations: map[string]any{},
				Metadata: map[string]any{"id": stream.ID, "region": r.region, "logGroupId": group.ID, "logGroupName": groupName, "createdAt": stream.CreationTime},
			})
		}
	}
	return resources, nil
}

// Huawei Cloud SWR Enterprise paths mirror the official
// huaweicloud-sdk-go-v3 services/swr/v2 request definitions:
// ListInstance, ListInstanceNamespaces, ListNamespaceRepositories and
// ListInstanceTags. ListInstanceTags is one of the few SWR Enterprise APIs
// whose URI does not include {project_id}. The tag detail action uses
// ShowInstanceArtifact, which accepts a digest as its reference path parameter.
func (r *HuaweiCloudRunner) discoverSWR(ctx context.Context) ([]DiscoveredResource, error) {
	instances, err := r.listSWRInstances(ctx)
	if err != nil {
		return nil, err
	}
	resources := make([]DiscoveredResource, 0, len(instances))
	for _, instance := range instances {
		instanceID := strings.TrimSpace(instance.ID)
		if !huaweiSWRInstancePattern.MatchString(instanceID) {
			continue
		}
		instanceName := strings.TrimSpace(instance.Name)
		if instanceName == "" {
			instanceName = instanceID
		}
		resources = append(resources, DiscoveredResource{
			ExternalID: r.swrExternalID("instance", instanceID), Name: instanceName, ResourceType: "huawei_swr_instance",
			Labels:      map[string]any{"region": r.region, "status": instance.Status},
			Annotations: map[string]any{},
			Metadata: map[string]any{
				"id": instanceID, "name": instanceName, "region": r.region, "status": instance.Status,
				"description": instance.Description, "spec": instance.Spec, "version": instance.Version,
				"accessAddress": instance.AccessAddress, "vpcId": instance.VpcID, "subnetId": instance.SubnetID,
				"createdAt": instance.CreatedAt, "updatedAt": instance.UpdatedAt,
			},
		})

		namespaces, err := r.listSWRNamespaces(ctx, instanceID)
		if err != nil {
			return nil, fmt.Errorf("list SWR namespaces for instance %s: %w", instanceID, err)
		}
		for _, namespace := range namespaces {
			namespaceName := strings.TrimSpace(namespace.Name)
			if !huaweiSWRNamespacePattern.MatchString(namespaceName) {
				continue
			}
			resources = append(resources, DiscoveredResource{
				ExternalID: r.swrExternalID("namespace", instanceID, namespaceName), Name: namespaceName, ResourceType: "huawei_swr_namespace",
				Labels:      map[string]any{"region": r.region, "instance": instanceName, "public": namespace.Metadata.Public},
				Annotations: map[string]any{},
				Metadata: map[string]any{
					"id": namespace.ID, "name": namespaceName, "region": r.region, "instanceId": instanceID,
					"instanceName": instanceName, "repositoryCount": namespace.RepositoryCount,
					"public": namespace.Metadata.Public, "autoScan": namespace.Metadata.AutoScan,
					"preventVulnerability": namespace.Metadata.PreventVulnerability,
					"severity":             namespace.Metadata.Severity, "createdAt": namespace.CreatedAt, "updatedAt": namespace.UpdatedAt,
				},
			})

			repositories, err := r.listSWRRepositories(ctx, instanceID, namespaceName)
			if err != nil {
				return nil, fmt.Errorf("list SWR repositories for %s/%s: %w", instanceID, namespaceName, err)
			}
			for _, repository := range repositories {
				repositoryName := strings.TrimSpace(repository.Name)
				if !huaweiSWRRepositoryPattern.MatchString(repositoryName) {
					continue
				}
				resources = append(resources, DiscoveredResource{
					ExternalID: r.swrExternalID("repository", instanceID, namespaceName, repositoryName), Name: repositoryName, ResourceType: "huawei_swr_repository",
					Labels:      map[string]any{"region": r.region, "instance": instanceName, "namespace": namespaceName},
					Annotations: map[string]any{},
					Metadata: map[string]any{
						"id": repository.ID, "name": repositoryName, "region": r.region,
						"instanceId": instanceID, "instanceName": instanceName,
						"namespaceId": namespace.ID, "namespaceName": namespaceName,
						"tagCount": repository.TagCount, "artifactCount": repository.ArtifactCount, "pullCount": repository.PullCount,
						"description": repository.Description, "createdAt": repository.CreatedAt, "updatedAt": repository.UpdatedAt,
					},
				})

				tags, err := r.listSWRTags(ctx, instanceID, namespaceName, repositoryName)
				if err != nil {
					return nil, fmt.Errorf("list SWR tags for %s/%s/%s: %w", instanceID, namespaceName, repositoryName, err)
				}
				for _, tag := range tags {
					tagName := strings.TrimSpace(tag.Name)
					if !huaweiSWRTagPattern.MatchString(tagName) {
						continue
					}
					resources = append(resources, DiscoveredResource{
						ExternalID: r.swrExternalID("tag", instanceID, namespaceName, repositoryName, tagName), Name: tagName, ResourceType: "huawei_swr_tag",
						Labels:      map[string]any{"region": r.region, "instance": instanceName, "namespace": namespaceName, "repository": repositoryName},
						Annotations: map[string]any{},
						Metadata: map[string]any{
							"id": tag.ID, "name": tagName, "region": r.region, "instanceId": instanceID, "instanceName": instanceName,
							"namespaceId": namespace.ID, "namespaceName": namespaceName, "repositoryId": repository.ID,
							"repositoryName": repositoryName, "artifactId": tag.ArtifactID, "digest": tag.Digest,
							"size": tag.Size, "type": tag.Type, "mediaType": tag.MediaType,
							"manifestMediaType": tag.ManifestMediaType, "pullTime": tag.PullTime, "pushTime": tag.PushTime,
						},
					})
				}
			}
		}
	}
	return resources, nil
}

type huaweiSWRInstance struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Spec          string `json:"spec"`
	Version       string `json:"version"`
	AccessAddress string `json:"access_address"`
	Status        string `json:"status"`
	VpcID         string `json:"vpc_id"`
	SubnetID      string `json:"subnet_id"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type huaweiSWRNamespace struct {
	Name            string `json:"name"`
	ID              int64  `json:"namespace_id"`
	RepositoryCount int64  `json:"repo_count"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	Metadata        struct {
		Public               string `json:"public"`
		AutoScan             string `json:"auto_scan"`
		PreventVulnerability string `json:"prevent_vul"`
		Severity             string `json:"severity"`
	} `json:"metadata"`
}

type huaweiSWRRepository struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	TagCount      int64  `json:"tag_count"`
	ArtifactCount int64  `json:"artifact_count"`
	PullCount     int64  `json:"pull_count"`
	Description   string `json:"description"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type huaweiSWRTag struct {
	Name              string `json:"name"`
	ID                int64  `json:"id"`
	ArtifactID        int64  `json:"artifact_id"`
	Digest            string `json:"digest"`
	Size              int64  `json:"size"`
	Type              string `json:"type"`
	MediaType         string `json:"media_type"`
	ManifestMediaType string `json:"manifest_media_type"`
	PullTime          string `json:"pull_time"`
	PushTime          string `json:"push_time"`
}

const huaweiSWRPageLimit = 100
const huaweiSWRMaxPages = 1000

func (r *HuaweiCloudRunner) listSWRInstances(ctx context.Context) ([]huaweiSWRInstance, error) {
	resources := []huaweiSWRInstance{}
	for page, offset := 0, 0; page < huaweiSWRMaxPages; page++ {
		var out struct {
			Instances []huaweiSWRInstance `json:"instances"`
			Total     int64               `json:"total"`
		}
		if err := r.getSignedJSON(ctx, "swr", "/v2/{project_id}/instances", huaweiSWRPageQuery(offset), &out); err != nil {
			return nil, err
		}
		resources = append(resources, out.Instances...)
		if !huaweiSWRHasNextPage(offset, len(out.Instances), out.Total) {
			return resources, nil
		}
		offset += len(out.Instances)
	}
	return nil, fmt.Errorf("SWR instance discovery exceeded %d pages", huaweiSWRMaxPages)
}

func (r *HuaweiCloudRunner) listSWRNamespaces(ctx context.Context, instanceID string) ([]huaweiSWRNamespace, error) {
	resources := []huaweiSWRNamespace{}
	for page, offset := 0, 0; page < huaweiSWRMaxPages; page++ {
		var out struct {
			Namespaces []huaweiSWRNamespace `json:"namespaces"`
			Total      int64                `json:"total"`
		}
		path := "/v2/{project_id}/instances/" + instanceID + "/namespaces"
		if err := r.getSignedJSON(ctx, "swr", path, huaweiSWRPageQuery(offset), &out); err != nil {
			return nil, err
		}
		resources = append(resources, out.Namespaces...)
		if !huaweiSWRHasNextPage(offset, len(out.Namespaces), out.Total) {
			return resources, nil
		}
		offset += len(out.Namespaces)
	}
	return nil, fmt.Errorf("SWR namespace discovery exceeded %d pages", huaweiSWRMaxPages)
}

func (r *HuaweiCloudRunner) listSWRRepositories(ctx context.Context, instanceID, namespaceName string) ([]huaweiSWRRepository, error) {
	resources := []huaweiSWRRepository{}
	for page, offset := 0, 0; page < huaweiSWRMaxPages; page++ {
		var out struct {
			Repositories []huaweiSWRRepository `json:"repositories"`
			Total        int64                 `json:"total"`
		}
		path := "/v2/{project_id}/instances/" + url.PathEscape(instanceID) + "/namespaces/" + url.PathEscape(namespaceName) + "/repositories"
		if err := r.getSignedJSON(ctx, "swr", path, huaweiSWRPageQuery(offset), &out); err != nil {
			return nil, err
		}
		resources = append(resources, out.Repositories...)
		if !huaweiSWRHasNextPage(offset, len(out.Repositories), out.Total) {
			return resources, nil
		}
		offset += len(out.Repositories)
	}
	return nil, fmt.Errorf("SWR repository discovery exceeded %d pages", huaweiSWRMaxPages)
}

func (r *HuaweiCloudRunner) listSWRTags(ctx context.Context, instanceID, namespaceName, repositoryName string) ([]huaweiSWRTag, error) {
	resources := []huaweiSWRTag{}
	for page, offset := 0, 0; page < huaweiSWRMaxPages; page++ {
		var out struct {
			Tags  []huaweiSWRTag `json:"tags"`
			Total int64          `json:"total"`
		}
		path := "/v2/instances/" + url.PathEscape(instanceID) + "/namespaces/" + url.PathEscape(namespaceName) +
			"/repositories/" + url.PathEscape(repositoryName) + "/tags"
		if err := r.getSignedJSON(ctx, "swr", path, huaweiSWRPageQuery(offset), &out); err != nil {
			return nil, err
		}
		resources = append(resources, out.Tags...)
		if !huaweiSWRHasNextPage(offset, len(out.Tags), out.Total) {
			return resources, nil
		}
		offset += len(out.Tags)
	}
	return nil, fmt.Errorf("SWR tag discovery exceeded %d pages", huaweiSWRMaxPages)
}

func huaweiSWRPageQuery(offset int) [][2]string {
	return [][2]string{{"limit", strconv.Itoa(huaweiSWRPageLimit)}, {"offset", strconv.Itoa(offset)}}
}

func huaweiSWRHasNextPage(offset, count int, total int64) bool {
	if count <= 0 || count < huaweiSWRPageLimit {
		return false
	}
	return total <= 0 || int64(offset+count) < total
}

// --- Execution ---

func (r *HuaweiCloudRunner) Execute(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	if r.regionMode != "single" {
		region, err := huaweiTaskRegion(task.Resource)
		if err != nil {
			return nil, r.lease(task.Grant.ExpiresAt), err
		}
		allowed, err := r.resolveRegions(ctx)
		if err != nil {
			return nil, r.lease(task.Grant.ExpiresAt), err
		}
		if !containsString(allowed, region) {
			return nil, r.lease(task.Grant.ExpiresAt), fmt.Errorf("Huawei Cloud region %s is outside the Integration scope", region)
		}
		return r.forRegion(region).executeRegion(ctx, task)
	}
	return r.executeRegion(ctx, task)
}

func (r *HuaweiCloudRunner) executeRegion(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	lease := r.lease(task.Grant.ExpiresAt)
	if err := validateHuaweiActionResource(task.Action.Name, task.Resource.Type); err != nil {
		return nil, lease, err
	}
	switch task.Action.Name {
	case "huawei.account.get":
		if task.Resource.ExternalID != "account/"+r.region {
			return nil, lease, fmt.Errorf("Huawei Cloud account identity does not match its authorized scope")
		}
		return r.getAccount(ctx, lease)
	case "huawei.ecs.get":
		id, err := huaweiResourceID(task.Resource, "ecs", r.region)
		if err != nil {
			return nil, lease, err
		}
		var out map[string]any
		if err := r.getSignedJSON(ctx, "ecs", "/v1/{project_id}/cloudservers/"+id, nil, &out); err != nil {
			return nil, lease, err
		}
		return out, lease, nil
	case "huawei.ecs.create":
		if task.Resource.ExternalID != "account/"+r.region {
			return nil, lease, fmt.Errorf("Huawei Cloud account identity does not match its authorized scope")
		}
		return r.createECS(ctx, task, lease)
	case "huawei.ecs.metrics":
		id, err := huaweiResourceID(task.Resource, "ecs", r.region)
		if err != nil {
			return nil, lease, err
		}
		return r.getECSMetrics(ctx, id, task.Parameters, lease)
	case "huawei.ecs.start", "huawei.ecs.stop", "huawei.ecs.reboot":
		id, err := huaweiResourceID(task.Resource, "ecs", r.region)
		if err != nil {
			return nil, lease, err
		}
		return r.changeECSLifecycle(ctx, id, task.Action.Name, lease)
	case "huawei.ecs.update_metadata":
		id, err := huaweiResourceID(task.Resource, "ecs", r.region)
		if err != nil {
			return nil, lease, err
		}
		return r.updateECSMetadata(ctx, id, task.Parameters, lease)
	case "huawei.ecs.delete":
		id, err := huaweiResourceID(task.Resource, "ecs", r.region)
		if err != nil {
			return nil, lease, err
		}
		return r.deleteECS(ctx, id, task, lease)
	case "huawei.rds.get":
		id, err := huaweiResourceID(task.Resource, "rds", r.region)
		if err != nil {
			return nil, lease, err
		}
		var out map[string]any
		if err := r.getSignedJSON(ctx, "rds", "/v3/{project_id}/instances", [][2]string{{"id", id}}, &out); err != nil {
			return nil, lease, err
		}
		return out, lease, nil
	case "huawei.elb.get":
		id, err := huaweiResourceID(task.Resource, "elb", r.region)
		if err != nil {
			return nil, lease, err
		}
		var out map[string]any
		if err := r.getSignedJSON(ctx, "elb", "/v3/{project_id}/elb/loadbalancers/"+id, nil, &out); err != nil {
			return nil, lease, err
		}
		return out, lease, nil
	case "huawei.obs.bucket.get":
		bucket := strings.TrimPrefix(task.Resource.ExternalID, "obs/")
		if bucket == task.Resource.ExternalID || !huaweiBucketPattern.MatchString(bucket) {
			return nil, lease, fmt.Errorf("invalid OBS bucket identity")
		}
		data, err := r.obsRequest(ctx, bucket, "storageinfo")
		if err != nil {
			return nil, lease, err
		}
		var info struct {
			Size         int64 `xml:"Size"`
			ObjectNumber int64 `xml:"ObjectNumber"`
		}
		if err := xml.Unmarshal(data, &info); err != nil {
			return nil, lease, fmt.Errorf("parse OBS storage info: %w", err)
		}
		return map[string]any{"bucket": bucket, "sizeBytes": info.Size, "objectCount": info.ObjectNumber}, lease, nil
	case "huawei.obs.object.list":
		bucket, err := huaweiOBSBucketName(task.Resource)
		if err != nil {
			return nil, lease, err
		}
		prefix := ""
		if v, ok := task.Parameters["prefix"].(string); ok && v != "" {
			if len(v) > 1024 {
				return nil, lease, fmt.Errorf("OBS prefix too long")
			}
			prefix = v
		}
		maxKeys := 100
		if v, ok := task.Parameters["maxKeys"].(float64); ok && v > 0 {
			maxKeys = int(v)
		}
		if maxKeys < 1 || maxKeys > 1000 {
			return nil, lease, fmt.Errorf("maxKeys must be between 1 and 1000")
		}
		data, _, err := r.obsSignedRequest(ctx, http.MethodGet, bucket, "", url.Values{
			"prefix":   {prefix},
			"max-keys": {fmt.Sprintf("%d", maxKeys)},
		})
		if err != nil {
			return nil, lease, err
		}
		var result obsListObjectsResult
		if err := xml.Unmarshal(data, &result); err != nil {
			return nil, lease, fmt.Errorf("parse OBS object list: %w", err)
		}
		objects := make([]map[string]any, 0, len(result.Contents))
		for _, obj := range result.Contents {
			objects = append(objects, map[string]any{
				"key":          obj.Key,
				"size":         obj.Size,
				"lastModified": obj.LastModified,
				"etag":         obj.ETag,
			})
		}
		return map[string]any{
			"bucket":    bucket,
			"objects":   objects,
			"count":     len(objects),
			"truncated": result.IsTruncated,
		}, lease, nil
	case "huawei.obs.object.metadata.get":
		bucket, err := huaweiOBSBucketName(task.Resource)
		if err != nil {
			return nil, lease, err
		}
		objectKey, err := requireOBSObjectKey(task.Parameters)
		if err != nil {
			return nil, lease, err
		}
		_, header, err := r.obsSignedRequest(ctx, http.MethodHead, bucket, objectKey, nil)
		if err != nil {
			return nil, lease, err
		}
		return map[string]any{
			"bucket":       bucket,
			"objectKey":    objectKey,
			"size":         header.Get("Content-Length"),
			"contentType":  header.Get("Content-Type"),
			"lastModified": header.Get("Last-Modified"),
			"etag":         strings.Trim(header.Get("ETag"), "\""),
		}, lease, nil
	case "huawei.obs.object.delete":
		bucket, err := huaweiOBSBucketName(task.Resource)
		if err != nil {
			return nil, lease, err
		}
		objectKey, err := requireOBSObjectKey(task.Parameters)
		if err != nil {
			return nil, lease, err
		}
		confirm, _ := task.Parameters["confirmObjectKey"].(string)
		if confirm != objectKey {
			return nil, lease, fmt.Errorf("OBS object delete requires exact objectKey confirmation")
		}
		_, _, err = r.obsSignedRequest(ctx, http.MethodDelete, bucket, objectKey, nil)
		if err != nil {
			return nil, lease, err
		}
		return map[string]any{"bucket": bucket, "deletedObjectKey": objectKey}, lease, nil
	case "huawei.lts.logs.query":
		if !r.serviceAllowed("lts") {
			return nil, lease, fmt.Errorf("Huawei Cloud LTS is outside the Integration service scope")
		}
		groupID, streamID, err := r.ltsStreamIdentity(task.Resource)
		if err != nil {
			return nil, lease, err
		}
		return r.queryLTSLogs(ctx, groupID, streamID, task.Parameters, lease)
	case "huawei.swr.instance.get":
		if !r.serviceAllowed("swr") {
			return nil, lease, fmt.Errorf("Huawei Cloud SWR is outside the Integration service scope")
		}
		identity, err := r.swrIdentity(task.Resource, "instance")
		if err != nil {
			return nil, lease, err
		}
		var out map[string]any
		if err := r.getSignedJSON(ctx, "swr", "/v2/{project_id}/instances/"+identity.InstanceID, nil, &out); err != nil {
			return nil, lease, err
		}
		return out, lease, nil
	case "huawei.swr.namespace.get":
		if !r.serviceAllowed("swr") {
			return nil, lease, fmt.Errorf("Huawei Cloud SWR is outside the Integration service scope")
		}
		identity, err := r.swrIdentity(task.Resource, "namespace")
		if err != nil {
			return nil, lease, err
		}
		var out map[string]any
		path := "/v2/{project_id}/instances/" + identity.InstanceID + "/namespaces/" + identity.Namespace
		if err := r.getSignedJSON(ctx, "swr", path, nil, &out); err != nil {
			return nil, lease, err
		}
		return out, lease, nil
	case "huawei.swr.repository.get":
		if !r.serviceAllowed("swr") {
			return nil, lease, fmt.Errorf("Huawei Cloud SWR is outside the Integration service scope")
		}
		identity, err := r.swrIdentity(task.Resource, "repository")
		if err != nil {
			return nil, lease, err
		}
		var out map[string]any
		path := "/v2/{project_id}/instances/" + url.PathEscape(identity.InstanceID) + "/namespaces/" + url.PathEscape(identity.Namespace) +
			"/repositories/" + url.PathEscape(identity.Repository)
		if err := r.getSignedJSON(ctx, "swr", path, nil, &out); err != nil {
			return nil, lease, err
		}
		return out, lease, nil
	case "huawei.swr.tag.get":
		if !r.serviceAllowed("swr") {
			return nil, lease, fmt.Errorf("Huawei Cloud SWR is outside the Integration service scope")
		}
		identity, err := r.swrIdentity(task.Resource, "tag")
		if err != nil {
			return nil, lease, err
		}
		digest, err := r.resolveSWRTagDigest(ctx, identity)
		if err != nil {
			return nil, lease, err
		}
		var out map[string]any
		path := "/v2/{project_id}/instances/" + url.PathEscape(identity.InstanceID) + "/namespaces/" + url.PathEscape(identity.Namespace) +
			"/repositories/" + url.PathEscape(identity.Repository) + "/artifacts/" + url.PathEscape(digest)
		if err := r.getSignedJSON(ctx, "swr", path, nil, &out); err != nil {
			return nil, lease, err
		}
		return out, lease, nil
	default:
		return nil, lease, fmt.Errorf("unsupported Huawei Cloud action %q", task.Action.Name)
	}
}

// getAccount re-probes each in-scope service with a minimal list call so the
// caller sees the account's effective permissions right now.
func (r *HuaweiCloudRunner) getAccount(ctx context.Context, lease Lease) (map[string]any, Lease, error) {
	services := map[string]any{}
	for _, service := range huaweiCloudServiceOrder {
		if !r.serviceAllowed(service) {
			services[service] = "OUT_OF_SCOPE"
			continue
		}
		var err error
		switch service {
		case "ecs":
			err = r.getSignedJSON(ctx, "ecs", "/v1/{project_id}/cloudservers/detail", [][2]string{{"limit", "1"}}, &map[string]any{})
		case "rds":
			err = r.getSignedJSON(ctx, "rds", "/v3/{project_id}/instances", [][2]string{{"limit", "1"}}, &map[string]any{})
		case "elb":
			err = r.getSignedJSON(ctx, "elb", "/v3/{project_id}/elb/loadbalancers", [][2]string{{"limit", "1"}}, &map[string]any{})
		case "obs":
			_, err = r.obsRequest(ctx, "", "")
		case "lts":
			err = r.probeLTS(ctx)
		case "swr":
			err = r.probeSWR(ctx)
		}
		if err != nil {
			services[service] = "UNAVAILABLE: " + domain.SanitizeErrorMessage(err.Error())
		} else {
			services[service] = "ACTIVE"
		}
	}
	return map[string]any{"region": r.region, "services": services}, lease, nil
}

func (r *HuaweiCloudRunner) probeLTS(ctx context.Context) error {
	return r.getSignedJSON(ctx, "lts", "/v2/{project_id}/groups", [][2]string{{"limit", "1"}}, &map[string]any{})
}

func (r *HuaweiCloudRunner) probeSWR(ctx context.Context) error {
	return r.getSignedJSON(ctx, "swr", "/v2/{project_id}/instances", [][2]string{{"limit", "1"}}, &map[string]any{})
}

func (r *HuaweiCloudRunner) queryLTSLogs(ctx context.Context, groupID, streamID string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	start, startOK := parameters["startTime"].(float64)
	end, endOK := parameters["endTime"].(float64)
	if !startOK || !endOK || start <= 0 || end <= start || int64(end) > time.Now().Add(5*time.Minute).UnixMilli() {
		return nil, lease, fmt.Errorf("startTime/endTime must be valid UTC milliseconds and endTime must be after startTime")
	}
	keywords, _ := parameters["keywords"].(string)
	if len(keywords) > 1024 {
		return nil, lease, fmt.Errorf("keywords cannot exceed 1024 bytes")
	}
	limit := 100
	if value, ok := parameters["limit"].(float64); ok {
		limit = int(value)
	}
	if limit < 1 || limit > 500 {
		return nil, lease, fmt.Errorf("limit must be between 1 and 500")
	}
	body, err := json.Marshal(map[string]any{
		"start_time": int64(start), "end_time": int64(end), "keywords": keywords,
		"limit": limit, "is_desc": true, "search_type": "init",
	})
	if err != nil {
		return nil, lease, err
	}
	var out map[string]any
	path := "/v2/{project_id}/groups/" + groupID + "/streams/" + streamID + "/content/query"
	if err := r.sendSignedJSON(ctx, "lts", http.MethodPost, path, nil, string(body), &out); err != nil {
		return nil, lease, err
	}
	return map[string]any{"provider": "huaweicloud", "region": r.region, "logGroupId": groupID, "logStreamId": streamID, "result": out}, lease, nil
}

func (r *HuaweiCloudRunner) getECSMetrics(ctx context.Context, serverID string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	metric := "cpu_util"
	if name, ok := parameters["metricName"].(string); ok && name != "" {
		if !huaweiMetricPattern.MatchString(name) {
			return nil, lease, fmt.Errorf("metricName must match %s", huaweiMetricPattern.String())
		}
		metric = name
	}
	minutes := 60
	if window, ok := parameters["minutes"].(float64); ok && window >= 5 && window <= 1440 {
		minutes = int(window)
	}
	now := time.Now()
	query := [][2]string{
		{"namespace", "SYS.ECS"},
		{"metric_name", metric},
		{"dim.0", "instance_id," + serverID},
		{"from", fmt.Sprintf("%d", now.Add(-time.Duration(minutes)*time.Minute).UnixMilli())},
		{"to", fmt.Sprintf("%d", now.UnixMilli())},
		{"period", "300"},
		{"filter", "average"},
	}
	var out map[string]any
	if err := r.getSignedJSON(ctx, "ces", "/V1.0/{project_id}/metric-data", query, &out); err != nil {
		return nil, lease, err
	}
	return out, lease, nil
}

func (r *HuaweiCloudRunner) changeECSLifecycle(ctx context.Context, serverID, action string, lease Lease) (map[string]any, Lease, error) {
	operation, targetStatus := "", ""
	switch action {
	case "huawei.ecs.start":
		operation, targetStatus = "os-start", "ACTIVE"
	case "huawei.ecs.stop":
		operation, targetStatus = "os-stop", "SHUTOFF"
	case "huawei.ecs.reboot":
		operation, targetStatus = "reboot", "ACTIVE"
	default:
		return nil, lease, fmt.Errorf("unsupported ECS lifecycle action %q", action)
	}
	request := map[string]any{"servers": []map[string]string{{"id": serverID}}}
	if operation == "os-stop" || operation == "reboot" {
		request["type"] = "SOFT"
	}
	body, err := json.Marshal(map[string]any{operation: request})
	if err != nil {
		return nil, lease, err
	}
	var accepted map[string]any
	if err := r.sendSignedJSON(ctx, "ecs", http.MethodPost, "/v1/{project_id}/cloudservers/action", nil, string(body), &accepted); err != nil {
		return nil, lease, err
	}
	if jobID := stringValue(accepted["job_id"]); jobID != "" {
		if !huaweiCloudIDPattern.MatchString(jobID) {
			return nil, lease, fmt.Errorf("Huawei Cloud returned an invalid ECS lifecycle job id")
		}
		if err := r.waitECSJob(ctx, jobID); err != nil {
			return nil, lease, err
		}
	}
	server, err := r.waitECSStatus(ctx, serverID, targetStatus)
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"accepted": accepted, "server": server, "targetStatus": targetStatus}, lease, nil
}

func (r *HuaweiCloudRunner) waitECSJob(ctx context.Context, jobID string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var job map[string]any
		if err := r.getSignedJSON(ctx, "ecs", "/v1/{project_id}/jobs/"+jobID, nil, &job); err != nil {
			return fmt.Errorf("verify ECS lifecycle job %s: %w", jobID, err)
		}
		status := strings.ToUpper(stringValue(job["status"]))
		switch status {
		case "SUCCESS":
			return nil
		case "FAIL", "FAILED":
			return fmt.Errorf("Huawei Cloud ECS lifecycle job %s failed: %s", jobID, domain.SanitizeErrorMessage(stringValue(job["fail_reason"])))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("verify ECS lifecycle job %s: %w", jobID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *HuaweiCloudRunner) waitECSStatus(ctx context.Context, serverID, targetStatus string) (map[string]any, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var detail map[string]any
		if err := r.getSignedJSON(ctx, "ecs", "/v1/{project_id}/cloudservers/"+serverID, nil, &detail); err != nil {
			return nil, fmt.Errorf("verify ECS %s final state: %w", serverID, err)
		}
		server, _ := detail["server"].(map[string]any)
		status := strings.ToUpper(stringValue(server["status"]))
		if status == targetStatus {
			return server, nil
		}
		if status == "ERROR" {
			return nil, fmt.Errorf("ECS %s entered ERROR while waiting for %s", serverID, targetStatus)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("verify ECS %s final state %s: %w", serverID, targetStatus, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *HuaweiCloudRunner) updateECSMetadata(ctx context.Context, serverID string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	allowedParameters := map[string]bool{"description": true, "tags": true, "_wardenContext": true}
	for key := range parameters {
		if !allowedParameters[key] {
			return nil, lease, fmt.Errorf("unsupported ECS metadata parameter %q", key)
		}
	}
	contextValues, ok := parameters["_wardenContext"].(map[string]any)
	if !ok {
		return nil, lease, fmt.Errorf("trusted Warden execution context is missing")
	}
	projectID := stringValue(contextValues["projectId"])
	if !wardenProjectIDPattern.MatchString(projectID) {
		return nil, lease, fmt.Errorf("trusted Warden project is invalid")
	}
	tags, err := huaweiBusinessTags(parameters["tags"], 3)
	if err != nil {
		return nil, lease, err
	}
	trustedTags := map[string]string{
		"warden-managed-by": "warden", "warden-project-id": projectID,
		"warden-project-name":   huaweiTagValue(stringValue(contextValues["projectName"])),
		"warden-environment":    huaweiTagValue(stringValue(contextValues["environment"])),
		"warden-integration-id": huaweiTagValue(stringValue(contextValues["integrationId"])),
	}
	for key, value := range trustedTags {
		tags[key] = value
	}
	if len(tags) > 0 {
		tagList := make([]map[string]string, 0, len(tags))
		for key, value := range tags {
			tagList = append(tagList, map[string]string{"key": key, "value": value})
		}
		sort.Slice(tagList, func(i, j int) bool { return tagList[i]["key"] < tagList[j]["key"] })
		body, _ := json.Marshal(map[string]any{"action": "create", "tags": tagList})
		if err := r.sendSignedJSON(ctx, "ecs", http.MethodPost, "/v1/{project_id}/cloudservers/"+serverID+"/tags/action", nil, string(body), &map[string]any{}); err != nil {
			return nil, lease, fmt.Errorf("update ECS governance tags: %w", err)
		}
	}
	if description, exists := parameters["description"]; exists {
		text, ok := description.(string)
		if !ok || len([]rune(text)) > 85 {
			return nil, lease, fmt.Errorf("description must be a string of at most 85 characters")
		}
		body, _ := json.Marshal(map[string]any{"server": map[string]string{"description": text}})
		if err := r.sendSignedJSON(ctx, "ecs", http.MethodPut, "/v1/{project_id}/cloudservers/"+serverID, nil, string(body), &map[string]any{}); err != nil {
			return nil, lease, fmt.Errorf("update ECS description: %w", err)
		}
	}
	var final map[string]any
	if err := r.getSignedJSON(ctx, "ecs", "/v1/{project_id}/cloudservers/"+serverID, nil, &final); err != nil {
		return nil, lease, fmt.Errorf("verify ECS metadata update: %w", err)
	}
	return final, lease, nil
}

func (r *HuaweiCloudRunner) deleteECS(ctx context.Context, serverID string, task domain.TaskEnvelope, lease Lease) (map[string]any, Lease, error) {
	allowedParameters := map[string]bool{"confirmServerName": true, "deletePublicIP": true, "deleteVolumes": true}
	for key := range task.Parameters {
		if !allowedParameters[key] {
			return nil, lease, fmt.Errorf("unsupported ECS delete parameter %q", key)
		}
	}
	confirmation, _ := task.Parameters["confirmServerName"].(string)
	if confirmation == "" || confirmation != task.Resource.Name {
		return nil, lease, fmt.Errorf("confirmServerName must exactly match the authorized ECS name %q", task.Resource.Name)
	}
	deletePublicIP, _ := task.Parameters["deletePublicIP"].(bool)
	deleteVolumes, _ := task.Parameters["deleteVolumes"].(bool)
	body, _ := json.Marshal(map[string]any{
		"delete_publicip": deletePublicIP,
		"delete_volume":   deleteVolumes,
		"servers":         []map[string]string{{"id": serverID}},
	})
	var accepted map[string]any
	if err := r.sendSignedJSON(ctx, "ecs", http.MethodPost, "/v1/{project_id}/cloudservers/delete", nil, string(body), &accepted); err != nil {
		return nil, lease, err
	}
	if jobID := stringValue(accepted["job_id"]); jobID != "" {
		if !huaweiCloudIDPattern.MatchString(jobID) {
			return nil, lease, fmt.Errorf("Huawei Cloud returned an invalid ECS delete job id")
		}
	}
	return map[string]any{"accepted": accepted, "deletionSubmitted": true, "serverId": serverID}, lease, nil
}

func huaweiBusinessTags(value any, limit int) (map[string]string, error) {
	tags := map[string]string{}
	if value == nil {
		return tags, nil
	}
	raw, ok := value.(map[string]any)
	if !ok || len(raw) > limit {
		return nil, fmt.Errorf("tags must be an object containing at most %d business tags", limit)
	}
	for key, value := range raw {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(key) == "" || len([]rune(key)) > 36 || len([]rune(text)) > 43 ||
			strings.HasPrefix(strings.ToLower(key), "warden-") || strings.ContainsAny(key+text, "=,*<>\\|/") {
			return nil, fmt.Errorf("business tags must be strings and cannot use the reserved warden-* prefix")
		}
		tags[key] = text
	}
	return tags, nil
}

func (r *HuaweiCloudRunner) createECS(ctx context.Context, task domain.TaskEnvelope, lease Lease) (map[string]any, Lease, error) {
	parameters := task.Parameters
	allowedParameters := map[string]bool{
		"projectId": true, "region": true, "name": true, "description": true, "flavor": true,
		"imageId": true, "vpcId": true, "subnetId": true, "securityGroupIds": true,
		"systemDiskSize": true, "tags": true, "_wardenContext": true,
	}
	for key := range parameters {
		if !allowedParameters[key] {
			return nil, lease, fmt.Errorf("unsupported ECS create parameter %q", key)
		}
	}
	region, _ := parameters["region"].(string)
	if region != r.region {
		return nil, lease, fmt.Errorf("region must match the selected Huawei Cloud account resource (%s)", r.region)
	}
	name, _ := parameters["name"].(string)
	flavor, _ := parameters["flavor"].(string)
	imageID, _ := parameters["imageId"].(string)
	vpcID, _ := parameters["vpcId"].(string)
	subnetID, _ := parameters["subnetId"].(string)
	projectID, _ := parameters["projectId"].(string)
	if !huaweiECSNamePattern.MatchString(name) || !huaweiCloudIDPattern.MatchString(flavor) ||
		!huaweiCloudIDPattern.MatchString(imageID) || !huaweiCloudIDPattern.MatchString(vpcID) ||
		!huaweiCloudIDPattern.MatchString(subnetID) || !wardenProjectIDPattern.MatchString(projectID) {
		return nil, lease, fmt.Errorf("name, projectId, flavor, imageId, vpcId, and subnetId are required and must be valid")
	}
	diskSize := 40
	if value, ok := parameters["systemDiskSize"].(float64); ok {
		diskSize = int(value)
	}
	if diskSize < 40 || diskSize > 1024 {
		return nil, lease, fmt.Errorf("systemDiskSize must be between 40 and 1024 GiB")
	}
	securityGroups := []map[string]string{}
	if raw, ok := parameters["securityGroupIds"].([]any); ok {
		for _, value := range raw {
			id, ok := value.(string)
			if !ok || !huaweiCloudIDPattern.MatchString(id) {
				return nil, lease, fmt.Errorf("securityGroupIds contains an invalid id")
			}
			securityGroups = append(securityGroups, map[string]string{"id": id})
		}
	}
	if len(securityGroups) == 0 || len(securityGroups) > 20 {
		return nil, lease, fmt.Errorf("securityGroupIds must contain between 1 and 20 ids")
	}
	contextValues, ok := parameters["_wardenContext"].(map[string]any)
	if !ok {
		return nil, lease, fmt.Errorf("trusted Warden execution context is missing")
	}
	contextProjectID, _ := contextValues["projectId"].(string)
	if contextProjectID != projectID {
		return nil, lease, fmt.Errorf("projectId does not match the trusted Warden execution context")
	}
	tags := map[string]string{}
	if raw, ok := parameters["tags"].(map[string]any); ok {
		if len(raw) > 3 {
			return nil, lease, fmt.Errorf("at most 3 custom tags are allowed because Warden reserves 7 governance tags")
		}
		for key, value := range raw {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(key) == "" || len([]rune(key)) > 36 || len([]rune(text)) > 43 ||
				strings.HasPrefix(strings.ToLower(key), "warden-") || strings.ContainsAny(key+text, "=,*<>\\|/") {
				return nil, lease, fmt.Errorf("custom tags must be strings and cannot use the reserved warden-* prefix")
			}
			tags[key] = text
		}
	}
	trustedTags := map[string]string{
		"warden-managed-by": "warden", "warden-project-id": contextProjectID,
		"warden-project-name": huaweiTagValue(stringValue(contextValues["projectName"])), "warden-environment": huaweiTagValue(stringValue(contextValues["environment"])),
		"warden-created-by": huaweiTagValue(stringValue(contextValues["createdBy"])), "warden-execution-id": huaweiTagValue(task.ExecutionID),
		"warden-integration-id": huaweiTagValue(stringValue(contextValues["integrationId"])),
	}
	for key, value := range trustedTags {
		tags[key] = value
	}
	tagList := make([]map[string]string, 0, len(tags))
	for key, value := range tags {
		tagList = append(tagList, map[string]string{"key": key, "value": value})
	}
	sort.Slice(tagList, func(i, j int) bool { return tagList[i]["key"] < tagList[j]["key"] })
	server := map[string]any{
		"name": name, "imageRef": imageID, "flavorRef": flavor, "vpcid": vpcID,
		"nics": []map[string]string{{"subnet_id": subnetID}}, "security_groups": securityGroups,
		"root_volume": map[string]any{"volumetype": "GPSSD", "size": diskSize},
		"metadata":    trustedTags,
	}
	if description, ok := parameters["description"].(string); ok && len([]rune(description)) <= 85 {
		server["description"] = description
	} else if description != "" {
		return nil, lease, fmt.Errorf("description cannot exceed 85 characters")
	}
	body, err := json.Marshal(map[string]any{"server": server})
	if err != nil {
		return nil, lease, err
	}
	var output map[string]any
	if err := r.sendSignedJSON(ctx, "ecs", http.MethodPost, "/v1/{project_id}/cloudservers",
		[][2]string{{"client_token", task.ExecutionID}}, string(body), &output); err != nil {
		return nil, lease, err
	}
	if jobID := stringValue(output["job_id"]); jobID != "" {
		if !huaweiCloudIDPattern.MatchString(jobID) {
			return nil, lease, fmt.Errorf("Huawei Cloud returned an invalid ECS create job id")
		}
		if err := r.waitECSJob(ctx, jobID); err != nil {
			return nil, lease, err
		}
	}
	serverID := ""
	if serverIDs, ok := output["server_ids"].([]any); ok && len(serverIDs) == 1 {
		serverID, _ = serverIDs[0].(string)
	}
	if !huaweiResourceIDPattern.MatchString(serverID) {
		serverID, err = r.waitCreatedECSByName(ctx, name)
		if err != nil {
			return nil, lease, fmt.Errorf("%w: ECS create job succeeded but the new server id could not be resolved; do not retry creation: %v", errHuaweiCreatePartial, err)
		}
	}
	output["server_ids"] = []string{serverID}
	tagBody, _ := json.Marshal(map[string]any{"action": "create", "tags": tagList})
	if err := r.sendSignedJSON(ctx, "ecs", http.MethodPost, "/v1/{project_id}/cloudservers/"+serverID+"/tags/action", nil, string(tagBody), &map[string]any{}); err != nil {
		return nil, lease, fmt.Errorf("%w: ECS %s was created but governance tag attachment failed; do not retry creation: %v", errHuaweiCreatePartial, serverID, err)
	}
	var detail map[string]any
	if err := r.getSignedJSON(ctx, "ecs", "/v1/{project_id}/cloudservers/"+serverID, nil, &detail); err != nil {
		return nil, lease, fmt.Errorf("%w: ECS %s was created but final verification failed; do not retry creation: %v", errHuaweiCreatePartial, serverID, err)
	}
	output["server"] = detail["server"]
	return output, lease, nil
}

func (r *HuaweiCloudRunner) waitCreatedECSByName(ctx context.Context, name string) (string, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var out struct {
			Servers []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"servers"`
		}
		if err := r.getSignedJSON(ctx, "ecs", "/v1/{project_id}/cloudservers/detail", [][2]string{{"name", name}, {"limit", "2"}}, &out); err != nil {
			return "", err
		}
		matches := []string{}
		for _, server := range out.Servers {
			if server.Name == name && huaweiResourceIDPattern.MatchString(server.ID) {
				matches = append(matches, server.ID)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return "", fmt.Errorf("multiple ECS instances named %q were returned", name)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func huaweiTagValue(value string) string {
	value = strings.Map(func(r rune) rune {
		if strings.ContainsRune("=,*<>\\|/", r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(value))
	runes := []rune(value)
	if len(runes) > 43 {
		runes = runes[:43]
	}
	if len(runes) == 0 {
		return "-"
	}
	return string(runes)
}

func (r *HuaweiCloudRunner) lease(expiresAt time.Time) Lease {
	fingerprint := sha256.Sum256([]byte(r.region + "/" + r.accessKey))
	return Lease{Provider: "HUAWEICLOUD_CREDENTIAL_REFERENCE", Fingerprint: hex.EncodeToString(fingerprint[:8]), ExpiresAt: expiresAt}
}

func validateHuaweiActionResource(action, resourceType string) error {
	expected := map[string]string{
		"huawei.account.get":             "huawei_cloud_account",
		"huawei.ecs.create":              "huawei_cloud_account",
		"huawei.ecs.get":                 "huawei_ecs_instance",
		"huawei.ecs.metrics":             "huawei_ecs_instance",
		"huawei.ecs.start":               "huawei_ecs_instance",
		"huawei.ecs.stop":                "huawei_ecs_instance",
		"huawei.ecs.reboot":              "huawei_ecs_instance",
		"huawei.ecs.update_metadata":     "huawei_ecs_instance",
		"huawei.ecs.delete":              "huawei_ecs_instance",
		"huawei.rds.get":                 "huawei_rds_instance",
		"huawei.elb.get":                 "huawei_elb_loadbalancer",
		"huawei.obs.bucket.get":          "huawei_obs_bucket",
		"huawei.obs.object.list":         "huawei_obs_bucket",
		"huawei.obs.object.metadata.get": "huawei_obs_bucket",
		"huawei.obs.object.delete":       "huawei_obs_bucket",
		"huawei.lts.logs.query":          "huawei_lts_log_stream",
		"huawei.swr.instance.get":        "huawei_swr_instance",
		"huawei.swr.namespace.get":       "huawei_swr_namespace",
		"huawei.swr.repository.get":      "huawei_swr_repository",
		"huawei.swr.tag.get":             "huawei_swr_tag",
	}
	if expectedType, ok := expected[action]; !ok || expectedType != resourceType {
		return fmt.Errorf("Huawei Cloud action %q is not allowed for resource type %q", action, resourceType)
	}
	return nil
}

func huaweiResourceID(resource domain.TaskResource, kind, expectedRegion string) (string, error) {
	prefix := kind + "/"
	if !strings.HasPrefix(resource.ExternalID, prefix) {
		return "", fmt.Errorf("Huawei Cloud %s identity does not match its authorized scope", kind)
	}
	id := strings.TrimPrefix(resource.ExternalID, prefix)
	if parts := strings.SplitN(id, "/", 2); len(parts) == 2 && huaweiRegionPattern.MatchString(parts[0]) {
		if parts[0] != expectedRegion {
			return "", fmt.Errorf("Huawei Cloud %s identity belongs to region %s, not authorized region %s", kind, parts[0], expectedRegion)
		}
		id = parts[1]
	}
	if !huaweiResourceIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid Huawei Cloud %s id", kind)
	}
	return id, nil
}

func (r *HuaweiCloudRunner) scopedExternalID(kind, id string) string {
	if r.regionScopedIDs {
		return kind + "/" + r.region + "/" + id
	}
	return kind + "/" + id
}

func (r *HuaweiCloudRunner) swrExternalID(kind string, parts ...string) string {
	escaped := make([]string, len(parts))
	for i, part := range parts {
		escaped[i] = url.PathEscape(part)
	}
	return r.scopedExternalID("swr-"+kind, strings.Join(escaped, "/"))
}

type huaweiSWRIdentity struct {
	InstanceID string
	Namespace  string
	Repository string
	Tag        string
}

func (r *HuaweiCloudRunner) swrIdentity(resource domain.TaskResource, kind string) (huaweiSWRIdentity, error) {
	prefix := "swr-" + kind + "/"
	if !strings.HasPrefix(resource.ExternalID, prefix) {
		return huaweiSWRIdentity{}, fmt.Errorf("Huawei Cloud SWR %s identity does not match its authorized scope", kind)
	}
	parts := strings.Split(strings.TrimPrefix(resource.ExternalID, prefix), "/")
	if len(parts) > 0 && huaweiRegionPattern.MatchString(parts[0]) {
		if parts[0] != r.region {
			return huaweiSWRIdentity{}, fmt.Errorf("Huawei Cloud SWR %s identity belongs to region %s, not authorized region %s", kind, parts[0], r.region)
		}
		parts = parts[1:]
	}
	identity := huaweiSWRIdentity{}
	expected := 1
	switch kind {
	case "namespace":
		expected = 2
	case "repository":
		expected = 3
	case "tag":
		expected = 4
	}
	if len(parts) != expected {
		return huaweiSWRIdentity{}, fmt.Errorf("invalid Huawei Cloud SWR %s identity", kind)
	}
	decoded := make([]string, len(parts))
	for i, part := range parts {
		value, err := url.PathUnescape(part)
		if err != nil {
			return huaweiSWRIdentity{}, fmt.Errorf("invalid Huawei Cloud SWR %s identity", kind)
		}
		decoded[i] = value
	}
	if !huaweiSWRInstancePattern.MatchString(decoded[0]) {
		return huaweiSWRIdentity{}, fmt.Errorf("invalid Huawei Cloud SWR instance id")
	}
	identity.InstanceID = decoded[0]
	if len(decoded) > 1 {
		if !huaweiSWRNamespacePattern.MatchString(decoded[1]) {
			return huaweiSWRIdentity{}, fmt.Errorf("invalid Huawei Cloud SWR namespace")
		}
		identity.Namespace = decoded[1]
	}
	if len(decoded) > 2 {
		if len(decoded[2]) > 255 || !huaweiSWRRepositoryPattern.MatchString(decoded[2]) {
			return huaweiSWRIdentity{}, fmt.Errorf("invalid Huawei Cloud SWR repository")
		}
		identity.Repository = decoded[2]
	}
	if len(decoded) > 3 {
		if !huaweiSWRTagPattern.MatchString(decoded[3]) {
			return huaweiSWRIdentity{}, fmt.Errorf("invalid Huawei Cloud SWR tag")
		}
		identity.Tag = decoded[3]
	}
	return identity, nil
}

func (r *HuaweiCloudRunner) resolveSWRTagDigest(ctx context.Context, identity huaweiSWRIdentity) (string, error) {
	tags, err := r.listSWRTags(ctx, identity.InstanceID, identity.Namespace, identity.Repository)
	if err != nil {
		return "", err
	}
	for _, tag := range tags {
		if strings.TrimSpace(tag.Name) != identity.Tag {
			continue
		}
		digest := strings.TrimSpace(tag.Digest)
		if digest == "" {
			return "", fmt.Errorf("Huawei Cloud SWR tag %s has no artifact digest", identity.Tag)
		}
		return digest, nil
	}
	return "", fmt.Errorf("Huawei Cloud SWR tag %s was not found in repository %s", identity.Tag, identity.Repository)
}

func (r *HuaweiCloudRunner) scopedLTSGroupID(groupID string) string {
	return "lts-group/" + r.region + "/" + groupID
}

func (r *HuaweiCloudRunner) scopedLTSStreamID(groupID, streamID string) string {
	return "lts-stream/" + r.region + "/" + groupID + "/" + streamID
}

func (r *HuaweiCloudRunner) ltsStreamIdentity(resource domain.TaskResource) (string, string, error) {
	parts := strings.Split(resource.ExternalID, "/")
	if len(parts) != 4 || parts[0] != "lts-stream" || parts[1] != r.region {
		return "", "", fmt.Errorf("Huawei Cloud LTS stream identity does not match its authorized scope")
	}
	if !huaweiCloudIDPattern.MatchString(parts[2]) || !huaweiCloudIDPattern.MatchString(parts[3]) {
		return "", "", fmt.Errorf("invalid Huawei Cloud LTS stream identity")
	}
	return parts[2], parts[3], nil
}

func huaweiTaskRegion(resource domain.TaskResource) (string, error) {
	parts := strings.Split(resource.ExternalID, "/")
	if len(parts) >= 2 && parts[0] == "account" && huaweiRegionPattern.MatchString(parts[1]) {
		return parts[1], nil
	}
	if len(parts) >= 3 && huaweiRegionPattern.MatchString(parts[1]) {
		return parts[1], nil
	}
	return "", fmt.Errorf("Huawei Cloud resource identity does not include its region")
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (r *HuaweiCloudRunner) forRegion(region string) *HuaweiCloudRunner {
	return &HuaweiCloudRunner{
		region: region, regionMode: "single", regionScopedIDs: r.regionScopedIDs,
		accessKey: r.accessKey, secretKey: r.secretKey, allowedServices: r.allowedServices,
		allowAll: r.allowAll, integrationID: r.integrationID, http: r.http, overrides: r.overrides,
	}
}

func (r *HuaweiCloudRunner) resolveRegions(ctx context.Context) ([]string, error) {
	if r.regionMode == "allowlist" {
		result := []string{}
		for _, region := range r.regions {
			if !r.deniedRegions[region] {
				result = append(result, region)
			}
		}
		if len(result) == 0 {
			return nil, fmt.Errorf("Huawei Cloud region scope is empty after deny rules")
		}
		return result, nil
	}
	if r.regionMode == "single" {
		return []string{r.region}, nil
	}
	r.mu.Lock()
	if len(r.resolvedRegions) > 0 {
		cached := append([]string(nil), r.resolvedRegions...)
		r.mu.Unlock()
		return cached, nil
	}
	r.mu.Unlock()
	var out struct {
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	data, err := r.signedRequest(ctx, "iam", http.MethodGet, "/v3/projects", nil, "")
	if err != nil {
		return nil, fmt.Errorf("list Huawei Cloud regions: %w", err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse Huawei Cloud regions: %w", err)
	}
	seen := map[string]bool{}
	result := []string{}
	for _, project := range out.Projects {
		if huaweiRegionPattern.MatchString(project.Name) && !r.deniedRegions[project.Name] && !seen[project.Name] {
			seen[project.Name] = true
			result = append(result, project.Name)
		}
	}
	sort.Strings(result)
	if len(result) == 0 {
		return nil, fmt.Errorf("no accessible Huawei Cloud regions found")
	}
	r.mu.Lock()
	r.resolvedRegions = append([]string(nil), result...)
	r.mu.Unlock()
	return result, nil
}

// --- Project resolution ---

// resolveProjectID looks up the IAM project bound to the region. Cached for
// the runner's lifetime (a rebuilt integration gets a fresh runner).
func (r *HuaweiCloudRunner) resolveProjectID(ctx context.Context) (string, error) {
	r.mu.Lock()
	cached := r.projectID
	r.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	var out struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	data, err := r.signedRequest(ctx, "iam", http.MethodGet, "/v3/projects", [][2]string{{"name", r.region}}, "")
	if err != nil {
		return "", fmt.Errorf("resolve Huawei Cloud project for %s: %w", r.region, err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("parse Huawei Cloud project response: %w", err)
	}
	for _, project := range out.Projects {
		if project.Name == r.region && project.ID != "" {
			r.mu.Lock()
			r.projectID = project.ID
			r.mu.Unlock()
			return project.ID, nil
		}
	}
	return "", fmt.Errorf("Huawei Cloud project for region %s not found (check IAM user permissions)", r.region)
}

// getSignedJSON resolves the project id, substitutes it into the path, issues
// a signed GET, and decodes the JSON response.
func (r *HuaweiCloudRunner) getSignedJSON(ctx context.Context, service, path string, query [][2]string, output any) error {
	return r.sendSignedJSON(ctx, service, http.MethodGet, path, query, "", output)
}

func (r *HuaweiCloudRunner) sendSignedJSON(ctx context.Context, service, method, path string, query [][2]string, body string, output any) error {
	if strings.Contains(path, "{project_id}") {
		projectID, err := r.resolveProjectID(ctx)
		if err != nil {
			return err
		}
		path = strings.ReplaceAll(path, "{project_id}", projectID)
	}
	data, err := r.signedRequest(ctx, service, method, path, query, body)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	return json.Unmarshal(data, output)
}

// --- Request signing (SDK-HMAC-SHA256, Huawei API gateway scheme) ---

// urlEscape percent-encodes per RFC 3986 (spaces as %20, not +) as required
// by the Huawei signing spec. Sign string and request URL must byte-match.
func urlEscape(value string) string {
	const upperhex = "0123456789ABCDEF"
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			builder.WriteByte(c)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(upperhex[c>>4])
		builder.WriteByte(upperhex[c&15])
	}
	return builder.String()
}

// encodeHuaweiQuery builds the canonical (and actual) query string: keys
// sorted, values percent-encoded with %20 for spaces. Sign string and request
// URL must byte-match, so both use this exact output.
func encodeHuaweiQuery(query [][2]string) string {
	pairs := make([]string, 0, len(query))
	for _, kv := range query {
		pairs = append(pairs, urlEscape(kv[0])+"="+urlEscape(kv[1]))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

func (r *HuaweiCloudRunner) signedRequest(ctx context.Context, service, method, path string, query [][2]string, body string) ([]byte, error) {
	base := r.endpoint(service)
	rawQuery := encodeHuaweiQuery(query)
	requestURL := base + path
	if rawQuery != "" {
		requestURL += "?" + rawQuery
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	date := time.Now().UTC().Format("20060102T150405Z")
	req.Header.Set("X-Sdk-Date", date)
	req.Header.Set("Content-Type", "application/json")

	canonicalURI := path
	if !strings.HasSuffix(canonicalURI, "/") {
		canonicalURI += "/"
	}
	headers := map[string]string{
		"host":         req.URL.Host,
		"x-sdk-date":   date,
		"content-type": "application/json",
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + headers[name] + "\n")
	}
	signedHeaders := strings.Join(names, ";")
	bodyHash := sha256.Sum256([]byte(body))
	canonicalRequest := strings.Join([]string{
		method, canonicalURI, rawQuery, canonicalHeaders.String(), signedHeaders, hex.EncodeToString(bodyHash[:]),
	}, "\n")
	requestHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := "SDK-HMAC-SHA256\n" + date + "\n" + hex.EncodeToString(requestHash[:])
	mac := hmac.New(sha256.New, []byte(r.secretKey))
	mac.Write([]byte(stringToSign))
	signature := hex.EncodeToString(mac.Sum(nil))
	req.Header.Set("Authorization",
		"SDK-HMAC-SHA256 Access="+r.accessKey+", SignedHeaders="+signedHeaders+", Signature="+signature)

	return r.doRequest(req)
}

// obsRequest issues a request against OBS, which uses its own S3-style
// signature (HMAC-SHA1 over a canonicalized string). An empty bucket lists
// all buckets; a subresource such as "storageinfo" queries bucket metadata.
func (r *HuaweiCloudRunner) obsRequest(ctx context.Context, bucket, subresource string) ([]byte, error) {
	base := r.endpoint("obs")
	requestURL := base + "/"
	canonicalResource := "/"
	if bucket != "" {
		if override, ok := r.overrides["obs"]; ok && override != "" {
			requestURL = strings.TrimRight(override, "/") + "/" + bucket
		} else {
			// Virtual-host style: https://{bucket}.obs.{region}.myhuaweicloud.com
			requestURL = "https://" + bucket + ".obs." + r.region + ".myhuaweicloud.com/"
		}
		canonicalResource = "/" + bucket + "/"
	}
	if subresource != "" {
		requestURL += "?" + subresource
		canonicalResource += "?" + subresource
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)
	stringToSign := "GET\n\n\n" + date + "\n" + canonicalResource
	mac := hmac.New(sha1.New, []byte(r.secretKey))
	mac.Write([]byte(stringToSign))
	req.Header.Set("Authorization", "OBS "+r.accessKey+":"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))

	return r.doRequest(req)
}

// obsSignedRequest issues an OBS request with the given method, bucket, object
// key and query parameters. Unlike obsRequest it supports HEAD and DELETE, and
// appends object keys to the canonical resource for correct signing. Regular
// query parameters (prefix, max-keys) are sent in the URL but NOT included in
// the canonical resource — only subresources belong there per OBS v1 signing.
func (r *HuaweiCloudRunner) obsSignedRequest(ctx context.Context, method, bucket, objectKey string, query url.Values) ([]byte, http.Header, error) {
	base := r.endpoint("obs")
	requestURL := base + "/"
	canonicalResource := "/"
	if bucket != "" {
		if override, ok := r.overrides["obs"]; ok && override != "" {
			requestURL = strings.TrimRight(override, "/") + "/" + bucket
		} else {
			requestURL = "https://" + bucket + ".obs." + r.region + ".myhuaweicloud.com/"
		}
		canonicalResource = "/" + bucket + "/"
	}
	if objectKey != "" {
		requestURL = strings.TrimRight(requestURL, "/") + "/" + objectKey
		canonicalResource += objectKey
	}
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return nil, nil, err
	}
	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)
	stringToSign := method + "\n\n\n" + date + "\n" + canonicalResource
	mac := hmac.New(sha1.New, []byte(r.secretKey))
	mac.Write([]byte(stringToSign))
	req.Header.Set("Authorization", "OBS "+r.accessKey+":"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))

	resp, err := r.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, huaweiMaxResponse+1))
	if readErr != nil {
		return nil, resp.Header, readErr
	}
	if int64(len(data)) > huaweiMaxResponse {
		data = data[:huaweiMaxResponse]
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if len(data) > 4096 {
			data = data[:4096]
		}
		message := strings.ReplaceAll(strings.TrimSpace(string(data)), r.secretKey, "[REDACTED]")
		message = strings.ReplaceAll(message, r.accessKey, "[REDACTED]")
		return nil, resp.Header, fmt.Errorf("Huawei Cloud returned %d: %s", resp.StatusCode, message)
	}
	return data, resp.Header, nil
}

func (r *HuaweiCloudRunner) doRequest(req *http.Request) ([]byte, error) {
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, huaweiMaxResponse+1))
	if readErr != nil {
		return nil, readErr
	}
	if int64(len(data)) > huaweiMaxResponse {
		data = data[:huaweiMaxResponse]
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if len(data) > 4096 {
			data = data[:4096]
		}
		message := strings.ReplaceAll(strings.TrimSpace(string(data)), r.secretKey, "[REDACTED]")
		message = strings.ReplaceAll(message, r.accessKey, "[REDACTED]")
		return nil, fmt.Errorf("Huawei Cloud returned %d: %s", resp.StatusCode, message)
	}
	return data, nil
}
