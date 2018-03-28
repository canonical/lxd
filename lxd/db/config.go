package db

import "github.com/lxc/lxd/lxd/db/query"

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

// ConfigValueSet is a convenience to set a cluster-level key/value config pair
// in a single transaction.
func ConfigValueSet(c *Cluster, key string, value string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		_, err := tx.tx.Exec("DELETE FROM config WHERE key=?", key)
		if err != nil {
			return err
		}

		if value != "" {
			str := `INSERT INTO config (key, value) VALUES (?, ?);`
			stmt, err := tx.tx.Prepare(str)
			if err != nil {
				return err
			}
			defer stmt.Close()
			_, err = stmt.Exec(key, value)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return err
}
