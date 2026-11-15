package connector

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

func mutationTask(resourceType, action, name, namespace, executionID string, parameters map[string]any) domain.TaskEnvelope {
	return domain.TaskEnvelope{
		ExecutionID: executionID,
		Resource:    domain.TaskResource{Type: resourceType, Name: name, Namespace: namespace},
		Action:      domain.TaskAction{Name: action}, Parameters: parameters,
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
}

func TestDeploymentStructuredMutations(t *testing.T) {
	replicas := int32(2)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "test", ResourceVersion: "10", Generation: 10},
		Spec: appsv1.DeploymentSpec{Replicas: &replicas, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "api", Image: "registry.example.com/order/api:v1"}},
		}}},
	})
	runner, err := NewKubernetesRunner(&staticCredentials{client: client}, []string{"test"}, []string{"registry.example.com/order/"})
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"name": "api", "namespace": "test", "expectedGeneration": int64(10)}

	restart := mutationTask("deployment", "deployment.restart", "api", "test", "restart-1", cloneParameters(base))
	result, _, err := runner.Execute(context.Background(), restart)
	if err != nil || result["restartedAt"] == "" {
		t.Fatalf("restart result=%#v err=%v", result, err)
	}

	scaleParams := cloneParameters(base)
	scaleParams["replicas"] = float64(4)
	result, _, err = runner.Execute(context.Background(), mutationTask("deployment", "deployment.scale", "api", "test", "scale-1", scaleParams))
	if err != nil || result["previousReplicas"] != int32(2) || result["replicas"] != int32(4) {
		t.Fatalf("scale result=%#v err=%v", result, err)
	}

	imageParams := cloneParameters(base)
	imageParams["container"] = "api"
	imageParams["image"] = "registry.example.com/order/api:v2"
	result, _, err = runner.Execute(context.Background(), mutationTask("deployment", "deployment.update_image", "api", "test", "image-1", imageParams))
	if err != nil || result["previousImage"] != "registry.example.com/order/api:v1" || result["image"] != "registry.example.com/order/api:v2" {
		t.Fatalf("image result=%#v err=%v", result, err)
	}
	updated, err := client.AppsV1().Deployments("test").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil || updated.Spec.Replicas == nil || *updated.Spec.Replicas != 4 || updated.Spec.Template.Spec.Containers[0].Image != "registry.example.com/order/api:v2" {
		t.Fatalf("deployment state=%#v err=%v", updated, err)
	}
}

func TestStatefulSetStructuredMutations(t *testing.T) {
	replicas := int32(1)
	client := fake.NewSimpleClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "test", ResourceVersion: "20", Generation: 20},
		Spec: appsv1.StatefulSetSpec{Replicas: &replicas, ServiceName: "db", Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "db"}}, Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "db", Image: "registry.example.com/order/db:v1"}},
			}}},
	})
	runner, err := NewKubernetesRunner(&staticCredentials{client: client}, []string{"test"}, []string{"registry.example.com/order/"})
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"name": "db", "namespace": "test", "expectedGeneration": int64(20)}
	if result, _, err := runner.Execute(context.Background(), mutationTask("statefulset", "statefulset.restart", "db", "test", "restart-2", cloneParameters(base))); err != nil || result["restartedAt"] == "" {
		t.Fatalf("restart result=%#v err=%v", result, err)
	}
	scaleParams := cloneParameters(base)
	scaleParams["replicas"] = float64(3)
	if result, _, err := runner.Execute(context.Background(), mutationTask("statefulset", "statefulset.scale", "db", "test", "scale-2", scaleParams)); err != nil || result["replicas"] != int32(3) {
		t.Fatalf("scale result=%#v err=%v", result, err)
	}
	imageParams := cloneParameters(base)
	imageParams["container"] = "db"
	imageParams["image"] = "registry.example.com/order/db:v2"
	if result, _, err := runner.Execute(context.Background(), mutationTask("statefulset", "statefulset.update_image", "db", "test", "image-2", imageParams)); err != nil || result["image"] != "registry.example.com/order/db:v2" {
		t.Fatalf("image result=%#v err=%v", result, err)
	}
	updated, err := client.AppsV1().StatefulSets("test").Get(context.Background(), "db", metav1.GetOptions{})
	if err != nil || updated.Spec.Replicas == nil || *updated.Spec.Replicas != 3 || updated.Spec.Template.Spec.Containers[0].Image != "registry.example.com/order/db:v2" {
		t.Fatalf("statefulset state=%#v err=%v", updated, err)
	}
}

func TestCronJobStructuredMutationsAndIdempotentTrigger(t *testing.T) {
	suspended := false
	client := fake.NewSimpleClientset(&batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "cleanup", Namespace: "test", ResourceVersion: "30", Generation: 30, UID: "cron-uid"},
		Spec: batchv1.CronJobSpec{Schedule: "0 * * * *", Suspend: &suspended, JobTemplate: batchv1.JobTemplateSpec{
			Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{{Name: "cleanup", Image: "registry.example.com/order/cleanup:v1"}}}}},
		}},
	})
	runner, err := NewKubernetesRunner(&staticCredentials{client: client}, []string{"test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"name": "cleanup", "namespace": "test", "expectedGeneration": int64(30)}
	if result, _, err := runner.Execute(context.Background(), mutationTask("cronjob", "cronjob.suspend", "cleanup", "test", "suspend-1", cloneParameters(base))); err != nil || result["suspend"] != true {
		t.Fatalf("suspend result=%#v err=%v", result, err)
	}
	if result, _, err := runner.Execute(context.Background(), mutationTask("cronjob", "cronjob.resume", "cleanup", "test", "resume-1", cloneParameters(base))); err != nil || result["suspend"] != false {
		t.Fatalf("resume result=%#v err=%v", result, err)
	}
	trigger := mutationTask("cronjob", "cronjob.trigger", "cleanup", "test", "execution-123", cloneParameters(base))
	first, _, err := runner.Execute(context.Background(), trigger)
	if err != nil || first["created"] != true {
		t.Fatalf("first trigger result=%#v err=%v", first, err)
	}
	second, _, err := runner.Execute(context.Background(), trigger)
	if err != nil || second["created"] != false || second["jobName"] != first["jobName"] {
		t.Fatalf("duplicate trigger result=%#v err=%v", second, err)
	}
	jobs, err := client.BatchV1().Jobs("test").List(context.Background(), metav1.ListOptions{})
	if err != nil || len(jobs.Items) != 1 {
		t.Fatalf("jobs=%d err=%v", len(jobs.Items), err)
	}
}

func TestKubernetesMutationRejectsStaleGenerationAndOutOfScopeNamespace(t *testing.T) {
	replicas := int32(2)
	client := fake.NewSimpleClientset(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "test", ResourceVersion: "10", Generation: 10}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}})
	credentials := &staticCredentials{client: client}
	runner, err := NewKubernetesRunner(credentials, []string{"test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stale := mutationTask("deployment", "deployment.scale", "api", "test", "stale-1", map[string]any{
		"name": "api", "namespace": "test", "expectedGeneration": int64(9), "replicas": float64(3),
	})
	if _, _, err := runner.Execute(context.Background(), stale); err == nil || !strings.Contains(err.Error(), "RESOURCE_VERSION_CONFLICT") {
		t.Fatalf("stale generation error=%v", err)
	}
	current, _ := client.AppsV1().Deployments("test").Get(context.Background(), "api", metav1.GetOptions{})
	if current.Spec.Replicas == nil || *current.Spec.Replicas != 2 {
		t.Fatal("stale request changed the Deployment")
	}

	beforeCalls := credentials.calls
	outOfScope := mutationTask("deployment", "deployment.restart", "api", "production", "scope-1", map[string]any{
		"name": "api", "namespace": "production", "expectedGeneration": int64(10),
	})
	if _, _, err := runner.Execute(context.Background(), outOfScope); err == nil {
		t.Fatal("out-of-scope mutation was accepted")
	}
	if credentials.calls != beforeCalls {
		t.Fatal("credential was issued before mutation namespace validation")
	}
}

func cloneParameters(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
