package connector

import (
	"context"
	"fmt"
	"io"
	"sync"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

// MultiRunner lets protocol adapters share an existing connector identity,
// heartbeat, task queue, and audit path with the infrastructure runner.
type MultiRunner struct {
	mu            sync.RWMutex
	runners       map[string]Runner
	order         []string
	byType        map[string]string
	byIntegration map[string]string
	providers     map[string]*ProviderExecutor
	nextID        int
}

func NewMultiRunner(primary Runner, primaryTypes ...string) *MultiRunner {
	runner := &MultiRunner{runners: make(map[string]Runner), byType: make(map[string]string), byIntegration: make(map[string]string), providers: make(map[string]*ProviderExecutor)}
	runner.Set("primary", primary, primaryTypes...)

	return runner
}

func (r *MultiRunner) ExecuteProvider(ctx context.Context, task domain.ProviderTask) (*domain.ProviderResponse, error) {
	r.mu.RLock()
	executor := r.providers[task.Binding.Integration.ID]
	primary := r.runners["primary"]
	r.mu.RUnlock()
	if executor == nil && task.Binding.Integration.Protocol == "kubernetes" {
		if native, ok := primary.(providerTaskRunner); ok {
			return native.ExecuteProvider(ctx, task)
		}
	}
	if executor == nil {
		return nil, &domain.ProviderRequestError{Code: domain.ProviderErrIntegrationInactive, Message: "Integration has no provider executor"}
	}
	return executor.Execute(ctx, task.Request, task.Binding)
}

func (r *MultiRunner) SetIntegrationProvider(integrationID string, executor *ProviderExecutor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if executor == nil {
		delete(r.providers, integrationID)
		return
	}
	r.providers[integrationID] = executor
}

func (r *MultiRunner) Add(runner Runner, resourceTypes ...string) {
	r.mu.Lock()
	r.nextID++
	name := fmt.Sprintf("runner-%d", r.nextID)
	r.mu.Unlock()
	r.Set(name, runner, resourceTypes...)
}

func (r *MultiRunner) Set(name string, runner Runner, resourceTypes ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	previous := r.runners[name]

	for resourceType, runnerName := range r.byType {
		if runnerName == name {
			delete(r.byType, resourceType)
		}
	}
	if runner == nil {
		delete(r.runners, name)
		closeRunner(previous)
		for index, item := range r.order {
			if item == name {
				r.order = append(r.order[:index], r.order[index+1:]...)
				break
			}
		}
		return
	}
	if previous != nil && previous != runner {
		closeRunner(previous)
	}
	if _, exists := r.runners[name]; !exists {
		r.order = append(r.order, name)
	}
	r.runners[name] = runner
	for _, resourceType := range resourceTypes {
		r.byType[resourceType] = name
	}
}

func (r *MultiRunner) ListResources(ctx context.Context) []DiscoveredResource {
	r.mu.RLock()
	runners := make([]Runner, 0, len(r.order))
	for _, name := range r.order {
		if runner := r.runners[name]; runner != nil {
			runners = append(runners, runner)
		}
	}
	r.mu.RUnlock()

	var resources []DiscoveredResource
	for _, runner := range runners {
		resources = append(resources, runner.ListResources(ctx)...)
	}

	return resources
}

// completenessAware is implemented by runners that can distinguish a complete
// discovery pass from one truncated by a transient failure. A truncated pass
// must not be treated as "everything not listed was deleted".
type completenessAware interface {
	ListResourcesComplete(context.Context) ([]DiscoveredResource, bool)
}

// DiscoveryGroup is a per-integration slice of the discovery result. Grouping
// by integration lets the control plane orphan stale resources at integration
// granularity instead of connector granularity, so a broken integration does
// not orphan another integration's healthy resources.
type DiscoveryGroup struct {
	IntegrationID string               `json:"integrationId,omitempty"`
	Complete      bool                 `json:"complete"`
	Resources     []DiscoveredResource `json:"resources"`
}

// DiscoverGroups returns discovery results grouped by integration. The primary
// runner (native resources, e.g. Kubernetes) reports under an empty integration
// id; each integration runner reports under its integration id. Completeness is
// computed per runner so a partial integration pass only suppresses orphaning
// for that integration.
func (r *MultiRunner) DiscoverGroups(ctx context.Context) []DiscoveryGroup {
	r.mu.RLock()
	type namedRunner struct {
		name   string
		runner Runner
	}
	runners := make([]namedRunner, 0, len(r.order))
	for _, name := range r.order {
		if runner := r.runners[name]; runner != nil {
			runners = append(runners, namedRunner{name, runner})
		}
	}
	runnerToIntegration := make(map[string]string, len(r.byIntegration))
	for integrationID, runnerName := range r.byIntegration {
		runnerToIntegration[runnerName] = integrationID
	}
	r.mu.RUnlock()

	groups := make([]DiscoveryGroup, 0, len(runners))
	for _, item := range runners {
		complete := true
		var resources []DiscoveredResource
		if aware, ok := item.runner.(completenessAware); ok {
			resources, complete = aware.ListResourcesComplete(ctx)
		} else {
			resources = item.runner.ListResources(ctx)
		}
		groups = append(groups, DiscoveryGroup{
			IntegrationID: runnerToIntegration[item.name],
			Complete:      complete,
			Resources:     resources,
		})
	}
	return groups
}

func (r *MultiRunner) Execute(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	r.mu.RLock()
	// Integration-scoped resources route by integration_id so one connector can
	// host multiple integrations of the same protocol (e.g. two Jenkins) without
	// their runners colliding. Native resources (no integration_id) fall back to
	// byType. A stale integration_id must not silently fall through to another
	// integration's runner, so it is treated as an error.
	var runnerName string
	var ok bool
	if task.Resource.IntegrationID != "" {
		runnerName, ok = r.byIntegration[task.Resource.IntegrationID]
	} else {
		runnerName, ok = r.byType[task.Resource.Type]
	}
	runner := r.runners[runnerName]
	r.mu.RUnlock()
	if !ok || runner == nil {
		return nil, Lease{}, fmt.Errorf("unsupported resource type %q", task.Resource.Type)
	}

	return runner.Execute(ctx, task)
}

// SnapshotResource reads only the runner that owns the target resource. This
// avoids scanning unrelated cloud and middleware integrations around every
// change while still providing a live pre/post state observation.
func (r *MultiRunner) SnapshotResource(ctx context.Context, resource domain.TaskResource) (DiscoveredResource, bool) {
	r.mu.RLock()
	var runnerName string
	var ok bool
	if resource.IntegrationID != "" {
		runnerName, ok = r.byIntegration[resource.IntegrationID]
	} else {
		runnerName, ok = r.byType[resource.Type]
	}
	runner := r.runners[runnerName]
	r.mu.RUnlock()
	if !ok || runner == nil {
		return DiscoveredResource{}, false
	}
	if targeted, ok := runner.(snapshotRunner); ok {
		return targeted.SnapshotResource(ctx, resource)
	}
	return findDiscoveredResource(runner.ListResources(ctx), resource)
}

// SetIntegration registers (or, when runner == nil, removes) a runner bound to
// a specific integration. The runner name is the integration id, so multiple
// integrations of the same protocol coexist without overwriting each other.
func (r *MultiRunner) SetIntegration(integrationID string, runner Runner, resourceTypes ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	previous := r.runners[integrationID]

	if runner == nil {
		// Remove integration-scoped runner. It is registered under the
		// integration id; clearing byType entries it owned keeps native routing
		// consistent.
		for resourceType, runnerName := range r.byType {
			if runnerName == integrationID {
				delete(r.byType, resourceType)
			}
		}
		delete(r.byIntegration, integrationID)
		delete(r.providers, integrationID)
		delete(r.runners, integrationID)
		closeRunner(previous)
		for index, item := range r.order {
			if item == integrationID {
				r.order = append(r.order[:index], r.order[index+1:]...)
				break
			}
		}
		return
	}
	if previous != nil && previous != runner {
		closeRunner(previous)
	}
	if _, exists := r.runners[integrationID]; !exists {
		r.order = append(r.order, integrationID)
	}
	r.runners[integrationID] = runner
	for _, resourceType := range resourceTypes {
		r.byType[resourceType] = integrationID
	}
	r.byIntegration[integrationID] = integrationID
}

func closeRunner(runner Runner) {
	if closer, ok := runner.(io.Closer); ok {
		_ = closer.Close()
	}
}
