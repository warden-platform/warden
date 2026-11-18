package connector

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

func TestTokenRequestSpecUsesServerDefaultAudienceWhenEmpty(t *testing.T) {
	spec := tokenRequestSpec("", 600)
	if len(spec.Audiences) != 0 {
		t.Fatalf("empty audience must be omitted, got %v", spec.Audiences)
	}
	spec = tokenRequestSpec(" https://kubernetes.default.svc.cluster.local ", 600)
	if len(spec.Audiences) != 1 || spec.Audiences[0] != "https://kubernetes.default.svc.cluster.local" {
		t.Fatalf("explicit audience was not preserved: %v", spec.Audiences)
	}
}

func TestTokenAudience(t *testing.T) {
	encode := func(value string) string { return base64.RawURLEncoding.EncodeToString([]byte(value)) }
	for _, tc := range []struct{ name, claims, want string }{
		{"array", `{"aud":["https://kubernetes.default.svc.cluster.local"]}`, "https://kubernetes.default.svc.cluster.local"},
		{"string", `{"aud":"custom-audience"}`, "custom-audience"},
		{"empty", `{}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := encode(`{"alg":"none"}`) + "." + encode(tc.claims) + ".signature"
			if got := tokenAudience(token); got != tc.want {
				t.Fatalf("tokenAudience() = %q, want %q", got, tc.want)
			}
		})
	}
}

type staticCredentials struct {
	client kubernetes.Interface
	calls  int
}

func (p *staticCredentials) Issue(context.Context, string, time.Duration) (kubernetes.Interface, Lease, error) {
	p.calls++
	return p.client, Lease{Provider: "TEST", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (p *staticCredentials) BootstrapClient() kubernetes.Interface { return p.client }

func TestKubernetesRunnerGetsAuthorizedDeployment(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "test"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "api", Image: "registry.example.com/order/api:v1"}},
		}}},
	})
	credentials := &staticCredentials{client: client}
	runner, err := NewKubernetesRunner(credentials, []string{"test"}, []string{"registry.example.com/order/"})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource:   domain.TaskResource{Name: "api", Namespace: "test"},
		Action:     domain.TaskAction{Name: "deployment.get"},
		Parameters: map[string]any{"name": "api", "namespace": "test"},
		Grant:      domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if result["name"] != "api" || credentials.calls != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestKubernetesRunnerRejectsNamespaceBeforeIssuingCredential(t *testing.T) {
	credentials := &staticCredentials{client: fake.NewSimpleClientset()}
	runner, err := NewKubernetesRunner(credentials, []string{"test"}, []string{"registry.example.com/order/"})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskEnvelope{
		Resource:   domain.TaskResource{Name: "api", Namespace: "production"},
		Action:     domain.TaskAction{Name: "deployment.get"},
		Parameters: map[string]any{"name": "api", "namespace": "production"},
		Grant:      domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	if _, _, err := runner.Execute(context.Background(), task); err == nil {
		t.Fatal("out-of-scope namespace was accepted")
	}
	if credentials.calls != 0 {
		t.Fatal("credential was issued before namespace validation")
	}
}

func TestKubernetesRunnerAppliesAndDeletesOnlyDashboardConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()
	credentials := &staticCredentials{client: client}
	runner, err := NewKubernetesRunner(credentials, []string{"observability"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	grant := domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)}
	apply := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "namespace", Name: "observability", ExternalID: "namespace/observability"},
		Action:   domain.TaskAction{Name: "namespace.dashboard_configmap.apply"}, Grant: grant,
		Parameters: map[string]any{"name": "observability", "configMapName": "grafana-dashboard-api", "dashboardKey": "api.json", "dashboard": map[string]any{"title": "API"}},
	}
	result, _, err := runner.Execute(context.Background(), apply)
	if err != nil {
		t.Fatal(err)
	}
	if result["created"] != true {
		t.Fatalf("unexpected apply result: %#v", result)
	}
	cm, err := client.CoreV1().ConfigMaps("observability").Get(context.Background(), "grafana-dashboard-api", metav1.GetOptions{})
	if err != nil || cm.Labels["app.kubernetes.io/managed-by"] != "warden" || cm.Data["api.json"] == "" {
		t.Fatalf("dashboard ConfigMap was not safely materialized: %#v %v", cm, err)
	}
	deleteTask := domain.TaskEnvelope{Resource: domain.TaskResource{Type: "configmap", Name: "grafana-dashboard-api", Namespace: "observability"}, Action: domain.TaskAction{Name: "configmap.dashboard.delete"}, Grant: grant, Parameters: map[string]any{"name": "grafana-dashboard-api", "namespace": "observability"}}
	if _, _, err := runner.Execute(context.Background(), deleteTask); err != nil {
		t.Fatal(err)
	}
	unsafe := apply
	unsafe.Parameters["configMapName"] = "application-config"
	if _, _, err := runner.Execute(context.Background(), unsafe); err == nil {
		t.Fatal("non-dashboard ConfigMap name was accepted")
	}
}

func TestKubernetesRunnerSnapshotResourceReadsDeployment(t *testing.T) {
	replicas := int32(2)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "test", ResourceVersion: "42", Generation: 3},
		Spec: appsv1.DeploymentSpec{Replicas: &replicas, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "api", Image: "registry.example.com/order/api:v1"}},
		}}},
	})
	credentials := &staticCredentials{client: client}
	runner, err := NewKubernetesRunner(credentials, []string{"test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resource, found := runner.SnapshotResource(context.Background(), domain.TaskResource{Type: "deployment", Name: "api", Namespace: "test", ExternalID: "test/api"})
	if !found {
		t.Fatal("expected deployment snapshot to be found")
	}
	if resource.Name != "api" || resource.Namespace != "test" || resource.ResourceType != "deployment" || resource.ExternalID != "test/api" {
		t.Fatalf("unexpected resource identity: %#v", resource)
	}
	if resource.Metadata["resourceVersion"] != "42" || resource.Metadata["generation"] != int64(3) {
		t.Fatalf("unexpected metadata: %#v", resource.Metadata)
	}
	if resource.Metadata["replicas"] != int32(2) {
		t.Fatalf("expected replicas=2, got %v", resource.Metadata["replicas"])
	}
}

func TestKubernetesRunnerSnapshotResourceReturnsAbsentOn404(t *testing.T) {
	credentials := &staticCredentials{client: fake.NewSimpleClientset()}
	runner, err := NewKubernetesRunner(credentials, []string{"test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, found := runner.SnapshotResource(context.Background(), domain.TaskResource{Type: "deployment", Name: "missing", Namespace: "test", ExternalID: "test/missing"})
	if found {
		t.Fatal("404 deployment snapshot must be absent")
	}
}

func TestKubernetesRunnerSnapshotResourceRejectsOutOfScopeNamespace(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "production"},
	})
	credentials := &staticCredentials{client: client}
	runner, err := NewKubernetesRunner(credentials, []string{"test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, found := runner.SnapshotResource(context.Background(), domain.TaskResource{Type: "deployment", Name: "api", Namespace: "production", ExternalID: "production/api"})
	if found {
		t.Fatal("out-of-scope namespace snapshot must be rejected")
	}
}
