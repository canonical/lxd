package main

import (
	"fmt"
	"path/filepath"
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
	"volume.lvm.thinpool_name":    shared.IsAny,
	"zfs.pool_name":               shared.IsAny,
}

func storagePoolValidateConfig(name string, driver string, config map[string]string) error {
	err := func(value string) error {
		return shared.IsOneOf(value, supportedStorageTypes)
	}(driver)
	if err != nil {
		return err
	}

	if config["source"] == "" {
		if driver == "dir" {
			config["source"] = filepath.Join(shared.VarPath("storage-pools"), name)
		} else {
			config["source"] = filepath.Join(shared.VarPath("disks"), name)
		}
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

	err = storagePoolFillDefault(name, driver, config)
	if err != nil {
		return err
	}

	return nil
}

func storagePoolFillDefault(name string, driver string, config map[string]string) error {
	if driver != "dir" {
		var size uint64
		if config["size"] == "" {
			st := syscall.Statfs_t{}
			err := syscall.Statfs(shared.VarPath(), &st)
			if err != nil {
				return fmt.Errorf("couldn't statfs %s: %s", shared.VarPath(), err)
			}

			/* choose 15 GB < x < 100GB, where x is 20% of the disk size */
			gb := uint64(1024 * 1024 * 1024)
			size = uint64(st.Frsize) * st.Blocks / 5
			if (size / gb) > 100 {
				size = 100 * gb
			}
			if (size / gb) < 15 {
				size = 15 * gb
			}
		} else {
			sz, err := shared.ParseByteSizeString(config["size"])
			if err != nil {
				return err
			}
			size = uint64(sz)
		}
		config["size"] = strconv.FormatUint(uint64(size), 10)
	}

	if driver == "zfs" {
		if val, ok := config["zfs.pool_name"]; !ok || val == "" {
			config["zfs.pool_name"] = name
		}
	}

	if driver == "lvm" {
		if config["volume.lvm.thinpool_name"] == "" {
			config["volume.lvm.thinpool_name"] = "LXDThinpool"
		}

		if config["volume.block.filesystem"] == "" {
			config["volume.block.filesystem"] = "ext4"
		}

		if config["volume.block.mount_options"] == "" {
			config["volume.block.mount_options"] = "discard"
		}
	}

	if config["volume.size"] == "" {
		if driver == "lvm" {
			sz, err := shared.ParseByteSizeString("10GB")
			if err != nil {
				return err
			}
			size := uint64(sz)
			config["volume.size"] = strconv.FormatUint(uint64(size), 10)
		} else {
			config["volume.size"] = "0"
		}
	}

	return nil
}
