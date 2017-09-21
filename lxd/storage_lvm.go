package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
)

type storageLvm struct {
	vgName       string
	thinPoolName string
	useThinpool  bool
	loopInfo     *os.File
	storageShared
}

// Only initialize the minimal information we need about a given storage type.
func (s *storageLvm) StorageCoreInit() error {
	s.sType = storageTypeLvm
	typeName, err := storageTypeToString(s.sType)
	if err != nil {
		return err
	}
	s.sTypeName = typeName

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

	logger.Debugf("Initializing an LVM driver.")
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
	logger.Debugf("Checking LVM storage pool \"%s\".", s.pool.Name)

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

	logger.Debugf("Checked LVM storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolCreate() error {
	logger.Infof("Creating LVM storage pool \"%s\".", s.pool.Name)

	s.pool.Config["volatile.initial_source"] = s.pool.Config["source"]

	var globalErr error
	tryUndo := true
	pvExisted := false
	vgExisted := false
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

	if source == "" {
		source = filepath.Join(shared.VarPath("disks"), fmt.Sprintf("%s.img", s.pool.Name))
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
				return fmt.Errorf("custom loop file locations are not supported")
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
			if s.pool.Config["lvm.vg_name"] != "" {
				// User gave us something weird.
				return fmt.Errorf("invalid combination of \"source\" and \"zfs.pool_name\" property")
			}
			s.pool.Config["lvm.vg_name"] = vgName
			s.vgName = vgName

			vgExisted, globalErr = storageVGExists(vgName)
			if globalErr != nil {
				return globalErr
			}

			// Volume group must exist but doesn't.
			if !vgExisted {
				return fmt.Errorf("the requested volume group \"%s\" does not exist", vgName)
			}
		}
	}

	if !pvExisted {
		// This is an internal error condition which should never be
		// hit.
		if pvName == "" {
			logger.Errorf("no name for physical volume detected")
		}

		output, err := shared.TryRunCommand("pvcreate", pvName)
		if err != nil {
			return fmt.Errorf("failed to create the physical volume for the lvm storage pool: %s", output)
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
			logger.Errorf("failed to determine whether the volume group \"%s\" is empty", poolName)
			return err
		}

		empty := true
		if count > 0 && !s.useThinpool {
			empty = false
		}

		if count > 0 && s.useThinpool {
			ok, err := storageLVMThinpoolExists(poolName, s.thinPoolName)
			if err != nil {
				logger.Errorf("failed to determine whether thinpool \"%s\" exists in volume group \"%s\": %s", poolName, s.thinPoolName, err)
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
		inUse, user, err := lxdUsesPool(s.s.DB, poolName, s.pool.Driver, "lvm.vg_name")
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

	logger.Infof("Created LVM storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolDelete() error {
	logger.Infof("Deleting LVM storage pool \"%s\".", s.pool.Name)

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
	// Delete the thinpool.
	if s.useThinpool {
		// Check that the thinpool actually exists. For example, it
		// won't when the user has never created a storage volume in the
		// storage pool.
		devPath := getLvmDevPath(poolName, "", s.thinPoolName)
		ok, _ := storageLVExists(devPath)
		if ok {
			msg, err := shared.TryRunCommand("lvremove", "-f", devPath)
			if err != nil {
				logger.Errorf("failed to delete thinpool \"%s\" from volume group \"%s\": %s", s.thinPoolName, poolName, msg)
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
	if count == 0 {
		output, err := shared.TryRunCommand("vgremove", "-f", poolName)
		if err != nil {
			logger.Errorf("failed to destroy the volume group for the lvm storage pool: %s", output)
			return err
		}
	}

	if s.loopInfo != nil {
		// Set LO_FLAGS_AUTOCLEAR before we remove the loop file
		// otherwise we will get EBADF.
		err = setAutoclearOnLoopDev(int(s.loopInfo.Fd()))
		if err != nil {
			logger.Warnf("Failed to set LO_FLAGS_AUTOCLEAR on loop device: %s. Manual cleanup needed.", err)
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

	logger.Infof("Deleted LVM storage pool \"%s\".", s.pool.Name)
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
			logger.Warnf("Received value over semaphore. This should not have happened.")
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
	logger.Infof("Creating LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
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

	err = lvmCreateLv(poolName, thinPoolName, s.volume.Name, lvFsType, lvSize, volumeType, s.useThinpool)
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

	_, err = s.StoragePoolVolumeMount()
	if err != nil {
		return err
	}

	tryUndo = false

	logger.Infof("Created LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeDelete() error {
	logger.Infof("Deleting LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	_, err := s.StoragePoolVolumeUmount()
	if err != nil {
		return err
	}

	volumeType, err := storagePoolVolumeTypeNameToAPIEndpoint(s.volume.Type)
	if err != nil {
		return err
	}

	poolName := s.getOnDiskPoolName()
	err = s.removeLV(poolName, volumeType, s.volume.Name)
	if err != nil {
		return err
	}

	if shared.PathExists(customPoolVolumeMntPoint) {
		err := os.Remove(customPoolVolumeMntPoint)
		if err != nil {
			return err
		}
	}

	err = db.StoragePoolVolumeDelete(
		s.s.DB,
		s.volume.Name,
		storagePoolVolumeTypeCustom,
		s.poolID)
	if err != nil {
		logger.Errorf(`Failed to delete database entry for LVM `+
			`storage volume "%s" on storage pool "%s"`,
			s.volume.Name, s.pool.Name)
	}

	logger.Infof("Deleted LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeMount() (bool, error) {
	logger.Debugf("Mounting LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	poolName := s.getOnDiskPoolName()
	lvFsType := s.getLvmFilesystem()
	volumeType, err := storagePoolVolumeTypeNameToAPIEndpoint(s.volume.Type)
	if err != nil {
		return false, err
	}
	lvmVolumePath := getLvmDevPath(poolName, volumeType, s.volume.Name)

	customMountLockID := getCustomMountLockID(s.pool.Name, s.volume.Name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[customMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore. This should not have happened.")
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

	logger.Debugf("Mounted LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageLvm) StoragePoolVolumeUmount() (bool, error) {
	logger.Debugf("Unmounting LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	customUmountLockID := getCustomUmountLockID(s.pool.Name, s.volume.Name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[customUmountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore. This should not have happened.")
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

	logger.Debugf("Unmounted LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
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

func (s *storageLvm) GetContainerPoolInfo() (int64, string) {
	return s.poolID, s.pool.Name
}

func (s *storageLvm) StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error {
	logger.Infof("Updating LVM storage pool \"%s\".", s.pool.Name)

	if shared.StringInSlice("size", changedConfig) {
		return fmt.Errorf("the \"size\" property cannot be changed")
	}

	if shared.StringInSlice("source", changedConfig) {
		return fmt.Errorf("the \"source\" property cannot be changed")
	}

	if shared.StringInSlice("volume.zfs.use_refquota", changedConfig) {
		return fmt.Errorf("the \"volume.zfs.use_refquota\" property does not apply to LVM drivers")
	}

	if shared.StringInSlice("volume.zfs.remove_snapshots", changedConfig) {
		return fmt.Errorf("the \"volume.zfs.remove_snapshots\" property does not apply to LVM drivers")
	}

	if shared.StringInSlice("zfs.pool_name", changedConfig) {
		return fmt.Errorf("the \"zfs.pool_name\" property does not apply to LVM drivers")
	}

	// "volume.block.mount_options" requires no on-disk modifications.
	// "volume.block.filesystem" requires no on-disk modifications.
	// "volume.size" requires no on-disk modifications.
	// "rsync.bwlimit" requires no on-disk modifications.

	// Given a set of changeable pool properties the change should be
	// "transactional": either the whole update succeeds or none. So try to
	// revert on error.
	revert := true
	if shared.StringInSlice("lvm.use_thinpool", changedConfig) {
		return fmt.Errorf("the \"lvm.use_thinpool\" property cannot be changed")
	}

	if shared.StringInSlice("lvm.thinpool_name", changedConfig) {
		if !s.useThinpool {
			return fmt.Errorf("the LVM storage pool \"%s\" does not use thin pools. The \"lvm.thinpool_name\" property cannot be set", s.pool.Name)
		}

		newThinpoolName := writable.Config["lvm.thinpool_name"]
		// Paranoia check
		if newThinpoolName == "" {
			return fmt.Errorf("could not rename volume group: No new name provided")
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
				logger.Warnf("Failed to rename LVM thinpool from \"%s\" to \"%s\": %s. Manual intervention needed.",
					newThinpoolName,
					oldThinpoolName,
					err)
			}
			s.setLvmThinpoolName(oldThinpoolName)
		}()
	}

	if shared.StringInSlice("lvm.vg_name", changedConfig) {
		newName := writable.Config["lvm.vg_name"]
		// Paranoia check
		if newName == "" {
			return fmt.Errorf("could not rename volume group: No new name provided")
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
				logger.Warnf("Failed to rename LVM volume group from \"%s\" to \"%s\": %s. Manual intervention needed.",
					newName,
					oldPoolName)
			}
			s.setOnDiskPoolName(oldPoolName)
		}()
	}

	// Update succeeded.
	revert = false

	logger.Infof("Updated LVM storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error {
	logger.Infof("Updating LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	if !(shared.StringInSlice("block.mount_options", changedConfig) && len(changedConfig) == 1) &&
		!(shared.StringInSlice("block.mount_options", changedConfig) && len(changedConfig) == 2 && shared.StringInSlice("size", changedConfig)) &&
		!(shared.StringInSlice("size", changedConfig) && len(changedConfig) == 1) {
		return fmt.Errorf("the properties \"%v\" cannot be changed", changedConfig)
	}

	if shared.StringInSlice("size", changedConfig) {
		// apply quota
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

	logger.Infof("Updated LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerStorageReady(name string) bool {
	containerLvmName := containerNameToLVName(name)
	poolName := s.getOnDiskPoolName()
	containerLvmPath := getLvmDevPath(poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName)
	ok, _ := storageLVExists(containerLvmPath)
	return ok
}

func (s *storageLvm) ContainerCreate(container container) error {
	logger.Debugf("Creating empty LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

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

	err = lvmCreateLv(poolName, thinPoolName, containerLvmName, lvFsType, lvSize, storagePoolVolumeAPIEndpointContainers, s.useThinpool)
	if err != nil {
		return err
	}
	defer func() {
		if tryUndo {
			s.ContainerDelete(container)
		}
	}()

	if container.IsSnapshot() {
		containerMntPoint := getSnapshotMountPoint(s.pool.Name, containerName)
		sourceName, _, _ := containerGetParentAndSnapshotName(containerName)
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "snapshots", sourceName)
		snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
		err := os.MkdirAll(containerMntPoint, 0755)
		if err != nil {
			return err
		}
		err = createSnapshotMountpoint(containerMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return err
		}
	} else {
		containerMntPoint := getContainerMountPoint(s.pool.Name, containerName)
		containerPath := container.Path()
		err := os.MkdirAll(containerMntPoint, 0755)
		if err != nil {
			return err
		}
		err = createContainerMountpoint(containerMntPoint, containerPath, container.IsPrivileged())
		if err != nil {
			return err
		}
	}

	tryUndo = false

	logger.Debugf("Created empty LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerCreateFromImage(container container, fingerprint string) error {
	logger.Debugf("Creating LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

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
		logger.Errorf(`Failed to create LVM storage volume for `+
			`container "%s" on storage pool "%s": %s`, containerName,
			s.pool.Name, err)
		return err
	}
	logger.Debugf(`Created LVM storage volume for container "%s" on `+
		`storage pool "%s"`, containerName, s.pool.Name)
	defer func() {
		if tryUndo {
			s.ContainerDelete(container)
		}
	}()

	containerMntPoint := getContainerMountPoint(s.pool.Name, containerName)
	containerPath := container.Path()
	err = os.MkdirAll(containerMntPoint, 0755)
	if err != nil {
		return err
	}
	err = createContainerMountpoint(containerMntPoint, containerPath, container.IsPrivileged())
	if err != nil {
		return err
	}

	poolName := s.getOnDiskPoolName()
	containerLvDevPath := getLvmDevPath(poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName)
	// Generate a new xfs's UUID
	lvFsType := s.getLvmFilesystem()
	if lvFsType == "xfs" {
		_, err := xfsGenerateNewUUID(containerLvDevPath)
		if err != nil {
			return err
		}
	}

	ourMount, err := s.ContainerMount(container)
	if err != nil {
		return err
	}
	if ourMount {
		defer s.ContainerUmount(containerName, containerPath)
	}

	if container.IsPrivileged() {
		err = os.Chmod(containerMntPoint, 0700)
	} else {
		err = os.Chmod(containerMntPoint, 0755)
	}
	if err != nil {
		return err
	}

	if !container.IsPrivileged() {
		err := s.shiftRootfs(container)
		if err != nil {
			return err
		}
	}

	err = container.TemplateApply("create")
	if err != nil {
		logger.Errorf("Error in create template during ContainerCreateFromImage, continuing to unmount: %s.", err)
		return err
	}

	tryUndo = false

	logger.Debugf("Created LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageLvm) ContainerDelete(container container) error {
	logger.Debugf("Deleting LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	containerMntPoint := ""

	containerName := container.Name()
	containerLvmName := containerNameToLVName(containerName)
	if container.IsSnapshot() {
		containerMntPoint = getSnapshotMountPoint(s.pool.Name, containerName)
	} else {
		containerMntPoint = getContainerMountPoint(s.pool.Name, containerName)
	}

	// Make sure that the container is really unmounted at this point.
	// Otherwise we will fail.
	if shared.IsMountPoint(containerMntPoint) {
		err := tryUnmount(containerMntPoint, 0)
		if err != nil {
			return fmt.Errorf("Failed to unmount container path '%s': %s", containerMntPoint, err)
		}
	}

	poolName := s.getOnDiskPoolName()
	err := s.removeLV(poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName)
	if err != nil {
		return err
	}

	if container.IsSnapshot() {
		sourceName, _, _ := containerGetParentAndSnapshotName(containerName)
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "snapshots", sourceName)
		snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
		err = deleteSnapshotMountpoint(containerMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return err
		}
	} else {
		err = deleteContainerMountpoint(containerMntPoint, container.Path(), s.GetStorageTypeName())
		if err != nil {
			return err
		}
	}

	logger.Debugf("Deleted LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerCopy(target container, source container, containerOnly bool) error {
	logger.Debugf("Copying LVM container storage for container %s -> %s.", source.Name(), target.Name())

	ourStart, err := source.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer source.StorageStop()
	}

	_, sourcePool := source.Storage().GetContainerPoolInfo()
	_, targetPool := target.Storage().GetContainerPoolInfo()
	if sourcePool != targetPool {
		return fmt.Errorf("copying containers between different storage pools is not implemented")
	}

	err = s.copyContainer(target, source)
	if err != nil {
		return err
	}

	if containerOnly {
		logger.Debugf("Copied LVM container storage %s -> %s.", source.Name(), target.Name())
		return nil
	}

	snapshots, err := source.Snapshots()
	if err != nil {
		return err
	}

	if len(snapshots) == 0 {
		logger.Debugf("Copied LVM container storage %s -> %s.", source.Name(), target.Name())
		return nil
	}

	for _, snap := range snapshots {
		_, snapOnlyName, _ := containerGetParentAndSnapshotName(snap.Name())
		newSnapName := fmt.Sprintf("%s/%s", target.Name(), snapOnlyName)

		logger.Debugf("Copying LVM container storage for snapshot %s -> %s.", snap.Name(), newSnapName)

		sourceSnapshot, err := containerLoadByName(s.s, snap.Name())
		if err != nil {
			return err
		}

		targetSnapshot, err := containerLoadByName(s.s, newSnapName)
		if err != nil {
			return err
		}

		err = s.copySnapshot(targetSnapshot, sourceSnapshot)
		if err != nil {
			return err
		}

		logger.Debugf("Copied LVM container storage for snapshot %s -> %s.", snap.Name(), newSnapName)
	}

	logger.Debugf("Copied LVM container storage for container %s -> %s.", source.Name(), target.Name())
	return nil
}

func (s *storageLvm) ContainerMount(c container) (bool, error) {
	name := c.Name()
	logger.Debugf("Mounting LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	containerLvmName := containerNameToLVName(name)
	lvFsType := s.getLvmFilesystem()
	poolName := s.getOnDiskPoolName()
	containerLvmPath := getLvmDevPath(poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName)
	containerMntPoint := getContainerMountPoint(s.pool.Name, name)
	if shared.IsSnapshot(name) {
		containerMntPoint = getSnapshotMountPoint(s.pool.Name, name)
	}

	containerMountLockID := getContainerMountLockID(s.pool.Name, name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore. This should not have happened.")
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
		return false, mounterr
	}

	logger.Debugf("Mounted LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageLvm) ContainerUmount(name string, path string) (bool, error) {
	logger.Debugf("Unmounting LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	containerMntPoint := getContainerMountPoint(s.pool.Name, name)
	if shared.IsSnapshot(name) {
		containerMntPoint = getSnapshotMountPoint(s.pool.Name, name)
	}

	containerUmountLockID := getContainerUmountLockID(s.pool.Name, name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerUmountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore. This should not have happened.")
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

	logger.Debugf("Unmounted LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return ourUmount, nil
}

func (s *storageLvm) ContainerRename(container container, newContainerName string) error {
	logger.Debugf("Renaming LVM storage volume for container \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newContainerName)

	tryUndo := true

	oldName := container.Name()
	oldLvmName := containerNameToLVName(oldName)
	newLvmName := containerNameToLVName(newContainerName)

	_, err := s.ContainerUmount(oldName, container.Path())
	if err != nil {
		return err
	}

	err = s.renameLVByPath(oldLvmName, newLvmName, storagePoolVolumeAPIEndpointContainers)
	if err != nil {
		return fmt.Errorf("Failed to rename a container LV, oldName='%s', newName='%s', err='%s'", oldLvmName, newLvmName, err)
	}
	defer func() {
		if tryUndo {
			s.renameLVByPath(newLvmName, oldLvmName, storagePoolVolumeAPIEndpointContainers)
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

		oldContainerMntPoint := getContainerMountPoint(s.pool.Name, oldName)
		oldContainerMntPointSymlink := container.Path()
		newContainerMntPoint := getContainerMountPoint(s.pool.Name, newContainerName)
		newContainerMntPointSymlink := shared.VarPath("containers", newContainerName)
		err = renameContainerMountpoint(oldContainerMntPoint, oldContainerMntPointSymlink, newContainerMntPoint, newContainerMntPointSymlink)
		if err != nil {
			return err
		}

		oldSnapshotPath := getSnapshotMountPoint(s.pool.Name, oldName)
		newSnapshotPath := getSnapshotMountPoint(s.pool.Name, newContainerName)
		if shared.PathExists(oldSnapshotPath) {
			err = os.Rename(oldSnapshotPath, newSnapshotPath)
			if err != nil {
				return err
			}
		}

		oldSnapshotSymlink := shared.VarPath("snapshots", oldName)
		newSnapshotSymlink := shared.VarPath("snapshots", newContainerName)
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

	logger.Debugf("Renamed LVM storage volume for container \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newContainerName)
	return nil
}

func (s *storageLvm) ContainerRestore(target container, source container) error {
	logger.Debugf("Restoring LVM storage volume for container \"%s\" from %s -> %s.", s.volume.Name, source.Name(), target.Name())

	ourStart, err := source.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer source.StorageStop()
	}

	_, sourcePool := source.Storage().GetContainerPoolInfo()
	if s.pool.Name != sourcePool {
		return fmt.Errorf("containers must be on the same pool to be restored")
	}

	sourceName := source.Name()
	sourceLvmName := containerNameToLVName(sourceName)

	targetName := target.Name()
	targetLvmName := containerNameToLVName(targetName)
	targetPath := target.Path()
	if s.useThinpool {
		_, err = target.Storage().ContainerUmount(targetName, targetPath)
		if err != nil {
			return err
		}

		poolName := s.getOnDiskPoolName()
		err = s.removeLV(poolName, storagePoolVolumeAPIEndpointContainers, targetLvmName)
		if err != nil {
			logger.Errorf(fmt.Sprintf("Failed to remove \"%s\": %s.", targetLvmName, err))
		}

		_, err = s.createSnapshotLV(poolName, sourceLvmName, storagePoolVolumeAPIEndpointContainers, targetLvmName, storagePoolVolumeAPIEndpointContainers, false, true)
		if err != nil {
			return fmt.Errorf("Error creating snapshot LV: %v", err)
		}
	} else {
		ourMount, err := target.Storage().ContainerMount(target)
		if err != nil {
			return err
		}
		if ourMount {
			defer target.Storage().ContainerUmount(targetName, targetPath)
		}

		poolName := s.getOnDiskPoolName()
		sourceName := source.Name()
		targetContainerMntPoint := getContainerMountPoint(poolName, targetName)
		sourceContainerMntPoint := getContainerMountPoint(poolName, sourceName)
		if source.IsSnapshot() {
			sourceContainerMntPoint = getSnapshotMountPoint(poolName, sourceName)
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

	logger.Debugf("Restored LVM storage volume for container \"%s\" from %s -> %s.", s.volume.Name, sourceName, targetName)
	return nil
}

func (s *storageLvm) ContainerGetUsage(container container) (int64, error) {
	return -1, fmt.Errorf("the LVM container backend doesn't support quotas")
}

func (s *storageLvm) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	logger.Debugf("Creating LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	err := s.createSnapshotContainer(snapshotContainer, sourceContainer, true)
	if err != nil {
		return err
	}

	logger.Debugf("Created LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerSnapshotDelete(snapshotContainer container) error {
	logger.Debugf("Deleting LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	err := s.ContainerDelete(snapshotContainer)
	if err != nil {
		return fmt.Errorf("Error deleting snapshot %s: %s", snapshotContainer.Name(), err)
	}

	logger.Debugf("Deleted LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerSnapshotRename(snapshotContainer container, newContainerName string) error {
	logger.Debugf("Renaming LVM storage volume for snapshot \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newContainerName)

	tryUndo := true

	oldName := snapshotContainer.Name()
	oldLvmName := containerNameToLVName(oldName)
	newLvmName := containerNameToLVName(newContainerName)

	err := s.renameLVByPath(oldLvmName, newLvmName, storagePoolVolumeAPIEndpointContainers)
	if err != nil {
		return fmt.Errorf("Failed to rename a container LV, oldName='%s', newName='%s', err='%s'", oldLvmName, newLvmName, err)
	}
	defer func() {
		if tryUndo {
			s.renameLVByPath(newLvmName, oldLvmName, storagePoolVolumeAPIEndpointContainers)
		}
	}()

	oldSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, oldName)
	newSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, newContainerName)
	err = os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
	if err != nil {
		return err
	}

	tryUndo = false

	logger.Debugf("Renamed LVM storage volume for snapshot \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newContainerName)
	return nil
}

func (s *storageLvm) ContainerSnapshotStart(container container) (bool, error) {
	logger.Debugf("Initializing LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	poolName := s.getOnDiskPoolName()
	containerName := container.Name()
	containerLvmName := containerNameToLVName(containerName)
	containerLvmPath := getLvmDevPath(poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName)

	wasWritableAtCheck, err := lvmLvIsWritable(containerLvmPath)
	if err != nil {
		return false, err
	}

	if !wasWritableAtCheck {
		output, err := shared.TryRunCommand("lvchange", "-prw", fmt.Sprintf("%s/%s_%s", poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName))
		if err != nil {
			logger.Errorf("Failed to make LVM snapshot \"%s\" read-write: %s.", containerName, output)
			return false, err
		}
	}

	lvFsType := s.getLvmFilesystem()
	containerMntPoint := getSnapshotMountPoint(s.pool.Name, containerName)
	if !shared.IsMountPoint(containerMntPoint) {
		mountFlags, mountOptions := lxdResolveMountoptions(s.getLvmMountOptions())
		err = tryMount(containerLvmPath, containerMntPoint, lvFsType, mountFlags, mountOptions)
		if err != nil {
			return false, fmt.Errorf("Error mounting snapshot LV path='%s': %s", containerMntPoint, err)
		}
	}

	logger.Debugf("Initialized LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	if wasWritableAtCheck {
		return false, nil
	}

	return true, nil
}

func (s *storageLvm) ContainerSnapshotStop(container container) (bool, error) {
	logger.Debugf("Stopping LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	containerName := container.Name()
	snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, containerName)

	poolName := s.getOnDiskPoolName()
	containerLvmName := containerNameToLVName(containerName)

	if shared.IsMountPoint(snapshotMntPoint) {
		err := tryUnmount(snapshotMntPoint, 0)
		if err != nil {
			return false, err
		}
	}

	containerLvmPath := getLvmDevPath(poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName)
	wasWritableAtCheck, err := lvmLvIsWritable(containerLvmPath)
	if err != nil {
		return false, err
	}

	if wasWritableAtCheck {
		output, err := shared.TryRunCommand("lvchange", "-pr", fmt.Sprintf("%s/%s_%s", poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName))
		if err != nil {
			logger.Errorf("Failed to make LVM snapshot read-only: %s.", output)
			return false, err
		}
	}

	logger.Debugf("Stopped LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	if wasWritableAtCheck {
		return false, nil
	}

	return true, nil
}

func (s *storageLvm) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	logger.Debugf("Creating empty LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	err := s.ContainerCreate(snapshotContainer)
	if err != nil {
		return err
	}

	logger.Debugf("Created empty LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ImageCreate(fingerprint string) error {
	logger.Debugf("Creating LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

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
			logger.Warnf("Could not delete image \"%s\" from storage volume database. Manual intervention needed.", fingerprint)
		}
	}()

	if s.useThinpool {
		err = lvmCreateThinpool(s.s, s.sTypeVersion, poolName, thinPoolName, lvFsType)
		if err != nil {
			return err
		}

		err = lvmCreateLv(poolName, thinPoolName, fingerprint, lvFsType, lvSize, storagePoolVolumeAPIEndpointImages, true)
		if err != nil {
			logger.Errorf("lvmCreateLv: %s.", err)
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
		err = unpackImage(imagePath, imageMntPoint, storageTypeLvm)
		if err != nil {
			return err
		}

		s.ImageUmount(fingerprint)
	}

	tryUndo = false

	logger.Debugf("Created LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return nil
}

func (s *storageLvm) ImageDelete(fingerprint string) error {
	logger.Debugf("Deleting LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

	if s.useThinpool {
		_, err := s.ImageUmount(fingerprint)
		if err != nil {
			return err
		}

		poolName := s.getOnDiskPoolName()
		err = s.removeLV(poolName, storagePoolVolumeAPIEndpointImages, fingerprint)
		if err != nil {
			return err
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

	logger.Debugf("Deleted LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return nil
}

func (s *storageLvm) ImageMount(fingerprint string) (bool, error) {
	logger.Debugf("Mounting LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

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
	lvmVolumePath := getLvmDevPath(poolName, storagePoolVolumeAPIEndpointImages, fingerprint)
	mountFlags, mountOptions := lxdResolveMountoptions(s.getLvmMountOptions())
	err := tryMount(lvmVolumePath, imageMntPoint, lvmFstype, mountFlags, mountOptions)
	if err != nil {
		logger.Errorf(fmt.Sprintf("Error mounting image LV for unpacking: %s", err))
		return false, fmt.Errorf("Error mounting image LV: %v", err)
	}

	logger.Debugf("Mounted LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return true, nil
}

func (s *storageLvm) ImageUmount(fingerprint string) (bool, error) {
	logger.Debugf("Unmounting LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if !shared.IsMountPoint(imageMntPoint) {
		return false, nil
	}

	err := tryUnmount(imageMntPoint, 0)
	if err != nil {
		return false, err
	}

	logger.Debugf("Unmounted LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return true, nil
}

func (s *storageLvm) MigrationType() MigrationFSType {
	return MigrationFSType_RSYNC
}

func (s *storageLvm) PreservesInodes() bool {
	return false
}

func (s *storageLvm) MigrationSource(container container, containerOnly bool) (MigrationStorageSourceDriver, error) {
	return rsyncMigrationSource(container, containerOnly)
}

func (s *storageLvm) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *idmap.IdmapSet, op *operation, containerOnly bool) error {
	return rsyncMigrationSink(live, container, snapshots, conn, srcIdmap, op, containerOnly)
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
				`for container \"%s\" when it is running`,
				ctName)
			logger.Errorf(msg)
			return fmt.Errorf(msg)
		}

		ctLvmName := containerNameToLVName(ctName)
		lvDevPath = getLvmDevPath(poolName, storagePoolVolumeAPIEndpointContainers, ctLvmName)
		mountpoint = getContainerMountPoint(s.pool.Name, ctName)
	default:
		lvDevPath = getLvmDevPath(poolName, storagePoolVolumeAPIEndpointCustom, s.volume.Name)
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
	err = db.StoragePoolVolumeUpdate(
		s.s.DB,
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
