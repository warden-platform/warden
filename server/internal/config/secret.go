package config

import (
	"fmt"
	"os"
	"strings"
)

// Secret reads a setting from NAME or NAME_FILE. File-backed settings keep
// secret values out of process arguments and service definitions.
func Secret(name string) (string, error) {
	value, valueSet := os.LookupEnv(name)
	file, fileSet := os.LookupEnv(name + "_FILE")
	if valueSet && fileSet {
		return "", fmt.Errorf("%s and %s_FILE cannot both be set", name, name)
	}
	if fileSet {
		if strings.TrimSpace(file) == "" {
			return "", fmt.Errorf("%s_FILE is empty", name)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read %s_FILE: %w", name, err)
		}
		value = strings.TrimSpace(string(data))
		if value == "" {
			return "", fmt.Errorf("%s_FILE contains an empty value", name)
		}
		return value, nil
	}
	return strings.TrimSpace(value), nil
}

func RequiredSecret(name string, minimumLength int) (string, error) {
	value, err := Secret(name)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%s or %s_FILE is required", name, name)
	}
	if len(value) < minimumLength {
		return "", fmt.Errorf("%s must contain at least %d bytes", name, minimumLength)
	}
	return value, nil
}
