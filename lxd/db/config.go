//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

import "github.com/lxc/lxd/lxd/db/query"

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t config.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e config objects
//go:generate mapper stmt -p db -e config create struct=Config
//go:generate mapper stmt -p db -e config delete
//
//go:generate mapper method -p db -e config GetMany
//go:generate mapper method -p db -e config Create struct=Config
//go:generate mapper method -p db -e config Update struct=Config
//go:generate mapper method -p db -e config DeleteMany

// Config is a reference struct representing one configuration entry of another entity.
type Config struct {
	ID          int `db:"primary=yes"`
	ReferenceID int
	Key         string
	Value       string
}

// Config fetches all LXD node-level config keys.
func (n *NodeTx) Config() (map[string]string, error) {
	return query.SelectConfig(n.tx, "config", "")
}

// UpdateConfig updates the given LXD node-level configuration keys in the
// config table. Config keys set to empty values will be deleted.
func (n *NodeTx) UpdateConfig(values map[string]string) error {
	return query.UpdateConfig(n.tx, "config", values)
}

// Config fetches all LXD cluster config keys.
func (c *ClusterTx) Config() (map[string]string, error) {
	return query.SelectConfig(c.tx, "config", "")
}

// UpdateClusterConfig updates the given LXD cluster configuration keys in the
// config table. Config keys set to empty values will be deleted.
func (c *ClusterTx) UpdateClusterConfig(values map[string]string) error {
	return query.UpdateConfig(c.tx, "config", values)
}
