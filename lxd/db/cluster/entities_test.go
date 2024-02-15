package cluster

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEntityStatementValidity(t *testing.T) {
	schema := Schema()
	db, err := schema.ExerciseUpdate(70, nil)
	require.NoError(t, err)

	for entityType, stmt := range entityStatementsAll {
		_, err := db.Prepare(stmt)
		assert.NoErrorf(t, err, "Entity statements %q (all): %v", entityType, err)
	}

	for entityType, stmt := range entityStatementsByID {
		_, err := db.Prepare(stmt)
		assert.NoErrorf(t, err, "Entity statements %q (by ID): %v", entityType, err)
	}

	for entityType, stmt := range entityStatementsByProjectName {
		_, err := db.Prepare(stmt)
		assert.NoErrorf(t, err, "Entity statements %q (by project): %v", entityType, err)
	}

	for outerEntityType, outerStmt := range entityStatementsByProjectName {
		for middleEntityType, middleStmt := range entityStatementsByID {
			for innerEntityType, innerStmt := range entityStatementsAll {
				unionStmt := strings.Join([]string{outerStmt, middleStmt, innerStmt}, " UNION ")
				_, err := db.Prepare(unionStmt)
				assert.NoErrorf(t, err, "Union statement (outer: %q; middle: %q; inner: %q): %v", outerEntityType, middleEntityType, innerEntityType, err)
			}
		}
	}
}
