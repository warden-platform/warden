package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// QueryApprovalContextParameter is injected by the control plane only while
// dispatching an approved ad-hoc database query. It lives inside Parameters so
// v1 task-envelope signatures remain compatible with older Connectors, while a
// new Connector can distinguish approval-backed and automatic Grants.
const QueryApprovalContextParameter = "_pamQueryApproval"

type TaskEnvelope struct {
	Version       int                   `json:"version"`
	ExecutionID   string                `json:"executionId"`
	Attempt       int                   `json:"attempt"`
	DispatchToken string                `json:"dispatchToken,omitempty"`
	ConnectorID   string                `json:"connectorId"`
	Resource      TaskResource          `json:"resource"`
	Action        TaskAction            `json:"action"`
	Parameters    map[string]any        `json:"parameters"`
	ParameterHash string                `json:"parameterHash"`
	Grant         TaskGrant             `json:"grant"`
	Safety        *ChangeSafetyContract `json:"safety,omitempty"`
	Provider      *ProviderTask         `json:"provider,omitempty"`
	TraceID       string                `json:"traceId"`
	IssuedAt      time.Time             `json:"issuedAt"`
	Signature     string                `json:"signature"`
}

// ProviderTask is populated only for provider.request deliveries. The binding
// is resolved by the control plane and covered by the task signature, so the
// Connector can independently repeat all boundary checks without trusting
// agent-supplied Integration or capability data.
type ProviderTask struct {
	Request ProviderRequest        `json:"request"`
	Binding ProviderRequestBinding `json:"binding"`
}

type TaskResource struct {
	ID            string `json:"id"`
	ExternalID    string `json:"externalId"`
	Type          string `json:"type"`
	Name          string `json:"name"`
	Namespace     string `json:"namespace,omitempty"`
	IntegrationID string `json:"integrationId,omitempty"`
}

type TaskAction struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

type TaskGrant struct {
	ID        string    `json:"id"`
	ScopeHash string    `json:"scopeHash"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (e *TaskEnvelope) Sign(key []byte) error {
	if len(key) < 32 {
		return fmt.Errorf("task signing key must be at least 32 bytes")
	}
	payload, err := e.signingPayload()
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	e.Signature = "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
	return nil
}

func (e TaskEnvelope) Verify(key []byte, now time.Time) error {
	if e.Version != 1 {
		return fmt.Errorf("unsupported task envelope version %d", e.Version)
	}
	if e.DispatchToken == "" || e.Attempt < 1 {
		return fmt.Errorf("task dispatch lease is missing")
	}
	if now.After(e.Grant.ExpiresAt) {
		return fmt.Errorf("task grant has expired")
	}
	if e.IssuedAt.After(now.Add(time.Minute)) || e.IssuedAt.Before(now.Add(-5*time.Minute)) {
		return fmt.Errorf("task envelope is outside its issue window")
	}
	provided := e.Signature
	payload, err := e.signingPayload()
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	expected := "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(provided), []byte(expected)) {
		return fmt.Errorf("invalid task envelope signature")
	}
	if HashParameters(e.Parameters) != e.ParameterHash {
		return fmt.Errorf("task parameter hash mismatch")
	}
	return nil
}

func (e TaskEnvelope) signingPayload() ([]byte, error) {
	e.Signature = ""
	// DispatchToken is an opaque delivery capability transported over the
	// Connector-authenticated channel. Excluding it keeps v1 envelopes readable
	// by connectors built before dispatch leases were introduced.
	e.DispatchToken = ""
	return json.Marshal(e)
}
