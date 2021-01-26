// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/shared/api"
)

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
	arg1 := []interface{}{projectName, name}
	arg2 := []interface{}{&id, &acl.Description, &ingressJSON, &egressJSON}

	err := dbQueryRowScan(c, q, arg1, arg2)
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

	acl.Config, err = c.networkACLConfig(id)
	if err != nil {
		return -1, nil, errors.Wrapf(err, "Failed loading config")
	}

	return id, &acl, nil
}

// networkACLConfig returns the config map of the Network ACL with the given ID.
func (c *Cluster) networkACLConfig(id int64) (map[string]string, error) {
	var key, value string
	query := `
		SELECT key, value
		FROM networks_acls_config
		WHERE network_acl_id=?
	`
	inargs := []interface{}{id}
	outfmt := []interface{}{key, value}
	results, err := queryScan(c, query, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	config := make(map[string]string, len(results))

	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)

		_, found := config[key]
		if found {
			return nil, fmt.Errorf("Duplicate config row found for key %q for network ACL ID %d", key, id)
		}

		config[key] = value
	}

	return config, nil
}
