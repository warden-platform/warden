package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// IsControlPlaneAction reports whether the action is executed inline by the
// control plane instead of being dispatched to a Connector. Control-plane
// actions never claim tasks, never require a Connector heartbeat and never
// create execution Grants.
func IsControlPlaneAction(name string) bool {
	return strings.HasPrefix(name, "credential.")
}

// credentialNamePattern mirrors credentialReferencePattern in the control
// package: lowercase letters, digits, dots and hyphens, 1-253 characters.
var credentialNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)

// ValidateCredentialName reports whether name is a valid pam: credential
// reference (used both by the create parameter schema and by pam: consumers).
func ValidateCredentialName(name string) bool {
	return credentialNamePattern.MatchString(name)
}

// MaskSecretValue produces a masked preview for audit and display. Empty
// values stay empty; values up to 8 characters collapse to "****"; longer
// values keep the first two and last four characters: "ab****wxyz".
func MaskSecretValue(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 8 {
		return "****"
	}
	return string(runes[:2]) + "****" + string(runes[len(runes)-4:])
}

// HashValue returns the sha256 hex digest of a secret. This is the only
// representation of a secret that is allowed to be persisted in
// executions.parameters or exposed in audit metadata.
func HashValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// ExtractCredentialValue moves the plaintext "value" out of parameters and
// replaces it with "value_sha256", so the persisted executions.parameters,
// access requests and audit trail never contain plaintext. It returns the
// plaintext secret (which lives only in memory) and whether the action
// actually carries one (credential.create / credential.rotate).
func ExtractCredentialValue(actionName string, parameters map[string]any) (string, bool) {
	if !IsControlPlaneAction(actionName) {
		return "", false
	}
	if actionName != "credential.create" && actionName != "credential.rotate" {
		return "", false
	}
	secret, _ := parameters["value"].(string)
	delete(parameters, "value")
	parameters["value_sha256"] = HashValue(secret)
	return secret, true
}
