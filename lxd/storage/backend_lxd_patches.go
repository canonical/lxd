package storage

import (
	"github.com/lxc/lxd/lxd/storage/drivers"
)

var lxdEarlyPatches = map[string]func(b *lxdBackend) error{}

var lxdLatePatches = map[string]func(b *lxdBackend) error{
	"storage_create_vm": lxdPatchStorageCreateVM,
}

// Patches start here.
func lxdPatchStorageCreateVM(b *lxdBackend) error {
	return b.createStorageStructure(drivers.GetPoolMountPath(b.name))
}
