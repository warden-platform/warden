package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gitee.com/warden-platform/warden/server/internal/ai"
	"gitee.com/warden-platform/warden/server/internal/config"
	"gitee.com/warden-platform/warden/server/internal/domain"
	"gitee.com/warden-platform/warden/server/internal/policy"
	"gitee.com/warden-platform/warden/server/internal/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	pool                 *pgxpool.Pool
	decisionProvider     policy.Provider
	taskSigningKey       []byte
	jwtSigningKey        []byte
	credentialCipher     *credentialCipher
	adminAPIKey          string
	registrationAudience string
	connectorReplays     connectorReplayCache
	dingTalk             *dingTalkClient
	dingTalkCoolApp      *dingTalkCoolAppClient
	dingTalkCard         *dingTalkCardClient

	principals           PrincipalStore
	apiKeys              APIKeyStore
	resources            *postgres.ResourceRepo
	credentials          *postgres.CredentialRepo
	actions              *postgres.ActionRepo
	policyDecisions      *postgres.DecisionRepo
	executions           *postgres.ExecutionRepo
	grants               *postgres.GrantRepo
	audit                *postgres.AuditRepo
	requests             *postgres.RequestRepo
	connectors           *postgres.ConnectorRepo
	integrations         *postgres.IntegrationRepo
	approvalRules        *postgres.ApprovalRuleRepo
	policyRules          *postgres.PolicyMatrixRuleRepo
	providerCapabilities *postgres.ProviderCapabilityRepo
	projects             *postgres.ProjectRepo
	projectMemberships   *postgres.ProjectMembershipRepo
	projectUsers         *postgres.ProjectUserRepo
	externalIdentities   *postgres.ExternalIdentityRepo
	delegationTokens     *postgres.DelegationTokenRepo
	federated            *postgres.FederatedRepo
	federatedAssertion   *federatedAssertionVerifier
	oidc                 *oidcState
	queryTemplates       *postgres.QueryTemplateRepo
	notificationChannels NotificationChannelStore
	dingTalkRepo         *postgres.DingTalkRepo
	outbox               *postgres.OutboxRepo
	aiConfig             *ai.Config
	aiStore              AIStore
}

type AIStore interface {
	CreateConversation(context.Context, string, string) (*domain.AIConversation, error)
	GetConversation(context.Context, string, string) (*domain.AIConversation, error)
	CreateRun(context.Context, string) (*domain.AIRun, error)
	FinishRun(context.Context, string, string, string) error
	AddMessage(context.Context, string, string, string, string) (*domain.AIMessage, error)
	ListMessages(context.Context, string, string, int) ([]domain.AIMessage, error)
	AddToolCall(context.Context, *domain.AIToolCall) error
}

type NotificationChannelStore interface {
	Get(context.Context, string) (*domain.NotificationChannel, error)
	GetByConversation(ctx context.Context, channelType, conversationID string) (*domain.NotificationChannel, error)
	Upsert(context.Context, *domain.NotificationChannel) error
}

type APIKeyStore interface {
	Create(context.Context, *domain.PrincipalAPIKey) error
	GetForAuthentication(context.Context, string) (*domain.PrincipalAPIKey, *domain.Principal, error)
	ListByPrincipal(context.Context, string) ([]domain.PrincipalAPIKey, error)
	GetByID(context.Context, string) (*domain.PrincipalAPIKey, error)
	NamesByIDs(ctx context.Context, ids []string) (map[string]string, error)
	MarkUsed(context.Context, string, string) error
	Revoke(context.Context, string, string) (bool, error)
}

func NewHandler(pool *pgxpool.Pool) (*Handler, error) {
	if _, err := configuredCORSOrigin(); err != nil {
		return nil, err
	}
	registrationAudience, err := configuredRegistrationAudience()
	if err != nil {
		return nil, err
	}
	decisionProvider, err := policy.NewProvider()
	if err != nil {
		return nil, err
	}
	taskKey, err := config.RequiredSecret("WARDEN_TASK_SIGNING_KEY", 32)
	if err != nil {
		return nil, err
	}
	jwtKey, err := config.RequiredSecret("WARDEN_JWT_SIGNING_KEY", 32)
	if err != nil {
		return nil, err
	}
	cipherSecret, err := config.RequiredSecret("WARDEN_CREDENTIAL_ENCRYPTION_KEY", 32)
	if err != nil {
		return nil, err
	}
	adminAPIKey, err := config.Secret("WARDEN_ADMIN_API_KEY")
	if err != nil {
		return nil, err
	}
	if adminAPIKey == "" {
		// Compatibility migration: existing development installations may keep
		// using WARDEN_AGENT_API_KEY_FILE until their service definition is updated.
		adminAPIKey, err = config.RequiredSecret("WARDEN_AGENT_API_KEY", 32)
		if err != nil {
			return nil, fmt.Errorf("WARDEN_ADMIN_API_KEY is required (legacy WARDEN_AGENT_API_KEY is also accepted): %w", err)
		}
	} else if len(adminAPIKey) < 32 {
		return nil, fmt.Errorf("WARDEN_ADMIN_API_KEY must contain at least 32 bytes")
	}
	secretValues := []struct {
		name  string
		value string
	}{
		{"WARDEN_TASK_SIGNING_KEY", taskKey}, {"WARDEN_JWT_SIGNING_KEY", jwtKey},
		{"WARDEN_CREDENTIAL_ENCRYPTION_KEY", cipherSecret}, {"WARDEN_ADMIN_API_KEY", adminAPIKey},
	}
	for i := range secretValues {
		for j := i + 1; j < len(secretValues); j++ {
			if secretValues[i].value == secretValues[j].value {
				return nil, fmt.Errorf("%s and %s must use independent values", secretValues[i].name, secretValues[j].name)
			}
		}
	}
	credentialCipher, err := newCredentialCipher([]byte(cipherSecret))
	if err != nil {
		return nil, err
	}
	dingTalk, err := loadDingTalkClient()
	if err != nil {
		return nil, fmt.Errorf("invalid DingTalk notification configuration: %w", err)
	}
	dingTalkCoolApp, err := loadDingTalkCoolAppClient()
	if err != nil {
		return nil, fmt.Errorf("invalid DingTalk interactive approval configuration: %w", err)
	}
	dingTalkCard, err := loadDingTalkCardClient(dingTalkCoolApp)
	if err != nil {
		return nil, fmt.Errorf("invalid DingTalk card configuration: %w", err)
	}
	h := &Handler{
		pool:                 pool,
		decisionProvider:     decisionProvider,
		taskSigningKey:       []byte(taskKey),
		jwtSigningKey:        []byte(jwtKey),
		credentialCipher:     credentialCipher,
		adminAPIKey:          adminAPIKey,
		registrationAudience: registrationAudience,
		dingTalk:             dingTalk,
		dingTalkCoolApp:      dingTalkCoolApp,
		dingTalkCard:         dingTalkCard,
	}
	aiConfig, err := ai.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("invalid AI configuration: %w", err)
	}
	h.aiConfig = aiConfig
	if pool != nil {
		h.principals = postgres.NewPrincipalRepo(pool)
		h.apiKeys = postgres.NewAPIKeyRepo(pool)
		h.resources = postgres.NewResourceRepo(pool)
		h.credentials = postgres.NewCredentialRepo(pool)
		h.actions = postgres.NewActionRepo(pool)
		h.policyDecisions = postgres.NewDecisionRepo(pool)
		h.executions = postgres.NewExecutionRepo(pool)
		h.grants = postgres.NewGrantRepo(pool)
		h.audit = postgres.NewAuditRepo(pool)
		h.requests = postgres.NewRequestRepo(pool)
		h.connectors = postgres.NewConnectorRepo(pool)
		h.integrations = postgres.NewIntegrationRepo(pool)
		h.approvalRules = postgres.NewApprovalRuleRepo(pool)
		h.policyRules = postgres.NewPolicyMatrixRuleRepo(pool)
		h.providerCapabilities = postgres.NewProviderCapabilityRepo(pool)
		h.projects = postgres.NewProjectRepo(pool)
		h.projectMemberships = postgres.NewProjectMembershipRepo(pool)
		h.projectUsers = postgres.NewProjectUserRepo(pool)
		h.externalIdentities = postgres.NewExternalIdentityRepo(pool)
		h.delegationTokens = postgres.NewDelegationTokenRepo(pool)
		h.federated = postgres.NewFederatedRepo(pool)
		h.queryTemplates = postgres.NewQueryTemplateRepo(pool)
		h.notificationChannels = postgres.NewNotificationChannelRepo(pool)
		h.dingTalkRepo = postgres.NewDingTalkRepo(pool)
		h.outbox = postgres.NewOutboxRepo(pool)
		h.aiStore = postgres.NewAIRepo(pool)
		if err := h.EnsureAdminPassword(context.Background()); err != nil {
			return nil, fmt.Errorf("initialize admin password: %w", err)
		}
	}
	// The federated assertion verifier is optional so existing deployments keep
	// working until the federated integration public key is configured. The
	// federated endpoint returns a clear NOT_CONFIGURED response meanwhile.
	if verifier, err := loadFederatedAssertionVerifier(); err != nil {
		return nil, fmt.Errorf("invalid federated assertion verifier configuration: %w", err)
	} else {
		h.federatedAssertion = verifier
	}
	oidcConfig, err := loadOIDCConfig()
	if err != nil {
		return nil, fmt.Errorf("invalid OIDC configuration: %w", err)
	}
	if oidcConfig != nil {
		h.oidc = &oidcState{config: oidcConfig}
	}
	return h, nil
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Public orchestration probes (no API key auth).
	mux.HandleFunc("GET /api/v1/health", h.Readiness)
	mux.HandleFunc("GET /livez", h.Liveness)
	mux.HandleFunc("GET /readyz", h.Readiness)
	// Public auth route: login issues a JWT, no middleware required.
	mux.HandleFunc("POST /api/v1/auth/login", h.Login)
	mux.HandleFunc("POST /api/v1/auth/federated/login", h.FederatedLogin)
	mux.HandleFunc("GET /api/v1/auth/oidc/start", h.OIDCLoginStart)
	mux.HandleFunc("GET /api/v1/auth/oidc/callback", h.OIDCLoginCallback)
	mux.HandleFunc("GET /api/v1/auth/oidc/config", h.OIDCConfig)
	mux.HandleFunc("GET /api/v1/auth/oidc/logout", h.OIDCLogout)
	mux.HandleFunc("GET /api/v1/dingtalk/config", h.DingTalkConfig)
	mux.HandleFunc("POST /api/v1/dingtalk/auth", h.DingTalkAuth)
	mux.HandleFunc("GET /api/v1/dingtalk/access-requests", h.DingTalkListAccessRequests)
	mux.HandleFunc("POST /api/v1/dingtalk/access-requests/{id}/approve", h.DingTalkApproveRequest)
	mux.HandleFunc("POST /api/v1/dingtalk/access-requests/{id}/reject", h.DingTalkRejectRequest)
	mux.HandleFunc("POST /api/v1/dingtalk/logout", h.DingTalkLogout)
	mux.HandleFunc("GET /downloads/install-linux.sh", h.DownloadLinuxInstaller)
	mux.HandleFunc("GET /downloads/install-host.sh", h.DownloadHostInstaller)
	mux.HandleFunc("GET /downloads/uninstall-linux.sh", h.DownloadLinuxUninstaller)
	mux.HandleFunc("GET /downloads/install-network.sh", h.DownloadNetworkInstaller)
	mux.HandleFunc("GET /downloads/warden-linux-connector-linux/{arch}", h.DownloadLinuxConnector)
	mux.HandleFunc("GET /downloads/warden-linux-helper-linux/{arch}", h.DownloadLinuxHelper)
	mux.HandleFunc("GET /downloads/warden-network-connector-linux/{arch}", h.DownloadNetworkConnector)
	mux.HandleFunc("GET /downloads/warden-host-connector/{platform}/{arch}", h.DownloadHostConnector)
	mux.HandleFunc("GET /downloads/warden-operator-chart.tgz", h.DownloadOperatorChart)
	mux.HandleFunc("GET /downloads/SHA256SUMS", h.DownloadChecksums)
	mux.HandleFunc("POST /api/v1/operator/pairings", h.CreateOperatorPairing)
	mux.HandleFunc("GET /api/v1/operator/pairings/{id}", h.GetOperatorPairing)
	mux.HandleFunc("POST /api/v1/operator/pairings/{id}/ready", h.MarkOperatorPairingReady)
	mux.HandleFunc("POST /api/v1/operator/pairings/{id}/deleted", h.MarkOperatorPairingDeleted)
	mux.HandleFunc("POST /api/v1/linux/pairings", h.CreateLinuxPairing)
	mux.HandleFunc("POST /api/v1/host/pairings", h.CreateHostPairing)

	// Connector runtime routes use the per-Connector task key to authenticate
	// every request. The signature also covers timestamp, nonce, path and body.
	connectorAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return ConnectorAuthMiddleware(h.resolveConnectorRequestKey, &h.connectorReplays, next)
	}
	mux.HandleFunc("POST /api/v1/connectors/register", connectorAuth(h.RegisterConnector))
	mux.HandleFunc("POST /api/v1/connectors/{id}/heartbeat", connectorAuth(h.ConnectorHeartbeat))
	mux.HandleFunc("GET /api/v1/connectors/{id}/tasks", connectorAuth(h.PollTasks))
	mux.HandleFunc("POST /api/v1/connectors/{id}/tasks/{executionId}/result", connectorAuth(h.SubmitTaskResult))
	mux.HandleFunc("POST /api/v1/resources/sync", connectorAuth(h.SyncResources))
	// V2 advertises grouped, completeness-aware catalog synchronization. Keeping
	// it on a distinct path lets newer connectors detect older control planes
	// during rolling upgrades instead of having a V1 server silently decode a
	// grouped request as an empty connector-wide snapshot.
	mux.HandleFunc("POST /api/v2/resources/sync", connectorAuth(h.SyncResources))
	mux.HandleFunc("GET /api/v1/connector-runtime/{id}/integrations", connectorAuth(h.ListConnectorIntegrations))
	mux.HandleFunc("POST /api/v1/connector-runtime/{id}/integrations/{integrationID}/health", connectorAuth(h.ReportIntegrationHealth))
	mux.HandleFunc("POST /api/v1/connectors/credentials/resolve", connectorAuth(h.ResolveCredentialValue))

	protected := http.NewServeMux()
	protected.HandleFunc("POST /api/v1/executions", RequireScope("warden:executions:create", h.CreateExecution))
	protected.HandleFunc("GET /api/v1/executions/{id}", RequireScope("warden:executions:read:self", h.GetExecution))
	protected.HandleFunc("POST /api/v1/executions/{id}/cancel", RequireScope("warden:executions:cancel:self", h.CancelExecution))
	protected.HandleFunc("GET /api/v1/executions", RequireScope("warden:executions:read:self", h.ListExecutions))
	protected.HandleFunc("GET /api/v1/resources", RequireScope("warden:resources:read", h.ListResources))
	protected.HandleFunc("GET /api/v1/resources/stats", RequireScope("warden:resources:read", h.ResourceStats))
	protected.HandleFunc("GET /api/v1/actions", RequireScope("warden:resources:read", h.ListAvailableActions))
	protected.HandleFunc("GET /api/v1/resources/{id}/actions", RequireScope("warden:resources:read", h.ListResourceActions))
	protected.HandleFunc("GET /api/v1/resources/{id}/provider-capabilities", RequireScope("warden:resources:read", h.ListProviderCapabilities))
	protected.HandleFunc("POST /api/v1/diagnostics", RequireScope("warden:resources:read", h.CreateDiagnosticContext))
	protected.HandleFunc("GET /api/v1/diagnostics/{traceId}", RequireScope("warden:resources:read", h.GetDiagnosticResult))
	protected.HandleFunc("GET /api/v1/provider-capabilities", RequireAnyScope([]string{"warden:resources:read", "warden:admin"}, h.ListProviderCapabilityCatalog))
	protected.HandleFunc("POST /api/v1/provider-capabilities", RequireTenantAdmin(h.CreateProviderCapability))
	protected.HandleFunc("PUT /api/v1/provider-capabilities/{id}", RequireTenantAdmin(h.UpdateProviderCapability))
	protected.HandleFunc("DELETE /api/v1/provider-capabilities/{id}", RequireTenantAdmin(h.DeleteProviderCapability))
	protected.HandleFunc("GET /api/v1/access-requests", RequireScope("warden:approve", h.ListAccessRequests))
	protected.HandleFunc("GET /api/v1/my/access-requests", h.ListMyAccessRequests)
	protected.HandleFunc("POST /api/v1/access-requests/{id}/approve", RequireScope("warden:approve", h.ApproveRequest))
	protected.HandleFunc("POST /api/v1/access-requests/{id}/reject", RequireScope("warden:approve", h.RejectRequest))
	protected.HandleFunc("GET /api/v1/audit-events", RequireScope("warden:audit:read", h.ListAuditEvents))
	protected.HandleFunc("POST /api/v1/ai/chat", h.AIChat)
	protected.HandleFunc("GET /api/v1/accounts", RequireScope("warden:admin", RequirePlatformTenant(h.ListAccounts)))
	protected.HandleFunc("PUT /api/v1/accounts/{id}/role", RequireScope("warden:admin", RequirePlatformTenant(h.UpdateAccountRole)))
	protected.HandleFunc("POST /api/v1/accounts/{id}/remove", RequireScope("warden:admin", RequirePlatformTenant(h.RemoveAccountFromOrganization)))
	protected.HandleFunc("POST /api/v1/accounts/{id}/reenable", RequireScope("warden:admin", RequirePlatformTenant(h.ReenableAccount)))
	protected.HandleFunc("GET /api/v1/unaffiliated-identities", RequireScope("warden:admin", RequirePlatformTenant(h.ListUnaffiliatedIdentities)))
	protected.HandleFunc("POST /api/v1/unaffiliated-identities/{id}/assign", RequireScope("warden:admin", RequirePlatformTenant(h.AssignUnaffiliatedIdentity)))
	protected.HandleFunc("GET /api/v1/invitations", RequireTenantAdmin(h.ListInvitations))
	protected.HandleFunc("POST /api/v1/invitations", RequireTenantAdmin(h.CreateInvitation))
	protected.HandleFunc("DELETE /api/v1/invitations/{id}", RequireTenantAdmin(h.RevokeInvitation))
	protected.HandleFunc("GET /api/v1/my/invitations", h.MyInvitations)
	protected.HandleFunc("POST /api/v1/my/invitations/{id}/accept", h.AcceptInvitation)
	protected.HandleFunc("GET /api/v1/principals", RequireTenantAdmin(h.ListPrincipals))
	protected.HandleFunc("POST /api/v1/principals", RequireTenantAdmin(h.CreatePrincipal))
	protected.HandleFunc("DELETE /api/v1/principals/{id}", RequireTenantAdmin(h.DeletePrincipal))
	protected.HandleFunc("GET /api/v1/principals/{id}/api-keys", RequireTenantAdmin(h.ListAPIKeys))
	protected.HandleFunc("POST /api/v1/principals/{id}/api-keys", RequireTenantAdmin(h.CreateAPIKey))
	protected.HandleFunc("POST /api/v1/principals/{id}/registration-api-keys", RequireTenantAdmin(h.CreateRegistrationAPIKey))
	protected.HandleFunc("POST /api/v1/api-keys/{id}/revoke", RequireTenantAdmin(h.RevokeAPIKey))
	protected.HandleFunc("POST /api/v1/api-keys/{id}/rotate", RequireTenantAdmin(h.RotateAPIKey))
	protected.HandleFunc("GET /api/v1/external-bindings", RequireTenantAdmin(h.ListExternalBindings))
	protected.HandleFunc("POST /api/v1/external-bindings", RequireTenantAdmin(h.CreateExternalBinding))
	protected.HandleFunc("POST /api/v1/external-bindings/revoke-workspace", RequireTenantAdmin(h.RevokeExternalWorkspace))
	protected.HandleFunc("DELETE /api/v1/external-bindings/{id}", RequireTenantAdmin(h.RevokeExternalBinding))
	protected.HandleFunc("POST /api/v1/external-delegations/{id}/revoke", RequireTenantAdmin(h.RevokeDelegationToken))
	protected.HandleFunc("POST /api/v1/external-delegations/exchange", RequireScope("warden:delegation:exchange", h.ExchangeDelegationToken))
	protected.HandleFunc("GET /api/v1/federated/tenants", RequireTenantAdmin(h.ListFederatedTenants))
	protected.HandleFunc("POST /api/v1/federated/tenants", RequireTenantAdmin(h.BindFederatedTenant))
	protected.HandleFunc("GET /api/v1/federated/projects", RequireTenantAdmin(h.ListFederatedProjects))
	protected.HandleFunc("POST /api/v1/federated/projects", RequireTenantAdmin(h.BindFederatedProject))
	protected.HandleFunc("DELETE /api/v1/federated/projects/{id}", RequireTenantAdmin(h.RevokeFederatedProject))
	protected.HandleFunc("GET /api/v1/connectors", RequireTenantAdmin(h.ListConnectors))
	protected.HandleFunc("GET /api/v1/install-config", RequireTenantAdmin(h.GetInstallConfig))
	protected.HandleFunc("POST /api/v1/connectors/onboard", RequireTenantAdmin(h.OnboardCluster))
	protected.HandleFunc("POST /api/v1/connectors/network", RequireTenantAdmin(h.CreateNetworkConnector))
	protected.HandleFunc("GET /api/v1/connectors/{id}/integrations", RequireAnyScope([]string{"warden:registration:read"}, h.ListConnectorIntegrations))
	protected.HandleFunc("GET /api/v1/integrations", RequireAnyScope([]string{"warden:registration:read"}, h.ListIntegrations))
	protected.HandleFunc("POST /api/v1/cluster-pairings/confirm", RequireTenantAdmin(h.ConfirmClusterPairing))
	protected.HandleFunc("POST /api/v1/cluster-pairings/options", RequireTenantAdmin(h.GetClusterPairingOptions))
	protected.HandleFunc("POST /api/v1/linux-pairings/options", RequireTenantAdmin(h.GetLinuxPairingOptions))
	protected.HandleFunc("POST /api/v1/linux-pairings/confirm", RequireTenantAdmin(h.ConfirmLinuxPairing))
	protected.HandleFunc("POST /api/v1/host-pairings/options", RequireTenantAdmin(h.GetHostPairingOptions))
	protected.HandleFunc("POST /api/v1/host-pairings/confirm", RequireTenantAdmin(h.ConfirmHostPairing))
	protected.HandleFunc("POST /api/v1/host/pairings/options", RequireTenantAdmin(h.GetHostPairingOptions))
	protected.HandleFunc("POST /api/v1/host/pairings/confirm", RequireTenantAdmin(h.ConfirmHostPairing))
	protected.HandleFunc("DELETE /api/v1/connectors/{id}", RequireTenantAdmin(h.DeleteConnector))
	protected.HandleFunc("POST /api/v1/connectors/{id}/archive", RequireTenantAdmin(h.ArchiveConnector))
	protected.HandleFunc("POST /api/v1/connectors/{id}/force-archive", RequireTenantAdmin(h.ForceArchiveConnector))
	protected.HandleFunc("POST /api/v1/connectors/{id}/check", RequireTenantAdmin(h.CheckConnector))
	protected.HandleFunc("PUT /api/v1/connectors/{id}/namespaces", RequireTenantAdmin(h.UpdateConnectorNamespaces))
	protected.HandleFunc("PUT /api/v1/connectors/{id}/services", RequireTenantAdmin(h.UpdateConnectorServices))
	protected.HandleFunc("PUT /api/v1/connectors/{id}/project", RequireTenantAdmin(h.SetConnectorProject))
	protected.HandleFunc("PUT /api/v1/connectors/{id}/environment", RequireTenantAdmin(h.SetConnectorEnvironment))
	protected.HandleFunc("PUT /api/v1/resources/{id}/project", RequireTenantAdmin(h.SetResourceProject))
	protected.HandleFunc("DELETE /api/v1/resources/{id}/project", RequireTenantAdmin(h.DeleteResourceProject))
	protected.HandleFunc("GET /api/v1/resources/{id}/project-scope", RequireTenantAdmin(h.GetResourceProjectScope))
	protected.HandleFunc("PUT /api/v1/resources/{id}/project-scope", RequireTenantAdmin(h.UpdateResourceProjectScope))
	protected.HandleFunc("PUT /api/v1/resources/{id}/environment", RequireTenantAdmin(h.SetResourceEnvironment))
	protected.HandleFunc("DELETE /api/v1/resources/{id}/environment", RequireTenantAdmin(h.DeleteResourceEnvironment))
	protected.HandleFunc("GET /api/v1/projects", RequireAnyScope([]string{"warden:registration:read", "warden:resources:read"}, h.ListProjects))
	protected.HandleFunc("GET /api/v1/projects/{projectId}/members", RequireScope("warden:resources:read", h.ListProjectMembers))
	protected.HandleFunc("GET /api/v1/projects/{projectId}/member-candidates", RequireScope("warden:resources:read", h.ListProjectMemberCandidates))
	protected.HandleFunc("POST /api/v1/projects/{projectId}/users", RequireTenantAdmin(h.CreateProjectUser))
	protected.HandleFunc("POST /api/v1/projects/{projectId}/members", RequireScope("warden:resources:read", h.AddProjectMember))
	protected.HandleFunc("PUT /api/v1/projects/{projectId}/members/{principalId}", RequireScope("warden:resources:read", h.UpdateProjectMember))
	protected.HandleFunc("DELETE /api/v1/projects/{projectId}/members/{principalId}", RequireScope("warden:resources:read", h.RemoveProjectMember))
	protected.HandleFunc("GET /api/v1/me/projects", RequireScope("warden:resources:read", h.ListMyProjects))
	protected.HandleFunc("GET /api/v1/project-membership-policy", RequireScope("warden:resources:read", h.GetProjectMembershipPolicy))
	protected.HandleFunc("PUT /api/v1/project-membership-policy", RequireTenantAdmin(h.UpdateProjectMembershipPolicy))
	protected.HandleFunc("GET /api/v1/project-users", RequireTenantAdmin(h.ListProjectUsers))
	protected.HandleFunc("GET /api/v1/project-users/{principalId}", RequireTenantAdmin(h.GetProjectUser))
	protected.HandleFunc("PUT /api/v1/project-users/{principalId}/status", RequireTenantAdmin(h.UpdateProjectUserStatus))
	protected.HandleFunc("POST /api/v1/project-users/{principalId}/reset-password", RequireTenantAdmin(h.ResetProjectUserPassword))
	protected.HandleFunc("POST /api/v1/project-users/{principalId}/revoke-sessions", RequireTenantAdmin(h.RevokeProjectUserSessions))
	protected.HandleFunc("POST /api/v1/projects", RequireTenantAdmin(h.CreateProject))
	protected.HandleFunc("PUT /api/v1/projects/{id}", RequireTenantAdmin(h.UpdateProject))
	protected.HandleFunc("DELETE /api/v1/projects/{id}", RequireTenantAdmin(h.DeleteProject))
	protected.HandleFunc("GET /api/v1/project-namespace-rules", RequireTenantAdmin(h.ListProjectNamespaceRules))
	protected.HandleFunc("PUT /api/v1/project-namespace-rules", RequireTenantAdmin(h.UpsertProjectNamespaceRule))
	protected.HandleFunc("DELETE /api/v1/project-namespace-rules/{id}", RequireTenantAdmin(h.DeleteProjectNamespaceRule))
	protected.HandleFunc("POST /api/v1/connectors/{id}/integrations", RequireAnyScope([]string{"warden:registration:write"}, h.CreateIntegration))
	protected.HandleFunc("PUT /api/v1/connectors/{id}/integrations/{integrationId}", RequireAnyScope([]string{"warden:registration:write"}, h.UpdateIntegration))
	protected.HandleFunc("DELETE /api/v1/connectors/{id}/integrations/{integrationId}", RequireTenantAdmin(h.DeleteIntegration))
	protected.HandleFunc("GET /api/v1/integrations/{integrationId}/project-scope", RequireAnyScope([]string{"warden:registration:read"}, h.GetIntegrationProjectScope))
	protected.HandleFunc("PUT /api/v1/integrations/{integrationId}/project-scope", RequireAnyScope([]string{"warden:registration:write"}, h.UpdateIntegrationProjectScope))
	protected.HandleFunc("GET /api/v1/resources/{id}/consumer-projects", RequireTenantAdmin(h.GetResourceConsumerProjects))
	protected.HandleFunc("PUT /api/v1/resources/{id}/consumer-projects", RequireTenantAdmin(h.UpdateResourceConsumerProjects))
	protected.HandleFunc("GET /api/v1/auth/me", h.GetMe)
	protected.HandleFunc("PUT /api/v1/auth/password", h.ChangeOwnPassword)
	protected.HandleFunc("GET /api/v1/tenants", RequireScope("warden:admin", RequirePlatformTenant(h.ListTenants)))
	protected.HandleFunc("POST /api/v1/tenants", RequireScope("warden:admin", RequirePlatformTenant(h.CreateTenant)))
	protected.HandleFunc("POST /api/v1/tenants/bootstrap", RequireScope("warden:admin", RequirePlatformTenant(h.BootstrapTenant)))
	protected.HandleFunc("GET /api/v1/tenants/{slug}", RequireScope("warden:admin", RequirePlatformTenant(h.GetTenant)))
	protected.HandleFunc("GET /api/v1/tenants/{slug}/admins", RequireScope("warden:admin", RequirePlatformTenant(h.ListTenantAdmins)))
	protected.HandleFunc("POST /api/v1/tenants/{slug}/admins", RequireScope("warden:admin", RequirePlatformTenant(h.CreateTenantAdmin)))
	protected.HandleFunc("GET /api/v1/tenants/{slug}/members", RequireScope("warden:admin", RequirePlatformTenant(h.ListTenantMembers)))
	protected.HandleFunc("POST /api/v1/tenants/{slug}/members", RequireScope("warden:admin", RequirePlatformTenant(h.CreateTenantMember)))
	protected.HandleFunc("PUT /api/v1/tenants/{slug}/quota", RequireScope("warden:admin", RequirePlatformTenant(h.UpdateTenantQuota)))
	protected.HandleFunc("PUT /api/v1/tenants/{slug}/settings", RequireScope("warden:admin", RequirePlatformTenant(h.UpdateTenantSettings)))
	protected.HandleFunc("POST /api/v1/tenants/{slug}/disable", RequireScope("warden:admin", RequirePlatformTenant(h.DisableTenant)))
	protected.HandleFunc("POST /api/v1/auth/logout", h.Logout)
	protected.HandleFunc("GET /api/v1/approval-rules", RequireTenantAdmin(h.ListApprovalRules))
	protected.HandleFunc("PUT /api/v1/approval-rules/{id}", RequireTenantAdmin(h.UpdateApprovalRule))
	protected.HandleFunc("GET /api/v1/policy-rules", RequireTenantAdmin(h.ListPolicyRules))
	protected.HandleFunc("POST /api/v1/policy-rules", RequireTenantAdmin(h.CreatePolicyRule))
	protected.HandleFunc("PUT /api/v1/policy-rules/{id}", RequireTenantAdmin(h.UpdatePolicyRule))
	protected.HandleFunc("DELETE /api/v1/policy-rules/{id}", RequireTenantAdmin(h.DeletePolicyRule))
	protected.HandleFunc("POST /api/v1/policy-rules/{id}/copy", RequireTenantAdmin(h.CopyPolicyRule))
	protected.HandleFunc("POST /api/v1/policy-rules/simulate", RequireTenantAdmin(h.SimulatePolicyRule))
	protected.HandleFunc("GET /api/v1/notification-channels/dingtalk", RequireTenantAdmin(h.GetDingTalkChannel))
	protected.HandleFunc("PUT /api/v1/notification-channels/dingtalk", RequireTenantAdmin(h.UpdateDingTalkChannel))
	protected.HandleFunc("POST /api/v1/notification-channels/dingtalk/test", RequireTenantAdmin(h.TestDingTalkChannel))
	protected.HandleFunc("POST /api/v1/database/query-templates", RequireTenantAdmin(h.ImportQueryTemplate))
	protected.HandleFunc("GET /api/v1/database/query-templates", RequireTenantAdmin(h.ListQueryTemplates))
	protected.HandleFunc("GET /api/v1/database/query-templates/{id}", RequireTenantAdmin(h.GetQueryTemplate))
	protected.HandleFunc("POST /api/v1/database/query-templates/{id}/publish", RequireTenantAdmin(h.SubmitTemplatePublish))
	protected.HandleFunc("GET /api/v1/database/query-templates/publish-requests", RequireTenantAdmin(h.ListTemplatePublishRequests))
	protected.HandleFunc("POST /api/v1/database/query-templates/{id}/publish-requests/{requestId}/approve", RequireTenantAdmin(h.ApproveTemplatePublish))
	protected.HandleFunc("POST /api/v1/database/query-templates/{id}/publish-requests/{requestId}/reject", RequireTenantAdmin(h.RejectTemplatePublish))
	mux.Handle("/api/v1/", IdentityAuthMiddleware(h.authenticate, h.tenantOverride(h.authorizationAuditMiddleware(protected))))
}

// ValidateConnectorCredentials fails startup when the configured encryption
// key cannot decrypt existing Connector task keys. This prevents a partially
// healthy control plane from claiming tasks that it cannot sign.
func (h *Handler) ValidateConnectorCredentials(ctx context.Context) error {
	if h.pool == nil {
		return nil
	}
	rows, err := h.pool.Query(ctx, "SELECT id, name, task_key_ciphertext FROM connectors "+
		"WHERE archived_at IS NULL AND task_key_ciphertext IS NOT NULL")
	if err != nil {
		return fmt.Errorf("query Connector credentials: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		var encrypted []byte
		if err := rows.Scan(&id, &name, &encrypted); err != nil {
			return fmt.Errorf("scan Connector credential: %w", err)
		}
		decrypted, err := h.credentialCipher.Decrypt(encrypted)
		if err != nil || len(decrypted) < 32 {
			return fmt.Errorf("decrypt Connector credential %q (%s): configured encryption key does not match", name, id)
		}
	}
	return rows.Err()
}

// --- Health ---

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	h.Readiness(w, r)
}

func (h *Handler) Liveness(w http.ResponseWriter, _ *http.Request) {
	jsonResp(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		jsonError(w, http.StatusServiceUnavailable, "NOT_READY", "数据库未连接")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.pool.Ping(ctx); err != nil {
		jsonError(w, http.StatusServiceUnavailable, "NOT_READY", "数据库不可用")
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"status": "ready"})
}

// --- Create Execution (core flow) ---

type createExecutionRequest struct {
	TaskID          string                           `json:"taskId"`
	ExternalContext *domain.ExternalExecutionContext `json:"externalContext,omitempty"`
	ResourceID      string                           `json:"resourceId"`
	ActingProjectID string                           `json:"actingProjectId,omitempty"`
	Action          string                           `json:"action"`
	ActionVersion   int                              `json:"actionVersion"`
	Parameters      map[string]any                   `json:"parameters"`
	Reason          string                           `json:"reason"`
	TraceID         string                           `json:"traceId,omitempty"`
}

func (h *Handler) CreateExecution(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		jsonError(w, http.StatusServiceUnavailable, "NO_DATABASE", "数据库未连接，请先启动 PostgreSQL")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")

	var req createExecutionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}

	if req.ResourceID == "" || req.Action == "" {
		jsonError(w, http.StatusBadRequest, "MISSING_FIELDS", "resourceId 和 action 为必填")
		return
	}

	// A caller may pin a session trace id (validate first). The id is correlation
	// only and never affects authorization; it links executions into one trace.
	traceID := req.TraceID
	if !domain.ValidateTraceID(traceID) {
		traceID = domain.NewTraceID()
	}

	ctx := r.Context()
	identity := identityFromContext(ctx)
	if identity == nil || identity.Principal == nil {
		jsonError(w, http.StatusUnauthorized, "UNAUTHORIZED", "缺少认证身份")
		return
	}
	principal := identity.Principal
	externalContext, externalContextErr := normalizeExternalExecutionContext(req.ExternalContext)
	if externalContextErr != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_EXTERNAL_CONTEXT", externalContextErr.Error())
		return
	}
	if req.ExternalContext != nil && req.TaskID != "" && req.ExternalContext.TaskID != "" && req.TaskID != req.ExternalContext.TaskID {
		jsonError(w, http.StatusBadRequest, "EXTERNAL_CONTEXT_MISMATCH", "taskId 必须与 externalContext.taskId 一致")
		return
	}
	if req.TaskID == "" && req.ExternalContext != nil {
		req.TaskID = req.ExternalContext.TaskID
	}
	if len(identity.DelegationExternalContext) > 0 {
		if len(externalContext) > 0 && !externalContextsEqual(externalContext, identity.DelegationExternalContext) {
			jsonError(w, http.StatusBadRequest, "DELEGATION_CONTEXT_MISMATCH", "请求上下文必须与委托 Token 绑定上下文一致")
			return
		}
		externalContext = identity.DelegationExternalContext
		if taskID, _ := externalContext["taskId"].(string); taskID != "" {
			if req.TaskID != "" && req.TaskID != taskID {
				jsonError(w, http.StatusBadRequest, "DELEGATION_CONTEXT_MISMATCH", "taskId 必须与委托 Token 绑定任务一致")
				return
			}
			req.TaskID = taskID
		}
		if identity.DelegationProjectID != "" {
			if req.ActingProjectID != "" && req.ActingProjectID != identity.DelegationProjectID {
				jsonError(w, http.StatusForbidden, "DELEGATION_PROJECT_MISMATCH", "委托 Token 不能执行绑定 Warden project 之外的项目")
				return
			}
			req.ActingProjectID = identity.DelegationProjectID
		}
	}
	// Concurrency safety valve: protects the shared control plane from a
	// runaway agent flooding it with in-flight work. This is an operational
	// guard, not a commercial meter. Idempotent retries are checked later.
	var inFlight, maxInFlight int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM executions WHERE tenant_id=$1 AND status IN ('WAITING_APPROVAL','QUEUED','DISPATCHED','RUNNING')`, identity.TenantID).Scan(&inFlight); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "检查在途执行失败")
		return
	}
	if err := h.pool.QueryRow(ctx, `SELECT max_concurrent_tasks FROM tenant_quotas WHERE tenant_id=$1`, identity.TenantID).Scan(&maxInFlight); err == nil && inFlight >= maxInFlight {
		jsonError(w, http.StatusTooManyRequests, "CONCURRENCY_LIMIT_REACHED", "并发保护：在途执行已达上限，请等待现有执行完成，或联系平台管理员调整阈值")
		return
	}

	// 1. 加载资源
	res, err := h.resources.GetByID(ctx, req.ResourceID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询资源失败")
		return
	}
	if res == nil {
		jsonError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "资源不存在")
		return
	}
	if res.Status != domain.ResourceActive {
		jsonError(w, http.StatusConflict, "RESOURCE_NOT_ACTIVE", "资源未激活")
		return
	}
	projectScopeAccess, scopeErr := h.identityResourceProjectAccess(ctx, identity, res, req.ActingProjectID)
	if scopeErr != nil {
		log.Printf("authorize resource project scope failed: %v", scopeErr)
		jsonError(w, http.StatusInternalServerError, "PROJECT_SCOPE_ERROR", "校验资源项目权限失败")
		return
	}
	if projectScopeAccess == resourceProjectAccessNone {
		jsonError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "资源不存在")
		return
	}
	actingProjectID, actingProjectErr := h.resolveActingProject(ctx, res.ID, req.ActingProjectID)
	if actingProjectErr != nil {
		var accessErr *projectMembershipAccessError
		if errors.As(actingProjectErr, &accessErr) {
			jsonError(w, http.StatusForbidden, accessErr.Code, accessErr.Message)
		} else {
			jsonError(w, http.StatusForbidden, "RESOURCE_PROJECT_SCOPE_DENIED", actingProjectErr.Error())
		}
		return
	}

	// 2. 加载 Action
	act, err := h.actions.GetByName(ctx, req.Action)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询动作失败")
		return
	}
	if act == nil {
		jsonError(w, http.StatusNotFound, "ACTION_NOT_FOUND", "动作定义不存在")
		return
	}
	if req.ActionVersion != act.Version {
		jsonError(w, http.StatusBadRequest, "ACTION_VERSION_MISMATCH", "Action 版本不匹配")
		return
	}
	if projectScopeAccess == resourceProjectAccessConsumer && !consumerProjectCanRunAction(act.Name) {
		jsonError(w, http.StatusNotFound, "ACTION_NOT_FOUND", "动作定义不存在")
		return
	}
	// Execution location is explicit action metadata. It is independent from
	// synchronous/asynchronous behavior and does not require a fake Connector.
	controlPlane := act.ExecutorType == domain.ExecutorControlPlane
	if act.ResourceType != res.ResourceType && act.Name != "provider.request" && act.Name != "provider.write" && !(act.ResourceType == "kubernetes_resource" && genericKubernetesActionApplies(res.ResourceType, act.Name)) {
		jsonError(w, http.StatusBadRequest, "ACTION_RESOURCE_TYPE_MISMATCH", "Action 不支持该资源类型")
		return
	}
	if err := validateJenkinsResourceParameters(act.Name, req.Parameters, res); err != nil {
		jsonError(w, http.StatusBadRequest, "RESOURCE_PARAMETER_MISMATCH", err.Error())
		return
	}
	if act.Name == "provider.request" {
		prepared, providerErr := h.prepareProviderRequest(ctx, req.Parameters, res, traceID)
		if providerErr != nil {
			writeProviderError(w, providerErr)
			return
		}
		req.Parameters = prepared
	}
	if act.Name == "provider.write" {
		prepared, providerErr := h.prepareProviderWrite(ctx, req.Parameters, res, traceID)
		if providerErr != nil {
			writeProviderError(w, providerErr)
			return
		}
		req.Parameters = prepared
	}
	if act.Name != "provider.request" && act.Name != "provider.write" {
		if err := validateActionParameters(act.ParameterSchema, req.Parameters); err != nil {
			jsonError(w, http.StatusBadRequest, "INVALID_ACTION_PARAMETERS", domain.SanitizeErrorMessage(err.Error()))
			return
		}
	}
	// Plaintext boundary: the credential secret exists only in memory. It is
	// stripped from Parameters (persisted) before paramHash is computed, so
	// executions.parameters, access requests and audit metadata only ever carry
	// value_sha256 + valuePreview. PendingSecret holds the AES-GCM ciphertext
	// when an approval round-trip is required.
	secretValue, _ := domain.ExtractCredentialValue(act.Name, req.Parameters)
	if strings.HasPrefix(act.Name, "kubernetes.resource.") {
		if err := validateGenericKubernetesParameters(req.Parameters, res); err != nil {
			jsonError(w, http.StatusBadRequest, "INVALID_KUBERNETES_RESOURCE", domain.SanitizeErrorMessage(err.Error()))
			return
		}
	}
	// Create-type actions (e.g. jenkins.job.create, grafana.folder.create)
	// target a parent resource but their "name" parameter names the NEW
	// resource being created, so the generic name/namespace consistency
	// check must be skipped.
	skipResourceIdentityValidation := strings.HasSuffix(act.Name, ".create") ||
		(act.ResourceType == "kubernetes_resource" && res.ResourceType == "namespace" &&
			(act.Name == "kubernetes.resource.list" || (act.Name == "kubernetes.resource.preview" && fmt.Sprint(req.Parameters["operation"]) == "create")))
	if !skipResourceIdentityValidation {
		if err := validateResourceParameters(req.Parameters, res); err != nil {
			jsonError(w, http.StatusBadRequest, "RESOURCE_PARAMETER_MISMATCH", err.Error())
			return
		}
	}
	var connector *domain.Connector
	connectorType := "control_plane"
	if controlPlane {
		if res.ManagementType != domain.ManagementControlPlane || res.ConnectorID != "" {
			jsonError(w, http.StatusConflict, "INVALID_EXECUTION_TARGET", "控制面动作必须绑定控制面资源")
			return
		}
	} else {
		if res.ManagementType == domain.ManagementControlPlane || res.ConnectorID == "" {
			jsonError(w, http.StatusConflict, "INVALID_EXECUTION_TARGET", "Connector 动作必须绑定执行通道")
			return
		}
		connector, err = h.connectors.GetByID(ctx, res.ConnectorID)
		if err != nil || connector == nil || connector.Status != domain.ConnectorActive {
			jsonError(w, http.StatusConflict, "CONNECTOR_NOT_ACTIVE", "资源所属 Connector 未激活")
			return
		}
		connectorType = connector.ConnectorType
	}
	if !controlPlane {
		if connector.LastHeartbeat == nil || time.Since(*connector.LastHeartbeat) > 90*time.Second {
			jsonError(w, http.StatusConflict, "CONNECTOR_NOT_ACTIVE", "资源所属 Connector 心跳已超时")
			return
		}
		if res.IntegrationID != "" {
			integration, integrationErr := h.integrations.GetByID(ctx, res.IntegrationID)
			if integrationErr != nil || integration == nil || integration.Status != domain.IntegrationActive {
				jsonError(w, http.StatusConflict, "INTEGRATION_NOT_ACTIVE", "资源所属 Integration 未启用")
				return
			}
			if available, code, reason := h3cInterfaceActionScopeAvailability(act.Name, integration.Scope); !available {
				jsonError(w, http.StatusConflict, code, reason)
				return
			}
		}
		if !domain.SupportsAction(connector.Capabilities, act.Name, act.Version) {
			jsonError(w, http.StatusConflict, "CONNECTOR_UPGRADE_REQUIRED", fmt.Sprintf(
				"执行通道 %s（当前版本 %s）不支持该动作，需要升级至包含能力 %s 的版本",
				connector.Name, connector.Version, domain.RequiredCapability(act.Name, act.Version)))
			return
		}
		if domain.ActionSafetyContract(act.Name, act.RiskLevel).Change && !domain.SupportsChangeSafety(connector.Capabilities) {
			jsonError(w, http.StatusConflict, "CONNECTOR_UPGRADE_REQUIRED", fmt.Sprintf(
				"执行通道 %s（当前版本 %s）缺少变更安全能力 %s，请先升级后再执行变更",
				connector.Name, connector.Version, domain.ChangeSafetyCapability))
			return
		}
	}
	if act.Name == "huawei.ecs.create" {
		prepared, err := h.prepareHuaweiECSCreate(ctx, res, principal, req.Parameters)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "INVALID_CLOUD_PROJECT", err.Error())
			return
		}
		req.Parameters = prepared
	}
	if act.Name == "huawei.ecs.update_metadata" {
		prepared, err := h.prepareHuaweiECSMetadataUpdate(ctx, res, req.Parameters)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "INVALID_CLOUD_PROJECT", err.Error())
			return
		}
		req.Parameters = prepared
	}
	if act.Name == "database.query.preview" || act.Name == "database.query.execute" {
		if err := h.prepareDatabaseQueryScope(ctx, res, req.Parameters); err != nil {
			jsonError(w, http.StatusBadRequest, "INVALID_QUERY_SCOPE", domain.SanitizeErrorMessage(err.Error()))
			return
		}
	}

	// Template executions carry only templateId/templateVersion/bindings from the
	// Agent; resolve the immutable published template here (after parameter
	// validation, before hashing/dispatch) and inject the canonical AST plus the
	// derived masking map so the Connector never resolves a template from Agent
	// input and never applies Agent-chosen masking.
	if act.Name == "database.query_template.execute" {
		if err := h.resolveQueryTemplate(ctx, res, req.Parameters); err != nil {
			jsonError(w, http.StatusBadRequest, "QUERY_TEMPLATE_RESOLUTION_FAILED", domain.SanitizeErrorMessage(err.Error()))
			return
		}
	}
	var queryPreviewGate *databaseQueryPreviewGate

	// 3. 计算参数 Hash 和校验幂等键
	paramHash := domain.HashParameters(req.Parameters)
	if idempotencyKey != "" {
		existing, err := h.executions.GetByIdempotencyKey(ctx, principal.ID, idempotencyKey)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询幂等键失败")
			return
		}
		if existing != nil {
			if existing.ResourceID != res.ID || existing.ActionID != act.ID || existing.ActingProjectID != actingProjectID || existing.ParameterHash != paramHash || !externalContextsEqual(existing.ExternalContext, externalContext) {
				jsonError(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "相同幂等键已用于不同请求")
				return
			}
			jsonResp(w, http.StatusOK, executionToResp(existing))
			return
		}
	}
	if act.Name == "database.query.execute" {
		queryPreviewGate, err = h.loadDatabaseQueryPreviewGate(ctx, principal.ID, res.ID, actingProjectID, req.Parameters)
		if err != nil {
			jsonError(w, http.StatusConflict, "QUERY_PREVIEW_REQUIRED", domain.SanitizeErrorMessage(err.Error()))
			return
		}
	}
	// 4. 策略决策使用认证得到的 Principal，绝不信任请求体身份。
	decisionInput := policy.Input{
		Principal: policy.PrincipalInfo{
			ID:     principal.ID,
			Kind:   string(principal.Kind),
			Name:   principal.Name,
			Status: string(principal.Status),
		},
		Resource: policy.ResourceInfo{
			ID:          res.ID,
			Type:        res.ResourceType,
			Name:        res.Name,
			Namespace:   res.Namespace,
			Environment: res.Environment,
			OwnerTeam:   res.OwnerTeam,
		},
		Action:      act.Name,
		ProjectID:   actingProjectID,
		ActionRisk:  string(act.RiskLevel),
		Parameters:  req.Parameters,
		Environment: res.Environment,
		Connector: policy.ConnectorInfo{
			ID:   res.ConnectorID,
			Type: connectorType,
		},
	}
	decisionStarted := time.Now()
	decisionOutput, err := h.decisionProvider.Evaluate(ctx, decisionInput)

	decisionResult := domain.DecisionDeny
	if err != nil {
		log.Printf("策略决策失败 (将默认 DENY): %v", err)
		decisionOutput = &policy.Output{
			Decision: domain.DecisionDeny, ReasonCodes: []string{"DECISION_PROVIDER_ERROR"},
			PolicyBundleHash: "unavailable", MaxDurationSeconds: 60,
			CredentialMode: "KUBERNETES_TOKEN_REQUEST", ParameterConstraints: map[string]any{},
			Obligations: []string{"AUDIT_FULL"},
		}
	} else {
		decisionResult = decisionOutput.Decision
	}
	policyDecision := &domain.PolicyDecision{
		PrincipalID: principal.ID, ResourceID: res.ID, ActionID: act.ID,
		ActingProjectID: actingProjectID,
		OPAInput:        toMap(decisionInput), OPAOutput: toMap(decisionOutput),
		Result: decisionResult, BundleHash: decisionOutput.PolicyBundleHash,
		DecisionSeconds: int(time.Since(decisionStarted).Seconds()),
	}
	// 5b. Apply the visual policy matrix on top of Rego. Rego DENY is an
	// immutable lower bound; matrix rules may tighten ALLOW or decide whether a
	// REQUIRE_APPROVAL request can proceed automatically.
	if h.policyRules != nil {
		matrixRule, matrixErr := h.policyRules.Match(ctx, domain.PolicyMatchInput{
			ProjectID: actingProjectID, ResourceType: res.ResourceType, ActionName: act.Name,
			Environment: res.Environment, RiskLevel: string(act.RiskLevel), PrincipalKind: string(principal.Kind),
		})
		if matrixErr != nil {
			log.Printf("策略矩阵匹配失败 (将默认 DENY): matrix=%v", matrixErr)
			decisionResult = domain.DecisionDeny
			policyDecision.OPAOutput["matrix_error"] = true
		} else if matrixRule != nil {
			decisionResult = combinePolicyDecision(decisionResult, matrixRule)
			policyDecision.OPAOutput["matrix_rule_id"] = matrixRule.ID
			policyDecision.OPAOutput["matrix_rule_name"] = matrixRule.Name
			policyDecision.OPAOutput["matrix_decision"] = matrixRule.Decision
			_ = h.policyRules.RecordHit(ctx, matrixRule.ID)
		}
		policyDecision.Result = decisionResult
	}
	if exempted := enforceActionApprovalExemption(decisionResult, act.Name); exempted != decisionResult {
		decisionResult = exempted
		policyDecision.Result = exempted
		policyDecision.OPAOutput["action_approval_exemption"] = act.Name
	}
	if enforced := enforceProductionChangeApproval(decisionResult, res.Environment, act.Name, domain.ActionSafetyContract(act.Name, act.RiskLevel)); enforced != decisionResult {
		decisionResult = enforced
		policyDecision.Result = enforced
		policyDecision.OPAOutput["production_change_approval_floor"] = true
	}
	if queryPreviewGate != nil {
		policyDecision.OPAOutput["database_query_preview_execution_id"] = queryPreviewGate.ExecutionID
		policyDecision.OPAOutput["database_query_preview_hash"] = queryPreviewGate.PreviewHash
		policyDecision.OPAOutput["database_query_cost_verdict"] = queryPreviewGate.Verdict
		policyDecision.OPAOutput["database_query_cost_assessment"] = queryPreviewGate.Assessment
		if enforced := enforceDatabaseQueryCostFloor(decisionResult, queryPreviewGate.Verdict); enforced != decisionResult {
			decisionResult = enforced
			policyDecision.Result = enforced
			policyDecision.OPAOutput["database_query_cost_floor"] = true
		}
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "开始执行事务失败")
		return
	}
	defer tx.Rollback(ctx)
	if err := h.policyDecisions.CreateTx(ctx, tx, policyDecision); err != nil {
		log.Printf("持久化策略决策失败: %v", err)
		jsonError(w, http.StatusInternalServerError, "DECISION_AUDIT_FAILED", "策略决策无法持久化")
		return
	}

	// 6. 创建 Execution 记录
	exec := &domain.Execution{
		PrincipalID:            principal.ID,
		ActingProjectID:        actingProjectID,
		ExternalContext:        externalContext,
		DelegationTokenID:      identity.DelegationTokenID,
		OnBehalfOfPrincipalID:  identity.OnBehalfOfPrincipalID,
		AccountablePrincipalID: identity.AccountablePrincipalID,
		CredentialID:           identity.CredentialID,
		ResourceID:             res.ID,
		ActionID:               act.ID,
		Parameters:             req.Parameters,
		ParameterHash:          paramHash,
		IdempotencyKey:         idempotencyKey,
		TaskID:                 req.TaskID,
		TraceID:                traceID,
		Reason:                 req.Reason,
		TimeoutSeconds:         act.TimeoutSeconds,
		ConnectorID:            res.ConnectorID,
		DecisionID:             policyDecision.ID,
		ExecutorType:           act.ExecutorType,
	}
	executionEvent := func(action string) *domain.AuditEvent {
		event := executionAuditEvent(action, identity, principal, res, exec.ID, traceID)
		event.ActingProjectID = actingProjectID
		attachExecutionAudit(event, exec)
		return event
	}

	switch decisionResult {
	case domain.DecisionDeny:
		exec.Status = domain.ExecFailed
		exec.ErrorCode = "POLICY_DENIED"
		exec.ErrorMessage = "策略拒绝：无匹配的允许规则"
		if queryPreviewGate != nil && queryPreviewGate.Verdict == databaseCostReject {
			exec.ErrorCode = "DATABASE_COST_LIMIT_EXCEEDED"
			exec.ErrorMessage = "查询预检超过硬性成本限制，已拒绝执行"
		}
		if policyDecision.OPAOutput["matrix_decision"] == domain.DecisionDeny {
			exec.ErrorMessage = fmt.Sprintf("策略规则「%v」明确拒绝该操作", policyDecision.OPAOutput["matrix_rule_name"])
		}
		if err := h.executions.CreateTx(ctx, tx, exec); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存拒绝结果失败")
			return
		}
		if err := h.policyDecisions.LinkExecutionTx(ctx, tx, policyDecision.ID, exec.ID); err != nil ||
			h.audit.CreateTx(ctx, tx, executionEvent("execution.failed")) != nil || tx.Commit(ctx) != nil {
			jsonError(w, http.StatusInternalServerError, "EXECUTION_TRANSACTION_FAILED", "保存拒绝结果失败")
			return
		}
		jsonResp(w, http.StatusForbidden, executionToResp(exec))
		return

	case domain.DecisionRequireApproval:
		exec.Status = domain.ExecWaitingApproval
		// Hold the AES-GCM ciphertext on the execution while awaiting
		// approval so the plaintext never touches executions.parameters.
		if controlPlane && secretValue != "" {
			pending, encryptErr := h.credentialCipher.Encrypt([]byte(secretValue))
			if encryptErr != nil {
				jsonError(w, http.StatusInternalServerError, "CREDENTIAL_ENCRYPT_FAILED", "凭据加密失败")
				return
			}
			exec.PendingSecret = pending
		}
		if err := h.executions.CreateTx(ctx, tx, exec); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存审批执行失败")
			return
		}
		if err := h.policyDecisions.LinkExecutionTx(ctx, tx, policyDecision.ID, exec.ID); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "关联策略决策失败")
			return
		}
		// 创建审批请求
		reqRecord := &domain.AccessRequest{
			ExecutionID:            exec.ID,
			PrincipalID:            principal.ID,
			ResourceID:             res.ID,
			ActionID:               act.ID,
			ActingProjectID:        actingProjectID,
			ExternalContext:        externalContext,
			DelegationTokenID:      identity.DelegationTokenID,
			OnBehalfOfPrincipalID:  identity.OnBehalfOfPrincipalID,
			AccountablePrincipalID: identity.AccountablePrincipalID,
			Parameters:             req.Parameters,
			ParameterHash:          paramHash,
			Reason:                 req.Reason,
			Status:                 domain.RequestPending,
			DecisionID:             policyDecision.ID,
			ExpiresAt:              timePtr(time.Now().Add(30 * time.Minute)),
		}
		if err := h.requests.CreateTx(ctx, tx, reqRecord); err != nil {
			jsonError(w, http.StatusInternalServerError, "ACCESS_REQUEST_CREATE_FAILED", "审批请求创建失败")
			return
		}
		requestAudit := executionEvent("access_request.created")
		requestAudit.TargetID = reqRecord.ID
		requestAudit.DecisionID = policyDecision.ID
		requestAudit.Metadata["access_request_id"] = reqRecord.ID
		if err := h.audit.CreateTx(ctx, tx, requestAudit); err != nil {
			jsonError(w, http.StatusInternalServerError, "AUDIT_WRITE_FAILED", "审计写入失败")
			return
		}
		if err := h.outbox.PublishTx(ctx, tx, "access_request.submitted.v1", "execution", exec.ID, map[string]any{
			"execution_id": exec.ID,
			"status":       exec.Status,
			"trace_id":     traceID,
		}); err != nil {
			jsonError(w, http.StatusInternalServerError, "OUTBOX_WRITE_FAILED", "事件写入失败")
			return
		}
		if err := tx.Commit(ctx); err != nil {
			jsonError(w, http.StatusInternalServerError, "EXECUTION_TRANSACTION_FAILED", "提交审批执行失败")
			return
		}
		h.notifyApprovalAsync(ctx, *reqRecord, "access_request.created", "")
		jsonResp(w, http.StatusAccepted, executionToResp(exec))
		return

	case domain.DecisionAllow:
		exec.Status = domain.ExecQueued
		if err := h.executions.CreateTx(ctx, tx, exec); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存执行失败")
			return
		}
		if err := h.policyDecisions.LinkExecutionTx(ctx, tx, policyDecision.ID, exec.ID); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "关联策略决策失败")
			return
		}

		// Control-plane credential actions execute inline: no Grant is created
		// and the effect is applied directly against the Warden-managed ledger.
		if controlPlane {
			if err := h.audit.CreateTx(ctx, tx, executionEvent("execution.queued")); err != nil {
				jsonError(w, http.StatusInternalServerError, "AUDIT_WRITE_FAILED", "审计写入失败")
				return
			}
			result, execErr := h.executeCredentialAction(ctx, tx, exec, act, res, secretValue)
			auditAction, auditSummary := "execution.completed", "execution.completed"
			var errorCode, errorMessage string
			if execErr != nil {
				exec.Status = domain.ExecFailed
				exec.ErrorCode = "CREDENTIAL_ACTION_FAILED"
				exec.ErrorMessage = domain.SanitizeErrorMessage(execErr.Error())
				if errors.Is(execErr, context.DeadlineExceeded) {
					exec.Status = domain.ExecUnknown
					exec.ErrorCode = "ACTION_TIMEOUT"
					exec.ErrorMessage = "凭据操作超时，结果未知；禁止自动重试"
				}
				auditAction, auditSummary = "execution.failed", "execution.failed"
				errorCode, errorMessage = exec.ErrorCode, exec.ErrorMessage
			} else {
				exec.Status = domain.ExecSucceeded
				exec.Result = result
			}
			changed, completeErr := h.executions.CompleteControlPlaneTx(ctx, tx, exec.ID, domain.ExecQueued, exec.Status, exec.Result, errorCode, errorMessage)
			if completeErr != nil {
				jsonError(w, http.StatusInternalServerError, "EXECUTION_TRANSACTION_FAILED", "保存凭据执行结果失败")
				return
			}
			if !changed {
				jsonError(w, http.StatusConflict, "EXECUTION_STATE_CHANGED", "执行状态已被其他请求更新")
				return
			}
			auditMeta := executionResultAuditMetadata(exec.Result)
			auditMeta["outcome"] = exec.Status
			auditMeta["action"] = act.Name
			event := executionEvent(auditAction)
			for key, value := range auditMeta {
				event.Metadata[key] = value
			}
			event.Summary = auditSummary
			if err := h.audit.CreateTx(ctx, tx, event); err != nil {
				jsonError(w, http.StatusInternalServerError, "AUDIT_WRITE_FAILED", "审计写入失败")
				return
			}
			if err := h.outbox.PublishTx(ctx, tx, "execution.completed.v1", "execution", exec.ID, map[string]any{
				"execution_id": exec.ID,
				"status":       exec.Status,
				"trace_id":     traceID,
			}); err != nil {
				jsonError(w, http.StatusInternalServerError, "OUTBOX_WRITE_FAILED", "事件写入失败")
				return
			}
			if err := tx.Commit(ctx); err != nil {
				jsonError(w, http.StatusInternalServerError, "EXECUTION_TRANSACTION_FAILED", "提交执行失败")
				return
			}
			jsonResp(w, http.StatusOK, executionToResp(exec))
			return
		}

		// 创建 Grant
		grant := &domain.AccessGrant{
			PrincipalID:     principal.ID,
			ActingProjectID: actingProjectID,
			ResourceScope:   map[string]any{"resource_id": res.ID, "type": res.ResourceType, "acting_project_id": actingProjectID},
			ActionScope:     map[string]any{"action": act.Name, "version": act.Version},
			ParameterHash:   paramHash,
			ScopeHash:       domain.HashScope(principal.ID, res.ID, act.ID, paramHash, actingProjectID),
			MaxUses:         1,
			TTLSeconds:      act.TimeoutSeconds,
			ExpiresAt:       time.Now().Add(time.Duration(act.TimeoutSeconds) * time.Second),
			Status:          domain.GrantActive,
		}
		if err := h.grants.CreateTx(ctx, tx, grant); err != nil {
			jsonError(w, http.StatusInternalServerError, "GRANT_CREATE_FAILED", "授权创建失败")
			return
		}

		exec.GrantID = grant.ID
		exec.ExpiresAt = &grant.ExpiresAt
		if err := h.executions.AttachGrantTx(ctx, tx, exec.ID, grant.ID, grant.ExpiresAt); err != nil {
			jsonError(w, http.StatusInternalServerError, "GRANT_ATTACH_FAILED", "授权关联失败")
			return
		}

		if err := h.audit.CreateTx(ctx, tx, executionEvent("grant.issued")); err != nil {
			jsonError(w, http.StatusInternalServerError, "AUDIT_WRITE_FAILED", "审计写入失败")
			return
		}
		if err := h.audit.CreateTx(ctx, tx, executionEvent("execution.queued")); err != nil {
			jsonError(w, http.StatusInternalServerError, "AUDIT_WRITE_FAILED", "审计写入失败")
			return
		}
		if err := h.outbox.PublishTx(ctx, tx, "execution.queued.v1", "execution", exec.ID, map[string]any{
			"execution_id": exec.ID,
			"grant_id":     grant.ID,
			"status":       exec.Status,
			"trace_id":     traceID,
		}); err != nil {
			jsonError(w, http.StatusInternalServerError, "OUTBOX_WRITE_FAILED", "事件写入失败")
			return
		}
		if err := tx.Commit(ctx); err != nil {
			jsonError(w, http.StatusInternalServerError, "EXECUTION_TRANSACTION_FAILED", "提交执行失败")
			return
		}
		jsonResp(w, http.StatusAccepted, executionToResp(exec))
		return
	}
}

func (h *Handler) resolveActingProject(ctx context.Context, resourceID, requestedProjectID string) (string, error) {
	projectID, err := h.resolveResourceActingProject(ctx, resourceID, requestedProjectID)
	if err != nil {
		return "", err
	}
	if err := h.projectAccess(ctx, projectID, true); err != nil {
		return "", err
	}
	return projectID, nil
}

func (h *Handler) resolveResourceActingProject(ctx context.Context, resourceID, requestedProjectID string) (string, error) {
	scope, err := h.projects.GetResourceScope(ctx, resourceID)
	if err != nil {
		return "", errors.New("读取资源项目范围失败")
	}
	if scope == nil {
		return "", errors.New("资源不存在")
	}
	if scope.Mode == domain.ProjectScopeUnassigned || len(scope.ProjectIDs) == 0 {
		return "", errors.New("资源尚未开放给任何项目")
	}
	requestedProjectID = strings.TrimSpace(requestedProjectID)
	if requestedProjectID != "" {
		for _, projectID := range scope.ProjectIDs {
			if projectID == requestedProjectID {
				return requestedProjectID, nil
			}
		}
		return "", errors.New("资源未开放给指定项目")
	}
	// Compatibility for existing API clients: inherited resources keep using
	// their effective primary project until callers start sending actingProjectId.
	if scope.Mode == domain.ProjectScopeInherited {
		if inherited, inheritedErr := h.resources.EffectiveProjectID(ctx, resourceID); inheritedErr == nil && inherited != "" {
			for _, projectID := range scope.ProjectIDs {
				if projectID == inherited {
					return inherited, nil
				}
			}
		}
	}
	if len(scope.ProjectIDs) == 1 {
		return scope.ProjectIDs[0], nil
	}
	return "", errors.New("资源对多个项目开放，请指定 actingProjectId")
}

func (h *Handler) prepareProviderRequest(ctx context.Context, parameters map[string]any, resource *domain.Resource, traceID string) (map[string]any, error) {
	return h.prepareProviderRequestWith(ctx, parameters, resource, false, traceID)
}

// prepareProviderWrite is the provider.write counterpart to
// prepareProviderRequest. It runs the same signed-binding normalization but
// validates under the write contract (ValidateProviderWrite), which requires
// the resolved capability to declare write=true and enforces the POST/PUT/
// PATCH + body-schema rules. Writes never run against native Kubernetes.
func (h *Handler) prepareProviderWrite(ctx context.Context, parameters map[string]any, resource *domain.Resource, traceID string) (map[string]any, error) {
	if resource.IntegrationID == "" {
		return nil, &domain.ProviderRequestError{Code: domain.ProviderErrIntegrationMismatch, Message: "provider.write requires an Integration resource"}
	}
	return h.prepareProviderRequestWith(ctx, parameters, resource, true, traceID)
}

func (h *Handler) prepareProviderRequestWith(ctx context.Context, parameters map[string]any, resource *domain.Resource, write bool, traceID string) (map[string]any, error) {
	if len(parameters) != 1 {
		return nil, &domain.ProviderRequestError{Code: domain.ProviderErrInvalidRequest, Message: "provider request accepts only providerRequest parameters"}
	}
	raw, ok := parameters["providerRequest"]
	if !ok {
		return nil, &domain.ProviderRequestError{Code: domain.ProviderErrInvalidRequest, Message: "providerRequest is required"}
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, &domain.ProviderRequestError{Code: domain.ProviderErrInvalidRequest, Message: "providerRequest is invalid"}
	}
	var request domain.ProviderRequest
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, &domain.ProviderRequestError{Code: domain.ProviderErrInvalidRequest, Message: "providerRequest contains invalid fields"}
	}
	// The control plane pins the session trace id into the signed binding so an
	// agent cannot forge a different trace across the provider hop. The value is
	// validated by the same correlation rules immediately below.
	if request.Correlation == nil {
		request.Correlation = map[string]string{}
	}
	request.Correlation["trace_id"] = traceID
	if resource.IntegrationID == "" {
		connector, err := h.connectors.GetByID(ctx, resource.ConnectorID)
		if err != nil || connector == nil || connector.ConnectorType != "kubernetes" {
			return nil, &domain.ProviderRequestError{Code: domain.ProviderErrIntegrationMismatch, Message: "resource has no Integration"}
		}
		integration := domain.Integration{ConnectorID: resource.ConnectorID, Protocol: "kubernetes", Endpoint: "https://kubernetes.default.svc", TenantID: resource.TenantID, Status: domain.IntegrationActive}
		capability, ok, err := h.findProviderCapability(ctx, request.Capability, resource.ResourceType, integration.Protocol)
		if err != nil {
			return nil, &domain.ProviderRequestError{Code: domain.ProviderErrCapabilityNotFound, Message: "capability catalog is unavailable"}
		}
		if !ok {
			return nil, &domain.ProviderRequestError{Code: domain.ProviderErrCapabilityNotFound, Message: "capability is not registered for this resource"}
		}
		binding := domain.ProviderRequestBinding{Resource: *resource, Integration: integration, Capability: capability}
		if err := h.validateProviderBoundRequest(request, binding, write); err != nil {
			return nil, err
		}
		return normalizeProviderParameters(request, binding)
	}
	integration, err := h.integrations.GetByID(ctx, resource.IntegrationID)
	if err != nil || integration == nil {
		return nil, &domain.ProviderRequestError{Code: domain.ProviderErrIntegrationMismatch, Message: "resource Integration was not found"}
	}
	capability, ok, err := h.findProviderCapability(ctx, request.Capability, resource.ResourceType, integration.Protocol)
	if err != nil {
		return nil, &domain.ProviderRequestError{Code: domain.ProviderErrCapabilityNotFound, Message: "capability catalog is unavailable"}
	}
	if !ok {
		return nil, &domain.ProviderRequestError{Code: domain.ProviderErrCapabilityNotFound, Message: "capability is not registered for this resource"}
	}
	binding := domain.ProviderRequestBinding{Resource: *resource, Integration: *integration, Capability: capability}
	if err := h.validateProviderBoundRequest(request, binding, write); err != nil {
		return nil, err
	}
	// Persist only the signed Connector binding. Agent-supplied siblings are
	// discarded, so endpoint/credential/capability substitution cannot survive.
	return normalizeProviderParameters(request, binding)
}

func (h *Handler) validateProviderBoundRequest(request domain.ProviderRequest, binding domain.ProviderRequestBinding, write bool) error {
	if write {
		return domain.ValidateProviderWrite(request, binding)
	}
	return domain.ValidateProviderRequest(request, binding)
}

func normalizeProviderParameters(request domain.ProviderRequest, binding domain.ProviderRequestBinding) (map[string]any, error) {
	payload, err := json.Marshal(map[string]any{"providerRequest": request, "providerBinding": binding})
	if err != nil {
		return nil, &domain.ProviderRequestError{Code: domain.ProviderErrInvalidRequest, Message: "provider request could not be normalized"}
	}
	var normalized map[string]any
	if err := json.Unmarshal(payload, &normalized); err != nil {
		return nil, &domain.ProviderRequestError{Code: domain.ProviderErrInvalidRequest, Message: "provider request could not be normalized"}
	}
	return normalized, nil
}

func (h *Handler) ListProviderCapabilities(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	resource, err := h.resources.GetByID(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询资源失败")
		return
	}
	if resource == nil || resource.Status == domain.ResourceDisabled || resource.Status == domain.ResourceOrphaned {
		jsonError(w, http.StatusNotFound, domain.ProviderErrResourceNotFound, "资源不存在")
		return
	}
	if accessErr := h.ensureResourceProjectAccess(r.Context(), resource, false); accessErr != nil {
		h.writeProjectMembershipError(w, accessErr)
		return
	}
	resourceAllowed, scopeErr := h.identityCanAccessResource(r.Context(), identityFromContext(r.Context()), resource, "")
	if scopeErr != nil {
		jsonError(w, http.StatusInternalServerError, "PROJECT_SCOPE_ERROR", "校验资源项目权限失败")
		return
	}
	if !resourceAllowed {
		jsonError(w, http.StatusNotFound, domain.ProviderErrResourceNotFound, "资源不存在")
		return
	}
	if resource.IntegrationID == "" {
		connector, err := h.connectors.GetByID(r.Context(), resource.ConnectorID)
		if err == nil && connector != nil && connector.ConnectorType == "kubernetes" {
			capabilities, err := h.providerCapabilitiesFor(r.Context(), resource.ResourceType, "kubernetes")
			if err != nil {
				jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询能力目录失败")
				return
			}
			jsonResp(w, http.StatusOK, capabilities)
			return
		}
		jsonResp(w, http.StatusOK, []domain.ResourceCapability{})
		return
	}
	integration, err := h.integrations.GetByID(r.Context(), resource.IntegrationID)
	if err != nil || integration == nil || integration.Status != domain.IntegrationActive {
		jsonResp(w, http.StatusOK, []domain.ResourceCapability{})
		return
	}
	capabilities, err := h.providerCapabilitiesFor(r.Context(), resource.ResourceType, integration.Protocol)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询能力目录失败")
		return
	}
	jsonResp(w, http.StatusOK, capabilities)
}

func writeProviderError(w http.ResponseWriter, err error) {
	var providerErr *domain.ProviderRequestError
	if errors.As(err, &providerErr) {
		jsonError(w, providerErr.HTTPStatus(), providerErr.Code, providerErr.Message)
		return
	}
	jsonError(w, http.StatusBadRequest, domain.ProviderErrInvalidRequest, "provider request validation failed")
}

// loadProviderCapabilities returns the active capability catalog. When no
// database is configured (unit tests) it falls back to the built-in catalog
// seeded by migration 065.
func (h *Handler) loadProviderCapabilities(ctx context.Context) ([]domain.ResourceCapability, error) {
	if h.providerCapabilities == nil {
		return append([]domain.ResourceCapability(nil), domain.BuiltinProviderCapabilities...), nil
	}
	return h.providerCapabilities.List(ctx)
}

func (h *Handler) findProviderCapability(ctx context.Context, key, resourceKind, protocol string) (domain.ResourceCapability, bool, error) {
	capabilities, err := h.loadProviderCapabilities(ctx)
	if err != nil {
		return domain.ResourceCapability{}, false, err
	}
	capability, ok := domain.FindProviderCapabilityIn(capabilities, key, resourceKind, protocol)
	return capability, ok, nil
}

func (h *Handler) providerCapabilitiesFor(ctx context.Context, resourceKind, protocol string) ([]domain.ResourceCapability, error) {
	capabilities, err := h.loadProviderCapabilities(ctx)
	if err != nil {
		return nil, err
	}
	return domain.ProviderCapabilitiesForKinds(capabilities, resourceKind, protocol), nil
}

// ListProviderCapabilityCatalog returns the global configurable capability
// catalog (administrator CRUD view).
func (h *Handler) ListProviderCapabilityCatalog(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	capabilities, err := h.providerCapabilities.List(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询能力目录失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{"capabilities": capabilities})
}

func (h *Handler) decodeProviderCapabilityBody(w http.ResponseWriter, r *http.Request) (*domain.ResourceCapability, bool) {
	var capability domain.ResourceCapability
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&capability); err != nil {
		jsonError(w, http.StatusBadRequest, domain.ProviderErrInvalidRequest, "能力目录请求格式无效")
		return nil, false
	}
	// Write capabilities are validated under the write contract (POST/PUT/
	// PATCH only, never Kubernetes, body schema compiles). Read capabilities
	// keep the immutable GET/HEAD contract.
	validate := domain.ValidateProviderCapability
	if capability.Write {
		validate = domain.ValidateProviderWriteCapability
	}
	if err := validate(capability); err != nil {
		var providerErr *domain.ProviderRequestError
		if errors.As(err, &providerErr) {
			jsonError(w, http.StatusBadRequest, providerErr.Code, providerErr.Message)
		} else {
			jsonError(w, http.StatusBadRequest, domain.ProviderErrInvalidRequest, "能力目录条目校验失败")
		}
		return nil, false
	}
	return &capability, true
}

// CreateProviderCapability registers a new read capability. Every entry must
// pass ValidateProviderCapability; a duplicate (name, version) is rejected.
func (h *Handler) CreateProviderCapability(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	capability, ok := h.decodeProviderCapabilityBody(w, r)
	if !ok {
		return
	}
	actor := identityFromContext(r.Context())
	id, err := h.providerCapabilities.Create(r.Context(), capability, actor.Principal.ID)
	if err != nil {
		jsonError(w, http.StatusConflict, "PROVIDER_CAPABILITY_CONFLICT", "能力名称与版本已存在")
		return
	}
	capability.ID = id
	h.writeNotificationAudit(r.Context(), "provider_capability.created", id, "能力目录新增: "+capability.Key(), map[string]any{"name": capability.Name, "version": capability.Version}, actor.Principal.ID, string(actor.Principal.Kind))
	jsonResp(w, http.StatusCreated, capability)
}

// UpdateProviderCapability replaces an active capability in place.
func (h *Handler) UpdateProviderCapability(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	capability, ok := h.decodeProviderCapabilityBody(w, r)
	if !ok {
		return
	}
	actor := identityFromContext(r.Context())
	err := h.providerCapabilities.Update(r.Context(), r.PathValue("id"), capability)
	if errors.Is(err, pgx.ErrNoRows) {
		jsonError(w, http.StatusNotFound, domain.ProviderErrCapabilityNotFound, "能力不存在或已停用")
		return
	}
	if err != nil {
		jsonError(w, http.StatusConflict, "PROVIDER_CAPABILITY_CONFLICT", "能力名称与版本冲突")
		return
	}
	h.writeNotificationAudit(r.Context(), "provider_capability.updated", r.PathValue("id"), "能力目录更新: "+capability.Key(), map[string]any{"name": capability.Name, "version": capability.Version}, actor.Principal.ID, string(actor.Principal.Kind))
	capability.ID = r.PathValue("id")
	jsonResp(w, http.StatusOK, capability)
}

// DeleteProviderCapability soft-disables a capability; in-flight resolved
// bindings remain signed in their executions, but future resolution refuses it.
func (h *Handler) DeleteProviderCapability(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	deleted, err := h.providerCapabilities.Delete(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "删除能力失败")
		return
	}
	if !deleted {
		jsonError(w, http.StatusNotFound, domain.ProviderErrCapabilityNotFound, "能力不存在或已停用")
		return
	}
	actor := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "provider_capability.deleted", r.PathValue("id"), "能力目录停用", map[string]any{"id": r.PathValue("id")}, actor.Principal.ID, string(actor.Principal.Kind))
	w.WriteHeader(http.StatusNoContent)
}

func enforceActionApprovalExemption(decision domain.DecisionResult, actionName string) domain.DecisionResult {
	if decision == domain.DecisionRequireApproval && actionName == "jenkins.job.create" {
		return domain.DecisionAllow
	}
	return decision
}

func enforceProductionChangeApproval(decision domain.DecisionResult, environment, actionName string, contract domain.ChangeSafetyContract) domain.DecisionResult {
	if decision == domain.DecisionAllow && environment == "production" && contract.Change && actionName != "jenkins.job.create" {
		return domain.DecisionRequireApproval
	}
	return decision
}

// canAccessExecution mirrors the ListExecutions visibility ladder: platform
// admins see every run, tenant admins see every run in their own tenant (the
// repository's tenant filter already enforces the boundary), members only
// their own. Detail and cancel must agree with the list, otherwise tenant
// admins get "visible in list, 404 by ID" on member and agent executions.
func canAccessExecution(identity *AuthenticatedIdentity, e *domain.Execution) bool {
	if identity == nil || identity.Principal == nil {
		return false
	}
	return identity.HasScope("warden:admin") || identity.IsTenantAdministrator() || e.PrincipalID == identity.Principal.ID
}

func (h *Handler) GetExecution(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	e, err := h.executions.GetByID(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询执行记录失败")
		return
	}
	if e == nil {
		jsonError(w, http.StatusNotFound, "NOT_FOUND", "执行记录不存在")
		return
	}
	if identity := identityFromContext(r.Context()); !canAccessExecution(identity, e) {
		jsonError(w, http.StatusNotFound, "NOT_FOUND", "执行记录不存在")
		return
	}
	h.enrichExecution(r.Context(), e)
	jsonResp(w, http.StatusOK, executionToResp(e))
}

func (h *Handler) CancelExecution(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	e, err := h.executions.GetByID(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询执行记录失败")
		return
	}
	if e == nil {
		jsonError(w, http.StatusNotFound, "NOT_FOUND", "执行记录不存在")
		return
	}
	identity := identityFromContext(r.Context())
	if !canAccessExecution(identity, e) {
		jsonError(w, http.StatusNotFound, "NOT_FOUND", "执行记录不存在")
		return
	}
	if e.Status == domain.ExecWaitingApproval {
		request, err := h.requests.GetPendingByExecutionID(r.Context(), e.ID)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "CANCEL_FAILED", "查询待审批请求失败")
			return
		}
		tx, err := h.pool.Begin(r.Context())
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "CANCEL_FAILED", "开始取消事务失败")
			return
		}
		defer tx.Rollback(r.Context())
		changed, err := h.executions.TransitionStatusTx(r.Context(), tx, e.ID, domain.ExecWaitingApproval, domain.ExecCancelled, "", "用户取消")
		if err != nil || !changed {
			jsonError(w, http.StatusConflict, "EXECUTION_STATE_CHANGED", "执行状态已变化")
			return
		}
		if request != nil {
			if err := h.requests.UpdateStatusTx(r.Context(), tx, request.ID, string(domain.RequestPending), string(domain.RequestCancelled), "", nil); err != nil {
				jsonError(w, http.StatusConflict, "REQUEST_STATE_CHANGED", "审批请求状态已变化")
				return
			}
			cancelledAudit := executionAuditEvent("access_request.cancelled", identity, nil, nil, e.ID, e.TraceID)
			attachExecutionAudit(cancelledAudit, e)
			if err := h.audit.CreateTx(r.Context(), tx, cancelledAudit); err != nil {
				jsonError(w, http.StatusInternalServerError, "AUDIT_WRITE_FAILED", "取消审计写入失败")
				return
			}
		}
		if err := tx.Commit(r.Context()); err != nil {
			jsonError(w, http.StatusInternalServerError, "CANCEL_FAILED", "提交取消事务失败")
			return
		}
		e.Status = domain.ExecCancelled
		if request != nil {
			h.notifyApprovalAsync(r.Context(), *request, "access_request.cancelled", "")
		}
	} else if e.Status == domain.ExecQueued {
		changed, err := h.executions.TransitionStatus(r.Context(), e.ID, e.Status, domain.ExecCancelled, "", "用户取消")
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "CANCEL_FAILED", "取消执行失败")
			return
		}
		if !changed {
			jsonError(w, http.StatusConflict, "EXECUTION_STATE_CHANGED", "执行状态已变化")
			return
		}
		e.Status = domain.ExecCancelled
	}
	jsonResp(w, http.StatusOK, executionToResp(e))
}

func (h *Handler) ListExecutions(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	statusFilter := r.URL.Query().Get("status")
	limit, offset := listLimitOffset(r, 50)
	identity := identityFromContext(r.Context())
	execs, err := h.listExecutionsPage(r.Context(), identity, statusFilter, r.URL.Query().Get("projectId"), limit, offset)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询执行列表失败")
		return
	}
	h.enrichExecutions(r.Context(), execs)
	result := make([]any, len(execs))
	for i, e := range execs {
		result[i] = executionToResp(&e)
	}
	jsonResp(w, http.StatusOK, result)
}

// listExecutionsPage applies the visibility ladder (super admin cross-tenant,
// tenant admin whole-org, member own runs) plus project filtering with honest
// pagination: offset/limit count rows of the FILTERED view. Without an active
// project filter the repository's own LIMIT/OFFSET is used; otherwise source
// pages are scanned in order and filtered batch by batch, instead of the old
// "fetch 50 then filter" which silently shrank pages and could not continue.
func (h *Handler) listExecutionsPage(ctx context.Context, identity *AuthenticatedIdentity, statusFilter, projectID string, limit, offset int) ([]domain.Execution, error) {
	listAll := identity.IsPlatformAdmin && domain.TenantIDFromContext(ctx) == identity.TenantID
	fetch := func(l, o int) ([]domain.Execution, error) {
		if listAll {
			return h.executions.ListAllTenants(ctx, statusFilter, l, o)
		}
		principalID := ""
		if !identity.IsTenantAdministrator() {
			principalID = identity.Principal.ID
		}
		return h.executions.List(ctx, principalID, statusFilter, l, o)
	}
	_, scoped, err := principalProjectScope(identity)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(projectID) == "" && !scoped {
		return fetch(limit, offset)
	}
	const chunk = 200
	out := make([]domain.Execution, 0, limit)
	skipped, sourceOffset := 0, 0
	for len(out) < limit {
		batch, err := fetch(chunk, sourceOffset)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		filtered, err := h.filterExecutionsForProject(ctx, identity, batch, projectID)
		if err != nil {
			return nil, err
		}
		for _, e := range filtered {
			if skipped < offset {
				skipped++
				continue
			}
			out = append(out, e)
			if len(out) == limit {
				break
			}
		}
		sourceOffset += len(batch)
		if len(batch) < chunk {
			break
		}
	}
	return out, nil
}

// enrichExecutions batch-loads principal names and API key names for a list of
// executions so the UI can display "Warden ReadOnly · key warden-agent" instead
// of "375bb5e5 · key adb456e7".
func (h *Handler) enrichExecutions(ctx context.Context, execs []domain.Execution) {
	if len(execs) == 0 || h.principals == nil {
		return
	}
	seen := make(map[string]bool, len(execs))
	principalIDs := make([]string, 0, len(execs))
	for _, e := range execs {
		if !seen[e.PrincipalID] {
			seen[e.PrincipalID] = true
			principalIDs = append(principalIDs, e.PrincipalID)
		}
	}
	pMap := make(map[string]*domain.Principal, len(principalIDs))
	if summaries, err := h.principals.SummariesByIDs(ctx, principalIDs); err == nil {
		for id, p := range summaries {
			p := p
			pMap[id] = &p
		}
	}
	credSeen := make(map[string]bool)
	credIDs := make([]string, 0)
	for _, e := range execs {
		if e.CredentialID != "" && !credSeen[e.CredentialID] {
			credSeen[e.CredentialID] = true
			credIDs = append(credIDs, e.CredentialID)
		}
	}
	kMap := make(map[string]string, len(credIDs))
	if h.apiKeys != nil {
		if names, err := h.apiKeys.NamesByIDs(ctx, credIDs); err == nil {
			kMap = names
		}
	}
	for i := range execs {
		if p, ok := pMap[execs[i].PrincipalID]; ok {
			execs[i].PrincipalName = p.Name
			execs[i].PrincipalKind = string(p.Kind)
		}
		if name, ok := kMap[execs[i].CredentialID]; ok {
			execs[i].CredentialName = name
		}
	}
	// Batch-load resource names so clients don't have to download the full
	// resource catalog just to render "prod-jenkins" instead of a UUID prefix.
	if h.resources != nil {
		resSeen := make(map[string]bool, len(execs))
		resIDs := make([]string, 0, len(execs))
		for _, e := range execs {
			if e.ResourceID != "" && !resSeen[e.ResourceID] {
				resSeen[e.ResourceID] = true
				resIDs = append(resIDs, e.ResourceID)
			}
		}
		if rMap, err := h.resources.SummariesByIDs(ctx, resIDs); err == nil {
			for i := range execs {
				if s, ok := rMap[execs[i].ResourceID]; ok {
					execs[i].ResourceName = s.Name
					execs[i].ResourceType = s.ResourceType
				}
			}
		}
	}
	if h.actions != nil {
		actionSeen := make(map[string]bool, len(execs))
		actionIDs := make([]string, 0, len(execs))
		for _, exec := range execs {
			if exec.ActionID != "" && !actionSeen[exec.ActionID] {
				actionSeen[exec.ActionID] = true
				actionIDs = append(actionIDs, exec.ActionID)
			}
		}
		if actionMap, err := h.actions.SummariesByIDs(ctx, actionIDs); err == nil {
			for i := range execs {
				if action, ok := actionMap[execs[i].ActionID]; ok {
					execs[i].ActionName = action.Name
					execs[i].ActionDisplay = action.DisplayName
					execs[i].ActionRisk = action.RiskLevel
				}
			}
		}
	}
}

func (h *Handler) enrichExecution(ctx context.Context, e *domain.Execution) {
	if h.principals == nil || e == nil {
		return
	}
	if p, err := h.principals.GetByID(ctx, e.PrincipalID); err == nil && p != nil {
		e.PrincipalName = p.Name
		e.PrincipalKind = string(p.Kind)
	}
	if e.CredentialID != "" && h.apiKeys != nil {
		if k, err := h.apiKeys.GetByID(ctx, e.CredentialID); err == nil && k != nil {
			e.CredentialName = k.Name
		}
	}
	if h.actions != nil && e.ActionID != "" {
		if actionMap, err := h.actions.SummariesByIDs(ctx, []string{e.ActionID}); err == nil {
			if action, ok := actionMap[e.ActionID]; ok {
				e.ActionName = action.Name
				e.ActionDisplay = action.DisplayName
				e.ActionRisk = action.RiskLevel
			}
		}
	}
	if h.resources != nil && e.ResourceID != "" {
		if resourceMap, err := h.resources.SummariesByIDs(ctx, []string{e.ResourceID}); err == nil {
			if resource, ok := resourceMap[e.ResourceID]; ok {
				e.ResourceName = resource.Name
				e.ResourceType = resource.ResourceType
			}
		}
	}
}

// --- Resources ---

func (h *Handler) ListResources(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	typeFilter := r.URL.Query().Get("type")
	domainFilter := r.URL.Query().Get("domain")
	excludeType := r.URL.Query().Get("excludeType")
	namespaceFilter := r.URL.Query().Get("namespace")
	integrationFilter := r.URL.Query().Get("integrationId")
	listLimit := 0
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		if parsed, parseErr := strconv.Atoi(rawLimit); parseErr == nil && parsed > 0 && parsed <= 1000 {
			listLimit = parsed
		}
	}
	listOffset := 0
	if rawOffset := r.URL.Query().Get("offset"); rawOffset != "" {
		if parsed, parseErr := strconv.Atoi(rawOffset); parseErr == nil && parsed >= 0 {
			listOffset = parsed
		}
	}
	if domainFilter != "" && domainFilter != "kubernetes" && domainFilter != "linux" && domainFilter != "jenkins" &&
		domainFilter != "grafana" && domainFilter != "database" && domainFilter != "huawei" && domainFilter != "network" {
		jsonError(w, http.StatusBadRequest, "INVALID_RESOURCE_DOMAIN", "资源域参数无效")
		return
	}
	var resources []domain.Resource
	var err error
	topology := r.URL.Query().Get("topology") == "true"
	identity := identityFromContext(r.Context())
	_, principalScoped, scopeErr := principalProjectScope(identity)
	if scopeErr != nil {
		jsonError(w, http.StatusInternalServerError, "PROJECT_SCOPE_ERROR", "校验资源项目权限失败")
		return
	}
	restrictedByMembership := h.projectMembershipEnforced(r.Context()) && identity != nil && !identity.IsTenantAdministrator()
	restrictedTopology := topology && (restrictedByMembership || principalScoped)
	if r.URL.Query().Get("availableForExecution") == "true" {
		resources, err = h.resources.ListAvailable(r.Context(), typeFilter)
	} else {
		if topology && !restrictedTopology {
			resources, err = h.resources.ListTopology(r.Context())
		} else {
			resources, err = h.resources.List(r.Context(), typeFilter, domainFilter, excludeType, namespaceFilter, integrationFilter, listLimit, listOffset)
		}
	}
	if err != nil {
		log.Printf("list resources failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询资源失败")
		return
	}
	if resources == nil {
		resources = []domain.Resource{}
	}
	if topology && !restrictedTopology {
		jsonResp(w, http.StatusOK, resources)
		return
	}
	if err := h.resources.EnrichProjectScopes(r.Context(), resources); err != nil {
		log.Printf("enrich resource project scopes failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询资源项目范围失败")
		return
	}
	if projectID := strings.TrimSpace(r.URL.Query().Get("projectId")); projectID != "" {
		if err := h.projectAccess(r.Context(), projectID, false); err != nil {
			h.writeProjectMembershipError(w, err)
			return
		}
		if project, projectErr := h.projects.GetByID(r.Context(), projectID); projectErr != nil || project == nil {
			jsonError(w, http.StatusBadRequest, "INVALID_PROJECT", "项目不存在")
			return
		}
		filtered := make([]domain.Resource, 0, len(resources))
		for _, resource := range resources {
			for _, allowedProjectID := range resource.ProjectIDs {
				if allowedProjectID == projectID {
					filtered = append(filtered, resource)
					break
				}
			}
		}
		resources = filtered
	}
	resources, err = h.filterResourcesForIdentity(r.Context(), identity, resources, r.URL.Query().Get("projectId"))
	if err != nil {
		log.Printf("filter resources by principal project scope failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "PROJECT_SCOPE_ERROR", "按身份过滤资源失败")
		return
	}
	if resources, err = h.filterResourcesByProjectMembership(r.Context(), resources); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询项目成员范围失败")
		return
	}
	if restrictedTopology {
		jsonResp(w, http.StatusOK, projectTopology(resources))
		return
	}
	if err := h.enrichResourceActionAccess(r.Context(), resources); err != nil {
		log.Printf("enrich resource action access failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询资源动作能力失败")
		return
	}
	jsonResp(w, http.StatusOK, resources)
}

func projectTopology(resources []domain.Resource) []domain.Resource {
	items := map[string]domain.Resource{}
	counts := map[string]int{}
	for _, resource := range resources {
		key := resource.IntegrationID + ":" + resource.ConnectorID + ":" + resource.ResourceType + ":" + string(resource.Status)
		counts[key]++
		if _, exists := items[key]; !exists {
			items[key] = domain.Resource{
				ID: "topology:" + key, Name: resource.ResourceType, ResourceType: resource.ResourceType,
				IntegrationID: resource.IntegrationID, ConnectorID: resource.ConnectorID, Status: resource.Status,
				Metadata: map[string]any{},
			}
		}
	}
	result := make([]domain.Resource, 0, len(items))
	for key, item := range items {
		item.Metadata["topologyCount"] = counts[key]
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func catalogResourceDomain(resourceType string) string {
	switch {
	case strings.HasPrefix(resourceType, "jenkins"):
		return "jenkins"
	case strings.HasPrefix(resourceType, "grafana"):
		return "grafana"
	case strings.HasPrefix(resourceType, "database"):
		return "database"
	case strings.HasPrefix(resourceType, "huawei"), strings.HasPrefix(resourceType, "aliyun_sls"):
		return "huawei"
	case strings.HasPrefix(resourceType, "network_"):
		return "network"
	case genericKubernetesResourceType(resourceType):
		return "kubernetes"
	default:
		return "linux"
	}
}

func (h *Handler) enrichResourceActionAccess(ctx context.Context, resources []domain.Resource) error {
	actions, err := h.actions.ListAll(ctx)
	if err != nil {
		return err
	}
	connectors, err := h.connectors.List(ctx)
	if err != nil {
		return err
	}
	actionsByType := make(map[string][]domain.ActionDefinition)
	for _, action := range actions {
		actionsByType[action.ResourceType] = append(actionsByType[action.ResourceType], action)
	}
	connectorsByID := make(map[string]domain.Connector, len(connectors))
	for _, connector := range connectors {
		connectorsByID[connector.ID] = connector
	}
	for i := range resources {
		resourceActions := actionsByType[resources[i].ResourceType]
		for _, genericAction := range actionsByType["kubernetes_resource"] {
			if genericKubernetesActionApplies(resources[i].ResourceType, genericAction.Name) {
				genericAction.ResourceType = resources[i].ResourceType
				resourceActions = append(resourceActions, genericAction)
			}
		}
		if resources[i].ManagementType == domain.ManagementControlPlane {
			capabilities := []string{domain.ChangeSafetyCapability}
			controlActions := make([]domain.ActionDefinition, 0, len(resourceActions))
			for _, action := range resourceActions {
				if action.ExecutorType == domain.ExecutorControlPlane {
					controlActions = append(controlActions, action)
					capabilities = append(capabilities, domain.RequiredCapability(action.Name, action.Version))
				}
			}
			resources[i].ActionAccess, resources[i].AvailableActionCount, resources[i].ActionUpgradeRequired =
				domain.SummarizeActionAccess(capabilities, controlActions)
			continue
		}
		connector := connectorsByID[resources[i].ConnectorID]
		resources[i].ActionAccess, resources[i].AvailableActionCount, resources[i].ActionUpgradeRequired =
			domain.SummarizeActionAccess(connector.Capabilities, resourceActions)
	}
	return nil
}

// --- Diagnostics (session trace context + result aggregation) ---

type createDiagnosticContextRequest struct {
	Project     string            `json:"project"`
	Environment string            `json:"environment"`
	Query       string            `json:"query"`
	Labels      map[string]string `json:"labels"`
	TraceID     string            `json:"traceId"`
	TimeWindow  *struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	} `json:"timeWindow"`
}

// CreateDiagnosticContext resolves a session trace id and returns a filtered,
// server-side working directory of resources the agent may target, together
// with each resource's available action names. It performs no dispatch and no
// policy decision; every subsequent execution still runs the full pipeline.
func (h *Handler) CreateDiagnosticContext(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var req createDiagnosticContextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	traceID := req.TraceID
	if !domain.ValidateTraceID(traceID) {
		traceID = domain.NewTraceID()
	}
	ctx := r.Context()
	resources, err := h.resources.ListAvailable(ctx, "")
	if err != nil {
		log.Printf("diagnostic context list resources: %v", err)
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询资源失败")
		return
	}
	if err := h.resources.EnrichProjectScopes(ctx, resources); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询资源项目范围失败")
		return
	}
	resources, err = h.filterResourcesByProjectMembership(ctx, resources)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询项目成员范围失败")
		return
	}
	resources, err = h.filterResourcesForIdentity(ctx, identityFromContext(ctx), resources, "")
	if err != nil {
		log.Printf("diagnostic context principal project scope: %v", err)
		jsonError(w, http.StatusInternalServerError, "PROJECT_SCOPE_ERROR", "按身份过滤诊断资源失败")
		return
	}
	filtered := filterDiagnosticResources(resources, req)
	namesByResource, err := h.diagnosticActionNames(ctx, filtered)
	if err != nil {
		log.Printf("diagnostic action names: %v", err)
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询动作能力失败")
		return
	}

	items := make([]map[string]any, 0, len(filtered))
	byType := map[string]int{}
	byEnvironment := map[string]int{}
	for _, res := range filtered {
		byType[res.ResourceType]++
		byEnvironment[res.Environment]++
		items = append(items, map[string]any{
			"id":               res.ID,
			"name":             res.Name,
			"resourceType":     res.ResourceType,
			"projectName":      res.ProjectName,
			"environment":      res.Environment,
			"status":           res.Status,
			"connectorId":      res.ConnectorID,
			"integrationId":    res.IntegrationID,
			"availableActions": namesByResource[res.ID],
		})
	}
	jsonResp(w, http.StatusOK, map[string]any{
		"traceId":    traceID,
		"timeWindow": req.TimeWindow,
		"summary": map[string]any{
			"total":         len(items),
			"byType":        byType,
			"byEnvironment": byEnvironment,
		},
		"resources": items,
	})
}

// GetDiagnosticResult aggregates every execution sharing a session trace into
// a provider-agnostic classification view. It exposes only resource business
// identifiers plus classification; internal ids and secrets never appear.
func (h *Handler) GetDiagnosticResult(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	traceID := r.PathValue("traceId")
	if !domain.ValidateTraceID(traceID) {
		jsonError(w, http.StatusBadRequest, "INVALID_TRACE_ID", "traceId 格式无效")
		return
	}
	rows, err := h.executions.ListByTraceID(r.Context(), traceID)
	if err != nil {
		log.Printf("diagnostic result list: %v", err)
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询诊断结果失败")
		return
	}
	summary := map[string]int{
		string(domain.DiagnosticSucceeded):    0,
		string(domain.DiagnosticPartial):      0,
		string(domain.DiagnosticNoData):       0,
		string(domain.DiagnosticTimeout):      0,
		string(domain.DiagnosticNoPermission): 0,
		string(domain.DiagnosticNotConnected): 0,
		string(domain.DiagnosticFailed):       0,
		string(domain.DiagnosticUnknown):      0,
	}
	results := make([]map[string]any, 0, len(rows))
	identity := identityFromContext(r.Context())
	for _, row := range rows {
		if identity != nil && !identity.IsTenantAdministrator() && row.PrincipalID != identity.Principal.ID {
			continue
		}
		class := domain.ClassifyDiagnosticStatus(row.Status, providerStatusFromResult(row.Result), row.ErrorCode)
		summary[string(class)]++
		results = append(results, map[string]any{
			"resource": map[string]any{
				"name":        row.ResourceName,
				"type":        row.ResourceType,
				"projectName": row.ProjectName,
				"environment": row.Environment,
			},
			"classification": class,
			"status":         row.Status,
			"startedAt":      row.CreatedAt,
			"finishedAt":     row.UpdatedAt,
		})
	}
	jsonResp(w, http.StatusOK, map[string]any{
		"traceId":               traceID,
		"classificationSummary": summary,
		"results":               results,
	})
}

// providerStatusFromResult extracts the provider response status embedded in a
// persisted execution result. A missing or non-string status yields "" so the
// classifier falls through to the execution status.
func providerStatusFromResult(result map[string]any) domain.ProviderResponseStatus {
	if result == nil {
		return ""
	}
	if raw, ok := result["status"].(string); ok {
		return domain.ProviderResponseStatus(raw)
	}
	return ""
}

// filterDiagnosticResources narrows the ACTIVE resource directory in memory
// using project, environment, free-text query, and label equality. Matching is
// case-insensitive; an empty filter is a pass-through.
func filterDiagnosticResources(resources []domain.Resource, req createDiagnosticContextRequest) []domain.Resource {
	project := strings.ToLower(strings.TrimSpace(req.Project))
	environment := strings.ToLower(strings.TrimSpace(req.Environment))
	query := strings.ToLower(strings.TrimSpace(req.Query))
	out := make([]domain.Resource, 0, len(resources))
	for _, res := range resources {
		if project != "" && !strings.EqualFold(res.ProjectName, project) && !strings.EqualFold(res.ProjectID, project) {
			continue
		}
		if environment != "" && !strings.EqualFold(res.Environment, environment) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(res.Name+" "+res.Namespace+" "+res.ExternalID), query) {
			continue
		}
		if !resourceMatchesLabels(res, req.Labels) {
			continue
		}
		out = append(out, res)
	}
	return out
}

func resourceMatchesLabels(res domain.Resource, labels map[string]string) bool {
	for key, value := range labels {
		raw, ok := res.Labels[key]
		if !ok {
			return false
		}
		if !strings.EqualFold(fmt.Sprint(raw), value) {
			return false
		}
	}
	return true
}

// diagnosticActionNames returns the action names each resource can execute,
// honoring connector capability + change-safety gating without a per-resource
// query. provider.request/provider.write are generic and handled explicitly
// because they are keyed under the synthetic provider_resource type.
func (h *Handler) diagnosticActionNames(ctx context.Context, resources []domain.Resource) (map[string][]string, error) {
	actions, err := h.actions.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	connectors, err := h.connectors.List(ctx)
	if err != nil {
		return nil, err
	}
	actionsByType := map[string][]domain.ActionDefinition{}
	for _, action := range actions {
		actionsByType[action.ResourceType] = append(actionsByType[action.ResourceType], action)
	}
	capsByConnector := map[string][]string{}
	for _, connector := range connectors {
		capsByConnector[connector.ID] = connector.Capabilities
	}
	out := make(map[string][]string, len(resources))
	for _, res := range resources {
		caps := capsByConnector[res.ConnectorID]
		seen := map[string]bool{}
		var names []string
		add := func(name string) {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
		for _, action := range actionsByType[res.ResourceType] {
			if supportsActionForResource(caps, action) {
				add(action.Name)
			}
		}
		for _, action := range actionsByType["kubernetes_resource"] {
			if genericKubernetesActionApplies(res.ResourceType, action.Name) && supportsActionForResource(caps, action) {
				add(action.Name)
			}
		}
		if domain.SupportsAction(caps, "provider.request", 1) {
			add("provider.request")
		}
		if res.IntegrationID != "" && domain.SupportsAction(caps, "provider.write", 1) && domain.SupportsChangeSafety(caps) {
			add("provider.write")
		}
		sort.Strings(names)
		out[res.ID] = names
	}
	return out, nil
}

func supportsActionForResource(caps []string, action domain.ActionDefinition) bool {
	if !domain.SupportsAction(caps, action.Name, action.Version) {
		return false
	}
	if domain.ActionSafetyContract(action.Name, action.RiskLevel).Change && !domain.SupportsChangeSafety(caps) {
		return false
	}
	return true
}

// ResourceStats returns aggregate resource counts (total/active/byType/byStatus)
// so overview-style pages can render KPIs without the full catalog payload.
func (h *Handler) ResourceStats(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	identity := identityFromContext(r.Context())
	_, principalScoped, scopeErr := principalProjectScope(identity)
	if scopeErr != nil {
		jsonError(w, http.StatusInternalServerError, "PROJECT_SCOPE_ERROR", "校验资源项目权限失败")
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("projectId"))
	membershipRestricted := h.projectMembershipEnforced(r.Context()) && identity != nil && !identity.IsTenantAdministrator()
	if !membershipRestricted && !principalScoped && projectID == "" {
		stats, err := h.resources.Stats(r.Context())
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询资源统计失败")
			return
		}
		jsonResp(w, http.StatusOK, stats)
		return
	}
	resources, err := h.resources.List(r.Context(), "", "", "", "", "", 0, 0)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询资源统计失败")
		return
	}
	if err := h.resources.EnrichProjectScopes(r.Context(), resources); err != nil {
		jsonError(w, 500, "DB_ERROR", "查询资源项目范围失败")
		return
	}
	resources, err = h.filterResourcesForIdentity(r.Context(), identity, resources, projectID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PROJECT_SCOPE_ERROR", "按身份过滤资源统计失败")
		return
	}
	resources, err = h.filterResourcesByProjectMembership(r.Context(), resources)
	if err != nil {
		jsonError(w, 500, "DB_ERROR", "查询项目成员范围失败")
		return
	}
	jsonResp(w, http.StatusOK, resourceStatsForResources(resources))
}

func resourceStatsForResources(resources []domain.Resource) map[string]any {
	byType, byStatus := map[string]int{}, map[string]int{}
	catalogByDomainStatus := map[string]map[string]int{}
	catalogByProjectDomainStatus := map[string]map[string]map[string]int{}
	total, active, current, history, abnormal := 0, 0, 0, 0, 0
	for _, resource := range resources {
		total++
		byType[resource.ResourceType]++
		byStatus[string(resource.Status)]++
		if resource.Status == domain.ResourceActive {
			active++
		}
		if resource.Status == domain.ResourceOrphaned {
			history++
		} else {
			current++
			if resource.Status != domain.ResourceActive {
				abnormal++
			}
		}
		resourceDomain := catalogResourceDomain(resource.ResourceType)
		status := string(resource.Status)
		if catalogByDomainStatus[resourceDomain] == nil {
			catalogByDomainStatus[resourceDomain] = map[string]int{}
		}
		catalogByDomainStatus[resourceDomain][status]++
		for _, projectID := range resource.ProjectIDs {
			if catalogByProjectDomainStatus[projectID] == nil {
				catalogByProjectDomainStatus[projectID] = map[string]map[string]int{}
			}
			if catalogByProjectDomainStatus[projectID][resourceDomain] == nil {
				catalogByProjectDomainStatus[projectID][resourceDomain] = map[string]int{}
			}
			catalogByProjectDomainStatus[projectID][resourceDomain][status]++
		}
	}
	return map[string]any{"total": total, "current": current, "active": active, "history": history, "abnormal": abnormal, "byType": byType, "byStatus": byStatus, "catalogByDomainStatus": catalogByDomainStatus, "catalogByProjectDomainStatus": catalogByProjectDomainStatus}
}

// ReportIntegrationHealth records a connector-side probe result (reconcile or
// credential errors) so the UI can surface unhealthy integrations.
func (h *Handler) ReportIntegrationHealth(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	connectorID := r.PathValue("id")
	integrationID := r.PathValue("integrationID")
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_BODY", "请求体格式错误")
		return
	}
	if err := h.integrations.ReportHealth(r.Context(), connectorID, integrationID, domain.SanitizeErrorMessage(body.Error)); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "记录探活结果失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) ListAvailableActions(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	actions, err := h.actions.ListAvailable(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询动作失败")
		return
	}
	if actions == nil {
		actions = []domain.ActionDefinition{}
	}
	jsonResp(w, http.StatusOK, actions)
}

func (h *Handler) ListResourceActions(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	id := r.PathValue("id")
	res, err := h.resources.GetByID(r.Context(), id)
	if err != nil || res == nil {
		jsonError(w, http.StatusNotFound, "NOT_FOUND", "资源不存在")
		return
	}
	if accessErr := h.ensureResourceProjectAccess(r.Context(), res, false); accessErr != nil {
		h.writeProjectMembershipError(w, accessErr)
		return
	}
	projectScopeAccess, scopeErr := h.identityResourceProjectAccess(r.Context(), identityFromContext(r.Context()), res, "")
	if scopeErr != nil {
		jsonError(w, http.StatusInternalServerError, "PROJECT_SCOPE_ERROR", "校验资源项目权限失败")
		return
	}
	if projectScopeAccess == resourceProjectAccessNone {
		jsonError(w, http.StatusNotFound, "NOT_FOUND", "资源不存在")
		return
	}
	actions, err := h.actions.GetByResourceType(r.Context(), res.ResourceType)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询动作失败")
		return
	}
	if actions == nil {
		actions = []domain.ActionDefinition{}
	}
	if genericKubernetesResourceType(res.ResourceType) {
		genericActions, genericErr := h.actions.GetByResourceType(r.Context(), "kubernetes_resource")
		if genericErr != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询 Kubernetes 通用动作失败")
			return
		}
		for _, action := range genericActions {
			if genericKubernetesActionApplies(res.ResourceType, action.Name) {
				action.ResourceType = res.ResourceType
				actions = append(actions, action)
			}
		}
	}
	// provider.request is a governed read that applies to any resource whose
	// Integration protocol (or native Kubernetes) has registered provider
	// capabilities. Project it onto the per-resource action list so agents can
	// discover the action together with its capabilities.
	hasProviderCapabilities := false
	if protocol := resourceProviderProtocol(r.Context(), h, res); protocol != "" {
		capabilities, capabilitiesErr := h.providerCapabilitiesFor(r.Context(), res.ResourceType, protocol)
		if capabilitiesErr != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询能力目录失败")
			return
		}
		hasProviderCapabilities = len(capabilities) > 0
	}
	if hasProviderCapabilities {
		providerAction, providerErr := h.actions.GetByName(r.Context(), "provider.request")
		if providerErr != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询 provider.request 动作失败")
			return
		}
		if providerAction != nil {
			providerAction.ResourceType = res.ResourceType
			actions = append(actions, *providerAction)
		}
	}
	var connector *domain.Connector
	if res.ManagementType != domain.ManagementControlPlane {
		connector, err = h.connectors.GetByID(r.Context(), res.ConnectorID)
		if err != nil || connector == nil {
			jsonError(w, http.StatusNotFound, "CONNECTOR_NOT_FOUND", "资源所属 Connector 不存在")
			return
		}
	}
	includeUnavailable := r.URL.Query().Get("includeUnavailable") == "true"
	result := make([]actionAvailabilityResponse, 0, len(actions))
	for _, action := range actions {
		if projectScopeAccess == resourceProjectAccessConsumer && !consumerProjectCanRunAction(action.Name) {
			continue
		}
		item := actionAvailabilityResponse{ActionDefinition: action, RequiredCapability: domain.RequiredCapability(action.Name, action.Version)}
		if connector != nil {
			item.ConnectorVersion = connector.Version
		}
		item.Available, item.AvailabilityCode, item.AvailabilityReason = resourceActionAvailability(r.Context(), h, res, connector, action)
		if connector != nil && !item.Available && domain.SupportsAction(connector.Capabilities, action.Name, action.Version) &&
			domain.ActionSafetyContract(action.Name, action.RiskLevel).Change && !domain.SupportsChangeSafety(connector.Capabilities) {
			item.RequiredCapability = domain.ChangeSafetyCapability
		}
		if item.Available || includeUnavailable {
			result = append(result, item)
		}
	}
	jsonResp(w, http.StatusOK, result)
}

var genericKubernetesConcreteTypes = map[string]bool{
	"configmap": true, "service": true, "deployment": true, "statefulset": true, "daemonset": true,
	"job": true, "cronjob": true, "ingress": true, "networkpolicy": true, "poddisruptionbudget": true,
}

func genericKubernetesResourceType(resourceType string) bool {
	return resourceType == "namespace" || genericKubernetesConcreteTypes[resourceType]
}

func genericKubernetesActionApplies(resourceType, actionName string) bool {
	if resourceType == "namespace" {
		return actionName == "kubernetes.resource.list" || actionName == "kubernetes.resource.create" || actionName == "kubernetes.resource.preview"
	}
	if !genericKubernetesConcreteTypes[resourceType] {
		return false
	}
	return actionName == "kubernetes.resource.get" || actionName == "kubernetes.resource.preview" || actionName == "kubernetes.resource.update" || actionName == "kubernetes.resource.delete"
}

// resourceProviderProtocol resolves the protocol a resource would use for a
// provider.request: its Integration protocol, or "kubernetes" for resources
// owned by a native Kubernetes Connector.
func resourceProviderProtocol(ctx context.Context, h *Handler, resource *domain.Resource) string {
	if resource.IntegrationID != "" {
		integration, err := h.integrations.GetByID(ctx, resource.IntegrationID)
		if err != nil || integration == nil {
			return ""
		}
		return integration.Protocol
	}
	connector, err := h.connectors.GetByID(ctx, resource.ConnectorID)
	if err != nil || connector == nil || connector.ConnectorType != "kubernetes" {
		return ""
	}
	return "kubernetes"
}

// resourceSupportsProviderRequest reports whether a resource's protocol has
// registered provider capabilities, which is when provider.request is
// meaningful for it.
func resourceSupportsProviderRequest(resourceType, protocol string) bool {
	return protocol != "" && len(domain.ProviderCapabilitiesFor(resourceType, protocol)) > 0
}

type actionAvailabilityResponse struct {
	domain.ActionDefinition
	Available          bool   `json:"available"`
	AvailabilityCode   string `json:"availability_code,omitempty"`
	AvailabilityReason string `json:"availability_reason,omitempty"`
	RequiredCapability string `json:"required_capability"`
	ConnectorVersion   string `json:"connector_version,omitempty"`
}

func resourceActionAvailability(ctx context.Context, h *Handler, resource *domain.Resource, connector *domain.Connector, action domain.ActionDefinition) (bool, string, string) {
	if resource.Status != domain.ResourceActive {
		return false, "RESOURCE_NOT_ACTIVE", "资源未激活"
	}
	if resource.ManagementType == domain.ManagementControlPlane {
		if action.ExecutorType != domain.ExecutorControlPlane {
			return false, "EXECUTOR_MISMATCH", "动作不属于控制面执行器"
		}
		return true, "", ""
	}
	if connector == nil {
		return false, "CONNECTOR_NOT_FOUND", "执行通道不存在"
	}
	if connector.Status != domain.ConnectorActive {
		return false, "CONNECTOR_NOT_ACTIVE", "执行通道未激活"
	}
	if connector.LastHeartbeat == nil || time.Since(*connector.LastHeartbeat) > 90*time.Second {
		return false, "CONNECTOR_HEARTBEAT_TIMEOUT", "执行通道心跳已超时"
	}
	if resource.IntegrationID != "" {
		integration, err := h.integrations.GetByID(ctx, resource.IntegrationID)
		if err != nil || integration == nil || integration.Status != domain.IntegrationActive {
			return false, "INTEGRATION_NOT_ACTIVE", "Integration 未启用"
		}
		if available, code, reason := h3cInterfaceActionScopeAvailability(action.Name, integration.Scope); !available {
			return false, code, reason
		}
	}
	if !domain.SupportsAction(connector.Capabilities, action.Name, action.Version) {
		return false, "CONNECTOR_UPGRADE_REQUIRED", "需要升级执行通道"
	}
	if domain.ActionSafetyContract(action.Name, action.RiskLevel).Change && !domain.SupportsChangeSafety(connector.Capabilities) {
		return false, "CONNECTOR_UPGRADE_REQUIRED", "需要升级执行通道以支持变更前后快照与验证"
	}
	return true, "", ""
}

func isH3CInterfaceMutationAction(action string) bool {
	switch action {
	case "network.interface.description.update", "network.interface.access_vlan.update",
		"network.interface.trunk_vlan.add", "network.interface.trunk_vlan.remove":
		return true
	default:
		return false
	}
}

func h3cInterfaceActionScopeAvailability(action string, scope map[string]any) (bool, string, string) {
	if isH3CInterfaceMutationAction(action) && !boolFromScope(scope, "allowInterfaceChanges") {
		return false, "NETWORK_CHANGES_DISABLED", "该 H3C Integration 尚未显式开启接口变更"
	}
	return true, "", ""
}

// --- Connectors ---

func (h *Handler) OnboardCluster(w http.ResponseWriter, r *http.Request) {
	if !legacyConnectorKeyEnabled() {
		jsonError(w, http.StatusGone, "LEGACY_ONBOARDING_DISABLED", "共享任务密钥接入已停用，请使用 Operator 配对流程")
		return
	}
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var cfg OnboardConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	if cfg.TokenAudience == "" {
		cfg.TokenAudience = "https://kubernetes.default.svc"
	}
	if cfg.ExecutionServiceAccount == "" {
		cfg.ExecutionServiceAccount = "warden-executor"
	}
	if cfg.ConnectorNamespace == "" {
		cfg.ConnectorNamespace = "warden-system"
	}
	if err := validateOnboardConfig(cfg); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_ONBOARD_CONFIG", err.Error())
		return
	}

	manifest, err := GenerateConnectorManifest(cfg)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "MANIFEST_GENERATION_FAILED", err.Error())
		return
	}

	// Persist a DRAFT connector row carrying the onboarding form as config.
	// Self-registration will later flip status to ACTIVE without overwriting config.
	configJSON := map[string]any{
		"connectorNamespace":      cfg.ConnectorNamespace,
		"allowedNamespaces":       cfg.AllowedNamespaces,
		"allowedImagePrefixes":    cfg.AllowedImagePrefixes,
		"controlURL":              cfg.ControlURL,
		"tokenAudience":           cfg.TokenAudience,
		"executionServiceAccount": cfg.ExecutionServiceAccount,
	}
	var connID string
	tenantID := domain.TenantIDFromContext(r.Context())
	if tenantID == "" {
		jsonError(w, http.StatusUnauthorized, "TENANT_CONTEXT_REQUIRED", "缺少租户上下文")
		return
	}
	err = h.pool.QueryRow(r.Context(),
		`INSERT INTO connectors (name, connector_type, environment, version, capabilities, config, status, tenant_id)
		VALUES ($1,'kubernetes',$2,'0.1.0','[]'::jsonb,$3,'DRAFT',$4)
		ON CONFLICT (name, tenant_id)
		DO UPDATE SET environment=EXCLUDED.environment, config=EXCLUDED.config, updated_at=now()
		RETURNING id`,
		cfg.Name, cfg.Environment, configJSON, tenantID).Scan(&connID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存 Connector 草稿失败")
		return
	}

	instructions := fmt.Sprintf(
		"1. Create signing key secret:\n"+
			"   kubectl create secret generic warden-task-signing -n %s --from-literal=key=<same key as WARDEN_TASK_SIGNING_KEY on warden-control>\n"+
			"2. Apply the manifest:\n"+
			"   kubectl apply -f manifest.yaml\n"+
			"3. warden-connector self-registers and resources will appear in the resource catalog automatically.",
		cfg.ConnectorNamespace,
	)
	_ = h.writeAudit(r.Context(), "connector.registered", nil, nil, "", "")
	jsonResp(w, http.StatusOK, map[string]any{
		"connectorId":  connID,
		"manifest":     manifest,
		"secretName":   "warden-task-signing",
		"instructions": instructions,
	})
}

func (h *Handler) RegisterConnector(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var req struct {
		Name          string         `json:"name"`
		ConnectorType string         `json:"connectorType"`
		Environment   string         `json:"environment"`
		Version       string         `json:"version"`
		Capabilities  []string       `json:"capabilities"`
		Config        map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	if req.Name == "" {
		jsonError(w, http.StatusBadRequest, "MISSING_NAME", "名称必填")
		return
	}
	if req.Environment == "" {
		req.Environment = "testing"
	}
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	capabilities, err := domain.NormalizeCapabilities(req.Capabilities)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_CONNECTOR_CAPABILITY", "Connector capability 格式无效: "+domain.SanitizeErrorMessage(err.Error()))
		return
	}
	req.Capabilities = capabilities
	tenantID := domain.TenantIDFromContext(r.Context())
	if tenantID == "" {
		jsonError(w, http.StatusUnauthorized, "TENANT_CONTEXT_REQUIRED", "Connector 未绑定租户")
		return
	}

	// Upsert: when a connector with the same name exists (e.g. created by the
	// onboarding flow with a DRAFT status and admin-provided config), only
	// refresh runtime fields. We deliberately do NOT overwrite config, status,
	// or environment so admin onboarding decisions survive self-registration.
	var connID string
	err = h.pool.QueryRow(r.Context(),
		`INSERT INTO connectors (name, tenant_id, connector_type, environment, version, capabilities, config, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'ACTIVE')
		ON CONFLICT (name, tenant_id)
		DO UPDATE SET version=EXCLUDED.version, capabilities=EXCLUDED.capabilities,
			status=CASE WHEN EXISTS (
				SELECT 1 FROM cluster_pairings WHERE connector_id=connectors.id AND status='DELETE_REQUESTED'
			) THEN 'DISABLED'::connector_status ELSE 'ACTIVE'::connector_status END,
			last_heartbeat=now(),
			archived_at=NULL
		RETURNING id`,
		req.Name, tenantID, req.ConnectorType, req.Environment, req.Version, req.Capabilities, req.Config).Scan(&connID)
	if err != nil {
		log.Printf("register connector %q failed: %v", req.Name, err)
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "注册 Connector 失败")
		return
	}
	_ = h.writeAudit(r.Context(), "connector.registered", nil, nil, "", "")
	jsonResp(w, http.StatusOK, map[string]string{"id": connID, "status": "ACTIVE"})
}

func (h *Handler) ConnectorHeartbeat(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	if err := h.connectors.Heartbeat(r.Context(), r.PathValue("id")); err != nil {
		jsonError(w, http.StatusNotFound, "NOT_FOUND", "Connector 不存在")
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) CheckConnector(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	connector, err := h.connectors.GetByID(r.Context(), r.PathValue("id"))
	if err != nil || connector == nil {
		jsonError(w, http.StatusNotFound, "CONNECTOR_NOT_FOUND", "Connector 不存在")
		return
	}
	ageSeconds := int64(-1)
	if connector.LastHeartbeat != nil {
		ageSeconds = int64(time.Since(*connector.LastHeartbeat).Seconds())
	}
	healthy := connector.Status == domain.ConnectorActive && ageSeconds >= 0 && ageSeconds <= 90
	jsonResp(w, http.StatusOK, map[string]any{
		"connectorId": connector.ID, "healthy": healthy, "status": connector.Status,
		"lastHeartbeat": connector.LastHeartbeat, "heartbeatAgeSeconds": ageSeconds, "checkedAt": time.Now(),
	})
}

func (h *Handler) ListConnectors(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	conns, err := h.connectors.List(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询 Connector 失败")
		return
	}
	jsonResp(w, http.StatusOK, conns)
}

func (h *Handler) PollTasks(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	connID := r.PathValue("id")
	if _, err := h.executions.ExpireDispatchLeases(r.Context()); err != nil {
		jsonError(w, http.StatusInternalServerError, "DISPATCH_RECOVERY_FAILED", "恢复过期任务失败")
		return
	}
	connector, err := h.connectors.GetByID(r.Context(), connID)
	if err != nil || connector == nil || connector.Status != domain.ConnectorActive {
		jsonError(w, http.StatusForbidden, "CONNECTOR_NOT_ACTIVE", "Connector 不存在或未激活")
		return
	}
	tasks, err := h.executions.ClaimTasks(r.Context(), connID, 10)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询任务失败")
		return
	}
	for i := range tasks {
		signingKey := h.taskSigningKey
		var encryptedKey []byte
		if err := h.pool.QueryRow(r.Context(), `SELECT task_key_ciphertext FROM connectors WHERE id=$1`, connID).Scan(&encryptedKey); err == nil && len(encryptedKey) > 0 {
			if signingKey, err = h.credentialCipher.Decrypt(encryptedKey); err != nil {
				jsonError(w, http.StatusInternalServerError, "TASK_SIGNING_FAILED", "Connector 凭证解密失败")
				return
			}
		}
		if err := tasks[i].Sign(signingKey); err != nil {
			jsonError(w, http.StatusInternalServerError, "TASK_SIGNING_FAILED", "任务签名失败")
			return
		}
		_ = h.writeAudit(r.Context(), "execution.dispatched", nil, nil, tasks[i].ExecutionID, tasks[i].TraceID)
	}
	jsonResp(w, http.StatusOK, tasks)
}

func (h *Handler) SubmitTaskResult(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var result struct {
		DispatchToken string         `json:"dispatchToken"`
		Status        string         `json:"status"`
		Result        map[string]any `json:"result"`
		Error         string         `json:"error"`
		Lease         struct {
			Provider    string    `json:"provider"`
			Fingerprint string    `json:"fingerprint"`
			ExpiresAt   time.Time `json:"expiresAt"`
		} `json:"lease"`
	}
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_BODY", "请求体解析失败")
		return
	}

	execID := r.PathValue("executionId")
	ctx := r.Context()
	exec, err := h.executions.GetByID(ctx, execID)
	if err != nil || exec == nil {
		jsonError(w, http.StatusNotFound, "EXECUTION_NOT_FOUND", "执行不存在")
		return
	}
	if exec.ConnectorID != r.PathValue("id") {
		jsonError(w, http.StatusConflict, "INVALID_EXECUTION_STATE", "执行不属于该 Connector")
		return
	}
	if strings.TrimSpace(result.DispatchToken) == "" {
		if !strings.EqualFold(strings.TrimSpace(os.Getenv("WARDEN_ENABLE_LEGACY_DISPATCH_RESULT")), "true") {
			jsonError(w, http.StatusConflict, "MISSING_DISPATCH_TOKEN", "缺少任务分发凭证")
			return
		}
		if err := h.pool.QueryRow(ctx, `SELECT dispatch_token::text FROM executions
			WHERE id=$1 AND connector_id=$2 AND status='DISPATCHED'`, execID, exec.ConnectorID).Scan(&result.DispatchToken); err != nil {
			jsonError(w, http.StatusConflict, "MISSING_DISPATCH_TOKEN", "缺少任务分发凭证")
			return
		}
	}
	result.Result = domain.SanitizeResult(result.Result)

	var terminalStatus domain.ExecutionStatus
	var errorCode, errorMessage string
	switch result.Status {
	case "SUCCEEDED":
		terminalStatus = domain.ExecSucceeded
	case "FAILED":
		terminalStatus = domain.ExecFailed
		errorCode, errorMessage = connectorExecutionError(result.Error)
	case "UNKNOWN":
		terminalStatus, errorCode, errorMessage = domain.ExecUnknown, "UNKNOWN_RESULT", domain.SanitizeErrorMessage(result.Error)
		if errorMessage == "" {
			errorMessage = "变更结果未知，已重新读取目标状态；禁止自动重试"
		}
	default:
		jsonError(w, http.StatusBadRequest, "INVALID_RESULT_STATUS", "不支持的执行结果状态")
		return
	}
	var leaseExpiry *time.Time
	if !result.Lease.ExpiresAt.IsZero() {
		leaseExpiry = &result.Lease.ExpiresAt
	}
	auditAction := "execution.completed"
	if terminalStatus != domain.ExecSucceeded {
		auditAction = "execution.failed"
	}
	request, requestErr := h.requests.GetByExecutionID(ctx, execID)
	if requestErr != nil {
		jsonError(w, http.StatusInternalServerError, "RESULT_PERSIST_FAILED", "查询关联审批请求失败")
		return
	}
	actionName := actionNameFromID(ctx, h.pool, exec.ActionID)
	if terminalStatus == domain.ExecFailed && errorCode == "DATABASE_APPROVAL_REQUIRED" && actionName == "database.query.execute" {
		if request != nil && request.Status == domain.RequestPending && exec.Status == domain.ExecWaitingApproval {
			jsonResp(w, http.StatusOK, map[string]string{"status": "accepted"})
			return
		}
		if request == nil {
			approvalRequest, changed, escalateErr := h.escalateRuntimeDatabaseQueryApproval(ctx, exec, result.DispatchToken, result.Result)
			if escalateErr != nil {
				jsonError(w, http.StatusInternalServerError, "RUNTIME_APPROVAL_ESCALATION_FAILED", "运行时查询成本审批创建失败")
				return
			}
			if !changed {
				jsonError(w, http.StatusConflict, "EXECUTION_STATE_CHANGED", "执行状态已被其他请求更新")
				return
			}
			h.notifyApprovalAsync(ctx, *approvalRequest, "access_request.created", "")
			jsonResp(w, http.StatusOK, map[string]string{"status": "accepted"})
			return
		}
	}
	auditMetadata := executionResultAuditMetadata(result.Result)
	auditMetadata["outcome"] = terminalStatus
	auditMetadata["action"] = actionName
	if auditMetadata["action"] == "provider.request" {
		if request, ok := providerRequestFromParameters(exec.Parameters); ok {
			auditMetadata["provider_capability"] = request.Capability
			auditMetadata["provider_method"] = strings.ToUpper(request.Method)
			auditMetadata["provider_path"] = request.Path
			auditMetadata["provider_query"] = domain.NormalizeProviderQuery(request.Query)
			auditMetadata["provider_status"] = result.Result["status"]
		}
	}
	if exec.CredentialID != "" {
		auditMetadata["credential_id"] = exec.CredentialID
	}
	if len(exec.ExternalContext) > 0 {
		auditMetadata["external_context"] = exec.ExternalContext
	}
	if request != nil {
		auditMetadata["access_request_id"] = request.ID
	}
	actorType := ""
	if h.principals != nil {
		if principal, principalErr := h.principals.GetByID(ctx, exec.PrincipalID); principalErr == nil && principal != nil {
			actorType = string(principal.Kind)
		}
	}
	auditSummary := auditAction
	if terminalStatus == domain.ExecUnknown {
		auditSummary = "execution.unknown"
	}
	auditEvent := &domain.AuditEvent{Action: auditAction, ActorID: exec.PrincipalID, CredentialID: exec.CredentialID,
		ActorType: actorType, ActingProjectID: exec.ActingProjectID, ResourceID: exec.ResourceID, ExecutionID: execID, GrantID: exec.GrantID,
		DecisionID: exec.DecisionID, ConnectorID: exec.ConnectorID, TraceID: exec.TraceID,
		Summary: auditSummary, Metadata: auditMetadata}
	attachExecutionAudit(auditEvent, exec)
	changed, err := h.executions.FinalizeWithLease(ctx, execID, result.DispatchToken, terminalStatus, result.Result, errorCode, errorMessage,
		result.Lease.Provider, result.Lease.Fingerprint, leaseExpiry, auditEvent)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "RESULT_PERSIST_FAILED", "执行结果保存失败")
		return
	}
	if !changed {
		current, getErr := h.executions.GetByID(ctx, execID)
		if getErr == nil && acceptsDuplicateTerminalResult(current, terminalStatus) {
			jsonResp(w, http.StatusOK, map[string]string{"status": "accepted"})
			return
		}
		jsonError(w, http.StatusConflict, "EXECUTION_STATE_CHANGED", "执行状态已被其他请求更新")
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func providerRequestFromParameters(parameters map[string]any) (domain.ProviderRequest, bool) {
	var request domain.ProviderRequest
	raw, ok := parameters["providerRequest"]
	if !ok {
		return request, false
	}
	payload, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(payload, &request) != nil {
		return domain.ProviderRequest{}, false
	}
	return request, true
}

func connectorExecutionError(raw string) (string, string) {
	message := domain.SanitizeErrorMessage(raw)
	for _, code := range []string{
		domain.ProviderErrInvalidRequest,
		domain.ProviderErrResourceNotFound,
		domain.ProviderErrResourceUnavailable,
		domain.ProviderErrIntegrationMismatch,
		domain.ProviderErrIntegrationInactive,
		domain.ProviderErrCapabilityNotFound,
		domain.ProviderErrCapabilityMismatch,
		domain.ProviderErrMethodDenied,
		domain.ProviderErrPathDenied,
		domain.ProviderErrQueryDenied,
		domain.ProviderErrHeaderDenied,
		domain.ProviderErrScopeDenied,
		domain.ProviderErrLimitExceeded,
		domain.ProviderErrEndpointUnsafe,
		domain.ProviderErrRedirectDenied,
		domain.ProviderErrResponseTooLarge,
		domain.ProviderErrTimeout,
		domain.ProviderErrUpstreamUnknown,
		"JENKINS_JOB_CONFLICT",
		"JENKINS_JOB_NOT_FOUND",
		"JENKINS_JOB_CONFIRMATION_MISMATCH",
		"RESOURCE_VERSION_CONFLICT",
		"PREVIEW_CONFLICT",
		"DATABASE_APPROVAL_REQUIRED",
		"DATABASE_COST_LIMIT_EXCEEDED",
	} {
		if message == code || strings.HasPrefix(message, code+":") {
			return code, message
		}
	}
	return "EXECUTION_ERROR", message
}

func acceptsDuplicateTerminalResult(current *domain.Execution, submitted domain.ExecutionStatus) bool {
	return current != nil && current.Status == submitted
}

func executionResultAuditMetadata(result map[string]any) map[string]any {
	metadata := map[string]any{}
	safety, ok := result["_warden_safety"].(map[string]any)
	if !ok {
		return metadata
	}
	for _, key := range []string{"contractVersion", "verification", "rollbackMode", "unknownResolutionMode", "unknownResolution"} {
		if value, exists := safety[key]; exists {
			metadata[key] = value
		}
	}
	if before, exists := safety["before"]; exists {
		metadata["before_summary"] = auditSnapshotSummary(before)
	}
	if after, exists := safety["after"]; exists {
		metadata["after_summary"] = auditSnapshotSummary(after)
	}
	return metadata
}

func auditSnapshotSummary(value any) map[string]any {
	snapshot, ok := value.(map[string]any)
	if !ok {
		return map[string]any{"invalid": true}
	}
	summary := map[string]any{}
	for _, key := range []string{"present", "externalId", "name", "namespace", "resourceType", "integrationId", "stateHash", "observedAt"} {
		if item, exists := snapshot[key]; exists {
			summary[key] = item
		}
	}
	return summary
}

type syncResourceItem struct {
	ExternalID    string         `json:"externalId"`
	Name          string         `json:"name"`
	Namespace     string         `json:"namespace"`
	ResourceType  string         `json:"resourceType"`
	IntegrationID string         `json:"integrationId"`
	Labels        map[string]any `json:"labels"`
	Annotations   map[string]any `json:"annotations"`
	Metadata      map[string]any `json:"metadata"`
}

func (h *Handler) SyncResources(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var req struct {
		ConnectorID string `json:"connectorId"`
		SyncID      string `json:"syncId"`
		Complete    *bool  `json:"complete"`
		Groups      []struct {
			IntegrationID string             `json:"integrationId"`
			Complete      bool               `json:"complete"`
			Resources     []syncResourceItem `json:"resources"`
		} `json:"groups"`
		Resources []syncResourceItem `json:"resources"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	if req.ConnectorID == "" || req.SyncID == "" {
		jsonError(w, http.StatusBadRequest, "MISSING_FIELDS", "connectorId 为必填")
		return
	}

	ctx := r.Context()
	connector, err := h.connectors.GetByID(ctx, req.ConnectorID)
	if err != nil || connector == nil {
		jsonError(w, http.StatusNotFound, "CONNECTOR_NOT_FOUND", "Connector 不存在")
		return
	}
	// Require a live heartbeat (within 5 minutes) before accepting sync data,
	// so a stale or hijacked connector id cannot poison the catalog.
	if connector.Status != domain.ConnectorActive {
		jsonError(w, http.StatusConflict, "CONNECTOR_NOT_ACTIVE", "Connector 未激活")
		return
	}
	if connector.LastHeartbeat == nil || time.Since(*connector.LastHeartbeat) > 5*time.Minute {
		jsonError(w, http.StatusConflict, "CONNECTOR_HEARTBEAT_STALE", "Connector 心跳超时")
		return
	}

	// Normalize the payload into per-integration batches. The grouped form is
	// produced by the shared MultiRunner; the flat form is kept for connectors
	// without a MultiRunner (e.g. Linux). Flat syncs orphan connector-wide.
	type syncBatch struct {
		integrationID string
		complete      bool
		resources     []syncResourceItem
	}
	grouped := len(req.Groups) > 0
	var batches []syncBatch
	if grouped {
		batches = make([]syncBatch, 0, len(req.Groups))
		for _, group := range req.Groups {
			batches = append(batches, syncBatch{
				integrationID: strings.TrimSpace(group.IntegrationID),
				complete:      group.Complete,
				resources:     group.Resources,
			})
		}
	} else {
		complete := true
		if req.Complete != nil {
			complete = *req.Complete
		}
		batches = []syncBatch{{integrationID: "", complete: complete, resources: req.Resources}}
	}

	validateIntegration := func(integrationID string) bool {
		integration, err := h.integrations.GetByID(ctx, integrationID)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "校验 Integration 归属失败")
			return false
		}
		if integration == nil || integration.ConnectorID != req.ConnectorID || integration.Status != domain.IntegrationActive {
			jsonError(w, http.StatusBadRequest, "INVALID_INTEGRATION_REFERENCE", "资源引用的 Integration 不属于当前执行通道或未启用")
			return false
		}
		if integration.TenantID != connector.TenantID {
			jsonError(w, http.StatusBadRequest, "INVALID_INTEGRATION_REFERENCE", "Integration 租户归属无效")
			return false
		}
		return true
	}
	validatedIntegrations := make(map[string]bool)
	for i := range batches {
		if grouped {
			for j := range batches[i].resources {
				batches[i].resources[j].IntegrationID = batches[i].integrationID
			}
		}
		for j := range batches[i].resources {
			integrationID := strings.TrimSpace(batches[i].resources[j].IntegrationID)
			batches[i].resources[j].IntegrationID = integrationID
			if integrationID == "" || validatedIntegrations[integrationID] {
				continue
			}
			if !validateIntegration(integrationID) {
				return
			}
			validatedIntegrations[integrationID] = true
		}
		// An empty integration still carries a group; validate its id too so a
		// connector cannot orphan another integration's resources.
		if grouped && batches[i].integrationID != "" && !validatedIntegrations[batches[i].integrationID] {
			if !validateIntegration(batches[i].integrationID) {
				return
			}
			validatedIntegrations[batches[i].integrationID] = true
		}
	}

	synced := 0
	total := 0
	for i := range batches {
		for _, item := range batches[i].resources {
			total++
			if item.Name == "" || item.ResourceType == "" {
				continue
			}
			res := &domain.Resource{
				ResourceType:   item.ResourceType,
				ExternalID:     item.ExternalID,
				Name:           item.Name,
				Namespace:      item.Namespace,
				ConnectorID:    req.ConnectorID,
				IntegrationID:  item.IntegrationID,
				Environment:    connector.Environment,
				Status:         domain.ResourceActive,
				Labels:         item.Labels,
				Annotations:    item.Annotations,
				Metadata:       item.Metadata,
				TenantID:       connector.TenantID,
				SyncGeneration: req.SyncID,
			}
			if res.Labels == nil {
				res.Labels = map[string]any{}
			}
			if res.Annotations == nil {
				res.Annotations = map[string]any{}
			}
			if res.Metadata == nil {
				res.Metadata = map[string]any{}
			}
			if err := h.resources.UpsertFromSync(ctx, res); err != nil {
				log.Printf("资源同步失败 (connector=%s resource=%s/%s): %v", req.ConnectorID, item.Namespace, item.Name, err)
				continue
			}
			synced++
		}
	}
	// Orphan stale resources per batch, but only for complete batches. A partial
	// discovery pass must not delete resources it merely failed to observe.
	for _, batch := range batches {
		if !batch.complete {
			continue
		}
		if grouped {
			if err := h.resources.MarkMissingFromSyncIntegration(ctx, req.ConnectorID, batch.integrationID, req.SyncID); err != nil {
				jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新失效资源失败")
				return
			}
		} else {
			if err := h.resources.MarkMissingFromSync(ctx, req.ConnectorID, req.SyncID); err != nil {
				jsonError(w, http.StatusInternalServerError, "DB_ERROR", "资源同步失败，已保留上一版资源状态")
				return
			}
		}
	}
	if purged, err := h.resources.PurgeExpiredHistory(ctx, req.ConnectorID); err != nil {
		log.Printf("清理过期资源历史失败 (connector=%s): %v", req.ConnectorID, err)
	} else if purged > 0 {
		log.Printf("已清理 %d 条过期资源历史 (connector=%s)", purged, req.ConnectorID)
	}

	auditEvent := &domain.AuditEvent{
		Action:      "connector.heartbeat",
		ConnectorID: req.ConnectorID,
		Summary:     fmt.Sprintf("synced %d resources", synced),
		Metadata: map[string]any{
			"synced": synced, "total": total, "duration_ms": time.Since(startedAt).Milliseconds(),
		},
	}
	if err := h.audit.Create(ctx, auditEvent); err != nil {
		log.Printf("写入资源同步审计事件失败: %v", err)
	}
	log.Printf("资源批量同步完成 (connector=%s synced=%d total=%d duration=%s)",
		req.ConnectorID, synced, total, time.Since(startedAt))
	jsonResp(w, http.StatusOK, map[string]any{
		"synced": synced, "total": total,
		"durationMs": time.Since(startedAt).Milliseconds(),
	})
}

// --- Access Requests ---

func (h *Handler) ListAccessRequests(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	statusFilter := r.URL.Query().Get("status")
	limit, offset := listLimitOffset(r, 50)
	requests, err := h.requests.List(r.Context(), statusFilter, limit, offset)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询审批请求失败")
		return
	}
	if requests == nil {
		requests = []domain.AccessRequest{}
	}
	h.enrichAccessRequests(r.Context(), requests)
	jsonResp(w, http.StatusOK, requests)
}

// ListMyAccessRequests is the initiator-facing view: any authenticated member
// can track the requests they raised, without holding the approve capability.
func (h *Handler) ListMyAccessRequests(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	identity := identityFromContext(r.Context())
	if identity == nil || identity.Principal == nil {
		jsonError(w, http.StatusUnauthorized, "UNAUTHORIZED", "未认证")
		return
	}
	limit, offset := listLimitOffset(r, 50)
	requests, err := h.requests.ListByPrincipal(r.Context(), identity.Principal.ID, r.URL.Query().Get("status"), limit, offset)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询我的申请失败")
		return
	}
	if requests == nil {
		requests = []domain.AccessRequest{}
	}
	h.enrichAccessRequests(r.Context(), requests)
	jsonResp(w, http.StatusOK, requests)
}

// enrichAccessRequests fills display names, resource state, action metadata and
// policy assessment context shared by the approver queue and the member view.
func (h *Handler) enrichAccessRequests(ctx context.Context, requests []domain.AccessRequest) {
	// Enrich with principal names so the UI shows "Warden ReadOnly" instead of UUID prefix.
	if h.principals != nil {
		pSeen := make(map[string]bool, len(requests))
		pIDs := make([]string, 0, len(requests))
		for _, req := range requests {
			if req.PrincipalID != "" && !pSeen[req.PrincipalID] {
				pSeen[req.PrincipalID] = true
				pIDs = append(pIDs, req.PrincipalID)
			}
		}
		if pMap, err := h.principals.SummariesByIDs(ctx, pIDs); err == nil {
			for i := range requests {
				if p, ok := pMap[requests[i].PrincipalID]; ok {
					requests[i].PrincipalName = p.Name
				}
			}
		}
	}
	// Enrich with resource display info so approvers see resource name and
	// current status without downloading the full resource catalog.
	if h.resources != nil {
		resSeen := make(map[string]bool, len(requests))
		resIDs := make([]string, 0, len(requests))
		for _, req := range requests {
			if req.ResourceID != "" && !resSeen[req.ResourceID] {
				resSeen[req.ResourceID] = true
				resIDs = append(resIDs, req.ResourceID)
			}
		}
		if rMap, err := h.resources.SummariesByIDs(ctx, resIDs); err == nil {
			for i := range requests {
				if s, ok := rMap[requests[i].ResourceID]; ok {
					requests[i].ResourceName = s.Name
					requests[i].ResourceType = s.ResourceType
					requests[i].ResourceState = s.Status
				}
			}
		}
	}
	if h.actions != nil {
		actionSeen := make(map[string]bool, len(requests))
		actionIDs := make([]string, 0, len(requests))
		for _, request := range requests {
			if request.ActionID != "" && !actionSeen[request.ActionID] {
				actionSeen[request.ActionID] = true
				actionIDs = append(actionIDs, request.ActionID)
			}
		}
		if actionMap, err := h.actions.SummariesByIDs(ctx, actionIDs); err == nil {
			for i := range requests {
				if action, ok := actionMap[requests[i].ActionID]; ok {
					requests[i].ActionName = action.Name
					requests[i].ActionDisplay = action.DisplayName
					requests[i].ActionRisk = action.RiskLevel
				}
			}
		}
	}
	if h.policyDecisions != nil {
		decisionSeen := make(map[string]bool, len(requests))
		decisionIDs := make([]string, 0, len(requests))
		for _, request := range requests {
			if request.DecisionID != "" && !decisionSeen[request.DecisionID] {
				decisionSeen[request.DecisionID] = true
				decisionIDs = append(decisionIDs, request.DecisionID)
			}
		}
		if outputs, err := h.policyDecisions.OutputsByIDs(ctx, decisionIDs); err == nil {
			for i := range requests {
				if output, ok := outputs[requests[i].DecisionID]; ok {
					if assessment, ok := output["database_query_cost_assessment"].(map[string]any); ok {
						requests[i].ApprovalContext = assessment
					}
				}
			}
		}
	}
}

func (h *Handler) ApproveRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actor := identityFromContext(r.Context())
	req, err := h.decideApproval(r.Context(), id, true, actor)
	if err != nil {
		status, code, message := approvalDecisionHTTPError(err)
		jsonError(w, status, code, message)
		return
	}
	h.notifyApprovalAsync(r.Context(), *req, "access_request.approved", actor.Principal.Name)
	jsonResp(w, http.StatusOK, map[string]string{"id": id, "status": "APPROVED"})
}

func (h *Handler) RejectRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actor := identityFromContext(r.Context())
	req, err := h.decideApproval(r.Context(), id, false, actor)
	if err != nil {
		status, code, message := approvalDecisionHTTPError(err)
		jsonError(w, status, code, message)
		return
	}
	h.notifyApprovalAsync(r.Context(), *req, "access_request.rejected", actor.Principal.Name)
	jsonResp(w, http.StatusOK, map[string]string{"id": id, "status": "REJECTED"})
}

// --- Audit ---

func (h *Handler) ListAuditEvents(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	actionFilter := r.URL.Query().Get("action")
	auditFilter := postgres.AuditListFilter{
		Action: actionFilter, Category: r.URL.Query().Get("category"),
		ExecutionID: r.URL.Query().Get("executionId"), TraceID: r.URL.Query().Get("traceId"),
		ConnectorID: r.URL.Query().Get("connectorId"), Query: strings.TrimSpace(r.URL.Query().Get("q")),
		ExternalSource:         strings.TrimSpace(r.URL.Query().Get("externalSource")),
		ExternalWorkspaceID:    strings.TrimSpace(r.URL.Query().Get("workspaceId")),
		ExternalProjectID:      strings.TrimSpace(r.URL.Query().Get("externalProjectId")),
		ExternalAgentID:        strings.TrimSpace(r.URL.Query().Get("agentId")),
		ExternalTaskID:         strings.TrimSpace(r.URL.Query().Get("taskId")),
		ExternalIssueID:        strings.TrimSpace(r.URL.Query().Get("issueId")),
		OriginatorUserID:       strings.TrimSpace(r.URL.Query().Get("originatorUserId")),
		DelegationTokenID:      strings.TrimSpace(r.URL.Query().Get("delegationTokenId")),
		OnBehalfOfPrincipalID:  strings.TrimSpace(r.URL.Query().Get("onBehalfOfPrincipalId")),
		AccountablePrincipalID: strings.TrimSpace(r.URL.Query().Get("accountablePrincipalId")),
	}
	limit, offset := 500, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	events, err := h.audit.List(r.Context(), auditFilter, limit, offset)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询审计事件失败")
		return
	}
	if events == nil {
		events = []domain.AuditEvent{}
	}
	jsonResp(w, http.StatusOK, events)
}

// --- Helpers ---

func executionAuditEvent(action string, identity *AuthenticatedIdentity, principal *domain.Principal, resource *domain.Resource, executionID, traceID string) *domain.AuditEvent {
	e := &domain.AuditEvent{Action: action, ExecutionID: executionID, TraceID: traceID, Summary: action, Metadata: map[string]any{}}
	if principal != nil {
		e.ActorID, e.ActorType = principal.ID, string(principal.Kind)
	}
	if resource != nil {
		e.ResourceID = resource.ID
	}
	if identity != nil {
		if principal == nil && identity.Principal != nil {
			e.ActorID, e.ActorType = identity.Principal.ID, string(identity.Principal.Kind)
		}
		e.CredentialID = identity.CredentialID
		for key, value := range identity.AuditMetadata {
			e.Metadata[key] = value
		}
		if identity.SessionID != "" {
			e.Metadata["session_id"] = identity.SessionID
		}
		attachDelegationAudit(e, identity.DelegationTokenID, identity.OnBehalfOfPrincipalID, identity.AccountablePrincipalID)
	}
	return e
}

func (h *Handler) writeAudit(ctx context.Context, action string, principal *domain.Principal, resource *domain.Resource, executionID, traceID string) error {
	e := &domain.AuditEvent{
		Action:      action,
		ExecutionID: executionID,
		TraceID:     traceID,
		Summary:     action,
		Metadata:    map[string]any{},
	}
	if principal != nil {
		e.ActorID = principal.ID
		e.ActorType = string(principal.Kind)
	}
	if identity := identityFromContext(ctx); identity != nil {
		e.CredentialID = identity.CredentialID
		if e.ActorID == "" && identity.Principal != nil {
			e.ActorID = identity.Principal.ID
			e.ActorType = string(identity.Principal.Kind)
		}
		for key, value := range identity.AuditMetadata {
			e.Metadata[key] = value
		}
		if identity.SessionID != "" {
			e.Metadata["session_id"] = identity.SessionID
		}
		attachDelegationAudit(e, identity.DelegationTokenID, identity.OnBehalfOfPrincipalID, identity.AccountablePrincipalID)
	}
	if executionID != "" && h.executions != nil {
		// Lifecycle callbacks may be authenticated as the Connector rather than
		// the original caller. Recover immutable execution attribution without
		// replacing an explicitly supplied audit actor.
		if execution, err := h.executions.GetByID(ctx, executionID); err == nil && execution != nil {
			e.ActingProjectID = execution.ActingProjectID
			e.ResourceID = execution.ResourceID
			e.GrantID = execution.GrantID
			e.DecisionID = execution.DecisionID
			e.ConnectorID = execution.ConnectorID
			if e.CredentialID == "" {
				e.CredentialID = execution.CredentialID
			}
			attachExecutionAudit(e, execution)
			if e.ActorID == "" {
				e.ActorID = execution.PrincipalID
				if h.principals != nil {
					if actor, err := h.principals.GetByID(ctx, execution.PrincipalID); err == nil && actor != nil {
						e.ActorType = string(actor.Kind)
					}
				}
			}
		}
	}
	if resource != nil {
		e.ResourceID = resource.ID
		e.TargetID = resource.Name
	}
	if err := h.audit.Create(ctx, e); err != nil {
		log.Printf("写入审计事件失败: %v", err)
		return err
	}
	return nil
}

// --- Approval Rules ---

func (h *Handler) ListApprovalRules(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	rules, err := h.approvalRules.List(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询审批规则失败")
		return
	}
	result := make([]any, len(rules))
	for i, rule := range rules {
		result[i] = map[string]any{
			"id":                rule.ID,
			"risk_level":        string(rule.RiskLevel),
			"environment":       rule.Environment,
			"requires_approval": rule.RequiresApproval,
			"priority":          rule.Priority,
			"description":       rule.Description,
			"created_at":        rule.CreatedAt,
			"updated_at":        rule.UpdatedAt,
		}
	}
	jsonResp(w, http.StatusOK, result)
}

func (h *Handler) UpdateApprovalRule(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var body struct {
		RequiresApproval *bool   `json:"requires_approval"`
		Priority         *int    `json:"priority"`
		Description      *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	id := r.PathValue("id")
	rules, err := h.approvalRules.List(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询审批规则失败")
		return
	}
	var rule *domain.ApprovalRule
	for i := range rules {
		if rules[i].ID == id {
			rule = &rules[i]
			break
		}
	}
	if rule == nil {
		jsonError(w, http.StatusNotFound, "NOT_FOUND", "审批规则不存在")
		return
	}
	if body.RequiresApproval != nil {
		rule.RequiresApproval = *body.RequiresApproval
	}
	if body.Priority != nil {
		rule.Priority = *body.Priority
	}
	if body.Description != nil {
		rule.Description = *body.Description
	}
	if err := h.approvalRules.Upsert(r.Context(), *rule); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新审批规则失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{
		"id":                rule.ID,
		"risk_level":        string(rule.RiskLevel),
		"environment":       rule.Environment,
		"requires_approval": rule.RequiresApproval,
		"priority":          rule.Priority,
		"description":       rule.Description,
	})
}

func executionToResp(e *domain.Execution) map[string]any {
	resp := map[string]any{
		"id":                     e.ID,
		"status":                 e.Status,
		"principalId":            e.PrincipalID,
		"principalName":          e.PrincipalName,
		"principalKind":          e.PrincipalKind,
		"actingProjectId":        e.ActingProjectID,
		"externalContext":        e.ExternalContext,
		"delegationTokenId":      e.DelegationTokenID,
		"onBehalfOfPrincipalId":  e.OnBehalfOfPrincipalID,
		"accountablePrincipalId": e.AccountablePrincipalID,
		"credentialId":           e.CredentialID,
		"credentialName":         e.CredentialName,
		"actionId":               e.ActionID,
		"actionName":             e.ActionName,
		"actionDisplayName":      e.ActionDisplay,
		"riskLevel":              e.ActionRisk,
		"resourceId":             e.ResourceID,
		"resourceName":           e.ResourceName,
		"resourceType":           e.ResourceType,
		"traceId":                e.TraceID,
		"errorCode":              e.ErrorCode,
		"errorMessage":           domain.SanitizeErrorMessage(e.ErrorMessage),
		"result":                 e.Result,
		"createdAt":              e.CreatedAt,
		"updatedAt":              e.UpdatedAt,
	}
	if e.TenantID != "" {
		resp["tenantId"] = e.TenantID
	}
	return resp
}

func jsonResp(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, status int, code, message string) {
	jsonResp(w, status, map[string]string{"error": code, "message": message})
}

func databaseUnavailable(w http.ResponseWriter) {
	jsonError(w, http.StatusServiceUnavailable, "NO_DATABASE", "数据库未连接")
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func validateResourceParameters(parameters map[string]any, resource *domain.Resource) error {
	if value, exists := parameters["name"]; exists {
		name, ok := value.(string)
		if !ok || name != resource.Name {
			return fmt.Errorf("参数 name 必须与授权资源一致")
		}
	}
	if value, exists := parameters["namespace"]; exists {
		namespace, ok := value.(string)
		if !ok || namespace != resource.Namespace {
			return fmt.Errorf("参数 namespace 必须与授权资源一致")
		}
	}
	return nil
}

func validateJenkinsResourceParameters(action string, parameters map[string]any, resource *domain.Resource) error {
	if !strings.HasPrefix(action, "jenkins.job.") || action == "jenkins.job.create" {
		return nil
	}
	if resource.ResourceType != "jenkins_job" {
		return fmt.Errorf("Jenkins Job 动作必须绑定 Jenkins Job 资源")
	}
	for _, key := range []string{"name", "job", "jobName", "fullName"} {
		value, exists := parameters[key]
		if !exists {
			continue
		}
		name, ok := value.(string)
		if !ok || strings.TrimSpace(name) != resource.Name {
			return fmt.Errorf("参数 %s 必须与授权 Jenkins Job 一致", key)
		}
	}
	if action == "jenkins.job.delete" {
		confirmation, ok := parameters["confirmJobName"].(string)
		if !ok || strings.TrimSpace(confirmation) != resource.Name {
			return fmt.Errorf("删除确认必须与授权 Jenkins Job 完整名称一致")
		}
	}
	return nil
}

var controlledGenericKubernetesKinds = map[string]string{
	"v1/ConfigMap": "configmap", "v1/Service": "service",
	"apps/v1/Deployment": "deployment", "apps/v1/StatefulSet": "statefulset", "apps/v1/DaemonSet": "daemonset",
	"batch/v1/Job": "job", "batch/v1/CronJob": "cronjob",
	"networking.k8s.io/v1/Ingress": "ingress", "networking.k8s.io/v1/NetworkPolicy": "networkpolicy",
	"policy/v1/PodDisruptionBudget": "poddisruptionbudget",
}

func validateGenericKubernetesParameters(parameters map[string]any, resource *domain.Resource) error {
	apiVersion, apiOK := parameters["apiVersion"].(string)
	kind, kindOK := parameters["kind"].(string)
	resourceType, allowed := controlledGenericKubernetesKinds[strings.TrimSpace(apiVersion)+"/"+strings.TrimSpace(kind)]
	if !apiOK || !kindOK || !allowed {
		return fmt.Errorf("Kubernetes GVK 不在通用资源白名单中")
	}
	if resource.ResourceType != "namespace" && resource.ResourceType != resourceType {
		return fmt.Errorf("Kubernetes GVK 与授权资源类型不一致")
	}
	manifest, exists := parameters["manifest"]
	if !exists {
		return nil
	}
	payload, err := json.Marshal(manifest)
	if err != nil || len(payload) > 1<<20 {
		return fmt.Errorf("Kubernetes Manifest 必须是有效对象且不超过 1 MiB")
	}
	if path, found := genericSensitiveParameterPath(manifest, nil); found {
		return fmt.Errorf("Kubernetes Manifest 包含禁止的敏感字段 %s", strings.Join(path, "."))
	}
	return nil
}

func genericSensitiveParameterPath(value any, path []string) ([]string, bool) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
			if normalized == "stringdata" || strings.Contains(normalized, "password") || strings.Contains(normalized, "token") || strings.Contains(normalized, "privatekey") || strings.Contains(normalized, "clientsecret") || normalized == "authorization" {
				return append(path, key), true
			}
			if foundPath, found := genericSensitiveParameterPath(child, append(path, key)); found {
				return foundPath, true
			}
		}
	case []any:
		for _, child := range current {
			if foundPath, found := genericSensitiveParameterPath(child, path); found {
				return foundPath, true
			}
		}
	case string:
		upper := strings.ToUpper(current)
		if strings.Contains(upper, "-----BEGIN") && strings.Contains(upper, "PRIVATE KEY-----") {
			return path, true
		}
	}
	return nil, false
}

func toMap(value any) map[string]any {
	payload, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"serialization_error": true}
	}
	result := map[string]any{}
	if err := json.Unmarshal(payload, &result); err != nil {
		return map[string]any{"serialization_error": true}
	}
	return result
}

func actionNameFromID(ctx context.Context, pool *pgxpool.Pool, id string) string {
	var name string
	_ = pool.QueryRow(ctx, `SELECT name FROM action_definitions WHERE id = $1`, id).Scan(&name)
	return name
}
