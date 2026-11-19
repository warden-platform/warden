package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestReconcileCreatesConnectorAndScopedRBAC(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "warden-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "warden-bot"}},
	)
	op := &operator{
		client: client, controlURL: "https://warden.example.test", namespace: "warden-system",
		connectorImage: "registry.example.test/warden-connector:0.2.0", imagePullSecret: "swr-secret",
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	pairing := pairingStatus{
		ConnectorID:    "12345678-1234-1234-1234-123456789abc",
		TaskSigningKey: base64.RawStdEncoding.EncodeToString(key),
		Config: map[string]any{
			"connectorName": "test-cluster", "environment": "testing",
			"allowedNamespaces":       []any{"warden-bot"},
			"allowedImagePrefixes":    []any{"registry.example.test/team/"},
			"tokenAudience":           "https://kubernetes.default.svc.cluster.local",
			"executionServiceAccount": "warden-executor",
			"credentialRefs":          []any{"prod-jenkins-token", "orders-db-password"},
		},
	}
	if err := op.reconcile(context.Background(), pairing, "fallback"); err != nil {
		t.Fatal(err)
	}
	secret, err := client.CoreV1().Secrets("warden-system").Get(context.Background(), "warden-task-signing-12345678", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(secret.Data["key"]) != string(key) {
		t.Fatal("connector secret does not contain delivered task key")
	}
	deployment, err := client.AppsV1().Deployments("warden-system").Get(context.Background(), "warden-connector-12345678", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Spec.Template.Spec.Containers[0].Image != op.connectorImage {
		t.Fatalf("connector image = %q", deployment.Spec.Template.Spec.Containers[0].Image)
	}
	if deployment.Spec.Template.Spec.Containers[0].ImagePullPolicy != corev1.PullAlways {
		t.Fatalf("connector image pull policy = %q", deployment.Spec.Template.Spec.Containers[0].ImagePullPolicy)
	}
	if deployment.Spec.Template.Spec.ServiceAccountName != "warden-connector-12345678" {
		t.Fatalf("connector service account = %q", deployment.Spec.Template.Spec.ServiceAccountName)
	}
	if len(deployment.Spec.Template.Spec.ImagePullSecrets) != 1 || deployment.Spec.Template.Spec.ImagePullSecrets[0].Name != "swr-secret" {
		t.Fatal("connector deployment is missing image pull secret")
	}
	// Jenkins integration config is no longer baked into env vars; the connector
	// pulls integrations at runtime. Assert no WARDEN_JENKINS_* env is present.
	for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
		if strings.HasPrefix(env.Name, "WARDEN_JENKINS_") {
			t.Fatalf("connector deployment should not carry Jenkins env, found %q", env.Name)
		}
	}
	secretRole, err := client.RbacV1().Roles("warden-system").Get(context.Background(), "warden-secret-reader-12345678", metav1.GetOptions{})
	if err != nil || len(secretRole.Rules) != 1 {
		t.Fatalf("connector-specific Secret Role missing: %v", err)
	}
	if got := strings.Join(secretRole.Rules[0].ResourceNames, ","); got != "orders-db-password,prod-jenkins-token" {
		t.Fatalf("unexpected Secret allowlist: %s", got)
	}
	secretBinding, err := client.RbacV1().RoleBindings("warden-system").Get(context.Background(), "warden-secret-reader-12345678", metav1.GetOptions{})
	if err != nil || len(secretBinding.Subjects) != 1 || secretBinding.Subjects[0].Name != "warden-connector-12345678" {
		t.Fatalf("connector-specific Secret binding missing: %#v %v", secretBinding, err)
	}
	if _, err := client.RbacV1().Roles("warden-bot").Get(context.Background(), "warden-token-issuer", metav1.GetOptions{}); err == nil {
		t.Fatal("reconcile must not create a dynamic warden-token-issuer Role")
	}
	tokenBinding, err := client.RbacV1().RoleBindings("warden-bot").Get(context.Background(), "warden-token-issuer-12345678", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tokenBinding.RoleRef.Kind != "ClusterRole" || tokenBinding.RoleRef.Name != "warden-token-issuer-warden-system" {
		t.Fatalf("unexpected token issuer role ref: %#v", tokenBinding.RoleRef)
	}
	for _, roleName := range []string{"warden-executor", "warden-resource-discoverer"} {
		if _, err := client.RbacV1().Roles("warden-bot").Get(context.Background(), roleName, metav1.GetOptions{}); err == nil {
			t.Fatalf("reconcile must not create a dynamic %s Role", roleName)
		}
		binding, err := client.RbacV1().RoleBindings("warden-bot").Get(context.Background(), roleName+"-12345678", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if binding.RoleRef.Kind != "ClusterRole" || binding.RoleRef.Name != roleName+"-warden-system" {
			t.Fatalf("unexpected %s role ref: %#v", roleName, binding.RoleRef)
		}
	}
}

func TestCleanupRemovesConnectorOwnedResources(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "warden-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "warden-bot"}},
	)
	op := &operator{client: client, namespace: "warden-system"}
	pairing := pairingStatus{
		ConnectorID: "12345678-1234-1234-1234-123456789abc",
		Config:      map[string]any{"allowedNamespaces": []any{"warden-bot"}},
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	pairing.TaskSigningKey = base64.RawStdEncoding.EncodeToString(key)
	op.controlURL = "https://warden.example.test"
	op.connectorImage = "registry.example.test/warden-connector:0.2.0"
	if err := op.reconcile(context.Background(), pairing, "fallback"); err != nil {
		t.Fatal(err)
	}
	if err := op.cleanup(context.Background(), pairing); err != nil {
		t.Fatal(err)
	}
	if _, err := client.AppsV1().Deployments("warden-system").Get(context.Background(), "warden-connector-12345678", metav1.GetOptions{}); err == nil {
		t.Fatal("connector deployment still exists")
	}
	if _, err := client.RbacV1().Roles("warden-system").Get(context.Background(), "warden-secret-reader-12345678", metav1.GetOptions{}); err == nil {
		t.Fatal("connector Secret Role still exists")
	}
	if _, err := client.RbacV1().RoleBindings("warden-bot").Get(context.Background(), "warden-token-issuer-12345678", metav1.GetOptions{}); err == nil {
		t.Fatal("connector role binding still exists")
	}
	for _, name := range []string{"warden-executor-12345678", "warden-resource-discoverer-12345678"} {
		if _, err := client.RbacV1().RoleBindings("warden-bot").Get(context.Background(), name, metav1.GetOptions{}); err == nil {
			t.Fatalf("connector role binding %s still exists", name)
		}
	}
	for _, name := range []string{"warden-cluster-reader-discoverer-12345678", "warden-cluster-reader-executor-warden-bot-12345678"} {
		if _, err := client.RbacV1().ClusterRoleBindings().Get(context.Background(), name, metav1.GetOptions{}); err == nil {
			t.Fatalf("connector ClusterRoleBinding %s still exists", name)
		}
	}
}

func TestUpsertClusterRoleBindingReplacesImmutableRoleRef(t *testing.T) {
	client := fake.NewSimpleClientset(&rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "warden-cluster-reader-discoverer-12345678"},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "warden-cluster-reader"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "legacy", Namespace: "warden-system"}},
	})
	if err := upsertClusterRoleBinding(context.Background(), client, "warden-cluster-reader-discoverer-12345678", "warden-cluster-reader-warden-system", "warden-system", "warden-connector-12345678"); err != nil {
		t.Fatal(err)
	}
	binding, err := client.RbacV1().ClusterRoleBindings().Get(context.Background(), "warden-cluster-reader-discoverer-12345678", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if binding.RoleRef.Name != "warden-cluster-reader-warden-system" || len(binding.Subjects) != 1 || binding.Subjects[0].Name != "warden-connector-12345678" {
		t.Fatalf("legacy binding was not replaced: %#v", binding)
	}
}

func TestImageRepositoryPrefix(t *testing.T) {
	if got := imageRepositoryPrefix("registry.example.test/team/warden-connector:v1"); got != "registry.example.test/team/" {
		t.Fatalf("prefix = %q", got)
	}
	if got := imageRepositoryPrefix("registry.example.test/team/warden-connector@sha256:abc"); got != "registry.example.test/team/" {
		t.Fatalf("digest prefix = %q", got)
	}
}

func TestTokenAudienceForVersion(t *testing.T) {
	if got := tokenAudienceForVersion("v1.33.1-k3s1"); got != "https://kubernetes.default.svc.cluster.local" {
		t.Fatalf("k3s audience = %q", got)
	}
	if got := tokenAudienceForVersion("v1.31.4-r0-CCE25.3.1.B001"); got != "https://kubernetes.default.svc.cluster.local" {
		t.Fatalf("managed Kubernetes audience = %q", got)
	}
}

func TestResolvedTokenAudienceMigratesLegacyManagedClusterDefault(t *testing.T) {
	if got := resolvedTokenAudience("https://kubernetes.default.svc", "https://kubernetes.default.svc.cluster.local"); got != "https://kubernetes.default.svc.cluster.local" {
		t.Fatalf("legacy managed-cluster audience was not migrated: %q", got)
	}
	if got := resolvedTokenAudience("https://custom.example/audience", ""); got != "https://custom.example/audience" {
		t.Fatalf("custom audience was changed: %q", got)
	}
	if got := resolvedTokenAudience("https://kubernetes.default.svc.cluster.local", "https://kubernetes.default.svc.cluster.local"); got == "" {
		t.Fatal("k3s audience was cleared")
	}
}
