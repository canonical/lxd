package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

type storageLvm struct {
	vgName       string
	thinPoolName string
	useThinpool  bool
	loopInfo     *os.File
	storageShared
}

var lvmVersion = ""

// Only initialize the minimal information we need about a given storage type.
func (s *storageLvm) StorageCoreInit() error {
	s.sType = storageTypeLvm
	typeName, err := storageTypeToString(s.sType)
	if err != nil {
		return err
	}
	s.sTypeName = typeName

	if lvmVersion != "" {
		s.sTypeVersion = lvmVersion
		return nil
	}

	output, err := shared.RunCommand("lvm", "version")
	if err != nil {
		return fmt.Errorf("Error getting LVM version: %v\noutput:'%s'", err, output)
	}
	lines := strings.Split(output, "\n")

	s.sTypeVersion = ""
	for idx, line := range lines {
		fields := strings.SplitAfterN(line, ":", 2)
		if len(fields) < 2 {
			continue
		}
		if idx > 0 {
			s.sTypeVersion += " / "
		}
		s.sTypeVersion += strings.TrimSpace(fields[1])
	}

	lvmVersion = s.sTypeVersion

	return nil
}

func (s *storageLvm) StoragePoolInit() error {
	err := s.StorageCoreInit()
	if err != nil {
		return err
	}

	source := s.pool.Config["source"]
	s.thinPoolName = s.getLvmThinpoolName()
	s.useThinpool = s.usesThinpool()

	if s.pool.Config["lvm.vg_name"] != "" {
		s.vgName = s.pool.Config["lvm.vg_name"]
	}

	if source != "" && !filepath.IsAbs(source) {
		ok, err := storageVGExists(source)
		if err != nil {
			// Internal error.
			return err
		} else if !ok {
			// Volume group does not exist.
			return fmt.Errorf("the requested volume group \"%s\" does not exist", source)
		}
		s.vgName = source
	}

	return nil
}

func (s *storageLvm) StoragePoolCheck() error {
	logger.Debugf("Checking LVM storage pool \"%s\"", s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}
	if s.loopInfo != nil {
		defer s.loopInfo.Close()
		defer func() { s.loopInfo = nil }()
	}

	poolName := s.getOnDiskPoolName()
	err = storageVGActivate(poolName)
	if err != nil {
		return err
	}

	logger.Debugf("Checked LVM storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolCreate() error {
	logger.Infof("Creating LVM storage pool \"%s\"", s.pool.Name)

	s.pool.Config["volatile.initial_source"] = s.pool.Config["source"]

	var globalErr error
	var pvExisted bool
	var vgExisted bool
	tryUndo := true
	poolName := s.getOnDiskPoolName()
	source := s.pool.Config["source"]
	// must be initialized
	vgName := ""
	// not initialized in all cases
	pvName := ""

	// Create the mountpoint for the storage pool.
	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	err := os.MkdirAll(poolMntPoint, 0711)
	if err != nil {
		return err
	}
	defer func() {
		if tryUndo {
			os.Remove(poolMntPoint)
		}
	}()

	defaultSource := filepath.Join(shared.VarPath("disks"), fmt.Sprintf("%s.img", s.pool.Name))
	if source == "" || source == defaultSource {
		source = defaultSource
		s.pool.Config["source"] = source

		if s.pool.Config["lvm.vg_name"] == "" {
			s.pool.Config["lvm.vg_name"] = poolName
		}

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

		_, err = s.StoragePoolMount()
		if err != nil {
			return err
		}
		defer func() {
			if tryUndo {
				os.Remove(source)
			}
		}()
		if s.loopInfo != nil {
			defer s.loopInfo.Close()
			defer func() { s.loopInfo = nil }()
		}

		// Check if the physical volume already exists.
		pvName = s.loopInfo.Name()
		pvExisted, globalErr = storagePVExists(pvName)
		if globalErr != nil {
			return globalErr
		}

		// Check if the volume group already exists.
		vgExisted, globalErr = storageVGExists(poolName)
		if globalErr != nil {
			return globalErr
		}
	} else {
		s.pool.Config["size"] = ""
		if filepath.IsAbs(source) {
			pvName = source
			if !shared.IsBlockdevPath(pvName) {
				return fmt.Errorf("Custom loop file locations are not supported")
			}

			if s.pool.Config["lvm.vg_name"] == "" {
				s.pool.Config["lvm.vg_name"] = poolName
			}

			// Set source to volume group name.
			s.pool.Config["source"] = poolName

			// Check if the physical volume already exists.
			pvExisted, globalErr = storagePVExists(pvName)
			if globalErr != nil {
				return globalErr
			}

			// Check if the volume group already exists.
			vgExisted, globalErr = storageVGExists(poolName)
			if globalErr != nil {
				return globalErr
			}
		} else {
			// The physical volume must already consist
			pvExisted = true
			vgName = source
			if s.pool.Config["lvm.vg_name"] != "" && s.pool.Config["lvm.vg_name"] != vgName {
				// User gave us something weird.
				return fmt.Errorf("Invalid combination of \"source\" and \"lvm.vg_name\" property")
			}

			s.pool.Config["lvm.vg_name"] = vgName
			s.vgName = vgName

			vgExisted, globalErr = storageVGExists(vgName)
			if globalErr != nil {
				return globalErr
			}

			// Volume group must exist but doesn't.
			if !vgExisted {
				return fmt.Errorf("The requested volume group \"%s\" does not exist", vgName)
			}
		}
	}

	if !pvExisted {
		// This is an internal error condition which should never be
		// hit.
		if pvName == "" {
			logger.Errorf("No name for physical volume detected")
		}

		output, err := shared.TryRunCommand("pvcreate", pvName)
		if err != nil {
			return fmt.Errorf("Failed to create the physical volume for the lvm storage pool: %s", output)
		}
		defer func() {
			if tryUndo {
				shared.TryRunCommand("pvremove", pvName)
			}
		}()
	}

	if vgExisted {
		// Check that the volume group is empty.
		// Otherwise we will refuse to use it.
		count, err := lvmGetLVCount(poolName)
		if err != nil {
			logger.Errorf("Failed to determine whether the volume group \"%s\" is empty", poolName)
			return err
		}

		empty := true
		if count > 0 && !s.useThinpool {
			empty = false
		}

		if count > 0 && s.useThinpool {
			ok, err := storageLVMThinpoolExists(poolName, s.thinPoolName)
			if err != nil {
				logger.Errorf("Failed to determine whether thinpool \"%s\" exists in volume group \"%s\": %s", poolName, s.thinPoolName, err)
				return err
			}
			empty = ok
		}

		if !empty {
			msg := fmt.Sprintf("volume group \"%s\" is not empty", poolName)
			logger.Errorf(msg)
			return fmt.Errorf(msg)
		}

		// Check that we don't already use this volume group.
		inUse, user, err := lxdUsesPool(s.s.Cluster, poolName, s.pool.Driver, "lvm.vg_name")
		if err != nil {
			return err
		}

		if inUse {
			msg := fmt.Sprintf("LXD already uses volume group \"%s\" for pool \"%s\"", poolName, user)
			logger.Errorf(msg)
			return fmt.Errorf(msg)
		}
	} else {
		output, err := shared.TryRunCommand("vgcreate", poolName, pvName)
		if err != nil {
			return fmt.Errorf("failed to create the volume group for the lvm storage pool: %s", output)
		}
	}

	err = s.StoragePoolCheck()
	if err != nil {
		return err
	}

	// Deregister cleanup.
	tryUndo = false

	logger.Infof("Created LVM storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolDelete() error {
	logger.Infof("Deleting LVM storage pool \"%s\"", s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}
	if s.loopInfo != nil {
		defer s.loopInfo.Close()
		defer func() { s.loopInfo = nil }()
	}

	poolName := s.getOnDiskPoolName()
	poolExists, _ := storageVGExists(poolName)

	// Delete the thinpool.
	if s.useThinpool && poolExists {
		// Check that the thinpool actually exists. For example, it
		// won't when the user has never created a storage volume in the
		// storage pool.
		devPath := getLvmDevPath("default", poolName, "", s.thinPoolName)
		ok, _ := storageLVExists(devPath)
		if ok {
			msg, err := shared.TryRunCommand("lvremove", "-f", devPath)
			if err != nil {
				logger.Errorf("Failed to delete thinpool \"%s\" from volume group \"%s\": %s", s.thinPoolName, poolName, msg)
				return err
			}
		}
	}

	// Check that the count in the volume group is zero. If not, we need to
	// assume that other users are using the volume group, so don't remove
	// it. This actually goes against policy since we explicitly state: our
	// pool, and nothing but our pool but still, let's not hurt users.
	count, err := lvmGetLVCount(poolName)
	if err != nil {
		return err
	}

	// Remove the volume group.
	if count == 0 && poolExists {
		output, err := shared.TryRunCommand("vgremove", "-f", poolName)
		if err != nil {
			logger.Errorf("Failed to destroy the volume group for the lvm storage pool: %s", output)
			return err
		}
	}

	if s.loopInfo != nil {
		// Set LO_FLAGS_AUTOCLEAR before we remove the loop file
		// otherwise we will get EBADF.
		err = setAutoclearOnLoopDev(int(s.loopInfo.Fd()))
		if err != nil {
			logger.Warnf("Failed to set LO_FLAGS_AUTOCLEAR on loop device: %s, manual cleanup needed", err)
		}

		output, err := shared.TryRunCommand("pvremove", "-f", s.loopInfo.Name())
		if err != nil {
			logger.Warnf("Failed to destroy the physical volume for the lvm storage pool: %s", output)
		}
	}

	if filepath.IsAbs(source) {
		// This is a loop file so deconfigure the associated loop
		// device.
		err = os.Remove(source)
		if err != nil {
			return err
		}
	}

	// Delete the mountpoint for the storage pool.
	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	err = os.RemoveAll(poolMntPoint)
	if err != nil {
		return err
	}

	logger.Infof("Deleted LVM storage pool \"%s\"", s.pool.Name)
	return nil
}

// Currently only used for loop-backed LVM storage pools. Can be called without
// overhead since it is essentially a noop for non-loop-backed LVM storage
// pools.
func (s *storageLvm) StoragePoolMount() (bool, error) {
	source := s.pool.Config["source"]
	if source == "" {
		return false, fmt.Errorf("no \"source\" property found for the storage pool")
	}

	if !filepath.IsAbs(source) {
		return true, nil
	}

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

	if filepath.IsAbs(source) && !shared.IsBlockdevPath(source) {
		// Try to prepare new loop device.
		loopF, loopErr := prepareLoopDev(source, 0)
		if loopErr != nil {
			return false, loopErr
		}
		// Make sure that LO_FLAGS_AUTOCLEAR is unset.
		loopErr = unsetAutoclearOnLoopDev(int(loopF.Fd()))
		if loopErr != nil {
			return false, loopErr
		}
		s.loopInfo = loopF
	}

	return true, nil
}

func (s *storageLvm) StoragePoolUmount() (bool, error) {
	return true, nil
}

func (s *storageLvm) StoragePoolVolumeCreate() error {
	logger.Infof("Creating LVM storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	tryUndo := true

	poolName := s.getOnDiskPoolName()
	thinPoolName := s.getLvmThinpoolName()
	lvFsType := s.getLvmFilesystem()
	lvSize, err := s.getLvmVolumeSize()
	if lvSize == "" {
		return err
	}

	volumeType, err := storagePoolVolumeTypeNameToAPIEndpoint(s.volume.Type)
	if err != nil {
		return err
	}

	if s.useThinpool {
		err = lvmCreateThinpool(s.s, s.sTypeVersion, poolName, thinPoolName, lvFsType)
		if err != nil {
			return err
		}
	}

	err = lvmCreateLv("default", poolName, thinPoolName, s.volume.Name, lvFsType, lvSize, volumeType, s.useThinpool)
	if err != nil {
		return fmt.Errorf("Error Creating LVM LV for new image: %v", err)
	}
	defer func() {
		if tryUndo {
			s.StoragePoolVolumeDelete()
		}
	}()

	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	err = os.MkdirAll(customPoolVolumeMntPoint, 0711)
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

	tryUndo = false

	logger.Infof("Created LVM storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeDelete() error {
	logger.Infof("Deleting LVM storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	poolName := s.getOnDiskPoolName()
	customLvmDevPath := getLvmDevPath("default", poolName,
		storagePoolVolumeAPIEndpointCustom, s.volume.Name)
	lvExists, _ := storageLVExists(customLvmDevPath)

	if lvExists {
		_, err := s.StoragePoolVolumeUmount()
		if err != nil {
			return err
		}
	}

	volumeType, err := storagePoolVolumeTypeNameToAPIEndpoint(s.volume.Type)
	if err != nil {
		return err
	}

	if lvExists {
		err = removeLV("default", poolName, volumeType, s.volume.Name)
		if err != nil {
			return err
		}
	}

	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	if shared.PathExists(customPoolVolumeMntPoint) {
		err := os.RemoveAll(customPoolVolumeMntPoint)
		if err != nil {
			return err
		}
	}

	err = s.s.Cluster.StoragePoolVolumeDelete(
		"default",
		s.volume.Name,
		storagePoolVolumeTypeCustom,
		s.poolID)
	if err != nil {
		logger.Errorf(`Failed to delete database entry for LVM storage volume "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)
	}

	logger.Infof("Deleted LVM storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeMount() (bool, error) {
	logger.Debugf("Mounting LVM storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	poolName := s.getOnDiskPoolName()
	lvFsType := s.getLvmFilesystem()
	volumeType, err := storagePoolVolumeTypeNameToAPIEndpoint(s.volume.Type)
	if err != nil {
		return false, err
	}
	lvmVolumePath := getLvmDevPath("default", poolName, volumeType, s.volume.Name)

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
		mountFlags, mountOptions := lxdResolveMountoptions(s.getLvmMountOptions())
		customerr = tryMount(lvmVolumePath, customPoolVolumeMntPoint, lvFsType, mountFlags, mountOptions)
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

	logger.Debugf("Mounted LVM storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageLvm) StoragePoolVolumeUmount() (bool, error) {
	logger.Debugf("Unmounting LVM storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

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
		customerr = tryUnmount(customPoolVolumeMntPoint, 0)
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

	logger.Debugf("Unmounted LVM storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return ourUmount, nil
}

func (s *storageLvm) GetStoragePoolWritable() api.StoragePoolPut {
	return s.pool.Writable()
}

func (s *storageLvm) GetStoragePoolVolumeWritable() api.StorageVolumePut {
	return s.volume.Writable()
}

func (s *storageLvm) SetStoragePoolWritable(writable *api.StoragePoolPut) {
	s.pool.StoragePoolPut = *writable
}

func (s *storageLvm) SetStoragePoolVolumeWritable(writable *api.StorageVolumePut) {
	s.volume.StorageVolumePut = *writable
}

func (s *storageLvm) GetContainerPoolInfo() (int64, string, string) {
	return s.poolID, s.pool.Name, s.getOnDiskPoolName()
}

func (s *storageLvm) StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error {
	logger.Infof(`Updating LVM storage pool "%s"`, s.pool.Name)

	changeable := changeableStoragePoolProperties["lvm"]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		return updateStoragePoolError(unchangeable, "lvm")
	}

	// "volume.block.mount_options" requires no on-disk modifications.
	// "volume.block.filesystem" requires no on-disk modifications.
	// "volume.size" requires no on-disk modifications.
	// "rsync.bwlimit" requires no on-disk modifications.

	revert := true

	if shared.StringInSlice("lvm.thinpool_name", changedConfig) {
		if !s.useThinpool {
			return fmt.Errorf(`The LVM storage pool "%s" does `+
				`not use thin pools. The "lvm.thinpool_name" `+
				`property cannot be set`, s.pool.Name)
		}

		newThinpoolName := writable.Config["lvm.thinpool_name"]
		// Paranoia check
		if newThinpoolName == "" {
			return fmt.Errorf(`Could not rename volume group: No ` +
				`new name provided`)
		}

		poolName := s.getOnDiskPoolName()
		oldThinpoolName := s.getLvmThinpoolName()
		err := lvmLVRename(poolName, oldThinpoolName, newThinpoolName)
		if err != nil {
			return err
		}

		// Already set the new thinpool name so that any potentially
		// following operations use the correct on-disk name of the
		// volume group.
		s.setLvmThinpoolName(newThinpoolName)
		defer func() {
			if !revert {
				return
			}

			err = lvmLVRename(poolName, newThinpoolName, oldThinpoolName)
			if err != nil {
				logger.Warnf(`Failed to rename LVM thinpool from "%s" to "%s": %s. Manual intervention needed`, newThinpoolName, oldThinpoolName, err)
			}
			s.setLvmThinpoolName(oldThinpoolName)
		}()
	}

	if shared.StringInSlice("lvm.vg_name", changedConfig) {
		newName := writable.Config["lvm.vg_name"]
		// Paranoia check
		if newName == "" {
			return fmt.Errorf(`Could not rename volume group: No ` +
				`new name provided`)
		}
		writable.Config["source"] = newName

		oldPoolName := s.getOnDiskPoolName()
		err := lvmVGRename(oldPoolName, newName)
		if err != nil {
			return err
		}

		// Already set the new dataset name so that any potentially
		// following operations use the correct on-disk name of the
		// volume group.
		s.setOnDiskPoolName(newName)
		defer func() {
			if !revert {
				return
			}

			err := lvmVGRename(newName, oldPoolName)
			if err != nil {
				logger.Warnf(`Failed to rename LVM volume group from "%s" to "%s": %s. Manual intervention needed`, newName, oldPoolName, err)
			}
			s.setOnDiskPoolName(oldPoolName)
		}()
	}

	// Update succeeded.
	revert = false

	logger.Infof(`Updated LVM storage pool "%s"`, s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeUpdate(writable *api.StorageVolumePut,
	changedConfig []string) error {

	if writable.Restore != "" {
		logger.Infof(`Restoring LVM storage volume "%s" from snapshot "%s"`,
			s.volume.Name, writable.Restore)

		_, err := s.StoragePoolVolumeUmount()
		if err != nil {
			return err
		}

		sourceLvmName := containerNameToLVName(fmt.Sprintf("%s/%s", s.volume.Name, writable.Restore))
		targetLvmName := s.volume.Name

		if s.useThinpool {
			poolName := s.getOnDiskPoolName()

			err := removeLV("default", poolName,
				storagePoolVolumeAPIEndpointCustom, targetLvmName)
			if err != nil {
				logger.Errorf("Failed to remove \"%s\": %s",
					targetLvmName, err)
			}

			_, err = s.createSnapshotLV("default", poolName, sourceLvmName,
				storagePoolVolumeAPIEndpointCustom, targetLvmName,
				storagePoolVolumeAPIEndpointCustom, false, true)
			if err != nil {
				return fmt.Errorf("Error creating snapshot LV: %v", err)
			}
		} else {
			poolName := s.getOnDiskPoolName()
			sourceName := fmt.Sprintf("%s/%s", s.volume.Name, writable.Restore)
			sourceVolumeMntPoint := getStoragePoolVolumeSnapshotMountPoint(poolName, sourceName)
			targetVolumeMntPoint := getStoragePoolVolumeMountPoint(poolName, s.volume.Name)

			bwlimit := s.pool.Config["rsync.bwlimit"]
			output, err := rsyncLocalCopy(sourceVolumeMntPoint, targetVolumeMntPoint, bwlimit)
			if err != nil {
				return fmt.Errorf("failed to rsync container: %s: %s", string(output), err)
			}
		}

		logger.Infof(`Restored LVM storage volume "%s" from snapshot "%s"`,
			s.volume.Name, writable.Restore)
		return nil
	}

	logger.Infof(`Updating LVM storage volume "%s"`, s.volume.Name)

	changeable := changeableStoragePoolVolumeProperties["lvm"]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		return updateStoragePoolVolumeError(unchangeable, "lvm")
	}

	if shared.StringInSlice("size", changedConfig) {
		if s.volume.Type != storagePoolVolumeTypeNameCustom {
			return updateStoragePoolVolumeError([]string{"size"}, "lvm")
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

	logger.Infof(`Updated LVM storage volume "%s"`, s.volume.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeRename(newName string) error {
	logger.Infof(`Renaming LVM storage volume on storage pool "%s" from "%s" to "%s`,
		s.pool.Name, s.volume.Name, newName)

	_, err := s.StoragePoolVolumeUmount()
	if err != nil {
		return err
	}

	usedBy, err := storagePoolVolumeUsedByContainersGet(s.s, "default", s.volume.Name, storagePoolVolumeTypeNameCustom)
	if err != nil {
		return err
	}
	if len(usedBy) > 0 {
		return fmt.Errorf(`LVM storage volume "%s" on storage pool "%s" is attached to containers`,
			s.volume.Name, s.pool.Name)
	}

	err = s.renameLVByPath("default", s.volume.Name, newName,
		storagePoolVolumeAPIEndpointCustom)
	if err != nil {
		return fmt.Errorf(`Failed to rename logical volume from "%s" to "%s": %s`,
			s.volume.Name, newName, err)
	}

	sourceName, _, ok := containerGetParentAndSnapshotName(s.volume.Name)
	if !ok {
		return fmt.Errorf("Not a snapshot name")
	}
	fullSnapshotName := fmt.Sprintf("%s%s%s", sourceName, shared.SnapshotDelimiter, newName)
	oldPath := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	newPath := getStoragePoolVolumeMountPoint(s.pool.Name, fullSnapshotName)
	err = os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	logger.Infof(`Renamed ZFS storage volume on storage pool "%s" from "%s" to "%s`,
		s.pool.Name, s.volume.Name, newName)

	return s.s.Cluster.StoragePoolVolumeRename("default", s.volume.Name, newName,
		storagePoolVolumeTypeCustom, s.poolID)
}

func (s *storageLvm) ContainerStorageReady(container container) bool {
	containerLvmName := containerNameToLVName(container.Name())
	poolName := s.getOnDiskPoolName()
	containerLvmPath := getLvmDevPath(container.Project(), poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName)
	ok, _ := storageLVExists(containerLvmPath)
	return ok
}

func (s *storageLvm) ContainerCreate(container container) error {
	logger.Debugf("Creating empty LVM storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	tryUndo := true

	containerName := container.Name()
	containerLvmName := containerNameToLVName(containerName)
	thinPoolName := s.getLvmThinpoolName()
	lvFsType := s.getLvmFilesystem()
	lvSize, err := s.getLvmVolumeSize()
	if lvSize == "" {
		return err
	}

	poolName := s.getOnDiskPoolName()
	if s.useThinpool {
		err = lvmCreateThinpool(s.s, s.sTypeVersion, poolName, thinPoolName, lvFsType)
		if err != nil {
			return err
		}
	}

	err = lvmCreateLv(container.Project(), poolName, thinPoolName, containerLvmName, lvFsType, lvSize, storagePoolVolumeAPIEndpointContainers, s.useThinpool)
	if err != nil {
		return err
	}
	defer func() {
		if tryUndo {
			s.ContainerDelete(container)
		}
	}()

	if container.IsSnapshot() {
		containerMntPoint := getSnapshotMountPoint(container.Project(), s.pool.Name, containerName)
		sourceName, _, _ := containerGetParentAndSnapshotName(containerName)
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", projectPrefix(container.Project(), sourceName))
		snapshotMntPointSymlink := shared.VarPath("snapshots", projectPrefix(container.Project(), sourceName))
		err := os.MkdirAll(containerMntPoint, 0711)
		if err != nil {
			return err
		}
		err = createSnapshotMountpoint(containerMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return err
		}
	} else {
		containerMntPoint := getContainerMountPoint(container.Project(), s.pool.Name, containerName)
		containerPath := container.Path()
		err := os.MkdirAll(containerMntPoint, 0711)
		if err != nil {
			return err
		}
		err = createContainerMountpoint(containerMntPoint, containerPath, container.IsPrivileged())
		if err != nil {
			return err
		}
	}

	tryUndo = false

	logger.Debugf("Created empty LVM storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerCreateFromImage(container container, fingerprint string) error {
	logger.Debugf("Creating LVM storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	tryUndo := true

	containerName := container.Name()
	containerLvmName := containerNameToLVName(containerName)

	var err error
	if s.useThinpool {
		err = s.containerCreateFromImageThinLv(container, fingerprint)
	} else {
		err = s.containerCreateFromImageLv(container, fingerprint)
	}
	if err != nil {
		logger.Errorf(`Failed to create LVM storage volume for container "%s" on storage pool "%s": %s`, containerName, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Created LVM storage volume for container "%s" on storage pool "%s"`, containerName, s.pool.Name)
	defer func() {
		if tryUndo {
			s.ContainerDelete(container)
		}
	}()

	containerMntPoint := getContainerMountPoint(container.Project(), s.pool.Name, containerName)
	containerPath := container.Path()
	err = os.MkdirAll(containerMntPoint, 0711)
	if err != nil {
		return errors.Wrapf(err, "Create container mount point directory at %s", containerMntPoint)
	}
	err = createContainerMountpoint(containerMntPoint, containerPath, container.IsPrivileged())
	if err != nil {
		return errors.Wrap(err, "Create container mount point")
	}

	poolName := s.getOnDiskPoolName()
	containerLvDevPath := getLvmDevPath(container.Project(), poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName)
	// Generate a new xfs's UUID
	lvFsType := s.getLvmFilesystem()
	msg, err := fsGenerateNewUUID(lvFsType, containerLvDevPath)
	if err != nil {
		logger.Errorf("Failed to create new \"%s\" UUID for container \"%s\" on storage pool \"%s\": %s", lvFsType, containerName, s.pool.Name, msg)
		return err
	}

	ourMount, err := s.ContainerMount(container)
	if err != nil {
		return errors.Wrap(err, "Container mount")
	}
	if ourMount {
		defer s.ContainerUmount(container, containerPath)
	}

	if container.IsPrivileged() {
		err = os.Chmod(containerMntPoint, 0700)
	} else {
		err = os.Chmod(containerMntPoint, 0711)
	}
	if err != nil {
		return errors.Wrap(err, "Set mount point permissions")
	}

	if !container.IsPrivileged() {
		err := s.shiftRootfs(container, nil)
		if err != nil {
			return errors.Wrap(err, "Shift rootfs")
		}
	}

	err = container.TemplateApply("create")
	if err != nil {
		logger.Errorf("Error in create template during ContainerCreateFromImage, continuing to unmount: %s", err)
		return err
	}

	tryUndo = false

	logger.Debugf("Created LVM storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func lvmContainerDeleteInternal(project, poolName string, ctName string, isSnapshot bool, vgName string, ctPath string) error {
	containerMntPoint := ""
	containerLvmName := containerNameToLVName(ctName)
	if isSnapshot {
		containerMntPoint = getSnapshotMountPoint(project, poolName, ctName)
	} else {
		containerMntPoint = getContainerMountPoint(project, poolName, ctName)
	}

	if shared.IsMountPoint(containerMntPoint) {
		err := tryUnmount(containerMntPoint, 0)
		if err != nil {
			return fmt.Errorf(`Failed to unmount container path `+
				`"%s": %s`, containerMntPoint, err)
		}
	}

	containerLvmDevPath := getLvmDevPath(project, vgName,
		storagePoolVolumeAPIEndpointContainers, containerLvmName)

	lvExists, _ := storageLVExists(containerLvmDevPath)
	if lvExists {
		err := removeLV(project, vgName, storagePoolVolumeAPIEndpointContainers, containerLvmName)
		if err != nil {
			return err
		}
	}

	var err error
	if isSnapshot {
		sourceName, _, _ := containerGetParentAndSnapshotName(ctName)
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", poolName, "containers-snapshots", projectPrefix(project, sourceName))
		snapshotMntPointSymlink := shared.VarPath("snapshots", projectPrefix(project, sourceName))
		err = deleteSnapshotMountpoint(containerMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	} else {
		err = deleteContainerMountpoint(containerMntPoint, ctPath, "lvm")
	}
	if err != nil {
		return err
	}

	return nil
}

func (s *storageLvm) ContainerDelete(container container) error {
	logger.Debugf("Deleting LVM storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	containerName := container.Name()
	poolName := s.getOnDiskPoolName()
	err := lvmContainerDeleteInternal(container.Project(), s.pool.Name, containerName, container.IsSnapshot(), poolName, container.Path())
	if err != nil {
		return err
	}

	logger.Debugf("Deleted LVM storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerCopy(target container, source container, containerOnly bool) error {
	logger.Debugf("Copying LVM container storage for container %s to %s", source.Name(), target.Name())

	err := s.doContainerCopy(target, source, containerOnly, false, nil)
	if err != nil {
		return err
	}

	logger.Debugf("Copied LVM container storage for container %s to %s", source.Name(), target.Name())
	return nil
}

func (s *storageLvm) doContainerCopy(target container, source container, containerOnly bool, refresh bool, refreshSnapshots []container) error {
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
	srcState := s.s
	if sourcePool != targetPool {
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
		srcState = srcStorage.GetState()
	}

	err = s.copyContainer(target, source, refresh)
	if err != nil {
		return err
	}

	if containerOnly {
		return nil
	}

	var snapshots []container

	if refresh {
		snapshots = refreshSnapshots
	} else {
		snapshots, err = source.Snapshots()
		if err != nil {
			return err
		}
	}

	if len(snapshots) == 0 {
		return nil
	}

	for _, snap := range snapshots {
		_, snapOnlyName, _ := containerGetParentAndSnapshotName(snap.Name())
		newSnapName := fmt.Sprintf("%s/%s", target.Name(), snapOnlyName)

		logger.Debugf("Copying LVM container storage for snapshot %s to %s", snap.Name(), newSnapName)

		sourceSnapshot, err := containerLoadByProjectAndName(srcState, source.Project(), snap.Name())
		if err != nil {
			return err
		}

		targetSnapshot, err := containerLoadByProjectAndName(s.s, source.Project(), newSnapName)
		if err != nil {
			return err
		}

		err = s.copySnapshot(targetSnapshot, sourceSnapshot, refresh)
		if err != nil {
			return err
		}

		logger.Debugf("Copied LVM container storage for snapshot %s to %s", snap.Name(), newSnapName)
	}

	return nil
}

func (s *storageLvm) ContainerRefresh(target container, source container, snapshots []container) error {
	logger.Debugf("Refreshing LVM container storage for %s from %s", target.Name(), source.Name())

	err := s.doContainerCopy(target, source, len(snapshots) == 0, true, snapshots)
	if err != nil {
		return err
	}

	logger.Debugf("Refreshed LVM container storage for %s from %s", target.Name(), source.Name())
	return nil
}

func (s *storageLvm) ContainerMount(c container) (bool, error) {
	return s.doContainerMount(c.Project(), c.Name())
}

func (s *storageLvm) doContainerMount(project, name string) (bool, error) {
	logger.Debugf("Mounting LVM storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	containerLvmName := containerNameToLVName(name)
	lvFsType := s.getLvmFilesystem()
	poolName := s.getOnDiskPoolName()
	containerLvmPath := getLvmDevPath(project, poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName)
	containerMntPoint := getContainerMountPoint(project, s.pool.Name, name)
	if shared.IsSnapshot(name) {
		containerMntPoint = getSnapshotMountPoint(project, s.pool.Name, name)
	}

	containerMountLockID := getContainerMountLockID(s.pool.Name, name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore, this should not have happened")
		}
		// Give the benefit of the doubt and assume that the other
		// thread actually succeeded in mounting the storage volume.
		return false, nil
	}

	lxdStorageOngoingOperationMap[containerMountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	var mounterr error
	ourMount := false
	if !shared.IsMountPoint(containerMntPoint) {
		mountFlags, mountOptions := lxdResolveMountoptions(s.getLvmMountOptions())
		mounterr = tryMount(containerLvmPath, containerMntPoint, lvFsType, mountFlags, mountOptions)
		ourMount = true
	}

	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerMountLockID]; ok {
		close(waitChannel)
		delete(lxdStorageOngoingOperationMap, containerMountLockID)
	}
	lxdStorageMapLock.Unlock()

	if mounterr != nil {
		return false, errors.Wrapf(mounterr, "Mount %s onto %s", containerLvmPath, containerMntPoint)
	}

	logger.Debugf("Mounted LVM storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageLvm) ContainerUmount(c container, path string) (bool, error) {
	return s.umount(c.Project(), c.Name(), path)
}

func (s *storageLvm) umount(project, name string, path string) (bool, error) {
	logger.Debugf("Unmounting LVM storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	containerMntPoint := getContainerMountPoint(project, s.pool.Name, name)
	if shared.IsSnapshot(name) {
		containerMntPoint = getSnapshotMountPoint(project, s.pool.Name, name)
	}

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
	if shared.IsMountPoint(containerMntPoint) {
		imgerr = tryUnmount(containerMntPoint, 0)
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

	logger.Debugf("Unmounted LVM storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return ourUmount, nil
}

func (s *storageLvm) ContainerRename(container container, newContainerName string) error {
	logger.Debugf("Renaming LVM storage volume for container \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newContainerName)

	tryUndo := true

	oldName := container.Name()
	oldLvmName := containerNameToLVName(oldName)
	newLvmName := containerNameToLVName(newContainerName)

	_, err := s.ContainerUmount(container, container.Path())
	if err != nil {
		return err
	}

	err = s.renameLVByPath(container.Project(), oldLvmName, newLvmName, storagePoolVolumeAPIEndpointContainers)
	if err != nil {
		return fmt.Errorf("Failed to rename a container LV, oldName='%s', newName='%s', err='%s'", oldLvmName, newLvmName, err)
	}
	defer func() {
		if tryUndo {
			s.renameLVByPath(container.Project(), newLvmName, oldLvmName, storagePoolVolumeAPIEndpointContainers)
		}
	}()

	// MAYBE(FIXME(brauner)): Register another cleanup function that tries to
	// rename alreday renamed snapshots back to their old name when the
	// rename fails.
	if !container.IsSnapshot() {
		snaps, err := container.Snapshots()
		if err != nil {
			return err
		}

		for _, snap := range snaps {
			baseSnapName := filepath.Base(snap.Name())
			newSnapshotName := newContainerName + shared.SnapshotDelimiter + baseSnapName
			err := s.ContainerRename(snap, newSnapshotName)
			if err != nil {
				return err
			}
		}

		oldContainerMntPoint := getContainerMountPoint(container.Project(), s.pool.Name, oldName)
		oldContainerMntPointSymlink := container.Path()
		newContainerMntPoint := getContainerMountPoint(container.Project(), s.pool.Name, newContainerName)
		newContainerMntPointSymlink := shared.VarPath("containers", projectPrefix(container.Project(), newContainerName))
		err = renameContainerMountpoint(oldContainerMntPoint, oldContainerMntPointSymlink, newContainerMntPoint, newContainerMntPointSymlink)
		if err != nil {
			return err
		}

		oldSnapshotPath := getSnapshotMountPoint(container.Project(), s.pool.Name, oldName)
		newSnapshotPath := getSnapshotMountPoint(container.Project(), s.pool.Name, newContainerName)
		if shared.PathExists(oldSnapshotPath) {
			err = os.Rename(oldSnapshotPath, newSnapshotPath)
			if err != nil {
				return err
			}
		}

		oldSnapshotSymlink := shared.VarPath("snapshots", projectPrefix(container.Project(), oldName))
		newSnapshotSymlink := shared.VarPath("snapshots", projectPrefix(container.Project(), newContainerName))
		if shared.PathExists(oldSnapshotSymlink) {
			err := os.Remove(oldSnapshotSymlink)
			if err != nil {
				return err
			}

			err = os.Symlink(newSnapshotPath, newSnapshotSymlink)
			if err != nil {
				return err
			}
		}
	}

	tryUndo = false

	logger.Debugf("Renamed LVM storage volume for container \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newContainerName)
	return nil
}

func (s *storageLvm) ContainerRestore(target container, source container) error {
	logger.Debugf("Restoring LVM storage volume for container \"%s\" from %s to %s", s.volume.Name, source.Name(), target.Name())

	_, sourcePool, _ := source.Storage().GetContainerPoolInfo()
	if s.pool.Name != sourcePool {
		return fmt.Errorf("containers must be on the same pool to be restored")
	}

	sourceName := source.Name()
	sourceLvmName := containerNameToLVName(sourceName)

	targetName := target.Name()
	targetLvmName := containerNameToLVName(targetName)
	targetPath := target.Path()
	if s.useThinpool {
		ourUmount, err := target.Storage().ContainerUmount(target, targetPath)
		if err != nil {
			return err
		}
		if ourUmount {
			defer target.Storage().ContainerMount(target)
		}

		poolName := s.getOnDiskPoolName()

		err = removeLV(target.Project(), poolName,
			storagePoolVolumeAPIEndpointContainers, targetLvmName)
		if err != nil {
			logger.Errorf("Failed to remove \"%s\": %s",
				targetLvmName, err)
		}

		_, err = s.createSnapshotLV(source.Project(), poolName, sourceLvmName,
			storagePoolVolumeAPIEndpointContainers, targetLvmName,
			storagePoolVolumeAPIEndpointContainers, false, true)
		if err != nil {
			return fmt.Errorf("Error creating snapshot LV: %v", err)
		}
	} else {
		ourStart, err := source.StorageStart()
		if err != nil {
			return err
		}
		if ourStart {
			defer source.StorageStop()
		}

		ourStart, err = target.StorageStart()
		if err != nil {
			return err
		}
		if ourStart {
			defer target.StorageStop()
		}

		poolName := s.getOnDiskPoolName()
		sourceName := source.Name()
		targetContainerMntPoint := getContainerMountPoint(target.Project(), poolName, targetName)
		sourceContainerMntPoint := getContainerMountPoint(target.Project(), poolName, sourceName)
		if source.IsSnapshot() {
			sourceContainerMntPoint = getSnapshotMountPoint(target.Project(), poolName, sourceName)
		}

		err = target.Freeze()
		if err != nil {
		}
		defer target.Unfreeze()

		bwlimit := s.pool.Config["rsync.bwlimit"]
		output, err := rsyncLocalCopy(sourceContainerMntPoint, targetContainerMntPoint, bwlimit)
		if err != nil {
			return fmt.Errorf("failed to rsync container: %s: %s", string(output), err)
		}
	}

	logger.Debugf("Restored LVM storage volume for container \"%s\" from %s to %s", s.volume.Name, sourceName, targetName)
	return nil
}

func (s *storageLvm) ContainerGetUsage(container container) (int64, error) {
	return -1, fmt.Errorf("the LVM container backend doesn't support quotas")
}

func (s *storageLvm) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	logger.Debugf("Creating LVM storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	err := s.createSnapshotContainer(snapshotContainer, sourceContainer, true)
	if err != nil {
		return err
	}

	logger.Debugf("Created LVM storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerSnapshotDelete(snapshotContainer container) error {
	logger.Debugf("Deleting LVM storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	err := s.ContainerDelete(snapshotContainer)
	if err != nil {
		return fmt.Errorf("Error deleting snapshot %s: %s", snapshotContainer.Name(), err)
	}

	logger.Debugf("Deleted LVM storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerSnapshotRename(snapshotContainer container, newContainerName string) error {
	logger.Debugf("Renaming LVM storage volume for snapshot \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newContainerName)

	tryUndo := true

	oldName := snapshotContainer.Name()
	oldLvmName := containerNameToLVName(oldName)
	newLvmName := containerNameToLVName(newContainerName)

	err := s.renameLVByPath(snapshotContainer.Project(), oldLvmName, newLvmName, storagePoolVolumeAPIEndpointContainers)
	if err != nil {
		return fmt.Errorf("Failed to rename a container LV, oldName='%s', newName='%s', err='%s'", oldLvmName, newLvmName, err)
	}
	defer func() {
		if tryUndo {
			s.renameLVByPath(snapshotContainer.Project(), newLvmName, oldLvmName, storagePoolVolumeAPIEndpointContainers)
		}
	}()

	oldSnapshotMntPoint := getSnapshotMountPoint(snapshotContainer.Project(), s.pool.Name, oldName)
	newSnapshotMntPoint := getSnapshotMountPoint(snapshotContainer.Project(), s.pool.Name, newContainerName)
	err = os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
	if err != nil {
		return err
	}

	tryUndo = false

	logger.Debugf("Renamed LVM storage volume for snapshot \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newContainerName)
	return nil
}

func (s *storageLvm) ContainerSnapshotStart(container container) (bool, error) {
	logger.Debugf(`Initializing LVM storage volume for snapshot "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)

	poolName := s.getOnDiskPoolName()
	containerName := container.Name()
	containerLvmName := containerNameToLVName(containerName)
	containerLvmPath := getLvmDevPath(container.Project(), poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName)

	wasWritableAtCheck, err := lvmLvIsWritable(containerLvmPath)
	if err != nil {
		return false, err
	}

	if !wasWritableAtCheck {
		output, err := shared.TryRunCommand("lvchange", "-prw", fmt.Sprintf("%s/%s_%s", poolName, storagePoolVolumeAPIEndpointContainers, projectPrefix(container.Project(), containerLvmName)))
		if err != nil {
			logger.Errorf("Failed to make LVM snapshot \"%s\" read-write: %s", containerName, output)
			return false, err
		}
	}

	lvFsType := s.getLvmFilesystem()
	containerMntPoint := getSnapshotMountPoint(container.Project(), s.pool.Name, containerName)
	if !shared.IsMountPoint(containerMntPoint) {
		mntOptString := s.getLvmMountOptions()
		mountFlags, mountOptions := lxdResolveMountoptions(mntOptString)

		if lvFsType == "xfs" {
			idx := strings.Index(mountOptions, "nouuid")
			if idx < 0 {
				mountOptions += ",nouuid"
			}
		}

		err = tryMount(containerLvmPath, containerMntPoint, lvFsType, mountFlags, mountOptions)
		if err != nil {
			logger.Errorf(`Failed to mount LVM snapshot "%s" with filesystem "%s" options "%s" onto "%s": %s`, s.volume.Name, lvFsType, mntOptString, containerMntPoint, err)
			return false, err
		}
	}

	logger.Debugf(`Initialized LVM storage volume for snapshot "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)

	if wasWritableAtCheck {
		return false, nil
	}

	return true, nil
}

func (s *storageLvm) ContainerSnapshotStop(container container) (bool, error) {
	logger.Debugf("Stopping LVM storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	containerName := container.Name()
	snapshotMntPoint := getSnapshotMountPoint(container.Project(), s.pool.Name, containerName)

	poolName := s.getOnDiskPoolName()

	if shared.IsMountPoint(snapshotMntPoint) {
		err := tryUnmount(snapshotMntPoint, 0)
		if err != nil {
			return false, err
		}
	}

	containerLvmPath := getLvmDevPath(container.Project(), poolName, storagePoolVolumeAPIEndpointContainers, containerNameToLVName(containerName))
	wasWritableAtCheck, err := lvmLvIsWritable(containerLvmPath)
	if err != nil {
		return false, err
	}

	if wasWritableAtCheck {
		containerLvmName := containerNameToLVName(projectPrefix(container.Project(), containerName))
		output, err := shared.TryRunCommand("lvchange", "-pr", fmt.Sprintf("%s/%s_%s", poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName))
		if err != nil {
			logger.Errorf("Failed to make LVM snapshot read-only: %s", output)
			return false, err
		}
	}

	logger.Debugf("Stopped LVM storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	if wasWritableAtCheck {
		return false, nil
	}

	return true, nil
}

func (s *storageLvm) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	logger.Debugf("Creating empty LVM storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	err := s.ContainerCreate(snapshotContainer)
	if err != nil {
		return err
	}

	logger.Debugf("Created empty LVM storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerBackupCreate(backup backup, source container) error {
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

	// Prepare for rsync
	rsync := func(oldPath string, newPath string, bwlimit string) error {
		output, err := rsyncLocalCopy(oldPath, newPath, bwlimit)
		if err != nil {
			return fmt.Errorf("Failed to rsync: %s: %s", string(output), err)
		}

		return nil
	}

	bwlimit := s.pool.Config["rsync.bwlimit"]

	// Handle snapshots
	if !backup.containerOnly {
		snapshotsPath := fmt.Sprintf("%s/snapshots", tmpPath)

		// Retrieve the snapshots
		snapshots, err := source.Snapshots()
		if err != nil {
			return err
		}

		// Create the snapshot path
		if len(snapshots) > 0 {
			err = os.MkdirAll(snapshotsPath, 0711)
			if err != nil {
				return err
			}
		}

		for _, snap := range snapshots {
			_, snapName, _ := containerGetParentAndSnapshotName(snap.Name())
			snapshotMntPoint := getSnapshotMountPoint(snap.Project(), s.pool.Name, snap.Name())
			target := fmt.Sprintf("%s/%s", snapshotsPath, snapName)

			// Mount the snapshot
			_, err := s.ContainerSnapshotStart(snap)
			if err != nil {
				return err
			}

			// Copy the snapshot
			err = rsync(snapshotMntPoint, target, bwlimit)
			s.ContainerSnapshotStop(snap)
			if err != nil {
				return err
			}
		}
	}

	// Make a temporary snapshot of the container
	sourceLvmDatasetSnapshot := fmt.Sprintf("snapshot-%s", uuid.NewRandom().String())
	tmpContainerMntPoint := getContainerMountPoint(source.Project(), s.pool.Name, sourceLvmDatasetSnapshot)
	err = os.MkdirAll(tmpContainerMntPoint, 0700)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	_, err = s.createSnapshotLV(source.Project(), s.pool.Name, source.Name(),
		storagePoolVolumeAPIEndpointContainers, containerNameToLVName(sourceLvmDatasetSnapshot),
		storagePoolVolumeAPIEndpointContainers, false, s.useThinpool)
	if err != nil {
		return err
	}
	defer removeLV(source.Project(), s.pool.Name, storagePoolVolumeAPIEndpointContainers,
		containerNameToLVName(sourceLvmDatasetSnapshot))

	// Mount the temporary snapshot
	_, err = s.doContainerMount(source.Project(), sourceLvmDatasetSnapshot)
	if err != nil {
		return err
	}

	// Copy the container
	containerPath := fmt.Sprintf("%s/container", tmpPath)
	err = rsync(tmpContainerMntPoint, containerPath, bwlimit)
	s.umount(source.Project(), sourceLvmDatasetSnapshot, "")
	if err != nil {
		return err
	}

	// Pack the backup
	err = backupCreateTarball(s.s, tmpPath, backup)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageLvm) ContainerBackupLoad(info backupInfo, data io.ReadSeeker, tarArgs []string) error {
	containerPath, err := s.doContainerBackupLoad(info.Project, info.Name, info.Privileged, false)
	if err != nil {
		return err
	}

	// Prepare tar arguments
	args := append(tarArgs, []string{
		"-",
		"--strip-components=2",
		"--xattrs-include=*",
		"-C", containerPath, "backup/container",
	}...)

	// Extract container
	data.Seek(0, 0)
	err = shared.RunCommandWithFds(data, nil, "tar", args...)
	if err != nil {
		return err
	}

	for _, snap := range info.Snapshots {
		containerPath, err := s.doContainerBackupLoad(info.Project, fmt.Sprintf("%s/%s", info.Name, snap),
			info.Privileged, true)
		if err != nil {
			return err
		}

		// Prepare tar arguments
		args := append(tarArgs, []string{
			"-",
			"--strip-components=3",
			"--xattrs-include=*",
			"-C", containerPath, fmt.Sprintf("backup/snapshots/%s", snap),
		}...)

		// Extract snapshots
		data.Seek(0, 0)
		err = shared.RunCommandWithFds(data, nil, "tar", args...)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageLvm) doContainerBackupLoad(project, containerName string, privileged bool,
	snapshot bool) (string, error) {
	tryUndo := true

	var containerPath string
	if snapshot {
		containerPath = shared.VarPath("snapshots", projectPrefix(project, containerName))
	} else {
		containerPath = shared.VarPath("containers", projectPrefix(project, containerName))
	}
	containerLvmName := containerNameToLVName(containerName)
	thinPoolName := s.getLvmThinpoolName()
	lvFsType := s.getLvmFilesystem()
	lvSize, err := s.getLvmVolumeSize()
	if lvSize == "" {
		return "", err
	}

	poolName := s.getOnDiskPoolName()
	if s.useThinpool {
		err = lvmCreateThinpool(s.s, s.sTypeVersion, poolName, thinPoolName, lvFsType)
		if err != nil {
			return "", err
		}
	}

	if !snapshot {
		err = lvmCreateLv(project, poolName, thinPoolName, containerLvmName, lvFsType, lvSize,
			storagePoolVolumeAPIEndpointContainers, s.useThinpool)
	} else {
		cname, _, _ := containerGetParentAndSnapshotName(containerName)
		_, err = s.createSnapshotLV(project, poolName, cname, storagePoolVolumeAPIEndpointContainers,
			containerLvmName, storagePoolVolumeAPIEndpointContainers, false, s.useThinpool)
	}
	if err != nil {
		return "", err
	}

	defer func() {
		if tryUndo {
			lvmContainerDeleteInternal(project, s.pool.Name, containerName, false, poolName,
				containerPath)
		}
	}()

	var containerMntPoint string
	if snapshot {
		containerMntPoint = getSnapshotMountPoint(project, s.pool.Name, containerName)
	} else {
		containerMntPoint = getContainerMountPoint(project, s.pool.Name, containerName)
	}
	err = os.MkdirAll(containerMntPoint, 0711)
	if err != nil {
		return "", err
	}

	if snapshot {
		cname, _, _ := containerGetParentAndSnapshotName(containerName)
		snapshotMntPointSymlink := shared.VarPath("snapshots", projectPrefix(project, cname))
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", projectPrefix(project, cname))
		err = createSnapshotMountpoint(containerMntPoint, snapshotMntPointSymlinkTarget,
			snapshotMntPointSymlink)
	} else {
		err = createContainerMountpoint(containerMntPoint, containerPath, privileged)
	}
	if err != nil {
		return "", err
	}

	_, err = s.doContainerMount(project, containerName)
	if err != nil {
		return "", err
	}

	tryUndo = false

	return containerPath, nil
}

func (s *storageLvm) ImageCreate(fingerprint string) error {
	logger.Debugf("Creating LVM storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)

	tryUndo := true
	trySubUndo := true

	poolName := s.getOnDiskPoolName()
	thinPoolName := s.getLvmThinpoolName()
	lvFsType := s.getLvmFilesystem()
	lvSize, err := s.getLvmVolumeSize()
	if lvSize == "" {
		return err
	}

	err = s.createImageDbPoolVolume(fingerprint)
	if err != nil {
		return err
	}
	defer func() {
		if !trySubUndo {
			return
		}
		err := s.deleteImageDbPoolVolume(fingerprint)
		if err != nil {
			logger.Warnf("Could not delete image \"%s\" from storage volume database, manual intervention needed", fingerprint)
		}
	}()

	if s.useThinpool {
		err = lvmCreateThinpool(s.s, s.sTypeVersion, poolName, thinPoolName, lvFsType)
		if err != nil {
			return err
		}

		err = lvmCreateLv("default", poolName, thinPoolName, fingerprint, lvFsType, lvSize, storagePoolVolumeAPIEndpointImages, true)
		if err != nil {
			return fmt.Errorf("Error Creating LVM LV for new image: %v", err)
		}
		defer func() {
			if tryUndo {
				s.ImageDelete(fingerprint)
			}
		}()
	}
	trySubUndo = false

	// Create image mountpoint.
	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if !shared.PathExists(imageMntPoint) {
		err := os.MkdirAll(imageMntPoint, 0700)
		if err != nil {
			return err
		}
	}

	if s.useThinpool {
		_, err = s.ImageMount(fingerprint)
		if err != nil {
			return err
		}

		imagePath := shared.VarPath("images", fingerprint)
		err = unpackImage(imagePath, imageMntPoint, storageTypeLvm, s.s.OS.RunningInUserNS)
		if err != nil {
			return err
		}

		s.ImageUmount(fingerprint)
	}

	tryUndo = false

	logger.Debugf("Created LVM storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
	return nil
}

func (s *storageLvm) ImageDelete(fingerprint string) error {
	logger.Debugf("Deleting LVM storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)

	if s.useThinpool {
		poolName := s.getOnDiskPoolName()
		imageLvmDevPath := getLvmDevPath("default", poolName,
			storagePoolVolumeAPIEndpointImages, fingerprint)
		lvExists, _ := storageLVExists(imageLvmDevPath)

		if lvExists {
			_, err := s.ImageUmount(fingerprint)
			if err != nil {
				return err
			}

			err = removeLV("default", poolName, storagePoolVolumeAPIEndpointImages, fingerprint)
			if err != nil {
				return err
			}
		}
	}

	err := s.deleteImageDbPoolVolume(fingerprint)
	if err != nil {
		return err
	}

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if shared.PathExists(imageMntPoint) {
		err := os.Remove(imageMntPoint)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Deleted LVM storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
	return nil
}

func (s *storageLvm) ImageMount(fingerprint string) (bool, error) {
	logger.Debugf("Mounting LVM storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if shared.IsMountPoint(imageMntPoint) {
		return false, nil
	}

	// Shouldn't happen.
	lvmFstype := s.getLvmFilesystem()
	if lvmFstype == "" {
		return false, fmt.Errorf("no filesystem type specified")
	}

	poolName := s.getOnDiskPoolName()
	lvmVolumePath := getLvmDevPath("default", poolName, storagePoolVolumeAPIEndpointImages, fingerprint)
	mountFlags, mountOptions := lxdResolveMountoptions(s.getLvmMountOptions())
	err := tryMount(lvmVolumePath, imageMntPoint, lvmFstype, mountFlags, mountOptions)
	if err != nil {
		logger.Errorf(fmt.Sprintf("Error mounting image LV for unpacking: %s", err))
		return false, fmt.Errorf("Error mounting image LV: %v", err)
	}

	logger.Debugf("Mounted LVM storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
	return true, nil
}

func (s *storageLvm) ImageUmount(fingerprint string) (bool, error) {
	logger.Debugf("Unmounting LVM storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if !shared.IsMountPoint(imageMntPoint) {
		return false, nil
	}

	err := tryUnmount(imageMntPoint, 0)
	if err != nil {
		return false, err
	}

	logger.Debugf("Unmounted LVM storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
	return true, nil
}

func (s *storageLvm) MigrationType() migration.MigrationFSType {
	return migration.MigrationFSType_RSYNC
}

func (s *storageLvm) PreservesInodes() bool {
	return false
}

func (s *storageLvm) MigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	return rsyncMigrationSource(args)
}

func (s *storageLvm) MigrationSink(conn *websocket.Conn, op *operation, args MigrationSinkArgs) error {
	return rsyncMigrationSink(conn, op, args)
}

func (s *storageLvm) StorageEntitySetQuota(volumeType int, size int64, data interface{}) error {
	logger.Debugf(`Setting LVM quota for "%s"`, s.volume.Name)

	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return fmt.Errorf("Invalid storage type")
	}

	poolName := s.getOnDiskPoolName()
	var c container
	fsType := s.getLvmFilesystem()
	lvDevPath := ""
	mountpoint := ""
	switch volumeType {
	case storagePoolVolumeTypeContainer:
		c = data.(container)
		ctName := c.Name()
		if c.IsRunning() {
			msg := fmt.Sprintf(`Cannot resize LVM storage volume `+
				`for container "%s" when it is running`,
				ctName)
			logger.Errorf(msg)
			return fmt.Errorf(msg)
		}

		ctLvmName := containerNameToLVName(ctName)
		lvDevPath = getLvmDevPath("default", poolName, storagePoolVolumeAPIEndpointContainers, ctLvmName)
		mountpoint = getContainerMountPoint("default", s.pool.Name, ctName)
	default:
		lvDevPath = getLvmDevPath("default", poolName, storagePoolVolumeAPIEndpointCustom, s.volume.Name)
		mountpoint = getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	}

	oldSize, err := shared.ParseByteSizeString(s.volume.Config["size"])
	if err != nil {
		return err
	}

	// The right disjunct just means that someone unset the size property in
	// the container's config. We obviously cannot resize to 0.
	if oldSize == size || size == 0 {
		return nil
	}

	if size < oldSize {
		err = s.lvReduce(lvDevPath, size, fsType, mountpoint, volumeType, data)
	} else if size > oldSize {
		err = s.lvExtend(lvDevPath, size, fsType, mountpoint, volumeType, data)
	}
	if err != nil {
		return err
	}

	// Update the database
	s.volume.Config["size"] = shared.GetByteSizeString(size, 0)
	err = s.s.Cluster.StoragePoolVolumeUpdate(
		s.volume.Name,
		volumeType,
		s.poolID,
		s.volume.Description,
		s.volume.Config)
	if err != nil {
		return err
	}

	logger.Debugf(`Set LVM quota for "%s"`, s.volume.Name)
	return nil
}

func (s *storageLvm) StoragePoolResources() (*api.ResourcesStoragePool, error) {
	args := []string{s.pool.Config["lvm.vg_name"], "--noheadings",
		"--units", "b", "--nosuffix", "-o"}

	totalBuf, err := shared.TryRunCommand("vgs", append(args, "vg_size")...)
	if err != nil {
		return nil, err
	}

	totalStr := string(totalBuf)
	totalStr = strings.TrimSpace(totalStr)
	total, err := strconv.ParseUint(totalStr, 10, 64)
	if err != nil {
		return nil, err
	}

	res := api.ResourcesStoragePool{}
	res.Space.Total = total

	// Thinpools will always report zero free space so no use in calculating
	// a used count. It'll be useless information for the user.
	if !s.useThinpool {
		freeBuf, err := shared.TryRunCommand("vgs", append(args, "vg_free")...)
		if err != nil {
			return nil, err
		}
		freeStr := string(freeBuf)
		freeStr = strings.TrimSpace(freeStr)
		free, err := strconv.ParseUint(freeStr, 10, 64)
		if err != nil {
			return nil, err
		}
		res.Space.Used = total - free
	}

	return &res, nil
}

func (s *storageLvm) StoragePoolVolumeCopy(source *api.StorageVolumeSource) error {
	logger.Infof("Copying LVM storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)
	successMsg := fmt.Sprintf("Copied LVM storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)

	srcMountPoint := getStoragePoolVolumeMountPoint(source.Pool, source.Name)
	dstMountPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	sourceName := source.Name
	targetName := s.volume.Name

	if strings.Contains(sourceName, "/") {
		sourceName = containerNameToLVName(sourceName)
	}

	if strings.Contains(targetName, "/") {
		targetName = containerNameToLVName(targetName)
	}

	if s.pool.Name == source.Pool && s.useThinpool {
		err := os.MkdirAll(dstMountPoint, 0711)
		if err != nil {
			logger.Errorf("Failed to create mountpoint \"%s\" for LVM storage volume \"%s\" on storage pool \"%s\": %s", dstMountPoint, s.volume.Name, s.pool.Name, err)
			return err
		}

		poolName := s.getOnDiskPoolName()
		lvFsType := s.getLvmFilesystem()
		lvSize, err := s.getLvmVolumeSize()
		if lvSize == "" {
			logger.Errorf("Failed to get size for LVM storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
			return err
		}

		_, err = s.createSnapshotLV("default", poolName, sourceName, storagePoolVolumeAPIEndpointCustom, targetName, storagePoolVolumeAPIEndpointCustom, false, s.useThinpool)
		if err != nil {
			logger.Errorf("Failed to create snapshot for LVM storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
			return err
		}

		lvDevPath := getLvmDevPath("default", poolName, storagePoolVolumeAPIEndpointCustom, targetName)
		msg, err := fsGenerateNewUUID(lvFsType, lvDevPath)
		if err != nil {
			logger.Errorf("Failed to create new UUID for filesystem \"%s\" for RBD storage volume \"%s\" on storage pool \"%s\": %s: %s", lvFsType, s.volume.Name, s.pool.Name, msg, err)
			return err
		}

		logger.Infof(successMsg)
		return nil
	}

	if s.pool.Name != source.Pool {
		// setup storage for the source volume
		srcStorage, err := storagePoolVolumeInit(s.s, "default", source.Pool, source.Name, storagePoolVolumeTypeCustom)
		if err != nil {
			logger.Errorf("Failed to initialize LVM storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
			return err
		}

		ourMount, err := srcStorage.StoragePoolVolumeMount()
		if err != nil {
			logger.Errorf("Failed to mount LVM storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
			return err
		}

		if ourMount {
			defer srcStorage.StoragePoolVolumeUmount()
		}
	}

	err := s.StoragePoolVolumeCreate()
	if err != nil {
		logger.Errorf("Failed to create LVM storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	ourMount, err := s.StoragePoolVolumeMount()
	if err != nil {
		return err
	}
	if ourMount {
		defer s.StoragePoolVolumeUmount()
	}

	bwlimit := s.pool.Config["rsync.bwlimit"]
	_, err = rsyncLocalCopy(srcMountPoint, dstMountPoint, bwlimit)
	if err != nil {
		os.RemoveAll(dstMountPoint)
		logger.Errorf("Failed to rsync into LVM storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	logger.Infof(successMsg)
	return nil
}

func (s *storageLvm) StorageMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	return rsyncStorageMigrationSource(args)
}

func (s *storageLvm) StorageMigrationSink(conn *websocket.Conn, op *operation, args MigrationSinkArgs) error {
	return rsyncStorageMigrationSink(conn, op, args)
}

func (s *storageLvm) GetStoragePool() *api.StoragePool {
	return s.pool
}

func (s *storageLvm) GetStoragePoolVolume() *api.StorageVolume {
	return s.volume
}

func (s *storageLvm) GetState() *state.State {
	return s.s
}

func (s *storageLvm) StoragePoolVolumeSnapshotCreate(target *api.StorageVolumeSnapshotsPost) error {
	logger.Debugf("Creating LVM storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	poolName := s.getOnDiskPoolName()
	sourceOnlyName, _, ok := containerGetParentAndSnapshotName(target.Name)
	if !ok {
		return fmt.Errorf("Not a snapshot")
	}

	targetLvmName := containerNameToLVName(target.Name)
	_, err := s.createSnapshotLV("default", poolName, sourceOnlyName, storagePoolVolumeAPIEndpointCustom, targetLvmName, storagePoolVolumeAPIEndpointCustom, true, s.useThinpool)
	if err != nil {
		return fmt.Errorf("Failed to create snapshot logical volume %s", err)
	}

	targetPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, target.Name)
	err = os.MkdirAll(targetPath, snapshotsDirMode)
	if err != nil {
		logger.Errorf("Failed to create mountpoint \"%s\" for RBD storage volume \"%s\" on storage pool \"%s\": %s", targetPath, s.volume.Name, s.pool.Name, err)
		return err
	}

	logger.Debugf("Created LVM storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeSnapshotDelete() error {
	logger.Infof("Deleting LVM storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	snapshotLVName := containerNameToLVName(s.volume.Name)
	storageVolumeSnapshotPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	if shared.IsMountPoint(storageVolumeSnapshotPath) {
		err := tryUnmount(storageVolumeSnapshotPath, 0)
		if err != nil {
			return fmt.Errorf("Failed to unmount snapshot path \"%s\": %s", storageVolumeSnapshotPath, err)
		}
	}

	poolName := s.getOnDiskPoolName()
	snapshotLVDevPath := getLvmDevPath("default", poolName, storagePoolVolumeAPIEndpointCustom, snapshotLVName)
	lvExists, _ := storageLVExists(snapshotLVDevPath)
	if lvExists {
		err := removeLV("default", poolName, storagePoolVolumeAPIEndpointCustom, snapshotLVName)
		if err != nil {
			return err
		}
	}

	err := os.Remove(storageVolumeSnapshotPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	sourceName, _, _ := containerGetParentAndSnapshotName(s.volume.Name)
	storageVolumeSnapshotPath = getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, sourceName)
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
		logger.Errorf(`Failed to delete database entry for LVM storage volume "%s" on storage pool "%s"`,
			s.volume.Name, s.pool.Name)
	}

	logger.Infof("Deleted LVM storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeSnapshotRename(newName string) error {
	logger.Infof("Renaming LVM storage volume on storage pool \"%s\" from \"%s\" to \"%s\"", s.pool.Name, s.volume.Name, newName)

	_, err := s.StoragePoolVolumeUmount()
	if err != nil {
		return err
	}

	sourceName, _, ok := containerGetParentAndSnapshotName(s.volume.Name)
	if !ok {
		return fmt.Errorf("Not a snapshot name")
	}
	fullSnapshotName := fmt.Sprintf("%s%s%s", sourceName, shared.SnapshotDelimiter, newName)

	err = s.renameLVByPath("default", s.volume.Name, fullSnapshotName, storagePoolVolumeAPIEndpointCustom)
	if err != nil {
		return fmt.Errorf("Failed to rename logical volume from \"%s\" to \"%s\": %s", s.volume.Name, newName, err)
	}

	oldPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	newPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, fullSnapshotName)
	err = os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	logger.Infof("Renamed LVM storage volume on storage pool \"%s\" from \"%s\" to \"%s\"", s.pool.Name, s.volume.Name, newName)

	return s.s.Cluster.StoragePoolVolumeRename("default", s.volume.Name, newName, storagePoolVolumeTypeCustom, s.poolID)
}
