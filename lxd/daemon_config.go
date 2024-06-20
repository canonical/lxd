package main

import (
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
)

func daemonConfigRender(state *state.State) (map[string]interface{}, error) {
	config := map[string]interface{}{}

	// Turn the config into a JSON-compatible map
	err := state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		clusterConfig, err := cluster.ConfigLoad(tx)
		if err != nil {
			return err
		}
		for key, value := range clusterConfig.Dump() {
			config[key] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = state.Node.Transaction(func(tx *db.NodeTx) error {
		nodeConfig, err := node.ConfigLoad(tx)
		if err != nil {
			return err
		}
		for key, value := range nodeConfig.Dump() {
			config[key] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return config, nil
}

func daemonConfigSetProxy(d *Daemon, config *cluster.Config) {
	// Update the cached proxy function
	d.proxy = shared.ProxyFromConfig(
		config.ProxyHTTPS(),
		config.ProxyHTTP(),
		config.ProxyIgnoreHosts(),
	)
}
