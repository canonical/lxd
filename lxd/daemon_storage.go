package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/rsync"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

func daemonStorageVolumesUnmount(s *state.State, ctx context.Context) error {
	var storageBackups string
	var storageImages string

	err := s.DB.Node.Transaction(ctx, func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		storageBackups = nodeConfig.StorageBackupsVolume()
		storageImages = nodeConfig.StorageImagesVolume()

		return nil
	})
	if err != nil {
		return err
	}

	unmount := func(source string) error {
		// Parse the source.
		poolName, volumeName, err := daemonStorageSplitVolume(source)
		if err != nil {
			return err
		}

		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return err
		}

		// Unmount volume.
		_, err = pool.UnmountCustomVolume(api.ProjectDefaultName, volumeName, nil)
		if err != nil {
			return fmt.Errorf("Failed to unmount storage volume %q: %w", source, err)
		}

		return nil
	}

	select {
	case <-ctx.Done():
		return errors.New("Timed out waiting for image and backup volume")
	default:
		if storageBackups != "" {
			err := unmount(storageBackups)
			if err != nil {
				return fmt.Errorf("Failed to unmount backups storage: %w", err)
			}
		}

		if storageImages != "" {
			err := unmount(storageImages)
			if err != nil {
				return fmt.Errorf("Failed to unmount images storage: %w", err)
			}
		}
	}

	return nil
}

func daemonStorageMount(s *state.State) error {
	var storageBackups string
	var storageImages string
	err := s.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		storageBackups = nodeConfig.StorageBackupsVolume()
		storageImages = nodeConfig.StorageImagesVolume()

		return nil
	})
	if err != nil {
		return err
	}

	mount := func(source string) error {
		// Parse the source.
		poolName, volumeName, err := daemonStorageSplitVolume(source)
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
			return fmt.Errorf("Failed to mount storage volume %q: %w", source, err)
		}

		return nil
	}

	if storageBackups != "" {
		err := mount(storageBackups)
		if err != nil {
			return fmt.Errorf("Failed to mount backups storage: %w", err)
		}
	}

	if storageImages != "" {
		err := mount(storageImages)
		if err != nil {
			return fmt.Errorf("Failed to mount images storage: %w", err)
		}
	}

	return nil
}

func daemonStorageSplitVolume(volume string) (poolName string, volumeName string, err error) {
	if strings.Count(volume, "/") != 1 {
		return "", "", errors.New("Invalid syntax for volume, must be <pool>/<volume>")
	}

	poolName, volumeName, _ = strings.Cut(volume, "/")

	// Validate pool name.
	if strings.Contains(poolName, "\\") || strings.Contains(poolName, "..") {
		return "", "", fmt.Errorf("Invalid pool name: %q", poolName)
	}

	// Validate volume name.
	if strings.Contains(volumeName, "\\") || strings.Contains(volumeName, "..") {
		return "", "", fmt.Errorf("Invalid volume name: %q", volumeName)
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

	for _, entry := range entries {
		entryName := entry.Name()

		// Don't fail on clean ext4 volumes.
		if entryName == "lost+found" {
			continue
		}

		// Don't fail on systems with snapdir=visible.
		if entryName == ".zfs" {
			continue
		}

		return "", fmt.Errorf("Storage volume %q isn't empty", target)
	}

	return pool.Name() + "/" + dbVol.Name, nil
}

func daemonStorageMove(s *state.State, storageType string, oldConfig string, newconfig string) error {
	destPath := shared.VarPath(storageType)
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
	}

	moveContent := func(source string, target string) error {
		// Copy the content.
		_, err := rsync.LocalCopy(source, target, "", false)
		if err != nil {
			return err
		}

		// Remove the source content.
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}

		for _, entry := range entries {
			err := os.RemoveAll(filepath.Join(source, entry.Name()))
			if err != nil {
				return err
			}
		}

		return nil
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

		// Unmount old volume.
		projectName, sourceVolumeName := project.StorageVolumeParts(sourceVolume)
		_, err = pool.UnmountCustomVolume(projectName, sourceVolumeName, nil)
		if err != nil {
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

	// Set ownership & mode.
	switch storageType {
	case "images":
		destPath = s.ImagesStoragePath()
	case "backups":
		destPath = s.BackupsStoragePath()
	}

	err = os.Chmod(destPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to set permissions on %q: %w", destPath, err)
	}

	err = os.Chown(destPath, 0, 0)
	if err != nil {
		return fmt.Errorf("Failed to set ownership on %q: %w", destPath, err)
	}

	// Handle changes.
	if sourcePath != shared.VarPath(storageType) {
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
		if err != nil {
			return fmt.Errorf(`Failed to umount storage volume "%s/%s": %w`, sourcePool, sourceVolumeName, err)
		}

		return nil
	}

	// Move the data across.
	err = moveContent(sourcePath, destPath)
	if err != nil {
		return fmt.Errorf("Failed to move data over to directory %q: %w", destPath, err)
	}

	// Remove the old data.
	err = os.RemoveAll(sourcePath)
	if err != nil {
		return fmt.Errorf("Failed to cleanup old directory %q: %w", sourcePath, err)
	}

	return nil
}
