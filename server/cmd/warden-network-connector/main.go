package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"gitee.com/warden-platform/warden/server/internal/connector"
)

var version = "0.2.0"

func main() {
	controlURL := requiredEnv("WARDEN_CONTROL_URL")
	signingKey := []byte(secretEnv("WARDEN_TASK_SIGNING_KEY"))
	if len(signingKey) < 32 {
		log.Fatal("WARDEN_TASK_SIGNING_KEY must contain at least 32 bytes")
	}
	name := envOr("WARDEN_CONNECTOR_NAME", "h3c-network-connector")
	environment := envOr("WARDEN_CONNECTOR_ENVIRONMENT", "testing")
	credentialDir := envOr("WARDEN_NETWORK_CREDENTIAL_DIR", "/var/lib/warden-network/credentials")

	// The Network Connector has no target-device credentials in its process
	// environment. It resolves a Warden-managed credential over the authenticated
	// connector channel, or reads a local file with a safe reference name.
	runner := connector.NewMultiRunner(nil)
	runtime := connector.NewTypedRuntime(controlURL, name, environment, version, "network", signingKey,
		append([]string{}, connector.H3CCapabilities...), runner)
	resolver := func(ctx context.Context, ref, integrationID string) (string, error) {
		if strings.HasPrefix(ref, "warden:") {
			return runtime.FetchCredentialValue(ctx, strings.TrimPrefix(ref, "warden:"), integrationID)
		}
		if strings.ContainsAny(ref, `/\\`) || ref == "." || ref == ".." {
			return "", fmt.Errorf("network credential reference is not a file name")
		}
		value, err := os.ReadFile(filepath.Join(credentialDir, ref))
		if err != nil {
			return "", fmt.Errorf("read network credential %s: %w", ref, err)
		}
		return string(value), nil
	}
	runtime.SetIntegrationReconciler(connector.NewIntegrationReconciler(runner, resolver, runtime.ReportIntegrationHealth))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := runtime.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func secretEnv(name string) string {
	value, file := strings.TrimSpace(os.Getenv(name)), strings.TrimSpace(os.Getenv(name+"_FILE"))
	if value != "" && file != "" {
		log.Fatalf("%s and %s_FILE cannot both be set", name, name)
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			log.Fatalf("read %s: %v", file, err)
		}
		value = strings.TrimSpace(string(data))
	}
	if value == "" {
		log.Fatalf("%s or %s_FILE is required", name, name)
	}
	return value
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
