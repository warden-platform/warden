package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type operator struct {
	client          kubernetes.Interface
	http            *http.Client
	controlURL      string
	namespace       string
	operatorID      string
	connectorImage  string
	imagePullSecret string
}

type pairingBootstrap struct {
	PairingID      string    `json:"pairingId"`
	Code           string    `json:"code"`
	BootstrapToken string    `json:"bootstrapToken"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

type pairingStatus struct {
	Status         string         `json:"status"`
	ConnectorID    string         `json:"connectorId"`
	TaskSigningKey string         `json:"taskSigningKey"`
	Config         map[string]any `json:"config"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal(err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal(err)
	}
	namespace := envOr("PAM_OPERATOR_NAMESPACE", "pam-system")
	op := &operator{
		client: client, http: &http.Client{Timeout: 15 * time.Second},
		controlURL: strings.TrimRight(requiredEnv("PAM_CONTROL_URL"), "/"), namespace: namespace,
		operatorID:      envOr("PAM_OPERATOR_ID", "pam-operator-"+namespace),
		connectorImage:  requiredEnv("PAM_CONNECTOR_IMAGE"),
		imagePullSecret: envOr("PAM_IMAGE_PULL_SECRET", ""),
	}
	if err := op.run(ctx); err != nil {
		log.Fatal(err)
	}
}

func (o *operator) run(ctx context.Context) error {
	ns, err := o.client.CoreV1().Namespaces().Get(ctx, o.namespace, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read operator namespace: %w", err)
	}
	version, err := o.client.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("detect kubernetes version: %w", err)
	}
	audience := tokenAudienceForVersion(version.GitVersion)
	namespaceList, err := o.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list kubernetes namespaces: %w", err)
	}
	availableNamespaces := make([]string, 0, len(namespaceList.Items))
	for _, item := range namespaceList.Items {
		if item.Name != o.namespace && !strings.HasPrefix(item.Name, "kube-") {
			availableNamespaces = append(availableNamespaces, item.Name)
		}
	}
	sort.Strings(availableNamespaces)
	bootstrap, created, err := o.loadOrCreatePairing(ctx, string(ns.UID), version.GitVersion, audience, availableNamespaces)
	if err != nil {
		return err
	}
	if created || (bootstrap.Code != "" && time.Now().Before(bootstrap.ExpiresAt)) {
		log.Printf("PAM pairing code: %s (expires %s)", bootstrap.Code, bootstrap.ExpiresAt.Format(time.RFC3339))
	} else {
		log.Printf("resuming PAM pairing %s", bootstrap.PairingID)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(3 * time.Second):
			var status pairingStatus
			if err := o.get(ctx, "/api/v1/operator/pairings/"+bootstrap.PairingID, bootstrap.BootstrapToken, &status); err != nil {
				log.Printf("pairing status: %v", err)
				continue
			}
			switch status.Status {
			case "PENDING":
				continue
			case "EXPIRED", "DELETED":
				if err := o.client.CoreV1().Secrets(o.namespace).Delete(ctx, "pam-operator-state", metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("reset completed pairing state: %w", err)
				}
				bootstrap, _, err = o.loadOrCreatePairing(ctx, string(ns.UID), version.GitVersion, audience, availableNamespaces)
				if err != nil {
					return fmt.Errorf("create replacement pairing: %w", err)
				}
				log.Printf("PAM pairing code: %s (expires %s)", bootstrap.Code, bootstrap.ExpiresAt.Format(time.RFC3339))
				continue
			case "CONFIRMED", "READY", "DELETE_REQUESTED":
				return o.reconcileLoop(ctx, bootstrap, status, audience)
			default:
				return fmt.Errorf("unexpected pairing status %q", status.Status)
			}
		}
	}
}

func tokenAudienceForVersion(gitVersion string) string {
	return "https://kubernetes.default.svc.cluster.local"
}

func resolvedTokenAudience(configured, fallback string) string {
	configured = strings.TrimSpace(configured)
	// Older operators persisted this invalid generic audience into existing
	// managed-cluster pairings. Migrate that generated default while preserving
	// explicitly configured custom audiences.
	if configured == "https://kubernetes.default.svc" {
		return fallback
	}
	return configured
}

func (o *operator) loadOrCreatePairing(ctx context.Context, clusterUID, clusterVersion, audience string, namespaces []string) (pairingBootstrap, bool, error) {
	secret, err := o.client.CoreV1().Secrets(o.namespace).Get(ctx, "pam-operator-state", metav1.GetOptions{})
	if err == nil && len(secret.Data["pairingId"]) > 0 && len(secret.Data["bootstrapToken"]) > 0 {
		bootstrap := pairingBootstrap{PairingID: string(secret.Data["pairingId"]), BootstrapToken: string(secret.Data["bootstrapToken"]), Code: string(secret.Data["code"])}
		if value := string(secret.Data["expiresAt"]); value != "" {
			bootstrap.ExpiresAt, _ = time.Parse(time.RFC3339Nano, value)
		}
		return bootstrap, false, nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return pairingBootstrap{}, false, err
	}
	var bootstrap pairingBootstrap
	err = o.post(ctx, "/api/v1/operator/pairings", "", map[string]any{
		"operatorId": o.operatorID, "clusterUid": clusterUID, "clusterVersion": clusterVersion,
		"suggestedAudience": audience, "availableNamespaces": namespaces,
		"suggestedImagePrefix": imageRepositoryPrefix(o.connectorImage),
	}, &bootstrap)
	if err != nil {
		return pairingBootstrap{}, false, fmt.Errorf("create pairing: %w", err)
	}
	state := map[string][]byte{"pairingId": []byte(bootstrap.PairingID), "bootstrapToken": []byte(bootstrap.BootstrapToken), "code": []byte(bootstrap.Code), "expiresAt": []byte(bootstrap.ExpiresAt.Format(time.RFC3339Nano))}
	if err := upsertSecret(ctx, o.client, o.namespace, "pam-operator-state", state); err != nil {
		return pairingBootstrap{}, false, fmt.Errorf("persist pairing state: %w", err)
	}
	return bootstrap, true, nil
}

func (o *operator) reconcileLoop(ctx context.Context, bootstrap pairingBootstrap, status pairingStatus, audience string) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	readyReported := status.Status == "READY"
	for {
		if err := o.get(ctx, "/api/v1/operator/pairings/"+bootstrap.PairingID, bootstrap.BootstrapToken, &status); err != nil {
			log.Printf("pairing status: %v", err)
		} else if status.Status == "DELETE_REQUESTED" {
			if err := o.cleanup(ctx, status); err != nil {
				log.Printf("cleanup connector: %v", err)
			} else if err := o.post(ctx, "/api/v1/operator/pairings/"+bootstrap.PairingID+"/deleted", bootstrap.BootstrapToken, nil, nil); err != nil {
				log.Printf("mark pairing deleted: %v", err)
			} else {
				log.Printf("connector %s removed", status.ConnectorID)
				status.Status = "DELETED"
			}
			goto wait
		} else if status.Status == "DELETED" {
			goto wait
		}
		if err := o.reconcile(ctx, status, audience); err != nil {
			log.Printf("reconcile connector: %v", err)
		} else if !readyReported {
			if err := o.post(ctx, "/api/v1/operator/pairings/"+bootstrap.PairingID+"/ready", bootstrap.BootstrapToken, nil, nil); err != nil {
				log.Printf("mark pairing ready: %v", err)
			} else {
				readyReported = true
				log.Printf("cluster paired; connector %s installed", status.ConnectorID)
			}
		}
	wait:
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (o *operator) cleanup(ctx context.Context, pairing pairingStatus) error {
	if len(pairing.ConnectorID) < 8 {
		return fmt.Errorf("invalid connector id")
	}
	suffix := pairing.ConnectorID[:8]
	namespaces := stringsFromConfig(pairing.Config, "allowedNamespaces")
	for _, namespace := range namespaces {
		for _, name := range []string{"pam-token-issuer-" + suffix, "pam-executor-" + suffix, "pam-resource-discoverer-" + suffix} {
			if err := o.client.RbacV1().RoleBindings(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
		if err := o.client.RbacV1().ClusterRoleBindings().Delete(ctx, "pam-cluster-reader-executor-"+namespace+"-"+suffix, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	if err := o.client.RbacV1().ClusterRoleBindings().Delete(ctx, "pam-cluster-reader-discoverer-"+suffix, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	for _, deleteResource := range []func() error{
		func() error {
			return o.client.AppsV1().Deployments(o.namespace).Delete(ctx, "pam-connector-"+suffix, metav1.DeleteOptions{})
		},
		func() error {
			return o.client.CoreV1().Secrets(o.namespace).Delete(ctx, "pam-task-signing-"+suffix, metav1.DeleteOptions{})
		},
		func() error {
			return o.client.CoreV1().ServiceAccounts(o.namespace).Delete(ctx, "pam-connector-"+suffix, metav1.DeleteOptions{})
		},
		func() error {
			return o.client.RbacV1().RoleBindings(o.namespace).Delete(ctx, "pam-secret-reader-"+suffix, metav1.DeleteOptions{})
		},
		func() error {
			return o.client.RbacV1().Roles(o.namespace).Delete(ctx, "pam-secret-reader-"+suffix, metav1.DeleteOptions{})
		},
	} {
		if err := deleteResource(); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (o *operator) reconcile(ctx context.Context, pairing pairingStatus, fallbackAudience string) error {
	taskKey, err := base64.RawStdEncoding.DecodeString(pairing.TaskSigningKey)
	if err != nil || len(taskKey) < 32 {
		return fmt.Errorf("invalid connector task key")
	}
	namespaces := stringsFromConfig(pairing.Config, "allowedNamespaces")
	registries := stringsFromConfig(pairing.Config, "allowedImagePrefixes")
	if len(namespaces) == 0 {
		return fmt.Errorf("pairing has no allowed namespaces")
	}
	audience := resolvedTokenAudience(stringFromConfig(pairing.Config, "tokenAudience", fallbackAudience), fallbackAudience)
	executorSA := stringFromConfig(pairing.Config, "executionServiceAccount", "pam-executor")
	connectorName := "pam-connector-" + pairing.ConnectorID[:8]
	// Each connector gets an isolated runtime identity. Its Secret Role below
	// contains resourceNames for only this connector's Integration credentials.
	bootstrapSA := connectorName
	tokenIssuerRole := "pam-token-issuer-" + o.namespace
	executorRole := "pam-executor-" + o.namespace
	discovererRole := "pam-resource-discoverer-" + o.namespace
	clusterReaderRole := "pam-cluster-reader-" + o.namespace
	allowedSet := make(map[string]struct{}, len(namespaces))
	for _, namespace := range namespaces {
		allowedSet[namespace] = struct{}{}
	}
	allNamespaces, err := o.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, namespace := range allNamespaces.Items {
		if _, allowed := allowedSet[namespace.Name]; allowed {
			continue
		}
		for _, name := range []string{"pam-token-issuer-" + pairing.ConnectorID[:8], "pam-executor-" + pairing.ConnectorID[:8], "pam-resource-discoverer-" + pairing.ConnectorID[:8]} {
			if err := o.client.RbacV1().RoleBindings(namespace.Name).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
		if err := o.client.RbacV1().ClusterRoleBindings().Delete(ctx, "pam-cluster-reader-executor-"+namespace.Name+"-"+pairing.ConnectorID[:8], metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	if err := upsertSecret(ctx, o.client, o.namespace, "pam-task-signing-"+pairing.ConnectorID[:8], map[string][]byte{"key": taskKey}); err != nil {
		return err
	}
	if err := upsertServiceAccount(ctx, o.client, o.namespace, bootstrapSA); err != nil {
		return err
	}
	credentialRefs := stringsFromConfig(pairing.Config, "credentialRefs")
	sort.Strings(credentialRefs)
	secretRules := []rbacv1.PolicyRule{}
	if len(credentialRefs) > 0 {
		secretRules = append(secretRules, rbacv1.PolicyRule{
			APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: credentialRefs, Verbs: []string{"get"},
		})
	}
	secretRole := "pam-secret-reader-" + pairing.ConnectorID[:8]
	if err := upsertRole(ctx, o.client, o.namespace, secretRole, secretRules); err != nil {
		return err
	}
	if err := upsertRoleBinding(ctx, o.client, o.namespace, secretRole, secretRole, o.namespace, bootstrapSA); err != nil {
		return err
	}
	// Cluster-scoped read access (nodes, namespaces) for discovery + execution.
	// The ClusterRole is fixed and installed by the chart.
	if err := upsertClusterRoleBinding(ctx, o.client, "pam-cluster-reader-discoverer-"+pairing.ConnectorID[:8], clusterReaderRole, o.namespace, bootstrapSA); err != nil {
		return err
	}
	for _, namespace := range namespaces {
		if _, err := o.client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{}); err != nil {
			return fmt.Errorf("allowed namespace %s: %w", namespace, err)
		}
		if err := upsertServiceAccount(ctx, o.client, namespace, executorSA); err != nil {
			return err
		}
		// pam-token-issuer is a fixed ClusterRole installed by the chart. A
		// namespaced binding keeps token issuance limited to pam-executor in the
		// selected namespace without asking the operator to escalate a Role.
		if err := upsertRoleBindingToClusterRole(ctx, o.client, namespace, "pam-token-issuer-"+pairing.ConnectorID[:8], tokenIssuerRole, o.namespace, bootstrapSA); err != nil {
			return err
		}
		if err := upsertRoleBindingToClusterRole(ctx, o.client, namespace, "pam-executor-"+pairing.ConnectorID[:8], executorRole, namespace, executorSA); err != nil {
			return err
		}
		if err := upsertClusterRoleBinding(ctx, o.client, "pam-cluster-reader-executor-"+namespace+"-"+pairing.ConnectorID[:8], clusterReaderRole, namespace, executorSA); err != nil {
			return err
		}
		if err := upsertRoleBindingToClusterRole(ctx, o.client, namespace, "pam-resource-discoverer-"+pairing.ConnectorID[:8], discovererRole, o.namespace, bootstrapSA); err != nil {
			return err
		}
	}
	environment := map[string]string{
		"PAM_KUBERNETES_ENABLED": "true", "PAM_CONTROL_URL": o.controlURL,
		"PAM_CONNECTOR_NAME":        stringFromConfig(pairing.Config, "connectorName", connectorName),
		"PAM_CONNECTOR_ENVIRONMENT": stringFromConfig(pairing.Config, "environment", "testing"),
		"PAM_ALLOWED_NAMESPACES":    strings.Join(namespaces, ","), "PAM_ALLOWED_IMAGE_PREFIXES": strings.Join(registries, ","),
		"PAM_EXECUTION_SERVICE_ACCOUNT": executorSA, "PAM_KUBERNETES_TOKEN_AUDIENCE": audience,
		"PAM_CONNECTOR_NAMESPACE": o.namespace,
	}

	return upsertDeployment(ctx, o.client, o.namespace, connectorName, bootstrapSA, o.connectorImage, environment,
		nil, "pam-task-signing-"+pairing.ConnectorID[:8], o.imagePullSecret)
}

func (o *operator) get(ctx context.Context, path, token string, output any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.controlURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return o.do(req, output)
}

func (o *operator) post(ctx context.Context, path, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.controlURL+path, body)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return o.do(req, output)
}

func (o *operator) do(req *http.Request, output any) error {
	resp, err := o.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("control returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if output != nil {
		return json.NewDecoder(resp.Body).Decode(output)
	}
	return nil
}

func upsertSecret(ctx context.Context, client kubernetes.Interface, namespace, name string, data map[string][]byte) error {
	api := client.CoreV1().Secrets(namespace)
	current, err := api.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Data: data}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	current.Data = data
	_, err = api.Update(ctx, current, metav1.UpdateOptions{})
	return err
}
func upsertServiceAccount(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	api := client.CoreV1().ServiceAccounts(namespace)
	_, err := api.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, AutomountServiceAccountToken: boolPtr(false)}, metav1.CreateOptions{})
	}
	return err
}
func upsertRole(ctx context.Context, client kubernetes.Interface, namespace, name string, rules []rbacv1.PolicyRule) error {
	api := client.RbacV1().Roles(namespace)
	current, err := api.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Rules: rules}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	current.Rules = rules
	_, err = api.Update(ctx, current, metav1.UpdateOptions{})
	return err
}
func upsertRoleBinding(ctx context.Context, client kubernetes.Interface, namespace, name, role, subjectNamespace, subject string) error {
	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: role}, Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: subject, Namespace: subjectNamespace}}}
	api := client.RbacV1().RoleBindings(namespace)
	current, err := api.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, binding, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if current.RoleRef != binding.RoleRef {
		if err := api.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			return err
		}
		_, err = api.Create(ctx, binding, metav1.CreateOptions{})
		return err
	}
	current.RoleRef = binding.RoleRef
	current.Subjects = binding.Subjects
	_, err = api.Update(ctx, current, metav1.UpdateOptions{})
	return err
}
func upsertRoleBindingToClusterRole(ctx context.Context, client kubernetes.Interface, namespace, name, role, subjectNamespace, subject string) error {
	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: role}, Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: subject, Namespace: subjectNamespace}}}
	api := client.RbacV1().RoleBindings(namespace)
	current, err := api.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, binding, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if current.RoleRef != binding.RoleRef {
		if err := api.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			return err
		}
		_, err = api.Create(ctx, binding, metav1.CreateOptions{})
		return err
	}
	current.RoleRef = binding.RoleRef
	current.Subjects = binding.Subjects
	_, err = api.Update(ctx, current, metav1.UpdateOptions{})
	return err
}
func upsertClusterRoleBinding(ctx context.Context, client kubernetes.Interface, name, role, subjectNamespace, subject string) error {
	binding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name}, RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: role}, Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: subject, Namespace: subjectNamespace}}}
	api := client.RbacV1().ClusterRoleBindings()
	current, err := api.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, binding, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if current.RoleRef != binding.RoleRef {
		if err := api.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			return err
		}
		_, err = api.Create(ctx, binding, metav1.CreateOptions{})
		return err
	}
	current.RoleRef = binding.RoleRef
	current.Subjects = binding.Subjects
	_, err = api.Update(ctx, current, metav1.UpdateOptions{})
	return err
}
func upsertDeployment(ctx context.Context, client kubernetes.Interface, namespace, name, sa, image string, env map[string]string,
	extraEnv []corev1.EnvVar, secret, imagePullSecret string) error {
	labels := map[string]string{"app.kubernetes.io/name": "pam-connector", "pam.io/managed-by": "pam-operator"}
	values := make([]corev1.EnvVar, 0, len(env)+1)
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := env[key]
		values = append(values, corev1.EnvVar{Name: key, Value: value})
	}
	values = append(values, extraEnv...)
	values = append(values, corev1.EnvVar{Name: "PAM_TASK_SIGNING_KEY", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: "key"}}})
	replicas := int32(1)
	// Release jobs may republish a connector tag before the chart version is
	// bumped. Always resolve the tag when a Pod is created so a rollout cannot
	// silently reuse a stale node-local image with an obsolete capability set.
	podSpec := corev1.PodSpec{ServiceAccountName: sa, AutomountServiceAccountToken: boolPtr(true), Containers: []corev1.Container{{Name: "connector", Image: image, ImagePullPolicy: corev1.PullAlways, Env: values, SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: boolPtr(false), ReadOnlyRootFilesystem: boolPtr(true), RunAsNonRoot: boolPtr(true)}}}}
	if imagePullSecret != "" {
		podSpec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: imagePullSecret}}
	}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}, Spec: appsv1.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: labels}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: podSpec}}}
	api := client.AppsV1().Deployments(namespace)
	current, err := api.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, dep, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	current.Spec = dep.Spec
	_, err = api.Update(ctx, current, metav1.UpdateOptions{})
	return err
}
func stringsFromConfig(config map[string]any, key string) []string {
	switch raw := config[key].(type) {
	case []any:
		result := make([]string, 0, len(raw))
		for _, value := range raw {
			if text, ok := value.(string); ok && text != "" {
				result = append(result, text)
			}
		}
		return result
	case []string:
		result := make([]string, 0, len(raw))
		for _, value := range raw {
			if value != "" {
				result = append(result, value)
			}
		}
		return result
	default:
		return nil
	}
}
func stringFromConfig(config map[string]any, key, fallback string) string {
	if value, ok := config[key].(string); ok && value != "" {
		return value
	}
	return fallback
}
func boolPtr(value bool) *bool { return &value }
func imageRepositoryPrefix(image string) string {
	if at := strings.IndexByte(image, '@'); at >= 0 {
		image = image[:at]
	}
	if slash := strings.LastIndexByte(image, '/'); slash >= 0 {
		return image[:slash+1]
	}
	return ""
}
func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func requiredEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("%s is required", key)
	}
	return value
}
