package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"gitee.com/pam-platform/pam/server/internal/connector"
	"gitee.com/pam-platform/pam/server/internal/domain"
)

var version = "0.1.0"

type state struct {
	PairingID       string   `json:"pairingId"`
	BootstrapToken  string   `json:"bootstrapToken"`
	Name            string   `json:"name"`
	Environment     string   `json:"environment"`
	SigningKey      string   `json:"signingKey"`
	AllowedCommands []string `json:"allowedCommands"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	controlURL := strings.TrimRight(envOr("PAM_CONTROL_URL", "https://127.0.0.1:8080"), "/")
	if err := validateControlURL(controlURL); err != nil {
		log.Fatal(err)
	}
	statePath := envOr("PAM_HOST_STATE_FILE", defaultStatePath())
	current, err := loadState(statePath)
	if os.IsNotExist(err) {
		current, err = pair(ctx, controlURL, statePath)
	}
	if err != nil {
		log.Fatal(err)
	}
	key, err := base64.RawStdEncoding.DecodeString(current.SigningKey)
	if err != nil || len(key) < 32 {
		log.Fatal("invalid task signing key in state")
	}
	hostname, _ := os.Hostname()
	platform := runtime.GOOS
	if platform == "darwin" {
		platform = "macos"
	}
	runner := connector.NewHostRunner(hostname, machineID(hostname), platform, runtime.GOARCH, current.AllowedCommands)
	rt := connector.NewTypedRuntime(controlURL, current.Name, current.Environment, version, platform, key, []string{domain.ChangeSafetyCapability, "host.command.run@v1"}, runner)
	if err := rt.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func pair(ctx context.Context, controlURL, statePath string) (*state, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	platform := runtime.GOOS
	if platform == "darwin" {
		platform = "macos"
	}
	machine := machineID(hostname)
	var created struct {
		PairingID      string `json:"pairingId"`
		Code           string `json:"code"`
		BootstrapToken string `json:"bootstrapToken"`
	}
	if err := request(ctx, http.MethodPost, controlURL+"/api/v1/host/pairings", "", map[string]any{"platform": platform, "machineId": machine, "hostname": hostname, "commands": connector.DiscoverHostCommands(), "metadata": map[string]any{"arch": runtime.GOARCH}}, &created); err != nil {
		return nil, err
	}
	log.Printf("PAM %s host pairing code: %s", platform, created.Code)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			var status struct {
				Status         string         `json:"status"`
				TaskSigningKey string         `json:"taskSigningKey"`
				Config         map[string]any `json:"config"`
			}
			if request(ctx, http.MethodGet, controlURL+"/api/v1/operator/pairings/"+created.PairingID, created.BootstrapToken, nil, &status) != nil {
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
			current := &state{PairingID: created.PairingID, BootstrapToken: created.BootstrapToken, Name: name, Environment: environment, SigningKey: status.TaskSigningKey, AllowedCommands: stringSlice(status.Config["allowedCommands"])}
			if err := saveState(statePath, current); err != nil {
				return nil, err
			}
			return current, nil
		}
	}
}

func validateControlURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid PAM_CONTROL_URL")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	if parsed.Scheme == "http" && (host == "127.0.0.1" || host == "localhost" || host == "::1") {
		return nil
	}
	return fmt.Errorf("PAM_CONTROL_URL must use HTTPS (HTTP is allowed only for localhost development)")
}
func machineID(hostname string) string {
	sum := sha256.Sum256([]byte(runtime.GOOS + ":" + runtime.GOARCH + ":" + hostname))
	return hex.EncodeToString(sum[:16])
}
func defaultStatePath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramData"), "pam-host", "state.json")
	}
	return "/var/lib/pam-host/state.json"
}
func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func stringSlice(value any) []string {
	raw, _ := value.([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
func request(ctx context.Context, method, target, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
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
