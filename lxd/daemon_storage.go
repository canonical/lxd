package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
)

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
		// Parse the source
		fields := strings.Split(source, "/")
		if len(fields) != 2 {
			return fmt.Errorf("Invalid syntax for volume, must be <pool>/<volume>")
		}

		poolName := fields[0]
		volumeName := fields[1]

		// Mount volume
		volume, err := storageInit(s, "default", poolName, volumeName, storagePoolVolumeTypeCustom)
		if err != nil {
			return errors.Wrapf(err, "Unable to load storage volume \"%s\"", source)
		}

		_, err = volume.StoragePoolVolumeMount()
		if err != nil {
			return errors.Wrapf(err, "Failed to mount storage volume \"%s\"", source)
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

func daemonStorageUsed(s *state.State, poolName string, volumeName string) (bool, error) {
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
		return false, err
	}

	fullName := fmt.Sprintf("%s/%s", poolName, volumeName)
	if storageBackups == fullName || storageImages == fullName {
		return true, nil
	}

	return false, nil
}

func daemonStorageValidate(s *state.State, target string) error {
	// Check syntax
	if target == "" {
		return nil
	}

	fields := strings.Split(target, "/")
	if len(fields) != 2 {
		return fmt.Errorf("Invalid syntax for volume, must be <pool>/<volume>")
	}

	poolName := fields[0]
	volumeName := fields[1]

	// Validate pool exists
	poolID, pool, err := s.Cluster.StoragePoolGet(poolName)
	if err != nil {
		return errors.Wrapf(err, "Unable to load storage pool \"%s\"", poolName)
	}

	// Validate pool driver (can't be CEPH or CEPHFS)
	if pool.Driver == "ceph" || pool.Driver == "cephfs" {
		return fmt.Errorf("Server storage volumes cannot be stored on Ceph")
	}

	// Confirm volume exists
	volume, err := storageInit(s, "default", poolName, volumeName, storagePoolVolumeTypeCustom)
	if err != nil {
		return errors.Wrapf(err, "Unable to load storage volume \"%s\"", target)
	}

	snapshots, err := s.Cluster.StoragePoolVolumeSnapshotsGetType(volumeName, storagePoolVolumeTypeCustom, poolID)
	if err != nil {
		return errors.Wrapf(err, "Unable to load storage volume snapshots \"%s\"", target)
	}

	if len(snapshots) != 0 {
		return fmt.Errorf("Storage volumes for use by LXD itself cannot have snapshots")
	}

	// Mount volume
	ourMount, err := volume.StoragePoolVolumeMount()
	if err != nil {
		return errors.Wrapf(err, "Failed to mount storage volume \"%s\"", target)
	}
	if ourMount {
		defer volume.StoragePoolUmount()
	}

	// Validate volume is empty (ignore lost+found)
	mountpoint := shared.VarPath("storage-pools", poolName, "custom", volumeName)

	entries, err := ioutil.ReadDir(mountpoint)
	if err != nil {
		return errors.Wrapf(err, "Failed to list \"%s\"", mountpoint)
	}

	for _, entry := range entries {
		entryName := entry.Name()

		if entryName == "lost+found" {
			continue
		}

		return fmt.Errorf("Storage volume \"%s\" isn't empty", target)
	}

	return nil
}

func daemonStorageMove(s *state.State, storageType string, target string) error {
	destPath := shared.VarPath(storageType)

	// Track down the current storage
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
		// Copy the content
		_, err := rsyncLocalCopy(source, target, "", false)
		if err != nil {
			return err
		}

		// Remove the source content
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

	// Deal with unsetting
	if target == "" {
		// Things already look correct
		if sourcePath == destPath {
			return nil
		}

		// Remove the symlink
		err = os.Remove(destPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to delete storage symlink at \"%s\"", destPath)
		}

		// Re-create as a directory
		err = os.MkdirAll(destPath, 0700)
		if err != nil {
			return errors.Wrapf(err, "Failed to create directory \"%s\"", destPath)
		}

		// Move the data across
		err = moveContent(sourcePath, destPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to move data over to directory \"%s\"", destPath)
		}

		// Unmount old volume
		volume, err := storageInit(s, "default", sourcePool, sourceVolume, storagePoolVolumeTypeCustom)
		if err != nil {
			return errors.Wrapf(err, "Unable to load storage volume \"%s/%s\"", sourcePool, sourceVolume)
		}

		_, err = volume.StoragePoolVolumeUmount()
		if err != nil {
			return errors.Wrapf(err, "Failed to umount storage volume \"%s/%s\"", sourcePool, sourceVolume)
		}

		return nil
	}

	// Parse the target
	fields := strings.Split(target, "/")
	if len(fields) != 2 {
		return fmt.Errorf("Invalid syntax for volume, must be <pool>/<volume>")
	}

	poolName := fields[0]
	volumeName := fields[1]

	// Mount volume
	volume, err := storageInit(s, "default", poolName, volumeName, storagePoolVolumeTypeCustom)
	if err != nil {
		return errors.Wrapf(err, "Unable to load storage volume \"%s\"", target)
	}

	_, err = volume.StoragePoolVolumeMount()
	if err != nil {
		return errors.Wrapf(err, "Failed to mount storage volume \"%s\"", target)
	}

	// Set ownership & mode
	mountpoint := shared.VarPath("storage-pools", poolName, "custom", volumeName)
	destPath = mountpoint

	err = os.Chmod(mountpoint, 0700)
	if err != nil {
		return errors.Wrapf(err, "Failed to set permissions on \"%s\"", mountpoint)
	}

	err = os.Chown(mountpoint, 0, 0)
	if err != nil {
		return errors.Wrapf(err, "Failed to set ownership on \"%s\"", mountpoint)
	}

	// Handle changes
	if sourcePath != shared.VarPath(storageType) {
		// Remove the symlink
		err := os.Remove(shared.VarPath(storageType))
		if err != nil {
			return errors.Wrapf(err, "Failed to remove the new symlink at \"%s\"", shared.VarPath(storageType))
		}

		// Create the new symlink
		err = os.Symlink(destPath, shared.VarPath(storageType))
		if err != nil {
			return errors.Wrapf(err, "Failed to create the new symlink at \"%s\"", shared.VarPath(storageType))
		}

		// Move the data across
		err = moveContent(sourcePath, destPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to move data over to directory \"%s\"", destPath)
		}

		// Unmount old volume
		volume, err := storageInit(s, "default", sourcePool, sourceVolume, storagePoolVolumeTypeCustom)
		if err != nil {
			return errors.Wrapf(err, "Unable to load storage volume \"%s/%s\"", sourcePool, sourceVolume)
		}

		_, err = volume.StoragePoolVolumeUmount()
		if err != nil {
			return errors.Wrapf(err, "Failed to umount storage volume \"%s/%s\"", sourcePool, sourceVolume)
		}

		return nil
	}

	sourcePath = shared.VarPath(storageType) + ".temp"

	// Rename the existing storage
	err = os.Rename(shared.VarPath(storageType), sourcePath)
	if err != nil {
		return errors.Wrapf(err, "Failed to rename existing storage \"%s\"", shared.VarPath(storageType))
	}

	// Create the new symlink
	err = os.Symlink(destPath, shared.VarPath(storageType))
	if err != nil {
		return errors.Wrapf(err, "Failed to create the new symlink at \"%s\"", shared.VarPath(storageType))
	}

	// Move the data across
	err = moveContent(sourcePath, destPath)
	if err != nil {
		return errors.Wrapf(err, "Failed to move data over to directory \"%s\"", destPath)
	}

	// Remove the old data
	err = os.RemoveAll(sourcePath)
	if err != nil {
		return errors.Wrapf(err, "Failed to cleanup old directory \"%s\"", sourcePath)
	}

	return nil
}
