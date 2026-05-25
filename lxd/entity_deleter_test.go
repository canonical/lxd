package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/entity"
)

// TestGetEntityDeleterCoversAllProjectEntityTypes checks that every entity type
// that requires a project has a corresponding deleter registered in
// getEntityDeleter. If a new project-level entity type is added without a
// deleter, this test will fail.
func TestGetEntityDeleterCoversAllProjectEntityTypes(t *testing.T) {
	// All known entity types. Update this list when new types are added to
	// shared/entity/type.go.
	allTypes := []entity.Type{
		entity.TypeContainer,
		entity.TypeImage,
		entity.TypeProfile,
		entity.TypeProject,
		entity.TypeCertificate,
		entity.TypeInstance,
		entity.TypeInstanceBackup,
		entity.TypeInstanceSnapshot,
		entity.TypeNetwork,
		entity.TypeNetworkACL,
		entity.TypeClusterMember,
		entity.TypeStoragePool,
		entity.TypeStorageVolume,
		entity.TypeStorageVolumeBackup,
		entity.TypeStorageVolumeSnapshot,
		entity.TypeClusterGroup,
		entity.TypeStorageBucket,
		entity.TypeServer,
		entity.TypeImageAlias,
		entity.TypeNetworkZone,
		entity.TypeIdentity,
		entity.TypeAuthGroup,
		entity.TypeIdentityProviderGroup,
		entity.TypePlacementGroup,
		entity.TypeClusterLink,
		entity.TypeReplicator,
	}

	// These project-level entity types are intentionally excluded: they are
	// sub-entities that are deleted implicitly when their parent is deleted,
	// so no top-level deleter is needed.
	noDeleterNeeded := map[entity.Type]bool{
		entity.TypeContainer:             true, // alias for TypeInstance; deleted via instanceDeleter
		entity.TypeInstanceBackup:        true, // deleted when instance is deleted
		entity.TypeInstanceSnapshot:      true, // deleted when instance is deleted
		entity.TypeStorageVolumeBackup:   true, // deleted when volume is deleted
		entity.TypeStorageVolumeSnapshot: true, // deleted when volume is deleted
		entity.TypeImageAlias:            true, // deleted when image is deleted
	}

	for _, entityType := range allTypes {
		requiresProject, err := entityType.RequiresProject()
		require.NoError(t, err, "entity type %q failed RequiresProject", entityType)

		if !requiresProject || noDeleterNeeded[entityType] {
			continue
		}

		_, err = getEntityDeleter(entityType)
		assert.NoError(t, err, "entity type %q requires a project but has no deleter registered in getEntityDeleter", entityType)
	}
}
