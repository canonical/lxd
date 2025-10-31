package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

type dir struct {
	common
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *dir) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_lvm_skipactivation":                         nil,
		"storage_missing_snapshot_records":                   nil,
		"storage_delete_old_snapshot_records":                nil,
		"storage_zfs_drop_block_volume_filesystem_extension": nil,
		"storage_prefix_bucket_names_with_project":           nil,
	}

	return nil
}

// Info returns info about the driver and its environment.
func (d *dir) Info() Info {
	return Info{
		Name:                         "dir",
		Version:                      "1",
		DefaultBlockSize:             d.defaultBlockVolumeSize(),
		DefaultVMBlockFilesystemSize: d.defaultVMBlockFilesystemSize(),
		OptimizedImages:              false,
		PreservesInodes:              false,
		Remote:                       d.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeBucket, VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		BlockBacking:                 false,
		RunningCopyFreeze:            true,
		DirectIO:                     true,
		IOUring:                      true,
		MountedRoot:                  true,
		Buckets:                      true,
		PopulateParentVolumeUUID:     false,
	}
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *dir) FillConfig() error {
	// Set default source if missing.
	if d.config["source"] == "" {
		d.config["source"] = GetPoolMountPath(d.name)
	}

	return nil
}

// SourceIdentifier returns the underlying source.
func (d *dir) SourceIdentifier() (string, error) {
	source := d.config["source"]
	if source != "" {
		return source, nil
	}

	return "", errors.New("Cannot derive identifier from empty source")
}

// ValidateSource checks whether the required config keys are valid to access the underlying source.
func (d *dir) ValidateSource() error {
	sourcePath := shared.HostPath(d.config["source"])

	if !shared.PathExists(sourcePath) {
		return fmt.Errorf("Source path %q doesn't exist", sourcePath)
	}

	// Check that if within LXD_DIR, we're at our expected spot.
	cleanSource := filepath.Clean(sourcePath)
	if strings.HasPrefix(cleanSource, shared.VarPath()) && cleanSource != GetPoolMountPath(d.name) {
		return fmt.Errorf("Source path %q is within the LXD directory", cleanSource)
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *dir) Create() error {
	sourcePath := shared.HostPath(d.config["source"])

	// Check that the path is currently empty.
	isEmpty, err := shared.PathIsEmpty(sourcePath)
	if err != nil {
		return err
	}

	if !isEmpty {
		// If directory is not empty, the "lost+found" subdirectory is acceptable when
		// the source path is the root of a mounted filesystem.
		if !filesystem.IsMountPoint(sourcePath) {
			return fmt.Errorf("Source path %q isn't empty", sourcePath)
		}

		entries, err := os.ReadDir(sourcePath)
		if err != nil {
			return fmt.Errorf("Failed to read directory content of source path %q", sourcePath)
		}

		for _, e := range entries {
			if e.Name() != "lost+found" {
				return fmt.Errorf("Source path %q isn't empty", sourcePath)
			}
		}
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
	return d.validatePool(config, nil, nil)
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
	err := TryMount(context.TODO(), sourcePath, path, "none", unix.MS_BIND, "")
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
