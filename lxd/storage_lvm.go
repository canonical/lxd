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
	"github.com/lxc/lxd/shared/logger"
)

func storageVGActivate(lvmVolumePath string) error {
	output, err := shared.TryRunCommand("vgchange", "-ay", lvmVolumePath)
	if err != nil {
		return fmt.Errorf("could not activate volume group \"%s\": %s", lvmVolumePath, output)
	}

	return nil
}

func storageLVActivate(lvmVolumePath string) error {
	output, err := shared.TryRunCommand("lvchange", "-ay", lvmVolumePath)
	if err != nil {
		return fmt.Errorf("could not activate logival volume \"%s\": %s", lvmVolumePath, output)
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
		return false, fmt.Errorf("error checking for physical volume \"%s\"", pvName)
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
		return false, fmt.Errorf("error checking for volume group \"%s\"", vgName)
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
		return false, fmt.Errorf("error checking for logical volume \"%s\"", lvName)
	}

	return true, nil
}

func lvmGetLVSize(lvPath string) (string, error) {
	msg, err := shared.TryRunCommand("lvs", "--noheadings", "-o", "size", "--nosuffix", "--units", "b", lvPath)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve size of logical volume: %s: %s", string(msg), err)
	}

	sizeString := string(msg)
	sizeString = strings.TrimSpace(sizeString)
	size, err := strconv.ParseInt(sizeString, 10, 64)
	if err != nil {
		return "", err
	}

	detectedSize := shared.GetByteSizeString(size, 0)

	return detectedSize, nil
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
		return false, fmt.Errorf("error checking for pool \"%s\"", poolName)
	}
	// Found LV named poolname, check type:
	attrs := strings.TrimSpace(string(output[:]))
	if strings.HasPrefix(attrs, "t") {
		return true, nil
	}

	return false, fmt.Errorf("pool named \"%s\" exists but is not a thin pool", poolName)
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
		return fmt.Errorf("error checking if a pool is already in use: %v", err)
	}

	if len(users) > 0 {
		return fmt.Errorf("can not change LVM config. Images or containers are still using LVs: %v", users)
	}

	if value != "" {
		if vgName == "" {
			return fmt.Errorf("can not set lvm.thinpool_name without lvm.vg_name set")
		}

		poolExists, err := storageLVMThinpoolExists(vgName, value)
		if err != nil {
			return fmt.Errorf("error checking for thin pool \"%s\" in \"%s\": %v", value, vgName, err)
		}

		if !poolExists {
			return fmt.Errorf("pool \"'%s\" does not exist in Volume Group \"%s\"", value, vgName)
		}
	}

	return nil
}

func lvmVGRename(oldName string, newName string) error {
	output, err := shared.TryRunCommand("vgrename", oldName, newName)
	if err != nil {
		return fmt.Errorf("could not rename volume group from \"%s\" to \"%s\": %s", oldName, newName, output)
	}

	return nil
}

func lvmLVRename(vgName string, oldName string, newName string) error {
	output, err := shared.TryRunCommand("lvrename", vgName, oldName, newName)
	if err != nil {
		return fmt.Errorf("could not rename volume group from \"%s\" to \"%s\": %s", oldName, newName, output)
	}

	return nil
}

func xfsGenerateNewUUID(lvpath string) error {
	output, err := shared.RunCommand(
		"xfs_admin",
		"-U", "generate",
		lvpath)
	if err != nil {
		return fmt.Errorf("Error generating new UUID: %v\noutput:'%s'", err, output)
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
	useThinpool  bool
	loopInfo     *os.File
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

func (s *storageLvm) usesThinpool() bool {
	// Default is to use a thinpool.
	if s.pool.Config["lvm.use_thinpool"] == "" {
		return true
	}

	return shared.IsTrue(s.pool.Config["lvm.use_thinpool"])
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

func lvmVersionIsAtLeast(sTypeVersion string, versionString string) (bool, error) {
	lvmVersion := strings.Split(sTypeVersion, "/")[0]

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
	}

	return true, nil
}

func (s *storageLvm) StoragePoolCreate() error {
	logger.Infof("Creating LVM storage pool \"%s\".", s.pool.Name)
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

	poolName := s.getOnDiskPoolName()
	source := s.pool.Config["source"]
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
		loopDevicePath := s.loopInfo.Name()
		ok, err := storagePVExists(loopDevicePath)
		if err == nil && !ok {
			// Create a new lvm physical volume.
			output, err := shared.TryRunCommand("pvcreate", loopDevicePath)
			if err != nil {
				return fmt.Errorf("failed to create the physical volume for the lvm storage pool: %s", output)
			}
			defer func() {
				if tryUndo {
					shared.TryRunCommand("pvremove", loopDevicePath)
				}
			}()
		}

		// Check if the volume group already exists.
		ok, err = storageVGExists(poolName)
		if err == nil && !ok {
			// Create a volume group on the physical volume.
			output, err := shared.TryRunCommand("vgcreate", poolName, loopDevicePath)
			if err != nil {
				return fmt.Errorf("failed to create the volume group for the lvm storage pool: %s", output)
			}
		}
	} else {
		s.pool.Config["size"] = ""
		if filepath.IsAbs(source) {
			if !shared.IsBlockdevPath(source) {
				return fmt.Errorf("custom loop file locations are not supported")
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
				output, err := shared.TryRunCommand("pvcreate", source)
				if err != nil {
					return fmt.Errorf("failed to create the physical volume for the lvm storage pool: %s", output)
				}
				defer func() {
					if tryUndo {
						shared.TryRunCommand("pvremove", source)
					}
				}()
			}

			// Check if the volume group already exists.
			ok, err = storageVGExists(poolName)
			if err == nil && !ok {
				// Create a volume group on the physical volume.
				output, err := shared.TryRunCommand("vgcreate", poolName, source)
				if err != nil {
					return fmt.Errorf("failed to create the volume group for the lvm storage pool: %s", output)
				}
			}
		} else {
			if s.pool.Config["lvm.vg_name"] != "" {
				// User gave us something weird.
				return fmt.Errorf("invalid combination of \"source\" and \"zfs.pool_name\" property")
			}
			s.pool.Config["lvm.vg_name"] = source
			s.vgName = source

			ok, err := storageVGExists(source)
			if err != nil {
				// Internal error.
				return err
			} else if !ok {
				// Volume group does not exist.
				return fmt.Errorf("the requested volume group \"%s\" does not exist", source)
			}
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
	// Remove the volume group.
	output, err := shared.TryRunCommand("vgremove", "-f", poolName)
	if err != nil {
		return fmt.Errorf("failed to destroy the volume group for the lvm storage pool: %s", output)
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
		err = lvmCreateThinpool(s.d, s.sTypeVersion, poolName, thinPoolName, lvFsType)
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

	logger.Infof("Deleted LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) StoragePoolVolumeMount() (bool, error) {
	logger.Debugf("Mounting LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	poolName := s.getOnDiskPoolName()
	mountOptions := s.getLvmBlockMountOptions()
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
		customerr = tryMount(lvmVolumePath, customPoolVolumeMntPoint, lvFsType, 0, mountOptions)
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
			return fmt.Errorf("the LVM storage pool \"%s\" does not use thin pools. The \"lvm.thinpool_name\" porperty cannot be set", s.pool.Name)
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

func (s *storageLvm) StoragePoolVolumeUpdate(changedConfig []string) error {
	logger.Infof("Updating LVM storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	if shared.StringInSlice("block.mount_options", changedConfig) && len(changedConfig) == 1 {
		// noop
	} else {
		return fmt.Errorf("the properties \"%v\" cannot be changed", changedConfig)
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
		err = lvmCreateThinpool(s.d, s.sTypeVersion, poolName, thinPoolName, lvFsType)
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

	logger.Debugf("Created empty LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageLvm) containerCreateFromImageThinLv(c container, fp string) error {
	poolName := s.getOnDiskPoolName()
	// Check if the image already exists.
	imageLvmDevPath := getLvmDevPath(poolName, storagePoolVolumeAPIEndpointImages, fp)

	imageStoragePoolLockID := getImageCreateLockID(poolName, fp)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[imageStoragePoolLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore. This should not have happened.")
		}
	} else {
		lxdStorageOngoingOperationMap[imageStoragePoolLockID] = make(chan bool)
		lxdStorageMapLock.Unlock()

		var imgerr error
		ok, _ := storageLVExists(imageLvmDevPath)
		if !ok {
			imgerr = s.ImageCreate(fp)
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

	containerName := c.Name()
	containerLvmName := containerNameToLVName(containerName)
	_, err := s.createSnapshotLV(poolName, fp, storagePoolVolumeAPIEndpointImages, containerLvmName, storagePoolVolumeAPIEndpointContainers, false, s.useThinpool)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageLvm) containerCreateFromImageLv(c container, fp string) error {
	err := s.ContainerCreate(c)
	if err != nil {
		return err
	}

	containerName := c.Name()
	containerPath := c.Path()
	_, err = s.ContainerMount(containerName, containerPath)
	if err != nil {
		return err
	}

	imagePath := shared.VarPath("images", fp)
	poolName := s.getOnDiskPoolName()
	containerMntPoint := getContainerMountPoint(poolName, containerName)
	err = unpackImage(s.d, imagePath, containerMntPoint, storageTypeLvm)
	if err != nil {
		return err
	}

	s.ContainerUmount(containerName, containerPath)

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

	poolName := s.getOnDiskPoolName()
	containerLvDevPath := getLvmDevPath(poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName)
	// Generate a new xfs's UUID
	lvFsType := s.getLvmFilesystem()
	if lvFsType == "xfs" {
		err := xfsGenerateNewUUID(containerLvDevPath)
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

	logger.Debugf("Deleted LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

// Copy an lvm container.
func (s *storageLvm) copyContainer(target container, source container) error {
	targetContainerMntPoint := getContainerMountPoint(s.pool.Name, target.Name())
	err := createContainerMountpoint(targetContainerMntPoint, target.Path(), target.IsPrivileged())
	if err != nil {
		return err
	}

	if s.useThinpool {
		// If the storage pool uses a thinpool we can have snapshots of
		// snapshots.
		err = s.copyContainerThinpool(target, source, false)
	} else {
		// If the storage pools does not use a thinpool we need to
		// perform full copies.
		err = s.copyContainerLv(target, source, false)
	}
	if err != nil {
		return err
	}

	err = s.setUnprivUserACL(source, targetContainerMntPoint)
	if err != nil {
		return err
	}

	err = target.TemplateApply("copy")
	if err != nil {
		return err
	}

	return nil
}

// Copy a container on a storage pool that does not use a thinpool.
func (s *storageLvm) copyContainerLv(target container, source container, readonly bool) error {
	err := s.ContainerCreate(target)
	if err != nil {
		return err
	}

	targetName := target.Name()
	targetStart, err := target.StorageStart()
	if err != nil {
		return err
	}
	if targetStart {
		defer target.StorageStop()
	}

	sourceName := source.Name()
	sourceStart, err := source.StorageStart()
	if err != nil {
		return err
	}
	if sourceStart {
		defer source.StorageStop()
	}

	poolName := s.getOnDiskPoolName()
	sourceContainerMntPoint := getContainerMountPoint(poolName, sourceName)
	if source.IsSnapshot() {
		sourceContainerMntPoint = getSnapshotMountPoint(poolName, sourceName)
	}
	targetContainerMntPoint := getContainerMountPoint(poolName, targetName)
	if target.IsSnapshot() {
		targetContainerMntPoint = getSnapshotMountPoint(poolName, targetName)
	}

	if source.IsRunning() {
		err = source.Freeze()
		if err != nil {
			return err
		}
		defer source.Unfreeze()
	}

	bwlimit := s.pool.Config["rsync.bwlimit"]
	output, err := rsyncLocalCopy(sourceContainerMntPoint, targetContainerMntPoint, bwlimit)
	if err != nil {
		return fmt.Errorf("failed to rsync container: %s: %s", string(output), err)
	}

	if readonly {
		targetLvmName := containerNameToLVName(targetName)
		output, err := shared.TryRunCommand("lvchange", "-pr", fmt.Sprintf("%s/%s_%s", poolName, storagePoolVolumeAPIEndpointContainers, targetLvmName))
		if err != nil {
			logger.Errorf("Failed to make LVM snapshot \"%s\" read-write: %s.", targetName, output)
			return err
		}
	}

	return nil
}

// Copy a container on a storage pool that does use a thinpool.
func (s *storageLvm) copyContainerThinpool(target container, source container, readonly bool) error {
	err := s.createSnapshotContainer(target, source, readonly)
	if err != nil {
		logger.Errorf("Error creating snapshot LV for copy: %s.", err)
		return err
	}

	return nil
}

func (s *storageLvm) copySnapshot(target container, source container) error {
	fields := strings.SplitN(target.Name(), shared.SnapshotDelimiter, 2)
	containersPath := getSnapshotMountPoint(s.pool.Name, fields[0])
	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "snapshots", fields[0])
	snapshotMntPointSymlink := shared.VarPath("snapshots", fields[0])
	err := createSnapshotMountpoint(containersPath, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	if err != nil {
		return err
	}

	if s.useThinpool {
		err = s.copyContainerThinpool(target, source, true)
	} else {
		err = s.copyContainerLv(target, source, true)
	}
	if err != nil {
		logger.Errorf("Error creating snapshot LV for copy: %s.", err)
		return err
	}

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
		fields := strings.SplitN(snap.Name(), shared.SnapshotDelimiter, 2)
		newSnapName := fmt.Sprintf("%s/%s", target.Name(), fields[1])

		logger.Debugf("Copying LVM container storage for snapshot %s -> %s.", snap.Name(), newSnapName)

		sourceSnapshot, err := containerLoadByName(s.d, snap.Name())
		if err != nil {
			return err
		}

		targetSnapshot, err := containerLoadByName(s.d, newSnapName)
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

func (s *storageLvm) ContainerMount(name string, path string) (bool, error) {
	logger.Debugf("Mounting LVM storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	containerLvmName := containerNameToLVName(name)
	lvFsType := s.getLvmFilesystem()
	poolName := s.getOnDiskPoolName()
	containerLvmPath := getLvmDevPath(poolName, storagePoolVolumeAPIEndpointContainers, containerLvmName)
	mountOptions := s.getLvmBlockMountOptions()

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
		ourMount, err := target.Storage().ContainerMount(targetName, targetPath)
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

func (s *storageLvm) ContainerSetQuota(container container, size int64) error {
	return fmt.Errorf("the LVM container backend doesn't support quotas")
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

func (s *storageLvm) createSnapshotContainer(snapshotContainer container, sourceContainer container, readonly bool) error {
	tryUndo := true

	sourceContainerName := sourceContainer.Name()
	targetContainerName := snapshotContainer.Name()
	sourceContainerLvmName := containerNameToLVName(sourceContainerName)
	targetContainerLvmName := containerNameToLVName(targetContainerName)
	logger.Debugf("Creating snapshot: %s -> %s.", sourceContainerName, targetContainerName)

	poolName := s.getOnDiskPoolName()
	_, err := s.createSnapshotLV(poolName, sourceContainerLvmName, storagePoolVolumeAPIEndpointContainers, targetContainerLvmName, storagePoolVolumeAPIEndpointContainers, readonly, s.useThinpool)
	if err != nil {
		return fmt.Errorf("Error creating snapshot LV: %s", err)
	}
	defer func() {
		if tryUndo {
			s.ContainerDelete(snapshotContainer)
		}
	}()

	targetContainerMntPoint := ""
	targetContainerPath := snapshotContainer.Path()
	targetIsSnapshot := snapshotContainer.IsSnapshot()
	if targetIsSnapshot {
		targetContainerMntPoint = getSnapshotMountPoint(s.pool.Name, targetContainerName)
		sourceFields := strings.SplitN(sourceContainerName, shared.SnapshotDelimiter, 2)
		sourceName := sourceFields[0]
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", poolName, "snapshots", sourceName)
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
	mountOptions := s.getLvmBlockMountOptions()
	containerMntPoint := getSnapshotMountPoint(s.pool.Name, containerName)

	if !shared.IsMountPoint(containerMntPoint) {
		err = tryMount(containerLvmPath, containerMntPoint, lvFsType, 0, mountOptions)
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
		err = lvmCreateThinpool(s.d, s.sTypeVersion, poolName, thinPoolName, lvFsType)
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
		err = unpackImage(s.d, imagePath, imageMntPoint, storageTypeLvm)
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
	lvmMountOptions := s.getLvmBlockMountOptions()
	err := tryMount(lvmVolumePath, imageMntPoint, lvmFstype, 0, lvmMountOptions)
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

func createDefaultThinPool(sTypeVersion string, vgName string, thinPoolName string, lvFsType string) error {
	isRecent, err := lvmVersionIsAtLeast(sTypeVersion, "2.02.99")
	if err != nil {
		return fmt.Errorf("Error checking LVM version: %s", err)
	}

	// Create the thin pool
	lvmThinPool := fmt.Sprintf("%s/%s", vgName, thinPoolName)
	var output string
	if isRecent {
		output, err = shared.TryRunCommand(
			"lvcreate",
			"--poolmetadatasize", "1G",
			"-l", "100%FREE",
			"--thinpool", lvmThinPool)
	} else {
		output, err = shared.TryRunCommand(
			"lvcreate",
			"--poolmetadatasize", "1G",
			"-L", "1G",
			"--thinpool", lvmThinPool)
	}

	if err != nil {
		logger.Errorf("Could not create thin pool \"%s\": %s.", thinPoolName, string(output))
		return fmt.Errorf("Could not create LVM thin pool named %s", thinPoolName)
	}

	if !isRecent {
		// Grow it to the maximum VG size (two step process required by old LVM)
		output, err = shared.TryRunCommand("lvextend", "--alloc", "anywhere", "-l", "100%FREE", lvmThinPool)

		if err != nil {
			logger.Errorf("Could not grow thin pool: \"%s\": %s.", thinPoolName, string(output))
			return fmt.Errorf("Could not grow LVM thin pool named %s", thinPoolName)
		}
	}

	return nil
}

func lvmCreateThinpool(d *Daemon, sTypeVersion string, vgName string, thinPoolName string, lvFsType string) error {
	exists, err := storageLVMThinpoolExists(vgName, thinPoolName)
	if err != nil {
		return err
	}

	if exists {
		return nil
	}

	err = createDefaultThinPool(sTypeVersion, vgName, thinPoolName, lvFsType)
	if err != nil {
		return err
	}

	err = storageLVMValidateThinPoolName(d, vgName, thinPoolName)
	if err != nil {
		logger.Errorf("Setting thin pool name: %s.", err)
		return fmt.Errorf("Error setting LVM thin pool config: %v", err)
	}

	return nil
}

func lvmCreateLv(vgName string, thinPoolName string, lvName string, lvFsType string, lvSize string, volumeType string, makeThinLv bool) error {
	var output string
	var err error

	targetVg := vgName
	lvmPoolVolumeName := getPrefixedLvName(volumeType, lvName)
	if makeThinLv {
		targetVg = fmt.Sprintf("%s/%s", vgName, thinPoolName)
		output, err = shared.TryRunCommand("lvcreate", "--thin", "-n", lvmPoolVolumeName, "--virtualsize", lvSize+"B", targetVg)
	} else {
		output, err = shared.TryRunCommand("lvcreate", "-n", lvmPoolVolumeName, "--size", lvSize+"B", vgName)
	}
	if err != nil {
		logger.Errorf("Could not create LV \"%s\": %s.", lvmPoolVolumeName, output)
		return fmt.Errorf("Could not create thin LV named %s", lvmPoolVolumeName)
	}

	fsPath := getLvmDevPath(vgName, volumeType, lvName)
	switch lvFsType {
	case "xfs":
		output, err = shared.TryRunCommand("mkfs.xfs", fsPath)
	default:
		// default = ext4
		output, err = shared.TryRunCommand(
			"mkfs.ext4",
			"-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0",
			fsPath)
	}

	if err != nil {
		logger.Errorf("Filesystem creation failed: %s.", output)
		return fmt.Errorf("Error making filesystem on image LV: %v", err)
	}

	return nil
}

func (s *storageLvm) removeLV(vgName string, volumeType string, lvName string) error {
	lvmVolumePath := getLvmDevPath(vgName, volumeType, lvName)
	output, err := shared.TryRunCommand("lvremove", "-f", lvmVolumePath)

	if err != nil {
		logger.Errorf("Could not remove LV \"%s\": %s.", lvName, output)
		return fmt.Errorf("Could not remove LV named %s", lvName)
	}

	return nil
}

func (s *storageLvm) createSnapshotLV(vgName string, origLvName string, origVolumeType string, lvName string, volumeType string, readonly bool, makeThinLv bool) (string, error) {
	sourceLvmVolumePath := getLvmDevPath(vgName, origVolumeType, origLvName)
	logger.Debugf("in createSnapshotLV: %s.", sourceLvmVolumePath)
	isRecent, err := lvmVersionIsAtLeast(s.sTypeVersion, "2.02.99")
	if err != nil {
		return "", fmt.Errorf("Error checking LVM version: %v", err)
	}

	lvmPoolVolumeName := getPrefixedLvName(volumeType, lvName)
	var output string
	args := []string{"-n", lvmPoolVolumeName, "-s", sourceLvmVolumePath}
	if isRecent {
		args = append(args, "-kn")
	}

	// If the source is not a thin volume the size needs to be specified.
	// According to LVM tools 15-20% of the original volume should be
	// sufficient. However, let's not be stingy at first otherwise we might
	// force users to fiddle around with lvextend.
	if !makeThinLv {
		lvSize, err := s.getLvmVolumeSize()
		if lvSize == "" {
			return "", err
		}
		args = append(args, "--size", lvSize+"B")
	}

	if readonly {
		args = append(args, "-pr")
	} else {
		args = append(args, "-prw")
	}

	output, err = shared.TryRunCommand("lvcreate", args...)
	if err != nil {
		logger.Errorf("Could not create LV snapshot: %s -> %s: %s.", origLvName, lvName, output)
		return "", fmt.Errorf("Could not create snapshot LV named %s", lvName)
	}

	targetLvmVolumePath := getLvmDevPath(vgName, volumeType, lvName)
	if makeThinLv {
		// Snapshots of thin logical volumes can be directly activated.
		// Normal snapshots will complain about changing the origin
		// (Which they never do.), so skip the activation since the
		// logical volume will be automatically activated anyway.
		err := storageLVActivate(targetLvmVolumePath)
		if err != nil {
			return "", err
		}
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

func (s *storageLvm) MigrationSource(container container, containerOnly bool) (MigrationStorageSourceDriver, error) {
	return rsyncMigrationSource(container, containerOnly)
}

func (s *storageLvm) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation, containerOnly bool) error {
	return rsyncMigrationSink(live, container, snapshots, conn, srcIdmap, op, containerOnly)
}

func lvmLvIsWritable(lvName string) (bool, error) {
	output, err := shared.TryRunCommand("lvs", "--noheadings", "-o", "lv_attr", lvName)
	if err != nil {
		return false, fmt.Errorf("Error retrieving attributes for logical volume \"%s\"", lvName)
	}

	output = strings.TrimSpace(output)
	return rune(output[1]) == 'w', nil
}
