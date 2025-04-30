package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/node"
)

func main() {
	err := freshSchema(os.Args[1:])
	if err != nil {
		_, _ = os.Stderr.Write([]byte(err.Error()))
		os.Exit(1)
	}
}

// UpdateSchema updates the schema.go file of the cluster and node databases.
func freshSchema(args []string) error {
	if len(args) < 1 {
		return errors.New(`Schema kind must be provided (must be "node", or "cluster")`)
	}

	kind := args[0]
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
