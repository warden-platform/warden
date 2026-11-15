package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

var H3CResourceTypes = []string{"network_device", "network_interface", "network_vlan"}

var H3CCapabilities = []string{
	domain.ChangeSafetyCapability,
	"network.device.get@v1",
	"network.device.audit@v1",
	"network.device.health@v1",
	"network.interface.list@v1",
	"network.vlan.list@v1",
	"network.topology.lldp@v1",
	"network.link_aggregation.status@v1",
	"network.stp.status@v1",
	"network.port.health@v1",
	"network.interface.description.preview@v1",
	"network.interface.description.update@v1",
	"network.interface.access_vlan.preview@v1",
	"network.interface.access_vlan.update@v1",
	"network.interface.trunk_vlan.preview@v1",
	"network.interface.trunk_vlan.add@v1",
	"network.interface.trunk_vlan.remove@v1",
}

var (
	h3cEndpointPattern = regexp.MustCompile(`^ssh://([^/:]+):([0-9]{1,5})$`)
	h3cUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,80}$`)
	h3cInterfaceLine   = regexp.MustCompile(`(?i)^\s*(\S+)\s+(UP|DOWN|ADM)\s+(.+?)\s+(\d+)(?:\s+(.+?))?\s*$`)
	h3cVLANLine        = regexp.MustCompile(`(?i)^\s*(\d{1,4})\s+VLAN\s+`)
	h3cModelPattern    = regexp.MustCompile(`(?i)H3C\s+(\S+)\s+uptime`)
	h3cSoftwarePattern = regexp.MustCompile(`(?i)H3C Comware Software, Version\s+(.+)`)
	h3cPromptPattern   = regexp.MustCompile(`(?m)<[A-Za-z0-9][A-Za-z0-9_.-]{0,79}>\s*$`)
	h3cMACPattern      = regexp.MustCompile(`(?i)([0-9a-f]{4}[-.]?){2}[0-9a-f]{4}|([0-9a-f]{2}[:-]){5}[0-9a-f]{2}`)
	h3cIPv4Pattern     = regexp.MustCompile(`^((25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])\.){3}(25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])$`)
	h3cPortPattern     = regexp.MustCompile(`(?i)^(?:GE|XGE|FGE|GigabitEthernet|Ten-GigabitEthernet|FortyGigE|BAGG|Bridge-Aggregation)[0-9/]+$`)
)

var h3cDeviceRoles = map[string]bool{"ac": true, "core": true, "aggregation": true, "poe": true, "router": true, "switch": true}

// These commands are intentionally kept separate from discovery. A device
// that supports the base inventory can still be discovered even when an
// optional status command is unavailable, while each action reports its own
// bounded read failure. No user supplied text is interpolated into commands.
var h3cReadOnlyOperationCommands = map[string]string{
	"network.topology.lldp":           "screen-length disable\ndisplay lldp neighbor-information list\nquit\nquit\n",
	"network.link_aggregation.status": "screen-length disable\ndisplay link-aggregation summary\nquit\nquit\n",
	"network.stp.status":              "screen-length disable\ndisplay stp brief\nquit\nquit\n",
	"network.mac.lookup":              "screen-length disable\ndisplay mac-address\nquit\nquit\n",
	"network.arp.lookup":              "screen-length disable\ndisplay arp\nquit\nquit\n",
}

// Role-specific audit profiles mirror the fixed, read-only checks maintained
// by the h3c-network-ops skill.  They deliberately contain only literal
// Comware commands: no user supplied text is ever interpolated into a CLI
// session.  The base inventory commands remain the default profile.
var h3cAuditProfileCommands = map[string][]string{
	"standard": {},
	"security": {
		"display ntp-service status",
		"display startup",
		"display current-configuration | include ntp",
		"display current-configuration | include info-center",
		"display current-configuration | include snmp-agent",
		"display current-configuration | include ssh server",
		"display current-configuration | include telnet server",
		"display current-configuration | include ip http",
		"display current-configuration | include ip https",
	},
	"core": {
		"display link-aggregation verbose Bridge-Aggregation1",
		"display link-aggregation verbose Bridge-Aggregation2",
		"display link-aggregation verbose Bridge-Aggregation3",
		"display dhcp server statistics",
		"display dhcp server conflict",
		"display dhcp server pool",
		"display dhcp server ip-in-use",
		"display current-configuration | include ntp",
		"display current-configuration | include info-center",
		"display current-configuration | include snmp-agent",
		"display current-configuration | include ssh server",
		"display current-configuration | include telnet server",
		"display current-configuration | include ip http",
		"display current-configuration | include ip https",
		"display current-configuration | include packet-filter",
		"display current-configuration | include dhcp server ip-pool",
		"display current-configuration | include interface Vlan-interface",
		"display current-configuration | include port link-type",
		"display current-configuration | include port trunk",
	},
	"router": {
		"display ip routing-table 0.0.0.0 0",
		"display ip policy-based-route interface GigabitEthernet0/3",
		"display current-configuration interface GigabitEthernet0/7",
		"display nqa statistics",
		"display nqa history",
		"display nat server",
		"display nat session statistics",
		"display qos car",
		"display qos carl",
		"display object-group ip address",
		"display ntp-service status",
		"display startup",
		"display current-configuration | include packet-filter",
		"display current-configuration | include nat server",
		"display current-configuration | include ssh server",
		"display current-configuration | include telnet server",
		"display current-configuration | include ip http",
		"display current-configuration | include ip https",
	},
	"ac": {
		"display wlan ap all",
		"display wlan service-template",
		"display wlan client",
		"display ntp-service status",
		"display startup",
		"display current-configuration | include ntp",
		"display current-configuration | include info-center",
		"display current-configuration | include snmp-agent",
		"display current-configuration | include ssh server",
		"display current-configuration | include telnet server",
		"display current-configuration | include ip http",
		"display current-configuration | include ip https",
		"display current-configuration | include radius",
		"display local-user service-type lan-access | include Total",
	},
	"aggregation": {
		"display clock",
		"display lldp neighbor-information verbose",
		"display poe interface",
		"display stp brief",
		"display ntp-service status",
		"display startup",
		"display counters rate inbound interface",
		"display counters rate outbound interface",
		"display mac-address statistics",
		"display current-configuration | include ssh",
		"display current-configuration | include telnet",
		"display current-configuration | include http",
		"display current-configuration | include https",
	},
	"switch": {
		"display clock",
		"display lldp neighbor-information verbose",
		"display stp brief",
		"display mac-address statistics",
		"display ntp-service status",
		"display startup",
		"display current-configuration | include ssh",
		"display current-configuration | include telnet",
		"display current-configuration | include http",
		"display current-configuration | include https",
	},
	"poe": {
		"display clock",
		"display lldp neighbor-information verbose",
		"display poe interface",
		"display poe power-usage",
		"display stp brief",
		"display ntp-service status",
		"display startup",
		"display current-configuration | include ssh",
		"display current-configuration | include telnet",
		"display current-configuration | include http",
		"display current-configuration | include https",
	},
}

var h3cAuditProfilesByRole = map[string]string{
	"ac": "ac", "core": "core", "router": "router", "aggregation": "aggregation",
	"poe": "poe", "switch": "switch", "storage-irf": "switch", "datacenter-aggregation": "aggregation",
}

const h3cReadCommands = "screen-length disable\ndisplay version\ndisplay interface brief\ndisplay vlan brief\ndisplay link-aggregation summary\ndisplay lldp neighbor-information list\ndisplay stp brief\ndisplay ntp-service status\ndisplay startup\nquit\nquit\n"

type H3CConfig struct {
	Endpoint              string
	Username              string
	PrivateKey            string
	HostKeySHA256         string
	LegacyRSA             bool
	DeviceName            string
	DeviceRole            string
	IntegrationID         string
	AllowInterfaceChanges bool
	ProtectedInterfaces   []string
	Dial                  func(context.Context) (h3cSSHSession, error)
}

type h3cSSHSession interface {
	CombinedOutput(context.Context, string) ([]byte, error)
	Close() error
}

type H3CRunner struct {
	host                  string
	port                  int
	username              string
	privateKey            []byte
	hostKeySHA256         string
	legacyRSA             bool
	deviceName            string
	deviceRole            string
	integrationID         string
	allowInterfaceChanges bool
	protectedInterfaces   map[string]bool
	dial                  func(context.Context) (h3cSSHSession, error)
	last                  *h3cInventory
}

type h3cInventory struct {
	Model      string
	Software   string
	Interfaces []h3cInterface
	VLANs      []int
	ObservedAt time.Time
}

type h3cInterface struct {
	Name        string
	Link        string
	Speed       string
	Duplex      string
	LinkType    string
	PVID        int
	Description string
}

func NewH3CRunner(config H3CConfig) (*H3CRunner, error) {
	match := h3cEndpointPattern.FindStringSubmatch(strings.TrimSpace(config.Endpoint))
	if len(match) != 3 {
		return nil, fmt.Errorf("H3C endpoint must use ssh://host:port")
	}
	host := strings.TrimSpace(match[1])
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
		return nil, fmt.Errorf("H3C endpoint must contain a fixed unicast IP address")
	}
	port, err := strconv.Atoi(match[2])
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("H3C SSH port is invalid")
	}
	if !h3cUsernamePattern.MatchString(strings.TrimSpace(config.Username)) {
		return nil, fmt.Errorf("H3C SSH username is invalid")
	}
	if strings.TrimSpace(config.DeviceName) == "" || len(config.DeviceName) > 255 || strings.ContainsAny(config.DeviceName, "/\\\x00\r\n") {
		return nil, fmt.Errorf("H3C device name is required")
	}
	if !h3cDeviceRoles[strings.TrimSpace(config.DeviceRole)] {
		return nil, fmt.Errorf("H3C device role is required")
	}
	if config.Dial == nil && strings.TrimSpace(config.HostKeySHA256) == "" {
		return nil, fmt.Errorf("H3C SSH host key SHA256 fingerprint is required")
	}
	runner := &H3CRunner{host: host, port: port, username: strings.TrimSpace(config.Username),
		privateKey: []byte(config.PrivateKey), hostKeySHA256: strings.TrimSpace(config.HostKeySHA256),
		legacyRSA: config.LegacyRSA, deviceName: strings.TrimSpace(config.DeviceName),
		deviceRole: strings.TrimSpace(config.DeviceRole), integrationID: config.IntegrationID,
		allowInterfaceChanges: config.AllowInterfaceChanges, protectedInterfaces: map[string]bool{}}
	for _, interfaceName := range config.ProtectedInterfaces {
		runner.protectedInterfaces[h3cInterfaceCanonicalName(interfaceName)] = true
	}
	if config.Dial != nil {
		runner.dial = config.Dial
	} else {
		if len(runner.privateKey) == 0 {
			return nil, fmt.Errorf("H3C SSH private key is required")
		}
		runner.dial = runner.dialSSH
	}
	return runner, nil
}

func (r *H3CRunner) ListResources(ctx context.Context) []DiscoveredResource {
	inventory, err := r.readInventory(ctx)
	if err != nil {
		return nil
	}
	r.last = inventory
	return r.resources(inventory)
}

func (r *H3CRunner) ListResourcesComplete(ctx context.Context) ([]DiscoveredResource, bool) {
	inventory, err := r.readInventory(ctx)
	if err != nil {
		return nil, false
	}
	r.last = inventory
	return r.resources(inventory), true
}

func (r *H3CRunner) Execute(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	if task.Resource.IntegrationID != r.integrationID {
		return nil, Lease{}, fmt.Errorf("H3C task targets another integration")
	}
	lease := Lease{Provider: "H3C_COMWARE_SSH", Fingerprint: r.hostKeySHA256, ExpiresAt: task.Grant.ExpiresAt}
	if isH3CInterfaceAction(task.Action.Name) {
		return r.executeInterfaceAction(ctx, task, lease)
	}
	if command, ok := h3cReadOnlyOperationCommands[task.Action.Name]; ok {
		if err := requireH3CDeviceResource(task, r.deviceExternalID()); err != nil {
			return nil, lease, err
		}
		return r.executeReadOnlyOperation(ctx, task.Action.Name, command, task.Parameters, lease)
	}
	inventory, err := r.readInventory(ctx)
	if err != nil {
		return nil, Lease{}, err
	}
	r.last = inventory
	switch task.Action.Name {
	case "network.device.get":
		if task.Resource.Type != "network_device" || task.Resource.ExternalID != r.deviceExternalID() {
			return nil, lease, fmt.Errorf("H3C device resource identity mismatch")
		}
		return r.deviceResult(inventory), lease, nil
	case "network.device.audit":
		if err := requireH3CDeviceResource(task, r.deviceExternalID()); err != nil {
			return nil, lease, err
		}
		profile := strings.TrimSpace(fmt.Sprint(task.Parameters["profile"]))
		if profile == "" || profile == "<nil>" {
			profile = h3cAuditProfilesByRole[r.deviceRole]
			if profile == "" {
				profile = "standard"
			}
		}
		if _, ok := h3cAuditProfileCommands[profile]; !ok {
			return nil, lease, fmt.Errorf("unsupported H3C audit profile %q", profile)
		}
		if profile != "standard" && profile != "security" {
			roleProfile := h3cAuditProfilesByRole[r.deviceRole]
			if roleProfile != profile {
				return nil, lease, fmt.Errorf("H3C audit profile %q is not allowed for device role %q", profile, r.deviceRole)
			}
		}
		return r.executeDeviceAudit(ctx, inventory, profile, lease)
	case "network.device.health":
		if err := requireH3CDeviceResource(task, r.deviceExternalID()); err != nil {
			return nil, lease, err
		}
		return r.deviceHealthResult(inventory), lease, nil
	case "network.interface.list":
		if task.Resource.Type != "network_device" || task.Resource.ExternalID != r.deviceExternalID() {
			return nil, lease, fmt.Errorf("H3C interface list requires the registered device resource")
		}
		return map[string]any{"items": interfaceResults(inventory.Interfaces), "count": len(inventory.Interfaces), "observedAt": inventory.ObservedAt}, lease, nil
	case "network.vlan.list":
		if err := requireH3CDeviceResource(task, r.deviceExternalID()); err != nil {
			return nil, lease, fmt.Errorf("H3C VLAN list requires the registered device resource")
		}
		return map[string]any{"items": inventory.VLANs, "count": len(inventory.VLANs), "observedAt": inventory.ObservedAt}, lease, nil
	case "network.port.health":
		if err := requireH3CDeviceResource(task, r.deviceExternalID()); err != nil {
			return nil, lease, err
		}
		return r.portHealthResult(inventory), lease, nil
	default:
		return nil, lease, fmt.Errorf("unsupported H3C action %q", task.Action.Name)
	}
}

func (r *H3CRunner) executeDeviceAudit(ctx context.Context, inventory *h3cInventory, profile string, lease Lease) (map[string]any, Lease, error) {
	commands := h3cAuditProfileCommands[profile]
	result := r.deviceResult(inventory)
	result["profile"] = profile
	result["checks"] = []string{"version", "interface_brief", "vlan_brief", "link_aggregation", "lldp", "stp", "ntp", "startup"}
	if len(commands) == 0 {
		result["findings"] = map[string]any{}
		return result, lease, nil
	}

	session, err := r.dial(ctx)
	if err != nil {
		return nil, lease, fmt.Errorf("connect H3C device for %s audit: %w", profile, err)
	}
	defer session.Close()
	all := append([]string{"screen-length disable"}, commands...)
	all = append(all, "quit", "quit")
	raw, err := session.CombinedOutput(ctx, strings.Join(all, "\n")+"\n")
	if err != nil {
		return nil, lease, fmt.Errorf("read H3C %s audit: %w", profile, err)
	}
	if len(raw) > 4<<20 {
		return nil, lease, fmt.Errorf("H3C %s audit response exceeds 4 MiB", profile)
	}
	clean := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r", ""), "\x00", "")
	if h3cCLIErrorPattern.MatchString(clean) {
		return nil, lease, fmt.Errorf("H3C device rejected a read-only command for %s audit", profile)
	}
	result["audit"] = summarizeH3CAudit(profile, commands, clean)
	return result, lease, nil
}

func requireH3CDeviceResource(task domain.TaskEnvelope, externalID string) error {
	if task.Resource.Type != "network_device" || task.Resource.ExternalID != externalID {
		return fmt.Errorf("H3C operation requires the registered device resource")
	}
	return nil
}

func (r *H3CRunner) executeReadOnlyOperation(ctx context.Context, action, command string, parameters map[string]any, lease Lease) (map[string]any, Lease, error) {
	if action == "network.mac.lookup" {
		mac, ok := parameters["mac"].(string)
		if !ok || !validH3CMAC(mac) {
			return nil, lease, fmt.Errorf("network.mac.lookup MAC address is invalid")
		}
	}
	if action == "network.arp.lookup" {
		ip, ok := parameters["ip"].(string)
		if !ok || !validH3CIPv4(ip) {
			return nil, lease, fmt.Errorf("network.arp.lookup IPv4 address is invalid")
		}
	}
	session, err := r.dial(ctx)
	if err != nil {
		return nil, lease, fmt.Errorf("connect H3C device: %w", err)
	}
	defer session.Close()
	raw, err := session.CombinedOutput(ctx, command)
	if err != nil {
		return nil, lease, fmt.Errorf("read H3C %s: %w", action, err)
	}
	if len(raw) > 4<<20 {
		return nil, lease, fmt.Errorf("H3C %s response exceeds 4 MiB", action)
	}
	if h3cCLIErrorPattern.MatchString(strings.ReplaceAll(string(raw), "\r", "")) {
		return nil, lease, fmt.Errorf("H3C device rejected the read-only command for %s", action)
	}
	if action == "network.mac.lookup" {
		mac, ok := parameters["mac"].(string)
		if !ok {
			return nil, lease, fmt.Errorf("network.mac.lookup requires a MAC address")
		}
		result, lookupErr := r.lookupMACResult(mac, string(raw))
		return result, lease, lookupErr
	}
	if action == "network.arp.lookup" {
		ip, ok := parameters["ip"].(string)
		if !ok {
			return nil, lease, fmt.Errorf("network.arp.lookup requires an IPv4 address")
		}
		result, lookupErr := r.lookupARPResult(ip, string(raw))
		return result, lease, lookupErr
	}
	return summarizeH3CReadOnlyOperation(action, r.deviceName, string(raw), time.Now().UTC()), lease, nil
}

func normalizeH3CMAC(value string) string {
	value = strings.ToLower(strings.NewReplacer(":", "", "-", "", ".", "").Replace(strings.TrimSpace(value)))
	return value
}

func validH3CMAC(value string) bool {
	return h3cMACPattern.MatchString(strings.TrimSpace(value)) && len(normalizeH3CMAC(value)) == 12
}

func validH3CIPv4(value string) bool {
	value = strings.TrimSpace(value)
	return h3cIPv4Pattern.MatchString(value) && net.ParseIP(value) != nil
}

func (r *H3CRunner) lookupMACResult(value, output string) (map[string]any, error) {
	if !validH3CMAC(value) {
		return nil, fmt.Errorf("network.mac.lookup MAC address is invalid")
	}
	target := normalizeH3CMAC(value)
	items := make([]map[string]any, 0)
	for _, rawLine := range strings.Split(strings.ReplaceAll(output, "\r", ""), "\n") {
		line := strings.TrimSpace(rawLine)
		match := h3cMACPattern.FindString(line)
		if match == "" || normalizeH3CMAC(match) != target {
			continue
		}
		fields := strings.Fields(line)
		item := map[string]any{"mac": strings.ToLower(match)}
		for _, field := range fields {
			if vlan, err := strconv.Atoi(field); err == nil && vlan >= 1 && vlan <= 4094 {
				item["vlan"] = vlan
			}
			if h3cPortPattern.MatchString(field) {
				item["interface"] = field
			}
		}
		items = append(items, item)
	}
	return map[string]any{"name": r.deviceName, "mac": strings.ToLower(strings.TrimSpace(value)), "matches": items, "count": len(items), "observedAt": time.Now().UTC()}, nil
}

func (r *H3CRunner) lookupARPResult(value, output string) (map[string]any, error) {
	ip := strings.TrimSpace(value)
	if !validH3CIPv4(ip) {
		return nil, fmt.Errorf("network.arp.lookup IPv4 address is invalid")
	}
	items := make([]map[string]any, 0)
	for _, rawLine := range strings.Split(strings.ReplaceAll(output, "\r", ""), "\n") {
		line := strings.TrimSpace(rawLine)
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != ip {
			continue
		}
		item := map[string]any{"ip": ip}
		if match := h3cMACPattern.FindString(line); match != "" {
			item["mac"] = strings.ToLower(match)
		}
		for _, field := range fields[1:] {
			if vlan, err := strconv.Atoi(field); err == nil && vlan >= 1 && vlan <= 4094 {
				item["vlan"] = vlan
			}
			if h3cPortPattern.MatchString(field) {
				item["interface"] = field
			}
		}
		items = append(items, item)
	}
	return map[string]any{"name": r.deviceName, "ip": ip, "matches": items, "count": len(items), "observedAt": time.Now().UTC()}, nil
}

func summarizeH3CReadOnlyOperation(action, deviceName, output string, observedAt time.Time) map[string]any {
	clean := strings.ReplaceAll(strings.ReplaceAll(output, "\r", ""), "\x00", "")
	lines := make([]string, 0)
	for _, line := range strings.Split(clean, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "screen-length ") || strings.HasPrefix(line, "display ") || h3cPromptPattern.MatchString(line) || line == "quit" {
			continue
		}
		lines = append(lines, line)
	}
	result := map[string]any{"name": deviceName, "device": deviceName, "action": action, "observedAt": observedAt, "lineCount": len(lines)}
	switch action {
	case "network.topology.lldp":
		neighbors, chassisIDs := 0, 0
		for _, line := range lines {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "neighbor index") {
				neighbors++
			}
			if strings.Contains(lower, "chassis id") {
				chassisIDs++
			}
		}
		if neighbors == 0 {
			neighbors = chassisIDs
		}
		result["neighborCount"] = neighbors
	case "network.link_aggregation.status":
		aggregations, selected, unselected := 0, 0, 0
		for _, line := range lines {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "bridge-aggregation") || strings.Contains(lower, "bagg") {
				aggregations++
			}
			if strings.Contains(lower, "selected") {
				selected++
			}
			if strings.Contains(lower, "unselected") {
				unselected++
			}
		}
		result["aggregationLineCount"], result["selectedLineCount"], result["unselectedLineCount"] = aggregations, selected, unselected
	case "network.stp.status":
		forwarding, discarding := 0, 0
		for _, line := range lines {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "forward") {
				forwarding++
			}
			if strings.Contains(lower, "discard") || strings.Contains(lower, "block") {
				discarding++
			}
		}
		result["forwardingLineCount"], result["discardingOrBlockingLineCount"] = forwarding, discarding
	}
	return result
}

var h3cSensitiveAuditLine = regexp.MustCompile(`(?i)(password|passwd|secret|cipher|private-key|shared-key|token|community)`)

// summarizeH3CAudit returns bounded, redacted sections rather than raw CLI
// output.  This keeps the audit useful for operators while preventing common
// credential/config-secret fields from crossing the Connector boundary.
func summarizeH3CAudit(profile string, commands []string, output string) map[string]any {
	lines := strings.Split(output, "\n")
	sections := make(map[string][]string, len(commands))
	current := ""
	total := 0
	redacted := 0
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || line == "screen-length disable" || line == "quit" || h3cPromptPattern.MatchString(line) {
			continue
		}
		lowerLine := strings.ToLower(line)
		if strings.HasPrefix(lowerLine, "display ") {
			current = line
			continue
		}
		// Comware commonly echoes commands as `<device>display ...`.
		if idx := strings.Index(lowerLine, "display "); strings.HasPrefix(line, "<") && idx > 0 && strings.HasSuffix(line[:idx], ">") {
			current = line[idx:]
			continue
		}
		if current == "" || total >= 2000 {
			continue
		}
		if h3cSensitiveAuditLine.MatchString(line) {
			line = "[REDACTED sensitive line]"
			redacted++
		}
		if len(sections[current]) >= 200 {
			continue
		}
		sections[current] = append(sections[current], line)
		total++
	}
	warnings := make([]string, 0, 8)
	joined := strings.ToLower(output)
	if strings.Contains(joined, "telnet server enable") {
		warnings = append(warnings, "telnet_enabled")
	}
	if strings.Contains(joined, "ip http enable") || strings.Contains(joined, "ip https enable") {
		warnings = append(warnings, "web_management_enabled")
	}
	if strings.Contains(joined, "unsynchronized") || strings.Contains(joined, "unsynchronised") {
		warnings = append(warnings, "ntp_unsynchronized")
	}
	if strings.Contains(joined, "unselected") || strings.Contains(joined, "discarding") {
		warnings = append(warnings, "link_or_stp_attention")
	}
	result := map[string]any{
		"profile": profile, "commands": commands, "sections": sections,
		"lineCount": total, "redactedLineCount": redacted, "warnings": warnings,
		"truncated": total >= 2000,
	}
	switch profile {
	case "core":
		result["sviCount"] = countSectionLines(sections, "display interface brief", `(?i)^vlan(?:-interface)?\d+\b`)
		result["dhcpConflictCount"] = countSectionContains(sections, "display dhcp server conflict", "conflict")
		result["dhcpPoolLineCount"] = len(sections["display dhcp server pool"])
	case "router":
		result["pbrLineCount"] = len(sections["display ip policy-based-route interface GigabitEthernet0/3"])
		result["natServerLineCount"] = len(sections["display nat server"])
		result["qosLineCount"] = len(sections["display qos car"]) + len(sections["display qos carl"])
	case "ac":
		result["apLineCount"] = len(sections["display wlan ap all"])
		result["clientLineCount"] = len(sections["display wlan client"])
		result["radiusLineCount"] = countSectionContains(sections, "display current-configuration | include radius", "radius")
	case "aggregation", "poe", "switch":
		result["lldpLineCount"] = len(sections["display lldp neighbor-information verbose"])
		result["poeLineCount"] = len(sections["display poe interface"]) + len(sections["display poe power-usage"])
	}
	return result
}

func countSectionContains(sections map[string][]string, section, needle string) int {
	count := 0
	for _, line := range sections[section] {
		if strings.Contains(strings.ToLower(line), strings.ToLower(needle)) {
			count++
		}
	}
	return count
}

func countSectionLines(sections map[string][]string, section, pattern string) int {
	re := regexp.MustCompile(pattern)
	count := 0
	for _, line := range sections[section] {
		if re.MatchString(line) {
			count++
		}
	}
	return count
}

func (r *H3CRunner) resources(inventory *h3cInventory) []DiscoveredResource {
	baseMetadata := map[string]any{"vendor": "H3C", "model": inventory.Model, "software": inventory.Software,
		"role": r.deviceRole, "transport": "ssh", "observedAt": inventory.ObservedAt}
	resources := []DiscoveredResource{{ExternalID: r.deviceExternalID(), Name: r.deviceName,
		ResourceType: "network_device", IntegrationID: r.integrationID,
		Labels: map[string]any{"vendor": "h3c", "role": r.deviceRole}, Annotations: map[string]any{}, Metadata: baseMetadata}}
	for _, item := range inventory.Interfaces {
		resources = append(resources, DiscoveredResource{ExternalID: r.deviceExternalID() + "/interface/" + item.Name,
			Name: item.Name, Namespace: r.deviceName, ResourceType: "network_interface", IntegrationID: r.integrationID,
			Labels:      map[string]any{"vendor": "h3c", "device": r.deviceName, "link": strings.ToLower(item.Link)},
			Annotations: map[string]any{}, Metadata: map[string]any{"device": r.deviceName, "interface": item.Name,
				"link": item.Link, "speed": item.Speed, "duplex": item.Duplex, "linkType": item.LinkType, "pvid": item.PVID, "description": item.Description}})
	}
	for _, vlan := range inventory.VLANs {
		resources = append(resources, DiscoveredResource{ExternalID: fmt.Sprintf("%s/vlan/%d", r.deviceExternalID(), vlan),
			Name: fmt.Sprintf("VLAN %d", vlan), Namespace: r.deviceName, ResourceType: "network_vlan", IntegrationID: r.integrationID,
			Labels: map[string]any{"vendor": "h3c", "device": r.deviceName}, Annotations: map[string]any{},
			Metadata: map[string]any{"device": r.deviceName, "vlan": vlan}})
	}
	return resources
}

func (r *H3CRunner) readInventory(ctx context.Context) (*h3cInventory, error) {
	session, err := r.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect H3C device: %w", err)
	}
	defer session.Close()
	raw, err := session.CombinedOutput(ctx, h3cReadCommands)
	if err != nil {
		return nil, fmt.Errorf("read H3C inventory: %w", err)
	}
	if len(raw) > 4<<20 {
		return nil, fmt.Errorf("H3C inventory response exceeds 4 MiB")
	}
	return parseH3CInventory(string(raw), time.Now().UTC())
}

func parseH3CInventory(output string, observedAt time.Time) (*h3cInventory, error) {
	clean := strings.ReplaceAll(strings.ReplaceAll(output, "\r", ""), "\x00", "")
	if h3cCLIErrorPattern.MatchString(clean) {
		return nil, fmt.Errorf("H3C device returned a CLI error")
	}
	inventory := &h3cInventory{ObservedAt: observedAt}
	if match := h3cModelPattern.FindStringSubmatch(clean); len(match) == 2 {
		inventory.Model = strings.TrimSpace(match[1])
	}
	if match := h3cSoftwarePattern.FindStringSubmatch(clean); len(match) == 2 {
		inventory.Software = strings.TrimSpace(match[1])
	}
	seenVLAN := map[int]bool{}
	for _, line := range strings.Split(clean, "\n") {
		if match := h3cInterfaceLine.FindStringSubmatch(line); len(match) == 6 {
			pvid, _ := strconv.Atoi(match[4])
			columns := strings.Fields(match[3])
			if len(columns) == 0 {
				continue
			}
			duplex, linkType := "", ""
			if len(columns) > 1 {
				duplex = columns[1]
			}
			if len(columns) > 2 {
				linkType = strings.ToUpper(columns[2])
			}
			inventory.Interfaces = append(inventory.Interfaces, h3cInterface{Name: match[1], Link: strings.ToUpper(match[2]),
				Speed: columns[0], Duplex: duplex, LinkType: linkType, PVID: pvid, Description: strings.TrimSpace(match[5])})
		}
		if match := h3cVLANLine.FindStringSubmatch(line); len(match) == 2 {
			vlan, _ := strconv.Atoi(match[1])
			if vlan >= 1 && vlan <= 4094 && !seenVLAN[vlan] {
				seenVLAN[vlan] = true
				inventory.VLANs = append(inventory.VLANs, vlan)
			}
		}
	}
	if inventory.Model == "" && inventory.Software == "" {
		return nil, fmt.Errorf("H3C inventory output did not contain device facts")
	}
	sort.Ints(inventory.VLANs)
	return inventory, nil
}

func (r *H3CRunner) deviceResult(inventory *h3cInventory) map[string]any {
	return map[string]any{"name": r.deviceName, "vendor": "H3C", "model": inventory.Model, "software": inventory.Software,
		"role": r.deviceRole, "interfaceCount": len(inventory.Interfaces), "vlanCount": len(inventory.VLANs), "observedAt": inventory.ObservedAt}
}

func (r *H3CRunner) deviceHealthResult(inventory *h3cInventory) map[string]any {
	up, down, adminDown := 0, 0, 0
	speeds := map[string]int{}
	for _, item := range inventory.Interfaces {
		switch item.Link {
		case "UP":
			up++
		case "DOWN":
			down++
		case "ADM":
			adminDown++
		}
		if item.Speed != "" {
			speeds[item.Speed]++
		}
	}
	return map[string]any{"name": r.deviceName, "vendor": "H3C", "model": inventory.Model, "software": inventory.Software,
		"role": r.deviceRole, "observedAt": inventory.ObservedAt, "interfaceCount": len(inventory.Interfaces),
		"vlanCount": len(inventory.VLANs), "interfaces": map[string]any{"up": up, "down": down, "administrativelyDown": adminDown}, "speedCounts": speeds}
}

func (r *H3CRunner) portHealthResult(inventory *h3cInventory) map[string]any {
	result := r.deviceHealthResult(inventory)
	result["interfaces"] = interfaceResults(inventory.Interfaces)
	return result
}

func interfaceResults(items []h3cInterface) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{"name": item.Name, "link": item.Link, "speed": item.Speed, "duplex": item.Duplex,
			"linkType": item.LinkType, "pvid": item.PVID, "description": item.Description})
	}
	return result
}

func (r *H3CRunner) deviceExternalID() string { return "h3c/device/" + r.deviceName }

type realH3CSession struct {
	client *ssh.Client
}

type h3cPromptWriter struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	ready     chan struct{}
	readyOnce sync.Once
}

func (w *h3cPromptWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	_, _ = w.buffer.Write(p)
	text := w.buffer.String()
	w.mu.Unlock()
	if h3cPromptPattern.MatchString(text) {
		w.readyOnce.Do(func() { close(w.ready) })
	}
	return len(p), nil
}

func (w *h3cPromptWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buffer.Bytes()...)
}

func (r *H3CRunner) dialSSH(ctx context.Context) (h3cSSHSession, error) {
	signer, err := ssh.ParsePrivateKey(r.privateKey)
	if err != nil {
		return nil, fmt.Errorf("parse SSH private key: %w", err)
	}
	hostKeyCallback := func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if ssh.FingerprintSHA256(key) != r.hostKeySHA256 {
			return fmt.Errorf("SSH host key fingerprint mismatch")
		}
		return nil
	}
	config := &ssh.ClientConfig{User: r.username, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: hostKeyCallback,
		Timeout: 10 * time.Second}
	if r.legacyRSA {
		config.HostKeyAlgorithms = []string{ssh.KeyAlgoRSA}
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(r.host, strconv.Itoa(r.port)))
	if err != nil {
		return nil, err
	}
	conn, chans, reqs, err := ssh.NewClientConn(connection, net.JoinHostPort(r.host, strconv.Itoa(r.port)), config)
	if err != nil {
		connection.Close()
		return nil, err
	}
	return &realH3CSession{client: ssh.NewClient(conn, chans, reqs)}, nil
}

func (s *realH3CSession) CombinedOutput(ctx context.Context, commands string) ([]byte, error) {
	session, err := s.client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		return nil, err
	}
	output := &h3cPromptWriter{ready: make(chan struct{})}
	session.Stdout, session.Stderr = output, output
	if err := session.RequestPty("vt100", 80, 40, ssh.TerminalModes{ssh.ECHO: 0}); err != nil {
		return nil, err
	}
	if err := session.Shell(); err != nil {
		return nil, err
	}
	select {
	case <-output.ready:
	case <-time.After(5 * time.Second):
		return output.Bytes(), fmt.Errorf("timed out waiting for H3C shell prompt")
	case <-ctx.Done():
		return output.Bytes(), ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		_, writeErr := stdin.Write([]byte(commands))
		if writeErr == nil {
			writeErr = stdin.Close()
		}
		if waitErr := session.Wait(); writeErr == nil {
			writeErr = waitErr
		}
		done <- writeErr
	}()
	select {
	case err := <-done:
		return output.Bytes(), err
	case <-ctx.Done():
		_ = session.Close()
		return output.Bytes(), ctx.Err()
	}
}

func (s *realH3CSession) Close() error { return s.client.Close() }

// MarshalJSON is used only by tests and diagnostics to ensure inventory values
// remain serializable before they enter resource sync or execution results.
func (i h3cInventory) MarshalJSON() ([]byte, error) {
	type alias h3cInventory
	return json.Marshal(alias(i))
}
