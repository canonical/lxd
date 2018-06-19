package main

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func (c *cmdInit) RunAuto(cmd *cobra.Command, args []string, d lxd.ContainerServer) (*cmdInitData, error) {
	// Sanity checks
	if c.flagStorageBackend != "" && !shared.StringInSlice(c.flagStorageBackend, supportedStoragePoolDrivers) {
		return nil, fmt.Errorf("The requested backend '%s' isn't supported by lxd init", c.flagStorageBackend)
	}

	if c.flagStorageBackend != "" && !shared.StringInSlice(c.flagStorageBackend, c.availableStorageDrivers("all")) {
		return nil, fmt.Errorf("The requested backend '%s' isn't available on your system (missing tools)", c.flagStorageBackend)
	}

	if c.flagStorageBackend == "dir" || c.flagStorageBackend == "" {
		if c.flagStorageLoopSize != -1 || c.flagStorageDevice != "" || c.flagStoragePool != "" {
			return nil, fmt.Errorf("None of --storage-pool, --storage-create-device or --storage-create-loop may be used with the 'dir' backend")
		}
	} else {
		if c.flagStorageLoopSize != -1 && c.flagStorageDevice != "" {
			return nil, fmt.Errorf("Only one of --storage-create-device or --storage-create-loop can be specified")
		}
	}

	if c.flagNetworkAddress == "" {
		if c.flagNetworkPort != -1 {
			return nil, fmt.Errorf("--network-port can't be used without --network-address")
		}

		if c.flagTrustPassword != "" {
			return nil, fmt.Errorf("--trust-password can't be used without --network-address")
		}
	}

	storagePools, err := d.GetStoragePoolNames()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to retrieve list of storage pools")
	}

	if len(storagePools) > 0 && (c.flagStorageBackend != "" || c.flagStorageDevice != "" || c.flagStorageLoopSize != -1 || c.flagStoragePool != "") {
		return nil, fmt.Errorf("Storage has already been configured")
	}

	// Defaults
	if c.flagStorageBackend == "" {
		c.flagStorageBackend = "dir"
	}

	if c.flagNetworkPort == -1 {
		c.flagNetworkPort = 8443
	}

	// Fill in the node configuration
	config := initDataNode{}
	config.Config = map[string]interface{}{}

	// Network listening
	if c.flagNetworkAddress != "" {
		config.Config["core.https_address"] = fmt.Sprintf("%s:%d", c.flagNetworkAddress, c.flagNetworkPort)

		if c.flagTrustPassword != "" {
			config.Config["core.trust_password"] = c.flagTrustPassword
		}
	}

	// Storage configuration
	if len(storagePools) == 0 {
		// Storage pool
		pool := api.StoragePoolsPost{
			Name:   "default",
			Driver: c.flagStorageBackend,
		}
		pool.Config = map[string]string{}

		if c.flagStorageDevice != "" {
			pool.Config["source"] = c.flagStorageDevice
		} else if c.flagStorageLoopSize > 0 {
			pool.Config["size"] = fmt.Sprintf("%dGB", c.flagStorageLoopSize)
		} else {
			pool.Config["source"] = c.flagStoragePool
		}

		// If using a device or loop, --storage-pool refers to the name of the new pool
		if c.flagStoragePool != "" && (c.flagStorageDevice != "" || c.flagStorageLoopSize != -1) {
			pool.Name = c.flagStoragePool
		}

		config.StoragePools = []api.StoragePoolsPost{pool}

		// Profile entry
		config.Profiles = []api.ProfilesPost{{
			Name: "default",
			ProfilePut: api.ProfilePut{
				Devices: map[string]map[string]string{
					"root": {
						"type": "disk",
						"path": "/",
						"pool": pool.Name,
					},
				},
			},
		}}
	}

	// Network configuration
	networks, err := d.GetNetworks()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to retrieve list of networks")
	}

	// Extract managed networks
	managedNetworks := []api.Network{}
	for _, network := range networks {
		if network.Managed {
			managedNetworks = append(managedNetworks, network)
		}
	}

	// Look for an existing network device in the profile
	defaultProfileNetwork := false
	defaultProfile, _, err := d.GetProfile("default")
	if err == nil {
		for _, dev := range defaultProfile.Devices {
			if dev["type"] == "nic" {
				defaultProfileNetwork = true
				break
			}
		}
	}

	// Define a new network
	if len(managedNetworks) == 0 && !defaultProfileNetwork {
		// Find a new name
		idx := 0
		for {
			if shared.PathExists(fmt.Sprintf("/sys/class/net/lxdbr%d", idx)) {
				idx++
				continue
			}

			break
		}

		// Define the new network
		network := api.NetworksPost{}
		network.Name = fmt.Sprintf("lxdbr%d", idx)
		config.Networks = append(config.Networks, network)

		// Add it to the profile
		if config.Profiles == nil {
			config.Profiles = []api.ProfilesPost{{
				Name: "default",
				ProfilePut: api.ProfilePut{
					Devices: map[string]map[string]string{
						"eth0": {
							"type":    "nic",
							"nictype": "bridged",
							"parent":  network.Name,
							"name":    "eth0",
						},
					},
				},
			}}
		} else {
			config.Profiles[0].Devices["eth0"] = map[string]string{
				"type":    "nic",
				"nictype": "bridged",
				"parent":  network.Name,
				"name":    "eth0",
			}
		}
	}

	return &cmdInitData{Node: config}, nil
}
