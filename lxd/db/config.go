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

func ConfigValueSet(cluster *Cluster, key string, value string) error {
	tx, err := begin(cluster.db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM config WHERE key=?", key)
	if err != nil {
		tx.Rollback()
		return err
	}

	if value != "" {
		str := `INSERT INTO config (key, value) VALUES (?, ?);`
		stmt, err := tx.Prepare(str)
		if err != nil {
			tx.Rollback()
			return err
		}
		defer stmt.Close()
		_, err = stmt.Exec(key, value)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	err = TxCommit(tx)
	if err != nil {
		return err
	}

	return nil
}
