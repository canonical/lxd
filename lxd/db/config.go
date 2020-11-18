// +build linux,cgo,!agent

package db

import "github.com/grant-he/lxd/lxd/db/query"

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

// UpdateConfig updates the given LXD cluster configuration keys in the
// config table. Config keys set to empty values will be deleted.
func (c *ClusterTx) UpdateConfig(values map[string]string) error {
	return query.UpdateConfig(c.tx, "config", values)
}
