package connector

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

// rejectMetadataEndpoint prevents accidentally forwarding integration
// credentials to cloud instance metadata services. Private RFC1918 targets
// remain supported because connectors are explicitly intended for internal
// systems; deployments should additionally enforce an egress allowlist.
func rejectMetadataEndpoint(endpoint *url.URL) error {
	host := endpoint.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLinkLocalUnicast() || ip.String() == "100.100.100.200" {
			return fmt.Errorf("integration endpoint targets a metadata or link-local address")
		}
	}
	if strings.EqualFold(host, "metadata.google.internal") || strings.EqualFold(host, "metadata") {
		return fmt.Errorf("integration endpoint targets a metadata service")
	}
	return nil
}

// CredentialResolver returns the API token for an integration given its
// credential reference (a K8s Secret name on the cluster connector, or a file
// name on the Linux connector). The integrationID is passed through so the
// control plane can enforce a "pam:" credential's binding scope.
type CredentialResolver func(ctx context.Context, credentialRef, integrationID string) (string, error)

// NewIntegrationReconciler returns a callback suitable for
// Runtime.SetIntegrationReconciler. It diffs the current set of integrations
// (Jenkins, Grafana, ...) against the ones already materialized on the
// MultiRunner and adds/removes runners so each integration routes by its id.
//
// active tracks the integration ids that currently have a runner, so removed
// or DISABLED integrations are torn down. The map is shared across protocols
// because integration ids are globally unique.
func NewIntegrationReconciler(runner *MultiRunner, resolve CredentialResolver, report func(ctx context.Context, integrationID, message string)) func(ctx context.Context, integrations []domain.Integration) error {
	// active tracks the UpdatedAt timestamp of each materialized integration so
	// changes to scope/endpoint/credential are detected and the runner rebuilt.
	active := make(map[string]time.Time)
	// lastHealth dedupes probe reports so an unchanged result is not re-posted
	// on every 10s reconcile tick.
	lastHealth := make(map[string]string)
	reportHealth := func(ctx context.Context, id, message string) {
		if report == nil {
			return
		}
		if prev, ok := lastHealth[id]; ok && prev == message {
			return
		}
		lastHealth[id] = message
		report(ctx, id, message)
	}
	return func(ctx context.Context, integrations []domain.Integration) error {
		seen := make(map[string]struct{}, len(integrations))
		for i := range integrations {
			ig := integrations[i]
			resourceTypes := integrationResourceTypes(ig.Protocol)
			if resourceTypes == nil {
				continue
			}
			seen[ig.ID] = struct{}{}
			if ig.Status != domain.IntegrationActive {
				// Tear down runner if the integration was previously active.
				if _, exists := active[ig.ID]; exists {
					runner.SetIntegration(ig.ID, nil)
					runner.SetIntegrationProvider(ig.ID, nil)
					delete(active, ig.ID)
				}
				continue
			}
			// Skip if already materialized and unchanged.
			if prev, exists := active[ig.ID]; exists && prev.Equal(ig.UpdatedAt) {
				continue
			}
			// Remove old runner if it exists (scope/endpoint/credential may have changed).
			if _, exists := active[ig.ID]; exists {
				runner.SetIntegration(ig.ID, nil)
				runner.SetIntegrationProvider(ig.ID, nil)
			}
			token, err := resolve(ctx, ig.CredentialRef, ig.ID)
			if err != nil {
				fmt.Printf("integration %s: read credential %s: %v\n", ig.ID, ig.CredentialRef, err)
				reportHealth(ctx, ig.ID, fmt.Sprintf("读取凭据 %s 失败: %v", ig.CredentialRef, err))
				continue
			}
			if err := materializeIntegration(ctx, runner, ig, strings.TrimSpace(token), resolve, resourceTypes); err != nil {
				fmt.Printf("integration %s: build runner: %v\n", ig.ID, err)
				reportHealth(ctx, ig.ID, fmt.Sprintf("构建适配器失败: %v", err))
				continue
			}
			reportHealth(ctx, ig.ID, "")
			active[ig.ID] = ig.UpdatedAt
		}
		// Tear down runners for integrations that are no longer present.
		for id := range active {
			if _, ok := seen[id]; ok {
				continue
			}
			runner.SetIntegration(id, nil)
			runner.SetIntegrationProvider(id, nil)
			delete(active, id)
		}
		return nil
	}
}

// integrationResourceTypes returns the resource types a protocol owns, or nil
// if the protocol is not an integration adapter.
func integrationResourceTypes(protocol string) []string {
	switch protocol {
	case "jenkins":
		return JenkinsResourceTypes
	case "grafana":
		return GrafanaResourceTypes
	case "postgresql", "mysql":
		return DatabaseResourceTypes
	case "huaweicloud":
		return HuaweiCloudResourceTypes
	case "aliyun_sls":
		return AliyunSLSResourceTypes
	case "h3c_comware_ssh":
		return H3CResourceTypes
	default:
		return nil
	}
}

// materializeIntegration constructs the protocol-specific runner and registers
// it on the shared MultiRunner under the integration id.
func materializeIntegration(ctx context.Context, runner *MultiRunner, ig domain.Integration, token string, resolve CredentialResolver, resourceTypes []string) error {
	switch ig.Protocol {
	case "jenkins":
		jenkinsRunner, err := NewJenkinsRunner(JenkinsConfig{
			BaseURL: ig.Endpoint, Username: ig.Username, APIToken: token,
			AllowedJobs: stringSliceFromScopeKey(ig.Scope, "allowedJobs"), AllowAll: boolFromScopeKey(ig.Scope, "allowAll"), IntegrationID: ig.ID,
		})
		if err != nil {
			return err
		}
		runner.SetIntegration(ig.ID, jenkinsRunner, resourceTypes...)
		provider, err := NewProviderExecutor(ProviderExecutorConfig{Integration: ig, Credential: token, HTTPClient: jenkinsRunner.http})
		if err != nil {
			return err
		}
		runner.SetIntegrationProvider(ig.ID, provider)
		return nil
	case "grafana":
		grafanaRunner, err := NewGrafanaRunner(GrafanaConfig{
			BaseURL: ig.Endpoint, APIToken: token,
			AllowedDashboards: stringSliceFromScopeKey(ig.Scope, "allowedDashboards"), AllowAll: boolFromScopeKey(ig.Scope, "allowAll"), IntegrationID: ig.ID,
		})
		if err != nil {
			return err
		}
		runner.SetIntegration(ig.ID, grafanaRunner, resourceTypes...)
		provider, err := NewProviderExecutor(ProviderExecutorConfig{Integration: ig, Credential: token, HTTPClient: grafanaRunner.http})
		if err != nil {
			return err
		}
		runner.SetIntegrationProvider(ig.ID, provider)
		return nil
	case "postgresql", "mysql":
		profiles := make(map[string]DatabaseCredentialProfile, len(ig.CredentialProfiles))
		for _, profile := range ig.CredentialProfiles {
			profileToken, err := resolve(ctx, profile.CredentialRef, ig.ID)
			if err != nil {
				return fmt.Errorf("integration %s: read credential profile %s: %w", ig.ID, profile.Name, err)
			}
			profiles[profile.Name] = DatabaseCredentialProfile{Username: profile.Username, Password: strings.TrimSpace(profileToken)}
		}
		databaseRunner, err := NewDatabaseRunner(DatabaseConfig{
			Protocol: ig.Protocol, Endpoint: ig.Endpoint, Username: ig.Username, Password: token,
			AllowedSchemas: stringSliceFromScopeKey(ig.Scope, "allowedSchemas"), AllowAll: boolFromScopeKey(ig.Scope, "allowAll"), IntegrationID: ig.ID,
			CredentialProfiles:       profiles,
			AllowCreateSchemas:       boolFromScopeKey(ig.Scope, "allowCreateSchemas"),
			CreateSchemaNamePrefixes: stringSliceFromScopeKey(ig.Scope, "createSchemaNamePrefixes"),
			AllowedCharsets:          stringSliceFromScopeKey(ig.Scope, "allowedCharsets"),
			AllowedCollations:        stringSliceFromScopeKey(ig.Scope, "allowedCollations"),
		})
		if err != nil {
			return err
		}
		runner.SetIntegration(ig.ID, databaseRunner, resourceTypes...)
		return nil
	case "huaweicloud":
		accessKey, secretKey, err := ParseHuaweiCloudCredential(token)
		if err != nil {
			return err
		}
		regionMode, regions, deniedRegions := huaweiRegionPolicyFromScope(ig.Scope)
		huaweiRunner, err := NewHuaweiCloudRunner(HuaweiCloudConfig{
			Region: ig.Endpoint, AccessKey: accessKey, SecretKey: secretKey,
			RegionMode: regionMode, Regions: regions, DeniedRegions: deniedRegions,
			AllowedServices: stringSliceFromScopeKey(ig.Scope, "allowedServices"), AllowAll: boolFromScopeKey(ig.Scope, "allowAll"), IntegrationID: ig.ID,
		})
		if err != nil {
			return err
		}
		runner.SetIntegration(ig.ID, huaweiRunner, resourceTypes...)
		provider, err := NewProviderExecutor(ProviderExecutorConfig{Integration: ig, Credential: token, HTTPClient: huaweiRunner.http,
			ResolveTarget: huaweiRunner.providerTarget, Authenticate: huaweiRunner.authenticateProviderRequest})
		if err != nil {
			return err
		}
		runner.SetIntegrationProvider(ig.ID, provider)
		return nil
	case "aliyun_sls":
		accessKey, secretKey, err := ParseAliyunSLSCredential(token)
		if err != nil {
			return err
		}
		regionMode, regions, deniedRegions := huaweiRegionPolicyFromScope(ig.Scope)
		slsRunner, err := NewAliyunSLSRunner(AliyunSLSConfig{
			Endpoint: ig.Endpoint, AccessKey: accessKey, SecretKey: secretKey, IntegrationID: ig.ID,
			RegionMode: regionMode, Regions: regions, DeniedRegions: deniedRegions,
			AllowedProjects: stringSliceFromScopeKey(ig.Scope, "allowedProjects"), AllowAll: boolFromScopeKey(ig.Scope, "allowAll"),
		})
		if err != nil {
			return err
		}
		runner.SetIntegration(ig.ID, slsRunner, resourceTypes...)
		return nil
	case "h3c_comware_ssh":
		legacyRSA, _ := ig.Scope["legacyRsa"].(bool)
		deviceRole, _ := ig.Scope["deviceRole"].(string)
		deviceName := ig.Name
		if value, ok := ig.Scope["deviceName"].(string); ok && strings.TrimSpace(value) != "" {
			deviceName = value
		}
		fingerprint, _ := ig.Scope["hostKeySHA256"].(string)
		networkRunner, err := NewH3CRunner(H3CConfig{
			Endpoint: ig.Endpoint, Username: ig.Username, PrivateKey: token,
			HostKeySHA256: fingerprint, LegacyRSA: legacyRSA, DeviceName: deviceName,
			DeviceRole: deviceRole, IntegrationID: ig.ID,
			AllowInterfaceChanges: boolFromScopeKey(ig.Scope, "allowInterfaceChanges"),
			ProtectedInterfaces:   stringSliceFromScopeKey(ig.Scope, "protectedInterfaces"),
		})
		if err != nil {
			return err
		}
		runner.SetIntegration(ig.ID, networkRunner, resourceTypes...)
		return nil
	default:
		return fmt.Errorf("unsupported integration protocol %q", ig.Protocol)
	}
}

func huaweiRegionPolicyFromScope(scope map[string]any) (string, []string, []string) {
	policy, _ := scope["regionPolicy"].(map[string]any)
	if policy == nil {
		return "single", nil, nil
	}
	mode, _ := policy["mode"].(string)
	return mode, anyStringSlice(policy["regions"]), anyStringSlice(policy["deniedRegions"])
}

func anyStringSlice(raw any) []string {
	items, _ := raw.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok {
			result = append(result, value)
		}
	}
	return result
}

func boolFromScopeKey(scope map[string]any, key string) bool {
	value, _ := scope[key].(bool)
	return value
}

func stringSliceFromScopeKey(scope map[string]any, key string) []string {
	if scope == nil {
		return nil
	}
	raw, ok := scope[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
