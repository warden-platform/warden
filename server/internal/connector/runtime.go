package connector

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

type Runtime struct {
	controlURL            string
	name                  string
	environment           string
	version               string
	signingKey            []byte
	runner                Runner
	connectorType         string
	capabilities          []string
	http                  *http.Client
	connectorID           string
	syncInterval          time.Duration
	integrationReconciler func(ctx context.Context, integrations []domain.Integration) error
}

type Runner interface {
	ListResources(context.Context) []DiscoveredResource
	Execute(context.Context, domain.TaskEnvelope) (map[string]any, Lease, error)
}

func NewRuntime(controlURL, name, environment, version string, signingKey []byte, runner Runner) *Runtime {
	return NewTypedRuntime(controlURL, name, environment, version, "kubernetes", signingKey,
		[]string{
			domain.ChangeSafetyCapability,
			"deployment.get@v1", "deployment.update_image@v2", "deployment.restart@v1", "deployment.scale@v1", "deployment.rollout_status@v1", "pod.logs.read@v1",
			"pod.get@v1", "pod.events@v1", "pod.metrics@v1", "event.list@v1",
			"statefulset.get@v1", "statefulset.restart@v1", "statefulset.scale@v1", "statefulset.update_image@v1",
			"daemonset.get@v1", "replicaset.get@v1", "job.get@v1", "cronjob.get@v1", "cronjob.suspend@v1", "cronjob.resume@v1", "cronjob.trigger@v1",
			"service.get@v1", "endpoints.get@v1", "ingress.get@v1", "configmap.get@v1",
			"node.get@v1", "node.metrics@v1", "namespace.list@v1", "namespace.dashboard_configmap.apply@v1", "configmap.dashboard.delete@v1",
		}, runner)
}

func NewTypedRuntime(controlURL, name, environment, version, connectorType string, signingKey []byte, capabilities []string, runner Runner) *Runtime {
	return &Runtime{controlURL: strings.TrimRight(controlURL, "/"), name: name,
		environment: environment, version: version, connectorType: connectorType, signingKey: signingKey,
		capabilities: capabilities, runner: runner,
		http: &http.Client{Timeout: 15 * time.Second}, syncInterval: 5 * time.Minute}
}

const maxResourceSyncJitter = 30 * time.Second

func resourceSyncInterval(base time.Duration, connectorID string) time.Duration {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(connectorID))
	jitter := time.Duration(hash.Sum32()%uint32(maxResourceSyncJitter/time.Millisecond+1)) * time.Millisecond
	return base + jitter
}

// SetIntegrationReconciler installs a callback invoked whenever the connector
// re-syncs its integrations from the control plane. The callback receives the
// current set of integrations and is responsible for materializing/removing
// runners on the shared MultiRunner.
func (r *Runtime) SetIntegrationReconciler(fn func(ctx context.Context, integrations []domain.Integration) error) {
	r.integrationReconciler = fn
}

func (r *Runtime) Run(ctx context.Context) error {
	if err := r.register(ctx); err != nil {
		return err
	}
	// Materialize integration runners before the first catalog sync. Syncing
	// the primary runner alone would make every existing middleware/cloud
	// resource look missing until the next (potentially slow) discovery pass.
	if r.integrationReconciler != nil {
		_ = r.syncIntegrations(ctx)
	}
	heartbeat := time.NewTicker(15 * time.Second)
	poll := time.NewTicker(2 * time.Second)
	resourceSyncEvery := resourceSyncInterval(r.syncInterval, r.connectorID)
	resourceSync := time.NewTicker(resourceSyncEvery)
	log.Printf("connector resource sync scheduled (connector=%s interval=%s)", r.connectorID, resourceSyncEvery)
	resourceSyncDone := make(chan error, 1)
	resourceSyncRunning := false
	startResourceSync := func() {
		if resourceSyncRunning {
			return
		}
		resourceSyncRunning = true
		go func() { resourceSyncDone <- r.syncResources(ctx) }()
	}
	// Discover immediately, but never block heartbeats or task polling. Cloud
	// integrations may scan many regions and take longer than one sync period.
	startResourceSync()
	var integrationSync *time.Ticker
	var integrationC <-chan time.Time
	if r.integrationReconciler != nil {
		// Reconcile integrations (e.g. Jenkins runners) frequently so a newly
		// added integration is materialized within seconds.
		integrationSync = time.NewTicker(10 * time.Second)
		integrationC = integrationSync.C
	}
	defer heartbeat.Stop()
	defer poll.Stop()
	defer resourceSync.Stop()
	if integrationSync != nil {
		defer integrationSync.Stop()
	}
	var heartbeatFailures, pollFailures int
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeat.C:
			if err := r.post(ctx, "/api/v1/connectors/"+r.connectorID+"/heartbeat", nil, nil); err != nil {
				heartbeatFailures++
				log.Printf("connector heartbeat failed (attempt %d): %v", heartbeatFailures, err)
			} else {
				heartbeatFailures = 0
			}
		case <-poll.C:
			if err := r.poll(ctx); err != nil {
				pollFailures++
				log.Printf("poll connector tasks failed (attempt %d): %v", pollFailures, err)
			} else {
				pollFailures = 0
			}
		case <-resourceSync.C:
			startResourceSync()
		case err := <-resourceSyncDone:
			resourceSyncRunning = false
			if err != nil {
				// Sync failures are transient; log and continue rather than
				// tearing down the whole runtime.
				fmt.Printf("resource sync failed: %v\n", err)
			}
		case <-integrationC:
			// The current discovery pass holds a stable runner snapshot. Delay
			// reconciliation instead of closing/replacing a runner in use.
			if resourceSyncRunning {
				continue
			}
			if err := r.syncIntegrations(ctx); err != nil {
				fmt.Printf("integration sync failed: %v\n", err)
			}
		}
	}
}

func (r *Runtime) register(ctx context.Context) error {
	payload := map[string]any{"name": r.name, "connectorType": r.connectorType, "environment": r.environment,
		"version": r.version, "capabilities": r.capabilities}
	var response struct {
		ID string `json:"id"`
	}
	if err := r.post(ctx, "/api/v1/connectors/register", payload, &response); err != nil {
		return fmt.Errorf("register connector: %w", err)
	}
	if response.ID == "" {
		return fmt.Errorf("register connector returned no id")
	}
	r.connectorID = response.ID
	return nil
}

// syncResources discovers deployments across allowed namespaces and reports
// them to pam-control so the resource catalog stays in sync with the cluster.
func (r *Runtime) syncResources(ctx context.Context) error {
	if r.runner == nil {
		return nil
	}
	syncID := time.Now().UTC().Format(time.RFC3339Nano)
	// The shared MultiRunner reports discovery grouped by integration so the
	// control plane can orphan stale resources per integration. Non-MultiRunner
	// connectors (e.g. Linux) keep the flat payload.
	if multi, ok := r.runner.(*MultiRunner); ok {
		groups := multi.DiscoverGroups(ctx)
		payload := map[string]any{"connectorId": r.connectorID, "syncId": syncID, "groups": groups}
		if err := r.post(ctx, "/api/v2/resources/sync", payload, nil); err == nil {
			return nil
		} else if !controlPlaneReturned(err, http.StatusNotFound) {
			return fmt.Errorf("sync grouped resources: %w", err)
		}

		// A V1 control plane only understands a connector-wide flat snapshot. It
		// is safe to fall back when every group completed; otherwise skip the
		// entire sync so an old server cannot orphan resources hidden by a failed
		// integration discovery pass.
		legacyResources := make([]DiscoveredResource, 0)
		for _, group := range groups {
			if !group.Complete {
				return fmt.Errorf("control plane does not support grouped resource sync; refusing incomplete legacy snapshot")
			}
			legacyResources = append(legacyResources, group.Resources...)
		}
		legacyPayload := map[string]any{"connectorId": r.connectorID, "syncId": syncID, "resources": legacyResources}
		if err := r.post(ctx, "/api/v1/resources/sync", legacyPayload, nil); err != nil {
			return fmt.Errorf("sync legacy resources: %w", err)
		}
		return nil
	}
	startedAt := time.Now()
	resources := r.runner.ListResources(ctx)
	discoveryDuration := time.Since(startedAt)
	log.Printf("connector resource discovery completed (connector=%s resources=%d duration=%s)",
		r.connectorID, len(resources), discoveryDuration)
	payload := map[string]any{"connectorId": r.connectorID, "syncId": syncID, "complete": true, "resources": resources}
	if err := r.post(ctx, "/api/v1/resources/sync", payload, nil); err != nil {
		return fmt.Errorf("sync resources (connector=%s resources=%d discovery=%s total=%s): %w",
			r.connectorID, len(resources), discoveryDuration, time.Since(startedAt), err)
	}
	log.Printf("connector resource sync completed (connector=%s resources=%d discovery=%s upload=%s total=%s)",
		r.connectorID, len(resources), discoveryDuration, time.Since(startedAt)-discoveryDuration, time.Since(startedAt))
	return nil
}

// syncIntegrations pulls the integrations assigned to this connector from the
// control plane and asks the reconciler to materialize/remove runners. This is
// how a connector learns about Jenkins (and other protocol) integrations
// without env-var baking.
func (r *Runtime) syncIntegrations(ctx context.Context) error {
	if r.integrationReconciler == nil {
		return nil
	}
	var integrations []domain.Integration
	if err := r.get(ctx, "/api/v1/connector-runtime/"+r.connectorID+"/integrations", &integrations); err != nil {
		return fmt.Errorf("sync integrations: %w", err)
	}
	if err := r.integrationReconciler(ctx, integrations); err != nil {
		return fmt.Errorf("reconcile integrations: %w", err)
	}
	return nil
}

// FetchCredentialValue resolves a "pam:" credential reference against the
// control plane's managed credential ledger. The request is HMAC-signed and
// authenticated as this connector; the control plane enforces the credential's
// binding scope (integration + connector) before returning the plaintext.
func (r *Runtime) FetchCredentialValue(ctx context.Context, name, integrationID string) (string, error) {
	var response struct {
		Value string `json:"value"`
	}
	payload := map[string]any{"connectorId": r.connectorID, "name": name, "integrationId": integrationID}
	if err := r.post(ctx, "/api/v1/connectors/credentials/resolve", payload, &response); err != nil {
		return "", fmt.Errorf("resolve credential %s: %w", name, err)
	}
	if response.Value == "" {
		return "", fmt.Errorf("control plane returned empty credential value for %s", name)
	}
	return response.Value, nil
}

// ReportIntegrationHealth posts a reconcile-time probe result for an
// integration back to the control plane. An empty message means healthy.
func (r *Runtime) ReportIntegrationHealth(ctx context.Context, integrationID, message string) {
	payload := map[string]string{"error": message}
	if err := r.post(ctx, "/api/v1/connector-runtime/"+r.connectorID+"/integrations/"+integrationID+"/health", payload, nil); err != nil {
		fmt.Printf("report integration health %s: %v\n", integrationID, err)
	}
}

func (r *Runtime) poll(ctx context.Context) error {
	var tasks []domain.TaskEnvelope
	if err := r.get(ctx, "/api/v1/connectors/"+r.connectorID+"/tasks", &tasks); err != nil {
		return err
	}
	for _, task := range tasks {
		if err := task.Verify(r.signingKey, time.Now()); err != nil {
			_ = r.submit(ctx, task.ExecutionID, task.DispatchToken, "FAILED", nil, Lease{}, "TASK_ENVELOPE_REJECTED: "+err.Error())
			continue
		}
		if task.ConnectorID != r.connectorID {
			_ = r.submit(ctx, task.ExecutionID, task.DispatchToken, "FAILED", nil, Lease{}, "CONNECTOR_SCOPE_MISMATCH")
			continue
		}
		taskCtx, cancel := context.WithDeadline(ctx, task.Grant.ExpiresAt)
		var status, message string
		var result map[string]any
		var lease Lease
		if task.Provider != nil {
			status, result, message = r.executeProviderTask(taskCtx, task)
		} else {
			status, result, lease, message = r.executeTask(taskCtx, task)
		}
		cancel()
		if err := r.submit(ctx, task.ExecutionID, task.DispatchToken, status, result, lease, message); err != nil {
			return err
		}
	}
	return nil
}

type providerTaskRunner interface {
	ExecuteProvider(context.Context, domain.ProviderTask) (*domain.ProviderResponse, error)
}

func (r *Runtime) executeProviderTask(ctx context.Context, task domain.TaskEnvelope) (string, map[string]any, string) {
	runner, ok := r.runner.(providerTaskRunner)
	if !ok || task.Provider == nil {
		return "FAILED", nil, domain.ProviderErrIntegrationInactive + ": provider executor unavailable"
	}
	response, err := runner.ExecuteProvider(ctx, *task.Provider)
	if err != nil {
		status := "FAILED"
		if task.Provider.Binding.Capability.Write && isProviderTimeout(err) {
			// A write that times out may already have been applied upstream;
			// the result is indeterminate and must never be auto-retried.
			status = "UNKNOWN"
		}
		return status, nil, domain.SanitizeErrorMessage(err.Error())
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return "FAILED", nil, domain.ProviderErrUpstreamUnknown + ": normalize provider response"
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		return "FAILED", nil, domain.ProviderErrUpstreamUnknown + ": normalize provider response"
	}
	return "SUCCEEDED", result, ""
}

// isProviderTimeout reports whether err is the executor's timeout sentinel,
// which marks the outcome of a provider write as indeterminate.
func isProviderTimeout(err error) bool {
	var providerErr *domain.ProviderRequestError
	return errors.As(err, &providerErr) && providerErr.Code == domain.ProviderErrTimeout
}

func (r *Runtime) submit(ctx context.Context, executionID, dispatchToken, status string, result map[string]any, lease Lease, message string) error {
	payload := map[string]any{"dispatchToken": dispatchToken, "status": status, "result": result, "error": message, "lease": lease}
	return r.post(ctx, "/api/v1/connectors/"+r.connectorID+"/tasks/"+executionID+"/result", payload, nil)
}

func (r *Runtime) get(ctx context.Context, path string, output any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.controlURL+path, nil)
	if err != nil {
		return err
	}
	return r.do(req, output)
}

func (r *Runtime) post(ctx context.Context, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.controlURL+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return r.do(req, output)
}

func (r *Runtime) do(req *http.Request, output any) error {
	body, err := requestBodyBytes(req)
	if err != nil {
		return err
	}
	nonceBytes := make([]byte, 18)
	if _, err := rand.Read(nonceBytes); err != nil {
		return fmt.Errorf("generate connector request nonce: %w", err)
	}
	if err := domain.SignConnectorRequest(req, body, r.signingKey, time.Now(), base64.RawURLEncoding.EncodeToString(nonceBytes)); err != nil {
		return fmt.Errorf("sign connector request: %w", err)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &controlPlaneHTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	if output != nil {
		return json.NewDecoder(resp.Body).Decode(output)
	}
	return nil
}

type controlPlaneHTTPError struct {
	StatusCode int
	Body       string
}

func (e *controlPlaneHTTPError) Error() string {
	return fmt.Sprintf("control plane returned %d: %s", e.StatusCode, e.Body)
}

func controlPlaneReturned(err error, statusCode int) bool {
	var responseErr *controlPlaneHTTPError
	return errors.As(err, &responseErr) && responseErr.StatusCode == statusCode
}

func requestBodyBytes(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("read connector request body: %w", err)
	}
	req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}
