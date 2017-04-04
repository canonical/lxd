package config

import (
	"github.com/lxc/lxd"
)

// Legacy returns a legacy *lxd.Config
func (c *Config) Legacy() *lxd.Config {
	conf := &lxd.Config{
		DefaultRemote: c.DefaultRemote,
		Aliases:       c.Aliases,
		ConfigDir:     c.ConfigDir,
	}

	remotes := map[string]lxd.RemoteConfig{}

	for k, v := range c.Remotes {
		remote := lxd.RemoteConfig{
			Addr:     v.Addr,
			Public:   v.Public,
			Protocol: v.Protocol,
			Static:   v.Static,
		}

		remotes[k] = remote
	}

	conf.Remotes = remotes

	return conf
}
