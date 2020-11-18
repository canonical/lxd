package main

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	storageDrivers "github.com/grant-he/lxd/lxd/storage/drivers"
	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/units"
	"github.com/grant-he/lxd/shared/validate"
)

var storagePoolConfigKeys = map[string]func(value string) error{
	// valid drivers: btrfs
	// (Note, that we can't be smart in detecting mount options since a lot
	// of filesystems come with their own additional ones (e.g.
	// "user_subvol_rm_allowed" for btrfs or "zfsutils" for zfs). So
	// shared.IsAny() must do.)
	"btrfs.mount_options": validate.IsAny,

	// valid drivers: ceph
	"ceph.cluster_name":       validate.IsAny,
	"ceph.osd.force_reuse":    validate.Optional(validate.IsBool),
	"ceph.osd.pool_name":      validate.IsAny,
	"ceph.osd.data_pool_name": validate.IsAny,
	"ceph.osd.pg_num": func(value string) error {
		if value == "" {
			return nil
		}

		_, err := units.ParseByteSizeString(value)
		return err
	},
	"ceph.rbd.clone_copy": validate.Optional(validate.IsBool),
	"ceph.user.name":      validate.IsAny,

	// valid drivers: cephfs
	"cephfs.cluster_name": validate.IsAny,
	"cephfs.path":         validate.IsAny,
	"cephfs.user.name":    validate.IsAny,

	// valid drivers: lvm
	"lvm.thinpool_name":       validate.IsAny,
	"lvm.use_thinpool":        validate.Optional(validate.IsBool),
	"lvm.vg_name":             validate.IsAny,
	"volume.lvm.stripes":      validate.Optional(validate.IsUint32),
	"volume.lvm.stripes.size": validate.Optional(validate.IsSize),
	"lvm.vg.force_reuse":      validate.Optional(validate.IsBool),

	// valid drivers: btrfs, lvm, zfs
	"size": validate.Optional(validate.IsSize),

	// valid drivers: btrfs, dir, lvm, zfs
	"source": validate.IsAny,

	// Using it as an indicator whether we created the pool or are just
	// re-using it. Note that the valid drivers only list ceph for now. This
	// approach is however generalizable. It's just that we currently don't
	// really need it for the other drivers.
	// valid drivers: ceph
	"volatile.pool.pristine":  validate.IsAny,
	"volatile.initial_source": validate.IsAny,

	// valid drivers: ceph, lvm
	"volume.block.filesystem": func(value string) error {
		return validate.IsOneOf(value, []string{"btrfs", "ext4", "xfs"})
	},
	"volume.block.mount_options": validate.IsAny,

	// valid drivers: ceph, lvm
	"volume.size": validate.Optional(validate.IsSize),

	// valid drivers: zfs
	"volume.zfs.remove_snapshots": validate.Optional(validate.IsBool),
	"volume.zfs.use_refquota":     validate.Optional(validate.IsBool),

	// valid drivers: zfs
	"zfs.clone_copy": validate.Optional(func(value string) error {
		if value == "rebase" {
			return nil
		}

		return validate.IsBool(value)
	}),
	"zfs.pool_name": validate.IsAny,
	"rsync.bwlimit": validate.IsAny,

	// valid drivers: btrfs, ceph, cephfs, zfs
	"rsync.compression": validate.Optional(validate.IsBool),
}

func storagePoolValidateConfig(name string, driver string, config map[string]string, oldConfig map[string]string) error {
	err := func(value string) error {
		return validate.IsOneOf(value, storageDrivers.AllDriverNames())
	}(driver)
	if err != nil {
		return err
	}

	v, ok := config["rsync.bwlimit"]
	if ok && v != "" {
		_, err := units.ParseByteSizeString(v)
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
		if driver == "dir" || driver == "ceph" || driver == "cephfs" {
			if key == "size" {
				return fmt.Errorf("the key %s cannot be used with %s storage pools", key, strings.ToUpper(driver))
			}
		}

		if driver != "lvm" {
			if prfx(key, "lvm.") {
				return fmt.Errorf("the key %s cannot be used with %s storage pools", key, strings.ToUpper(driver))
			}
		}

		if driver != "lvm" && driver != "ceph" {
			if prfx(key, "volume.block.") {
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
	if driver == "dir" || driver == "ceph" || driver == "cephfs" {
		if config["size"] != "" {
			return fmt.Errorf(`The "size" property does not apply `+
				`to %s storage pools`, driver)
		}
	} else {
		if config["size"] == "" {
			st := unix.Statfs_t{}
			err := unix.Statfs(shared.VarPath(), &st)
			if err != nil {
				return fmt.Errorf("Couldn't statfs %s: %s", shared.VarPath(), err)
			}

			/* choose 5 GB < x < 30GB, where x is 20% of the disk size */
			defaultSize := uint64(st.Frsize) * st.Blocks / (1024 * 1024 * 1024) / 5
			if defaultSize > 30 {
				defaultSize = 30
			}
			if defaultSize < 5 {
				defaultSize = 5
			}

			config["size"] = strconv.FormatUint(uint64(defaultSize), 10) + "GB"
		} else {
			_, err := units.ParseByteSizeString(config["size"])
			if err != nil {
				return err
			}
		}
	}

	if driver == "lvm" {
		// We use thin pools by default.
		useThinpool := true
		if config["lvm.use_thinpool"] != "" {
			useThinpool = shared.IsTrue(config["lvm.use_thinpool"])
		}

		if useThinpool && config["lvm.thinpool_name"] == "" {
			// Unchangeable pool property: Set unconditionally.
			config["lvm.thinpool_name"] = "LXDThinPool"
		}
	}

	if config["volume.size"] != "" {
		_, err := units.ParseByteSizeString(config["volume.size"])
		if err != nil {
			return err
		}
	}

	return nil
}
