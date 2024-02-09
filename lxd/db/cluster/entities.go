package cluster

import (
	"database/sql/driver"
	"fmt"

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
