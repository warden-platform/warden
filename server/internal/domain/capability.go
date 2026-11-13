package domain

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var capabilityPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:@v[1-9][0-9]*)?$`)

// NormalizeCapabilities validates, trims and de-duplicates connector
// capabilities. Versioned action keys are preferred; unversioned keys remain
// accepted for compatibility with connectors released before capability
// versioning was introduced.
func NormalizeCapabilities(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || !capabilityPattern.MatchString(value) {
			return nil, fmt.Errorf("invalid connector capability %q", raw)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func RequiredCapability(actionName string, actionVersion int) string {
	return fmt.Sprintf("%s@v%d", actionName, actionVersion)
}

// SplitCapabilityKey parses a versioned capability key ("name@v1") into its
// name and version. An unversioned key yields version 0, which never matches a
// registered capability version (>=1).
func SplitCapabilityKey(key string) (name string, version int, ok bool) {
	if key == "" || !capabilityPattern.MatchString(key) {
		return "", 0, false
	}
	if index := strings.LastIndex(key, "@v"); index >= 0 {
		name = key[:index]
		version, err := strconv.Atoi(key[index+2:])
		if err != nil || version < 1 {
			return "", 0, false
		}
		return name, version, true
	}
	return key, 0, true
}

// SupportsAction accepts an exact versioned capability. An unversioned action
// key is treated as a legacy wildcard for that action only.
func SupportsAction(capabilities []string, actionName string, actionVersion int) bool {
	required := RequiredCapability(actionName, actionVersion)
	for _, capability := range capabilities {
		if capability == required || capability == actionName {
			return true
		}
	}
	return false
}

func SummarizeActionAccess(capabilities []string, actions []ActionDefinition) (ActionAccess, int, bool) {
	access := ActionAccessReadOnly
	available := 0
	for _, action := range actions {
		if !SupportsAction(capabilities, action.Name, action.Version) {
			continue
		}
		if ActionSafetyContract(action.Name, action.RiskLevel).Change && !SupportsChangeSafety(capabilities) {
			continue
		}
		available++
		switch action.RiskLevel {
		case RiskHigh, RiskCritical:
			access = ActionAccessRestrictedChange
		case RiskMedium:
			if access != ActionAccessRestrictedChange {
				access = ActionAccessChange
			}
		}
	}
	return access, available, len(actions) > available
}
