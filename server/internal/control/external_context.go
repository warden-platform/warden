package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

var externalContextIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+\-]{0,254}$`)
var externalContextSourcePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

// normalizeExternalExecutionContext validates the fixed external attribution
// envelope and converts it to the JSON representation stored by Warden. Unknown
// fields cannot enter this representation because only the allow-listed domain
// fields are copied. The envelope is deliberately not used as an authorization
// input; it records how an authenticated caller reached Warden.
func normalizeExternalExecutionContext(input *domain.ExternalExecutionContext) (map[string]any, error) {
	if input == nil {
		return nil, nil
	}
	trimmed := *input
	trimmed.Source = strings.TrimSpace(trimmed.Source)
	if trimmed.Source == "" || !externalContextSourcePattern.MatchString(trimmed.Source) {
		return nil, fmt.Errorf("externalContext.source 必须是小写平台标识")
	}
	values := map[string]string{
		"workspaceId":         strings.TrimSpace(trimmed.WorkspaceID),
		"projectId":           strings.TrimSpace(trimmed.ProjectID),
		"agentId":             strings.TrimSpace(trimmed.AgentID),
		"taskId":              strings.TrimSpace(trimmed.TaskID),
		"issueId":             strings.TrimSpace(trimmed.IssueID),
		"originatorUserId":    strings.TrimSpace(trimmed.OriginatorUserID),
		"accountableUserId":   strings.TrimSpace(trimmed.AccountableUserID),
		"originatorSource":    strings.TrimSpace(trimmed.OriginatorSource),
		"delegatedFromTaskId": strings.TrimSpace(trimmed.DelegatedFromTaskID),
	}
	hasCorrelation := false
	for key, value := range values {
		if value == "" {
			continue
		}
		if len(value) > 255 || !externalContextIDPattern.MatchString(value) {
			return nil, fmt.Errorf("externalContext.%s 格式非法或长度超过 255", key)
		}
		if key != "originatorSource" {
			hasCorrelation = true
		}
	}
	if values["originatorSource"] != "" && !externalContextSourcePattern.MatchString(values["originatorSource"]) {
		return nil, fmt.Errorf("externalContext.originatorSource 格式非法")
	}
	if !hasCorrelation {
		return nil, fmt.Errorf("externalContext 至少需要一个外部关联标识")
	}

	result := map[string]any{"source": trimmed.Source}
	for key, value := range values {
		if value != "" {
			result[key] = value
		}
	}
	return result, nil
}

func externalContextsEqual(left, right map[string]any) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func attachExternalContextAudit(event *domain.AuditEvent, context map[string]any) {
	if event == nil || len(context) == 0 {
		return
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	event.Metadata["external_context"] = context
}

// attachDelegationAudit records the immutable Warden-side attribution for work
// performed through an external agent delegation. These identifiers are
// metadata rather than credential_id/target_id so existing audit foreign keys
// retain their meaning and the event remains queryable after token revocation.
func attachDelegationAudit(event *domain.AuditEvent, delegationTokenID, onBehalfOfPrincipalID, accountablePrincipalID string) {
	if event == nil || (delegationTokenID == "" && onBehalfOfPrincipalID == "" && accountablePrincipalID == "") {
		return
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	if delegationTokenID != "" {
		event.Metadata["delegation_token_id"] = delegationTokenID
	}
	if onBehalfOfPrincipalID != "" {
		event.Metadata["on_behalf_of_principal_id"] = onBehalfOfPrincipalID
	}
	if accountablePrincipalID != "" {
		event.Metadata["accountable_principal_id"] = accountablePrincipalID
	}
}

func attachExecutionAudit(event *domain.AuditEvent, execution *domain.Execution) {
	if execution == nil {
		return
	}
	attachExternalContextAudit(event, execution.ExternalContext)
	attachDelegationAudit(event, execution.DelegationTokenID, execution.OnBehalfOfPrincipalID, execution.AccountablePrincipalID)
}

func attachRequestAudit(event *domain.AuditEvent, request *domain.AccessRequest) {
	if request == nil {
		return
	}
	attachExternalContextAudit(event, request.ExternalContext)
	attachDelegationAudit(event, request.DelegationTokenID, request.OnBehalfOfPrincipalID, request.AccountablePrincipalID)
}
