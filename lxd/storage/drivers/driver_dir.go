package drivers

import (
	"fmt"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

type dir struct {
	name   string
	path   string
	config map[string]string
}

// Functions used by the loader
func (d *dir) create(dbPool *api.StoragePool) error {
	// WARNING: The create() function cannot rely on any of the struct attributes being set

	// Set default source if missing
	if dbPool.Config["source"] == "" {
		dbPool.Config["source"] = shared.VarPath("storage-pools", dbPool.Name)
	}

	if !shared.PathExists(dbPool.Config["source"]) {
		return fmt.Errorf("Source path '%s' doesn't exist", dbPool.Config["source"])
	}

	// Check that the path is currently empty
	isEmpty, err := shared.PathIsEmpty(dbPool.Config["source"])
	if err != nil {
		return err
	}

	if !isEmpty {
		return fmt.Errorf("Source path '%s' isn't empty", dbPool.Config["source"])
	}

	return nil
}

func (d *dir) Name() string {
	return "dir"
}

func (d *dir) Version() string {
	return "1"
}

func (d *dir) Delete(op *operations.Operation) error {
	// On delete, wipe everything in the directory
	err := wipeDirectory(d.path)
	if err != nil {
		return err
	}

	// Unmount the path
	_, err = d.Unmount()
	if err != nil {
		return err
	}

	return nil
}

func (d *dir) Mount() (bool, error) {
	// Check if we're dealing with an external mount
	if d.config["source"] == d.path {
		return false, nil
	}

	// Check if already mounted
	if sameMount(d.config["source"], d.path) {
		return false, nil
	}

	// Setup the bind-mount
	err := tryMount(d.config["source"], d.path, "none", unix.MS_BIND, "")
	if err != nil {
		return false, err
	}

	return true, nil
}

func (d *dir) Unmount() (bool, error) {
	// Check if we're dealing with an external mount
	if d.config["source"] == d.path {
		return false, nil
	}

	// Unmount until nothing is left mounted
	return forceUnmount(d.path)
}

func (d *dir) GetResources() (*api.ResourcesStoragePool, error) {
	// Use the generic VFS resources
	return vfsResources(d.path)
}

func (d *dir) DeleteVolume(volType VolumeType, name string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (d *dir) RenameVolume(volType VolumeType, name string, newName string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (d *dir) MigrationType() migration.MigrationFSType {
	return migration.MigrationFSType_RSYNC
}

func (d *dir) PreservesInodes() bool {
	return false
}
