package cluster

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/entity"
)

func TestEntityStatementValidity(t *testing.T) {
	schema := Schema()
	db, err := schema.ExerciseUpdate(SchemaVersion, nil)
	require.NoError(t, err)

	for entityType, info := range entityTypes {
		allURLsQuery := info.allURLsQuery()
		if allURLsQuery == "" {
			continue
		}

		_, err := db.Prepare(allURLsQuery)
		assert.NoErrorf(t, err, "Entity statements %q (all): %v", entityType, err)
	}

	for entityType, info := range entityTypes {
		urlByIDQuery := info.urlsByIDsQuery(1)
		if urlByIDQuery == "" {
			continue
		}

		_, err := db.Prepare(urlByIDQuery)
		assert.NoErrorf(t, err, "Entity statements %q (by ID): %v", entityType, err)
	}

	for entityType, info := range entityTypes {
		urlsByProjectQuery := info.urlsByProjectQuery()
		if urlsByProjectQuery == "" {
			continue
		}

		_, err := db.Prepare(urlsByProjectQuery)
		assert.NoErrorf(t, err, "Entity statements %q (by project): %v", entityType, err)
	}

	for outerEntityType, outerEntityInfo := range entityTypes {
		outerQuery := outerEntityInfo.urlsByProjectQuery()
		if outerQuery == "" {
			continue
		}

		for middleEntityType, middleEntityInfo := range entityTypes {
			middleQuery := middleEntityInfo.urlsByIDsQuery(1)
			if middleQuery == "" {
				continue
			}

			for innerEntityType, innerEntityInfo := range entityTypes {
				innerQuery := innerEntityInfo.allURLsQuery()
				if innerQuery == "" {
					continue
				}

				unionQuery := strings.Join([]string{outerQuery, middleQuery, innerQuery}, " UNION ")
				_, err := db.Prepare(unionQuery)
				assert.NoErrorf(t, err, "Union statement (outer: %q; middle: %q; inner: %q): %v", outerEntityType, middleEntityType, innerEntityType, err)
			}
		}
	}

	for outerEntityType, outerEntityInfo := range entityTypes {
		outerQuery := outerEntityInfo.idFromURLQuery()
		if outerQuery == "" {
			continue
		}

		_, err := db.Prepare(outerQuery)
		assert.NoErrorf(t, err, "Entity ID from URL statement %q: %v", outerEntityType, err)
		for innerEntityType, innerEntityInfo := range entityTypes {
			innerQuery := innerEntityInfo.idFromURLQuery()
			if innerQuery == "" {
				continue
			}

			_, err = db.Prepare(strings.Join([]string{outerQuery, innerQuery}, " UNION "))
			assert.NoErrorf(t, err, "Union entity ID from URL statement (outer: %q; inner: %q): %v", outerEntityType, innerEntityType, err)
		}
	}
}

// TestEntityTypesCoversAllEntityTypes checks that every type in [entity.AllTypes] has a corresponding
// entry in the local entityTypes map. If a new entity type is added to shared/entity/type.go without
// a corresponding DB entry here, this test will fail.
func TestEntityTypesCoversAllEntityTypes(t *testing.T) {
	for _, entityType := range entity.AllTypes() {
		_, ok := entityTypes[entityType]
		assert.Truef(t, ok, "entity type %q is missing from lxd/db/cluster entityTypes map", entityType)
	}
}
