package db_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
)

// Node database objects automatically initialize their schema as needed.
func TestNode_Schema(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	// The underlying node-level database has exactly one row in the schema
	// table.
	db := node.DB()
	rows, err := db.Query("SELECT COUNT(*) FROM schema")
	assert.NoError(t, err)
	defer rows.Close()
	assert.Equal(t, true, rows.Next())
	var n int
	assert.NoError(t, rows.Scan(&n))
	assert.Equal(t, 1, n)
}
