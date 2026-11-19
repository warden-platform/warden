package ai

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gitee.com/warden-platform/warden/server/internal/config"
)

type Config struct {
	Enabled       bool
	Model         string
	BaseURL       string
	APIKey        string
	Timeout       time.Duration
	MaxTokens     int
	MaxIterations int
}

func LoadConfig() (*Config, error) {
	enabled := true
	if raw := strings.TrimSpace(os.Getenv("WARDEN_AI_ENABLED")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("WARDEN_AI_ENABLED must be a boolean")
		}
		enabled = value
	}
	apiKey, err := config.Secret("WARDEN_AI_API_KEY")
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		Enabled: enabled, Model: strings.TrimSpace(os.Getenv("WARDEN_AI_MODEL")),
		BaseURL: strings.TrimSpace(os.Getenv("WARDEN_AI_BASE_URL")), APIKey: apiKey,
		Timeout: 90 * time.Second, MaxTokens: 1600, MaxIterations: 8,
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if raw := strings.TrimSpace(os.Getenv("WARDEN_AI_TIMEOUT")); raw != "" {
		cfg.Timeout, err = time.ParseDuration(raw)
		if err != nil || cfg.Timeout < 5*time.Second || cfg.Timeout > 5*time.Minute {
			return nil, fmt.Errorf("WARDEN_AI_TIMEOUT must be between 5s and 5m")
		}
	}
	if raw := strings.TrimSpace(os.Getenv("WARDEN_AI_MAX_TOKENS")); raw != "" {
		cfg.MaxTokens, err = strconv.Atoi(raw)
		if err != nil || cfg.MaxTokens < 128 || cfg.MaxTokens > 8192 {
			return nil, fmt.Errorf("WARDEN_AI_MAX_TOKENS must be between 128 and 8192")
		}
	}
	if (cfg.Model == "") != (cfg.APIKey == "") {
		return nil, fmt.Errorf("WARDEN_AI_MODEL and WARDEN_AI_API_KEY must be configured together")
	}
	return cfg, nil
}

func (c *Config) Configured() bool {
	return c != nil && c.Enabled && c.Model != "" && c.APIKey != ""
}
