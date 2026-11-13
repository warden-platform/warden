package connector

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

type genericTestCredentials struct {
	*staticCredentials
	dynamic dynamic.Interface
}

func (p *genericTestCredentials) IssueDynamic(context.Context, string, time.Duration) (dynamic.Interface, Lease, error) {
	return p.dynamic, Lease{Provider: "TEST_DYNAMIC", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func newGenericTestRunner(t *testing.T, objects ...runtime.Object) (*KubernetesRunner, dynamic.Interface) {
	t.Helper()
	scheme := runtime.NewScheme()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "configmaps"}:                            "ConfigMapList",
		{Group: "apps", Version: "v1", Resource: "deployments"}:            "DeploymentList",
		{Group: "batch", Version: "v1", Resource: "jobs"}:                  "JobList",
		{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}: "IngressList",
	}, objects...)
	createDryRuns := map[string]bool{}
	client.PrependReactor("create", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		object := action.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		executionID := object.GetAnnotations()[genericExecutionAnnotation]
		if createDryRuns[executionID] {
			return false, nil, nil
		}
		createDryRuns[executionID] = true
		result := object.DeepCopy()
		// Real API servers may generate different identity metadata for each
		// create dry-run, even when the submitted manifest is unchanged.
		result.SetUID(types.UID("dry-run-" + executionID))
		result.SetCreationTimestamp(metav1.NewTime(time.Now()))
		result.SetResourceVersion("dry-run-" + executionID)
		return true, result, nil
	})
	updateDryRuns := map[string]bool{}
	client.PrependReactor("update", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		object := action.(k8stesting.UpdateAction).GetObject().(*unstructured.Unstructured)
		executionID := object.GetAnnotations()[genericExecutionAnnotation]
		if updateDryRuns[executionID] {
			return false, nil, nil
		}
		updateDryRuns[executionID] = true
		return true, object.DeepCopy(), nil
	})
	credentials := &genericTestCredentials{staticCredentials: &staticCredentials{client: kubernetesfake.NewSimpleClientset()}, dynamic: client}
	runner, err := NewKubernetesRunner(credentials, []string{"pam-e2e-7213"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return runner, client
}

func genericTask(action, executionID string, resource domain.TaskResource, parameters map[string]any) domain.TaskEnvelope {
	return domain.TaskEnvelope{
		ExecutionID: executionID,
		Resource:    resource,
		Action:      domain.TaskAction{Name: action, Version: 1},
		Parameters:  parameters,
		Grant:       domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
}

func configMapManifest(name, value string) map[string]any {
	return map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": name, "namespace": "pam-e2e-7213"},
		"data":     map[string]any{"message": value},
	}
}

func TestGenericKubernetesCreatePreviewIdempotencyAndList(t *testing.T) {
	runner, _ := newGenericTestRunner(t)
	namespaceResource := domain.TaskResource{Type: "namespace", Name: "pam-e2e-7213"}
	base := map[string]any{"operation": "create", "apiVersion": "v1", "kind": "ConfigMap", "namespace": "pam-e2e-7213", "manifest": configMapManifest("pam-e2e-7213-config", "v1")}
	preview, _, err := runner.Execute(context.Background(), genericTask("kubernetes.resource.preview", "preview-1", namespaceResource, base))
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	hash, _ := preview["previewHash"].(string)
	if len(hash) != 64 {
		t.Fatalf("invalid preview hash: %#v", preview)
	}
	after, _ := preview["after"].(map[string]any)
	metadata, _ := after["metadata"].(map[string]any)
	for _, field := range []string{"uid", "creationTimestamp", "resourceVersion"} {
		if _, found := metadata[field]; found {
			t.Fatalf("create preview contains non-deterministic metadata %q: %#v", field, preview)
		}
	}
	createParameters := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "namespace": "pam-e2e-7213", "manifest": configMapManifest("pam-e2e-7213-config", "v1"), "previewHash": hash}
	createTask := genericTask("kubernetes.resource.create", "create-1", namespaceResource, createParameters)
	created, _, err := runner.Execute(context.Background(), createTask)
	if err != nil || created["name"] != "pam-e2e-7213-config" {
		t.Fatalf("create failed: result=%#v err=%v", created, err)
	}
	replayed, _, err := runner.Execute(context.Background(), createTask)
	if err != nil || replayed["idempotentReplay"] != true {
		t.Fatalf("idempotent replay failed: result=%#v err=%v", replayed, err)
	}
	listed, _, err := runner.Execute(context.Background(), genericTask("kubernetes.resource.list", "list-1", namespaceResource,
		map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "namespace": "pam-e2e-7213", "limit": float64(10)}))
	if err != nil || listed["count"] != 1 {
		t.Fatalf("list failed: result=%#v err=%v", listed, err)
	}
}

func TestGenericKubernetesUpdateConflictAndDeleteConfirmation(t *testing.T) {
	existing := &unstructured.Unstructured{Object: configMapManifest("pam-e2e-7213-config", "v1")}
	existing.SetResourceVersion("10")
	runner, client := newGenericTestRunner(t, existing)
	resource := domain.TaskResource{Type: "configmap", Name: existing.GetName(), Namespace: existing.GetNamespace()}
	previewParameters := map[string]any{"operation": "update", "apiVersion": "v1", "kind": "ConfigMap", "name": existing.GetName(), "namespace": existing.GetNamespace(), "expectedResourceVersion": "10", "manifest": configMapManifest(existing.GetName(), "v2")}
	preview, _, err := runner.Execute(context.Background(), genericTask("kubernetes.resource.preview", "preview-update", resource, previewParameters))
	if err != nil {
		t.Fatalf("update preview failed: %v", err)
	}
	updateParameters := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "name": existing.GetName(), "namespace": existing.GetNamespace(), "expectedResourceVersion": "10", "manifest": configMapManifest(existing.GetName(), "v2"), "previewHash": preview["previewHash"]}
	updated, _, err := runner.Execute(context.Background(), genericTask("kubernetes.resource.update", "update-1", resource, updateParameters))
	if err != nil || updated["resourceVersion"] == "" {
		t.Fatalf("update failed: result=%#v err=%v", updated, err)
	}
	stale := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "name": existing.GetName(), "namespace": existing.GetNamespace(), "expectedResourceVersion": "9", "manifest": configMapManifest(existing.GetName(), "v3"), "previewHash": preview["previewHash"]}
	if _, _, err := runner.Execute(context.Background(), genericTask("kubernetes.resource.update", "update-stale", resource, stale)); err == nil || !strings.Contains(err.Error(), "RESOURCE_VERSION_CONFLICT") {
		t.Fatalf("stale update was not rejected: %v", err)
	}
	live, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).Namespace(existing.GetNamespace()).Get(context.Background(), existing.GetName(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	deleteParameters := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "name": existing.GetName(), "namespace": existing.GetNamespace(), "expectedResourceVersion": live.GetResourceVersion(), "confirmResource": "wrong", "propagationPolicy": "Background"}
	if _, _, err := runner.Execute(context.Background(), genericTask("kubernetes.resource.delete", "delete-wrong", resource, deleteParameters)); err == nil {
		t.Fatal("wrong delete confirmation was accepted")
	}
	deleteParameters["confirmResource"] = "ConfigMap/pam-e2e-7213/pam-e2e-7213-config"
	deleted, _, err := runner.Execute(context.Background(), genericTask("kubernetes.resource.delete", "delete-1", resource, deleteParameters))
	if err != nil || deleted["deletionSubmitted"] != true {
		t.Fatalf("delete failed: result=%#v err=%v", deleted, err)
	}
}

func TestGenericKubernetesRejectsForbiddenGVKScopeAndSensitiveManifest(t *testing.T) {
	runner, _ := newGenericTestRunner(t)
	namespaceResource := domain.TaskResource{Type: "namespace", Name: "pam-e2e-7213"}
	forbidden := map[string]any{"operation": "create", "apiVersion": "v1", "kind": "Secret", "namespace": "pam-e2e-7213", "manifest": map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "bad", "namespace": "pam-e2e-7213"}, "stringData": map[string]any{"token": "secret"}}}
	if _, _, err := runner.Execute(context.Background(), genericTask("kubernetes.resource.preview", "forbidden", namespaceResource, forbidden)); err == nil {
		t.Fatal("Secret GVK was accepted")
	}
	sensitive := map[string]any{"operation": "create", "apiVersion": "v1", "kind": "ConfigMap", "namespace": "pam-e2e-7213", "manifest": map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "bad", "namespace": "pam-e2e-7213"}, "data": map[string]any{"password": "secret"}}}
	if _, _, err := runner.Execute(context.Background(), genericTask("kubernetes.resource.preview", "sensitive", namespaceResource, sensitive)); err == nil {
		t.Fatal("sensitive manifest field was accepted")
	}
	privateKey := map[string]any{"operation": "create", "apiVersion": "v1", "kind": "ConfigMap", "namespace": "pam-e2e-7213", "manifest": map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "bad", "namespace": "pam-e2e-7213"}, "data": map[string]any{"tls.key": "-----BEGIN PRIVATE KEY-----\nredacted\n-----END PRIVATE KEY-----"}}}
	if _, _, err := runner.Execute(context.Background(), genericTask("kubernetes.resource.preview", "private-key", namespaceResource, privateKey)); err == nil {
		t.Fatal("private key material was accepted")
	}
	outOfScope := map[string]any{"operation": "create", "apiVersion": "v1", "kind": "ConfigMap", "namespace": "default", "manifest": configMapManifest("bad", "v1")}
	if _, _, err := runner.Execute(context.Background(), genericTask("kubernetes.resource.preview", "scope", namespaceResource, outOfScope)); err == nil {
		t.Fatal("out-of-scope namespace was accepted")
	}
}
