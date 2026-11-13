package connector

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

type fakeH3CSession struct {
	output   []byte
	outputs  [][]byte
	err      error
	closed   bool
	command  string
	commands []string
}

func (s *fakeH3CSession) CombinedOutput(_ context.Context, command string) ([]byte, error) {
	s.command = command
	s.commands = append(s.commands, command)
	if len(s.outputs) > 0 {
		output := s.outputs[0]
		s.outputs = s.outputs[1:]
		return output, s.err
	}
	return s.output, s.err
}
func (s *fakeH3CSession) Close() error { s.closed = true; return nil }

const sampleH3COutput = `H3C Comware Software, Version 7.1.070
H3C S7503X-M uptime is 12 weeks, 1 day
GigabitEthernet1/0/1 UP 1G(a) F 100 uplink to core
GigabitEthernet1/0/2 DOWN 1G(a) F 200
Ten-GigabitEthernet1/0/49 ADM 10G(a) F 1 reserved
1 VLAN 0001                         active
100 VLAN 0100                       active
100 VLAN 0100                       active
4094 VLAN 4094                      active
`

func newTestH3CRunner(t *testing.T, session *fakeH3CSession) *H3CRunner {
	t.Helper()
	runner, err := NewH3CRunner(H3CConfig{
		Endpoint: "ssh://192.0.2.10:22", Username: "netops", DeviceName: "core-01", DeviceRole: "core",
		IntegrationID: "ig-1", AllowInterfaceChanges: true,
		Dial: func(context.Context) (h3cSSHSession, error) { return session, nil },
	})
	if err != nil {
		t.Fatalf("NewH3CRunner: %v", err)
	}
	return runner
}

const sampleH3CAccessInterface = `
interface GigabitEthernet1/0/2
 description office-201
 port link-type access
 port access vlan 100
#
GigabitEthernet1/0/2 UP 1G(a) F 100 office-201
`

const sampleH3CAccessInterfaceChanged = `
interface GigabitEthernet1/0/2
 description office-202
 port link-type access
 port access vlan 200
#
GigabitEthernet1/0/2 UP 1G(a) F 200 office-202
`

const sampleH3CTrunkInterface = `
interface Ten-GigabitEthernet1/0/49
 description distribution-01
 port link-type trunk
 port trunk permit vlan 1 100 200 to 202
#
Ten-GigabitEthernet1/0/49 UP 10G(a) F 1 distribution-01
`

const sampleH3CRoutedInterface = `
interface GigabitEthernet1/0/2
 port link-mode route
 ip address 192.0.2.1 255.255.255.0
#
GigabitEthernet1/0/2 UP 1G(a) F --
`

const sampleH3CHybridInterface = `
interface GigabitEthernet1/0/2
 port link-mode bridge
 port link-type hybrid
 port hybrid vlan 100 tagged
#
GigabitEthernet1/0/2 UP 1G(a) F 1
`

func h3cInterfaceTask(action string, parameters map[string]any) domain.TaskEnvelope {
	return domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "network_interface", ExternalID: "h3c/device/core-01/interface/GigabitEthernet1/0/2",
			Name: "GigabitEthernet1/0/2", Namespace: "core-01", IntegrationID: "ig-1"},
		Action: domain.TaskAction{Name: action, Version: 1}, Parameters: parameters,
	}
}

func TestH3CInterfacePreviewAndVerifiedMutation(t *testing.T) {
	previewRunner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CAccessInterface)})
	preview, _, err := previewRunner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.preview", map[string]any{
		"description": "office-202", "save": false,
	}))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	previewHash, _ := preview["previewHash"].(string)
	if len(previewHash) != 64 || preview["applied"] != false {
		t.Fatalf("unexpected preview: %#v", preview)
	}

	session := &fakeH3CSession{outputs: [][]byte{
		[]byte(sampleH3CAccessInterface), []byte("<core-01>\n"), []byte(sampleH3CAccessInterfaceChanged),
	}}
	runner := newTestH3CRunner(t, session)
	result, _, err := runner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.update", map[string]any{
		"description": "office-202", "save": false, "previewHash": previewHash,
	}))
	if err != nil {
		t.Fatalf("mutation: %v", err)
	}
	if result["applied"] != true || len(session.commands) != 3 {
		t.Fatalf("unexpected mutation result=%#v commands=%d", result, len(session.commands))
	}
	if !strings.Contains(session.commands[1], "description office-202") || strings.Contains(session.commands[1], "save force") {
		t.Fatalf("unexpected mutation command set: %q", session.commands[1])
	}
	if got, want := session.commands[1], "system-view\ninterface GigabitEthernet1/0/2\ndescription office-202\nquit\nquit\nquit\nquit\n"; got != want {
		t.Fatalf("mutation command sequence = %q, want %q", got, want)
	}
}

func TestH3CInterfaceMutationSavesOnlyWhenRequested(t *testing.T) {
	previewRunner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CAccessInterface)})
	preview, _, err := previewRunner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.preview", map[string]any{
		"description": "office-202", "save": true,
	}))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	session := &fakeH3CSession{outputs: [][]byte{
		[]byte(sampleH3CAccessInterface), []byte("<core-01>\n"), []byte(sampleH3CAccessInterfaceChanged),
	}}
	runner := newTestH3CRunner(t, session)
	_, _, err = runner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.update", map[string]any{
		"description": "office-202", "save": true, "previewHash": preview["previewHash"],
	}))
	if err != nil {
		t.Fatalf("mutation: %v", err)
	}
	if got, want := session.commands[1], "system-view\ninterface GigabitEthernet1/0/2\ndescription office-202\nquit\nquit\nsave force\nquit\nquit\n"; got != want {
		t.Fatalf("mutation command sequence = %q, want %q", got, want)
	}
}

func TestH3CInterfaceMutationRejectsStalePreview(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CAccessInterface)})
	_, _, err := runner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.update", map[string]any{
		"description": "office-202", "previewHash": strings.Repeat("0", 64),
	}))
	if err == nil || !strings.Contains(err.Error(), "PREVIEW_CONFLICT") {
		t.Fatalf("error = %v, want preview conflict", err)
	}
}

func TestH3CInterfaceMutationSkipsNoOp(t *testing.T) {
	previewRunner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CAccessInterface)})
	preview, _, err := previewRunner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.preview", map[string]any{
		"description": "office-201", "save": false,
	}))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview["noOp"] != true || !reflect.DeepEqual(preview["commands"], []string{}) {
		t.Fatalf("unexpected no-op preview: %#v", preview)
	}

	session := &fakeH3CSession{output: []byte(sampleH3CAccessInterface)}
	runner := newTestH3CRunner(t, session)
	result, _, err := runner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.update", map[string]any{
		"description": "office-201", "save": false, "previewHash": preview["previewHash"],
	}))
	if err != nil {
		t.Fatalf("no-op mutation: %v", err)
	}
	if result["noOp"] != true || result["applied"] != false || len(session.commands) != 1 {
		t.Fatalf("no-op mutation sent a write: result=%#v commands=%#v", result, session.commands)
	}
}

func TestH3CInterfaceMatchingStateWithSaveOnlyPersistsConfiguration(t *testing.T) {
	previewRunner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CAccessInterface)})
	preview, _, err := previewRunner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.preview", map[string]any{
		"description": "office-201", "save": true,
	}))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview["noOp"] != false || !reflect.DeepEqual(preview["commands"], []string{"save force", "quit", "quit"}) {
		t.Fatalf("unexpected save-only preview: %#v", preview)
	}

	session := &fakeH3CSession{outputs: [][]byte{
		[]byte(sampleH3CAccessInterface), []byte("<core-01>\n"), []byte(sampleH3CAccessInterface),
	}}
	runner := newTestH3CRunner(t, session)
	result, _, err := runner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.update", map[string]any{
		"description": "office-201", "save": true, "previewHash": preview["previewHash"],
	}))
	if err != nil {
		t.Fatalf("save-only mutation: %v", err)
	}
	if result["applied"] != true || len(session.commands) != 3 {
		t.Fatalf("unexpected save-only result=%#v commands=%#v", result, session.commands)
	}
	if got, want := session.commands[1], "save force\nquit\nquit\n"; got != want {
		t.Fatalf("save-only command sequence = %q, want %q", got, want)
	}
}

func TestH3CInterfaceVLANGuards(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CAccessInterface)})
	_, _, err := runner.Execute(context.Background(), h3cInterfaceTask("network.interface.access_vlan.preview", map[string]any{
		"vlan": float64(200), "allowDisruptive": false,
	}))
	if err == nil || !strings.Contains(err.Error(), "allowDisruptive=true") {
		t.Fatalf("error = %v, want disruption guard", err)
	}

	state, err := runner.parseInterfaceState("Ten-GigabitEthernet1/0/49", sampleH3CTrunkInterface)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.TrunkVLANs, []int{1, 100, 200, 201, 202}) {
		t.Fatalf("trunk VLANs = %#v", state.TrunkVLANs)
	}
	if _, err := runner.planInterfaceChange("network.interface.trunk_vlan.preview", map[string]any{
		"operation": "remove", "vlan": float64(200), "allowDisruptive": true,
	}, state); err != nil {
		t.Fatalf("valid trunk preview rejected: %v", err)
	}
}

func TestH3CInterfaceTrunkPlansUseComwareCommands(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{})
	state, err := runner.parseInterfaceState("Ten-GigabitEthernet1/0/49", sampleH3CTrunkInterface)
	if err != nil {
		t.Fatal(err)
	}
	add, err := runner.planInterfaceChange("network.interface.trunk_vlan.preview", map[string]any{
		"operation": "add", "vlan": float64(300),
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := add.Commands, []string{"port trunk permit vlan 300"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("trunk add commands = %#v, want %#v", got, want)
	}
	remove, err := runner.planInterfaceChange("network.interface.trunk_vlan.preview", map[string]any{
		"operation": "remove", "vlan": float64(200), "allowDisruptive": true,
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := remove.Commands, []string{"undo port trunk permit vlan 200"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("trunk remove commands = %#v, want %#v", got, want)
	}
}

func TestH3CInterfacePreviewReturnsCompleteDeviceCommandPlan(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CAccessInterface)})
	preview, _, err := runner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.preview", map[string]any{
		"description": "office-202", "save": true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"system-view", "interface GigabitEthernet1/0/2", "description office-202", "quit", "quit", "save force", "quit", "quit"}
	if got := preview["commands"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("preview commands = %#v, want %#v", got, want)
	}
}

func TestH3CInterfaceVLANNoOpsDoNotRequireRiskOverrides(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CAccessInterface)})
	access, err := runner.parseInterfaceState("GigabitEthernet1/0/2", sampleH3CAccessInterface)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := runner.planInterfaceChange("network.interface.access_vlan.preview", map[string]any{
		"vlan": float64(100), "allowDisruptive": false,
	}, access)
	if err != nil || !plan.NoOp || len(plan.Commands) != 0 {
		t.Fatalf("access no-op plan = %#v, err=%v", plan, err)
	}

	trunk, err := runner.parseInterfaceState("Ten-GigabitEthernet1/0/49", sampleH3CTrunkInterface)
	if err != nil {
		t.Fatal(err)
	}
	add, err := runner.planInterfaceChange("network.interface.trunk_vlan.preview", map[string]any{
		"operation": "add", "vlan": float64(200),
	}, trunk)
	if err != nil || !add.NoOp {
		t.Fatalf("trunk add no-op plan = %#v, err=%v", add, err)
	}
	remove, err := runner.planInterfaceChange("network.interface.trunk_vlan.preview", map[string]any{
		"operation": "remove", "vlan": float64(300), "allowDisruptive": false,
	}, trunk)
	if err != nil || !remove.NoOp {
		t.Fatalf("trunk remove no-op plan = %#v, err=%v", remove, err)
	}
}

func TestH3CInterfaceVLANChangesRejectRoutedAndHybridPorts(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{})
	routed, err := runner.parseInterfaceState("GigabitEthernet1/0/2", sampleH3CRoutedInterface)
	if err != nil {
		t.Fatal(err)
	}
	if routed.LayerMode != "route" {
		t.Fatalf("layer mode = %q, want route", routed.LayerMode)
	}
	if _, err := runner.planInterfaceChange("network.interface.access_vlan.preview", map[string]any{
		"vlan": float64(200), "allowDisruptive": true,
	}, routed); err == nil || !strings.Contains(err.Error(), "Layer 3") {
		t.Fatalf("routed port error = %v, want Layer 3 rejection", err)
	}

	hybrid, err := runner.parseInterfaceState("GigabitEthernet1/0/2", sampleH3CHybridInterface)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.planInterfaceChange("network.interface.access_vlan.preview", map[string]any{
		"vlan": float64(200), "allowDisruptive": true,
	}, hybrid); err == nil || !strings.Contains(err.Error(), "not an access port") {
		t.Fatalf("hybrid port error = %v, want access rejection", err)
	}
}

func TestH3CInterfaceStateRequiresExactConfiguration(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{})
	_, err := runner.parseInterfaceState("GigabitEthernet1/0/2", "GigabitEthernet1/0/2 UP 1G(a) F 100 office-201\n")
	if err == nil || !strings.Contains(err.Error(), "refusing to infer") {
		t.Fatalf("error = %v, want incomplete configuration rejection", err)
	}
}

func TestH3CInterfaceStateMatchesShortAndLongComwareNames(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{})
	output := `
interface GigabitEthernet0/0/20
 port link-type access
 port access vlan 1
#
GE0/0/20 DOWN 1G(a) A 1
`
	state, err := runner.parseInterfaceState("GE0/0/20", output)
	if err != nil {
		t.Fatalf("parse short/long interface names: %v", err)
	}
	if state.Name != "GE0/0/20" || state.LinkType != "access" || state.PVID != 1 {
		t.Fatalf("unexpected parsed state: %#v", state)
	}
}

func TestH3CInterfaceParsesTrunkAllWithExclusions(t *testing.T) {
	output := `
interface Ten-GigabitEthernet1/0/49
 port link-type trunk
 port trunk permit vlan all
 undo port trunk permit vlan 200
#
Ten-GigabitEthernet1/0/49 UP 10G(a) F 1
`
	runner := newTestH3CRunner(t, &fakeH3CSession{})
	state, err := runner.parseInterfaceState("Ten-GigabitEthernet1/0/49", output)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.TrunkVLANs) != 4093 || !containsInt(state.TrunkVLANs, 100) || containsInt(state.TrunkVLANs, 200) {
		t.Fatalf("unexpected trunk all parsing: count=%d has100=%v has200=%v", len(state.TrunkVLANs), containsInt(state.TrunkVLANs, 100), containsInt(state.TrunkVLANs, 200))
	}
}

func TestH3CInterfaceProtectedPortRequiresExplicitOverride(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CTrunkInterface)})
	runner.protectedInterfaces["ten-gigabitethernet1/0/49"] = true
	task := h3cInterfaceTask("network.interface.trunk_vlan.preview", map[string]any{
		"operation": "add", "vlan": float64(300),
	})
	task.Resource.ExternalID = "h3c/device/core-01/interface/Ten-GigabitEthernet1/0/49"
	task.Resource.Name = "Ten-GigabitEthernet1/0/49"
	_, _, err := runner.Execute(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "allowCritical=true") {
		t.Fatalf("error = %v, want protected-port guard", err)
	}
}

func TestH3CInterfaceProtectedPortDescriptionRequiresExplicitOverride(t *testing.T) {
	output := strings.Replace(sampleH3CAccessInterface, "office-201", "uplink-to-core", 2)
	runner := newTestH3CRunner(t, &fakeH3CSession{})
	state, err := runner.parseInterfaceState("GigabitEthernet1/0/2", output)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Critical {
		t.Fatal("uplink description was not classified as protected")
	}
	if _, err := runner.planInterfaceChange("network.interface.description.preview", map[string]any{
		"description": "office-202",
	}, state); err == nil || !strings.Contains(err.Error(), "allowCritical=true") {
		t.Fatalf("error = %v, want protected description guard", err)
	}
}

func TestH3CInterfaceResourceIdentityMismatchIsRejected(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CAccessInterface)})
	task := h3cInterfaceTask("network.interface.description.preview", map[string]any{"description": "office-202"})
	task.Resource.ExternalID = "h3c/device/other/interface/GigabitEthernet1/0/2"
	_, _, err := runner.Execute(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("error = %v, want resource identity mismatch", err)
	}
}

func TestH3CInterfaceMutationDisabledByDefault(t *testing.T) {
	previewRunner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CAccessInterface)})
	preview, _, err := previewRunner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.preview", map[string]any{"description": "office-202"}))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	runner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CAccessInterface)})
	runner.allowInterfaceChanges = false
	_, _, err = runner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.update", map[string]any{
		"description": "office-202", "previewHash": preview["previewHash"],
	}))
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("error = %v, want integration change switch rejection", err)
	}
}

func TestH3CInterfaceDescriptionRejectsCLIControlCharacters(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3CAccessInterface)})
	_, _, err := runner.Execute(context.Background(), h3cInterfaceTask("network.interface.description.preview", map[string]any{
		"description": "office-202\nsystem-view",
	}))
	if err == nil || !strings.Contains(err.Error(), "safe characters") {
		t.Fatalf("error = %v, want safe description rejection", err)
	}
}

func TestParseH3CInventory(t *testing.T) {
	inventory, err := parseH3CInventory(sampleH3COutput, time.Unix(123, 0).UTC())
	if err != nil {
		t.Fatalf("parseH3CInventory: %v", err)
	}
	if inventory.Model != "S7503X-M" || inventory.Software != "7.1.070" {
		t.Fatalf("unexpected facts: %#v", inventory)
	}
	if got := len(inventory.Interfaces); got != 3 {
		t.Fatalf("interfaces = %d, want 3", got)
	}
	if got, want := inventory.Interfaces[0].Description, "uplink to core"; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
	if got, want := inventory.VLANs, []int{1, 100, 4094}; !reflect.DeepEqual(got, want) {
		t.Errorf("VLANs = %#v, want %#v", got, want)
	}
}

func TestH3CRunnerDiscoveryAndReadOnlyExecution(t *testing.T) {
	session := &fakeH3CSession{output: []byte(sampleH3COutput)}
	runner := newTestH3CRunner(t, session)
	resources, complete := runner.ListResourcesComplete(context.Background())
	if !complete || len(resources) != 7 { // device + 3 interfaces + 3 VLANs
		t.Fatalf("discovery complete=%v resources=%d", complete, len(resources))
	}
	if !strings.Contains(session.command, "display interface brief") || strings.Contains(session.command, "configure") {
		t.Fatalf("unexpected command set: %q", session.command)
	}
	result, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "network_device", ExternalID: "h3c/device/core-01", IntegrationID: "ig-1"},
		Action:   domain.TaskAction{Name: "network.interface.list", Version: 1},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["count"] != 3 {
		t.Fatalf("result count = %#v", result["count"])
	}
}

func TestH3CReadOnlyOperationsUseBoundedCommands(t *testing.T) {
	for _, tc := range []struct {
		action string
		output string
		want   string
		value  int
	}{
		{"network.topology.lldp", "Neighbor index : 1\nChassis ID : 00:11\n<core-01>\n", "neighborCount", 1},
		{"network.link_aggregation.status", "Bridge-Aggregation1 Selected\nBridge-Aggregation2 Unselected\n<core-01>\n", "aggregationLineCount", 2},
		{"network.stp.status", "GE1/0/1 Forwarding\nGE1/0/2 Discarding\n<core-01>\n", "forwardingLineCount", 1},
	} {
		t.Run(tc.action, func(t *testing.T) {
			session := &fakeH3CSession{output: []byte(tc.output)}
			runner := newTestH3CRunner(t, session)
			result, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
				Resource: domain.TaskResource{Type: "network_device", ExternalID: "h3c/device/core-01", IntegrationID: "ig-1"},
				Action:   domain.TaskAction{Name: tc.action, Version: 1},
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result[tc.want] != tc.value {
				t.Fatalf("%s = %#v, want %d; result=%#v", tc.want, result[tc.want], tc.value, result)
			}
			if strings.Contains(session.command, "system-view") || strings.Contains(session.command, "save") {
				t.Fatalf("read-only operation contained a write command: %q", session.command)
			}
		})
	}
}

func TestSummarizeH3CAuditRedactsAndFindsRoleSignals(t *testing.T) {
	output := `<core>display current-configuration | include telnet server
telnet server enable
<core>display ntp-service status
Clock status: unsynchronized
<core>display current-configuration | include snmp-agent
snmp-agent community read secret-value
<core>display dhcp server pool
Pool name: office
<core>display lldp neighbor-information verbose
Neighbor 1
<core>`
	result := summarizeH3CAudit("core", []string{
		"display current-configuration | include telnet server",
		"display ntp-service status",
		"display current-configuration | include snmp-agent",
		"display dhcp server pool",
		"display lldp neighbor-information verbose",
	}, output)
	warnings, ok := result["warnings"].([]string)
	if !ok || !containsH3CTestString(warnings, "telnet_enabled") || !containsH3CTestString(warnings, "ntp_unsynchronized") {
		t.Fatalf("warnings = %#v", result["warnings"])
	}
	sections, ok := result["sections"].(map[string][]string)
	if !ok || len(sections["display dhcp server pool"]) != 1 {
		t.Fatalf("sections = %#v", result["sections"])
	}
	if got := sections["display current-configuration | include snmp-agent"]; len(got) != 1 || got[0] != "[REDACTED sensitive line]" {
		t.Fatalf("sensitive section = %#v", got)
	}
}

func TestH3CDeviceAuditUsesRoleProfileWithoutWriteCommands(t *testing.T) {
	session := &fakeH3CSession{output: []byte(sampleH3COutput)}
	runner := newTestH3CRunner(t, session)
	result, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "network_device", ExternalID: "h3c/device/core-01", IntegrationID: "ig-1"},
		Action:   domain.TaskAction{Name: "network.device.audit", Version: 1},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["profile"] != "core" {
		t.Fatalf("profile = %#v", result["profile"])
	}
	audit, ok := result["audit"].(map[string]any)
	if !ok || audit["profile"] != "core" {
		t.Fatalf("audit = %#v", result["audit"])
	}
	if strings.Contains(session.command, "system-view") || strings.Contains(session.command, "save") || strings.Contains(session.command, "undo ") {
		t.Fatalf("audit contained a write command: %q", session.command)
	}
}

func containsH3CTestString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func TestH3CDeviceHealthAndPortHealthAreDerivedFromInventory(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3COutput)})
	resource := domain.TaskResource{Type: "network_device", ExternalID: "h3c/device/core-01", IntegrationID: "ig-1"}
	for _, action := range []string{"network.device.health", "network.port.health"} {
		result, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{Resource: resource, Action: domain.TaskAction{Name: action, Version: 1}})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		interfaces, ok := result["interfaces"].(map[string]any)
		if action == "network.device.health" && (!ok || interfaces["up"] != 1 || interfaces["down"] != 1 || interfaces["administrativelyDown"] != 1) {
			t.Fatalf("unexpected health result: %#v", result)
		}
		if action == "network.port.health" {
			if _, ok := result["interfaces"].([]map[string]any); !ok {
				t.Fatalf("port health did not include interface details: %#v", result)
			}
		}
	}
}

func TestH3CLookupsValidateInputAndReturnDerivedMatches(t *testing.T) {
	tests := []struct {
		action string
		params map[string]any
		output string
		want   string
	}{
		{"network.mac.lookup", map[string]any{"mac": "000f-e2aa-bbcc"}, "000f-e2aa-bbcc 100 Learned GE1/0/2\n", "mac"},
		{"network.arp.lookup", map[string]any{"ip": "192.0.2.25"}, "192.0.2.25 000f-e2aa-bbcc 20 100 GE1/0/2\n", "ip"},
	}
	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			session := &fakeH3CSession{output: []byte(tc.output)}
			runner := newTestH3CRunner(t, session)
			result, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
				Resource: domain.TaskResource{Type: "network_device", ExternalID: "h3c/device/core-01", IntegrationID: "ig-1"},
				Action:   domain.TaskAction{Name: tc.action, Version: 1}, Parameters: tc.params,
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result[tc.want] != tc.params[tc.want] {
				t.Fatalf("lookup key %s = %#v, want %#v", tc.want, result[tc.want], tc.params[tc.want])
			}
			if result["count"] != 1 {
				t.Fatalf("count = %#v, want 1; result=%#v", result["count"], result)
			}
			if strings.Contains(session.command, "system-view") || strings.Contains(session.command, "save") {
				t.Fatalf("lookup contained a write command: %q", session.command)
			}
		})
	}
	invalid := []struct {
		action string
		params map[string]any
	}{
		{"network.mac.lookup", map[string]any{"mac": "not-a-mac"}},
		{"network.arp.lookup", map[string]any{"ip": "192.0.2.999"}},
	}
	for _, tc := range invalid {
		runner := newTestH3CRunner(t, &fakeH3CSession{output: []byte("<core-01>\n")})
		_, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
			Resource: domain.TaskResource{Type: "network_device", ExternalID: "h3c/device/core-01", IntegrationID: "ig-1"},
			Action:   domain.TaskAction{Name: tc.action, Version: 1}, Parameters: tc.params,
		})
		if err == nil {
			t.Fatalf("%s accepted invalid parameters %#v", tc.action, tc.params)
		}
	}
}

func TestNewH3CRunnerRejectsUnsafeConfiguration(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		finger   string
		want     string
	}{
		{"hostname", "ssh://switch.example:22", "SHA256:abc=", "fixed unicast IP"},
		{"missing fingerprint", "ssh://192.0.2.10:22", "", "fingerprint"},
		{"bad username", "ssh://192.0.2.10:22", "SHA256:abc=", "username"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			username := "netops"
			if tc.name == "bad username" {
				username = "net ops"
			}
			_, err := NewH3CRunner(H3CConfig{Endpoint: tc.endpoint, Username: username, PrivateKey: "x", HostKeySHA256: tc.finger, DeviceName: "d", DeviceRole: "core"})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestH3CRunnerDiscoveryFailureIsIncomplete(t *testing.T) {
	session := &fakeH3CSession{err: errors.New("timeout")}
	runner := newTestH3CRunner(t, session)
	resources, complete := runner.ListResourcesComplete(context.Background())
	if complete || resources != nil {
		t.Fatalf("failure returned resources=%#v complete=%v", resources, complete)
	}
}

func TestH3CPromptWriterSignalsOnlyComwarePrompt(t *testing.T) {
	w := &h3cPromptWriter{ready: make(chan struct{})}
	_, _ = w.Write([]byte("Copyright (c) 2004-2024 <not a prompt>\r\n"))
	select {
	case <-w.ready:
		t.Fatal("writer signaled on a non-Comware prompt")
	default:
	}
	_, _ = w.Write([]byte("<SCXXKJ-Core>\r\n"))
	select {
	case <-w.ready:
	default:
		t.Fatal("writer did not signal after Comware prompt")
	}
}

func TestH3CRunnerRejectsWrongResource(t *testing.T) {
	runner := newTestH3CRunner(t, &fakeH3CSession{output: []byte(sampleH3COutput)})
	_, _, err := runner.Execute(context.Background(), domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "network_device", ExternalID: "h3c/device/other", IntegrationID: "ig-1"},
		Action:   domain.TaskAction{Name: "network.device.get", Version: 1},
	})
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("error = %v", err)
	}
}
