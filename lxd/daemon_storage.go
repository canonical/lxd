package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/storage"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

func daemonStorageUnmount(s *state.State) {
	logger.Info("Stopping storage pools")

	var err error
	var storageBackups string
	var storageImages string
	var pools map[string]storage.Pool

	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second*5)

	// Do all database queries in time protected go routine.
	go func() {
		defer ctxCancel()

		err = s.Node.Transaction(func(tx *db.NodeTx) error {
			nodeConfig, err := node.ConfigLoad(tx)
			if err != nil {
				return err
			}

			storageBackups = nodeConfig.StorageBackupsVolume()
			storageImages = nodeConfig.StorageImagesVolume()

			return nil
		})
		if err != nil {
			logger.Error("Failed getting storage backup and image volume names", log.Ctx{"err": err})
			return
		}

		// Load pools.
		poolNames, err := s.Cluster.GetStoragePoolNames()
		if err != nil && err != db.ErrNoSuchObject {
			logger.Error("Failed getting storage pool names", log.Ctx{"err": err})
			return
		}

		pools = make(map[string]storage.Pool, len(poolNames))

		for _, poolName := range poolNames {
			pool, err := storagePools.GetPoolByName(s, poolName)
			if err != nil {
				logger.Error("Failed loading storage pool", log.Ctx{"pool": poolName, "err": err})
				continue
			}

			pools[poolName] = pool
		}
	}()

	<-ctx.Done()
	if ctx.Err() == context.DeadlineExceeded {
		err = ctx.Err()
	}

	if err != nil {
		logger.Error("Failed stopping storage pools", log.Ctx{"err": err})
		return
	}

	unmount := func(storageType string, source string) error {
		// Parse the source.
		fields := strings.Split(source, "/")
		if len(fields) != 2 {
			return fmt.Errorf("Invalid syntax for volume, must be <pool>/<volume>")
		}

		poolName := fields[0]
		volumeName := fields[1]

		pool := pools[poolName]
		if pool == nil {
			logger.Error("Pool not found when unmounting volume", log.Ctx{"pool": poolName, "volume": volumeName})
		}

		// Unmount volume.
		_, err := pool.UnmountCustomVolume(project.Default, volumeName, nil)
		if err != nil {
			return fmt.Errorf("Failed to unmount storage volume %q: %w", source, err)
		}

		return nil
	}

	if storageBackups != "" {
		err := unmount("backups", storageBackups)
		if err != nil {
			logger.Error("Failed unmounting storage backup volume", log.Ctx{"volume": storageBackups, "err": err})
		}
	}

	if storageImages != "" {
		err := unmount("images", storageImages)
		if err != nil {
			logger.Error("Failed unmounting storage image volume %q: %v", log.Ctx{"volume": storageImages, "err": err})
		}
	}

	for _, pool := range pools {
		if pool.Driver().Info().Name == "lvm" {
			continue // TODO figure out the intermittent issue with LVM tests when this is removed.
		}

		ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second*30)
		go func() {
			defer ctxCancel()
			_, err = pool.Unmount()
		}()

		<-ctx.Done()
		if ctx.Err() == context.DeadlineExceeded {
			err = ctx.Err()
		}

		if err != nil {
			logger.Error("Failed unmounting storage pool", log.Ctx{"pool": pool.Name(), "err": err})
		}
	}
}

func daemonStorageMount(s *state.State) error {
	var storageBackups string
	var storageImages string
	err := s.Node.Transaction(func(tx *db.NodeTx) error {
		nodeConfig, err := node.ConfigLoad(tx)
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

	mount := func(storageType string, source string) error {
		// Parse the source.
		fields := strings.Split(source, "/")
		if len(fields) != 2 {
			return fmt.Errorf("Invalid syntax for volume, must be <pool>/<volume>")
		}

		poolName := fields[0]
		volumeName := fields[1]

		pool, err := storagePools.GetPoolByName(s, poolName)
		if err != nil {
			return err
		}

		// Mount volume.
		err = pool.MountCustomVolume(project.Default, volumeName, nil)
		if err != nil {
			return errors.Wrapf(err, "Failed to mount storage volume %q", source)
		}

		return nil
	}

	if storageBackups != "" {
		err := mount("backups", storageBackups)
		if err != nil {
			return errors.Wrap(err, "Failed to mount backups storage")
		}
	}

	if storageImages != "" {
		err := mount("images", storageImages)
		if err != nil {
			return errors.Wrap(err, "Failed to mount images storage")
		}
	}

	return nil
}

func daemonStorageValidate(s *state.State, target string) error {
	// Check syntax.
	if target == "" {
		return nil
	}

	fields := strings.Split(target, "/")
	if len(fields) != 2 {
		return fmt.Errorf("Invalid syntax for volume, must be <pool>/<volume>")
	}

	poolName := fields[0]
	volumeName := fields[1]

	// Validate pool exists.
	poolID, _, _, err := s.Cluster.GetStoragePool(poolName)
	if err != nil {
		return errors.Wrapf(err, "Unable to load storage pool %q", poolName)
	}

	// Confirm volume exists.
	_, _, err = s.Cluster.GetLocalStoragePoolVolume(project.Default, volumeName, db.StoragePoolVolumeTypeCustom, poolID)
	if err != nil {
		return errors.Wrapf(err, "Unable to load storage volume %q", target)
	}

	snapshots, err := s.Cluster.GetLocalStoragePoolVolumeSnapshotsWithType(project.Default, volumeName, db.StoragePoolVolumeTypeCustom, poolID)
	if err != nil {
		return errors.Wrapf(err, "Unable to load storage volume snapshots %q", target)
	}

	if len(snapshots) != 0 {
		return fmt.Errorf("Storage volumes for use by LXD itself cannot have snapshots")
	}

	pool, err := storagePools.GetPoolByName(s, poolName)
	if err != nil {
		return err
	}

	// Mount volume.
	err = pool.MountCustomVolume(project.Default, volumeName, nil)
	if err != nil {
		return errors.Wrapf(err, "Failed to mount storage volume %q", target)
	}
	defer pool.UnmountCustomVolume(project.Default, volumeName, nil)

	// Validate volume is empty (ignore lost+found).
	volStorageName := project.StorageVolume(project.Default, volumeName)
	mountpoint := storageDrivers.GetVolumeMountPath(poolName, storageDrivers.VolumeTypeCustom, volStorageName)

	entries, err := ioutil.ReadDir(mountpoint)
	if err != nil {
		return errors.Wrapf(err, "Failed to list %q", mountpoint)
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

		return fmt.Errorf("Storage volume %q isn't empty", target)
	}

	return nil
}

func daemonStorageMove(s *state.State, storageType string, target string) error {
	destPath := shared.VarPath(storageType)

	// Track down the current storage.
	var sourcePool string
	var sourceVolume string

	sourcePath, err := os.Readlink(destPath)
	if err != nil {
		sourcePath = destPath
	} else {
		fields := strings.Split(sourcePath, "/")
		sourcePool = fields[len(fields)-3]
		sourceVolume = fields[len(fields)-1]
	}

	moveContent := func(source string, target string) error {
		// Copy the content.
		_, err := rsync.LocalCopy(source, target, "", false)
		if err != nil {
			return err
		}

		// Remove the source content.
		entries, err := ioutil.ReadDir(source)
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
	if target == "" {
		// Things already look correct.
		if sourcePath == destPath {
			return nil
		}

		// Remove the symlink.
		err = os.Remove(destPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to delete storage symlink at %q", destPath)
		}

		// Re-create as a directory.
		err = os.MkdirAll(destPath, 0700)
		if err != nil {
			return errors.Wrapf(err, "Failed to create directory %q", destPath)
		}

		// Move the data across.
		err = moveContent(sourcePath, destPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to move data over to directory %q", destPath)
		}

		pool, err := storagePools.GetPoolByName(s, sourcePool)
		if err != nil {
			return err
		}

		// Unmount old volume.
		projectName, sourceVolumeName := project.StorageVolumeParts(sourceVolume)
		_, err = pool.UnmountCustomVolume(projectName, sourceVolumeName, nil)
		if err != nil {
			return errors.Wrapf(err, `Failed to umount storage volume "%s/%s"`, sourcePool, sourceVolumeName)
		}

		return nil
	}

	// Parse the target.
	fields := strings.Split(target, "/")
	if len(fields) != 2 {
		return fmt.Errorf("Invalid syntax for volume, must be <pool>/<volume>")
	}

	poolName := fields[0]
	volumeName := fields[1]

	pool, err := storagePools.GetPoolByName(s, poolName)
	if err != nil {
		return err
	}

	// Mount volume.
	err = pool.MountCustomVolume(project.Default, volumeName, nil)
	if err != nil {
		return errors.Wrapf(err, "Failed to mount storage volume %q", target)
	}

	// Set ownership & mode.
	volStorageName := project.StorageVolume(project.Default, volumeName)
	mountpoint := storageDrivers.GetVolumeMountPath(poolName, storageDrivers.VolumeTypeCustom, volStorageName)
	destPath = mountpoint

	err = os.Chmod(mountpoint, 0700)
	if err != nil {
		return errors.Wrapf(err, "Failed to set permissions on %q", mountpoint)
	}

	err = os.Chown(mountpoint, 0, 0)
	if err != nil {
		return errors.Wrapf(err, "Failed to set ownership on %q", mountpoint)
	}

	// Handle changes.
	if sourcePath != shared.VarPath(storageType) {
		// Remove the symlink.
		err := os.Remove(shared.VarPath(storageType))
		if err != nil {
			return errors.Wrapf(err, "Failed to remove the new symlink at %q", shared.VarPath(storageType))
		}

		// Create the new symlink.
		err = os.Symlink(destPath, shared.VarPath(storageType))
		if err != nil {
			return errors.Wrapf(err, "Failed to create the new symlink at %q", shared.VarPath(storageType))
		}

		// Move the data across.
		err = moveContent(sourcePath, destPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to move data over to directory %q", destPath)
		}

		pool, err := storagePools.GetPoolByName(s, sourcePool)
		if err != nil {
			return err
		}

		// Unmount old volume.
		projectName, sourceVolumeName := project.StorageVolumeParts(sourceVolume)
		_, err = pool.UnmountCustomVolume(projectName, sourceVolumeName, nil)
		if err != nil {
			return errors.Wrapf(err, `Failed to umount storage volume "%s/%s"`, sourcePool, sourceVolumeName)
		}

		return nil
	}

	sourcePath = shared.VarPath(storageType) + ".temp"

	// Rename the existing storage.
	err = os.Rename(shared.VarPath(storageType), sourcePath)
	if err != nil {
		return errors.Wrapf(err, "Failed to rename existing storage %q", shared.VarPath(storageType))
	}

	// Create the new symlink.
	err = os.Symlink(destPath, shared.VarPath(storageType))
	if err != nil {
		return errors.Wrapf(err, "Failed to create the new symlink at %q", shared.VarPath(storageType))
	}

	// Move the data across.
	err = moveContent(sourcePath, destPath)
	if err != nil {
		return errors.Wrapf(err, "Failed to move data over to directory %q", destPath)
	}

	// Remove the old data.
	err = os.RemoveAll(sourcePath)
	if err != nil {
		return errors.Wrapf(err, "Failed to cleanup old directory %q", sourcePath)
	}

	return nil
}
