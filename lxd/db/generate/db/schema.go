package db

import (
	"fmt"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/node"
)

// UpdateSchema updates the schema.go file of the cluster and node databases.
func UpdateSchema(kind string) error {
	var err error
	switch kind {
	case "node":
		err = node.SchemaDotGo()
	case "cluster":
		err = cluster.SchemaDotGo()
	default:
		return fmt.Errorf(`No such schema kind %q (must be "node", or "cluster")`, kind)
	}

	if err != nil {
		return fmt.Errorf("Update node database schema: %w", err)
	}

	return nil
}
