//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/shared/api"
)

// GetNetworkACLs returns the names of existing Network ACLs.
func (c *ClusterTx) GetNetworkACLs(project string) ([]string, error) {
	q := `SELECT name FROM networks_acls
		WHERE project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1)
		ORDER BY id
	`

	var aclNames []string
	err := c.QueryScan(q, func(scan func(dest ...interface{}) error) error {
		var aclName string

		err := scan(&aclName)
		if err != nil {
			return err
		}

		aclNames = append(aclNames, aclName)

		return nil
	}, project)

	if err != nil {
		return nil, err
	}

	return aclNames, nil
}

// GetNetworkACLs returns the names of existing Network ACLs.
func (c *Cluster) GetNetworkACLs(project string) ([]string, error) {
	var aclNames []string
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		aclNames, err = tx.GetNetworkACLs(project)
		return err
	})
	if err != nil {
		return nil, err
	}

	return aclNames, nil
}

// GetNetworkACLIDsByNames returns a map of names to IDs of existing Network ACLs.
func (c *Cluster) GetNetworkACLIDsByNames(project string) (map[string]int64, error) {
	q := `SELECT id, name FROM networks_acls
		WHERE project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1)
		ORDER BY id
	`

	acls := make(map[string]int64)

	err := c.Transaction(func(tx *ClusterTx) error {
		return tx.QueryScan(q, func(scan func(dest ...interface{}) error) error {
			var aclID int64
			var aclName string

			err := scan(&aclID, &aclName)
			if err != nil {
				return err
			}

			acls[aclName] = aclID

			return nil
		}, project)
	})
	if err != nil {
		return nil, err
	}

	return acls, nil
}

// GetNetworkACL returns the Network ACL with the given name in the given project.
func (c *Cluster) GetNetworkACL(projectName string, name string) (int64, *api.NetworkACL, error) {
	var id int64 = int64(-1)
	var ingressJSON string
	var egressJSON string

	acl := api.NetworkACL{
		NetworkACLPost: api.NetworkACLPost{
			Name: name,
		},
	}

	q := `
		SELECT id, description, ingress, egress
		FROM networks_acls
		WHERE project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1) AND name=?
		LIMIT 1
	`

	err := c.Transaction(func(tx *ClusterTx) error {
		err := tx.tx.QueryRow(q, projectName, name).Scan(&id, &acl.Description, &ingressJSON, &egressJSON)
		if err != nil {
			return err
		}

		err = networkACLConfig(tx, id, &acl)
		if err != nil {
			return errors.Wrapf(err, "Failed loading config")
		}

		return nil
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, ErrNoSuchObject
		}

		return -1, nil, err
	}

	acl.Ingress = []api.NetworkACLRule{}
	if ingressJSON != "" {
		err = json.Unmarshal([]byte(ingressJSON), &acl.Ingress)
		if err != nil {
			return -1, nil, errors.Wrapf(err, "Failed unmarshalling ingress rules")
		}
	}

	acl.Egress = []api.NetworkACLRule{}
	if egressJSON != "" {
		err = json.Unmarshal([]byte(egressJSON), &acl.Egress)
		if err != nil {
			return -1, nil, errors.Wrapf(err, "Failed unmarshalling egress rules")
		}
	}

	return id, &acl, nil
}

// GetNetworkACLNameAndProjectWithID returns the network ACL name and project name for the given ID.
func (c *Cluster) GetNetworkACLNameAndProjectWithID(networkACLID int) (string, string, error) {
	var networkACLName string
	var projectName string

	q := `SELECT networks_acls.name, projects.name FROM networks_acls JOIN projects ON projects.id=networks.project_id WHERE networks_acls.id=?`

	err := c.Transaction(func(tx *ClusterTx) error {
		return tx.tx.QueryRow(q, networkACLID).Scan(&networkACLName, &projectName)
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", ErrNoSuchObject
		}

		return "", "", err
	}

	return networkACLName, projectName, nil
}

// networkACLConfig populates the config map of the Network ACL with the given ID.
func networkACLConfig(tx *ClusterTx, id int64, acl *api.NetworkACL) error {
	q := `
		SELECT key, value
		FROM networks_acls_config
		WHERE network_acl_id=?
	`

	acl.Config = make(map[string]string)
	return tx.QueryScan(q, func(scan func(dest ...interface{}) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := acl.Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for network ACL ID %d", key, id)
		}

		acl.Config[key] = value

		return nil
	}, id)
}

// CreateNetworkACL creates a new Network ACL.
func (c *Cluster) CreateNetworkACL(projectName string, info *api.NetworkACLsPost) (int64, error) {
	var id int64
	var err error
	var ingressJSON, egressJSON []byte

	if info.Ingress != nil {
		ingressJSON, err = json.Marshal(info.Ingress)
		if err != nil {
			return -1, errors.Wrapf(err, "Failed marshalling ingress rules")
		}
	}

	if info.Egress != nil {
		egressJSON, err = json.Marshal(info.Egress)
		if err != nil {
			return -1, errors.Wrapf(err, "Failed marshalling egress rules")
		}
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		// Insert a new Network ACL record.
		result, err := tx.tx.Exec(`
			INSERT INTO networks_acls (project_id, name, description, ingress, egress)
			VALUES ((SELECT id FROM projects WHERE name = ? LIMIT 1), ?, ?, ?, ?)
		`, projectName, info.Name, info.Description, string(ingressJSON), string(egressJSON))
		if err != nil {
			return err
		}

		id, err := result.LastInsertId()
		if err != nil {
			return err
		}

		err = networkACLConfigAdd(tx.tx, id, info.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		id = -1
	}

	return id, err
}

// networkACLConfigAdd inserts Network ACL config keys.
func networkACLConfigAdd(tx *sql.Tx, id int64, config map[string]string) error {
	sql := "INSERT INTO networks_acls_config (network_acl_id, key, value) VALUES(?, ?, ?)"
	stmt, err := tx.Prepare(sql)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(id, k, v)
		if err != nil {
			return errors.Wrapf(err, "Failed inserting config")
		}
	}

	return nil
}

// UpdateNetworkACL updates the Network ACL with the given ID.
func (c *Cluster) UpdateNetworkACL(id int64, config *api.NetworkACLPut) error {
	var err error
	var ingressJSON, egressJSON []byte

	if config.Ingress != nil {
		ingressJSON, err = json.Marshal(config.Ingress)
		if err != nil {
			return errors.Wrapf(err, "Failed marshalling ingress rules")
		}
	}

	if config.Egress != nil {
		egressJSON, err = json.Marshal(config.Egress)
		if err != nil {
			return errors.Wrapf(err, "Failed marshalling egress rules")
		}
	}

	return c.Transaction(func(tx *ClusterTx) error {
		_, err := tx.tx.Exec(`
			UPDATE networks_acls
			SET description=?, ingress = ?, egress = ?
			WHERE id=?
		`, config.Description, ingressJSON, egressJSON, id)
		if err != nil {
			return err
		}

		_, err = tx.tx.Exec("DELETE FROM networks_acls_config WHERE network_acl_id=?", id)
		if err != nil {
			return err
		}

		err = networkACLConfigAdd(tx.tx, id, config.Config)
		if err != nil {
			return err
		}

		return nil
	})
}

// RenameNetworkACL renames a Network ACL.
func (c *Cluster) RenameNetworkACL(id int64, newName string) error {
	return c.Transaction(func(tx *ClusterTx) error {
		_, err := tx.tx.Exec("UPDATE networks_acls SET name=? WHERE id=?", newName, id)
		return err
	})
}

// DeleteNetworkACL deletes the Network ACL.
func (c *Cluster) DeleteNetworkACL(id int64) error {
	return c.Transaction(func(tx *ClusterTx) error {
		_, err := tx.tx.Exec("DELETE FROM networks_acls WHERE id=?", id)
		return err
	})
}
