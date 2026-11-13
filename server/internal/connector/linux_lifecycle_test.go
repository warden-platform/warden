package connector

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

type fakeLinuxHelper struct {
	calls map[string]int
	live  map[string]bool
}

func (f *fakeLinuxHelper) Call(_ context.Context, request HelperRequest) (map[string]any, error) {
	f.calls[request.Action]++
	if request.Action == "docker.container.list" {
		containers := []any{}
		for id, live := range f.live {
			if live {
				containers = append(containers, map[string]any{"ID": id, "Names": "api", "Image": "api:v1", "State": "running", "Status": "Up"})
			}
		}
		return map[string]any{"containers": containers}, nil
	}
	if strings.HasPrefix(request.Action, "systemd.service.") {
		return map[string]any{"unit": request.Unit, "action": strings.TrimPrefix(request.Action, "systemd.service.")}, nil
	}
	if !f.live[request.Container] {
		return nil, fmt.Errorf("not found")
	}
	if request.Action == "docker.container.inspect" {
		return map[string]any{"id": request.Container, "name": "/api", "state": "running"}, nil
	}
	return map[string]any{"id": request.Container, "name": "/api", "state": "running"}, nil
}

func linuxLifecycleTask(resourceType, externalID, name, action, executionID string) domain.TaskEnvelope {
	return domain.TaskEnvelope{ExecutionID: executionID,
		Resource: domain.TaskResource{Type: resourceType, ExternalID: externalID, Name: name, Namespace: "host-a"},
		Action:   domain.TaskAction{Name: action}, Parameters: map[string]any{"name": name},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)}}
}

func TestLinuxLifecycleRequiresLiveDiscoveredAllowlistedTargetAndIsIdempotent(t *testing.T) {
	containerID := strings.Repeat("a", 64)
	notAllowedID := strings.Repeat("b", 64)
	helper := &fakeLinuxHelper{calls: map[string]int{}, live: map[string]bool{containerID: true, notAllowedID: true}}
	serviceLive := true
	mutationCalls := map[string]int{}
	runner := NewLinuxRunner("host-a", "machine-a", []string{"api.service"}, containerID)
	runner.helper = helper
	runner.command = func(_ context.Context, name string, args ...string) (string, error) {
		if name != "systemctl" {
			return "", nil
		}
		if len(args) > 0 && args[0] == "show" {
			if !serviceLive {
				return "", fmt.Errorf("not found")
			}
			return "Id=api.service\nDescription=API\nLoadState=loaded\nActiveState=active\nSubState=running\n", nil
		}
		mutationCalls[args[0]]++
		return "", nil
	}

	resources := runner.ListResources(context.Background())
	if _, ok := findDiscoveredResource(resources, domain.TaskResource{Type: "systemd_service", ExternalID: "machine-a/api.service"}); !ok {
		t.Fatal("allowlisted live systemd service was not discovered")
	}
	if _, ok := findDiscoveredResource(resources, domain.TaskResource{Type: "docker_container", ExternalID: "machine-a/docker/" + containerID}); !ok {
		t.Fatal("allowlisted live Docker container was not discovered")
	}
	listResult, _, err := runner.Execute(context.Background(), linuxLifecycleTask("linux_host", "machine-a", "host-a", "docker.container.list", "list"))
	if err != nil || len(listResult["containers"].([]any)) != 1 {
		t.Fatalf("Docker list leaked a non-allowlisted container: result=%#v err=%v", listResult, err)
	}

	serviceTask := linuxLifecycleTask("systemd_service", "machine-a/api.service", "api.service", "systemd.service.restart", "execution-systemd")
	for attempt := 0; attempt < 2; attempt++ {
		result, _, err := runner.Execute(context.Background(), serviceTask)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 1 && result["idempotentReplay"] != true {
			t.Fatalf("duplicate result=%#v", result)
		}
	}
	if helper.calls["systemd.service.restart"] != 1 {
		t.Fatalf("restart calls=%d, want 1", helper.calls["systemd.service.restart"])
	}

	dockerTask := linuxLifecycleTask("docker_container", "machine-a/docker/"+containerID, "api", "docker.container.restart", "execution-docker")
	for attempt := 0; attempt < 2; attempt++ {
		if _, _, err := runner.Execute(context.Background(), dockerTask); err != nil {
			t.Fatal(err)
		}
	}
	if helper.calls["docker.container.restart"] != 1 {
		t.Fatalf("Docker restart calls=%d, want 1", helper.calls["docker.container.restart"])
	}

	serviceLive = false
	vanished := linuxLifecycleTask("systemd_service", "machine-a/api.service", "api.service", "systemd.service.start", "vanished")
	if _, _, err := runner.Execute(context.Background(), vanished); err == nil || !strings.Contains(err.Error(), "no longer discovered") {
		t.Fatalf("vanished service error=%v", err)
	}
	helper.live[containerID] = false
	if _, _, err := runner.Execute(context.Background(), linuxLifecycleTask("docker_container", "machine-a/docker/"+containerID, "api", "docker.container.start", "vanished-docker")); err == nil || !strings.Contains(err.Error(), "no longer discovered") {
		t.Fatalf("vanished container error=%v", err)
	}
}

func TestLinuxLifecycleRejectsOutOfScopeAndInjectedNames(t *testing.T) {
	containerID := strings.Repeat("a", 64)
	runner := NewLinuxRunner("host-a", "machine-a", []string{"api.service"}, containerID)
	runner.helper = &fakeLinuxHelper{calls: map[string]int{}, live: map[string]bool{containerID: true}}
	runner.command = func(_ context.Context, _ string, _ ...string) (string, error) {
		return "Id=api.service\nLoadState=loaded\n", nil
	}

	tests := []domain.TaskEnvelope{
		linuxLifecycleTask("systemd_service", "machine-a/api.service;reboot", "api.service;reboot", "systemd.service.restart", "inject-systemd"),
		linuxLifecycleTask("systemd_service", "machine-a/other.service", "other.service", "systemd.service.restart", "scope-systemd"),
		linuxLifecycleTask("docker_container", "machine-a/docker/abc123;rm", "api", "docker.container.restart", "inject-docker"),
		linuxLifecycleTask("docker_container", "machine-a/docker/not-allowed", "api", "docker.container.restart", "scope-docker"),
	}
	for _, task := range tests {
		if _, _, err := runner.Execute(context.Background(), task); err == nil {
			t.Fatalf("unsafe task was accepted: %#v", task.Resource)
		}
	}
}

func TestHelperRejectsLifecycleNameInjectionAndUnsupportedDockerOperations(t *testing.T) {
	for _, request := range []HelperRequest{
		{Action: "systemd.service.restart", Unit: "safe.service;reboot"},
		{Action: "docker.container.restart", Container: "safe;docker rm all"},
		{Action: "docker.container.exec", Container: "abc123"},
		{Action: "docker.container.remove", Container: "abc123"},
	} {
		if _, err := executeHelperRequest(context.Background(), request); err == nil {
			t.Fatalf("unsafe helper request was accepted: %#v", request)
		}
	}
}
