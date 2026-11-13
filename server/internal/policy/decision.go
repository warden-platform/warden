package policy

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"

	"gitee.com/pam-platform/pam/server/internal/domain"
	"github.com/open-policy-agent/opa/rego"
)

type Provider interface {
	Evaluate(context.Context, Input) (*Output, error)
}

type Input struct {
	Principal   PrincipalInfo  `json:"principal"`
	Resource    ResourceInfo   `json:"resource"`
	Action      string         `json:"action"`
	ProjectID   string         `json:"project_id"`
	ActionRisk  string         `json:"action_risk"`
	Parameters  map[string]any `json:"parameters"`
	Environment string         `json:"environment"`
	Connector   ConnectorInfo  `json:"connector"`
}

type PrincipalInfo struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type ResourceInfo struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Environment string `json:"environment"`
	OwnerTeam   string `json:"owner_team"`
}

type ConnectorInfo struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

type Output struct {
	Decision             domain.DecisionResult `json:"decision"`
	ReasonCodes          []string              `json:"reason_codes"`
	PolicyBundleHash     string                `json:"policy_bundle_hash"`
	MaxDurationSeconds   int                   `json:"max_duration_seconds"`
	CredentialMode       string                `json:"credential_mode"`
	ParameterConstraints map[string]any        `json:"parameter_constraints"`
	Obligations          []string              `json:"obligations"`
}

func NewProvider() (Provider, error) {
	if os.Getenv("PAM_DECISION_MODE") == "remote" {
		return NewRemoteProvider()
	}
	return NewEmbeddedProvider(embeddedPolicy)
}

type EmbeddedProvider struct {
	query      rego.PreparedEvalQuery
	bundleHash string
}

func NewEmbeddedProvider(policy string) (*EmbeddedProvider, error) {
	query, err := rego.New(
		rego.Query("data.pam.kubernetes.decision"),
		rego.Module("pam-kubernetes.rego", policy),
	).PrepareForEval(context.Background())
	if err != nil {
		return nil, fmt.Errorf("prepare embedded Rego policy: %w", err)
	}
	sum := sha256.Sum256([]byte(policy))
	return &EmbeddedProvider{query: query, bundleHash: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

func (p *EmbeddedProvider) Evaluate(ctx context.Context, input Input) (*Output, error) {
	results, err := p.query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return nil, fmt.Errorf("evaluate embedded Rego policy: %w", err)
	}
	if len(results) != 1 || len(results[0].Expressions) != 1 {
		return nil, fmt.Errorf("embedded Rego policy returned no decision")
	}
	value, ok := results[0].Expressions[0].Value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("embedded Rego policy returned an invalid decision object")
	}
	output, err := decodeOutput(value)
	if err != nil {
		return nil, err
	}
	output.PolicyBundleHash = p.bundleHash
	return output, nil
}

func validateOutput(output *Output) error {
	switch output.Decision {
	case domain.DecisionAllow, domain.DecisionDeny, domain.DecisionRequireApproval:
	default:
		return fmt.Errorf("unknown policy decision %q", output.Decision)
	}
	if len(output.ReasonCodes) == 0 {
		return fmt.Errorf("policy decision is missing reason_codes")
	}
	if output.PolicyBundleHash == "" {
		return fmt.Errorf("policy decision is missing policy_bundle_hash")
	}
	if output.MaxDurationSeconds <= 0 || output.MaxDurationSeconds > 3600 {
		return fmt.Errorf("invalid max_duration_seconds %d", output.MaxDurationSeconds)
	}
	if output.CredentialMode == "" {
		return fmt.Errorf("policy decision is missing credential_mode")
	}
	return nil
}

//go:embed policy.rego
var embeddedPolicy string
