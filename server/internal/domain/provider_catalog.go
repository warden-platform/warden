package domain

import "net/http"

// BuiltinProviderCapabilities is the v1 administrator-owned catalog. Entries
// are intentionally narrow; extending it is a code/configuration change, not
// something an agent can request dynamically.
var BuiltinProviderCapabilities = []ResourceCapability{
	{Name: "grafana.dashboard.read", Version: 1, ResourceKinds: []string{"grafana_dashboard"}, Protocols: []string{"grafana"}, Methods: []string{http.MethodGet}, PathPatterns: []string{"/api/dashboards/uid/{uid}"}, MaxItems: 1, MaxResponseBytes: 1 << 20, TimeoutSeconds: 10},
	{Name: "jenkins.job.read", Version: 1, ResourceKinds: []string{"jenkins_job"}, Protocols: []string{"jenkins"}, Methods: []string{http.MethodGet}, PathPatterns: []string{"/job/{job}/api/json"}, QueryParameters: map[string]ProviderParamRule{"tree": {MaxValues: 1, MaxLength: 512}, "depth": {MaxValues: 1, MaxLength: 2, Pattern: "^[0-2]$"}}, MaxItems: 100, MaxResponseBytes: 1 << 20, TimeoutSeconds: 10},
	{Name: "kubernetes.resource.read", Version: 1, ResourceKinds: []string{"deployment", "statefulset", "daemonset", "replicaset", "job", "cronjob", "service", "endpoints", "ingress", "configmap", "pod", "node", "namespace", "kubernetes_resource"}, Protocols: []string{"kubernetes"}, Methods: []string{http.MethodGet}, PathPatterns: []string{"/api/{version}/namespaces/{namespace}/{plural}/{name}", "/apis/{group}/{version}/namespaces/{namespace}/{plural}/{name}"}, QueryParameters: map[string]ProviderParamRule{"labelSelector": {MaxValues: 1, MaxLength: 512}, "fieldSelector": {MaxValues: 1, MaxLength: 512}}, MaxItems: 500, MaxResponseBytes: 4 << 20, TimeoutSeconds: 20},
	{Name: "huawei.ecs.read", Version: 1, ResourceKinds: []string{"huawei_ecs_instance"}, Protocols: []string{"huaweicloud"}, Methods: []string{http.MethodGet}, PathPatterns: []string{"/servers/{server_id}"}, MaxItems: 1, MaxResponseBytes: 1 << 20, TimeoutSeconds: 20},
	{Name: "huawei.obs.object.metadata.read", Version: 1, ResourceKinds: []string{"huawei_obs_bucket"}, Protocols: []string{"huaweicloud"}, Methods: []string{http.MethodHead}, PathPatterns: []string{"/{bucket}/**"}, MaxItems: 1, MaxResponseBytes: 64 << 10, TimeoutSeconds: 15},
}

// FindProviderCapabilityIn matches a registered catalog entry by its exact
// versioned key and the resource kind + protocol it applies to. The catalog
// slice is the source of truth (the database-backed registry at runtime, or
// BuiltinProviderCapabilities as the built-in fallback).
func FindProviderCapabilityIn(capabilities []ResourceCapability, key, resourceKind, protocol string) (ResourceCapability, bool) {
	for _, capability := range capabilities {
		if capability.Key() == key && contains(capability.ResourceKinds, resourceKind) && contains(capability.Protocols, protocol) {
			return capability, true
		}
	}
	return ResourceCapability{}, false
}

// ProviderCapabilitiesForKinds returns every registered capability that applies
// to the given resource kind and protocol.
func ProviderCapabilitiesForKinds(capabilities []ResourceCapability, resourceKind, protocol string) []ResourceCapability {
	result := []ResourceCapability{}
	for _, capability := range capabilities {
		if contains(capability.ResourceKinds, resourceKind) && contains(capability.Protocols, protocol) {
			result = append(result, capability)
		}
	}
	return result
}

// FindProviderCapability and ProviderCapabilitiesFor remain available for the
// built-in catalog (seeded into the registry by migration 065) and for tests.
func FindProviderCapability(key, resourceKind, protocol string) (ResourceCapability, bool) {
	return FindProviderCapabilityIn(BuiltinProviderCapabilities, key, resourceKind, protocol)
}

func ProviderCapabilitiesFor(resourceKind, protocol string) []ResourceCapability {
	return ProviderCapabilitiesForKinds(BuiltinProviderCapabilities, resourceKind, protocol)
}
