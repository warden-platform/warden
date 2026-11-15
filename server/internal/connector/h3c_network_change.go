package connector

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

var (
	h3cInterfaceNamePattern   = regexp.MustCompile(`(?i)^(?:GE|XGE|GigabitEthernet|Ten-GigabitEthernet|BAGG|Bridge-Aggregation)[0-9/]+$`)
	h3cInterfaceSuffixPattern = regexp.MustCompile(`^[0-9/]+$`)
	h3cInterfaceBlockPattern  = regexp.MustCompile(`(?ims)^interface\s+(\S+)\s*$\n(.*?)(?:^#\s*$|\z)`)
	h3cCLIErrorPattern        = regexp.MustCompile(`(?i)%\s*(?:incomplete|unrecognized|invalid|wrong|ambiguous)|permission denied|error:`)
)

// h3cInterfaceCanonicalName collapses the short names emitted by
// `display interface brief` (for example GE0/0/20) and the long names used
// by `display current-configuration interface` (for example
// GigabitEthernet0/0/20) to the same comparison form.  Comware accepts both
// spellings as interface selectors, but its two display commands do not
// consistently use the same spelling.
func h3cInterfaceCanonicalName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, prefix := range []struct{ long, short string }{
		{"ten-gigabitethernet", "xge"},
		{"gigabitethernet", "ge"},
		{"bridge-aggregation", "bagg"},
		{"xge", "xge"},
		{"ge", "ge"},
		{"bagg", "bagg"},
	} {
		if strings.HasPrefix(name, prefix.long) {
			suffix := strings.TrimPrefix(name, prefix.long)
			if h3cInterfaceSuffixPattern.MatchString(suffix) {
				return prefix.short + suffix
			}
		}
	}
	return name
}

func h3cInterfaceNamesEqual(left, right string) bool {
	return h3cInterfaceCanonicalName(left) == h3cInterfaceCanonicalName(right)
}

var errH3CChangeOutcomeUnknown = errors.New("H3C change outcome is unknown")

type h3cInterfaceState struct {
	Name        string
	LayerMode   string
	LinkType    string
	PVID        int
	Description string
	TrunkVLANs  []int
	Critical    bool
}

type h3cInterfacePlan struct {
	Action      string
	Interface   string
	Before      map[string]any
	After       map[string]any
	Commands    []string
	Save        bool
	NoOp        bool
	PreviewHash string
}

func isH3CInterfaceAction(action string) bool {
	switch action {
	case "network.interface.description.preview", "network.interface.description.update",
		"network.interface.access_vlan.preview", "network.interface.access_vlan.update",
		"network.interface.trunk_vlan.preview", "network.interface.trunk_vlan.add", "network.interface.trunk_vlan.remove":
		return true
	default:
		return false
	}
}

func isH3CInterfaceMutation(action string) bool {
	switch action {
	case "network.interface.description.update", "network.interface.access_vlan.update",
		"network.interface.trunk_vlan.add", "network.interface.trunk_vlan.remove":
		return true
	default:
		return false
	}
}

func (r *H3CRunner) executeInterfaceAction(ctx context.Context, task domain.TaskEnvelope, lease Lease) (map[string]any, Lease, error) {
	interfaceName, err := r.interfaceNameFromResource(task.Resource)
	if err != nil {
		return nil, lease, err
	}
	state, err := r.readInterfaceState(ctx, interfaceName)
	if err != nil {
		return nil, lease, err
	}
	plan, err := r.planInterfaceChange(task.Action.Name, task.Parameters, state)
	if err != nil {
		return nil, lease, err
	}
	if !isH3CInterfaceMutation(task.Action.Name) {
		return plan.result(false, nil), lease, nil
	}
	if !r.allowInterfaceChanges {
		return nil, lease, fmt.Errorf("H3C interface changes are disabled for this integration")
	}
	previewHash, err := requiredString(task.Parameters, "previewHash")
	if err != nil || previewHash != plan.PreviewHash {
		return nil, lease, fmt.Errorf("PREVIEW_CONFLICT: a current matching H3C interface preview is required")
	}
	if plan.NoOp {
		return plan.result(false, state.stable()), lease, nil
	}
	if err := r.applyInterfacePlan(ctx, plan); err != nil {
		return plan.result(true, nil), lease, fmt.Errorf("%w: %v", errH3CChangeOutcomeUnknown, err)
	}
	after, err := r.readInterfaceState(ctx, interfaceName)
	if err != nil {
		return plan.result(true, nil), lease, fmt.Errorf("%w: post-change read failed: %v", errH3CChangeOutcomeUnknown, err)
	}
	if err := verifyH3CInterfacePlan(plan, after); err != nil {
		return plan.result(true, after.stable()), lease, fmt.Errorf("%w: %v", errH3CChangeOutcomeUnknown, err)
	}
	return plan.result(true, after.stable()), lease, nil
}

func (r *H3CRunner) interfaceNameFromResource(resource domain.TaskResource) (string, error) {
	prefix := r.deviceExternalID() + "/interface/"
	if resource.Type != "network_interface" || resource.IntegrationID != r.integrationID || !strings.HasPrefix(resource.ExternalID, prefix) {
		return "", fmt.Errorf("H3C interface resource identity mismatch")
	}
	name := strings.TrimPrefix(resource.ExternalID, prefix)
	if resource.Name != name || !h3cInterfaceNamePattern.MatchString(name) {
		return "", fmt.Errorf("H3C interface resource identity mismatch")
	}
	return name, nil
}

func (r *H3CRunner) readInterfaceState(ctx context.Context, interfaceName string) (*h3cInterfaceState, error) {
	if !h3cInterfaceNamePattern.MatchString(interfaceName) {
		return nil, fmt.Errorf("H3C interface name is invalid")
	}
	session, err := r.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect H3C device: %w", err)
	}
	defer session.Close()
	commands := "screen-length disable\ndisplay current-configuration interface " + interfaceName + "\ndisplay interface brief\nquit\nquit\n"
	raw, err := session.CombinedOutput(ctx, commands)
	if err != nil {
		return nil, fmt.Errorf("read H3C interface: %w", err)
	}
	if len(raw) > 1<<20 {
		return nil, fmt.Errorf("H3C interface response exceeds 1 MiB")
	}
	clean := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r", ""), "\x00", "")
	if h3cCLIErrorPattern.MatchString(clean) {
		return nil, fmt.Errorf("H3C device returned a CLI error")
	}
	return r.parseInterfaceState(interfaceName, clean)
}

func (r *H3CRunner) parseInterfaceState(interfaceName, output string) (*h3cInterfaceState, error) {
	state := &h3cInterfaceState{Name: interfaceName, LayerMode: "bridge", LinkType: "access", PVID: 1}
	trunkVLANs := map[int]bool{}
	configFound := false
	for _, match := range h3cInterfaceBlockPattern.FindAllStringSubmatch(output, -1) {
		if len(match) != 3 || !h3cInterfaceNamesEqual(match[1], interfaceName) {
			continue
		}
		configFound = true
		block := match[2]
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			lower := strings.ToLower(line)
			switch {
			case lower == "undo portswitch" || lower == "port link-mode route" || strings.HasPrefix(lower, "ip address "):
				state.LayerMode = "route"
			case lower == "port link-mode bridge":
				state.LayerMode = "bridge"
			case strings.HasPrefix(lower, "description "):
				state.Description = strings.TrimSpace(line[len("description "):])
			case strings.HasPrefix(lower, "port link-type "):
				state.LinkType = strings.ToLower(strings.TrimSpace(line[len("port link-type "):]))
			case strings.HasPrefix(lower, "port access vlan "):
				state.PVID, _ = strconv.Atoi(strings.TrimSpace(line[len("port access vlan "):]))
			case strings.HasPrefix(lower, "port trunk pvid vlan "):
				state.PVID, _ = strconv.Atoi(strings.TrimSpace(line[len("port trunk pvid vlan "):]))
			case strings.HasPrefix(lower, "port trunk permit vlan "):
				for _, vlan := range parseH3CVLANSet(strings.Fields(line[len("port trunk permit vlan "):])) {
					trunkVLANs[vlan] = true
				}
			case strings.HasPrefix(lower, "undo port trunk permit vlan "):
				for _, vlan := range parseH3CVLANSet(strings.Fields(line[len("undo port trunk permit vlan "):])) {
					delete(trunkVLANs, vlan)
				}
			}
		}
		break
	}
	if !configFound {
		interfaceMention := regexp.MustCompile(`(?im)^\s*` + regexp.QuoteMeta(interfaceName) + `\s+(?:UP|DOWN|ADM)\b`)
		if interfaceMention.MatchString(output) {
			return nil, fmt.Errorf("H3C interface configuration was not returned; refusing to infer a writable state from interface brief output")
		}
		return nil, fmt.Errorf("H3C interface was not found on the registered device")
	}
	state.TrunkVLANs = make([]int, 0, len(trunkVLANs))
	for vlan := range trunkVLANs {
		state.TrunkVLANs = append(state.TrunkVLANs, vlan)
	}
	sort.Ints(state.TrunkVLANs)
	lowerDescription := strings.ToLower(state.Description)
	state.Critical = strings.HasPrefix(strings.ToLower(interfaceName), "bagg") ||
		strings.HasPrefix(strings.ToLower(interfaceName), "bridge-aggregation") ||
		r.protectedInterfaces[h3cInterfaceCanonicalName(interfaceName)] ||
		r.protectedInterfaces[strings.ToLower(interfaceName)] ||
		strings.Contains(lowerDescription, "uplink") || strings.Contains(lowerDescription, "upstream") ||
		strings.Contains(lowerDescription, "core")
	return state, nil
}

func parseH3CVLANSet(tokens []string) []int {
	if len(tokens) == 1 && strings.EqualFold(tokens[0], "all") {
		result := make([]int, 4094)
		for vlan := 1; vlan <= 4094; vlan++ {
			result[vlan-1] = vlan
		}
		return result
	}
	result := make([]int, 0, len(tokens))
	for index := 0; index < len(tokens); index++ {
		start, err := strconv.Atoi(tokens[index])
		if err != nil || start < 1 || start > 4094 {
			continue
		}
		if index+2 < len(tokens) && strings.EqualFold(tokens[index+1], "to") {
			end, endErr := strconv.Atoi(tokens[index+2])
			if endErr == nil && end >= start && end <= 4094 {
				for vlan := start; vlan <= end; vlan++ {
					result = append(result, vlan)
				}
				index += 2
				continue
			}
		}
		result = append(result, start)
	}
	return result
}

func (s *h3cInterfaceState) stable() map[string]any {
	return map[string]any{"name": s.Name, "layerMode": s.LayerMode, "linkType": s.LinkType, "pvid": s.PVID,
		"description": s.Description, "trunkVlans": s.TrunkVLANs, "critical": s.Critical}
}

func (r *H3CRunner) planInterfaceChange(action string, parameters map[string]any, state *h3cInterfaceState) (*h3cInterfacePlan, error) {
	mutationAction := action
	if strings.HasSuffix(action, ".preview") {
		switch action {
		case "network.interface.description.preview":
			mutationAction = "network.interface.description.update"
		case "network.interface.access_vlan.preview":
			mutationAction = "network.interface.access_vlan.update"
		case "network.interface.trunk_vlan.preview":
			operation, err := requiredString(parameters, "operation")
			if err != nil || (operation != "add" && operation != "remove") {
				return nil, fmt.Errorf("parameter %q must be add or remove", "operation")
			}
			mutationAction = "network.interface.trunk_vlan." + operation
		}
	}
	if !map[string]bool{"switch": true, "core": true, "aggregation": true, "poe": true}[r.deviceRole] {
		return nil, fmt.Errorf("H3C interface changes are only supported for registered switch roles")
	}
	save, err := optionalBool(parameters, "save")
	if err != nil {
		return nil, err
	}
	allowCritical, err := optionalBool(parameters, "allowCritical")
	if err != nil {
		return nil, err
	}
	plan := &h3cInterfacePlan{Action: mutationAction, Interface: state.Name, Before: state.stable(), Save: save, Commands: []string{}}
	switch mutationAction {
	case "network.interface.description.update":
		description, err := requiredString(parameters, "description")
		if err != nil || !validH3CDescription(description) {
			return nil, fmt.Errorf("H3C interface description must contain 1-63 safe characters")
		}
		plan.After = map[string]any{"description": description}
		if state.Description == description {
			plan.NoOp = !save
			break
		}
		if state.Critical && !allowCritical {
			return nil, fmt.Errorf("target interface is protected; allowCritical=true is required")
		}
		plan.Commands = []string{"description " + description}
	case "network.interface.access_vlan.update":
		vlan, err := requiredVLAN(parameters)
		if err != nil {
			return nil, err
		}
		if state.LayerMode == "route" {
			return nil, fmt.Errorf("target interface is a routed Layer 3 port and cannot be changed as an access port")
		}
		if state.LinkType != "access" || len(state.TrunkVLANs) > 0 {
			return nil, fmt.Errorf("target interface is not an access port")
		}
		plan.After = map[string]any{"linkType": "access", "pvid": vlan}
		if state.PVID == vlan {
			plan.NoOp = !save
			break
		}
		if state.Critical && !allowCritical {
			return nil, fmt.Errorf("target interface is protected; allowCritical=true is required")
		}
		if allowed, err := requiredTrue(parameters, "allowDisruptive"); err != nil || !allowed {
			return nil, fmt.Errorf("access VLAN changes require allowDisruptive=true")
		}
		plan.Commands = []string{"port link-type access", fmt.Sprintf("port access vlan %d", vlan)}
	case "network.interface.trunk_vlan.add", "network.interface.trunk_vlan.remove":
		vlan, err := requiredVLAN(parameters)
		if err != nil {
			return nil, err
		}
		if state.LayerMode == "route" {
			return nil, fmt.Errorf("target interface is a routed Layer 3 port and cannot be changed as a trunk")
		}
		if state.LinkType != "trunk" {
			return nil, fmt.Errorf("target interface is not a trunk")
		}
		operation := "add"
		if strings.HasSuffix(mutationAction, ".remove") {
			operation = "remove"
		}
		plan.After = map[string]any{"trunkVlan": vlan, "operation": operation}
		present := containsInt(state.TrunkVLANs, vlan)
		if (operation == "add" && present) || (operation == "remove" && !present) {
			plan.NoOp = !save
			break
		}
		if state.Critical && !allowCritical {
			return nil, fmt.Errorf("target interface is protected; allowCritical=true is required")
		}
		if operation == "remove" {
			if allowed, err := requiredTrue(parameters, "allowDisruptive"); err != nil || !allowed {
				return nil, fmt.Errorf("trunk VLAN removal requires allowDisruptive=true")
			}
		}
		if operation == "remove" {
			plan.Commands = []string{fmt.Sprintf("undo port trunk permit vlan %d", vlan)}
		} else {
			plan.Commands = []string{fmt.Sprintf("port trunk permit vlan %d", vlan)}
		}
	default:
		return nil, fmt.Errorf("unsupported H3C interface action %q", action)
	}
	desired := map[string]any{"action": plan.Action, "interface": plan.Interface, "after": plan.After, "save": plan.Save}
	plan.PreviewHash = domain.HashParameters(map[string]any{"before": plan.Before, "desired": desired})
	return plan, nil
}

func validH3CDescription(value string) bool {
	if value != strings.TrimSpace(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 63 {
		return false
	}
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsNumber(char) || strings.ContainsRune(" ._-:/()#@+", char) {
			continue
		}
		return false
	}
	return true
}

func requiredVLAN(parameters map[string]any) (int, error) {
	value, ok := parameters["vlan"]
	if !ok {
		return 0, fmt.Errorf("parameter %q is required", "vlan")
	}
	var vlan int
	switch typed := value.(type) {
	case int:
		vlan = typed
	case float64:
		vlan = int(typed)
		if typed != float64(vlan) {
			return 0, fmt.Errorf("VLAN must be an integer")
		}
	default:
		return 0, fmt.Errorf("VLAN must be an integer")
	}
	if vlan < 2 || vlan > 4093 {
		return 0, fmt.Errorf("VLAN must be between 2 and 4093")
	}
	return vlan, nil
}

func optionalBool(parameters map[string]any, key string) (bool, error) {
	value, ok := parameters[key]
	if !ok {
		return false, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("parameter %q must be a boolean", key)
	}
	return result, nil
}

func requiredTrue(parameters map[string]any, key string) (bool, error) {
	value, ok := parameters[key].(bool)
	if !ok || !value {
		return false, fmt.Errorf("parameter %q must be true", key)
	}
	return true, nil
}

func (r *H3CRunner) applyInterfacePlan(ctx context.Context, plan *h3cInterfacePlan) error {
	session, err := r.dial(ctx)
	if err != nil {
		return fmt.Errorf("connect H3C device: %w", err)
	}
	defer session.Close()
	commands := plan.deviceCommands()
	raw, err := session.CombinedOutput(ctx, strings.Join(commands, "\n")+"\n")
	if err != nil {
		return fmt.Errorf("send H3C interface change: %w", err)
	}
	if len(raw) > 1<<20 || h3cCLIErrorPattern.Match(raw) {
		return fmt.Errorf("H3C device returned a CLI error during the change")
	}
	return nil
}

func (p *h3cInterfacePlan) deviceCommands() []string {
	commands := make([]string, 0, len(p.Commands)+6)
	if len(p.Commands) > 0 {
		commands = append(commands, "system-view", "interface "+p.Interface)
		commands = append(commands, p.Commands...)
		commands = append(commands, "quit", "quit")
	}
	if p.Save {
		commands = append(commands, "save force")
	}
	commands = append(commands, "quit", "quit")
	return commands
}

func verifyH3CInterfacePlan(plan *h3cInterfacePlan, state *h3cInterfaceState) error {
	switch plan.Action {
	case "network.interface.description.update":
		if state.Description != plan.After["description"] {
			return fmt.Errorf("post-change description does not match the requested value")
		}
	case "network.interface.access_vlan.update":
		if state.LinkType != "access" || state.PVID != plan.After["pvid"] {
			return fmt.Errorf("post-change access VLAN does not match the requested value")
		}
	case "network.interface.trunk_vlan.add", "network.interface.trunk_vlan.remove":
		vlan := plan.After["trunkVlan"].(int)
		present := containsInt(state.TrunkVLANs, vlan)
		if (plan.After["operation"] == "add" && !present) || (plan.After["operation"] == "remove" && present) {
			return fmt.Errorf("post-change trunk VLAN membership does not match the requested value")
		}
	}
	return nil
}

func containsInt(values []int, target int) bool {
	index := sort.SearchInts(values, target)
	return index < len(values) && values[index] == target
}

func (p *h3cInterfacePlan) result(applied bool, observed map[string]any) map[string]any {
	commands := p.deviceCommands()
	if p.NoOp {
		commands = []string{}
	}
	result := map[string]any{"action": p.Action, "interface": p.Interface, "before": p.Before,
		"after": p.After, "commands": commands, "save": p.Save, "noOp": p.NoOp, "previewHash": p.PreviewHash, "applied": applied}
	if observed != nil {
		result["observed"] = observed
	}
	return result
}

// SnapshotResource performs the exact interface reread used by the runtime's
// change-safety contract instead of rescanning every integration resource.
func (r *H3CRunner) SnapshotResource(ctx context.Context, resource domain.TaskResource) (DiscoveredResource, bool) {
	if resource.IntegrationID != r.integrationID {
		return DiscoveredResource{}, false
	}
	if resource.Type == "network_interface" {
		name, err := r.interfaceNameFromResource(resource)
		if err != nil {
			return DiscoveredResource{}, false
		}
		state, err := r.readInterfaceState(ctx, name)
		if err != nil {
			return DiscoveredResource{}, false
		}
		return DiscoveredResource{ExternalID: resource.ExternalID, Name: name, Namespace: r.deviceName,
			ResourceType: "network_interface", IntegrationID: r.integrationID,
			Labels: map[string]any{"vendor": "h3c", "device": r.deviceName}, Annotations: map[string]any{}, Metadata: state.stable()}, true
	}
	return findDiscoveredResource(r.ListResources(ctx), resource)
}
