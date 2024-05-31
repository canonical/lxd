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

	for _, entityType := range entityTypes {
		stmt := entityType.AllURLsQuery()
		if stmt != "" {
			_, err := db.Prepare(stmt)
			assert.NoErrorf(t, err, "Entity statements %q (all): %v", entityType, err)
		}
	}

	for _, entityType := range entityTypes {
		stmt := entityType.URLByIDQuery()
		if stmt != "" {
			_, err := db.Prepare(stmt)
			assert.NoErrorf(t, err, "Entity statements %q (by ID): %v", entityType, err)
		}
	}

	for _, entityType := range entityTypes {
		stmt := entityType.URLsByProjectQuery()
		if stmt != "" {
			_, err := db.Prepare(stmt)
			assert.NoErrorf(t, err, "Entity statements %q (by project): %v", entityType, err)
		}
	}

	for _, outerEntityType := range entityTypes {
		outerStmt := outerEntityType.URLsByProjectQuery()
		if outerStmt == "" {
			continue
		}

		for _, middleEntityType := range entityTypes {
			middleStmt := middleEntityType.URLByIDQuery()
			if middleStmt == "" {
				continue
			}

			for _, innerEntityType := range entityTypes {
				innerStmt := innerEntityType.AllURLsQuery()
				if innerStmt == "" {
					continue
				}

				unionStmt := strings.Join([]string{outerStmt, middleStmt, innerStmt}, " UNION ")
				_, err := db.Prepare(unionStmt)
				assert.NoErrorf(t, err, "Union statement (outer: %q; middle: %q; inner: %q): %v", outerEntityType, middleEntityType, innerEntityType, err)
			}
		}
	}

	for _, outerEntityType := range entityTypes {
		outerStmt := outerEntityType.IDFromURLQuery()
		if outerStmt == "" {
			continue
		}

		_, err := db.Prepare(outerStmt)
		assert.NoErrorf(t, err, "Entity ID from URL statement %q: %v", outerEntityType, err)
		for _, innerEntityType := range entityTypes {
			innerStmt := innerEntityType.IDFromURLQuery()
			if innerStmt == "" {
				continue
			}

			_, err = db.Prepare(strings.Join([]string{outerStmt, innerStmt}, " UNION "))
			assert.NoErrorf(t, err, "Union entity ID from URL statement (outer: %q; inner: %q): %v", outerEntityType, innerEntityType, err)
		}
	}
}
