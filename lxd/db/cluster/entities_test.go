package cluster

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEntityStatementValidity(t *testing.T) {
	schema := Schema()
	db, err := schema.ExerciseUpdate(71, nil)
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
		urlByIDQuery := info.urlByIDQuery()
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
			middleQuery := middleEntityInfo.urlByIDQuery()
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
