package connector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

const rolloutRestartAnnotation = "kubectl.kubernetes.io/restartedAt"

func valueOrDefault(value *int32, fallback int32) int32 {
	if value == nil {
		return fallback
	}
	return *value
}

func expectedGeneration(parameters map[string]any) (int64, error) {
	raw, ok := parameters["expectedGeneration"]
	if !ok {
		return 0, fmt.Errorf("parameter %q must be an integer", "expectedGeneration")
	}
	var value int64
	switch typed := raw.(type) {
	case float64:
		if typed != float64(int64(typed)) {
			return 0, fmt.Errorf("parameter %q must be an integer", "expectedGeneration")
		}
		value = int64(typed)
	case int:
		value = int64(typed)
	case int64:
		value = typed
	default:
		return 0, fmt.Errorf("parameter %q must be an integer", "expectedGeneration")
	}
	if value < 1 {
		return 0, fmt.Errorf("parameter %q must be positive", "expectedGeneration")
	}
	return value, nil
}

func verifyGeneration(actual, expected int64) error {
	if actual != expected {
		return fmt.Errorf("RESOURCE_VERSION_CONFLICT: expected generation %d, current %d", expected, actual)
	}
	return nil
}

func requiredReplicas(parameters map[string]any) (int32, error) {
	var value int64
	switch raw := parameters["replicas"].(type) {
	case float64:
		if raw != float64(int64(raw)) {
			return 0, fmt.Errorf("parameter %q must be an integer", "replicas")
		}
		value = int64(raw)
	case int:
		value = int64(raw)
	case int32:
		value = int64(raw)
	case int64:
		value = raw
	default:
		return 0, fmt.Errorf("parameter %q must be an integer", "replicas")
	}
	if value < 0 || value > 1000 {
		return 0, fmt.Errorf("parameter %q must be between 0 and 1000", "replicas")
	}
	return int32(value), nil
}

func setRestartAnnotation(annotations map[string]string) (map[string]string, string, string) {
	next := make(map[string]string, len(annotations)+1)
	for key, value := range annotations {
		next[key] = value
	}
	previous := next[rolloutRestartAnnotation]
	restartedAt := time.Now().UTC().Format(time.RFC3339Nano)
	next[rolloutRestartAnnotation] = restartedAt
	return next, previous, restartedAt
}

func updateDeploymentWithRetry(ctx context.Context, client kubernetes.Interface, namespace, name string, expected int64, mutate func(*appsv1.Deployment) (map[string]any, error)) (*appsv1.Deployment, map[string]any, error) {
	var updated *appsv1.Deployment
	var operation map[string]any
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if err := verifyGeneration(current.Generation, expected); err != nil {
			return err
		}
		next := current.DeepCopy()
		operation, err = mutate(next)
		if err != nil {
			return err
		}
		updated, err = client.AppsV1().Deployments(namespace).Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
	return updated, operation, err
}

func updateStatefulSetWithRetry(ctx context.Context, client kubernetes.Interface, namespace, name string, expected int64, mutate func(*appsv1.StatefulSet) (map[string]any, error)) (*appsv1.StatefulSet, map[string]any, error) {
	var updated *appsv1.StatefulSet
	var operation map[string]any
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if err := verifyGeneration(current.Generation, expected); err != nil {
			return err
		}
		next := current.DeepCopy()
		operation, err = mutate(next)
		if err != nil {
			return err
		}
		updated, err = client.AppsV1().StatefulSets(namespace).Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
	return updated, operation, err
}

func updateCronJobWithRetry(ctx context.Context, client kubernetes.Interface, namespace, name string, expected int64, mutate func(*batchv1.CronJob) map[string]any) (*batchv1.CronJob, map[string]any, error) {
	var updated *batchv1.CronJob
	var operation map[string]any
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := client.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if err := verifyGeneration(current.Generation, expected); err != nil {
			return err
		}
		next := current.DeepCopy()
		operation = mutate(next)
		updated, err = client.BatchV1().CronJobs(namespace).Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
	return updated, operation, err
}

func (r *KubernetesRunner) restartDeployment(ctx context.Context, client kubernetes.Interface, namespace, name string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	expected, err := expectedGeneration(parameters)
	if err != nil {
		return nil, lease, err
	}
	updated, operation, err := updateDeploymentWithRetry(ctx, client, namespace, name, expected, func(next *appsv1.Deployment) (map[string]any, error) {
		annotations, previous, restartedAt := setRestartAnnotation(next.Spec.Template.Annotations)
		next.Spec.Template.Annotations = annotations
		return map[string]any{"previousRestartedAt": previous, "restartedAt": restartedAt}, nil
	})
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"name": updated.Name, "namespace": updated.Namespace,
		"previousRestartedAt": operation["previousRestartedAt"], "restartedAt": operation["restartedAt"],
		"resourceVersion": updated.ResourceVersion, "generation": updated.Generation}, lease, nil
}

func (r *KubernetesRunner) scaleDeployment(ctx context.Context, client kubernetes.Interface, namespace, name string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	expected, err := expectedGeneration(parameters)
	if err != nil {
		return nil, lease, err
	}
	replicas, err := requiredReplicas(parameters)
	if err != nil {
		return nil, lease, err
	}
	updated, operation, err := updateDeploymentWithRetry(ctx, client, namespace, name, expected, func(next *appsv1.Deployment) (map[string]any, error) {
		previous := valueOrDefault(next.Spec.Replicas, 1)
		next.Spec.Replicas = &replicas
		return map[string]any{"previousReplicas": previous}, nil
	})
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"name": updated.Name, "namespace": updated.Namespace, "previousReplicas": operation["previousReplicas"],
		"replicas": replicas, "resourceVersion": updated.ResourceVersion, "generation": updated.Generation}, lease, nil
}

func (r *KubernetesRunner) restartStatefulSet(ctx context.Context, client kubernetes.Interface, namespace, name string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	expected, err := expectedGeneration(parameters)
	if err != nil {
		return nil, lease, err
	}
	updated, operation, err := updateStatefulSetWithRetry(ctx, client, namespace, name, expected, func(next *appsv1.StatefulSet) (map[string]any, error) {
		annotations, previous, restartedAt := setRestartAnnotation(next.Spec.Template.Annotations)
		next.Spec.Template.Annotations = annotations
		return map[string]any{"previousRestartedAt": previous, "restartedAt": restartedAt}, nil
	})
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"name": updated.Name, "namespace": updated.Namespace,
		"previousRestartedAt": operation["previousRestartedAt"], "restartedAt": operation["restartedAt"],
		"resourceVersion": updated.ResourceVersion, "generation": updated.Generation}, lease, nil
}

func (r *KubernetesRunner) scaleStatefulSet(ctx context.Context, client kubernetes.Interface, namespace, name string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	expected, err := expectedGeneration(parameters)
	if err != nil {
		return nil, lease, err
	}
	replicas, err := requiredReplicas(parameters)
	if err != nil {
		return nil, lease, err
	}
	updated, operation, err := updateStatefulSetWithRetry(ctx, client, namespace, name, expected, func(next *appsv1.StatefulSet) (map[string]any, error) {
		previous := valueOrDefault(next.Spec.Replicas, 1)
		next.Spec.Replicas = &replicas
		return map[string]any{"previousReplicas": previous}, nil
	})
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"name": updated.Name, "namespace": updated.Namespace, "previousReplicas": operation["previousReplicas"],
		"replicas": replicas, "resourceVersion": updated.ResourceVersion, "generation": updated.Generation}, lease, nil
}

func (r *KubernetesRunner) updateStatefulSetImage(ctx context.Context, client kubernetes.Interface, namespace, name string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	container, err := requiredString(parameters, "container")
	if err != nil {
		return nil, lease, err
	}
	image, err := requiredString(parameters, "image")
	if err != nil {
		return nil, lease, err
	}
	expected, err := expectedGeneration(parameters)
	if err != nil {
		return nil, lease, err
	}
	if !r.imageAllowed(image) {
		return nil, lease, fmt.Errorf("image %q is outside the allowed registry scope", image)
	}
	updated, operation, err := updateStatefulSetWithRetry(ctx, client, namespace, name, expected, func(next *appsv1.StatefulSet) (map[string]any, error) {
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

func containerIndex(containers []corev1.Container, name string) int {
	for index := range containers {
		if containers[index].Name == name {
			return index
		}
	}
	return -1
}

func (r *KubernetesRunner) setCronJobSuspended(ctx context.Context, client kubernetes.Interface, namespace, name string, parameters map[string]any, suspended bool, lease Lease) (map[string]any, Lease, error) {
	expected, err := expectedGeneration(parameters)
	if err != nil {
		return nil, lease, err
	}
	updated, operation, err := updateCronJobWithRetry(ctx, client, namespace, name, expected, func(next *batchv1.CronJob) map[string]any {
		previous := next.Spec.Suspend != nil && *next.Spec.Suspend
		next.Spec.Suspend = &suspended
		return map[string]any{"previousSuspend": previous, "changed": previous != suspended}
	})
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"name": updated.Name, "namespace": updated.Namespace, "previousSuspend": operation["previousSuspend"],
		"suspend": suspended, "changed": operation["changed"], "resourceVersion": updated.ResourceVersion,
		"generation": updated.Generation}, lease, nil
}

func (r *KubernetesRunner) triggerCronJob(ctx context.Context, client kubernetes.Interface, namespace, name string, task domain.TaskEnvelope, lease Lease) (map[string]any, Lease, error) {
	expected, err := expectedGeneration(task.Parameters)
	if err != nil {
		return nil, lease, err
	}
	if strings.TrimSpace(task.ExecutionID) == "" {
		return nil, lease, fmt.Errorf("execution id is required for idempotent CronJob triggering")
	}
	cronJob, err := client.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	if err := verifyGeneration(cronJob.Generation, expected); err != nil {
		return nil, lease, err
	}
	jobName := cronJobExecutionJobName(name, task.ExecutionID)
	labels := copyStringMap(cronJob.Spec.JobTemplate.Labels)
	annotations := copyStringMap(cronJob.Spec.JobTemplate.Annotations)
	annotations["warden.example.com/execution-id"] = task.ExecutionID
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: namespace, Labels: labels, Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(cronJob, batchv1.SchemeGroupVersion.WithKind("CronJob"))}},
		Spec: *cronJob.Spec.JobTemplate.Spec.DeepCopy(),
	}
	created, err := client.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		existing, getErr := client.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
		if getErr != nil {
			return nil, lease, getErr
		}
		if existing.Annotations["warden.example.com/execution-id"] != task.ExecutionID {
			return nil, lease, fmt.Errorf("CRONJOB_TRIGGER_CONFLICT: deterministic Job name is owned by another execution")
		}
		return map[string]any{"name": cronJob.Name, "namespace": namespace, "jobName": existing.Name,
			"created": false, "resourceVersion": cronJob.ResourceVersion}, lease, nil
	}
	if err != nil {
		return nil, lease, err
	}
	verified, err := client.BatchV1().Jobs(namespace).Get(ctx, created.Name, metav1.GetOptions{})
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{"name": cronJob.Name, "namespace": namespace, "jobName": verified.Name,
		"created": true, "resourceVersion": cronJob.ResourceVersion}, lease, nil
}

func cronJobExecutionJobName(cronJobName, executionID string) string {
	digest := sha256.Sum256([]byte(executionID))
	suffix := hex.EncodeToString(digest[:4])
	const separator = "-warden-"
	maxBase := 63 - len(separator) - len(suffix)
	base := strings.Trim(cronJobName, "-.")
	if len(base) > maxBase {
		base = strings.TrimRight(base[:maxBase], "-.")
	}
	if base == "" {
		base = "cronjob"
	}
	return base + separator + suffix
}

func copyStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}
