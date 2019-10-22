package main

import (
	"fmt"
)

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
