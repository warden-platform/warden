package domain

import "time"

// Principal represents a user, agent type, agent instance, or workload identity.
type Principal struct {
	ID           string          `json:"id"`
	Kind         PrincipalKind   `json:"kind"`
	Name         string          `json:"name"`
	Status       PrincipalStatus `json:"status"`
	TenantID     string          `json:"tenant_id"`
	Metadata     map[string]any  `json:"metadata"`
	PasswordHash string          `json:"-"`
	LastLoginAt  *time.Time      `json:"last_login_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type PrincipalKind string

const (
	PrincipalKindUser          PrincipalKind = "user"
	PrincipalKindAgentType     PrincipalKind = "agent_type"
	PrincipalKindAgentInstance PrincipalKind = "agent_instance"
	PrincipalKindWorkload      PrincipalKind = "workload"
)

type PrincipalStatus string

const (
	PrincipalStatusActive   PrincipalStatus = "ACTIVE"
	PrincipalStatusDisabled PrincipalStatus = "DISABLED"
)

// PrincipalAPIKey is a one-way stored credential bound to exactly one Principal.
// The plaintext secret is never persisted and is returned only at creation time.
type PrincipalAPIKey struct {
	ID               string        `json:"id"`
	PrincipalID      string        `json:"principal_id"`
	Name             string        `json:"name"`
	KeyPrefix        string        `json:"key_prefix"`
	KeyHash          []byte        `json:"-"`
	Status           APIKeyStatus  `json:"status"`
	Purpose          APIKeyPurpose `json:"purpose"`
	Audience         string        `json:"audience,omitempty"`
	Scopes           []string      `json:"scopes"`
	ExpiresAt        *time.Time    `json:"expires_at,omitempty"`
	LastUsedAt       *time.Time    `json:"last_used_at,omitempty"`
	LastUsedIP       string        `json:"last_used_ip,omitempty"`
	CreatedBy        string        `json:"created_by,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	RevokedAt        *time.Time    `json:"revoked_at,omitempty"`
	RevokedBy        string        `json:"revoked_by,omitempty"`
	RotationParentID string        `json:"rotation_parent_id,omitempty"`
}

// ExternalExecutionContext identifies the caller's context in an external
// orchestration platform. It is an attribution envelope, not an authorization
// input: Warden always authorizes the authenticated Principal, tenant, project
// membership, and acting project independently.
type ExternalExecutionContext struct {
	Source              string `json:"source,omitempty"`
	WorkspaceID         string `json:"workspaceId,omitempty"`
	ProjectID           string `json:"projectId,omitempty"`
	AgentID             string `json:"agentId,omitempty"`
	TaskID              string `json:"taskId,omitempty"`
	IssueID             string `json:"issueId,omitempty"`
	OriginatorUserID    string `json:"originatorUserId,omitempty"`
	AccountableUserID   string `json:"accountableUserId,omitempty"`
	OriginatorSource    string `json:"originatorSource,omitempty"`
	DelegatedFromTaskID string `json:"delegatedFromTaskId,omitempty"`
}

type ExternalIdentityType string

const (
	ExternalIdentityAgent ExternalIdentityType = "AGENT"
	ExternalIdentityUser  ExternalIdentityType = "USER"
)

type ExternalBindingStatus string

const (
	ExternalBindingActive  ExternalBindingStatus = "ACTIVE"
	ExternalBindingRevoked ExternalBindingStatus = "REVOKED"
)

// ExternalIdentityBinding maps a platform-owned identity to a Warden Principal.
// Agent bindings may additionally pin one external project to one Warden project.
type ExternalIdentityBinding struct {
	ID                string                `json:"id"`
	TenantID          string                `json:"tenant_id,omitempty"`
	Source            string                `json:"source"`
	WorkspaceID       string                `json:"workspaceId"`
	ExternalProjectID string                `json:"externalProjectId,omitempty"`
	IdentityType      ExternalIdentityType  `json:"identityType"`
	ExternalID        string                `json:"externalId"`
	PrincipalID       string                `json:"principalId"`
	ProjectID         string                `json:"projectId,omitempty"`
	Status            ExternalBindingStatus `json:"status"`
	CreatedBy         string                `json:"createdBy,omitempty"`
	CreatedAt         time.Time             `json:"createdAt"`
	UpdatedAt         time.Time             `json:"updatedAt"`
	RevokedAt         *time.Time            `json:"revokedAt,omitempty"`
}

// ExternalTenantBinding links a trusted external enterprise/workspace pair to
// one Warden tenant. The external identifiers are context keys only; Warden still
// performs all tenant and project authorization using its own records.
type ExternalTenantBinding struct {
	ID                   string     `json:"id"`
	TenantID             string     `json:"tenantId"`
	Source               string     `json:"source"`
	ExternalEnterpriseID string     `json:"externalEnterpriseId"`
	ExternalWorkspaceID  string     `json:"externalWorkspaceId"`
	Status               string     `json:"status"`
	CreatedBy            string     `json:"createdBy,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	RevokedAt            *time.Time `json:"revokedAt,omitempty"`
}

// FederatedIdentity is the stable issuer/subject link for a user provisioned
// by an external identity provider.
type FederatedIdentity struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenantId"`
	Issuer      string     `json:"issuer"`
	Subject     string     `json:"subject"`
	PrincipalID string     `json:"principalId"`
	Email       string     `json:"email,omitempty"`
	DisplayName string     `json:"displayName,omitempty"`
	Status      string     `json:"status"`
	LastLoginAt *time.Time `json:"lastLoginAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// ExternalProjectBinding maps one external project to an existing Warden
// project. Provisioning deliberately does not create this relationship
// automatically; a Warden tenant administrator must choose the target project.
type ExternalProjectBinding struct {
	ID                  string     `json:"id"`
	TenantID            string     `json:"tenantId"`
	Source              string     `json:"source"`
	ExternalWorkspaceID string     `json:"externalWorkspaceId"`
	ExternalProjectID   string     `json:"externalProjectId"`
	ExternalProjectName string     `json:"externalProjectName,omitempty"`
	ProjectID           string     `json:"projectId"`
	Status              string     `json:"status"`
	CreatedBy           string     `json:"createdBy,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	RevokedAt           *time.Time `json:"revokedAt,omitempty"`
}

// DelegationToken is a short-lived opaque credential issued after Warden has
// verified an external Agent binding and any supplied external user bindings.
// TokenHash is never returned by the API.
type DelegationToken struct {
	ID                     string         `json:"id"`
	TenantID               string         `json:"tenant_id,omitempty"`
	ParentCredentialID     string         `json:"parentCredentialId"`
	PrincipalID            string         `json:"principalId"`
	OnBehalfOfPrincipalID  string         `json:"onBehalfOfPrincipalId,omitempty"`
	AccountablePrincipalID string         `json:"accountablePrincipalId,omitempty"`
	TokenHash              []byte         `json:"-"`
	Scopes                 []string       `json:"scopes"`
	ExternalContext        map[string]any `json:"externalContext"`
	ActingProjectID        string         `json:"actingProjectId,omitempty"`
	IssuedAt               time.Time      `json:"issuedAt"`
	ExpiresAt              time.Time      `json:"expiresAt"`
	LastUsedAt             *time.Time     `json:"lastUsedAt,omitempty"`
	RevokedAt              *time.Time     `json:"revokedAt,omitempty"`
}

type APIKeyStatus string

type APIKeyPurpose string

const (
	APIKeyPurposeAgent        APIKeyPurpose = "AGENT"
	APIKeyPurposeRegistration APIKeyPurpose = "REGISTRATION"
)

const (
	APIKeyActive  APIKeyStatus = "ACTIVE"
	APIKeyRevoked APIKeyStatus = "REVOKED"
)

// Connector represents a connector runtime instance.
type Connector struct {
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	ConnectorType       string          `json:"connector_type"`
	Status              ConnectorStatus `json:"status"`
	Environment         string          `json:"environment"`
	TargetHost          string          `json:"target_host,omitempty"`
	Version             string          `json:"version"`
	Capabilities        []string        `json:"capabilities"`
	Permissions         map[string]any  `json:"permissions"`
	Config              map[string]any  `json:"config"`
	ProjectID           string          `json:"project_id,omitempty"`
	LastHeartbeat       *time.Time      `json:"last_heartbeat,omitempty"`
	TenantID            string          `json:"tenant_id"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	DeletionStatus      string          `json:"deletion_status,omitempty"`
	AvailableNamespaces []string        `json:"available_namespaces,omitempty"`
	AvailableServices   []string        `json:"available_services,omitempty"`
	AvailableContainers []string        `json:"available_containers,omitempty"`
}

type ConnectorStatus string

const (
	ConnectorDraft       ConnectorStatus = "DRAFT"
	ConnectorTesting     ConnectorStatus = "TESTING"
	ConnectorActive      ConnectorStatus = "ACTIVE"
	ConnectorDegraded    ConnectorStatus = "DEGRADED"
	ConnectorQuarantined ConnectorStatus = "QUARANTINED"
	ConnectorDisabled    ConnectorStatus = "DISABLED"
)

// Resource represents a target system resource (e.g., a Kubernetes Deployment).
type Resource struct {
	ID                    string           `json:"id"`
	ResourceType          string           `json:"resource_type"`
	ExternalID            string           `json:"external_id"`
	Name                  string           `json:"name"`
	Namespace             string           `json:"namespace,omitempty"`
	ConnectorID           string           `json:"connector_id"`
	ManagementType        ManagementType   `json:"management_type"`
	IntegrationID         string           `json:"integration_id,omitempty"`
	ProjectID             string           `json:"project_id,omitempty"`
	ProjectName           string           `json:"project_name,omitempty"`
	ProjectSource         string           `json:"project_source,omitempty"`
	ProjectScopeMode      ProjectScopeMode `json:"project_scope_mode"`
	ProjectIDs            []string         `json:"project_ids"`
	ProjectNames          []string         `json:"project_names"`
	OwnerTeam             string           `json:"owner_team,omitempty"`
	Environment           string           `json:"environment"`
	EnvironmentSource     string           `json:"environment_source,omitempty"`
	Status                ResourceStatus   `json:"status"`
	Labels                map[string]any   `json:"labels"`
	Annotations           map[string]any   `json:"annotations"`
	Metadata              map[string]any   `json:"metadata"`
	TenantID              string           `json:"tenant_id"`
	CreatedAt             time.Time        `json:"created_at"`
	UpdatedAt             time.Time        `json:"updated_at"`
	LastSeenAt            *time.Time       `json:"last_seen_at,omitempty"`
	SyncGeneration        string           `json:"-"`
	ActionAccess          ActionAccess     `json:"action_access,omitempty"`
	ActionUpgradeRequired bool             `json:"action_upgrade_required"`
	AvailableActionCount  int              `json:"available_action_count"`
}

type ProjectScopeMode string

const (
	ProjectScopeInherited  ProjectScopeMode = "INHERITED"
	ProjectScopeSelected   ProjectScopeMode = "SELECTED_PROJECTS"
	ProjectScopeAll        ProjectScopeMode = "ALL_PROJECTS"
	ProjectScopeUnassigned ProjectScopeMode = "UNASSIGNED"
)

type ResourceProjectScope struct {
	Mode         ProjectScopeMode `json:"mode"`
	ProjectIDs   []string         `json:"project_ids"`
	ProjectNames []string         `json:"project_names"`
}

type ManagementType string

const (
	ManagementConnector    ManagementType = "connector"
	ManagementControlPlane ManagementType = "control_plane"
)

type ActionAccess string

const (
	ActionAccessReadOnly         ActionAccess = "READ_ONLY"
	ActionAccessChange           ActionAccess = "CHANGE"
	ActionAccessRestrictedChange ActionAccess = "RESTRICTED_CHANGE"
)

type ResourceStatus string

const (
	ResourceActive     ResourceStatus = "ACTIVE"
	ResourceDiscovered ResourceStatus = "DISCOVERED"
	ResourceOrphaned   ResourceStatus = "ORPHANED"
	ResourceDisabled   ResourceStatus = "DISABLED"
)

// Integration is a first-class attach point on a connector: one cluster
// connector can host N integrations (e.g. multiple Jenkins instances), each
// with its own endpoint, credential reference, scope, and lifecycle.
type Integration struct {
	ID                 string                         `json:"id"`
	ConnectorID        string                         `json:"connector_id"`
	Protocol           string                         `json:"protocol"`
	Name               string                         `json:"name"`
	Endpoint           string                         `json:"endpoint"`
	Username           string                         `json:"username,omitempty"`
	CredentialRef      string                         `json:"credential_ref"`
	ProjectID          string                         `json:"project_id,omitempty"`
	Environment        string                         `json:"environment,omitempty"`
	ProjectIDs         []string                       `json:"project_ids"`
	ProjectRules       []IntegrationProjectRule       `json:"project_rules"`
	CredentialProfiles []IntegrationCredentialProfile `json:"credential_profiles"`
	Scope              map[string]any                 `json:"scope"`
	Status             IntegrationStatus              `json:"status"`
	LastProbeAt        *time.Time                     `json:"last_probe_at,omitempty"`
	LastError          string                         `json:"last_error,omitempty"`
	TenantID           string                         `json:"tenant_id"`
	CreatedAt          time.Time                      `json:"created_at"`
	UpdatedAt          time.Time                      `json:"updated_at"`
}

// IntegrationCredentialProfile attaches an additional purpose-bound credential
// to a database integration (e.g. a SELECT-only "query" account vs a "ddl"
// account). The Agent cannot choose a profile; routing is by action type.
type IntegrationCredentialProfile struct {
	ID            string    `json:"id,omitempty"`
	IntegrationID string    `json:"integration_id,omitempty"`
	Name          string    `json:"name"`
	Username      string    `json:"username,omitempty"`
	CredentialRef string    `json:"credential_ref"`
	TenantID      string    `json:"tenant_id,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// IntegrationProjectRule assigns a discovered resource to its primary project.
// The first release deliberately supports prefix matching only: it is easy to
// preview, deterministic, and avoids surprising glob/regexp behavior.
type IntegrationProjectRule struct {
	ID            string    `json:"id,omitempty"`
	IntegrationID string    `json:"integration_id,omitempty"`
	ProjectID     string    `json:"project_id"`
	MatcherType   string    `json:"matcher_type"`
	Pattern       string    `json:"pattern"`
	Priority      int       `json:"priority"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

type IntegrationStatus string

const (
	IntegrationActive   IntegrationStatus = "ACTIVE"
	IntegrationDisabled IntegrationStatus = "DISABLED"
	IntegrationFailed   IntegrationStatus = "FAILED"
)

// ActionDefinition defines a structured action that can be performed on a resource type.
type ActionDefinition struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Version         int            `json:"version"`
	DisplayName     string         `json:"display_name,omitempty"`
	Description     string         `json:"description,omitempty"`
	ResourceType    string         `json:"resource_type"`
	RiskLevel       RiskLevel      `json:"risk_level"`
	ExecutionMode   ExecutionMode  `json:"execution_mode"`
	ExecutorType    ExecutorType   `json:"executor_type"`
	ParameterSchema map[string]any `json:"parameter_schema"`
	TimeoutSeconds  int            `json:"timeout_seconds"`
	TenantID        string         `json:"tenant_id"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type ExecutorType string

const (
	ExecutorConnector    ExecutorType = "connector"
	ExecutorControlPlane ExecutorType = "control_plane"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type ExecutionMode string

const (
	ExecutionModeSync  ExecutionMode = "SYNCHRONOUS"
	ExecutionModeAsync ExecutionMode = "ASYNCHRONOUS"
)

// PolicyDecision is the immutable result of evaluating a request against OPA.
type PolicyDecision struct {
	ID              string         `json:"id"`
	ExecutionID     string         `json:"execution_id,omitempty"`
	PrincipalID     string         `json:"principal_id,omitempty"`
	ResourceID      string         `json:"resource_id,omitempty"`
	ActionID        string         `json:"action_id,omitempty"`
	ActingProjectID string         `json:"acting_project_id,omitempty"`
	OPAInput        map[string]any `json:"opa_input"`
	OPAOutput       map[string]any `json:"opa_output"`
	Result          DecisionResult `json:"result"`
	BundleHash      string         `json:"bundle_hash,omitempty"`
	DecisionSeconds int            `json:"decision_seconds"`
	CreatedAt       time.Time      `json:"created_at"`
}

type DecisionResult string

const (
	DecisionAllow           DecisionResult = "ALLOW"
	DecisionDeny            DecisionResult = "DENY"
	DecisionRequireApproval DecisionResult = "REQUIRE_APPROVAL"
)

// AccessRequest represents a pending approval request.
type AccessRequest struct {
	ID                     string         `json:"id"`
	TenantID               string         `json:"tenant_id,omitempty"`
	ExecutionID            string         `json:"execution_id"`
	PrincipalID            string         `json:"principal_id"`
	PrincipalName          string         `json:"principal_name,omitempty"` // enriched by handler
	ResourceID             string         `json:"resource_id"`
	ResourceName           string         `json:"resource_name,omitempty"`   // enriched by handler
	ResourceType           string         `json:"resource_type,omitempty"`   // enriched by handler
	ResourceState          string         `json:"resource_status,omitempty"` // enriched by handler
	ActionID               string         `json:"action_id"`
	ActingProjectID        string         `json:"acting_project_id,omitempty"`
	ExternalContext        map[string]any `json:"external_context,omitempty"`
	DelegationTokenID      string         `json:"delegation_token_id,omitempty"`
	OnBehalfOfPrincipalID  string         `json:"on_behalf_of_principal_id,omitempty"`
	AccountablePrincipalID string         `json:"accountable_principal_id,omitempty"`
	ActionName             string         `json:"action_name,omitempty"`         // enriched by handler
	ActionDisplay          string         `json:"action_display_name,omitempty"` // enriched by handler
	ActionRisk             RiskLevel      `json:"risk_level,omitempty"`          // enriched by handler
	ApprovalContext        map[string]any `json:"approval_context,omitempty"`    // enriched trusted policy/preview context
	Parameters             map[string]any `json:"parameters"`
	ParameterHash          string         `json:"parameter_hash"`
	Reason                 string         `json:"reason,omitempty"`
	Status                 RequestStatus  `json:"status"`
	DecisionID             string         `json:"decision_id,omitempty"`
	ApprovedBy             string         `json:"approved_by,omitempty"`
	ApprovedAt             *time.Time     `json:"approved_at,omitempty"`
	ExpiresAt              *time.Time     `json:"expires_at,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
}

type RequestStatus string

const (
	RequestPending   RequestStatus = "PENDING"
	RequestApproved  RequestStatus = "APPROVED"
	RequestRejected  RequestStatus = "REJECTED"
	RequestExpired   RequestStatus = "EXPIRED"
	RequestCancelled RequestStatus = "CANCELLED"
)

// ApprovalRule is a DB-driven override that supplements the embedded Rego
// policy. When Rego says REQUIRE_APPROVAL, the control plane consults the
// highest-priority rule matching (risk_level, environment) to decide whether
// approval is actually needed. This lets admins toggle approval per risk level
// / environment without editing Rego code.
type ApprovalRule struct {
	ID               string    `json:"id"`
	RiskLevel        RiskLevel `json:"risk_level"`
	Environment      string    `json:"environment"` // "*" matches any
	RequiresApproval bool      `json:"requires_approval"`
	Priority         int       `json:"priority"`
	Description      string    `json:"description,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// PolicyMatrixRule is the administrator-managed, attribute-based rule layer
// applied after the embedded Rego baseline. Empty project/risk/principal fields
// and "*" string fields are wildcards.
type PolicyMatrixRule struct {
	ID                   string         `json:"id"`
	Name                 string         `json:"name"`
	ProjectID            string         `json:"project_id,omitempty"`
	ProjectName          string         `json:"project_name,omitempty"`
	ResourceType         string         `json:"resource_type"`
	ActionName           string         `json:"action_name"`
	Environment          string         `json:"environment"`
	RiskLevel            string         `json:"risk_level"`
	PrincipalKind        string         `json:"principal_kind"`
	Decision             DecisionResult `json:"decision"`
	Priority             int            `json:"priority"`
	Enabled              bool           `json:"enabled"`
	Description          string         `json:"description,omitempty"`
	HitCount             int64          `json:"hit_count"`
	LastHitAt            *time.Time     `json:"last_hit_at,omitempty"`
	LegacyApprovalRuleID string         `json:"-"`
	ConflictIDs          []string       `json:"conflict_ids"`
	ShadowedBy           string         `json:"shadowed_by,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

type PolicyMatchInput struct {
	ProjectID     string
	ResourceType  string
	ActionName    string
	Environment   string
	RiskLevel     string
	PrincipalKind string
}

// NotificationChannel stores non-secret delivery preferences. Provider
// credentials are supplied to warden-control through file-backed secrets and are
// never persisted in PostgreSQL or returned by the API.
type NotificationChannel struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Events    []string  `json:"events"`
	TenantID  string    `json:"tenant_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DingTalkPrincipalBinding is an explicit trust mapping used by interactive
// approval callbacks. A DingTalk user can never approve as an arbitrary Warden
// principal without an administrator-created binding.
type DingTalkPrincipalBinding struct {
	UserID        string    `json:"user_id"`
	PrincipalID   string    `json:"principal_id"`
	DisplayName   string    `json:"display_name,omitempty"`
	PrincipalName string    `json:"principal_name"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// AccessGrant is an immutable time-bound grant for a specific execution scope.
type AccessGrant struct {
	ID              string         `json:"id"`
	TenantID        string         `json:"tenant_id"`
	RequestID       string         `json:"request_id,omitempty"`
	PrincipalID     string         `json:"principal_id"`
	ActingProjectID string         `json:"acting_project_id,omitempty"`
	ResourceScope   map[string]any `json:"resource_scope"`
	ActionScope     map[string]any `json:"action_scope"`
	ParameterHash   string         `json:"parameter_hash"`
	ScopeHash       string         `json:"scope_hash"`
	MaxUses         int            `json:"max_uses"`
	UseCount        int            `json:"use_count"`
	TTLSeconds      int            `json:"ttl_seconds"`
	ExpiresAt       time.Time      `json:"expires_at"`
	Status          GrantStatus    `json:"status"`
	CreatedAt       time.Time      `json:"created_at"`
}

type GrantStatus string

const (
	GrantActive  GrantStatus = "ACTIVE"
	GrantUsed    GrantStatus = "USED"
	GrantRevoked GrantStatus = "REVOKED"
	GrantExpired GrantStatus = "EXPIRED"
)

// Execution represents a single execution of an action.
type Execution struct {
	ID                     string          `json:"id"`
	TenantID               string          `json:"tenant_id"`
	GrantID                string          `json:"grant_id,omitempty"`
	PrincipalID            string          `json:"principal_id"`
	PrincipalName          string          `json:"principal_name,omitempty"` // enriched by handler, not stored in DB
	PrincipalKind          string          `json:"principal_kind,omitempty"` // enriched by handler, not stored in DB
	ActingProjectID        string          `json:"acting_project_id,omitempty"`
	ExternalContext        map[string]any  `json:"external_context,omitempty"`
	DelegationTokenID      string          `json:"delegation_token_id,omitempty"`
	OnBehalfOfPrincipalID  string          `json:"on_behalf_of_principal_id,omitempty"`
	AccountablePrincipalID string          `json:"accountable_principal_id,omitempty"`
	CredentialID           string          `json:"credential_id,omitempty"`
	CredentialName         string          `json:"credential_name,omitempty"` // enriched by handler, not stored in DB
	ResourceID             string          `json:"resource_id"`
	ResourceName           string          `json:"resource_name,omitempty"` // enriched by handler, not stored in DB
	ResourceType           string          `json:"resource_type,omitempty"` // enriched by handler, not stored in DB
	ActionID               string          `json:"action_id"`
	ActionName             string          `json:"action_name,omitempty"`         // enriched by handler
	ActionDisplay          string          `json:"action_display_name,omitempty"` // enriched by handler
	ActionRisk             RiskLevel       `json:"risk_level,omitempty"`          // enriched by handler
	Parameters             map[string]any  `json:"parameters"`
	ParameterHash          string          `json:"parameter_hash"`
	Status                 ExecutionStatus `json:"status"`
	DecisionID             string          `json:"decision_id,omitempty"`
	IdempotencyKey         string          `json:"idempotency_key,omitempty"`
	TaskID                 string          `json:"task_id,omitempty"`
	TraceID                string          `json:"trace_id"`
	Reason                 string          `json:"reason,omitempty"`
	TimeoutSeconds         int             `json:"timeout_seconds"`
	ExpiresAt              *time.Time      `json:"expires_at,omitempty"`
	ConnectorID            string          `json:"connector_id,omitempty"`
	Result                 map[string]any  `json:"result,omitempty"`
	ErrorCode              string          `json:"error_code,omitempty"`
	ErrorMessage           string          `json:"error_message,omitempty"`
	ExecutorType           ExecutorType    `json:"executor_type"`
	PendingSecret          []byte          `json:"-"` // AES-GCM ciphertext held while awaiting approval (credential.create/rotate)
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

// CredentialStatus tracks the lifecycle of a Warden-managed credential.
type CredentialStatus string

const (
	CredentialActive  CredentialStatus = "ACTIVE"
	CredentialRevoked CredentialStatus = "REVOKED"
	CredentialExpired CredentialStatus = "EXPIRED"
)

// CredentialBinding restricts a Warden credential to a specific target. Empty
// bindings mean "allow all targets"; a non-empty list is enforced on pam:
// reference resolution and on credential.get scope checks.
type CredentialBinding struct {
	TargetType string `json:"target_type"`
	TargetName string `json:"target_name"`
}

// Credential is a Warden-managed encrypted credential. The plaintext value never
// leaves the control plane and is never returned by the API: only
// ValuePreview (masked) and value_sha256 (see HashValue) are exposed.
type Credential struct {
	ID             string              `json:"id"`
	TenantID       string              `json:"tenant_id"`
	Name           string              `json:"name"`
	Kind           string              `json:"kind"`
	Username       string              `json:"username,omitempty"`
	EncryptedValue []byte              `json:"-"`
	ValuePreview   string              `json:"value_preview"`
	Purpose        string              `json:"purpose,omitempty"`
	Scope          map[string]any      `json:"scope"`
	Status         CredentialStatus    `json:"status"`
	Version        int                 `json:"version"`
	ExpiresAt      *time.Time          `json:"expires_at,omitempty"`
	LastUsedAt     *time.Time          `json:"last_used_at,omitempty"`
	RevokedAt      *time.Time          `json:"revoked_at,omitempty"`
	CreatedBy      string              `json:"created_by,omitempty"`
	Bindings       []CredentialBinding `json:"bindings"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

// CredentialBindings extracts the typed binding list from a credential scope.
func (c *Credential) CredentialBindings() []CredentialBinding {
	scope := c.Scope
	if scope == nil {
		return nil
	}
	raw, _ := scope["bindings"].([]any)
	bindings := make([]CredentialBinding, 0, len(raw))
	for _, item := range raw {
		if entry, ok := item.(map[string]any); ok {
			targetType, _ := entry["target_type"].(string)
			targetName, _ := entry["target_name"].(string)
			if targetType != "" && targetName != "" {
				bindings = append(bindings, CredentialBinding{TargetType: targetType, TargetName: targetName})
			}
		}
	}
	return bindings
}

type ExecutionStatus string

const (
	ExecWaitingApproval ExecutionStatus = "WAITING_APPROVAL"
	ExecQueued          ExecutionStatus = "QUEUED"
	ExecDispatched      ExecutionStatus = "DISPATCHED"
	ExecRunning         ExecutionStatus = "RUNNING"
	ExecSucceeded       ExecutionStatus = "SUCCEEDED"
	ExecFailed          ExecutionStatus = "FAILED"
	ExecUnknown         ExecutionStatus = "UNKNOWN"
	ExecCancelling      ExecutionStatus = "CANCELLING"
	ExecCancelled       ExecutionStatus = "CANCELLED"
	ExecExpired         ExecutionStatus = "EXPIRED"
)

// AuditEvent is an append-only record of a significant action.
type AuditEvent struct {
	ID              string         `json:"id"`
	Action          string         `json:"action"`
	ActorID         string         `json:"actor_id,omitempty"`
	ActorType       string         `json:"actor_type,omitempty"`
	ActingProjectID string         `json:"acting_project_id,omitempty"`
	CredentialID    string         `json:"credential_id,omitempty"`
	ResourceID      string         `json:"resource_id,omitempty"`
	ExecutionID     string         `json:"execution_id,omitempty"`
	GrantID         string         `json:"grant_id,omitempty"`
	DecisionID      string         `json:"decision_id,omitempty"`
	ConnectorID     string         `json:"connector_id,omitempty"`
	TraceID         string         `json:"trace_id,omitempty"`
	TargetID        string         `json:"target_id,omitempty"`
	Summary         string         `json:"summary,omitempty"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAt       time.Time      `json:"created_at"`
}

// Project groups resources for the catalog view. Assignment is derived at
// query time: namespace rule > connector project > default project.
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	IsDefault   bool      `json:"is_default"`
	TenantID    string    `json:"tenant_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ProjectRole string

const (
	ProjectRoleAdmin    ProjectRole = "PROJECT_ADMIN"
	ProjectRoleOperator ProjectRole = "OPERATOR"
	ProjectRoleViewer   ProjectRole = "VIEWER"
)

type ProjectMembershipStatus string

const (
	ProjectMembershipActive   ProjectMembershipStatus = "ACTIVE"
	ProjectMembershipDisabled ProjectMembershipStatus = "DISABLED"
)

type ProjectMembership struct {
	TenantID      string                  `json:"tenant_id"`
	ProjectID     string                  `json:"project_id"`
	ProjectName   string                  `json:"project_name"`
	PrincipalID   string                  `json:"principal_id"`
	PrincipalName string                  `json:"principal_name"`
	// PrincipalDisplayName is the human-readable IdP label for federated
	// users. Empty for local identities, whose name is already readable.
	PrincipalDisplayName string                  `json:"principal_display_name,omitempty"`
	PrincipalKind        PrincipalKind           `json:"principal_kind"`
	Role          ProjectRole             `json:"role"`
	Status        ProjectMembershipStatus `json:"status"`
	CreatedBy     string                  `json:"created_by,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
}

// ProjectUser is a login-capable tenant user whose permissions are granted
// through one or more project memberships.
type ProjectUser struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	// DisplayName carries the IdP profile label for federated users; the
	// stable internal name stays in Name for audit and lookup purposes.
	DisplayName string              `json:"display_name,omitempty"`
	Status      PrincipalStatus     `json:"status"`
	TenantID    string              `json:"tenant_id"`
	LastLoginAt *time.Time          `json:"last_login_at,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
	Memberships []ProjectMembership `json:"memberships"`
}

// ProjectNamespaceRule overrides the project of one namespace inside one
// connector, so a shared cluster can split namespaces across projects.
type ProjectNamespaceRule struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	ConnectorID string    `json:"connector_id"`
	Namespace   string    `json:"namespace"`
	CreatedAt   time.Time `json:"created_at"`
}

// QueryTemplate is an immutable, versioned governed query template (§5). The
// canonical_ast is the Warden Query AST (JSON) fixed at publish time; bindings are
// the only runtime inputs an Agent may supply. Published templates are
// pre-approved and pre-costed, so database.query_template.execute needs no
// per-execution preview (the EXPLAIN gate still runs as a backstop).
type QueryTemplate struct {
	ID               string         `json:"id"`
	IntegrationID    string         `json:"integration_id"`
	SchemaResourceID string         `json:"schema_resource_id"`
	Name             string         `json:"name"`
	Version          int            `json:"version"`
	Status           string         `json:"status"`
	CanonicalAST     map[string]any `json:"canonical_ast"`
	ParameterSchema  map[string]any `json:"parameter_schema,omitempty"`
	ResourceBindings []string       `json:"resource_bindings,omitempty"`
	ResultSchema     map[string]any `json:"result_schema,omitempty"`
	MaxRows          int            `json:"max_rows"`
	MaxResultBytes   int            `json:"max_result_bytes"`
	TimeoutSeconds   int            `json:"timeout_seconds"`
	CostPolicy       map[string]any `json:"cost_policy,omitempty"`
	Sensitivity      string         `json:"sensitivity"`
	Owner            string         `json:"owner,omitempty"`
	CreatedBy        string         `json:"created_by,omitempty"`
	ApprovedBy       string         `json:"approved_by,omitempty"`
	PublishedAt      *time.Time     `json:"published_at,omitempty"`
	TemplateHash     string         `json:"template_hash"`
	TenantID         string         `json:"tenant_id"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// ColumnClassification maps one (schema, table, column) to an output policy
// (allow/mask/hash/deny) for governed query results (§13.2). The control plane
// owns classifications; the Connector applies the resulting policy per cell.
type ColumnClassification struct {
	ID             string    `json:"id"`
	IntegrationID  string    `json:"integration_id"`
	SchemaName     string    `json:"schema_name"`
	TableName      string    `json:"table_name"`
	ColumnName     string    `json:"column_name"`
	Classification string    `json:"classification"`
	OutputPolicy   string    `json:"output_policy"`
	TenantID       string    `json:"tenant_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TemplatePublishRequest is the audit trail of publishing a DRAFT template. A
// template only becomes executable after a publish request is APPROVED; the
// immutable canonical AST is the human review target.
type TemplatePublishRequest struct {
	ID             string     `json:"id"`
	TemplateID     string     `json:"template_id"`
	RequestedBy    string     `json:"requested_by"`
	Status         string     `json:"status"` // PENDING | APPROVED | REJECTED
	ApprovedBy     string     `json:"approved_by,omitempty"`
	RejectedReason string     `json:"rejected_reason,omitempty"`
	RequestedAt    time.Time  `json:"requested_at"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	TenantID       string     `json:"tenant_id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
