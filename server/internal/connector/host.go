package connector

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

const (
	HostMachineResource = "host_machine"
	HostCommandAction   = "host.command.run"
	maxHostArgs         = 64
	maxHostArgLength    = 4096
	maxHostOutput       = 256 * 1024
)

type HostRunner struct {
	mu        sync.RWMutex
	hostname  string
	machineID string
	platform  string
	arch      string
	allowed   map[string]struct{}
	command   func(context.Context, string, ...string) ([]byte, []byte, int, error)
}

func NewHostRunner(hostname, machineID, platform, arch string, allowed []string) *HostRunner {
	r := &HostRunner{hostname: hostname, machineID: machineID, platform: platform, arch: arch, allowed: map[string]struct{}{}, command: runHostCommand}
	r.SetAllowedCommands(allowed)
	return r
}

func (r *HostRunner) SetAllowedCommands(commands []string) {
	allowed := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		if normalized, ok := normalizeHostProgram(command); ok {
			allowed[normalized] = struct{}{}
		}
	}
	r.mu.Lock()
	r.allowed = allowed
	r.mu.Unlock()
}

func (r *HostRunner) ListResources(context.Context) []DiscoveredResource {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return []DiscoveredResource{{ExternalID: r.machineID, Name: r.hostname, ResourceType: HostMachineResource,
		Labels: map[string]any{"os": r.platform, "arch": r.arch}, Annotations: map[string]any{},
		Metadata: map[string]any{"hostname": r.hostname, "machineId": r.machineID, "os": r.platform, "arch": r.arch}}}
}

func (r *HostRunner) Execute(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	if task.Resource.Type != HostMachineResource || task.Resource.ExternalID != r.machineID {
		return nil, Lease{}, fmt.Errorf("host resource is outside connector scope")
	}
	if task.Action.Name != HostCommandAction {
		return nil, Lease{}, fmt.Errorf("unsupported host action %q", task.Action.Name)
	}
	program, _ := task.Parameters["program"].(string)
	normalized, ok := normalizeHostProgram(program)
	if !ok {
		return nil, Lease{}, fmt.Errorf("invalid host program")
	}
	r.mu.RLock()
	_, allowed := r.allowed[normalized]
	r.mu.RUnlock()
	if !allowed {
		return nil, Lease{}, fmt.Errorf("program is not allowed")
	}
	rawArgs, _ := task.Parameters["args"].([]any)
	args := make([]string, 0, len(rawArgs))
	if len(rawArgs) > maxHostArgs {
		return nil, Lease{}, fmt.Errorf("too many command arguments")
	}
	for _, raw := range rawArgs {
		arg, ok := raw.(string)
		if !ok || len(arg) > maxHostArgLength || strings.IndexByte(arg, 0) >= 0 {
			return nil, Lease{}, fmt.Errorf("invalid command argument")
		}
		args = append(args, arg)
	}
	started := time.Now()
	stdout, stderr, exitCode, err := r.command(ctx, normalized, args...)
	result := map[string]any{"program": normalized, "argsCount": len(args), "exitCode": exitCode,
		"stdout": string(limitHostOutput(stdout)), "stderr": string(limitHostOutput(stderr)), "durationMs": time.Since(started).Milliseconds()}
	if len(stdout) > maxHostOutput || len(stderr) > maxHostOutput {
		result["outputTruncated"] = true
	}
	return result, Lease{}, err
}

func normalizeHostProgram(program string) (string, bool) {
	if program == "" || len(program) > 128 || filepath.Base(program) != program || strings.Contains(program, "..") || strings.ContainsAny(program, `/\\;|&$'\"\x00`) {
		return "", false
	}
	return strings.ToLower(program), true
}

func limitHostOutput(value []byte) []byte {
	if len(value) <= maxHostOutput {
		return value
	}
	return value[:maxHostOutput]
}

func runHostCommand(ctx context.Context, program string, args ...string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Env = minimalHostEnv()
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return []byte(stdout.String()), []byte(stderr.String()), exitCode, err
}

func minimalHostEnv() []string {
	keep := map[string]bool{"PATH": true, "SystemRoot": true, "TEMP": true, "TMP": true, "HOME": true, "LANG": true, "LC_ALL": true}
	env := make([]string, 0)
	for _, item := range os.Environ() {
		key := item
		if index := strings.IndexByte(item, '='); index >= 0 {
			key = item[:index]
		}
		if keep[key] {
			env = append(env, item)
		}
	}
	return env
}

func DiscoverHostCommands() []string {
	commands := map[string][]string{"windows": {"hostname.exe", "systeminfo.exe", "tasklist.exe"}, "darwin": {"sw_vers", "hostname", "ps", "df"}}[runtime.GOOS]
	result := make([]string, 0, len(commands))
	for _, command := range commands {
		if _, err := exec.LookPath(command); err == nil {
			result = append(result, command)
		}
	}
	sort.Strings(result)
	return result
}
