package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/device"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/ioprogress"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/units"
	"github.com/lxc/lxd/shared/version"
)

func init() {
	// Expose storageVolumeMount to the device package as StorageVolumeMount.
	device.StorageVolumeMount = storageVolumeMount
	// Expose storageVolumeUmount to the device package as StorageVolumeUmount.
	device.StorageVolumeUmount = storageVolumeUmount
	// Expose storageRootFSApplyQuota to the device package as StorageRootFSApplyQuota.
	device.StorageRootFSApplyQuota = storageRootFSApplyQuota

}

// lxdStorageLockMap is a hashmap that allows functions to check whether the
// operation they are about to perform is already in progress. If it is the
// channel can be used to wait for the operation to finish. If it is not, the
// function that wants to perform the operation should store its code in the
// hashmap.
// Note that any access to this map must be done while holding a lock.
var lxdStorageOngoingOperationMap = map[string]chan bool{}

// lxdStorageMapLock is used to access lxdStorageOngoingOperationMap.
var lxdStorageMapLock sync.Mutex

// The following functions are used to construct simple operation codes that are
// unique.
func getPoolMountLockID(poolName string) string {
	return fmt.Sprintf("mount/pool/%s", poolName)
}

func getPoolUmountLockID(poolName string) string {
	return fmt.Sprintf("umount/pool/%s", poolName)
}

func getImageCreateLockID(poolName string, fingerprint string) string {
	return fmt.Sprintf("create/image/%s/%s", poolName, fingerprint)
}

func getContainerMountLockID(poolName string, containerName string) string {
	return fmt.Sprintf("mount/container/%s/%s", poolName, containerName)
}

func getContainerUmountLockID(poolName string, containerName string) string {
	return fmt.Sprintf("umount/container/%s/%s", poolName, containerName)
}

func getCustomMountLockID(poolName string, volumeName string) string {
	return fmt.Sprintf("mount/custom/%s/%s", poolName, volumeName)
}

func getCustomUmountLockID(poolName string, volumeName string) string {
	return fmt.Sprintf("umount/custom/%s/%s", poolName, volumeName)
}

// Simply cache used to storage the activated drivers on this LXD instance. This
// allows us to avoid querying the database everytime and API call is made.
var storagePoolDriversCacheVal atomic.Value
var storagePoolDriversCacheLock sync.Mutex

func readStoragePoolDriversCache() map[string]string {
	drivers := storagePoolDriversCacheVal.Load()
	if drivers == nil {
		return map[string]string{}
	}

	return drivers.(map[string]string)
}

// storageType defines the type of a storage
type storageType int

const (
	storageTypeBtrfs storageType = iota
	storageTypeCeph
	storageTypeCephFs
	storageTypeDir
	storageTypeLvm
	storageTypeMock
	storageTypeZfs
)

var supportedStoragePoolDrivers = []string{"btrfs", "ceph", "cephfs", "dir", "lvm", "zfs"}

func storageTypeToString(sType storageType) (string, error) {
	switch sType {
	case storageTypeBtrfs:
		return "btrfs", nil
	case storageTypeCeph:
		return "ceph", nil
	case storageTypeCephFs:
		return "cephfs", nil
	case storageTypeDir:
		return "dir", nil
	case storageTypeLvm:
		return "lvm", nil
	case storageTypeMock:
		return "mock", nil
	case storageTypeZfs:
		return "zfs", nil
	}

	return "", fmt.Errorf("invalid storage type")
}

func storageStringToType(sName string) (storageType, error) {
	switch sName {
	case "btrfs":
		return storageTypeBtrfs, nil
	case "ceph":
		return storageTypeCeph, nil
	case "cephfs":
		return storageTypeCephFs, nil
	case "dir":
		return storageTypeDir, nil
	case "lvm":
		return storageTypeLvm, nil
	case "mock":
		return storageTypeMock, nil
	case "zfs":
		return storageTypeZfs, nil
	}

	return -1, fmt.Errorf("invalid storage type name")
}

type Storage struct {
	sType     storageType
	sTypeName string

	s *state.State

	poolID int64
	pool   *api.StoragePool

	volumeID int64
	volume   *api.StorageVolume

	driver driver.Driver
}

func (s *Storage) GetStorageType() storageType {
	return s.sType
}

func (s *Storage) GetStorageTypeName() string {
	return s.sTypeName
}

func (s *Storage) GetStorageTypeVersion() string {
	return s.driver.GetVersion()
}

func (s *Storage) GetState() *state.State {
	return s.s
}

func (s *Storage) GetStoragePoolWritable() api.StoragePoolPut {
	return s.pool.Writable()
}

func (s *Storage) SetStoragePoolWritable(writable *api.StoragePoolPut) {
	s.pool.StoragePoolPut = *writable
}

func (s *Storage) GetStoragePool() *api.StoragePool {
	return s.pool
}

func (s *Storage) GetStoragePoolVolumeWritable() api.StorageVolumePut {
	return s.volume.Writable()
}

func (s *Storage) SetStoragePoolVolumeWritable(writable *api.StorageVolumePut) {
	s.volume.StorageVolumePut = *writable
}

func (s *Storage) GetStoragePoolVolume() *api.StorageVolume {
	return s.volume
}

func (s *Storage) GetContainerPoolInfo() (int64, string, string) {
	return s.poolID, s.pool.Name, s.pool.Name
}

func (s *Storage) StorageCoreInit() error {
	var err error

	s.driver, err = driver.Init(s.sTypeName, s.s, s.pool, s.poolID, s.volume)

	return err
}

func (s *Storage) StoragePoolInit() error {
	return s.StorageCoreInit()
}

func (s *Storage) StoragePoolCheck() error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
	}
	success := false

	defer logAction(
		"Checking storage pool",
		"Checked storage pool",
		"Failed to check storage pool",
		&ctx, &success, &err)()

	err = s.driver.StoragePoolCheck()
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) StoragePoolCreate() error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
	}
	success := false

	defer logAction(
		"Creating storage pool",
		"Created storage pool",
		"Failed to create storage pool",
		&ctx, &success, &err)()

	s.pool.Config["volatile.initial_source"] = s.pool.Config["source"]

	err = s.driver.StoragePoolCreate()
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) StoragePoolDelete() error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
	}
	success := false

	defer logAction(
		"Deleting storage pool",
		"Deleted storage pool",
		"Failed to delete storage pool",
		&ctx, &success, &err)()

	err = s.driver.StoragePoolDelete()
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) StoragePoolMount() (bool, error) {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
	}
	success := false

	defer logAction(
		"Mounting storage pool",
		"Mounted storage pool",
		"Failed to mount storage pool",
		&ctx, &success, &err)()

	ok, err := s.driver.StoragePoolMount()
	if err != nil {
		return ok, err
	}

	success = true

	return ok, nil
}

func (s *Storage) StoragePoolUmount() (bool, error) {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
	}
	success := false

	defer logAction(
		"Unmounting storage pool",
		"Unmounted storage pool",
		"Failed to unmount storage pool",
		&ctx, &success, &err)()

	ok, err := s.driver.StoragePoolUmount()
	if err != nil {
		return ok, err
	}

	success = true

	return ok, nil
}

func (s *Storage) StoragePoolResources() (*api.ResourcesStoragePool, error) {
	return s.driver.StoragePoolResources()
}

func (s *Storage) StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
	}
	success := false

	defer logAction(
		"Updating storage pool",
		"Updated storage pool",
		"Failed to update storage pool",
		&ctx, &success, &err)()

	changeable := changeableStoragePoolProperties[s.sTypeName]
	unchangeable := []string{}

	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		err = updateStoragePoolError(unchangeable, s.sTypeName)
		return err
	}

	err = s.driver.StoragePoolUpdate(writable, changedConfig)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) StoragePoolVolumeCreate() error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"volume": s.volume.Name,
	}
	success := false

	defer logAction(
		"Creating storage pool volume",
		"Created storage pool volume",
		"Failed to create storage pool volume",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	isSnapshot := shared.IsSnapshot(s.volume.Name)

	// Create subvolume path on the storage pool.
	var volumePath string

	if isSnapshot {
		volumePath = driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, "")
	} else {
		volumePath = driver.GetStoragePoolVolumeMountPoint(s.pool.Name, "")
	}

	if !shared.PathExists(volumePath) {
		err = os.MkdirAll(volumePath, driver.CustomDirMode)
		if err != nil {
			return err
		}
	}

	err = s.driver.VolumeCreate("default", s.volume.Name, driver.VolumeTypeCustom)
	if err != nil {
		return err
	}

	volumeMntPoint := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	if !shared.PathExists(volumeMntPoint) {
		err = os.MkdirAll(volumeMntPoint, 0711)
		if err != nil {
			return err
		}
	}

	success = true

	return nil
}

func (s *Storage) StoragePoolVolumeDelete() error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"volume": s.volume.Name,
	}
	success := false

	defer logAction(
		"Deleting storage pool volume",
		"Deleted storage pool volume",
		"Failed to delete storage pool volume",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	volumeMntPoint := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	err = s.driver.VolumeDelete("default", s.volume.Name, true, driver.VolumeTypeCustom)
	if err != nil {
		return err
	}

	err = os.RemoveAll(volumeMntPoint)
	if err != nil {
		return err
	}

	err = s.s.Cluster.StoragePoolVolumeDelete(
		"default",
		s.volume.Name,
		storagePoolVolumeTypeCustom,
		s.poolID)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) StoragePoolVolumeMount() (bool, error) {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"volume": s.volume.Name,
	}
	success := false

	defer logAction(
		"Mounting storage pool volume",
		"Mounted storage pool volume",
		"Failed to mount storage pool volume",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return ourMount, err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	ok, err := s.driver.VolumeMount("default", s.volume.Name, driver.VolumeTypeCustom)
	if err != nil {
		return ok, err
	}

	success = true

	return ok, nil
}

func (s *Storage) StoragePoolVolumeUmount() (bool, error) {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"volume": s.volume.Name,
	}
	success := false

	defer logAction(
		"Unmounting storage pool volume",
		"Unmounting storage pool volume",
		"Failed to unmount storage pool volume",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return ourMount, err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	ok, err := s.driver.VolumeUmount("default", s.volume.Name, driver.VolumeTypeCustom)
	if err != nil {
		return ok, err
	}

	success = true

	return ok, nil
}

func (s *Storage) StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"volume": s.volume.Name,
	}
	success := false

	defer logAction(
		"Updating storage pool volume",
		"Updated storage pool volume",
		"Failed to update storage pool volume",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	if writable.Restore != "" {
		err = s.driver.VolumeRestore("default", fmt.Sprintf("%s/%s", s.volume.Name, writable.Restore), s.volume.Name, driver.VolumeTypeCustomSnapshot)
		if err != nil {
			return err
		}

		success = true

		return nil
	}

	changeable := changeableStoragePoolVolumeProperties[s.sTypeName]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		err = updateStoragePoolVolumeError(unchangeable, s.sTypeName)
		return err
	}

	err = s.driver.VolumeUpdate(writable, changedConfig)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) StoragePoolVolumeRename(newName string) error {
	var err error
	ctx := log.Ctx{
		"driver":   s.sTypeName,
		"pool":     s.pool.Name,
		"old_name": s.volume.Name,
		"new_name": newName,
	}
	success := false

	defer logAction(
		"Renaming storage pool volume",
		"Renamed storage pool volume",
		"Failed to rename storage pool volume",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	usedBy, err := storagePoolVolumeUsedByContainersGet(s.s, "default", s.volume.Name,
		storagePoolVolumeTypeNameCustom)
	if err != nil {
		return err
	}

	if len(usedBy) > 0 {
		err = fmt.Errorf(`storage volume "%s" on storage pool "%s" is attached to containers`,
			s.volume.Name, s.pool.Name)
		return err
	}

	err = s.driver.VolumeRename("default", s.volume.Name, newName, nil, driver.VolumeTypeCustom)
	if err != nil {
		return err
	}

	err = s.s.Cluster.StoragePoolVolumeRename("default", s.volume.Name, newName,
		storagePoolVolumeTypeCustom, s.poolID)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) StoragePoolVolumeCopy(source *api.StorageVolumeSource) error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"source": source.Name,
		"target": s.volume.Name,
	}
	success := false

	defer logAction(
		"Copying storage pool volume",
		"Copied storage pool volume",
		"Failed to copy storage pool volume",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	if s.pool.Name != source.Pool {
		err = s.doCrossPoolVolumeCopy(source)
		if err != nil {
			return err
		}

		success = true

		return nil
	}

	isSnapshot := shared.IsSnapshot(source.Name)
	volumeMntPoint := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, "")

	err = os.MkdirAll(volumeMntPoint, driver.CustomDirMode)
	if err != nil {
		return err
	}

	if isSnapshot {
		return s.driver.VolumeSnapshotCopy("default", source.Name, s.volume.Name, driver.VolumeTypeCustomSnapshot)
	}

	snapshots, err := s.s.Cluster.StoragePoolVolumeSnapshotsGetType(source.Name, storagePoolVolumeTypeCustom, s.poolID)
	if err != nil {
		return err
	}

	var snapOnlyNames []string

	for _, snap := range snapshots {
		snapOnlyNames = append(snapOnlyNames, shared.ExtractSnapshotName(snap))
	}

	err = s.driver.VolumeCopy("default", source.Name, s.volume.Name, snapOnlyNames, driver.VolumeTypeCustom)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) doCrossPoolVolumeCopy(source *api.StorageVolumeSource) error {
	// setup storage for the source volume
	srcStorage, err := storagePoolVolumeInit(s.s, "default", source.Pool, source.Name,
		storagePoolVolumeTypeCustom)
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

	err = s.StoragePoolVolumeCreate()
	if err != nil {
		return err
	}

	ourMount, err = s.StoragePoolVolumeMount()
	if err != nil {
		return err
	}
	if ourMount {
		defer s.StoragePoolVolumeUmount()
	}

	dstMountPoint := driver.GetStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	bwlimit := s.pool.Config["rsync.bwlimit"]

	if !source.VolumeOnly {
		snapshots, err := storagePoolVolumeSnapshotsGet(s.s, source.Pool, source.Name, storagePoolVolumeTypeCustom)
		if err != nil {
			return err
		}

		for _, snap := range snapshots {
			srcMountPoint := driver.GetStoragePoolVolumeSnapshotMountPoint(source.Pool, snap)

			_, err = rsyncLocalCopy(srcMountPoint, dstMountPoint, bwlimit, true)
			if err != nil {
				logger.Errorf("Failed to rsync into ZFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
				return err
			}

			_, snapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(source.Name)

			s.StoragePoolVolumeSnapshotCreate(&api.StorageVolumeSnapshotsPost{Name: fmt.Sprintf("%s/%s", s.volume.Name, snapOnlyName)})
		}
	}

	var srcMountPoint string

	if shared.IsSnapshot(source.Name) {
		srcMountPoint = driver.GetStoragePoolVolumeSnapshotMountPoint(source.Pool, source.Name)
	} else {
		srcMountPoint = driver.GetStoragePoolVolumeMountPoint(source.Pool, source.Name)
	}

	_, err = rsyncLocalCopy(srcMountPoint, dstMountPoint, bwlimit, true)
	if err != nil {
		os.RemoveAll(dstMountPoint)
		return err
	}

	return nil
}

func (s *Storage) StoragePoolVolumeSnapshotCreate(target *api.StorageVolumeSnapshotsPost) error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"source": s.volume.Name,
		"target": target.Name,
	}
	success := false

	defer logAction(
		"Creating storage pool volume snapshot",
		"Created storage pool volume snapshot",
		"Failed to create storage pool volume snapshot",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	_, _, ok := shared.ContainerGetParentAndSnapshotName(target.Name)
	if !ok {
		err = fmt.Errorf("Not a snapshot name")
		return err
	}

	targetPath := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)

	err = os.MkdirAll(targetPath, driver.SnapshotsDirMode)
	if err != nil {
		return err
	}

	err = s.driver.VolumeSnapshotCreate("default", s.volume.Name, target.Name,
		driver.VolumeTypeCustomSnapshot)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) StoragePoolVolumeSnapshotDelete() error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"volume": s.volume.Name,
	}
	success := false

	defer logAction(
		"Deleting storage pool volume snapshot",
		"Deleted storage pool volume snapshot",
		"Failed to delete storage pool volume snapshot",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	err = s.driver.VolumeSnapshotDelete("default", s.volume.Name, true, driver.VolumeTypeCustomSnapshot)
	if err != nil {
		return err
	}

	snapshotMntPoint := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)

	err = os.RemoveAll(snapshotMntPoint)
	if err != nil {
		return err
	}

	sourceVolumeName, _, _ := shared.ContainerGetParentAndSnapshotName(s.volume.Name)
	snapshotVolumePath := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, sourceVolumeName)

	empty, _ := shared.PathIsEmpty(snapshotVolumePath)
	if empty {
		err = os.Remove(snapshotVolumePath)
		if err != nil {
			return err
		}

		snapshotSymlink := shared.VarPath("custom-snapshots", sourceVolumeName)
		if shared.PathExists(snapshotSymlink) {
			err = os.Remove(snapshotSymlink)
			if err != nil {
				return err
			}
		}
	}

	err = s.s.Cluster.StoragePoolVolumeDelete(
		"default",
		s.volume.Name,
		storagePoolVolumeTypeCustom,
		s.poolID)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) StoragePoolVolumeSnapshotRename(newName string) error {
	var err error
	ctx := log.Ctx{
		"driver":   s.sTypeName,
		"pool":     s.pool.Name,
		"old_name": s.volume.Name,
		"new_name": newName,
	}
	success := false

	defer logAction(
		"Renaming storage pool volume snapshot",
		"Renamed storage pool volume snapshot",
		"Failed to rename storage pool volume snapshot",
		&ctx, &success, &err)()

	sourceName, _, _ := shared.ContainerGetParentAndSnapshotName(s.volume.Name)
	fullSnapshotName := fmt.Sprintf("%s%s%s", sourceName, shared.SnapshotDelimiter, newName)

	oldPath := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	newPath := driver.GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, fullSnapshotName)

	err = os.MkdirAll(newPath, driver.CustomDirMode)
	if err != nil {
		return err
	}

	err = s.driver.VolumeSnapshotRename("default", s.volume.Name, fullSnapshotName, driver.VolumeTypeCustomSnapshot)
	if err != nil {
		return err
	}

	// It might be, that the driver already renamed the path.
	if shared.PathExists(oldPath) {
		err = os.Rename(oldPath, newPath)
	}

	err = s.s.Cluster.StoragePoolVolumeRename("default", s.volume.Name, fullSnapshotName, storagePoolVolumeTypeCustom, s.poolID)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) ContainerCreate(container Instance) error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"name":   container.Name(),
	}
	success := false

	defer logAction(
		"Creating container",
		"Created container",
		"Failed to create container",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		err = errors.Wrapf(err, "Mount storage pool '%s'", s.pool.Name)
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	containerPath := driver.GetContainerMountPoint("default", s.pool.Name, "")

	err = os.MkdirAll(containerPath, driver.ContainersDirMode)
	if err != nil {
		err = errors.Wrapf(err, "Create containers mountpoint '%s'", containerPath)
		return err
	}

	// Create container volume
	err = s.driver.VolumeCreate(container.Project(), container.Name(),
		driver.VolumeTypeContainer)
	if err != nil {
		err = errors.Wrapf(err, "Create container '%s'", container.Name())
		return err
	}

	// Create directories
	containerMntPoint := driver.GetContainerMountPoint(container.Project(), s.pool.Name, container.Name())

	err = driver.CreateContainerMountpoint(containerMntPoint, container.Path(), container.IsPrivileged())
	if err != nil {
		err = errors.Wrapf(err, "Create container mountpoint '%s'", containerMntPoint)
		return err
	}

	revert := false

	defer func() {
		if revert {
			deleteContainerMountpoint(containerMntPoint, container.Path(), s.GetStorageTypeName())
		}
	}()

	success = true

	return container.TemplateApply("create")
}

func (s *Storage) ContainerCreateFromImage(container Instance, fingerprint string, tracker *ioprogress.ProgressTracker) error {
	var err error
	ctx := log.Ctx{
		"driver":      s.sTypeName,
		"pool":        s.pool.Name,
		"name":        container.Name(),
		"fingerprint": fingerprint,
	}
	success := false

	defer logAction(
		"Creating container from image",
		"Created container from image",
		"Failed to create container from image",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		err = errors.Wrapf(err, "Mount storage pool '%s'", s.pool.Name)
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	containerPath := driver.GetContainerMountPoint("default", s.pool.Name, "")

	err = os.MkdirAll(containerPath, driver.ContainersDirMode)
	if err != nil {
		err = errors.Wrapf(err, "Create containers mountpoint '%s'", containerPath)
		return err
	}

	// Create directories
	containerMntPoint := driver.GetContainerMountPoint(container.Project(), s.pool.Name, container.Name())

	imageMntPoint := shared.VarPath("images", fingerprint)
	revert := true

	// Create container volume
	err = s.driver.VolumeCreate(container.Project(), container.Name(), driver.VolumeTypeContainer)
	if err != nil {
		err = errors.Wrap(err, "Create container")
		return err
	}

	err = driver.CreateContainerMountpoint(containerMntPoint, container.Path(), container.IsPrivileged())
	if err != nil {
		err = errors.Wrapf(err, "Create container mountpoint '%s'", containerMntPoint)
		return err
	}

	defer func() {
		if revert {
			deleteContainerMountpoint(containerMntPoint, container.Path(), s.GetStorageTypeName())
		}
	}()

	ourMount, err = s.driver.VolumeMount(container.Project(), container.Name(), driver.VolumeTypeContainer)
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.VolumeUmount(container.Project(), container.Name(), driver.VolumeTypeContainer)
	}

	err = unpackImage(imageMntPoint, containerMntPoint, s.sType, s.s.OS.RunningInUserNS,
		tracker)
	if err != nil {
		err = errors.Wrap(err, "Unpack image")
		return err
	}

	revert = false
	success = true

	return container.TemplateApply("create")
}

func (s *Storage) ContainerDelete(c Instance) error {
	var err error
	ctx := log.Ctx{
		"driver":    s.sTypeName,
		"pool":      s.pool.Name,
		"container": c.Name(),
	}
	success := false

	defer logAction(
		"Deleting container",
		"Deleted container",
		"Failed to delete container",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	containerMntPoint := driver.GetContainerMountPoint(c.Project(), s.pool.Name, c.Name())
	// ${LXD_DIR}/snapshots/<container_name> to ${POOL}/snapshots/<container_name>

	err = s.driver.VolumeDelete(c.Project(), c.Name(), true, driver.VolumeTypeContainer)
	if err != nil {
		return err
	}

	err = deleteContainerMountpoint(containerMntPoint, c.Path(), s.GetStorageTypeName())
	if err != nil {
		return err
	}

	// Delete potential leftover snapshot mountpoints.
	snapshotMntPoint := driver.GetSnapshotMountPoint(c.Project(), s.pool.Name, c.Name())
	if shared.PathExists(snapshotMntPoint) {
		err := os.RemoveAll(snapshotMntPoint)
		if err != nil {
			return err
		}
	}

	// Delete potential leftover snapshot symlinks:
	// ${LXD_DIR}/snapshots/<container_name> to ${POOL}/snapshots/<container_name>
	snapshotSymlink := shared.VarPath("snapshots", project.Prefix(c.Project(), c.Name()))
	if shared.PathExists(snapshotSymlink) {
		err := os.Remove(snapshotSymlink)
		if err != nil {
			return err
		}
	}

	success = true

	return nil
}

func (s *Storage) ContainerRename(c Instance, newName string) error {
	var err error
	ctx := log.Ctx{
		"driver":   s.sTypeName,
		"pool":     s.pool.Name,
		"old_name": c.Name(),
		"new_name": newName,
	}
	success := false

	logAction(
		"Renaming container",
		"Renamed container",
		"Failed to rename container",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	oldContainerMntPoint := driver.GetContainerMountPoint(c.Project(), s.pool.Name, c.Name())
	oldContainerSymlink := driver.ContainerPath(project.Prefix(c.Project(), c.Name()), false)
	newContainerMntPoint := driver.GetContainerMountPoint(c.Project(), s.pool.Name, newName)
	newContainerSymlink := driver.ContainerPath(project.Prefix(c.Project(), newName), false)

	var snapshotNames []string

	snapshots, err := c.Snapshots()
	if err != nil {
		return err
	}

	for _, snap := range snapshots {
		snapshotNames = append(snapshotNames, shared.ExtractSnapshotName(snap.Name()))
	}

	// Snapshots are renamed here as well as they're tied to a volume/containers.
	// There's no need to call VolumeSnapshotRename for each snapshot.
	err = s.driver.VolumeRename(c.Project(), c.Name(), newName, snapshotNames,
		driver.VolumeTypeContainer)
	if err != nil {
		return err
	}

	err = renameContainerMountpoint(oldContainerMntPoint, oldContainerSymlink,
		newContainerMntPoint, newContainerSymlink)
	if err != nil {
		return err
	}

	if c.IsSnapshot() {
		success = true
		return nil
	}

	oldSnapshotsMntPoint := driver.GetSnapshotMountPoint(c.Project(), s.pool.Name, c.Name())
	newSnapshotsMntPoint := driver.GetSnapshotMountPoint(c.Project(), s.pool.Name, newName)
	oldSnapshotSymlink := driver.ContainerPath(project.Prefix(c.Project(), c.Name()), true)
	newSnapshotSymlink := driver.ContainerPath(project.Prefix(c.Project(), newName), true)

	err = renameContainerMountpoint(oldSnapshotsMntPoint, oldSnapshotSymlink, newSnapshotsMntPoint, newSnapshotSymlink)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) ContainerMount(c Instance) (bool, error) {
	var err error
	ctx := log.Ctx{
		"driver":    s.sTypeName,
		"pool":      s.pool.Name,
		"container": c.Name(),
	}
	success := false

	defer logAction(
		"Mounting container",
		"Mounted container",
		"Failed to mount container",
		&ctx, &success, &err)()

	_, err = s.driver.StoragePoolMount()
	if err != nil {
		return false, err
	}

	ok, err := s.driver.VolumeMount(c.Project(), c.Name(), driver.VolumeTypeContainer)
	if err != nil {
		return ok, err
	}

	success = true

	return ok, nil
}

func (s *Storage) ContainerUmount(c Instance, path string) (bool, error) {
	var err error
	ctx := log.Ctx{
		"driver":    s.sTypeName,
		"pool":      s.pool.Name,
		"container": c.Name(),
	}
	success := false

	defer logAction(
		"Unmounting container",
		"Unmounted container",
		"Failed to unmount container",
		&ctx, &success, &err)()

	ok, err := s.driver.VolumeUmount(c.Project(), c.Name(), driver.VolumeTypeContainer)
	if err != nil {
		return ok, err
	}

	success = true

	return ok, nil
}

func (s *Storage) ContainerCopy(target Instance, source Instance, containerOnly bool) error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"source": source.Name(),
		"target": target.Name(),
	}
	success := false

	defer logAction(
		"Copying container",
		"Copied container",
		"Failed to copy container",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	ourStart, err := source.StorageStart()
	if err != nil {
		return err
	}

	if ourStart {
		defer source.StorageStop()
	}

	sourcePool, err := source.StoragePool()
	if err != nil {
		return err
	}

	targetPool, err := target.StoragePool()
	if err != nil {
		return err
	}

	var snapshots []Instance

	if !containerOnly {
		snapshots, err = source.Snapshots()
		if err != nil {
			return err
		}
	}

	if sourcePool != targetPool {
		err = s.doCrossPoolContainerCopy(target, source, containerOnly, false, snapshots)
		if err != nil {
			return err
		}

		success = true
		return nil
	}

	containerMntPoint := driver.GetContainerMountPoint("default", s.pool.Name, "")

	err = os.MkdirAll(containerMntPoint, driver.ContainersDirMode)
	if err != nil {
		return err
	}

	targetMntPoint := driver.GetContainerMountPoint(target.Project(), s.pool.Name, target.Name())

	var snapshotNames []string

	if !containerOnly {
		for _, c := range snapshots {
			snapshotNames = append(snapshotNames, shared.ExtractSnapshotName(c.Name()))
		}

		targetParentName, _, _ := shared.ContainerGetParentAndSnapshotName(target.Name())
		snapshotMntPoint := driver.GetSnapshotMountPoint(target.Project(), s.pool.Name, target.Name())
		snapshotMntPointSymlinkTarget := driver.GetSnapshotMountPoint(target.Project(), s.pool.Name, targetParentName)
		snapshotMntPointSymlink := driver.ContainerPath(project.Prefix(target.Project(), targetParentName), true)

		err = driver.CreateSnapshotMountpoint(snapshotMntPoint, snapshotMntPointSymlinkTarget,
			snapshotMntPointSymlink)
		if err != nil {
			return err
		}
	}

	if shared.IsSnapshot(source.Name()) {
		err = s.driver.VolumeSnapshotCopy(source.Project(), source.Name(), target.Name(), driver.VolumeTypeContainerSnapshot)
	} else {
		err = s.driver.VolumeCopy(source.Project(), source.Name(), target.Name(), snapshotNames, driver.VolumeTypeContainer)
	}
	if err != nil {
		return err
	}

	err = driver.CreateContainerMountpoint(targetMntPoint, target.Path(), target.IsPrivileged())
	if err != nil {
		return err
	}

	err = target.TemplateApply("copy")
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) ContainerGetUsage(container Instance) (int64, error) {
	return s.driver.VolumeGetUsage(container.Project(), container.Name(), container.Path())
}

func (s *Storage) ContainerRefresh(target Instance, source Instance, snapshots []Instance) error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"source": source.Name(),
		"target": target.Name(),
	}
	success := false

	defer logAction(
		"Refreshing container",
		"Refreshed container",
		"Failed to refresh container",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	err = s.doCrossPoolContainerCopy(target, source, len(snapshots) == 0, true, snapshots)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) doCrossPoolContainerCopy(target Instance, source Instance, containerOnly bool,
	refresh bool, refreshSnapshots []Instance) error {
	sourcePool, err := source.StoragePool()
	if err != nil {
		return err
	}

	targetPool, err := target.StoragePool()
	if err != nil {
		return err
	}

	// setup storage for the source volume
	srcStorage, err := storagePoolVolumeInit(s.s, "default", sourcePool, source.Name(),
		storagePoolVolumeTypeContainer)
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

	var snapshots []Instance

	if refresh {
		snapshots = refreshSnapshots
	} else {
		snapshots, err = source.Snapshots()
		if err != nil {
			return err
		}

		// create the main container
		err = s.ContainerCreate(target)
		if err != nil {
			return err
		}
	}

	_, err = s.ContainerMount(target)
	if err != nil {
		return err
	}
	defer s.ContainerUmount(target, shared.VarPath("containers", project.Prefix(target.Project(), target.Name())))

	destContainerMntPoint := driver.GetContainerMountPoint(target.Project(), targetPool, target.Name())
	bwlimit := s.pool.Config["rsync.bwlimit"]

	if !containerOnly {
		snapshotSubvolumePath := driver.GetSnapshotMountPoint(target.Project(), s.pool.Name, target.Name())
		if !shared.PathExists(snapshotSubvolumePath) {
			err := os.MkdirAll(snapshotSubvolumePath, driver.ContainersDirMode)
			if err != nil {
				return err
			}
		}

		snapshotMntPoint := driver.GetSnapshotMountPoint(target.Project(), s.pool.Name, s.volume.Name)
		snapshotMntPointSymlink := driver.ContainerPath(project.Prefix(target.Project(), target.Name()), true)

		err = driver.CreateSnapshotMountpoint(snapshotMntPoint, snapshotMntPoint, snapshotMntPointSymlink)
		if err != nil {
			return err
		}

		for _, snap := range snapshots {
			srcSnapshotMntPoint := driver.GetSnapshotMountPoint(source.Project(), sourcePool, snap.Name())
			targetParentName, snapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(snap.Name())
			destSnapshotMntPoint := driver.GetSnapshotMountPoint(target.Project(), targetPool,
				fmt.Sprintf("%s%s%s", target.Name(), shared.SnapshotDelimiter, snapOnlyName))

			_, err = rsyncLocalCopy(srcSnapshotMntPoint, destSnapshotMntPoint, bwlimit, true)
			if err != nil {
				return err
			}

			err := driver.CreateSnapshotMountpoint(destSnapshotMntPoint, destSnapshotMntPoint,
				shared.VarPath("snapshots",
					project.Prefix(target.Project(), targetParentName)))
			if err != nil {
				return err
			}
		}
	}

	srcContainerMntPoint := driver.GetContainerMountPoint(source.Project(), sourcePool, source.Name())

	_, err = rsyncLocalCopy(srcContainerMntPoint, destContainerMntPoint, bwlimit, true)
	if err != nil {
		return err
	}

	return nil
}

func (s *Storage) ContainerRestore(targetContainer Instance, sourceContainer Instance) error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"source": sourceContainer.Name(),
		"target": targetContainer.Name(),
	}
	success := false

	defer logAction(
		"Restoring container",
		"Restored container",
		"Failed to restore container",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	snapshots, err := targetContainer.Snapshots()
	if err != nil {
		return err
	}

	var snapshotNames []string

	for _, snap := range snapshots {
		snapshotNames = append(snapshotNames, snap.Name())
	}

	deleteSnapshots := func() error {
		for i := len(snapshots) - 1; i != 0; i-- {
			if snapshots[i].Name() == sourceContainer.Name() {
				break
			}

			err := snapshots[i].Delete()
			if err != nil {
				return err
			}
		}

		return nil
	}

	err = s.driver.VolumePrepareRestore(sourceContainer.Name(), targetContainer.Name(), snapshotNames, deleteSnapshots)
	if err != nil {
		return err
	}

	err = s.driver.VolumeRestore(sourceContainer.Project(), sourceContainer.Name(),
		targetContainer.Name(), driver.VolumeTypeContainerSnapshot)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) ContainerStorageReady(c Instance) bool {
	return s.driver.VolumeReady(c.Project(), c.Name())
}

func (s *Storage) ContainerSnapshotCreate(target Instance, source Instance) error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"source": source.Name(),
		"target": target.Name(),
	}
	success := false

	defer logAction(
		"Creating container snapshot",
		"Created container snapshot",
		"Failed to create container snapshot",
		&ctx, &success, &err)()

	_, err = s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	// We can only create the btrfs subvolume under the mounted storage
	// pool. The on-disk layout for snapshots on a btrfs storage pool will
	// thus be
	// ${LXD_DIR}/storage-pools/<pool>/snapshots/. The btrfs tool will
	// complain if the intermediate path does not exist, so create it if it
	// doesn't already.
	snapshotSubvolumePath := driver.GetSnapshotMountPoint(source.Project(), s.pool.Name, source.Name())
	err = os.MkdirAll(snapshotSubvolumePath, driver.ContainersDirMode)
	if err != nil {
		return err
	}

	snapshotMntPoint := driver.GetSnapshotMountPoint(source.Project(), s.pool.Name, target.Name())
	//snapshotParentName, _, _ := shared.ContainerGetParentAndSnapshotName(source.Name())
	snapshotMntPointSymlinkTarget := driver.GetSnapshotMountPoint(source.Project(), s.pool.Name, source.Name())
	snapshotMntPointSymlink := driver.ContainerPath(project.Prefix(source.Project(), source.Name()), target.IsSnapshot())

	err = driver.CreateSnapshotMountpoint(snapshotMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	if err != nil {
		return err
	}

	err = s.driver.VolumeSnapshotCreate(source.Project(), source.Name(), target.Name(), driver.VolumeTypeContainerSnapshot)
	if err != nil {
		return s.ContainerDelete(target)
	}

	// This is used only in Dir
	if source.IsRunning() {
		err = source.Freeze()
		if err != nil {
			// Don't just fail here
			success = true
			return nil
		}

		defer source.Unfreeze()

		err = s.driver.VolumeSnapshotCreate(source.Project(), source.Name(), target.Name(), driver.VolumeTypeContainerSnapshot)
		if err != nil {
			return err
		}
	}

	success = true

	return nil
}

func (s *Storage) ContainerSnapshotCreateEmpty(c Instance) error {
	var err error
	ctx := log.Ctx{
		"driver":   s.sTypeName,
		"pool":     s.pool.Name,
		"snapshot": c.Name(),
	}
	success := false

	defer logAction(
		"Creating empty container snapshot",
		"Created empty container snapshot",
		"Failed to create empty container snapshot",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	parentName, _, _ := shared.ContainerGetParentAndSnapshotName(c.Name())
	snapshotMntPoint := driver.GetSnapshotMountPoint(c.Project(), s.pool.Name, parentName)

	err = os.MkdirAll(snapshotMntPoint, driver.ContainersDirMode)
	if err != nil {
		return err
	}

	err = s.driver.VolumeSnapshotCreate(c.Project(), "", c.Name(), driver.VolumeTypeContainerSnapshot)
	if err != nil {
		return err
	}

	sourceName, _, _ := shared.ContainerGetParentAndSnapshotName(c.Name())
	snapshotMntPointSymlinkTarget := driver.GetSnapshotMountPoint(c.Project(), s.pool.Name, sourceName)
	snapshotMntPointSymlink := driver.ContainerPath(project.Prefix(c.Project(), sourceName), true)

	err = driver.CreateSnapshotMountpoint(snapshotMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) ContainerSnapshotDelete(c Instance) error {
	var err error
	ctx := log.Ctx{
		"driver":    s.sTypeName,
		"pool":      s.pool.Name,
		"container": c.Name(),
	}
	success := false

	defer logAction(
		"Deleting container snapshot",
		"Deleted container snapshot",
		"Failed to delete container snapshot",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	sourceContainerName, _, _ := shared.ContainerGetParentAndSnapshotName(c.Name())
	snapshotMntPoint := driver.GetSnapshotMountPoint(c.Project(), s.pool.Name, c.Name())
	snapshotSymlink := shared.VarPath("snapshots", project.Prefix(c.Project(), sourceContainerName))

	err = s.driver.VolumeSnapshotDelete(c.Project(), c.Name(), true, driver.VolumeTypeContainerSnapshot)
	if err != nil {
		return err
	}

	deleteSnapshotMountpoint(snapshotMntPoint, snapshotMntPoint, snapshotSymlink)

	if shared.PathExists(snapshotMntPoint) {
		err := os.RemoveAll(snapshotMntPoint)
		if err != nil {
			return err
		}
	}

	snapshotContainerPath := driver.GetSnapshotMountPoint(c.Project(), s.pool.Name, sourceContainerName)

	empty, _ := shared.PathIsEmpty(snapshotContainerPath)
	if empty {
		err = os.Remove(snapshotContainerPath)
		if err != nil {
			return err
		}

		snapshotSymlink := shared.VarPath("snapshots", project.Prefix(c.Project(), sourceContainerName))
		if shared.PathExists(snapshotSymlink) {
			err = os.Remove(snapshotSymlink)
			if err != nil {
				return err
			}
		}
	}

	success = true

	return nil
}

func (s *Storage) ContainerSnapshotRename(c Instance, newName string) error {
	var err error
	ctx := log.Ctx{
		"driver":   s.sTypeName,
		"pool":     s.pool.Name,
		"old_name": c.Name(),
		"new_name": newName,
	}
	success := false

	defer logAction(
		"Renaming container snapshot",
		"Renamed container snapshot",
		"Failed to rename container snapshot",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	oldSnapshotMntPoint := driver.GetSnapshotMountPoint(c.Project(), s.pool.Name, c.Name())
	newSnapshotMntPoint := driver.GetSnapshotMountPoint(c.Project(), s.pool.Name, newName)

	err = s.driver.VolumeSnapshotRename(c.Project(), c.Name(), newName,
		driver.VolumeTypeContainerSnapshot)
	if err != nil {
		return err
	}

	// It might be, that the driver already renamed the path.
	if shared.PathExists(oldSnapshotMntPoint) {
		err = os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
		if err != nil {
			return err
		}
	}

	success = true

	return nil
}

func (s *Storage) ContainerSnapshotStart(c Instance) (bool, error) {
	var err error
	ctx := log.Ctx{
		"driver":    s.sTypeName,
		"pool":      s.pool.Name,
		"container": c.Name(),
	}
	success := false

	defer logAction(
		"Starting container snapshot",
		"Started container snapshot",
		"Failed to start container snapshot",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return false, errors.Wrap(err, "Mount storage pool")
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	ok, err := s.driver.VolumeMount(c.Project(), c.Name(), driver.VolumeTypeContainerSnapshot)
	if err != nil {
		return ok, err
	}

	success = true

	return ok, nil
}

func (s *Storage) ContainerSnapshotStop(c Instance) (bool, error) {
	var err error
	ctx := log.Ctx{
		"driver":    s.sTypeName,
		"pool":      s.pool.Name,
		"container": c.Name(),
	}
	success := false

	defer logAction(
		"Stopping container snapshot",
		"Stopped container snapshot",
		"Failed to stop container snapshot",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return false, errors.Wrap(err, "Mount storage pool")
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	ok, err := s.driver.VolumeUmount(c.Project(), c.Name(), driver.VolumeTypeContainerSnapshot)
	if err != nil {
		return ok, err
	}

	success = true

	return ok, nil
}

func (s *Storage) ImageCreate(fingerprint string, tracker *ioprogress.ProgressTracker) error {
	var err error
	ctx := log.Ctx{
		"driver":      s.sTypeName,
		"pool":        s.pool.Name,
		"fingerprint": fingerprint,
	}
	success := false

	defer logAction(
		"Creating image",
		"Created image",
		"Failed to create image",
		&ctx, &success, &err)()

	cleanupFunc := driver.LockImageCreate(s.pool.Name, fingerprint)
	if cleanupFunc == nil {
		success = true
		return nil
	}
	defer cleanupFunc()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return errors.Wrap(err, "Mount storage pool")
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	// Don't create image if it already exists
	if shared.PathExists(driver.GetImageMountPoint(s.pool.Name, fingerprint)) {
		success = true
		return nil
	}

	err = s.createImageDbPoolVolume(fingerprint)
	if err != nil {
		return errors.Wrap(err, "Create image db pool volume")
	}

	undo := true
	defer func() {
		if undo {
			s.deleteImageDbPoolVolume(fingerprint)
		}
	}()

	imageSourcePath := shared.VarPath("images", fingerprint)
	imageVolumePath := driver.GetImageMountPoint(s.pool.Name, "")

	if !shared.PathExists(imageVolumePath) {
		err = os.MkdirAll(imageVolumePath, driver.ImagesDirMode)
		if err != nil {
			return errors.Wrap(err, "Create image mount point")
		}
	}

	volumeName := fingerprint
	imageTargetPath := driver.GetImageMountPoint(s.pool.Name, volumeName)

	err = s.driver.VolumeCreate("default", volumeName, driver.VolumeTypeImage)
	if err != nil {
		return errors.Wrap(err, "Create volume")
	}

	ourMount, err = s.driver.VolumeMount("default", volumeName, driver.VolumeTypeImage)
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.VolumeUmount("default", volumeName, driver.VolumeTypeImage)
	}

	err = unpackImage(imageSourcePath, imageTargetPath, s.sType, s.s.OS.RunningInUserNS, tracker)
	if err != nil {
		return errors.Wrap(err, "Unpack image")
	}

	undo = false
	success = true

	return nil
}

func (s *Storage) ImageDelete(fingerprint string) error {
	var err error
	ctx := log.Ctx{
		"driver":      s.sTypeName,
		"pool":        s.pool.Name,
		"fingerprint": fingerprint,
	}
	success := false

	defer logAction(
		"Deleting image",
		"Deleted image",
		"Failed to delete image",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if err != nil {
		return err
	}

	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	err = s.deleteImageDbPoolVolume(fingerprint)
	if err != nil {
		return err
	}

	imageMntPoint := driver.GetImageMountPoint(s.pool.Name, fingerprint)

	err = s.driver.VolumeDelete("default", fingerprint, false, driver.VolumeTypeImage)
	if err != nil {
		return err
	}

	// Now delete the mountpoint for the image:
	// ${LXD_DIR}/images/<fingerprint>.
	if shared.PathExists(imageMntPoint) {
		err := os.RemoveAll(imageMntPoint)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	success = true

	return nil
}

func (s *Storage) StorageEntitySetQuota(volumeType int, size int64, data interface{}) error {
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return fmt.Errorf("Invalid storage type")
	}

	var c container
	var subvol string
	var volType driver.VolumeType

	project := "default"

	switch volumeType {
	case storagePoolVolumeTypeContainer:
		c = data.(container)
		subvol = c.Name()
		volType = driver.VolumeTypeContainer
		project = c.Project()
	case storagePoolVolumeTypeCustom:
		subvol = s.volume.Name
		volType = driver.VolumeTypeCustom
	}

	return s.driver.VolumeSetQuota(project, subvol, size, s.s.OS.RunningInUserNS, volType)
}

func (s *Storage) ContainerBackupCreate(backup backup, source Instance) error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"source": source.Name(),
	}
	success := false

	defer logAction(
		"Creating container backup",
		"Created container backup",
		"Failed to create container backup",
		&ctx, &success, &err)()

	// Start storage
	ourStart, err := source.StorageStart()
	if err != nil {
		return err
	}

	if ourStart {
		defer source.StorageStop()
	}

	// Create a temporary path for the backup
	tmpPath, err := ioutil.TempDir(shared.VarPath("backups"), "lxd_backup_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpPath)

	var snapshots []string

	if !backup.instanceOnly {
		var snaps []Instance

		snaps, err = source.Snapshots()
		if err != nil {
			return err
		}

		for _, snap := range snaps {
			snapshots = append(snapshots, shared.ExtractSnapshotName(snap.Name()))
		}
	}

	err = s.driver.VolumeBackupCreate(tmpPath, source.Project(), source.Name(), snapshots, backup.optimizedStorage)
	if err != nil {
		return err
	}

	// Pack the backup
	err = backupCreateTarball(s.s, tmpPath, backup)
	if err != nil {
		return err
	}

	success = true

	return nil
}

func (s *Storage) ContainerBackupLoad(info backupInfo, data io.ReadSeeker, tarArgs []string) error {
	var err error
	ctx := log.Ctx{
		"driver": s.sTypeName,
		"pool":   s.pool.Name,
		"backup": info,
	}
	success := false

	defer logAction(
		"Loading container backup",
		"Loaded container backup",
		"Failed to load container backup",
		&ctx, &success, &err)()

	ourMount, err := s.driver.StoragePoolMount()
	if ourMount {
		defer s.driver.StoragePoolUmount()
	}

	if info.HasBinaryFormat {
		containerName, _, _ := shared.ContainerGetParentAndSnapshotName(info.Name)
		containerMntPoint := driver.GetContainerMountPoint("default", s.pool.Name, "")

		/*
			err := driver.CreateContainerMountpoint(containerMntPoint, driver.ContainerPath(info.Project, info.Name, false), info.Privileged)
			if err != nil {
				return err
			}
		*/

		var unpackDir string

		unpackDir, err = ioutil.TempDir(containerMntPoint, containerName)
		if err != nil {
			return err
		}
		defer os.RemoveAll(unpackDir)

		err = os.Chmod(unpackDir, 0700)
		if err != nil {
			return err
		}

		// ${LXD_DIR}/storage-pools/<pool>/containers/<container_name>.XXX/.backup_unpack
		unpackPath := fmt.Sprintf("%s/.backup_unpack", unpackDir)
		err = os.MkdirAll(unpackPath, 0711)
		if err != nil {
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
			logger.Errorf("Failed to untar \"%s\" into \"%s\": %s", "backup", unpackPath, err)
			return err
		}

		err = s.driver.VolumeBackupLoad(unpackDir, info.Project, info.Name,
			info.Snapshots, info.Privileged, info.HasBinaryFormat)
		if err != nil {
			return err
		}

		_, err = s.driver.VolumeMount(info.Project, info.Name, driver.VolumeTypeContainer)
		if err != nil {
			return err
		}

		success = true

		return nil
	}

	containersPath := driver.GetContainerMountPoint("default", s.pool.Name, "")

	if !shared.PathExists(containersPath) {
		err = os.MkdirAll(containersPath, driver.ContainersDirMode)
		if err != nil {
			return err
		}
	}

	containerMntPoint := driver.GetContainerMountPoint(info.Project, s.pool.Name, info.Name)

	// Create the mountpoint for the container at:
	// ${LXD_DIR}/containers/<name>
	err = driver.CreateContainerMountpoint(containerMntPoint,
		driver.ContainerPath(project.Prefix(info.Project, info.Name), false),
		info.Privileged)
	if err != nil {
		return err
	}

	// create the main container
	err = s.driver.VolumeCreate(info.Project, info.Name,
		driver.VolumeTypeContainer)
	if err != nil {
		return err
	}

	_, err = s.driver.VolumeMount(info.Project, info.Name, driver.VolumeTypeContainer)
	if err != nil {
		return err
	}

	// Extract container
	for _, snap := range info.Snapshots {
		cur := fmt.Sprintf("backup/snapshots/%s", snap)

		// Prepare tar arguments
		args := append(tarArgs, []string{
			"-",
			"--recursive-unlink",
			"--xattrs-include=*",
			"--strip-components=3",
			"-C", containerMntPoint, cur,
		}...)

		// Extract snapshots
		data.Seek(0, 0)
		err = shared.RunCommandWithFds(data, nil, "tar", args...)
		if err != nil {
			logger.Errorf("Failed to untar \"%s\" into \"%s\": %s", cur, containerMntPoint, err)
			return err
		}

		// create snapshot
		fullSnapshotName := fmt.Sprintf("%s/%s", info.Name, snap)

		snapshotPath := driver.GetSnapshotMountPoint(info.Project, s.pool.Name, info.Name)
		if !shared.PathExists(snapshotPath) {
			err = os.MkdirAll(snapshotPath, driver.ContainersDirMode)
			if err != nil {
				return err
			}
		}

		snapshotMntPoint := driver.GetSnapshotMountPoint(info.Project, s.pool.Name, info.Name)
		snapshotMntPointSymlink := shared.VarPath("snapshots",
			project.Prefix(info.Project, info.Name))

		err = driver.CreateSnapshotMountpoint(snapshotMntPoint, snapshotMntPoint, snapshotMntPointSymlink)
		if err != nil {
			return err
		}

		err = s.driver.VolumeSnapshotCreate(info.Project, info.Name, fullSnapshotName,
			driver.VolumeTypeContainerSnapshot)
		if err != nil {
			return err
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
		return err
	}

	success = true

	return nil
}

func (s *Storage) MigrationType() migration.MigrationFSType {
	return migration.MigrationFSType_RSYNC
}

func (s *Storage) PreservesInodes() bool {
	return false
}

func (s *Storage) MigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	return rsyncMigrationSource(args)
}

func (s *Storage) MigrationSink(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error {
	return rsyncMigrationSink(conn, op, args)
}

func (s *Storage) StorageMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	return rsyncStorageMigrationSource(args)
}

func (s *Storage) StorageMigrationSink(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error {
	return rsyncStorageMigrationSink(conn, op, args)
}

func (s *Storage) createImageDbPoolVolume(fingerprint string) error {
	// Fill in any default volume config.
	volumeConfig := map[string]string{}
	err := storageVolumeFillDefault(fingerprint, volumeConfig, s.pool)
	if err != nil {
		return err
	}

	// Create a db entry for the storage volume of the image.
	_, err = s.s.Cluster.StoragePoolVolumeCreate("default", fingerprint, "", storagePoolVolumeTypeImage, false, s.poolID, volumeConfig)
	if err != nil {
		// Try to delete the db entry on error.
		s.deleteImageDbPoolVolume(fingerprint)
		return err
	}

	return nil
}

func (s *Storage) deleteImageDbPoolVolume(fingerprint string) error {
	err := s.s.Cluster.StoragePoolVolumeDelete("default", fingerprint, storagePoolVolumeTypeImage, s.poolID)
	if err != nil {
		return err
	}

	return nil
}

// The storage interface defines the functions needed to implement a storage
// backend for a given storage driver.
type storage interface {
	// Functions dealing with basic driver properties only.
	StorageCoreInit() error
	GetStorageType() storageType
	GetStorageTypeName() string
	GetStorageTypeVersion() string
	GetState() *state.State

	// Functions dealing with storage pools.
	StoragePoolInit() error
	StoragePoolCheck() error
	StoragePoolCreate() error
	StoragePoolDelete() error
	StoragePoolMount() (bool, error)
	StoragePoolUmount() (bool, error)
	StoragePoolResources() (*api.ResourcesStoragePool, error)
	StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error
	GetStoragePoolWritable() api.StoragePoolPut
	SetStoragePoolWritable(writable *api.StoragePoolPut)
	GetStoragePool() *api.StoragePool

	// Functions dealing with custom storage volumes.
	StoragePoolVolumeCreate() error
	StoragePoolVolumeDelete() error
	StoragePoolVolumeMount() (bool, error)
	StoragePoolVolumeUmount() (bool, error)
	StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error
	StoragePoolVolumeRename(newName string) error
	StoragePoolVolumeCopy(source *api.StorageVolumeSource) error
	GetStoragePoolVolumeWritable() api.StorageVolumePut
	SetStoragePoolVolumeWritable(writable *api.StorageVolumePut)
	GetStoragePoolVolume() *api.StorageVolume

	// Functions dealing with custom storage volume snapshots.
	StoragePoolVolumeSnapshotCreate(target *api.StorageVolumeSnapshotsPost) error
	StoragePoolVolumeSnapshotDelete() error
	StoragePoolVolumeSnapshotRename(newName string) error

	// Functions dealing with container storage volumes.
	// ContainerCreate creates an empty container (no rootfs/metadata.yaml)
	ContainerCreate(container Instance) error

	// ContainerCreateFromImage creates a container from a image.
	ContainerCreateFromImage(c Instance, fingerprint string, tracker *ioprogress.ProgressTracker) error
	ContainerDelete(c Instance) error
	ContainerCopy(target Instance, source Instance, containerOnly bool) error
	ContainerRefresh(target Instance, source Instance, snapshots []Instance) error
	ContainerMount(c Instance) (bool, error)
	ContainerUmount(c Instance, path string) (bool, error)
	ContainerRename(container Instance, newName string) error
	ContainerRestore(container Instance, sourceContainer Instance) error
	ContainerGetUsage(container Instance) (int64, error)
	GetContainerPoolInfo() (int64, string, string)
	ContainerStorageReady(container Instance) bool

	ContainerSnapshotCreate(target Instance, source Instance) error
	ContainerSnapshotDelete(c Instance) error
	ContainerSnapshotRename(c Instance, newName string) error
	ContainerSnapshotStart(c Instance) (bool, error)
	ContainerSnapshotStop(c Instance) (bool, error)

	ContainerBackupCreate(backup backup, sourceContainer Instance) error
	ContainerBackupLoad(info backupInfo, data io.ReadSeeker, tarArgs []string) error

	// For use in migrating snapshots.
	ContainerSnapshotCreateEmpty(c Instance) error

	// Functions dealing with image storage volumes.
	ImageCreate(fingerprint string, tracker *ioprogress.ProgressTracker) error
	ImageDelete(fingerprint string) error

	// Storage type agnostic functions.
	StorageEntitySetQuota(volumeType int, size int64, data interface{}) error

	// Functions dealing with migration.
	MigrationType() migration.MigrationFSType
	// Does this storage backend preserve inodes when it is moved across LXD
	// hosts?
	PreservesInodes() bool

	// Get the pieces required to migrate the source. This contains a list
	// of the "object" (i.e. container or snapshot, depending on whether or
	// not it is a snapshot name) to be migrated in order, and a channel
	// for arguments of the specific migration command. We use a channel
	// here so we don't have to invoke `zfs send` or `rsync` or whatever
	// and keep its stdin/stdout open for each snapshot during the course
	// of migration, we can do it lazily.
	//
	// N.B. that the order here important: e.g. in btrfs/zfs, snapshots
	// which are parents of other snapshots should be sent first, to save
	// as much transfer as possible. However, the base container is always
	// sent as the first object, since that is the grandparent of every
	// snapshot.
	//
	// We leave sending containers which are snapshots of other containers
	// already present on the target instance as an exercise for the
	// enterprising developer.
	MigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error)
	MigrationSink(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error

	StorageMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error)
	StorageMigrationSink(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error
}

func storageCoreInit(driver string) (storage, error) {
	sType, err := storageStringToType(driver)
	if err != nil {
		return nil, err
	}

	switch sType {
	case storageTypeBtrfs:
		btrfs := storageBtrfs{}
		err = btrfs.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &btrfs, nil
	case storageTypeDir:
		return storageCoreInit2(driver)
	case storageTypeCeph:
		ceph := storageCeph{}
		err = ceph.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &ceph, nil
	case storageTypeCephFs:
		cephfs := storageCephFs{}
		err = cephfs.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &cephfs, nil
	case storageTypeLvm:
		lvm := storageLvm{}
		err = lvm.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &lvm, nil
	case storageTypeMock:
		mock := storageMock{}
		err = mock.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &mock, nil
	case storageTypeZfs:
		zfs := storageZfs{}
		err = zfs.StorageCoreInit()
		if err != nil {
			return nil, err
		}
		return &zfs, nil
	}

	return nil, fmt.Errorf("invalid storage type")
}

func storageCoreInit2(storageDriver string) (storage, error) {
	var err error

	st := Storage{}

	st.driver, err = driver.Init(storageDriver, nil, nil, -1, nil)
	if err != nil {
		return nil, err
	}

	return &st, nil
}

func storageInit(s *state.State, project, poolName, volumeName string, volumeType int) (storage, error) {
	// Load the storage pool.
	poolID, pool, err := s.Cluster.StoragePoolGet(poolName)
	if err != nil {
		return nil, errors.Wrapf(err, "Load storage pool %q", poolName)
	}

	driver := pool.Driver
	if driver == "" {
		// This shouldn't actually be possible but better safe than
		// sorry.
		return nil, fmt.Errorf("no storage driver was provided")
	}

	// Load the storage volume.
	volume := &api.StorageVolume{}
	if volumeName != "" {
		_, volume, err = s.Cluster.StoragePoolNodeVolumeGetTypeByProject(project, volumeName, volumeType, poolID)
		if err != nil {
			return nil, err
		}
	}

	sType, err := storageStringToType(driver)
	if err != nil {
		return nil, err
	}

	switch sType {
	case storageTypeBtrfs:
		btrfs := storageBtrfs{}
		btrfs.poolID = poolID
		btrfs.pool = pool
		btrfs.volume = volume
		btrfs.s = s
		err = btrfs.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &btrfs, nil
	case storageTypeDir:
		return storageInit2(s, project, poolName, volumeName, volumeType)
	case storageTypeCeph:
		ceph := storageCeph{}
		ceph.poolID = poolID
		ceph.pool = pool
		ceph.volume = volume
		ceph.s = s
		err = ceph.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &ceph, nil
	case storageTypeCephFs:
		cephfs := storageCephFs{}
		cephfs.poolID = poolID
		cephfs.pool = pool
		cephfs.volume = volume
		cephfs.s = s
		err = cephfs.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &cephfs, nil
	case storageTypeLvm:
		lvm := storageLvm{}
		lvm.poolID = poolID
		lvm.pool = pool
		lvm.volume = volume
		lvm.s = s
		err = lvm.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &lvm, nil
	case storageTypeMock:
		mock := storageMock{}
		mock.poolID = poolID
		mock.pool = pool
		mock.volume = volume
		mock.s = s
		err = mock.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &mock, nil
	case storageTypeZfs:
		zfs := storageZfs{}
		zfs.poolID = poolID
		zfs.pool = pool
		zfs.volume = volume
		zfs.s = s
		err = zfs.StoragePoolInit()
		if err != nil {
			return nil, err
		}
		return &zfs, nil
	}

	return nil, fmt.Errorf("invalid storage type")
}

func storageInit2(s *state.State, project, poolName, volumeName string, volumeType int) (storage, error) {
	// Load the storage pool.
	poolID, pool, err := s.Cluster.StoragePoolGet(poolName)
	if err != nil {
		return nil, errors.Wrapf(err, "Load storage pool %q", poolName)
	}

	if pool.Driver == "" {
		// This shouldn't actually be possible but better safe than
		// sorry.
		return nil, fmt.Errorf("no storage driver was provided")
	}

	// Load the storage volume.
	volume := &api.StorageVolume{}
	volumeID := int64(-1)
	if volumeName != "" {
		volumeID, volume, err = s.Cluster.StoragePoolNodeVolumeGetTypeByProject(project, volumeName, volumeType, poolID)
		if err != nil {
			return nil, err
		}
	}

	sType, err := storageStringToType(pool.Driver)
	if err != nil {
		return nil, err
	}

	st := Storage{}
	st.poolID = poolID
	st.pool = pool
	st.volumeID = volumeID
	st.volume = volume
	st.s = s
	st.sType = sType
	st.sTypeName = pool.Driver

	st.driver, err = driver.Init(pool.Driver, s, pool, poolID, volume)
	if err != nil {
		return nil, err
	}

	return &st, nil
}

func storagePoolInit(s *state.State, poolName string) (storage, error) {
	return storageInit(s, "default", poolName, "", -1)
}

func storagePoolVolumeAttachInit(s *state.State, poolName string, volumeName string, volumeType int, i Instance) (storage, error) {
	st, err := storageInit(s, "default", poolName, volumeName, volumeType)
	if err != nil {
		return nil, err
	}

	c, ok := i.(*containerLXC)
	if !ok {
		return st, nil
	}

	// Check if unmapped
	poolVolumePut := st.GetStoragePoolVolumeWritable()
	if shared.IsTrue(poolVolumePut.Config["security.unmapped"]) {
		// No need to look at containers and maps for unmapped volumes
		return st, nil
	}

	// Get the on-disk idmap for the volume
	var lastIdmap *idmap.IdmapSet
	if poolVolumePut.Config["volatile.idmap.last"] != "" {
		lastIdmap, err = idmapsetFromString(poolVolumePut.Config["volatile.idmap.last"])
		if err != nil {
			logger.Errorf("Failed to unmarshal last idmapping: %s", poolVolumePut.Config["volatile.idmap.last"])
			return nil, err
		}
	}

	var nextIdmap *idmap.IdmapSet
	nextJsonMap := "[]"
	if !shared.IsTrue(poolVolumePut.Config["security.shifted"]) {
		// Get the container's idmap
		if c.IsRunning() {
			nextIdmap, err = c.CurrentIdmap()
		} else {
			nextIdmap, err = c.NextIdmap()
		}
		if err != nil {
			return nil, err
		}

		if nextIdmap != nil {
			nextJsonMap, err = idmapsetToJSON(nextIdmap)
			if err != nil {
				return nil, err
			}
		}
	}
	poolVolumePut.Config["volatile.idmap.next"] = nextJsonMap

	// get mountpoint of storage volume
	remapPath := driver.GetStoragePoolVolumeMountPoint(poolName, volumeName)

	if !nextIdmap.Equals(lastIdmap) {
		logger.Debugf("Shifting storage volume")

		if !shared.IsTrue(poolVolumePut.Config["security.shifted"]) {
			volumeUsedBy, err := storagePoolVolumeUsedByContainersGet(s, "default", poolName, volumeName)
			if err != nil {
				return nil, err
			}

			if len(volumeUsedBy) > 1 {
				for _, ctName := range volumeUsedBy {
					instt, err := instanceLoadByProjectAndName(s, c.Project(), ctName)
					if err != nil {
						continue
					}

					if instt.Type() != instancetype.Container {
						continue
					}

					ct := instt.(container)

					var ctNextIdmap *idmap.IdmapSet
					if ct.IsRunning() {
						ctNextIdmap, err = ct.CurrentIdmap()
					} else {
						ctNextIdmap, err = ct.NextIdmap()
					}
					if err != nil {
						return nil, fmt.Errorf("Failed to retrieve idmap of container")
					}

					if !nextIdmap.Equals(ctNextIdmap) {
						return nil, fmt.Errorf("Idmaps of container %v and storage volume %v are not identical", ctName, volumeName)
					}
				}
			} else if len(volumeUsedBy) == 1 {
				// If we're the only one who's attached that container
				// we can shift the storage volume.
				// I'm not sure if we want some locking here.
				if volumeUsedBy[0] != c.Name() {
					return nil, fmt.Errorf("idmaps of container and storage volume are not identical")
				}
			}
		}

		// mount storage volume
		ourMount, err := st.StoragePoolVolumeMount()
		if err != nil {
			return nil, err
		}
		if ourMount {
			defer func() {
				_, err := st.StoragePoolVolumeUmount()
				if err != nil {
					logger.Warnf("Failed to unmount storage volume")
				}
			}()
		}

		// unshift rootfs
		if lastIdmap != nil {
			var err error

			if st.GetStorageType() == storageTypeZfs {
				err = lastIdmap.UnshiftRootfs(remapPath, zfsIdmapSetSkipper)
			} else {
				err = lastIdmap.UnshiftRootfs(remapPath, nil)
			}
			if err != nil {
				logger.Errorf("Failed to unshift \"%s\"", remapPath)
				return nil, err
			}
			logger.Debugf("Unshifted \"%s\"", remapPath)
		}

		// shift rootfs
		if nextIdmap != nil {
			var err error

			if st.GetStorageType() == storageTypeZfs {
				err = nextIdmap.ShiftRootfs(remapPath, zfsIdmapSetSkipper)
			} else {
				err = nextIdmap.ShiftRootfs(remapPath, nil)
			}
			if err != nil {
				logger.Errorf("Failed to shift \"%s\"", remapPath)
				return nil, err
			}
			logger.Debugf("Shifted \"%s\"", remapPath)
		}
		logger.Debugf("Shifted storage volume")
	}

	jsonIdmap := "[]"
	if nextIdmap != nil {
		var err error
		jsonIdmap, err = idmapsetToJSON(nextIdmap)
		if err != nil {
			logger.Errorf("Failed to marshal idmap")
			return nil, err
		}
	}

	// update last idmap
	poolVolumePut.Config["volatile.idmap.last"] = jsonIdmap

	st.SetStoragePoolVolumeWritable(&poolVolumePut)

	poolID, err := s.Cluster.StoragePoolGetID(poolName)
	if err != nil {
		return nil, err
	}
	err = s.Cluster.StoragePoolVolumeUpdate(volumeName, volumeType, poolID, poolVolumePut.Description, poolVolumePut.Config)
	if err != nil {
		return nil, err
	}

	return st, nil
}

func storagePoolVolumeInit(s *state.State, project, poolName, volumeName string, volumeType int) (storage, error) {
	// No need to detect storage here, its a new container.
	return storageInit(s, project, poolName, volumeName, volumeType)
}

func storagePoolVolumeImageInit(s *state.State, poolName string, imageFingerprint string) (storage, error) {
	return storagePoolVolumeInit(s, "default", poolName, imageFingerprint, storagePoolVolumeTypeImage)
}

func storagePoolVolumeContainerCreateInit(s *state.State, project string, poolName string, containerName string) (storage, error) {
	return storagePoolVolumeInit(s, project, poolName, containerName, storagePoolVolumeTypeContainer)
}

func storagePoolVolumeContainerLoadInit(s *state.State, project, containerName string) (storage, error) {
	// Get the storage pool of a given container.
	poolName, err := s.Cluster.ContainerPool(project, containerName)
	if err != nil {
		return nil, errors.Wrapf(err, "Load storage pool for container %q in project %q", containerName, project)
	}

	return storagePoolVolumeInit(s, project, poolName, containerName, storagePoolVolumeTypeContainer)
}

func deleteContainerMountpoint(mountPoint string, mountPointSymlink string, storageTypeName string) error {
	if shared.PathExists(mountPointSymlink) {
		err := os.Remove(mountPointSymlink)
		if err != nil {
			return err
		}
	}

	if shared.PathExists(mountPoint) {
		err := os.Remove(mountPoint)
		if err != nil {
			return err
		}
	}

	if storageTypeName == "" {
		return nil
	}

	mntPointSuffix := storageTypeName
	oldStyleMntPointSymlink := fmt.Sprintf("%s.%s", mountPointSymlink,
		mntPointSuffix)
	if shared.PathExists(oldStyleMntPointSymlink) {
		err := os.Remove(oldStyleMntPointSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

func renameContainerMountpoint(oldMountPoint string, oldMountPointSymlink string, newMountPoint string, newMountPointSymlink string) error {
	if shared.PathExists(oldMountPoint) {
		err := os.Rename(oldMountPoint, newMountPoint)
		if err != nil {
			return err
		}
	}

	// Rename the symlink target.
	if shared.PathExists(oldMountPointSymlink) {
		err := os.Remove(oldMountPointSymlink)
		if err != nil {
			return err
		}
	}

	// Create the new symlink.
	err := os.Symlink(newMountPoint, newMountPointSymlink)
	if err != nil {
		return err
	}

	return nil
}

func deleteSnapshotMountpoint(snapshotMountpoint string, snapshotsSymlinkTarget string, snapshotsSymlink string) error {
	if shared.PathExists(snapshotMountpoint) {
		err := os.Remove(snapshotMountpoint)
		if err != nil {
			return err
		}
	}

	couldRemove := false
	if shared.PathExists(snapshotsSymlinkTarget) {
		err := os.Remove(snapshotsSymlinkTarget)
		if err == nil {
			couldRemove = true
		}
	}

	if couldRemove && shared.PathExists(snapshotsSymlink) {
		err := os.Remove(snapshotsSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

func resetContainerDiskIdmap(container container, srcIdmap *idmap.IdmapSet) error {
	dstIdmap, err := container.DiskIdmap()
	if err != nil {
		return err
	}

	if dstIdmap == nil {
		dstIdmap = new(idmap.IdmapSet)
	}

	if !srcIdmap.Equals(dstIdmap) {
		var jsonIdmap string
		if srcIdmap != nil {
			idmapBytes, err := json.Marshal(srcIdmap.Idmap)
			if err != nil {
				return err
			}
			jsonIdmap = string(idmapBytes)
		} else {
			jsonIdmap = "[]"
		}

		err := container.VolatileSet(map[string]string{"volatile.last_state.idmap": jsonIdmap})
		if err != nil {
			return err
		}
	}

	return nil
}

func progressWrapperRender(op *operations.Operation, key string, description string, progressInt int64, speedInt int64) {
	meta := op.Metadata()
	if meta == nil {
		meta = make(map[string]interface{})
	}

	progress := fmt.Sprintf("%s (%s/s)", units.GetByteSizeString(progressInt, 2), units.GetByteSizeString(speedInt, 2))
	if description != "" {
		progress = fmt.Sprintf("%s: %s (%s/s)", description, units.GetByteSizeString(progressInt, 2), units.GetByteSizeString(speedInt, 2))
	}

	if meta[key] != progress {
		meta[key] = progress
		op.UpdateMetadata(meta)
	}
}

// StorageProgressReader reports the read progress.
func StorageProgressReader(op *operations.Operation, key string, description string) func(io.ReadCloser) io.ReadCloser {
	return func(reader io.ReadCloser) io.ReadCloser {
		if op == nil {
			return reader
		}

		progress := func(progressInt int64, speedInt int64) {
			progressWrapperRender(op, key, description, progressInt, speedInt)
		}

		readPipe := &ioprogress.ProgressReader{
			ReadCloser: reader,
			Tracker: &ioprogress.ProgressTracker{
				Handler: progress,
			},
		}

		return readPipe
	}
}

// StorageProgressWriter reports the write progress.
func StorageProgressWriter(op *operations.Operation, key string, description string) func(io.WriteCloser) io.WriteCloser {
	return func(writer io.WriteCloser) io.WriteCloser {
		if op == nil {
			return writer
		}

		progress := func(progressInt int64, speedInt int64) {
			progressWrapperRender(op, key, description, progressInt, speedInt)
		}

		writePipe := &ioprogress.ProgressWriter{
			WriteCloser: writer,
			Tracker: &ioprogress.ProgressTracker{
				Handler: progress,
			},
		}

		return writePipe
	}
}

func SetupStorageDriver(s *state.State, forceCheck bool) error {
	pools, err := s.Cluster.StoragePoolsNotPending()
	if err != nil {
		if err == db.ErrNoSuchObject {
			logger.Debugf("No existing storage pools detected")
			return nil
		}
		logger.Debugf("Failed to retrieve existing storage pools")
		return err
	}

	// In case the daemon got killed during upgrade we will already have a
	// valid storage pool entry but it might have gotten messed up and so we
	// cannot perform StoragePoolCheck(). This case can be detected by
	// looking at the patches db: If we already have a storage pool defined
	// but the upgrade somehow got messed up then there will be no
	// "storage_api" entry in the db.
	if len(pools) > 0 && !forceCheck {
		appliedPatches, err := s.Node.Patches()
		if err != nil {
			return err
		}

		if !shared.StringInSlice("storage_api", appliedPatches) {
			logger.Warnf("Incorrectly applied \"storage_api\" patch, skipping storage pool initialization as it might be corrupt")
			return nil
		}

	}

	for _, pool := range pools {
		logger.Debugf("Initializing and checking storage pool \"%s\"", pool)
		s, err := storagePoolInit(s, pool)
		if err != nil {
			logger.Errorf("Error initializing storage pool \"%s\": %s, correct functionality of the storage pool cannot be guaranteed", pool, err)
			continue
		}

		err = s.StoragePoolCheck()
		if err != nil {
			return err
		}
	}

	// Update the storage drivers cache in api_1.0.go.
	storagePoolDriversCacheUpdate(s.Cluster)
	return nil
}

func storagePoolDriversCacheUpdate(cluster *db.Cluster) {
	// Get a list of all storage drivers currently in use
	// on this LXD instance. Only do this when we do not already have done
	// this once to avoid unnecessarily querying the db. All subsequent
	// updates of the cache will be done when we create or delete storage
	// pools in the db. Since this is a rare event, this cache
	// implementation is a classic frequent-read, rare-update case so
	// copy-on-write semantics without locking in the read case seems
	// appropriate. (Should be cheaper then querying the db all the time,
	// especially if we keep adding more storage drivers.)

	drivers, err := cluster.StoragePoolsGetDrivers()
	if err != nil && err != db.ErrNoSuchObject {
		return
	}

	data := map[string]string{}
	for _, driver := range drivers {
		// Initialize a core storage interface for the given driver.
		sCore, err := storageCoreInit(driver)
		if err != nil {
			continue
		}

		// Grab the version
		data[driver] = sCore.GetStorageTypeVersion()
	}

	backends := []string{}
	for k, v := range data {
		backends = append(backends, fmt.Sprintf("%s %s", k, v))
	}

	// Update the agent
	version.UserAgentStorageBackends(backends)

	storagePoolDriversCacheLock.Lock()
	storagePoolDriversCacheVal.Store(data)
	storagePoolDriversCacheLock.Unlock()

	return
}

// storageVolumeMount initialises a new storage interface and checks the pool and volume are
// mounted. If they are not then they are mounted.
func storageVolumeMount(state *state.State, poolName string, volumeName string, volumeTypeName string, instance device.Instance) error {
	c, ok := instance.(*containerLXC)
	if !ok {
		return fmt.Errorf("Received non-LXC container instance")
	}

	volumeType, _ := storagePoolVolumeTypeNameToType(volumeTypeName)
	s, err := storagePoolVolumeAttachInit(state, poolName, volumeName, volumeType, c)
	if err != nil {
		return err
	}

	_, err = s.StoragePoolVolumeMount()
	if err != nil {
		return err
	}

	return nil
}

// storageVolumeUmount unmounts a storage volume on a pool.
func storageVolumeUmount(state *state.State, poolName string, volumeName string, volumeType int) error {
	// Custom storage volumes do not currently support projects, so hardcode "default" project.
	s, err := storagePoolVolumeInit(state, "default", poolName, volumeName, volumeType)
	if err != nil {
		return err
	}

	_, err = s.StoragePoolVolumeUmount()
	if err != nil {
		return err
	}

	return nil
}

// storageRootFSApplyQuota applies a quota to an instance if it can, if it cannot then it will
// return false indicating that the quota needs to be stored in volatile to be applied on next boot.
func storageRootFSApplyQuota(instance device.Instance, newSizeBytes int64) (bool, error) {
	c, ok := instance.(*containerLXC)
	if !ok {
		return false, fmt.Errorf("Received non-LXC container instance")
	}

	err := c.initStorage()
	if err != nil {
		return false, errors.Wrap(err, "Initialize storage")
	}

	storageTypeName := c.storage.GetStorageTypeName()
	storageIsReady := c.storage.ContainerStorageReady(c)

	// If we cannot apply the quota now, then return false as needs to be applied on next boot.
	if (storageTypeName == "lvm" || storageTypeName == "ceph") && c.IsRunning() || !storageIsReady {
		return false, nil
	}

	err = c.storage.StorageEntitySetQuota(storagePoolVolumeTypeContainer, newSizeBytes, c)
	if err != nil {
		return false, errors.Wrap(err, "Set storage quota")
	}

	return true, nil
}
