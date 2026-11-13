package connector

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const DefaultLinuxHelperSocket = "/run/pam-linux/helper.sock"

type HelperRequest struct {
	Action    string `json:"action"`
	Container string `json:"container,omitempty"`
	Unit      string `json:"unit,omitempty"`
	Lines     int    `json:"lines,omitempty"`
}

type HelperResponse struct {
	Result map[string]any `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type LinuxHelperClient struct{ socket string }

type linuxHelper interface {
	Call(context.Context, HelperRequest) (map[string]any, error)
}

func NewLinuxHelperClient(socket string) *LinuxHelperClient {
	if socket == "" {
		socket = DefaultLinuxHelperSocket
	}
	return &LinuxHelperClient{socket: socket}
}

func (c *LinuxHelperClient) Call(ctx context.Context, request HelperRequest) (map[string]any, error) {
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, fmt.Errorf("connect Linux helper: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	} else {
		_ = connection.SetDeadline(time.Now().Add(30 * time.Second))
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return nil, err
	}
	var response HelperResponse
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&response); err != nil {
		return nil, err
	}
	if response.Error != "" {
		return nil, fmt.Errorf("Linux helper: %s", response.Error)
	}
	return response.Result, nil
}

func ServeLinuxHelper(ctx context.Context, socket string) error {
	if socket == "" {
		socket = DefaultLinuxHelperSocket
	}
	if err := os.MkdirAll(strings.TrimSuffix(socket, "/helper.sock"), 0750); err != nil {
		return err
	}
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0660); err != nil {
		return err
	}
	go func() { <-ctx.Done(); _ = listener.Close() }()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go handleHelperConnection(ctx, connection)
	}
}

func handleHelperConnection(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(30 * time.Second))
	var request HelperRequest
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&request); err != nil {
		_ = json.NewEncoder(connection).Encode(HelperResponse{Error: "invalid structured request"})
		return
	}
	result, err := executeHelperRequest(ctx, request)
	response := HelperResponse{Result: result}
	if err != nil {
		response.Result = nil
		response.Error = err.Error()
	}
	_ = json.NewEncoder(connection).Encode(response)
}

func executeHelperRequest(ctx context.Context, request HelperRequest) (map[string]any, error) {
	switch request.Action {
	case "systemd.service.start", "systemd.service.stop", "systemd.service.restart":
		if !validSystemdUnit(request.Unit) {
			return nil, fmt.Errorf("invalid systemd unit")
		}
		verb := strings.TrimPrefix(request.Action, "systemd.service.")
		if _, err := helperCommand(ctx, "systemctl", verb, "--", request.Unit); err != nil {
			return nil, err
		}
		output, err := helperCommand(ctx, "systemctl", "show", request.Unit, "--no-pager",
			"--property=Id,Description,LoadState,ActiveState,SubState,MainPID,ExecMainStartTimestamp")
		if err != nil {
			return nil, err
		}
		return map[string]any{"unit": request.Unit, "action": verb, "properties": keyValueOutput(output)}, nil
	case "docker.container.list":
		output, err := helperCommand(ctx, "docker", "ps", "--all", "--no-trunc", "--format", "{{json .}}")
		if err != nil {
			return nil, err
		}
		containers := make([]map[string]any, 0)
		for _, line := range lines(output) {
			item := map[string]any{}
			if json.Unmarshal([]byte(line), &item) == nil {
				containers = append(containers, item)
			}
		}
		return map[string]any{"containers": containers}, nil
	case "docker.container.inspect":
		if !validDockerContainerID(request.Container) {
			return nil, fmt.Errorf("invalid container identifier")
		}
		output, err := helperCommand(ctx, "docker", "inspect", "--format", `{"id":{{json .Id}},"name":{{json .Name}},"image":{{json .Config.Image}},"state":{{json .State.Status}},"startedAt":{{json .State.StartedAt}},"restartCount":{{json .RestartCount}}}`, request.Container)
		if err != nil {
			return nil, err
		}
		result := map[string]any{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
			return nil, fmt.Errorf("decode docker inspect result: %w", err)
		}
		return result, nil
	case "docker.container.logs":
		if !validDockerContainerID(request.Container) {
			return nil, fmt.Errorf("invalid container identifier")
		}
		count := request.Lines
		if count <= 0 {
			count = 200
		}
		if count > 1000 {
			count = 1000
		}
		output, err := helperCommand(ctx, "docker", "logs", "--tail", strconv.Itoa(count), "--timestamps", request.Container)
		if err != nil {
			return nil, err
		}
		if len(output) > 1024*1024 {
			output = output[len(output)-1024*1024:]
		}
		return map[string]any{"container": request.Container, "logs": output, "lines": count}, nil
	case "docker.container.start", "docker.container.stop", "docker.container.restart":
		if !validDockerContainerID(request.Container) {
			return nil, fmt.Errorf("invalid container identifier")
		}
		verb := strings.TrimPrefix(request.Action, "docker.container.")
		if _, err := helperCommand(ctx, "docker", verb, request.Container); err != nil {
			return nil, err
		}
		// Return a structured, live state. The caller and change-safety layer also
		// independently re-read the catalog after the operation.
		output, err := helperCommand(ctx, "docker", "inspect", "--format", `{"id":{{json .Id}},"name":{{json .Name}},"image":{{json .Config.Image}},"state":{{json .State.Status}},"startedAt":{{json .State.StartedAt}},"restartCount":{{json .RestartCount}}}`, request.Container)
		if err != nil {
			return nil, err
		}
		result := map[string]any{"action": verb}
		if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
			return nil, fmt.Errorf("decode docker lifecycle result: %w", err)
		}
		result["action"] = verb
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported helper action")
	}
}

func validDockerContainer(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if !(ch == '.' || ch == '-' || ch == '_' || ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z') {
			return false
		}
	}
	return true
}

func validDockerContainerID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func helperCommand(ctx context.Context, name string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s read failed: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
