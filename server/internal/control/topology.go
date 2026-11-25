package control

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"gitee.com/warden-platform/warden/server/internal/storage/postgres"
)

type topologyActionRef struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Risk        string `json:"risk,omitempty"`
}

type topologySample struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Status    string `json:"status"`
}

type topologyGroup struct {
	ID          string              `json:"id"`
	Type        string              `json:"type"`
	Count       int                 `json:"count"`
	ActiveCount int                 `json:"activeCount"`
	Status      string              `json:"status"`
	Health      string              `json:"health"`
	Live        bool                `json:"live"`
	Samples     []topologySample    `json:"samples"`
	Actions     []topologyActionRef `json:"actions"`
}

type topologyIntegration struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Protocol string          `json:"protocol"`
	Endpoint string          `json:"endpoint"`
	Status   string          `json:"status"`
	Live     bool            `json:"live"`
	Groups   []topologyGroup `json:"groups"`
}

type topologyConnector struct {
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Type         string                `json:"type"`
	Version      string                `json:"version"`
	Status       string                `json:"status"`
	Live         bool                  `json:"live"`
	Integrations []topologyIntegration `json:"integrations"`
	Groups       []topologyGroup       `json:"groups"`
}

type topologySummary struct {
	Connectors   int `json:"connectors"`
	Integrations int `json:"integrations"`
	Groups       int `json:"groups"`
	Resources    int `json:"resources"`
	Actions      int `json:"actions"`
}

type topologyResponse struct {
	Summary    topologySummary     `json:"summary"`
	Connectors []topologyConnector `json:"connectors"`
}

// GetTopology returns the nested connector → integration → group → sample tree
// used by the drill-down topology view.
func (h *Handler) GetTopology(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	ctx := r.Context()
	identity := identityFromContext(ctx)
	_, principalScoped, err := principalProjectScope(identity)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PROJECT_SCOPE_ERROR", "校验资源项目权限失败")
		return
	}
	restricted := principalScoped || (h.projectMembershipEnforced(ctx) && identity != nil && !identity.IsTenantAdministrator())

	connectors, err := h.connectors.List(ctx)
	if err != nil {
		log.Printf("list topology connectors failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询 Connector 失败")
		return
	}
	integrations, err := h.integrations.ListAll(ctx)
	if err != nil {
		log.Printf("list topology integrations failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询集成失败")
		return
	}
	actions, err := h.actions.ListAvailable(ctx)
	if err != nil {
		log.Printf("list topology actions failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询动作失败")
		return
	}

	var groups []postgres.TopologyGroupStat
	if restricted {
		var resources []domain.Resource
		resources, err = h.resources.List(ctx, "", "", "", "", "", 0, 0)
		if err == nil {
			err = h.resources.EnrichProjectScopes(ctx, resources)
		}
		if err == nil {
			resources, err = h.filterResourcesForIdentity(ctx, identity, resources, "")
		}
		if err == nil {
			resources, err = h.filterResourcesByProjectMembership(ctx, resources)
		}
		if err != nil {
			log.Printf("list topology resources failed: %v", err)
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询资源失败")
			return
		}
		groups = aggregateTopologyGroups(resources)
	} else {
		groups, err = h.resources.ListTopologyGroups(ctx)
		if err != nil {
			log.Printf("list topology groups failed: %v", err)
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询拓扑失败")
			return
		}
	}

	live, err := h.resources.ListTopologyLiveGroups(ctx)
	if err != nil {
		// Live signals are decorative: never fail the topology tree for them.
		log.Printf("list topology live groups failed: %v", err)
		live = map[string]struct{}{}
	}

	jsonResp(w, http.StatusOK, buildTopologyResponse(connectors, integrations, groups, actions, live))
}

func buildTopologyResponse(
	connectors []domain.Connector,
	integrations []domain.Integration,
	groups []postgres.TopologyGroupStat,
	actions []domain.ActionDefinition,
	live map[string]struct{},
) topologyResponse {
	actionsByType := map[string][]topologyActionRef{}
	for _, action := range actions {
		actionsByType[action.ResourceType] = append(actionsByType[action.ResourceType], topologyActionRef{
			Name:        action.Name,
			DisplayName: action.DisplayName,
			Risk:        string(action.RiskLevel),
		})
	}
	for resourceType := range actionsByType {
		sort.Slice(actionsByType[resourceType], func(i, j int) bool {
			return actionsByType[resourceType][i].Name < actionsByType[resourceType][j].Name
		})
	}

	groupsByParent := map[string][]topologyGroup{}
	for _, stat := range groups {
		group := topologyGroup{
			ID:          "group:" + topologyParentID(stat.IntegrationID, stat.ConnectorID) + ":" + stat.ResourceType,
			Type:        stat.ResourceType,
			Count:       stat.Count,
			ActiveCount: stat.ActiveCount,
			Status:      topologyGroupStatus(stat),
			Health:      topologyGroupHealth(stat),
			Live:        topologyLive(stat, live),
			Samples:     make([]topologySample, 0, len(stat.Samples)),
			Actions:     actionsByType[stat.ResourceType],
		}
		if group.Actions == nil {
			group.Actions = []topologyActionRef{}
		}
		for _, sample := range stat.Samples {
			group.Samples = append(group.Samples, topologySample{Name: sample.Name, Namespace: sample.Namespace, Status: sample.Status})
		}
		parentID := stat.IntegrationID
		if parentID == "" {
			parentID = stat.ConnectorID
		}
		groupsByParent[parentID] = append(groupsByParent[parentID], group)
	}

	integrationsByConnector := map[string][]domain.Integration{}
	for _, integration := range integrations {
		integrationsByConnector[integration.ConnectorID] = append(integrationsByConnector[integration.ConnectorID], integration)
	}

	result := topologyResponse{Connectors: make([]topologyConnector, 0, len(connectors))}
	result.Summary.Actions = len(actions)

	for _, connector := range connectors {
		node := topologyConnector{
			ID:           connector.ID,
			Name:         connector.Name,
			Type:         connector.ConnectorType,
			Version:      connector.Version,
			Status:       string(connector.Status),
			Integrations: []topologyIntegration{},
			Groups:       sortTopologyGroups(groupsByParent[connector.ID]),
		}
		for _, group := range node.Groups {
			node.Live = node.Live || group.Live
		}

		connectorIntegrations := integrationsByConnector[connector.ID]
		sort.Slice(connectorIntegrations, func(i, j int) bool { return connectorIntegrations[i].Name < connectorIntegrations[j].Name })
		for _, integration := range connectorIntegrations {
			integrationNode := topologyIntegration{
				ID:       integration.ID,
				Name:     integration.Name,
				Protocol: integration.Protocol,
				Endpoint: integration.Endpoint,
				Status:   string(integration.Status),
				Groups:   sortTopologyGroups(groupsByParent[integration.ID]),
			}
			for _, group := range integrationNode.Groups {
				integrationNode.Live = integrationNode.Live || group.Live
			}
			node.Live = node.Live || integrationNode.Live
			node.Integrations = append(node.Integrations, integrationNode)
			result.Summary.Integrations++
		}

		for _, group := range node.Groups {
			result.Summary.Groups++
			result.Summary.Resources += group.Count
		}
		for _, integration := range node.Integrations {
			for _, group := range integration.Groups {
				result.Summary.Groups++
				result.Summary.Resources += group.Count
			}
		}
		result.Connectors = append(result.Connectors, node)
	}
	result.Summary.Connectors = len(result.Connectors)

	sort.Slice(result.Connectors, func(i, j int) bool {
		if result.Connectors[i].Name == result.Connectors[j].Name {
			return result.Connectors[i].ID < result.Connectors[j].ID
		}
		return result.Connectors[i].Name < result.Connectors[j].Name
	})
	return result
}

func topologyParentID(integrationID, connectorID string) string {
	if integrationID != "" {
		return integrationID
	}
	return connectorID
}

func topologyGroupStatus(stat postgres.TopologyGroupStat) string {
	if stat.Count == 0 || stat.ActiveCount == 0 {
		return "error"
	}
	if stat.ActiveCount >= stat.Count {
		return "active"
	}
	return "warning"
}

func topologyGroupHealth(stat postgres.TopologyGroupStat) string {
	switch {
	case stat.Count == 0:
		return "无资源"
	case stat.ActiveCount == 0 && stat.Count == 1:
		return "不可用"
	case stat.ActiveCount == 0:
		return "全部不可用"
	case stat.ActiveCount >= stat.Count && stat.Count == 1:
		return "正常"
	case stat.ActiveCount >= stat.Count:
		return "全部正常"
	default:
		return fmt.Sprintf("%d/%d 可用", stat.ActiveCount, stat.Count)
	}
}

func topologyLive(stat postgres.TopologyGroupStat, live map[string]struct{}) bool {
	if len(live) == 0 {
		return false
	}
	_, ok := live[stat.ConnectorID+"|"+stat.IntegrationID+"|"+stat.ResourceType]
	return ok
}

func sortTopologyGroups(groups []topologyGroup) []topologyGroup {
	if groups == nil {
		return []topologyGroup{}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Type < groups[j].Type })
	return groups
}

// aggregateTopologyGroups folds filtered resources for restricted identities.
// It mirrors ListTopologyGroups so both code paths render the same tree.
func aggregateTopologyGroups(resources []domain.Resource) []postgres.TopologyGroupStat {
	sort.SliceStable(resources, func(i, j int) bool {
		iActive := resources[i].Status == domain.ResourceActive
		jActive := resources[j].Status == domain.ResourceActive
		if iActive != jActive {
			return iActive
		}
		return resources[i].CreatedAt.After(resources[j].CreatedAt)
	})

	type groupKey struct {
		integrationID string
		connectorID   string
		resourceType  string
	}
	stats := map[groupKey]*postgres.TopologyGroupStat{}
	order := make([]groupKey, 0)
	for _, resource := range resources {
		if resource.Status == domain.ResourceOrphaned || resource.ManagementType == domain.ManagementControlPlane {
			continue
		}
		if deletesFromTopology(resource.Metadata) {
			continue
		}
		if resource.IntegrationID == "" && integrationScopedType(resource.ResourceType) {
			continue
		}
		key := groupKey{resource.IntegrationID, resource.ConnectorID, resource.ResourceType}
		stat, ok := stats[key]
		if !ok {
			stat = &postgres.TopologyGroupStat{
				IntegrationID: resource.IntegrationID,
				ConnectorID:   resource.ConnectorID,
				ResourceType:  resource.ResourceType,
			}
			stats[key] = stat
			order = append(order, key)
		}
		stat.Count++
		if resource.Status == domain.ResourceActive {
			stat.ActiveCount++
		}
		if len(stat.Samples) < 6 {
			stat.Samples = append(stat.Samples, postgres.TopologySample{
				Name:      resource.Name,
				Namespace: resource.Namespace,
				Status:    string(resource.Status),
			})
		}
	}

	sort.Slice(order, func(i, j int) bool {
		if order[i].integrationID != order[j].integrationID {
			return order[i].integrationID < order[j].integrationID
		}
		if order[i].connectorID != order[j].connectorID {
			return order[i].connectorID < order[j].connectorID
		}
		return order[i].resourceType < order[j].resourceType
	})
	result := make([]postgres.TopologyGroupStat, 0, len(order))
	for _, key := range order {
		result = append(result, *stats[key])
	}
	return result
}

func deletesFromTopology(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	return metadata["integrationDeleted"] == "true" || metadata["connectorDeleted"] == "true"
}

func integrationScopedType(resourceType string) bool {
	for _, prefix := range []string{"jenkins_", "grafana_", "database_", "huawei_", "aliyun_sls_"} {
		if strings.HasPrefix(resourceType, prefix) {
			return true
		}
	}
	return false
}
