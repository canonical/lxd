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
	op, _, err := r.queryOperation("POST", "/cluster/nodes", cluster, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// AcceptNode requests to accept a new node into the cluster.
func (r *ProtocolLXD) AcceptNode(targetPassword, name, address string, schema, apiExt int, pools []api.StoragePool, networks []api.Network) (*api.ClusterNodeAccepted, error) {
	cluster := api.ClusterPost{
		Name:           name,
		Address:        address,
		Schema:         schema,
		API:            apiExt,
		TargetPassword: targetPassword,
		StoragePools:   pools,
		Networks:       networks,
	}
	info := &api.ClusterNodeAccepted{}
	_, err := r.queryStruct("POST", "/cluster/nodes", cluster, "", &info)

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
	op, _, err := r.queryOperation("POST", "/cluster/nodes", cluster, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// LeaveCluster makes the given node leave the cluster (gracefully or not,
// depending on the force flag).
func (r *ProtocolLXD) LeaveCluster(name string, force bool) error {
	params := ""
	if force {
		params += "?force=1"
	}
	url := fmt.Sprintf("/cluster/nodes/%s%s", name, params)
	_, err := r.queryStruct("DELETE", url, nil, "", nil)
	return err
}

// GetNodes returns the current nodes in the cluster.
func (r *ProtocolLXD) GetNodes() ([]api.Node, error) {
	nodes := []api.Node{}
	path := "/cluster/nodes"
	_, err := r.queryStruct("GET", path, nil, "", &nodes)

	if err != nil {
		return nil, err
	}

	return nodes, nil
}

// GetNode returns information about the given node.
func (r *ProtocolLXD) GetNode(name string) (*api.Node, error) {
	node := api.Node{}
	path := fmt.Sprintf("/cluster/nodes/%s", name)
	_, err := r.queryStruct("GET", path, nil, "", &node)

	if err != nil {
		return nil, err
	}

	return &node, nil
}

// RenameNode changes the name of an existing node
func (r *ProtocolLXD) RenameNode(name string, node api.NodePost) error {
	url := fmt.Sprintf("/cluster/nodes/%s", name)
	_, _, err := r.query("POST", url, node, "")
	return err
}
