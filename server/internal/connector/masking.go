package connector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Column classification → output policy (§13.2). The control plane owns the
// classification; the connector applies the resulting policy to each returned
// cell so business data never leaves the connector unmasked.
type MaskingPolicy string

const (
	MaskAllow MaskingPolicy = "allow"
	MaskMask  MaskingPolicy = "mask"
	MaskHash  MaskingPolicy = "hash"
	MaskDeny  MaskingPolicy = "deny"
)

// ParseMaskingPolicy normalizes a policy token, rejecting unknowns.
func ParseMaskingPolicy(value string) (MaskingPolicy, error) {
	switch MaskingPolicy(value) {
	case MaskAllow, MaskMask, MaskHash, MaskDeny:
		return MaskingPolicy(value), nil
	default:
		return "", fmt.Errorf("unsupported masking policy %q", value)
	}
}

// parseMaskingPolicies reads the optional "masking" task parameter: an object
// keyed by output alias whose values are policy tokens. The parameter is
// injected by the control plane after parameter validation (never trusted from
// the agent), so it is deliberately absent from the action parameter schema.
func parseMaskingPolicies(parameters map[string]any) (map[string]MaskingPolicy, error) {
	raw, ok := parameters["masking"]
	if !ok || raw == nil {
		return nil, nil
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("masking must be an object keyed by output alias")
	}
	policies := make(map[string]MaskingPolicy, len(object))
	for alias, value := range object {
		token, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("masking policy for %q must be a string", alias)
		}
		policy, err := ParseMaskingPolicy(token)
		if err != nil {
			return nil, fmt.Errorf("masking %q: %w", alias, err)
		}
		policies[alias] = policy
	}
	return policies, nil
}

// applyMasking rewrites a result row according to the per-column policies keyed
// by output alias. It returns the masked row and the number of cells that were
// masked or hashed. A deny policy drops the column entirely.
func applyMasking(row map[string]any, policies map[string]MaskingPolicy) (map[string]any, int) {
	if len(policies) == 0 {
		return row, 0
	}
	masked := 0
	result := make(map[string]any, len(row))
	for column, value := range row {
		policy, ok := policies[column]
		if !ok || policy == MaskAllow {
			result[column] = value
			continue
		}
		switch policy {
		case MaskDeny:
			continue
		case MaskMask:
			result[column] = maskValue(value)
			masked++
		case MaskHash:
			result[column] = hashValue(value)
			masked++
		}
	}
	return result, masked
}

func maskValue(value any) any {
	switch current := value.(type) {
	case string:
		runes := []rune(current)
		if len(runes) <= 2 {
			return "***"
		}
		return string(runes[:2]) + "***"
	case float64, int, int32, int64, json.Number:
		return 0
	case bool:
		return nil
	default:
		return "[MASKED]"
	}
}

func hashValue(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		encoded = []byte(fmt.Sprint(value))
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
