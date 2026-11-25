package control

import (
	"testing"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"gitee.com/warden-platform/warden/server/internal/storage/postgres"
)

func testConnector(id, name string) domain.Connector {
	return domain.Connector{ID: id, Name: name, ConnectorType: "kubernetes", Status: domain.ConnectorActive, Version: "1.0.0"}
}

func testIntegration(id, connectorID, name string) domain.Integration {
	return domain.Integration{ID: id, ConnectorID: connectorID, Name: name, Protocol: "jenkins", Endpoint: "https://jenkins.example.test", Status: domain.IntegrationActive}
}

func testAction(name, resourceType string) domain.ActionDefinition {
	return domain.ActionDefinition{Name: name, ResourceType: resourceType, RiskLevel: domain.RiskLow}
}

func TestBuildTopologyResponseNestsIntegrationsAndGroups(t *testing.T) {
	connectors := []domain.Connector{testConnector("c1", "prod-cluster")}
	integrations := []domain.Integration{testIntegration("i1", "c1", "jenkins-ci")}
	groups := []postgres.TopologyGroupStat{
		{ConnectorID: "c1", IntegrationID: "i1", ResourceType: "jenkins_build", Count: 359, ActiveCount: 359,
			Samples: []postgres.TopologySample{{Name: "job-1", Status: "ACTIVE"}}},
		{ConnectorID: "c1", ResourceType: "deployment", Count: 2, ActiveCount: 1},
	}
	response := buildTopologyResponse(connectors, integrations, groups, []domain.ActionDefinition{testAction("jenkins.job.get", "jenkins_build")}, nil)

	if response.Summary.Connectors != 1 || response.Summary.Integrations != 1 || response.Summary.Groups != 2 || response.Summary.Resources != 361 {
		t.Fatalf("unexpected summary: %+v", response.Summary)
	}
	connector := response.Connectors[0]
	if len(connector.Integrations) != 1 {
		t.Fatalf("expected 1 integration, got %d", len(connector.Integrations))
	}
	integration := connector.Integrations[0]
	if len(integration.Groups) != 1 || integration.Groups[0].Health != "全部正常" {
		t.Fatalf("unexpected integration groups: %+v", integration.Groups)
	}
	if len(integration.Groups[0].Actions) != 1 || integration.Groups[0].Actions[0].Name != "jenkins.job.get" {
		t.Fatalf("expected action attached to group, got %+v", integration.Groups[0].Actions)
	}
	if len(connector.Groups) != 1 || connector.Groups[0].Health != "1/2 可用" || connector.Groups[0].Status != "warning" {
		t.Fatalf("unexpected direct groups: %+v", connector.Groups)
	}
	if connector.Groups[0].Samples == nil || integration.Groups[0].Samples == nil {
		t.Fatal("samples must serialize as an array, not null")
	}
}

func TestBuildTopologyResponsePropagatesLiveSignals(t *testing.T) {
	connectors := []domain.Connector{testConnector("c1", "prod-cluster")}
	integrations := []domain.Integration{testIntegration("i1", "c1", "jenkins-ci")}
	groups := []postgres.TopologyGroupStat{
		{ConnectorID: "c1", IntegrationID: "i1", ResourceType: "jenkins_build", Count: 1, ActiveCount: 1},
	}
	live := map[string]struct{}{"c1|i1|jenkins_build": {}}
	response := buildTopologyResponse(connectors, integrations, groups, nil, live)

	connector := response.Connectors[0]
	if !connector.Live || !connector.Integrations[0].Live || !connector.Integrations[0].Groups[0].Live {
		t.Fatalf("expected live signal to propagate: %+v", connector)
	}
}

func TestBuildTopologyResponseDropsDetachedGroups(t *testing.T) {
	groups := []postgres.TopologyGroupStat{{ConnectorID: "missing", ResourceType: "deployment", Count: 3, ActiveCount: 3}}
	response := buildTopologyResponse(nil, nil, groups, nil, nil)
	if response.Summary.Groups != 0 || response.Summary.Resources != 0 {
		t.Fatalf("detached groups must not reach the summary: %+v", response.Summary)
	}
	if response.Connectors == nil {
		t.Fatal("connectors must serialize as an array, not null")
	}
}

func TestAggregateTopologyGroupsForRestrictedIdentities(t *testing.T) {
	now := time.Now()
	resources := []domain.Resource{
		{Name: "job-active", ResourceType: "jenkins_build", ConnectorID: "c1", IntegrationID: "i1", Status: domain.ResourceActive, CreatedAt: now},
		{Name: "job-old", ResourceType: "jenkins_build", ConnectorID: "c1", IntegrationID: "i1", Status: domain.ResourceDisabled, CreatedAt: now.Add(-time.Hour)},
		{Name: "job-gone", ResourceType: "jenkins_build", ConnectorID: "c1", IntegrationID: "i1", Status: domain.ResourceOrphaned, CreatedAt: now},
		{Name: "job-deleted", ResourceType: "jenkins_build", ConnectorID: "c1", IntegrationID: "i1", Status: domain.ResourceActive, CreatedAt: now,
			Metadata: map[string]any{"connectorDeleted": "true"}},
	}
	stats := aggregateTopologyGroups(resources)
	if len(stats) != 1 {
		t.Fatalf("expected one group, got %d", len(stats))
	}
	stat := stats[0]
	if stat.Count != 2 || stat.ActiveCount != 1 {
		t.Fatalf("unexpected counts: %+v", stat)
	}
	if len(stat.Samples) != 2 || stat.Samples[0].Name != "job-active" {
		t.Fatalf("expected active sample first, got %+v", stat.Samples)
	}
}
