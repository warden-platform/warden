package connector

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

type LinuxRunner struct {
	mu                sync.RWMutex
	lifecycleMu       sync.Mutex
	hostname          string
	machineID         string
	allowedServices   map[string]struct{}
	allowedContainers map[string]struct{}
	helper            linuxHelper
	command           func(context.Context, string, ...string) (string, error)
	completed         map[string]map[string]any
}

func (r *LinuxRunner) SetScope(services, containers []string) {
	allowed := make(map[string]struct{}, len(services))
	for _, service := range services {
		if validSystemdUnit(service) {
			allowed[service] = struct{}{}
		}
	}
	allowedContainers := make(map[string]struct{}, len(containers))
	for _, container := range containers {
		if validDockerContainerID(container) {
			allowedContainers[container] = struct{}{}
		}
	}
	r.mu.Lock()
	r.allowedServices = allowed
	r.allowedContainers = allowedContainers
	r.mu.Unlock()
}

func (r *LinuxRunner) SetServices(services []string) { r.SetScope(services, r.containerScope()) }

func (r *LinuxRunner) containerScope() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]string, 0, len(r.allowedContainers))
	for item := range r.allowedContainers {
		result = append(result, item)
	}
	return result
}

func NewLinuxRunner(hostname, machineID string, services []string, containers ...string) *LinuxRunner {
	allowed := make(map[string]struct{}, len(services))
	for _, service := range services {
		if validSystemdUnit(service) {
			allowed[service] = struct{}{}
		}
	}
	allowedContainers := make(map[string]struct{}, len(containers))
	for _, container := range containers {
		if validDockerContainerID(container) {
			allowedContainers[container] = struct{}{}
		}
	}
	return &LinuxRunner{hostname: hostname, machineID: machineID, allowedServices: allowed,
		allowedContainers: allowedContainers, helper: NewLinuxHelperClient(""), command: fixedCommand,
		completed: map[string]map[string]any{}}
}

func (r *LinuxRunner) ListResources(ctx context.Context) []DiscoveredResource {
	r.mu.RLock()
	defer r.mu.RUnlock()
	resources := []DiscoveredResource{{ExternalID: r.machineID, Name: r.hostname, ResourceType: "linux_host",
		Labels: map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH}, Annotations: map[string]any{},
		Metadata: LinuxMachineMetadata(r.hostname, r.machineID)}}
	services := make([]string, 0, len(r.allowedServices))
	for service := range r.allowedServices {
		services = append(services, service)
	}
	sort.Strings(services)
	for _, service := range services {
		properties, ok := r.systemdState(ctx, service)
		if !ok {
			continue
		}
		resources = append(resources, DiscoveredResource{ExternalID: r.machineID + "/" + service,
			Name: service, Namespace: r.hostname, ResourceType: "systemd_service", Labels: map[string]any{},
			Annotations: map[string]any{}, Metadata: map[string]any{"host": r.hostname, "unit": service, "properties": properties}})
	}
	if result, err := r.helper.Call(ctx, HelperRequest{Action: "docker.container.list"}); err == nil {
		if containers, ok := result["containers"].([]any); ok {
			for _, raw := range containers {
				container, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				name := strings.TrimPrefix(fmt.Sprint(container["Names"]), "/")
				id := fmt.Sprint(container["ID"])
				if !validDockerContainer(name) || !validDockerContainerID(id) {
					continue
				}
				if _, allowed := r.allowedContainers[id]; !allowed {
					continue
				}
				resources = append(resources, DiscoveredResource{ExternalID: r.machineID + "/docker/" + id,
					Name: name, Namespace: r.hostname, ResourceType: "docker_container", Labels: map[string]any{"state": container["State"]},
					Annotations: map[string]any{}, Metadata: map[string]any{"containerId": id, "image": container["Image"], "status": container["Status"]}})
			}
		}
	}
	return resources
}

func (r *LinuxRunner) Execute(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	if task.Resource.Type == "linux_host" && task.Resource.ExternalID != r.machineID {
		return nil, Lease{}, fmt.Errorf("host resource is outside connector scope")
	}
	if task.Resource.Type == "systemd_service" {
		r.mu.RLock()
		_, allowed := r.allowedServices[task.Resource.Name]
		r.mu.RUnlock()
		if !allowed || !validSystemdUnit(task.Resource.Name) || task.Resource.ExternalID != r.machineID+"/"+task.Resource.Name {
			return nil, Lease{}, fmt.Errorf("systemd unit is outside connector scope")
		}
		if _, found := r.systemdState(ctx, task.Resource.Name); !found {
			return nil, Lease{}, fmt.Errorf("systemd unit is no longer discovered")
		}
	}
	containerID := ""
	if task.Resource.Type == "docker_container" {
		containerID = strings.TrimPrefix(task.Resource.ExternalID, r.machineID+"/docker/")
		r.mu.RLock()
		_, allowed := r.allowedContainers[containerID]
		r.mu.RUnlock()
		if !allowed || !validDockerContainerID(containerID) || task.Resource.ExternalID != r.machineID+"/docker/"+containerID {
			return nil, Lease{}, fmt.Errorf("Docker container is outside connector scope")
		}
		current, err := r.helper.Call(ctx, HelperRequest{Action: "docker.container.inspect", Container: containerID})
		if err != nil || fmt.Sprint(current["id"]) != containerID {
			return nil, Lease{}, fmt.Errorf("Docker container is no longer discovered")
		}
	}
	switch task.Action.Name {
	case "linux.host.info":
		return r.hostInfo(ctx)
	case "linux.process.list":
		limit := boundedInt(task.Parameters["limit"], 50, 1, 200)
		output, err := r.command(ctx, "ps", "-eo", "pid,ppid,user,%cpu,%mem,stat,comm", "--sort=-%cpu")
		return map[string]any{"processes": firstLines(output, limit+1), "limit": limit}, Lease{}, err
	case "linux.disk.usage":
		output, err := r.command(ctx, "df", "-P", "-T")
		return map[string]any{"filesystems": lines(output)}, Lease{}, err
	case "systemd.service.status":
		output, err := r.command(ctx, "systemctl", "show", task.Resource.Name, "--no-pager",
			"--property=Id,Description,LoadState,ActiveState,SubState,MainPID,ExecMainStartTimestamp")
		return map[string]any{"unit": task.Resource.Name, "properties": keyValueOutput(output)}, Lease{}, err
	case "systemd.service.logs":
		count := boundedInt(task.Parameters["lines"], 200, 1, 1000)
		output, err := r.command(ctx, "journalctl", "--unit", task.Resource.Name, "--lines", strconv.Itoa(count), "--no-pager", "--output", "short-iso")
		return map[string]any{"unit": task.Resource.Name, "logs": output, "lines": count}, Lease{}, err
	case "systemd.service.start", "systemd.service.stop", "systemd.service.restart":
		if task.Resource.Type != "systemd_service" {
			return nil, Lease{}, fmt.Errorf("action requires a systemd service resource")
		}
		return r.lifecycleOnce(ctx, task, func() (map[string]any, error) {
			return r.helper.Call(ctx, HelperRequest{Action: task.Action.Name, Unit: task.Resource.Name})
		})
	case "docker.container.list":
		result, err := r.helper.Call(ctx, HelperRequest{Action: task.Action.Name})
		if err != nil {
			return nil, Lease{}, err
		}
		return r.filterAllowedContainers(result), Lease{}, nil
	case "docker.container.inspect":
		result, err := r.helper.Call(ctx, HelperRequest{Action: task.Action.Name, Container: containerID})
		return result, Lease{}, err
	case "docker.container.logs":
		count := boundedInt(task.Parameters["lines"], 200, 1, 1000)
		result, err := r.helper.Call(ctx, HelperRequest{Action: task.Action.Name, Container: containerID, Lines: count})
		return result, Lease{}, err
	case "docker.container.start", "docker.container.stop", "docker.container.restart":
		if task.Resource.Type != "docker_container" {
			return nil, Lease{}, fmt.Errorf("action requires a Docker container resource")
		}
		return r.lifecycleOnce(ctx, task, func() (map[string]any, error) {
			return r.helper.Call(ctx, HelperRequest{Action: task.Action.Name, Container: containerID})
		})
	default:
		return nil, Lease{}, fmt.Errorf("unsupported Linux action %q", task.Action.Name)
	}
}

func (r *LinuxRunner) filterAllowedContainers(result map[string]any) map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	filtered := make([]any, 0)
	containers, _ := result["containers"].([]any)
	for _, raw := range containers {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, allowed := r.allowedContainers[fmt.Sprint(item["ID"])]; allowed {
			filtered = append(filtered, item)
		}
	}
	return map[string]any{"containers": filtered}
}

func (r *LinuxRunner) systemdState(ctx context.Context, unit string) (map[string]string, bool) {
	output, err := r.command(ctx, "systemctl", "show", unit, "--no-pager",
		"--property=Id,Description,LoadState,ActiveState,SubState,MainPID,ExecMainStartTimestamp")
	if err != nil {
		return nil, false
	}
	properties := keyValueOutput(output)
	return properties, properties["LoadState"] == "loaded" && properties["Id"] == unit
}

func (r *LinuxRunner) lifecycleOnce(ctx context.Context, task domain.TaskEnvelope, run func() (map[string]any, error)) (map[string]any, Lease, error) {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	key := task.ExecutionID + ":" + task.Action.Name + ":" + task.Resource.ExternalID
	r.mu.RLock()
	cached := r.completed[key]
	r.mu.RUnlock()
	if cached != nil {
		result := cloneAnyMap(cached)
		result["idempotentReplay"] = true
		return result, Lease{}, nil
	}
	result, err := run()
	if err != nil {
		return nil, Lease{}, err
	}
	r.mu.Lock()
	if len(r.completed) >= 1024 {
		r.completed = map[string]map[string]any{}
	}
	r.completed[key] = cloneAnyMap(result)
	r.mu.Unlock()
	return result, Lease{}, nil
}

func cloneAnyMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (r *LinuxRunner) hostInfo(ctx context.Context) (map[string]any, Lease, error) {
	kernel, kernelErr := r.command(ctx, "uname", "-srvm")
	uptime, uptimeErr := r.command(ctx, "uptime", "-p")
	if kernelErr != nil {
		return nil, Lease{}, kernelErr
	}
	if uptimeErr != nil {
		return nil, Lease{}, uptimeErr
	}
	result := LinuxMachineMetadata(r.hostname, r.machineID)
	result["kernel"] = strings.TrimSpace(kernel)
	result["uptime"] = strings.TrimSpace(uptime)
	return result, Lease{}, nil
}

func LinuxMachineMetadata(hostname, machineID string) map[string]any {
	metadata := map[string]any{"hostname": hostname, "machineId": machineID, "os": runtime.GOOS, "arch": runtime.GOARCH}
	if file, err := os.Open("/etc/os-release"); err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			key, value, ok := strings.Cut(scanner.Text(), "=")
			if ok && (key == "ID" || key == "VERSION_ID" || key == "PRETTY_NAME") {
				metadata[strings.ToLower(key)] = strings.Trim(strings.TrimSpace(value), "\"")
			}
		}
	}
	return metadata
}

func DiscoverSystemdServices(ctx context.Context) []string {
	output, err := fixedCommand(ctx, "systemctl", "list-unit-files", "--type=service", "--state=enabled", "--no-legend", "--no-pager")
	if err != nil {
		return []string{}
	}
	var services []string
	for _, line := range lines(output) {
		fields := strings.Fields(line)
		if len(fields) > 0 && validSystemdUnit(fields[0]) {
			services = append(services, fields[0])
		}
	}
	sort.Strings(services)
	return services
}

func DiscoverDockerContainers(ctx context.Context) []string {
	result, err := NewLinuxHelperClient("").Call(ctx, HelperRequest{Action: "docker.container.list"})
	if err != nil {
		return []string{}
	}
	var containers []string
	rawContainers, ok := result["containers"].([]any)
	if !ok {
		return []string{}
	}
	for _, raw := range rawContainers {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := fmt.Sprint(item["ID"])
		if validDockerContainerID(id) {
			containers = append(containers, id)
		}
	}
	sort.Strings(containers)
	return containers
}

func validSystemdUnit(value string) bool {
	if !strings.HasSuffix(value, ".service") || len(value) > 255 {
		return false
	}
	for _, ch := range value {
		if !(ch == '.' || ch == '-' || ch == '_' || ch == '@' || ch == ':' || ch == '\\' || ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z') {
			return false
		}
	}
	return true
}

func fixedCommand(ctx context.Context, name string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func lines(value string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return []string{}
	}
	return strings.Split(trimmed, "\n")
}

func firstLines(value string, count int) []string {
	items := lines(value)
	if len(items) > count {
		return items[:count]
	}
	return items
}

func keyValueOutput(value string) map[string]string {
	result := map[string]string{}
	for _, line := range lines(value) {
		if key, item, ok := strings.Cut(line, "="); ok {
			result[key] = item
		}
	}
	return result
}

func boundedInt(value any, fallback, minimum, maximum int) int {
	number := fallback
	switch typed := value.(type) {
	case float64:
		number = int(typed)
	case int:
		number = typed
	case json.Number:
		if parsed, err := strconv.Atoi(typed.String()); err == nil {
			number = parsed
		}
	}
	if number < minimum {
		return minimum
	}
	if number > maximum {
		return maximum
	}
	return number
}
