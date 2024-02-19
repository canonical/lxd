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
var profileEntitiesByProjectName = fmt.Sprintf(`%s WHERE profiles.id = ?`, profileEntities)

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

// storageBucketEntities gets the entity of type entity.TypeStorageBucket with a particular ID.
var storageBucketEntityByID = fmt.Sprintf(`%s WHERE storage_buckets.id = ?`, storageBucketEntities)

// storageBucketEntities returns all entities of type entity.TypeStorageBucket in a particular project.
var storageBucketEntitiesByProjectName = fmt.Sprintf(`%s WHERE projects.name = ?`, storageBucketEntities)

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
}

// entityRef represents the expected format of entity URL queries.
type entityRef struct {
	entityType  EntityType
	entityID    int
	projectName string
	location    string
	pathArgs    []string
}

// scan accepts a scanning function (e.g. `(*sql.Row).Scan`) and uses it to parse the row and set its fields.
func (e *entityRef) scan(scan func(dest ...any) error) error {
	var pathArgs string
	err := scan(&e.entityType, &e.entityID, &e.projectName, &e.location, &pathArgs)
	if err != nil {
		return fmt.Errorf("Failed to scan entity URL: %w", err)
	}

	err = json.Unmarshal([]byte(pathArgs), &e.pathArgs)
	if err != nil {
		return fmt.Errorf("Failed to unmarshal entity URL path arguments: %w", err)
	}

	return nil
}

// getURL is a convenience for generating a URL from the entityRef.
func (e *entityRef) getURL() (*api.URL, error) {
	u, err := entity.Type(e.entityType).URL(e.projectName, e.location, e.pathArgs...)
	if err != nil {
		return nil, fmt.Errorf("Failed to create entity URL: %w", err)
	}

	return u, nil
}

// GetEntityURL returns the *api.URL of a single entity by its type and ID.
func GetEntityURL(ctx context.Context, tx *sql.Tx, entityType entity.Type, entityID int) (*api.URL, error) {
	stmt, ok := entityStatementsByID[entityType]
	if !ok {
		return nil, fmt.Errorf("Could not get entity URL: No statement found for entity type %q", entityType)
	}

	row := tx.QueryRowContext(ctx, stmt, entityID)
	if errors.Is(row.Err(), sql.ErrNoRows) {
		return nil, api.StatusErrorf(http.StatusNotFound, "No entity found with id `%d` and type %q", entityID, entityType)
	}

	entityRef := &entityRef{}
	err := entityRef.scan(row.Scan)
	if err != nil {
		return nil, fmt.Errorf("Failed to scan entity URL: %w", err)
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
			stmt, ok := entityStatementsAll[entityType]
			if !ok {
				return nil, fmt.Errorf("Could not get entity URLs: No statement found for entity type %q", entityType)
			}

			stmts = append(stmts, stmt)
			result[entityType] = make(map[int]*api.URL)
		}
	} else {
		for _, entityType := range entityTypes {
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
		entityRef := &entityRef{}
		err := entityRef.scan(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("Failed to scan entity URL: %w", err)
		}

		u, err := entityRef.getURL()
		if err != nil {
			return nil, err
		}

		result[entity.Type(entityRef.entityType)][entityRef.entityID] = u
	}

	return result, nil
}
