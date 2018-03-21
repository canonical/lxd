package main

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func (c *cmdInit) RunAuto(cmd *cobra.Command, args []string, d lxd.ContainerServer) (*initData, error) {
	// Sanity checks
	if !shared.StringInSlice(c.flagStorageBackend, supportedStoragePoolDrivers) {
		return nil, fmt.Errorf("The requested backend '%s' isn't supported by lxd init", c.flagStorageBackend)
	}

	if !shared.StringInSlice(c.flagStorageBackend, c.availableStorageDrivers()) {
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

	// Fill in the configuration
	config := initData{}

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

	return &config, nil
}
