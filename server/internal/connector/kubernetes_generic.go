package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

const genericExecutionAnnotation = "warden.example.com/execution-id"
const genericManifestMaxBytes = 1 << 20

type genericKindSpec struct {
	APIVersion   string
	Kind         string
	ResourceType string
	GVR          schema.GroupVersionResource
}

var genericKubernetesKinds = map[string]genericKindSpec{
	"v1/ConfigMap":                       {APIVersion: "v1", Kind: "ConfigMap", ResourceType: "configmap", GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}},
	"v1/Service":                         {APIVersion: "v1", Kind: "Service", ResourceType: "service", GVR: schema.GroupVersionResource{Version: "v1", Resource: "services"}},
	"apps/v1/Deployment":                 {APIVersion: "apps/v1", Kind: "Deployment", ResourceType: "deployment", GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}},
	"apps/v1/StatefulSet":                {APIVersion: "apps/v1", Kind: "StatefulSet", ResourceType: "statefulset", GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}},
	"apps/v1/DaemonSet":                  {APIVersion: "apps/v1", Kind: "DaemonSet", ResourceType: "daemonset", GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}},
	"batch/v1/Job":                       {APIVersion: "batch/v1", Kind: "Job", ResourceType: "job", GVR: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}},
	"batch/v1/CronJob":                   {APIVersion: "batch/v1", Kind: "CronJob", ResourceType: "cronjob", GVR: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}},
	"networking.k8s.io/v1/Ingress":       {APIVersion: "networking.k8s.io/v1", Kind: "Ingress", ResourceType: "ingress", GVR: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}},
	"networking.k8s.io/v1/NetworkPolicy": {APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy", ResourceType: "networkpolicy", GVR: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}},
	"policy/v1/PodDisruptionBudget":      {APIVersion: "policy/v1", Kind: "PodDisruptionBudget", ResourceType: "poddisruptionbudget", GVR: schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"}},
}

func (r *KubernetesRunner) executeGenericResource(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	provider, ok := r.credentials.(DynamicCredentialProvider)
	if !ok {
		return nil, Lease{}, fmt.Errorf("generic Kubernetes CRUD is unavailable for this credential provider")
	}
	namespace, err := requiredString(task.Parameters, "namespace")
	if err != nil {
		return nil, Lease{}, err
	}
	if _, ok := r.allowedNamespaces[namespace]; !ok {
		return nil, Lease{}, fmt.Errorf("namespace %q is outside connector scope", namespace)
	}
	if task.Action.Name == "kubernetes.resource.create" || task.Action.Name == "kubernetes.resource.list" ||
		(task.Action.Name == "kubernetes.resource.preview" && stringParameter(task.Parameters, "operation") == "create") {
		if task.Resource.Type != "namespace" || task.Resource.Name != namespace {
			return nil, Lease{}, fmt.Errorf("generic create/list must be bound to the authorized namespace resource")
		}
	} else if task.Resource.Namespace != namespace {
		return nil, Lease{}, fmt.Errorf("task parameters do not match the authorized resource")
	}
	ttl := time.Until(task.Grant.ExpiresAt)
	if ttl <= 0 {
		return nil, Lease{}, fmt.Errorf("grant has expired")
	}
	client, lease, err := provider.IssueDynamic(ctx, namespace, ttl)
	if err != nil {
		return nil, Lease{}, err
	}
	spec, err := genericKindFromParameters(task.Parameters)
	if err != nil {
		return nil, lease, err
	}
	resource := client.Resource(spec.GVR).Namespace(namespace)
	switch task.Action.Name {
	case "kubernetes.resource.get":
		return r.genericGet(ctx, resource, task, spec, lease)
	case "kubernetes.resource.list":
		return r.genericList(ctx, resource, task.Parameters, spec, lease)
	case "kubernetes.resource.preview":
		return r.genericPreview(ctx, resource, task, spec, lease)
	case "kubernetes.resource.create":
		return r.genericCreate(ctx, resource, task, spec, lease)
	case "kubernetes.resource.update":
		return r.genericUpdate(ctx, resource, task, spec, lease)
	case "kubernetes.resource.delete":
		return r.genericDelete(ctx, resource, task, spec, lease)
	default:
		return nil, lease, fmt.Errorf("unsupported generic Kubernetes action %q", task.Action.Name)
	}
}

type dynamicResource interface {
	Get(context.Context, string, metav1.GetOptions, ...string) (*unstructured.Unstructured, error)
	List(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error)
	Create(context.Context, *unstructured.Unstructured, metav1.CreateOptions, ...string) (*unstructured.Unstructured, error)
	Update(context.Context, *unstructured.Unstructured, metav1.UpdateOptions, ...string) (*unstructured.Unstructured, error)
	Delete(context.Context, string, metav1.DeleteOptions, ...string) error
}

func (r *KubernetesRunner) genericGet(ctx context.Context, resource dynamicResource, task domain.TaskEnvelope, spec genericKindSpec, lease Lease) (map[string]any, Lease, error) {
	name, err := genericBoundName(task, spec)
	if err != nil {
		return nil, lease, err
	}
	object, err := resource.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	return genericObjectResult(object, spec), lease, nil
}

func (r *KubernetesRunner) genericList(ctx context.Context, resource dynamicResource, parameters map[string]any, spec genericKindSpec, lease Lease) (map[string]any, Lease, error) {
	limit := int64(100)
	if value, ok := numericParameter(parameters["limit"]); ok {
		if value < 1 || value > 200 {
			return nil, lease, fmt.Errorf("parameter %q must be between 1 and 200", "limit")
		}
		limit = int64(value)
	}
	list, err := resource.List(ctx, metav1.ListOptions{LabelSelector: stringParameter(parameters, "labelSelector"), FieldSelector: stringParameter(parameters, "fieldSelector"), Limit: limit})
	if err != nil {
		return nil, lease, err
	}
	items := make([]map[string]any, 0, len(list.Items))
	for i := range list.Items {
		items = append(items, genericObjectResult(&list.Items[i], spec))
	}
	return map[string]any{"apiVersion": spec.APIVersion, "kind": spec.Kind, "count": len(items), "items": items, "continue": list.GetContinue()}, lease, nil
}

func (r *KubernetesRunner) genericPreview(ctx context.Context, resource dynamicResource, task domain.TaskEnvelope, spec genericKindSpec, lease Lease) (map[string]any, Lease, error) {
	operation := stringParameter(task.Parameters, "operation")
	object, err := genericManifest(task.Parameters, spec, stringParameter(task.Parameters, "namespace"))
	if err != nil {
		return nil, lease, err
	}
	var before *unstructured.Unstructured
	var after *unstructured.Unstructured
	switch operation {
	case "create":
		annotateGenericExecution(object, task.ExecutionID)
		after, err = resource.Create(ctx, object, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "warden-control"})
	case "update":
		name, boundErr := genericBoundName(task, spec)
		if boundErr != nil {
			return nil, lease, boundErr
		}
		before, err = resource.Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			err = prepareGenericUpdate(object, before, task.Parameters, task.ExecutionID)
		}
		if err == nil {
			after, err = resource.Update(ctx, object, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "warden-control"})
		}
	default:
		return nil, lease, fmt.Errorf("parameter %q must be create or update", "operation")
	}
	if err != nil {
		return nil, lease, err
	}
	return genericPreviewResult(operation, before, after, spec), lease, nil
}

func (r *KubernetesRunner) genericCreate(ctx context.Context, resource dynamicResource, task domain.TaskEnvelope, spec genericKindSpec, lease Lease) (map[string]any, Lease, error) {
	object, err := genericManifest(task.Parameters, spec, stringParameter(task.Parameters, "namespace"))
	if err != nil {
		return nil, lease, err
	}
	annotateGenericExecution(object, task.ExecutionID)
	dryRun, err := resource.Create(ctx, object.DeepCopy(), metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "warden-control"})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			existing, getErr := resource.Get(ctx, object.GetName(), metav1.GetOptions{})
			if getErr == nil && existing.GetAnnotations()[genericExecutionAnnotation] == task.ExecutionID {
				result := genericObjectResult(existing, spec)
				result["idempotentReplay"] = true
				return result, lease, nil
			}
		}
		return nil, lease, err
	}
	if err := requireGenericPreviewHash(task.Parameters, genericPreviewResult("create", nil, dryRun, spec)); err != nil {
		return nil, lease, err
	}
	created, err := resource.Create(ctx, object, metav1.CreateOptions{FieldManager: "warden-control"})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			existing, getErr := resource.Get(ctx, object.GetName(), metav1.GetOptions{})
			if getErr == nil && existing.GetAnnotations()[genericExecutionAnnotation] == task.ExecutionID {
				result := genericObjectResult(existing, spec)
				result["idempotentReplay"] = true
				return result, lease, nil
			}
		}
		return nil, lease, err
	}
	result := genericObjectResult(created, spec)
	result["preview"] = genericPreviewResult("create", nil, dryRun, spec)
	return result, lease, nil
}

func (r *KubernetesRunner) genericUpdate(ctx context.Context, resource dynamicResource, task domain.TaskEnvelope, spec genericKindSpec, lease Lease) (map[string]any, Lease, error) {
	name, err := genericBoundName(task, spec)
	if err != nil {
		return nil, lease, err
	}
	before, err := resource.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	object, err := genericManifest(task.Parameters, spec, stringParameter(task.Parameters, "namespace"))
	if err != nil {
		return nil, lease, err
	}
	if err := prepareGenericUpdate(object, before, task.Parameters, task.ExecutionID); err != nil {
		return nil, lease, err
	}
	dryRun, err := resource.Update(ctx, object.DeepCopy(), metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "warden-control"})
	if err != nil {
		return nil, lease, err
	}
	preview := genericPreviewResult("update", before, dryRun, spec)
	if err := requireGenericPreviewHash(task.Parameters, preview); err != nil {
		return nil, lease, err
	}
	updated, err := resource.Update(ctx, object, metav1.UpdateOptions{FieldManager: "warden-control"})
	if err != nil {
		return nil, lease, err
	}
	result := genericObjectResult(updated, spec)
	result["preview"] = preview
	return result, lease, nil
}

func (r *KubernetesRunner) genericDelete(ctx context.Context, resource dynamicResource, task domain.TaskEnvelope, spec genericKindSpec, lease Lease) (map[string]any, Lease, error) {
	name, err := genericBoundName(task, spec)
	if err != nil {
		return nil, lease, err
	}
	confirmation, err := requiredString(task.Parameters, "confirmResource")
	if err != nil {
		return nil, lease, err
	}
	expectedConfirmation := fmt.Sprintf("%s/%s/%s", spec.Kind, stringParameter(task.Parameters, "namespace"), name)
	if confirmation != expectedConfirmation {
		return nil, lease, fmt.Errorf("resource deletion confirmation does not match the authorized target")
	}
	existing, err := resource.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return map[string]any{"name": name, "namespace": stringParameter(task.Parameters, "namespace"), "deleted": true, "idempotentReplay": true}, lease, nil
	}
	if err != nil {
		return nil, lease, err
	}
	expected, err := requiredString(task.Parameters, "expectedResourceVersion")
	if err != nil {
		return nil, lease, err
	}
	if existing.GetResourceVersion() != expected {
		return nil, lease, fmt.Errorf("RESOURCE_VERSION_CONFLICT: expected resourceVersion %s, current %s", expected, existing.GetResourceVersion())
	}
	policy := metav1.DeletePropagationBackground
	switch stringParameter(task.Parameters, "propagationPolicy") {
	case "Foreground":
		policy = metav1.DeletePropagationForeground
	case "Orphan":
		policy = metav1.DeletePropagationOrphan
	case "", "Background":
	default:
		return nil, lease, fmt.Errorf("unsupported propagationPolicy")
	}
	if err := resource.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &policy, Preconditions: &metav1.Preconditions{ResourceVersion: &expected}}); err != nil && !apierrors.IsNotFound(err) {
		return nil, lease, err
	}
	return map[string]any{"name": name, "namespace": stringParameter(task.Parameters, "namespace"), "kind": spec.Kind, "deleted": true, "deletionSubmitted": true}, lease, nil
}

func genericKindFromParameters(parameters map[string]any) (genericKindSpec, error) {
	apiVersion, err := requiredString(parameters, "apiVersion")
	if err != nil {
		return genericKindSpec{}, err
	}
	kind, err := requiredString(parameters, "kind")
	if err != nil {
		return genericKindSpec{}, err
	}
	spec, ok := genericKubernetesKinds[apiVersion+"/"+kind]
	if !ok {
		return genericKindSpec{}, fmt.Errorf("Kubernetes GVK %s/%s is not allowed for generic CRUD", apiVersion, kind)
	}
	return spec, nil
}

func genericBoundName(task domain.TaskEnvelope, spec genericKindSpec) (string, error) {
	name, err := requiredString(task.Parameters, "name")
	if err != nil {
		return "", err
	}
	if task.Resource.Name != name || task.Resource.Type != spec.ResourceType {
		return "", fmt.Errorf("task parameters do not match the authorized resource")
	}
	return name, nil
}

func genericManifest(parameters map[string]any, spec genericKindSpec, namespace string) (*unstructured.Unstructured, error) {
	manifest, ok := parameters["manifest"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("parameter %q must be an object", "manifest")
	}
	data, err := json.Marshal(manifest)
	if err != nil || len(data) > genericManifestMaxBytes {
		return nil, fmt.Errorf("manifest must be valid JSON and no larger than 1 MiB")
	}
	var copied map[string]any
	if err := json.Unmarshal(data, &copied); err != nil {
		return nil, fmt.Errorf("manifest must be valid JSON")
	}
	object := &unstructured.Unstructured{Object: copied}
	if object.GetAPIVersion() != spec.APIVersion || object.GetKind() != spec.Kind {
		return nil, fmt.Errorf("manifest GVK does not match the authorized action parameters")
	}
	if object.GetNamespace() == "" {
		object.SetNamespace(namespace)
	}
	if object.GetNamespace() != namespace || strings.TrimSpace(object.GetName()) == "" {
		return nil, fmt.Errorf("manifest name/namespace does not match the authorized scope")
	}
	unstructured.RemoveNestedField(object.Object, "status")
	unstructured.RemoveNestedField(object.Object, "metadata", "managedFields")
	if path, found := findSensitiveManifestField(object.Object, nil); found {
		return nil, fmt.Errorf("manifest contains forbidden sensitive field %q", strings.Join(path, "."))
	}
	return object, nil
}

func prepareGenericUpdate(object, current *unstructured.Unstructured, parameters map[string]any, executionID string) error {
	expected, err := requiredString(parameters, "expectedResourceVersion")
	if err != nil {
		return err
	}
	if current.GetResourceVersion() != expected {
		return fmt.Errorf("RESOURCE_VERSION_CONFLICT: expected resourceVersion %s, current %s", expected, current.GetResourceVersion())
	}
	if object.GetName() != current.GetName() || object.GetNamespace() != current.GetNamespace() {
		return fmt.Errorf("manifest identity does not match the authorized resource")
	}
	object.SetResourceVersion(expected)
	annotateGenericExecution(object, executionID)
	return nil
}

func annotateGenericExecution(object *unstructured.Unstructured, executionID string) {
	annotations := object.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[genericExecutionAnnotation] = executionID
	object.SetAnnotations(annotations)
}

func genericPreviewResult(operation string, before, after *unstructured.Unstructured, spec genericKindSpec) map[string]any {
	afterManifest := sanitizeGenericObject(after)
	if operation == "create" {
		normalizeGenericCreatePreview(afterManifest)
	}
	result := map[string]any{"operation": operation, "apiVersion": spec.APIVersion, "kind": spec.Kind, "after": afterManifest}
	if before != nil {
		result["before"] = sanitizeGenericObject(before)
	}
	result["previewHash"] = domain.HashParameters(result)
	return result
}

// A Kubernetes API server may synthesize a fresh object identity for every
// server-side create dry-run. Those values are not part of the requested
// change and would otherwise make the preview hash differ when the mutation
// reruns its mandatory dry-run.
func normalizeGenericCreatePreview(manifest map[string]any) {
	if manifest == nil {
		return
	}
	for _, field := range []string{"uid", "creationTimestamp", "resourceVersion", "generation", "selfLink"} {
		unstructured.RemoveNestedField(manifest, "metadata", field)
	}
}

func requireGenericPreviewHash(parameters map[string]any, preview map[string]any) error {
	expected, err := requiredString(parameters, "previewHash")
	if err != nil {
		return fmt.Errorf("a successful server-side dry-run preview is required before mutation")
	}
	if expected != preview["previewHash"] {
		return fmt.Errorf("PREVIEW_CONFLICT: manifest or live resource changed after dry-run")
	}
	return nil
}

func genericObjectResult(object *unstructured.Unstructured, spec genericKindSpec) map[string]any {
	return map[string]any{"apiVersion": spec.APIVersion, "kind": spec.Kind, "name": object.GetName(), "namespace": object.GetNamespace(), "resourceVersion": object.GetResourceVersion(), "manifest": sanitizeGenericObject(object)}
}

func sanitizeGenericObject(object *unstructured.Unstructured) map[string]any {
	if object == nil {
		return nil
	}
	data, _ := json.Marshal(object.Object)
	var copied map[string]any
	_ = json.Unmarshal(data, &copied)
	unstructured.RemoveNestedField(copied, "metadata", "managedFields")
	if annotations, found, _ := unstructured.NestedStringMap(copied, "metadata", "annotations"); found {
		delete(annotations, genericExecutionAnnotation)
		if len(annotations) == 0 {
			unstructured.RemoveNestedField(copied, "metadata", "annotations")
		} else {
			_ = unstructured.SetNestedStringMap(copied, annotations, "metadata", "annotations")
		}
	}
	redactSensitiveManifestFields(copied)
	return copied
}

func findSensitiveManifestField(value any, path []string) ([]string, bool) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			if normalized == "stringdata" || strings.Contains(normalized, "password") || strings.Contains(normalized, "token") || strings.Contains(normalized, "privatekey") || strings.Contains(normalized, "clientsecret") || normalized == "authorization" {
				return append(path, key), true
			}
			if foundPath, found := findSensitiveManifestField(child, append(path, key)); found {
				return foundPath, true
			}
		}
	case []any:
		for _, child := range current {
			if foundPath, found := findSensitiveManifestField(child, path); found {
				return foundPath, true
			}
		}
	case string:
		if isPrivateKeyMaterial(current) {
			return path, true
		}
	}
	return nil, false
}

func redactSensitiveManifestFields(value any) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			if normalized == "stringdata" || strings.Contains(normalized, "password") || strings.Contains(normalized, "token") || strings.Contains(normalized, "privatekey") || strings.Contains(normalized, "clientsecret") || normalized == "authorization" {
				current[key] = "[REDACTED]"
				continue
			}
			if text, ok := child.(string); ok && isPrivateKeyMaterial(text) {
				current[key] = "[REDACTED]"
				continue
			}
			redactSensitiveManifestFields(child)
		}
	case []any:
		for _, child := range current {
			redactSensitiveManifestFields(child)
		}
	}
}

func isPrivateKeyMaterial(value string) bool {
	upper := strings.ToUpper(value)
	return strings.Contains(upper, "-----BEGIN") && strings.Contains(upper, "PRIVATE KEY-----")
}

func stringParameter(parameters map[string]any, key string) string {
	value, _ := parameters[key].(string)
	return strings.TrimSpace(value)
}

func numericParameter(value any) (int, bool) {
	switch number := value.(type) {
	case float64:
		return int(number), number == float64(int(number))
	case int:
		return number, true
	case int64:
		return int(number), true
	default:
		return 0, false
	}
}
