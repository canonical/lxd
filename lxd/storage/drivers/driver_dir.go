package drivers

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

type dir struct {
	common
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *dir) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_create_vm":                        nil,
		"storage_zfs_mount":                        nil,
		"storage_create_vm_again":                  nil,
		"storage_zfs_volmode":                      nil,
		"storage_rename_custom_volume_add_project": nil,
		"storage_lvm_skipactivation":               nil,
	}

	return nil
}

// Info returns info about the driver and its environment.
func (d *dir) Info() Info {
	return Info{
		Name:               "dir",
		Version:            "1",
		OptimizedImages:    false,
		PreservesInodes:    false,
		Remote:             d.isRemote(),
		VolumeTypes:        []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		BlockBacking:       false,
		RunningQuotaResize: true,
		RunningCopyFreeze:  true,
		DirectIO:           true,
		MountedRoot:        true,
	}
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *dir) Create() error {
	// Set default source if missing.
	if d.config["source"] == "" {
		d.config["source"] = GetPoolMountPath(d.name)
	}

	sourcePath := shared.HostPath(d.config["source"])

	if !shared.PathExists(sourcePath) {
		return fmt.Errorf("Source path '%s' doesn't exist", sourcePath)
	}

	// Check that if within LXD_DIR, we're at our expected spot.
	cleanSource := filepath.Clean(sourcePath)
	if strings.HasPrefix(cleanSource, shared.VarPath()) && cleanSource != GetPoolMountPath(d.name) {
		return fmt.Errorf("Source path '%s' is within the LXD directory", cleanSource)
	}

	// Check that the path is currently empty.
	isEmpty, err := shared.PathIsEmpty(sourcePath)
	if err != nil {
		return err
	}

	if !isEmpty {
		return fmt.Errorf("Source path '%s' isn't empty", sourcePath)
	}

	return nil
}

// Delete removes the storage pool from the storage device.
func (d *dir) Delete(op *operations.Operation) error {
	// On delete, wipe everything in the directory.
	err := wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	// Unmount the path.
	_, err = d.Unmount()
	if err != nil {
		return err
	}

	return nil
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *dir) Validate(config map[string]string) error {
	return d.validatePool(config, nil)
}

// Update applies any driver changes required from a configuration change.
func (d *dir) Update(changedConfig map[string]string) error {
	return nil
}

// Mount mounts the storage pool.
func (d *dir) Mount() (bool, error) {
	path := GetPoolMountPath(d.name)
	sourcePath := shared.HostPath(d.config["source"])

	// Check if we're dealing with an external mount.
	if sourcePath == path {
		return false, nil
	}

	// Check if already mounted.
	if sameMount(sourcePath, path) {
		return false, nil
	}

	// Setup the bind-mount.
	err := TryMount(sourcePath, path, "none", unix.MS_BIND, "")
	if err != nil {
		return false, err
	}

	return true, nil
}

// Unmount unmounts the storage pool.
func (d *dir) Unmount() (bool, error) {
	path := GetPoolMountPath(d.name)

	// Check if we're dealing with an external mount.
	if d.config["source"] == path {
		return false, nil
	}

	// Unmount until nothing is left mounted.
	return forceUnmount(path)
}

// GetResources returns the pool resource usage information.
func (d *dir) GetResources() (*api.ResourcesStoragePool, error) {
	return genericVFSGetResources(d)
}
