package drivers

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
)

type common struct {
	name                 string
	config               map[string]string
	getVolID             func(volType VolumeType, volName string) (int64, error)
	getCommonVolumeRules func(vol Volume) map[string]func(string) error
	state                *state.State
	logger               logger.Logger
	patches              map[string]func() error
}

func (d *common) init(state *state.State, name string, config map[string]string, logger logger.Logger, volIDFunc func(volType VolumeType, volName string) (int64, error), commonVolRulesFunc func(vol Volume) map[string]func(string) error) {
	d.name = name
	d.config = config
	d.getVolID = volIDFunc
	d.getCommonVolumeRules = commonVolRulesFunc
	d.state = state
	d.logger = logger
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
	rules := d.getCommonVolumeRules(vol)

	// Merge driver specific rules into common rules.
	for field, validator := range driverRules {
		rules[field] = validator
	}

	// Run the validator against each field.
	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(vol.config[k])
		if err != nil {
			return errors.Wrapf(err, "Invalid value for volume option %s", k)
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
	return []migration.Type{
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: []string{"xattrs", "delete", "compress", "bidirectional"},
		},
	}
}

// Name returns the pool name.
func (d *common) Name() string {
	return d.name
}

// Logger returns the current logger.
func (d *common) Logger() logger.Logger {
	return d.logger
}

// Config returns the storage pool config (as a copy, so not modifiable).
func (d *common) Config() map[string]string {
	confCopy := make(map[string]string, len(d.config))
	for k, v := range d.config {
		confCopy[k] = v
	}

	return confCopy
}

// ApplyPatch looks for a suitable patch and runs it.
func (d *common) ApplyPatch(name string) error {
	if d.patches == nil {
		return fmt.Errorf("The patch mechanism isn't implemented on pool '%s'", d.name)
	}

	// Locate the patch.
	patch, ok := d.patches[name]
	if !ok {
		return fmt.Errorf("Patch '%s' isn't implemented on pool '%s'", name, d.name)
	}

	// Handle cases where a patch isn't needed.
	if patch == nil {
		return nil
	}

	return patch()
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
	// Rename the volume itself.
	srcVolumePath := GetVolumeMountPath(d.name, vol.volType, vol.name)
	dstVolumePath := GetVolumeMountPath(d.name, vol.volType, newVolName)

	err := os.Rename(srcVolumePath, dstVolumePath)
	if err != nil {
		return errors.Wrapf(err, "Failed to rename '%s' to '%s'", srcVolumePath, dstVolumePath)
	}

	revertRename := true
	defer func() {
		if !revertRename {
			return
		}

		os.Rename(dstVolumePath, srcVolumePath)
	}()

	// And if present, the snapshots too.
	srcSnapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)
	dstSnapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, newVolName)

	if shared.PathExists(srcSnapshotDir) {
		err = os.Rename(srcSnapshotDir, dstSnapshotDir)
		if err != nil {
			return errors.Wrapf(err, "Failed to rename '%s' to '%s'", srcSnapshotDir, dstSnapshotDir)
		}
	}

	revertRename = false
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

		return nil, errors.Wrapf(err, "Failed to list directory '%s'", snapshotDir)
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
		return errors.Wrapf(err, "Failed to rename '%s' to '%s'", oldPath, newPath)
	}

	return nil
}

// vfsMigrateVolume is a generic MigrateVolume implementation for VFS-only drivers.
func (d *common) vfsMigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	bwlimit := d.config["rsync.bwlimit"]

	for _, snapName := range volSrcArgs.Snapshots {
		snapshot, err := vol.NewSnapshot(snapName)
		if err != nil {
			return err
		}

		// Send snapshot to recipient (ensure local snapshot volume is mounted if needed).
		err = snapshot.MountTask(func(mountPath string, op *operations.Operation) error {
			var wrapper *ioprogress.ProgressTracker
			if volSrcArgs.TrackProgress {
				wrapper = migration.ProgressTracker(op, "fs_progress", snapshot.name)
			}

			path := shared.AddSlash(mountPath)
			return rsync.Send(snapshot.name, path, conn, wrapper, volSrcArgs.MigrationType.Features, bwlimit, d.state.OS.ExecPath)
		}, op)
		if err != nil {
			return err
		}
	}

	// Send volume to recipient (ensure local volume is mounted if needed).
	return vol.MountTask(func(mountPath string, op *operations.Operation) error {
		var wrapper *ioprogress.ProgressTracker
		if volSrcArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
		}

		path := shared.AddSlash(mountPath)
		return rsync.Send(vol.name, path, conn, wrapper, volSrcArgs.MigrationType.Features, bwlimit, d.state.OS.ExecPath)
	}, op)
}

// vfsHasVolume is a generic HasVolume implementation for VFS-only drivers.
func (d *common) vfsHasVolume(vol Volume) bool {
	if shared.PathExists(vol.MountPath()) {
		return true
	}

	return false
}

// vfsGetVolumeDiskPath is a generic GetVolumeDiskPath implementation for VFS-only drivers.
func (d *common) vfsGetVolumeDiskPath(vol Volume) (string, error) {
	if vol.contentType != ContentTypeBlock {
		return "", fmt.Errorf("No disk paths for filesystems")
	}

	return filepath.Join(vol.MountPath(), "root.img"), nil
}

// vfsBackupVolume is a generic BackupVolume implementation for VFS-only drivers.
func (d *common) vfsBackupVolume(vol Volume, targetPath string, snapshots bool, op *operations.Operation) error {
	bwlimit := d.config["rsync.bwlimit"]

	// Backups only implemented for containers currently.
	if vol.volType != VolumeTypeContainer {
		return ErrNotImplemented
	}
	// Handle snapshots.
	if snapshots {
		snapshotsPath := filepath.Join(targetPath, "snapshots")

		// List the snapshots.
		snapshots, err := vol.Snapshots(op)
		if err != nil {
			return err
		}

		// Create the snapshot path.
		if len(snapshots) > 0 {
			err = os.MkdirAll(snapshotsPath, 0711)
			if err != nil {
				return errors.Wrapf(err, "Failed to create directory '%s'", snapshotsPath)
			}
		}

		for _, snapshot := range snapshots {
			_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snapshot.Name())
			target := filepath.Join(snapshotsPath, snapName)

			// Copy the snapshot.
			err = snapshot.MountTask(func(mountPath string, op *operations.Operation) error {
				_, err := rsync.LocalCopy(mountPath, target, bwlimit, true)
				if err != nil {
					return err
				}

				return nil
			}, op)
			if err != nil {
				return err
			}
		}
	}

	// Copy the parent volume itself.
	target := filepath.Join(targetPath, "container")
	err := vol.MountTask(func(mountPath string, op *operations.Operation) error {
		_, err := rsync.LocalCopy(mountPath, target, bwlimit, true)
		if err != nil {
			return err
		}

		return nil
	}, op)
	if err != nil {
		return err
	}

	return nil
}
