package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// EntityType is a database representation of an entity type.
//
// EntityType is defined on string so that entity.Type constants can be converted by casting. The sql.Scanner and
// driver.Valuer interfaces are implemented on this type such that the string constants are converted into their int64
// counterparts as they are written to the database, or converted back into an EntityType as they are read from the
// database. It is not possible to read/write invalid entity types from/to the database when using this type.
type EntityType string

const (
	entityTypeNone                  int64 = -1
	entityTypeContainer             int64 = 0
	entityTypeImage                 int64 = 1
	entityTypeProfile               int64 = 2
	entityTypeProject               int64 = 3
	entityTypeCertificate           int64 = 4
	entityTypeInstance              int64 = 5
	entityTypeInstanceBackup        int64 = 6
	entityTypeInstanceSnapshot      int64 = 7
	entityTypeNetwork               int64 = 8
	entityTypeNetworkACL            int64 = 9
	entityTypeNode                  int64 = 10
	entityTypeOperation             int64 = 11
	entityTypeStoragePool           int64 = 12
	entityTypeStorageVolume         int64 = 13
	entityTypeStorageVolumeBackup   int64 = 14
	entityTypeStorageVolumeSnapshot int64 = 15
	entityTypeWarning               int64 = 16
	entityTypeClusterGroup          int64 = 17
	entityTypeStorageBucket         int64 = 18
	entityTypeNetworkZone           int64 = 19
	entityTypeImageAlias            int64 = 20
	entityTypeServer                int64 = 21
	entityTypeAuthGroup             int64 = 22
	entityTypeIdentityProviderGroup int64 = 23
	entityTypeIdentity              int64 = 24
)

// Scan implements sql.Scanner for EntityType. This converts the integer value back into the correct entity.Type
// constant or returns an error.
func (e *EntityType) Scan(value any) error {
	// Always expect null values to be coalesced into entityTypeNone (-1).
	if value == nil {
		return fmt.Errorf("Entity type cannot be null")
	}

	intValue, err := driver.Int32.ConvertValue(value)
	if err != nil {
		return fmt.Errorf("Invalid entity type `%v`: %w", value, err)
	}

	entityTypeInt, ok := intValue.(int64)
	if !ok {
		return fmt.Errorf("Entity should be an integer, got `%v` (%T)", intValue, intValue)
	}

	switch entityTypeInt {
	case entityTypeNone:
		*e = ""
	case entityTypeContainer:
		*e = EntityType(entity.TypeContainer)
	case entityTypeImage:
		*e = EntityType(entity.TypeImage)
	case entityTypeProfile:
		*e = EntityType(entity.TypeProfile)
	case entityTypeProject:
		*e = EntityType(entity.TypeProject)
	case entityTypeCertificate:
		*e = EntityType(entity.TypeCertificate)
	case entityTypeInstance:
		*e = EntityType(entity.TypeInstance)
	case entityTypeInstanceBackup:
		*e = EntityType(entity.TypeInstanceBackup)
	case entityTypeInstanceSnapshot:
		*e = EntityType(entity.TypeInstanceSnapshot)
	case entityTypeNetwork:
		*e = EntityType(entity.TypeNetwork)
	case entityTypeNetworkACL:
		*e = EntityType(entity.TypeNetworkACL)
	case entityTypeNode:
		*e = EntityType(entity.TypeNode)
	case entityTypeOperation:
		*e = EntityType(entity.TypeOperation)
	case entityTypeStoragePool:
		*e = EntityType(entity.TypeStoragePool)
	case entityTypeStorageVolume:
		*e = EntityType(entity.TypeStorageVolume)
	case entityTypeStorageVolumeBackup:
		*e = EntityType(entity.TypeStorageVolumeBackup)
	case entityTypeStorageVolumeSnapshot:
		*e = EntityType(entity.TypeStorageVolumeSnapshot)
	case entityTypeWarning:
		*e = EntityType(entity.TypeWarning)
	case entityTypeClusterGroup:
		*e = EntityType(entity.TypeClusterGroup)
	case entityTypeStorageBucket:
		*e = EntityType(entity.TypeStorageBucket)
	case entityTypeNetworkZone:
		*e = EntityType(entity.TypeNetworkZone)
	case entityTypeImageAlias:
		*e = EntityType(entity.TypeImageAlias)
	case entityTypeServer:
		*e = EntityType(entity.TypeServer)
	case entityTypeAuthGroup:
		*e = EntityType(entity.TypeAuthGroup)
	case entityTypeIdentityProviderGroup:
		*e = EntityType(entity.TypeIdentityProviderGroup)
	case entityTypeIdentity:
		*e = EntityType(entity.TypeIdentity)
	default:
		return fmt.Errorf("Unknown entity type %d", entityTypeInt)
	}

	return nil
}

// Value implements driver.Valuer for EntityType. This converts the EntityType into an integer or throws an error.
func (e EntityType) Value() (driver.Value, error) {
	switch e {
	case "":
		return entityTypeNone, nil
	case EntityType(entity.TypeContainer):
		return entityTypeContainer, nil
	case EntityType(entity.TypeImage):
		return entityTypeImage, nil
	case EntityType(entity.TypeProfile):
		return entityTypeProfile, nil
	case EntityType(entity.TypeProject):
		return entityTypeProject, nil
	case EntityType(entity.TypeCertificate):
		return entityTypeCertificate, nil
	case EntityType(entity.TypeInstance):
		return entityTypeInstance, nil
	case EntityType(entity.TypeInstanceBackup):
		return entityTypeInstanceBackup, nil
	case EntityType(entity.TypeInstanceSnapshot):
		return entityTypeInstanceSnapshot, nil
	case EntityType(entity.TypeNetwork):
		return entityTypeNetwork, nil
	case EntityType(entity.TypeNetworkACL):
		return entityTypeNetworkACL, nil
	case EntityType(entity.TypeNode):
		return entityTypeNode, nil
	case EntityType(entity.TypeOperation):
		return entityTypeOperation, nil
	case EntityType(entity.TypeStoragePool):
		return entityTypeStoragePool, nil
	case EntityType(entity.TypeStorageVolume):
		return entityTypeStorageVolume, nil
	case EntityType(entity.TypeStorageVolumeBackup):
		return entityTypeStorageVolumeBackup, nil
	case EntityType(entity.TypeStorageVolumeSnapshot):
		return entityTypeStorageVolumeSnapshot, nil
	case EntityType(entity.TypeWarning):
		return entityTypeWarning, nil
	case EntityType(entity.TypeClusterGroup):
		return entityTypeClusterGroup, nil
	case EntityType(entity.TypeStorageBucket):
		return entityTypeStorageBucket, nil
	case EntityType(entity.TypeNetworkZone):
		return entityTypeNetworkZone, nil
	case EntityType(entity.TypeImageAlias):
		return entityTypeImageAlias, nil
	case EntityType(entity.TypeServer):
		return entityTypeServer, nil
	case EntityType(entity.TypeAuthGroup):
		return entityTypeAuthGroup, nil
	case EntityType(entity.TypeIdentityProviderGroup):
		return entityTypeIdentityProviderGroup, nil
	case EntityType(entity.TypeIdentity):
		return entityTypeIdentity, nil
	default:
		return nil, fmt.Errorf("Unknown entity type %q", e)
	}
}

/*
The following queries return the information required for generating a unique URL of an entity in a common format.
Each row returned by all of these queries has the following format:
 1. Entity type. Including the entity type in the result allows for querying multiple entity types at once by performing
    a UNION of two or more queries.
 2. Entity ID. The caller will likely have an entity type and an ID that they are trying to get a URL for (see warnings
    API/table). In other cases the caller may want to list all URLs of a particular type, so returning the ID along with
    the URL allows for subsequent mapping or usage.
 3. The project name (empty if the entity is not project specific).
 4. The location (target) of the entity. Some entities require this parameter for uniqueness (e.g. storage volumes and buckets).
 5. Path arguments that comprise the URL of the entity. These are returned as a JSON array in the order that they appear
    in the URL.
*/

// containerEntities returns all entities of type entity.TypeContainer. These are just instance entities with an addition type constraint.
var containerEntities = fmt.Sprintf(`%s WHERE instances.type = %d`, instanceEntities, instancetype.Container)

// containerEntityByID gets the entity of type entity.TypeContainer with a particular ID.
var containerEntityByID = fmt.Sprintf(`%s AND instances.type = %d`, instanceEntityByID, instancetype.Container)

// containerEntitiesByProjectName returns all entities of type entity.TypeContainer in a particular project.
var containerEntitiesByProjectName = fmt.Sprintf(`%s AND instances.type = %d`, instanceEntitiesByProjectName, instancetype.Container)

// imageEntities returns all entities of type entity.TypeImage.
var imageEntities = fmt.Sprintf(`SELECT %d, images.id, projects.name, '', json_array(images.fingerprint) FROM images JOIN projects ON images.project_id = projects.id`, entityTypeImage)

// imageEntities gets the entity of type entity.TypeImage with a particular ID.
var imageEntityByID = fmt.Sprintf(`%s WHERE images.id = ?`, imageEntities)

// imageEntities returns all entities of type entity.TypeImage in a particular project.
var imageEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, imageEntities)

// profileEntities returns all entities of type entity.TypeProfile.
var profileEntities = fmt.Sprintf(`SELECT %d, profiles.id, projects.name, '', json_array(profiles.name) FROM profiles JOIN projects ON profiles.project_id = projects.id`, entityTypeProfile)

// profileEntities gets the entity of type entity.TypeProfile with a particular ID.
var profileEntityByID = fmt.Sprintf(`%s WHERE profiles.id = ?`, profileEntities)

// profileEntities returns all entities of type entity.TypeProfile in a particular project.
var profileEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, profileEntities)

// projectEntities returns all entities of type entity.TypeProject.
var projectEntities = fmt.Sprintf(`SELECT %d, projects.id, '', '', json_array(projects.name) FROM projects`, entityTypeProject)

// projectEntities gets the entity of type entity.TypeProject with a particular ID.
var projectEntityByID = fmt.Sprintf(`%s WHERE id = ?`, projectEntities)

// certificateEntities returns all entities of type entity.TypeCertificate.
var certificateEntities = fmt.Sprintf(`SELECT %d, identities.id, '', '', json_array(identities.identifier) FROM identities WHERE auth_method = %d`, entityTypeCertificate, authMethodTLS)

// certificateEntities gets the entity of type entity.TypeCertificate with a particular ID.
var certificateEntityByID = fmt.Sprintf(`%s AND identities.id = ?`, certificateEntities)

// instanceEntities returns all entities of type entity.TypeInstance.
var instanceEntities = fmt.Sprintf(`SELECT %d, instances.id, projects.name, '', json_array(instances.name) FROM instances JOIN projects ON instances.project_id = projects.id`, entityTypeInstance)

// instanceEntities gets the entity of type entity.TypeInstance with a particular ID.
var instanceEntityByID = fmt.Sprintf(`%s WHERE instances.id = ?`, instanceEntities)

// instanceEntities returns all entities of type entity.TypeInstance in a particular project.
var instanceEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, instanceEntities)

// instanceBackupEntities returns all entities of type entity.TypeInstanceBackup.
var instanceBackupEntities = fmt.Sprintf(`SELECT %d, instances_backups.id, projects.name, '', json_array(instances.name, instances_backups.name) FROM instances_backups JOIN instances ON instances_backups.instance_id = instances.id JOIN projects ON instances.project_id = projects.id`, entityTypeInstanceBackup)

// instanceBackupEntities gets the entity of type entity.TypeInstanceBackup with a particular ID.
var instanceBackupEntityByID = fmt.Sprintf(`%s WHERE instances_backups.id = ?`, instanceBackupEntities)

// instanceBackupEntities returns all entities of type entity.TypeInstanceBackup in a particular project.
var instanceBackupEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, instanceBackupEntities)

// instanceSnapshotEntities returns all entities of type entity.TypeInstanceSnapshot.
var instanceSnapshotEntities = fmt.Sprintf(`SELECT %d, instances_snapshots.id, projects.name, '', json_array(instances.name, instances_snapshots.name) FROM instances_snapshots JOIN instances ON instances_snapshots.instance_id = instances.id JOIN projects ON instances.project_id = projects.id`, entityTypeInstanceBackup)

// instanceSnapshotEntities gets the entity of type entity.TypeInstanceSnapshot with a particular ID.
var instanceSnapshotEntityByID = fmt.Sprintf(`%s WHERE instances_snapshots.id = ?`, instanceSnapshotEntities)

// instanceSnapshotEntities returns all entities of type entity.TypeInstanceSnapshot in a particular project.
var instanceSnapshotEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, instanceSnapshotEntities)

// networkEntities returns all entities of type entity.TypeNetwork.
var networkEntities = fmt.Sprintf(`SELECT %d, networks.id, projects.name, '', json_array(networks.name) FROM networks JOIN projects ON networks.project_id = projects.id`, entityTypeNetwork)

// networkEntities gets the entity of type entity.TypeNetwork with a particular ID.
var networkEntityByID = fmt.Sprintf(`%s WHERE networks.id = ?`, networkEntities)

// networkEntities returns all entities of type entity.TypeNetwork in a particular project.
var networkEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, networkEntities)

// networkACLEntities returns all entities of type entity.TypeNetworkACL.
var networkACLEntities = fmt.Sprintf(`SELECT %d, networks_acls.id, projects.name, '', json_array(networks_acls.name) FROM networks_acls JOIN projects ON networks_acls.project_id = projects.id`, entityTypeNetworkACL)

// networkACLEntities gets the entity of type entity.TypeNetworkACL with a particular ID.
var networkACLEntityByID = fmt.Sprintf(`%s WHERE networks_acls.id = ?`, networkACLEntities)

// networkACLEntities returns all entities of type entity.TypeNetworkACL in a particular project.
var networkACLEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, networkACLEntities)

// networkZoneEntities returns all entities of type entity.TypeNetworkZone.
var networkZoneEntities = fmt.Sprintf(`SELECT %d, networks_zones.id, projects.name, '', json_array(networks_zones.name) FROM networks_zones JOIN projects ON networks_zones.project_id = projects.id`, entityTypeNetworkZone)

// networkZoneEntityByID gets the entity of type entity.TypeNetworkZone with a particular ID.
var networkZoneEntityByID = fmt.Sprintf(`%s WHERE networks_zones.id = ?`, networkZoneEntities)

// networkZoneEntitiesByProjectName returns all entities of type entity.TypeNetworkZone in a particular project.
var networkZoneEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, networkZoneEntities)

// imageAliasEntities returns all entities of type entity.TypeImageAlias.
var imageAliasEntities = fmt.Sprintf(`SELECT %d, images_aliases.id, projects.name, '', json_array(images_aliases.name) FROM images_aliases JOIN projects ON images_aliases.project_id = projects.id`, entityTypeImageAlias)

// imageAliasEntityByID gets the entity of type entity.TypeImageAlias with a particular ID.
var imageAliasEntityByID = fmt.Sprintf(`%s WHERE images_aliases.id = ?`, imageAliasEntities)

// imageAliasEntitiesByProjectName returns all the entities of type entity.TypeImageAlias in a particular project.
var imageAliasEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, imageAliasEntities)

// nodeEntities returns all entities of type entity.TypeNode.
var nodeEntities = fmt.Sprintf(`SELECT %d, nodes.id, '', '', json_array(nodes.name) FROM nodes`, entityTypeNode)

// nodeEntities gets the entity of type entity.TypeNode with a particular ID.
var nodeEntityByID = fmt.Sprintf(`%s WHERE nodes.id = ?`, nodeEntities)

// operationEntities returns all entities of type entity.TypeOperation.
var operationEntities = fmt.Sprintf(`SELECT %d, operations.id, coalesce(projects.name, ''), '', json_array(operations.uuid) FROM operations LEFT JOIN projects ON operations.project_id = projects.id`, entityTypeOperation)

// operationEntities gets the entity of type entity.TypeOperation with a particular ID.
var operationEntityByID = fmt.Sprintf(`%s WHERE operations.id = ?`, operationEntities)

// operationEntities returns all entities of type entity.TypeOperation in a particular project.
var operationEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, strings.Replace(operationEntities, "LEFT JOIN projects", "JOIN projects", 1))

// storagePoolEntities returns all entities of type entity.TypeStoragePool.
var storagePoolEntities = fmt.Sprintf(`SELECT %d, storage_pools.id, '', '', json_array(storage_pools.name) FROM storage_pools`, entityTypeStoragePool)

// storagePoolEntities gets the entity of type entity.TypeStoragePool with a particular ID.
var storagePoolEntityByID = fmt.Sprintf(`%s WHERE storage_pools.id = ?`, storagePoolEntities)

// storageVolumeEntities returns all entities of type entity.TypeStorageVolume.
var storageVolumeEntities = fmt.Sprintf(`
SELECT 
	%d, 
	storage_volumes.id, 
	projects.name, 
	replace(coalesce(nodes.name, ''), 'none', ''), 
	json_array(
		storage_pools.name,
		CASE storage_volumes.type
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
		END,
		storage_volumes.name
	)
FROM storage_volumes
	JOIN projects ON storage_volumes.project_id = projects.id
	JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
	LEFT JOIN nodes ON storage_volumes.node_id = nodes.id
`,
	entityTypeStorageVolume,
	StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeNameContainer,
	StoragePoolVolumeTypeImage, StoragePoolVolumeTypeNameImage,
	StoragePoolVolumeTypeCustom, StoragePoolVolumeTypeNameCustom,
	StoragePoolVolumeTypeVM, StoragePoolVolumeTypeNameVM,
)

// storageVolumeEntities gets the entity of type entity.TypeStorageVolume with a particular ID.
var storageVolumeEntityByID = fmt.Sprintf(`%s WHERE storage_volumes.id = ?`, storageVolumeEntities)

// storageVolumeEntities returns all entities of type entity.TypeStorageVolume in a particular project.
var storageVolumeEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, storageVolumeEntities)

// storageVolumeBackupEntities returns all entities of type entity.TypeStorageVolumeBackup.
var storageVolumeBackupEntities = fmt.Sprintf(`
SELECT 
	%d, 
	storage_volumes_backups.id, 
	projects.name, 
	replace(coalesce(nodes.name, ''), 'none', ''), 
	json_array(
		storage_pools.name,
		CASE storage_volumes.type
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
		END,
		storage_volumes.name,
		storage_volumes_backups.name
	)
FROM storage_volumes_backups
	JOIN storage_volumes ON storage_volumes_backups.storage_volume_id = storage_volumes.id
	JOIN projects ON storage_volumes.project_id = projects.id
	JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
	LEFT JOIN nodes ON storage_volumes.node_id = nodes.id
`,
	entityTypeStorageVolumeBackup,
	StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeNameContainer,
	StoragePoolVolumeTypeImage, StoragePoolVolumeTypeNameImage,
	StoragePoolVolumeTypeCustom, StoragePoolVolumeTypeNameCustom,
	StoragePoolVolumeTypeVM, StoragePoolVolumeTypeNameVM,
)

// storageVolumeBackupEntities gets the entity of type entity.TypeStorageVolumeBackup with a particular ID.
var storageVolumeBackupEntityByID = fmt.Sprintf(`%s WHERE storage_volumes_backups.id = ?`, storageVolumeBackupEntities)

// storageVolumeBackupEntities returns all entities of type entity.TypeStorageVolumeBackup in a particular project.
var storageVolumeBackupEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, storageVolumeBackupEntities)

// storageVolumeSnapshotEntities returns all entities of type entity.TypeStorageVolumeSnapshot.
var storageVolumeSnapshotEntities = fmt.Sprintf(`
SELECT 
	%d, 
	storage_volumes_snapshots.id, 
	projects.name, 
	replace(coalesce(nodes.name, ''), 'none', ''), 
	json_array(
		storage_pools.name,
		CASE storage_volumes.type
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
		END,
		storage_volumes.name,
		storage_volumes_snapshots.name
	)
FROM storage_volumes_snapshots
	JOIN storage_volumes ON storage_volumes_snapshots.storage_volume_id = storage_volumes.id
	JOIN projects ON storage_volumes.project_id = projects.id
	JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
	LEFT JOIN nodes ON storage_volumes.node_id = nodes.id
`,
	entityTypeStorageVolumeSnapshot,
	StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeNameContainer,
	StoragePoolVolumeTypeImage, StoragePoolVolumeTypeNameImage,
	StoragePoolVolumeTypeCustom, StoragePoolVolumeTypeNameCustom,
	StoragePoolVolumeTypeVM, StoragePoolVolumeTypeNameVM,
)

// storageVolumeSnapshotEntities gets the entity of type entity.TypeStorageVolumeSnapshot with a particular ID.
var storageVolumeSnapshotEntityByID = fmt.Sprintf(`%s WHERE storage_volumes_snapshots.id = ?`, storageVolumeSnapshotEntities)

// storageVolumeSnapshotEntities returns all entities of type entity.TypeStorageVolumeSnapshot in a particular project.
var storageVolumeSnapshotEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, storageVolumeSnapshotEntities)

// warningEntities returns all entities of type entity.TypeWarning.
var warningEntities = fmt.Sprintf(`SELECT %d, warnings.id, coalesce(projects.name, ''), replace(coalesce(nodes.name, ''), 'none', ''), json_array(warnings.uuid) FROM warnings LEFT JOIN projects ON warnings.project_id = projects.id LEFT JOIN nodes ON warnings.node_id = nodes.id`, entityTypeWarning)

// warningEntities gets the entity of type entity.TypeWarning with a particular ID.
var warningEntityByID = fmt.Sprintf(`%s WHERE warnings.id = ?`, warningEntities)

// warningEntities returns all entities of type entity.TypeWarning in a particular project.
var warningEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, strings.Replace(warningEntities, "LEFT JOIN projects", "JOIN projects", 1))

// clusterGroupEntities returns all entities of type entity.TypeClusterGroup.
var clusterGroupEntities = fmt.Sprintf(`SELECT %d, cluster_groups.id, '', '', json_array(cluster_groups.name) FROM cluster_groups`, entityTypeClusterGroup)

// clusterGroupEntities gets the entity of type entity.TypeClusterGroup with a particular ID.
var clusterGroupEntityByID = fmt.Sprintf(`%s WHERE cluster_groups.id = ?`, clusterGroupEntities)

// storageBucketEntities returns all entities of type entity.TypeStorageBucket.
var storageBucketEntities = fmt.Sprintf(`
SELECT %d, storage_buckets.id, projects.name, replace(coalesce(nodes.name, ''), 'none', ''), json_array(storage_pools.name, storage_buckets.name)
FROM storage_buckets
	JOIN projects ON storage_buckets.project_id = projects.id
	JOIN storage_pools ON storage_buckets.storage_pool_id = storage_pools.id
	LEFT JOIN nodes ON storage_buckets.node_id = nodes.id
`, entityTypeStorageBucket,
)

// storageBucketEntityByID gets the entity of type entity.TypeStorageBucket with a particular ID.
var storageBucketEntityByID = fmt.Sprintf(`%s WHERE storage_buckets.id = ?`, storageBucketEntities)

// storageBucketEntities returns all entities of type entity.TypeStorageBucket in a particular project.
var storageBucketEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, storageBucketEntities)

// authGroupEntities returns all entities of type entity.TypeGroup.
var authGroupEntities = fmt.Sprintf(`SELECT %d, auth_groups.id, '', '', json_array(auth_groups.name) FROM auth_groups`, entityTypeAuthGroup)

// authGroupEntityByID gets the entity of type entity.TypeGroup with a particular ID.
var authGroupEntityByID = fmt.Sprintf(`%s WHERE auth_groups.id = ?`, authGroupEntities)

// identityProviderGroupEntities returns all entities of type entity.TypeIdentityProviderGroup.
var identityProviderGroupEntities = fmt.Sprintf(`SELECT %d, identity_provider_groups.id, '', '', json_array(identity_provider_groups.name) FROM identity_provider_groups`, entityTypeIdentityProviderGroup)

// identityProviderGroupByEntityID gets the entity of type entity.TypeIdentityProviderGroup with a particular ID.
var identityProviderGroupEntityByID = fmt.Sprintf(`%s WHERE identity_provider_groups.id = ?`, identityProviderGroupEntities)

// identityEntities returns all entities of type entity.TypeIdentity.
var identityEntities = fmt.Sprintf(`
SELECT 
	%d, 
	identities.id, 
	'', 
	'', 
	json_array(
		CASE identities.auth_method
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
		END,
		identities.identifier
	) 
FROM identities
`,
	entityTypeIdentity,
	authMethodTLS, api.AuthenticationMethodTLS,
	authMethodOIDC, api.AuthenticationMethodOIDC,
)

// identityEntityByID gets the entity of type entity.TypeIdentity with a particular ID.
var identityEntityByID = fmt.Sprintf(`%s WHERE identities.id = ?`, identityEntities)

// entityStatementsAll is a map of entity type to the statement which queries for all URL information for entities of that type.
var entityStatementsAll = map[entity.Type]string{
	entity.TypeContainer:             containerEntities,
	entity.TypeImage:                 imageEntities,
	entity.TypeProfile:               profileEntities,
	entity.TypeProject:               projectEntities,
	entity.TypeCertificate:           certificateEntities,
	entity.TypeInstance:              instanceEntities,
	entity.TypeInstanceBackup:        instanceBackupEntities,
	entity.TypeInstanceSnapshot:      instanceSnapshotEntities,
	entity.TypeNetwork:               networkEntities,
	entity.TypeNetworkACL:            networkACLEntities,
	entity.TypeNode:                  nodeEntities,
	entity.TypeOperation:             operationEntities,
	entity.TypeStoragePool:           storagePoolEntities,
	entity.TypeStorageVolume:         storageVolumeEntities,
	entity.TypeStorageVolumeBackup:   storageVolumeBackupEntities,
	entity.TypeStorageVolumeSnapshot: storageVolumeSnapshotEntities,
	entity.TypeWarning:               warningEntities,
	entity.TypeClusterGroup:          clusterGroupEntities,
	entity.TypeStorageBucket:         storageBucketEntities,
	entity.TypeImageAlias:            imageAliasEntities,
	entity.TypeNetworkZone:           networkZoneEntities,
	entity.TypeAuthGroup:             authGroupEntities,
	entity.TypeIdentityProviderGroup: identityProviderGroupEntities,
	entity.TypeIdentity:              identityEntities,
}

// entityStatementsByID is a map of entity type to the statement which queries for all URL information for a single entity of that type with a given ID.
var entityStatementsByID = map[entity.Type]string{
	entity.TypeContainer:             containerEntityByID,
	entity.TypeImage:                 imageEntityByID,
	entity.TypeProfile:               profileEntityByID,
	entity.TypeProject:               projectEntityByID,
	entity.TypeCertificate:           certificateEntityByID,
	entity.TypeInstance:              instanceEntityByID,
	entity.TypeInstanceBackup:        instanceBackupEntityByID,
	entity.TypeInstanceSnapshot:      instanceSnapshotEntityByID,
	entity.TypeNetwork:               networkEntityByID,
	entity.TypeNetworkACL:            networkACLEntityByID,
	entity.TypeNode:                  nodeEntityByID,
	entity.TypeOperation:             operationEntityByID,
	entity.TypeStoragePool:           storagePoolEntityByID,
	entity.TypeStorageVolume:         storageVolumeEntityByID,
	entity.TypeStorageVolumeBackup:   storageVolumeBackupEntityByID,
	entity.TypeStorageVolumeSnapshot: storageVolumeSnapshotEntityByID,
	entity.TypeWarning:               warningEntityByID,
	entity.TypeClusterGroup:          clusterGroupEntityByID,
	entity.TypeStorageBucket:         storageBucketEntityByID,
	entity.TypeImageAlias:            imageAliasEntityByID,
	entity.TypeNetworkZone:           networkZoneEntityByID,
	entity.TypeAuthGroup:             authGroupEntityByID,
	entity.TypeIdentityProviderGroup: identityProviderGroupEntityByID,
	entity.TypeIdentity:              identityEntityByID,
}

// entityStatementsByProjectName is a map of entity type to the statement which queries for all URL information for all entities of that type within a given project.
var entityStatementsByProjectName = map[entity.Type]string{
	entity.TypeContainer:             containerEntitiesByProjectName,
	entity.TypeImage:                 imageEntitiesByProjectName,
	entity.TypeProfile:               profileEntitiesByProjectName,
	entity.TypeInstance:              instanceEntitiesByProjectName,
	entity.TypeInstanceBackup:        instanceBackupEntitiesByProjectName,
	entity.TypeInstanceSnapshot:      instanceSnapshotEntitiesByProjectName,
	entity.TypeNetwork:               networkEntitiesByProjectName,
	entity.TypeNetworkACL:            networkACLEntitiesByProjectName,
	entity.TypeOperation:             operationEntitiesByProjectName,
	entity.TypeStorageVolume:         storageVolumeEntitiesByProjectName,
	entity.TypeStorageVolumeBackup:   storageVolumeBackupEntitiesByProjectName,
	entity.TypeStorageVolumeSnapshot: storageVolumeSnapshotEntitiesByProjectName,
	entity.TypeWarning:               warningEntitiesByProjectName,
	entity.TypeStorageBucket:         storageBucketEntitiesByProjectName,
	entity.TypeImageAlias:            imageAliasEntitiesByProjectName,
	entity.TypeNetworkZone:           networkZoneEntitiesByProjectName,
}

// EntityRef represents the expected format of entity URL queries.
type EntityRef struct {
	EntityType  EntityType
	EntityID    int
	ProjectName string
	Location    string
	PathArgs    []string
}

// scan accepts a scanning function (e.g. `(*sql.Row).Scan`) and uses it to parse the row and set its fields.
func (e *EntityRef) scan(scan func(dest ...any) error) error {
	var pathArgs string
	err := scan(&e.EntityType, &e.EntityID, &e.ProjectName, &e.Location, &pathArgs)
	if err != nil {
		return fmt.Errorf("Failed to scan entity URL: %w", err)
	}

	err = json.Unmarshal([]byte(pathArgs), &e.PathArgs)
	if err != nil {
		return fmt.Errorf("Failed to unmarshal entity URL path arguments: %w", err)
	}

	return nil
}

// getURL is a convenience for generating a URL from the EntityRef.
func (e *EntityRef) getURL() (*api.URL, error) {
	u, err := entity.Type(e.EntityType).URL(e.ProjectName, e.Location, e.PathArgs...)
	if err != nil {
		return nil, fmt.Errorf("Failed to create entity URL: %w", err)
	}

	return u, nil
}

// GetEntityURL returns the *api.URL of a single entity by its type and ID.
func GetEntityURL(ctx context.Context, tx *sql.Tx, entityType entity.Type, entityID int) (*api.URL, error) {
	if entityType == entity.TypeServer {
		return entity.ServerURL(), nil
	}

	stmt, ok := entityStatementsByID[entityType]
	if !ok {
		return nil, fmt.Errorf("Could not get entity URL: No statement found for entity type %q", entityType)
	}

	row := tx.QueryRowContext(ctx, stmt, entityID)
	entityRef := &EntityRef{}
	err := entityRef.scan(row.Scan)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("Failed to scan entity URL: %w", err)
	} else if err != nil {
		return nil, api.StatusErrorf(http.StatusNotFound, "No entity found with id `%d` and type %q", entityID, entityType)
	}

	return entityRef.getURL()
}

// GetEntityURLs accepts a project name and a variadic of entity types and returns a map of entity.Type to map of entity ID, to *api.URL.
// This method combines the above queries into a single query using the UNION operator. If no entity types are given, this function will
// return URLs for all entity types. If no project name is given, this function will return URLs for all projects. This may result in
// stupendously large queries, so use with caution!
func GetEntityURLs(ctx context.Context, tx *sql.Tx, projectName string, entityTypes ...entity.Type) (map[entity.Type]map[int]*api.URL, error) { //nolint:unused // This will be used in a forthcoming feature.
	var stmts []string
	var args []any
	result := make(map[entity.Type]map[int]*api.URL)

	// If the server entity type is in the list of entity types, or if we are getting all entity types and
	// not filtering by project, we need to add a server URL to the result. The entity ID of the server entity type is
	// always zero.
	if shared.ValueInSlice(entity.TypeServer, entityTypes) || (len(entityTypes) == 0 && projectName == "") {
		result[entity.TypeServer] = map[int]*api.URL{0: entity.ServerURL()}

		// Return early if there are no other entity types in the list (no queries to execute).
		if len(entityTypes) == 1 {
			return result, nil
		}
	}

	// Collate all the statements we need.
	// If the project is not empty, each statement will need an argument for the project name.
	// Additionally, pre-populate the result map as we know the entity types in advance (this is so that we don't have
	// to check and assign on each loop iteration when scanning rows).
	if len(entityTypes) == 0 && projectName == "" {
		for entityType, stmt := range entityStatementsAll {
			stmts = append(stmts, stmt)
			result[entityType] = make(map[int]*api.URL)
		}
	} else if len(entityTypes) == 0 && projectName != "" {
		for entityType, stmt := range entityStatementsByProjectName {
			stmts = append(stmts, stmt)
			args = append(args, projectName)
			result[entityType] = make(map[int]*api.URL)
		}
	} else if projectName == "" {
		for _, entityType := range entityTypes {
			// We've already added the server url to the result.
			if entityType == entity.TypeServer {
				continue
			}

			stmt, ok := entityStatementsAll[entityType]
			if !ok {
				return nil, fmt.Errorf("Could not get entity URLs: No statement found for entity type %q", entityType)
			}

			stmts = append(stmts, stmt)
			result[entityType] = make(map[int]*api.URL)
		}
	} else {
		for _, entityType := range entityTypes {
			// We've already added the server url to the result.
			if entityType == entity.TypeServer {
				continue
			}

			stmt, ok := entityStatementsByProjectName[entityType]
			if !ok {
				return nil, fmt.Errorf("Could not get entity URLs: No statement found for entity type %q", entityType)
			}

			stmts = append(stmts, stmt)
			args = append(args, projectName)
			result[entityType] = make(map[int]*api.URL)
		}
	}

	// Join into a single statement with UNION and query.
	stmt := strings.Join(stmts, " UNION ")
	rows, err := tx.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed to perform entity URL query: %w", err)
	}

	for rows.Next() {
		entityRef := &EntityRef{}
		err := entityRef.scan(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("Failed to scan entity URL: %w", err)
		}

		u, err := entityRef.getURL()
		if err != nil {
			return nil, err
		}

		result[entity.Type(entityRef.EntityType)][entityRef.EntityID] = u
	}

	return result, nil
}

/*
The following queries return the ID of an entity by the information contained in its unique URL in a common format.
These queries are not used in isolation, they are used together as part of a larger UNION query.
Because of this, all of the below queries expect as arguments the project name, the location, and the path arguments of
the URL.
Some entity types don't require a project name or location, so that's why they explicitly check for an empty project
name or location being passed in.
Additionally, all of the queries accept an index number as their first binding so that the results can be correlated in
the calling function (see PopulateEntityReferencesFromURLs below).

TODO: We could avoid a query snippet per entity by making these snippets support multiple entities for a single entity type.
(e.g. `WHERE projects.name IN (?, ...) AND instances.name IN (?, ...)` we'd need to be very careful!).
*/

// containerIDFromURL gets the ID of a container from its URL.
var containerIDFromURL = fmt.Sprintf(`
SELECT ?, instances.id 
FROM instances 
JOIN projects ON instances.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND instances.name = ? 
	AND instances.type = %d
`, instancetype.Container)

// imageIDFromURL gets the ID of an image from its URL.
var imageIDFromURL = `
SELECT ?, images.id 
FROM images 
JOIN projects ON images.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND images.fingerprint = ?`

// profileIDFromURL gets the ID of a profile from its URL.
var profileIDFromURL = `
SELECT ?, profiles.id 
FROM profiles 
JOIN projects ON profiles.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND profiles.name = ?`

// projectIDFromURL gets the ID of a project from its URL.
var projectIDFromURL = `
SELECT ?, projects.id 
FROM projects 
WHERE '' = ? 
	AND '' = ? 
	AND projects.name = ?`

// certificateIDFromURL gets the ID of a certificate from its URL.
var certificateIDFromURL = fmt.Sprintf(`
SELECT ?, identities.id 
FROM identities 
WHERE '' = ? 
	AND '' = ? 
	AND identities.identifier = ? 
	AND identities.auth_method = %d
`, authMethodTLS)

// instanceIDFromURL gets the ID of an instance from its URL.
var instanceIDFromURL = `
SELECT ?, instances.id 
FROM instances 
JOIN projects ON instances.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND instances.name = ?`

// instanceBackupIDFromURL gets the ID of an instance backup from its URL.
var instanceBackupIDFromURL = `
SELECT ?, instances_backups.id 
FROM instances_backups 
JOIN instances ON instances_backups.instance_id = instances.id 
JOIN projects ON instances.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND instances.name = ? 
	AND instances_backups.name = ?`

// instanceSnapshotIDFromURL gets the ID of an instance snapshot from its URL.
var instanceSnapshotIDFromURL = `
SELECT ?, instances_snapshots.id 
FROM instances_snapshots 
JOIN instances ON instances_snapshots.instance_id = instances.id 
JOIN projects ON instances.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND instances.name = ? 
	AND instances_snapshots.name = ?`

// networkIDFromURL gets the ID of a network from its URL.
var networkIDFromURL = `
SELECT ?, networks.id 
FROM networks 
JOIN projects ON networks.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND networks.name = ?`

// networkACLIDFromURL gets the ID of a network ACL from its URL.
var networkACLIDFromURL = `
SELECT ?, networks_acls.id 
FROM networks_acls 
JOIN projects ON networks_acls.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND networks_acls.name = ?`

// nodeIDFromURL gets the ID of a node from its URL.
var nodeIDFromURL = `
SELECT ?, nodes.id 
FROM nodes 
WHERE '' = ? 
	AND '' = ? 
	AND nodes.name = ?`

// operationIDFromURL gets the ID of an operation from its URL.
var operationIDFromURL = `
SELECT ?, operations.id 
FROM operations 
LEFT JOIN projects ON operations.project_id = projects.id 
WHERE coalesce(projects.name, '') = ? 
	AND '' = ? 
	AND operations.uuid = ?`

// storagePoolIDFromURL gets the ID of a storage pool from its URL.
var storagePoolIDFromURL = `
SELECT ?, storage_pools.id 
FROM storage_pools 
WHERE '' = ? 
	AND '' = ? 
	AND storage_pools.name = ?`

// storageVolumeIDFromURL gets the ID of a storage volume from its URL.
var storageVolumeIDFromURL = fmt.Sprintf(`
SELECT ?, storage_volumes.id 
FROM storage_volumes
JOIN projects ON storage_volumes.project_id = projects.id
JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
LEFT JOIN nodes ON storage_volumes.node_id = nodes.id
WHERE projects.name = ? 
	AND replace(coalesce(nodes.name, ''), 'none', '') = ? 
	AND storage_pools.name = ? 
	AND CASE storage_volumes.type 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
	END = ? 
	AND storage_volumes.name = ?
`, StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeNameContainer,
	StoragePoolVolumeTypeImage, StoragePoolVolumeTypeNameImage,
	StoragePoolVolumeTypeCustom, StoragePoolVolumeTypeNameCustom,
	StoragePoolVolumeTypeVM, StoragePoolVolumeTypeNameVM)

// storageVolumeBackupIDFromURL gets the ID of a storageVolumeBackup from its URL.
var storageVolumeBackupIDFromURL = fmt.Sprintf(`
SELECT ?, storage_volumes_backups.id 
FROM storage_volumes_backups
JOIN storage_volumes ON storage_volumes_backups.storage_volume_id = storage_volumes.id
JOIN projects ON storage_volumes.project_id = projects.id
JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
LEFT JOIN nodes ON storage_volumes.node_id = nodes.id
WHERE projects.name = ? 
	AND replace(coalesce(nodes.name, ''), 'none', '') = ? 
	AND storage_pools.name = ? 
	AND CASE storage_volumes.type 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
	END = ? 
	AND storage_volumes.name = ? 
	AND storage_volumes_backups.name = ?
`, StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeNameContainer,
	StoragePoolVolumeTypeImage, StoragePoolVolumeTypeNameImage,
	StoragePoolVolumeTypeCustom, StoragePoolVolumeTypeNameCustom,
	StoragePoolVolumeTypeVM, StoragePoolVolumeTypeNameVM)

// storageVolumeSnapshotIDFromURL gets the ID of a storageVolumeSnapshot from its URL.
var storageVolumeSnapshotIDFromURL = fmt.Sprintf(`
SELECT ?, storage_volumes_snapshots.id 
FROM storage_volumes_snapshots
JOIN storage_volumes ON storage_volumes_snapshots.storage_volume_id = storage_volumes.id
JOIN projects ON storage_volumes.project_id = projects.id
JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
LEFT JOIN nodes ON storage_volumes.node_id = nodes.id
WHERE projects.name = ? 
	AND replace(coalesce(nodes.name, ''), 'none', '') = ? 
	AND storage_pools.name = ? 
	AND CASE storage_volumes.type
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
	END = ? 
	AND storage_volumes.name = ? 
	AND storage_volumes_snapshots.name = ?
`, StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeNameContainer, StoragePoolVolumeTypeImage, StoragePoolVolumeTypeNameImage, StoragePoolVolumeTypeCustom, StoragePoolVolumeTypeNameCustom, StoragePoolVolumeTypeVM, StoragePoolVolumeTypeNameVM)

// warningIDFromURL gets the ID of a warning from its URL.
var warningIDFromURL = `
SELECT ?, warnings.id 
FROM warnings 
LEFT JOIN projects ON warnings.project_id = projects.id 
WHERE coalesce(projects.name, '') = ? 
	AND '' = ? 
	AND warnings.uuid = ?`

// clusterGroupIDFromURL gets the ID of a clusterGroup from its URL.
var clusterGroupIDFromURL = `
SELECT ?, cluster_groups.id 
FROM cluster_groups 
WHERE '' = ? 
	AND '' = ? 
	AND cluster_groups.name = ?`

// storageBucketIDFromURL gets the ID of a storageBucket from its URL.
var storageBucketIDFromURL = `
SELECT ?, storage_buckets.id 
FROM storage_buckets
JOIN projects ON storage_buckets.project_id = projects.id
JOIN storage_pools ON storage_buckets.storage_pool_id = storage_pools.id
LEFT JOIN nodes ON storage_buckets.node_id = nodes.id
WHERE projects.name = ? 
	AND replace(coalesce(nodes.name, ''), 'none', '') = ? 
	AND storage_pools.name = ? 
	AND storage_buckets.name = ?
`

// networkZoneIDFromURL gets the ID of a networkZone from its URL.
var networkZoneIDFromURL = `
SELECT ?, networks_zones.id 
FROM networks_zones 
JOIN projects ON networks_zones.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND networks_zones.name = ?`

// imageAliasIDFromURL gets the ID of a imageAlias from its URL.
var imageAliasIDFromURL = `
SELECT ?, images_aliases.id 
FROM images_aliases 
JOIN projects ON images_aliases.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND images_aliases.name = ? `

// authGroupIDFromURL gets the ID of a group from its URL.
var authGroupIDFromURL = `
SELECT ?, auth_groups.id 
FROM auth_groups 
WHERE '' = ? 
	AND '' = ? 
	AND auth_groups.name = ?`

// identityProviderGroupIDFromURL gets the ID of a identityProviderGroup from its URL.
var identityProviderGroupIDFromURL = `
SELECT ?, identity_provider_groups.id 
FROM identity_provider_groups 
WHERE '' = ? 
	AND '' = ? 
	AND identity_provider_groups.name = ?`

// identityIDFromURL gets the ID of a identity from its URL.
var identityIDFromURL = fmt.Sprintf(`
SELECT ?, identities.id 
FROM identities 
WHERE '' = ? 
	AND '' = ? 
	AND CASE identities.auth_method 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
	END = ? 
	AND identities.identifier = ?
`, authMethodTLS, api.AuthenticationMethodTLS,
	authMethodOIDC, api.AuthenticationMethodOIDC)

// identityIDFromURLStatements is a map of entity.Type to a statement that can be used to get the ID of the entity from its URL.
var entityIDFromURLStatements = map[entity.Type]string{
	entity.TypeContainer:             containerIDFromURL,
	entity.TypeImage:                 imageIDFromURL,
	entity.TypeProfile:               profileIDFromURL,
	entity.TypeProject:               projectIDFromURL,
	entity.TypeCertificate:           certificateIDFromURL,
	entity.TypeInstance:              instanceIDFromURL,
	entity.TypeInstanceBackup:        instanceBackupIDFromURL,
	entity.TypeInstanceSnapshot:      instanceSnapshotIDFromURL,
	entity.TypeNetwork:               networkIDFromURL,
	entity.TypeNetworkACL:            networkACLIDFromURL,
	entity.TypeNode:                  nodeIDFromURL,
	entity.TypeOperation:             operationIDFromURL,
	entity.TypeStoragePool:           storagePoolIDFromURL,
	entity.TypeStorageVolume:         storageVolumeIDFromURL,
	entity.TypeStorageVolumeBackup:   storageVolumeBackupIDFromURL,
	entity.TypeStorageVolumeSnapshot: storageVolumeSnapshotIDFromURL,
	entity.TypeWarning:               warningIDFromURL,
	entity.TypeClusterGroup:          clusterGroupIDFromURL,
	entity.TypeStorageBucket:         storageBucketIDFromURL,
	entity.TypeImageAlias:            imageAliasIDFromURL,
	entity.TypeNetworkZone:           networkZoneIDFromURL,
	entity.TypeAuthGroup:             authGroupIDFromURL,
	entity.TypeIdentityProviderGroup: identityProviderGroupIDFromURL,
	entity.TypeIdentity:              identityIDFromURL,
}

// PopulateEntityReferencesFromURLs populates the values in the given map with entity references corresponding to the api.URL keys.
// It will return an error if any of the given URLs do not correspond to a LXD entity.
func PopulateEntityReferencesFromURLs(ctx context.Context, tx *sql.Tx, entityURLMap map[*api.URL]*EntityRef) error {
	// If the input list is empty, nothing to do.
	if len(entityURLMap) == 0 {
		return nil
	}

	entityURLs := make([]*api.URL, 0, len(entityURLMap))
	for entityURL := range entityURLMap {
		entityURLs = append(entityURLs, entityURL)
	}

	stmts := make([]string, 0, len(entityURLs))
	var args []any
	for i, entityURL := range entityURLs {
		// Parse the URL to get the majority of the fields of the EntityRef for that URL.
		entityType, projectName, location, pathArgs, err := entity.ParseURL(entityURL.URL)
		if err != nil {
			return fmt.Errorf("Failed to get entity IDs from URLs: %w", err)
		}

		// Populate the result map.
		entityURLMap[entityURL] = &EntityRef{
			EntityType:  EntityType(entityType),
			ProjectName: projectName,
			Location:    location,
			PathArgs:    pathArgs,
		}

		// If the given URL is the server url it is valid but there is no need to perform a query for it, the entity
		// ID of the server is always zero (by virtue of being the zero value for int).
		if entityType == entity.TypeServer {
			continue
		}

		// Get the statement corresponding to the entity type.
		stmt, ok := entityIDFromURLStatements[entityType]
		if !ok {
			return fmt.Errorf("Could not get entity IDs from URLs: No statement found for entity type %q", entityType)
		}

		// Each statement accepts an identifier for the query, the project name, the location, and all path arguments as arguments.
		// In this case we can use the index of the url from the argument slice as an identifier.
		stmts = append(stmts, stmt)
		args = append(args, i, projectName, location)
		for _, pathArg := range pathArgs {
			args = append(args, pathArg)
		}
	}

	// If the only argument was a server URL we don't have any statements to execute.
	if len(stmts) == 0 {
		return nil
	}

	// Join the statements with a union and execute.
	stmt := strings.Join(stmts, " UNION ")
	rows, err := tx.QueryContext(ctx, stmt, args...)
	if err != nil {
		return fmt.Errorf("Failed to get entityIDs from URLS: %w", err)
	}

	for rows.Next() {
		var rowID, entityID int
		err = rows.Scan(&rowID, &entityID)
		if err != nil {
			return fmt.Errorf("Failed to get entityIDs from URLS: %w", err)
		}

		if rowID >= len(entityURLs) {
			return fmt.Errorf("Failed to get entityIDs from URLS: Internal error, returned row ID greater than number of URLs")
		}

		// Using the row ID, get the *api.URL from the argument slice, then use it as a key in our result map to get the *EntityRef.
		entityRef, ok := entityURLMap[entityURLs[rowID]]
		if !ok {
			return fmt.Errorf("Failed to get entityIDs from URLS: Internal error, entity URL missing from result object")
		}

		// Set the value of the EntityID in the *EntityRef.
		entityRef.EntityID = entityID
	}

	err = rows.Err()
	if err != nil {
		return fmt.Errorf("Failed to get entity IDs from URLs: %w", err)
	}

	// Check that all given URLs have been resolved to an ID.
	for u, ref := range entityURLMap {
		if ref.EntityID == 0 && ref.EntityType != EntityType(entity.TypeServer) {
			return fmt.Errorf("Failed to find entity ID for URL %q", u.String())
		}
	}

	return nil
}

// GetEntityReferenceFromURL gets a single EntityRef by parsing the given api.URL and finding the ID of the entity.
// It is used by the OpenFGA datastore implementation to find permissions for the entity with the given URL.
func GetEntityReferenceFromURL(ctx context.Context, tx *sql.Tx, entityURL *api.URL) (*EntityRef, error) {
	// Parse the URL to get the majority of the fields of the EntityRef for that URL.
	entityType, projectName, location, pathArgs, err := entity.ParseURL(entityURL.URL)
	if err != nil {
		return nil, fmt.Errorf("Failed to get entity ID from URL: %w", err)
	}

	// Populate the fields we know from the URL.
	entityRef := &EntityRef{
		EntityType:  EntityType(entityType),
		ProjectName: projectName,
		Location:    location,
		PathArgs:    pathArgs,
	}

	// If the given URL is the server url it is valid but there is no need to perform a query for it, the entity
	// ID of the server is always zero (by virtue of being the zero value for int).
	if entityType == entity.TypeServer {
		return entityRef, nil
	}

	// Get the statement corresponding to the entity type.
	stmt, ok := entityIDFromURLStatements[entityType]
	if !ok {
		return nil, fmt.Errorf("Could not get entity ID from URL: No statement found for entity type %q", entityType)
	}

	// The first bind argument in all entityIDFromURL queries is an index that we use to correspond output of large UNION
	// queries (see PopulateEntityReferencesFromURLs). In this case we are only querying for one ID, so the `0` argument
	// is a placeholder.
	args := []any{0, projectName, location}
	for _, pathArg := range pathArgs {
		args = append(args, pathArg)
	}

	row := tx.QueryRowContext(ctx, stmt, args...)

	var rowID, entityID int
	err = row.Scan(&rowID, &entityID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, api.StatusErrorf(http.StatusNotFound, "No such entity %q", entityURL.String())
		}

		return nil, fmt.Errorf("Failed to get entityID from URL: %w", err)
	}

	entityRef.EntityID = entityID

	return entityRef, nil
}

var entityDeletionTriggers = map[entity.Type]string{
	entity.TypeImage:                 imageDeletionTrigger,
	entity.TypeProfile:               profileDeletionTrigger,
	entity.TypeProject:               projectDeletionTrigger,
	entity.TypeInstance:              instanceDeletionTrigger,
	entity.TypeInstanceBackup:        instanceBackupDeletionTrigger,
	entity.TypeInstanceSnapshot:      instanceSnapshotDeletionTrigger,
	entity.TypeNetwork:               networkDeletionTrigger,
	entity.TypeNetworkACL:            networkACLDeletionTrigger,
	entity.TypeNode:                  nodeDeletionTrigger,
	entity.TypeOperation:             operationDeletionTrigger,
	entity.TypeStoragePool:           storagePoolDeletionTrigger,
	entity.TypeStorageVolume:         storageVolumeDeletionTrigger,
	entity.TypeStorageVolumeBackup:   storageVolumeBackupDeletionTrigger,
	entity.TypeStorageVolumeSnapshot: storageVolumeSnapshotDeletionTrigger,
	entity.TypeWarning:               warningDeletionTrigger,
	entity.TypeClusterGroup:          clusterGroupDeletionTrigger,
	entity.TypeStorageBucket:         storageBucketDeletionTrigger,
	entity.TypeImageAlias:            imageAliasDeletionTrigger,
	entity.TypeNetworkZone:           networkZoneDeletionTrigger,
	entity.TypeAuthGroup:             authGroupDeletionTrigger,
	entity.TypeIdentityProviderGroup: identityProviderGroupDeletionTrigger,
	entity.TypeIdentity:              identityDeletionTrigger,
}

// imageDeletionTrigger deletes any permissions or warnings associated with an image when it is deleted.
var imageDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_image_delete;
CREATE TRIGGER on_image_delete
	AFTER DELETE ON images
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeImage, entityTypeImage)

// profileDeletionTrigger deletes any permissions or warnings associated with a profile when it is deleted.
var profileDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_profile_delete;
CREATE TRIGGER on_profile_delete
	AFTER DELETE ON profiles
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeProfile, entityTypeProfile)

// projectDeletionTrigger deletes any permissions or warnings associated with a project when it is deleted.
var projectDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_project_delete;
CREATE TRIGGER on_project_delete
	AFTER DELETE ON projects
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeProject, entityTypeProject)

// instanceDeletionTrigger deletes any permissions or warnings associated with an instance when it is deleted.
var instanceDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_instance_delete;
CREATE TRIGGER on_instance_delete
	AFTER DELETE ON instances
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeInstance, entityTypeInstance)

// instanceBackupDeletionTrigger deletes any permissions or warnings associated with an instance backup when it is deleted.
var instanceBackupDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_instance_backup_delete;
CREATE TRIGGER on_instance_backup_delete
	AFTER DELETE ON instances_backups
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeInstanceBackup, entityTypeInstanceBackup)

// instanceSnapshotDeletionTrigger deletes any permissions or warnings associated with an instance snapshot when it is deleted.
var instanceSnapshotDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_instance_snaphot_delete;
CREATE TRIGGER on_instance_snaphot_delete
	AFTER DELETE ON instances_snapshots
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeInstanceSnapshot, entityTypeInstanceSnapshot)

// networkDeletionTrigger deletes any permissions or warnings associated with a network when it is deleted.
var networkDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_network_delete;
CREATE TRIGGER on_network_delete
	AFTER DELETE ON networks
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeNetwork, entityTypeNetwork)

// networkACLDeletionTrigger deletes any permissions or warnings associated with a network ACL when it is deleted.
var networkACLDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_network_acl_delete;
CREATE TRIGGER on_network_acl_delete
	AFTER DELETE ON networks_acls
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeNetworkACL, entityTypeNetworkACL)

// nodeDeletionTrigger deletes any permissions or warnings associated with a node when it is deleted.
var nodeDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_node_delete;
CREATE TRIGGER on_node_delete
	AFTER DELETE ON nodes
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeNode, entityTypeNode)

// operationDeletionTrigger deletes any permissions or warnings associated with an operation when it is deleted.
var operationDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_operation_delete;
CREATE TRIGGER on_operation_delete
	AFTER DELETE ON operations
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeOperation, entityTypeOperation)

// storagePoolDeletionTrigger deletes any permissions or warnings associated with a storage pool when it is deleted.
var storagePoolDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_storage_pool_delete;
CREATE TRIGGER on_storage_pool_delete
	AFTER DELETE ON storage_pools
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeStoragePool, entityTypeStoragePool)

// storageVolumeDeletionTrigger deletes any permissions or warnings associated with a storage volume when it is deleted.
var storageVolumeDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_storage_volume_delete;
CREATE TRIGGER on_storage_volume_delete
	AFTER DELETE ON storage_volumes
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeStorageVolume, entityTypeStorageVolume)

// storageVolumeBackupDeletionTrigger deletes any permissions or warnings associated with a storage volume backup when it is deleted.
var storageVolumeBackupDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_storage_volume_backup_delete;
CREATE TRIGGER on_storage_volume_backup_delete
	AFTER DELETE ON storage_volumes_backups
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeStorageVolumeBackup, entityTypeStorageVolumeBackup)

// storageVolumeSnapshotDeletionTrigger deletes any permissions or warnings associated with a storage volume snapshot when it is deleted.
var storageVolumeSnapshotDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_storage_volume_snapshot_delete;
CREATE TRIGGER on_storage_volume_snapshot_delete
	AFTER DELETE ON storage_volumes_snapshots
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeStorageVolumeSnapshot, entityTypeStorageVolumeSnapshot)

// warningDeletionTrigger deletes any permissions associated with a warning when it is deleted.
var warningDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_warning_delete;
CREATE TRIGGER on_warning_delete
	AFTER DELETE ON warnings
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	END
`, entityTypeWarning)

// clusterGroupDeletionTrigger deletes any permissions or warnings associated with a cluster group when it is deleted.
var clusterGroupDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_cluster_group_delete;
CREATE TRIGGER on_cluster_group_delete
	AFTER DELETE ON cluster_groups
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeClusterGroup, entityTypeClusterGroup)

// storageBucketDeletionTrigger deletes any permissions or warnings associated with a storage bucket when it is deleted.
var storageBucketDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_storage_bucket_delete;
CREATE TRIGGER on_storage_bucket_delete
	AFTER DELETE ON storage_buckets
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeStorageBucket, entityTypeStorageBucket)

// networkZoneDeletionTrigger deletes any permissions or warnings associated with a network zone when it is deleted.
var networkZoneDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_network_zone_delete;
CREATE TRIGGER on_network_zone_delete
	AFTER DELETE ON networks_zones
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeNetworkZone, entityTypeNetworkZone)

// imageAliasDeletionTrigger deletes any permissions or warnings associated with an image alias when it is deleted.
var imageAliasDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_image_alias_delete;
CREATE TRIGGER on_image_alias_delete
	AFTER DELETE ON images_aliases
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeImageAlias, entityTypeImageAlias)

// authGroupDeletionTrigger deletes any warnings associated with an auth group when it is deleted. Permissions are
// related to auth groups via foreign key and will have already been deleted.
var authGroupDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_auth_group_delete;
CREATE TRIGGER on_auth_group_delete
	AFTER DELETE ON auth_groups
	BEGIN
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeAuthGroup)

// identityProviderGroupDeletionTrigger deletes any permissions or warnings associated with an identity provider group when it is deleted.
var identityProviderGroupDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_identity_provider_group_delete;
CREATE TRIGGER on_identity_provider_group_delete
	AFTER DELETE ON identity_provider_groups
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeIdentityProviderGroup, entityTypeIdentityProviderGroup)

// identityDeletionTrigger deletes any permissions or warnings associated with an identity when it is deleted.
var identityDeletionTrigger = fmt.Sprintf(`
DROP TRIGGER IF EXISTS on_identity_delete;
CREATE TRIGGER on_identity_delete
	AFTER DELETE ON identities
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, entityTypeIdentity, entityTypeIdentity)
