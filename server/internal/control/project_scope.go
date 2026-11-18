package control

import (
	"context"
	"fmt"
	"strings"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

const principalProjectIDsMetadataKey = "project_ids"

type resourceProjectAccess int

const (
	resourceProjectAccessNone resourceProjectAccess = iota
	resourceProjectAccessConsumer
	resourceProjectAccessPrimary
)

// principalProjectScope returns the Warden projects explicitly granted to an
// identity. An absent project_ids field keeps existing principals unrestricted;
// an explicitly empty field grants no resource access.
func principalProjectScope(identity *AuthenticatedIdentity) (map[string]struct{}, bool, error) {
	if identity == nil || identity.Principal == nil || identity.HasScope("warden:admin") {
		return nil, false, nil
	}
	raw, exists := identity.Principal.Metadata[principalProjectIDsMetadataKey]
	if !exists {
		return nil, false, nil
	}
	projects := map[string]struct{}{}
	switch values := raw.(type) {
	case []string:
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				projects[value] = struct{}{}
			}
		}
	case []any:
		for _, rawValue := range values {
			value, ok := rawValue.(string)
			if !ok {
				return nil, true, fmt.Errorf("principal metadata %s must contain only strings", principalProjectIDsMetadataKey)
			}
			if value = strings.TrimSpace(value); value != "" {
				projects[value] = struct{}{}
			}
		}
	default:
		return nil, true, fmt.Errorf("principal metadata %s must be an array", principalProjectIDsMetadataKey)
	}
	return projects, true, nil
}

func (h *Handler) identityResourceProjectAccess(ctx context.Context, identity *AuthenticatedIdentity, resource *domain.Resource, requestedProjectID string) (resourceProjectAccess, error) {
	allowed, scoped, err := principalProjectScope(identity)
	if err != nil {
		return resourceProjectAccessNone, err
	}
	requestedProjectID = strings.TrimSpace(requestedProjectID)
	if scoped && requestedProjectID != "" {
		if _, ok := allowed[requestedProjectID]; !ok {
			return resourceProjectAccessNone, nil
		}
	}
	if !scoped && requestedProjectID == "" {
		return resourceProjectAccessPrimary, nil
	}

	primaryProjectID := strings.TrimSpace(resource.ProjectID)
	if primaryProjectID == "" && h.resources != nil {
		primaryProjectID, err = h.resources.EffectiveProjectID(ctx, resource.ID)
		if err != nil {
			return resourceProjectAccessNone, err
		}
	}

	projectIDs := append([]string(nil), resource.ProjectIDs...)
	if len(projectIDs) == 0 && h.projects != nil {
		scope, scopeErr := h.projects.GetResourceScope(ctx, resource.ID)
		if scopeErr != nil {
			return resourceProjectAccessNone, scopeErr
		}
		if scope != nil {
			projectIDs = append(projectIDs, scope.ProjectIDs...)
		}
	}
	if len(projectIDs) == 0 && primaryProjectID != "" {
		projectIDs = append(projectIDs, primaryProjectID)
	}

	consumerProjects := map[string]struct{}{}
	if resource.ResourceType == "jenkins_job" && h.projects != nil {
		consumers, consumerErr := h.projects.ListResourceConsumers(ctx, resource.ID)
		if consumerErr != nil {
			return resourceProjectAccessNone, consumerErr
		}
		for _, projectID := range consumers {
			consumerProjects[projectID] = struct{}{}
		}
	}

	accessForProject := func(projectID string) resourceProjectAccess {
		if projectID == "" || (scoped && !projectAllowed(allowed, projectID)) {
			return resourceProjectAccessNone
		}
		if _, consumer := consumerProjects[projectID]; consumer {
			return resourceProjectAccessConsumer
		}
		return resourceProjectAccessPrimary
	}

	if requestedProjectID != "" {
		for _, projectID := range projectIDs {
			if projectID == requestedProjectID {
				return accessForProject(projectID), nil
			}
		}
		return resourceProjectAccessNone, nil
	}

	consumerAccess := resourceProjectAccessNone
	for _, projectID := range projectIDs {
		access := accessForProject(projectID)
		if access == resourceProjectAccessPrimary {
			return access, nil
		}
		if access == resourceProjectAccessConsumer {
			consumerAccess = access
		}
	}
	return consumerAccess, nil
}

func (h *Handler) identityCanAccessResource(ctx context.Context, identity *AuthenticatedIdentity, resource *domain.Resource, requestedProjectID string) (bool, error) {
	access, err := h.identityResourceProjectAccess(ctx, identity, resource, requestedProjectID)
	return access != resourceProjectAccessNone, err
}

func consumerProjectCanRunAction(actionName string) bool {
	return actionName == "jenkins.job.get" || actionName == "jenkins.job.build"
}

func (h *Handler) identityCanExecuteResourceAction(ctx context.Context, identity *AuthenticatedIdentity, resource *domain.Resource, actionName string) (bool, error) {
	access, err := h.identityResourceProjectAccess(ctx, identity, resource, "")
	if err != nil {
		return false, err
	}
	return access == resourceProjectAccessPrimary || (access == resourceProjectAccessConsumer && consumerProjectCanRunAction(actionName)), nil
}

func projectAllowed(allowed map[string]struct{}, projectID string) bool {
	_, ok := allowed[projectID]
	return projectID != "" && ok
}

func (h *Handler) filterResourcesForIdentity(ctx context.Context, identity *AuthenticatedIdentity, resources []domain.Resource, projectID string) ([]domain.Resource, error) {
	filtered := make([]domain.Resource, 0, len(resources))
	for i := range resources {
		ok, err := h.identityCanAccessResource(ctx, identity, &resources[i], projectID)
		if err != nil {
			return nil, err
		}
		if ok {
			filtered = append(filtered, resources[i])
		}
	}
	return filtered, nil
}

func filterProjectsForIdentity(identity *AuthenticatedIdentity, projects []domain.Project) ([]domain.Project, error) {
	allowed, scoped, err := principalProjectScope(identity)
	if err != nil || !scoped {
		return projects, err
	}
	filtered := make([]domain.Project, 0, len(projects))
	for _, project := range projects {
		if projectAllowed(allowed, project.ID) {
			filtered = append(filtered, project)
		}
	}
	return filtered, nil
}

func (h *Handler) filterExecutionsForProject(ctx context.Context, identity *AuthenticatedIdentity, executions []domain.Execution, projectID string) ([]domain.Execution, error) {
	projectID = strings.TrimSpace(projectID)
	_, scoped, err := principalProjectScope(identity)
	if err != nil {
		return nil, err
	}
	if projectID == "" && !scoped {
		return executions, nil
	}
	filtered := make([]domain.Execution, 0, len(executions))
	for _, execution := range executions {
		resource, err := h.resources.GetByID(ctx, execution.ResourceID)
		if err != nil {
			return nil, err
		}
		if resource == nil {
			continue
		}
		allowed, err := h.identityCanAccessResource(ctx, identity, resource, projectID)
		if err != nil {
			return nil, err
		}
		if allowed {
			filtered = append(filtered, execution)
		}
	}
	return filtered, nil
}
