package connector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

const (
	jenkinsMaxResponse = 4 << 20
	jenkinsMaxLog      = 1 << 20
)

var JenkinsResourceTypes = []string{
	"jenkins_controller", "jenkins_job", "jenkins_build", "jenkins_queue_item", "jenkins_node", "jenkins_credential_reference",
}

var JenkinsCapabilities = []string{
	"jenkins.controller.get@v1", "jenkins.job.get@v1", "jenkins.job.configure@v1", "jenkins.job.enable@v1",
	"jenkins.job.disable@v1", "jenkins.job.build@v1", "jenkins.build.get@v1", "jenkins.build.logs@v1",
	"jenkins.build.stop@v1", "jenkins.build.retry@v1", "jenkins.queue.get@v1", "jenkins.queue.cancel@v1",
	"jenkins.node.get@v1", "jenkins.node.set_offline@v1", "jenkins.credential_reference.get@v1",
	"jenkins.job.config.get@v1", "jenkins.job.create@v1", "jenkins.job.update@v1", "jenkins.job.delete@v1",
}

type JenkinsConfig struct {
	BaseURL       string
	Username      string
	APIToken      string
	AllowedJobs   []string
	AllowAll      bool
	IntegrationID string
	HTTPClient    *http.Client
}

// jenkinsHTTPError wraps a non-2xx/3xx Jenkins response so callers can map
// Jenkins transport responses to stable, sanitized action error codes.
type jenkinsHTTPError struct {
	StatusCode int
	Body       string
}

func (e *jenkinsHTTPError) Error() string {
	return fmt.Sprintf("Jenkins returned %d: %s", e.StatusCode, domain.SanitizeErrorMessage(e.Body))
}

type JenkinsRunner struct {
	baseURL       *url.URL
	username      string
	apiToken      string
	allowedJobs   []string
	allowAll      bool
	integrationID string
	http          *http.Client
	cookieMu      sync.RWMutex
	sessionCookie string
}

func NewJenkinsRunner(config JenkinsConfig) (*JenkinsRunner, error) {
	endpoint, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, fmt.Errorf("WARDEN_JENKINS_URL must be an absolute http(s) URL")
	}
	if endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.User != nil {
		return nil, fmt.Errorf("WARDEN_JENKINS_URL must not contain credentials, query, or fragment")
	}
	if err := rejectMetadataEndpoint(endpoint); err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.Username) == "" || config.APIToken == "" {
		return nil, fmt.Errorf("Jenkins username and API token are required")
	}
	// Empty AllowedJobs means "allow all jobs". The scope is narrowed later
	// from the control plane via the integration's Job picker.
	allowed := make([]string, 0, len(config.AllowedJobs))
	for _, item := range config.AllowedJobs {
		item = strings.Trim(strings.TrimSpace(item), "/")
		if item == "" || item == "*" || strings.Contains(item, "..") {
			return nil, fmt.Errorf("wildcard, empty, or relative Jenkins job scope is not allowed")
		}
		allowed = append(allowed, item)
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	// Jenkins' CSRF crumb is bound to the HTTP session that issued it. Clone
	// the configured client so each runner owns its session state, then attach
	// a cookie jar when the caller did not provide one. Without this, Jenkins
	// may reject a mutation even when the crumb header itself is correct.
	clientCopy := *client
	if clientCopy.Jar == nil {
		jar, jarErr := cookiejar.New(nil)
		if jarErr != nil {
			return nil, fmt.Errorf("initialize Jenkins cookie jar: %w", jarErr)
		}
		clientCopy.Jar = jar
	}
	client = &clientCopy
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")

	return &JenkinsRunner{baseURL: endpoint, username: config.Username, apiToken: config.APIToken, allowedJobs: allowed, allowAll: config.AllowAll, integrationID: config.IntegrationID, http: client}, nil
}

func NewJenkinsRunnerFromEnvironment() (*JenkinsRunner, error) {
	return NewJenkinsRunner(JenkinsConfig{
		BaseURL: os.Getenv("WARDEN_JENKINS_URL"), Username: os.Getenv("WARDEN_JENKINS_USERNAME"),
		APIToken: os.Getenv("WARDEN_JENKINS_API_TOKEN"), AllowedJobs: splitCommaSeparated(os.Getenv("WARDEN_JENKINS_ALLOWED_JOBS")),
	})
}

func (r *JenkinsRunner) ListResources(ctx context.Context) []DiscoveredResource {
	resources, _ := r.listResources(ctx)
	return resources
}

func (r *JenkinsRunner) ListResourcesComplete(ctx context.Context) ([]DiscoveredResource, bool) {
	return r.listResources(ctx)
}

func (r *JenkinsRunner) listResources(ctx context.Context) ([]DiscoveredResource, bool) {
	controllerName := r.baseURL.Host + r.baseURL.Path
	resources := []DiscoveredResource{{
		ExternalID: controllerName, Name: controllerName, ResourceType: "jenkins_controller",
		Labels: map[string]any{}, Annotations: map[string]any{}, Metadata: map[string]any{"endpoint": r.baseURL.Scheme + "://" + controllerName},
	}}
	jobs, jobsComplete := r.discoverJobsComplete(ctx)
	queue, queueComplete := r.discoverQueueComplete(ctx)
	nodes, nodesComplete := r.discoverNodesComplete(ctx)
	credentials, credentialsComplete := r.discoverCredentialReferencesComplete(ctx)
	resources = append(resources, r.enrichJobPipelineMetadata(ctx, jobs...)...)
	resources = append(resources, queue...)
	resources = append(resources, nodes...)
	resources = append(resources, credentials...)

	// Stamp every discovered resource with the owning integration id so the
	// control plane can route execution back to this runner.
	if r.integrationID != "" {
		for i := range resources {
			resources[i].IntegrationID = r.integrationID
		}
	}

	return resources, jobsComplete && queueComplete && nodesComplete && credentialsComplete
}

// SnapshotResource performs a targeted read for change-safety verification.
// It intentionally returns only the same metadata exposed by discovery; Job
// configuration and credential material are never included in snapshots.
func (r *JenkinsRunner) SnapshotResource(ctx context.Context, resource domain.TaskResource) (DiscoveredResource, bool) {
	if err := r.validateResource(resource); err != nil {
		return DiscoveredResource{}, false
	}
	switch resource.Type {
	case "jenkins_controller":
		return DiscoveredResource{ExternalID: resource.ExternalID, Name: resource.Name, ResourceType: resource.Type,
			IntegrationID: r.integrationID, Labels: map[string]any{}, Annotations: map[string]any{},
			Metadata: map[string]any{"endpoint": r.baseURL.Scheme + "://" + r.baseURL.Host + r.baseURL.Path}}, true
	case "jenkins_job":
		var job jenkinsJob
		err := r.getJSON(ctx, jobPath(resource.Name)+"/api/json?tree=name,color,buildable,inQueue", &job)
		if err != nil {
			var httpErr *jenkinsHTTPError
			if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
				return DiscoveredResource{}, false
			}
			return DiscoveredResource{}, false
		}
		job.Name = resource.Name
		discovered := jobResource(resource.Name, job)
		r.enrichSingleJobPipelineMetadata(ctx, &discovered)
		discovered.IntegrationID = r.integrationID
		return discovered, true
	default:
		// Non-Job mutations retain the existing discovery fallback semantics.
		return findDiscoveredResource(r.ListResources(ctx), resource)
	}
}

func (r *JenkinsRunner) Execute(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	if err := r.validateResource(task.Resource); err != nil {
		return nil, Lease{}, err
	}
	if err := validateJenkinsActionResource(task.Action.Name, task.Resource.Type); err != nil {
		return nil, Lease{}, err
	}
	if err := validateJenkinsTaskIdentity(task); err != nil {
		return nil, Lease{}, err
	}
	lease := r.lease(task.Grant.ExpiresAt)

	switch task.Action.Name {
	case "jenkins.controller.get":
		var result map[string]any
		err := r.getJSON(ctx, "/api/json?tree=nodeName,mode,quietingDown,useCrumbs,version", &result)
		return result, lease, err
	case "jenkins.job.get":
		return r.getJob(ctx, task.Resource.Name, lease)
	case "jenkins.job.config.get":
		return r.getJobConfig(ctx, task.Resource.Name, lease)
	case "jenkins.job.configure":
		return r.configureJob(ctx, task.Resource.Name, task.Parameters, lease)
	case "jenkins.job.enable":
		return r.postJobAction(ctx, task.Resource.Name, "enable", lease)
	case "jenkins.job.disable":
		return r.postJobAction(ctx, task.Resource.Name, "disable", lease)
	case "jenkins.job.build":
		return r.triggerBuild(ctx, task.Resource.Name, task.Parameters, lease)
	case "jenkins.job.create":
		return r.createJob(ctx, task.Parameters, lease)
	case "jenkins.job.update":
		return r.updateJob(ctx, task.Resource.Name, task.Parameters, lease)
	case "jenkins.job.delete":
		return r.deleteJob(ctx, task.Resource.Name, task.Parameters, lease)
	case "jenkins.build.get":
		return r.getBuild(ctx, task.Resource, lease)
	case "jenkins.build.logs":
		return r.getBuildLogs(ctx, task.Resource, lease)
	case "jenkins.build.stop":
		return r.postBuildAction(ctx, task.Resource, "stop", lease)
	case "jenkins.build.retry":
		return r.retryBuild(ctx, task.Resource, task.Parameters, lease)
	case "jenkins.queue.get":
		return r.getQueueItem(ctx, task.Resource, lease)
	case "jenkins.queue.cancel":
		return r.cancelQueueItem(ctx, task.Resource, lease)
	case "jenkins.node.get":
		return r.getNode(ctx, task.Resource.Name, lease)
	case "jenkins.node.set_offline":
		return r.setNodeOffline(ctx, task.Resource.Name, task.Parameters, lease)
	case "jenkins.credential_reference.get":
		return r.getCredentialReference(ctx, task.Resource.Name, lease)
	default:
		return nil, lease, fmt.Errorf("unsupported Jenkins action %q", task.Action.Name)
	}
}

func validateJenkinsTaskIdentity(task domain.TaskEnvelope) error {
	if task.Resource.Type != "jenkins_job" || !strings.HasPrefix(task.Action.Name, "jenkins.job.") || task.Action.Name == "jenkins.job.create" {
		return nil
	}
	for _, key := range []string{"name", "job", "jobName", "fullName"} {
		value, exists := task.Parameters[key]
		if !exists {
			continue
		}
		name, ok := value.(string)
		if !ok || strings.TrimSpace(name) != task.Resource.Name {
			return fmt.Errorf("Jenkins Job target does not match the authorized resource")
		}
	}
	return nil
}

func (r *JenkinsRunner) discoverJobs(ctx context.Context) []DiscoveredResource {
	resources, _ := r.discoverJobsComplete(ctx)
	return resources
}

func (r *JenkinsRunner) discoverJobsComplete(ctx context.Context) ([]DiscoveredResource, bool) {
	var root struct {
		Jobs []jenkinsJob `json:"jobs"`
	}
	if err := r.getJSON(ctx, "/api/json?tree=jobs[name,_class,color,buildable,inQueue,lastBuild[number,result,timestamp],builds[number,result,timestamp]{0,20}]", &root); err != nil {
		return nil, false
	}
	var resources []DiscoveredResource
	complete := true
	for _, job := range root.Jobs {
		discovered, jobComplete := r.discoverJobComplete(ctx, job.Name, job, 0)
		resources = append(resources, discovered...)
		complete = complete && jobComplete
	}

	return resources, complete
}

func (r *JenkinsRunner) discoverJob(ctx context.Context, fullName string, job jenkinsJob, depth int) []DiscoveredResource {
	resources, _ := r.discoverJobComplete(ctx, fullName, job, depth)
	return resources
}

func (r *JenkinsRunner) discoverJobComplete(ctx context.Context, fullName string, job jenkinsJob, depth int) ([]DiscoveredResource, bool) {
	if depth > 10 {
		return nil, false
	}
	// Skip Jenkins' lost+found folder — it holds abandoned/orphaned jobs
	// that only clutter the resource catalog.
	if strings.Contains(fullName, "lost+found") {
		return nil, true
	}
	var resources []DiscoveredResource
	if r.jobAllowed(fullName) {
		resources = append(resources, jobResource(fullName, job))
		for _, build := range job.Builds {
			resources = append(resources, buildResource(fullName, build))
		}
		if len(job.Builds) == 0 && job.LastBuild != nil {
			resources = append(resources, buildResource(fullName, *job.LastBuild))
		}
	}
	if !strings.Contains(strings.ToLower(job.Class), "folder") {
		return resources, true
	}
	var folder struct {
		Jobs []jenkinsJob `json:"jobs"`
	}
	if err := r.getJSON(ctx, jobPath(fullName)+"/api/json?tree=jobs[name,_class,color,buildable,inQueue,lastBuild[number,result,timestamp],builds[number,result,timestamp]{0,20}]", &folder); err != nil {
		return resources, false
	}
	complete := true
	for _, child := range folder.Jobs {
		discovered, childComplete := r.discoverJobComplete(ctx, fullName+"/"+child.Name, child, depth+1)
		resources = append(resources, discovered...)
		complete = complete && childComplete
	}

	return resources, complete
}

func (r *JenkinsRunner) discoverQueue(ctx context.Context) []DiscoveredResource {
	resources, _ := r.discoverQueueComplete(ctx)
	return resources
}

func (r *JenkinsRunner) discoverQueueComplete(ctx context.Context) ([]DiscoveredResource, bool) {
	var response struct {
		Items []struct {
			ID           int64  `json:"id"`
			Why          string `json:"why"`
			InQueueSince int64  `json:"inQueueSince"`
			Task         struct {
				Name     string `json:"name"`
				FullName string `json:"fullName"`
			} `json:"task"`
		} `json:"items"`
	}
	if err := r.getJSON(ctx, "/queue/api/json?tree=items[id,why,inQueueSince,task[name,fullName]]", &response); err != nil {
		return nil, false
	}
	var resources []DiscoveredResource
	for _, item := range response.Items {
		job := item.Task.FullName
		if job == "" {
			job = item.Task.Name
		}
		if !r.jobAllowed(job) {
			continue
		}
		id := strconv.FormatInt(item.ID, 10)
		resources = append(resources, DiscoveredResource{ExternalID: "queue/" + id, Name: id, Namespace: job,
			ResourceType: "jenkins_queue_item", Labels: map[string]any{}, Annotations: map[string]any{},
			Metadata: map[string]any{"job": job, "why": item.Why, "inQueueSince": item.InQueueSince}})
	}

	return resources, true
}

func (r *JenkinsRunner) discoverNodes(ctx context.Context) []DiscoveredResource {
	resources, _ := r.discoverNodesComplete(ctx)
	return resources
}

func (r *JenkinsRunner) discoverNodesComplete(ctx context.Context) ([]DiscoveredResource, bool) {
	var response struct {
		Computer []struct {
			DisplayName        string `json:"displayName"`
			Offline            bool   `json:"offline"`
			TemporarilyOffline bool   `json:"temporarilyOffline"`
			Idle               bool   `json:"idle"`
			NumExecutors       int    `json:"numExecutors"`
		} `json:"computer"`
	}
	if err := r.getJSON(ctx, "/computer/api/json?tree=computer[displayName,offline,temporarilyOffline,idle,numExecutors]", &response); err != nil {
		return nil, false
	}
	resources := make([]DiscoveredResource, 0, len(response.Computer))
	for _, node := range response.Computer {
		resources = append(resources, DiscoveredResource{ExternalID: "node/" + node.DisplayName, Name: node.DisplayName,
			ResourceType: "jenkins_node", Labels: map[string]any{"offline": node.Offline}, Annotations: map[string]any{},
			Metadata: map[string]any{"temporarilyOffline": node.TemporarilyOffline, "idle": node.Idle, "numExecutors": node.NumExecutors}})
	}

	return resources, true
}

func (r *JenkinsRunner) discoverCredentialReferences(ctx context.Context) []DiscoveredResource {
	resources, _ := r.discoverCredentialReferencesComplete(ctx)
	return resources
}

func (r *JenkinsRunner) discoverCredentialReferencesComplete(ctx context.Context) ([]DiscoveredResource, bool) {
	var response struct {
		Credentials []jenkinsCredential `json:"credentials"`
	}
	if err := r.getJSON(ctx, "/credentials/store/system/domain/_/api/json?tree=credentials[id,displayName,description,typeName]", &response); err != nil {
		return nil, false
	}
	resources := make([]DiscoveredResource, 0, len(response.Credentials))
	for _, credential := range response.Credentials {
		resources = append(resources, credentialResource(credential))
	}

	return resources, true
}

func (r *JenkinsRunner) getJob(ctx context.Context, name string, lease Lease) (map[string]any, Lease, error) {
	var job map[string]any
	err := r.getJSON(ctx, jobPath(name)+"/api/json?tree=name,fullName,displayName,description,color,buildable,inQueue,disabled,lastBuild[number,result,timestamp,duration,building]", &job)

	return job, lease, err
}

func (r *JenkinsRunner) getJobConfig(ctx context.Context, name string, lease Lease) (map[string]any, Lease, error) {
	data, err := r.do(ctx, http.MethodGet, jobPath(name)+"/config.xml", nil, "", jenkinsMaxResponse)
	if err != nil {
		return nil, lease, err
	}

	return map[string]any{"config": string(data)}, lease, nil
}

var jenkinsJobNameSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.-]{0,254}$`)

// jenkinsCreateItemPath splits a full job name (e.g. "folder/sub/job") into
// the parent path for Jenkins' createItem endpoint and the leaf job name.
// Root-level jobs use "/createItem"; nested jobs use "/job/folder/createItem".
func jenkinsCreateItemPath(name string) (parentPath, jobName string) {
	parts := strings.Split(strings.Trim(name, "/"), "/")
	jobName = parts[len(parts)-1]
	if len(parts) == 1 {
		return "/createItem", jobName
	}
	var path strings.Builder
	for _, part := range parts[:len(parts)-1] {
		path.WriteString("/job/")
		path.WriteString(part)
	}
	path.WriteString("/createItem")
	return path.String(), jobName
}

func (r *JenkinsRunner) createJob(ctx context.Context, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	name, _ := parameters["name"].(string)
	name = strings.Trim(strings.TrimSpace(name), "/")
	if name == "" {
		return nil, lease, fmt.Errorf("name is required")
	}
	for _, segment := range strings.Split(name, "/") {
		if !jenkinsJobNameSegmentPattern.MatchString(segment) {
			return nil, lease, fmt.Errorf("job name segment %q must start with an alphanumeric character and contain only letters, digits, spaces, underscores, hyphens, or dots", segment)
		}
	}
	if !r.jobAllowed(name) {
		return nil, lease, fmt.Errorf("Jenkins job %q is outside connector scope", name)
	}

	mode, _ := parameters["mode"].(string)
	description, _ := parameters["description"].(string)
	if len(description) > 4000 {
		return nil, lease, fmt.Errorf("description exceeds 4000 characters")
	}
	quietPeriod, hasQuietPeriod, err := jenkinsQuietPeriod(parameters)
	if err != nil {
		return nil, lease, err
	}

	var config []byte
	switch mode {
	case "inline":
		pipelineScript, _ := parameters["pipelineScript"].(string)
		if strings.TrimSpace(pipelineScript) == "" {
			return nil, lease, fmt.Errorf("pipelineScript is required for inline mode")
		}
		if len(pipelineScript) > 65536 {
			return nil, lease, fmt.Errorf("pipelineScript exceeds 65536 characters")
		}
		config, err = buildInlinePipelineConfig(pipelineScript, description, quietPeriod, hasQuietPeriod)
		if err != nil {
			return nil, lease, err
		}
	case "scm":
		scm, ok := parameters["scm"].(map[string]any)
		if !ok {
			return nil, lease, fmt.Errorf("scm is required for scm mode")
		}
		config, err = r.buildScmPipelineConfig(ctx, scm, description, quietPeriod, hasQuietPeriod)
		if err != nil {
			return nil, lease, err
		}
	default:
		return nil, lease, fmt.Errorf("mode must be \"inline\" or \"scm\"")
	}

	parentPath, jobName := jenkinsCreateItemPath(name)
	createPath := parentPath + "?name=" + url.QueryEscape(jobName)
	_, err = r.do(ctx, http.MethodPost, createPath, bytes.NewReader(config), "application/xml", 64<<10)
	if err != nil {
		var httpErr *jenkinsHTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusBadRequest {
			// Jenkins installations render createItem conflicts differently. Some
			// themes return an HTML error page without the "already exists" text,
			// so confirm the target now exists instead of relying only on the body.
			var existing struct {
				FullName string `json:"fullName"`
			}
			if strings.Contains(strings.ToLower(httpErr.Body), "already exists") ||
				r.getJSON(ctx, jobPath(name)+"/api/json?tree=fullName", &existing) == nil {
				return nil, lease, fmt.Errorf("JENKINS_JOB_CONFLICT: Jenkins Job %q already exists", name)
			}
		}
		return nil, lease, err
	}

	return map[string]any{"job": name, "created": true}, lease, nil
}

func buildInlinePipelineConfig(pipelineScript, description string, quietPeriod int, hasQuietPeriod bool) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("<?xml version='1.0' encoding='UTF-8'?>\n")
	buf.WriteString("<flow-definition plugin=\"workflow-job\">\n")
	if description != "" {
		buf.WriteString("  <description>")
		xml.EscapeText(&buf, []byte(description))
		buf.WriteString("</description>\n")
	}
	writeJenkinsQuietPeriod(&buf, quietPeriod, hasQuietPeriod)
	buf.WriteString("  <definition class=\"org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition\" plugin=\"workflow-cps\">\n")
	buf.WriteString("    <script>")
	xml.EscapeText(&buf, []byte(pipelineScript))
	buf.WriteString("</script>\n")
	buf.WriteString("    <sandbox>true</sandbox>\n")
	buf.WriteString("  </definition>\n")
	buf.WriteString("  <disabled>false</disabled>\n")
	buf.WriteString("</flow-definition>\n")
	return buf.Bytes(), nil
}

func (r *JenkinsRunner) buildScmPipelineConfig(ctx context.Context, scm map[string]any, description string, quietPeriod int, hasQuietPeriod bool) ([]byte, error) {
	repoURL, _ := scm["repoUrl"].(string)
	if strings.TrimSpace(repoURL) == "" {
		return nil, fmt.Errorf("scm.repoUrl is required")
	}
	if len(repoURL) > 2048 {
		return nil, fmt.Errorf("scm.repoUrl exceeds 2048 characters")
	}
	credentialsID, _ := scm["credentialsId"].(string)
	if len(credentialsID) > 128 {
		return nil, fmt.Errorf("scm.credentialsId exceeds 128 characters")
	}
	if strings.TrimSpace(credentialsID) != "" {
		found, err := r.credentialReferenceExists(ctx, credentialsID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("JENKINS_CREDENTIAL_REFERENCE_NOT_FOUND: Jenkins credential reference %q was not found", credentialsID)
		}
	}
	branch, _ := scm["branch"].(string)
	if branch == "" {
		branch = "*/main"
	}
	if len(branch) > 256 {
		return nil, fmt.Errorf("scm.branch exceeds 256 characters")
	}
	scriptPath, _ := scm["scriptPath"].(string)
	if scriptPath == "" {
		scriptPath = "Jenkinsfile"
	}
	if len(scriptPath) > 256 {
		return nil, fmt.Errorf("scm.scriptPath exceeds 256 characters")
	}

	var buf bytes.Buffer
	buf.WriteString("<?xml version='1.0' encoding='UTF-8'?>\n")
	buf.WriteString("<flow-definition plugin=\"workflow-job\">\n")
	if description != "" {
		buf.WriteString("  <description>")
		xml.EscapeText(&buf, []byte(description))
		buf.WriteString("</description>\n")
	}
	writeJenkinsQuietPeriod(&buf, quietPeriod, hasQuietPeriod)
	buf.WriteString("  <definition class=\"org.jenkinsci.plugins.workflow.cps.CpsScmFlowDefinition\" plugin=\"workflow-cps\">\n")
	buf.WriteString("    <scm class=\"hudson.plugins.git.GitSCM\" plugin=\"git\">\n")
	buf.WriteString("      <userRemoteConfigs>\n")
	buf.WriteString("        <hudson.plugins.git.UserRemoteConfig>\n")
	buf.WriteString("          <url>")
	xml.EscapeText(&buf, []byte(repoURL))
	buf.WriteString("</url>\n")
	if credentialsID != "" {
		buf.WriteString("          <credentialsId>")
		xml.EscapeText(&buf, []byte(credentialsID))
		buf.WriteString("</credentialsId>\n")
	}
	buf.WriteString("        </hudson.plugins.git.UserRemoteConfig>\n")
	buf.WriteString("      </userRemoteConfigs>\n")
	buf.WriteString("      <branches>\n")
	buf.WriteString("        <hudson.plugins.git.BranchSpec>\n")
	buf.WriteString("          <name>")
	xml.EscapeText(&buf, []byte(branch))
	buf.WriteString("</name>\n")
	buf.WriteString("        </hudson.plugins.git.BranchSpec>\n")
	buf.WriteString("      </branches>\n")
	buf.WriteString("    </scm>\n")
	buf.WriteString("    <scriptPath>")
	xml.EscapeText(&buf, []byte(scriptPath))
	buf.WriteString("</scriptPath>\n")
	buf.WriteString("  </definition>\n")
	buf.WriteString("  <disabled>false</disabled>\n")
	buf.WriteString("</flow-definition>\n")
	return buf.Bytes(), nil
}

func writeJenkinsQuietPeriod(buf *bytes.Buffer, quietPeriod int, provided bool) {
	if provided {
		fmt.Fprintf(buf, "  <quietPeriod>%d</quietPeriod>\n", quietPeriod)
	}
}

func jenkinsQuietPeriod(parameters map[string]any) (int, bool, error) {
	value, provided := parameters["quietPeriod"]
	quietPeriod, valid := optionalInt(value, 0, 3600)
	if provided && !valid {
		return 0, false, fmt.Errorf("quietPeriod must be an integer between 0 and 3600")
	}
	return quietPeriod, provided, nil
}

func (r *JenkinsRunner) updateJob(ctx context.Context, name string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	mode, _ := parameters["mode"].(string)
	description, _ := parameters["description"].(string)
	if len(description) > 4000 {
		return nil, lease, fmt.Errorf("description exceeds 4000 characters")
	}
	quietPeriod, hasQuietPeriod, err := jenkinsQuietPeriod(parameters)
	if err != nil {
		return nil, lease, err
	}

	var config []byte
	switch mode {
	case "inline":
		pipelineScript, _ := parameters["pipelineScript"].(string)
		if strings.TrimSpace(pipelineScript) == "" {
			return nil, lease, fmt.Errorf("pipelineScript is required for inline mode")
		}
		if len(pipelineScript) > 65536 {
			return nil, lease, fmt.Errorf("pipelineScript exceeds 65536 characters")
		}
		config, err = buildInlinePipelineConfig(pipelineScript, description, quietPeriod, hasQuietPeriod)
	case "scm":
		scm, ok := parameters["scm"].(map[string]any)
		if !ok {
			return nil, lease, fmt.Errorf("scm is required for scm mode")
		}
		config, err = r.buildScmPipelineConfig(ctx, scm, description, quietPeriod, hasQuietPeriod)
	default:
		return nil, lease, fmt.Errorf("mode must be \"inline\" or \"scm\"")
	}
	if err != nil {
		return nil, lease, err
	}
	if _, err = r.do(ctx, http.MethodPost, jobPath(name)+"/config.xml", bytes.NewReader(config), "application/xml", 64<<10); err != nil {
		var httpErr *jenkinsHTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return nil, lease, fmt.Errorf("JENKINS_JOB_NOT_FOUND: Jenkins Job %q does not exist", name)
		}
		return nil, lease, err
	}
	return map[string]any{"job": name, "updated": true, "mode": mode}, lease, nil
}

func (r *JenkinsRunner) deleteJob(ctx context.Context, name string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	confirmation, _ := parameters["confirmJobName"].(string)
	if confirmation != name {
		return nil, lease, fmt.Errorf("JENKINS_JOB_CONFIRMATION_MISMATCH: confirmJobName must exactly match %q", name)
	}
	if _, err := r.do(ctx, http.MethodPost, jobPath(name)+"/doDelete", nil, "", 64<<10); err != nil {
		var httpErr *jenkinsHTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return nil, lease, fmt.Errorf("JENKINS_JOB_NOT_FOUND: Jenkins Job %q does not exist", name)
		}
		return nil, lease, err
	}
	return map[string]any{"job": name, "deleted": true}, lease, nil
}

func (r *JenkinsRunner) configureJob(ctx context.Context, name string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	descriptionValue, hasDescription := parameters["description"]
	description, descriptionOK := descriptionValue.(string)
	if hasDescription && !descriptionOK {
		return nil, lease, fmt.Errorf("description must be a string")
	}
	quietPeriodValue, quietPeriodProvided := parameters["quietPeriod"]
	quietPeriod, hasQuietPeriod := optionalInt(quietPeriodValue, 0, 3600)
	if quietPeriodProvided && !hasQuietPeriod {
		return nil, lease, fmt.Errorf("quietPeriod must be an integer between 0 and 3600")
	}
	if !hasDescription && !hasQuietPeriod {
		return nil, lease, fmt.Errorf("description or quietPeriod is required")
	}
	if len(description) > 4000 {
		return nil, lease, fmt.Errorf("description exceeds 4000 characters")
	}
	config, err := r.do(ctx, http.MethodGet, jobPath(name)+"/config.xml", nil, "", jenkinsMaxResponse)
	if err != nil {
		return nil, lease, err
	}
	updated, err := updateJobConfig(config, description, hasDescription, quietPeriod, hasQuietPeriod)
	if err != nil {
		return nil, lease, err
	}
	if _, err := r.do(ctx, http.MethodPost, jobPath(name)+"/config.xml", bytes.NewReader(updated), "application/xml", 64<<10); err != nil {
		return nil, lease, err
	}

	return map[string]any{"job": name, "descriptionUpdated": hasDescription, "quietPeriodUpdated": hasQuietPeriod}, lease, nil
}

func (r *JenkinsRunner) postJobAction(ctx context.Context, name, action string, lease Lease) (map[string]any, Lease, error) {
	_, err := r.do(ctx, http.MethodPost, jobPath(name)+"/"+action, nil, "", 64<<10)

	return map[string]any{"job": name, "action": action}, lease, err
}

func (r *JenkinsRunner) triggerBuild(ctx context.Context, name string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	values, err := formParameters(parameters["parameters"])
	if err != nil {
		return nil, lease, err
	}
	path := jobPath(name) + "/build"
	if len(values) > 0 {
		path += "WithParameters"
	}
	_, err = r.do(ctx, http.MethodPost, path, strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", 64<<10)
	if err != nil {
		return nil, lease, err
	}

	return map[string]any{"job": name, "queued": true}, lease, nil
}

func (r *JenkinsRunner) getBuild(ctx context.Context, resource domain.TaskResource, lease Lease) (map[string]any, Lease, error) {
	job, number, err := buildIdentity(resource)
	if err != nil {
		return nil, lease, err
	}
	var build map[string]any
	err = r.getJSON(ctx, jobPath(job)+"/"+number+"/api/json?tree=number,result,timestamp,duration,estimatedDuration,building,displayName,description,queueId", &build)

	return build, lease, err
}

func (r *JenkinsRunner) getBuildLogs(ctx context.Context, resource domain.TaskResource, lease Lease) (map[string]any, Lease, error) {
	job, number, err := buildIdentity(resource)
	if err != nil {
		return nil, lease, err
	}
	logData, err := r.do(ctx, http.MethodGet, jobPath(job)+"/"+number+"/consoleText", nil, "", jenkinsMaxLog)

	return map[string]any{"job": job, "number": number, "logs": string(logData), "truncated": len(logData) == jenkinsMaxLog}, lease, err
}

func (r *JenkinsRunner) postBuildAction(ctx context.Context, resource domain.TaskResource, action string, lease Lease) (map[string]any, Lease, error) {
	job, number, err := buildIdentity(resource)
	if err != nil {
		return nil, lease, err
	}
	_, err = r.do(ctx, http.MethodPost, jobPath(job)+"/"+number+"/"+action, nil, "", 64<<10)

	return map[string]any{"job": job, "number": number, "action": action}, lease, err
}

func (r *JenkinsRunner) retryBuild(ctx context.Context, resource domain.TaskResource, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	job, number, err := buildIdentity(resource)
	if err != nil {
		return nil, lease, err
	}
	result, lease, err := r.triggerBuild(ctx, job, parameters, lease)
	if result != nil {
		result["retriedBuild"] = number
	}

	return result, lease, err
}

func (r *JenkinsRunner) getQueueItem(ctx context.Context, resource domain.TaskResource, lease Lease) (map[string]any, Lease, error) {
	id, err := queueIdentity(resource)
	if err != nil {
		return nil, lease, err
	}
	var item map[string]any
	err = r.getJSON(ctx, "/queue/item/"+id+"/api/json", &item)

	return item, lease, err
}

func (r *JenkinsRunner) cancelQueueItem(ctx context.Context, resource domain.TaskResource, lease Lease) (map[string]any, Lease, error) {
	id, err := queueIdentity(resource)
	if err != nil {
		return nil, lease, err
	}
	form := url.Values{"id": []string{id}}
	_, err = r.do(ctx, http.MethodPost, "/queue/cancelItem", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", 64<<10)

	return map[string]any{"queueId": id, "cancelled": err == nil}, lease, err
}

// jenkinsNodePathSegment returns the URL path segment Jenkins uses to address
// a specific computer. The built-in node is addressed via "(master)" in the
// URL path - the parentheses are literal and the internal name stays "master"
// even after Jenkins 2.319+ renamed the displayName to "Built-In Node".
// Regular agent nodes use their displayName directly.
func jenkinsNodePathSegment(displayName string) string {
	switch displayName {
	case "Built-In Node", "master":
		return "(master)"
	}
	return displayName
}

func (r *JenkinsRunner) getNode(ctx context.Context, name string, lease Lease) (map[string]any, Lease, error) {
	var node map[string]any
	err := r.getJSON(ctx, "/computer/"+jenkinsNodePathSegment(name)+"/api/json?tree=displayName,offline,temporarilyOffline,offlineCauseReason,idle,numExecutors,executors[currentExecutable[number,url]]", &node)

	return node, lease, err
}

func (r *JenkinsRunner) setNodeOffline(ctx context.Context, name string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	offline, ok := parameters["offline"].(bool)
	if !ok {
		return nil, lease, fmt.Errorf("parameter %q must be a boolean", "offline")
	}
	message, _ := parameters["message"].(string)
	if len(message) > 500 {
		return nil, lease, fmt.Errorf("offline message exceeds 500 characters")
	}
	current, _, err := r.getNode(ctx, name, lease)
	if err != nil {
		return nil, lease, err
	}
	currentOffline, _ := current["temporarilyOffline"].(bool)
	if currentOffline != offline {
		form := url.Values{"offlineMessage": []string{message}}
		_, err = r.do(ctx, http.MethodPost, "/computer/"+jenkinsNodePathSegment(name)+"/toggleOffline", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", 64<<10)
	}

	return map[string]any{"node": name, "offline": offline, "changed": currentOffline != offline}, lease, err
}

func (r *JenkinsRunner) getCredentialReference(ctx context.Context, id string, lease Lease) (map[string]any, Lease, error) {
	var response struct {
		Credentials []jenkinsCredential `json:"credentials"`
	}
	if err := r.getJSON(ctx, "/credentials/store/system/domain/_/api/json?tree=credentials[id,displayName,description,typeName]", &response); err != nil {
		return nil, lease, err
	}
	for _, credential := range response.Credentials {
		if credential.ID == id {
			return map[string]any{"id": credential.ID, "displayName": credential.DisplayName, "description": credential.Description, "typeName": credential.TypeName}, lease, nil
		}
	}

	return nil, lease, fmt.Errorf("Jenkins credential reference %q was not found", id)
}

func (r *JenkinsRunner) credentialReferenceExists(ctx context.Context, id string) (bool, error) {
	var response struct {
		Credentials []jenkinsCredential `json:"credentials"`
	}
	if err := r.getJSON(ctx, "/credentials/store/system/domain/_/api/json?tree=credentials[id,displayName,description,typeName]", &response); err != nil {
		return false, err
	}
	for _, credential := range response.Credentials {
		if credential.ID == id {
			return true, nil
		}
	}
	return false, nil
}

func (r *JenkinsRunner) validateResource(resource domain.TaskResource) error {
	switch resource.Type {
	case "jenkins_job":
		if resource.ExternalID != "job/"+resource.Name || !r.jobAllowed(resource.Name) {
			return fmt.Errorf("Jenkins job is outside connector scope")
		}
	case "jenkins_build":
		job, _, err := buildIdentity(resource)
		if err != nil || !r.jobAllowed(job) {
			return fmt.Errorf("Jenkins build is outside connector scope")
		}
	case "jenkins_queue_item":
		if _, err := queueIdentity(resource); err != nil || !r.jobAllowed(resource.Namespace) {
			return fmt.Errorf("Jenkins queue item is outside connector scope")
		}
	case "jenkins_node", "jenkins_credential_reference":
		if strings.TrimSpace(resource.Name) == "" || strings.ContainsAny(resource.Name, "/?#") || resource.Name == ".." {
			return fmt.Errorf("Jenkins resource name is required")
		}
		expectedPrefix := "node/"
		if resource.Type == "jenkins_credential_reference" {
			expectedPrefix = "credential/"
		}
		if resource.ExternalID != expectedPrefix+resource.Name {
			return fmt.Errorf("Jenkins resource identity does not match its authorized scope")
		}
	case "jenkins_controller":
		if resource.ExternalID != r.baseURL.Host+r.baseURL.Path {
			return fmt.Errorf("Jenkins controller identity does not match its authorized scope")
		}
	default:
		return fmt.Errorf("unsupported Jenkins resource type %q", resource.Type)
	}

	return nil
}

func validateJenkinsActionResource(action, resourceType string) error {
	expected := map[string]string{
		"jenkins.controller.get": "jenkins_controller",
		"jenkins.job.get":        "jenkins_job", "jenkins.job.config.get": "jenkins_job", "jenkins.job.configure": "jenkins_job", "jenkins.job.enable": "jenkins_job",
		"jenkins.job.disable": "jenkins_job", "jenkins.job.build": "jenkins_job", "jenkins.job.create": "jenkins_controller",
		"jenkins.job.update": "jenkins_job", "jenkins.job.delete": "jenkins_job",
		"jenkins.build.get": "jenkins_build", "jenkins.build.logs": "jenkins_build", "jenkins.build.stop": "jenkins_build",
		"jenkins.build.retry": "jenkins_build", "jenkins.queue.get": "jenkins_queue_item", "jenkins.queue.cancel": "jenkins_queue_item",
		"jenkins.node.get": "jenkins_node", "jenkins.node.set_offline": "jenkins_node",
		"jenkins.credential_reference.get": "jenkins_credential_reference",
	}
	if expectedType, ok := expected[action]; !ok || expectedType != resourceType {
		return fmt.Errorf("Jenkins action %q is not allowed for resource type %q", action, resourceType)
	}

	return nil
}

func (r *JenkinsRunner) jobAllowed(name string) bool {
	// Empty scope = allow all jobs. Used when an integration is first created
	// without a pinned Job list; the admin narrows it from the connector detail
	// page once the connector has discovered the available jobs.
	if len(r.allowedJobs) == 0 {
		return r.allowAll
	}
	name = strings.Trim(strings.TrimSpace(name), "/")
	for _, scope := range r.allowedJobs {
		if strings.HasSuffix(scope, "/*") {
			prefix := strings.TrimSuffix(scope, "*")
			if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
				return true
			}
		} else if name == scope {
			return true
		}
	}

	return false
}

func (r *JenkinsRunner) getJSON(ctx context.Context, path string, output any) error {
	data, err := r.do(ctx, http.MethodGet, path, nil, "", jenkinsMaxResponse)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, output)
}

func (r *JenkinsRunner) do(ctx context.Context, method, path string, body io.Reader, contentType string, limit int64) ([]byte, error) {
	endpoint := *r.baseURL
	endpoint.Path = strings.TrimRight(r.baseURL.Path, "/") + strings.Split(path, "?")[0]
	// Go's url.URL.EscapedPath() over-escapes parentheses as %28/%29 even
	// though RFC 3986 permits them in path segments. Jenkins' Stapler router
	// does not decode %28/%29 back to ( ) , so /computer/(Built-In Node)/api/json
	// would be sent as /computer/%28Built-In%20Node%29/api/json and 404. Set
	// RawPath with literal parens so the HTTP request emits them unescaped.
	endpoint.RawPath = strings.NewReplacer("%28", "(", "%29", ")").Replace((&url.URL{Path: endpoint.Path}).EscapedPath())
	if index := strings.IndexByte(path, '?'); index >= 0 {
		endpoint.RawQuery = path[index+1:]
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(r.username, r.apiToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if method != http.MethodGet {
		crumb, field, crumbErr := r.crumb(ctx)
		if crumbErr != nil {
			return nil, crumbErr
		}
		if crumb != "" && field != "" {
			req.Header.Set(field, crumb)
		}
		// Some Jenkins reverse proxies emit a session cookie with the crumb
		// response but do not reliably honor Go's CookieJar on the follow-up
		// request. Forward the cookies captured from that response explicitly.
		r.cookieMu.RLock()
		if r.sessionCookie != "" {
			req.Header.Set("Cookie", r.sessionCookie)
		}
		r.cookieMu.RUnlock()
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
	// Jenkins' createItem endpoint returns 302 on success (redirecting to the
	// new job's page). The HTTP client is configured with CheckRedirect set to
	// http.ErrUseLastResponse so the 302 is surfaced to us intact. Treat any
	// 2xx or 3xx as success.
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		if len(data) > 4096 {
			data = data[:4096]
		}
		message := strings.ReplaceAll(strings.TrimSpace(string(data)), r.apiToken, "[REDACTED]")

		return nil, &jenkinsHTTPError{StatusCode: resp.StatusCode, Body: message}
	}

	return data, nil
}

func (r *JenkinsRunner) crumb(ctx context.Context) (string, string, error) {
	endpoint := *r.baseURL
	endpoint.Path = strings.TrimRight(r.baseURL.Path, "/") + "/crumbIssuer/api/json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", "", err
	}
	req.SetBasicAuth(r.username, r.apiToken)
	resp, err := r.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", "", nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("Jenkins crumb issuer returned %d", resp.StatusCode)
	}
	var value struct{ Crumb, CrumbRequestField string }
	responseCookies := resp.Cookies()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&value); err != nil {
		return "", "", err
	}
	if len(responseCookies) > 0 {
		cookies := make([]string, 0, len(responseCookies))
		for _, cookie := range responseCookies {
			cookies = append(cookies, cookie.Name+"="+cookie.Value)
		}
		r.cookieMu.Lock()
		r.sessionCookie = strings.Join(cookies, "; ")
		r.cookieMu.Unlock()
	}

	return value.Crumb, value.CrumbRequestField, nil
}

func (r *JenkinsRunner) lease(expiresAt time.Time) Lease {
	fingerprint := sha256.Sum256([]byte(r.username + "@" + r.baseURL.Host))

	return Lease{Provider: "JENKINS_CREDENTIAL_REFERENCE", Fingerprint: hex.EncodeToString(fingerprint[:8]), ExpiresAt: expiresAt}
}

type jenkinsJob struct {
	Name      string         `json:"name"`
	Class     string         `json:"_class"`
	Color     string         `json:"color"`
	Buildable bool           `json:"buildable"`
	InQueue   bool           `json:"inQueue"`
	LastBuild *jenkinsBuild  `json:"lastBuild"`
	Builds    []jenkinsBuild `json:"builds"`
}

type jenkinsBuild struct {
	Number    int64  `json:"number"`
	Result    string `json:"result"`
	Timestamp int64  `json:"timestamp"`
}

type jenkinsCredential struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	TypeName    string `json:"typeName"`
}

func jobResource(fullName string, job jenkinsJob) DiscoveredResource {
	return DiscoveredResource{ExternalID: "job/" + fullName, Name: fullName, ResourceType: "jenkins_job",
		Labels: map[string]any{"color": job.Color}, Annotations: map[string]any{},
		Metadata: map[string]any{"buildable": job.Buildable, "inQueue": job.InQueue}}
}

func buildResource(job string, build jenkinsBuild) DiscoveredResource {
	number := strconv.FormatInt(build.Number, 10)
	return DiscoveredResource{ExternalID: "job/" + job + "/build/" + number, Name: number, Namespace: job,
		ResourceType: "jenkins_build", Labels: map[string]any{"result": build.Result}, Annotations: map[string]any{},
		Metadata: map[string]any{"job": job, "number": build.Number, "timestamp": build.Timestamp}}
}

func credentialResource(credential jenkinsCredential) DiscoveredResource {
	return DiscoveredResource{ExternalID: "credential/" + credential.ID, Name: credential.ID,
		ResourceType: "jenkins_credential_reference", Labels: map[string]any{"type": credential.TypeName}, Annotations: map[string]any{},
		Metadata: map[string]any{"displayName": credential.DisplayName, "description": credential.Description}}
}

func jobPath(name string) string {
	parts := strings.Split(strings.Trim(name, "/"), "/")
	var path strings.Builder
	for _, part := range parts {
		path.WriteString("/job/")
		path.WriteString(part)
	}

	return path.String()
}

func buildIdentity(resource domain.TaskResource) (string, string, error) {
	prefix := "job/" + resource.Namespace + "/build/"
	if resource.Namespace == "" || !strings.HasPrefix(resource.ExternalID, prefix) || resource.ExternalID != prefix+resource.Name {
		return "", "", fmt.Errorf("build resource identity does not match its authorized scope")
	}
	if _, err := strconv.ParseInt(resource.Name, 10, 64); err != nil {
		return "", "", fmt.Errorf("invalid Jenkins build number")
	}

	return resource.Namespace, resource.Name, nil
}

func queueIdentity(resource domain.TaskResource) (string, error) {
	if resource.ExternalID != "queue/"+resource.Name {
		return "", fmt.Errorf("queue resource identity does not match its authorized scope")
	}
	if _, err := strconv.ParseInt(resource.Name, 10, 64); err != nil {
		return "", fmt.Errorf("invalid Jenkins queue ID")
	}

	return resource.Name, nil
}

func optionalInt(value any, minimum, maximum int) (int, bool) {
	var number int
	switch typed := value.(type) {
	case float64:
		number = int(typed)
	case int:
		number = typed
	case json.Number:
		parsed, err := strconv.Atoi(typed.String())
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}

	return number, number >= minimum && number <= maximum
}

func formParameters(value any) (url.Values, error) {
	result := url.Values{}
	if value == nil {
		return result, nil
	}
	parameters, ok := value.(map[string]any)
	if !ok || len(parameters) > 50 {
		return nil, fmt.Errorf("parameters must be an object with at most 50 entries")
	}
	for key, raw := range parameters {
		if strings.TrimSpace(key) == "" || len(key) > 128 {
			return nil, fmt.Errorf("invalid Jenkins build parameter name")
		}
		var text string
		switch typed := raw.(type) {
		case string:
			text = typed
		case bool, float64, int, json.Number:
			text = fmt.Sprint(typed)
		default:
			return nil, fmt.Errorf("Jenkins build parameter %q must be a scalar", key)
		}
		if len(text) > 4000 {
			return nil, fmt.Errorf("Jenkins build parameter %q is too large", key)
		}
		result.Set(key, text)
	}

	return result, nil
}

func updateJobConfig(config []byte, description string, setDescription bool, quietPeriod int, setQuietPeriod bool) ([]byte, error) {
	config = bytes.Replace(config, []byte(`<?xml version="1.1"`), []byte(`<?xml version="1.0"`), 1)
	config = bytes.Replace(config, []byte(`<?xml version='1.1'`), []byte(`<?xml version='1.0'`), 1)
	decoder := xml.NewDecoder(bytes.NewReader(config))
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	var stack []string
	foundDescription := false
	foundQuietPeriod := false
	skipTextDepth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse Jenkins job config: %w", err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			stack = append(stack, typed.Name.Local)
			if len(stack) == 2 && typed.Name.Local == "description" && setDescription {
				foundDescription = true
				skipTextDepth = len(stack)
			}
			if len(stack) == 2 && typed.Name.Local == "quietPeriod" && setQuietPeriod {
				foundQuietPeriod = true
				skipTextDepth = len(stack)
			}
		case xml.EndElement:
			if len(stack) == 1 {
				if setDescription && !foundDescription {
					if err := encoder.EncodeElement(description, xml.StartElement{Name: xml.Name{Local: "description"}}); err != nil {
						return nil, err
					}
				}
				if setQuietPeriod && !foundQuietPeriod {
					if err := encoder.EncodeElement(quietPeriod, xml.StartElement{Name: xml.Name{Local: "quietPeriod"}}); err != nil {
						return nil, err
					}
				}
			}
			if skipTextDepth == len(stack) {
				var value any = description
				if typed.Name.Local == "quietPeriod" {
					value = quietPeriod
				}
				if err := encoder.EncodeToken(xml.CharData(fmt.Sprint(value))); err != nil {
					return nil, err
				}
				skipTextDepth = 0
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if skipTextDepth > 0 {
				continue
			}
		}
		if err := encoder.EncodeToken(token); err != nil {
			return nil, err
		}
	}
	if err := encoder.Flush(); err != nil {
		return nil, err
	}

	return output.Bytes(), nil
}

func IsJenkinsUnknownOutcome(action string, err error) bool {
	if err == nil || (!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled)) {
		return false
	}
	switch action {
	case "jenkins.job.create", "jenkins.job.update", "jenkins.job.configure", "jenkins.job.delete", "jenkins.job.enable", "jenkins.job.disable", "jenkins.job.build", "jenkins.build.stop",
		"jenkins.build.retry", "jenkins.queue.cancel", "jenkins.node.set_offline":
		return true
	default:
		return false
	}
}

func splitCommaSeparated(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}

	return result
}
