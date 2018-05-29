package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxc/lxd/shared"
)

func updateStoragePoolError(unchangeable []string, driverName string) error {
	return fmt.Errorf(`The %v properties cannot be changed for "%s" `+
		`storage pools`, unchangeable, driverName)
}

var changeableStoragePoolProperties = map[string][]string{
	"btrfs": {
		"rsync.bwlimit",
		"btrfs.mount_options"},

	"ceph": {
		"volume.block.filesystem",
		"volume.block.mount_options",
		"volume.size"},

	"dir": {
		"rsync.bwlimit"},

	"lvm": {
		"lvm.thinpool_name",
		"lvm.vg_name",
		"volume.block.filesystem",
		"volume.block.mount_options",
		"volume.size"},

	"zfs": {
		"rsync_bwlimit",
		"volume.zfs.remove_snapshots",
		"volume.zfs.use_refquota",
		"zfs.clone_copy"},
}

var storagePoolConfigKeys = map[string]func(value string) error{
	// valid drivers: btrfs
	// (Note, that we can't be smart in detecting mount options since a lot
	// of filesystems come with their own additional ones (e.g.
	// "user_subvol_rm_allowed" for btrfs or "zfsutils" for zfs). So
	// shared.IsAny() must do.)
	"btrfs.mount_options": shared.IsAny,

	// valid drivers: ceph
	"ceph.cluster_name":    shared.IsAny,
	"ceph.osd.force_reuse": shared.IsBool,
	"ceph.osd.pool_name":   shared.IsAny,
	"ceph.osd.pg_num": func(value string) error {
		if value == "" {
			return nil
		}

		_, err := shared.ParseByteSizeString(value)
		return err
	},
	"ceph.rbd.clone_copy": shared.IsBool,
	"ceph.user.name":      shared.IsAny,

	// valid drivers: lvm
	"lvm.thinpool_name": shared.IsAny,
	"lvm.use_thinpool":  shared.IsBool,
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

	// Using it as an indicator whether we created the pool or are just
	// re-using it. Note that the valid drivers only list ceph for now. This
	// approach is however generalizable. It's just that we currently don't
	// really need it for the other drivers.
	// valid drivers: ceph
	"volatile.pool.pristine":  shared.IsAny,
	"volatile.initial_source": shared.IsAny,

	// valid drivers: ceph, lvm
	"volume.block.filesystem": func(value string) error {
		return shared.IsOneOf(value, []string{"btrfs", "ext4", "xfs"})
	},
	"volume.block.mount_options": shared.IsAny,

	// valid drivers: ceph, lvm
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
	"rsync.bwlimit":  shared.IsAny,
}

func storagePoolValidateConfig(name string, driver string, config map[string]string, oldConfig map[string]string) error {
	err := func(value string) error {
		return shared.IsOneOf(value, supportedStoragePoolDrivers)
	}(driver)
	if err != nil {
		return err
	}

	if driver == "lvm" {
		v, ok := config["lvm.use_thinpool"]
		if ok && !shared.IsTrue(v) && config["lvm.thinpool_name"] != "" {
			return fmt.Errorf("the key \"lvm.use_thinpool\" cannot be set to a false value when \"lvm.thinpool_name\" is set for LVM storage pools")
		}
	}

	v, ok := config["rsync.bwlimit"]
	if ok && v != "" {
		_, err := shared.ParseByteSizeString(v)
		if err != nil {
			return err
		}
	}

	// Check whether the config properties for the driver container sane
	// values.
	for key, val := range config {
		// Skip unchanged keys
		if oldConfig != nil && oldConfig[key] == val {
			continue
		}

		// User keys are not validated.
		if strings.HasPrefix(key, "user.") {
			continue
		}

		prfx := strings.HasPrefix
		if driver == "dir" || driver == "ceph" {
			if key == "size" {
				return fmt.Errorf("the key %s cannot be used with %s storage pools", key, strings.ToUpper(driver))
			}
		}

		if driver != "lvm" && driver != "ceph" {
			if prfx(key, "lvm.") || prfx(key, "volume.block.") || key == "volume.size" {
				return fmt.Errorf("the key %s cannot be used with %s storage pools", key, strings.ToUpper(driver))
			}
		}

		if driver != "zfs" {
			if prfx(key, "volume.zfs.") || prfx(key, "zfs.") {
				return fmt.Errorf("the key %s cannot be used with %s storage pools", key, strings.ToUpper(driver))
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
	if driver == "dir" || driver == "ceph" {
		if config["size"] != "" {
			return fmt.Errorf(`The "size" property does not apply `+
				`to %s storage pools`, driver)
		}
	} else {
		if config["size"] == "" {
			st := syscall.Statfs_t{}
			err := syscall.Statfs(shared.VarPath(), &st)
			if err != nil {
				return fmt.Errorf("Couldn't statfs %s: %s", shared.VarPath(), err)
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
		// We use thin pools per default.
		useThinpool := true
		if config["lvm.use_thinpool"] != "" {
			useThinpool = shared.IsTrue(config["lvm.use_thinpool"])
		}

		if useThinpool && config["lvm.thinpool_name"] == "" {
			// Unchangeable pool property: Set unconditionally.
			config["lvm.thinpool_name"] = "LXDThinPool"
		}
	}

	if driver == "btrfs" || driver == "ceph" || driver == "lvm" || driver == "zfs" {
		if config["volume.size"] != "" {
			_, err := shared.ParseByteSizeString(config["volume.size"])
			if err != nil {
				return err
			}
		}
	}

	return nil
}
