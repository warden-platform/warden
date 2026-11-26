package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
)

const DefaultProtocolVersion = "2025-06-18"

var supportedProtocolVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Server is a minimal MCP stdio server. It only speaks newline-delimited
// JSON-RPC over stdin/stdout; logs must go to stderr.
type Server struct {
	Name         string
	Version      string
	Instructions string
	Tools        []Tool
	In           io.Reader
	Out          io.Writer
	Log          *log.Logger
}

func NewServer(name, version, instructions string, tools []Tool, in io.Reader, out io.Writer, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	if tools == nil {
		tools = []Tool{}
	}
	return &Server{
		Name:         name,
		Version:      version,
		Instructions: instructions,
		Tools:        tools,
		In:           in,
		Out:          out,
		Log:          logger,
	}
}

func (s *Server) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(s.In)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	encoder := json.NewEncoder(s.Out)
	encoder.SetEscapeHTML(false)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		response := s.handleLine(ctx, []byte(line))
		if response == nil {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	return nil
}

func (s *Server) handleLine(ctx context.Context, line []byte) *rpcResponse {
	var request rpcRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return errorResponse(json.RawMessage("null"), -32700, "Parse error")
	}
	if request.JSONRPC != "2.0" {
		return errorResponse(idOrNull(request.ID), -32600, "Invalid Request")
	}
	if isNotification(request.ID) {
		// MCP notifications (initialized/cancelled/progress) carry no response.
		return nil
	}

	switch request.Method {
	case "initialize":
		return s.handleInitialize(request)
	case "ping":
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{}}
	case "tools/list":
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"tools": s.Tools}}
	case "tools/call":
		return s.handleToolCall(ctx, request)
	default:
		return errorResponse(request.ID, -32601, "Method not found")
	}
}

func (s *Server) handleInitialize(request rpcRequest) *rpcResponse {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
		ClientInfo      any    `json:"clientInfo"`
	}
	if len(request.Params) > 0 && string(request.Params) != "null" {
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return errorResponse(request.ID, -32602, "Invalid params")
		}
	}
	protocolVersion := strings.TrimSpace(params.ProtocolVersion)
	if !supportedProtocolVersions[protocolVersion] {
		protocolVersion = DefaultProtocolVersion
	}
	result := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    s.Name,
			"version": s.Version,
		},
	}
	if s.Instructions != "" {
		result["instructions"] = s.Instructions
	}
	return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result}
}

func (s *Server) handleToolCall(ctx context.Context, request rpcRequest) *rpcResponse {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if len(request.Params) == 0 || string(request.Params) == "null" {
		return errorResponse(request.ID, -32602, "Invalid params")
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return errorResponse(request.ID, -32602, "Invalid params")
	}
	tool := s.findTool(params.Name)
	if tool == nil {
		return errorResponse(request.ID, -32602, "Unknown tool")
	}

	result, err := tool.Handler(ctx, params.Arguments)
	if err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: toolResult(toolErrorText(err), true)}
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: toolResult(`{"error":{"code":"INTERNAL","message":"工具结果序列化失败"}}`, true)}
	}
	return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: toolResult(string(payload), false)}
}

func (s *Server) findTool(name string) *Tool {
	for i := range s.Tools {
		if s.Tools[i].Name == name {
			return &s.Tools[i]
		}
	}
	return nil
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"isError": isError,
	}
}

func toolErrorText(err error) string {
	var toolErr *ToolError
	code := "UNAVAILABLE"
	message := "工具执行失败"
	if errors.As(err, &toolErr) {
		code = toolErr.Code
		message = toolErr.Message
	}
	payload, marshalErr := json.Marshal(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
	if marshalErr != nil {
		return `{"error":{"code":"INTERNAL","message":"工具错误序列化失败"}}`
	}
	return string(payload)
}

func errorResponse(id json.RawMessage, code int, message string) *rpcResponse {
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      idOrNull(id),
		Error:   &rpcError{Code: code, Message: message},
	}
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func isNotification(id json.RawMessage) bool {
	return len(id) == 0 || string(id) == "null"
}
