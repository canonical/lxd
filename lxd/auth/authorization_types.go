package auth

import (
	"fmt"

	"github.com/canonical/lxd/shared"
)

// Entitlement represents a permission that can be applied to an entity.
type Entitlement string

const (
	// EntitlementCanView is the `can_view` Entitlement. It applies to most entity types.
	EntitlementCanView Entitlement = "can_view"

	// EntitlementCanEdit is the `can_edit` Entitlement. It applies to most entity types.
	EntitlementCanEdit Entitlement = "can_edit"

	// EntitlementCanDelete is the `can_delete` Entitlement. It applies to most entity types.
	EntitlementCanDelete Entitlement = "can_delete"

	// EntitlementServerAdmin is the `admin` Entitlement. It applies to entity.TypeServer.
	EntitlementServerAdmin Entitlement = "admin"

	// EntitlementServerViewer is the `viewer` Entitlement. It applies to entity.TypeServer.
	EntitlementServerViewer Entitlement = "viewer"

	// EntitlementPermissionManager is the `permission_manager` Entitlement. It applies to entity.TypeServer.
	EntitlementPermissionManager Entitlement = "permission_manager"

	// EntitlementCanViewPermissions is the `can_view_permissions` Entitlement. It applies to entity.TypeServer.
	EntitlementCanViewPermissions Entitlement = "can_view_permissions"

	// EntitlementCanCreateIdentities is the `can_create_identities` Entitlement. It applies to entity.TypeServer.
	EntitlementCanCreateIdentities Entitlement = "can_create_identities"

	// EntitlementCanViewIdentities is the `can_view_identities` Entitlement. It applies to entity.TypeServer.
	EntitlementCanViewIdentities Entitlement = "can_view_identities"

	// EntitlementCanEditIdentities is the `can_edit_identities` Entitlement. It applies to entity.TypeServer.
	EntitlementCanEditIdentities Entitlement = "can_edit_identities"

	// EntitlementCanDeleteIdentities is the `can_delete_identities` Entitlement. It applies to entity.TypeServer.
	EntitlementCanDeleteIdentities Entitlement = "can_delete_identities"

	// EntitlementCanCreateGroups is the `can_create_groups` Entitlement. It applies to entity.TypeServer.
	EntitlementCanCreateGroups Entitlement = "can_create_groups"

	// EntitlementCanViewGroups is the `can_view_groups` Entitlement. It applies to entity.TypeServer.
	EntitlementCanViewGroups Entitlement = "can_view_groups"

	// EntitlementCanEditGroups is the `can_edit_groups` Entitlement. It applies to entity.TypeServer.
	EntitlementCanEditGroups Entitlement = "can_edit_groups"

	// EntitlementCanDeleteGroups is the `can_delete_groups` Entitlement. It applies to entity.TypeServer.
	EntitlementCanDeleteGroups Entitlement = "can_delete_groups"

	// EntitlementCanCreateIdentityProviderGroups is the `can_create_identity_provider_groups` Entitlement. It applies to entity.TypeServer.
	EntitlementCanCreateIdentityProviderGroups Entitlement = "can_create_identity_provider_groups"

	// EntitlementCanViewIdentityProviderGroups is the `can_view_identity_provider_groups` Entitlement. It applies to entity.TypeServer.
	EntitlementCanViewIdentityProviderGroups Entitlement = "can_view_identity_provider_groups"

	// EntitlementCanEditIdentityProviderGroups is the `can_edit_identity_provider_groups` Entitlement. It applies to entity.TypeServer.
	EntitlementCanEditIdentityProviderGroups Entitlement = "can_edit_identity_provider_groups"

	// EntitlementCanDeleteIdentityProviderGroups is the `can_delete_identity_provider_groups` Entitlement. It applies to entity.TypeServer.
	EntitlementCanDeleteIdentityProviderGroups Entitlement = "can_delete_identity_provider_groups"

	// EntitlementStoragePoolManager is the `storage_pool_manager` Entitlement. It applies to entity.TypeServer.
	EntitlementStoragePoolManager Entitlement = "storage_pool_manager"

	// EntitlementCanCreateStoragePools is the `can_create_storage_pools` Entitlement. It applies to entity.TypeServer.
	EntitlementCanCreateStoragePools Entitlement = "can_create_storage_pools"

	// EntitlementCanEditStoragePools is the `can_edit_storage_pools` Entitlement. It applies to entity.TypeServer.
	EntitlementCanEditStoragePools Entitlement = "can_edit_storage_pools"

	// EntitlementCanDeleteStoragePools is the `can_delete_storage_pools` Entitlement. It applies to entity.TypeServer.
	EntitlementCanDeleteStoragePools Entitlement = "can_delete_storage_pools"

	// EntitlementProjectManager is the `project_manager` Entitlement. It applies to entity.TypeServer.
	EntitlementProjectManager Entitlement = "project_manager"

	// EntitlementCanCreateProjects is the `can_create_projects` Entitlement. It applies to entity.TypeServer.
	EntitlementCanCreateProjects Entitlement = "can_create_projects"

	// EntitlementCanViewProjects is the `can_view_projects` Entitlement. It applies to entity.TypeServer.
	EntitlementCanViewProjects Entitlement = "can_view_projects"

	// EntitlementCanEditProjects is the `can_edit_projects` Entitlement. It applies to entity.TypeServer.
	EntitlementCanEditProjects Entitlement = "can_edit_projects"

	// EntitlementCanDeleteProjects is the `can_delete_projects` Entitlement. It applies to entity.TypeServer.
	EntitlementCanDeleteProjects Entitlement = "can_delete_projects"

	// EntitlementCanOverrideClusterTargetRestriction is the `can_override_cluster_target_restriction` Entitlement. It applies to entity.TypeServer.
	EntitlementCanOverrideClusterTargetRestriction Entitlement = "can_override_cluster_target_restriction"

	// EntitlementCanViewPrivilegedEvents is the `can_view_privileged_events` Entitlement. It applies to entity.TypeServer.
	EntitlementCanViewPrivilegedEvents Entitlement = "can_view_privileged_events"

	// EntitlementCanViewResources is the `can_view_resources` Entitlement. It applies to entity.TypeServer.
	EntitlementCanViewResources Entitlement = "can_view_resources"

	// EntitlementCanViewMetrics is the `can_view_metrics` Entitlement. It applies to entity.TypeServer.
	EntitlementCanViewMetrics Entitlement = "can_view_metrics"

	// EntitlementCanViewWarnings is the `can_view_warnings` Entitlement. It applies to entity.TypeServer.
	EntitlementCanViewWarnings Entitlement = "can_view_warnings"

	// EntitlementProjectOperator is the `operator` Entitlement. It applies to entity.TypeProject.
	EntitlementProjectOperator Entitlement = "operator"

	// EntitlementProjectViewer is the `viewer` Entitlement. It applies to entity.TypeProject.
	EntitlementProjectViewer Entitlement = "viewer"

	// EntitlementImageManager is the `image_manager` Entitlement. It applies to entity.TypeProject.
	EntitlementImageManager Entitlement = "image_manager"

	// EntitlementCanCreateImages is the `can_create_images` Entitlement. It applies to entity.TypeProject.
	EntitlementCanCreateImages Entitlement = "can_create_images"

	// EntitlementCanViewImages is the `can_view_images` Entitlement. It applies to entity.TypeProject.
	EntitlementCanViewImages Entitlement = "can_view_images"

	// EntitlementCanEditImages is the `can_edit_images` Entitlement. It applies to entity.TypeProject.
	EntitlementCanEditImages Entitlement = "can_edit_images"

	// EntitlementCanDeleteImages is the `can_delete_images` Entitlement. It applies to entity.TypeProject.
	EntitlementCanDeleteImages Entitlement = "can_delete_images"

	// EntitlementImageAliasManager is the `image_alias_manager` Entitlement. It applies to entity.TypeProject.
	EntitlementImageAliasManager Entitlement = "image_alias_manager"

	// EntitlementCanCreateImageAliases is the `can_create_image_aliases` Entitlement. It applies to entity.TypeProject.
	EntitlementCanCreateImageAliases Entitlement = "can_create_image_aliases"

	// EntitlementCanViewImageAliases is the `can_view_image_aliases` Entitlement. It applies to entity.TypeProject.
	EntitlementCanViewImageAliases Entitlement = "can_view_image_aliases"

	// EntitlementCanEditImageAliases is the `can_edit_image_aliases` Entitlement. It applies to entity.TypeProject.
	EntitlementCanEditImageAliases Entitlement = "can_edit_image_aliases"

	// EntitlementCanDeleteImageAliases is the `can_delete_image_aliases` Entitlement. It applies to entity.TypeProject.
	EntitlementCanDeleteImageAliases Entitlement = "can_delete_image_aliases"

	// EntitlementInstanceManager is the `instance_manager` Entitlement. It applies to entity.TypeProject.
	EntitlementInstanceManager Entitlement = "instance_manager"

	// EntitlementCanCreateInstances is the `can_create_instances` Entitlement. It applies to entity.TypeProject.
	EntitlementCanCreateInstances Entitlement = "can_create_instances"

	// EntitlementCanViewInstances is the `can_view_instances` Entitlement. It applies to entity.TypeProject.
	EntitlementCanViewInstances Entitlement = "can_view_instances"

	// EntitlementCanEditInstances is the `can_edit_instances` Entitlement. It applies to entity.TypeProject.
	EntitlementCanEditInstances Entitlement = "can_edit_instances"

	// EntitlementCanDeleteInstances is the `can_delete_instances` Entitlement. It applies to entity.TypeProject.
	EntitlementCanDeleteInstances Entitlement = "can_delete_instances"

	// EntitlementCanOperateInstances is the `can_operate_instances` Entitlement. It applies to entity.TypeProject.
	EntitlementCanOperateInstances Entitlement = "can_operate_instances"

	// EntitlementNetworkManager is the `network_manager` Entitlement. It applies to entity.TypeProject.
	EntitlementNetworkManager Entitlement = "network_manager"

	// EntitlementCanCreateNetworks is the `can_create_networks` Entitlement. It applies to entity.TypeProject.
	EntitlementCanCreateNetworks Entitlement = "can_create_networks"

	// EntitlementCanViewNetworks is the `can_view_networks` Entitlement. It applies to entity.TypeProject.
	EntitlementCanViewNetworks Entitlement = "can_view_networks"

	// EntitlementCanEditNetworks is the `can_edit_networks` Entitlement. It applies to entity.TypeProject.
	EntitlementCanEditNetworks Entitlement = "can_edit_networks"

	// EntitlementCanDeleteNetworks is the `can_delete_networks` Entitlement. It applies to entity.TypeProject.
	EntitlementCanDeleteNetworks Entitlement = "can_delete_networks"

	// EntitlementNetworkACLManager is the `network_acl_manager` Entitlement. It applies to entity.TypeProject.
	EntitlementNetworkACLManager Entitlement = "network_acl_manager"

	// EntitlementCanCreateNetworkACLs is the `can_create_network_acls` Entitlement. It applies to entity.TypeProject.
	EntitlementCanCreateNetworkACLs Entitlement = "can_create_network_acls"

	// EntitlementCanViewNetworkACLs is the `can_view_network_acls` Entitlement. It applies to entity.TypeProject.
	EntitlementCanViewNetworkACLs Entitlement = "can_view_network_acls"

	// EntitlementCanEditNetworkACLs is the `can_edit_network_acls` Entitlement. It applies to entity.TypeProject.
	EntitlementCanEditNetworkACLs Entitlement = "can_edit_network_acls"

	// EntitlementCanDeleteNetworkACLs is the `can_delete_network_acls` Entitlement. It applies to entity.TypeProject.
	EntitlementCanDeleteNetworkACLs Entitlement = "can_delete_network_acls"

	// EntitlementNetworkZoneManager is the `network_zone_manager` Entitlement. It applies to entity.TypeProject.
	EntitlementNetworkZoneManager Entitlement = "network_zone_manager"

	// EntitlementCanCreateNetworkZones is the `can_create_network_zones` Entitlement. It applies to entity.TypeProject.
	EntitlementCanCreateNetworkZones Entitlement = "can_create_network_zones"

	// EntitlementCanViewNetworkZones is the `can_view_network_zones` Entitlement. It applies to entity.TypeProject.
	EntitlementCanViewNetworkZones Entitlement = "can_view_network_zones"

	// EntitlementCanEditNetworkZones is the `can_edit_network_zones` Entitlement. It applies to entity.TypeProject.
	EntitlementCanEditNetworkZones Entitlement = "can_edit_network_zones"

	// EntitlementCanDeleteNetworkZones is the `can_delete_network_zones` Entitlement. It applies to entity.TypeProject.
	EntitlementCanDeleteNetworkZones Entitlement = "can_delete_network_zones"

	// EntitlementProfileManager is the `profile_manager` Entitlement. It applies to entity.TypeProject.
	EntitlementProfileManager Entitlement = "profile_manager"

	// EntitlementCanCreateProfiles is the `can_create_profiles` Entitlement. It applies to entity.TypeProject.
	EntitlementCanCreateProfiles Entitlement = "can_create_profiles"

	// EntitlementCanViewProfiles is the `can_view_profiles` Entitlement. It applies to entity.TypeProject.
	EntitlementCanViewProfiles Entitlement = "can_view_profiles"

	// EntitlementCanEditProfiles is the `can_edit_profiles` Entitlement. It applies to entity.TypeProject.
	EntitlementCanEditProfiles Entitlement = "can_edit_profiles"

	// EntitlementCanDeleteProfiles is the `can_delete_profiles` Entitlement. It applies to entity.TypeProject.
	EntitlementCanDeleteProfiles Entitlement = "can_delete_profiles"

	// EntitlementStorageVolumeManager is the `storage_volume_manager` Entitlement. It applies to entity.TypeProject.
	EntitlementStorageVolumeManager Entitlement = "storage_volume_manager"

	// EntitlementCanCreateStorageVolumes is the `can_create_storage_volumes` Entitlement. It applies to entity.TypeProject.
	EntitlementCanCreateStorageVolumes Entitlement = "can_create_storage_volumes"

	// EntitlementCanViewStorageVolumes is the `can_view_storage_volumes` Entitlement. It applies to entity.TypeProject.
	EntitlementCanViewStorageVolumes Entitlement = "can_view_storage_volumes"

	// EntitlementCanEditStorageVolumes is the `can_edit_storage_volumes` Entitlement. It applies to entity.TypeProject.
	EntitlementCanEditStorageVolumes Entitlement = "can_edit_storage_volumes"

	// EntitlementCanDeleteStorageVolumes is the `can_delete_storage_volumes` Entitlement. It applies to entity.TypeProject.
	EntitlementCanDeleteStorageVolumes Entitlement = "can_delete_storage_volumes"

	// EntitlementStorageBucketManager is the `storage_bucket_manager` Entitlement. It applies to entity.TypeProject.
	EntitlementStorageBucketManager Entitlement = "storage_bucket_manager"

	// EntitlementCanCreateStorageBuckets is the `can_create_storage_buckets` Entitlement. It applies to entity.TypeProject.
	EntitlementCanCreateStorageBuckets Entitlement = "can_create_storage_buckets"

	// EntitlementCanViewStorageBuckets is the `can_view_storage_buckets` Entitlement. It applies to entity.TypeProject.
	EntitlementCanViewStorageBuckets Entitlement = "can_view_storage_buckets"

	// EntitlementCanEditStorageBuckets is the `can_edit_storage_buckets` Entitlement. It applies to entity.TypeProject.
	EntitlementCanEditStorageBuckets Entitlement = "can_edit_storage_buckets"

	// EntitlementCanDeleteStorageBuckets is the `can_delete_storage_buckets` Entitlement. It applies to entity.TypeProject.
	EntitlementCanDeleteStorageBuckets Entitlement = "can_delete_storage_buckets"

	// EntitlementCanViewOperations is the `can_view_operations` Entitlement. It applies to entity.TypeProject.
	EntitlementCanViewOperations Entitlement = "can_view_operations"

	// EntitlementCanViewEvents is the `can_view_events` Entitlement. It applies to entity.TypeProject.
	EntitlementCanViewEvents Entitlement = "can_view_events"

	// EntitlementInstanceUser is the `user` Entitlement. It applies to entity.TypeInstance.
	EntitlementInstanceUser Entitlement = "user"

	// EntitlementInstanceOperator is the `operator` Entitlement. It applies to entity.TypeInstance.
	EntitlementInstanceOperator Entitlement = "operator"

	// EntitlementCanUpdateState is the `can_update_state` Entitlement. It applies to entity.TypeInstance.
	EntitlementCanUpdateState Entitlement = "can_update_state"

	// EntitlementCanConnectSFTP is the `can_connect_sftp` Entitlement. It applies to entity.TypeInstance.
	EntitlementCanConnectSFTP Entitlement = "can_connect_sftp"

	// EntitlementCanAccessFiles is the `can_access_files` Entitlement. It applies to entity.TypeInstance.
	EntitlementCanAccessFiles Entitlement = "can_access_files"

	// EntitlementCanAccessConsole is the `can_access_console` Entitlement. It applies to entity.TypeInstance.
	EntitlementCanAccessConsole Entitlement = "can_access_console"

	// EntitlementCanExec is the `can_exec` Entitlement. It applies to entity.TypeInstance.
	EntitlementCanExec Entitlement = "can_exec"

	// EntitlementCanManageSnapshots is the `can_manage_snapshots` Entitlement. It applies to entity.TypeInstance and entity.TypeStorageVolume.
	EntitlementCanManageSnapshots Entitlement = "can_manage_snapshots"

	// EntitlementCanManageBackups is the `can_manage_backups` Entitlement. It applies to entity.TypeInstance and entity.TypeStorageVolume.
	EntitlementCanManageBackups Entitlement = "can_manage_backups"
)

var allEntitlements = []Entitlement{
	EntitlementCanView,
	EntitlementCanEdit,
	EntitlementCanDelete,
	EntitlementServerAdmin,
	EntitlementServerViewer,
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
	EntitlementInstanceUser,
	EntitlementInstanceOperator,
	EntitlementCanUpdateState,
	EntitlementCanConnectSFTP,
	EntitlementCanAccessFiles,
	EntitlementCanAccessConsole,
	EntitlementCanExec,
	EntitlementCanManageSnapshots,
	EntitlementCanManageBackups,
}

// Validate returns an error if the Entitlement is not recognised.
func Validate(e Entitlement) error {
	if !shared.ValueInSlice(e, allEntitlements) {
		return fmt.Errorf("Entitlement %q not defined", e)
	}

	return nil
}
