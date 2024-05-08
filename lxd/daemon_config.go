package main

import (
	"context"

	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
)

func daemonConfigRender(state *state.State) (map[string]string, error) {
	config := map[string]string{}

	dump, err := state.GlobalConfig.Dump()
	if err != nil {
		return nil, err
	}

	// Turn the config into a JSON-compatible map.
	for key, value := range dump {
		config[key] = value
	}

	// Apply the local config.
	err = state.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		dump, err := nodeConfig.Dump()
		if err != nil {
			return err
		}

		for key, value := range dump {
			config[key] = value
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return config, nil
}

func daemonConfigSetProxy(d *Daemon, config *clusterConfig.Config) error {
	proxyHTTPS, err := config.ProxyHTTPS()
	if err != nil {
		return err
	}

	proxyHTTP, err := config.ProxyHTTP()
	if err != nil {
		return err
	}

	proxyIgnoreHosts, err := config.ProxyIgnoreHosts()
	if err != nil {
		return err
	}

	// Update the cached proxy function
	d.proxy = shared.ProxyFromConfig(
		proxyHTTPS,
		proxyHTTP,
		proxyIgnoreHosts,
	)

	if d.oidcVerifier != nil {
		d.oidcVerifier.ExpireConfig()
	}

	return nil
}
