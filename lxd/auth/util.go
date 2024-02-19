package auth

import (
	"fmt"
	"net/http"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// ValidateAuthenticationMethod returns an api.StatusError with http.StatusBadRequest if the given authentication
// method is not recognised.
func ValidateAuthenticationMethod(authenticationMethod string) error {
	if !shared.ValueInSlice(authenticationMethod, []string{api.AuthenticationMethodTLS, api.AuthenticationMethodOIDC}) {
		return api.StatusErrorf(http.StatusBadRequest, "Unrecognized authentication method %q", authenticationMethod)
	}

	return nil
}

// ValidateEntitlement returns an error if the given Entitlement does not apply to the entity.Type.
func ValidateEntitlement(entityType entity.Type, entitlement Entitlement) error {
	entitlements, err := EntitlementsByEntityType(entityType)
	if err != nil {
		return err
	}

	if len(entitlements) == 0 {
		return fmt.Errorf("No entitlements can be granted against entities of type %q", entityType)
	}

	if !shared.ValueInSlice(entitlement, entitlements) {
		return fmt.Errorf("Entitlement %q not valid for entity type %q", entitlement, entityType)
	}

	return nil
}

// EntitlementsByEntityType returns a list of available Entitlement for the entity.Type.
func EntitlementsByEntityType(entityType entity.Type) ([]Entitlement, error) {
	err := entityType.Validate()
	if err != nil {
		return nil, fmt.Errorf("Entity type %q is not valid: %w", entityType, err)
	}

	// Some entity types do not have entitlements
	if shared.ValueInSlice(entityType, []entity.Type{
		entity.TypeContainer,
		entity.TypeCertificate,
		entity.TypeInstanceBackup,
		entity.TypeInstanceSnapshot,
		entity.TypeNode,
		entity.TypeOperation,
		entity.TypeStorageVolumeBackup,
		entity.TypeStorageVolumeSnapshot,
		entity.TypeWarning,
		entity.TypeClusterGroup,
	}) {
		return []Entitlement{}, nil
	}

	// With the exception of entity types in the list below. All entity types have EntitlementCanView,
	// EntitlementCanEdit, and EntitlementCanDelete.
	if !shared.ValueInSlice(entityType, []entity.Type{entity.TypeStorageVolume, entity.TypeInstance, entity.TypeProject, entity.TypeServer}) {
		return []Entitlement{EntitlementCanView, EntitlementCanEdit, EntitlementCanDelete}, nil
	}

	switch entityType {
	case entity.TypeStorageVolume:
		return []Entitlement{
			EntitlementCanView,
			EntitlementCanEdit,
			EntitlementCanDelete,
			EntitlementCanManageBackups,
			EntitlementCanManageSnapshots,
		}, nil
	case entity.TypeInstance:
		return []Entitlement{
			EntitlementCanView,
			EntitlementCanEdit,
			EntitlementCanDelete,
			EntitlementInstanceUser,
			EntitlementInstanceOperator,
			EntitlementCanUpdateState,
			EntitlementCanConnectSFTP,
			EntitlementCanAccessFiles,
			EntitlementCanAccessConsole,
			EntitlementCanExec,
			EntitlementCanManageBackups,
			EntitlementCanManageSnapshots,
		}, nil
	case entity.TypeProject:
		return []Entitlement{
			EntitlementCanView,
			EntitlementCanEdit,
			EntitlementCanDelete,
			EntitlementProjectOperator,
			EntitlementProjectViewer,
			EntitlementImageManager,
			EntitlementCanCreateImages,
			EntitlementCanViewImages,
			EntitlementCanEditImages,
			EntitlementCanDeleteImages,
			EntitlementImageAliasManager,
			EntitlementCanCreateImageAliases,
			EntitlementCanViewImageAliases,
			EntitlementCanEditImageAliases,
			EntitlementCanDeleteImageAliases,
			EntitlementInstanceManager,
			EntitlementCanCreateInstances,
			EntitlementCanViewInstances,
			EntitlementCanEditInstances,
			EntitlementCanDeleteInstances,
			EntitlementCanOperateInstances,
			EntitlementNetworkManager,
			EntitlementCanCreateNetworks,
			EntitlementCanViewNetworks,
			EntitlementCanEditNetworks,
			EntitlementCanDeleteNetworks,
			EntitlementNetworkACLManager,
			EntitlementCanCreateNetworkACLs,
			EntitlementCanViewNetworkACLs,
			EntitlementCanEditNetworkACLs,
			EntitlementCanDeleteNetworkACLs,
			EntitlementNetworkZoneManager,
			EntitlementCanCreateNetworkZones,
			EntitlementCanViewNetworkZones,
			EntitlementCanEditNetworkZones,
			EntitlementCanDeleteNetworkZones,
			EntitlementProfileManager,
			EntitlementCanCreateProfiles,
			EntitlementCanViewProfiles,
			EntitlementCanEditProfiles,
			EntitlementCanDeleteProfiles,
			EntitlementStorageVolumeManager,
			EntitlementCanCreateStorageVolumes,
			EntitlementCanViewStorageVolumes,
			EntitlementCanEditStorageVolumes,
			EntitlementCanDeleteStorageVolumes,
			EntitlementStorageBucketManager,
			EntitlementCanCreateStorageBuckets,
			EntitlementCanViewStorageBuckets,
			EntitlementCanEditStorageBuckets,
			EntitlementCanDeleteStorageBuckets,
			EntitlementCanViewOperations,
			EntitlementCanViewEvents,
		}, nil
	case entity.TypeServer:
		return []Entitlement{
			EntitlementCanView,
			EntitlementCanEdit,
			EntitlementServerAdmin,
			EntitlementServerViewer,
			EntitlementCanViewConfiguration,
			EntitlementPermissionManager,
			EntitlementCanViewPermissions,
			EntitlementCanCreateIdentities,
			EntitlementCanViewIdentities,
			EntitlementCanEditIdentities,
			EntitlementCanDeleteIdentities,
			EntitlementCanCreateGroups,
			EntitlementCanViewGroups,
			EntitlementCanEditGroups,
			EntitlementCanDeleteGroups,
			EntitlementStoragePoolManager,
			EntitlementCanCreateStoragePools,
			EntitlementCanEditStoragePools,
			EntitlementCanDeleteStoragePools,
			EntitlementProjectManager,
			EntitlementCanCreateProjects,
			EntitlementCanViewProjects,
			EntitlementCanEditProjects,
			EntitlementCanDeleteProjects,
			EntitlementCanOverrideClusterTargetRestriction,
			EntitlementCanViewPrivilegedEvents,
			EntitlementCanViewResources,
			EntitlementCanViewMetrics,
			EntitlementCanViewWarnings,
		}, nil
	}

	return nil, fmt.Errorf("Missing entitlements definition for entity type %q", entityType)
}
