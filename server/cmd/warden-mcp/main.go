package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"gitee.com/warden-platform/warden/server/internal/mcp"
)

const version = "0.1.0"

const instructions = "Warden is an action-level authorization platform. This adapter is read-only in its first release: use warden_list_resources and warden_list_resource_actions to discover targets, then warden_get_execution / warden_wait_execution to read execution status. No write, approval or raw shell/SQL tools are exposed."

func main() {
	logger := log.New(os.Stderr, "warden-mcp: ", 0)

	endpoint := strings.TrimSpace(os.Getenv("WARDEN_API_ENDPOINT"))
	apiKey := strings.TrimSpace(os.Getenv("WARDEN_AGENT_API_KEY"))
	if endpoint == "" || apiKey == "" {
		logger.Fatal("WARDEN_API_ENDPOINT 和 WARDEN_AGENT_API_KEY 必须配置")
	}

	requestTimeout := durationFromEnv("WARDEN_MCP_TIMEOUT_SECONDS", 30)
	client, err := mcp.NewClient(endpoint, apiKey, requestTimeout)
	if err != nil {
		logger.Fatal(err)
	}

	maxWait := durationFromEnv("WARDEN_MCP_MAX_WAIT_SECONDS", 60)

	startupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	identity, err := client.Me(startupCtx)
	cancel()
	if err != nil {
		logger.Fatalf("Warden 认证失败: %v", err)
	}

	tools := mcp.ReadOnlyTools(client, identity.Capabilities, maxWait)
	if len(tools) == 0 {
		logger.Println("当前 API Key 没有 warden:resources:read 或 warden:executions:read:self，MCP 工具列表为空")
	}

	server := mcp.NewServer("warden-mcp", version, instructions, tools, os.Stdin, os.Stdout, logger)
	if err := server.Run(context.Background()); err != nil {
		logger.Fatalf("运行失败: %v", err)
	}
}

func durationFromEnv(name string, fallbackSeconds int) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return time.Duration(fallbackSeconds) * time.Second
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return time.Duration(fallbackSeconds) * time.Second
	}
	return time.Duration(value) * time.Second
}
