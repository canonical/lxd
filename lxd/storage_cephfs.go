package main

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/state"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/units"
)

type storageCephFs struct {
	ClusterName string
	FsName      string
	UserName    string
	storageShared
}

func (s *storageCephFs) StorageCoreInit() error {
	s.sType = storageTypeCeph
	typeName, err := storageTypeToString(s.sType)
	if err != nil {
		return err
	}
	s.sTypeName = typeName

	if cephVersion != "" {
		s.sTypeVersion = cephVersion
		return nil
	}

	msg, err := shared.RunCommand("rbd", "--version")
	if err != nil {
		return fmt.Errorf("Error getting CEPH version: %s", err)
	}
	s.sTypeVersion = strings.TrimSpace(msg)
	cephVersion = s.sTypeVersion

	return nil
}

func (s *storageCephFs) StoragePoolInit() error {
	var err error

	err = s.StorageCoreInit()
	if err != nil {
		return errors.Wrap(err, "Storage pool init")
	}

	// set cluster name
	if s.pool.Config["cephfs.cluster_name"] != "" {
		s.ClusterName = s.pool.Config["cephfs.cluster_name"]
	} else {
		s.ClusterName = "ceph"
	}

	// set ceph user name
	if s.pool.Config["cephfs.user.name"] != "" {
		s.UserName = s.pool.Config["cephfs.user.name"]
	} else {
		s.UserName = "admin"
	}

	// set osd pool name
	if s.pool.Config["cephfs.path"] != "" {
		s.FsName = s.pool.Config["cephfs.path"]
	}

	return nil
}

func (s *storageCephFs) StoragePoolCheck() error {
	return nil
}

func (s *storageCephFs) StoragePoolCreate() error {
	logger.Infof(`Creating CEPHFS storage pool "%s" in cluster "%s"`, s.pool.Name, s.ClusterName)

	// Setup config
	s.pool.Config["volatile.initial_source"] = s.pool.Config["source"]

	if s.pool.Config["source"] == "" {
		return fmt.Errorf("A CEPHFS name or name/path source is required")
	}

	if s.pool.Config["cephfs.path"] != "" && s.pool.Config["cephfs.path"] != s.pool.Config["source"] {
		return fmt.Errorf("cephfs.path must match the source")
	}

	if s.pool.Config["cephfs.cluster_name"] == "" {
		s.pool.Config["cephfs.cluster_name"] = "ceph"
	}

	if s.pool.Config["cephfs.user.name"] != "" {
		s.pool.Config["cephfs.user.name"] = "admin"
	}

	s.pool.Config["cephfs.path"] = s.pool.Config["source"]
	s.FsName = s.pool.Config["source"]

	// Parse the namespace / path
	fields := strings.SplitN(s.FsName, "/", 2)
	fsName := fields[0]
	fsPath := "/"
	if len(fields) > 1 {
		fsPath = fields[1]
	}

	// Check that the filesystem exists
	if !cephFsExists(s.ClusterName, s.UserName, fsName) {
		return fmt.Errorf("The requested '%v' CEPHFS doesn't exist", fsName)
	}

	// Create a temporary mountpoint
	mountPath, err := ioutil.TempDir("", "lxd_cephfs_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mountPath)

	err = os.Chmod(mountPath, 0700)
	if err != nil {
		return err
	}

	mountPoint := filepath.Join(mountPath, "mount")
	err = os.Mkdir(mountPoint, 0700)
	if err != nil {
		return err
	}

	// Get the credentials and host
	monAddresses, userSecret, err := cephFsConfig(s.ClusterName, s.UserName)
	if err != nil {
		return err
	}

	connected := false
	for _, monAddress := range monAddresses {
		uri := fmt.Sprintf("%s:6789:/", monAddress)
		err = driver.TryMount(uri, mountPoint, "ceph", 0, fmt.Sprintf("name=%v,secret=%v,mds_namespace=%v", s.UserName, userSecret, fsName))
		if err != nil {
			continue
		}

		connected = true
		defer driver.TryUnmount(mountPoint, syscall.MNT_DETACH)
		break
	}

	if !connected {
		return err
	}

	// Create the path if missing
	err = os.MkdirAll(filepath.Join(mountPoint, fsPath), 0755)
	if err != nil {
		return err
	}

	// Check that the existing path is empty
	ok, _ := shared.PathIsEmpty(filepath.Join(mountPoint, fsPath))
	if !ok {
		return fmt.Errorf("Only empty CEPHFS paths can be used as a LXD storage pool")
	}

	// Create the mountpoint for the storage pool.
	poolMntPoint := driver.GetStoragePoolMountPoint(s.pool.Name)
	err = os.MkdirAll(poolMntPoint, 0711)
	if err != nil {
		return err
	}

	logger.Infof(`Created CEPHFS storage pool "%s" in cluster "%s"`, s.pool.Name, s.ClusterName)

	return nil
}

func (s *storageCephFs) StoragePoolDelete() error {
	logger.Infof(`Deleting CEPHFS storage pool "%s" in cluster "%s"`, s.pool.Name, s.ClusterName)

	// Parse the namespace / path
	fields := strings.SplitN(s.FsName, "/", 2)
	fsName := fields[0]
	fsPath := "/"
	if len(fields) > 1 {
		fsPath = fields[1]
	}

	// Create a temporary mountpoint
	mountPath, err := ioutil.TempDir("", "lxd_cephfs_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mountPath)

	err = os.Chmod(mountPath, 0700)
	if err != nil {
		return err
	}

	mountPoint := filepath.Join(mountPath, "mount")
	err = os.Mkdir(mountPoint, 0700)
	if err != nil {
		return err
	}

	// Get the credentials and host
	monAddresses, userSecret, err := cephFsConfig(s.ClusterName, s.UserName)
	if err != nil {
		return err
	}

	connected := false
	for _, monAddress := range monAddresses {
		uri := fmt.Sprintf("%s:6789:/", monAddress)
		err = driver.TryMount(uri, mountPoint, "ceph", 0, fmt.Sprintf("name=%v,secret=%v,mds_namespace=%v", s.UserName, userSecret, fsName))
		if err != nil {
			continue
		}

		connected = true
		defer driver.TryUnmount(mountPoint, syscall.MNT_DETACH)
		break
	}

	if !connected {
		return err
	}

	if shared.PathExists(filepath.Join(mountPoint, fsPath)) {
		// Delete the usual directories
		for _, dir := range []string{"custom", "custom-snapshots"} {
			if shared.PathExists(filepath.Join(mountPoint, fsPath, dir)) {
				err = os.Remove(filepath.Join(mountPoint, fsPath, dir))
				if err != nil {
					return err
				}
			}
		}

		// Confirm that the path is now empty
		ok, _ := shared.PathIsEmpty(filepath.Join(mountPoint, fsPath))
		if !ok {
			return fmt.Errorf("Only empty CEPHFS paths can be used as a LXD storage pool")
		}

		// Delete the path itself
		if fsPath != "" && fsPath != "/" {
			err = os.Remove(filepath.Join(mountPoint, fsPath))
			if err != nil {
				return err
			}
		}
	}

	// Make sure the existing pool is unmounted
	_, err = s.StoragePoolUmount()
	if err != nil {
		return err
	}

	// Delete the mountpoint for the storage pool
	poolMntPoint := driver.GetStoragePoolMountPoint(s.pool.Name)
	if shared.PathExists(poolMntPoint) {
		err := os.RemoveAll(poolMntPoint)
		if err != nil {
			return err
		}
		logger.Debugf(`Deleted mountpoint "%s" for CEPHFS storage pool "%s" in cluster "%s"`, poolMntPoint, s.FsName, s.ClusterName)
	}

	logger.Infof(`Deleted CEPHFS storage pool "%s" in cluster "%s"`, s.pool.Name, s.ClusterName)
	return nil
}

func (s *storageCephFs) StoragePoolMount() (bool, error) {
	logger.Debugf("Mounting CEPHFS storage pool \"%s\"", s.pool.Name)

	// Locking
	poolMountLockID := getPoolMountLockID(s.pool.Name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[poolMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore, this should not have happened")
		}
		// Give the benefit of the doubt and assume that the other
		// thread actually succeeded in mounting the storage pool.
		return false, nil
	}

	lxdStorageOngoingOperationMap[poolMountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	removeLockFromMap := func() {
		lxdStorageMapLock.Lock()
		if waitChannel, ok := lxdStorageOngoingOperationMap[poolMountLockID]; ok {
			close(waitChannel)
			delete(lxdStorageOngoingOperationMap, poolMountLockID)
		}
		lxdStorageMapLock.Unlock()
	}
	defer removeLockFromMap()

	// Check if already mounted
	poolMntPoint := driver.GetStoragePoolMountPoint(s.pool.Name)
	if shared.IsMountPoint(poolMntPoint) {
		return false, nil
	}

	// Parse the namespace / path
	fields := strings.SplitN(s.FsName, "/", 2)
	fsName := fields[0]
	fsPath := "/"
	if len(fields) > 1 {
		fsPath = fields[1]
	}

	// Get the credentials and host
	monAddresses, secret, err := cephFsConfig(s.ClusterName, s.UserName)
	if err != nil {
		return false, err
	}

	// Do the actual mount
	connected := false
	for _, monAddress := range monAddresses {
		uri := fmt.Sprintf("%s:6789:/%s", monAddress, fsPath)
		err = driver.TryMount(uri, poolMntPoint, "ceph", 0, fmt.Sprintf("name=%v,secret=%v,mds_namespace=%v", s.UserName, secret, fsName))
		if err != nil {
			continue
		}

		connected = true
		break
	}

	if !connected {
		return false, err
	}

	logger.Debugf("Mounted CEPHFS storage pool \"%s\"", s.pool.Name)

	return true, nil
}

func (s *storageCephFs) StoragePoolUmount() (bool, error) {
	logger.Debugf("Unmounting CEPHFS storage pool \"%s\"", s.pool.Name)

	// Locking
	poolUmountLockID := getPoolUmountLockID(s.pool.Name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[poolUmountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore, this should not have happened")
		}
		// Give the benefit of the doubt and assume that the other
		// thread actually succeeded in unmounting the storage pool.
		return false, nil
	}

	lxdStorageOngoingOperationMap[poolUmountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	removeLockFromMap := func() {
		lxdStorageMapLock.Lock()
		if waitChannel, ok := lxdStorageOngoingOperationMap[poolUmountLockID]; ok {
			close(waitChannel)
			delete(lxdStorageOngoingOperationMap, poolUmountLockID)
		}
		lxdStorageMapLock.Unlock()
	}

	defer removeLockFromMap()

	// Check if already unmounted
	poolMntPoint := driver.GetStoragePoolMountPoint(s.pool.Name)
	if !shared.IsMountPoint(poolMntPoint) {
		return false, nil
	}

	// Unmount
	err := driver.TryUnmount(poolMntPoint, syscall.MNT_DETACH)
	if err != nil {
		return false, err
	}

	logger.Debugf("Unmounted CEPHFS pool \"%s\"", s.pool.Name)
	return true, nil
}

func (s *storageCephFs) GetStoragePoolWritable() api.StoragePoolPut {
	return s.pool.Writable()
}

func (s *storageCephFs) GetStoragePoolVolumeWritable() api.StorageVolumePut {
	return s.volume.Writable()
}

func (s *storageCephFs) SetStoragePoolWritable(writable *api.StoragePoolPut) {
	s.pool.StoragePoolPut = *writable
}

func (s *storageCephFs) SetStoragePoolVolumeWritable(writable *api.StorageVolumePut) {
	s.volume.StorageVolumePut = *writable
}

func (s *storageCephFs) GetContainerPoolInfo() (int64, string, string) {
	return s.poolID, s.pool.Name, s.pool.Name
}

func (s *storageCephFs) StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error {
	logger.Infof(`Updating CEPHFS storage pool "%s"`, s.pool.Name)

	// Validate the properties
	changeable := changeableStoragePoolProperties["cephfs"]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		return updateStoragePoolError(unchangeable, "cephfs")
	}

	logger.Infof(`Updated CEPHFS storage pool "%s"`, s.pool.Name)
	return nil
}

// Functions dealing with storage pools.
func (s *storageCephFs) StoragePoolVolumeCreate() error {
	logger.Infof("Creating CEPHFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	// Make sure the pool is currently mounted
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Create the volume
	storageVolumePath := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	err = os.MkdirAll(storageVolumePath, 0711)
	if err != nil {
		return err
	}

	logger.Infof("Created CEPHFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageCephFs) StoragePoolVolumeDelete() error {
	logger.Infof("Deleting CEPHFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	// Make sure the pool is currently mounted
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Check if not gone already
	storageVolumePath := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	if !shared.PathExists(storageVolumePath) {
		return nil
	}

	// Delete the volume
	err = os.RemoveAll(storageVolumePath)
	if err != nil {
		return err
	}

	// Delete the snapshot directory
	err = os.Remove(driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Delete the database entry
	err = s.s.Cluster.StoragePoolVolumeDelete("default", s.volume.Name, storagePoolVolumeTypeCustom, s.poolID)
	if err != nil {
		return err
	}

	logger.Infof("Deleted CEPHFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageCephFs) StoragePoolVolumeMount() (bool, error) {
	// Make sure the pool is currently mounted
	_, err := s.StoragePoolMount()
	if err != nil {
		return true, err
	}

	return true, nil
}

func (s *storageCephFs) StoragePoolVolumeUmount() (bool, error) {
	return true, nil
}

func (s *storageCephFs) StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error {
	// Snapshot restores
	if writable.Restore != "" {
		logger.Infof(`Restoring CEPHFS storage volume "%s" from snapshot "%s"`, s.volume.Name, writable.Restore)

		// Make sure the pool is currently mounted
		_, err := s.StoragePoolMount()
		if err != nil {
			return err
		}

		targetPath := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
		sourcePath := filepath.Join(targetPath, ".snap", writable.Restore)

		// Restore using rsync
		bwlimit := s.pool.Config["rsync.bwlimit"]
		output, err := rsyncLocalCopy(sourcePath, targetPath, bwlimit, false)
		if err != nil {
			return fmt.Errorf("Failed to rsync container: %s: %s", string(output), err)
		}

		logger.Infof(`Restored CEPHFS storage volume "%s" from snapshot "%s"`, s.volume.Name, writable.Restore)
		return nil
	}

	// Config updates
	logger.Infof(`Updating CEPHFS storage volume "%s"`, s.volume.Name)

	// Validate the properties
	changeable := changeableStoragePoolVolumeProperties["cephfs"]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		return updateStoragePoolVolumeError(unchangeable, "cephfs")
	}

	// Handle setting quotas
	if shared.StringInSlice("size", changedConfig) {
		if s.volume.Type != storagePoolVolumeTypeNameCustom {
			return updateStoragePoolVolumeError([]string{"size"}, "cephfs")
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

	logger.Infof(`Updated CEPHFS storage volume "%s"`, s.volume.Name)
	return nil
}

func (s *storageCephFs) StoragePoolVolumeRename(newName string) error {
	logger.Infof(`Renaming CEPHFS storage volume on storage pool "%s" from "%s" to "%s`, s.pool.Name, s.volume.Name, newName)

	// Sanity check
	usedBy, err := storagePoolVolumeUsedByContainersGet(s.s, "default", s.pool.Name, s.volume.Name)
	if err != nil {
		return err
	}
	if len(usedBy) > 0 {
		return fmt.Errorf(`CEPHFS storage volume "%s" on storage pool "%s" is attached to containers`,
			s.volume.Name, s.pool.Name)
	}

	// Make sure the pool is currently mounted
	_, err = s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Rename the directory
	oldPath := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	newPath := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, newName)
	err = os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	// Update the database entry
	err = s.s.Cluster.StoragePoolVolumeRename("default", s.volume.Name, newName, storagePoolVolumeTypeCustom, s.poolID)
	if err != nil {
		return err
	}

	logger.Infof(`Renamed CEPHFS storage volume on storage pool "%s" from "%s" to "%s`, s.pool.Name, s.volume.Name, newName)
	return nil
}

func (s *storageCephFs) ContainerStorageReady(container container) bool {
	containerMntPoint := driver.GetContainerMountPoint(container.Project(), s.pool.Name, container.Name())
	ok, _ := shared.PathIsEmpty(containerMntPoint)
	return !ok
}

func (s *storageCephFs) ContainerCreate(container container) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerCreateFromImage(container container, imageFingerprint string, tracker *ioprogress.ProgressTracker) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerCanRestore(container container, sourceContainer container) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerDelete(container container) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerCopy(target container, source container, containerOnly bool) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerRefresh(target container, source container, snapshots []container) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerMount(c container) (bool, error) {
	return false, fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerUmount(c container, path string) (bool, error) {
	return false, fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerRename(container container, newName string) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerRestore(container container, sourceContainer container) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerGetUsage(c container) (int64, error) {
	return -1, fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerSnapshotDelete(snapshotContainer container) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerSnapshotRename(snapshotContainer container, newName string) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerSnapshotStart(container container) (bool, error) {
	return false, fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerSnapshotStop(container container) (bool, error) {
	return false, fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerBackupCreate(backup backup, source container) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ContainerBackupLoad(info backupInfo, data io.ReadSeeker, tarArgs []string) error {
	return fmt.Errorf("CEPHFS cannot be used for containers")
}

func (s *storageCephFs) ImageCreate(fingerprint string, tracker *ioprogress.ProgressTracker) error {
	return fmt.Errorf("CEPHFS cannot be used for images")
}

func (s *storageCephFs) ImageDelete(fingerprint string) error {
	return fmt.Errorf("CEPHFS cannot be used for images")
}

func (s *storageCephFs) ImageMount(fingerprint string) (bool, error) {
	return false, fmt.Errorf("CEPHFS cannot be used for images")
}

func (s *storageCephFs) ImageUmount(fingerprint string) (bool, error) {
	return false, fmt.Errorf("CEPHFS cannot be used for images")
}

func (s *storageCephFs) MigrationType() migration.MigrationFSType {
	return migration.MigrationFSType_RSYNC
}

func (s *storageCephFs) PreservesInodes() bool {
	return false
}

func (s *storageCephFs) MigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	return rsyncMigrationSource(args)
}

func (s *storageCephFs) MigrationSink(conn *websocket.Conn, op *operation, args MigrationSinkArgs) error {
	return rsyncMigrationSink(conn, op, args)
}

func (s *storageCephFs) StorageEntitySetQuota(volumeType int, size int64, data interface{}) error {
	// Make sure the pool is currently mounted
	_, err := s.StoragePoolMount()
	if err != nil {
		return nil
	}

	// Apply the limit
	storageVolumePath := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	_, err = shared.RunCommand("setfattr", "-n", "ceph.quota.max_bytes", "-v", fmt.Sprintf("%d", size), storageVolumePath)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageCephFs) StoragePoolResources() (*api.ResourcesStoragePool, error) {
	// Make sure the pool is currently mounted
	_, err := s.StoragePoolMount()
	if err != nil {
		return nil, err
	}

	poolMntPoint := driver.GetStoragePoolMountPoint(s.pool.Name)
	return driver.GetStorageResource(poolMntPoint)
}

func (s *storageCephFs) StoragePoolVolumeCopy(source *api.StorageVolumeSource) error {
	logger.Infof("Copying CEPHFS storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)

	// Make sure the pool is currently mounted
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Setup storage for the source volume
	if s.pool.Name != source.Pool {
		srcStorage, err := storagePoolVolumeInit(s.s, "default", source.Pool, source.Name, storagePoolVolumeTypeCustom)
		if err != nil {
			logger.Errorf("Failed to initialize CEPHFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
			return err
		}

		ourMount, err := srcStorage.StoragePoolMount()
		if err != nil {
			logger.Errorf("Failed to mount CEPHFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
			return err
		}
		if ourMount {
			defer srcStorage.StoragePoolUmount()
		}
	}

	// Create empty volume
	storageVolumePath := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	err = os.MkdirAll(storageVolumePath, 0711)
	if err != nil {
		return err
	}

	// Copy the snapshots
	if !source.VolumeOnly {
		snapshots, err := storagePoolVolumeSnapshotsGet(s.s, source.Pool, source.Name, storagePoolVolumeTypeCustom)
		if err != nil {
			return err
		}

		for _, snap := range snapshots {
			_, snapOnlyName, _ := containerGetParentAndSnapshotName(snap)
			err = s.copyVolume(source.Pool, snap, fmt.Sprintf("%s/%s", s.volume.Name, snapOnlyName))
			if err != nil {
				return err
			}
		}
	}

	// Copy the main volume
	err = s.copyVolume(source.Pool, source.Name, s.volume.Name)
	if err != nil {
		return err
	}

	logger.Infof("Copied CEPHFS storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageCephFs) StorageMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	return rsyncStorageMigrationSource(args)
}

func (s *storageCephFs) StorageMigrationSink(conn *websocket.Conn, op *operation, args MigrationSinkArgs) error {
	return rsyncStorageMigrationSink(conn, op, args)
}

func (s *storageCephFs) GetStoragePool() *api.StoragePool {
	return s.pool
}

func (s *storageCephFs) GetStoragePoolVolume() *api.StorageVolume {
	return s.volume
}

func (s *storageCephFs) GetState() *state.State {
	return s.s
}

func (s *storageCephFs) StoragePoolVolumeSnapshotCreate(target *api.StorageVolumeSnapshotsPost) error {
	logger.Infof("Creating CEPHFS storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	// Make sure the pool is currently mounted
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Parse the name
	sourceName, snapName, ok := containerGetParentAndSnapshotName(target.Name)
	if !ok {
		return fmt.Errorf("Not a snapshot name")
	}

	// Create the snapshot
	sourcePath := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, sourceName)
	cephSnapPath := filepath.Join(sourcePath, ".snap", snapName)

	err = os.Mkdir(cephSnapPath, 0711)
	if err != nil {
		return err
	}

	// Make the snapshot path a symlink
	targetPath := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, target.Name)
	err = os.MkdirAll(filepath.Dir(targetPath), 0711)
	if err != nil {
		return err
	}

	err = os.Symlink(cephSnapPath, targetPath)
	if err != nil {
		return err
	}

	logger.Infof("Created CEPHFS storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageCephFs) StoragePoolVolumeSnapshotDelete() error {
	logger.Infof("Deleting CEPHFS storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	// Make sure the pool is currently mounted
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Parse the name
	sourceName, snapName, ok := containerGetParentAndSnapshotName(s.volume.Name)
	if !ok {
		return fmt.Errorf("Not a snapshot name")
	}

	// Create the snapshot
	sourcePath := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, sourceName)
	cephSnapPath := filepath.Join(sourcePath, ".snap", snapName)

	err = os.Remove(cephSnapPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Make the snapshot path a symlink
	targetPath := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	err = os.Remove(targetPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Delete the database entry
	err = s.s.Cluster.StoragePoolVolumeDelete("default", s.volume.Name, storagePoolVolumeTypeCustom, s.poolID)
	if err != nil {
		logger.Errorf(`Failed to delete database entry for CEPHFS storage volume snapshot "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)
		return err
	}

	logger.Infof("Deleted CEPHFS storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageCephFs) StoragePoolVolumeSnapshotRename(newName string) error {
	logger.Infof("Renaming CEPHFS storage volume on storage pool \"%s\" from \"%s\" to \"%s\"", s.pool.Name, s.volume.Name, newName)

	// Make sure the pool is currently mounted
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Rename the snapshot entry
	sourceName, oldSnapName, _ := containerGetParentAndSnapshotName(s.volume.Name)
	sourcePath := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, sourceName)
	oldCephSnapPath := filepath.Join(sourcePath, ".snap", oldSnapName)
	newCephSnapPath := filepath.Join(sourcePath, ".snap", newName)

	err = os.Rename(oldCephSnapPath, newCephSnapPath)
	if err != nil {
		return err
	}

	// Re-generate the snapshot symlink
	oldPath := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	err = os.Remove(oldPath)
	if err != nil {
		return err
	}

	newPath := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, filepath.Join(sourceName, newName))
	err = os.Symlink(newCephSnapPath, newPath)
	if err != nil {
		return err
	}

	// Update the database record
	fullSnapshotName := fmt.Sprintf("%s%s%s", sourceName, shared.SnapshotDelimiter, newName)
	err = s.s.Cluster.StoragePoolVolumeRename("default", s.volume.Name, fullSnapshotName, storagePoolVolumeTypeCustom, s.poolID)
	if err != nil {
		return err
	}

	logger.Infof("Renamed CEPHFS storage volume on storage pool \"%s\" from \"%s\" to \"%s\"", s.pool.Name, s.volume.Name, newName)
	return nil
}

func (s *storageCephFs) copyVolume(sourcePool string, source string, target string) error {
	// Figure out the mountpoints
	var srcMountPoint string
	if shared.IsSnapshot(source) {
		srcMountPoint = driver.GetStoragePoolVolumeSnapshotMountPoint(sourcePool, source)
	} else {
		srcMountPoint = driver.GetStoragePoolVolumeMountPoint(sourcePool, source)
	}

	// Split target name
	targetVolName, targetSnapName, ok := containerGetParentAndSnapshotName(target)

	// Figure out target path
	dstMountPoint := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, targetVolName)

	// Sync data on target
	bwlimit := s.pool.Config["rsync.bwlimit"]
	_, err := rsyncLocalCopy(srcMountPoint, dstMountPoint, bwlimit, false)
	if err != nil {
		logger.Errorf("Failed to rsync into CEPHFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	// Snapshot target
	if ok {
		cephSnapPath := filepath.Join(dstMountPoint, ".snap", targetSnapName)
		err := os.Mkdir(cephSnapPath, 0711)
		if err != nil {
			return err
		}

		// Make the snapshot path a symlink
		targetPath := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, target)
		err = os.MkdirAll(filepath.Dir(targetPath), 0711)
		if err != nil {
			return err
		}

		err = os.Symlink(cephSnapPath, targetPath)
		if err != nil {
			return err
		}
	}

	return nil
}

func cephFsExists(clusterName string, userName string, fsName string) bool {
	_, err := shared.RunCommand("ceph", "--name", fmt.Sprintf("client.%s", userName), "--cluster", clusterName, "fs", "get", fsName)
	if err != nil {
		return false
	}

	return true
}

func cephFsConfig(clusterName string, userName string) ([]string, string, error) {
	// Parse the CEPH configuration
	cephConf, err := os.Open(fmt.Sprintf("/etc/ceph/%s.conf", clusterName))
	if err != nil {
		return nil, "", err
	}

	cephMon := []string{}

	scan := bufio.NewScanner(cephConf)
	for scan.Scan() {
		line := scan.Text()
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "mon_host") {
			fields := strings.SplitN(line, "=", 2)
			if len(fields) < 2 {
				continue
			}

			servers := strings.Split(fields[1], ",")
			for _, server := range servers {
				cephMon = append(cephMon, strings.TrimSpace(server))
			}
			break
		}
	}

	if len(cephMon) == 0 {
		return nil, "", fmt.Errorf("Couldn't find a CPEH mon")
	}

	// Parse the CEPH keyring
	cephKeyring, err := os.Open(fmt.Sprintf("/etc/ceph/%v.client.%v.keyring", clusterName, userName))
	if err != nil {
		return nil, "", err
	}

	var cephSecret string

	scan = bufio.NewScanner(cephKeyring)
	for scan.Scan() {
		line := scan.Text()
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "key") {
			fields := strings.SplitN(line, "=", 2)
			if len(fields) < 2 {
				continue
			}

			cephSecret = strings.TrimSpace(fields[1])
			break
		}
	}

	if cephSecret == "" {
		return nil, "", fmt.Errorf("Couldn't find a keyring entry")
	}

	return cephMon, cephSecret, nil
}
