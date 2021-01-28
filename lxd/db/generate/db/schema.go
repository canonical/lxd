package db

import (
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/node"
)

// UpdateSchema updates the schema.go file of the cluster and node databases.
func UpdateSchema() error {
	err := cluster.SchemaDotGo()
	if err != nil {
		return errors.Wrap(err, "Update cluster database schema")
	}

	err = node.SchemaDotGo()
	if err != nil {
		return errors.Wrap(err, "Update node database schema")
	}

	return nil
}
