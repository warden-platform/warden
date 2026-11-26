package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"strings"
	"testing"
)

func TestServerInitializeListAndCall(t *testing.T) {
	tools := []Tool{
		{
			Name:        "echo",
			Description: "Echo input",
			InputSchema: objectSchema(map[string]any{
				"text": map[string]any{"type": "string"},
			}, []string{"text"}),
			Handler: func(_ context.Context, args map[string]any) (any, error) {
				return map[string]any{"echo": args["text"]}, nil
			},
		},
		{
			Name:        "bad",
			Description: "Always fails",
			InputSchema: objectSchema(nil, nil),
			Handler: func(context.Context, map[string]any) (any, error) {
				return nil, &ToolError{Code: "BAD", Message: "expected failure"}
			},
		},
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"bad","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"does/not/exist"}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	server := NewServer("warden-mcp", "test", "read-only", tools, strings.NewReader(input), &out, log.New(io.Discard, "", 0))
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d responses, want 5: %s", len(lines), out.String())
	}

	initialize := decodeResponse(t, lines[0])
	result := initialize["result"].(map[string]any)
	if got := result["protocolVersion"]; got != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want 2025-06-18", got)
	}
	serverInfo := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "warden-mcp" {
		t.Errorf("serverInfo.name = %v", serverInfo["name"])
	}

	list := decodeResponse(t, lines[1])
	listResult := list["result"].(map[string]any)
	listedTools := listResult["tools"].([]any)
	if len(listedTools) != 2 {
		t.Fatalf("tools/list returned %d tools, want 2", len(listedTools))
	}

	call := decodeResponse(t, lines[2])
	callResult := call["result"].(map[string]any)
	if callResult["isError"] != false {
		t.Errorf("echo isError = %v, want false", callResult["isError"])
	}
	text := firstText(t, callResult)
	if text != `{"echo":"hi"}` {
		t.Errorf("echo text = %s", text)
	}

	bad := decodeResponse(t, lines[3])
	badResult := bad["result"].(map[string]any)
	if badResult["isError"] != true {
		t.Errorf("bad isError = %v, want true", badResult["isError"])
	}
	if text := firstText(t, badResult); !strings.Contains(text, `"code":"BAD"`) {
		t.Errorf("bad text = %s", text)
	}

	unknown := decodeResponse(t, lines[4])
	unknownErr := unknown["error"].(map[string]any)
	if code := unknownErr["code"]; code != float64(-32601) {
		t.Errorf("unknown method code = %v, want -32601", code)
	}
}

func TestServerParseErrorAndNotificationOnly(t *testing.T) {
	var out bytes.Buffer
	server := NewServer("warden-mcp", "test", "", nil, strings.NewReader("not-json\n"), &out, log.New(io.Discard, "", 0))
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	response := decodeResponse(t, strings.TrimSpace(out.String()))
	errObj := response["error"].(map[string]any)
	if code := errObj["code"]; code != float64(-32700) {
		t.Errorf("parse error code = %v, want -32700", code)
	}

	out.Reset()
	server = NewServer("warden-mcp", "test", "", nil, strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"), &out, log.New(io.Discard, "", 0))
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run notification: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("notification should not produce a response, got %q", out.String())
	}
}

func decodeResponse(t *testing.T, line string) map[string]any {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		t.Fatalf("decode response %q: %v", line, err)
	}
	return response
}

func firstText(t *testing.T, result map[string]any) string {
	t.Helper()
	content := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("empty content")
	}
	return content[0].(map[string]any)["text"].(string)
}
