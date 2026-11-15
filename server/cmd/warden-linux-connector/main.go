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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gitee.com/pam-platform/warden/server/internal/connector"
	"gitee.com/pam-platform/warden/server/internal/domain"
)

var version = "0.2.9"

type state struct {
	PairingID      string   `json:"pairingId"`
	BootstrapToken string   `json:"bootstrapToken"`
	ConnectorID    string   `json:"connectorId"`
	Name           string   `json:"name"`
	Environment    string   `json:"environment"`
	SigningKey     string   `json:"signingKey"`
	Services       []string `json:"services"`
	Containers     []string `json:"containers"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	controlURL := strings.TrimRight(envOr("WARDEN_CONTROL_URL", "http://127.0.0.1:8080"), "/")
	statePath := envOr("WARDEN_LINUX_STATE_FILE", "/var/lib/warden-linux/state.json")
	current, err := loadState(statePath)
	if os.IsNotExist(err) {
		current, err = pair(ctx, controlURL, statePath)
	}
	if err != nil {
		log.Fatal(err)
	}
	hostname, _ := os.Hostname()
	machineID := readMachineID(hostname)
	key, err := base64.RawStdEncoding.DecodeString(current.SigningKey)
	if err != nil || len(key) < 32 {
		log.Fatal("invalid task signing key in state")
	}
	linuxRunner := connector.NewLinuxRunner(hostname, machineID, current.Services, current.Containers...)
	runner := connector.NewMultiRunner(linuxRunner, "linux_host", "systemd_service", "docker_container")
	capabilities := []string{domain.ChangeSafetyCapability, "linux.host.info@v1", "linux.process.list@v1", "linux.disk.usage@v1",
		"systemd.service.status@v1", "systemd.service.logs@v1", "systemd.service.start@v1", "systemd.service.stop@v1", "systemd.service.restart@v1",
		"docker.container.list@v1", "docker.container.inspect@v1", "docker.container.logs@v1", "docker.container.start@v1", "docker.container.stop@v1", "docker.container.restart@v1"}
	capabilities = append(capabilities, connector.JenkinsCapabilities...)
	capabilities = append(capabilities, connector.GrafanaCapabilities...)
	capabilities = append(capabilities, connector.DatabaseCapabilities...)
	capabilities = append(capabilities, connector.HuaweiCloudCapabilities...)
	capabilities = append(capabilities, connector.AliyunSLSCapabilities...)
	capabilities = append(capabilities, "provider.request@v1")
	capabilities = append(capabilities, "provider.write@v1")
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go monitorPairing(runCtx, cancel, controlURL, statePath, current, linuxRunner)
	runtime := connector.NewTypedRuntime(controlURL, current.Name, current.Environment, version, "linux", key,
		capabilities, runner)
	// Integrations (e.g. Jenkins) are pulled from the control plane and
	// materialized as runners on the shared MultiRunner. Credentials are read
	// from files under the Linux credential directory.
	runtime.SetIntegrationReconciler(connector.NewIntegrationReconciler(runner, readCredentialFile(), runtime.ReportIntegrationHealth))
	if err := runtime.Run(runCtx); err != nil && runCtx.Err() == nil {
		log.Fatal(err)
	}
}

func monitorPairing(ctx context.Context, cancel context.CancelFunc, controlURL, statePath string, current *state,
	linuxRunner *connector.LinuxRunner) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var response struct {
				Status string         `json:"status"`
				Config map[string]any `json:"config"`
			}
			if request(ctx, http.MethodGet, controlURL+"/api/v1/operator/pairings/"+current.PairingID,
				current.BootstrapToken, nil, &response) != nil {
				continue
			}
			if response.Status != "DELETE_REQUESTED" {
				services := stringSlice(response.Config["allowedServices"])
				containers := stringSlice(response.Config["allowedContainers"])
				if !sameStrings(current.Services, services) || !sameStrings(current.Containers, containers) {
					current.Services = services
					current.Containers = containers
					linuxRunner.SetScope(services, containers)
					_ = saveState(statePath, current)
				}
				continue
			}
			if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
				continue
			}
			if err := request(ctx, http.MethodPost, controlURL+"/api/v1/operator/pairings/"+current.PairingID+"/deleted",
				current.BootstrapToken, nil, nil); err != nil {
				continue
			}
			log.Printf("Warden Linux connector credentials removed; run sudo warden-linux-uninstall")
			cancel()
			return
		}
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, item := range left {
		seen[item]++
	}
	for _, item := range right {
		seen[item]--
		if seen[item] < 0 {
			return false
		}
	}
	return true
}

func pair(ctx context.Context, controlURL, statePath string) (*state, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	machineID := readMachineID(hostname)
	services := connector.DiscoverSystemdServices(ctx)
	containers := connector.DiscoverDockerContainers(ctx)
	payload := map[string]any{"machineId": machineID, "hostname": hostname, "services": services,
		"containers": containers, "metadata": connector.LinuxMachineMetadata(hostname, machineID)}
	var created struct {
		PairingID      string `json:"pairingId"`
		Code           string `json:"code"`
		BootstrapToken string `json:"bootstrapToken"`
	}
	if err := request(ctx, http.MethodPost, controlURL+"/api/v1/linux/pairings", "", payload, &created); err != nil {
		return nil, err
	}
	log.Printf("Warden Linux pairing code: %s", created.Code)
	log.Printf("Open Warden and choose Linux host onboarding; this code expires in 10 minutes")
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			var status struct {
				Status         string         `json:"status"`
				ConnectorID    string         `json:"connectorId"`
				TaskSigningKey string         `json:"taskSigningKey"`
				Config         map[string]any `json:"config"`
			}
			if err := request(ctx, http.MethodGet, controlURL+"/api/v1/operator/pairings/"+created.PairingID, created.BootstrapToken, nil, &status); err != nil {
				continue
			}
			if status.Status == "EXPIRED" || status.Status == "DELETED" {
				return nil, fmt.Errorf("pairing ended with status %s", status.Status)
			}
			if status.Status != "CONFIRMED" && status.Status != "READY" {
				continue
			}
			name, _ := status.Config["connectorName"].(string)
			environment, _ := status.Config["environment"].(string)
			selected := stringSlice(status.Config["allowedServices"])
			selectedContainers := stringSlice(status.Config["allowedContainers"])
			current := &state{PairingID: created.PairingID, BootstrapToken: created.BootstrapToken,
				ConnectorID: status.ConnectorID, Name: name, Environment: environment,
				SigningKey: status.TaskSigningKey, Services: selected, Containers: selectedContainers}
			if err := saveState(statePath, current); err != nil {
				return nil, err
			}
			_ = request(ctx, http.MethodPost, controlURL+"/api/v1/operator/pairings/"+created.PairingID+"/ready", created.BootstrapToken, nil, nil)
			return current, nil
		}
	}
}

// readCredentialFile returns a CredentialResolver that reads the API token
// from a file under the Linux credential directory, named by each integration's
// credential_ref.
func readCredentialFile() connector.CredentialResolver {
	credentialDir := envOr("WARDEN_LINUX_CREDENTIAL_DIR", "/etc/warden-linux/credentials")
	return func(ctx context.Context, credentialRef, _ string) (string, error) {
		if credentialRef == "" || filepath.Base(credentialRef) != credentialRef || strings.ContainsAny(credentialRef, `/\`) {
			return "", fmt.Errorf("invalid integration credential reference")
		}
		token, err := os.ReadFile(filepath.Join(credentialDir, credentialRef))
		if err != nil {
			return "", fmt.Errorf("read integration credential reference: %w", err)
		}
		return string(token), nil
	}
}

func request(ctx context.Context, method, url, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("control plane returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if output != nil {
		return json.NewDecoder(resp.Body).Decode(output)
	}
	return nil
}

func loadState(path string) (*state, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value state
	err = json.Unmarshal(data, &value)
	return &value, err
}
func saveState(path string, value *state) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
func readMachineID(fallback string) string {
	data, err := os.ReadFile("/etc/machine-id")
	if err == nil && strings.TrimSpace(string(data)) != "" {
		return strings.TrimSpace(string(data))
	}
	return fallback
}
func stringSlice(value any) []string {
	raw, _ := value.([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
