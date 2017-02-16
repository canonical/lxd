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

	log "gopkg.in/inconshreveable/log15.v2"
)

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
			return fmt.Errorf("Can not set lvm_thinpool_name without lvm_vg_name set.")
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
	storageShared
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
func (s *storageLvm) StorageCoreInit() (*storageCore, error) {
	sCore := storageCore{}
	sCore.sType = storageTypeLvm
	typeName, err := storageTypeToString(sCore.sType)
	if err != nil {
		return nil, err
	}
	sCore.sTypeName = typeName

	output, err := exec.Command("lvm", "version").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Error getting LVM version: %v\noutput:'%s'", err, string(output))
	}
	lines := strings.Split(string(output), "\n")

	sCore.sTypeVersion = ""
	for idx, line := range lines {
		fields := strings.SplitAfterN(line, ":", 2)
		if len(fields) < 2 {
			continue
		}
		if idx > 0 {
			sCore.sTypeVersion += " / "
		}
		sCore.sTypeVersion += strings.TrimSpace(fields[1])
	}

	err = sCore.initShared()
	if err != nil {
		return nil, err
	}

	s.storageCore = sCore

	return &sCore, nil
}

func (s *storageLvm) StoragePoolInit(config map[string]interface{}) (storage, error) {
	_, err := s.StorageCoreInit()
	if err != nil {
		return s, err
	}

	return s, nil
}

func (s *storageLvm) StoragePoolCheck() error {
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
	tryUndo := true

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("No \"source\" property found for the storage pool.")
	}

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

	if !shared.IsBlockdevPath(source) {
		return fmt.Errorf("Loop backed lvm storage volumes are currently not supported.")
	}

	// Create a lvm physical volume.
	output, err := exec.Command("pvcreate", source).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Failed to create the physical volume for the lvm storage pool: %s.", output)
	}
	defer func() {
		if tryUndo {
			exec.Command("pvremove", source).Run()
		}
	}()

	// Create a volume group on the physical volume.
	output, err = exec.Command("vgcreate", s.pool.Name, source).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Failed to create the volume group for the lvm storage pool: %s.", output)
	}

	s.pool.Config["source"] = s.pool.Name

	// Deregister cleanup.
	tryUndo = false

	return nil
}

func (s *storageLvm) StoragePoolDelete() error {
	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("No \"source\" property found for the storage pool.")
	}

	// Remove the volume group.
	output, err := exec.Command("vgremove", "-f", s.pool.Name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Failed to destroy the volume group for the lvm storage pool: %s.", output)
	}

	// Delete the mountpoint for the storage pool.
	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	err = os.RemoveAll(poolMntPoint)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageLvm) StoragePoolMount() (bool, error) {
	return true, nil
}

func (s *storageLvm) StoragePoolUmount() (bool, error) {
	return true, nil
}

func (s *storageLvm) StoragePoolVolumeCreate() error {
	tryUndo := true

	vgName := s.pool.Name
	thinPoolName := s.volume.Config["lvm.thinpool_name"]
	lvFsType := s.volume.Config["block.filesystem"]
	lvSize := s.volume.Config["size"]

	volumeType, err := storagePoolVolumeTypeNameToApiEndpoint(s.volume.Type)
	if err != nil {
		return err
	}

	err = s.createThinLV(vgName, thinPoolName, s.volume.Name, lvFsType, lvSize, volumeType)
	if err != nil {
		s.log.Error("LVMCreateThinLV", log.Ctx{"err": err})
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

	return nil
}

func (s *storageLvm) StoragePoolVolumeDelete() error {
	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	_, err := s.StoragePoolVolumeUmount()
	if err != nil {
		return err
	}

	volumeType, err := storagePoolVolumeTypeNameToApiEndpoint(s.volume.Type)
	if err != nil {
		return err
	}

	err = s.removeLV(s.pool.Name, volumeType, s.volume.Name)
	if err != nil {
		return err
	}

	if shared.PathExists(customPoolVolumeMntPoint) {
		err := os.Remove(customPoolVolumeMntPoint)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageLvm) StoragePoolVolumeMount() (bool, error) {
	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	if shared.IsMountPoint(customPoolVolumeMntPoint) {
		return false, nil
	}

	lvFsType := s.volume.Config["block.filesystem"]
	volumeType, err := storagePoolVolumeTypeNameToApiEndpoint(s.volume.Type)
	if err != nil {
		return false, err
	}

	lvmVolumePath := getLvmDevPath(s.pool.Name, volumeType, s.volume.Name)
	mountOptions := s.volume.Config["block.mount_options"]
	err = tryMount(lvmVolumePath, customPoolVolumeMntPoint, lvFsType, 0, mountOptions)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (s *storageLvm) StoragePoolVolumeUmount() (bool, error) {
	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	if !shared.IsMountPoint(customPoolVolumeMntPoint) {
		return false, nil
	}

	err := tryUnmount(customPoolVolumeMntPoint, 0)
	if err != nil {
		return false, err
	}

	return true, nil
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

func (s *storageLvm) ContainerPoolGet() string {
	return s.pool.Name
}

func (s *storageLvm) ContainerPoolIDGet() int64 {
	return s.poolID
}

func (s *storageLvm) StoragePoolUpdate(changedConfig []string) error {
	if shared.StringInSlice("size", changedConfig) {
		return fmt.Errorf("The \"size\" property cannot be changed.")
	}

	if shared.StringInSlice("source", changedConfig) {
		return fmt.Errorf("The \"source\" property cannot be changed.")
	}

	if shared.StringInSlice("volume.zfs.use_refquota", changedConfig) {
		return fmt.Errorf("The \"volume.zfs.use_refquota\" property cannot be changed.")
	}

	if shared.StringInSlice("volume.zfs.remove_snapshots", changedConfig) {
		return fmt.Errorf("The \"volume.zfs.remove_snapshots\" property cannot be changed.")
	}

	if shared.StringInSlice("zfs.pool_name", changedConfig) {
		return fmt.Errorf("The \"zfs.pool_name\" property cannot be changed.")
	}

	if shared.StringInSlice("volume.block.mount_options", changedConfig) {
		// noop
	}

	if shared.StringInSlice("volume.block.filesystem", changedConfig) {
		// noop
	}

	if shared.StringInSlice("volume.size", changedConfig) {
		// noop
	}

	if shared.StringInSlice("volume.lvm.thinpool_name", changedConfig) {
		return fmt.Errorf("The \"volume.lvm.thinpool_name\" property cannot be changed.")
	}

	return nil
}

func (s *storageLvm) StoragePoolVolumeUpdate(changedConfig []string) error {
	if shared.StringInSlice("block.mount_options", changedConfig) && len(changedConfig) == 1 {
		// noop
	} else {
		return fmt.Errorf("The properties \"%v\" cannot be changed.", changedConfig)
	}

	return nil
}

func (s *storageLvm) ContainerCreate(container container) error {
	tryUndo := true

	containerName := container.Name()
	containerLvmName := containerNameToLVName(containerName)
	thinPoolName := s.volume.Config["lvm.thinpool_name"]
	lvFsType := s.volume.Config["block.filesystem"]
	lvSize := s.volume.Config["size"]

	err := s.createThinLV(s.pool.Name, thinPoolName, containerLvmName, lvFsType, lvSize, storagePoolVolumeApiEndpointContainers)
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

	return nil
}

func (s *storageLvm) ContainerCreateFromImage(container container, fingerprint string) error {
	tryUndo := true

	// Check if the image already exists.
	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	imageLvmDevPath := getLvmDevPath(s.pool.Name, storagePoolVolumeApiEndpointImages, fingerprint)

	imageStoragePoolLockID := getImageCreateLockID(s.pool.Name, fingerprint)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[imageStoragePoolLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			s.log.Warn("Received value over semaphore. This should not have happened.")
		}
	} else {
		lxdStorageOngoingOperationMap[imageStoragePoolLockID] = make(chan bool)
		lxdStorageMapLock.Unlock()

		var imgerr error
		if !shared.PathExists(imageMntPoint) || !shared.PathExists(imageLvmDevPath) {
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
	containerLvSnapshotPath, err := s.createSnapshotLV(s.pool.Name, fingerprint, storagePoolVolumeApiEndpointImages, containerLvmName, storagePoolVolumeApiEndpointContainers, false)
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
	lvFsType := s.volume.Config["block.filesystem"]
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
		s.log.Error("Error in create template during ContainerCreateFromImage, continuing to unmount", log.Ctx{"err": err})
		return err
	}

	tryUndo = false

	return nil
}

func (s *storageLvm) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageLvm) ContainerDelete(container container) error {
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
			return fmt.Errorf("failed to unmount container path '%s': %s", containerMntPoint, err)
		}
	}

	err := s.removeLV(s.pool.Name, storagePoolVolumeApiEndpointContainers, containerLvmName)
	if err != nil {
		return err
	}

	if container.IsSnapshot() {
		fields := strings.SplitN(containerName, shared.SnapshotDelimiter, 2)
		sourceName := fields[0]
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "snapshots", sourceName)
		snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
		err = deleteSnapshotMountpoint(containerMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	} else {
		err = tryUnmount(containerMntPoint, 0)
		err = deleteContainerMountpoint(containerMntPoint, container.Path(), s.GetStorageTypeName())
	}
	if err != nil {
		return err
	}

	return nil
}

func (s *storageLvm) ContainerCopy(container container, sourceContainer container) error {
	tryUndo := true

	err := sourceContainer.StorageStart()
	if err != nil {
		return err
	}
	defer sourceContainer.StorageStop()

	if sourceContainer.Storage().GetStorageType() == storageTypeLvm {
		err := s.createSnapshotContainer(container, sourceContainer, false)
		if err != nil {
			s.log.Error("Error creating snapshot LV for copy", log.Ctx{"err": err})
			return err
		}
	} else {
		sourceContainerName := sourceContainer.Name()
		targetContainerName := container.Name()
		s.log.Info("Copy from Non-LVM container", log.Ctx{"container": targetContainerName, "sourceContainer": sourceContainerName})
		err := s.ContainerCreate(container)
		if err != nil {
			s.log.Error("Error creating empty container", log.Ctx{"err": err})
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
			s.log.Error("Error starting/mounting container", log.Ctx{"err": err, "container": targetContainerName})
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

		sourcePool := sourceContainer.Storage().ContainerPoolGet()
		sourceContainerMntPoint := getContainerMountPoint(sourcePool, sourceContainerName)
		targetContainerMntPoint := getContainerMountPoint(s.pool.Name, targetContainerName)
		output, err := storageRsyncCopy(sourceContainerMntPoint, targetContainerMntPoint)
		if err != nil {
			s.log.Error("ContainerCopy: rsync failed", log.Ctx{"output": string(output)})
			s.ContainerDelete(container)
			return fmt.Errorf("rsync failed: %s", string(output))
		}
	}

	err = container.TemplateApply("copy")
	if err != nil {
		return err
	}

	tryUndo = false

	return nil
}

func (s *storageLvm) ContainerMount(name string, path string) (bool, error) {
	containerLvmName := containerNameToLVName(name)
	lvFsType := s.volume.Config["block.filesystem"]
	containerLvmPath := getLvmDevPath(s.pool.Name, storagePoolVolumeApiEndpointContainers, containerLvmName)
	mountOptions := s.volume.Config["block.mount_options"]
	containerMntPoint := getContainerMountPoint(s.pool.Name, name)

	containerMountLockID := getContainerMountLockID(s.pool.Name, name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			s.log.Warn("Received value over semaphore. This should not have happened.")
		}
		// Give the benefit of the doubt and assume that the other
		// thread actually succeeded in mounting the storage volume.
		return false, nil
	}

	lxdStorageOngoingOperationMap[containerMountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	var imgerr error
	ourMount := false
	if !shared.IsMountPoint(containerMntPoint) {
		imgerr = tryMount(containerLvmPath, containerMntPoint, lvFsType, 0, mountOptions)
		ourMount = true
	}

	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerMountLockID]; ok {
		close(waitChannel)
		delete(lxdStorageOngoingOperationMap, containerMountLockID)
	}
	lxdStorageMapLock.Unlock()

	if imgerr != nil {
		return false, imgerr
	}

	return ourMount, nil
}

func (s *storageLvm) ContainerUmount(name string, path string) (bool, error) {
	containerMntPoint := getContainerMountPoint(s.pool.Name, name)

	containerUmountLockID := getContainerUmountLockID(s.pool.Name, name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerUmountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			s.log.Warn("Received value over semaphore. This should not have happened.")
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

	return ourUmount, nil
}

func (s *storageLvm) ContainerRename(container container, newContainerName string) error {
	tryUndo := true

	oldName := container.Name()
	oldLvmName := containerNameToLVName(oldName)
	newLvmName := containerNameToLVName(newContainerName)

	_, err := s.ContainerUmount(oldName, container.Path())
	if err != nil {
		return err
	}

	output, err := s.renameLV(oldLvmName, newLvmName, storagePoolVolumeApiEndpointContainers)
	if err != nil {
		s.log.Error("Failed to rename a container LV", log.Ctx{"oldName": oldLvmName, "newName": newLvmName, "err": err, "output": string(output)})
		return fmt.Errorf("Failed to rename a container LV, oldName='%s', newName='%s', err='%s'", oldLvmName, newLvmName, err)
	}
	defer func() {
		if tryUndo {
			s.renameLV(newLvmName, oldLvmName, storagePoolVolumeApiEndpointContainers)
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

	return nil
}

func (s *storageLvm) ContainerRestore(container container, sourceContainer container) error {
	err := sourceContainer.StorageStart()
	if err != nil {
		return err
	}
	defer sourceContainer.StorageStop()

	if s.pool.Name != sourceContainer.Storage().ContainerPoolGet() {
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

	err = s.removeLV(s.pool.Name, storagePoolVolumeApiEndpointContainers, destName)
	if err != nil {
		s.log.Error(fmt.Sprintf("Failed to remove \"%s\": %s.", destName, err))
	}

	_, err = s.createSnapshotLV(s.pool.Name, srcLvName, storagePoolVolumeApiEndpointContainers, destLvName, storagePoolVolumeApiEndpointContainers, false)
	if err != nil {
		return fmt.Errorf("Error creating snapshot LV: %v", err)
	}

	return nil
}

func (s *storageLvm) ContainerSetQuota(container container, size int64) error {
	return fmt.Errorf("The LVM container backend doesn't support quotas.")
}

func (s *storageLvm) ContainerGetUsage(container container) (int64, error) {
	return -1, fmt.Errorf("The LVM container backend doesn't support quotas.")
}

func (s *storageLvm) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	return s.createSnapshotContainer(snapshotContainer, sourceContainer, true)
}

func (s *storageLvm) createSnapshotContainer(snapshotContainer container, sourceContainer container, readonly bool) error {
	tryUndo := true

	sourceContainerName := sourceContainer.Name()
	targetContainerName := snapshotContainer.Name()
	sourceContainerLvmName := containerNameToLVName(sourceContainerName)
	targetContainerLvmName := containerNameToLVName(targetContainerName)
	s.log.Debug("Creating snapshot", log.Ctx{"srcName": sourceContainerName, "destName": targetContainerName})

	_, err := s.createSnapshotLV(s.pool.Name, sourceContainerLvmName, storagePoolVolumeApiEndpointContainers, targetContainerLvmName, storagePoolVolumeApiEndpointContainers, readonly)
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
		sourcePool := sourceContainer.Storage().ContainerPoolGet()
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
	err := s.ContainerDelete(snapshotContainer)
	if err != nil {
		return fmt.Errorf("Error deleting snapshot %s: %s", snapshotContainer.Name(), err)
	}

	return nil
}

func (s *storageLvm) ContainerSnapshotRename(snapshotContainer container, newContainerName string) error {
	tryUndo := true

	oldName := snapshotContainer.Name()
	oldLvmName := containerNameToLVName(oldName)
	newLvmName := containerNameToLVName(newContainerName)

	output, err := s.renameLV(oldLvmName, newLvmName, storagePoolVolumeApiEndpointContainers)
	if err != nil {
		s.log.Error("Failed to rename a snapshot LV", log.Ctx{"oldName": oldLvmName, "newName": newLvmName, "err": err, "output": string(output)})
		return fmt.Errorf("Failed to rename a container LV, oldName='%s', newName='%s', err='%s'", oldLvmName, newLvmName, err)
	}
	defer func() {
		if tryUndo {
			s.renameLV(newLvmName, oldLvmName, storagePoolVolumeApiEndpointContainers)
		}
	}()

	oldSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, oldName)
	newSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, newContainerName)
	err = os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
	if err != nil {
		return err
	}

	tryUndo = false

	return nil
}

func (s *storageLvm) ContainerSnapshotStart(container container) error {
	tryUndo := true

	sourceName := container.Name()
	targetName := sourceName
	sourceLvmName := containerNameToLVName(sourceName)
	targetLvmName := containerNameToLVName(targetName)

	tmpTargetLvmName := getTmpSnapshotName(targetLvmName)

	s.log.Debug("Creating snapshot", log.Ctx{"srcName": sourceLvmName, "destName": targetLvmName})

	lvpath, err := s.createSnapshotLV(s.pool.Name, sourceLvmName, storagePoolVolumeApiEndpointContainers, tmpTargetLvmName, storagePoolVolumeApiEndpointContainers, false)
	if err != nil {
		return fmt.Errorf("Error creating snapshot LV: %s", err)
	}
	defer func() {
		if tryUndo {
			s.removeLV(s.pool.Name, storagePoolVolumeApiEndpointContainers, tmpTargetLvmName)
		}
	}()

	lvFsType := s.volume.Config["block.filesystem"]
	containerLvmPath := getLvmDevPath(s.pool.Name, storagePoolVolumeApiEndpointContainers, tmpTargetLvmName)
	mountOptions := s.volume.Config["block.mount_options"]
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

	return nil
}

func (s *storageLvm) ContainerSnapshotStop(container container) error {
	name := container.Name()
	snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, name)

	if shared.IsMountPoint(snapshotMntPoint) {
		err := tryUnmount(snapshotMntPoint, 0)
		if err != nil {
			return err
		}
	}

	lvName := containerNameToLVName(name)
	tmpLvName := getTmpSnapshotName(lvName)
	err := s.removeLV(s.pool.Name, storagePoolVolumeApiEndpointContainers, tmpLvName)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageLvm) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	return s.ContainerCreate(snapshotContainer)
}

func (s *storageLvm) ImageCreate(fingerprint string) error {
	tryUndo := true

	vgName := s.pool.Name
	thinPoolName := s.volume.Config["lvm.thinpool_name"]
	lvFsType := s.volume.Config["block.filesystem"]
	lvSize := s.volume.Config["size"]

	err := s.createImageDbPoolVolume(fingerprint)
	if err != nil {
		return err
	}

	err = s.createThinLV(vgName, thinPoolName, fingerprint, lvFsType, lvSize, storagePoolVolumeApiEndpointImages)
	if err != nil {
		s.log.Error("LVMCreateThinLV", log.Ctx{"err": err})
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

	return nil
}

func (s *storageLvm) ImageDelete(fingerprint string) error {
	_, err := s.ImageUmount(fingerprint)
	if err != nil {
		return err
	}

	err = s.removeLV(s.pool.Name, storagePoolVolumeApiEndpointImages, fingerprint)
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

	return nil
}

func (s *storageLvm) ImageMount(fingerprint string) (bool, error) {
	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if shared.IsMountPoint(imageMntPoint) {
		return false, nil
	}

	// Shouldn't happen.
	lvmFstype := s.volume.Config["block.filesystem"]
	if lvmFstype == "" {
		return false, fmt.Errorf("No filesystem type specified.")
	}

	lvmVolumePath := getLvmDevPath(s.pool.Name, storagePoolVolumeApiEndpointImages, fingerprint)
	lvmMountOptions := s.volume.Config["block.mount_options"]
	// Shouldn't be necessary since it should be validated in the config
	// checks.
	if lvmFstype == "ext4" && lvmMountOptions == "" {
		lvmMountOptions = "discard"
	}

	err := tryMount(lvmVolumePath, imageMntPoint, lvmFstype, 0, lvmMountOptions)
	if err != nil {
		s.log.Info(fmt.Sprintf("Error mounting image LV for unpacking: %s", err))
		return false, fmt.Errorf("Error mounting image LV: %v", err)
	}

	return true, nil
}

func (s *storageLvm) ImageUmount(fingerprint string) (bool, error) {
	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if !shared.IsMountPoint(imageMntPoint) {
		return false, nil
	}

	err := tryUnmount(imageMntPoint, 0)
	if err != nil {
		return false, err
	}

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
			s.log.Error("Setting thin pool name", log.Ctx{"err": err})
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
		s.log.Error("Could not create LV", log.Ctx{"lvname": lvmPoolVolumeName, "output": string(output)})
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
		s.log.Error("Filesystem creation failed", log.Ctx{"output": string(output)})
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
		s.log.Error("Could not create thin pool", log.Ctx{"name": thinPoolName, "err": err, "output": string(output)})
		return fmt.Errorf("Could not create LVM thin pool named %s", thinPoolName)
	}

	if !isRecent {
		// Grow it to the maximum VG size (two step process required by old LVM)
		output, err = tryExec("lvextend", "--alloc", "anywhere", "-l", "100%FREE", lvmThinPool)

		if err != nil {
			s.log.Error("Could not grow thin pool", log.Ctx{"name": thinPoolName, "err": err, "output": string(output)})
			return fmt.Errorf("Could not grow LVM thin pool named %s", thinPoolName)
		}
	}

	return nil
}

func (s *storageLvm) removeLV(vgName string, volumeType string, lvName string) error {
	lvmVolumePath := getLvmDevPath(vgName, volumeType, lvName)
	output, err := tryExec("lvremove", "-f", lvmVolumePath)

	if err != nil {
		s.log.Error("Could not remove LV", log.Ctx{"lvname": lvName, "output": string(output)})
		return fmt.Errorf("Could not remove LV named %s", lvName)
	}

	return nil
}

func (s *storageLvm) createSnapshotLV(vgName string, origLvName string, origVolumeType string, lvName string, volumeType string, readonly bool) (string, error) {
	sourceLvmVolumePath := getLvmDevPath(vgName, origVolumeType, origLvName)
	s.log.Debug("in createSnapshotLV:", log.Ctx{"lvname": lvName, "dev string": sourceLvmVolumePath})
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
		s.log.Error("Could not create LV snapshot", log.Ctx{"lvname": lvName, "origlvname": origLvName, "output": string(output)})
		return "", fmt.Errorf("Could not create snapshot LV named %s", lvName)
	}

	targetLvmVolumePath := getLvmDevPath(vgName, volumeType, lvName)
	if readonly {
		output, err = tryExec("lvchange", "-ay", "-pr", targetLvmVolumePath)
	} else {
		output, err = tryExec("lvchange", "-ay", targetLvmVolumePath)
	}

	if err != nil {
		return "", fmt.Errorf("Could not activate new snapshot '%s': %v\noutput:%s", lvName, err, string(output))
	}

	return targetLvmVolumePath, nil
}

func (s *storageLvm) renameLV(oldName string, newName string, volumeType string) (string, error) {
	oldLvmName := getPrefixedLvName(volumeType, oldName)
	newLvmName := getPrefixedLvName(volumeType, newName)
	output, err := tryExec("lvrename", s.pool.Name, oldLvmName, newLvmName)
	return string(output), err
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
