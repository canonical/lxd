//go:build linux && cgo && !agent

package db

import (
	"context"

	"github.com/lxc/lxd/lxd/db/cluster"
)

// GetProject returns the project with the given key.
func (db *DB) GetProject(ctx context.Context, projectName string) (*cluster.Project, error) {
	var err error
	var p *cluster.Project
	err = db.Transaction(ctx, "global", func(ctx context.Context, tx *Tx) error {
		p, err = cluster.GetProject(ctx, tx, projectName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return p, nil
}
