package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
)

type storageBtrfs struct {
	remount uintptr
	storageShared
}

var btrfsVersion = ""

func (s *storageBtrfs) getBtrfsMountOptions() string {
	if s.pool.Config["btrfs.mount_options"] != "" {
		return s.pool.Config["btrfs.mount_options"]
	}

	return "user_subvol_rm_allowed"
}

func (s *storageBtrfs) setBtrfsMountOptions(mountOptions string) {
	s.pool.Config["btrfs.mount_options"] = mountOptions
}

// ${LXD_DIR}/storage-pools/<pool>/containers
func (s *storageBtrfs) getContainerSubvolumePath(poolName string) string {
	return shared.VarPath("storage-pools", poolName, "containers")
}

// ${LXD_DIR}/storage-pools/<pool>/containers-snapshots
func getSnapshotSubvolumePath(poolName string, containerName string) string {
	return shared.VarPath("storage-pools", poolName, "containers-snapshots", containerName)
}

// ${LXD_DIR}/storage-pools/<pool>/images
func (s *storageBtrfs) getImageSubvolumePath(poolName string) string {
	return shared.VarPath("storage-pools", poolName, "images")
}

// ${LXD_DIR}/storage-pools/<pool>/custom
func (s *storageBtrfs) getCustomSubvolumePath(poolName string) string {
	return shared.VarPath("storage-pools", poolName, "custom")
}

// ${LXD_DIR}/storage-pools/<pool>/custom-snapshots
func (s *storageBtrfs) getCustomSnapshotSubvolumePath(poolName string) string {
	return shared.VarPath("storage-pools", poolName, "custom-snapshots")
}

func (s *storageBtrfs) StorageCoreInit() error {
	s.sType = storageTypeBtrfs
	typeName, err := storageTypeToString(s.sType)
	if err != nil {
		return err
	}
	s.sTypeName = typeName

	if btrfsVersion != "" {
		s.sTypeVersion = btrfsVersion
		return nil
	}

	out, err := exec.LookPath("btrfs")
	if err != nil || len(out) == 0 {
		return fmt.Errorf("The 'btrfs' tool isn't available")
	}

	output, err := shared.RunCommand("btrfs", "version")
	if err != nil {
		return fmt.Errorf("The 'btrfs' tool isn't working properly")
	}

	count, err := fmt.Sscanf(strings.SplitN(output, " ", 2)[1], "v%s\n", &s.sTypeVersion)
	if err != nil || count != 1 {
		return fmt.Errorf("The 'btrfs' tool isn't working properly")
	}

	btrfsVersion = s.sTypeVersion

	return nil
}

func (s *storageBtrfs) StoragePoolInit() error {
	err := s.StorageCoreInit()
	if err != nil {
		return err
	}

	return nil
}

func (s *storageBtrfs) StoragePoolCheck() error {
	// FIXEM(brauner): Think of something smart or useful (And then think
	// again if it is worth implementing it. :)).
	logger.Debugf("Checking BTRFS storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageBtrfs) StoragePoolCreate() error {
	logger.Infof("Creating BTRFS storage pool \"%s\"", s.pool.Name)
	s.pool.Config["volatile.initial_source"] = s.pool.Config["source"]

	isBlockDev := false

	source := s.pool.Config["source"]
	if strings.HasPrefix(source, "/") {
		source = shared.HostPath(s.pool.Config["source"])
	}

	if source == "" {
		source = filepath.Join(shared.VarPath("disks"), fmt.Sprintf("%s.img", s.pool.Name))
		s.pool.Config["source"] = source

		f, err := os.Create(source)
		if err != nil {
			return fmt.Errorf("Failed to open %s: %s", source, err)
		}
		defer f.Close()

		err = f.Chmod(0600)
		if err != nil {
			return fmt.Errorf("Failed to chmod %s: %s", source, err)
		}

		size, err := shared.ParseByteSizeString(s.pool.Config["size"])
		if err != nil {
			return err
		}
		err = f.Truncate(size)
		if err != nil {
			return fmt.Errorf("Failed to create sparse file %s: %s", source, err)
		}

		output, err := makeFSType(source, "btrfs", &mkfsOptions{label: s.pool.Name})
		if err != nil {
			return fmt.Errorf("Failed to create the BTRFS pool: %s", output)
		}
	} else {
		// Unset size property since it doesn't make sense.
		s.pool.Config["size"] = ""

		if filepath.IsAbs(source) {
			isBlockDev = shared.IsBlockdevPath(source)
			if isBlockDev {
				output, err := makeFSType(source, "btrfs", &mkfsOptions{label: s.pool.Name})
				if err != nil {
					return fmt.Errorf("Failed to create the BTRFS pool: %s", output)
				}
			} else {
				if isBtrfsSubVolume(source) {
					subvols, err := btrfsSubVolumesGet(source)
					if err != nil {
						return fmt.Errorf("could not determine if existing BTRFS subvolume ist empty: %s", err)
					}
					if len(subvols) > 0 {
						return fmt.Errorf("requested BTRFS subvolume exists but is not empty")
					}
				} else {
					cleanSource := filepath.Clean(source)
					lxdDir := shared.VarPath()
					poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
					if shared.PathExists(source) && !isOnBtrfs(source) {
						return fmt.Errorf("existing path is neither a BTRFS subvolume nor does it reside on a BTRFS filesystem")
					} else if strings.HasPrefix(cleanSource, lxdDir) {
						if cleanSource != poolMntPoint {
							return fmt.Errorf("BTRFS subvolumes requests in LXD directory \"%s\" are only valid under \"%s\"\n(e.g. source=%s)", shared.VarPath(), shared.VarPath("storage-pools"), poolMntPoint)
						} else if s.s.OS.BackingFS != "btrfs" {
							return fmt.Errorf("creation of BTRFS subvolume requested but \"%s\" does not reside on BTRFS filesystem", source)
						}
					}

					err := btrfsSubVolumeCreate(source)
					if err != nil {
						return err
					}
				}
			}
		} else {
			return fmt.Errorf("invalid \"source\" property")
		}
	}

	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	if !shared.PathExists(poolMntPoint) {
		err := os.MkdirAll(poolMntPoint, storagePoolsDirMode)
		if err != nil {
			return err
		}
	}

	var err1 error
	var devUUID string
	mountFlags, mountOptions := lxdResolveMountoptions(s.getBtrfsMountOptions())
	mountFlags |= s.remount
	if isBlockDev && filepath.IsAbs(source) {
		devUUID, _ = shared.LookupUUIDByBlockDevPath(source)
		// The symlink might not have been created even with the delay
		// we granted it above. So try to call btrfs filesystem show and
		// parse it out. (I __hate__ this!)
		if devUUID == "" {
			logger.Warnf("Failed to detect UUID by looking at /dev/disk/by-uuid")
			devUUID, err1 = s.btrfsLookupFsUUID(source)
			if err1 != nil {
				logger.Errorf("Failed to detect UUID by parsing filesystem info")
				return err1
			}
		}
		s.pool.Config["source"] = devUUID

		// If the symlink in /dev/disk/by-uuid hasn't been created yet
		// aka we only detected it by parsing btrfs filesystem show, we
		// cannot call StoragePoolMount() since it will try to do the
		// reverse operation. So instead we shamelessly mount using the
		// block device path at the time of pool creation.
		err1 = syscall.Mount(source, poolMntPoint, "btrfs", mountFlags, mountOptions)
	} else {
		_, err1 = s.StoragePoolMount()
	}
	if err1 != nil {
		return err1
	}

	// Create default subvolumes.
	dummyDir := getContainerMountPoint(s.pool.Name, "")
	err := btrfsSubVolumeCreate(dummyDir)
	if err != nil {
		return fmt.Errorf("Could not create btrfs subvolume: %s", dummyDir)
	}

	dummyDir = getSnapshotMountPoint(s.pool.Name, "")
	err = btrfsSubVolumeCreate(dummyDir)
	if err != nil {
		return fmt.Errorf("Could not create btrfs subvolume: %s", dummyDir)
	}

	dummyDir = getImageMountPoint(s.pool.Name, "")
	err = btrfsSubVolumeCreate(dummyDir)
	if err != nil {
		return fmt.Errorf("Could not create btrfs subvolume: %s", dummyDir)
	}

	dummyDir = getStoragePoolVolumeMountPoint(s.pool.Name, "")
	err = btrfsSubVolumeCreate(dummyDir)
	if err != nil {
		return fmt.Errorf("Could not create btrfs subvolume: %s", dummyDir)
	}

	dummyDir = getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, "")
	err = btrfsSubVolumeCreate(dummyDir)
	if err != nil {
		return fmt.Errorf("Could not create btrfs subvolume: %s", dummyDir)
	}

	err = s.StoragePoolCheck()
	if err != nil {
		return err
	}

	logger.Infof("Created BTRFS storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageBtrfs) StoragePoolDelete() error {
	logger.Infof("Deleting BTRFS storage pool \"%s\"", s.pool.Name)

	source := s.pool.Config["source"]
	if strings.HasPrefix(source, "/") {
		source = shared.HostPath(s.pool.Config["source"])
	}

	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	// Delete default subvolumes.
	dummyDir := getContainerMountPoint(s.pool.Name, "")
	btrfsSubVolumesDelete(dummyDir)

	dummyDir = getSnapshotMountPoint(s.pool.Name, "")
	btrfsSubVolumesDelete(dummyDir)

	dummyDir = getImageMountPoint(s.pool.Name, "")
	btrfsSubVolumesDelete(dummyDir)

	dummyDir = getStoragePoolVolumeMountPoint(s.pool.Name, "")
	btrfsSubVolumesDelete(dummyDir)

	_, err := s.StoragePoolUmount()
	if err != nil {
		return err
	}

	// This is a UUID. Check whether we can find the block device.
	if !filepath.IsAbs(source) {
		// Try to lookup the disk device by UUID but don't fail. If we
		// don't find one this might just mean we have been given the
		// UUID of a subvolume.
		byUUID := fmt.Sprintf("/dev/disk/by-uuid/%s", source)
		diskPath, err := os.Readlink(byUUID)
		msg := ""
		if err == nil {
			msg = fmt.Sprintf("Removing disk device %s with UUID: %s.", diskPath, source)
		} else {
			msg = fmt.Sprintf("Failed to lookup disk device with UUID: %s: %s.", source, err)
		}
		logger.Debugf(msg)
	} else {
		var err error
		cleanSource := filepath.Clean(source)
		sourcePath := shared.VarPath("disks", s.pool.Name)
		loopFilePath := sourcePath + ".img"
		if cleanSource == loopFilePath {
			// This is a loop file so simply remove it.
			err = os.Remove(source)
		} else {
			if !isBtrfsFilesystem(source) && isBtrfsSubVolume(source) {
				err = btrfsSubVolumesDelete(source)
			}
		}
		if err != nil {
			return err
		}
	}

	// Remove the mountpoint for the storage pool.
	os.RemoveAll(getStoragePoolMountPoint(s.pool.Name))

	logger.Infof("Deleted BTRFS storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageBtrfs) StoragePoolMount() (bool, error) {
	logger.Debugf("Mounting BTRFS storage pool \"%s\"", s.pool.Name)

	source := s.pool.Config["source"]
	if strings.HasPrefix(source, "/") {
		source = shared.HostPath(s.pool.Config["source"])
	}

	if source == "" {
		return false, fmt.Errorf("no \"source\" property found for the storage pool")
	}

	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)

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

	// Check whether the mount poolMntPoint exits.
	if !shared.PathExists(poolMntPoint) {
		err := os.MkdirAll(poolMntPoint, storagePoolsDirMode)
		if err != nil {
			return false, err
		}
	}

	if shared.IsMountPoint(poolMntPoint) && (s.remount&syscall.MS_REMOUNT) == 0 {
		return false, nil
	}

	mountFlags, mountOptions := lxdResolveMountoptions(s.getBtrfsMountOptions())
	mountSource := source
	isBlockDev := shared.IsBlockdevPath(source)
	if filepath.IsAbs(source) {
		cleanSource := filepath.Clean(source)
		poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
		loopFilePath := shared.VarPath("disks", s.pool.Name+".img")
		if !isBlockDev && cleanSource == loopFilePath {
			// If source == "${LXD_DIR}"/disks/{pool_name} it is a
			// loop file we're dealing with.
			//
			// Since we mount the loop device LO_FLAGS_AUTOCLEAR is
			// fine since the loop device will be kept around for as
			// long as the mount exists.
			loopF, loopErr := prepareLoopDev(source, LoFlagsAutoclear)
			if loopErr != nil {
				return false, loopErr
			}
			mountSource = loopF.Name()
			defer loopF.Close()
		} else if !isBlockDev && cleanSource != poolMntPoint {
			mountSource = source
			mountFlags |= syscall.MS_BIND
		} else if !isBlockDev && cleanSource == poolMntPoint && s.s.OS.BackingFS == "btrfs" {
			return false, nil
		}
		// User is using block device path.
	} else {
		// Try to lookup the disk device by UUID but don't fail. If we
		// don't find one this might just mean we have been given the
		// UUID of a subvolume.
		byUUID := fmt.Sprintf("/dev/disk/by-uuid/%s", source)
		diskPath, err := os.Readlink(byUUID)
		if err == nil {
			mountSource = fmt.Sprintf("/dev/%s", strings.Trim(diskPath, "../../"))
		} else {
			// We have very likely been given a subvolume UUID. In
			// this case we should simply assume that the user has
			// mounted the parent of the subvolume or the subvolume
			// itself. Otherwise this becomes a really messy
			// detection task.
			return false, nil
		}
	}

	mountFlags |= s.remount
	err := syscall.Mount(mountSource, poolMntPoint, "btrfs", mountFlags, mountOptions)
	if err != nil {
		logger.Errorf("Failed to mount BTRFS storage pool \"%s\" onto \"%s\" with mountoptions \"%s\": %s", mountSource, poolMntPoint, mountOptions, err)
		return false, err
	}

	logger.Debugf("Mounted BTRFS storage pool \"%s\"", s.pool.Name)
	return true, nil
}

func (s *storageBtrfs) StoragePoolUmount() (bool, error) {
	logger.Debugf("Unmounting BTRFS storage pool \"%s\"", s.pool.Name)

	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)

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

	if shared.IsMountPoint(poolMntPoint) {
		err := syscall.Unmount(poolMntPoint, 0)
		if err != nil {
			return false, err
		}
	}

	logger.Debugf("Unmounted BTRFS storage pool \"%s\"", s.pool.Name)
	return true, nil
}

func (s *storageBtrfs) StoragePoolUpdate(writable *api.StoragePoolPut,
	changedConfig []string) error {
	logger.Infof(`Updating BTRFS storage pool "%s"`, s.pool.Name)

	changeable := changeableStoragePoolProperties["btrfs"]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		return updateStoragePoolError(unchangeable, "btrfs")
	}

	// "rsync.bwlimit" requires no on-disk modifications.

	if shared.StringInSlice("btrfs.mount_options", changedConfig) {
		s.setBtrfsMountOptions(writable.Config["btrfs.mount_options"])
		s.remount |= syscall.MS_REMOUNT
		_, err := s.StoragePoolMount()
		if err != nil {
			return err
		}
	}

	logger.Infof(`Updated BTRFS storage pool "%s"`, s.pool.Name)
	return nil
}

func (s *storageBtrfs) GetStoragePoolWritable() api.StoragePoolPut {
	return s.pool.Writable()
}

func (s *storageBtrfs) SetStoragePoolWritable(writable *api.StoragePoolPut) {
	s.pool.StoragePoolPut = *writable
}

func (s *storageBtrfs) GetContainerPoolInfo() (int64, string, string) {
	return s.poolID, s.pool.Name, s.pool.Name
}

// Functions dealing with storage volumes.
func (s *storageBtrfs) StoragePoolVolumeCreate() error {
	logger.Infof("Creating BTRFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Create subvolume path on the storage pool.
	customSubvolumePath := s.getCustomSubvolumePath(s.pool.Name)
	if !shared.PathExists(customSubvolumePath) {
		err := os.MkdirAll(customSubvolumePath, 0700)
		if err != nil {
			return err
		}
	}

	// Create subvolume.
	customSubvolumeName := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	err = btrfsSubVolumeCreate(customSubvolumeName)
	if err != nil {
		return err
	}

	// apply quota
	if s.volume.Config["size"] != "" {
		size, err := shared.ParseByteSizeString(s.volume.Config["size"])
		if err != nil {
			return err
		}

		err = s.StorageEntitySetQuota(storagePoolVolumeTypeCustom, size, nil)
		if err != nil {
			return err
		}
	}

	logger.Infof("Created BTRFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageBtrfs) StoragePoolVolumeDelete() error {
	logger.Infof("Deleting BTRFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Delete subvolume.
	customSubvolumeName := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	if shared.PathExists(customSubvolumeName) && isBtrfsSubVolume(customSubvolumeName) {
		err = btrfsSubVolumesDelete(customSubvolumeName)
		if err != nil {
			return err
		}
	}

	// Delete the mountpoint.
	if shared.PathExists(customSubvolumeName) {
		err = os.Remove(customSubvolumeName)
		if err != nil {
			return err
		}
	}

	err = s.s.Cluster.StoragePoolVolumeDelete(
		s.volume.Name,
		storagePoolVolumeTypeCustom,
		s.poolID)
	if err != nil {
		logger.Errorf(`Failed to delete database entry for BTRFS storage volume "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)
	}

	logger.Infof("Deleted BTRFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageBtrfs) StoragePoolVolumeMount() (bool, error) {
	logger.Debugf("Mounting BTRFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	// The storage pool must be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return false, err
	}

	logger.Debugf("Mounted BTRFS storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return true, nil
}

func (s *storageBtrfs) StoragePoolVolumeUmount() (bool, error) {
	return true, nil
}

func (s *storageBtrfs) StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error {
	logger.Infof(`Updating BTRFS storage volume "%s"`, s.pool.Name)

	changeable := changeableStoragePoolVolumeProperties["btrfs"]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		return updateStoragePoolVolumeError(unchangeable, "btrfs")
	}

	if shared.StringInSlice("size", changedConfig) {
		if s.volume.Type != storagePoolVolumeTypeNameCustom {
			return updateStoragePoolVolumeError([]string{"size"}, "btrfs")
		}

		if s.volume.Config["size"] != writable.Config["size"] {
			size, err := shared.ParseByteSizeString(writable.Config["size"])
			if err != nil {
				return err
			}

			err = s.StorageEntitySetQuota(storagePoolVolumeTypeCustom, size, nil)
			if err != nil {
				return err
			}
		}
	}

	logger.Infof(`Updated BTRFS storage volume "%s"`, s.pool.Name)
	return nil
}

func (s *storageBtrfs) StoragePoolVolumeRename(newName string) error {
	logger.Infof(`Renaming BTRFS storage volume on storage pool "%s" from "%s" to "%s`,
		s.pool.Name, s.volume.Name, newName)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	usedBy, err := storagePoolVolumeUsedByContainersGet(s.s, s.volume.Name, storagePoolVolumeTypeNameCustom)
	if err != nil {
		return err
	}
	if len(usedBy) > 0 {
		return fmt.Errorf(`BTRFS storage volume "%s" on storage pool "%s" is attached to containers`,
			s.volume.Name, s.pool.Name)
	}

	oldPath := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	newPath := getStoragePoolVolumeMountPoint(s.pool.Name, newName)
	err = os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	logger.Infof(`Renamed BTRFS storage volume on storage pool "%s" from "%s" to "%s`,
		s.pool.Name, s.volume.Name, newName)

	return s.s.Cluster.StoragePoolVolumeRename(s.volume.Name, newName,
		storagePoolVolumeTypeCustom, s.poolID)
}

func (s *storageBtrfs) GetStoragePoolVolumeWritable() api.StorageVolumePut {
	return s.volume.Writable()
}

func (s *storageBtrfs) SetStoragePoolVolumeWritable(writable *api.StorageVolumePut) {
	s.volume.StorageVolumePut = *writable
}

// Functions dealing with container storage.
func (s *storageBtrfs) ContainerStorageReady(name string) bool {
	containerMntPoint := getContainerMountPoint(s.pool.Name, name)
	return isBtrfsSubVolume(containerMntPoint)
}

func (s *storageBtrfs) doContainerCreate(name string, privileged bool) error {
	logger.Debugf("Creating empty BTRFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// We can only create the btrfs subvolume under the mounted storage
	// pool. The on-disk layout for containers on a btrfs storage pool will
	// thus be
	// ${LXD_DIR}/storage-pools/<pool>/containers/. The btrfs tool will
	// complain if the intermediate path does not exist, so create it if it
	// doesn't already.
	containerSubvolumePath := s.getContainerSubvolumePath(s.pool.Name)
	if !shared.PathExists(containerSubvolumePath) {
		err := os.MkdirAll(containerSubvolumePath, containersDirMode)
		if err != nil {
			return err
		}
	}

	// Create empty subvolume for container.
	containerSubvolumeName := getContainerMountPoint(s.pool.Name, name)
	err = btrfsSubVolumeCreate(containerSubvolumeName)
	if err != nil {
		return err
	}

	// Create the mountpoint for the container at:
	// ${LXD_DIR}/containers/<name>
	err = createContainerMountpoint(containerSubvolumeName, shared.VarPath("containers", name), privileged)
	if err != nil {
		return err
	}

	logger.Debugf("Created empty BTRFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageBtrfs) ContainerCreate(container container) error {
	err := s.doContainerCreate(container.Name(), container.IsPrivileged())
	if err != nil {
		return err
	}

	return container.TemplateApply("create")
}

// And this function is why I started hating on btrfs...
func (s *storageBtrfs) ContainerCreateFromImage(container container, fingerprint string) error {
	logger.Debugf("Creating BTRFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// We can only create the btrfs subvolume under the mounted storage
	// pool. The on-disk layout for containers on a btrfs storage pool will
	// thus be
	// ${LXD_DIR}/storage-pools/<pool>/containers/. The btrfs tool will
	// complain if the intermediate path does not exist, so create it if it
	// doesn't already.
	containerSubvolumePath := s.getContainerSubvolumePath(s.pool.Name)
	if !shared.PathExists(containerSubvolumePath) {
		err := os.MkdirAll(containerSubvolumePath, containersDirMode)
		if err != nil {
			return err
		}
	}

	// Mountpoint of the image:
	// ${LXD_DIR}/images/<fingerprint>
	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
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
		if !shared.PathExists(imageMntPoint) || !isBtrfsSubVolume(imageMntPoint) {
			imgerr = s.ImageCreate(fingerprint)
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

	// Create a rw snapshot at
	// ${LXD_DIR}/storage-pools/<pool>/containers/<name>
	// from the mounted ro image snapshot mounted at
	// ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>
	containerSubvolumeName := getContainerMountPoint(s.pool.Name, container.Name())
	err = s.btrfsPoolVolumesSnapshot(imageMntPoint, containerSubvolumeName, false, false)
	if err != nil {
		return err
	}

	// Create the mountpoint for the container at:
	// ${LXD_DIR}/containers/<name>
	err = createContainerMountpoint(containerSubvolumeName, container.Path(), container.IsPrivileged())
	if err != nil {
		return err
	}

	if !container.IsPrivileged() {
		err := s.shiftRootfs(container, nil)
		if err != nil {
			s.ContainerDelete(container)
			return err
		}
	}

	logger.Debugf("Created BTRFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return container.TemplateApply("create")
}

func (s *storageBtrfs) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageBtrfs) ContainerDelete(container container) error {
	logger.Debugf("Deleting BTRFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	// The storage pool needs to be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Delete the subvolume.
	containerSubvolumeName := getContainerMountPoint(s.pool.Name, container.Name())
	if shared.PathExists(containerSubvolumeName) && isBtrfsSubVolume(containerSubvolumeName) {
		err = btrfsSubVolumesDelete(containerSubvolumeName)
		if err != nil {
			return err
		}
	}

	// Delete the container's symlink to the subvolume.
	err = deleteContainerMountpoint(containerSubvolumeName, container.Path(), s.GetStorageTypeName())
	if err != nil {
		return err
	}

	// Delete potential snapshot mountpoints.
	snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, container.Name())
	if shared.PathExists(snapshotMntPoint) {
		err := os.RemoveAll(snapshotMntPoint)
		if err != nil {
			return err
		}
	}

	// Delete potential symlink
	// ${LXD_DIR}/snapshots/<container_name> to ${POOL}/snapshots/<container_name>
	snapshotSymlink := shared.VarPath("snapshots", container.Name())
	if shared.PathExists(snapshotSymlink) {
		err := os.Remove(snapshotSymlink)
		if err != nil {
			return err
		}
	}

	backups, err := container.Backups()
	if err != nil {
		return err
	}

	for _, backup := range backups {
		backupName := strings.Split(backup.Name(), "/")[1]
		s.ContainerBackupDelete(backupName)
	}

	logger.Debugf("Deleted BTRFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageBtrfs) copyContainer(target container, source container) error {
	sourceContainerSubvolumeName := getContainerMountPoint(s.pool.Name, source.Name())
	if source.IsSnapshot() {
		sourceContainerSubvolumeName = getSnapshotMountPoint(s.pool.Name, source.Name())
	}
	targetContainerSubvolumeName := getContainerMountPoint(s.pool.Name, target.Name())

	containersPath := getContainerMountPoint(s.pool.Name, "")
	// Ensure that the directories immediately preceding the subvolume directory exist.
	if !shared.PathExists(containersPath) {
		err := os.MkdirAll(containersPath, containersDirMode)
		if err != nil {
			return err
		}
	}

	err := s.btrfsPoolVolumesSnapshot(sourceContainerSubvolumeName, targetContainerSubvolumeName, false, true)
	if err != nil {
		return err
	}

	err = createContainerMountpoint(targetContainerSubvolumeName, target.Path(), target.IsPrivileged())
	if err != nil {
		return err
	}

	err = s.setUnprivUserACL(source, targetContainerSubvolumeName)
	if err != nil {
		s.ContainerDelete(target)
		return err
	}

	err = target.TemplateApply("copy")
	if err != nil {
		return err
	}

	return nil
}

func (s *storageBtrfs) copySnapshot(target container, source container) error {
	sourceName := source.Name()
	targetName := target.Name()
	sourceContainerSubvolumeName := getSnapshotMountPoint(s.pool.Name, sourceName)
	targetContainerSubvolumeName := getSnapshotMountPoint(s.pool.Name, targetName)

	targetParentName, _, _ := containerGetParentAndSnapshotName(target.Name())
	containersPath := getSnapshotMountPoint(s.pool.Name, targetParentName)
	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", targetParentName)
	snapshotMntPointSymlink := shared.VarPath("snapshots", targetParentName)
	err := createSnapshotMountpoint(containersPath, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	if err != nil {
		return err
	}

	// Ensure that the directories immediately preceding the subvolume directory exist.
	if !shared.PathExists(containersPath) {
		err := os.MkdirAll(containersPath, containersDirMode)
		if err != nil {
			return err
		}
	}

	err = s.btrfsPoolVolumesSnapshot(sourceContainerSubvolumeName, targetContainerSubvolumeName, false, true)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageBtrfs) doCrossPoolContainerCopy(target container, source container, containerOnly bool) error {
	sourcePool, err := source.StoragePool()
	if err != nil {
		return err
	}

	// setup storage for the source volume
	srcStorage, err := storagePoolVolumeInit(s.s, sourcePool, source.Name(), storagePoolVolumeTypeContainer)
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

	snapshots, err := source.Snapshots()
	if err != nil {
		return err
	}

	// create the main container
	err = s.doContainerCreate(target.Name(), target.IsPrivileged())
	if err != nil {
		return err
	}

	destContainerMntPoint := getContainerMountPoint(targetPool, target.Name())
	bwlimit := s.pool.Config["rsync.bwlimit"]
	if !containerOnly {
		for _, snap := range snapshots {
			srcSnapshotMntPoint := getSnapshotMountPoint(sourcePool, snap.Name())
			_, err = rsyncLocalCopy(srcSnapshotMntPoint, destContainerMntPoint, bwlimit)
			if err != nil {
				logger.Errorf("Failed to rsync into BTRFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
				return err
			}

			// create snapshot
			_, snapOnlyName, _ := containerGetParentAndSnapshotName(snap.Name())
			err = s.doContainerSnapshotCreate(fmt.Sprintf("%s/%s", target.Name(), snapOnlyName), target.Name())
			if err != nil {
				return err
			}
		}
	}

	srcContainerMntPoint := getContainerMountPoint(sourcePool, source.Name())
	_, err = rsyncLocalCopy(srcContainerMntPoint, destContainerMntPoint, bwlimit)
	if err != nil {
		logger.Errorf("Failed to rsync into BTRFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	return nil
}

func (s *storageBtrfs) ContainerCopy(target container, source container, containerOnly bool) error {
	logger.Debugf("Copying BTRFS container storage %s to %s", source.Name(), target.Name())

	// The storage pool needs to be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

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
		return s.doCrossPoolContainerCopy(target, source, containerOnly)
	}

	err = s.copyContainer(target, source)
	if err != nil {
		return err
	}

	if containerOnly {
		logger.Debugf("Copied BTRFS container storage %s to %s", source.Name(), target.Name())
		return nil
	}

	snapshots, err := source.Snapshots()
	if err != nil {
		return err
	}

	if len(snapshots) == 0 {
		logger.Debugf("Copied BTRFS container storage %s to %s", source.Name(), target.Name())
		return nil
	}

	for _, snap := range snapshots {
		sourceSnapshot, err := containerLoadByName(s.s, snap.Name())
		if err != nil {
			return err
		}

		_, snapOnlyName, _ := containerGetParentAndSnapshotName(snap.Name())
		newSnapName := fmt.Sprintf("%s/%s", target.Name(), snapOnlyName)
		targetSnapshot, err := containerLoadByName(s.s, newSnapName)
		if err != nil {
			return err
		}

		err = s.copySnapshot(targetSnapshot, sourceSnapshot)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Copied BTRFS container storage %s to %s", source.Name(), target.Name())
	return nil
}

func (s *storageBtrfs) ContainerMount(c container) (bool, error) {
	logger.Debugf("Mounting BTRFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	// The storage pool must be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return false, err
	}

	logger.Debugf("Mounted BTRFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return true, nil
}

func (s *storageBtrfs) ContainerUmount(name string, path string) (bool, error) {
	return true, nil
}

func (s *storageBtrfs) ContainerRename(container container, newName string) error {
	logger.Debugf("Renaming BTRFS storage volume for container \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newName)

	// The storage pool must be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	oldContainerSubvolumeName := getContainerMountPoint(s.pool.Name, container.Name())
	newContainerSubvolumeName := getContainerMountPoint(s.pool.Name, newName)
	err = os.Rename(oldContainerSubvolumeName, newContainerSubvolumeName)
	if err != nil {
		return err
	}

	newSymlink := shared.VarPath("containers", newName)
	err = renameContainerMountpoint(oldContainerSubvolumeName, container.Path(), newContainerSubvolumeName, newSymlink)
	if err != nil {
		return err
	}

	oldSnapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, container.Name())
	newSnapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, newName)
	if shared.PathExists(oldSnapshotSubvolumeName) {
		err = os.Rename(oldSnapshotSubvolumeName, newSnapshotSubvolumeName)
		if err != nil {
			return err
		}
	}

	oldSnapshotSymlink := shared.VarPath("snapshots", container.Name())
	newSnapshotSymlink := shared.VarPath("snapshots", newName)
	if shared.PathExists(oldSnapshotSymlink) {
		err := os.Remove(oldSnapshotSymlink)
		if err != nil {
			return err
		}

		err = os.Symlink(newSnapshotSubvolumeName, newSnapshotSymlink)
		if err != nil {
			return err
		}
	}

	backups, err := container.Backups()
	if err != nil {
		return err
	}

	for _, backup := range backups {
		backupName := strings.Split(backup.Name(), "/")[1]
		newName := fmt.Sprintf("%s/%s", newName, backupName)
		s.ContainerBackupRename(backup, newName)
	}

	logger.Debugf("Renamed BTRFS storage volume for container \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newName)
	return nil
}

func (s *storageBtrfs) ContainerRestore(container container, sourceContainer container) error {
	logger.Debugf("Restoring BTRFS storage volume for container \"%s\" from %s to %s", s.volume.Name, sourceContainer.Name(), container.Name())

	// The storage pool must be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Create a backup so we can revert.
	targetContainerSubvolumeName := getContainerMountPoint(s.pool.Name, container.Name())
	backupTargetContainerSubvolumeName := fmt.Sprintf("%s.back", targetContainerSubvolumeName)
	err = os.Rename(targetContainerSubvolumeName, backupTargetContainerSubvolumeName)
	if err != nil {
		return err
	}
	undo := true
	defer func() {
		if undo {
			os.Rename(backupTargetContainerSubvolumeName, targetContainerSubvolumeName)
		}
	}()

	ourStart, err := sourceContainer.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer sourceContainer.StorageStop()
	}

	// Mount the source container.
	srcContainerStorage := sourceContainer.Storage()
	_, sourcePool, _ := srcContainerStorage.GetContainerPoolInfo()
	sourceContainerSubvolumeName := ""
	if sourceContainer.IsSnapshot() {
		sourceContainerSubvolumeName = getSnapshotMountPoint(sourcePool, sourceContainer.Name())
	} else {
		sourceContainerSubvolumeName = getContainerMountPoint(sourcePool, sourceContainer.Name())
	}

	var failure error
	_, targetPool, _ := s.GetContainerPoolInfo()
	if targetPool == sourcePool {
		// They are on the same storage pool, so we can simply snapshot.
		err := s.btrfsPoolVolumesSnapshot(sourceContainerSubvolumeName, targetContainerSubvolumeName, false, true)
		if err != nil {
			failure = err
		}
	} else {
		err := btrfsSubVolumeCreate(targetContainerSubvolumeName)
		if err == nil {
			// Use rsync to fill the empty volume.  Sync by using
			// the subvolume name.
			bwlimit := s.pool.Config["rsync.bwlimit"]
			output, err := rsyncLocalCopy(sourceContainerSubvolumeName, targetContainerSubvolumeName, bwlimit)
			if err != nil {
				s.ContainerDelete(container)
				logger.Errorf("ContainerRestore: rsync failed: %s", string(output))
				failure = err
			}
		} else {
			failure = err
		}
	}

	// Now allow unprivileged users to access its data.
	err = s.setUnprivUserACL(sourceContainer, targetContainerSubvolumeName)
	if err != nil {
		failure = err
	}

	if failure == nil {
		undo = false
		_, sourcePool, _ := srcContainerStorage.GetContainerPoolInfo()
		_, targetPool, _ := s.GetContainerPoolInfo()
		if targetPool == sourcePool {
			// Remove the backup, we made
			return btrfsSubVolumesDelete(backupTargetContainerSubvolumeName)
		}
		os.RemoveAll(backupTargetContainerSubvolumeName)
	}

	logger.Debugf("Restored BTRFS storage volume for container \"%s\" from %s to %s", s.volume.Name, sourceContainer.Name(), container.Name())
	return failure
}

func (s *storageBtrfs) ContainerGetUsage(container container) (int64, error) {
	return s.btrfsPoolVolumeQGroupUsage(container.Path())
}

func (s *storageBtrfs) doContainerSnapshotCreate(targetName string, sourceName string) error {
	logger.Debugf("Creating BTRFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// We can only create the btrfs subvolume under the mounted storage
	// pool. The on-disk layout for snapshots on a btrfs storage pool will
	// thus be
	// ${LXD_DIR}/storage-pools/<pool>/snapshots/. The btrfs tool will
	// complain if the intermediate path does not exist, so create it if it
	// doesn't already.
	snapshotSubvolumePath := getSnapshotSubvolumePath(s.pool.Name, sourceName)
	if !shared.PathExists(snapshotSubvolumePath) {
		err := os.MkdirAll(snapshotSubvolumePath, containersDirMode)
		if err != nil {
			return err
		}
	}

	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", s.volume.Name)
	snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
	if !shared.PathExists(snapshotMntPointSymlink) {
		if !shared.PathExists(snapshotMntPointSymlinkTarget) {
			err = os.MkdirAll(snapshotMntPointSymlinkTarget, snapshotsDirMode)
			if err != nil {
				return err
			}
		}

		err := os.Symlink(snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return err
		}
	}

	srcContainerSubvolumeName := getContainerMountPoint(s.pool.Name, sourceName)
	snapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, targetName)
	err = s.btrfsPoolVolumesSnapshot(srcContainerSubvolumeName, snapshotSubvolumeName, true, true)
	if err != nil {
		return err
	}

	logger.Debugf("Created BTRFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageBtrfs) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	err := s.doContainerSnapshotCreate(snapshotContainer.Name(), sourceContainer.Name())
	if err != nil {
		s.ContainerSnapshotDelete(snapshotContainer)
		return err
	}

	return nil
}

func btrfsSnapshotDeleteInternal(poolName string, snapshotName string) error {
	snapshotSubvolumeName := getSnapshotMountPoint(poolName, snapshotName)
	if shared.PathExists(snapshotSubvolumeName) && isBtrfsSubVolume(snapshotSubvolumeName) {
		err := btrfsSubVolumesDelete(snapshotSubvolumeName)
		if err != nil {
			return err
		}
	}

	sourceSnapshotMntPoint := shared.VarPath("snapshots", snapshotName)
	os.Remove(sourceSnapshotMntPoint)
	os.Remove(snapshotSubvolumeName)

	sourceName, _, _ := containerGetParentAndSnapshotName(snapshotName)
	snapshotSubvolumePath := getSnapshotSubvolumePath(poolName, sourceName)
	os.Remove(snapshotSubvolumePath)
	if !shared.PathExists(snapshotSubvolumePath) {
		snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
		os.Remove(snapshotMntPointSymlink)
	}

	return nil
}

func (s *storageBtrfs) ContainerSnapshotDelete(snapshotContainer container) error {
	logger.Debugf("Deleting BTRFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	err = btrfsSnapshotDeleteInternal(s.pool.Name, snapshotContainer.Name())
	if err != nil {
		return err
	}

	logger.Debugf("Deleted BTRFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageBtrfs) ContainerSnapshotStart(container container) (bool, error) {
	logger.Debugf("Initializing BTRFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return false, err
	}

	snapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, container.Name())
	roSnapshotSubvolumeName := fmt.Sprintf("%s.ro", snapshotSubvolumeName)
	if shared.PathExists(roSnapshotSubvolumeName) {
		logger.Debugf("The BTRFS snapshot is already mounted read-write")
		return false, nil
	}

	err = os.Rename(snapshotSubvolumeName, roSnapshotSubvolumeName)
	if err != nil {
		return false, err
	}

	err = s.btrfsPoolVolumesSnapshot(roSnapshotSubvolumeName, snapshotSubvolumeName, false, true)
	if err != nil {
		return false, err
	}

	logger.Debugf("Initialized BTRFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return true, nil
}

func (s *storageBtrfs) ContainerSnapshotStop(container container) (bool, error) {
	logger.Debugf("Stopping BTRFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return false, err
	}

	snapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, container.Name())
	roSnapshotSubvolumeName := fmt.Sprintf("%s.ro", snapshotSubvolumeName)
	if !shared.PathExists(roSnapshotSubvolumeName) {
		logger.Debugf("The BTRFS snapshot is currently not mounted read-write")
		return false, nil
	}

	if shared.PathExists(snapshotSubvolumeName) && isBtrfsSubVolume(snapshotSubvolumeName) {
		err = btrfsSubVolumesDelete(snapshotSubvolumeName)
		if err != nil {
			return false, err
		}
	}

	err = os.Rename(roSnapshotSubvolumeName, snapshotSubvolumeName)
	if err != nil {
		return false, err
	}

	logger.Debugf("Stopped BTRFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return true, nil
}

// ContainerSnapshotRename renames a snapshot of a container.
func (s *storageBtrfs) ContainerSnapshotRename(snapshotContainer container, newName string) error {
	logger.Debugf("Renaming BTRFS storage volume for snapshot \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newName)

	// The storage pool must be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Unmount the snapshot if it is mounted otherwise we'll get EBUSY.
	// Rename the subvolume on the storage pool.
	oldSnapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, snapshotContainer.Name())
	newSnapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, newName)
	err = os.Rename(oldSnapshotSubvolumeName, newSnapshotSubvolumeName)
	if err != nil {
		return err
	}

	logger.Debugf("Renamed BTRFS storage volume for snapshot \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newName)
	return nil
}

// Needed for live migration where an empty snapshot needs to be created before
// rsyncing into it.
func (s *storageBtrfs) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	logger.Debugf("Creating empty BTRFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	// Mount the storage pool.
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Create the snapshot subvole path on the storage pool.
	sourceName, _, _ := containerGetParentAndSnapshotName(snapshotContainer.Name())
	snapshotSubvolumePath := getSnapshotSubvolumePath(s.pool.Name, sourceName)
	snapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, snapshotContainer.Name())
	if !shared.PathExists(snapshotSubvolumePath) {
		err := os.MkdirAll(snapshotSubvolumePath, containersDirMode)
		if err != nil {
			return err
		}
	}

	err = btrfsSubVolumeCreate(snapshotSubvolumeName)
	if err != nil {
		return err
	}

	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", sourceName)
	snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
	if !shared.PathExists(snapshotMntPointSymlink) {
		err := createContainerMountpoint(snapshotMntPointSymlinkTarget, snapshotMntPointSymlink, snapshotContainer.IsPrivileged())
		if err != nil {
			return err
		}
	}

	logger.Debugf("Created empty BTRFS storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageBtrfs) doBtrfsBackup(cur string, prev string, target string) error {
	args := []string{"send"}
	if prev != "" {
		args = append(args, "-p", prev)
	}
	args = append(args, cur)

	eater, err := os.OpenFile(target, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer eater.Close()

	btrfsSendCmd := exec.Command("btrfs", args...)
	btrfsSendCmd.Stdout = eater

	err = btrfsSendCmd.Run()
	if err != nil {
		return err
	}

	return err
}

func (s *storageBtrfs) doContainerBackupCreateOptimized(backup backup, source container) error {
	// /var/lib/lxd/storage-pools/<pool>/backups
	baseMntPoint := getBackupMountPoint(s.pool.Name, backup.Name())

	// /var/lib/lxd/storage-pools/<pool>/backups/<container>/container
	err := os.MkdirAll(baseMntPoint, 0711)
	if err != nil {
		logger.Errorf("Failed to create directory \"%s\": %s", baseMntPoint, err)
		return err
	}

	// retrieve snapshots
	snapshots, err := source.Snapshots()
	if err != nil {
		return err
	}

	finalParent := ""
	if !backup.containerOnly && len(snapshots) > 0 {
		// /var/lib/lxd/storage-pools/<pool>/backups/<container>/snapshots
		targetBackupSnapshotsMntPoint := fmt.Sprintf("%s/snapshots", baseMntPoint)
		err = os.MkdirAll(targetBackupSnapshotsMntPoint, 0711)
		if err != nil {
			logger.Errorf("Failed to create directory \"%s\": %s", targetBackupSnapshotsMntPoint, err)
			return err
		}
		logger.Debugf("Created directory \"%s\"", targetBackupSnapshotsMntPoint)

		for i, snap := range snapshots {
			prev := ""
			if i > 0 {
				// /var/lib/lxd/storage-pools/<pool>/containers-snapshots/<container>/<snapshot>
				prev = getSnapshotMountPoint(s.pool.Name, snapshots[i-1].Name())
			}

			// /var/lib/lxd/storage-pools/<pool>/snapshots/<container>/<snapshot>
			cur := getSnapshotMountPoint(s.pool.Name, snap.Name())

			_, snapOnlyName, _ := containerGetParentAndSnapshotName(snap.Name())

			// /var/lib/lxd/storage-pools/<pool>/backups/<container>/snapshots/<snapshot>
			target := fmt.Sprintf("%s/%s.bin", targetBackupSnapshotsMntPoint, snapOnlyName)
			err := s.doBtrfsBackup(cur, prev, target)
			if err != nil {
				logger.Errorf("Failed to btrfs send snapshot \"%s\" -p \"%s\" to \"%s\": %s", cur, prev, target, err)
				return err
			}
			logger.Debugf("Performed btrfs send snapshot \"%s\" -p \"%s\" to \"%s\"", cur, prev, target)

			// /var/lib/lxd/storage-pools/<pool>/snapshots/<container>/<snapshot>
			finalParent = cur
		}
	}

	// /var/lib/lxd/storage-pools/<pool>/containers/<container>
	sourceVolume := getContainerMountPoint(s.pool.Name, source.Name())

	containersPath := getContainerMountPoint(s.pool.Name, "")
	tmpContainerMntPoint, err := ioutil.TempDir(containersPath, source.Name())
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	err = os.Chmod(tmpContainerMntPoint, 0700)
	if err != nil {
		return err
	}
	// /var/lib/lxd/storage-pools/<pool>/containers/<container-tmp>/.backup
	targetVolume := fmt.Sprintf("%s/.backup", tmpContainerMntPoint)

	err = s.btrfsPoolVolumesSnapshot(sourceVolume, targetVolume, true, true)
	if err != nil {
		logger.Errorf("Failed to create read-only btrfs snapshot \"%s\" of \"%s\": %s", targetVolume, sourceVolume, err)
		return err
	}
	defer btrfsSubVolumesDelete(targetVolume)

	// /var/lib/lxd/storage-pools/<pool>/backups/<container>/container/<container>
	fsDump := fmt.Sprintf("%s/container.bin", baseMntPoint)
	err = s.doBtrfsBackup(targetVolume, finalParent, fsDump)
	if err != nil {
		logger.Errorf("Failed to btrfs send container \"%s\" -p \"%s\" to \"%s\": %s", targetVolume, finalParent, fsDump, err)
		return err
	}

	return nil
}

func (s *storageBtrfs) doContainerBackupCreateVanilla(backup backup, source container) error {
	// Create the path for the backup.
	baseMntPoint := getBackupMountPoint(s.pool.Name, backup.Name())
	targetBackupContainerMntPoint := fmt.Sprintf("%s/container", baseMntPoint)
	err := os.MkdirAll(targetBackupContainerMntPoint, 0711)
	if err != nil {
		return err
	}

	// retrieve snapshots
	snapshots, err := source.Snapshots()
	if err != nil {
		return err
	}

	rsync := func(oldPath string, newPath string, bwlimit string) error {
		output, err := rsyncLocalCopy(oldPath, newPath, bwlimit)
		if err != nil {
			s.ContainerBackupDelete(backup.Name())
			return fmt.Errorf("failed to rsync: %s: %s", string(output), err)
		}
		return nil
	}

	bwlimit := s.pool.Config["rsync.bwlimit"]
	if !backup.containerOnly && len(snapshots) > 0 {
		// /var/lib/lxd/storage-pools/<pool>/backups/<container>/snapshots
		targetBackupSnapshotsMntPoint := fmt.Sprintf("%s/snapshots", baseMntPoint)
		err = os.MkdirAll(targetBackupSnapshotsMntPoint, 0711)
		if err != nil {
			logger.Errorf("Failed to create directory \"%s\": %s", targetBackupSnapshotsMntPoint, err)
			return err
		}
		logger.Debugf("Created directory \"%s\"", targetBackupSnapshotsMntPoint)

		for _, snap := range snapshots {
			_, err := s.ContainerSnapshotStart(snap)
			if err != nil {
				return err
			}

			snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, snap.Name())
			_, snapName, _ := containerGetParentAndSnapshotName(snap.Name())
			target := fmt.Sprintf("%s/%s", targetBackupSnapshotsMntPoint, snapName)
			err = rsync(snapshotMntPoint, target, bwlimit)
			s.ContainerSnapshotStop(snap)
			if err != nil {
				return err
			}
		}
	}

	// /var/lib/lxd/storage-pools/<pool>/containers/<container>
	sourceVolume := getContainerMountPoint(s.pool.Name, source.Name())

	containersPath := getContainerMountPoint(s.pool.Name, "")
	tmpContainerMntPoint, err := ioutil.TempDir(containersPath, source.Name())
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	err = os.Chmod(tmpContainerMntPoint, 0700)
	if err != nil {
		return err
	}
	// /var/lib/lxd/storage-pools/<pool>/containers/<container-tmp>/.backup
	targetVolume := fmt.Sprintf("%s/.backup", tmpContainerMntPoint)
	err = s.btrfsPoolVolumesSnapshot(sourceVolume, targetVolume, true, true)
	if err != nil {
		logger.Errorf("Failed to create read-only btrfs snapshot \"%s\" of \"%s\": %s", targetVolume, sourceVolume, err)
		return err
	}
	defer btrfsSubVolumesDelete(targetVolume)

	err = rsync(targetVolume, targetBackupContainerMntPoint, bwlimit)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageBtrfs) ContainerBackupCreate(backup backup, source container) error {
	logger.Debugf("Creating BTRFS storage volume for backup \"%s\" on storage pool \"%s\"", backup.Name(), s.pool.Name)

	// start storage
	ourStart, err := source.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer source.StorageStop()
	}

	if backup.optimizedStorage {
		logger.Debugf("Created BTRFS storage volume for backup \"%s\" on storage pool \"%s\"", backup.Name(), s.pool.Name)
		return s.doContainerBackupCreateOptimized(backup, source)
	}

	return s.doContainerBackupCreateVanilla(backup, source)
}

func (s *storageBtrfs) ContainerBackupDelete(name string) error {
	logger.Debugf("Deleting BTRFS storage volume for backup \"%s\" on storage pool \"%s\"", name, s.pool.Name)
	backupContainerMntPoint := getBackupMountPoint(s.pool.Name, name)
	if shared.PathExists(backupContainerMntPoint) {
		err := os.RemoveAll(backupContainerMntPoint)
		if err != nil {
			return err
		}
	}

	sourceContainerName, _, _ := containerGetParentAndSnapshotName(name)
	backupContainerPath := getBackupMountPoint(s.pool.Name, sourceContainerName)
	empty, _ := shared.PathIsEmpty(backupContainerPath)
	if empty == true {
		err := os.Remove(backupContainerPath)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Deleted BTRFS storage volume for backup \"%s\" on storage pool \"%s\"", name, s.pool.Name)
	return nil
}

func (s *storageBtrfs) ContainerBackupRename(backup backup, newName string) error {
	logger.Debugf("Renaming BTRFS storage volume for backup \"%s\" from %s to %s", backup.Name(), backup.Name(), newName)
	oldBackupMntPoint := getBackupMountPoint(s.pool.Name, backup.Name())
	newBackupMntPoint := getBackupMountPoint(s.pool.Name, newName)

	// Rename directory
	if shared.PathExists(oldBackupMntPoint) {
		err := os.Rename(oldBackupMntPoint, newBackupMntPoint)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Renamed BTRFS storage volume for backup \"%s\" from %s to %s", backup.Name(), backup.Name(), newName)
	return nil
}

func (s *storageBtrfs) ContainerBackupDump(backup backup) ([]byte, error) {
	backupMntPoint := getBackupMountPoint(s.pool.Name, backup.Name())
	logger.Debugf("Taring up \"%s\" on storage pool \"%s\"", backupMntPoint, s.pool.Name)

	args := []string{"-cJf", "-", "--xattrs", "-C", backupMntPoint, "--transform", "s,^./,backup/,"}
	if backup.ContainerOnly() {
		// Exclude snapshots directory
		args = append(args, "--exclude", fmt.Sprintf("%s/snapshots", backup.Name()))
	}
	args = append(args, ".")

	var buffer bytes.Buffer
	err := shared.RunCommandWithFds(nil, &buffer, "tar", args...)
	if err != nil {
		return nil, err
	}

	logger.Debugf("Tared up \"%s\" on storage pool \"%s\"", backupMntPoint, s.pool.Name)
	return buffer.Bytes(), nil
}

func (s *storageBtrfs) doContainerBackupLoadOptimized(info backupInfo, data io.ReadSeeker) error {
	containerName, _, _ := containerGetParentAndSnapshotName(info.Name)

	containerMntPoint := getContainerMountPoint(s.pool.Name, "")
	unpackDir, err := ioutil.TempDir(containerMntPoint, containerName)
	if err != nil {
		return err
	}
	defer os.RemoveAll(unpackDir)

	err = os.Chmod(unpackDir, 0700)
	if err != nil {
		return err
	}

	unpackPath := fmt.Sprintf("%s/.backup_unpack", unpackDir)
	err = os.MkdirAll(unpackPath, 0711)
	if err != nil {
		return err
	}

	// Extract container
	data.Seek(0, 0)
	err = shared.RunCommandWithFds(data, nil, "tar", "-xJf", "-",
		"--strip-components=1", "-C", unpackPath, "backup")
	if err != nil {
		logger.Errorf("Failed to untar \"%s\" into \"%s\": %s", "backup", unpackPath, err)
		return err
	}

	for _, snapshotOnlyName := range info.Snapshots {
		snapshotBackup := fmt.Sprintf("%s/snapshots/%s.bin", unpackPath, snapshotOnlyName)
		feeder, err := os.Open(snapshotBackup)
		if err != nil {
			return err
		}

		// create mountpoint
		snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, containerName)
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", containerName)
		snapshotMntPointSymlink := shared.VarPath("snapshots", containerName)
		err = createSnapshotMountpoint(snapshotMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			feeder.Close()
			return err
		}

		// /var/lib/lxd/storage-pools/<pool>/snapshots/<container>/
		btrfsRecvCmd := exec.Command("btrfs", "receive", "-e", snapshotMntPoint)
		btrfsRecvCmd.Stdin = feeder
		msg, err := btrfsRecvCmd.CombinedOutput()
		feeder.Close()
		if err != nil {
			logger.Errorf("Failed to receive contents of btrfs backup \"%s\": %s", snapshotBackup, string(msg))
			return err
		}
	}

	containerBackupFile := fmt.Sprintf("%s/container.bin", unpackPath)
	feeder, err := os.Open(containerBackupFile)
	if err != nil {
		return err
	}
	defer feeder.Close()

	// /var/lib/lxd/storage-pools/<pool>/containers/
	btrfsRecvCmd := exec.Command("btrfs", "receive", "-vv", "-e", unpackDir)
	btrfsRecvCmd.Stdin = feeder
	msg, err := btrfsRecvCmd.CombinedOutput()
	if err != nil {
		logger.Errorf("Failed to receive contents of btrfs backup \"%s\": %s", containerBackupFile, string(msg))
		return err
	}
	tmpContainerMntPoint := fmt.Sprintf("%s/.backup", unpackDir)
	defer btrfsSubVolumesDelete(tmpContainerMntPoint)

	containerMntPoint = getContainerMountPoint(s.pool.Name, info.Name)
	err = s.btrfsPoolVolumesSnapshot(tmpContainerMntPoint, containerMntPoint, false, true)
	if err != nil {
		logger.Errorf("Failed to create btrfs snapshot \"%s\" of \"%s\": %s", tmpContainerMntPoint, containerMntPoint, err)
		return err
	}

	// Create mountpoints
	err = createContainerMountpoint(containerMntPoint, shared.VarPath("containers", info.Name), info.Privileged)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageBtrfs) doContainerBackupLoadVanilla(info backupInfo, data io.ReadSeeker) error {
	// create the main container
	err := s.doContainerCreate(info.Name, info.Privileged)
	if err != nil {
		return err
	}

	containerMntPoint := getContainerMountPoint(s.pool.Name, info.Name)
	// Extract container
	for _, snap := range info.Snapshots {
		// Extract snapshots
		cur := fmt.Sprintf("backup/snapshots/%s", snap)
		data.Seek(0, 0)
		err = shared.RunCommandWithFds(data, nil, "tar", "-xJf", "-",
			"--recursive-unlink", "--xattrs-include=*", "--strip-components=3", "-C", containerMntPoint, cur)
		if err != nil {
			logger.Errorf("Failed to untar \"%s\" into \"%s\": %s", cur, containerMntPoint, err)
			return err
		}

		// create snapshot
		err = s.doContainerSnapshotCreate(fmt.Sprintf("%s/%s", info.Name, snap), info.Name)
		if err != nil {
			return err
		}
	}

	// Extract container
	data.Seek(0, 0)
	err = shared.RunCommandWithFds(data, nil, "tar", "-xJf", "-",
		"--strip-components=2", "--xattrs-include=*", "-C", containerMntPoint, "backup/container")
	if err != nil {
		logger.Errorf("Failed to untar \"backup/container\" into \"%s\": %s", containerMntPoint, err)
		return err
	}

	return nil
}

func (s *storageBtrfs) ContainerBackupLoad(info backupInfo, data io.ReadSeeker) error {
	logger.Debugf("Loading BTRFS storage volume for backup \"%s\" on storage pool \"%s\"", info.Name, s.pool.Name)

	if info.HasBinaryFormat {
		return s.doContainerBackupLoadOptimized(info, data)
	}

	return s.doContainerBackupLoadVanilla(info, data)
}

func (s *storageBtrfs) ImageCreate(fingerprint string) error {
	logger.Debugf("Creating BTRFS storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)

	// Create the subvolume.
	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	err = s.createImageDbPoolVolume(fingerprint)
	if err != nil {
		return err
	}

	// We can only create the btrfs subvolume under the mounted storage
	// pool. The on-disk layout for images on a btrfs storage pool will thus
	// be
	// ${LXD_DIR}/storage-pools/<pool>/images/. The btrfs tool will
	// complain if the intermediate path does not exist, so create it if it
	// doesn't already.
	imageSubvolumePath := s.getImageSubvolumePath(s.pool.Name)
	if !shared.PathExists(imageSubvolumePath) {
		err := os.MkdirAll(imageSubvolumePath, imagesDirMode)
		if err != nil {
			return err
		}
	}

	// Create a temporary rw btrfs subvolume. From this rw subvolume we'll
	// create a ro snapshot below. The path with which we do this is
	// ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>@<pool>_tmp.
	imageSubvolumeName := getImageMountPoint(s.pool.Name, fingerprint)
	tmpImageSubvolumeName := fmt.Sprintf("%s_tmp", imageSubvolumeName)
	err = btrfsSubVolumeCreate(tmpImageSubvolumeName)
	if err != nil {
		return err
	}
	// Delete volume on error.
	undo := true
	defer func() {
		if undo {
			btrfsSubVolumesDelete(tmpImageSubvolumeName)
		}
	}()

	// Unpack the image in imageMntPoint.
	imagePath := shared.VarPath("images", fingerprint)
	err = unpackImage(imagePath, tmpImageSubvolumeName, storageTypeBtrfs, s.s.OS.RunningInUserNS)
	if err != nil {
		return err
	}

	// Now create a read-only snapshot of the subvolume.
	// The path with which we do this is
	// ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>.
	err = s.btrfsPoolVolumesSnapshot(tmpImageSubvolumeName, imageSubvolumeName, true, true)
	if err != nil {
		return err
	}

	defer func() {
		if undo {
			btrfsSubVolumesDelete(imageSubvolumeName)
		}
	}()

	err = btrfsSubVolumesDelete(tmpImageSubvolumeName)
	if err != nil {
		return err
	}

	undo = false

	logger.Debugf("Created BTRFS storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
	return nil
}

func (s *storageBtrfs) ImageDelete(fingerprint string) error {
	logger.Debugf("Deleting BTRFS storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Delete the btrfs subvolume. The path with which we
	// do this is ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>.
	imageSubvolumeName := getImageMountPoint(s.pool.Name, fingerprint)
	if shared.PathExists(imageSubvolumeName) && isBtrfsSubVolume(imageSubvolumeName) {
		err = btrfsSubVolumesDelete(imageSubvolumeName)
		if err != nil {
			return err
		}
	}

	err = s.deleteImageDbPoolVolume(fingerprint)
	if err != nil {
		return err
	}

	// Now delete the mountpoint for the image:
	// ${LXD_DIR}/images/<fingerprint>.
	if shared.PathExists(imageSubvolumeName) {
		err := os.RemoveAll(imageSubvolumeName)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Deleted BTRFS storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
	return nil
}

func (s *storageBtrfs) ImageMount(fingerprint string) (bool, error) {
	logger.Debugf("Mounting BTRFS storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)

	// The storage pool must be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return false, err
	}

	logger.Debugf("Mounted BTRFS storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
	return true, nil
}

func (s *storageBtrfs) ImageUmount(fingerprint string) (bool, error) {
	return true, nil
}

func btrfsSubVolumeCreate(subvol string) error {
	parentDestPath := filepath.Dir(subvol)
	if !shared.PathExists(parentDestPath) {
		err := os.MkdirAll(parentDestPath, 0711)
		if err != nil {
			return err
		}
	}

	output, err := shared.RunCommand(
		"btrfs",
		"subvolume",
		"create",
		subvol)
	if err != nil {
		logger.Errorf("Failed to create BTRFS subvolume \"%s\": %s", subvol, output)
		return err
	}

	return nil
}

func btrfsSubVolumeQGroup(subvol string) (string, error) {
	output, err := shared.RunCommand(
		"btrfs",
		"qgroup",
		"show",
		subvol,
		"-e",
		"-f")

	if err != nil {
		return "", db.ErrNoSuchObject
	}

	var qgroup string
	for _, line := range strings.Split(output, "\n") {
		if line == "" || strings.HasPrefix(line, "qgroupid") || strings.HasPrefix(line, "---") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}

		qgroup = fields[0]
	}

	if qgroup == "" {
		return "", fmt.Errorf("Unable to find quota group")
	}

	return qgroup, nil
}

func (s *storageBtrfs) btrfsPoolVolumeQGroupUsage(subvol string) (int64, error) {
	output, err := shared.RunCommand(
		"btrfs",
		"qgroup",
		"show",
		subvol,
		"-e",
		"-f")

	if err != nil {
		return -1, fmt.Errorf("BTRFS quotas not supported. Try enabling them with \"btrfs quota enable\"")
	}

	for _, line := range strings.Split(output, "\n") {
		if line == "" || strings.HasPrefix(line, "qgroupid") || strings.HasPrefix(line, "---") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}

		usage, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			continue
		}

		return usage, nil
	}

	return -1, fmt.Errorf("Unable to find current qgroup usage")
}

func btrfsSubVolumeDelete(subvol string) error {
	// Attempt (but don't fail on) to delete any qgroup on the subvolume
	qgroup, err := btrfsSubVolumeQGroup(subvol)
	if err == nil {
		shared.RunCommand(
			"btrfs",
			"qgroup",
			"destroy",
			qgroup,
			subvol)
	}

	// Attempt to make the subvolume writable
	shared.RunCommand("btrfs", "property", "set", subvol, "ro", "false")

	// Delete the subvolume itself
	_, err = shared.RunCommand(
		"btrfs",
		"subvolume",
		"delete",
		subvol)

	return err
}

// btrfsPoolVolumesDelete is the recursive variant on btrfsPoolVolumeDelete,
// it first deletes subvolumes of the subvolume and then the
// subvolume itself.
func btrfsSubVolumesDelete(subvol string) error {
	// Delete subsubvols.
	subsubvols, err := btrfsSubVolumesGet(subvol)
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(subsubvols)))

	for _, subsubvol := range subsubvols {
		err := btrfsSubVolumeDelete(path.Join(subvol, subsubvol))
		if err != nil {
			return err
		}
	}

	// Delete the subvol itself
	err = btrfsSubVolumeDelete(subvol)
	if err != nil {
		return err
	}

	return nil
}

/*
 * btrfsSnapshot creates a snapshot of "source" to "dest"
 * the result will be readonly if "readonly" is True.
 */
func btrfsSnapshot(source string, dest string, readonly bool) error {
	var output string
	var err error
	if readonly {
		output, err = shared.RunCommand(
			"btrfs",
			"subvolume",
			"snapshot",
			"-r",
			source,
			dest)
	} else {
		output, err = shared.RunCommand(
			"btrfs",
			"subvolume",
			"snapshot",
			source,
			dest)
	}
	if err != nil {
		return fmt.Errorf(
			"subvolume snapshot failed, source=%s, dest=%s, output=%s",
			source,
			dest,
			output,
		)
	}

	return err
}

func (s *storageBtrfs) btrfsPoolVolumeSnapshot(source string, dest string, readonly bool) error {
	return btrfsSnapshot(source, dest, readonly)
}

func (s *storageBtrfs) btrfsPoolVolumesSnapshot(source string, dest string, readonly bool, recursive bool) error {
	// Now snapshot all subvolumes of the root.
	if recursive {
		// Get a list of subvolumes of the root
		subsubvols, err := btrfsSubVolumesGet(source)
		if err != nil {
			return err
		}
		sort.Sort(sort.StringSlice(subsubvols))

		if len(subsubvols) > 0 && readonly {
			// A root with subvolumes can never be readonly,
			// also don't make subvolumes readonly.
			readonly = false

			logger.Warnf("Subvolumes detected, ignoring ro flag")
		}

		// First snapshot the root
		err = s.btrfsPoolVolumeSnapshot(source, dest, readonly)
		if err != nil {
			return err
		}

		for _, subsubvol := range subsubvols {
			// Clear the target for the subvol to use
			os.Remove(path.Join(dest, subsubvol))

			err := s.btrfsPoolVolumeSnapshot(path.Join(source, subsubvol), path.Join(dest, subsubvol), readonly)
			if err != nil {
				return err
			}
		}
	} else {
		err := s.btrfsPoolVolumeSnapshot(source, dest, readonly)
		if err != nil {
			return err
		}
	}

	return nil
}

// isBtrfsSubVolume returns true if the given Path is a btrfs subvolume else
// false.
func isBtrfsSubVolume(subvolPath string) bool {
	fs := syscall.Stat_t{}
	err := syscall.Lstat(subvolPath, &fs)
	if err != nil {
		return false
	}

	// Check if BTRFS_FIRST_FREE_OBJECTID
	if fs.Ino != 256 {
		return false
	}

	return true
}

func isBtrfsFilesystem(path string) bool {
	_, err := shared.RunCommand("btrfs", "filesystem", "show", path)
	if err != nil {
		return false
	}

	return true
}

func isOnBtrfs(path string) bool {
	fs := syscall.Statfs_t{}

	err := syscall.Statfs(path, &fs)
	if err != nil {
		return false
	}

	if fs.Type != util.FilesystemSuperMagicBtrfs {
		return false
	}

	return true
}

func btrfsSubVolumesGet(path string) ([]string, error) {
	result := []string{}

	if !strings.HasSuffix(path, "/") {
		path = path + "/"
	}

	// Unprivileged users can't get to fs internals
	filepath.Walk(path, func(fpath string, fi os.FileInfo, err error) error {
		// Skip walk errors
		if err != nil {
			return nil
		}

		// Ignore the base path
		if strings.TrimRight(fpath, "/") == strings.TrimRight(path, "/") {
			return nil
		}

		// Subvolumes can only be directories
		if !fi.IsDir() {
			return nil
		}

		// Check if a btrfs subvolume
		if isBtrfsSubVolume(fpath) {
			result = append(result, strings.TrimPrefix(fpath, path))
		}

		return nil
	})

	return result, nil
}

type btrfsMigrationSourceDriver struct {
	container          container
	snapshots          []container
	btrfsSnapshotNames []string
	btrfs              *storageBtrfs
	runningSnapName    string
	stoppedSnapName    string
}

func (s *btrfsMigrationSourceDriver) Snapshots() []container {
	return s.snapshots
}

func (s *btrfsMigrationSourceDriver) send(conn *websocket.Conn, btrfsPath string, btrfsParent string, readWrapper func(io.ReadCloser) io.ReadCloser) error {
	args := []string{"send"}
	if btrfsParent != "" {
		args = append(args, "-p", btrfsParent)
	}
	args = append(args, btrfsPath)

	cmd := exec.Command("btrfs", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	readPipe := io.ReadCloser(stdout)
	if readWrapper != nil {
		readPipe = readWrapper(stdout)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	<-shared.WebsocketSendStream(conn, readPipe, 4*1024*1024)

	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		logger.Errorf("Problem reading btrfs send stderr: %s", err)
	}

	err = cmd.Wait()
	if err != nil {
		logger.Errorf("Problem with btrfs send: %s", string(output))
	}

	return err
}

func (s *btrfsMigrationSourceDriver) SendWhileRunning(conn *websocket.Conn, op *operation, bwlimit string, containerOnly bool) error {
	_, containerPool, _ := s.container.Storage().GetContainerPoolInfo()
	containerName := s.container.Name()
	containersPath := getContainerMountPoint(containerPool, "")
	sourceName := containerName

	// Deal with sending a snapshot to create a container on another LXD
	// instance.
	if s.container.IsSnapshot() {
		sourceName, _, _ := containerGetParentAndSnapshotName(containerName)
		snapshotsPath := getSnapshotMountPoint(containerPool, sourceName)
		tmpContainerMntPoint, err := ioutil.TempDir(snapshotsPath, sourceName)
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpContainerMntPoint)

		err = os.Chmod(tmpContainerMntPoint, 0700)
		if err != nil {
			return err
		}

		migrationSendSnapshot := fmt.Sprintf("%s/.migration-send", tmpContainerMntPoint)
		snapshotMntPoint := getSnapshotMountPoint(containerPool, containerName)
		err = s.btrfs.btrfsPoolVolumesSnapshot(snapshotMntPoint, migrationSendSnapshot, true, true)
		if err != nil {
			return err
		}
		defer btrfsSubVolumesDelete(migrationSendSnapshot)

		wrapper := StorageProgressReader(op, "fs_progress", containerName)
		return s.send(conn, migrationSendSnapshot, "", wrapper)
	}

	if !containerOnly {
		for i, snap := range s.snapshots {
			prev := ""
			if i > 0 {
				prev = getSnapshotMountPoint(containerPool, s.snapshots[i-1].Name())
			}

			snapMntPoint := getSnapshotMountPoint(containerPool, snap.Name())
			wrapper := StorageProgressReader(op, "fs_progress", snap.Name())
			if err := s.send(conn, snapMntPoint, prev, wrapper); err != nil {
				return err
			}
		}
	}

	tmpContainerMntPoint, err := ioutil.TempDir(containersPath, containerName)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	err = os.Chmod(tmpContainerMntPoint, 0700)
	if err != nil {
		return err
	}

	migrationSendSnapshot := fmt.Sprintf("%s/.migration-send", tmpContainerMntPoint)
	containerMntPoint := getContainerMountPoint(containerPool, sourceName)
	err = s.btrfs.btrfsPoolVolumesSnapshot(containerMntPoint, migrationSendSnapshot, true, true)
	if err != nil {
		return err
	}
	defer btrfsSubVolumesDelete(migrationSendSnapshot)

	btrfsParent := ""
	if len(s.btrfsSnapshotNames) > 0 {
		btrfsParent = s.btrfsSnapshotNames[len(s.btrfsSnapshotNames)-1]
	}

	wrapper := StorageProgressReader(op, "fs_progress", containerName)
	return s.send(conn, migrationSendSnapshot, btrfsParent, wrapper)
}

func (s *btrfsMigrationSourceDriver) SendAfterCheckpoint(conn *websocket.Conn, bwlimit string) error {
	tmpPath := getSnapshotMountPoint(s.btrfs.pool.Name,
		fmt.Sprintf("%s/.migration-send", s.container.Name()))
	err := os.MkdirAll(tmpPath, 0711)
	if err != nil {
		return err
	}

	err = os.Chmod(tmpPath, 0700)
	if err != nil {
		return err
	}

	s.stoppedSnapName = fmt.Sprintf("%s/.root", tmpPath)
	parentName, _, _ := containerGetParentAndSnapshotName(s.container.Name())
	containerMntPt := getContainerMountPoint(s.btrfs.pool.Name, parentName)
	err = s.btrfs.btrfsPoolVolumesSnapshot(containerMntPt, s.stoppedSnapName, true, true)
	if err != nil {
		return err
	}

	return s.send(conn, s.stoppedSnapName, s.runningSnapName, nil)
}

func (s *btrfsMigrationSourceDriver) Cleanup() {
	if s.stoppedSnapName != "" {
		btrfsSubVolumesDelete(s.stoppedSnapName)
	}

	if s.runningSnapName != "" {
		btrfsSubVolumesDelete(s.runningSnapName)
	}
}

func (s *storageBtrfs) MigrationType() migration.MigrationFSType {
	if s.s.OS.RunningInUserNS {
		return migration.MigrationFSType_RSYNC
	}

	return migration.MigrationFSType_BTRFS
}

func (s *storageBtrfs) PreservesInodes() bool {
	if s.s.OS.RunningInUserNS {
		return false
	}

	return true
}

func (s *storageBtrfs) MigrationSource(c container, containerOnly bool) (MigrationStorageSourceDriver, error) {
	if s.s.OS.RunningInUserNS {
		return rsyncMigrationSource(c, containerOnly)
	}

	/* List all the snapshots in order of reverse creation. The idea here
	 * is that we send the oldest to newest snapshot, hopefully saving on
	 * xfer costs. Then, after all that, we send the container itself.
	 */
	var err error
	var snapshots = []container{}
	if !containerOnly {
		snapshots, err = c.Snapshots()
		if err != nil {
			return nil, err
		}
	}

	driver := &btrfsMigrationSourceDriver{
		container:          c,
		snapshots:          snapshots,
		btrfsSnapshotNames: []string{},
		btrfs:              s,
	}

	if !containerOnly {
		for _, snap := range snapshots {
			btrfsPath := getSnapshotMountPoint(s.pool.Name, snap.Name())
			driver.btrfsSnapshotNames = append(driver.btrfsSnapshotNames, btrfsPath)
		}
	}

	return driver, nil
}

func (s *storageBtrfs) MigrationSink(live bool, container container, snapshots []*migration.Snapshot, conn *websocket.Conn, srcIdmap *idmap.IdmapSet, op *operation, containerOnly bool) error {
	if s.s.OS.RunningInUserNS {
		return rsyncMigrationSink(live, container, snapshots, conn, srcIdmap, op, containerOnly)
	}

	btrfsRecv := func(snapName string, btrfsPath string, targetPath string, isSnapshot bool, writeWrapper func(io.WriteCloser) io.WriteCloser) error {
		args := []string{"receive", "-e", btrfsPath}
		cmd := exec.Command("btrfs", args...)

		// Remove the existing pre-created subvolume
		err := btrfsSubVolumesDelete(targetPath)
		if err != nil {
			logger.Errorf("Failed to delete pre-created BTRFS subvolume: %s", btrfsPath)
			return err
		}

		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			return err
		}

		err = cmd.Start()
		if err != nil {
			return err
		}

		writePipe := io.WriteCloser(stdin)
		if writeWrapper != nil {
			writePipe = writeWrapper(stdin)
		}

		<-shared.WebsocketRecvStream(writePipe, conn)

		output, err := ioutil.ReadAll(stderr)
		if err != nil {
			logger.Debugf("Problem reading btrfs receive stderr %s", err)
		}

		err = cmd.Wait()
		if err != nil {
			logger.Errorf("Problem with btrfs receive: %s", string(output))
			return err
		}

		receivedSnapshot := fmt.Sprintf("%s/.migration-send", btrfsPath)
		// handle older lxd versions
		if !shared.PathExists(receivedSnapshot) {
			receivedSnapshot = fmt.Sprintf("%s/.root", btrfsPath)
		}
		if isSnapshot {
			receivedSnapshot = fmt.Sprintf("%s/%s", btrfsPath, snapName)
			err = s.btrfsPoolVolumesSnapshot(receivedSnapshot, targetPath, true, true)
		} else {
			err = s.btrfsPoolVolumesSnapshot(receivedSnapshot, targetPath, false, true)
		}
		if err != nil {
			logger.Errorf("Problem with btrfs snapshot: %s", err)
			return err
		}

		err = btrfsSubVolumesDelete(receivedSnapshot)
		if err != nil {
			logger.Errorf("Failed to delete BTRFS subvolume \"%s\": %s", btrfsPath, err)
			return err
		}

		return nil
	}

	containerName := container.Name()
	_, containerPool, _ := container.Storage().GetContainerPoolInfo()
	containersPath := getSnapshotMountPoint(containerPool, containerName)
	if !containerOnly && len(snapshots) > 0 {
		err := os.MkdirAll(containersPath, containersDirMode)
		if err != nil {
			return err
		}

		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", containerPool, "containers-snapshots", containerName)
		snapshotMntPointSymlink := shared.VarPath("snapshots", containerName)
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
	parentExpandedDevices := container.ExpandedDevices()
	parentLocalRootDiskDeviceKey, parentLocalRootDiskDevice, _ := shared.GetRootDiskDevice(parentExpandedDevices)
	if parentLocalRootDiskDeviceKey != "" {
		parentStoragePool = parentLocalRootDiskDevice["pool"]
	}

	// A little neuroticism.
	if parentStoragePool == "" {
		return fmt.Errorf("detected that the container's root device is missing the pool property during BTRFS migration")
	}

	if !containerOnly {
		for _, snap := range snapshots {
			args := snapshotProtobufToContainerArgs(containerName, snap)

			// Ensure that snapshot and parent container have the
			// same storage pool in their local root disk device.
			// If the root disk device for the snapshot comes from a
			// profile on the new instance as well we don't need to
			// do anything.
			if args.Devices != nil {
				snapLocalRootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(args.Devices)
				if snapLocalRootDiskDeviceKey != "" {
					args.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
				}
			}

			snapshotMntPoint := getSnapshotMountPoint(containerPool, args.Name)
			_, err := containerCreateEmptySnapshot(container.DaemonState(), args)
			if err != nil {
				return err
			}

			snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", containerName)
			snapshotMntPointSymlink := shared.VarPath("snapshots", containerName)
			err = createSnapshotMountpoint(snapshotMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
			if err != nil {
				return err
			}

			tmpSnapshotMntPoint, err := ioutil.TempDir(containersPath, containerName)
			if err != nil {
				return err
			}
			defer os.RemoveAll(tmpSnapshotMntPoint)

			err = os.Chmod(tmpSnapshotMntPoint, 0700)
			if err != nil {
				return err
			}

			wrapper := StorageProgressWriter(op, "fs_progress", *snap.Name)
			err = btrfsRecv(*(snap.Name), tmpSnapshotMntPoint, snapshotMntPoint, true, wrapper)
			os.RemoveAll(tmpSnapshotMntPoint)
			if err != nil {
				return err
			}
		}
	}

	containersMntPoint := getContainerMountPoint(s.pool.Name, "")
	err := createContainerMountpoint(containersMntPoint, container.Path(), container.IsPrivileged())
	if err != nil {
		return err
	}

	/* finally, do the real container */
	wrapper := StorageProgressWriter(op, "fs_progress", containerName)
	tmpContainerMntPoint, err := ioutil.TempDir(containersMntPoint, containerName)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	err = os.Chmod(tmpContainerMntPoint, 0700)
	if err != nil {
		return err
	}

	containerMntPoint := getContainerMountPoint(s.pool.Name, containerName)
	err = btrfsRecv("", tmpContainerMntPoint, containerMntPoint, false, wrapper)
	if err != nil {
		return err
	}

	if live {
		err = btrfsRecv("", tmpContainerMntPoint, containerMntPoint, false, wrapper)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageBtrfs) btrfsLookupFsUUID(fs string) (string, error) {
	output, err := shared.RunCommand(
		"btrfs",
		"filesystem",
		"show",
		"--raw",
		fs)
	if err != nil {
		return "", fmt.Errorf("failed to detect UUID")
	}

	outputString := output
	idx := strings.Index(outputString, "uuid: ")
	outputString = outputString[idx+6:]
	outputString = strings.TrimSpace(outputString)
	idx = strings.Index(outputString, "\t")
	outputString = outputString[:idx]
	outputString = strings.Trim(outputString, "\n")

	return outputString, nil
}

func (s *storageBtrfs) StorageEntitySetQuota(volumeType int, size int64, data interface{}) error {
	logger.Debugf(`Setting BTRFS quota for "%s"`, s.volume.Name)

	var c container
	var subvol string
	switch volumeType {
	case storagePoolVolumeTypeContainer:
		c = data.(container)
		subvol = getContainerMountPoint(s.pool.Name, c.Name())
	case storagePoolVolumeTypeCustom:
		subvol = getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	}

	_, err := btrfsSubVolumeQGroup(subvol)
	if err != nil {
		if err != db.ErrNoSuchObject {
			return err
		}

		// Enable quotas
		poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
		output, err := shared.RunCommand(
			"btrfs", "quota", "enable", poolMntPoint)
		if err != nil && !s.s.OS.RunningInUserNS {
			return fmt.Errorf("Failed to enable quotas on BTRFS pool: %s", output)
		}
	}

	output, err := shared.RunCommand(
		"btrfs",
		"qgroup",
		"limit",
		"-e", fmt.Sprintf("%d", size),
		subvol)

	if err != nil {
		return fmt.Errorf("Failed to set btrfs quota: %s", output)
	}

	logger.Debugf(`Set BTRFS quota for "%s"`, s.volume.Name)
	return nil
}

func (s *storageBtrfs) StoragePoolResources() (*api.ResourcesStoragePool, error) {
	ourMount, err := s.StoragePoolMount()
	if err != nil {
		return nil, err
	}
	if ourMount {
		defer s.StoragePoolUmount()
	}

	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)

	// Inode allocation is dynamic so no use in reporting them.

	return storageResource(poolMntPoint)
}

func (s *storageBtrfs) StoragePoolVolumeCopy(source *api.StorageVolumeSource) error {
	logger.Infof("Copying BTRFS storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)
	successMsg := fmt.Sprintf("Copied BTRFS storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)

	srcMountPoint := getStoragePoolVolumeMountPoint(source.Pool, source.Name)
	dstMountPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	if s.pool.Name == source.Pool {
		// Ensure that the directories immediately preceding the subvolume directory exist.
		customDir := getStoragePoolVolumeMountPoint(s.pool.Name, "")
		if !shared.PathExists(customDir) {
			err := os.MkdirAll(customDir, customDirMode)
			if err != nil {
				logger.Errorf("Failed to create directory \"%s\" for storage volume \"%s\" on storage pool \"%s\": %s", customDir, s.volume.Name, s.pool.Name, err)
				return err
			}
		}

		err := s.btrfsPoolVolumesSnapshot(srcMountPoint, dstMountPoint, false, true)
		if err != nil {
			logger.Errorf("Failed to create BTRFS snapshot for storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
			return err
		}

		logger.Infof(successMsg)
		return nil
	}

	// setup storage for the source volume
	srcStorage, err := storagePoolVolumeInit(s.s, source.Pool, source.Name, storagePoolVolumeTypeCustom)
	if err != nil {
		logger.Errorf("Failed to initialize storage for BTRFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	ourMount, err := srcStorage.StoragePoolVolumeMount()
	if err != nil {
		logger.Errorf("Failed to mount BTRFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}
	if ourMount {
		defer srcStorage.StoragePoolVolumeUmount()
	}

	err = s.StoragePoolVolumeCreate()
	if err != nil {
		logger.Errorf("Failed to create BTRFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	bwlimit := s.pool.Config["rsync.bwlimit"]
	_, err = rsyncLocalCopy(srcMountPoint, dstMountPoint, bwlimit)
	if err != nil {
		s.StoragePoolVolumeDelete()
		logger.Errorf("Failed to rsync into BTRFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	logger.Infof(successMsg)
	return nil
}

func (s *btrfsMigrationSourceDriver) SendStorageVolume(conn *websocket.Conn, op *operation, bwlimit string, storage storage) error {
	msg := fmt.Sprintf("Function not implemented")
	logger.Errorf(msg)
	return fmt.Errorf(msg)
}

func (s *storageBtrfs) StorageMigrationSource() (MigrationStorageSourceDriver, error) {
	return rsyncStorageMigrationSource()
}

func (s *storageBtrfs) StorageMigrationSink(conn *websocket.Conn, op *operation, storage storage) error {
	return rsyncStorageMigrationSink(conn, op, storage)
}

func (s *storageBtrfs) GetStoragePool() *api.StoragePool {
	return s.pool
}

func (s *storageBtrfs) GetStoragePoolVolume() *api.StorageVolume {
	return s.volume
}

func (s *storageBtrfs) GetState() *state.State {
	return s.s
}

func (s *storageBtrfs) StoragePoolVolumeSnapshotCreate(target *api.StorageVolumeSnapshotsPost) error {
	logger.Infof("Creating BTRFS storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	// Create subvolume path on the storage pool.
	customSubvolumePath := s.getCustomSubvolumePath(s.pool.Name)
	err := os.MkdirAll(customSubvolumePath, 0700)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	_, _, ok := containerGetParentAndSnapshotName(target.Name)
	if !ok {
		return err
	}

	customSnapshotSubvolumeName := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	err = os.MkdirAll(customSnapshotSubvolumeName, snapshotsDirMode)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	sourcePath := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	targetPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, target.Name)
	err = s.btrfsPoolVolumesSnapshot(sourcePath, targetPath, true, true)
	if err != nil {
		return err
	}

	logger.Infof("Created BTRFS storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageBtrfs) StoragePoolVolumeSnapshotDelete() error {
	logger.Infof("Deleting BTRFS storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	snapshotSubvolumeName := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	if shared.PathExists(snapshotSubvolumeName) && isBtrfsSubVolume(snapshotSubvolumeName) {
		err := btrfsSubVolumesDelete(snapshotSubvolumeName)
		if err != nil {
			return err
		}
	}

	err := os.RemoveAll(snapshotSubvolumeName)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	sourceName, _, _ := containerGetParentAndSnapshotName(s.volume.Name)
	storageVolumeSnapshotPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, sourceName)
	empty, err := shared.PathIsEmpty(storageVolumeSnapshotPath)
	if err == nil && empty {
		os.RemoveAll(storageVolumeSnapshotPath)
	}

	err = s.s.Cluster.StoragePoolVolumeDelete(
		s.volume.Name,
		storagePoolVolumeTypeCustom,
		s.poolID)
	if err != nil {
		logger.Errorf(`Failed to delete database entry for BTRFS storage volume "%s" on storage pool "%s"`,
			s.volume.Name, s.pool.Name)
	}

	logger.Infof("Deleted BTRFS storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageBtrfs) StoragePoolVolumeSnapshotRename(newName string) error {
	logger.Infof("Renaming BTRFS storage volume on storage pool \"%s\" from \"%s\" to \"%s\"", s.pool.Name, s.volume.Name, newName)

	sourceName, _, ok := containerGetParentAndSnapshotName(s.volume.Name)
	if !ok {
		return fmt.Errorf("Not a snapshot name")
	}

	fullSnapshotName := fmt.Sprintf("%s%s%s", sourceName, shared.SnapshotDelimiter, newName)
	oldPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	newPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, fullSnapshotName)
	err := os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	logger.Infof("Renamed BTRFS storage volume on storage pool \"%s\" from \"%s\" to \"%s\"", s.pool.Name, s.volume.Name, newName)

	return s.s.Cluster.StoragePoolVolumeRename(s.volume.Name, fullSnapshotName, storagePoolVolumeTypeCustom, s.poolID)
}
