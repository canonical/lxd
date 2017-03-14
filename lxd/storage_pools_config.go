package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxc/lxd/shared"
)

var storagePoolConfigKeys = map[string]func(value string) error{
	"source": shared.IsAny,
	"size": func(value string) error {
		if value == "" {
			return nil
		}

		_, err := shared.ParseByteSizeString(value)
		return err
	},
	"volume.block.mount_options": shared.IsAny,
	"volume.block.filesystem": func(value string) error {
		return shared.IsOneOf(value, []string{"ext4", "xfs"})
	},
	"volume.size": func(value string) error {
		if value == "" {
			return nil
		}

		_, err := shared.ParseByteSizeString(value)
		return err
	},
	"volume.zfs.use_refquota":     shared.IsBool,
	"volume.zfs.remove_snapshots": shared.IsBool,
	"lvm.thinpool_name":           shared.IsAny,
	"lvm.vg_name":                 shared.IsAny,
	"zfs.pool_name":               shared.IsAny,
}

func storagePoolValidateConfig(name string, driver string, config map[string]string) error {
	err := func(value string) error {
		return shared.IsOneOf(value, supportedStoragePoolDrivers)
	}(driver)
	if err != nil {
		return err
	}

	for key, val := range config {
		// User keys are not validated.
		if strings.HasPrefix(key, "user.") {
			continue
		}

		// Validate storage pool config keys.
		validator, ok := storagePoolConfigKeys[key]
		if !ok {
			return fmt.Errorf("Invalid storage pool configuration key: %s", key)
		}

		err := validator(val)
		if err != nil {
			return err
		}

		if driver != "zfs" || driver == "dir" {
			if config["volume.zfs.use_refquota"] != "" {
				return fmt.Errorf("The key volume.zfs.use_refquota cannot be used with non zfs storage pools.")
			}

			if config["volume.zfs.remove_snapshots"] != "" {
				return fmt.Errorf("The key volume.zfs.remove_snapshots cannot be used with non zfs storage pools.")
			}

			if config["zfs.pool_name"] != "" {
				return fmt.Errorf("The key zfs.pool_name cannot be used with non zfs storage pools.")
			}
		}

		if driver == "dir" {
			if config["size"] != "" {
				return fmt.Errorf("The key size cannot be used with dir storage pools.")
			}

			if config["volume.block.mount_options"] != "" {
				return fmt.Errorf("The key volume.block.mount_options cannot be used with dir storage pools.")
			}

			if config["volume.block.filesystem"] != "" {
				return fmt.Errorf("The key volume.block.filesystem cannot be used with dir storage pools.")
			}

			if config["volume.size"] != "" {
				return fmt.Errorf("The key volume.size cannot be used with dir storage pools.")
			}
		}
	}

	return nil
}

func storagePoolFillDefault(name string, driver string, config map[string]string) error {
	if driver != "dir" {
		if config["size"] == "" {
			st := syscall.Statfs_t{}
			err := syscall.Statfs(shared.VarPath(), &st)
			if err != nil {
				return fmt.Errorf("couldn't statfs %s: %s", shared.VarPath(), err)
			}

			/* choose 15 GB < x < 100GB, where x is 20% of the disk size */
			size := uint64(st.Frsize) * st.Blocks / (1024 * 1024 * 1024) / 5
			if size > 100 {
				size = 100
			}
			if size < 15 {
				size = 15
			}
			config["size"] = strconv.FormatUint(uint64(size), 10) + "GB"
		} else {
			_, err := shared.ParseByteSizeString(config["size"])
			if err != nil {
				return err
			}
		}
	}

	if driver == "lvm" {
		if config["lvm.thinpool_name"] == "" {
			// Unchangeable pool property: Set unconditionally.
			config["lvm.thinpool_name"] = "LXDThinpool"
		}

		if config["volume.size"] != "" {
			_, err := shared.ParseByteSizeString(config["volume.size"])
			if err != nil {
				return err
			}
		}
	}

	return nil
}
