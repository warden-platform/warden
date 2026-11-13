package control

import (
	"bytes"
	"encoding/json"
	"fmt"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"
)

// validateActionParameters enforces the same JSON Schema exposed by the API
// before policy evaluation, persistence, approval, or Connector dispatch.
func validateActionParameters(schema map[string]any, parameters map[string]any) error {
	if len(schema) == 0 {
		return nil
	}
	payload, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("encode action parameter schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("action-parameters.json", bytes.NewReader(payload)); err != nil {
		return fmt.Errorf("load action parameter schema: %w", err)
	}
	compiled, err := compiler.Compile("action-parameters.json")
	if err != nil {
		return fmt.Errorf("compile action parameter schema: %w", err)
	}
	if parameters == nil {
		parameters = map[string]any{}
	}
	if err := compiled.Validate(parameters); err != nil {
		return fmt.Errorf("parameters do not match action schema: %w", err)
	}
	return nil
}
