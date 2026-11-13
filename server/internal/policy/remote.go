package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gitee.com/pam-platform/pam/server/internal/config"
)

type RemoteProvider struct {
	endpoint string
	token    string
	http     *http.Client
}

func NewRemoteProvider() (*RemoteProvider, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv("OPA_ENDPOINT")), "/")
	if endpoint == "" {
		endpoint = "http://localhost:8181"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("OPA_ENDPOINT must be an absolute URL without credentials, query, or fragment")
	}
	loopback := strings.EqualFold(parsed.Hostname(), "localhost")
	if ip := net.ParseIP(parsed.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) && !strings.EqualFold(os.Getenv("PAM_ALLOW_INSECURE_OPA"), "true") {
		return nil, fmt.Errorf("OPA_ENDPOINT must use HTTPS; set PAM_ALLOW_INSECURE_OPA=true only on a trusted private network")
	}
	token, err := config.Secret("PAM_OPA_BEARER_TOKEN")
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	return &RemoteProvider{endpoint: endpoint, token: token, http: client}, nil
}

func (p *RemoteProvider) Evaluate(ctx context.Context, input Input) (*Output, error) {
	body, err := json.Marshal(map[string]any{"input": input})
	if err != nil {
		return nil, fmt.Errorf("marshal opa input: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/v1/data/pam/kubernetes/decision", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create opa request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call opa: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opa returned status %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, 1<<20)
	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(limited).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode opa response: %w", err)
	}
	return decodeOutput(envelope.Result)
}

func decodeOutput(value map[string]any) (*Output, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal policy decision: %w", err)
	}
	var output Output
	if err := json.Unmarshal(b, &output); err != nil {
		return nil, fmt.Errorf("decode policy decision: %w", err)
	}
	if err := validateOutput(&output); err != nil {
		return nil, err
	}
	return &output, nil
}
