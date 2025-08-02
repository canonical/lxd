package main

import (
	"context"
	"maps"

	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
)

func daemonConfigRender(state *state.State) (map[string]string, error) {
	config := map[string]string{}

	// Turn the config into a JSON-compatible map.
	maps.Copy(config, state.GlobalConfig.Dump())

	// Apply the local config.
	err := state.DB.Node.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		maps.Copy(config, nodeConfig.Dump())

		return nil
	})
	if err != nil {
		return nil, err
	}

	return config, nil
}

func daemonConfigSetProxy(d *Daemon, config *clusterConfig.Config) {
	// Update the cached proxy function
	d.proxy = shared.ProxyFromConfig(
		config.ProxyHTTPS(),
		config.ProxyHTTP(),
		config.ProxyIgnoreHosts(),
	)

	if d.oidcVerifier != nil {
		d.oidcVerifier.ExpireConfig()
	}
}
