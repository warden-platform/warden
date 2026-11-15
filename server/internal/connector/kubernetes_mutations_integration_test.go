package connector

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/clientcmd"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

func TestLocalKubernetesFixtureConnector(t *testing.T) {
	if os.Getenv("WARDEN_LOCAL_FIXTURE_CONNECTOR") != "1" {
		t.Skip("set WARDEN_LOCAL_FIXTURE_CONNECTOR=1 to expose a temporary local UI fixture")
	}
	keyFile := os.Getenv("WARDEN_TEST_TASK_SIGNING_KEY_FILE")
	if keyFile == "" {
		t.Fatal("WARDEN_TEST_TASK_SIGNING_KEY_FILE is required")
	}
	signingKey, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	signingKey = []byte(strings.TrimSpace(string(signingKey)))
	one := int32(1)
	suspended := false
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "ope7209-web", Namespace: "ope7209-test", ResourceVersion: "1", Generation: 1},
			Spec: appsv1.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "ope7209-web"}}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "ope7209-web"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "registry.example.com/team/web:v1"}}}}}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "ope7209-db", Namespace: "ope7209-test", ResourceVersion: "1", Generation: 1},
			Spec: appsv1.StatefulSetSpec{Replicas: &one, ServiceName: "ope7209-db", Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "ope7209-db"}}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "ope7209-db"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "db", Image: "registry.example.com/team/db:v1"}}}}}},
		&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "ope7209-cleanup", Namespace: "ope7209-test", ResourceVersion: "1", Generation: 1}, Spec: batchv1.CronJobSpec{Schedule: "0 * * * *", Suspend: &suspended, JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "cleanup", Image: "registry.example.com/team/cleanup:v1"}}}}}}}},
	)
	runner, err := NewKubernetesRunner(&staticCredentials{client: client}, []string{"ope7209-test"}, []string{"registry.example.com/team/"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime("http://localhost:8080", "ope7209-ui-fixture", "testing", "dev-ope7209", signingKey, runner)
	duration := 10 * time.Minute
	if value := os.Getenv("WARDEN_LOCAL_FIXTURE_DURATION"); value != "" {
		if parsed, parseErr := time.ParseDuration(value); parseErr == nil {
			duration = parsed
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	if err := runtime.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestKubernetesWorkloadMutationsOnRealCluster(t *testing.T) {
	kubeconfig := os.Getenv("WARDEN_KUBERNETES_TEST_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set WARDEN_KUBERNETES_TEST_KUBECONFIG to run the real-cluster mutation test")
	}
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	namespace := "warden-ope7209-" + time.Now().UTC().Format("150405")
	if _, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = client.CoreV1().Namespaces().Delete(cleanupCtx, namespace, metav1.DeleteOptions{})
	})

	zero := int32(0)
	deployment, err := client.AppsV1().Deployments(namespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec: appsv1.DeploymentSpec{Replicas: &zero, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}}, Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "web", Image: "registry.k8s.io/pause:3.9"}},
			}}},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	statefulSet, err := client.AppsV1().StatefulSets(namespace).Create(ctx, &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db"},
		Spec: appsv1.StatefulSetSpec{Replicas: &zero, ServiceName: "db", Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "db"}}, Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "db", Image: "registry.k8s.io/pause:3.9"}},
			}}},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	initialSuspend := false
	cronJob, err := client.BatchV1().CronJobs(namespace).Create(ctx, &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "cleanup"},
		Spec: batchv1.CronJobSpec{Schedule: "0 0 1 1 *", Suspend: &initialSuspend, JobTemplate: batchv1.JobTemplateSpec{
			Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{{Name: "cleanup", Image: "registry.k8s.io/pause:3.9"}}}}},
		}},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	runner, err := NewKubernetesRunner(&staticCredentials{client: client}, []string{namespace}, []string{"registry.k8s.io/"})
	if err != nil {
		t.Fatal(err)
	}

	// Deployment: restart, scale and rollback, image update and rollback.
	runRealMutation(t, runner, mutationTask("deployment", "deployment.restart", "web", namespace, "real-deploy-restart",
		map[string]any{"name": "web", "namespace": namespace, "expectedGeneration": deployment.Generation}))
	deployment = mustDeployment(t, ctx, client, namespace, "web")
	runRealMutation(t, runner, mutationTask("deployment", "deployment.scale", "web", namespace, "real-deploy-scale",
		map[string]any{"name": "web", "namespace": namespace, "expectedGeneration": deployment.Generation, "replicas": float64(1)}))
	deployment = mustDeployment(t, ctx, client, namespace, "web")
	runRealMutation(t, runner, mutationTask("deployment", "deployment.scale", "web", namespace, "real-deploy-scale-rollback",
		map[string]any{"name": "web", "namespace": namespace, "expectedGeneration": deployment.Generation, "replicas": float64(0)}))
	deployment = mustDeployment(t, ctx, client, namespace, "web")
	runRealMutation(t, runner, mutationTask("deployment", "deployment.update_image", "web", namespace, "real-deploy-image",
		map[string]any{"name": "web", "namespace": namespace, "expectedGeneration": deployment.Generation, "container": "web", "image": "registry.k8s.io/pause:3.10"}))
	deployment = mustDeployment(t, ctx, client, namespace, "web")
	runRealMutation(t, runner, mutationTask("deployment", "deployment.update_image", "web", namespace, "real-deploy-image-rollback",
		map[string]any{"name": "web", "namespace": namespace, "expectedGeneration": deployment.Generation, "container": "web", "image": "registry.k8s.io/pause:3.9"}))

	// StatefulSet: restart, scale and image update, followed by explicit rollback.
	runRealMutation(t, runner, mutationTask("statefulset", "statefulset.restart", "db", namespace, "real-sts-restart",
		map[string]any{"name": "db", "namespace": namespace, "expectedGeneration": statefulSet.Generation}))
	statefulSet = mustStatefulSet(t, ctx, client, namespace, "db")
	runRealMutation(t, runner, mutationTask("statefulset", "statefulset.scale", "db", namespace, "real-sts-scale",
		map[string]any{"name": "db", "namespace": namespace, "expectedGeneration": statefulSet.Generation, "replicas": float64(1)}))
	statefulSet = mustStatefulSet(t, ctx, client, namespace, "db")
	runRealMutation(t, runner, mutationTask("statefulset", "statefulset.scale", "db", namespace, "real-sts-scale-rollback",
		map[string]any{"name": "db", "namespace": namespace, "expectedGeneration": statefulSet.Generation, "replicas": float64(0)}))
	statefulSet = mustStatefulSet(t, ctx, client, namespace, "db")
	runRealMutation(t, runner, mutationTask("statefulset", "statefulset.update_image", "db", namespace, "real-sts-image",
		map[string]any{"name": "db", "namespace": namespace, "expectedGeneration": statefulSet.Generation, "container": "db", "image": "registry.k8s.io/pause:3.10"}))
	statefulSet = mustStatefulSet(t, ctx, client, namespace, "db")
	runRealMutation(t, runner, mutationTask("statefulset", "statefulset.update_image", "db", namespace, "real-sts-image-rollback",
		map[string]any{"name": "db", "namespace": namespace, "expectedGeneration": statefulSet.Generation, "container": "db", "image": "registry.k8s.io/pause:3.9"}))

	// CronJob: suspend, resume, deterministic trigger, and duplicate delivery.
	runRealMutation(t, runner, mutationTask("cronjob", "cronjob.suspend", "cleanup", namespace, "real-cron-suspend",
		map[string]any{"name": "cleanup", "namespace": namespace, "expectedGeneration": cronJob.Generation}))
	cronJob = mustCronJob(t, ctx, client, namespace, "cleanup")
	runRealMutation(t, runner, mutationTask("cronjob", "cronjob.resume", "cleanup", namespace, "real-cron-resume",
		map[string]any{"name": "cleanup", "namespace": namespace, "expectedGeneration": cronJob.Generation}))
	cronJob = mustCronJob(t, ctx, client, namespace, "cleanup")
	trigger := mutationTask("cronjob", "cronjob.trigger", "cleanup", namespace, "real-cron-trigger",
		map[string]any{"name": "cleanup", "namespace": namespace, "expectedGeneration": cronJob.Generation})
	first := runRealMutation(t, runner, trigger)
	second := runRealMutation(t, runner, trigger)
	if first["jobName"] != second["jobName"] || second["created"] != false {
		t.Fatalf("CronJob duplicate trigger was not idempotent: first=%#v second=%#v", first, second)
	}

	// Stale spec generation and unauthorized namespace must fail without mutation.
	if _, _, err := runner.Execute(ctx, mutationTask("deployment", "deployment.scale", "web", namespace, "real-stale",
		map[string]any{"name": "web", "namespace": namespace, "expectedGeneration": deployment.Generation + 100, "replicas": float64(2)})); err == nil {
		t.Fatal("stale generation was accepted")
	}
	if _, _, err := runner.Execute(ctx, mutationTask("deployment", "deployment.restart", "web", "default", "real-scope",
		map[string]any{"name": "web", "namespace": "default", "expectedGeneration": deployment.Generation})); err == nil {
		t.Fatal("out-of-scope namespace was accepted")
	}
}

func runRealMutation(t *testing.T, runner *KubernetesRunner, task domain.TaskEnvelope) map[string]any {
	t.Helper()
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("%s failed: %v", task.Action.Name, err)
	}
	return result
}

func mustDeployment(t *testing.T, ctx context.Context, client kubernetes.Interface, namespace, name string) *appsv1.Deployment {
	t.Helper()
	item, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func mustStatefulSet(t *testing.T, ctx context.Context, client kubernetes.Interface, namespace, name string) *appsv1.StatefulSet {
	t.Helper()
	item, err := client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func mustCronJob(t *testing.T, ctx context.Context, client kubernetes.Interface, namespace, name string) *batchv1.CronJob {
	t.Helper()
	item, err := client.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return item
}
