package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxc/lxd/shared"
)

var storagePoolConfigKeys = map[string]func(value string) error{
	// valid drivers: lvm
	"lvm.thinpool_name": shared.IsAny,
	"lvm.vg_name":       shared.IsAny,

	// valid drivers: btrfs, lvm, zfs
	"size": func(value string) error {
		if value == "" {
			return nil
		}

		_, err := shared.ParseByteSizeString(value)
		return err
	},

	// valid drivers: btrfs, dir, lvm, zfs
	"source": shared.IsAny,

	// valid drivers: lvm
	"volume.block.filesystem": func(value string) error {
		return shared.IsOneOf(value, []string{"ext4", "xfs"})
	},
	"volume.block.mount_options": shared.IsAny,

	// valid drivers: lvm
	"volume.size": func(value string) error {
		if value == "" {
			return nil
		}

		_, err := shared.ParseByteSizeString(value)
		return err
	},

	// valid drivers: zfs
	"volume.zfs.remove_snapshots": shared.IsBool,
	"volume.zfs.use_refquota":     shared.IsBool,

	// valid drivers: zfs
	"zfs.clone_copy": shared.IsBool,
	"zfs.pool_name":  shared.IsAny,
}

func storagePoolValidateConfig(name string, driver string, config map[string]string) error {
	err := func(value string) error {
		return shared.IsOneOf(value, supportedStoragePoolDrivers)
	}(driver)
	if err != nil {
		return err
	}

	// Check whether the config properties for the driver container sane
	// values.
	for key, val := range config {
		// User keys are not validated.
		if strings.HasPrefix(key, "user.") {
			continue
		}

		prfx := strings.HasPrefix
		if driver != "zfs" {
			if prfx(key, "volume.zfs.") || prfx(key, "zfs.") {
				return fmt.Errorf("The key %s cannot be used with %s storage pools.", key, strings.ToUpper(driver))
			}
		}

		if driver != "lvm" {
			if prfx(key, "lvm.") || prfx(key, "volume.block.") || key == "volume.size" {
				return fmt.Errorf("The key %s cannot be used with %s storage pools.", key, strings.ToUpper(driver))
			}
		}

		if driver == "dir" {
			if key == "size" {
				return fmt.Errorf("The key %s cannot be used with %s storage pools.", key, strings.ToUpper(driver))
			}
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
