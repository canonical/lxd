package entity

import (
	"fmt"
)

// Type represents a resource type in LXD that is addressable via the API.
type Type string

// typeInfo represents common attributes an entity type must have.
//
// To create a new entity type, add a new const Type, then create a type that implements typeInfo and add it to the
// entityTypes map.
type typeInfo interface {
	// requiresProject returns whether the Type requires a project to be uniquely specified, e.g. true if it is project
	// specific, false if not.
	requiresProject() bool

	// requiresLocation() bool returns whether the Type requires a location to be uniquely specified, e.g. true if it is
	// node specific, false if not.
	requiresLocation() bool

	// path returns the API path for the resource. The pathPlaceholder constant should be used in place of mux variables.
	path() []string
}

// typeInfoCommon partially implements typeInfo and can be embedded in typeInfo
// implementations for convenience.
type typeInfoCommon struct{}

// requiresLocation returns false by default.
func (typeInfoCommon) requiresLocation() bool {
	return false
}

const (
	// TypeContainer represents container resources.
	TypeContainer Type = "container"

	// TypeImage represents image resources.
	TypeImage Type = "image"

	// TypeProfile represents profile resources.
	TypeProfile Type = "profile"

	// TypeProject represents project resources.
	TypeProject Type = "project"

	// TypeCertificate represents certificate resources.
	TypeCertificate Type = "certificate"

	// TypeInstance represents instance resources.
	TypeInstance Type = "instance"

	// TypeInstanceBackup represents instance backup resources.
	TypeInstanceBackup Type = "instance_backup"

	// TypeInstanceSnapshot represents instance snapshot resources.
	TypeInstanceSnapshot Type = "instance_snapshot"

	// TypeNetwork represents network resources.
	TypeNetwork Type = "network"

	// TypeNetworkACL represents network acl resources.
	TypeNetworkACL Type = "network_acl"

	// TypeClusterMember represents node resources.
	TypeClusterMember Type = "cluster_member"

	// TypeOperation represents operation resources.
	TypeOperation Type = "operation"

	// TypeStoragePool represents storage pool resources.
	TypeStoragePool Type = "storage_pool"

	// TypeStorageVolume represents storage volume resources.
	TypeStorageVolume Type = "storage_volume"

	// TypeStorageVolumeBackup represents storage volume backup resources.
	TypeStorageVolumeBackup Type = "storage_volume_backup"

	// TypeStorageVolumeSnapshot represents storage volume snapshot resources.
	TypeStorageVolumeSnapshot Type = "storage_volume_snapshot"

	// TypeWarning represents warning resources.
	TypeWarning Type = "warning"

	// TypeClusterGroup represents cluster group resources.
	TypeClusterGroup Type = "cluster_group"

	// TypeStorageBucket represents storage bucket resources.
	TypeStorageBucket Type = "storage_bucket"

	// TypeServer represents the top level /1.0 resource.
	TypeServer Type = "server"

	// TypeImageAlias represents image alias resources.
	TypeImageAlias Type = "image_alias"

	// TypeNetworkZone represents network zone resources.
	TypeNetworkZone Type = "network_zone"

	// TypeIdentity represents identity resources.
	TypeIdentity Type = "identity"

	// TypeAuthGroup represents authorization group resources.
	TypeAuthGroup Type = "group"

	// TypeIdentityProviderGroup represents identity provider group resources.
	TypeIdentityProviderGroup Type = "identity_provider_group"

	// TypePlacementGroup represents placement group resources.
	TypePlacementGroup Type = "placement_group"
)

const (
	// pathPlaceholder is used to indicate that a path argument is expected in a URL.
	pathPlaceholder = "{pathArgument}"
)

// String implements fmt.Stringer for Type.
func (t Type) String() string {
	return string(t)
}

// Validate returns an error if the Type is not in the list of allowed types. If the allowEmpty argument is set to true
// an empty string is allowed. This is to accommodate that warnings may not refer to a specific entity type.
func (t Type) Validate() error {
	_, ok := entityTypes[t]
	if !ok {
		return fmt.Errorf("Unknown entity type %q", t)
	}

	return nil
}

// RequiresProject returns true if an entity of the Type can only exist within the context of a project. Operations and
// warnings may still be project specific but it is not an absolute requirement.
func (t Type) RequiresProject() (bool, error) {
	err := t.Validate()
	if err != nil {
		return false, err
	}

	return entityTypes[t].requiresProject(), nil
}

// entityTypes is the source of truth for available entity types in LXD. This should never be modified at runtime.
var entityTypes = map[Type]typeInfo{
	TypeContainer:             container{},
	TypeImage:                 image{},
	TypeProfile:               profile{},
	TypeProject:               project{},
	TypeCertificate:           certificate{},
	TypeInstance:              instance{},
	TypeInstanceBackup:        instanceBackup{},
	TypeInstanceSnapshot:      instanceSnapshot{},
	TypeNetwork:               network{},
	TypeNetworkACL:            networkACL{},
	TypeClusterMember:         clusterMember{},
	TypeOperation:             operation{},
	TypeStoragePool:           storagePool{},
	TypeStorageVolume:         storageVolume{},
	TypeStorageVolumeBackup:   storageVolumeBackup{},
	TypeStorageVolumeSnapshot: storageVolumeSnapshot{},
	TypeWarning:               warning{},
	TypeClusterGroup:          clusterGroup{},
	TypeStorageBucket:         storageBucket{},
	TypeServer:                server{},
	TypeImageAlias:            imageAlias{},
	TypeNetworkZone:           networkZone{},
	TypeIdentity:              identity{},
	TypeAuthGroup:             authGroup{},
	TypeIdentityProviderGroup: identityProviderGroup{},
	TypePlacementGroup:        placementGroup{},
}

// metricsEntityTypes is the source of truth for which entity types can be used to categorize endpoints
// for the API metrics.
var metricsEntityTypes = []Type{
	TypeImage,
	TypeProfile,
	TypeProject,
	TypeCertificate,
	TypeInstance,
	TypeNetwork,
	TypeClusterMember,
	TypeOperation,
	TypeStoragePool,
	TypeWarning,
	TypeServer,
	TypeIdentity,
	TypePlacementGroup,
}

// APIMetricsEntityTypes returns the list of entity types relevant for the API metrics.
func APIMetricsEntityTypes() []Type {
	return metricsEntityTypes
}

type container struct {
	typeInfoCommon
}

func (container) requiresProject() bool {
	return true
}

func (container) path() []string {
	return []string{"containers", pathPlaceholder}
}

type image struct {
	typeInfoCommon
}

func (image) requiresProject() bool {
	return true
}

func (image) path() []string {
	return []string{"images", pathPlaceholder}
}

type profile struct {
	typeInfoCommon
}

func (profile) requiresProject() bool {
	return true
}

func (profile) path() []string {
	return []string{"profiles", pathPlaceholder}
}

type project struct {
	typeInfoCommon
}

func (project) requiresProject() bool {
	return false
}

func (project) path() []string {
	return []string{"projects", pathPlaceholder}
}

type certificate struct {
	typeInfoCommon
}

func (certificate) requiresProject() bool {
	return false
}

func (certificate) path() []string {
	return []string{"certificates", pathPlaceholder}
}

type instance struct {
	typeInfoCommon
}

func (instance) requiresProject() bool {
	return true
}

func (instance) path() []string {
	return []string{"instances", pathPlaceholder}
}

type instanceBackup struct {
	typeInfoCommon
}

func (instanceBackup) requiresProject() bool {
	return true
}

func (instanceBackup) path() []string {
	return []string{"instances", pathPlaceholder, "backups", pathPlaceholder}
}

type instanceSnapshot struct {
	typeInfoCommon
}

func (instanceSnapshot) requiresProject() bool {
	return true
}

func (instanceSnapshot) path() []string {
	return []string{"instances", pathPlaceholder, "snapshots", pathPlaceholder}
}

type network struct {
	typeInfoCommon
}

func (network) requiresProject() bool {
	return true
}

func (network) path() []string {
	return []string{"networks", pathPlaceholder}
}

type networkACL struct {
	typeInfoCommon
}

func (networkACL) requiresProject() bool {
	return true
}

func (networkACL) path() []string {
	return []string{"network-acls", pathPlaceholder}
}

type clusterMember struct {
	typeInfoCommon
}

func (clusterMember) requiresProject() bool {
	return false
}

func (clusterMember) path() []string {
	return []string{"cluster", "members", pathPlaceholder}
}

type operation struct {
	typeInfoCommon
}

func (operation) requiresProject() bool {
	return false
}

func (operation) path() []string {
	return []string{"operations", pathPlaceholder}
}

type storagePool struct {
	typeInfoCommon
}

func (storagePool) requiresProject() bool {
	return false
}

func (storagePool) path() []string {
	return []string{"storage-pools", pathPlaceholder}
}

type storageVolume struct{}

func (storageVolume) requiresProject() bool {
	return true
}

func (storageVolume) path() []string {
	return []string{"storage-pools", pathPlaceholder, "volumes", pathPlaceholder, pathPlaceholder}
}

func (storageVolume) requiresLocation() bool {
	return true
}

type storageVolumeBackup struct{}

func (storageVolumeBackup) requiresProject() bool {
	return true
}

func (storageVolumeBackup) path() []string {
	return []string{"storage-pools", pathPlaceholder, "volumes", pathPlaceholder, pathPlaceholder, "backups", pathPlaceholder}
}

func (storageVolumeBackup) requiresLocation() bool {
	return true
}

type storageVolumeSnapshot struct{}

func (storageVolumeSnapshot) requiresProject() bool {
	return true
}

func (storageVolumeSnapshot) path() []string {
	return []string{"storage-pools", pathPlaceholder, "volumes", pathPlaceholder, pathPlaceholder, "snapshots", pathPlaceholder}
}

func (storageVolumeSnapshot) requiresLocation() bool {
	return true
}

type warning struct {
	typeInfoCommon
}

func (warning) requiresProject() bool {
	return false
}

func (warning) path() []string {
	return []string{"warnings", pathPlaceholder}
}

type clusterGroup struct {
	typeInfoCommon
}

func (clusterGroup) requiresProject() bool {
	return false
}

func (clusterGroup) path() []string {
	return []string{"cluster", "groups", pathPlaceholder}
}

type storageBucket struct {
	typeInfoCommon
}

func (storageBucket) requiresProject() bool {
	return true
}

func (storageBucket) path() []string {
	return []string{"storage-pools", pathPlaceholder, "buckets", pathPlaceholder}
}

type server struct {
	typeInfoCommon
}

func (server) requiresProject() bool {
	return false
}

func (server) path() []string {
	return []string{}
}

type imageAlias struct {
	typeInfoCommon
}

func (imageAlias) requiresProject() bool {
	return true
}

func (imageAlias) path() []string {
	return []string{"images", "aliases", pathPlaceholder}
}

type networkZone struct {
	typeInfoCommon
}

func (networkZone) requiresProject() bool {
	return true
}

func (networkZone) path() []string {
	return []string{"network-zones", pathPlaceholder}
}

type identity struct {
	typeInfoCommon
}

func (identity) requiresProject() bool {
	return false
}

func (identity) path() []string {
	return []string{"auth", "identities", pathPlaceholder, pathPlaceholder}
}

type authGroup struct {
	typeInfoCommon
}

func (authGroup) requiresProject() bool {
	return false
}

func (authGroup) path() []string {
	return []string{"auth", "groups", pathPlaceholder}
}

type identityProviderGroup struct {
	typeInfoCommon
}

func (identityProviderGroup) requiresProject() bool {
	return false
}

func (identityProviderGroup) path() []string {
	return []string{"auth", "identity-provider-groups", pathPlaceholder}
}

type placementGroup struct {
	typeInfoCommon
}

func (placementGroup) requiresProject() bool {
	return true
}

func (placementGroup) path() []string {
	return []string{"placement-groups", pathPlaceholder}
}
