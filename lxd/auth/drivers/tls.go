//go:build linux && cgo && !agent

package drivers

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

// featureFlags is a map of entity.Type to the feature flag that governs whether they are present in the requested or effective project.
var featureFlags = map[entity.Type]string{
	entity.TypeImage:                 "features.images",
	entity.TypeImageAlias:            "features.images",
	entity.TypeProfile:               "features.profiles",
	entity.TypeNetwork:               "features.networks",
	entity.TypeNetworkACL:            "features.networks",
	entity.TypeNetworkZone:           "features.networks.zones",
	entity.TypeStorageVolume:         "features.storage.volumes",
	entity.TypeStorageVolumeBackup:   "features.storage.volumes",
	entity.TypeStorageVolumeSnapshot: "features.storage.volumes",
	entity.TypeStorageBucket:         "features.storage.buckets",
}

// onCreateEntities is a map of project entitlements to the entity types they represent creation of.
// This is used when checking if the caller has permission to create an entity in the default project.
// They may not have access to the default project directly but are able to create e.g. a network in the default project
// because one or more of the projects that they can access does not have features.networks enabled.
var onCreateEntities = map[auth.Entitlement]entity.Type{
	auth.EntitlementCanCreateImages:         entity.TypeImage,
	auth.EntitlementCanCreateImageAliases:   entity.TypeImageAlias,
	auth.EntitlementCanCreateProfiles:       entity.TypeProfile,
	auth.EntitlementCanCreateNetworks:       entity.TypeNetwork,
	auth.EntitlementCanCreateNetworkACLs:    entity.TypeNetworkACL,
	auth.EntitlementCanCreateNetworkZones:   entity.TypeNetworkZone,
	auth.EntitlementCanCreateStorageVolumes: entity.TypeStorageVolume,
	auth.EntitlementCanCreateStorageBuckets: entity.TypeStorageBucket,
}

const (
	// DriverTLS is used at start up to allow communication between cluster members and initialise the cluster database.
	DriverTLS string = "tls"
)

func init() {
	authorizers[DriverTLS] = func() authorizer { return &tls{} }
}

type tls struct {
	commonAuthorizer
}

func (t *tls) load(ctx context.Context, opts Opts) error {
	return nil
}

// GetViewableProjects is not implemented for the TLS authorizer.
func (t *tls) GetViewableProjects(ctx context.Context, permissions []api.Permission) ([]string, error) {
	return nil, api.NewGenericStatusError(http.StatusNotImplemented)
}

// CheckPermission returns an error if the user does not have the given Entitlement on the given Object.
func (t *tls) CheckPermission(ctx context.Context, entityURL *api.URL, entitlement auth.Entitlement) error {
	entityType, projectName, _, pathArguments, err := entity.ParseURL(entityURL.URL)
	if err != nil {
		return fmt.Errorf("Failed parsing entity URL: %w", err)
	}

	err = auth.ValidateEntitlement(entityType, entitlement)
	if err != nil {
		return fmt.Errorf("Cannot check permissions for entity type %q and entitlement %q: %w", entityType, entitlement, err)
	}

	requestor, err := request.GetRequestor(ctx)
	if err != nil {
		return err
	}

	// Untrusted requests are denied.
	if !requestor.IsTrusted() {
		return api.NewGenericStatusError(http.StatusForbidden)
	}

	// Cluster or unix socket requests have admin permission.
	if requestor.IsAdmin() {
		return nil
	}

	idType, err := requestor.CallerIdentityType()
	if err != nil {
		return err
	}

	if idType.Name() == api.IdentityTypeCertificateMetricsUnrestricted && entitlement == auth.EntitlementCanViewMetrics {
		return nil
	}

	projectSpecific, err := entityType.RequiresProject()
	if err != nil {
		return fmt.Errorf("Failed checking project specificity of entity type %q: %w", entityType, err)
	}

	// Check non- project-specific entity types.
	if !projectSpecific {
		if t.allowProjectUnspecificEntityType(entitlement, entityType, requestor, projectName, pathArguments) {
			return nil
		}

		t.emitAuthzFail(ctx, entityURL, entitlement, entityType)
		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	callerProjectsWithFeatures := requestor.CallerAllowedProjectsWithFeatures()
	_, hasProject := callerProjectsWithFeatures[projectName]

	// Check project level permissions against the certificates project list.
	if !hasProject && !checkEffectiveProject(entityType, projectName, callerProjectsWithFeatures) {
		t.emitAuthzFail(ctx, entityURL, entitlement, entityType)
		return api.StatusErrorf(http.StatusForbidden, "User does not have permission for project %q", projectName)
	}

	return nil
}

// GetPermissionChecker returns a function that can be used to check whether a user has the required entitlement on an authorization object.
func (t *tls) GetPermissionChecker(ctx context.Context, entitlement auth.Entitlement, entityType entity.Type) (auth.PermissionChecker, error) {
	err := auth.ValidateEntitlement(entityType, entitlement)
	if err != nil {
		return nil, fmt.Errorf("Cannot get a permission checker for entity type %q and entitlement %q: %w", entityType, entitlement, err)
	}

	allowFunc := func(b bool) func(*api.URL) bool {
		return func(*api.URL) bool {
			return b
		}
	}

	requestor, err := request.GetRequestor(ctx)
	if err != nil {
		return nil, err
	}

	// Untrusted requests are denied.
	if !requestor.IsTrusted() {
		return allowFunc(false), nil
	}

	// Cluster or unix socket requests have admin permission.
	if requestor.IsAdmin() {
		return allowFunc(true), nil
	}

	idType, err := requestor.CallerIdentityType()
	if err != nil {
		return nil, err
	}

	if idType.Name() == api.IdentityTypeCertificateMetricsUnrestricted && entitlement == auth.EntitlementCanViewMetrics {
		return allowFunc(true), nil
	}

	projectSpecific, err := entityType.RequiresProject()
	if err != nil {
		return nil, fmt.Errorf("Failed checking project specificity of entity type %q: %w", entityType, err)
	}

	// Filter objects by project.
	return func(entityURL *api.URL) bool {
		eType, project, _, pathArguments, err := entity.ParseURL(entityURL.URL)
		if err != nil {
			logger.Warn("Permission checker failed parsing entity URL", logger.Ctx{"entity_url": entityURL, "err": err})
			return false
		}

		// GetPermissionChecker can only be used to check permissions on entities of the same type, e.g. a list of instances.
		if eType != entityType {
			logger.Warn("Permission checker received URL with unexpected entity type", logger.Ctx{"expected": entityType, "actual": eType, "entity_url": entityURL})
			return false
		}

		// Check non- project-specific entity types.
		if !projectSpecific {
			return t.allowProjectUnspecificEntityType(entitlement, entityType, requestor, project, pathArguments)
		}

		// Otherwise, check if the project is in the list of allowed projects for the entity.
		if slices.Contains(requestor.CallerAllowedProjectNames(), project) {
			return true
		}

		// Lastly, check effective project of entity type against the callers allowed project feature list.
		return checkEffectiveProject(entityType, project, requestor.CallerAllowedProjectsWithFeatures())
	}, nil
}

// checkEffectiveProject returns true if the given project name is [api.ProjectDefaultName] and any of the
// allowedProjectFeature flags is false for the given entity type. This means that if the caller has access to any
// projects that have e.g. features.networks=false, then they can view networks in the default project.
func checkEffectiveProject(entityType entity.Type, projectName string, allowedProjectFeatureFlags map[string]map[string]bool) bool {
	if projectName != api.ProjectDefaultName {
		return false
	}

	flag, ok := featureFlags[entityType]
	if !ok {
		return false
	}

	for _, features := range allowedProjectFeatureFlags {
		if !features[flag] {
			return true
		}
	}

	return false
}

func (t *tls) allowProjectUnspecificEntityType(entitlement auth.Entitlement, entityType entity.Type, requestor *request.Requestor, projectName string, pathArguments []string) bool {
	switch entityType {
	case entity.TypeServer:
		// Restricted TLS certificates have the following entitlements on server.
		//
		// Note: We have to keep EntitlementCanViewMetrics here for backwards compatibility with older versions of LXD.
		// Historically when viewing the metrics endpoint for a specific project with a restricted certificate also the
		// internal server metrics get returned.
		return slices.Contains([]auth.Entitlement{auth.EntitlementCanViewResources, auth.EntitlementCanViewMetrics, auth.EntitlementCanViewUnmanagedNetworks}, entitlement)
	case entity.TypeIdentity:
		// If the entity URL refers to the identity that made the request, then the second path argument of the URL is
		// the identifier of the identity. This line allows the caller to view their own identity and no one else's.
		return entitlement == auth.EntitlementCanView && len(pathArguments) > 1 && pathArguments[1] == requestor.Username
	case entity.TypeCertificate:
		// If the certificate URL refers to the identity that made the request, then the first path argument of the URL is
		// the identifier of the identity (their fingerprint). This line allows the caller to view their own certificate and no one else's.
		return entitlement == auth.EntitlementCanView && len(pathArguments) > 0 && pathArguments[0] == requestor.Username
	case entity.TypeProject:
		callerProjectsWithFeatures := requestor.CallerAllowedProjectsWithFeatures()
		_, hasProject := callerProjectsWithFeatures[projectName]

		// If the project is in the list of projects that the identity is restricted to, then they have the following
		// entitlements.
		if hasProject && slices.Contains([]auth.Entitlement{auth.EntitlementCanView, auth.EntitlementCanCreateImages, auth.EntitlementCanCreateImageAliases, auth.EntitlementCanCreateInstances, auth.EntitlementCanCreateNetworks, auth.EntitlementCanCreateNetworkACLs, auth.EntitlementCanCreateNetworkZones, auth.EntitlementCanCreateProfiles, auth.EntitlementCanCreateStorageVolumes, auth.EntitlementCanCreateStorageBuckets, auth.EntitlementCanViewEvents, auth.EntitlementCanViewOperations, auth.EntitlementCanViewMetrics}, entitlement) {
			return true
		}

		// If the project is not the default project, return.
		if projectName != api.ProjectDefaultName {
			return false
		}

		// Check if the entitlement represents entity creation.
		createdEntity, ok := onCreateEntities[entitlement]
		if !ok {
			return false
		}

		// Check if the created entity type is disabled in any of the callers projects.
		return checkEffectiveProject(createdEntity, api.ProjectDefaultName, callerProjectsWithFeatures)

	default:
		return false
	}
}
