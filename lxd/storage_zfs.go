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
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"

	"github.com/pborman/uuid"
)

var zfsUseRefquota = "false"
var zfsRemoveSnapshots = "false"

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

func zfsIsEnabled() bool {
	out, err := exec.LookPath("zfs")
	if err != nil || len(out) == 0 {
		return false
	}

	return true
}

func zfsModuleVersionGet() (string, error) {
	zfsVersion, err := ioutil.ReadFile("/sys/module/zfs/version")
	if err != nil {
		return "", fmt.Errorf("Could not determine ZFS module version.")
	}

	return strings.TrimSpace(string(zfsVersion)), nil
}

// Only initialize the minimal information we need about a given storage type.
func (s *storageZfs) StorageCoreInit() error {
	s.sType = storageTypeZfs
	typeName, err := storageTypeToString(s.sType)
	if err != nil {
		return err
	}
	s.sTypeName = typeName

	if !zfsIsEnabled() {
		return fmt.Errorf("The \"zfs\" tool is not enabled.")
	}

	s.sTypeVersion, err = zfsModuleVersionGet()
	if err != nil {
		return err
	}

	shared.LogDebugf("Initializing a ZFS driver.")
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
	shared.LogDebugf("Checking ZFS storage pool \"%s\".", s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("No \"source\" property found for the storage pool.")
	}

	poolName := s.getOnDiskPoolName()
	if filepath.IsAbs(source) {
		if zfsFilesystemEntityExists(poolName) {
			return nil
		}
		shared.LogDebugf("ZFS storage pool \"%s\" does not exist. Trying to import it.", poolName)

		disksPath := shared.VarPath("disks")
		output, err := shared.RunCommand(
			"zpool",
			"import",
			"-d", disksPath, poolName)
		if err != nil {
			return fmt.Errorf("ZFS storage pool \"%s\" could not be imported: %s.", poolName, output)
		}

		shared.LogDebugf("ZFS storage pool \"%s\" successfully imported.", poolName)
	}

	return nil
}

func (s *storageZfs) StoragePoolCreate() error {
	shared.LogInfof("Creating ZFS storage pool \"%s\".", s.pool.Name)

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

	storagePoolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	err = os.MkdirAll(storagePoolMntPoint, 0755)
	if err != nil {
		return err
	}

	err = s.StoragePoolCheck()
	if err != nil {
		return err
	}

	revert = false

	shared.LogInfof("Created ZFS storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageZfs) StoragePoolDelete() error {
	shared.LogInfof("Deleting ZFS storage pool \"%s\".", s.pool.Name)

	err := s.zfsFilesystemEntityDelete()
	if err != nil {
		return err
	}

	storagePoolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	if shared.PathExists(storagePoolMntPoint) {
		err := os.RemoveAll(storagePoolMntPoint)
		if err != nil {
			return err
		}
	}

	shared.LogInfof("Deleted ZFS storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageZfs) StoragePoolMount() (bool, error) {
	return true, nil
}

func (s *storageZfs) StoragePoolUmount() (bool, error) {
	return true, nil
}

func (s *storageZfs) StoragePoolVolumeCreate() error {
	shared.LogInfof("Creating ZFS storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	fs := fmt.Sprintf("custom/%s", s.volume.Name)
	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	err := s.zfsPoolVolumeCreate(fs)
	if err != nil {
		return err
	}
	revert := true
	defer func() {
		if !revert {
			return
		}
		s.StoragePoolVolumeDelete()
	}()

	err = s.zfsPoolVolumeSet(fs, "mountpoint", customPoolVolumeMntPoint)
	if err != nil {
		return err
	}

	if !shared.IsMountPoint(customPoolVolumeMntPoint) {
		s.zfsPoolVolumeMount(fs)
	}

	revert = false

	shared.LogInfof("Created ZFS storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) StoragePoolVolumeDelete() error {
	shared.LogInfof("Deleting ZFS storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	fs := fmt.Sprintf("custom/%s", s.volume.Name)
	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	err := s.zfsPoolVolumeDestroy(fs)
	if err != nil {
		return err
	}

	if shared.PathExists(customPoolVolumeMntPoint) {
		err := os.RemoveAll(customPoolVolumeMntPoint)
		if err != nil {
			return err
		}
	}

	shared.LogInfof("Deleted ZFS storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) StoragePoolVolumeMount() (bool, error) {
	shared.LogDebugf("Mounting ZFS storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	fs := fmt.Sprintf("custom/%s", s.volume.Name)
	customPoolVolumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

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
		customerr = s.zfsPoolVolumeMount(fs)
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

	shared.LogDebugf("Mounted ZFS storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageZfs) StoragePoolVolumeUmount() (bool, error) {
	shared.LogDebugf("Unmounting ZFS storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	fs := fmt.Sprintf("custom/%s", s.volume.Name)
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
		customerr = s.zfsPoolVolumeUmount(fs)
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

	shared.LogDebugf("Unmounted ZFS storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return ourUmount, nil
}

func (s *storageZfs) GetStoragePoolWritable() api.StoragePoolPut {
	return s.pool.Writable()
}

func (s *storageZfs) GetStoragePoolVolumeWritable() api.StorageVolumePut {
	return s.volume.Writable()
}

func (s *storageZfs) SetStoragePoolWritable(writable *api.StoragePoolPut) {
	s.pool.StoragePoolPut = *writable
}

func (s *storageZfs) SetStoragePoolVolumeWritable(writable *api.StorageVolumePut) {
	s.volume.StorageVolumePut = *writable
}

func (s *storageZfs) GetContainerPoolInfo() (int64, string) {
	return s.poolID, s.pool.Name
}

func (s *storageZfs) StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error {
	shared.LogInfof("Updating ZFS storage pool \"%s\".", s.pool.Name)

	if shared.StringInSlice("size", changedConfig) {
		return fmt.Errorf("The \"size\" property cannot be changed.")
	}

	if shared.StringInSlice("source", changedConfig) {
		return fmt.Errorf("The \"source\" property cannot be changed.")
	}

	if shared.StringInSlice("volume.size", changedConfig) {
		return fmt.Errorf("The \"volume.size\" property cannot be changed.")
	}

	if shared.StringInSlice("volume.block.mount_options", changedConfig) {
		return fmt.Errorf("The \"volume.block.mount_options\" property cannot be changed.")
	}

	if shared.StringInSlice("volume.block.filesystem", changedConfig) {
		return fmt.Errorf("The \"volume.block.filesystem\" property cannot be changed.")
	}

	if shared.StringInSlice("lvm.thinpool_name", changedConfig) {
		return fmt.Errorf("The \"lvm.thinpool_name\" property cannot be changed.")
	}

	if shared.StringInSlice("lvm.vg_name", changedConfig) {
		return fmt.Errorf("The \"lvm.vg_name\" property cannot be changed.")
	}

	if shared.StringInSlice("zfs.pool_name", changedConfig) {
		return fmt.Errorf("The \"zfs.pool_name\" property cannot be changed.")
	}

	shared.LogInfof("Updated ZFS storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageZfs) StoragePoolVolumeUpdate(changedConfig []string) error {
	shared.LogInfof("Updating ZFS storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	if shared.StringInSlice("block.mount_options", changedConfig) {
		return fmt.Errorf("The \"block.mount_options\" property cannot be changed.")
	}

	if shared.StringInSlice("block.filesystem", changedConfig) {
		return fmt.Errorf("The \"block.filesystem\" property cannot be changed.")
	}

	if shared.StringInSlice("size", changedConfig) {
		return fmt.Errorf("The \"size\" property cannot be changed.")
	}

	shared.LogInfof("Updated ZFS storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

// Things we don't need to care about
func (s *storageZfs) ContainerMount(name string, path string) (bool, error) {
	shared.LogDebugf("Mounting ZFS storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	fs := fmt.Sprintf("containers/%s", name)
	containerPoolVolumeMntPoint := getContainerMountPoint(s.pool.Name, name)

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

	var imgerr error
	ourMount := false
	if !shared.IsMountPoint(containerPoolVolumeMntPoint) {
		imgerr = s.zfsPoolVolumeMount(fs)
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

	shared.LogDebugf("Mounted ZFS storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageZfs) ContainerUmount(name string, path string) (bool, error) {
	shared.LogDebugf("Unmounting ZFS storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	fs := fmt.Sprintf("containers/%s", name)
	containerPoolVolumeMntPoint := getContainerMountPoint(s.pool.Name, name)

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
	if shared.IsMountPoint(containerPoolVolumeMntPoint) {
		imgerr = s.zfsPoolVolumeUmount(fs)
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

	shared.LogDebugf("Unmounted ZFS storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return ourUmount, nil
}

// Things we do have to care about
func (s *storageZfs) ContainerStorageReady(name string) bool {
	poolName := s.getOnDiskPoolName()
	fs := fmt.Sprintf("%s/containers/%s", poolName, name)
	return s.zfsFilesystemEntityExists(fs, false)
}

func (s *storageZfs) ContainerCreate(container container) error {
	shared.LogDebugf("Creating empty ZFS storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	containerPath := container.Path()
	containerName := container.Name()
	fs := fmt.Sprintf("containers/%s", containerName)
	containerPoolVolumeMntPoint := getContainerMountPoint(s.pool.Name, containerName)

	// Create volume.
	err := s.zfsPoolVolumeCreate(fs)
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

	// Set mountpoint.
	err = s.zfsPoolVolumeSet(fs, "mountpoint", containerPoolVolumeMntPoint)
	if err != nil {
		return err
	}

	err = createContainerMountpoint(containerPoolVolumeMntPoint, containerPath, container.IsPrivileged())
	if err != nil {
		return err
	}

	err = container.TemplateApply("create")
	if err != nil {
		return err
	}

	revert = false

	shared.LogDebugf("Created empty ZFS storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) ContainerCreateFromImage(container container, fingerprint string) error {
	shared.LogDebugf("Creating ZFS storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	containerPath := container.Path()
	containerName := container.Name()
	fs := fmt.Sprintf("containers/%s", containerName)
	containerPoolVolumeMntPoint := getContainerMountPoint(s.pool.Name, containerName)

	fsImage := fmt.Sprintf("images/%s", fingerprint)

	imageStoragePoolLockID := getImageCreateLockID(s.pool.Name, fingerprint)
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
		if !s.zfsFilesystemEntityExists(fsImage, true) {
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

	err := s.zfsPoolVolumeClone(fsImage, "readonly", fs, containerPoolVolumeMntPoint)
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

	privileged := container.IsPrivileged()
	err = createContainerMountpoint(containerPoolVolumeMntPoint, containerPath, privileged)
	if err != nil {
		return err
	}

	if !privileged {
		err = s.shiftRootfs(container)
		if err != nil {
			return err
		}
	}

	err = container.TemplateApply("create")
	if err != nil {
		return err
	}

	revert = false

	shared.LogDebugf("Created ZFS storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) ContainerCanRestore(container container, sourceContainer container) error {
	snaps, err := container.Snapshots()
	if err != nil {
		return err
	}

	if snaps[len(snaps)-1].Name() != sourceContainer.Name() {
		if s.pool.Config["volume.zfs.remove_snapshots"] != "" {
			zfsRemoveSnapshots = s.pool.Config["volume.zfs.remove_snapshots"]
		}
		if s.volume.Config["zfs.remove_snapshots"] != "" {
			zfsRemoveSnapshots = s.volume.Config["zfs.remove_snapshots"]
		}
		if !shared.IsTrue(zfsRemoveSnapshots) {
			return fmt.Errorf("ZFS can only restore from the latest snapshot. Delete newer snapshots or copy the snapshot into a new container instead.")
		}

		return nil
	}

	return nil
}

func (s *storageZfs) ContainerDelete(container container) error {
	shared.LogDebugf("Deleting ZFS storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	containerName := container.Name()
	fs := fmt.Sprintf("containers/%s", containerName)
	containerPoolVolumeMntPoint := getContainerMountPoint(s.pool.Name, containerName)

	if s.zfsFilesystemEntityExists(fs, true) {
		removable := true
		snaps, err := s.zfsPoolListSnapshots(fs)
		if err != nil {
			return err
		}

		for _, snap := range snaps {
			var err error
			removable, err = s.zfsPoolVolumeSnapshotRemovable(fs, snap)
			if err != nil {
				return err
			}

			if !removable {
				break
			}
		}

		if removable {
			origin, err := s.zfsFilesystemEntityPropertyGet(fs, "origin", true)
			if err != nil {
				return err
			}
			poolName := s.getOnDiskPoolName()
			origin = strings.TrimPrefix(origin, fmt.Sprintf("%s/", poolName))

			err = s.zfsPoolVolumeDestroy(fs)
			if err != nil {
				return err
			}

			err = s.zfsPoolVolumeCleanup(origin)
			if err != nil {
				return err
			}
		} else {
			err := s.zfsPoolVolumeSet(fs, "mountpoint", "none")
			if err != nil {
				return err
			}

			err = s.zfsPoolVolumeRename(fs, fmt.Sprintf("deleted/containers/%s", uuid.NewRandom().String()))
			if err != nil {
				return err
			}
		}
	}

	err := deleteContainerMountpoint(containerPoolVolumeMntPoint, container.Path(), s.GetStorageTypeName())
	if err != nil {
		return err
	}

	snapshotZfsDataset := fmt.Sprintf("snapshots/%s", containerName)
	s.zfsPoolVolumeDestroy(snapshotZfsDataset)

	// Delete potential leftover snapshot mountpoints.
	snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, containerName)
	if shared.PathExists(snapshotMntPoint) {
		err := os.RemoveAll(snapshotMntPoint)
		if err != nil {
			return err
		}
	}

	// Delete potential leftover snapshot symlinks:
	// ${LXD_DIR}/snapshots/<container_name> -> ${POOL}/snapshots/<container_name>
	snapshotSymlink := shared.VarPath("snapshots", containerName)
	if shared.PathExists(snapshotSymlink) {
		err := os.Remove(snapshotSymlink)
		if err != nil {
			return err
		}
	}

	shared.LogDebugf("Deleted ZFS storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) ContainerCopy(container container, sourceContainer container) error {
	shared.LogDebugf("Copying ZFS container storage %s -> %s.", sourceContainer.Name(), container.Name())

	sourceContainerName := sourceContainer.Name()
	sourceContainerPath := sourceContainer.Path()

	targetContainerName := container.Name()
	targetContainerPath := container.Path()
	targetContainerMountPoint := getContainerMountPoint(s.pool.Name, targetContainerName)

	sourceZfsDataset := ""
	sourceZfsDatasetSnapshot := ""
	sourceFields := strings.SplitN(sourceContainerName, shared.SnapshotDelimiter, 2)
	sourceName := sourceFields[0]

	targetZfsDataset := fmt.Sprintf("containers/%s", targetContainerName)

	if len(sourceFields) == 2 {
		sourceZfsDatasetSnapshot = sourceFields[1]
	}

	revert := true
	if sourceZfsDatasetSnapshot == "" {
		if s.zfsFilesystemEntityExists(fmt.Sprintf("containers/%s", sourceName), true) {
			sourceZfsDatasetSnapshot = fmt.Sprintf("copy-%s", uuid.NewRandom().String())
			sourceZfsDataset = fmt.Sprintf("containers/%s", sourceName)
			err := s.zfsPoolVolumeSnapshotCreate(sourceZfsDataset, sourceZfsDatasetSnapshot)
			if err != nil {
				return err
			}
			defer func() {
				if !revert {
					return
				}
				s.zfsPoolVolumeSnapshotDestroy(sourceZfsDataset, sourceZfsDatasetSnapshot)
			}()
		}
	} else {
		if s.zfsFilesystemEntityExists(fmt.Sprintf("containers/%s@snapshot-%s", sourceName, sourceZfsDatasetSnapshot), true) {
			sourceZfsDataset = fmt.Sprintf("containers/%s", sourceName)
			sourceZfsDatasetSnapshot = fmt.Sprintf("snapshot-%s", sourceZfsDatasetSnapshot)
		}
	}

	if sourceZfsDataset != "" {
		err := s.zfsPoolVolumeClone(sourceZfsDataset, sourceZfsDatasetSnapshot, targetZfsDataset, targetContainerMountPoint)
		if err != nil {
			return err
		}
		defer func() {
			if !revert {
				return
			}
			s.zfsPoolVolumeDestroy(targetZfsDataset)
		}()

		ourMount, err := s.ContainerMount(targetContainerName, targetContainerPath)
		if err != nil {
			return err
		}
		if ourMount {
			defer s.ContainerUmount(targetContainerName, targetContainerPath)
		}

		err = createContainerMountpoint(targetContainerMountPoint, targetContainerPath, container.IsPrivileged())
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
		err := s.ContainerCreate(container)
		if err != nil {
			return err
		}
		defer func() {
			if !revert {
				return
			}
			s.ContainerDelete(container)
		}()

		output, err := storageRsyncCopy(sourceContainerPath, targetContainerPath)
		if err != nil {
			return fmt.Errorf("rsync failed: %s", string(output))
		}
	}

	err := container.TemplateApply("copy")
	if err != nil {
		return err
	}

	revert = false

	shared.LogDebugf("Copied ZFS container storage %s -> %s.", sourceContainer.Name(), container.Name())
	return nil
}

func (s *storageZfs) ContainerRename(container container, newName string) error {
	shared.LogDebugf("Renaming ZFS storage volume for container \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newName)

	oldName := container.Name()

	// Unmount the dataset.
	_, err := s.ContainerUmount(oldName, "")
	if err != nil {
		return err
	}

	// Rename the dataset.
	oldZfsDataset := fmt.Sprintf("containers/%s", oldName)
	newZfsDataset := fmt.Sprintf("containers/%s", newName)
	err = s.zfsPoolVolumeRename(oldZfsDataset, newZfsDataset)
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
	newContainerMntPoint := getContainerMountPoint(s.pool.Name, newName)
	err = s.zfsPoolVolumeSet(newZfsDataset, "mountpoint", newContainerMntPoint)
	if err != nil {
		return err
	}

	// Unmount the dataset.
	_, err = s.ContainerUmount(newName, "")
	if err != nil {
		return err
	}

	// Create new mountpoint on the storage pool.
	oldContainerMntPoint := getContainerMountPoint(s.pool.Name, oldName)
	oldContainerMntPointSymlink := container.Path()
	newContainerMntPointSymlink := shared.VarPath("containers", newName)
	err = renameContainerMountpoint(oldContainerMntPoint, oldContainerMntPointSymlink, newContainerMntPoint, newContainerMntPointSymlink)
	if err != nil {
		return err
	}

	// Rename the snapshot mountpoint on the storage pool.
	oldSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, oldName)
	newSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, newName)
	if shared.PathExists(oldSnapshotMntPoint) {
		err := os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
		if err != nil {
			return err
		}
	}

	// Remove old symlink.
	oldSnapshotPath := shared.VarPath("snapshots", oldName)
	if shared.PathExists(oldSnapshotPath) {
		err := os.Remove(oldSnapshotPath)
		if err != nil {
			return err
		}
	}

	// Create new symlink.
	newSnapshotPath := shared.VarPath("snapshots", newName)
	if shared.PathExists(newSnapshotPath) {
		err := os.Symlink(newSnapshotMntPoint, newSnapshotPath)
		if err != nil {
			return err
		}
	}

	revert = false

	shared.LogDebugf("Renamed ZFS storage volume for container \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newName)
	return nil
}

func (s *storageZfs) ContainerRestore(container container, sourceContainer container) error {
	shared.LogDebugf("Restoring ZFS storage volume for container \"%s\" from %s -> %s.", s.volume.Name, sourceContainer.Name(), container.Name())

	// Remove any needed snapshot
	snaps, err := container.Snapshots()
	if err != nil {
		return err
	}

	for i := len(snaps) - 1; i != 0; i-- {
		if snaps[i].Name() == sourceContainer.Name() {
			break
		}

		err := snaps[i].Delete()
		if err != nil {
			return err
		}
	}

	// Restore the snapshot
	fields := strings.SplitN(sourceContainer.Name(), shared.SnapshotDelimiter, 2)
	cName := fields[0]
	snapName := fmt.Sprintf("snapshot-%s", fields[1])

	err = s.zfsPoolVolumeSnapshotRestore(fmt.Sprintf("containers/%s", cName), snapName)
	if err != nil {
		return err
	}

	shared.LogDebugf("Restored ZFS storage volume for container \"%s\" from %s -> %s.", s.volume.Name, sourceContainer.Name(), container.Name())
	return nil
}

func (s *storageZfs) ContainerSetQuota(container container, size int64) error {
	shared.LogDebugf("Setting ZFS quota for container \"%s\".", container.Name())

	var err error

	fs := fmt.Sprintf("containers/%s", container.Name())

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

	if size > 0 {
		err = s.zfsPoolVolumeSet(fs, property, fmt.Sprintf("%d", size))
	} else {
		err = s.zfsPoolVolumeSet(fs, property, "none")
	}

	if err != nil {
		return err
	}

	shared.LogDebugf("Set ZFS quota for container \"%s\".", container.Name())
	return nil
}

func (s *storageZfs) ContainerGetUsage(container container) (int64, error) {
	var err error

	fs := fmt.Sprintf("containers/%s", container.Name())

	property := "used"

	if s.pool.Config["volume.zfs.use_refquota"] != "" {
		zfsUseRefquota = s.pool.Config["volume.zfs.use_refquota"]
	}
	if s.volume.Config["zfs.use_refquota"] != "" {
		zfsUseRefquota = s.volume.Config["zfs.use_refquota"]
	}

	if shared.IsTrue(zfsUseRefquota) {
		property = "usedbydataset"
	}

	value, err := s.zfsFilesystemEntityPropertyGet(fs, property, true)
	if err != nil {
		return -1, err
	}

	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return -1, err
	}

	return valueInt, nil
}

func (s *storageZfs) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	shared.LogDebugf("Creating ZFS storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	snapshotContainerName := snapshotContainer.Name()
	sourceContainerName := sourceContainer.Name()

	fields := strings.SplitN(snapshotContainerName, shared.SnapshotDelimiter, 2)
	cName := fields[0]
	snapName := fmt.Sprintf("snapshot-%s", fields[1])

	sourceZfsDataset := fmt.Sprintf("containers/%s", cName)
	err := s.zfsPoolVolumeSnapshotCreate(sourceZfsDataset, snapName)
	if err != nil {
		return err
	}
	revert := true
	defer func() {
		if !revert {
			return
		}
		s.ContainerSnapshotDelete(snapshotContainer)
	}()

	snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, snapshotContainerName)
	if !shared.PathExists(snapshotMntPoint) {
		err := os.MkdirAll(snapshotMntPoint, 0700)
		if err != nil {
			return err
		}
	}

	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "snapshots", s.volume.Name)
	snapshotMntPointSymlink := shared.VarPath("snapshots", sourceContainerName)
	if !shared.PathExists(snapshotMntPointSymlink) {
		err := os.Symlink(snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return err
		}
	}

	revert = false

	shared.LogDebugf("Created ZFS storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) ContainerSnapshotDelete(snapshotContainer container) error {
	shared.LogDebugf("Deleting ZFS storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	fields := strings.SplitN(snapshotContainer.Name(), shared.SnapshotDelimiter, 2)
	sourceContainerName := fields[0]
	snapName := fmt.Sprintf("snapshot-%s", fields[1])

	if s.zfsFilesystemEntityExists(fmt.Sprintf("containers/%s@%s", sourceContainerName, snapName), true) {
		removable, err := s.zfsPoolVolumeSnapshotRemovable(fmt.Sprintf("containers/%s", sourceContainerName), snapName)
		if removable {
			err = s.zfsPoolVolumeSnapshotDestroy(fmt.Sprintf("containers/%s", sourceContainerName), snapName)
			if err != nil {
				return err
			}
		} else {
			err = s.zfsPoolVolumeSnapshotRename(fmt.Sprintf("containers/%s", sourceContainerName), snapName, fmt.Sprintf("copy-%s", uuid.NewRandom().String()))
			if err != nil {
				return err
			}
		}
	}

	// Delete the snapshot on its storage pool:
	// ${POOL}/snapshots/<snapshot_name>
	snapshotContainerName := snapshotContainer.Name()
	snapshotContainerMntPoint := getSnapshotMountPoint(s.pool.Name, snapshotContainerName)
	if shared.PathExists(snapshotContainerMntPoint) {
		err := os.RemoveAll(snapshotContainerMntPoint)
		if err != nil {
			return err
		}
	}

	// Check if we can remove the snapshot symlink:
	// ${LXD_DIR}/snapshots/<container_name> -> ${POOL}/snapshots/<container_name>
	// by checking if the directory is empty.
	snapshotContainerPath := getSnapshotMountPoint(s.pool.Name, sourceContainerName)
	empty, _ := shared.PathIsEmpty(snapshotContainerPath)
	if empty == true {
		// Remove the snapshot directory for the container:
		// ${POOL}/snapshots/<source_container_name>
		err := os.Remove(snapshotContainerPath)
		if err != nil {
			return err
		}

		snapshotSymlink := shared.VarPath("snapshots", sourceContainerName)
		if shared.PathExists(snapshotSymlink) {
			err := os.Remove(snapshotSymlink)
			if err != nil {
				return err
			}
		}
	}

	// Legacy
	snapPath := shared.VarPath(fmt.Sprintf("snapshots/%s/%s.zfs", sourceContainerName, fields[1]))
	if shared.PathExists(snapPath) {
		err := os.Remove(snapPath)
		if err != nil {
			return err
		}
	}

	// Legacy
	parent := shared.VarPath(fmt.Sprintf("snapshots/%s", sourceContainerName))
	if ok, _ := shared.PathIsEmpty(parent); ok {
		err := os.Remove(parent)
		if err != nil {
			return err
		}
	}

	shared.LogDebugf("Deleted ZFS storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) ContainerSnapshotRename(snapshotContainer container, newName string) error {
	shared.LogDebugf("Renaming ZFS storage volume for snapshot \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newName)

	oldName := snapshotContainer.Name()

	oldFields := strings.SplitN(snapshotContainer.Name(), shared.SnapshotDelimiter, 2)
	oldcName := oldFields[0]
	oldZfsDatasetName := fmt.Sprintf("snapshot-%s", oldFields[1])

	newFields := strings.SplitN(newName, shared.SnapshotDelimiter, 2)
	newZfsDatasetName := fmt.Sprintf("snapshot-%s", newFields[1])

	if oldZfsDatasetName != newZfsDatasetName {
		err := s.zfsPoolVolumeSnapshotRename(fmt.Sprintf("containers/%s", oldcName), oldZfsDatasetName, newZfsDatasetName)
		if err != nil {
			return err
		}
	}
	revert := true
	defer func() {
		if !revert {
			return
		}
		s.ContainerSnapshotRename(snapshotContainer, oldName)
	}()

	oldStyleSnapshotMntPoint := shared.VarPath(fmt.Sprintf("snapshots/%s/%s.zfs", oldcName, oldFields[1]))
	if shared.PathExists(oldStyleSnapshotMntPoint) {
		err := os.Remove(oldStyleSnapshotMntPoint)
		if err != nil {
			return err
		}
	}

	oldSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, oldName)
	if shared.PathExists(oldSnapshotMntPoint) {
		err := os.Remove(oldSnapshotMntPoint)
		if err != nil {
			return err
		}
	}

	newSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, newName)
	if !shared.PathExists(newSnapshotMntPoint) {
		err := os.MkdirAll(newSnapshotMntPoint, 0700)
		if err != nil {
			return err
		}
	}

	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "snapshots", oldcName)
	snapshotMntPointSymlink := shared.VarPath("snapshots", oldcName)
	if !shared.PathExists(snapshotMntPointSymlink) {
		err := os.Symlink(snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return err
		}
	}

	revert = false

	shared.LogDebugf("Renamed ZFS storage volume for snapshot \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newName)
	return nil
}

func (s *storageZfs) ContainerSnapshotStart(container container) error {
	shared.LogDebugf("Initializing ZFS storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	fields := strings.SplitN(container.Name(), shared.SnapshotDelimiter, 2)
	if len(fields) < 2 {
		return fmt.Errorf("Invalid snapshot name: %s", container.Name())
	}

	cName := fields[0]
	sName := fields[1]
	sourceFs := fmt.Sprintf("containers/%s", cName)
	sourceSnap := fmt.Sprintf("snapshot-%s", sName)
	destFs := fmt.Sprintf("snapshots/%s/%s", cName, sName)

	snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, container.Name())
	err := s.zfsPoolVolumeClone(sourceFs, sourceSnap, destFs, snapshotMntPoint)
	if err != nil {
		return err
	}

	shared.LogDebugf("Initialized ZFS storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) ContainerSnapshotStop(container container) error {
	shared.LogDebugf("Stopping ZFS storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	fields := strings.SplitN(container.Name(), shared.SnapshotDelimiter, 2)
	if len(fields) < 2 {
		return fmt.Errorf("Invalid snapshot name: %s", container.Name())
	}
	cName := fields[0]
	sName := fields[1]
	destFs := fmt.Sprintf("snapshots/%s/%s", cName, sName)

	err := s.zfsPoolVolumeDestroy(destFs)
	if err != nil {
		return err
	}

	/* zfs creates this directory on clone (start), so we need to clean it
	 * up on stop */
	shared.LogDebugf("Stopped ZFS storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	err = os.RemoveAll(container.Path())
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	/* don't touch the fs yet, as migration will do that for us */
	return nil
}

// - create temporary directory ${LXD_DIR}/images/lxd_images_
// - create new zfs volume images/<fingerprint>
// - mount the zfs volume on ${LXD_DIR}/images/lxd_images_
// - unpack the downloaded image in ${LXD_DIR}/images/lxd_images_
// - mark new zfs volume images/<fingerprint> readonly
// - remove mountpoint property from zfs volume images/<fingerprint>
// - create read-write snapshot from zfs volume images/<fingerprint>
func (s *storageZfs) ImageCreate(fingerprint string) error {
	shared.LogDebugf("Creating ZFS storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	fs := fmt.Sprintf("images/%s", fingerprint)
	revert := true
	subrevert := true

	err := s.createImageDbPoolVolume(fingerprint)
	if err != nil {
		return err
	}
	defer func() {
		if !subrevert {
			return
		}
		s.deleteImageDbPoolVolume(fingerprint)
	}()

	if s.zfsFilesystemEntityExists(fmt.Sprintf("deleted/%s", fs), true) {
		err := s.zfsPoolVolumeRename(fmt.Sprintf("deleted/%s", fs), fs)
		if err != nil {
			return err
		}

		defer func() {
			if !revert {
				return
			}
			s.ImageDelete(fingerprint)
		}()

		// In case this is an image from an older lxd instance, wipe the
		// mountpoint.
		err = s.zfsPoolVolumeSet(fs, "mountpoint", "none")
		if err != nil {
			return err
		}

		revert = false

		return nil
	}

	if !shared.PathExists(imageMntPoint) {
		err := os.MkdirAll(imageMntPoint, 0700)
		if err != nil {
			return err
		}
		defer func() {
			if !subrevert {
				return
			}
			os.RemoveAll(imageMntPoint)
		}()
	}

	// Create temporary mountpoint directory.
	tmp := getImageMountPoint(s.pool.Name, "")
	tmpImageDir, err := ioutil.TempDir(tmp, "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpImageDir)

	imagePath := shared.VarPath("images", fingerprint)

	// Create a new storage volume on the storage pool for the image.
	err = s.zfsPoolVolumeCreate(fs)
	if err != nil {
		return err
	}
	subrevert = false
	defer func() {
		if !revert {
			return
		}
		s.ImageDelete(fingerprint)
	}()

	// Set a temporary mountpoint for the image.
	err = s.zfsPoolVolumeSet(fs, "mountpoint", tmpImageDir)
	if err != nil {
		return err
	}

	// Make sure that the image actually got mounted.
	if !shared.IsMountPoint(tmpImageDir) {
		s.zfsPoolVolumeMount(fs)
	}

	// Unpack the image into the temporary mountpoint.
	err = unpackImage(s.d, imagePath, tmpImageDir, storageTypeZfs)
	if err != nil {
		return err
	}

	// Mark the new storage volume for the image as readonly.
	err = s.zfsPoolVolumeSet(fs, "readonly", "on")
	if err != nil {
		return err
	}

	// Remove the temporary mountpoint from the image storage volume.
	err = s.zfsPoolVolumeSet(fs, "mountpoint", "none")
	if err != nil {
		return err
	}

	// Make sure that the image actually got unmounted.
	if shared.IsMountPoint(tmpImageDir) {
		s.zfsPoolVolumeUmount(fs)
	}

	// Create a snapshot of that image on the storage pool which we clone for
	// container creation.
	err = s.zfsPoolVolumeSnapshotCreate(fs, "readonly")
	if err != nil {
		return err
	}

	revert = false

	shared.LogDebugf("Created ZFS storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return nil
}

func (s *storageZfs) ImageDelete(fingerprint string) error {
	shared.LogDebugf("Deleting ZFS storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

	fs := fmt.Sprintf("images/%s", fingerprint)

	if s.zfsFilesystemEntityExists(fs, true) {
		removable, err := s.zfsPoolVolumeSnapshotRemovable(fs, "readonly")
		if err != nil {
			return err
		}

		if removable {
			err := s.zfsPoolVolumeDestroy(fs)
			if err != nil {
				return err
			}
		} else {
			err := s.zfsPoolVolumeSet(fs, "mountpoint", "none")
			if err != nil {
				return err
			}

			err = s.zfsPoolVolumeRename(fs, fmt.Sprintf("deleted/%s", fs))
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

	shared.LogDebugf("Deleted ZFS storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return nil
}

func (s *storageZfs) ImageMount(fingerprint string) (bool, error) {
	return true, nil
}

func (s *storageZfs) ImageUmount(fingerprint string) (bool, error) {
	return true, nil
}

// Helper functions
func (s *storageZfs) zfsPoolCheck(pool string) error {
	output, err := shared.RunCommand(
		"zfs", "get", "type", "-H", "-o", "value", pool)
	if err != nil {
		return fmt.Errorf(strings.Split(output, "\n")[0])
	}

	poolType := strings.Split(output, "\n")[0]
	if poolType != "filesystem" {
		return fmt.Errorf("Unsupported pool type: %s", poolType)
	}

	return nil
}

func (s *storageZfs) zfsPoolCreate() error {
	zpoolName := s.getOnDiskPoolName()
	vdev := s.pool.Config["source"]
	if vdev == "" {
		vdev = filepath.Join(shared.VarPath("disks"), fmt.Sprintf("%s.img", s.pool.Name))
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

		size, err := shared.ParseByteSizeString(s.pool.Config["size"])
		if err != nil {
			return err
		}
		err = f.Truncate(size)
		if err != nil {
			return fmt.Errorf("Failed to create sparse file %s: %s", vdev, err)
		}

		output, err := shared.RunCommand(
			"zpool",
			"create", zpoolName, vdev,
			"-f", "-m", "none", "-O", "compression=on")
		if err != nil {
			return fmt.Errorf("Failed to create the ZFS pool: %s", output)
		}
	} else {
		// Unset size property since it doesn't make sense.
		s.pool.Config["size"] = ""

		if filepath.IsAbs(vdev) {
			if !shared.IsBlockdevPath(vdev) {
				return fmt.Errorf("Custom loop file locations are not supported.")
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
			output, err := shared.RunCommand(
				"zpool",
				"create", zpoolName, vdev,
				"-f", "-m", "none", "-O", "compression=on")
			if err != nil {
				return fmt.Errorf("Failed to create the ZFS pool: %s", output)
			}
		} else {
			if s.pool.Config["zfs.pool_name"] != "" {
				return fmt.Errorf("Invalid combination of \"source\" and \"zfs.pool_name\" property.")
			}
			s.pool.Config["zfs.pool_name"] = vdev
			s.dataset = vdev

			if strings.Contains(vdev, "/") {
				ok := s.zfsFilesystemEntityExists(vdev, false)
				if !ok {
					output, err := shared.RunCommand(
						"zfs",
						"create",
						"-p",
						"-o",
						"mountpoint=none",
						vdev)
					if err != nil {
						shared.LogErrorf("zfs create failed: %s.", output)
						return fmt.Errorf("Failed to create ZFS filesystem: %s", output)
					}
				}
			} else {
				err := s.zfsPoolCheck(vdev)
				if err != nil {
					return err
				}

				subvols, err := s.zfsPoolListSubvolumes(vdev)
				if err != nil {
					return err
				}

				if len(subvols) > 0 {
					return fmt.Errorf("Provided ZFS pool (or dataset) isn't empty")
				}
			}
		}
	}

	// Create default dummy datasets to avoid zfs races during container
	// creation.
	err := s.zfsPoolVolumeCreate("containers")
	if err != nil {
		return err
	}

	err = s.zfsPoolVolumeSet("containers", "mountpoint", "none")
	if err != nil {
		return err
	}

	err = s.zfsPoolVolumeCreate("images")
	if err != nil {
		return err
	}

	err = s.zfsPoolVolumeSet("images", "mountpoint", "none")
	if err != nil {
		return err
	}

	err = s.zfsPoolVolumeCreate("custom")
	if err != nil {
		return err
	}

	err = s.zfsPoolVolumeSet("custom", "mountpoint", "none")
	if err != nil {
		return err
	}

	err = s.zfsPoolVolumeCreate("deleted")
	if err != nil {
		return err
	}

	err = s.zfsPoolVolumeSet("deleted", "mountpoint", "none")
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeClone(source string, name string, dest string, mountpoint string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.RunCommand(
		"zfs",
		"clone",
		"-p",
		"-o", fmt.Sprintf("mountpoint=%s", mountpoint),
		fmt.Sprintf("%s/%s@%s", poolName, source, name),
		fmt.Sprintf("%s/%s", poolName, dest))
	if err != nil {
		shared.LogErrorf("zfs clone failed: %s.", output)
		return fmt.Errorf("Failed to clone the filesystem: %s", output)
	}

	subvols, err := s.zfsPoolListSubvolumes(fmt.Sprintf("%s/%s", poolName, source))
	if err != nil {
		return err
	}

	for _, sub := range subvols {
		snaps, err := s.zfsPoolListSnapshots(sub)
		if err != nil {
			return err
		}

		if !shared.StringInSlice(name, snaps) {
			continue
		}

		destSubvol := dest + strings.TrimPrefix(sub, source)
		snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, destSubvol)

		output, err := shared.RunCommand(
			"zfs",
			"clone",
			"-p",
			"-o", fmt.Sprintf("mountpoint=%s", snapshotMntPoint),
			fmt.Sprintf("%s/%s@%s", poolName, sub, name),
			fmt.Sprintf("%s/%s", poolName, destSubvol))
		if err != nil {
			shared.LogErrorf("zfs clone failed: %s.", output)
			return fmt.Errorf("Failed to clone the sub-volume: %s", output)
		}
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeCreate(path string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.RunCommand(
		"zfs",
		"create",
		"-p",
		fmt.Sprintf("%s/%s", poolName, path))
	if err != nil {
		shared.LogErrorf("zfs create failed: %s.", output)
		return fmt.Errorf("Failed to create ZFS filesystem: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsFilesystemEntityDelete() error {
	var output string
	var err error
	poolName := s.getOnDiskPoolName()
	if strings.Contains(poolName, "/") {
		// Command to destroy a zfs dataset.
		output, err = shared.RunCommand("zfs", "destroy", "-r", poolName)
	} else {
		// Command to destroy a zfs pool.
		output, err = shared.RunCommand("zpool", "destroy", "-f", poolName)
	}
	if err != nil {
		return fmt.Errorf("Failed to delete the ZFS pool: %s", output)
	}

	// Cleanup storage
	vdev := s.pool.Config["source"]
	if filepath.IsAbs(vdev) && !shared.IsBlockdevPath(vdev) {
		os.RemoveAll(vdev)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeDestroy(path string) error {
	mountpoint, err := s.zfsFilesystemEntityPropertyGet(path, "mountpoint", true)
	if err != nil {
		return err
	}

	if mountpoint != "none" && shared.IsMountPoint(mountpoint) {
		err := syscall.Unmount(mountpoint, syscall.MNT_DETACH)
		if err != nil {
			shared.LogErrorf("umount failed: %s.", err)
			return err
		}
	}

	poolName := s.getOnDiskPoolName()
	// Due to open fds or kernel refs, this may fail for a bit, give it 10s
	output, err := shared.TryRunCommand(
		"zfs",
		"destroy",
		"-r",
		fmt.Sprintf("%s/%s", poolName, path))

	if err != nil {
		shared.LogErrorf("zfs destroy failed: %s.", output)
		return fmt.Errorf("Failed to destroy ZFS filesystem: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeCleanup(path string) error {
	if strings.HasPrefix(path, "deleted/") {
		// Cleanup of filesystems kept for refcount reason
		removablePath, err := s.zfsPoolVolumeSnapshotRemovable(path, "")
		if err != nil {
			return err
		}

		// Confirm that there are no more clones
		if removablePath {
			if strings.Contains(path, "@") {
				// Cleanup snapshots
				err = s.zfsPoolVolumeDestroy(path)
				if err != nil {
					return err
				}

				// Check if the parent can now be deleted
				subPath := strings.SplitN(path, "@", 2)[0]
				snaps, err := s.zfsPoolListSnapshots(subPath)
				if err != nil {
					return err
				}

				if len(snaps) == 0 {
					err := s.zfsPoolVolumeCleanup(subPath)
					if err != nil {
						return err
					}
				}
			} else {
				// Cleanup filesystems
				origin, err := s.zfsFilesystemEntityPropertyGet(path, "origin", true)
				if err != nil {
					return err
				}
				poolName := s.getOnDiskPoolName()
				origin = strings.TrimPrefix(origin, fmt.Sprintf("%s/", poolName))

				err = s.zfsPoolVolumeDestroy(path)
				if err != nil {
					return err
				}

				// Attempt to remove its parent
				if origin != "-" {
					err := s.zfsPoolVolumeCleanup(origin)
					if err != nil {
						return err
					}
				}
			}

			return nil
		}
	} else if strings.HasPrefix(path, "containers") && strings.Contains(path, "@copy-") {
		// Just remove the copy- snapshot for copies of active containers
		err := s.zfsPoolVolumeDestroy(path)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageZfs) zfsFilesystemEntityExists(path string, prefixPathWithPool bool) bool {
	output, _ := s.zfsFilesystemEntityPropertyGet(path, "name", prefixPathWithPool)

	// If prefixPathWithPool is false we assume that the path passed in
	// already is a valid zfs entity we want to check for.
	fsToCheck := path
	if prefixPathWithPool {
		fsToCheck = fmt.Sprintf("%s/%s", s.getOnDiskPoolName(), path)
	}
	if output == fsToCheck {
		return true
	}

	return false
}

func (s *storageZfs) zfsFilesystemEntityPropertyGet(path string, key string, prefixPathWithPool bool) (string, error) {
	// If prefixPathWithPool is false we assume that the path passed in
	// already is a valid zfs entity we want to check for.
	fsToCheck := path
	if prefixPathWithPool {
		fsToCheck = fmt.Sprintf("%s/%s", s.getOnDiskPoolName(), path)
	}

	output, err := shared.RunCommand(
		"zfs",
		"get",
		"-H",
		"-p",
		"-o", "value",
		key,
		fsToCheck)
	if err != nil {
		return "", fmt.Errorf("Failed to get ZFS config: %s", output)
	}

	return strings.TrimRight(output, "\n"), nil
}

func (s *storageZfs) zfsPoolVolumeRename(source string, dest string) error {
	var err error
	var output string

	poolName := s.getOnDiskPoolName()
	for i := 0; i < 20; i++ {
		output, err = shared.RunCommand(
			"zfs",
			"rename",
			"-p",
			fmt.Sprintf("%s/%s", poolName, source),
			fmt.Sprintf("%s/%s", poolName, dest))

		// Success
		if err == nil {
			return nil
		}

		// zfs rename can fail because of descendants, yet still manage the rename
		if !s.zfsFilesystemEntityExists(source, true) && s.zfsFilesystemEntityExists(dest, true) {
			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Timeout
	shared.LogErrorf("zfs rename failed: %s.", output)
	return fmt.Errorf("Failed to rename ZFS filesystem: %s", output)
}

func (s *storageZfs) zfsPoolVolumeSet(path string, key string, value string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.RunCommand(
		"zfs",
		"set",
		fmt.Sprintf("%s=%s", key, value),
		fmt.Sprintf("%s/%s", poolName, path))
	if err != nil {
		shared.LogErrorf("zfs set failed: %s.", output)
		return fmt.Errorf("Failed to set ZFS config: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeSnapshotCreate(path string, name string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.RunCommand(
		"zfs",
		"snapshot",
		"-r",
		fmt.Sprintf("%s/%s@%s", poolName, path, name))
	if err != nil {
		shared.LogErrorf("zfs snapshot failed: %s.", output)
		return fmt.Errorf("Failed to create ZFS snapshot: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeSnapshotDestroy(path string, name string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.RunCommand(
		"zfs",
		"destroy",
		"-r",
		fmt.Sprintf("%s/%s@%s", poolName, path, name))
	if err != nil {
		shared.LogErrorf("zfs destroy failed: %s.", output)
		return fmt.Errorf("Failed to destroy ZFS snapshot: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeSnapshotRestore(path string, name string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.TryRunCommand(
		"zfs",
		"rollback",
		fmt.Sprintf("%s/%s@%s", poolName, path, name))
	if err != nil {
		shared.LogErrorf("zfs rollback failed: %s.", output)
		return fmt.Errorf("Failed to restore ZFS snapshot: %s", output)
	}

	subvols, err := s.zfsPoolListSubvolumes(fmt.Sprintf("%s/%s", poolName, path))
	if err != nil {
		return err
	}

	for _, sub := range subvols {
		snaps, err := s.zfsPoolListSnapshots(sub)
		if err != nil {
			return err
		}

		if !shared.StringInSlice(name, snaps) {
			continue
		}

		output, err := shared.TryRunCommand(
			"zfs",
			"rollback",
			fmt.Sprintf("%s/%s@%s", poolName, sub, name))
		if err != nil {
			shared.LogErrorf("zfs rollback failed: %s.", output)
			return fmt.Errorf("Failed to restore ZFS sub-volume snapshot: %s", output)
		}
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeSnapshotRename(path string, oldName string, newName string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.RunCommand(
		"zfs",
		"rename",
		"-r",
		fmt.Sprintf("%s/%s@%s", poolName, path, oldName),
		fmt.Sprintf("%s/%s@%s", poolName, path, newName))
	if err != nil {
		shared.LogErrorf("zfs snapshot rename failed: %s.", output)
		return fmt.Errorf("Failed to rename ZFS snapshot: %s", output)
	}

	return nil
}

func zfsMount(poolName string, path string) error {
	output, err := shared.TryRunCommand(
		"zfs",
		"mount",
		fmt.Sprintf("%s/%s", poolName, path))
	if err != nil {
		return fmt.Errorf("Failed to mount ZFS filesystem: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeMount(path string) error {
	return zfsMount(s.getOnDiskPoolName(), path)
}

func zfsUmount(poolName string, path string) error {
	output, err := shared.TryRunCommand(
		"zfs",
		"unmount",
		fmt.Sprintf("%s/%s", poolName, path))
	if err != nil {
		return fmt.Errorf("Failed to unmount ZFS filesystem: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeUmount(path string) error {
	return zfsUmount(s.getOnDiskPoolName(), path)
}

func (s *storageZfs) zfsPoolListSubvolumes(path string) ([]string, error) {
	output, err := shared.RunCommand(
		"zfs",
		"list",
		"-t", "filesystem",
		"-o", "name",
		"-H",
		"-r", path)
	if err != nil {
		shared.LogErrorf("zfs list failed: %s.", output)
		return []string{}, fmt.Errorf("Failed to list ZFS filesystems: %s", output)
	}

	children := []string{}
	for _, entry := range strings.Split(output, "\n") {
		if entry == "" {
			continue
		}

		if entry == path {
			continue
		}

		poolName := s.getOnDiskPoolName()
		children = append(children, strings.TrimPrefix(entry, fmt.Sprintf("%s/", poolName)))
	}

	return children, nil
}

func (s *storageZfs) zfsPoolListSnapshots(path string) ([]string, error) {
	poolName := s.getOnDiskPoolName()
	path = strings.TrimRight(path, "/")
	fullPath := poolName
	if path != "" {
		fullPath = fmt.Sprintf("%s/%s", poolName, path)
	}

	output, err := shared.RunCommand(
		"zfs",
		"list",
		"-t", "snapshot",
		"-o", "name",
		"-H",
		"-d", "1",
		"-s", "creation",
		"-r", fullPath)
	if err != nil {
		shared.LogErrorf("zfs list failed: %s.", output)
		return []string{}, fmt.Errorf("Failed to list ZFS snapshots: %s", output)
	}

	children := []string{}
	for _, entry := range strings.Split(output, "\n") {
		if entry == "" {
			continue
		}

		if entry == fullPath {
			continue
		}

		children = append(children, strings.SplitN(entry, "@", 2)[1])
	}

	return children, nil
}

func (s *storageZfs) zfsPoolVolumeSnapshotRemovable(path string, name string) (bool, error) {
	var snap string
	if name == "" {
		snap = path
	} else {
		snap = fmt.Sprintf("%s@%s", path, name)
	}

	clones, err := s.zfsFilesystemEntityPropertyGet(snap, "clones", true)
	if err != nil {
		return false, err
	}

	if clones == "-" || clones == "" {
		return true, nil
	}

	return false, nil
}

func (s *storageZfs) zfsPoolGetUsers() ([]string, error) {
	poolName := s.getOnDiskPoolName()
	subvols, err := s.zfsPoolListSubvolumes(poolName)
	if err != nil {
		return []string{}, err
	}

	exceptions := []string{
		"containers",
		"images",
		"snapshots",
		"deleted",
		"deleted/containers",
		"deleted/images"}

	users := []string{}
	for _, subvol := range subvols {
		path := strings.Split(subvol, "/")

		// Only care about plausible LXD paths
		if !shared.StringInSlice(path[0], exceptions) {
			continue
		}

		// Ignore empty paths
		if shared.StringInSlice(subvol, exceptions) {
			continue
		}

		users = append(users, subvol)
	}

	return users, nil
}

func zfsFilesystemEntityExists(zfsEntity string) bool {
	output, err := shared.RunCommand(
		"zfs",
		"get",
		"type",
		"-H",
		"-o",
		"name",
		zfsEntity)
	if err != nil {
		return false
	}

	detectedName := strings.TrimSpace(output)
	if detectedName != zfsEntity {
		return false
	}

	return true
}

type zfsMigrationSourceDriver struct {
	container        container
	snapshots        []container
	zfsSnapshotNames []string
	zfs              *storageZfs
	runningSnapName  string
	stoppedSnapName  string
}

func (s *zfsMigrationSourceDriver) Snapshots() []container {
	return s.snapshots
}

func (s *zfsMigrationSourceDriver) send(conn *websocket.Conn, zfsName string, zfsParent string, readWrapper func(io.ReadCloser) io.ReadCloser) error {
	fields := strings.SplitN(s.container.Name(), shared.SnapshotDelimiter, 2)
	poolName := s.zfs.getOnDiskPoolName()
	args := []string{"send", fmt.Sprintf("%s/containers/%s@%s", poolName, fields[0], zfsName)}
	if zfsParent != "" {
		args = append(args, "-i", fmt.Sprintf("%s/containers/%s@%s", poolName, s.container.Name(), zfsParent))
	}

	cmd := exec.Command("zfs", args...)

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

	if err := cmd.Start(); err != nil {
		return err
	}

	<-shared.WebsocketSendStream(conn, readPipe, 4*1024*1024)

	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		shared.LogErrorf("Problem reading zfs send stderr: %s.", err)
	}

	err = cmd.Wait()
	if err != nil {
		shared.LogErrorf("Problem with zfs send: %s.", string(output))
	}

	return err
}

func (s *zfsMigrationSourceDriver) SendWhileRunning(conn *websocket.Conn, op *operation) error {
	if s.container.IsSnapshot() {
		fields := strings.SplitN(s.container.Name(), shared.SnapshotDelimiter, 2)
		snapshotName := fmt.Sprintf("snapshot-%s", fields[1])
		wrapper := StorageProgressReader(op, "fs_progress", s.container.Name())
		return s.send(conn, snapshotName, "", wrapper)
	}

	lastSnap := ""

	for i, snap := range s.zfsSnapshotNames {
		prev := ""
		if i > 0 {
			prev = s.zfsSnapshotNames[i-1]
		}

		lastSnap = snap

		wrapper := StorageProgressReader(op, "fs_progress", snap)
		if err := s.send(conn, snap, prev, wrapper); err != nil {
			return err
		}
	}

	s.runningSnapName = fmt.Sprintf("migration-send-%s", uuid.NewRandom().String())
	if err := s.zfs.zfsPoolVolumeSnapshotCreate(fmt.Sprintf("containers/%s", s.container.Name()), s.runningSnapName); err != nil {
		return err
	}

	wrapper := StorageProgressReader(op, "fs_progress", s.container.Name())
	if err := s.send(conn, s.runningSnapName, lastSnap, wrapper); err != nil {
		return err
	}

	return nil
}

func (s *zfsMigrationSourceDriver) SendAfterCheckpoint(conn *websocket.Conn) error {
	s.stoppedSnapName = fmt.Sprintf("migration-send-%s", uuid.NewRandom().String())
	if err := s.zfs.zfsPoolVolumeSnapshotCreate(fmt.Sprintf("containers/%s", s.container.Name()), s.stoppedSnapName); err != nil {
		return err
	}

	if err := s.send(conn, s.stoppedSnapName, s.runningSnapName, nil); err != nil {
		return err
	}

	return nil
}

func (s *zfsMigrationSourceDriver) Cleanup() {
	if s.stoppedSnapName != "" {
		s.zfs.zfsPoolVolumeSnapshotDestroy(fmt.Sprintf("containers/%s", s.container.Name()), s.stoppedSnapName)
	}

	if s.runningSnapName != "" {
		s.zfs.zfsPoolVolumeSnapshotDestroy(fmt.Sprintf("containers/%s", s.container.Name()), s.runningSnapName)
	}
}

func (s *storageZfs) MigrationType() MigrationFSType {
	return MigrationFSType_ZFS
}

func (s *storageZfs) PreservesInodes() bool {
	return true
}

func (s *storageZfs) MigrationSource(ct container) (MigrationStorageSourceDriver, error) {
	/* If the container is a snapshot, let's just send that; we don't need
	 * to send anything else, because that's all the user asked for.
	 */
	if ct.IsSnapshot() {
		return &zfsMigrationSourceDriver{container: ct, zfs: s}, nil
	}

	driver := zfsMigrationSourceDriver{
		container:        ct,
		snapshots:        []container{},
		zfsSnapshotNames: []string{},
		zfs:              s,
	}

	/* List all the snapshots in order of reverse creation. The idea here
	 * is that we send the oldest to newest snapshot, hopefully saving on
	 * xfer costs. Then, after all that, we send the container itself.
	 */
	snapshots, err := s.zfsPoolListSnapshots(fmt.Sprintf("containers/%s", ct.Name()))
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

		lxdName := fmt.Sprintf("%s%s%s", ct.Name(), shared.SnapshotDelimiter, snap[len("snapshot-"):])
		snapshot, err := containerLoadByName(s.d, lxdName)
		if err != nil {
			return nil, err
		}

		driver.snapshots = append(driver.snapshots, snapshot)
		driver.zfsSnapshotNames = append(driver.zfsSnapshotNames, snap)
	}

	return &driver, nil
}

func (s *storageZfs) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation) error {
	poolName := s.getOnDiskPoolName()
	zfsRecv := func(zfsName string, writeWrapper func(io.WriteCloser) io.WriteCloser) error {
		zfsFsName := fmt.Sprintf("%s/%s", poolName, zfsName)
		args := []string{"receive", "-F", "-u", zfsFsName}
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
			shared.LogDebugf("problem reading zfs recv stderr %s.", err)
		}

		err = cmd.Wait()
		if err != nil {
			shared.LogErrorf("problem with zfs recv: %s.", string(output))
		}
		return err
	}

	/* In some versions of zfs we can write `zfs recv -F` to mounted
	 * filesystems, and in some versions we can't. So, let's always unmount
	 * this fs (it's empty anyway) before we zfs recv. N.B. that `zfs recv`
	 * of a snapshot also needs tha actual fs that it has snapshotted
	 * unmounted, so we do this before receiving anything.
	 */
	zfsName := fmt.Sprintf("containers/%s", container.Name())
	err := s.zfsPoolVolumeUmount(zfsName)
	if err != nil {
		return err
	}

	if len(snapshots) > 0 {
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "snapshots", s.volume.Name)
		snapshotMntPointSymlink := shared.VarPath("snapshots", container.Name())
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
	parentLocalRootDiskDeviceKey, parentLocalRootDiskDevice, _ := containerGetRootDiskDevice(parentExpandedDevices)
	if parentLocalRootDiskDeviceKey != "" {
		parentStoragePool = parentLocalRootDiskDevice["pool"]
	}

	// A little neuroticism.
	if parentStoragePool == "" {
		return fmt.Errorf("Detected that the container's root device is missing the pool property during BTRFS migration.")
	}

	for _, snap := range snapshots {
		args := snapshotProtobufToContainerArgs(container.Name(), snap)

		// Ensure that snapshot and parent container have the
		// same storage pool in their local root disk device.
		// If the root disk device for the snapshot comes from a
		// profile on the new instance as well we don't need to
		// do anything.
		if args.Devices != nil {
			snapLocalRootDiskDeviceKey, _, _ := containerGetRootDiskDevice(args.Devices)
			if snapLocalRootDiskDeviceKey != "" {
				args.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
			}
		}
		_, err := containerCreateEmptySnapshot(container.Daemon(), args)
		if err != nil {
			return err
		}

		wrapper := StorageProgressWriter(op, "fs_progress", snap.GetName())
		name := fmt.Sprintf("containers/%s@snapshot-%s", container.Name(), snap.GetName())
		if err := zfsRecv(name, wrapper); err != nil {
			return err
		}

		snapshotMntPoint := getSnapshotMountPoint(poolName, fmt.Sprintf("%s/%s", container.Name(), *snap.Name))
		if !shared.PathExists(snapshotMntPoint) {
			err := os.MkdirAll(snapshotMntPoint, 0700)
			if err != nil {
				return err
			}
		}
	}

	defer func() {
		/* clean up our migration-send snapshots that we got from recv. */
		zfsSnapshots, err := s.zfsPoolListSnapshots(fmt.Sprintf("containers/%s", container.Name()))
		if err != nil {
			shared.LogErrorf("failed listing snapshots post migration: %s.", err)
			return
		}

		for _, snap := range zfsSnapshots {
			// If we received a bunch of snapshots, remove the migration-send-* ones, if not, wipe any snapshot we got
			if snapshots != nil && len(snapshots) > 0 && !strings.HasPrefix(snap, "migration-send") {
				continue
			}

			s.zfsPoolVolumeSnapshotDestroy(fmt.Sprintf("containers/%s", container.Name()), snap)
		}
	}()

	/* finally, do the real container */
	wrapper := StorageProgressWriter(op, "fs_progress", container.Name())
	if err := zfsRecv(zfsName, wrapper); err != nil {
		return err
	}

	if live {
		/* and again for the post-running snapshot if this was a live migration */
		wrapper := StorageProgressWriter(op, "fs_progress", container.Name())
		if err := zfsRecv(zfsName, wrapper); err != nil {
			return err
		}
	}

	/* Sometimes, zfs recv mounts this anyway, even if we pass -u
	 * (https://forums.freebsd.org/threads/zfs-receive-u-shouldnt-mount-received-filesystem-right.36844/)
	 * but sometimes it doesn't. Let's try to mount, but not complain about
	 * failure.
	 */
	s.zfsPoolVolumeMount(zfsName)
	return nil
}
