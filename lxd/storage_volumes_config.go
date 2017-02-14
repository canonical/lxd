package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

var storageVolumeConfigKeys = map[string]func(value string) error{
	"block.mount_options": shared.IsAny,
	"block.filesystem": func(value string) error {
		return shared.IsOneOf(value, []string{"ext4", "xfs"})
	},
	"size": func(value string) error {
		if value == "" {
			return nil
		}

		_, err := shared.ParseByteSizeString(value)
		return err
	},
	"zfs.use_refquota":     shared.IsBool,
	"zfs.remove_snapshots": shared.IsBool,
}

func storageVolumeValidateConfig(name string, config map[string]string, parentPool *api.StoragePool) error {
	for key, val := range config {
		// User keys are not validated.
		if strings.HasPrefix(key, "user.") {
			continue
		}

		// Validate storage volume config keys.
		validator, ok := storageVolumeConfigKeys[key]
		if !ok {
			return fmt.Errorf("Invalid storage volume configuration key: %s", key)
		}

		err := validator(val)
		if err != nil {
			return err
		}

		if parentPool.Driver != "zfs" || parentPool.Driver == "dir" {
			if config["zfs.use_refquota"] != "" {
				return fmt.Errorf("The key volume.zfs.use_refquota cannot be used with non zfs storage volumes.")
			}

			if config["zfs.remove_snapshots"] != "" {
				return fmt.Errorf("The key volume.zfs.remove_snapshots cannot be used with non zfs storage volumes.")
			}
		}

		if parentPool.Driver == "dir" {
			if config["block.mount_options"] != "" {
				return fmt.Errorf("The key block.mount_options cannot be used with dir storage volumes.")
			}

			if config["block.filesystem"] != "" {
				return fmt.Errorf("The key block.filesystem cannot be used with dir storage volumes.")
			}

			if config["size"] != "" {
				return fmt.Errorf("The key size cannot be used with dir storage volumes.")
			}
		}

		if parentPool.Driver == "lvm" {
			if config["block.filesystem"] == "" {
				config["block.filesystem"] = parentPool.Config["volume.block.filesystem"]
			}

			if config["block.mount_options"] == "" {
				config["block.mount_options"] = parentPool.Config["volume.block.mount_options"]
			}
		}
	}

	err := storageVolumeFillDefault(name, config, parentPool)
	if err != nil {
		return err
	}

	return nil
}

func storageVolumeFillDefault(name string, config map[string]string, parentPool *api.StoragePool) error {
	if parentPool.Driver == "dir" {
		config["size"] = "0"
	} else if parentPool.Driver == "lvm" {
		if config["size"] == "0" || config["size"] == "" {
			config["size"] = parentPool.Config["volume.size"]
		}

		if config["size"] == "0" || config["size"] == "" {
			sz, err := shared.ParseByteSizeString("10GB")
			if err != nil {
				return err
			}
			size := uint64(sz)
			config["size"] = strconv.FormatUint(uint64(size), 10)
		}
	} else {
		if config["size"] == "" {
			config["size"] = parentPool.Config["volume.size"]
		}

		if config["size"] == "" {
			config["size"] = "0"
		}
	}

	if parentPool.Driver == "lvm" {
		if config["block.filesystem"] == "" {
			config["block.filesystem"] = "ext4"
		}

		if config["block.mount_options"] == "" && config["block.filesystem"] == "ext4" {
			config["block.mount_options"] = "discard"
		}

		if config["lvm.thinpool_name"] == "" {
			config["lvm.thinpool_name"] = parentPool.Config["volume.lvm.thinpool_name"]
			if config["lvm.thinpool_name"] == "" {
				config["lvm.thinpool_name"] = "LXDThinPool"
			}
		}
	}

	return nil
}
