package connector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

const (
	grafanaMaxResponse = 4 << 20
)

// grafanaUIDPattern matches Grafana dashboard/folder/datasource UIDs: short
// alphanumeric slugs used in API paths. Kept conservative so path injection
// is impossible even if a caller crafts a malicious ExternalID.
var grafanaUIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,40}$`)

var GrafanaResourceTypes = []string{
	"grafana_instance", "grafana_folder", "grafana_dashboard", "grafana_datasource",
}

var GrafanaCapabilities = []string{
	"grafana.instance.get@v1", "grafana.folder.list@v1", "grafana.folder.get@v1", "grafana.folder.create@v1", "grafana.folder.update@v1", "grafana.folder.delete@v1",
	"grafana.dashboard.list@v1", "grafana.dashboard.get@v1", "grafana.dashboard.export@v1", "grafana.dashboard.create@v1",
	"grafana.dashboard.update@v1", "grafana.dashboard.delete@v1", "grafana.datasource.list@v1", "grafana.datasource.query@v1",
	"grafana.datasource.get@v1", "grafana.datasource.create@v1", "grafana.datasource.update@v1", "grafana.datasource.delete@v1",
}

type GrafanaConfig struct {
	BaseURL           string
	APIToken          string
	AllowedDashboards []string
	AllowAll          bool
	IntegrationID     string
	HTTPClient        *http.Client
}

type GrafanaRunner struct {
	baseURL           *url.URL
	apiToken          string
	allowedDashboards []string
	allowAll          bool
	integrationID     string
	http              *http.Client
}

// NewGrafanaRunner materializes a Grafana protocol adapter. The API token is a
// Grafana Service Account Token sent as a Bearer header; there is no CSRF
// crumb (unlike Jenkins). Empty AllowedDashboards means "allow all dashboards".
func NewGrafanaRunner(config GrafanaConfig) (*GrafanaRunner, error) {
	endpoint, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, fmt.Errorf("WARDEN_GRAFANA_URL must be an absolute http(s) URL")
	}
	if endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.User != nil {
		return nil, fmt.Errorf("WARDEN_GRAFANA_URL must not contain credentials, query, or fragment")
	}
	if err := rejectMetadataEndpoint(endpoint); err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.APIToken) == "" {
		return nil, fmt.Errorf("Grafana API token is required")
	}
	// Empty AllowedDashboards means "allow all dashboards". The scope is
	// narrowed later from the control plane via the integration's dashboard
	// picker.
	allowed := make([]string, 0, len(config.AllowedDashboards))
	for _, uid := range config.AllowedDashboards {
		uid = strings.TrimSpace(uid)
		if uid == "" || !grafanaUIDPattern.MatchString(uid) {
			return nil, fmt.Errorf("Grafana dashboard scope UID must match %s", grafanaUIDPattern.String())
		}
		allowed = append(allowed, uid)
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")

	return &GrafanaRunner{baseURL: endpoint, apiToken: config.APIToken, allowedDashboards: allowed, allowAll: config.AllowAll, integrationID: config.IntegrationID, http: client}, nil
}

func (r *GrafanaRunner) ListResources(ctx context.Context) []DiscoveredResource {
	resources := []DiscoveredResource{{
		ExternalID: "instance/" + r.baseURL.Host, Name: r.baseURL.Host, ResourceType: "grafana_instance",
		Labels: map[string]any{}, Annotations: map[string]any{},
		Metadata: map[string]any{"endpoint": r.baseURL.Scheme + "://" + r.baseURL.Host},
	}}
	resources = append(resources, r.discoverFolders(ctx)...)
	resources = append(resources, r.discoverDashboards(ctx)...)
	resources = append(resources, r.discoverDatasources(ctx)...)

	// Stamp every discovered resource with the owning integration id so the
	// control plane can route execution back to this runner.
	if r.integrationID != "" {
		for i := range resources {
			resources[i].IntegrationID = r.integrationID
		}
	}

	return resources
}

func (r *GrafanaRunner) Execute(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	if err := r.validateResource(task.Resource); err != nil {
		return nil, Lease{}, err
	}
	if err := validateGrafanaActionResource(task.Action.Name, task.Resource.Type); err != nil {
		return nil, Lease{}, err
	}
	lease := r.lease(task.Grant.ExpiresAt)

	switch task.Action.Name {
	case "grafana.instance.get":
		return r.getInstance(ctx, lease)
	case "grafana.folder.list":
		return r.listFolders(ctx, lease)
	case "grafana.folder.get":
		return r.getFolder(ctx, task.Resource, lease)
	case "grafana.folder.create":
		return r.createFolder(ctx, task.Parameters, lease)
	case "grafana.folder.update":
		return r.updateFolder(ctx, task.Resource, task.Parameters, lease)
	case "grafana.folder.delete":
		return r.deleteFolder(ctx, task.Resource, lease)
	case "grafana.dashboard.list":
		return r.listDashboards(ctx, lease)
	case "grafana.dashboard.get":
		return r.getDashboard(ctx, task.Resource, lease)
	case "grafana.dashboard.export":
		return r.exportDashboard(ctx, task.Resource, lease)
	case "grafana.dashboard.create":
		return r.createDashboard(ctx, task.Parameters, lease)
	case "grafana.dashboard.update":
		return r.updateDashboard(ctx, task.Resource, task.Parameters, lease)
	case "grafana.dashboard.delete":
		return r.deleteDashboard(ctx, task.Resource, lease)
	case "grafana.datasource.list":
		return r.listDatasources(ctx, lease)
	case "grafana.datasource.query":
		return r.queryDatasource(ctx, task.Resource, task.Parameters, lease)
	case "grafana.datasource.get":
		return r.getDatasource(ctx, task.Resource, lease)
	case "grafana.datasource.create":
		return r.createDatasource(ctx, task.Parameters, lease)
	case "grafana.datasource.update":
		return r.updateDatasource(ctx, task.Resource, task.Parameters, lease)
	case "grafana.datasource.delete":
		return r.deleteDatasource(ctx, task.Resource, lease)
	default:
		return nil, lease, fmt.Errorf("unsupported Grafana action %q", task.Action.Name)
	}
}

func (r *GrafanaRunner) discoverFolders(ctx context.Context) []DiscoveredResource {
	var folders []struct {
		UID   string `json:"uid"`
		Title string `json:"title"`
		ID    int64  `json:"id"`
	}
	if err := r.getJSON(ctx, "/api/folders?limit=1000", &folders); err != nil {
		return nil
	}
	resources := make([]DiscoveredResource, 0, len(folders))
	for _, folder := range folders {
		if folder.UID == "" {
			continue
		}
		resources = append(resources, DiscoveredResource{
			ExternalID: "folder/" + folder.UID, Name: folder.Title, ResourceType: "grafana_folder",
			Labels: map[string]any{}, Annotations: map[string]any{},
			Metadata: map[string]any{"uid": folder.UID, "title": folder.Title, "id": folder.ID},
		})
	}

	return resources
}

func (r *GrafanaRunner) discoverDashboards(ctx context.Context) []DiscoveredResource {
	var dashboards []struct {
		UID         string `json:"uid"`
		Title       string `json:"title"`
		URI         string `json:"uri"`
		FolderTitle string `json:"folderTitle"`
		FolderUID   string `json:"folderUid"`
	}
	if err := r.getJSON(ctx, "/api/search?type=dash-db&limit=1000", &dashboards); err != nil {
		return nil
	}
	resources := make([]DiscoveredResource, 0, len(dashboards))
	for _, dashboard := range dashboards {
		if dashboard.UID == "" {
			continue
		}
		// Scope filters discovery: a connector with allowedDashboards set only
		// surfaces authorized dashboards so the agent catalog stays clean.
		if !r.dashboardAllowed(dashboard.UID) {
			continue
		}
		resources = append(resources, DiscoveredResource{
			ExternalID: "dashboard/" + dashboard.UID, Name: dashboard.Title, ResourceType: "grafana_dashboard",
			Labels: map[string]any{}, Annotations: map[string]any{},
			Metadata: map[string]any{"uid": dashboard.UID, "title": dashboard.Title, "folderUid": dashboard.FolderUID, "folderTitle": dashboard.FolderTitle},
		})
	}

	return resources
}

func (r *GrafanaRunner) discoverDatasources(ctx context.Context) []DiscoveredResource {
	var datasources []struct {
		UID  string `json:"uid"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := r.getJSON(ctx, "/api/datasources", &datasources); err != nil {
		return nil
	}
	resources := make([]DiscoveredResource, 0, len(datasources))
	for _, ds := range datasources {
		if ds.UID == "" {
			continue
		}
		resources = append(resources, DiscoveredResource{
			ExternalID: "datasource/" + ds.UID, Name: ds.Name, ResourceType: "grafana_datasource",
			Labels: map[string]any{"type": ds.Type}, Annotations: map[string]any{},
			Metadata: map[string]any{"uid": ds.UID, "name": ds.Name, "type": ds.Type},
		})
	}

	return resources
}

func (r *GrafanaRunner) getInstance(ctx context.Context, lease Lease) (map[string]any, Lease, error) {
	var result map[string]any
	err := r.getJSON(ctx, "/api/health", &result)
	return result, lease, err
}

func (r *GrafanaRunner) listFolders(ctx context.Context, lease Lease) (map[string]any, Lease, error) {
	var folders []map[string]any
	if err := r.getJSON(ctx, "/api/folders?limit=1000", &folders); err != nil {
		return nil, lease, err
	}
	return map[string]any{"folders": folders}, lease, nil
}

func (r *GrafanaRunner) getFolder(ctx context.Context, resource domain.TaskResource, lease Lease) (map[string]any, Lease, error) {
	uid, err := grafanaResourceUID(resource, "folder")
	if err != nil {
		return nil, lease, err
	}
	var folder map[string]any
	err = r.getJSON(ctx, "/api/folders/"+uid, &folder)
	return folder, lease, err
}

func (r *GrafanaRunner) createFolder(ctx context.Context, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	title, _ := parameters["title"].(string)
	if strings.TrimSpace(title) == "" || len(title) > 255 {
		return nil, lease, fmt.Errorf("title must be a non-empty string up to 255 characters")
	}
	uid, _ := parameters["uid"].(string)
	if uid != "" && !grafanaUIDPattern.MatchString(uid) {
		return nil, lease, fmt.Errorf("uid must match %s", grafanaUIDPattern.String())
	}
	body := map[string]any{"title": title}
	if uid != "" {
		body["uid"] = uid
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, lease, err
	}
	data, err := r.do(ctx, http.MethodPost, "/api/folders", bytes.NewReader(payload), "application/json", grafanaMaxResponse)
	if err != nil {
		return nil, lease, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, lease, err
	}
	return result, lease, nil
}

func (r *GrafanaRunner) deleteFolder(ctx context.Context, resource domain.TaskResource, lease Lease) (map[string]any, Lease, error) {
	uid, err := grafanaResourceUID(resource, "folder")
	if err != nil {
		return nil, lease, err
	}
	_, err = r.do(ctx, http.MethodDelete, "/api/folders/"+uid, nil, "", 64<<10)
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"uid": uid, "deleted": true}, lease, nil
}

func (r *GrafanaRunner) updateFolder(ctx context.Context, resource domain.TaskResource, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	uid, err := grafanaResourceUID(resource, "folder")
	if err != nil {
		return nil, lease, err
	}
	if err := rejectGrafanaSensitiveFields(parameters); err != nil {
		return nil, lease, err
	}
	title, _ := parameters["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 255 {
		return nil, lease, fmt.Errorf("title must be a non-empty string up to 255 characters")
	}
	version, ok := optionalInt(parameters["version"], 0, int(^uint(0)>>1))
	if !ok {
		return nil, lease, fmt.Errorf("version must be a non-negative integer")
	}
	payload, err := json.Marshal(map[string]any{"title": title, "version": version, "overwrite": false})
	if err != nil {
		return nil, lease, err
	}
	data, err := r.do(ctx, http.MethodPut, "/api/folders/"+uid, bytes.NewReader(payload), "application/json", grafanaMaxResponse)
	if err != nil {
		return nil, lease, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, lease, err
	}
	result["uid"] = uid
	return result, lease, nil
}

func (r *GrafanaRunner) listDashboards(ctx context.Context, lease Lease) (map[string]any, Lease, error) {
	var dashboards []map[string]any
	if err := r.getJSON(ctx, "/api/search?type=dash-db&limit=1000", &dashboards); err != nil {
		return nil, lease, err
	}
	return map[string]any{"dashboards": dashboards}, lease, nil
}

func (r *GrafanaRunner) getDashboard(ctx context.Context, resource domain.TaskResource, lease Lease) (map[string]any, Lease, error) {
	uid, err := grafanaResourceUID(resource, "dashboard")
	if err != nil {
		return nil, lease, err
	}
	var result map[string]any
	err = r.getJSON(ctx, "/api/dashboards/uid/"+uid, &result)
	return result, lease, err
}

func (r *GrafanaRunner) exportDashboard(ctx context.Context, resource domain.TaskResource, lease Lease) (map[string]any, Lease, error) {
	uid, err := grafanaResourceUID(resource, "dashboard")
	if err != nil {
		return nil, lease, err
	}
	// Grafana has no dedicated /export endpoint; the dashboard JSON returned by
	// GET /api/dashboards/uid/{uid} IS the exportable model (wrapped in a meta
	// envelope). Strip meta so callers get the importable dashboard object.
	var result map[string]any
	err = r.getJSON(ctx, "/api/dashboards/uid/"+uid, &result)
	if err != nil {
		return nil, lease, err
	}
	dashboard, _ := result["dashboard"].(map[string]any)
	if dashboard == nil {
		return nil, lease, fmt.Errorf("grafana dashboard %q export returned no dashboard model", uid)
	}
	return map[string]any{"dashboard": dashboard}, lease, nil
}

func (r *GrafanaRunner) createDashboard(ctx context.Context, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	body, err := grafanaDashboardPayload(parameters, false)
	if err != nil {
		return nil, lease, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, lease, err
	}
	data, err := r.do(ctx, http.MethodPost, "/api/dashboards/db", bytes.NewReader(payload), "application/json", grafanaMaxResponse)
	if err != nil {
		return nil, lease, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, lease, err
	}
	return result, lease, nil
}

func (r *GrafanaRunner) updateDashboard(ctx context.Context, resource domain.TaskResource, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	uid, err := grafanaResourceUID(resource, "dashboard")
	if err != nil {
		return nil, lease, err
	}
	if err := rejectGrafanaSensitiveFields(parameters); err != nil {
		return nil, lease, err
	}
	body, err := grafanaDashboardPayload(parameters, true)
	if err != nil {
		return nil, lease, err
	}
	// Pin the dashboard UID to the authorized resource so an update can't
	// hijack another dashboard by embedding a different uid in the body.
	dashboard, _ := body["dashboard"].(map[string]any)
	if dashboard == nil {
		dashboard = map[string]any{}
	}
	if requestedUID, exists := dashboard["uid"]; exists && strings.TrimSpace(fmt.Sprint(requestedUID)) != uid {
		return nil, lease, fmt.Errorf("dashboard uid is fixed to the authorized resource")
	}
	if err := rejectGrafanaSensitiveFields(dashboard); err != nil {
		return nil, lease, err
	}
	dashboard["uid"] = uid
	body["dashboard"] = dashboard
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, lease, err
	}
	data, err := r.do(ctx, http.MethodPost, "/api/dashboards/db", bytes.NewReader(payload), "application/json", grafanaMaxResponse)
	if err != nil {
		return nil, lease, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, lease, err
	}
	return result, lease, nil
}

func (r *GrafanaRunner) deleteDashboard(ctx context.Context, resource domain.TaskResource, lease Lease) (map[string]any, Lease, error) {
	uid, err := grafanaResourceUID(resource, "dashboard")
	if err != nil {
		return nil, lease, err
	}
	_, err = r.do(ctx, http.MethodDelete, "/api/dashboards/uid/"+uid, nil, "", 64<<10)
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"uid": uid, "deleted": true}, lease, nil
}

func (r *GrafanaRunner) listDatasources(ctx context.Context, lease Lease) (map[string]any, Lease, error) {
	var datasources []map[string]any
	if err := r.getJSON(ctx, "/api/datasources", &datasources); err != nil {
		return nil, lease, err
	}
	return map[string]any{"datasources": datasources}, lease, nil
}

func (r *GrafanaRunner) getDatasource(ctx context.Context, resource domain.TaskResource, lease Lease) (map[string]any, Lease, error) {
	uid, err := grafanaResourceUID(resource, "datasource")
	if err != nil {
		return nil, lease, err
	}
	var datasource map[string]any
	if err := r.getJSON(ctx, "/api/datasources/uid/"+uid, &datasource); err != nil {
		return nil, lease, err
	}
	return map[string]any{"datasource": safeGrafanaDatasource(datasource, uid)}, lease, nil
}

func (r *GrafanaRunner) queryDatasource(ctx context.Context, resource domain.TaskResource, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	uid, err := grafanaResourceUID(resource, "datasource")
	if err != nil {
		return nil, lease, err
	}
	queries, ok := parameters["queries"].([]any)
	if !ok || len(queries) == 0 {
		return nil, lease, fmt.Errorf("queries must be a non-empty array")
	}
	if len(queries) > 20 {
		return nil, lease, fmt.Errorf("queries must contain at most 20 entries")
	}
	// Pin every query to the authorized datasource so a caller can't reach
	// another datasource by embedding a different datasource.uid in the body.
	pinned := make([]any, 0, len(queries))
	for _, raw := range queries {
		query, ok := raw.(map[string]any)
		if !ok {
			return nil, lease, fmt.Errorf("each query must be an object")
		}
		query["datasource"] = map[string]any{"uid": uid}
		pinned = append(pinned, query)
	}
	body := map[string]any{"queries": pinned}
	if from, ok := parameters["from"].(string); ok && from != "" {
		body["from"] = from
	}
	if to, ok := parameters["to"].(string); ok && to != "" {
		body["to"] = to
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, lease, err
	}
	data, err := r.do(ctx, http.MethodPost, "/api/ds/query", bytes.NewReader(payload), "application/json", grafanaMaxResponse)
	if err != nil {
		return nil, lease, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, lease, err
	}
	return result, lease, nil
}

func (r *GrafanaRunner) createDatasource(ctx context.Context, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	body, err := grafanaDatasourcePayload(parameters)
	if err != nil {
		return nil, lease, err
	}
	if uid, _ := parameters["datasourceUid"].(string); uid != "" {
		if !grafanaUIDPattern.MatchString(uid) {
			return nil, lease, fmt.Errorf("datasourceUid must match %s", grafanaUIDPattern.String())
		}
		body["uid"] = uid
	}
	return r.writeDatasource(ctx, http.MethodPost, "/api/datasources", body, lease)
}

func (r *GrafanaRunner) updateDatasource(ctx context.Context, resource domain.TaskResource, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	uid, err := grafanaResourceUID(resource, "datasource")
	if err != nil {
		return nil, lease, err
	}
	if _, exists := parameters["uid"]; exists {
		return nil, lease, fmt.Errorf("datasource uid is fixed to the authorized resource")
	}
	if _, exists := parameters["datasourceUid"]; exists {
		return nil, lease, fmt.Errorf("datasource uid is fixed to the authorized resource")
	}
	body, err := grafanaDatasourcePayload(parameters)
	if err != nil {
		return nil, lease, err
	}
	body["uid"] = uid
	return r.writeDatasource(ctx, http.MethodPut, "/api/datasources/uid/"+uid, body, lease)
}

func (r *GrafanaRunner) deleteDatasource(ctx context.Context, resource domain.TaskResource, lease Lease) (map[string]any, Lease, error) {
	uid, err := grafanaResourceUID(resource, "datasource")
	if err != nil {
		return nil, lease, err
	}
	if _, err := r.do(ctx, http.MethodDelete, "/api/datasources/uid/"+uid, nil, "", 64<<10); err != nil {
		return nil, lease, err
	}
	return map[string]any{"uid": uid, "deleted": true}, lease, nil
}

func (r *GrafanaRunner) writeDatasource(ctx context.Context, method, path string, body map[string]any, lease Lease) (map[string]any, Lease, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, lease, err
	}
	data, err := r.do(ctx, method, path, bytes.NewReader(payload), "application/json", grafanaMaxResponse)
	if err != nil {
		return nil, lease, err
	}
	var result map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, lease, err
		}
	}
	return result, lease, nil
}

func grafanaDatasourcePayload(parameters map[string]any) (map[string]any, error) {
	if err := rejectGrafanaSensitiveFields(parameters); err != nil {
		return nil, err
	}
	name, _ := parameters["datasourceName"].(string)
	if strings.TrimSpace(name) == "" || len(name) > 190 {
		return nil, fmt.Errorf("datasourceName must be non-empty and at most 190 characters")
	}
	typeName, _ := parameters["type"].(string)
	if typeName != "prometheus" && typeName != "loki" {
		return nil, fmt.Errorf("datasource type must be prometheus or loki")
	}
	rawURL, _ := parameters["url"].(string)
	endpoint, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, fmt.Errorf("datasource url must be an absolute http(s) URL without credentials or fragment")
	}
	if err := rejectMetadataEndpoint(endpoint); err != nil {
		return nil, err
	}
	access, _ := parameters["access"].(string)
	if access == "" {
		access = "proxy"
	}
	if access != "proxy" && access != "direct" {
		return nil, fmt.Errorf("access must be proxy or direct")
	}
	body := map[string]any{"name": name, "type": typeName, "url": endpoint.String(), "access": access}
	if value, ok := parameters["isDefault"].(bool); ok {
		body["isDefault"] = value
	}
	if jsonData, ok := parameters["jsonData"].(map[string]any); ok {
		encoded, err := json.Marshal(jsonData)
		if err != nil || len(encoded) > 32<<10 {
			return nil, fmt.Errorf("jsonData must be valid and at most 32 KiB")
		}
		body["jsonData"] = jsonData
	}
	return body, nil
}

func safeGrafanaDatasource(datasource map[string]any, uid string) map[string]any {
	safe := map[string]any{"uid": uid}
	for _, key := range []string{"name", "type", "url", "access", "isDefault"} {
		if value, exists := datasource[key]; exists {
			safe[key] = value
		}
	}
	if jsonData, ok := datasource["jsonData"].(map[string]any); ok {
		safe["jsonData"] = filterGrafanaPublicConfig(jsonData)
	}
	return safe
}

func filterGrafanaPublicConfig(value map[string]any) map[string]any {
	filtered := make(map[string]any, len(value))
	for key, item := range value {
		if grafanaSensitiveKey(key) {
			continue
		}
		filtered[key] = filterGrafanaPublicValue(item)
	}
	return filtered
}

func filterGrafanaPublicValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return filterGrafanaPublicConfig(typed)
	case []any:
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = filterGrafanaPublicValue(item)
		}
		return items
	default:
		return value
	}
}

func rejectGrafanaSensitiveFields(value map[string]any) error {
	for key, item := range value {
		if grafanaSensitiveKey(key) {
			return fmt.Errorf("Grafana secret field %q is not allowed in ordinary actions", key)
		}
		if err := rejectGrafanaSensitiveValue(item); err != nil {
			return err
		}
	}
	return nil
}

func rejectGrafanaSensitiveValue(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		return rejectGrafanaSensitiveFields(typed)
	case []any:
		for _, item := range typed {
			if err := rejectGrafanaSensitiveValue(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func grafanaSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "_", ""), "-", ""))
	return normalized == "securejsondata" || normalized == "securejsonfields" || normalized == "password" ||
		normalized == "basicauthpassword" || normalized == "token" || normalized == "apikey" || normalized == "authorization"
}

func (r *GrafanaRunner) validateResource(resource domain.TaskResource) error {
	switch resource.Type {
	case "grafana_instance":
		if resource.ExternalID != "instance/"+r.baseURL.Host {
			return fmt.Errorf("Grafana instance identity does not match its authorized scope")
		}
	case "grafana_folder":
		if _, err := grafanaResourceUID(resource, "folder"); err != nil {
			return err
		}
	case "grafana_dashboard":
		uid, err := grafanaResourceUID(resource, "dashboard")
		if err != nil {
			return err
		}
		if !r.dashboardAllowed(uid) {
			return fmt.Errorf("Grafana dashboard is outside connector scope")
		}
	case "grafana_datasource":
		if _, err := grafanaResourceUID(resource, "datasource"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported Grafana resource type %q", resource.Type)
	}

	return nil
}

func validateGrafanaActionResource(action, resourceType string) error {
	expected := map[string]string{
		"grafana.instance.get":      "grafana_instance",
		"grafana.folder.list":       "grafana_instance",
		"grafana.folder.get":        "grafana_folder",
		"grafana.folder.create":     "grafana_instance",
		"grafana.folder.update":     "grafana_folder",
		"grafana.folder.delete":     "grafana_folder",
		"grafana.dashboard.list":    "grafana_instance",
		"grafana.dashboard.get":     "grafana_dashboard",
		"grafana.dashboard.export":  "grafana_dashboard",
		"grafana.dashboard.create":  "grafana_instance",
		"grafana.dashboard.update":  "grafana_dashboard",
		"grafana.dashboard.delete":  "grafana_dashboard",
		"grafana.datasource.list":   "grafana_instance",
		"grafana.datasource.query":  "grafana_datasource",
		"grafana.datasource.get":    "grafana_datasource",
		"grafana.datasource.create": "grafana_instance",
		"grafana.datasource.update": "grafana_datasource",
		"grafana.datasource.delete": "grafana_datasource",
	}
	if expectedType, ok := expected[action]; !ok || expectedType != resourceType {
		return fmt.Errorf("Grafana action %q is not allowed for resource type %q", action, resourceType)
	}

	return nil
}

// dashboardAllowed reports whether the given dashboard UID is in scope. An empty
// allowedDashboards list means "allow all" - the default until the admin narrows
// the scope from the integration detail page.
func (r *GrafanaRunner) dashboardAllowed(uid string) bool {
	if len(r.allowedDashboards) == 0 {
		return r.allowAll
	}
	for _, scoped := range r.allowedDashboards {
		if scoped == uid {
			return true
		}
	}
	return false
}

func (r *GrafanaRunner) getJSON(ctx context.Context, path string, output any) error {
	data, err := r.do(ctx, http.MethodGet, path, nil, "", grafanaMaxResponse)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, output)
}

// do issues an authenticated request to Grafana. The API token is sent as a
// Bearer header (no CSRF crumb - Grafana's REST API is token-authenticated).
// Error bodies are scrubbed of the token before being surfaced.
func (r *GrafanaRunner) do(ctx context.Context, method, path string, body io.Reader, contentType string, limit int64) ([]byte, error) {
	endpoint := *r.baseURL
	endpoint.Path = strings.TrimRight(r.baseURL.Path, "/") + strings.Split(path, "?")[0]
	if index := strings.IndexByte(path, '?'); index >= 0 {
		endpoint.RawQuery = path[index+1:]
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.apiToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if readErr != nil {
		return nil, readErr
	}
	if int64(len(data)) > limit {
		data = data[:limit]
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if len(data) > 4096 {
			data = data[:4096]
		}
		message := strings.ReplaceAll(strings.TrimSpace(string(data)), r.apiToken, "[REDACTED]")
		return nil, fmt.Errorf("Grafana returned %d: %s", resp.StatusCode, message)
	}

	return data, nil
}

func (r *GrafanaRunner) lease(expiresAt time.Time) Lease {
	fingerprint := sha256.Sum256([]byte(r.baseURL.Host))

	return Lease{Provider: "GRAFANA_CREDENTIAL_REFERENCE", Fingerprint: hex.EncodeToString(fingerprint[:8]), ExpiresAt: expiresAt}
}

// grafanaResourceUID extracts and validates the UID from a Grafana resource's
// ExternalID (e.g. "dashboard/abc-123" -> "abc-123"). The UID pattern is
// conservative so it is always safe to interpolate into an API path.
func grafanaResourceUID(resource domain.TaskResource, kind string) (string, error) {
	prefix := kind + "/"
	if !strings.HasPrefix(resource.ExternalID, prefix) {
		return "", fmt.Errorf("Grafana %s identity does not match its authorized scope", kind)
	}
	uid := strings.TrimPrefix(resource.ExternalID, prefix)
	if uid == "" || strings.ContainsAny(uid, "/?#") || !grafanaUIDPattern.MatchString(uid) {
		return "", fmt.Errorf("invalid Grafana %s UID", kind)
	}
	return uid, nil
}

// grafanaDashboardPayload builds the POST /api/dashboards/db body from action
// parameters. The dashboard object is required; folderUid and message are
// optional. allowOverwrite permits the "overwrite" flag (update action only).
func grafanaDashboardPayload(parameters map[string]any, allowOverwrite bool) (map[string]any, error) {
	dashboard, ok := parameters["dashboard"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("dashboard must be an object")
	}
	if err := rejectGrafanaSensitiveFields(dashboard); err != nil {
		return nil, err
	}
	body := map[string]any{"dashboard": dashboard}
	if folderUID, ok := parameters["folderUid"].(string); ok && folderUID != "" {
		if !grafanaUIDPattern.MatchString(folderUID) {
			return nil, fmt.Errorf("folderUid must match %s", grafanaUIDPattern.String())
		}
		body["folderUid"] = folderUID
	}
	if message, ok := parameters["message"].(string); ok && message != "" {
		if len(message) > 500 {
			return nil, fmt.Errorf("message exceeds 500 characters")
		}
		body["message"] = message
	}
	if allowOverwrite {
		if overwrite, ok := parameters["overwrite"].(bool); ok {
			body["overwrite"] = overwrite
		}
	}
	return body, nil
}

// IsGrafanaUnknownOutcome reports whether a Grafana change action ended with a
// context deadline/cancellation, meaning the mutation may have partially
// succeeded. The runtime surfaces these as UNKNOWN outcomes so the caller can
// reconcile by re-reading state.
func IsGrafanaUnknownOutcome(action string, err error) bool {
	if err == nil || (!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled)) {
		return false
	}
	switch action {
	case "grafana.folder.create", "grafana.folder.update", "grafana.folder.delete", "grafana.dashboard.create",
		"grafana.dashboard.update", "grafana.dashboard.delete", "grafana.datasource.create",
		"grafana.datasource.update", "grafana.datasource.delete":
		return true
	default:
		return false
	}
}
