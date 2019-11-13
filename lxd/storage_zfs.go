package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/rsync"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/units"

	"github.com/pborman/uuid"
)

// Global defaults
var zfsUseRefquota = "false"
var zfsRemoveSnapshots = "false"

// Cache
var zfsVersion = ""

type storageZfs struct {
	dataset string
	storageShared
}

func (s *storageZfs) getOnDiskPoolName() string {
	if s.dataset != "" {
		return s.dataset
	}

	return s.pool.Name
}

// Only initialize the minimal information we need about a given storage type.
func (s *storageZfs) StorageCoreInit() error {
	s.sType = storageTypeZfs
	typeName, err := storageTypeToString(s.sType)
	if err != nil {
		return err
	}
	s.sTypeName = typeName

	if zfsVersion != "" {
		s.sTypeVersion = zfsVersion
		return nil
	}

	util.LoadModule("zfs")

	if !zfsIsEnabled() {
		return fmt.Errorf("The \"zfs\" tool is not enabled")
	}

	s.sTypeVersion, err = zfsToolVersionGet()
	if err != nil {
		s.sTypeVersion, err = zfsModuleVersionGet()
		if err != nil {
			return err
		}
	}

	zfsVersion = s.sTypeVersion

	return nil
}

// Functions dealing with storage pools.
func (s *storageZfs) StoragePoolInit() error {
	err := s.StorageCoreInit()
	if err != nil {
		return err
	}

	// Detect whether we have been given a zfs dataset as source.
	if s.pool.Config["zfs.pool_name"] != "" {
		s.dataset = s.pool.Config["zfs.pool_name"]
	}

	return nil
}

func (s *storageZfs) StoragePoolCheck() error {
	logger.Debugf("Checking ZFS storage pool \"%s\"", s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	poolName := s.getOnDiskPoolName()
	purePoolName := strings.Split(poolName, "/")[0]
	exists := zfsFilesystemEntityExists(purePoolName, "")
	if exists {
		return nil
	}

	logger.Debugf("ZFS storage pool \"%s\" does not exist, trying to import it", poolName)

	var err error
	var msg string
	if filepath.IsAbs(source) {
		disksPath := shared.VarPath("disks")
		msg, err = shared.RunCommand("zpool", "import", "-d", disksPath, poolName)
	} else {
		msg, err = shared.RunCommand("zpool", "import", purePoolName)
	}

	if err != nil {
		return fmt.Errorf("ZFS storage pool \"%s\" could not be imported: %s", poolName, msg)
	}

	logger.Debugf("ZFS storage pool \"%s\" successfully imported", poolName)
	return nil
}

func (s *storageZfs) StoragePoolCreate() error {
	logger.Infof("Creating ZFS storage pool \"%s\"", s.pool.Name)

	err := s.zfsPoolCreate()
	if err != nil {
		return err
	}
	revert := true
	defer func() {
		if !revert {
			return
		}
		s.StoragePoolDelete()
	}()

	storagePoolMntPoint := driver.GetStoragePoolMountPoint(s.pool.Name)
	err = os.MkdirAll(storagePoolMntPoint, 0711)
	if err != nil {
		return err
	}

	err = s.StoragePoolCheck()
	if err != nil {
		return err
	}

	revert = false

	logger.Infof("Created ZFS storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageZfs) zfsPoolCreate() error {
	s.pool.Config["volatile.initial_source"] = s.pool.Config["source"]

	zpoolName := s.getOnDiskPoolName()
	vdev := s.pool.Config["source"]
	defaultVdev := filepath.Join(shared.VarPath("disks"), fmt.Sprintf("%s.img", s.pool.Name))
	if vdev == "" || vdev == defaultVdev {
		vdev = defaultVdev
		s.pool.Config["source"] = vdev

		if s.pool.Config["zfs.pool_name"] == "" {
			s.pool.Config["zfs.pool_name"] = zpoolName
		}

		f, err := os.Create(vdev)
		if err != nil {
			return fmt.Errorf("Failed to open %s: %s", vdev, err)
		}
		defer f.Close()

		err = f.Chmod(0600)
		if err != nil {
			return fmt.Errorf("Failed to chmod %s: %s", vdev, err)
		}

		size, err := units.ParseByteSizeString(s.pool.Config["size"])
		if err != nil {
			return err
		}
		err = f.Truncate(size)
		if err != nil {
			return fmt.Errorf("Failed to create sparse file %s: %s", vdev, err)
		}

		err = zfsPoolCreate(zpoolName, vdev)
		if err != nil {
			return err
		}
	} else {
		// Unset size property since it doesn't make sense.
		s.pool.Config["size"] = ""

		if filepath.IsAbs(vdev) {
			if !shared.IsBlockdevPath(vdev) {
				return fmt.Errorf("Custom loop file locations are not supported")
			}

			if s.pool.Config["zfs.pool_name"] == "" {
				s.pool.Config["zfs.pool_name"] = zpoolName
			}

			// This is a block device. Note, that we do not store the
			// block device path or UUID or PARTUUID or similar in
			// the database. All of those might change or might be
			// used in a special way (For example, zfs uses a single
			// UUID in a multi-device pool for all devices.). The
			// safest way is to just store the name of the zfs pool
			// we create.
			s.pool.Config["source"] = zpoolName
			err := zfsPoolCreate(zpoolName, vdev)
			if err != nil {
				return err
			}
		} else {
			if s.pool.Config["zfs.pool_name"] != "" && s.pool.Config["zfs.pool_name"] != vdev {
				return fmt.Errorf("Invalid combination of \"source\" and \"zfs.pool_name\" property")
			}

			s.pool.Config["zfs.pool_name"] = vdev
			s.dataset = vdev

			if strings.Contains(vdev, "/") {
				if !zfsFilesystemEntityExists(vdev, "") {
					err := zfsPoolCreate("", vdev)
					if err != nil {
						return err
					}
				}
			} else {
				err := zfsPoolCheck(vdev)
				if err != nil {
					return err
				}
			}

			subvols, err := zfsPoolListSubvolumes(zpoolName, vdev)
			if err != nil {
				return err
			}

			if len(subvols) > 0 {
				return fmt.Errorf("Provided ZFS pool (or dataset) isn't empty")
			}

			err = zfsPoolApplyDefaults(vdev)
			if err != nil {
				return err
			}
		}
	}

	// Create default dummy datasets to avoid zfs races during container
	// creation.
	poolName := s.getOnDiskPoolName()
	dataset := fmt.Sprintf("%s/containers", poolName)
	msg, err := zfsPoolVolumeCreate(dataset, "mountpoint=none")
	if err != nil {
		logger.Errorf("Failed to create containers dataset: %s", msg)
		return err
	}

	fixperms := shared.VarPath("storage-pools", s.pool.Name, "containers")
	err = os.MkdirAll(fixperms, driver.ContainersDirMode)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = os.Chmod(fixperms, driver.ContainersDirMode)
	if err != nil {
		logger.Warnf("Failed to chmod \"%s\" to \"0%s\": %s", fixperms, strconv.FormatInt(int64(driver.ContainersDirMode), 8), err)
	}

	dataset = fmt.Sprintf("%s/images", poolName)
	msg, err = zfsPoolVolumeCreate(dataset, "mountpoint=none")
	if err != nil {
		logger.Errorf("Failed to create images dataset: %s", msg)
		return err
	}

	fixperms = shared.VarPath("storage-pools", s.pool.Name, "images")
	err = os.MkdirAll(fixperms, driver.ImagesDirMode)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	err = os.Chmod(fixperms, driver.ImagesDirMode)
	if err != nil {
		logger.Warnf("Failed to chmod \"%s\" to \"0%s\": %s", fixperms, strconv.FormatInt(int64(driver.ImagesDirMode), 8), err)
	}

	dataset = fmt.Sprintf("%s/custom", poolName)
	msg, err = zfsPoolVolumeCreate(dataset, "mountpoint=none")
	if err != nil {
		logger.Errorf("Failed to create custom dataset: %s", msg)
		return err
	}

	fixperms = shared.VarPath("storage-pools", s.pool.Name, "custom")
	err = os.MkdirAll(fixperms, driver.CustomDirMode)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	err = os.Chmod(fixperms, driver.CustomDirMode)
	if err != nil {
		logger.Warnf("Failed to chmod \"%s\" to \"0%s\": %s", fixperms, strconv.FormatInt(int64(driver.CustomDirMode), 8), err)
	}

	dataset = fmt.Sprintf("%s/deleted", poolName)
	msg, err = zfsPoolVolumeCreate(dataset, "mountpoint=none")
	if err != nil {
		logger.Errorf("Failed to create deleted dataset: %s", msg)
		return err
	}

	dataset = fmt.Sprintf("%s/snapshots", poolName)
	msg, err = zfsPoolVolumeCreate(dataset, "mountpoint=none")
	if err != nil {
		logger.Errorf("Failed to create snapshots dataset: %s", msg)
		return err
	}

	fixperms = shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots")
	err = os.MkdirAll(fixperms, driver.SnapshotsDirMode)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	err = os.Chmod(fixperms, driver.SnapshotsDirMode)
	if err != nil {
		logger.Warnf("Failed to chmod \"%s\" to \"0%s\": %s", fixperms, strconv.FormatInt(int64(driver.SnapshotsDirMode), 8), err)
	}

	dataset = fmt.Sprintf("%s/custom-snapshots", poolName)
	msg, err = zfsPoolVolumeCreate(dataset, "mountpoint=none")
	if err != nil {
		logger.Errorf("Failed to create snapshots dataset: %s", msg)
		return err
	}

	fixperms = shared.VarPath("storage-pools", s.pool.Name, "custom-snapshots")
	err = os.MkdirAll(fixperms, driver.SnapshotsDirMode)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	err = os.Chmod(fixperms, driver.SnapshotsDirMode)
	if err != nil {
		logger.Warnf("Failed to chmod \"%s\" to \"0%s\": %s", fixperms, strconv.FormatInt(int64(driver.SnapshotsDirMode), 8), err)
	}

	return nil
}

func (s *storageZfs) StoragePoolDelete() error {
	logger.Infof("Deleting ZFS storage pool \"%s\"", s.pool.Name)

	poolName := s.getOnDiskPoolName()
	if zfsFilesystemEntityExists(poolName, "") {
		err := zfsFilesystemEntityDelete(s.pool.Config["source"], poolName)
		if err != nil {
			return err
		}
	}

	storagePoolMntPoint := driver.GetStoragePoolMountPoint(s.pool.Name)
	if shared.PathExists(storagePoolMntPoint) {
		err := os.RemoveAll(storagePoolMntPoint)
		if err != nil {
			return err
		}
	}

	logger.Infof("Deleted ZFS storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageZfs) StoragePoolMount() (bool, error) {
	return true, nil
}

func (s *storageZfs) StoragePoolUmount() (bool, error) {
	return true, nil
}

func (s *storageZfs) StoragePoolVolumeCreate() error {
	logger.Infof("Creating ZFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	isSnapshot := shared.IsSnapshot(s.volume.Name)

	var fs string

	if isSnapshot {
		fs = fmt.Sprintf("custom-snapshots/%s", s.volume.Name)
	} else {
		fs = fmt.Sprintf("custom/%s", s.volume.Name)
	}
	poolName := s.getOnDiskPoolName()
	dataset := fmt.Sprintf("%s/%s", poolName, fs)

	var customPoolVolumeMntPoint string

	if isSnapshot {
		customPoolVolumeMntPoint = driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	} else {
		customPoolVolumeMntPoint = driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	}

	msg, err := zfsPoolVolumeCreate(dataset, "mountpoint=none", "canmount=noauto")
	if err != nil {
		logger.Errorf("Failed to create ZFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, msg)
		return err
	}
	revert := true
	defer func() {
		if !revert {
			return
		}
		s.StoragePoolVolumeDelete()
	}()

	err = zfsPoolVolumeSet(poolName, fs, "mountpoint", customPoolVolumeMntPoint)
	if err != nil {
		return err
	}

	if !shared.IsMountPoint(customPoolVolumeMntPoint) {
		err := zfsMount(poolName, fs)
		if err != nil {
			return err
		}
		defer zfsUmount(poolName, fs, customPoolVolumeMntPoint)
	}

	// apply quota
	if s.volume.Config["size"] != "" {
		size, err := units.ParseByteSizeString(s.volume.Config["size"])
		if err != nil {
			return err
		}

		err = s.StorageEntitySetQuota(storagePoolVolumeTypeCustom, size, nil)
		if err != nil {
			return err
		}
	}

	revert = false

	logger.Infof("Created ZFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) StoragePoolVolumeDelete() error {
	logger.Infof("Deleting ZFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	fs := fmt.Sprintf("custom/%s", s.volume.Name)
	customPoolVolumeMntPoint := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	poolName := s.getOnDiskPoolName()
	if zfsFilesystemEntityExists(poolName, fs) {
		removable := true
		snaps, err := zfsPoolListSnapshots(poolName, fs)
		if err != nil {
			return err
		}

		for _, snap := range snaps {
			var err error
			removable, err = zfsPoolVolumeSnapshotRemovable(poolName, fs, snap)
			if err != nil {
				return err
			}

			if !removable {
				break
			}
		}

		if removable {
			origin, err := zfsFilesystemEntityPropertyGet(poolName, fs, "origin")
			if err != nil {
				return err
			}
			poolName := s.getOnDiskPoolName()
			origin = strings.TrimPrefix(origin, fmt.Sprintf("%s/", poolName))

			err = zfsPoolVolumeDestroy(poolName, fs)
			if err != nil {
				return err
			}

			err = zfsPoolVolumeCleanup(poolName, origin)
			if err != nil {
				return err
			}
		} else {
			err := zfsPoolVolumeSet(poolName, fs, "mountpoint", "none")
			if err != nil {
				return err
			}

			err = zfsPoolVolumeRename(poolName, fs, fmt.Sprintf("deleted/custom/%s", uuid.NewRandom().String()), true)
			if err != nil {
				return err
			}
		}
	}

	if shared.PathExists(customPoolVolumeMntPoint) {
		err := os.RemoveAll(customPoolVolumeMntPoint)
		if err != nil {
			return err
		}
	}

	err := s.s.Cluster.StoragePoolVolumeDelete(
		"default",
		s.volume.Name,
		storagePoolVolumeTypeCustom,
		s.poolID)
	if err != nil {
		logger.Errorf(`Failed to delete database entry for ZFS storage volume "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)
	}

	logger.Infof("Deleted ZFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) StoragePoolVolumeMount() (bool, error) {
	logger.Debugf("Mounting ZFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	fs := fmt.Sprintf("custom/%s", s.volume.Name)
	customPoolVolumeMntPoint := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	customMountLockID := getCustomMountLockID(s.pool.Name, s.volume.Name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[customMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore, this should not have happened")
		}
		// Give the benefit of the doubt and assume that the other
		// thread actually succeeded in mounting the storage volume.
		return false, nil
	}

	lxdStorageOngoingOperationMap[customMountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	var customerr error
	ourMount := false
	if !shared.IsMountPoint(customPoolVolumeMntPoint) {
		customerr = zfsMount(s.getOnDiskPoolName(), fs)
		ourMount = true
	}

	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[customMountLockID]; ok {
		close(waitChannel)
		delete(lxdStorageOngoingOperationMap, customMountLockID)
	}
	lxdStorageMapLock.Unlock()

	if customerr != nil {
		return false, customerr
	}

	logger.Debugf("Mounted ZFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageZfs) StoragePoolVolumeUmount() (bool, error) {
	logger.Debugf("Unmounting ZFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	fs := fmt.Sprintf("custom/%s", s.volume.Name)
	customPoolVolumeMntPoint := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	customUmountLockID := getCustomUmountLockID(s.pool.Name, s.volume.Name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[customUmountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore, this should not have happened")
		}
		// Give the benefit of the doubt and assume that the other
		// thread actually succeeded in unmounting the storage volume.
		return false, nil
	}

	lxdStorageOngoingOperationMap[customUmountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	var customerr error
	ourUmount := false
	if shared.IsMountPoint(customPoolVolumeMntPoint) {
		customerr = zfsUmount(s.getOnDiskPoolName(), fs, customPoolVolumeMntPoint)
		ourUmount = true
	}

	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[customUmountLockID]; ok {
		close(waitChannel)
		delete(lxdStorageOngoingOperationMap, customUmountLockID)
	}
	lxdStorageMapLock.Unlock()

	if customerr != nil {
		return false, customerr
	}

	logger.Debugf("Unmounted ZFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return ourUmount, nil
}

func (s *storageZfs) GetContainerPoolInfo() (int64, string, string) {
	return s.poolID, s.pool.Name, s.getOnDiskPoolName()
}

func (s *storageZfs) StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error {
	logger.Infof(`Updating ZFS storage pool "%s"`, s.pool.Name)

	changeable := changeableStoragePoolProperties["zfs"]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		return updateStoragePoolError(unchangeable, "zfs")
	}

	// "rsync.bwlimit" requires no on-disk modifications.
	// "volume.zfs.remove_snapshots" requires no on-disk modifications.
	// "volume.zfs.use_refquota" requires no on-disk modifications.

	logger.Infof(`Updated ZFS storage pool "%s"`, s.pool.Name)
	return nil
}

func (s *storageZfs) StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error {
	if writable.Restore != "" {
		logger.Infof(`Restoring ZFS storage volume "%s" from snapshot "%s"`,
			s.volume.Name, writable.Restore)

		// Check that we can remove the snapshot
		poolID, err := s.s.Cluster.StoragePoolGetID(s.pool.Name)
		if err != nil {
			return err
		}

		// Get the names of all storage volume snapshots of a given volume
		volumes, err := s.s.Cluster.StoragePoolVolumeSnapshotsGetType(s.volume.Name, storagePoolVolumeTypeCustom, poolID)
		if err != nil {
			return err
		}

		if volumes[len(volumes)-1].Name != fmt.Sprintf("%s/%s", s.volume.Name, writable.Restore) {
			return fmt.Errorf("ZFS can only restore from the latest snapshot. Delete newer snapshots or copy the snapshot into a new volume instead")
		}

		s.volume.Description = writable.Description
		s.volume.Config = writable.Config

		targetSnapshotDataset := fmt.Sprintf("%s/custom/%s@snapshot-%s", s.getOnDiskPoolName(), s.volume.Name, writable.Restore)
		msg, err := shared.RunCommand("zfs", "rollback", "-r", "-R", targetSnapshotDataset)
		if err != nil {
			logger.Errorf("Failed to rollback ZFS dataset: %s", msg)
			return err
		}

		logger.Infof(`Restored ZFS storage volume "%s" from snapshot "%s"`,
			s.volume.Name, writable.Restore)
		return nil
	}

	logger.Infof(`Updating ZFS storage volume "%s"`, s.volume.Name)

	changeable := changeableStoragePoolVolumeProperties["zfs"]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		return updateStoragePoolVolumeError(unchangeable, "zfs")
	}

	if shared.StringInSlice("size", changedConfig) {
		if s.volume.Type != storagePoolVolumeTypeNameCustom {
			return updateStoragePoolVolumeError([]string{"size"}, "zfs")
		}

		if s.volume.Config["size"] != writable.Config["size"] {
			size, err := units.ParseByteSizeString(writable.Config["size"])
			if err != nil {
				return err
			}

			err = s.StorageEntitySetQuota(storagePoolVolumeTypeCustom, size, nil)
			if err != nil {
				return err
			}
		}
	}

	logger.Infof(`Updated ZFS storage volume "%s"`, s.volume.Name)
	return nil
}

func (s *storageZfs) StoragePoolVolumeRename(newName string) error {
	logger.Infof(`Renaming ZFS storage volume on storage pool "%s" from "%s" to "%s`,
		s.pool.Name, s.volume.Name, newName)

	usedBy, err := storagePoolVolumeUsedByContainersGet(s.s, "default", s.pool.Name, s.volume.Name)
	if err != nil {
		return err
	}
	if len(usedBy) > 0 {
		return fmt.Errorf(`ZFS storage volume "%s" on storage pool "%s" is attached to containers`,
			s.volume.Name, s.pool.Name)
	}

	isSnapshot := shared.IsSnapshot(s.volume.Name)

	var oldPath string
	var newPath string

	if isSnapshot {
		oldPath = fmt.Sprintf("custom-snapshots/%s", s.volume.Name)
		newPath = fmt.Sprintf("custom-snapshots/%s", newName)
	} else {
		oldPath = fmt.Sprintf("custom/%s", s.volume.Name)
		newPath = fmt.Sprintf("custom/%s", newName)
	}
	poolName := s.getOnDiskPoolName()
	err = zfsPoolVolumeRename(poolName, oldPath, newPath, false)
	if err != nil {
		return err
	}

	logger.Infof(`Renamed ZFS storage volume on storage pool "%s" from "%s" to "%s`,
		s.pool.Name, s.volume.Name, newName)

	return s.s.Cluster.StoragePoolVolumeRename("default", s.volume.Name, newName,
		storagePoolVolumeTypeCustom, s.poolID)
}

// Things we don't need to care about
func (s *storageZfs) ContainerMount(c Instance) (bool, error) {
	return s.doContainerMount(c.Project(), c.Name(), c.IsPrivileged())
}

func (s *storageZfs) ContainerUmount(c Instance, path string) (bool, error) {
	logger.Debugf("Unmounting ZFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	name := c.Name()

	fs := fmt.Sprintf("containers/%s", project.Prefix(c.Project(), name))
	containerPoolVolumeMntPoint := driver.GetContainerMountPoint(c.Project(), s.pool.Name, name)

	containerUmountLockID := getContainerUmountLockID(s.pool.Name, name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerUmountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore, this should not have happened")
		}
		// Give the benefit of the doubt and assume that the other
		// thread actually succeeded in unmounting the storage volume.
		return false, nil
	}

	lxdStorageOngoingOperationMap[containerUmountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	var imgerr error
	ourUmount := false
	if shared.IsMountPoint(containerPoolVolumeMntPoint) {
		imgerr = zfsUmount(s.getOnDiskPoolName(), fs, containerPoolVolumeMntPoint)
		ourUmount = true
	}

	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerUmountLockID]; ok {
		close(waitChannel)
		delete(lxdStorageOngoingOperationMap, containerUmountLockID)
	}
	lxdStorageMapLock.Unlock()

	if imgerr != nil {
		return false, imgerr
	}

	logger.Debugf("Unmounted ZFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return ourUmount, nil
}

// Things we do have to care about
func (s *storageZfs) ContainerStorageReady(container Instance) bool {
	volumeName := project.Prefix(container.Project(), container.Name())
	fs := fmt.Sprintf("containers/%s", volumeName)
	return zfsFilesystemEntityExists(s.getOnDiskPoolName(), fs)
}

func (s *storageZfs) ContainerCreate(container Instance) error {
	err := s.doContainerCreate(container.Project(), container.Name(), container.IsPrivileged())
	if err != nil {
		s.doContainerDelete(container.Project(), container.Name())
		return err
	}

	ourMount, err := s.ContainerMount(container)
	if err != nil {
		return err
	}
	if ourMount {
		defer s.ContainerUmount(container, container.Path())
	}

	err = container.DeferTemplateApply("create")
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) ContainerCreateFromImage(container Instance, fingerprint string, tracker *ioprogress.ProgressTracker) error {
	logger.Debugf("Creating ZFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	containerPath := container.Path()
	containerName := container.Name()
	volumeName := project.Prefix(container.Project(), containerName)
	fs := fmt.Sprintf("containers/%s", volumeName)
	containerPoolVolumeMntPoint := driver.GetContainerMountPoint(container.Project(), s.pool.Name, containerName)

	poolName := s.getOnDiskPoolName()
	fsImage := fmt.Sprintf("images/%s", fingerprint)

	imageStoragePoolLockID := getImageCreateLockID(s.pool.Name, fingerprint)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[imageStoragePoolLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore, this should not have happened")
		}
	} else {
		lxdStorageOngoingOperationMap[imageStoragePoolLockID] = make(chan bool)
		lxdStorageMapLock.Unlock()

		var imgerr error
		if !zfsFilesystemEntityExists(poolName, fmt.Sprintf("%s@readonly", fsImage)) {
			imgerr = s.ImageCreate(fingerprint, tracker)
		}

		lxdStorageMapLock.Lock()
		if waitChannel, ok := lxdStorageOngoingOperationMap[imageStoragePoolLockID]; ok {
			close(waitChannel)
			delete(lxdStorageOngoingOperationMap, imageStoragePoolLockID)
		}
		lxdStorageMapLock.Unlock()

		if imgerr != nil {
			return imgerr
		}
	}

	err := zfsPoolVolumeClone(container.Project(), poolName, fsImage, "readonly", fs, containerPoolVolumeMntPoint)
	if err != nil {
		return err
	}

	revert := true
	defer func() {
		if !revert {
			return
		}
		s.ContainerDelete(container)
	}()

	ourMount, err := s.ContainerMount(container)
	if err != nil {
		return err
	}
	if ourMount {
		defer s.ContainerUmount(container, containerPath)
	}

	privileged := container.IsPrivileged()
	err = driver.CreateContainerMountpoint(containerPoolVolumeMntPoint, containerPath, privileged)
	if err != nil {
		return err
	}

	err = container.DeferTemplateApply("create")
	if err != nil {
		return err
	}

	revert = false

	logger.Debugf("Created ZFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) ContainerDelete(container Instance) error {
	err := s.doContainerDelete(container.Project(), container.Name())
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) copyWithoutSnapshotsSparse(target Instance, source Instance) error {
	poolName := s.getOnDiskPoolName()

	sourceContainerName := source.Name()
	sourceContainerPath := source.Path()

	targetContainerName := target.Name()
	targetContainerPath := target.Path()
	targetContainerMountPoint := driver.GetContainerMountPoint(target.Project(), s.pool.Name, targetContainerName)

	sourceZfsDataset := ""
	sourceZfsDatasetSnapshot := ""
	sourceName, sourceSnapOnlyName, isSnapshotName := shared.ContainerGetParentAndSnapshotName(sourceContainerName)

	targetZfsDataset := fmt.Sprintf("containers/%s", project.Prefix(target.Project(), targetContainerName))

	if isSnapshotName {
		sourceZfsDatasetSnapshot = sourceSnapOnlyName
	}

	revert := true
	if sourceZfsDatasetSnapshot == "" {
		if zfsFilesystemEntityExists(poolName, fmt.Sprintf("containers/%s", project.Prefix(source.Project(), sourceName))) {
			sourceZfsDatasetSnapshot = fmt.Sprintf("copy-%s", uuid.NewRandom().String())
			sourceZfsDataset = fmt.Sprintf("containers/%s", project.Prefix(source.Project(), sourceName))
			err := zfsPoolVolumeSnapshotCreate(poolName, sourceZfsDataset, sourceZfsDatasetSnapshot)
			if err != nil {
				return err
			}
			defer func() {
				if !revert {
					return
				}
				zfsPoolVolumeSnapshotDestroy(poolName, sourceZfsDataset, sourceZfsDatasetSnapshot)
			}()
		}
	} else {
		if zfsFilesystemEntityExists(poolName, fmt.Sprintf("containers/%s@snapshot-%s", project.Prefix(source.Project(), sourceName), sourceZfsDatasetSnapshot)) {
			sourceZfsDataset = fmt.Sprintf("containers/%s", project.Prefix(source.Project(), sourceName))
			sourceZfsDatasetSnapshot = fmt.Sprintf("snapshot-%s", sourceZfsDatasetSnapshot)
		}
	}

	if sourceZfsDataset != "" {
		err := zfsPoolVolumeClone(target.Project(), poolName, sourceZfsDataset, sourceZfsDatasetSnapshot, targetZfsDataset, targetContainerMountPoint)
		if err != nil {
			return err
		}
		defer func() {
			if !revert {
				return
			}
			zfsPoolVolumeDestroy(poolName, targetZfsDataset)
		}()

		ourMount, err := s.ContainerMount(target)
		if err != nil {
			return err
		}
		if ourMount {
			defer s.ContainerUmount(target, targetContainerPath)
		}

		err = driver.CreateContainerMountpoint(targetContainerMountPoint, targetContainerPath, target.IsPrivileged())
		if err != nil {
			return err
		}
		defer func() {
			if !revert {
				return
			}
			deleteContainerMountpoint(targetContainerMountPoint, targetContainerPath, s.GetStorageTypeName())
		}()
	} else {
		err := s.ContainerCreate(target)
		if err != nil {
			return err
		}
		defer func() {
			if !revert {
				return
			}
			s.ContainerDelete(target)
		}()

		bwlimit := s.pool.Config["rsync.bwlimit"]
		output, err := rsync.LocalCopy(sourceContainerPath, targetContainerPath, bwlimit, true)
		if err != nil {
			return fmt.Errorf("rsync failed: %s", string(output))
		}
	}

	err := target.DeferTemplateApply("copy")
	if err != nil {
		return err
	}

	revert = false

	return nil
}

func (s *storageZfs) copyWithoutSnapshotFull(target Instance, source Instance) error {
	logger.Debugf("Creating full ZFS copy \"%s\" to \"%s\"", source.Name(), target.Name())

	sourceIsSnapshot := source.IsSnapshot()
	poolName := s.getOnDiskPoolName()

	sourceName := source.Name()
	sourceDataset := ""
	snapshotSuffix := ""

	targetName := target.Name()
	targetDataset := fmt.Sprintf("%s/containers/%s", poolName, project.Prefix(target.Project(), targetName))
	targetSnapshotDataset := ""

	if sourceIsSnapshot {
		sourceParentName, sourceSnapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(source.Name())
		snapshotSuffix = fmt.Sprintf("snapshot-%s", sourceSnapOnlyName)
		sourceDataset = fmt.Sprintf("%s/containers/%s@%s", poolName, project.Prefix(source.Project(), sourceParentName), snapshotSuffix)
		targetSnapshotDataset = fmt.Sprintf("%s/containers/%s@snapshot-%s", poolName, project.Prefix(target.Project(), targetName), sourceSnapOnlyName)
	} else {
		snapshotSuffix = uuid.NewRandom().String()
		sourceDataset = fmt.Sprintf("%s/containers/%s@%s", poolName, project.Prefix(source.Project(), sourceName), snapshotSuffix)
		targetSnapshotDataset = fmt.Sprintf("%s/containers/%s@%s", poolName, project.Prefix(target.Project(), targetName), snapshotSuffix)

		fs := fmt.Sprintf("containers/%s", project.Prefix(source.Project(), sourceName))
		err := zfsPoolVolumeSnapshotCreate(poolName, fs, snapshotSuffix)
		if err != nil {
			return err
		}
		defer func() {
			err := zfsPoolVolumeSnapshotDestroy(poolName, fs, snapshotSuffix)
			if err != nil {
				logger.Warnf("Failed to delete temporary ZFS snapshot \"%s\", manual cleanup needed", sourceDataset)
			}
		}()
	}

	zfsSendCmd := exec.Command("zfs", "send", sourceDataset)

	zfsRecvCmd := exec.Command("zfs", "receive", targetDataset)

	zfsRecvCmd.Stdin, _ = zfsSendCmd.StdoutPipe()
	zfsRecvCmd.Stdout = os.Stdout
	zfsRecvCmd.Stderr = os.Stderr

	err := zfsRecvCmd.Start()
	if err != nil {
		return err
	}

	err = zfsSendCmd.Run()
	if err != nil {
		return err
	}

	err = zfsRecvCmd.Wait()
	if err != nil {
		return err
	}

	msg, err := shared.RunCommand("zfs", "rollback", "-r", "-R", targetSnapshotDataset)
	if err != nil {
		logger.Errorf("Failed to rollback ZFS dataset: %s", msg)
		return err
	}

	targetContainerMountPoint := driver.GetContainerMountPoint(target.Project(), s.pool.Name, targetName)
	targetfs := fmt.Sprintf("containers/%s", project.Prefix(target.Project(), targetName))

	err = zfsPoolVolumeSet(poolName, targetfs, "canmount", "noauto")
	if err != nil {
		return err
	}

	err = zfsPoolVolumeSet(poolName, targetfs, "mountpoint", targetContainerMountPoint)
	if err != nil {
		return err
	}

	err = zfsPoolVolumeSnapshotDestroy(poolName, targetfs, snapshotSuffix)
	if err != nil {
		return err
	}

	ourMount, err := s.ContainerMount(target)
	if err != nil {
		return err
	}
	if ourMount {
		defer s.ContainerUmount(target, targetContainerMountPoint)
	}

	err = driver.CreateContainerMountpoint(targetContainerMountPoint, target.Path(), target.IsPrivileged())
	if err != nil {
		return err
	}

	logger.Debugf("Created full ZFS copy \"%s\" to \"%s\"", source.Name(), target.Name())
	return nil
}

func (s *storageZfs) copyWithSnapshots(target Instance, source Instance, parentSnapshot string) error {
	sourceName := source.Name()
	targetParentName, targetSnapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(target.Name())
	containersPath := driver.GetSnapshotMountPoint(target.Project(), s.pool.Name, targetParentName)
	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", project.Prefix(target.Project(), targetParentName))
	snapshotMntPointSymlink := shared.VarPath("snapshots", project.Prefix(target.Project(), targetParentName))
	err := driver.CreateSnapshotMountpoint(containersPath, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	if err != nil {
		return err
	}

	poolName := s.getOnDiskPoolName()
	sourceParentName, sourceSnapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(sourceName)
	currentSnapshotDataset := fmt.Sprintf("%s/containers/%s@snapshot-%s", poolName, project.Prefix(source.Project(), sourceParentName), sourceSnapOnlyName)
	args := []string{"send", currentSnapshotDataset}
	if parentSnapshot != "" {
		parentName, parentSnaponlyName, _ := shared.ContainerGetParentAndSnapshotName(parentSnapshot)
		parentSnapshotDataset := fmt.Sprintf("%s/containers/%s@snapshot-%s", poolName, project.Prefix(source.Project(), parentName), parentSnaponlyName)
		args = append(args, "-i", parentSnapshotDataset)
	}

	zfsSendCmd := exec.Command("zfs", args...)
	targetSnapshotDataset := fmt.Sprintf("%s/containers/%s@snapshot-%s", poolName, project.Prefix(target.Project(), targetParentName), targetSnapOnlyName)
	zfsRecvCmd := exec.Command("zfs", "receive", "-F", targetSnapshotDataset)

	zfsRecvCmd.Stdin, _ = zfsSendCmd.StdoutPipe()
	zfsRecvCmd.Stdout = os.Stdout
	zfsRecvCmd.Stderr = os.Stderr

	err = zfsRecvCmd.Start()
	if err != nil {
		return err
	}

	err = zfsSendCmd.Run()
	if err != nil {
		return err
	}

	err = zfsRecvCmd.Wait()
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) doCrossPoolContainerCopy(target Instance, source Instance, containerOnly bool, refresh bool, refreshSnapshots []Instance) error {
	sourcePool, err := source.StoragePool()
	if err != nil {
		return err
	}

	// setup storage for the source volume
	srcStorage, err := storagePoolVolumeInit(s.s, "default", sourcePool, source.Name(), storagePoolVolumeTypeContainer)
	if err != nil {
		return err
	}

	ourMount, err := srcStorage.StoragePoolMount()
	if err != nil {
		return err
	}
	if ourMount {
		defer srcStorage.StoragePoolUmount()
	}

	targetPool, err := target.StoragePool()
	if err != nil {
		return err
	}

	var snapshots []Instance

	if refresh {
		snapshots = refreshSnapshots
	} else {
		snapshots, err = source.Snapshots()
		if err != nil {
			return err
		}

		// create the main container
		err = s.doContainerCreate(target.Project(), target.Name(), target.IsPrivileged())
		if err != nil {
			return err
		}
	}

	_, err = s.doContainerMount(target.Project(), target.Name(), target.IsPrivileged())
	if err != nil {
		return err
	}
	defer s.ContainerUmount(target, shared.VarPath("containers", project.Prefix(target.Project(), target.Name())))

	destContainerMntPoint := driver.GetContainerMountPoint(target.Project(), targetPool, target.Name())
	bwlimit := s.pool.Config["rsync.bwlimit"]
	if !containerOnly {
		for _, snap := range snapshots {
			srcSnapshotMntPoint := driver.GetSnapshotMountPoint(target.Project(), sourcePool, snap.Name())
			_, err = rsync.LocalCopy(srcSnapshotMntPoint, destContainerMntPoint, bwlimit, true)
			if err != nil {
				logger.Errorf("Failed to rsync into ZFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
				return err
			}

			// create snapshot
			_, snapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(snap.Name())
			err = s.doContainerSnapshotCreate(snap.Project(), fmt.Sprintf("%s/%s", target.Name(), snapOnlyName), target.Name())
			if err != nil {
				return err
			}
		}
	}

	srcContainerMntPoint := driver.GetContainerMountPoint(source.Project(), sourcePool, source.Name())
	_, err = rsync.LocalCopy(srcContainerMntPoint, destContainerMntPoint, bwlimit, true)
	if err != nil {
		logger.Errorf("Failed to rsync into ZFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	return nil
}

func (s *storageZfs) ContainerCopy(target Instance, source Instance, containerOnly bool) error {
	logger.Debugf("Copying ZFS container storage %s to %s", source.Name(), target.Name())

	ourStart, err := source.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer source.StorageStop()
	}

	_, sourcePool, _ := source.Storage().GetContainerPoolInfo()
	_, targetPool, _ := target.Storage().GetContainerPoolInfo()
	if sourcePool != targetPool {
		return s.doCrossPoolContainerCopy(target, source, containerOnly, false, nil)
	}

	snapshots, err := source.Snapshots()
	if err != nil {
		return err
	}

	if containerOnly || len(snapshots) == 0 {
		if s.pool.Config["zfs.clone_copy"] != "" && !shared.IsTrue(s.pool.Config["zfs.clone_copy"]) {
			err = s.copyWithoutSnapshotFull(target, source)
			if err != nil {
				return err
			}
		} else {
			err = s.copyWithoutSnapshotsSparse(target, source)
			if err != nil {
				return err
			}
		}
	} else {
		targetContainerName := target.Name()
		targetContainerPath := target.Path()
		targetContainerMountPoint := driver.GetContainerMountPoint(target.Project(), s.pool.Name, targetContainerName)
		err = driver.CreateContainerMountpoint(targetContainerMountPoint, targetContainerPath, target.IsPrivileged())
		if err != nil {
			return err
		}

		prev := ""
		prevSnapOnlyName := ""
		for i, snap := range snapshots {
			if i > 0 {
				prev = snapshots[i-1].Name()
			}

			sourceSnapshot, err := instanceLoadByProjectAndName(s.s, source.Project(), snap.Name())
			if err != nil {
				return err
			}

			_, snapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(snap.Name())
			prevSnapOnlyName = snapOnlyName
			newSnapName := fmt.Sprintf("%s/%s", target.Name(), snapOnlyName)
			targetSnapshot, err := instanceLoadByProjectAndName(s.s, target.Project(), newSnapName)
			if err != nil {
				return err
			}

			err = s.copyWithSnapshots(targetSnapshot, sourceSnapshot, prev)
			if err != nil {
				return err
			}
		}

		poolName := s.getOnDiskPoolName()

		// send actual container
		tmpSnapshotName := fmt.Sprintf("copy-send-%s", uuid.NewRandom().String())
		err = zfsPoolVolumeSnapshotCreate(poolName, fmt.Sprintf("containers/%s", project.Prefix(source.Project(), source.Name())), tmpSnapshotName)
		if err != nil {
			return err
		}

		currentSnapshotDataset := fmt.Sprintf("%s/containers/%s@%s", poolName, project.Prefix(source.Project(), source.Name()), tmpSnapshotName)
		args := []string{"send", currentSnapshotDataset}
		if prevSnapOnlyName != "" {
			parentSnapshotDataset := fmt.Sprintf("%s/containers/%s@snapshot-%s", poolName, project.Prefix(source.Project(), source.Name()), prevSnapOnlyName)
			args = append(args, "-i", parentSnapshotDataset)
		}

		zfsSendCmd := exec.Command("zfs", args...)
		targetSnapshotDataset := fmt.Sprintf("%s/containers/%s@%s", poolName, project.Prefix(target.Project(), target.Name()), tmpSnapshotName)
		zfsRecvCmd := exec.Command("zfs", "receive", "-F", targetSnapshotDataset)

		zfsRecvCmd.Stdin, _ = zfsSendCmd.StdoutPipe()
		zfsRecvCmd.Stdout = os.Stdout
		zfsRecvCmd.Stderr = os.Stderr

		err = zfsRecvCmd.Start()
		if err != nil {
			return err
		}

		err = zfsSendCmd.Run()
		if err != nil {
			return err
		}

		err = zfsRecvCmd.Wait()
		if err != nil {
			return err
		}

		zfsPoolVolumeSnapshotDestroy(poolName, fmt.Sprintf("containers/%s", project.Prefix(source.Project(), source.Name())), tmpSnapshotName)
		zfsPoolVolumeSnapshotDestroy(poolName, fmt.Sprintf("containers/%s", project.Prefix(target.Project(), target.Name())), tmpSnapshotName)

		fs := fmt.Sprintf("containers/%s", project.Prefix(target.Project(), target.Name()))
		err = zfsPoolVolumeSet(poolName, fs, "canmount", "noauto")
		if err != nil {
			return err
		}

		err = zfsPoolVolumeSet(poolName, fs, "mountpoint", targetContainerMountPoint)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Copied ZFS container storage %s to %s", source.Name(), target.Name())
	return nil
}

func (s *storageZfs) ContainerRefresh(target Instance, source Instance, snapshots []Instance) error {
	logger.Debugf("Refreshing ZFS container storage for %s from %s", target.Name(), source.Name())

	ourStart, err := source.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer source.StorageStop()
	}

	return s.doCrossPoolContainerCopy(target, source, len(snapshots) == 0, true, snapshots)
}

func (s *storageZfs) ContainerRename(container Instance, newName string) error {
	logger.Debugf("Renaming ZFS storage volume for container \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newName)

	poolName := s.getOnDiskPoolName()
	oldName := container.Name()

	// Unmount the dataset.
	_, err := s.ContainerUmount(container, "")
	if err != nil {
		return err
	}

	// Rename the dataset.
	oldZfsDataset := fmt.Sprintf("containers/%s", project.Prefix(container.Project(), oldName))
	newZfsDataset := fmt.Sprintf("containers/%s", project.Prefix(container.Project(), newName))
	err = zfsPoolVolumeRename(poolName, oldZfsDataset, newZfsDataset, false)
	if err != nil {
		return err
	}
	revert := true
	defer func() {
		if !revert {
			return
		}
		s.ContainerRename(container, oldName)
	}()

	// Set the new mountpoint for the dataset.
	newContainerMntPoint := driver.GetContainerMountPoint(container.Project(), s.pool.Name, newName)
	err = zfsPoolVolumeSet(poolName, newZfsDataset, "mountpoint", newContainerMntPoint)
	if err != nil {
		return err
	}

	// Unmount the dataset.
	container.(*containerLXC).name = newName
	_, err = s.ContainerUmount(container, "")
	if err != nil {
		return err
	}

	// Create new mountpoint on the storage pool.
	oldContainerMntPoint := driver.GetContainerMountPoint(container.Project(), s.pool.Name, oldName)
	oldContainerMntPointSymlink := container.Path()
	newContainerMntPointSymlink := shared.VarPath("containers", project.Prefix(container.Project(), newName))
	err = renameContainerMountpoint(oldContainerMntPoint, oldContainerMntPointSymlink, newContainerMntPoint, newContainerMntPointSymlink)
	if err != nil {
		return err
	}

	// Rename the snapshot mountpoint on the storage pool.
	oldSnapshotMntPoint := driver.GetSnapshotMountPoint(container.Project(), s.pool.Name, oldName)
	newSnapshotMntPoint := driver.GetSnapshotMountPoint(container.Project(), s.pool.Name, newName)
	if shared.PathExists(oldSnapshotMntPoint) {
		err := os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
		if err != nil {
			return err
		}
	}

	// Remove old symlink.
	oldSnapshotPath := shared.VarPath("snapshots", project.Prefix(container.Project(), oldName))
	if shared.PathExists(oldSnapshotPath) {
		err := os.Remove(oldSnapshotPath)
		if err != nil {
			return err
		}
	}

	// Create new symlink.
	newSnapshotPath := shared.VarPath("snapshots", project.Prefix(container.Project(), newName))
	if shared.PathExists(newSnapshotPath) {
		err := os.Symlink(newSnapshotMntPoint, newSnapshotPath)
		if err != nil {
			return err
		}
	}

	revert = false

	logger.Debugf("Renamed ZFS storage volume for container \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newName)
	return nil
}

func (s *storageZfs) ContainerRestore(target Instance, source Instance) error {
	logger.Debugf("Restoring ZFS storage volume for container \"%s\" from %s to %s", s.volume.Name, source.Name(), target.Name())

	snaps, err := target.Snapshots()
	if err != nil {
		return err
	}

	if snaps[len(snaps)-1].Name() != source.Name() {
		if s.pool.Config["volume.zfs.remove_snapshots"] != "" {
			zfsRemoveSnapshots = s.pool.Config["volume.zfs.remove_snapshots"]
		}

		if s.volume.Config["zfs.remove_snapshots"] != "" {
			zfsRemoveSnapshots = s.volume.Config["zfs.remove_snapshots"]
		}

		if !shared.IsTrue(zfsRemoveSnapshots) {
			return fmt.Errorf("ZFS can only restore from the latest snapshot. Delete newer snapshots or copy the snapshot into a new container instead")
		}
	}

	// Start storage for source container
	ourSourceStart, err := source.StorageStart()
	if err != nil {
		return err
	}
	if ourSourceStart {
		defer source.StorageStop()
	}

	// Start storage for target container
	ourTargetStart, err := target.StorageStart()
	if err != nil {
		return err
	}
	if ourTargetStart {
		defer target.StorageStop()
	}

	for i := len(snaps) - 1; i != 0; i-- {
		if snaps[i].Name() == source.Name() {
			break
		}

		err := snaps[i].Delete()
		if err != nil {
			return err
		}
	}

	// Restore the snapshot
	cName, snapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(source.Name())
	snapName := fmt.Sprintf("snapshot-%s", snapOnlyName)

	err = zfsPoolVolumeSnapshotRestore(s.getOnDiskPoolName(), fmt.Sprintf("containers/%s", project.Prefix(source.Project(), cName)), snapName)
	if err != nil {
		return err
	}

	logger.Debugf("Restored ZFS storage volume for container \"%s\" from %s to %s", s.volume.Name, source.Name(), target.Name())
	return nil
}

func (s *storageZfs) ContainerGetUsage(container Instance) (int64, error) {
	var err error

	fs := fmt.Sprintf("containers/%s", project.Prefix(container.Project(), container.Name()))

	property := "used"

	if s.pool.Config["volume.zfs.use_refquota"] != "" {
		zfsUseRefquota = s.pool.Config["volume.zfs.use_refquota"]
	}
	if s.volume.Config["zfs.use_refquota"] != "" {
		zfsUseRefquota = s.volume.Config["zfs.use_refquota"]
	}

	if shared.IsTrue(zfsUseRefquota) {
		property = "referenced"
	}

	// Shortcut for refquota
	mountpoint := driver.GetContainerMountPoint(container.Project(), s.pool.Name, container.Name())
	if property == "referenced" && shared.IsMountPoint(mountpoint) {
		var stat unix.Statfs_t
		err := unix.Statfs(mountpoint, &stat)
		if err != nil {
			return -1, err
		}

		return int64(stat.Blocks-stat.Bfree) * int64(stat.Bsize), nil
	}

	value, err := zfsFilesystemEntityPropertyGet(s.getOnDiskPoolName(), fs, property)
	if err != nil {
		return -1, err
	}

	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return -1, err
	}

	return valueInt, nil
}

func (s *storageZfs) doContainerSnapshotCreate(projectName, targetName string, sourceName string) error {
	snapshotContainerName := targetName
	logger.Debugf("Creating ZFS storage volume for snapshot \"%s\" on storage pool \"%s\"", snapshotContainerName, s.pool.Name)

	sourceContainerName := sourceName

	cName, snapshotSnapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(snapshotContainerName)
	snapName := fmt.Sprintf("snapshot-%s", snapshotSnapOnlyName)

	sourceZfsDataset := fmt.Sprintf("containers/%s", project.Prefix(projectName, cName))
	err := zfsPoolVolumeSnapshotCreate(s.getOnDiskPoolName(), sourceZfsDataset, snapName)
	if err != nil {
		return err
	}

	snapshotMntPoint := driver.GetSnapshotMountPoint(projectName, s.pool.Name, snapshotContainerName)
	if !shared.PathExists(snapshotMntPoint) {
		err := os.MkdirAll(snapshotMntPoint, 0100)
		if err != nil {
			return err
		}
	}

	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", project.Prefix(projectName, sourceName))
	snapshotMntPointSymlink := shared.VarPath("snapshots", project.Prefix(projectName, sourceContainerName))
	if !shared.PathExists(snapshotMntPointSymlink) {
		err := os.Symlink(snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Created ZFS storage volume for snapshot \"%s\" on storage pool \"%s\"", snapshotContainerName, s.pool.Name)
	return nil
}

func (s *storageZfs) ContainerSnapshotCreate(snapshotContainer Instance, sourceContainer Instance) error {
	err := s.doContainerSnapshotCreate(sourceContainer.Project(), snapshotContainer.Name(), sourceContainer.Name())
	if err != nil {
		s.ContainerSnapshotDelete(snapshotContainer)
		return err
	}
	return nil
}

func zfsSnapshotDeleteInternal(projectName, poolName string, ctName string, onDiskPoolName string) error {
	sourceContainerName, sourceContainerSnapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(ctName)
	snapName := fmt.Sprintf("snapshot-%s", sourceContainerSnapOnlyName)

	if zfsFilesystemEntityExists(onDiskPoolName,
		fmt.Sprintf("containers/%s@%s",
			project.Prefix(projectName, sourceContainerName), snapName)) {
		removable, err := zfsPoolVolumeSnapshotRemovable(onDiskPoolName,
			fmt.Sprintf("containers/%s",
				project.Prefix(projectName, sourceContainerName)),
			snapName)
		if err != nil {
			return err
		}

		if removable {
			err = zfsPoolVolumeSnapshotDestroy(onDiskPoolName,
				fmt.Sprintf("containers/%s",
					project.Prefix(projectName, sourceContainerName)),
				snapName)
		} else {
			err = zfsPoolVolumeSnapshotRename(onDiskPoolName,
				fmt.Sprintf("containers/%s",
					project.Prefix(projectName, sourceContainerName)),
				snapName,
				fmt.Sprintf("copy-%s", uuid.NewRandom().String()))
		}
		if err != nil {
			return err
		}
	}

	// Delete the snapshot on its storage pool:
	// ${POOL}/snapshots/<snapshot_name>
	snapshotContainerMntPoint := driver.GetSnapshotMountPoint(projectName, poolName, ctName)
	if shared.PathExists(snapshotContainerMntPoint) {
		err := os.RemoveAll(snapshotContainerMntPoint)
		if err != nil {
			return err
		}
	}

	// Check if we can remove the snapshot symlink:
	// ${LXD_DIR}/snapshots/<container_name> to ${POOL}/snapshots/<container_name>
	// by checking if the directory is empty.
	snapshotContainerPath := driver.GetSnapshotMountPoint(projectName, poolName, sourceContainerName)
	empty, _ := shared.PathIsEmpty(snapshotContainerPath)
	if empty == true {
		// Remove the snapshot directory for the container:
		// ${POOL}/snapshots/<source_container_name>
		err := os.Remove(snapshotContainerPath)
		if err != nil {
			return err
		}

		snapshotSymlink := shared.VarPath("snapshots", project.Prefix(projectName, sourceContainerName))
		if shared.PathExists(snapshotSymlink) {
			err := os.Remove(snapshotSymlink)
			if err != nil {
				return err
			}
		}
	}

	// Legacy
	snapPath := shared.VarPath(fmt.Sprintf("snapshots/%s/%s.zfs", project.Prefix(projectName, sourceContainerName), sourceContainerSnapOnlyName))
	if shared.PathExists(snapPath) {
		err := os.Remove(snapPath)
		if err != nil {
			return err
		}
	}

	// Legacy
	parent := shared.VarPath(fmt.Sprintf("snapshots/%s", project.Prefix(projectName, sourceContainerName)))
	if ok, _ := shared.PathIsEmpty(parent); ok {
		err := os.Remove(parent)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageZfs) ContainerSnapshotDelete(snapshotContainer Instance) error {
	logger.Debugf("Deleting ZFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	poolName := s.getOnDiskPoolName()
	err := zfsSnapshotDeleteInternal(snapshotContainer.Project(), s.pool.Name, snapshotContainer.Name(),
		poolName)
	if err != nil {
		return err
	}

	logger.Debugf("Deleted ZFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) ContainerSnapshotRename(snapshotContainer Instance, newName string) error {
	logger.Debugf("Renaming ZFS storage volume for snapshot \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newName)

	oldName := snapshotContainer.Name()

	oldcName, oldSnapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(snapshotContainer.Name())
	oldZfsDatasetName := fmt.Sprintf("snapshot-%s", oldSnapOnlyName)

	_, newSnapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(newName)
	newZfsDatasetName := fmt.Sprintf("snapshot-%s", newSnapOnlyName)

	if oldZfsDatasetName != newZfsDatasetName {
		err := zfsPoolVolumeSnapshotRename(
			s.getOnDiskPoolName(), fmt.Sprintf("containers/%s", project.Prefix(snapshotContainer.Project(), oldcName)), oldZfsDatasetName, newZfsDatasetName)
		if err != nil {
			return err
		}
	}
	revert := true
	defer func() {
		if !revert {
			return
		}
		//s.ContainerSnapshotRename(snapshotContainer, oldName)
	}()

	oldStyleSnapshotMntPoint := shared.VarPath(fmt.Sprintf("snapshots/%s/%s.zfs", project.Prefix(snapshotContainer.Project(), oldcName), oldSnapOnlyName))
	if shared.PathExists(oldStyleSnapshotMntPoint) {
		err := os.Remove(oldStyleSnapshotMntPoint)
		if err != nil {
			return err
		}
	}

	oldSnapshotMntPoint := driver.GetSnapshotMountPoint(snapshotContainer.Project(), s.pool.Name, oldName)
	if shared.PathExists(oldSnapshotMntPoint) {
		err := os.Remove(oldSnapshotMntPoint)
		if err != nil {
			return err
		}
	}

	newSnapshotMntPoint := driver.GetSnapshotMountPoint(snapshotContainer.Project(), s.pool.Name, newName)
	if !shared.PathExists(newSnapshotMntPoint) {
		err := os.MkdirAll(newSnapshotMntPoint, 0100)
		if err != nil {
			return err
		}
	}

	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", project.Prefix(snapshotContainer.Project(), oldcName))
	snapshotMntPointSymlink := shared.VarPath("snapshots", project.Prefix(snapshotContainer.Project(), oldcName))
	if !shared.PathExists(snapshotMntPointSymlink) {
		err := os.Symlink(snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return err
		}
	}

	revert = false

	logger.Debugf("Renamed ZFS storage volume for snapshot \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newName)
	return nil
}

func (s *storageZfs) ContainerSnapshotStart(container Instance) (bool, error) {
	logger.Debugf("Initializing ZFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	cName, sName, _ := shared.ContainerGetParentAndSnapshotName(container.Name())
	sourceFs := fmt.Sprintf("containers/%s", project.Prefix(container.Project(), cName))
	sourceSnap := fmt.Sprintf("snapshot-%s", sName)
	destFs := fmt.Sprintf("snapshots/%s/%s", project.Prefix(container.Project(), cName), sName)

	poolName := s.getOnDiskPoolName()
	snapshotMntPoint := driver.GetSnapshotMountPoint(container.Project(), s.pool.Name, container.Name())
	err := zfsPoolVolumeClone(container.Project(), poolName, sourceFs, sourceSnap, destFs, snapshotMntPoint)
	if err != nil {
		return false, err
	}

	err = zfsMount(poolName, destFs)
	if err != nil {
		return false, err
	}

	logger.Debugf("Initialized ZFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return true, nil
}

func (s *storageZfs) ContainerSnapshotStop(container Instance) (bool, error) {
	logger.Debugf("Stopping ZFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	cName, sName, _ := shared.ContainerGetParentAndSnapshotName(container.Name())
	destFs := fmt.Sprintf("snapshots/%s/%s", project.Prefix(container.Project(), cName), sName)

	err := zfsPoolVolumeDestroy(s.getOnDiskPoolName(), destFs)
	if err != nil {
		return false, err
	}

	logger.Debugf("Stopped ZFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return true, nil
}

func (s *storageZfs) ContainerSnapshotCreateEmpty(snapshotContainer Instance) error {
	/* don't touch the fs yet, as migration will do that for us */
	return nil
}

func (s *storageZfs) doContainerOnlyBackup(tmpPath string, backup backup.Backup, source Instance) error {
	sourceIsSnapshot := source.IsSnapshot()
	poolName := s.getOnDiskPoolName()

	sourceName := source.Name()
	sourceDataset := ""
	snapshotSuffix := ""

	if sourceIsSnapshot {
		sourceParentName, sourceSnapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(source.Name())
		snapshotSuffix = fmt.Sprintf("backup-%s", sourceSnapOnlyName)
		sourceDataset = fmt.Sprintf("%s/containers/%s@%s", poolName, project.Prefix(source.Project(), sourceParentName), snapshotSuffix)
	} else {
		snapshotSuffix = uuid.NewRandom().String()
		sourceDataset = fmt.Sprintf("%s/containers/%s@%s", poolName, project.Prefix(source.Project(), sourceName), snapshotSuffix)

		fs := fmt.Sprintf("containers/%s", project.Prefix(source.Project(), sourceName))
		err := zfsPoolVolumeSnapshotCreate(poolName, fs, snapshotSuffix)
		if err != nil {
			return err
		}

		defer func() {
			err := zfsPoolVolumeSnapshotDestroy(poolName, fs, snapshotSuffix)
			if err != nil {
				logger.Warnf("Failed to delete temporary ZFS snapshot \"%s\", manual cleanup needed", sourceDataset)
			}
		}()
	}

	// Dump the container to a file
	backupFile := fmt.Sprintf("%s/%s", tmpPath, "container.bin")
	f, err := os.OpenFile(backupFile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	zfsSendCmd := exec.Command("zfs", "send", sourceDataset)
	zfsSendCmd.Stdout = f
	err = zfsSendCmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) doSnapshotBackup(tmpPath string, backup backup.Backup, source Instance, parentSnapshot string) error {
	sourceName := source.Name()
	snapshotsPath := fmt.Sprintf("%s/snapshots", tmpPath)

	// Create backup path for snapshots
	err := os.MkdirAll(snapshotsPath, 0711)
	if err != nil {
		return err
	}

	poolName := s.getOnDiskPoolName()
	sourceParentName, sourceSnapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(sourceName)
	currentSnapshotDataset := fmt.Sprintf("%s/containers/%s@snapshot-%s", poolName, project.Prefix(source.Project(), sourceParentName), sourceSnapOnlyName)
	args := []string{"send", currentSnapshotDataset}
	if parentSnapshot != "" {
		parentName, parentSnaponlyName, _ := shared.ContainerGetParentAndSnapshotName(parentSnapshot)
		parentSnapshotDataset := fmt.Sprintf("%s/containers/%s@snapshot-%s", poolName, project.Prefix(source.Project(), parentName), parentSnaponlyName)
		args = append(args, "-i", parentSnapshotDataset)
	}

	backupFile := fmt.Sprintf("%s/%s.bin", snapshotsPath, sourceSnapOnlyName)
	f, err := os.OpenFile(backupFile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	zfsSendCmd := exec.Command("zfs", args...)
	zfsSendCmd.Stdout = f
	return zfsSendCmd.Run()
}

func (s *storageZfs) doContainerBackupCreateOptimized(tmpPath string, backup backup.Backup, source Instance) error {
	// Handle snapshots
	snapshots, err := source.Snapshots()
	if err != nil {
		return err
	}

	if backup.InstanceOnly() || len(snapshots) == 0 {
		err = s.doContainerOnlyBackup(tmpPath, backup, source)
	} else {
		prev := ""
		prevSnapOnlyName := ""
		for i, snap := range snapshots {
			if i > 0 {
				prev = snapshots[i-1].Name()
			}

			sourceSnapshot, err := instanceLoadByProjectAndName(s.s, source.Project(), snap.Name())
			if err != nil {
				return err
			}

			_, snapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(snap.Name())
			prevSnapOnlyName = snapOnlyName
			err = s.doSnapshotBackup(tmpPath, backup, sourceSnapshot, prev)
			if err != nil {
				return err
			}
		}

		// Dump the container to a file
		poolName := s.getOnDiskPoolName()
		tmpSnapshotName := fmt.Sprintf("backup-%s", uuid.NewRandom().String())
		err = zfsPoolVolumeSnapshotCreate(poolName, fmt.Sprintf("containers/%s", project.Prefix(source.Project(), source.Name())), tmpSnapshotName)
		if err != nil {
			return err
		}

		currentSnapshotDataset := fmt.Sprintf("%s/containers/%s@%s", poolName, project.Prefix(source.Project(), source.Name()), tmpSnapshotName)
		args := []string{"send", currentSnapshotDataset}
		if prevSnapOnlyName != "" {
			parentSnapshotDataset := fmt.Sprintf("%s/containers/%s@snapshot-%s", poolName, project.Prefix(source.Project(), source.Name()), prevSnapOnlyName)
			args = append(args, "-i", parentSnapshotDataset)
		}

		backupFile := fmt.Sprintf("%s/container.bin", tmpPath)
		f, err := os.OpenFile(backupFile, os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			return err
		}
		defer f.Close()

		zfsSendCmd := exec.Command("zfs", args...)
		zfsSendCmd.Stdout = f

		err = zfsSendCmd.Run()
		if err != nil {
			return err
		}

		zfsPoolVolumeSnapshotDestroy(poolName, fmt.Sprintf("containers/%s", project.Prefix(source.Project(), source.Name())), tmpSnapshotName)
	}
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) doContainerBackupCreateVanilla(tmpPath string, backup backup.Backup, source Instance) error {
	// Prepare for rsync
	rsync := func(oldPath string, newPath string, bwlimit string) error {
		output, err := rsync.LocalCopy(oldPath, newPath, bwlimit, true)
		if err != nil {
			return fmt.Errorf("Failed to rsync: %s: %s", string(output), err)
		}

		return nil
	}

	bwlimit := s.pool.Config["rsync.bwlimit"]
	projectName := source.Project()

	// Handle snapshots
	if !backup.InstanceOnly() {
		snapshotsPath := fmt.Sprintf("%s/snapshots", tmpPath)

		// Retrieve the snapshots
		snapshots, err := source.Snapshots()
		if err != nil {
			return errors.Wrap(err, "Retrieve snaphots")
		}

		// Create the snapshot path
		if len(snapshots) > 0 {
			err = os.MkdirAll(snapshotsPath, 0711)
			if err != nil {
				return errors.Wrap(err, "Create snapshot path")
			}
		}

		for _, snap := range snapshots {
			_, snapName, _ := shared.ContainerGetParentAndSnapshotName(snap.Name())

			// Mount the snapshot to a usable path
			_, err := s.ContainerSnapshotStart(snap)
			if err != nil {
				return errors.Wrap(err, "Mount snapshot")
			}

			snapshotMntPoint := driver.GetSnapshotMountPoint(projectName, s.pool.Name, snap.Name())
			target := fmt.Sprintf("%s/%s", snapshotsPath, snapName)

			// Copy the snapshot
			err = rsync(snapshotMntPoint, target, bwlimit)
			s.ContainerSnapshotStop(snap)
			if err != nil {
				return errors.Wrap(err, "Copy snapshot")
			}
		}
	}

	// Make a temporary copy of the container
	containersPath := driver.GetContainerMountPoint("default", s.pool.Name, "")
	tmpContainerMntPoint, err := ioutil.TempDir(containersPath, source.Name())
	if err != nil {
		return errors.Wrap(err, "Create temporary copy dir")
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	err = os.Chmod(tmpContainerMntPoint, 0100)
	if err != nil {
		return errors.Wrap(err, "Change temporary mount point permissions")
	}

	snapshotSuffix := uuid.NewRandom().String()
	sourceName := source.Name()
	fs := fmt.Sprintf("containers/%s", project.Prefix(projectName, sourceName))
	sourceZfsDatasetSnapshot := fmt.Sprintf("snapshot-%s", snapshotSuffix)
	poolName := s.getOnDiskPoolName()
	err = zfsPoolVolumeSnapshotCreate(poolName, fs, sourceZfsDatasetSnapshot)
	if err != nil {
		return err
	}
	defer zfsPoolVolumeSnapshotDestroy(poolName, fs, sourceZfsDatasetSnapshot)

	targetZfsDataset := fmt.Sprintf("containers/%s", snapshotSuffix)
	err = zfsPoolVolumeClone(source.Project(), poolName, fs, sourceZfsDatasetSnapshot, targetZfsDataset, tmpContainerMntPoint)
	if err != nil {
		return errors.Wrap(err, "Clone volume")
	}
	defer zfsPoolVolumeDestroy(poolName, targetZfsDataset)

	// Mount the temporary copy
	if !shared.IsMountPoint(tmpContainerMntPoint) {
		err = zfsMount(poolName, targetZfsDataset)
		if err != nil {
			return errors.Wrap(err, "Mount temporary copy")
		}
		defer zfsUmount(poolName, targetZfsDataset, tmpContainerMntPoint)
	}

	// Copy the container
	containerPath := fmt.Sprintf("%s/container", tmpPath)
	err = rsync(tmpContainerMntPoint, containerPath, bwlimit)
	if err != nil {
		return errors.Wrap(err, "Copy container")
	}

	return nil
}

func (s *storageZfs) ContainerBackupCreate(path string, backup backup.Backup, source Instance) error {
	// Generate the actual backup
	if backup.OptimizedStorage() {
		err := s.doContainerBackupCreateOptimized(path, backup, source)
		if err != nil {
			return errors.Wrap(err, "Optimized backup")
		}
	} else {
		err := s.doContainerBackupCreateVanilla(path, backup, source)
		if err != nil {
			return errors.Wrap(err, "Vanilla backup")
		}
	}

	return nil
}

func (s *storageZfs) doContainerBackupLoadOptimized(info backup.Info, data io.ReadSeeker, tarArgs []string) error {
	containerName, _, _ := shared.ContainerGetParentAndSnapshotName(info.Name)
	containerMntPoint := driver.GetContainerMountPoint(info.Project, s.pool.Name, containerName)
	err := driver.CreateContainerMountpoint(containerMntPoint, driver.InstancePath(instancetype.Container, info.Project, info.Name, false), info.Privileged)
	if err != nil {
		return err
	}

	unpackPath := fmt.Sprintf("%s/.backup", containerMntPoint)
	err = os.MkdirAll(unpackPath, 0711)
	if err != nil {
		return err
	}

	err = os.Chmod(unpackPath, 0100)
	if err != nil {
		// can't use defer because it needs to run before the mount
		os.RemoveAll(unpackPath)
		return err
	}

	// Prepare tar arguments
	args := append(tarArgs, []string{
		"-",
		"--strip-components=1",
		"-C", unpackPath, "backup",
	}...)

	// Extract container
	data.Seek(0, 0)
	err = shared.RunCommandWithFds(data, nil, "tar", args...)
	if err != nil {
		// can't use defer because it needs to run before the mount
		os.RemoveAll(unpackPath)
		logger.Errorf("Failed to untar \"%s\" into \"%s\": %s", info.Name, unpackPath, err)
		return err
	}

	poolName := s.getOnDiskPoolName()
	for _, snapshotOnlyName := range info.Snapshots {
		snapshotBackup := fmt.Sprintf("%s/snapshots/%s.bin", unpackPath, snapshotOnlyName)
		feeder, err := os.Open(snapshotBackup)
		if err != nil {
			// can't use defer because it needs to run before the mount
			os.RemoveAll(unpackPath)
			return err
		}

		snapshotDataset := fmt.Sprintf("%s/containers/%s@snapshot-%s", poolName, project.Prefix(info.Project, containerName), snapshotOnlyName)
		zfsRecvCmd := exec.Command("zfs", "receive", "-F", snapshotDataset)
		zfsRecvCmd.Stdin = feeder
		err = zfsRecvCmd.Run()
		feeder.Close()
		if err != nil {
			// can't use defer because it needs to run before the mount
			os.RemoveAll(unpackPath)
			return err
		}

		// create mountpoint
		snapshotMntPoint := driver.GetSnapshotMountPoint(info.Project, s.pool.Name, fmt.Sprintf("%s/%s", containerName, snapshotOnlyName))
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", project.Prefix(info.Project, containerName))
		snapshotMntPointSymlink := shared.VarPath("snapshots", project.Prefix(info.Project, containerName))
		err = driver.CreateSnapshotMountpoint(snapshotMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			// can't use defer because it needs to run before the mount
			os.RemoveAll(unpackPath)
			return err
		}
	}

	containerBackup := fmt.Sprintf("%s/container.bin", unpackPath)
	feeder, err := os.Open(containerBackup)
	if err != nil {
		// can't use defer because it needs to run before the mount
		os.RemoveAll(unpackPath)
		return err
	}
	defer feeder.Close()

	containerSnapshotDataset := fmt.Sprintf("%s/containers/%s@backup", poolName, project.Prefix(info.Project, containerName))
	zfsRecvCmd := exec.Command("zfs", "receive", "-F", containerSnapshotDataset)
	zfsRecvCmd.Stdin = feeder

	err = zfsRecvCmd.Run()
	os.RemoveAll(unpackPath)
	zfsPoolVolumeSnapshotDestroy(poolName, fmt.Sprintf("containers/%s", project.Prefix(info.Project, containerName)), "backup")
	if err != nil {
		return err
	}

	fs := fmt.Sprintf("containers/%s", project.Prefix(info.Project, containerName))
	err = zfsPoolVolumeSet(poolName, fs, "canmount", "noauto")
	if err != nil {
		return err
	}

	err = zfsPoolVolumeSet(poolName, fs, "mountpoint", containerMntPoint)
	if err != nil {
		return err
	}

	_, err = s.doContainerMount(info.Project, containerName, info.Privileged)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) doContainerBackupLoadVanilla(info backup.Info, data io.ReadSeeker, tarArgs []string) error {
	// create the main container
	err := s.doContainerCreate(info.Project, info.Name, info.Privileged)
	if err != nil {
		s.doContainerDelete(info.Project, info.Name)
		return errors.Wrap(err, "Create container")
	}

	_, err = s.doContainerMount(info.Project, info.Name, info.Privileged)
	if err != nil {
		return errors.Wrap(err, "Mount container")
	}

	containerMntPoint := driver.GetContainerMountPoint(info.Project, s.pool.Name, info.Name)
	// Extract container
	for _, snap := range info.Snapshots {
		// Extract snapshots
		cur := fmt.Sprintf("backup/snapshots/%s", snap)

		// Prepare tar arguments
		args := append(tarArgs, []string{
			"-",
			"--recursive-unlink",
			"--strip-components=3",
			"--xattrs-include=*",
			"-C", containerMntPoint, cur,
		}...)

		// Unpack
		data.Seek(0, 0)
		err = shared.RunCommandWithFds(data, nil, "tar", args...)
		if err != nil {
			logger.Errorf("Failed to untar \"%s\" into \"%s\": %s", cur, containerMntPoint, err)
			return errors.Wrap(err, "Unpack")
		}

		// create snapshot
		err = s.doContainerSnapshotCreate(info.Project, fmt.Sprintf("%s/%s", info.Name, snap), info.Name)
		if err != nil {
			return errors.Wrap(err, "Create snapshot")
		}
	}

	// Prepare tar arguments
	args := append(tarArgs, []string{
		"-",
		"--strip-components=2",
		"--xattrs-include=*",
		"-C", containerMntPoint, "backup/container",
	}...)

	// Extract container
	data.Seek(0, 0)
	err = shared.RunCommandWithFds(data, nil, "tar", args...)
	if err != nil {
		logger.Errorf("Failed to untar \"backup/container\" into \"%s\": %s", containerMntPoint, err)
		return errors.Wrap(err, "Extract")
	}

	return nil
}

func (s *storageZfs) ContainerBackupLoad(info backup.Info, data io.ReadSeeker, tarArgs []string) error {
	logger.Debugf("Loading ZFS storage volume for backup \"%s\" on storage pool \"%s\"", info.Name, s.pool.Name)

	if info.HasBinaryFormat {
		return s.doContainerBackupLoadOptimized(info, data, tarArgs)
	}

	return s.doContainerBackupLoadVanilla(info, data, tarArgs)
}

// - create temporary directory ${LXD_DIR}/images/lxd_images_
// - create new zfs volume images/<fingerprint>
// - mount the zfs volume on ${LXD_DIR}/images/lxd_images_
// - unpack the downloaded image in ${LXD_DIR}/images/lxd_images_
// - mark new zfs volume images/<fingerprint> readonly
// - remove mountpoint property from zfs volume images/<fingerprint>
// - create read-write snapshot from zfs volume images/<fingerprint>
func (s *storageZfs) ImageCreate(fingerprint string, tracker *ioprogress.ProgressTracker) error {
	logger.Debugf("Creating ZFS storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)

	// Common variables
	poolName := s.getOnDiskPoolName()
	imageMntPoint := driver.GetImageMountPoint(s.pool.Name, fingerprint)
	fs := fmt.Sprintf("images/%s", fingerprint)

	// Revert flags
	revertDB := true
	revertMountpoint := true
	revertDataset := true

	// Deal with bad/partial unpacks
	if zfsFilesystemEntityExists(poolName, fs) {
		zfsPoolVolumeDestroy(poolName, fmt.Sprintf("%s@readonly", fs))
		zfsPoolVolumeDestroy(poolName, fs)
		s.deleteImageDbPoolVolume(fingerprint)
	}

	// Create the image volume entry
	err := s.createImageDbPoolVolume(fingerprint)
	if err != nil {
		return err
	}

	defer func() {
		if !revertDB {
			return
		}

		s.deleteImageDbPoolVolume(fingerprint)
	}()

	// Create mountpoint if missing
	if !shared.PathExists(imageMntPoint) {
		err := os.MkdirAll(imageMntPoint, 0700)
		if err != nil {
			return err
		}

		defer func() {
			if !revertMountpoint {
				return
			}

			os.RemoveAll(imageMntPoint)
		}()
	}

	// Check for deleted images
	if zfsFilesystemEntityExists(poolName, fmt.Sprintf("deleted/%s", fmt.Sprintf("%s@readonly", fs))) {
		// Restore deleted image
		err := zfsPoolVolumeRename(poolName, fmt.Sprintf("deleted/%s", fs), fs, true)
		if err != nil {
			return err
		}

		// In case this is an image from an older lxd instance, wipe the mountpoint.
		err = zfsPoolVolumeSet(poolName, fs, "mountpoint", "none")
		if err != nil {
			return err
		}

		revertDB = false
		revertMountpoint = false
		return nil
	}

	// Create temporary mountpoint directory.
	tmp := driver.GetImageMountPoint(s.pool.Name, "")
	tmpImageDir, err := ioutil.TempDir(tmp, "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpImageDir)

	imagePath := shared.VarPath("images", fingerprint)

	// Create a new dataset for the image
	dataset := fmt.Sprintf("%s/%s", poolName, fs)
	msg, err := zfsPoolVolumeCreate(dataset, "mountpoint=none")
	if err != nil {
		logger.Errorf("Failed to create ZFS dataset \"%s\" on storage pool \"%s\": %s", dataset, s.pool.Name, msg)
		return err
	}

	defer func() {
		if !revertDataset {
			return
		}

		zfsPoolVolumeDestroy(poolName, fs)
	}()

	// Set a temporary mountpoint for the image.
	err = zfsPoolVolumeSet(poolName, fs, "mountpoint", tmpImageDir)
	if err != nil {
		return err
	}

	// Make sure that the image actually got mounted.
	if !shared.IsMountPoint(tmpImageDir) {
		zfsMount(poolName, fs)
	}

	// Unpack the image into the temporary mountpoint.
	err = driver.ImageUnpack(imagePath, tmpImageDir, false, s.s.OS.RunningInUserNS, nil)
	if err != nil {
		return err
	}

	// Mark the new storage volume for the image as readonly.
	if err = zfsPoolVolumeSet(poolName, fs, "readonly", "on"); err != nil {
		return err
	}

	// Remove the temporary mountpoint from the image storage volume.
	if err = zfsPoolVolumeSet(poolName, fs, "mountpoint", "none"); err != nil {
		return err
	}

	// Make sure that the image actually got unmounted.
	if shared.IsMountPoint(tmpImageDir) {
		zfsUmount(poolName, fs, tmpImageDir)
	}

	// Create a snapshot of that image on the storage pool which we clone for
	// container creation.
	err = zfsPoolVolumeSnapshotCreate(poolName, fs, "readonly")
	if err != nil {
		return err
	}

	revertDB = false
	revertMountpoint = false
	revertDataset = false

	logger.Debugf("Created ZFS storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
	return nil
}

func (s *storageZfs) ImageDelete(fingerprint string) error {
	logger.Debugf("Deleting ZFS storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)

	poolName := s.getOnDiskPoolName()
	fs := fmt.Sprintf("images/%s", fingerprint)

	if zfsFilesystemEntityExists(poolName, fs) {
		removable, err := zfsPoolVolumeSnapshotRemovable(poolName, fs, "readonly")
		if err != nil && zfsFilesystemEntityExists(poolName, fmt.Sprintf("%s@readonly", fs)) {
			return err
		}

		if removable {
			err := zfsPoolVolumeDestroy(poolName, fs)
			if err != nil {
				return err
			}
		} else {
			if err := zfsPoolVolumeSet(poolName, fs, "mountpoint", "none"); err != nil {
				return err
			}

			if err := zfsPoolVolumeRename(poolName, fs, fmt.Sprintf("deleted/%s", fs), true); err != nil {
				return err
			}
		}
	}

	err := s.deleteImageDbPoolVolume(fingerprint)
	if err != nil {
		return err
	}

	imageMntPoint := driver.GetImageMountPoint(s.pool.Name, fingerprint)
	if shared.PathExists(imageMntPoint) {
		err := os.RemoveAll(imageMntPoint)
		if err != nil {
			return err
		}
	}

	if shared.PathExists(shared.VarPath(fs + ".zfs")) {
		err := os.RemoveAll(shared.VarPath(fs + ".zfs"))
		if err != nil {
			return err
		}
	}

	logger.Debugf("Deleted ZFS storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
	return nil
}

func (s *storageZfs) ImageMount(fingerprint string) (bool, error) {
	return true, nil
}

func (s *storageZfs) ImageUmount(fingerprint string) (bool, error) {
	return true, nil
}

func (s *storageZfs) MigrationType() migration.MigrationFSType {
	return migration.MigrationFSType_ZFS
}

func (s *storageZfs) PreservesInodes() bool {
	return true
}

func (s *storageZfs) MigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	/* If the container is a snapshot, let's just send that; we don't need
	* to send anything else, because that's all the user asked for.
	 */
	if args.Instance.IsSnapshot() {
		return &zfsMigrationSourceDriver{instance: args.Instance, zfs: s, zfsFeatures: args.ZfsFeatures}, nil
	}

	driver := zfsMigrationSourceDriver{
		instance:         args.Instance,
		snapshots:        []Instance{},
		zfsSnapshotNames: []string{},
		zfs:              s,
		zfsFeatures:      args.ZfsFeatures,
	}

	if args.InstanceOnly {
		return &driver, nil
	}

	/* List all the snapshots in order of reverse creation. The idea here
	* is that we send the oldest to newest snapshot, hopefully saving on
	* xfer costs. Then, after all that, we send the container itself.
	 */
	snapshots, err := zfsPoolListSnapshots(s.getOnDiskPoolName(), fmt.Sprintf("containers/%s", project.Prefix(args.Instance.Project(), args.Instance.Name())))
	if err != nil {
		return nil, err
	}

	for _, snap := range snapshots {
		/* In the case of e.g. multiple copies running at the same
		* time, we will have potentially multiple migration-send
		* snapshots. (Or in the case of the test suite, sometimes one
		* will take too long to delete.)
		 */
		if !strings.HasPrefix(snap, "snapshot-") {
			continue
		}

		lxdName := fmt.Sprintf("%s%s%s", args.Instance.Name(), shared.SnapshotDelimiter, snap[len("snapshot-"):])
		snapshot, err := instanceLoadByProjectAndName(s.s, args.Instance.Project(), lxdName)
		if err != nil {
			return nil, err
		}

		driver.snapshots = append(driver.snapshots, snapshot)
		driver.zfsSnapshotNames = append(driver.zfsSnapshotNames, snap)
	}

	return &driver, nil
}

func (s *storageZfs) MigrationSink(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error {
	poolName := s.getOnDiskPoolName()
	zfsName := fmt.Sprintf("containers/%s", project.Prefix(args.Instance.Project(), args.Instance.Name()))
	zfsRecv := func(zfsName string, writeWrapper func(io.WriteCloser) io.WriteCloser) error {
		zfsFsName := fmt.Sprintf("%s/%s", poolName, zfsName)
		args := []string{"receive", "-F", "-o", "canmount=noauto", "-o", "mountpoint=none", "-u", zfsFsName}
		cmd := exec.Command("zfs", args...)

		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			return err
		}

		if err := cmd.Start(); err != nil {
			return err
		}

		writePipe := io.WriteCloser(stdin)
		if writeWrapper != nil {
			writePipe = writeWrapper(stdin)
		}

		<-shared.WebsocketRecvStream(writePipe, conn)

		output, err := ioutil.ReadAll(stderr)
		if err != nil {
			logger.Debugf("Problem reading zfs recv stderr %s", err)
		}

		err = cmd.Wait()
		if err != nil {
			logger.Errorf("Problem with zfs recv: %s", string(output))
		}
		return err
	}

	// Destroy the pre-existing (empty) dataset, this avoids issues with encryption
	err := zfsPoolVolumeDestroy(poolName, zfsName)
	if err != nil {
		return err
	}

	if len(args.Snapshots) > 0 {
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", project.Prefix(args.Instance.Project(), s.volume.Name))
		snapshotMntPointSymlink := shared.VarPath("snapshots", project.Prefix(args.Instance.Project(), args.Instance.Name()))
		if !shared.PathExists(snapshotMntPointSymlink) {
			err := os.Symlink(snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
			if err != nil {
				return err
			}
		}
	}

	// At this point we have already figured out the parent
	// container's root disk device so we can simply
	// retrieve it from the expanded devices.
	parentStoragePool := ""
	parentExpandedDevices := args.Instance.ExpandedDevices()
	parentLocalRootDiskDeviceKey, parentLocalRootDiskDevice, _ := shared.GetRootDiskDevice(parentExpandedDevices.CloneNative())
	if parentLocalRootDiskDeviceKey != "" {
		parentStoragePool = parentLocalRootDiskDevice["pool"]
	}

	// A little neuroticism.
	if parentStoragePool == "" {
		return fmt.Errorf("detected that the container's root device is missing the pool property during BTRFS migration")
	}

	for _, snap := range args.Snapshots {
		ctArgs := snapshotProtobufToInstanceArgs(args.Instance.Project(), args.Instance.Name(), snap)

		// Ensure that snapshot and parent container have the
		// same storage pool in their local root disk device.
		// If the root disk device for the snapshot comes from a
		// profile on the new instance as well we don't need to
		// do anything.
		if ctArgs.Devices != nil {
			snapLocalRootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(ctArgs.Devices.CloneNative())
			if snapLocalRootDiskDeviceKey != "" {
				ctArgs.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
			}
		}
		_, err := containerCreateEmptySnapshot(args.Instance.DaemonState(), ctArgs)
		if err != nil {
			return err
		}

		wrapper := migration.ProgressWriter(op, "fs_progress", snap.GetName())
		name := fmt.Sprintf("containers/%s@snapshot-%s", project.Prefix(args.Instance.Project(), args.Instance.Name()), snap.GetName())
		if err := zfsRecv(name, wrapper); err != nil {
			return err
		}

		snapshotMntPoint := driver.GetSnapshotMountPoint(args.Instance.Project(), poolName, fmt.Sprintf("%s/%s", args.Instance.Name(), *snap.Name))
		if !shared.PathExists(snapshotMntPoint) {
			err := os.MkdirAll(snapshotMntPoint, 0100)
			if err != nil {
				return err
			}
		}
	}

	defer func() {
		/* clean up our migration-send snapshots that we got from recv. */
		zfsSnapshots, err := zfsPoolListSnapshots(poolName, fmt.Sprintf("containers/%s", project.Prefix(args.Instance.Project(), args.Instance.Name())))
		if err != nil {
			logger.Errorf("Failed listing snapshots post migration: %s", err)
			return
		}

		for _, snap := range zfsSnapshots {
			// If we received a bunch of snapshots, remove the migration-send-* ones, if not, wipe any snapshot we got
			if args.Snapshots != nil && len(args.Snapshots) > 0 && !strings.HasPrefix(snap, "migration-send") {
				continue
			}

			zfsPoolVolumeSnapshotDestroy(poolName, fmt.Sprintf("containers/%s", project.Prefix(args.Instance.Project(), args.Instance.Name())), snap)
		}
	}()

	/* finally, do the real container */
	wrapper := migration.ProgressWriter(op, "fs_progress", args.Instance.Name())
	if err := zfsRecv(zfsName, wrapper); err != nil {
		return err
	}

	if args.Live {
		/* and again for the post-running snapshot if this was a live migration */
		wrapper := migration.ProgressWriter(op, "fs_progress", args.Instance.Name())
		if err := zfsRecv(zfsName, wrapper); err != nil {
			return err
		}
	}

	/* Sometimes, zfs recv mounts this anyway, even if we pass -u
	 * (https://forums.freebsd.org/threads/zfs-receive-u-shouldnt-mount-received-filesystem-right.36844/)
	 * but sometimes it doesn't. Let's try to mount, but not complain about
	 * failure.
	 */
	zfsMount(poolName, zfsName)
	return nil
}

func (s *storageZfs) StorageEntitySetQuota(volumeType int, size int64, data interface{}) error {
	logger.Debugf(`Setting ZFS quota for "%s"`, s.volume.Name)

	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return fmt.Errorf("Invalid storage type")
	}

	var c container
	var fs string
	switch volumeType {
	case storagePoolVolumeTypeContainer:
		c = data.(container)
		fs = fmt.Sprintf("containers/%s", project.Prefix(c.Project(), c.Name()))
	case storagePoolVolumeTypeCustom:
		fs = fmt.Sprintf("custom/%s", s.volume.Name)
	}

	property := "quota"

	if s.pool.Config["volume.zfs.use_refquota"] != "" {
		zfsUseRefquota = s.pool.Config["volume.zfs.use_refquota"]
	}
	if s.volume.Config["zfs.use_refquota"] != "" {
		zfsUseRefquota = s.volume.Config["zfs.use_refquota"]
	}

	if shared.IsTrue(zfsUseRefquota) {
		property = "refquota"
	}

	poolName := s.getOnDiskPoolName()
	var err error
	if size > 0 {
		err = zfsPoolVolumeSet(poolName, fs, property, fmt.Sprintf("%d", size))
	} else {
		err = zfsPoolVolumeSet(poolName, fs, property, "none")
	}

	if err != nil {
		return err
	}

	logger.Debugf(`Set ZFS quota for "%s"`, s.volume.Name)
	return nil
}

func (s *storageZfs) StoragePoolResources() (*api.ResourcesStoragePool, error) {
	poolName := s.getOnDiskPoolName()

	totalBuf, err := zfsFilesystemEntityPropertyGet(poolName, "", "available")
	if err != nil {
		return nil, err
	}

	totalStr := string(totalBuf)
	totalStr = strings.TrimSpace(totalStr)
	total, err := strconv.ParseUint(totalStr, 10, 64)
	if err != nil {
		return nil, err
	}

	usedBuf, err := zfsFilesystemEntityPropertyGet(poolName, "", "used")
	if err != nil {
		return nil, err
	}

	usedStr := string(usedBuf)
	usedStr = strings.TrimSpace(usedStr)
	used, err := strconv.ParseUint(usedStr, 10, 64)
	if err != nil {
		return nil, err
	}

	res := api.ResourcesStoragePool{}
	res.Space.Total = total
	res.Space.Used = used

	// Inode allocation is dynamic so no use in reporting them.

	return &res, nil
}

func (s *storageZfs) doCrossPoolStorageVolumeCopy(source *api.StorageVolumeSource) error {
	successMsg := fmt.Sprintf("Copied ZFS storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)
	// setup storage for the source volume
	srcStorage, err := storagePoolVolumeInit(s.s, "default", source.Pool, source.Name, storagePoolVolumeTypeCustom)
	if err != nil {
		logger.Errorf("Failed to initialize ZFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	ourMount, err := srcStorage.StoragePoolMount()
	if err != nil {
		return err
	}
	if ourMount {
		defer srcStorage.StoragePoolUmount()
	}

	// Create the main volume
	err = s.StoragePoolVolumeCreate()
	if err != nil {
		logger.Errorf("Failed to create ZFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	ourMount, err = s.StoragePoolVolumeMount()
	if err != nil {
		logger.Errorf("Failed to mount ZFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}
	if ourMount {
		defer s.StoragePoolVolumeUmount()
	}

	dstMountPoint := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	bwlimit := s.pool.Config["rsync.bwlimit"]

	snapshots, err := driver.VolumeSnapshotsGet(s.s, source.Pool, source.Name, storagePoolVolumeTypeCustom)
	if err != nil {
		return err
	}

	if !source.VolumeOnly {
		for _, snap := range snapshots {
			srcMountPoint := driver.GetStoragePoolVolumeSnapshotMountPoint(source.Pool, snap.Name)

			_, err = rsync.LocalCopy(srcMountPoint, dstMountPoint, bwlimit, true)
			if err != nil {
				logger.Errorf("Failed to rsync into ZFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
				return err
			}

			_, snapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(source.Name)

			s.StoragePoolVolumeSnapshotCreate(&api.StorageVolumeSnapshotsPost{Name: fmt.Sprintf("%s/%s", s.volume.Name, snapOnlyName)})
		}
	}

	var srcMountPoint string

	if strings.Contains(source.Name, "/") {
		srcMountPoint = driver.GetStoragePoolVolumeSnapshotMountPoint(source.Pool, source.Name)
	} else {
		srcMountPoint = driver.GetStoragePoolVolumeMountPoint(source.Pool, source.Name)
	}

	_, err = rsync.LocalCopy(srcMountPoint, dstMountPoint, bwlimit, true)
	if err != nil {
		os.RemoveAll(dstMountPoint)
		logger.Errorf("Failed to rsync into ZFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	logger.Infof(successMsg)
	return nil
}

func (s *storageZfs) copyVolumeWithoutSnapshotsFull(source *api.StorageVolumeSource) error {
	sourceIsSnapshot := shared.IsSnapshot(source.Name)

	var snapshotSuffix string
	var sourceDataset string
	var targetDataset string
	var targetSnapshotDataset string

	poolName := s.getOnDiskPoolName()

	if sourceIsSnapshot {
		sourceVolumeName, sourceSnapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(source.Name)
		snapshotSuffix = fmt.Sprintf("snapshot-%s", sourceSnapOnlyName)
		sourceDataset = fmt.Sprintf("%s/custom/%s@%s", source.Pool, sourceVolumeName, snapshotSuffix)
		targetSnapshotDataset = fmt.Sprintf("%s/custom/%s@snapshot-%s", poolName, s.volume.Name, sourceSnapOnlyName)
	} else {
		snapshotSuffix = uuid.NewRandom().String()
		sourceDataset = fmt.Sprintf("%s/custom/%s@%s", poolName, source.Name, snapshotSuffix)
		targetSnapshotDataset = fmt.Sprintf("%s/custom/%s@%s", poolName, s.volume.Name, snapshotSuffix)

		fs := fmt.Sprintf("custom/%s", source.Name)
		err := zfsPoolVolumeSnapshotCreate(poolName, fs, snapshotSuffix)
		if err != nil {
			return err
		}
		defer func() {
			err := zfsPoolVolumeSnapshotDestroy(poolName, fs, snapshotSuffix)
			if err != nil {
				logger.Warnf("Failed to delete temporary ZFS snapshot \"%s\", manual cleanup needed", sourceDataset)
			}
		}()
	}

	zfsSendCmd := exec.Command("zfs", "send", sourceDataset)

	zfsRecvCmd := exec.Command("zfs", "receive", targetDataset)

	zfsRecvCmd.Stdin, _ = zfsSendCmd.StdoutPipe()
	zfsRecvCmd.Stdout = os.Stdout
	zfsRecvCmd.Stderr = os.Stderr

	err := zfsRecvCmd.Start()
	if err != nil {
		return err
	}

	err = zfsSendCmd.Run()
	if err != nil {
		return err
	}

	err = zfsRecvCmd.Wait()
	if err != nil {
		return err
	}

	msg, err := shared.RunCommand("zfs", "rollback", "-r", "-R", targetSnapshotDataset)
	if err != nil {
		logger.Errorf("Failed to rollback ZFS dataset: %s", msg)
		return err
	}

	targetContainerMountPoint := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	targetfs := fmt.Sprintf("custom/%s", s.volume.Name)

	err = zfsPoolVolumeSet(poolName, targetfs, "canmount", "noauto")
	if err != nil {
		return err
	}

	err = zfsPoolVolumeSet(poolName, targetfs, "mountpoint", targetContainerMountPoint)
	if err != nil {
		return err
	}

	err = zfsPoolVolumeSnapshotDestroy(poolName, targetfs, snapshotSuffix)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) copyVolumeWithoutSnapshotsSparse(source *api.StorageVolumeSource) error {
	poolName := s.getOnDiskPoolName()

	sourceVolumeName := source.Name
	sourceVolumePath := driver.GetStoragePoolVolumeMountPoint(source.Pool, source.Name)

	targetVolumeName := s.volume.Name
	targetVolumePath := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	sourceZfsDataset := ""
	sourceZfsDatasetSnapshot := ""
	sourceName, sourceSnapOnlyName, isSnapshotName := shared.ContainerGetParentAndSnapshotName(sourceVolumeName)

	targetZfsDataset := fmt.Sprintf("custom/%s", targetVolumeName)

	if isSnapshotName {
		sourceZfsDatasetSnapshot = sourceSnapOnlyName
	}

	revert := true
	if sourceZfsDatasetSnapshot == "" {
		if zfsFilesystemEntityExists(poolName, fmt.Sprintf("custom/%s", sourceName)) {
			sourceZfsDatasetSnapshot = fmt.Sprintf("copy-%s", uuid.NewRandom().String())
			sourceZfsDataset = fmt.Sprintf("custom/%s", sourceName)

			err := zfsPoolVolumeSnapshotCreate(poolName, sourceZfsDataset, sourceZfsDatasetSnapshot)
			if err != nil {
				return err
			}

			defer func() {
				if !revert {
					return
				}
				zfsPoolVolumeSnapshotDestroy(poolName, sourceZfsDataset, sourceZfsDatasetSnapshot)
			}()
		}
	} else {
		if zfsFilesystemEntityExists(poolName, fmt.Sprintf("custom/%s@snapshot-%s", sourceName, sourceZfsDatasetSnapshot)) {
			sourceZfsDataset = fmt.Sprintf("custom/%s", sourceName)
			sourceZfsDatasetSnapshot = fmt.Sprintf("snapshot-%s", sourceZfsDatasetSnapshot)
		}
	}

	if sourceZfsDataset != "" {
		err := zfsPoolVolumeClone("default", poolName, sourceZfsDataset, sourceZfsDatasetSnapshot, targetZfsDataset, targetVolumePath)
		if err != nil {
			return err
		}

		defer func() {
			if !revert {
				return
			}
			zfsPoolVolumeDestroy(poolName, targetZfsDataset)
		}()
	} else {
		bwlimit := s.pool.Config["rsync.bwlimit"]

		output, err := rsync.LocalCopy(sourceVolumePath, targetVolumePath, bwlimit, true)
		if err != nil {
			return fmt.Errorf("rsync failed: %s", string(output))
		}
	}

	revert = false

	return nil
}

func (s *storageZfs) StoragePoolVolumeCopy(source *api.StorageVolumeSource) error {
	logger.Infof("Copying ZFS storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)
	successMsg := fmt.Sprintf("Copied ZFS storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)

	if source.Pool != s.pool.Name {
		return s.doCrossPoolStorageVolumeCopy(source)
	}

	var snapshots []string

	poolName := s.getOnDiskPoolName()

	if !shared.IsSnapshot(source.Name) {
		allSnapshots, err := zfsPoolListSnapshots(poolName, fmt.Sprintf("custom/%s", source.Name))
		if err != nil {
			return err
		}

		for _, snap := range allSnapshots {
			if strings.HasPrefix(snap, "snapshot-") {
				snapshots = append(snapshots, strings.TrimPrefix(snap, "snapshot-"))
			}
		}
	}

	targetStorageVolumeMountPoint := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	fs := fmt.Sprintf("custom/%s", s.volume.Name)

	if source.VolumeOnly || len(snapshots) == 0 {
		var err error

		if s.pool.Config["zfs.clone_copy"] != "" && !shared.IsTrue(s.pool.Config["zfs.clone_copy"]) {
			err = s.copyVolumeWithoutSnapshotsFull(source)
		} else {
			err = s.copyVolumeWithoutSnapshotsSparse(source)
		}
		if err != nil {
			return err
		}
	} else {
		targetVolumeMountPoint := driver.GetStoragePoolVolumeMountPoint(poolName, s.volume.Name)

		err := os.MkdirAll(targetVolumeMountPoint, 0711)
		if err != nil {
			return err
		}

		prev := ""
		prevSnapOnlyName := ""

		for i, snap := range snapshots {
			if i > 0 {
				prev = snapshots[i-1]
			}

			sourceDataset := fmt.Sprintf("%s/custom/%s@snapshot-%s", poolName, source.Name, snap)
			targetDataset := fmt.Sprintf("%s/custom/%s@snapshot-%s", poolName, s.volume.Name, snap)

			snapshotMntPoint := driver.GetStoragePoolVolumeSnapshotMountPoint(poolName, fmt.Sprintf("%s/%s", s.volume.Name, snap))

			err := os.MkdirAll(snapshotMntPoint, 0700)
			if err != nil {
				return err
			}

			prevSnapOnlyName = snap

			args := []string{"send", sourceDataset}

			if prev != "" {
				parentDataset := fmt.Sprintf("%s/custom/%s/snapshot-%s", poolName, source.Name, prev)
				args = append(args, "-i", parentDataset)
			}

			zfsSendCmd := exec.Command("zfs", args...)
			zfsRecvCmd := exec.Command("zfs", "receive", "-F", targetDataset)

			zfsRecvCmd.Stdin, _ = zfsSendCmd.StdoutPipe()
			zfsRecvCmd.Stdout = os.Stdout
			zfsRecvCmd.Stderr = os.Stderr

			err = zfsRecvCmd.Start()
			if err != nil {
				return err
			}

			err = zfsSendCmd.Run()
			if err != nil {
				return err
			}

			err = zfsRecvCmd.Wait()
			if err != nil {
				return err
			}
		}

		tmpSnapshotName := fmt.Sprintf("copy-send-%s", uuid.NewRandom().String())

		err = zfsPoolVolumeSnapshotCreate(poolName, fmt.Sprintf("custom/%s", source.Name), tmpSnapshotName)
		if err != nil {
			return err
		}

		defer zfsPoolVolumeSnapshotDestroy(poolName, fmt.Sprintf("custom/%s", source.Name), tmpSnapshotName)

		currentSnapshotDataset := fmt.Sprintf("%s/custom/%s@%s", poolName, source.Name, tmpSnapshotName)

		args := []string{"send", currentSnapshotDataset}

		if prevSnapOnlyName != "" {
			args = append(args, "-i", fmt.Sprintf("%s/custom/%s@snapshot-%s", poolName, source.Name, prevSnapOnlyName))
		}

		zfsSendCmd := exec.Command("zfs", args...)
		targetDataset := fmt.Sprintf("%s/custom/%s", poolName, s.volume.Name)
		zfsRecvCmd := exec.Command("zfs", "receive", "-F", targetDataset)

		zfsRecvCmd.Stdin, _ = zfsSendCmd.StdoutPipe()
		zfsRecvCmd.Stdout = os.Stdout
		zfsRecvCmd.Stderr = os.Stderr

		err = zfsRecvCmd.Start()
		if err != nil {
			return err
		}

		err = zfsSendCmd.Run()
		if err != nil {
			return err
		}

		err = zfsRecvCmd.Wait()
		if err != nil {
			return err
		}

		defer zfsPoolVolumeSnapshotDestroy(poolName, fmt.Sprintf("custom/%s", s.volume.Name), tmpSnapshotName)

		err = zfsPoolVolumeSet(poolName, fs, "canmount", "noauto")
		if err != nil {
			return err
		}

		err = zfsPoolVolumeSet(poolName, fs, "mountpoint", targetStorageVolumeMountPoint)
		if err != nil {
			return err
		}
	}

	if !shared.IsMountPoint(targetStorageVolumeMountPoint) {
		err := zfsMount(poolName, fs)
		if err != nil {
			return err
		}
		defer zfsUmount(poolName, fs, targetStorageVolumeMountPoint)
	}

	// apply quota
	if s.volume.Config["size"] != "" {
		size, err := units.ParseByteSizeString(s.volume.Config["size"])
		if err != nil {
			return err
		}

		err = s.StorageEntitySetQuota(storagePoolVolumeTypeCustom, size, nil)
		if err != nil {
			return err
		}
	}

	logger.Infof(successMsg)
	return nil
}

func (s *storageZfs) StorageMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	return rsyncStorageMigrationSource(args)
}

func (s *storageZfs) StorageMigrationSink(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error {
	return rsyncStorageMigrationSink(conn, op, args)
}

func (s *storageZfs) StoragePoolVolumeSnapshotCreate(target *api.StorageVolumeSnapshotsPost) error {
	logger.Infof("Creating ZFS storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	sourceOnlyName, snapshotOnlyName, ok := shared.ContainerGetParentAndSnapshotName(target.Name)
	if !ok {
		return fmt.Errorf("Not a snapshot name")
	}

	sourceDataset := fmt.Sprintf("custom/%s", sourceOnlyName)
	poolName := s.getOnDiskPoolName()
	snapName := fmt.Sprintf("snapshot-%s", snapshotOnlyName)
	err := zfsPoolVolumeSnapshotCreate(poolName, sourceDataset, snapName)
	if err != nil {
		return err
	}

	snapshotMntPoint := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, target.Name)
	if !shared.PathExists(snapshotMntPoint) {
		err := os.MkdirAll(snapshotMntPoint, 0700)
		if err != nil {
			return err
		}
	}

	logger.Infof("Created ZFS storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) StoragePoolVolumeSnapshotDelete() error {
	logger.Infof("Deleting ZFS storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	sourceName, snapshotOnlyName, _ := shared.ContainerGetParentAndSnapshotName(s.volume.Name)
	snapshotName := fmt.Sprintf("snapshot-%s", snapshotOnlyName)

	onDiskPoolName := s.getOnDiskPoolName()
	if zfsFilesystemEntityExists(onDiskPoolName, fmt.Sprintf("custom/%s@%s", sourceName, snapshotName)) {
		removable, err := zfsPoolVolumeSnapshotRemovable(onDiskPoolName, fmt.Sprintf("custom/%s", sourceName), snapshotName)
		if err != nil {
			return err
		}

		if removable {
			err = zfsPoolVolumeSnapshotDestroy(onDiskPoolName, fmt.Sprintf("custom/%s", sourceName), snapshotName)
		} else {
			err = zfsPoolVolumeSnapshotRename(onDiskPoolName, fmt.Sprintf("custom/%s", sourceName), snapshotName, fmt.Sprintf("copy-%s", uuid.NewRandom().String()))
		}
		if err != nil {
			return err
		}
	}

	storageVolumePath := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	err := os.RemoveAll(storageVolumePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	storageVolumeSnapshotPath := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, sourceName)
	empty, err := shared.PathIsEmpty(storageVolumeSnapshotPath)
	if err == nil && empty {
		os.RemoveAll(storageVolumeSnapshotPath)
	}

	err = s.s.Cluster.StoragePoolVolumeDelete(
		"default",
		s.volume.Name,
		storagePoolVolumeTypeCustom,
		s.poolID)
	if err != nil {
		logger.Errorf(`Failed to delete database entry for DIR storage volume "%s" on storage pool "%s"`,
			s.volume.Name, s.pool.Name)
	}

	logger.Infof("Deleted ZFS storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) StoragePoolVolumeSnapshotRename(newName string) error {
	sourceName, snapshotOnlyName, ok := shared.ContainerGetParentAndSnapshotName(s.volume.Name)
	fullSnapshotName := fmt.Sprintf("%s%s%s", sourceName, shared.SnapshotDelimiter, newName)

	logger.Infof("Renaming ZFS storage volume snapshot on storage pool \"%s\" from \"%s\" to \"%s\"", s.pool.Name, s.volume.Name, fullSnapshotName)

	if !ok {
		return fmt.Errorf("Not a snapshot name")
	}

	oldZfsDatasetName := fmt.Sprintf("snapshot-%s", snapshotOnlyName)
	newZfsDatasetName := fmt.Sprintf("snapshot-%s", newName)
	err := zfsPoolVolumeSnapshotRename(s.getOnDiskPoolName(), fmt.Sprintf("custom/%s", sourceName), oldZfsDatasetName, newZfsDatasetName)
	if err != nil {
		return err
	}

	logger.Infof("Renamed ZFS storage volume snapshot on storage pool \"%s\" from \"%s\" to \"%s\"", s.pool.Name, s.volume.Name, fullSnapshotName)

	return s.s.Cluster.StoragePoolVolumeRename("default", s.volume.Name, fmt.Sprintf("%s/%s", sourceName, newName), storagePoolVolumeTypeCustom, s.poolID)
}
