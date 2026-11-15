package connector

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

var dashboardConfigMapNamePattern = regexp.MustCompile(`^grafana-dashboard-[a-z0-9](?:[a-z0-9.-]{0,61}[a-z0-9])?$`)
var dashboardConfigMapKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,200}\.json$`)

var KubernetesResourceTypes = []string{
	"deployment", "statefulset", "daemonset", "replicaset",
	"pod", "job", "cronjob",
	"service", "endpoints", "ingress",
	"configmap", "networkpolicy", "poddisruptionbudget", "event",
	"node", "namespace",
}

var KubernetesCapabilities = []string{
	"kubernetes.resource.get@v1", "kubernetes.resource.list@v1", "kubernetes.resource.preview@v1", "kubernetes.resource.create@v1", "kubernetes.resource.update@v1", "kubernetes.resource.delete@v1",
	"deployment.get@v1", "deployment.update_image@v2", "deployment.restart@v1", "deployment.scale@v1", "deployment.rollout_status@v1", "pod.logs.read@v1",
	"pod.get@v1", "pod.events@v1", "pod.metrics@v1", "event.list@v1",
	"statefulset.get@v1", "statefulset.restart@v1", "statefulset.scale@v1", "statefulset.update_image@v1",
	"daemonset.get@v1", "replicaset.get@v1", "job.get@v1", "cronjob.get@v1", "cronjob.suspend@v1", "cronjob.resume@v1", "cronjob.trigger@v1",
	"service.get@v1", "endpoints.get@v1", "ingress.get@v1", "configmap.get@v1",
	"node.get@v1", "node.metrics@v1", "namespace.list@v1", "namespace.dashboard_configmap.apply@v1", "configmap.dashboard.delete@v1",
}

type CredentialProvider interface {
	Issue(context.Context, string, time.Duration) (kubernetes.Interface, Lease, error)
	// BootstrapClient returns the connector's bootstrap Kubernetes client, used
	// for read-only resource discovery (ListDeployments). The bootstrap client
	// carries the warden-connector-bootstrap identity, which the generated manifest
	// binds to the warden-resource-discoverer Role.
	BootstrapClient() kubernetes.Interface
}

// DynamicCredentialProvider is implemented by production credential providers
// that can mint the same short-lived execution identity for Kubernetes's
// unstructured Dynamic Client. Keeping it optional preserves typed-client test
// providers and makes generic CRUD fail closed when a provider cannot support it.
type DynamicCredentialProvider interface {
	IssueDynamic(context.Context, string, time.Duration) (dynamic.Interface, Lease, error)
}

type Lease struct {
	Provider    string    `json:"provider"`
	Fingerprint string    `json:"fingerprint"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type TokenRequestProvider struct {
	bootstrap      kubernetes.Interface
	baseConfig     *rest.Config
	serviceAccount string
	audience       string
}

func NewInClusterTokenRequestProvider(serviceAccount, audience string) (*TokenRequestProvider, error) {
	config, err := loadInClusterRestConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster configuration: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create bootstrap kubernetes client: %w", err)
	}
	if strings.TrimSpace(audience) == "" {
		audience = tokenAudience(config.BearerToken)
	}
	return &TokenRequestProvider{bootstrap: client, baseConfig: config, serviceAccount: serviceAccount, audience: audience}, nil
}

// tokenAudience inherits the audience of the projected service-account token
// already accepted by this API server. Managed distributions do not all accept
// TokenRequests with an empty audience list.
func tokenAudience(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Audience any `json:"aud"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	switch audience := claims.Audience.(type) {
	case string:
		return strings.TrimSpace(audience)
	case []any:
		for _, value := range audience {
			if candidate, ok := value.(string); ok && strings.TrimSpace(candidate) != "" {
				return strings.TrimSpace(candidate)
			}
		}
	}
	return ""
}

// loadInClusterRestConfig mirrors rest.InClusterConfig() but allows the
// service-account file directory to be overridden via WARDEN_SA_DIR for local
// development (where /var/run/secrets/... is not writable without root).
// When WARDEN_SA_DIR is unset, it falls back to rest.InClusterConfig().
func loadInClusterRestConfig() (*rest.Config, error) {
	saDir := os.Getenv("WARDEN_SA_DIR")
	if saDir == "" {
		return rest.InClusterConfig()
	}
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT must be set when WARDEN_SA_DIR is used")
	}
	token, err := os.ReadFile(filepath.Join(saDir, "token"))
	if err != nil {
		return nil, fmt.Errorf("read sa token: %w", err)
	}
	caData, err := os.ReadFile(filepath.Join(saDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("read sa ca.crt: %w", err)
	}
	return &rest.Config{
		Host:            "https://" + net.JoinHostPort(host, port),
		BearerToken:     string(token),
		TLSClientConfig: rest.TLSClientConfig{CAData: caData},
	}, nil
}

func (p *TokenRequestProvider) Issue(ctx context.Context, namespace string, ttl time.Duration) (kubernetes.Interface, Lease, error) {
	config, lease, err := p.issueExecutionConfig(ctx, namespace, ttl)
	if err != nil {
		return nil, Lease{}, err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, Lease{}, fmt.Errorf("create execution kubernetes client: %w", err)
	}
	return client, lease, nil
}

func (p *TokenRequestProvider) IssueDynamic(ctx context.Context, namespace string, ttl time.Duration) (dynamic.Interface, Lease, error) {
	config, lease, err := p.issueExecutionConfig(ctx, namespace, ttl)
	if err != nil {
		return nil, Lease{}, err
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, Lease{}, fmt.Errorf("create execution kubernetes dynamic client: %w", err)
	}
	return client, lease, nil
}

func (p *TokenRequestProvider) issueExecutionConfig(ctx context.Context, namespace string, ttl time.Duration) (*rest.Config, Lease, error) {
	seconds := int64(ttl.Seconds())
	// Kubernetes requires TokenRequest ExpirationSeconds >= 600 (10 min).
	// The connector only uses the token within the grant window, so clamping
	// here does not extend the actual authorization window.
	if seconds < 600 {
		seconds = 600
	}
	spec := tokenRequestSpec(p.audience, seconds)
	request := &authenticationv1.TokenRequest{Spec: spec}
	token, err := p.bootstrap.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, p.serviceAccount, request, metav1.CreateOptions{})
	if err != nil {
		return nil, Lease{}, fmt.Errorf("request short-lived service account token: %w", err)
	}
	config := rest.CopyConfig(p.baseConfig)
	config.BearerToken = token.Status.Token
	config.BearerTokenFile = ""
	fingerprint := sha256.Sum256([]byte(token.Status.Token))
	return config, Lease{
		Provider: "KUBERNETES_TOKEN_REQUEST", Fingerprint: hex.EncodeToString(fingerprint[:8]),
		ExpiresAt: token.Status.ExpirationTimestamp.Time,
	}, nil
}

func tokenRequestSpec(audience string, seconds int64) authenticationv1.TokenRequestSpec {
	spec := authenticationv1.TokenRequestSpec{ExpirationSeconds: &seconds}
	if audience = strings.TrimSpace(audience); audience != "" {
		spec.Audiences = []string{audience}
	}
	return spec
}

// BootstrapClient returns the bootstrap client used for resource discovery.
func (p *TokenRequestProvider) BootstrapClient() kubernetes.Interface { return p.bootstrap }

// DiscoveredResource is the discovery view of a target resource reported back
// to warden-control via POST /api/v1/resources/sync.
type DiscoveredResource struct {
	ExternalID    string         `json:"externalId"`
	Name          string         `json:"name"`
	Namespace     string         `json:"namespace"`
	ResourceType  string         `json:"resourceType"`
	IntegrationID string         `json:"integrationId,omitempty"`
	Labels        map[string]any `json:"labels"`
	Annotations   map[string]any `json:"annotations"`
	Metadata      map[string]any `json:"metadata"`
}

type KubernetesRunner struct {
	credentials       CredentialProvider
	allowedNamespaces map[string]struct{}
	allowedRegistries []string
}

func (r *KubernetesRunner) ExecuteProvider(ctx context.Context, provider domain.ProviderTask) (*domain.ProviderResponse, error) {
	actions := map[string]string{
		"deployment": "deployment.get", "statefulset": "statefulset.get", "daemonset": "daemonset.get",
		"replicaset": "replicaset.get", "job": "job.get", "cronjob": "cronjob.get", "service": "service.get",
		"endpoints": "endpoints.get", "ingress": "ingress.get", "configmap": "configmap.get", "pod": "pod.get", "node": "node.get",
	}
	action := actions[provider.Binding.Resource.ResourceType]
	if action == "" {
		return nil, &domain.ProviderRequestError{Code: domain.ProviderErrCapabilityMismatch, Message: "Kubernetes kind has no governed read adapter"}
	}
	_, _, timeout := domain.EffectiveProviderLimits(provider.Binding.Capability)
	expires := time.Now().Add(time.Duration(timeout) * time.Second)
	task := domain.TaskEnvelope{Resource: domain.TaskResource{ID: provider.Binding.Resource.ID, ExternalID: provider.Binding.Resource.ExternalID,
		Type: provider.Binding.Resource.ResourceType, Name: provider.Binding.Resource.Name, Namespace: provider.Binding.Resource.Namespace},
		Action: domain.TaskAction{Name: action, Version: 1}, Parameters: map[string]any{"name": provider.Binding.Resource.Name, "namespace": provider.Binding.Resource.Namespace},
		Grant: domain.TaskGrant{ExpiresAt: expires}}
	data, _, err := r.Execute(ctx, task)
	if err != nil {
		return nil, err
	}
	return &domain.ProviderResponse{Status: domain.ProviderResponseSucceeded,
		Resource: domain.ProviderResourceRef{Kind: provider.Binding.Resource.ResourceType, Name: provider.Binding.Resource.Name,
			ProjectID: provider.Binding.Resource.ProjectID, Environment: provider.Binding.Resource.Environment},
		Capability: provider.Binding.Capability.Key(), Data: domain.SanitizeResult(data)}, nil
}

func NewKubernetesRunner(credentials CredentialProvider, namespaces, registries []string) (*KubernetesRunner, error) {
	if len(namespaces) == 0 {
		return nil, fmt.Errorf("at least one allowed namespace is required")
	}
	allowed := make(map[string]struct{}, len(namespaces))
	for _, namespace := range namespaces {
		namespace = strings.TrimSpace(namespace)
		if namespace == "" || namespace == "*" {
			return nil, fmt.Errorf("wildcard or empty namespace is not allowed")
		}
		allowed[namespace] = struct{}{}
	}
	return &KubernetesRunner{credentials: credentials, allowedNamespaces: allowed, allowedRegistries: registries}, nil
}

func (r *KubernetesRunner) Execute(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	if strings.HasPrefix(task.Action.Name, "kubernetes.resource.") {
		return r.executeGenericResource(ctx, task)
	}
	name, err := requiredString(task.Parameters, "name")
	if err != nil {
		return nil, Lease{}, err
	}
	clusterScoped := isClusterScoped(task.Resource.Type)
	var namespace string
	if task.Action.Name == "namespace.dashboard_configmap.apply" {
		namespace = task.Resource.Name
		if _, ok := r.allowedNamespaces[namespace]; !ok {
			return nil, Lease{}, fmt.Errorf("namespace %q is outside connector scope", namespace)
		}
	} else if clusterScoped {
		// Cluster-scoped resources (node, namespace) have no namespace.
		// Use the first allowed namespace for token issuance; the SA's
		// ClusterRoleBinding grants cluster-scoped read access.
		for ns := range r.allowedNamespaces {
			namespace = ns
			break
		}
	} else {
		namespace, err = requiredString(task.Parameters, "namespace")
		if err != nil {
			return nil, Lease{}, err
		}
		if namespace != task.Resource.Namespace {
			return nil, Lease{}, fmt.Errorf("task parameters do not match the authorized resource")
		}
		if _, ok := r.allowedNamespaces[namespace]; !ok {
			return nil, Lease{}, fmt.Errorf("namespace %q is outside connector scope", namespace)
		}
	}
	if name != task.Resource.Name {
		return nil, Lease{}, fmt.Errorf("task parameters do not match the authorized resource")
	}
	ttl := time.Until(task.Grant.ExpiresAt)
	if ttl <= 0 {
		return nil, Lease{}, fmt.Errorf("grant has expired")
	}
	client, lease, err := r.credentials.Issue(ctx, namespace, ttl)
	if err != nil {
		return nil, Lease{}, err
	}

	switch task.Action.Name {
	case "deployment.get":
		return r.getDeployment(ctx, client, namespace, name, lease)
	case "deployment.update_image":
		return r.updateImage(ctx, client, namespace, name, task.Parameters, lease)
	case "deployment.restart":
		return r.restartDeployment(ctx, client, namespace, name, task.Parameters, lease)
	case "deployment.scale":
		return r.scaleDeployment(ctx, client, namespace, name, task.Parameters, lease)
	case "deployment.rollout_status":
		return r.rolloutStatus(ctx, client, namespace, name, lease)
	case "pod.logs.read":
		return r.readPodLogs(ctx, client, namespace, name, task.Parameters, lease)
	case "pod.get":
		return r.getPod(ctx, client, namespace, name, lease)
	case "pod.events":
		return r.getPodEvents(ctx, client, namespace, name, lease)
	case "pod.metrics":
		return r.getMetrics(ctx, client, "/apis/metrics.k8s.io/v1beta1/namespaces/"+url.PathEscape(namespace)+"/pods/"+url.PathEscape(name), "pod", lease)
	case "event.list":
		return r.listEvents(ctx, client, namespace, lease)
	// Phase 2: Workload resources
	case "statefulset.get":
		return r.getStatefulSet(ctx, client, namespace, name, lease)
	case "statefulset.restart":
		return r.restartStatefulSet(ctx, client, namespace, name, task.Parameters, lease)
	case "statefulset.scale":
		return r.scaleStatefulSet(ctx, client, namespace, name, task.Parameters, lease)
	case "statefulset.update_image":
		return r.updateStatefulSetImage(ctx, client, namespace, name, task.Parameters, lease)
	case "daemonset.get":
		return r.getDaemonSet(ctx, client, namespace, name, lease)
	case "replicaset.get":
		return r.getReplicaSet(ctx, client, namespace, name, lease)
	case "job.get":
		return r.getJob(ctx, client, namespace, name, lease)
	case "cronjob.get":
		return r.getCronJob(ctx, client, namespace, name, lease)
	case "cronjob.suspend":
		return r.setCronJobSuspended(ctx, client, namespace, name, task.Parameters, true, lease)
	case "cronjob.resume":
		return r.setCronJobSuspended(ctx, client, namespace, name, task.Parameters, false, lease)
	case "cronjob.trigger":
		return r.triggerCronJob(ctx, client, namespace, name, task, lease)
	// Phase 3: Network & config
	case "service.get":
		return r.getService(ctx, client, namespace, name, lease)
	case "endpoints.get":
		return r.getEndpoints(ctx, client, namespace, name, lease)
	case "ingress.get":
		return r.getIngress(ctx, client, namespace, name, lease)
	case "configmap.get":
		return r.getConfigMap(ctx, client, namespace, name, lease)
	case "configmap.dashboard.delete":
		return r.deleteDashboardConfigMap(ctx, client, namespace, name, lease)
	// Phase 4: Cluster health (cluster-scoped)
	case "node.get":
		return r.getNode(ctx, client, name, lease)
	case "node.metrics":
		return r.getMetrics(ctx, client, "/apis/metrics.k8s.io/v1beta1/nodes/"+url.PathEscape(name), "node", lease)
	case "namespace.list":
		return r.listNamespaces(ctx, client, lease)
	case "namespace.dashboard_configmap.apply":
		return r.applyDashboardConfigMap(ctx, client, namespace, task.Parameters, lease)
	default:
		return nil, lease, fmt.Errorf("unsupported action %q", task.Action.Name)
	}
}

func isClusterScoped(resourceType string) bool {
	return resourceType == "node" || resourceType == "namespace"
}

func (r *KubernetesRunner) getDeployment(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	deployment, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	containers := make([]map[string]string, 0, len(deployment.Spec.Template.Spec.Containers))
	for _, container := range deployment.Spec.Template.Spec.Containers {
		containers = append(containers, map[string]string{"name": container.Name, "image": container.Image})
	}
	return map[string]any{"name": deployment.Name, "namespace": deployment.Namespace,
		"resourceVersion": deployment.ResourceVersion, "generation": deployment.Generation,
		"desiredReplicas": valueOrDefault(deployment.Spec.Replicas, 1), "replicas": deployment.Status.Replicas,
		"containers": containers}, lease, nil
}

func (r *KubernetesRunner) updateImage(ctx context.Context, client kubernetes.Interface, namespace, name string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	container, err := requiredString(parameters, "container")
	if err != nil {
		return nil, lease, err
	}
	image, err := requiredString(parameters, "image")
	if err != nil {
		return nil, lease, err
	}
	if !r.imageAllowed(image) {
		return nil, lease, fmt.Errorf("image %q is outside the allowed registry scope", image)
	}
	expected, err := expectedGeneration(parameters)
	if err != nil {
		return nil, lease, err
	}
	updated, operation, err := updateDeploymentWithRetry(ctx, client, namespace, name, expected, func(next *appsv1.Deployment) (map[string]any, error) {
		index := containerIndex(next.Spec.Template.Spec.Containers, container)
		if index < 0 {
			return nil, fmt.Errorf("container %q does not exist", container)
		}
		previous := next.Spec.Template.Spec.Containers[index].Image
		next.Spec.Template.Spec.Containers[index].Image = image
		return map[string]any{"previousImage": previous}, nil
	})
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"name": updated.Name, "namespace": updated.Namespace, "container": container,
		"previousImage": operation["previousImage"], "image": image, "resourceVersion": updated.ResourceVersion,
		"generation": updated.Generation}, lease, nil
}

func (r *KubernetesRunner) rolloutStatus(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	deployment, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	complete := deployment.Status.ObservedGeneration >= deployment.Generation &&
		deployment.Status.UpdatedReplicas == desired && deployment.Status.AvailableReplicas == desired
	return map[string]any{"name": name, "namespace": namespace, "complete": complete,
		"desiredReplicas": desired, "updatedReplicas": deployment.Status.UpdatedReplicas,
		"availableReplicas": deployment.Status.AvailableReplicas}, lease, nil
}

func (r *KubernetesRunner) readPodLogs(ctx context.Context, client kubernetes.Interface, namespace, name string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	tail := int64(100)
	if value, ok := parameters["tailLines"].(float64); ok && value > 0 && value <= 1000 {
		tail = int64(value)
	}
	container, _ := parameters["container"].(string)
	logs, err := client.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{Container: container, TailLines: &tail}).DoRaw(ctx)
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"name": name, "namespace": namespace, "logs": string(logs), "tailLines": tail}, lease, nil
}

// getPod returns full pod status: container states, restart counts, conditions,
// QoS class, node name, IPs - everything an agent needs to diagnose CrashLoopBackOff,
// image pull errors, scheduling failures, etc.
func (r *KubernetesRunner) getPod(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	pod, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	containers := make([]map[string]any, 0, len(pod.Status.ContainerStatuses))
	for _, cs := range pod.Status.ContainerStatuses {
		state := "unknown"
		detail := map[string]any{}
		if cs.State.Running != nil {
			state = "running"
			detail["startedAt"] = cs.State.Running.StartedAt.Format(time.RFC3339)
		} else if cs.State.Waiting != nil {
			state = "waiting"
			detail["reason"] = cs.State.Waiting.Reason
			detail["message"] = cs.State.Waiting.Message
		} else if cs.State.Terminated != nil {
			state = "terminated"
			detail["reason"] = cs.State.Terminated.Reason
			detail["message"] = cs.State.Terminated.Message
			detail["exitCode"] = cs.State.Terminated.ExitCode
			detail["startedAt"] = cs.State.Terminated.StartedAt.Format(time.RFC3339)
			detail["finishedAt"] = cs.State.Terminated.FinishedAt.Format(time.RFC3339)
		}
		containers = append(containers, map[string]any{
			"name": cs.Name, "ready": cs.Ready, "restartCount": cs.RestartCount,
			"state": state, "stateDetail": detail, "image": cs.Image,
		})
	}
	conditions := make([]map[string]any, 0, len(pod.Status.Conditions))
	for _, c := range pod.Status.Conditions {
		conditions = append(conditions, map[string]any{
			"type": string(c.Type), "status": string(c.Status), "reason": c.Reason, "message": c.Message,
		})
	}
	return map[string]any{
		"name": pod.Name, "namespace": pod.Namespace, "nodeName": pod.Spec.NodeName,
		"podIP": pod.Status.PodIP, "hostIP": pod.Status.HostIP,
		"phase": string(pod.Status.Phase), "qosClass": string(pod.Status.QOSClass),
		"containers": containers, "conditions": conditions,
	}, lease, nil
}

// getPodEvents returns K8s events for a specific pod (image pull errors, scheduling
// failures, OOM kills, liveness probe failures).
func (r *KubernetesRunner) getPodEvents(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	selector := fmt.Sprintf("involvedObject.kind=Pod,involvedObject.name=%s,involvedObject.namespace=%s", name, namespace)
	list, err := client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: selector})
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"name": name, "namespace": namespace, "events": summarizeEvents(list.Items)}, lease, nil
}

// listEvents returns all events in a namespace - the #1 K8s troubleshooting tool
// (kubectl get events).
func (r *KubernetesRunner) listEvents(ctx context.Context, client kubernetes.Interface, namespace string, lease Lease) (map[string]any, Lease, error) {
	list, err := client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, lease, err
	}
	events := summarizeEvents(list.Items)
	return map[string]any{"namespace": namespace, "events": events, "count": len(events)}, lease, nil
}

func summarizeEvents(items []corev1.Event) []map[string]any {
	events := make([]map[string]any, 0, len(items))
	for _, e := range items {
		events = append(events, map[string]any{
			"type":    e.Type,
			"reason":  e.Reason,
			"message": e.Message,
			"involvedObject": map[string]any{
				"kind":      e.InvolvedObject.Kind,
				"name":      e.InvolvedObject.Name,
				"namespace": e.InvolvedObject.Namespace,
			},
			"lastTimestamp": e.LastTimestamp,
			"count":         e.Count,
		})
	}
	return events
}

// --- Phase 2: Workload resources ---

func (r *KubernetesRunner) getStatefulSet(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	ss, err := client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	desired := int32(0)
	if ss.Spec.Replicas != nil {
		desired = *ss.Spec.Replicas
	}
	return map[string]any{
		"name": ss.Name, "namespace": ss.Namespace,
		"resourceVersion": ss.ResourceVersion, "generation": ss.Generation,
		"desiredReplicas": desired, "replicas": ss.Status.Replicas,
		"readyReplicas": ss.Status.ReadyReplicas, "updatedReplicas": ss.Status.UpdatedReplicas,
		"updateStrategy": string(ss.Spec.UpdateStrategy.Type),
		"serviceName":    ss.Spec.ServiceName, "podManagementPolicy": string(ss.Spec.PodManagementPolicy),
		"containers": containerSummary(ss.Spec.Template.Spec.Containers),
	}, lease, nil
}

func (r *KubernetesRunner) getDaemonSet(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	ds, err := client.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{
		"name": ds.Name, "namespace": ds.Namespace,
		"desired": ds.Status.DesiredNumberScheduled, "current": ds.Status.CurrentNumberScheduled,
		"ready": ds.Status.NumberReady, "updated": ds.Status.UpdatedNumberScheduled,
		"available":      ds.Status.NumberAvailable,
		"updateStrategy": string(ds.Spec.UpdateStrategy.Type),
		"containers":     containerSummary(ds.Spec.Template.Spec.Containers),
	}, lease, nil
}

func (r *KubernetesRunner) getReplicaSet(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	rs, err := client.AppsV1().ReplicaSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	desired := int32(0)
	if rs.Spec.Replicas != nil {
		desired = *rs.Spec.Replicas
	}
	owners := make([]map[string]string, 0, len(rs.OwnerReferences))
	for _, ref := range rs.OwnerReferences {
		owners = append(owners, map[string]string{"kind": ref.Kind, "name": ref.Name})
	}
	return map[string]any{
		"name": rs.Name, "namespace": rs.Namespace,
		"desiredReplicas": desired, "replicas": rs.Status.Replicas,
		"readyReplicas": rs.Status.ReadyReplicas, "availableReplicas": rs.Status.AvailableReplicas,
		"owners": owners, "containers": containerSummary(rs.Spec.Template.Spec.Containers),
	}, lease, nil
}

func (r *KubernetesRunner) getJob(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	job, err := client.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	completions := int32(0)
	if job.Spec.Completions != nil {
		completions = *job.Spec.Completions
	}
	parallelism := int32(0)
	if job.Spec.Parallelism != nil {
		parallelism = *job.Spec.Parallelism
	}
	conditions := make([]map[string]any, 0, len(job.Status.Conditions))
	for _, c := range job.Status.Conditions {
		conditions = append(conditions, map[string]any{"type": string(c.Type), "status": string(c.Status), "reason": c.Reason, "message": c.Message})
	}
	return map[string]any{
		"name": job.Name, "namespace": job.Namespace,
		"completions": completions, "parallelism": parallelism,
		"active": job.Status.Active, "succeeded": job.Status.Succeeded, "failed": job.Status.Failed,
		"conditions": conditions, "startTime": job.Status.StartTime, "completionTime": job.Status.CompletionTime,
		"containers": containerSummary(job.Spec.Template.Spec.Containers),
	}, lease, nil
}

func (r *KubernetesRunner) getCronJob(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	cj, err := client.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	suspended := false
	if cj.Spec.Suspend != nil {
		suspended = *cj.Spec.Suspend
	}
	return map[string]any{
		"name": cj.Name, "namespace": cj.Namespace,
		"resourceVersion": cj.ResourceVersion, "generation": cj.Generation,
		"schedule": cj.Spec.Schedule, "suspend": suspended,
		"concurrencyPolicy": string(cj.Spec.ConcurrencyPolicy),
		"active":            len(cj.Status.Active), "lastScheduleTime": cj.Status.LastScheduleTime,
		"containers": containerSummary(cj.Spec.JobTemplate.Spec.Template.Spec.Containers),
	}, lease, nil
}

// --- Phase 3: Network & config ---

func (r *KubernetesRunner) getService(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	svc, err := client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	ports := make([]map[string]any, 0, len(svc.Spec.Ports))
	for _, p := range svc.Spec.Ports {
		ports = append(ports, map[string]any{"name": p.Name, "port": p.Port, "protocol": string(p.Protocol), "targetPort": p.TargetPort.IntValue()})
	}
	return map[string]any{
		"name": svc.Name, "namespace": svc.Namespace,
		"type": string(svc.Spec.Type), "clusterIP": svc.Spec.ClusterIP,
		"externalIPs": svc.Spec.ExternalIPs, "ports": ports, "selector": toStringMap(svc.Spec.Selector),
	}, lease, nil
}

func (r *KubernetesRunner) getEndpoints(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	eps, err := client.CoreV1().Endpoints(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	subsets := make([]map[string]any, 0, len(eps.Subsets))
	for _, s := range eps.Subsets {
		ready := make([]string, 0, len(s.Addresses))
		for _, a := range s.Addresses {
			ready = append(ready, a.IP)
		}
		notReady := make([]string, 0, len(s.NotReadyAddresses))
		for _, a := range s.NotReadyAddresses {
			notReady = append(notReady, a.IP)
		}
		ports := make([]map[string]any, 0, len(s.Ports))
		for _, p := range s.Ports {
			ports = append(ports, map[string]any{"port": p.Port, "protocol": string(p.Protocol), "name": p.Name})
		}
		subsets = append(subsets, map[string]any{"readyAddresses": ready, "notReadyAddresses": notReady, "ports": ports})
	}
	return map[string]any{"name": eps.Name, "namespace": eps.Namespace, "subsets": subsets}, lease, nil
}

func (r *KubernetesRunner) getIngress(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	ing, err := client.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	rules := make([]map[string]any, 0, len(ing.Spec.Rules))
	for _, rule := range ing.Spec.Rules {
		paths := make([]map[string]any, 0)
		if rule.HTTP != nil {
			for _, path := range rule.HTTP.Paths {
				backend := map[string]any{}
				if path.Backend.Service != nil {
					backend["serviceName"] = path.Backend.Service.Name
					backend["port"] = path.Backend.Service.Port.Number
				}
				paths = append(paths, map[string]any{"path": path.Path, "pathType": string(*path.PathType), "backend": backend})
			}
		}
		rules = append(rules, map[string]any{"host": rule.Host, "paths": paths})
	}
	tls := make([]map[string]any, 0, len(ing.Spec.TLS))
	for _, t := range ing.Spec.TLS {
		tls = append(tls, map[string]any{"hosts": t.Hosts, "secretName": t.SecretName})
	}
	lbIngress := make([]map[string]string, 0, len(ing.Status.LoadBalancer.Ingress))
	for _, i := range ing.Status.LoadBalancer.Ingress {
		entry := map[string]string{}
		if i.IP != "" {
			entry["ip"] = i.IP
		}
		if i.Hostname != "" {
			entry["hostname"] = i.Hostname
		}
		lbIngress = append(lbIngress, entry)
	}
	return map[string]any{"name": ing.Name, "namespace": ing.Namespace, "rules": rules, "tls": tls, "loadBalancer": lbIngress}, lease, nil
}

func (r *KubernetesRunner) getConfigMap(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	dataKeys := make([]string, 0, len(cm.Data))
	for k := range cm.Data {
		dataKeys = append(dataKeys, k)
	}
	binaryKeys := make([]string, 0, len(cm.BinaryData))
	for k := range cm.BinaryData {
		binaryKeys = append(binaryKeys, k)
	}
	return map[string]any{
		"name": cm.Name, "namespace": cm.Namespace,
		"data": cm.Data, "dataKeys": dataKeys,
		"binaryDataKeys": binaryKeys,
	}, lease, nil
}

func (r *KubernetesRunner) applyDashboardConfigMap(ctx context.Context, client kubernetes.Interface, namespace string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	name, err := requiredString(parameters, "configMapName")
	if err != nil || !dashboardConfigMapNamePattern.MatchString(name) {
		return nil, lease, fmt.Errorf("configMapName must use the grafana-dashboard- prefix and be DNS compatible")
	}
	key, err := requiredString(parameters, "dashboardKey")
	if err != nil || !dashboardConfigMapKeyPattern.MatchString(key) {
		return nil, lease, fmt.Errorf("dashboardKey must be a safe .json filename")
	}
	dashboard, ok := parameters["dashboard"].(map[string]any)
	if !ok || len(dashboard) == 0 {
		return nil, lease, fmt.Errorf("dashboard must be a non-empty object")
	}
	payload, err := json.Marshal(dashboard)
	if err != nil || len(payload) > 900<<10 {
		return nil, lease, fmt.Errorf("dashboard JSON must be valid and at most 900 KiB")
	}
	configMaps := client.CoreV1().ConfigMaps(namespace)
	current, err := configMaps.Get(ctx, name, metav1.GetOptions{})
	created := false
	if apierrors.IsNotFound(err) {
		created = true
		current = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	} else if err != nil {
		return nil, lease, err
	}
	if current.Labels == nil {
		current.Labels = map[string]string{}
	}
	current.Labels["grafana_dashboard"] = "1"
	current.Labels["app.kubernetes.io/managed-by"] = "pam"
	current.Data = map[string]string{key: string(payload)}
	if created {
		current, err = configMaps.Create(ctx, current, metav1.CreateOptions{})
	} else {
		current, err = configMaps.Update(ctx, current, metav1.UpdateOptions{})
	}
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"name": current.Name, "namespace": namespace, "dashboardKey": key, "created": created, "resourceVersion": current.ResourceVersion}, lease, nil
}

func (r *KubernetesRunner) deleteDashboardConfigMap(ctx context.Context, client kubernetes.Interface, namespace, name string, lease Lease) (map[string]any, Lease, error) {
	if !dashboardConfigMapNamePattern.MatchString(name) {
		return nil, lease, fmt.Errorf("only grafana-dashboard-* ConfigMaps can be deleted by this action")
	}
	if err := client.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return nil, lease, err
	}
	return map[string]any{"name": name, "namespace": namespace, "deleted": true}, lease, nil
}

func (r *KubernetesRunner) getMetrics(ctx context.Context, client kubernetes.Interface, path, kind string, lease Lease) (map[string]any, Lease, error) {
	raw, err := client.CoreV1().RESTClient().Get().AbsPath(path).DoRaw(ctx)
	if err != nil {
		return nil, lease, err
	}
	if len(raw) > 1<<20 {
		return nil, lease, fmt.Errorf("metrics response exceeds 1 MiB")
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, lease, err
	}
	return map[string]any{"kind": kind, "metrics": result}, lease, nil
}

// --- Phase 4: Cluster health (cluster-scoped) ---

func (r *KubernetesRunner) getNode(ctx context.Context, client kubernetes.Interface, name string, lease Lease) (map[string]any, Lease, error) {
	node, err := client.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	conditions := make([]map[string]any, 0, len(node.Status.Conditions))
	for _, c := range node.Status.Conditions {
		conditions = append(conditions, map[string]any{"type": string(c.Type), "status": string(c.Status), "reason": c.Reason, "message": c.Message})
	}
	addresses := make([]map[string]string, 0, len(node.Status.Addresses))
	for _, a := range node.Status.Addresses {
		addresses = append(addresses, map[string]string{"type": string(a.Type), "address": a.Address})
	}
	taints := make([]map[string]any, 0, len(node.Spec.Taints))
	for _, t := range node.Spec.Taints {
		taints = append(taints, map[string]any{"key": t.Key, "value": t.Value, "effect": string(t.Effect)})
	}
	return map[string]any{
		"name": node.Name, "conditions": conditions, "addresses": addresses, "taints": taints,
		"capacity": node.Status.Capacity, "allocatable": node.Status.Allocatable,
		"nodeInfo": map[string]string{
			"kubeletVersion": node.Status.NodeInfo.KubeletVersion,
			"osImage":        node.Status.NodeInfo.OSImage,
			"architecture":   node.Status.NodeInfo.Architecture,
		},
	}, lease, nil
}

func (r *KubernetesRunner) listNamespaces(ctx context.Context, client kubernetes.Interface, lease Lease) (map[string]any, Lease, error) {
	list, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, lease, err
	}
	namespaces := make([]map[string]any, 0, len(list.Items))
	for _, ns := range list.Items {
		if _, allowed := r.allowedNamespaces[ns.Name]; !allowed {
			continue
		}
		namespaces = append(namespaces, map[string]any{"name": ns.Name, "status": string(ns.Status.Phase), "creationTimestamp": ns.CreationTimestamp})
	}
	return map[string]any{"namespaces": namespaces, "count": len(namespaces)}, lease, nil
}

// containerSummary extracts name + image from a container list.
func containerSummary(containers []corev1.Container) []map[string]string {
	result := make([]map[string]string, 0, len(containers))
	for _, c := range containers {
		result = append(result, map[string]string{"name": c.Name, "image": c.Image})
	}
	return result
}

func (r *KubernetesRunner) imageAllowed(image string) bool {
	if len(r.allowedRegistries) == 0 {
		return false
	}
	for _, prefix := range r.allowedRegistries {
		prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "*")
		if prefix != "" && strings.HasPrefix(image, prefix) {
			return true
		}
	}
	return false
}

// ListDeployments walks every allowed namespace and returns the deployments
// visible to the bootstrap identity (bound to warden-resource-discoverer). The
// returned slice is what gets POSTed to /api/v1/resources/sync.
func (r *KubernetesRunner) ListDeployments(ctx context.Context) []DiscoveredResource {
	client := r.credentials.BootstrapClient()
	if client == nil {
		return nil
	}
	var discovered []DiscoveredResource
	for ns := range r.allowedNamespaces {
		discovered = append(discovered, DiscoveredResource{
			ExternalID: "namespace/" + ns, Name: ns, Namespace: "", ResourceType: "namespace",
			Labels: map[string]any{}, Annotations: map[string]any{}, Metadata: map[string]any{"scope": "allowed"},
		})
		list, err := client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for i := range list.Items {
			d := list.Items[i]
			containers := make([]map[string]string, 0, len(d.Spec.Template.Spec.Containers))
			for _, container := range d.Spec.Template.Spec.Containers {
				containers = append(containers, map[string]string{"name": container.Name, "image": container.Image})
			}
			discovered = append(discovered, DiscoveredResource{
				ExternalID:   d.Namespace + "/" + d.Name,
				Name:         d.Name,
				Namespace:    d.Namespace,
				ResourceType: "deployment",
				Labels:       toStringMap(d.Labels),
				Annotations:  toStringMap(d.Annotations),
				Metadata: map[string]any{
					"resourceVersion": d.ResourceVersion, "generation": d.Generation,
					"replicas": valueOrDefault(d.Spec.Replicas, 1), "containers": containers,
				},
			})
		}
		pods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for i := range pods.Items {
			pod := pods.Items[i]
			containers := make([]map[string]string, 0, len(pod.Spec.Containers))
			for _, container := range pod.Spec.Containers {
				containers = append(containers, map[string]string{"name": container.Name, "image": container.Image})
			}
			discovered = append(discovered, DiscoveredResource{
				ExternalID:   pod.Namespace + "/" + pod.Name,
				Name:         pod.Name,
				Namespace:    pod.Namespace,
				ResourceType: "pod",
				Labels:       toStringMap(pod.Labels),
				Annotations:  toStringMap(pod.Annotations),
				Metadata:     map[string]any{"containers": containers},
			})
		}
		// Discover a synthetic "event" collection resource per namespace so agents
		// can invoke event.list to retrieve namespace-level K8s events.
		discovered = append(discovered, DiscoveredResource{
			ExternalID:   ns + "/events",
			Name:         ns,
			Namespace:    ns,
			ResourceType: "event",
			Labels:       map[string]any{},
			Annotations:  map[string]any{},
			Metadata:     map[string]any{"collection": "events"},
		})
		// Phase 2: Workload resources
		if sts, err := client.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range sts.Items {
				ss := sts.Items[i]
				discovered = append(discovered, DiscoveredResource{
					ExternalID: ss.Namespace + "/" + ss.Name, Name: ss.Name, Namespace: ss.Namespace, ResourceType: "statefulset",
					Labels: toStringMap(ss.Labels), Annotations: toStringMap(ss.Annotations),
					Metadata: map[string]any{"resourceVersion": ss.ResourceVersion, "generation": ss.Generation,
						"replicas": valueOrDefault(ss.Spec.Replicas, 1), "readyReplicas": ss.Status.ReadyReplicas,
						"containers": containerSummary(ss.Spec.Template.Spec.Containers)},
				})
			}
		}
		if dss, err := client.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range dss.Items {
				ds := dss.Items[i]
				discovered = append(discovered, DiscoveredResource{
					ExternalID: ds.Namespace + "/" + ds.Name, Name: ds.Name, Namespace: ds.Namespace, ResourceType: "daemonset",
					Labels: toStringMap(ds.Labels), Annotations: toStringMap(ds.Annotations),
					Metadata: map[string]any{"desired": ds.Status.DesiredNumberScheduled, "ready": ds.Status.NumberReady, "containers": containerSummary(ds.Spec.Template.Spec.Containers)},
				})
			}
		}
		if rss, err := client.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range rss.Items {
				rs := rss.Items[i]
				owner := ""
				for _, ref := range rs.OwnerReferences {
					if ref.Kind == "Deployment" {
						owner = ref.Name
						break
					}
				}
				discovered = append(discovered, DiscoveredResource{
					ExternalID: rs.Namespace + "/" + rs.Name, Name: rs.Name, Namespace: rs.Namespace, ResourceType: "replicaset",
					Labels: toStringMap(rs.Labels), Annotations: toStringMap(rs.Annotations),
					Metadata: map[string]any{"replicas": rs.Status.Replicas, "readyReplicas": rs.Status.ReadyReplicas, "owner": owner, "containers": containerSummary(rs.Spec.Template.Spec.Containers)},
				})
			}
		}
		if jobs, err := client.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range jobs.Items {
				job := jobs.Items[i]
				discovered = append(discovered, DiscoveredResource{
					ExternalID: job.Namespace + "/" + job.Name, Name: job.Name, Namespace: job.Namespace, ResourceType: "job",
					Labels: toStringMap(job.Labels), Annotations: toStringMap(job.Annotations),
					Metadata: map[string]any{"succeeded": job.Status.Succeeded, "failed": job.Status.Failed, "containers": containerSummary(job.Spec.Template.Spec.Containers)},
				})
			}
		}
		if cjs, err := client.BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range cjs.Items {
				cj := cjs.Items[i]
				discovered = append(discovered, DiscoveredResource{
					ExternalID: cj.Namespace + "/" + cj.Name, Name: cj.Name, Namespace: cj.Namespace, ResourceType: "cronjob",
					Labels: toStringMap(cj.Labels), Annotations: toStringMap(cj.Annotations),
					Metadata: map[string]any{"resourceVersion": cj.ResourceVersion, "generation": cj.Generation, "schedule": cj.Spec.Schedule,
						"suspend":    cj.Spec.Suspend != nil && *cj.Spec.Suspend,
						"containers": containerSummary(cj.Spec.JobTemplate.Spec.Template.Spec.Containers)},
				})
			}
		}
		// Phase 3: Network & config
		if svcs, err := client.CoreV1().Services(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range svcs.Items {
				svc := svcs.Items[i]
				discovered = append(discovered, DiscoveredResource{
					ExternalID: svc.Namespace + "/" + svc.Name, Name: svc.Name, Namespace: svc.Namespace, ResourceType: "service",
					Labels: toStringMap(svc.Labels), Annotations: toStringMap(svc.Annotations),
					Metadata: map[string]any{"type": string(svc.Spec.Type), "clusterIP": svc.Spec.ClusterIP, "ports": len(svc.Spec.Ports)},
				})
			}
		}
		if eps, err := client.CoreV1().Endpoints(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range eps.Items {
				ep := eps.Items[i]
				readyCount := 0
				for _, s := range ep.Subsets {
					readyCount += len(s.Addresses)
				}
				discovered = append(discovered, DiscoveredResource{
					ExternalID: ep.Namespace + "/" + ep.Name, Name: ep.Name, Namespace: ep.Namespace, ResourceType: "endpoints",
					Labels: toStringMap(ep.Labels), Annotations: toStringMap(ep.Annotations),
					Metadata: map[string]any{"readyAddresses": readyCount},
				})
			}
		}
		if ings, err := client.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range ings.Items {
				ing := ings.Items[i]
				hosts := make([]string, 0, len(ing.Spec.Rules))
				for _, rule := range ing.Spec.Rules {
					hosts = append(hosts, rule.Host)
				}
				discovered = append(discovered, DiscoveredResource{
					ExternalID: ing.Namespace + "/" + ing.Name, Name: ing.Name, Namespace: ing.Namespace, ResourceType: "ingress",
					Labels: toStringMap(ing.Labels), Annotations: toStringMap(ing.Annotations),
					Metadata: map[string]any{"hosts": hosts},
				})
			}
		}
		if policies, err := client.NetworkingV1().NetworkPolicies(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range policies.Items {
				policy := policies.Items[i]
				discovered = append(discovered, DiscoveredResource{
					ExternalID: policy.Namespace + "/" + policy.Name, Name: policy.Name, Namespace: policy.Namespace, ResourceType: "networkpolicy",
					Labels: toStringMap(policy.Labels), Annotations: toStringMap(policy.Annotations),
					Metadata: map[string]any{"resourceVersion": policy.ResourceVersion, "generation": policy.Generation, "policyTypes": policy.Spec.PolicyTypes},
				})
			}
		}
		if budgets, err := client.PolicyV1().PodDisruptionBudgets(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range budgets.Items {
				budget := budgets.Items[i]
				discovered = append(discovered, DiscoveredResource{
					ExternalID: budget.Namespace + "/" + budget.Name, Name: budget.Name, Namespace: budget.Namespace, ResourceType: "poddisruptionbudget",
					Labels: toStringMap(budget.Labels), Annotations: toStringMap(budget.Annotations),
					Metadata: map[string]any{"resourceVersion": budget.ResourceVersion, "generation": budget.Generation, "currentHealthy": budget.Status.CurrentHealthy, "desiredHealthy": budget.Status.DesiredHealthy},
				})
			}
		}
		if cms, err := client.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range cms.Items {
				cm := cms.Items[i]
				discovered = append(discovered, DiscoveredResource{
					ExternalID: cm.Namespace + "/" + cm.Name, Name: cm.Name, Namespace: cm.Namespace, ResourceType: "configmap",
					Labels: toStringMap(cm.Labels), Annotations: toStringMap(cm.Annotations),
					Metadata: map[string]any{"dataKeys": len(cm.Data)},
				})
			}
		}
	}
	// Phase 4: Cluster-scoped resources (discovered once, not per-namespace).
	if nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		for i := range nodes.Items {
			node := nodes.Items[i]
			ready := "Unknown"
			for _, c := range node.Status.Conditions {
				if c.Type == "Ready" {
					ready = string(c.Status)
					break
				}
			}
			discovered = append(discovered, DiscoveredResource{
				ExternalID: "node/" + node.Name, Name: node.Name, Namespace: "", ResourceType: "node",
				Labels: toStringMap(node.Labels), Annotations: toStringMap(node.Annotations),
				Metadata: map[string]any{"ready": ready, "kubeletVersion": node.Status.NodeInfo.KubeletVersion},
			})
		}
	}
	return discovered
}

func (r *KubernetesRunner) ListResources(ctx context.Context) []DiscoveredResource {
	return r.ListDeployments(ctx)
}

// SnapshotResource performs a targeted reread for change-safety verification
// instead of rediscovering every resource in every allowed namespace. It
// reuses the bootstrap identity (warden-resource-discoverer) so no short-lived
// execution token needs to be minted. The metadata returned matches exactly
// what ListDeployments would produce for the same resource.
func (r *KubernetesRunner) SnapshotResource(ctx context.Context, resource domain.TaskResource) (DiscoveredResource, bool) {
	client := r.credentials.BootstrapClient()
	if client == nil {
		return findDiscoveredResource(r.ListResources(ctx), resource)
	}
	if !isClusterScoped(resource.Type) && !r.namespaceAllowed(resource.Namespace) {
		return DiscoveredResource{}, false
	}
	switch resource.Type {
	case "deployment":
		d, err := client.AppsV1().Deployments(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: d.Namespace + "/" + d.Name, Name: d.Name, Namespace: d.Namespace, ResourceType: "deployment",
			Labels: toStringMap(d.Labels), Annotations: toStringMap(d.Annotations),
			Metadata: map[string]any{
				"resourceVersion": d.ResourceVersion, "generation": d.Generation,
				"replicas": valueOrDefault(d.Spec.Replicas, 1), "containers": containerSummary(d.Spec.Template.Spec.Containers),
			},
		}, true
	case "pod":
		pod, err := client.CoreV1().Pods(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: pod.Namespace + "/" + pod.Name, Name: pod.Name, Namespace: pod.Namespace, ResourceType: "pod",
			Labels: toStringMap(pod.Labels), Annotations: toStringMap(pod.Annotations),
			Metadata: map[string]any{"containers": containerSummary(pod.Spec.Containers)},
		}, true
	case "statefulset":
		ss, err := client.AppsV1().StatefulSets(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: ss.Namespace + "/" + ss.Name, Name: ss.Name, Namespace: ss.Namespace, ResourceType: "statefulset",
			Labels: toStringMap(ss.Labels), Annotations: toStringMap(ss.Annotations),
			Metadata: map[string]any{
				"resourceVersion": ss.ResourceVersion, "generation": ss.Generation,
				"replicas": valueOrDefault(ss.Spec.Replicas, 1), "readyReplicas": ss.Status.ReadyReplicas,
				"containers": containerSummary(ss.Spec.Template.Spec.Containers),
			},
		}, true
	case "daemonset":
		ds, err := client.AppsV1().DaemonSets(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: ds.Namespace + "/" + ds.Name, Name: ds.Name, Namespace: ds.Namespace, ResourceType: "daemonset",
			Labels: toStringMap(ds.Labels), Annotations: toStringMap(ds.Annotations),
			Metadata: map[string]any{
				"desired": ds.Status.DesiredNumberScheduled, "ready": ds.Status.NumberReady,
				"containers": containerSummary(ds.Spec.Template.Spec.Containers),
			},
		}, true
	case "replicaset":
		rs, err := client.AppsV1().ReplicaSets(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		owner := ""
		for _, ref := range rs.OwnerReferences {
			if ref.Kind == "Deployment" {
				owner = ref.Name
				break
			}
		}
		return DiscoveredResource{
			ExternalID: rs.Namespace + "/" + rs.Name, Name: rs.Name, Namespace: rs.Namespace, ResourceType: "replicaset",
			Labels: toStringMap(rs.Labels), Annotations: toStringMap(rs.Annotations),
			Metadata: map[string]any{
				"replicas": rs.Status.Replicas, "readyReplicas": rs.Status.ReadyReplicas,
				"owner": owner, "containers": containerSummary(rs.Spec.Template.Spec.Containers),
			},
		}, true
	case "job":
		job, err := client.BatchV1().Jobs(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: job.Namespace + "/" + job.Name, Name: job.Name, Namespace: job.Namespace, ResourceType: "job",
			Labels: toStringMap(job.Labels), Annotations: toStringMap(job.Annotations),
			Metadata: map[string]any{
				"succeeded": job.Status.Succeeded, "failed": job.Status.Failed,
				"containers": containerSummary(job.Spec.Template.Spec.Containers),
			},
		}, true
	case "cronjob":
		cj, err := client.BatchV1().CronJobs(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: cj.Namespace + "/" + cj.Name, Name: cj.Name, Namespace: cj.Namespace, ResourceType: "cronjob",
			Labels: toStringMap(cj.Labels), Annotations: toStringMap(cj.Annotations),
			Metadata: map[string]any{
				"resourceVersion": cj.ResourceVersion, "generation": cj.Generation,
				"schedule": cj.Spec.Schedule, "suspend": cj.Spec.Suspend != nil && *cj.Spec.Suspend,
				"containers": containerSummary(cj.Spec.JobTemplate.Spec.Template.Spec.Containers),
			},
		}, true
	case "service":
		svc, err := client.CoreV1().Services(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: svc.Namespace + "/" + svc.Name, Name: svc.Name, Namespace: svc.Namespace, ResourceType: "service",
			Labels: toStringMap(svc.Labels), Annotations: toStringMap(svc.Annotations),
			Metadata: map[string]any{"type": string(svc.Spec.Type), "clusterIP": svc.Spec.ClusterIP, "ports": len(svc.Spec.Ports)},
		}, true
	case "endpoints":
		eps, err := client.CoreV1().Endpoints(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		readyCount := 0
		for _, s := range eps.Subsets {
			readyCount += len(s.Addresses)
		}
		return DiscoveredResource{
			ExternalID: eps.Namespace + "/" + eps.Name, Name: eps.Name, Namespace: eps.Namespace, ResourceType: "endpoints",
			Labels: toStringMap(eps.Labels), Annotations: toStringMap(eps.Annotations),
			Metadata: map[string]any{"readyAddresses": readyCount},
		}, true
	case "ingress":
		ing, err := client.NetworkingV1().Ingresses(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		hosts := make([]string, 0, len(ing.Spec.Rules))
		for _, rule := range ing.Spec.Rules {
			hosts = append(hosts, rule.Host)
		}
		return DiscoveredResource{
			ExternalID: ing.Namespace + "/" + ing.Name, Name: ing.Name, Namespace: ing.Namespace, ResourceType: "ingress",
			Labels: toStringMap(ing.Labels), Annotations: toStringMap(ing.Annotations),
			Metadata: map[string]any{"hosts": hosts},
		}, true
	case "networkpolicy":
		policy, err := client.NetworkingV1().NetworkPolicies(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: policy.Namespace + "/" + policy.Name, Name: policy.Name, Namespace: policy.Namespace, ResourceType: "networkpolicy",
			Labels: toStringMap(policy.Labels), Annotations: toStringMap(policy.Annotations),
			Metadata: map[string]any{"resourceVersion": policy.ResourceVersion, "generation": policy.Generation, "policyTypes": policy.Spec.PolicyTypes},
		}, true
	case "poddisruptionbudget":
		budget, err := client.PolicyV1().PodDisruptionBudgets(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: budget.Namespace + "/" + budget.Name, Name: budget.Name, Namespace: budget.Namespace, ResourceType: "poddisruptionbudget",
			Labels: toStringMap(budget.Labels), Annotations: toStringMap(budget.Annotations),
			Metadata: map[string]any{
				"resourceVersion": budget.ResourceVersion, "generation": budget.Generation,
				"currentHealthy": budget.Status.CurrentHealthy, "desiredHealthy": budget.Status.DesiredHealthy,
			},
		}, true
	case "configmap":
		cm, err := client.CoreV1().ConfigMaps(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: cm.Namespace + "/" + cm.Name, Name: cm.Name, Namespace: cm.Namespace, ResourceType: "configmap",
			Labels: toStringMap(cm.Labels), Annotations: toStringMap(cm.Annotations),
			Metadata: map[string]any{"dataKeys": len(cm.Data)},
		}, true
	case "node":
		node, err := client.CoreV1().Nodes().Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		ready := "Unknown"
		for _, c := range node.Status.Conditions {
			if c.Type == "Ready" {
				ready = string(c.Status)
				break
			}
		}
		return DiscoveredResource{
			ExternalID: "node/" + node.Name, Name: node.Name, Namespace: "", ResourceType: "node",
			Labels: toStringMap(node.Labels), Annotations: toStringMap(node.Annotations),
			Metadata: map[string]any{"ready": ready, "kubeletVersion": node.Status.NodeInfo.KubeletVersion},
		}, true
	case "namespace":
		ns, err := client.CoreV1().Namespaces().Get(ctx, resource.Name, metav1.GetOptions{})
		if err != nil {
			return DiscoveredResource{}, false
		}
		if !r.namespaceAllowed(ns.Name) {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{
			ExternalID: "namespace/" + ns.Name, Name: ns.Name, Namespace: "", ResourceType: "namespace",
			Labels: map[string]any{}, Annotations: map[string]any{}, Metadata: map[string]any{"scope": "allowed"},
		}, true
	case "event":
		return DiscoveredResource{
			ExternalID: resource.Namespace + "/events", Name: resource.Namespace, Namespace: resource.Namespace, ResourceType: "event",
			Labels: map[string]any{}, Annotations: map[string]any{}, Metadata: map[string]any{"collection": "events"},
		}, true
	default:
		return findDiscoveredResource(r.ListResources(ctx), resource)
	}
}

func (r *KubernetesRunner) namespaceAllowed(namespace string) bool {
	_, ok := r.allowedNamespaces[namespace]
	return ok
}

// toStringMap converts a map[string]string (K8s labels/annotations) to the
// map[string]any shape expected by the API and DB JSONB columns.
func toStringMap(in map[string]string) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func requiredString(parameters map[string]any, key string) (string, error) {
	value, ok := parameters[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("parameter %q must be a non-empty string", key)
	}
	return value, nil
}

func IsUnknownOutcome(action string, err error) bool {
	return ((isKubernetesMutation(action) || isLinuxLifecycleAction(action)) && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))) ||
		errors.Is(err, errHuaweiCreatePartial) ||
		errors.Is(err, errH3CChangeOutcomeUnknown) ||
		IsJenkinsUnknownOutcome(action, err) ||
		IsGrafanaUnknownOutcome(action, err)
}

func isLinuxLifecycleAction(action string) bool {
	switch action {
	case "systemd.service.start", "systemd.service.stop", "systemd.service.restart",
		"docker.container.start", "docker.container.stop", "docker.container.restart":
		return true
	default:
		return false
	}
}

func isKubernetesMutation(action string) bool {
	switch action {
	case "deployment.update_image", "deployment.restart", "deployment.scale",
		"statefulset.update_image", "statefulset.restart", "statefulset.scale",
		"cronjob.suspend", "cronjob.resume", "cronjob.trigger",
		"kubernetes.resource.create", "kubernetes.resource.update", "kubernetes.resource.delete":
		return true
	default:
		return false
	}
}
