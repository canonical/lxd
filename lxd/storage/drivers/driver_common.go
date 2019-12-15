package drivers

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

type common struct {
	name           string
	config         map[string]string
	getVolID       func(volType VolumeType, volName string) (int64, error)
	getCommonRules func() map[string]func(string) error
	state          *state.State
	logger         logger.Logger
}

func (d *common) init(state *state.State, name string, config map[string]string, logger logger.Logger, volIDFunc func(volType VolumeType, volName string) (int64, error), commonRulesFunc func() map[string]func(string) error) error {
	d.name = name
	d.config = config
	d.getVolID = volIDFunc
	d.getCommonRules = commonRulesFunc
	d.state = state
	d.logger = logger

	return d.load()
}

func (d *common) load() error {
	return nil
}

// validateVolume validates a volume config against common rules and optional driver specific rules.
// This functions has a removeUnknownKeys option that if set to true will remove any unknown fields
// (excluding those starting with "user.") which can be used when translating a volume config to a
// different storage driver that has different options.
func (d *common) validateVolume(vol Volume, driverRules map[string]func(value string) error, removeUnknownKeys bool) error {
	checkedFields := map[string]struct{}{}

	// Get rules common for all drivers.
	rules := d.getCommonRules()

	// Merge driver specific rules into common rules.
	for field, validator := range driverRules {
		rules[field] = validator
	}

	// Run the validator against each field.
	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(vol.config[k])
		if err != nil {
			return fmt.Errorf("Invalid value for volume option %s: %v", k, err)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range vol.config {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// User keys are not validated.
		if strings.HasPrefix(k, "user.") {
			continue
		}

		if removeUnknownKeys {
			delete(vol.config, k)
		} else {
			return fmt.Errorf("Invalid volume option: %s", k)
		}
	}

	// If volume type is not custom, don't allow "size" property.
	if vol.volType != VolumeTypeCustom && vol.config["size"] != "" {
		return fmt.Errorf("Volume 'size' property is only valid for custom volume types")
	}

	return nil
}

// MigrationType returns the type of transfer methods to be used when doing migrations between pools
// in preference order.
func (d *common) MigrationTypes(contentType ContentType, refresh bool) []migration.Type {
	if contentType != ContentTypeFS {
		return nil
	}

	return []migration.Type{
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: []string{"xattrs", "delete", "compress", "bidirectional"},
		},
	}
}

// Config returns the storage pool config (as a copy, so not modifiable).
func (d *common) Config() map[string]string {
	confCopy := make(map[string]string, len(d.config))
	for k, v := range d.config {
		confCopy[k] = v
	}

	return confCopy
}

// vfsGetResources is a generic GetResources implementation for VFS-only drivers.
func (d *common) vfsGetResources() (*api.ResourcesStoragePool, error) {
	// Get the VFS information
	st, err := shared.Statvfs(GetPoolMountPath(d.name))
	if err != nil {
		return nil, err
	}

	// Fill in the struct
	res := api.ResourcesStoragePool{}
	res.Space.Total = st.Blocks * uint64(st.Bsize)
	res.Space.Used = (st.Blocks - st.Bfree) * uint64(st.Bsize)

	// Some filesystems don't report inodes since they allocate them
	// dynamically e.g. btrfs.
	if st.Files > 0 {
		res.Inodes.Total = st.Files
		res.Inodes.Used = st.Files - st.Ffree
	}

	return &res, nil
}

// vfsRenameVolume is a generic RenameVolume implementation for VFS-only drivers.
func (d *common) vfsRenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	// Create new snapshots directory.
	snapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, newVolName)

	err := os.MkdirAll(snapshotDir, 0711)
	if err != nil {
		return err
	}

	type volRevert struct {
		oldPath string
		newPath string
	}

	// Create slice to record paths renamed if revert needed later.
	revertPaths := []volRevert{}
	defer func() {
		// Remove any paths rename if we are reverting.
		for _, vol := range revertPaths {
			os.Rename(vol.newPath, vol.oldPath)
		}

		// Remove the new snapshot directory if we are reverting.
		if len(revertPaths) > 0 {
			err = os.RemoveAll(snapshotDir)
		}
	}()

	// Rename any snapshots of the volume too.
	snapshots, err := vol.Snapshots(op)
	if err != nil {
		return err
	}

	for _, snapshot := range snapshots {
		oldPath := snapshot.MountPath()
		_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snapshot.name)
		newPath := GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(newVolName, snapName))

		err := os.Rename(oldPath, newPath)
		if err != nil {
			return err
		}

		revertPaths = append(revertPaths, volRevert{
			oldPath: oldPath,
			newPath: newPath,
		})
	}

	oldPath := GetVolumeMountPath(d.name, vol.volType, vol.name)
	newPath := GetVolumeMountPath(d.name, vol.volType, newVolName)
	err = os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	revertPaths = append(revertPaths, volRevert{
		oldPath: oldPath,
		newPath: newPath,
	})

	// Remove old snapshots directory.
	oldSnapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)

	err = os.RemoveAll(oldSnapshotDir)
	if err != nil {
		return err
	}

	revertPaths = nil
	return nil
}

// vfsVolumeSnapshots is a generic VolumeSnapshots implementation for VFS-only drivers.
func (d *common) vfsVolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	snapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)
	snapshots := []string{}

	ents, err := ioutil.ReadDir(snapshotDir)
	if err != nil {
		// If the snapshots directory doesn't exist, there are no snapshots.
		if os.IsNotExist(err) {
			return snapshots, nil
		}

		return nil, err
	}

	for _, ent := range ents {
		fileInfo, err := os.Stat(filepath.Join(snapshotDir, ent.Name()))
		if err != nil {
			return nil, err
		}

		if !fileInfo.IsDir() {
			continue
		}

		snapshots = append(snapshots, ent.Name())
	}

	return snapshots, nil
}

// vfsRenameVolumeSnapshot is a generic RenameVolumeSnapshot implementation for VFS-only drivers.
func (d *common) vfsRenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	parentName, _, _ := shared.InstanceGetParentAndSnapshotName(snapVol.name)
	oldPath := snapVol.MountPath()
	newPath := GetVolumeMountPath(d.name, snapVol.volType, GetSnapshotVolumeName(parentName, newSnapshotName))

	err := os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	return nil
}
