package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/canonical/lxd/lxd/config"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/rsync"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
)

func unmountDaemonStorageVolume(s *state.State, daemonStorageVolume string) error {
	// Parse the source.
	poolName, volumeName, err := daemonStorageSplitVolume(daemonStorageVolume)
	if err != nil {
		return err
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return err
	}

	// Unmount volume.
	_, err = pool.UnmountCustomVolume(api.ProjectDefaultName, volumeName, nil)
	if err != nil && !errors.Is(err, storageDrivers.ErrInUse) {
		return fmt.Errorf("Failed to unmount storage volume %q: %w", daemonStorageVolume, err)
	}

	return nil
}

func daemonStorageVolumesUnmount(s *state.State, ctx context.Context) error {
	storageBackups := s.LocalConfig.StorageBackupsVolume("")
	storageImages := s.LocalConfig.StorageImagesVolume("")

	select {
	case <-ctx.Done():
		return errors.New("Timed out waiting for image and backup volume")
	default:
		if storageBackups != "" {
			err := unmountDaemonStorageVolume(s, storageBackups)
			if err != nil {
				return fmt.Errorf("Failed to unmount backups storage: %w", err)
			}
		}

		if storageImages != "" {
			err := unmountDaemonStorageVolume(s, storageImages)
			if err != nil {
				return fmt.Errorf("Failed to unmount images storage: %w", err)
			}
		}

		for key, value := range s.LocalConfig.Dump() {
			// Look for all the project storage volumes.
			projectName, _ := config.ParseDaemonStorageConfigKey(key)
			if projectName == "" {
				continue
			}

			err := unmountDaemonStorageVolume(s, value)
			if err != nil {
				return fmt.Errorf("Failed to unmount project storage volume %q set under node config key %q: %w", value, key, err)
			}
		}
	}

	return nil
}

func mountDaemonStorageVolume(s *state.State, daemonStorageVolume string) error {
	// Parse the source.
	poolName, volumeName, err := daemonStorageSplitVolume(daemonStorageVolume)
	if err != nil {
		return err
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return err
	}

	// Mount volume.
	_, err = pool.MountCustomVolume(api.ProjectDefaultName, volumeName, nil)
	if err != nil {
		return fmt.Errorf("Failed to mount storage volume %q: %w", daemonStorageVolume, err)
	}

	return nil
}

func daemonStorageMount(s *state.State) error {
	storageBackups := s.LocalConfig.StorageBackupsVolume("")
	storageImages := s.LocalConfig.StorageImagesVolume("")

	if storageBackups != "" {
		err := mountDaemonStorageVolume(s, storageBackups)
		if err != nil {
			return fmt.Errorf("Failed to mount backups storage: %w", err)
		}
	}

	if storageImages != "" {
		err := mountDaemonStorageVolume(s, storageImages)
		if err != nil {
			return fmt.Errorf("Failed to mount images storage: %w", err)
		}
	}

	for key, value := range s.LocalConfig.Dump() {
		// Look for all the project storage volumes.
		projectName, _ := config.ParseDaemonStorageConfigKey(key)
		if projectName == "" {
			continue
		}

		err := mountDaemonStorageVolume(s, value)
		if err != nil {
			return fmt.Errorf("Failed to mount project storage volume %q set under node config key %q: %w", value, key, err)
		}
	}

	return nil
}

// Mount the volume specified by `daemonStorageVolume` (in the form of pool/volume)
// and create the necessary directory structure to use it as a `storageType` (either `images` or `backups`) storage.
func projectStorageVolumeSetup(s *state.State, daemonStorageVolume string, storageType config.DaemonStorageType) error {
	revert := revert.New()
	defer revert.Fail()

	err := mountDaemonStorageVolume(s, daemonStorageVolume)
	if err != nil {
		return fmt.Errorf("Failed to setup project %s storage: %w", storageType, err)
	}

	revert.Add(func() { _ = unmountDaemonStorageVolume(s, daemonStorageVolume) })

	// Ensure the destination directory structure exists within the target volume.
	path := daemonStoragePath(daemonStorageVolume, storageType)
	fileinfo, err := os.Stat(path)
	if err == nil {
		if !fileinfo.IsDir() {
			return fmt.Errorf("Cannot create directory, file already exists: %q", path)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		err = os.Mkdir(path, 0700)
		if err != nil {
			return fmt.Errorf("Failed to create directory %q: %w", path, err)
		}

		revert.Add(func() { os.Remove(path) })
	} else {
		return fmt.Errorf("Failed to stat() file %q: %w", path, err)
	}

	// Set ownership & mode.
	err = os.Chmod(path, 0700)
	if err != nil {
		return fmt.Errorf("Failed to set permissions on %q: %w", path, err)
	}

	err = os.Chown(path, 0, 0)
	if err != nil {
		return fmt.Errorf("Failed to set ownership on %q: %w", path, err)
	}

	revert.Success()
	return nil
}

// projectStorageVolumeChange handles changes of one of the storage.images_volume or storage.backups.volume configs.
// As these configs can be changed only on empty projects, we don't move any images around. Instead we only
// mount the volumes as needed.
func projectStorageVolumeChange(s *state.State, oldConfig string, newConfig string, storageType config.DaemonStorageType) error {
	if oldConfig != "" {
		err := unmountDaemonStorageVolume(s, oldConfig)
		if err != nil {
			return fmt.Errorf("Failed to unmount images storage: %w", err)
		}
	}

	if newConfig != "" {
		err := projectStorageVolumeSetup(s, newConfig, storageType)
		if err != nil {
			return fmt.Errorf("Failed to setup project %s storage: %w", storageType, err)
		}
	}

	return nil
}

func daemonStorageSplitVolume(volume string) (poolName string, volumeName string, err error) {
	if strings.Count(volume, "/") != 1 {
		return "", "", errors.New("Invalid syntax for volume, must be <pool>/<volume>")
	}

	poolName, volumeName, _ = strings.Cut(volume, "/")

	err = storageDrivers.ValidPoolName(poolName)
	if err != nil {
		return "", "", fmt.Errorf("Invalid pool name %q: %w", poolName, err)
	}

	err = storageDrivers.ValidVolumeName(volumeName)
	if err != nil {
		return "", "", fmt.Errorf("Invalid volume name %q: %w", volumeName, err)
	}

	return poolName, volumeName, nil
}

// daemonStorageValidate checks target "<poolName>/<volumeName>" value and returns the validated target from DB.
func daemonStorageValidate(s *state.State, target string) (validatedTarget string, err error) {
	// Check syntax.
	if target == "" {
		return "", nil
	}

	poolName, volumeName, err := daemonStorageSplitVolume(target)
	if err != nil {
		return "", err
	}

	// Validate pool exists.
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return "", fmt.Errorf("Failed loading storage pool %q: %w", poolName, err)
	}

	poolState := pool.Status()
	if poolState != api.StoragePoolStatusCreated {
		return "", fmt.Errorf("Storage pool %q cannot be used when in %q status", poolName, poolState)
	}

	// Checking only for remote storage drivers isn't sufficient as drivers
	// like CephFS can be safely used as the volume can be used on multiple nodes concurrently.
	if pool.Driver().Info().Remote && !pool.Driver().Info().VolumeMultiNode {
		return "", fmt.Errorf("Remote storage pool %q cannot be used", poolName)
	}

	var snapshots []db.StorageVolumeArgs
	var dbVol *db.StorageVolume

	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Confirm volume exists.
		dbVol, err = tx.GetStoragePoolVolume(ctx, pool.ID(), api.ProjectDefaultName, cluster.StoragePoolVolumeTypeCustom, volumeName, true)
		if err != nil {
			return fmt.Errorf("Failed loading storage volume %q in %q project: %w", target, api.ProjectDefaultName, err)
		}

		if dbVol.ContentType != cluster.StoragePoolVolumeContentTypeNameFS {
			return fmt.Errorf("Storage volume %q in %q project is not filesystem content type", target, api.ProjectDefaultName)
		}

		snapshots, err = tx.GetLocalStoragePoolVolumeSnapshotsWithType(ctx, api.ProjectDefaultName, volumeName, cluster.StoragePoolVolumeTypeCustom, pool.ID())
		if err != nil {
			return fmt.Errorf("Unable to load storage volume snapshots %q in %q project: %w", target, api.ProjectDefaultName, err)
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	if len(snapshots) != 0 {
		return "", errors.New("Storage volumes for use by LXD itself cannot have snapshots")
	}

	// Mount volume.
	_, err = pool.MountCustomVolume(api.ProjectDefaultName, volumeName, nil)
	if err != nil {
		return "", fmt.Errorf("Failed to mount storage volume %q: %w", target, err)
	}

	defer func() { _, _ = pool.UnmountCustomVolume(api.ProjectDefaultName, volumeName, nil) }()

	// Validate volume is empty (ignore lost+found).
	volStorageName := project.StorageVolume(api.ProjectDefaultName, volumeName)
	mountpoint := storageDrivers.GetVolumeMountPath(poolName, storageDrivers.VolumeTypeCustom, volStorageName)

	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		return "", fmt.Errorf("Failed to list %q: %w", mountpoint, err)
	}

	allowedEntries := []string{
		"lost+found", // Clean ext4 volumes.
		".zfs",       // Systems with snapdir=visible
		"images",
		"backups", // Allow re-use of volume for multiple images and backups stores.
	}

	for _, entry := range entries {
		entryName := entry.Name()

		// Don't fail on entries known to be possibly present.
		if slices.Contains(allowedEntries, entryName) {
			continue
		}

		return "", fmt.Errorf("Storage volume %q isn't empty", target)
	}

	return pool.Name() + "/" + dbVol.Name, nil
}

// daemonStoragePath returns the full path for a daemon storage located on the specific volume.
// The `storageType` is either `images`, or `backups`.
// The `daemonStorageVolume` is the specific volume in the form of "pool/volume".
func daemonStoragePath(daemonStorageVolume string, storageType config.DaemonStorageType) string {
	if daemonStorageVolume == "" {
		return shared.VarPath(string(storageType))
	}

	poolName, volumeName, _ := daemonStorageSplitVolume(daemonStorageVolume)
	volStorageName := project.StorageVolume(api.ProjectDefaultName, volumeName)
	volMountPath := storageDrivers.GetVolumeMountPath(poolName, storageDrivers.VolumeTypeCustom, volStorageName)
	return filepath.Join(volMountPath, string(storageType))
}

func daemonStorageMove(s *state.State, storageType config.DaemonStorageType, oldConfig string, newconfig string) error {
	destPath := shared.VarPath(string(storageType))
	var sourcePath string
	var sourcePool string
	var sourceVolume string
	var err error

	// Track down the current storage.
	if oldConfig == "" {
		sourcePath = destPath
	} else {
		sourcePool, sourceVolume, err = daemonStorageSplitVolume(oldConfig)

		if err != nil {
			return err
		}

		sourceVolume = project.StorageVolume(api.ProjectDefaultName, sourceVolume)
		sourcePath = storageDrivers.GetVolumeMountPath(sourcePool, storageDrivers.VolumeTypeCustom, sourceVolume)
		sourcePath = filepath.Join(sourcePath, string(storageType))
	}

	moveContent := func(source string, target string) error {
		// Copy the content.
		_, err := rsync.LocalCopy(source, target, "", false)
		if err != nil {
			return err
		}

		return os.RemoveAll(source)
	}

	// Deal with unsetting.
	if newconfig == "" {
		// Things already look correct.
		if sourcePath == destPath {
			return nil
		}

		// Re-create the directory.
		err := os.MkdirAll(destPath, 0700)
		if err != nil {
			return fmt.Errorf("Failed to create directory %q: %w", destPath, err)
		}

		// Move the data across.
		err = moveContent(sourcePath, destPath)
		if err != nil {
			return fmt.Errorf("Failed to move data over to directory %q: %w", destPath, err)
		}

		pool, err := storagePools.LoadByName(s, sourcePool)
		if err != nil {
			return err
		}

		// Unmount old volume if noone else is using it.
		projectName, sourceVolumeName := project.StorageVolumeParts(sourceVolume)
		_, err = pool.UnmountCustomVolume(projectName, sourceVolumeName, nil)
		if err != nil && !errors.Is(err, storageDrivers.ErrInUse) {
			return fmt.Errorf(`Failed to umount storage volume "%s/%s": %w`, sourcePool, sourceVolumeName, err)
		}

		return nil
	}

	// Parse the target.
	poolName, volumeName, err := daemonStorageSplitVolume(newconfig)
	if err != nil {
		return err
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return err
	}

	// Mount volume.
	_, err = pool.MountCustomVolume(api.ProjectDefaultName, volumeName, nil)
	if err != nil {
		return fmt.Errorf("Failed to mount storage volume %q: %w", newconfig, err)
	}

	// Ensure the destination directory structure exists within the target volume.
	destPath = daemonStoragePath(newconfig, storageType)
	err = os.MkdirAll(destPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to create directory %q: %w", destPath, err)
	}

	// Set ownership & mode.
	err = os.Chmod(destPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to set permissions on %q: %w", destPath, err)
	}

	err = os.Chown(destPath, 0, 0)
	if err != nil {
		return fmt.Errorf("Failed to set ownership on %q: %w", destPath, err)
	}

	// Handle changes.
	if sourcePath != shared.VarPath(string(storageType)) {
		// Move the data across.
		err = moveContent(sourcePath, destPath)
		if err != nil {
			return fmt.Errorf("Failed to move data over to directory %q: %w", destPath, err)
		}

		pool, err := storagePools.LoadByName(s, sourcePool)
		if err != nil {
			return err
		}

		// Unmount old volume.
		projectName, sourceVolumeName := project.StorageVolumeParts(sourceVolume)
		_, err = pool.UnmountCustomVolume(projectName, sourceVolumeName, nil)
		if err != nil && !errors.Is(err, storageDrivers.ErrInUse) {
			return fmt.Errorf(`Failed to umount storage volume "%s/%s": %w`, sourcePool, sourceVolumeName, err)
		}

		return nil
	}

	// Move the data across.
	err = moveContent(sourcePath, destPath)
	if err != nil {
		return fmt.Errorf("Failed to move data over to directory %q: %w", destPath, err)
	}

	return nil
}
