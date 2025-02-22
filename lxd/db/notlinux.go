//go:build !linux || !cgo || agent

package db

import (
	"context"
	"database/sql"
	"fmt"
)

const DefaultOfflineThreshold = 20

type ClusterTx struct {
	tx     *sql.Tx
	nodeID int64
}

func (c *ClusterTx) Config(ctx context.Context) (map[string]string, error) {
	if ctx != nil {
		return nil, fmt.Errorf("Config not supported on this platform")
	}
	return nil, nil
}

func (c *ClusterTx) UpdateClusterConfig(values map[string]string) error {
	if values != nil {
		return fmt.Errorf("UpdateClusterConfig not supported on this platform")
	}
	return nil
}
