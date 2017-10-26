package lxd

import (
	"fmt"

	"github.com/lxc/lxd/shared/api"
)

// GetCluster returns information about a cluster.
//
// If this client is not trusted, the password must be supplied.
func (r *ProtocolLXD) GetCluster(password string) (*api.Cluster, error) {
	cluster := &api.Cluster{}
	path := "/cluster"
	if password != "" {
		path += fmt.Sprintf("?password=%s", password)
	}
	_, err := r.queryStruct("GET", path, nil, "", &cluster)

	if err != nil {
		return nil, err
	}

	return cluster, nil
}

// BootstrapCluster requests to bootstrap a new cluster.
func (r *ProtocolLXD) BootstrapCluster(name string) (*Operation, error) {
	cluster := api.ClusterPost{Name: name}
	op, _, err := r.queryOperation("POST", "/cluster", cluster, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// AcceptNode requests to accept a new node into the cluster.
func (r *ProtocolLXD) AcceptNode(targetPassword, name, address string, schema, apiExt int) (*api.ClusterNodeAccepted, error) {
	cluster := api.ClusterPost{
		Name:           name,
		Address:        address,
		Schema:         schema,
		API:            apiExt,
		TargetPassword: targetPassword,
	}
	info := &api.ClusterNodeAccepted{}
	_, err := r.queryStruct("POST", "/cluster", cluster, "", &info)

	if err != nil {
		return nil, err
	}

	return info, nil
}

// JoinCluster requests to join an existing cluster.
func (r *ProtocolLXD) JoinCluster(targetAddress, targetPassword, targetCert, name string) (*Operation, error) {
	cluster := api.ClusterPost{
		TargetAddress:  targetAddress,
		TargetPassword: targetPassword,
		TargetCert:     targetCert,
		Name:           name,
	}
	op, _, err := r.queryOperation("POST", "/cluster", cluster, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}
