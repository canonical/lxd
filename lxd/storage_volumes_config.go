package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/units"
)

func init() {
	instance.StorageVolumeFillDefault = storageVolumeFillDefault
}

func storageVolumePropertiesTranslate(targetConfig map[string]string, targetParentPoolDriver string) (map[string]string, error) {
	newConfig := make(map[string]string, len(targetConfig))
	for key, val := range targetConfig {
		// User keys are not validated.
		if strings.HasPrefix(key, "user.") {
			continue
		}

		// Validate storage volume config keys.
		validator, ok := storageVolumeConfigKeys[key]
		if !ok {
			return nil, fmt.Errorf("Invalid storage volume configuration key: %s", key)
		}

		validStorageDrivers, err := validator(val)
		if err != nil {
			return nil, err
		}

		// Drop invalid keys.
		if !shared.StringInSlice(targetParentPoolDriver, validStorageDrivers) {
			continue
		}

		newConfig[key] = val
	}

	return newConfig, nil
}

func updateStoragePoolVolumeError(unchangeable []string, driverName string) error {
	return fmt.Errorf(`The %v properties cannot be changed for "%s" `+
		`storage volumes`, unchangeable, driverName)
}

// For storage volumes of type storagePoolVolumeTypeContainer We only allow to
// change properties directly on the storage volume if there's not
// corresponding way to change it by other means. A good example is the "size"
// property which can be manipulated by setting a root disk device "size"
// property.
var changeableStoragePoolVolumeProperties = map[string][]string{
	"btrfs": {
		"security.shifted",
		"security.unmapped",
		"size",
	},

	"ceph": {
		"security.shifted",
		"block.mount_options",
		"security.unmapped",
		"size"},

	"cephfs": {
		"security.shifted",
		"security.unmapped",
		"size",
	},

	"dir": {
		"security.shifted",
		"security.unmapped",
		"size",
	},

	"lvm": {
		"block.mount_options",
		"security.shifted",
		"security.unmapped",
		"size",
	},

	"zfs": {
		"security.shifted",
		"security.unmapped",
		"size",
		"zfs.remove_snapshots",
		"zfs.use_refquota",
	},
}

// btrfs, ceph, cephfs, dir, lvm, zfs
var storageVolumeConfigKeys = map[string]func(value string) ([]string, error){
	"block.filesystem": func(value string) ([]string, error) {
		err := shared.IsOneOf(value, []string{"btrfs", "ext4", "xfs"})
		if err != nil {
			return nil, err
		}

		return []string{"ceph", "lvm"}, nil
	},
	"block.mount_options": func(value string) ([]string, error) {
		return []string{"ceph", "lvm"}, shared.IsAny(value)
	},
	"security.shifted": func(value string) ([]string, error) {
		return supportedPoolTypes, shared.IsBool(value)
	},
	"security.unmapped": func(value string) ([]string, error) {
		return supportedPoolTypes, shared.IsBool(value)
	},
	"size": func(value string) ([]string, error) {
		if value == "" {
			return []string{"btrfs", "ceph", "cephfs", "lvm", "zfs"}, nil
		}

		_, err := units.ParseByteSizeString(value)
		if err != nil {
			return nil, err
		}

		return []string{"btrfs", "ceph", "cephfs", "lvm", "zfs"}, nil
	},
	"volatile.idmap.last": func(value string) ([]string, error) {
		return supportedPoolTypes, shared.IsAny(value)
	},
	"volatile.idmap.next": func(value string) ([]string, error) {
		return supportedPoolTypes, shared.IsAny(value)
	},
	"zfs.remove_snapshots": func(value string) ([]string, error) {
		err := shared.IsBool(value)
		if err != nil {
			return nil, err
		}

		return []string{"zfs"}, nil
	},
	"zfs.use_refquota": func(value string) ([]string, error) {
		err := shared.IsBool(value)
		if err != nil {
			return nil, err
		}

		return []string{"zfs"}, nil
	},
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

		_, err := validator(val)
		if err != nil {
			return err
		}

		if parentPool.Driver != "zfs" || parentPool.Driver == "dir" {
			if config["zfs.use_refquota"] != "" {
				return fmt.Errorf("the key volume.zfs.use_refquota cannot be used with non zfs storage volumes")
			}

			if config["zfs.remove_snapshots"] != "" {
				return fmt.Errorf("the key volume.zfs.remove_snapshots cannot be used with non zfs storage volumes")
			}
		}

		if parentPool.Driver == "dir" {
			if config["block.mount_options"] != "" {
				return fmt.Errorf("the key block.mount_options cannot be used with dir storage volumes")
			}

			if config["block.filesystem"] != "" {
				return fmt.Errorf("the key block.filesystem cannot be used with dir storage volumes")
			}
		}
	}

	return nil
}

func storageVolumeFillDefault(name string, config map[string]string, parentPool *api.StoragePool) error {
	if parentPool.Driver == "dir" {
		config["size"] = ""
	} else if parentPool.Driver == "lvm" || parentPool.Driver == "ceph" {
		if config["block.filesystem"] == "" {
			config["block.filesystem"] = parentPool.Config["volume.block.filesystem"]
		}
		if config["block.filesystem"] == "" {
			// Unchangeable volume property: Set unconditionally.
			config["block.filesystem"] = "ext4"
		}

		if config["block.mount_options"] == "" {
			config["block.mount_options"] = parentPool.Config["volume.block.mount_options"]
		}
		if config["block.mount_options"] == "" {
			// Unchangeable volume property: Set unconditionally.
			config["block.mount_options"] = "discard"
		}

		// Does the pool request a default size for new storage volumes?
		if config["size"] == "0" || config["size"] == "" {
			config["size"] = parentPool.Config["volume.size"]
		}
		// Does the user explicitly request a default size for new
		// storage volumes?
		if config["size"] == "0" || config["size"] == "" {
			config["size"] = "10GB"
		}
	} else {
		if config["size"] != "" {
			_, err := units.ParseByteSizeString(config["size"])
			if err != nil {
				return err
			}
		}
	}

	return nil
}
