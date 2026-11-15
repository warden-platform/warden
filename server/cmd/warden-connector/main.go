package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"gitee.com/pam-platform/warden/server/internal/connector"
	"gitee.com/pam-platform/warden/server/internal/domain"
)

var version = "0.3.2"

func main() {
	if os.Getenv("WARDEN_KUBERNETES_ENABLED") != "true" {
		log.Fatal("WARDEN_KUBERNETES_ENABLED=true is required; refusing to access Kubernetes implicitly")
	}
	namespaces := splitRequired("WARDEN_ALLOWED_NAMESPACES")
	registries := splitRequired("WARDEN_ALLOWED_IMAGE_PREFIXES")
	key := []byte(os.Getenv("WARDEN_TASK_SIGNING_KEY"))
	if len(key) < 32 {
		log.Fatal("WARDEN_TASK_SIGNING_KEY must contain at least 32 bytes")
	}
	serviceAccount := envOr("WARDEN_EXECUTION_SERVICE_ACCOUNT", "warden-executor")
	audience := envOr("WARDEN_KUBERNETES_TOKEN_AUDIENCE", "https://kubernetes.default.svc")
	credentials, err := connector.NewInClusterTokenRequestProvider(serviceAccount, audience)
	if err != nil {
		log.Fatal(err)
	}
	kubernetesRunner, err := connector.NewKubernetesRunner(credentials, namespaces, registries)
	if err != nil {
		log.Fatal(err)
	}
	runner := connector.NewMultiRunner(kubernetesRunner, connector.KubernetesResourceTypes...)
	capabilities := append(append(append(append(append(append([]string{}, connector.KubernetesCapabilities...), connector.JenkinsCapabilities...), connector.GrafanaCapabilities...), connector.DatabaseCapabilities...), connector.HuaweiCloudCapabilities...), connector.AliyunSLSCapabilities...)
	capabilities = append(capabilities, domain.ChangeSafetyCapability)
	capabilities = append(capabilities, "provider.request@v1")
	capabilities = append(capabilities, "provider.write@v1")
	runtime := connector.NewTypedRuntime(
		envOr("WARDEN_CONTROL_URL", "http://warden-control:8080"),
		envOr("WARDEN_CONNECTOR_NAME", "kubernetes-connector"), envOr("WARDEN_CONNECTOR_ENVIRONMENT", "testing"),
		version, "kubernetes", key, capabilities, runner,
	)
	// Integrations (e.g. Jenkins) are pulled from the control plane and
	// materialized as runners on the shared MultiRunner. Credentials are read
	// from K8s Secrets in the connector namespace via the bootstrap SA (which
	// the operator binds to the warden-secret-reader Role), or resolved from the
	// Warden-managed credential ledger when the reference uses the "pam:" prefix.
	secretNamespace := envOr("WARDEN_CONNECTOR_NAMESPACE", "warden-system")
	resolver := func(ctx context.Context, ref, integrationID string) (string, error) {
		if strings.HasPrefix(ref, "pam:") {
			return runtime.FetchCredentialValue(ctx, strings.TrimPrefix(ref, "pam:"), integrationID)
		}
		return readSecretToken(credentials.BootstrapClient(), secretNamespace)(ctx, ref, integrationID)
	}
	runtime.SetIntegrationReconciler(connector.NewIntegrationReconciler(runner, resolver, runtime.ReportIntegrationHealth))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := runtime.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

// readSecretToken returns a CredentialResolver that reads the "token" key from
// the K8s Secret named by each integration's credential_ref.
func readSecretToken(client kubernetes.Interface, namespace string) connector.CredentialResolver {
	return func(ctx context.Context, credentialRef, _ string) (string, error) {
		secret, err := client.CoreV1().Secrets(namespace).Get(ctx, credentialRef, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("read secret %s/%s: %w", namespace, credentialRef, err)
		}
		token, ok := secret.Data["token"]
		if !ok || len(token) == 0 {
			return "", fmt.Errorf("secret %s/%s has no token key", namespace, credentialRef)
		}
		return string(token), nil
	}
}

func splitRequired(name string) []string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	parts := strings.Split(value, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
