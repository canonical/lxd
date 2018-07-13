package main

import (
	"fmt"

	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v2"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
)

func (c *cmdInit) RunDump(d lxd.ContainerServer) error {
	currentServer, _, err := d.GetServer()
	if err != nil {
		return errors.Wrap(err, "Failed to retrieve current server configuration")
	}

	var config initDataNode
	config.Config = currentServer.Config

	networks, err := d.GetNetworks()
	if err != nil {
		return errors.Wrap(err, "Failed to retrieve current server configuration")
	}

	for _, network := range networks {
		// Only list managed networks
		if !network.Managed {
			continue
		}
		networksPost := api.NetworksPost{}
		networksPost.Config = network.Config
		networksPost.Description = network.Description
		networksPost.Managed = network.Managed
		networksPost.Name = network.Name
		networksPost.Type = network.Type

		config.Networks = append(config.Networks, networksPost)
	}

	storagePools, err := d.GetStoragePools()
	if err != nil {
		return errors.Wrap(err, "Failed to retrieve current server configuration")
	}

	for _, storagePool := range storagePools {
		storagePoolsPost := api.StoragePoolsPost{}
		storagePoolsPost.Config = storagePool.Config
		storagePoolsPost.Description = storagePool.Description
		storagePoolsPost.Name = storagePool.Name
		storagePoolsPost.Driver = storagePool.Driver

		config.StoragePools = append(config.StoragePools, storagePoolsPost)
	}

	profiles, err := d.GetProfiles()
	if err != nil {
		return errors.Wrap(err, "Failed to retrieve current server configuration")
	}

	for _, profile := range profiles {
		profilesPost := api.ProfilesPost{}
		profilesPost.Config = profile.Config
		profilesPost.Description = profile.Description
		profilesPost.Devices = profile.Devices
		profilesPost.Name = profile.Name

		config.Profiles = append(config.Profiles, profilesPost)
	}

	out, err := yaml.Marshal(config)
	if err != nil {
		return errors.Wrap(err, "Failed to retrieve current server configuration")
	}

	fmt.Printf("%s\n", out)

	return nil
}
