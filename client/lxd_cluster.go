package lxd

import "github.com/lxc/lxd/shared/api"

// BootstrapCluster request to bootstrap a new cluster.
func (r *ProtocolLXD) BootstrapCluster(name string) (*Operation, error) {
	cluster := api.ClusterPost{Name: name}
	op, _, err := r.queryOperation("POST", "/cluster", cluster, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}
