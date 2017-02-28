package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func storageVGActivate(lvmVolumePath string) error {
	output, err := tryExec("vgchange", "-ay", lvmVolumePath)
	if err != nil {
		return fmt.Errorf("Could not activate volume group \"%s\": %s.", lvmVolumePath, string(output))
	}

	return nil
}

func storageLVActivate(lvmVolumePath string, readonly bool) error {
	var output []byte
	var err error
	if readonly {
		output, err = tryExec("lvchange", "-ay", "-pr", lvmVolumePath)
	} else {
		output, err = tryExec("lvchange", "-ay", lvmVolumePath)
	}

	if err != nil {
		return fmt.Errorf("Could not activate logival volume \"%s\": %s.", lvmVolumePath, string(output))
	}

	return nil
}

func storagePVExists(pvName string) (bool, error) {
	err := exec.Command("pvs", "--noheadings", "-o", "lv_attr", pvName).Run()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			waitStatus := exitError.Sys().(syscall.WaitStatus)
			if waitStatus.ExitStatus() == 5 {
				// physical volume not found
				return false, nil
			}
		}
		return false, fmt.Errorf("Error checking for physical volume \"%s\"", pvName)
	}

	return true, nil
}

func storageVGExists(vgName string) (bool, error) {
	err := exec.Command("vgs", "--noheadings", "-o", "lv_attr", vgName).Run()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			waitStatus := exitError.Sys().(syscall.WaitStatus)
			if waitStatus.ExitStatus() == 5 {
				// volume group not found
				return false, nil
			}
		}
		return false, fmt.Errorf("Error checking for volume group \"%s\"", vgName)
	}

	return true, nil
}

func storageLVExists(lvName string) (bool, error) {
	err := exec.Command("lvs", "--noheadings", "-o", "lv_attr", lvName).Run()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			waitStatus := exitError.Sys().(syscall.WaitStatus)
			if waitStatus.ExitStatus() == 5 {
				// logical volume not found
				return false, nil
			}
		}
		return false, fmt.Errorf("Error checking for logical volume \"%s\"", lvName)
	}

	return true, nil
}

func storageLVMThinpoolExists(vgName string, poolName string) (bool, error) {
	output, err := exec.Command("vgs", "--noheadings", "-o", "lv_attr", fmt.Sprintf("%s/%s", vgName, poolName)).Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			waitStatus := exitError.Sys().(syscall.WaitStatus)
			if waitStatus.ExitStatus() == 5 {
				// pool LV was not found
				return false, nil
			}
		}
		return false, fmt.Errorf("Error checking for pool '%s'", poolName)
	}
	// Found LV named poolname, check type:
	attrs := strings.TrimSpace(string(output[:]))
	if strings.HasPrefix(attrs, "t") {
		return true, nil
	}

	return false, fmt.Errorf("Pool named '%s' exists but is not a thin pool.", poolName)
}

func storageLVMGetThinPoolUsers(d *Daemon) ([]string, error) {
	results := []string{}

	cNames, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return results, err
	}

	for _, cName := range cNames {
		var lvLinkPath string
		if strings.Contains(cName, shared.SnapshotDelimiter) {
			lvLinkPath = shared.VarPath("snapshots", fmt.Sprintf("%s.lv", cName))
		} else {
			lvLinkPath = shared.VarPath("containers", fmt.Sprintf("%s.lv", cName))
		}

		if shared.PathExists(lvLinkPath) {
			results = append(results, cName)
		}
	}

	imageNames, err := dbImagesGet(d.db, false)
	if err != nil {
		return results, err
	}

	for _, imageName := range imageNames {
		imageLinkPath := shared.VarPath("images", fmt.Sprintf("%s.lv", imageName))
		if shared.PathExists(imageLinkPath) {
			results = append(results, imageName)
		}
	}

	return results, nil
}

func storageLVMValidateThinPoolName(d *Daemon, vgName string, value string) error {
	users, err := storageLVMGetThinPoolUsers(d)
	if err != nil {
		return fmt.Errorf("Error checking if a pool is already in use: %v", err)
	}

	if len(users) > 0 {
		return fmt.Errorf("Can not change LVM config. Images or containers are still using LVs: %v", users)
	}

	if value != "" {
		if vgName == "" {
			return fmt.Errorf("Can not set lvm.thinpool_name without lvm.vg_name set.")
		}

		poolExists, err := storageLVMThinpoolExists(vgName, value)
		if err != nil {
			return fmt.Errorf("Error checking for thin pool '%s' in '%s': %v", value, vgName, err)
		}

		if !poolExists {
			return fmt.Errorf("Pool '%s' does not exist in Volume Group '%s'", value, vgName)
		}
	}

	return nil
}

func lvmVGRename(oldName string, newName string) error {
	output, err := tryExec("vgrename", oldName, newName)
	if err != nil {
		return fmt.Errorf("Could not rename volume group from \"%s\" to \"%s\": %s.", oldName, newName, string(output))
	}

	return nil
}

func lvmLVRename(vgName string, oldName string, newName string) error {
	output, err := tryExec("lvrename", vgName, oldName, newName)
	if err != nil {
		return fmt.Errorf("Could not rename volume group from \"%s\" to \"%s\": %s.", oldName, newName, string(output))
	}

	return nil
}

func xfsGenerateNewUUID(lvpath string) error {
	output, err := exec.Command(
		"xfs_admin",
		"-U", "generate",
		lvpath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Error generating new UUID: %v\noutput:'%s'", err, string(output))
	}

	return nil
}

func containerNameToLVName(containerName string) string {
	lvName := strings.Replace(containerName, "-", "--", -1)
	return strings.Replace(lvName, shared.SnapshotDelimiter, "-", -1)
}

type storageLvm struct {
	vgName       string
	thinPoolName string
	storageShared
}

func (s *storageLvm) getLvmBlockMountOptions() string {
	if s.volume.Config["block.mount_options"] != "" {
		return s.volume.Config["block.mount_options"]
	}

	if s.pool.Config["volume.block.mount_options"] != "" {
		return s.pool.Config["volume.block.mount_options"]
	}

	return "discard"
}

func (s *storageLvm) getLvmFilesystem() string {
	if s.volume.Config["block.filesystem"] != "" {
		return s.volume.Config["block.filesystem"]
	}

	if s.pool.Config["volume.block.filesystem"] != "" {
		return s.pool.Config["volume.block.filesystem"]
	}

	return "ext4"
}

func (s *storageLvm) getLvmVolumeSize() (string, error) {
	sz, err := shared.ParseByteSizeString(s.volume.Config["size"])
	if err != nil {
		return "", err
	}

	// Safety net: Set to default value.
	if sz == 0 {
		sz, _ = shared.ParseByteSizeString("10GB")
	}

	return fmt.Sprintf("%d", sz), nil
}

func (s *storageLvm) getLvmThinpoolName() string {
	if s.pool.Config["lvm.thinpool_name"] != "" {
		return s.pool.Config["lvm.thinpool_name"]
	}

	return "LXDThinpool"
}

func (s *storageLvm) setLvmThinpoolName(newThinpoolName string) {
	s.pool.Config["lvm.thinpool_name"] = newThinpoolName
}

func (s *storageLvm) getOnDiskPoolName() string {
	if s.vgName != "" {
		return s.vgName
	}

	return s.pool.Name
}

func (s *storageLvm) setOnDiskPoolName(newName string) {
	s.vgName = newName
	s.pool.Config["source"] = newName
}

func getLvmDevPath(lvmPool string, volumeType string, lvmVolume string) string {
	return fmt.Sprintf("/dev/%s/%s_%s", lvmPool, volumeType, lvmVolume)
}

func getPrefixedLvName(volumeType string, lvmVolume string) string {
	return fmt.Sprintf("%s_%s", volumeType, lvmVolume)
}

func getTmpSnapshotName(snap string) string {
	return fmt.Sprintf("%s_tmp", snap)
}

// Only initialize the minimal information we need about a given storage type.
func (s *storageLvm) StorageCoreInit() error {
	s.sType = storageTypeLvm
	typeName, err := storageTypeToString(s.sType)
	if err != nil {
		return err
	}
	s.sTypeName = typeName

	output, err := exec.Command("lvm", "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("Error getting LVM version: %v\noutput:'%s'", err, string(output))
	}
	lines := strings.Split(string(output), "\n")

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

	shared.LogDebugf("Initializing an LVM driver.")
	return nil
}

func (s *storageLvm) StoragePoolInit() error {
	err := s.StorageCoreInit()
	if err != nil {
		return err
	}

	source := s.pool.Config["source"]
	s.thinPoolName = s.getLvmThinpoolName()

	if s.pool.Config["lvm.vg_name"] != "" {
		s.vgName = s.pool.Config["lvm.vg_name"]
	}

	if source == "" {
		return fmt.Errorf("Loop backed lvm storage pools are not supported.")
	} else {
		if filepath.IsAbs(source) {
			if !shared.IsBlockdevPath(source) {
				return fmt.Errorf("Loop backed lvm storage pools are not supported.")
			}
		} else {
			ok, err := storageVGExists(source)
			if err != nil {
				// Internal error.
				return err
			} else if !ok {
				// Volume group does not exist.
				return fmt.Errorf("The requested volume group \"%s\" does not exist.", source)
			}
		}
	}

	return nil
}

func (s *storageLvm) StoragePoolCheck() error {
	shared.LogDebugf("Checking LVM storage pool \"%s\".", s.pool.Name)

	poolName := s.getOnDiskPoolName()
	err := storageVGActivate(poolName)
	if err != nil {
		return err
	}

	shared.LogDebugf("Checked LVM storage pool \"%s\".", s.pool.Name)
	return nil
}

func versionSplit(versionString string) (int, int, int, error) {
	fs := strings.Split(versionString, ".")
	majs, mins, incs := fs[0], fs[1], fs[2]

	maj, err := strconv.Atoi(majs)
	if err != nil {
		return 0, 0, 0, err
	}
	min, err := strconv.Atoi(mins)
	if err != nil {
		return 0, 0, 0, err
	}
	incs = strings.Split(incs, "(")[0]
	inc, err := strconv.Atoi(incs)
	if err != nil {
		return 0, 0, 0, err
	}

	return maj, min, inc, nil
}

func (s *storageLvm) lvmVersionIsAtLeast(versionString string) (bool, error) {
	lvmVersion := strings.Split(s.sTypeVersion, "/")[0]

	lvmMaj, lvmMin, lvmInc, err := versionSplit(lvmVersion)
	if err != nil {
		return false, err
	}

	inMaj, inMin, inInc, err := versionSplit(versionString)
	if err != nil {
		return false, err
	}

	if lvmMaj < inMaj || lvmMin < inMin || lvmInc < inInc {
		return false, nil
	} else {
		return true, nil
	}

}

func (s *storageLvm) StoragePoolCreate() error {
	shared.LogInfof("Creating LVM storage pool \"%s\".", s.pool.Name)
	tryUndo := true

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

	// Clear size as we're currently not using it for LVM.
	s.pool.Config["size"] = ""
	poolName := s.getOnDiskPoolName()
	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("Loop backed lvm storage pools are not supported.")
	} else {
		if filepath.IsAbs(source) {
			if !shared.IsBlockdevPath(source) {
				return fmt.Errorf("Loop backed lvm storage pools are not supported.")
			}

			if s.pool.Config["lvm.vg_name"] == "" {
				s.pool.Config["lvm.vg_name"] = poolName
			}

			// Set source to volume group name.
			s.pool.Config["source"] = poolName

			// Check if the physical volume already exists.
			ok, err := storagePVExists(source)
			if err == nil && !ok {
				// Create a new lvm physical volume.
				output, err := exec.Command("pvcreate", source).CombinedOutput()
				if err != nil {
					return fmt.Errorf("Failed to create the physical volume for the lvm storage pool: %s.", output)
				}
				defer func() {
					if tryUndo {
						exec.Command("pvremove", source).Run()
					}
				}()
			}

			// Check if the volume group already exists.
			ok, err = storageVGExists(poolName)
			if err == nil && !ok {
				// Create a volume group on the physical volume.
				output, err := exec.Command("vgcreate", poolName, source).CombinedOutput()
				if err != nil {
					return fmt.Errorf("Failed to create the volume group for the lvm storage pool: %s.", output)
				}
			}
		} else {
			if s.pool.Config["lvm.vg_name"] != "" {
				// User gave us something weird.
				return fmt.Errorf("Invalid combination of \"source\" and \"zfs.pool_name\" property.")
			}
			s.pool.Config["lvm.vg_name"] = source
			s.vgName = source

			ok, err := storageVGExists(source)
			if err != nil {
				// Internal error.
				return err
			} else if !ok {
				// Volume group does not exist.
				return fmt.Errorf("The requested volume group \"%s\" does not exist.", source)
			}
		}
	}

	err = s.StoragePoolCheck()
	if err != nil {
		return err
	}

	// Deregister cleanup.
	tryUndo = false

	shared.LogInfof("Created LVM storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolDelete() error {
	shared.LogInfof("Deleting LVM storage pool \"%s\".", s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("No \"source\" property found for the storage pool.")
	}

	poolName := s.getOnDiskPoolName()
	// Remove the volume group.
	output, err := exec.Command("vgremove", "-f", poolName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Failed to destroy the volume group for the lvm storage pool: %s.", output)
	}

	// Delete the mountpoint for the storage pool.
	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	err = os.RemoveAll(poolMntPoint)
	if err != nil {
		return err
	}

	shared.LogInfof("Deleted LVM storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolMount() (bool, error) {
	return true, nil
}

func (s *storageLvm) StoragePoolUmount() (bool, error) {
	return true, nil
}

func (s *storageLvm) StoragePoolVolumeCreate() error {
	shared.LogInfof("Creating LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	tryUndo := true

	poolName := s.getOnDiskPoolName()
	thinPoolName := s.getLvmThinpoolName()
	lvFsType := s.getLvmFilesystem()
	lvSize, err := s.getLvmVolumeSize()
	if lvSize == "" {
		return err
	}

	volumeType, err := storagePoolVolumeTypeNameToApiEndpoint(s.volume.Type)
	if err != nil {
		return err
	}

	err = s.createThinLV(poolName, thinPoolName, s.volume.Name, lvFsType, lvSize, volumeType)
	if err != nil {
		shared.LogErrorf("LVMCreateThinLV: %s.", err)
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

	_, err = s.StoragePoolVolumeMount()
	if err != nil {
		return err
	}

	tryUndo = false

	shared.LogInfof("Created LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeDelete() error {
	shared.LogInfof("Deleting LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	_, err := s.StoragePoolVolumeUmount()
	if err != nil {
		return err
	}

	volumeType, err := storagePoolVolumeTypeNameToApiEndpoint(s.volume.Type)
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

	shared.LogInfof("Deleted LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeMount() (bool, error) {
	shared.LogDebugf("Mounting LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	poolName := s.getOnDiskPoolName()
	mountOptions := s.getLvmBlockMountOptions()
	lvFsType := s.getLvmFilesystem()
	volumeType, err := storagePoolVolumeTypeNameToApiEndpoint(s.volume.Type)
	if err != nil {
		return false, err
	}
	lvmVolumePath := getLvmDevPath(poolName, volumeType, s.volume.Name)

	customMountLockID := getCustomMountLockID(s.pool.Name, s.volume.Name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[customMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			shared.LogWarnf("Received value over semaphore. This should not have happened.")
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
		customerr = s.StoragePoolCheck()
		if customerr == nil {
			customerr = tryMount(lvmVolumePath, customPoolVolumeMntPoint, lvFsType, 0, mountOptions)
		}
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

	shared.LogDebugf("Mounted LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageLvm) StoragePoolVolumeUmount() (bool, error) {
	shared.LogDebugf("Unmounting LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	customUmountLockID := getCustomUmountLockID(s.pool.Name, s.volume.Name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[customUmountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			shared.LogWarnf("Received value over semaphore. This should not have happened.")
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

	shared.LogDebugf("Unmounted LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
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
	shared.LogInfof("Updating LVM storage pool \"%s\".", s.pool.Name)

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	if shared.StringInSlice("size", changedConfig) {
		return fmt.Errorf("The \"size\" property cannot be changed.")
	}

	if shared.StringInSlice("source", changedConfig) {
		return fmt.Errorf("The \"source\" property cannot be changed.")
	}

	if shared.StringInSlice("volume.zfs.use_refquota", changedConfig) {
		return fmt.Errorf("The \"volume.zfs.use_refquota\" property does not apply to LVM drivers.")
	}

	if shared.StringInSlice("volume.zfs.remove_snapshots", changedConfig) {
		return fmt.Errorf("The \"volume.zfs.remove_snapshots\" property does not apply to LVM drivers.")
	}

	if shared.StringInSlice("zfs.pool_name", changedConfig) {
		return fmt.Errorf("The \"zfs.pool_name\" property does not apply to LVM drivers.")
	}

	// "volume.block.mount_options" requires no on-disk modifications.
	// "volume.block.filesystem" requires no on-disk modifications.
	// "volume.size" requires no on-disk modifications.

	// Given a set of changeable pool properties the change should be
	// "transactional": either the whole update succeeds or none. So try to
	// revert on error.
	revert := true
	if shared.StringInSlice("lvm.thinpool_name", changedConfig) {
		newThinpoolName := writable.Config["lvm.thinpool_name"]
		// Paranoia check
		if newThinpoolName == "" {
			return fmt.Errorf("Could not rename volume group: No new name provided.")
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
				shared.LogWarnf("Failed to rename LVM thinpool from \"%s\" to \"%s\": %s. Manual intervention needed.",
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
			return fmt.Errorf("Could not rename volume group: No new name provided.")
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
				shared.LogWarnf("Failed to rename LVM volume group from \"%s\" to \"%s\": %s. Manual intervention needed.",
					newName,
					oldPoolName)
			}
			s.setOnDiskPoolName(oldPoolName)
		}()
	}

	// Update succeeded.
	revert = false

	shared.LogInfof("Updated LVM storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeUpdate(changedConfig []string) error {
	shared.LogInfof("Updating LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	if shared.StringInSlice("block.mount_options", changedConfig) && len(changedConfig) == 1 {
		// noop
	} else {
		return fmt.Errorf("The properties \"%v\" cannot be changed.", changedConfig)
	}

	shared.LogInfof("Updated LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerStorageReady(name string) bool {
	err := s.StoragePoolCheck()
	if err != nil {
		return false
	}

	containerLvmName := containerNameToLVName(name)
	poolName := s.getOnDiskPoolName()
	containerLvmPath := getLvmDevPath(poolName, storagePoolVolumeApiEndpointContainers, containerLvmName)
	ok, _ := storageLVExists(containerLvmPath)
	return ok
}

func (s *storageLvm) ContainerCreate(container container) error {
	shared.LogDebugf("Creating empty LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

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
	err = s.createThinLV(poolName, thinPoolName, containerLvmName, lvFsType, lvSize, storagePoolVolumeApiEndpointContainers)
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
		fields := strings.SplitN(containerName, shared.SnapshotDelimiter, 2)
		sourceName := fields[0]
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

	shared.LogDebugf("Created empty LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerCreateFromImage(container container, fingerprint string) error {
	shared.LogDebugf("Creating LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	tryUndo := true

	poolName := s.getOnDiskPoolName()

	// Check if the image already exists.
	imageLvmDevPath := getLvmDevPath(poolName, storagePoolVolumeApiEndpointImages, fingerprint)

	imageStoragePoolLockID := getImageCreateLockID(poolName, fingerprint)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[imageStoragePoolLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			shared.LogWarnf("Received value over semaphore. This should not have happened.")
		}
	} else {
		lxdStorageOngoingOperationMap[imageStoragePoolLockID] = make(chan bool)
		lxdStorageMapLock.Unlock()

		var imgerr error
		ok, _ := storageLVExists(imageLvmDevPath)
		if !ok {
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

	containerName := container.Name()
	containerLvmName := containerNameToLVName(containerName)
	containerLvSnapshotPath, err := s.createSnapshotLV(poolName, fingerprint, storagePoolVolumeApiEndpointImages, containerLvmName, storagePoolVolumeApiEndpointContainers, false)
	if err != nil {
		return err
	}
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

	// Generate a new xfs's UUID
	lvFsType := s.getLvmFilesystem()
	if lvFsType == "xfs" {
		err := xfsGenerateNewUUID(containerLvSnapshotPath)
		if err != nil {
			return err
		}
	}

	ourMount, err := s.ContainerMount(containerName, containerPath)
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
		shared.LogErrorf("Error in create template during ContainerCreateFromImage, continuing to unmount: %s.", err)
		return err
	}

	tryUndo = false

	shared.LogDebugf("Created LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageLvm) ContainerDelete(container container) error {
	shared.LogDebugf("Deleting LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	containerName := container.Name()
	containerLvmName := containerNameToLVName(containerName)
	containerMntPoint := ""

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
	err := s.removeLV(poolName, storagePoolVolumeApiEndpointContainers, containerLvmName)
	if err != nil {
		return err
	}

	if container.IsSnapshot() {
		fields := strings.SplitN(containerName, shared.SnapshotDelimiter, 2)
		sourceName := fields[0]
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

	shared.LogDebugf("Deleted LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerCopy(container container, sourceContainer container) error {
	shared.LogDebugf("Copying LVM container storage %s -> %s.", sourceContainer.Name(), container.Name())

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	tryUndo := true

	err = sourceContainer.StorageStart()
	if err != nil {
		return err
	}
	defer sourceContainer.StorageStop()

	if sourceContainer.Storage().GetStorageType() == storageTypeLvm {
		err := s.createSnapshotContainer(container, sourceContainer, false)
		if err != nil {
			shared.LogErrorf("Error creating snapshot LV for copy: %s.", err)
			return err
		}
	} else {
		sourceContainerName := sourceContainer.Name()
		targetContainerName := container.Name()
		shared.LogDebugf("Copy from Non-LVM container: %s -> %s.", sourceContainerName, targetContainerName)
		err := s.ContainerCreate(container)
		if err != nil {
			shared.LogErrorf("Error creating empty container: %s.", err)
			return err
		}
		defer func() {
			if tryUndo {
				s.ContainerDelete(container)
			}
		}()

		targetContainerPath := container.Path()
		ourSourceMount, err := s.ContainerMount(targetContainerName, targetContainerPath)
		if err != nil {
			shared.LogErrorf("Error starting/mounting container \"%s\": %s.", targetContainerName, err)
			return err
		}
		if ourSourceMount {
			defer s.ContainerUmount(targetContainerName, targetContainerPath)
		}

		sourceContainerPath := sourceContainer.Path()
		ourTargetMount, err := sourceContainer.Storage().ContainerMount(sourceContainerName, sourceContainerPath)
		if err != nil {
			return err
		}
		if ourTargetMount {
			sourceContainer.Storage().ContainerUmount(sourceContainerName, sourceContainerPath)
		}

		_, sourcePool := sourceContainer.Storage().GetContainerPoolInfo()
		sourceContainerMntPoint := getContainerMountPoint(sourcePool, sourceContainerName)
		targetContainerMntPoint := getContainerMountPoint(s.pool.Name, targetContainerName)
		output, err := storageRsyncCopy(sourceContainerMntPoint, targetContainerMntPoint)
		if err != nil {
			shared.LogErrorf("ContainerCopy: rsync failed: %s.", string(output))
			s.ContainerDelete(container)
			return fmt.Errorf("rsync failed: %s", string(output))
		}
	}

	err = container.TemplateApply("copy")
	if err != nil {
		return err
	}

	tryUndo = false

	shared.LogDebugf("Copied LVM container storage %s -> %s.", sourceContainer.Name(), container.Name())
	return nil
}

func (s *storageLvm) ContainerMount(name string, path string) (bool, error) {
	shared.LogDebugf("Mounting LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	err := s.StoragePoolCheck()
	if err != nil {
		return false, err
	}

	containerLvmName := containerNameToLVName(name)
	lvFsType := s.getLvmFilesystem()
	poolName := s.getOnDiskPoolName()
	containerLvmPath := getLvmDevPath(poolName, storagePoolVolumeApiEndpointContainers, containerLvmName)
	mountOptions := s.getLvmBlockMountOptions()
	containerMntPoint := getContainerMountPoint(s.pool.Name, name)

	containerMountLockID := getContainerMountLockID(s.pool.Name, name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			shared.LogWarnf("Received value over semaphore. This should not have happened.")
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
		mounterr = tryMount(containerLvmPath, containerMntPoint, lvFsType, 0, mountOptions)
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

	shared.LogDebugf("Mounted LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageLvm) ContainerUmount(name string, path string) (bool, error) {
	shared.LogDebugf("Unmounting LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	containerMntPoint := getContainerMountPoint(s.pool.Name, name)

	containerUmountLockID := getContainerUmountLockID(s.pool.Name, name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerUmountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			shared.LogWarnf("Received value over semaphore. This should not have happened.")
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

	shared.LogDebugf("Unmounted LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return ourUmount, nil
}

func (s *storageLvm) ContainerRename(container container, newContainerName string) error {
	shared.LogDebugf("Renaming LVM storage volume for container \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newContainerName)

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	tryUndo := true

	oldName := container.Name()
	oldLvmName := containerNameToLVName(oldName)
	newLvmName := containerNameToLVName(newContainerName)

	_, err = s.ContainerUmount(oldName, container.Path())
	if err != nil {
		return err
	}

	err = s.renameLVByPath(oldLvmName, newLvmName, storagePoolVolumeApiEndpointContainers)
	if err != nil {
		return fmt.Errorf("Failed to rename a container LV, oldName='%s', newName='%s', err='%s'", oldLvmName, newLvmName, err)
	}
	defer func() {
		if tryUndo {
			s.renameLVByPath(newLvmName, oldLvmName, storagePoolVolumeApiEndpointContainers)
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

	shared.LogDebugf("Renamed LVM storage volume for container \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newContainerName)
	return nil
}

func (s *storageLvm) ContainerRestore(container container, sourceContainer container) error {
	shared.LogDebugf("Restoring LVM storage volume for container \"%s\" from %s -> %s.", s.volume.Name, sourceContainer.Name(), container.Name())

	err := sourceContainer.StorageStart()
	if err != nil {
		return err
	}
	defer sourceContainer.StorageStop()

	_, sourcePool := sourceContainer.Storage().GetContainerPoolInfo()
	if s.pool.Name != sourcePool {
		return fmt.Errorf("Containers must be on the same pool to be restored.")
	}

	srcName := sourceContainer.Name()
	srcLvName := containerNameToLVName(srcName)
	if sourceContainer.IsSnapshot() {
		srcLvName = getTmpSnapshotName(srcLvName)
	}

	destName := container.Name()
	destLvName := containerNameToLVName(destName)

	_, err = container.Storage().ContainerUmount(container.Name(), container.Path())
	if err != nil {
		return err
	}

	poolName := s.getOnDiskPoolName()
	err = s.removeLV(poolName, storagePoolVolumeApiEndpointContainers, destName)
	if err != nil {
		shared.LogErrorf(fmt.Sprintf("Failed to remove \"%s\": %s.", destName, err))
	}

	_, err = s.createSnapshotLV(poolName, srcLvName, storagePoolVolumeApiEndpointContainers, destLvName, storagePoolVolumeApiEndpointContainers, false)
	if err != nil {
		return fmt.Errorf("Error creating snapshot LV: %v", err)
	}

	shared.LogDebugf("Restored LVM storage volume for container \"%s\" from %s -> %s.", s.volume.Name, sourceContainer.Name(), container.Name())
	return nil
}

func (s *storageLvm) ContainerSetQuota(container container, size int64) error {
	return fmt.Errorf("The LVM container backend doesn't support quotas.")
}

func (s *storageLvm) ContainerGetUsage(container container) (int64, error) {
	return -1, fmt.Errorf("The LVM container backend doesn't support quotas.")
}

func (s *storageLvm) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	shared.LogDebugf("Creating LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	err := s.createSnapshotContainer(snapshotContainer, sourceContainer, true)
	if err != nil {
		return err
	}

	shared.LogDebugf("Created LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) createSnapshotContainer(snapshotContainer container, sourceContainer container, readonly bool) error {
	tryUndo := true

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	sourceContainerName := sourceContainer.Name()
	targetContainerName := snapshotContainer.Name()
	sourceContainerLvmName := containerNameToLVName(sourceContainerName)
	targetContainerLvmName := containerNameToLVName(targetContainerName)
	shared.LogDebugf("Creating snapshot: %s -> %s.", sourceContainerName, targetContainerName)

	poolName := s.getOnDiskPoolName()
	_, err = s.createSnapshotLV(poolName, sourceContainerLvmName, storagePoolVolumeApiEndpointContainers, targetContainerLvmName, storagePoolVolumeApiEndpointContainers, readonly)
	if err != nil {
		return fmt.Errorf("Error creating snapshot LV: %s", err)
	}
	defer func() {
		if tryUndo {
			s.ContainerCreate(snapshotContainer)
		}
	}()

	targetContainerMntPoint := ""
	targetContainerPath := snapshotContainer.Path()
	targetIsSnapshot := snapshotContainer.IsSnapshot()
	if targetIsSnapshot {
		targetContainerMntPoint = getSnapshotMountPoint(s.pool.Name, targetContainerName)
		sourceFields := strings.SplitN(sourceContainerName, shared.SnapshotDelimiter, 2)
		sourceName := sourceFields[0]
		_, sourcePool := sourceContainer.Storage().GetContainerPoolInfo()
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", sourcePool, "snapshots", sourceName)
		snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
		err = createSnapshotMountpoint(targetContainerMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	} else {
		targetContainerMntPoint = getContainerMountPoint(s.pool.Name, targetContainerName)
		err = createContainerMountpoint(targetContainerMntPoint, targetContainerPath, snapshotContainer.IsPrivileged())
	}
	if err != nil {
		return err
	}

	tryUndo = false

	return nil
}

func (s *storageLvm) ContainerSnapshotDelete(snapshotContainer container) error {
	shared.LogDebugf("Deleting LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	err := s.ContainerDelete(snapshotContainer)
	if err != nil {
		return fmt.Errorf("Error deleting snapshot %s: %s", snapshotContainer.Name(), err)
	}

	shared.LogDebugf("Deleted LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerSnapshotRename(snapshotContainer container, newContainerName string) error {
	shared.LogDebugf("Renaming LVM storage volume for snapshot \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newContainerName)

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	tryUndo := true

	oldName := snapshotContainer.Name()
	oldLvmName := containerNameToLVName(oldName)
	newLvmName := containerNameToLVName(newContainerName)

	err = s.renameLVByPath(oldLvmName, newLvmName, storagePoolVolumeApiEndpointContainers)
	if err != nil {
		return fmt.Errorf("Failed to rename a container LV, oldName='%s', newName='%s', err='%s'", oldLvmName, newLvmName, err)
	}
	defer func() {
		if tryUndo {
			s.renameLVByPath(newLvmName, oldLvmName, storagePoolVolumeApiEndpointContainers)
		}
	}()

	oldSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, oldName)
	newSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, newContainerName)
	err = os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
	if err != nil {
		return err
	}

	tryUndo = false

	shared.LogDebugf("Renamed LVM storage volume for snapshot \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newContainerName)
	return nil
}

func (s *storageLvm) ContainerSnapshotStart(container container) error {
	shared.LogDebugf("Initializing LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	tryUndo := true

	sourceName := container.Name()
	targetName := sourceName
	sourceLvmName := containerNameToLVName(sourceName)
	targetLvmName := containerNameToLVName(targetName)

	tmpTargetLvmName := getTmpSnapshotName(targetLvmName)

	shared.LogDebugf("Creating snapshot: %s -> %s.", sourceLvmName, targetLvmName)

	poolName := s.getOnDiskPoolName()
	lvpath, err := s.createSnapshotLV(poolName, sourceLvmName, storagePoolVolumeApiEndpointContainers, tmpTargetLvmName, storagePoolVolumeApiEndpointContainers, false)
	if err != nil {
		return fmt.Errorf("Error creating snapshot LV: %s", err)
	}
	defer func() {
		if tryUndo {
			s.removeLV(poolName, storagePoolVolumeApiEndpointContainers, tmpTargetLvmName)
		}
	}()

	lvFsType := s.getLvmFilesystem()
	containerLvmPath := getLvmDevPath(poolName, storagePoolVolumeApiEndpointContainers, tmpTargetLvmName)
	mountOptions := s.getLvmBlockMountOptions()
	containerMntPoint := getSnapshotMountPoint(s.pool.Name, sourceName)

	// Generate a new xfs's UUID
	if lvFsType == "xfs" {
		err := xfsGenerateNewUUID(lvpath)
		if err != nil {
			return err
		}
	}

	if !shared.IsMountPoint(containerMntPoint) {
		err = tryMount(containerLvmPath, containerMntPoint, lvFsType, 0, mountOptions)
		if err != nil {
			return fmt.Errorf("Error mounting snapshot LV path='%s': %s", containerMntPoint, err)
		}
	}

	tryUndo = false

	shared.LogDebugf("Initialized LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerSnapshotStop(container container) error {
	shared.LogDebugf("Stopping LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	name := container.Name()
	snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, name)

	poolName := s.getOnDiskPoolName()
	lvName := containerNameToLVName(name)

	if shared.IsMountPoint(snapshotMntPoint) {
		err := tryUnmount(snapshotMntPoint, 0)
		if err != nil {
			return err
		}
	}

	tmpLvName := getTmpSnapshotName(lvName)
	err = s.removeLV(poolName, storagePoolVolumeApiEndpointContainers, tmpLvName)
	if err != nil {
		return err
	}

	shared.LogDebugf("Stopped LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	shared.LogDebugf("Creating empty LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	err := s.ContainerCreate(snapshotContainer)
	if err != nil {
		return err
	}

	shared.LogDebugf("Created empty LVM storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) ImageCreate(fingerprint string) error {
	shared.LogDebugf("Creating LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	tryUndo := true

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

	err = s.createThinLV(poolName, thinPoolName, fingerprint, lvFsType, lvSize, storagePoolVolumeApiEndpointImages)
	if err != nil {
		shared.LogErrorf("LVMCreateThinLV: %s.", err)
		return fmt.Errorf("Error Creating LVM LV for new image: %v", err)
	}
	defer func() {
		if tryUndo {
			s.ImageDelete(fingerprint)
		}
	}()

	// Create image mountpoint.
	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if !shared.PathExists(imageMntPoint) {
		err := os.MkdirAll(imageMntPoint, 0700)
		if err != nil {
			return err
		}
	}

	_, err = s.ImageMount(fingerprint)
	if err != nil {
		return err
	}

	imagePath := shared.VarPath("images", fingerprint)
	err = unpackImage(s.d, imagePath, imageMntPoint, storageTypeLvm)
	if err != nil {
		return err
	}

	s.ImageUmount(fingerprint)

	tryUndo = false

	shared.LogDebugf("Created LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return nil
}

func (s *storageLvm) ImageDelete(fingerprint string) error {
	shared.LogDebugf("Deleting LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	_, err = s.ImageUmount(fingerprint)
	if err != nil {
		return err
	}

	poolName := s.getOnDiskPoolName()
	err = s.removeLV(poolName, storagePoolVolumeApiEndpointImages, fingerprint)
	if err != nil {
		return err
	}

	err = s.deleteImageDbPoolVolume(fingerprint)
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

	shared.LogDebugf("Deleted LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return nil
}

func (s *storageLvm) ImageMount(fingerprint string) (bool, error) {
	shared.LogDebugf("Mounting LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if shared.IsMountPoint(imageMntPoint) {
		return false, nil
	}

	// Shouldn't happen.
	lvmFstype := s.getLvmFilesystem()
	if lvmFstype == "" {
		return false, fmt.Errorf("No filesystem type specified.")
	}

	poolName := s.getOnDiskPoolName()
	lvmVolumePath := getLvmDevPath(poolName, storagePoolVolumeApiEndpointImages, fingerprint)
	lvmMountOptions := s.getLvmBlockMountOptions()
	err := tryMount(lvmVolumePath, imageMntPoint, lvmFstype, 0, lvmMountOptions)
	if err != nil {
		shared.LogErrorf(fmt.Sprintf("Error mounting image LV for unpacking: %s", err))
		return false, fmt.Errorf("Error mounting image LV: %v", err)
	}

	shared.LogDebugf("Mounted LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return true, nil
}

func (s *storageLvm) ImageUmount(fingerprint string) (bool, error) {
	shared.LogDebugf("Unmounting LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if !shared.IsMountPoint(imageMntPoint) {
		return false, nil
	}

	err := tryUnmount(imageMntPoint, 0)
	if err != nil {
		return false, err
	}

	shared.LogDebugf("Unmounted LVM storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return true, nil
}

func (s *storageLvm) createThinLV(vgName string, thinPoolName string, lvName string, lvFsType string, lvSize string, volumeType string) error {
	exists, err := storageLVMThinpoolExists(vgName, thinPoolName)
	if err != nil {
		return err
	}

	if !exists {
		err := s.createDefaultThinPool(vgName, thinPoolName, lvName, lvFsType)
		if err != nil {
			return err
		}

		err = storageLVMValidateThinPoolName(s.d, vgName, thinPoolName)
		if err != nil {
			shared.LogErrorf("Setting thin pool name: %s.", err)
			return fmt.Errorf("Error setting LVM thin pool config: %v", err)
		}
	}

	lvmThinPoolPath := fmt.Sprintf("%s/%s", vgName, thinPoolName)
	lvmPoolVolumeName := getPrefixedLvName(volumeType, lvName)
	output, err := tryExec(
		"lvcreate",
		"--thin",
		"-n", lvmPoolVolumeName,
		"--virtualsize", lvSize+"B", lvmThinPoolPath)
	if err != nil {
		shared.LogErrorf("Could not create LV \"%s\": %s.", lvmPoolVolumeName, string(output))
		return fmt.Errorf("Could not create thin LV named %s", lvmPoolVolumeName)
	}

	fsPath := getLvmDevPath(vgName, volumeType, lvName)
	switch lvFsType {
	case "xfs":
		output, err = tryExec("mkfs.xfs", fsPath)
	default:
		// default = ext4
		output, err = tryExec(
			"mkfs.ext4",
			"-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0",
			fsPath)
	}

	if err != nil {
		shared.LogErrorf("Filesystem creation failed: %s.", string(output))
		return fmt.Errorf("Error making filesystem on image LV: %v", err)
	}

	return nil
}

func (s *storageLvm) createDefaultThinPool(vgName string, thinPoolName string, lvName string, lvFsType string) error {
	isRecent, err := s.lvmVersionIsAtLeast("2.02.99")
	if err != nil {
		return fmt.Errorf("Error checking LVM version: %s", err)
	}

	// Create the thin pool
	lvmThinPool := fmt.Sprintf("%s/%s", vgName, thinPoolName)
	var output []byte
	if isRecent {
		output, err = tryExec(
			"lvcreate",
			"--poolmetadatasize", "1G",
			"-l", "100%FREE",
			"--thinpool", lvmThinPool)
	} else {
		output, err = tryExec(
			"lvcreate",
			"--poolmetadatasize", "1G",
			"-L", "1G",
			"--thinpool", lvmThinPool)
	}

	if err != nil {
		shared.LogErrorf("Could not create thin pool \"%s\": %s.", thinPoolName, string(output))
		return fmt.Errorf("Could not create LVM thin pool named %s", thinPoolName)
	}

	if !isRecent {
		// Grow it to the maximum VG size (two step process required by old LVM)
		output, err = tryExec("lvextend", "--alloc", "anywhere", "-l", "100%FREE", lvmThinPool)

		if err != nil {
			shared.LogErrorf("Could not grow thin pool: \"%s\": %s.", thinPoolName, string(output))
			return fmt.Errorf("Could not grow LVM thin pool named %s", thinPoolName)
		}
	}

	return nil
}

func (s *storageLvm) removeLV(vgName string, volumeType string, lvName string) error {
	lvmVolumePath := getLvmDevPath(vgName, volumeType, lvName)
	output, err := tryExec("lvremove", "-f", lvmVolumePath)

	if err != nil {
		shared.LogErrorf("Could not remove LV \"%s\": %s.", lvName, string(output))
		return fmt.Errorf("Could not remove LV named %s", lvName)
	}

	return nil
}

func (s *storageLvm) createSnapshotLV(vgName string, origLvName string, origVolumeType string, lvName string, volumeType string, readonly bool) (string, error) {
	sourceLvmVolumePath := getLvmDevPath(vgName, origVolumeType, origLvName)
	shared.LogDebugf("in createSnapshotLV: %s.", sourceLvmVolumePath)
	isRecent, err := s.lvmVersionIsAtLeast("2.02.99")
	if err != nil {
		return "", fmt.Errorf("Error checking LVM version: %v", err)
	}

	lvmPoolVolumeName := getPrefixedLvName(volumeType, lvName)
	var output []byte
	if isRecent {
		output, err = tryExec(
			"lvcreate",
			"-kn",
			"-n", lvmPoolVolumeName,
			"-s", sourceLvmVolumePath)
	} else {
		output, err = tryExec(
			"lvcreate",
			"-n", lvmPoolVolumeName,
			"-s", sourceLvmVolumePath)
	}
	if err != nil {
		shared.LogErrorf("Could not create LV snapshot: %s -> %s: %s.", origLvName, lvName, string(output))
		return "", fmt.Errorf("Could not create snapshot LV named %s", lvName)
	}

	targetLvmVolumePath := getLvmDevPath(vgName, volumeType, lvName)
	err = storageLVActivate(targetLvmVolumePath, readonly)
	if err != nil {
		return "", err
	}

	return targetLvmVolumePath, nil
}

func (s *storageLvm) renameLVByPath(oldName string, newName string, volumeType string) error {
	oldLvmName := getPrefixedLvName(volumeType, oldName)
	newLvmName := getPrefixedLvName(volumeType, newName)
	poolName := s.getOnDiskPoolName()
	return lvmLVRename(poolName, oldLvmName, newLvmName)
}

func (s *storageLvm) MigrationType() MigrationFSType {
	return MigrationFSType_RSYNC
}

func (s *storageLvm) PreservesInodes() bool {
	return false
}

func (s *storageLvm) MigrationSource(container container) (MigrationStorageSourceDriver, error) {
	return rsyncMigrationSource(container)
}

func (s *storageLvm) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation) error {
	return rsyncMigrationSink(live, container, snapshots, conn, srcIdmap, op)
}
