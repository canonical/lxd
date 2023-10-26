package auth

// Entitlement is a type representation of a permission as it applies to a particular ObjectType.
type Entitlement string

const (
	// Entitlements that apply to all resources.
	EntitlementCanEdit Entitlement = "can_edit"
	EntitlementCanView Entitlement = "can_view"

	// Server entitlements.
	EntitlementCanCreateStoragePools               Entitlement = "can_create_storage_pools"
	EntitlementCanCreateProjects                   Entitlement = "can_create_projects"
	EntitlementCanViewResources                    Entitlement = "can_view_resources"
	EntitlementCanCreateCertificates               Entitlement = "can_create_certificates"
	EntitlementCanViewMetrics                      Entitlement = "can_view_metrics"
	EntitlementCanOverrideClusterTargetRestriction Entitlement = "can_override_cluster_target_restriction"
	EntitlementCanViewPrivilegedEvents             Entitlement = "can_view_privileged_events"

	// Project entitlements.
	EntitlementCanCreateImages         Entitlement = "can_create_images"
	EntitlementCanCreateImageAliases   Entitlement = "can_create_image_aliases"
	EntitlementCanCreateInstances      Entitlement = "can_create_instances"
	EntitlementCanCreateNetworks       Entitlement = "can_create_networks"
	EntitlementCanCreateNetworkACLs    Entitlement = "can_create_network_acls"
	EntitlementCanCreateNetworkZones   Entitlement = "can_create_network_zones"
	EntitlementCanCreateProfiles       Entitlement = "can_create_profiles"
	EntitlementCanCreateStorageVolumes Entitlement = "can_create_storage_volumes"
	EntitlementCanCreateStorageBuckets Entitlement = "can_create_storage_buckets"
	EntitlementCanViewOperations       Entitlement = "can_view_operations"
	EntitlementCanViewEvents           Entitlement = "can_view_events"
	EntitlementCanCreateDeployments    Entitlement = "can_create_deployments"

	// Instance entitlements.
	EntitlementCanUpdateState   Entitlement = "can_update_state"
	EntitlementCanConnectSFTP   Entitlement = "can_connect_sftp"
	EntitlementCanAccessFiles   Entitlement = "can_access_files"
	EntitlementCanAccessConsole Entitlement = "can_access_console"
	EntitlementCanExec          Entitlement = "can_exec"

	// Instance and storage volume entitlements.
	EntitlementCanManageSnapshots Entitlement = "can_manage_snapshots"
	EntitlementCanManageBackups   Entitlement = "can_manage_backups"

	// Deployment entitlements.
	EntitlementCanAccessDeploymentKeys   Entitlement = "can_access_deployment_keys"
	EntitlementCanCreateDeploymentKeys   Entitlement = "can_create_deployment_keys"
	EntitlementCanAccessDeploymentShapes Entitlement = "can_access_deployment_shapes"
	EntitlementCanCreateDeploymentShapes Entitlement = "can_create_deployment_shapes"

	// Deployment shape entitlements.
	EntitlementCanAccessDeployedInstances Entitlement = "can_access_deployed_instances"
	EntitlementCanDeployInstances         Entitlement = "can_deploy_instances"
)

// ObjectType is a type of resource within LXD.
type ObjectType string

const (
	ObjectTypeUser                    ObjectType = "user"
	ObjectTypeServer                  ObjectType = "server"
	ObjectTypeCertificate             ObjectType = "certificate"
	ObjectTypeStoragePool             ObjectType = "storage_pool"
	ObjectTypeProject                 ObjectType = "project"
	ObjectTypeImage                   ObjectType = "image"
	ObjectTypeImageAlias              ObjectType = "image_alias"
	ObjectTypeInstance                ObjectType = "instance"
	ObjectTypeNetwork                 ObjectType = "network"
	ObjectTypeNetworkACL              ObjectType = "network_acl"
	ObjectTypeNetworkZone             ObjectType = "network_zone"
	ObjectTypeProfile                 ObjectType = "profile"
	ObjectTypeStorageBucket           ObjectType = "storage_bucket"
	ObjectTypeStorageVolume           ObjectType = "storage_volume"
	ObjectTypeDeployment              ObjectType = "deployment"
	ObjectTypeDeploymentShape         ObjectType = "deployment_shape"
	ObjectTypeDeploymentShapeInstance ObjectType = "deployment_shape_instance"
	ObjectTypeDeploymentKey           ObjectType = "deployment_key"
)

// Permission is a type representation of general permission levels in LXD. Used with TLS and RBAC drivers.
type Permission string

const (
	PermissionAdmin                          Permission = "admin"
	PermissionView                           Permission = "view"
	PermissionManageProjects                 Permission = "manage-projects"
	PermissionManageInstances                Permission = "manage-containers"
	PermissionManageImages                   Permission = "manage-images"
	PermissionManageNetworks                 Permission = "manage-networks"
	PermissionManageProfiles                 Permission = "manage-profiles"
	PermissionManageStorageVolumes           Permission = "manage-storage-volumes"
	PermissionManageDeployments              Permission = "manage-deployments"
	PermissionManageDeploymentShapes         Permission = "manage-deployment-shapes"
	PermissionManageDeploymentKeys           Permission = "manage-deployment-keys"
	PermissionManageDeploymentShapeInstances Permission = "manage-deployment-shape-instances"
	PermissionOperateInstances               Permission = "operate-containers"
)

const (
	relationServer  = "server"
	relationProject = "project"
	relationUser    = "user"
)
