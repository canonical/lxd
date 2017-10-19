package main

import (
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
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

	// Clear the simplestreams cache as it's tied to the old proxy config
	imageStreamCacheLock.Lock()
	for k := range imageStreamCache {
		delete(imageStreamCache, k)
	}
	imageStreamCacheLock.Unlock()
}
