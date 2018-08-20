package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
)

type storageDir struct {
	storageShared
}

// Only initialize the minimal information we need about a given storage type.
func (s *storageDir) StorageCoreInit() error {
	s.sType = storageTypeDir
	typeName, err := storageTypeToString(s.sType)
	if err != nil {
		return err
	}
	s.sTypeName = typeName
	s.sTypeVersion = "1"

	return nil
}

// Initialize a full storage interface.
func (s *storageDir) StoragePoolInit() error {
	err := s.StorageCoreInit()
	if err != nil {
		return err
	}

	return nil
}

// Initialize a full storage interface.
func (s *storageDir) StoragePoolCheck() error {
	logger.Debugf("Checking DIR storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolCreate() error {
	logger.Infof("Creating DIR storage pool \"%s\"", s.pool.Name)

	s.pool.Config["volatile.initial_source"] = s.pool.Config["source"]

	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)

	source := shared.HostPath(s.pool.Config["source"])
	if source == "" {
		source = filepath.Join(shared.VarPath("storage-pools"), s.pool.Name)
		s.pool.Config["source"] = source
	} else {
		cleanSource := filepath.Clean(source)
		lxdDir := shared.VarPath()
		if strings.HasPrefix(cleanSource, lxdDir) &&
			cleanSource != poolMntPoint {
			return fmt.Errorf(`DIR storage pool requests in LXD `+
				`directory "%s" are only valid under `+
				`"%s"\n(e.g. source=%s)`, shared.VarPath(),
				shared.VarPath("storage-pools"), poolMntPoint)
		}
		source = filepath.Clean(source)
	}

	revert := true
	if !shared.PathExists(source) {
		err := os.MkdirAll(source, 0711)
		if err != nil {
			return err
		}
		defer func() {
			if !revert {
				return
			}
			os.Remove(source)
		}()
	} else {
		empty, err := shared.PathIsEmpty(source)
		if err != nil {
			return err
		}

		if !empty {
			return fmt.Errorf("The provided directory is not empty")
		}
	}

	if !shared.PathExists(poolMntPoint) {
		err := os.MkdirAll(poolMntPoint, 0711)
		if err != nil {
			return err
		}
		defer func() {
			if !revert {
				return
			}
			os.Remove(poolMntPoint)
		}()
	}

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	_, err = s.StoragePoolMount()
	if err != nil {
		return err
	}

	revert = false

	logger.Infof("Created DIR storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolDelete() error {
	logger.Infof("Deleting DIR storage pool \"%s\"", s.pool.Name)

	source := shared.HostPath(s.pool.Config["source"])
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	_, err := s.StoragePoolUmount()
	if err != nil {
		return err
	}

	if shared.PathExists(source) {
		err := os.RemoveAll(source)
		if err != nil {
			return err
		}
	}

	prefix := shared.VarPath("storage-pools")
	if !strings.HasPrefix(source, prefix) {
		storagePoolSymlink := getStoragePoolMountPoint(s.pool.Name)
		if !shared.PathExists(storagePoolSymlink) {
			return nil
		}

		err := os.Remove(storagePoolSymlink)
		if err != nil {
			return err
		}
	}

	logger.Infof("Deleted DIR storage pool \"%s\"", s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolMount() (bool, error) {
	source := shared.HostPath(s.pool.Config["source"])
	if source == "" {
		return false, fmt.Errorf("no \"source\" property found for the storage pool")
	}
	cleanSource := filepath.Clean(source)
	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	if cleanSource == poolMntPoint {
		return true, nil
	}

	logger.Debugf("Mounting DIR storage pool \"%s\"", s.pool.Name)

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

	mountSource := cleanSource
	mountFlags := syscall.MS_BIND

	if shared.IsMountPoint(poolMntPoint) {
		return false, nil
	}

	err := syscall.Mount(mountSource, poolMntPoint, "", uintptr(mountFlags), "")
	if err != nil {
		logger.Errorf(`Failed to mount DIR storage pool "%s" onto "%s": %s`, mountSource, poolMntPoint, err)
		return false, err
	}

	logger.Debugf("Mounted DIR storage pool \"%s\"", s.pool.Name)

	return true, nil
}

func (s *storageDir) StoragePoolUmount() (bool, error) {
	source := s.pool.Config["source"]
	if source == "" {
		return false, fmt.Errorf("no \"source\" property found for the storage pool")
	}
	cleanSource := filepath.Clean(source)
	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	if cleanSource == poolMntPoint {
		return true, nil
	}

	logger.Debugf("Unmounting DIR storage pool \"%s\"", s.pool.Name)

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

	if !shared.IsMountPoint(poolMntPoint) {
		return false, nil
	}

	err := syscall.Unmount(poolMntPoint, 0)
	if err != nil {
		return false, err
	}

	logger.Debugf("Unmounted DIR pool \"%s\"", s.pool.Name)
	return true, nil
}

func (s *storageDir) GetStoragePoolWritable() api.StoragePoolPut {
	return s.pool.Writable()
}

func (s *storageDir) GetStoragePoolVolumeWritable() api.StorageVolumePut {
	return s.volume.Writable()
}

func (s *storageDir) SetStoragePoolWritable(writable *api.StoragePoolPut) {
	s.pool.StoragePoolPut = *writable
}

func (s *storageDir) SetStoragePoolVolumeWritable(writable *api.StorageVolumePut) {
	s.volume.StorageVolumePut = *writable
}

func (s *storageDir) GetContainerPoolInfo() (int64, string, string) {
	return s.poolID, s.pool.Name, s.pool.Name
}

func (s *storageDir) StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error {
	logger.Infof(`Updating DIR storage pool "%s"`, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	changeable := changeableStoragePoolProperties["dir"]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		return updateStoragePoolError(unchangeable, "dir")
	}

	// "rsync.bwlimit" requires no on-disk modifications.

	logger.Infof(`Updated DIR storage pool "%s"`, s.pool.Name)
	return nil
}

// Functions dealing with storage pools.
func (s *storageDir) StoragePoolVolumeCreate() error {
	logger.Infof("Creating DIR storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	storageVolumePath := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	err = os.MkdirAll(storageVolumePath, 0711)
	if err != nil {
		return err
	}

	logger.Infof("Created DIR storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolVolumeDelete() error {
	logger.Infof("Deleting DIR storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	storageVolumePath := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	if !shared.PathExists(storageVolumePath) {
		return nil
	}

	err := os.RemoveAll(storageVolumePath)
	if err != nil {
		return err
	}

	err = s.s.Cluster.StoragePoolVolumeDelete(
		s.volume.Name,
		storagePoolVolumeTypeCustom,
		s.poolID)
	if err != nil {
		logger.Errorf(`Failed to delete database entry for DIR storage volume "%s" on storage pool "%s"`,
			s.volume.Name, s.pool.Name)
	}

	logger.Infof("Deleted DIR storage volume \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolVolumeMount() (bool, error) {
	return true, nil
}

func (s *storageDir) StoragePoolVolumeUmount() (bool, error) {
	return true, nil
}

func (s *storageDir) StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error {
	logger.Infof(`Updating DIR storage volume "%s"`, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	changeable := changeableStoragePoolVolumeProperties["dir"]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		return updateStoragePoolVolumeError(unchangeable, "dir")
	}

	logger.Infof(`Updated DIR storage volume "%s"`, s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolVolumeRename(newName string) error {
	logger.Infof(`Renaming DIR storage volume on storage pool "%s" from "%s" to "%s`,
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
		return fmt.Errorf(`DIR storage volume "%s" on storage pool "%s" is attached to containers`,
			s.volume.Name, s.pool.Name)
	}

	oldPath := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	newPath := getStoragePoolVolumeMountPoint(s.pool.Name, newName)
	err = os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	logger.Infof(`Renamed DIR storage volume on storage pool "%s" from "%s" to "%s`,
		s.pool.Name, s.volume.Name, newName)

	return s.s.Cluster.StoragePoolVolumeRename(s.volume.Name, newName,
		storagePoolVolumeTypeCustom, s.poolID)
}

func (s *storageDir) ContainerStorageReady(name string) bool {
	containerMntPoint := getContainerMountPoint(s.pool.Name, name)
	ok, _ := shared.PathIsEmpty(containerMntPoint)
	return !ok
}

func (s *storageDir) ContainerCreate(container container) error {
	logger.Debugf("Creating empty DIR storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	containerMntPoint := getContainerMountPoint(s.pool.Name, container.Name())
	err = createContainerMountpoint(containerMntPoint, container.Path(), container.IsPrivileged())
	if err != nil {
		return err
	}
	revert := true
	defer func() {
		if !revert {
			return
		}
		deleteContainerMountpoint(containerMntPoint, container.Path(), s.GetStorageTypeName())
	}()

	err = container.TemplateApply("create")
	if err != nil {
		return err
	}

	revert = false

	logger.Debugf("Created empty DIR storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) ContainerCreateFromImage(container container, imageFingerprint string) error {
	logger.Debugf("Creating DIR storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	privileged := container.IsPrivileged()
	containerName := container.Name()
	containerMntPoint := getContainerMountPoint(s.pool.Name, containerName)
	err = createContainerMountpoint(containerMntPoint, container.Path(), privileged)
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

	imagePath := shared.VarPath("images", imageFingerprint)
	err = unpackImage(imagePath, containerMntPoint, storageTypeDir, s.s.OS.RunningInUserNS)
	if err != nil {
		return err
	}

	if !privileged {
		err := s.shiftRootfs(container, nil)
		if err != nil {
			return err
		}
	}

	err = container.TemplateApply("create")
	if err != nil {
		return err
	}

	revert = false

	logger.Debugf("Created DIR storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageDir) ContainerDelete(container container) error {
	logger.Debugf("Deleting DIR storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Delete the container on its storage pool:
	// ${POOL}/containers/<container_name>
	containerName := container.Name()
	containerMntPoint := getContainerMountPoint(s.pool.Name, containerName)
	if shared.PathExists(containerMntPoint) {
		err := os.RemoveAll(containerMntPoint)
		if err != nil {
			// RemovaAll fails on very long paths, so attempt an rm -Rf
			output, err := shared.RunCommand("rm", "-Rf", containerMntPoint)
			if err != nil {
				return fmt.Errorf("error removing %s: %s", containerMntPoint, output)
			}
		}
	}

	err = deleteContainerMountpoint(containerMntPoint, container.Path(), s.GetStorageTypeName())
	if err != nil {
		return err
	}

	// Delete potential leftover snapshot mountpoints.
	snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, container.Name())
	if shared.PathExists(snapshotMntPoint) {
		err := os.RemoveAll(snapshotMntPoint)
		if err != nil {
			return err
		}
	}

	// Delete potential leftover snapshot symlinks:
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

	logger.Debugf("Deleted DIR storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) copyContainer(target container, source container) error {
	_, sourcePool, _ := source.Storage().GetContainerPoolInfo()
	_, targetPool, _ := target.Storage().GetContainerPoolInfo()
	sourceContainerMntPoint := getContainerMountPoint(sourcePool, source.Name())
	if source.IsSnapshot() {
		sourceContainerMntPoint = getSnapshotMountPoint(sourcePool, source.Name())
	}
	targetContainerMntPoint := getContainerMountPoint(targetPool, target.Name())

	err := createContainerMountpoint(targetContainerMntPoint, target.Path(), target.IsPrivileged())
	if err != nil {
		return err
	}

	bwlimit := s.pool.Config["rsync.bwlimit"]
	output, err := rsyncLocalCopy(sourceContainerMntPoint, targetContainerMntPoint, bwlimit)
	if err != nil {
		return fmt.Errorf("failed to rsync container: %s: %s", string(output), err)
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

func (s *storageDir) copySnapshot(target container, targetPool string, source container, sourcePool string) error {
	sourceName := source.Name()
	targetName := target.Name()
	sourceContainerMntPoint := getSnapshotMountPoint(sourcePool, sourceName)
	targetContainerMntPoint := getSnapshotMountPoint(targetPool, targetName)

	targetParentName, _, _ := containerGetParentAndSnapshotName(target.Name())
	containersPath := getSnapshotMountPoint(targetPool, targetParentName)
	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", targetPool, "containers-snapshots", targetParentName)
	snapshotMntPointSymlink := shared.VarPath("snapshots", targetParentName)
	err := createSnapshotMountpoint(containersPath, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	if err != nil {
		return err
	}

	bwlimit := s.pool.Config["rsync.bwlimit"]
	output, err := rsyncLocalCopy(sourceContainerMntPoint, targetContainerMntPoint, bwlimit)
	if err != nil {
		return fmt.Errorf("failed to rsync container: %s: %s", string(output), err)
	}

	return nil
}

func (s *storageDir) ContainerCopy(target container, source container, containerOnly bool) error {
	logger.Debugf("Copying DIR container storage %s to %s", source.Name(), target.Name())

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
		srcState = srcStorage.GetState()
	}

	err = s.copyContainer(target, source)
	if err != nil {
		return err
	}

	if containerOnly {
		logger.Debugf("Copied DIR container storage %s to %s", source.Name(), target.Name())
		return nil
	}

	snapshots, err := source.Snapshots()
	if err != nil {
		return err
	}

	if len(snapshots) == 0 {
		logger.Debugf("Copied DIR container storage %s to %s", source.Name(), target.Name())
		return nil
	}

	for _, snap := range snapshots {
		sourceSnapshot, err := containerLoadByName(srcState, snap.Name())
		if err != nil {
			return err
		}

		_, snapOnlyName, _ := containerGetParentAndSnapshotName(snap.Name())
		newSnapName := fmt.Sprintf("%s/%s", target.Name(), snapOnlyName)
		targetSnapshot, err := containerLoadByName(s.s, newSnapName)
		if err != nil {
			return err
		}

		err = s.copySnapshot(targetSnapshot, targetPool, sourceSnapshot, sourcePool)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Copied DIR container storage %s to %s", source.Name(), target.Name())
	return nil
}

func (s *storageDir) ContainerMount(c container) (bool, error) {
	return s.StoragePoolMount()
}

func (s *storageDir) ContainerUmount(name string, path string) (bool, error) {
	return true, nil
}

func (s *storageDir) ContainerRename(container container, newName string) error {
	logger.Debugf("Renaming DIR storage volume for container \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newName)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	oldContainerMntPoint := getContainerMountPoint(s.pool.Name, container.Name())
	oldContainerSymlink := shared.VarPath("containers", container.Name())
	newContainerMntPoint := getContainerMountPoint(s.pool.Name, newName)
	newContainerSymlink := shared.VarPath("containers", newName)
	err = renameContainerMountpoint(oldContainerMntPoint, oldContainerSymlink, newContainerMntPoint, newContainerSymlink)
	if err != nil {
		return err
	}

	// Rename the snapshot mountpoint for the container if existing:
	// ${POOL}/snapshots/<old_container_name> to ${POOL}/snapshots/<new_container_name>
	oldSnapshotsMntPoint := getSnapshotMountPoint(s.pool.Name, container.Name())
	newSnapshotsMntPoint := getSnapshotMountPoint(s.pool.Name, newName)
	if shared.PathExists(oldSnapshotsMntPoint) {
		err = os.Rename(oldSnapshotsMntPoint, newSnapshotsMntPoint)
		if err != nil {
			return err
		}
	}

	// Remove the old snapshot symlink:
	// ${LXD_DIR}/snapshots/<old_container_name>
	oldSnapshotSymlink := shared.VarPath("snapshots", container.Name())
	newSnapshotSymlink := shared.VarPath("snapshots", newName)
	if shared.PathExists(oldSnapshotSymlink) {
		err := os.Remove(oldSnapshotSymlink)
		if err != nil {
			return err
		}

		// Create the new snapshot symlink:
		// ${LXD_DIR}/snapshots/<new_container_name> to ${POOL}/snapshots/<new_container_name>
		err = os.Symlink(newSnapshotsMntPoint, newSnapshotSymlink)
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

	logger.Debugf("Renamed DIR storage volume for container \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newName)
	return nil
}

func (s *storageDir) ContainerRestore(container container, sourceContainer container) error {
	logger.Debugf("Restoring DIR storage volume for container \"%s\" from %s to %s", s.volume.Name, sourceContainer.Name(), container.Name())

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	targetPath := container.Path()
	sourcePath := sourceContainer.Path()

	// Restore using rsync
	bwlimit := s.pool.Config["rsync.bwlimit"]
	output, err := rsyncLocalCopy(sourcePath, targetPath, bwlimit)
	if err != nil {
		return fmt.Errorf("failed to rsync container: %s: %s", string(output), err)
	}

	// Now allow unprivileged users to access its data.
	if err := s.setUnprivUserACL(sourceContainer, targetPath); err != nil {
		return err
	}

	logger.Debugf("Restored DIR storage volume for container \"%s\" from %s to %s", s.volume.Name, sourceContainer.Name(), container.Name())
	return nil
}

func (s *storageDir) ContainerGetUsage(container container) (int64, error) {
	return -1, fmt.Errorf("the directory container backend doesn't support quotas")
}

func (s *storageDir) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	logger.Debugf("Creating DIR storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Create the path for the snapshot.
	targetContainerName := snapshotContainer.Name()
	targetContainerMntPoint := getSnapshotMountPoint(s.pool.Name, targetContainerName)
	err = os.MkdirAll(targetContainerMntPoint, 0711)
	if err != nil {
		return err
	}

	rsync := func(snapshotContainer container, oldPath string, newPath string, bwlimit string) error {
		output, err := rsyncLocalCopy(oldPath, newPath, bwlimit)
		if err != nil {
			s.ContainerDelete(snapshotContainer)
			return fmt.Errorf("failed to rsync: %s: %s", string(output), err)
		}
		return nil
	}

	ourStart, err := sourceContainer.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer sourceContainer.StorageStop()
	}

	_, sourcePool, _ := sourceContainer.Storage().GetContainerPoolInfo()
	sourceContainerName := sourceContainer.Name()
	sourceContainerMntPoint := getContainerMountPoint(sourcePool, sourceContainerName)
	bwlimit := s.pool.Config["rsync.bwlimit"]
	err = rsync(snapshotContainer, sourceContainerMntPoint, targetContainerMntPoint, bwlimit)
	if err != nil {
		return err
	}

	if sourceContainer.IsRunning() {
		// This is done to ensure consistency when snapshotting. But we
		// probably shouldn't fail just because of that.
		logger.Debugf("Trying to freeze and rsync again to ensure consistency")

		err := sourceContainer.Freeze()
		if err != nil {
			logger.Errorf("Trying to freeze and rsync again failed")
			goto onSuccess
		}
		defer sourceContainer.Unfreeze()

		err = rsync(snapshotContainer, sourceContainerMntPoint, targetContainerMntPoint, bwlimit)
		if err != nil {
			return err
		}
	}

onSuccess:
	// Check if the symlink
	// ${LXD_DIR}/snapshots/<source_container_name> to ${POOL_PATH}/snapshots/<source_container_name>
	// exists and if not create it.
	sourceContainerSymlink := shared.VarPath("snapshots", sourceContainerName)
	sourceContainerSymlinkTarget := getSnapshotMountPoint(sourcePool, sourceContainerName)
	if !shared.PathExists(sourceContainerSymlink) {
		err = os.Symlink(sourceContainerSymlinkTarget, sourceContainerSymlink)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Created DIR storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	logger.Debugf("Creating empty DIR storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Create the path for the snapshot.
	targetContainerName := snapshotContainer.Name()
	targetContainerMntPoint := getSnapshotMountPoint(s.pool.Name, targetContainerName)
	err = os.MkdirAll(targetContainerMntPoint, 0711)
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

	// Check if the symlink
	// ${LXD_DIR}/snapshots/<source_container_name> to ${POOL_PATH}/snapshots/<source_container_name>
	// exists and if not create it.
	targetContainerMntPoint = getSnapshotMountPoint(s.pool.Name,
		targetContainerName)
	sourceName, _, _ := containerGetParentAndSnapshotName(targetContainerName)
	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", sourceName)
	snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
	err = createSnapshotMountpoint(targetContainerMntPoint,
		snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	if err != nil {
		return err
	}

	revert = false

	logger.Debugf("Created empty DIR storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func dirSnapshotDeleteInternal(poolName string, snapshotName string) error {
	snapshotContainerMntPoint := getSnapshotMountPoint(poolName, snapshotName)
	if shared.PathExists(snapshotContainerMntPoint) {
		err := os.RemoveAll(snapshotContainerMntPoint)
		if err != nil {
			return err
		}
	}

	sourceContainerName, _, _ := containerGetParentAndSnapshotName(snapshotName)
	snapshotContainerPath := getSnapshotMountPoint(poolName, sourceContainerName)
	empty, _ := shared.PathIsEmpty(snapshotContainerPath)
	if empty == true {
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

	return nil
}

func (s *storageDir) ContainerSnapshotDelete(snapshotContainer container) error {
	logger.Debugf("Deleting DIR storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	snapshotContainerName := snapshotContainer.Name()
	err = dirSnapshotDeleteInternal(s.pool.Name, snapshotContainerName)
	if err != nil {
		return err
	}

	logger.Debugf("Deleted DIR storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) ContainerSnapshotRename(snapshotContainer container, newName string) error {
	logger.Debugf("Renaming DIR storage volume for snapshot \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newName)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Rename the mountpoint for the snapshot:
	// ${POOL}/snapshots/<old_snapshot_name> to ${POOL}/snapshots/<new_snapshot_name>
	oldSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, snapshotContainer.Name())
	newSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, newName)
	err = os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
	if err != nil {
		return err
	}

	logger.Debugf("Renamed DIR storage volume for snapshot \"%s\" from %s to %s", s.volume.Name, s.volume.Name, newName)
	return nil
}

func (s *storageDir) ContainerSnapshotStart(container container) (bool, error) {
	return s.StoragePoolMount()
}

func (s *storageDir) ContainerSnapshotStop(container container) (bool, error) {
	return true, nil
}

func (s *storageDir) ContainerBackupCreate(backup backup, sourceContainer container) error {
	logger.Debugf("Creating DIR storage volume for backup \"%s\" on storage pool \"%s\"",
		backup.Name(), s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Create the path for the backup.
	baseMntPoint := getBackupMountPoint(s.pool.Name, backup.Name())
	targetBackupContainerMntPoint := fmt.Sprintf("%s/container", baseMntPoint)
	targetBackupSnapshotsMntPoint := fmt.Sprintf("%s/snapshots", baseMntPoint)

	err = os.MkdirAll(targetBackupContainerMntPoint, 0711)
	if err != nil {
		return err
	}

	if !backup.ContainerOnly() {
		// Create path for snapshots as well.
		err = os.MkdirAll(targetBackupSnapshotsMntPoint, 0711)
		if err != nil {
			return err
		}
	}

	rsync := func(oldPath string, newPath string, bwlimit string) error {
		output, err := rsyncLocalCopy(oldPath, newPath, bwlimit)
		if err != nil {
			s.ContainerBackupDelete(backup.Name())
			return fmt.Errorf("failed to rsync: %s: %s", string(output), err)
		}
		return nil
	}

	ourStart, err := sourceContainer.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer sourceContainer.StorageStop()
	}

	_, sourcePool, _ := sourceContainer.Storage().GetContainerPoolInfo()
	sourceContainerMntPoint := getContainerMountPoint(sourcePool,
		sourceContainer.Name())
	bwlimit := s.pool.Config["rsync.bwlimit"]
	err = rsync(sourceContainerMntPoint, targetBackupContainerMntPoint, bwlimit)
	if err != nil {
		return err
	}

	if sourceContainer.IsRunning() {
		// This is done to ensure consistency when snapshotting. But we
		// probably shouldn't fail just because of that.
		logger.Debugf("Trying to freeze and rsync again to ensure consistency")

		err := sourceContainer.Freeze()
		if err != nil {
			logger.Errorf("Trying to freeze and rsync again failed")
		}
		defer sourceContainer.Unfreeze()

		err = rsync(sourceContainerMntPoint, targetBackupContainerMntPoint, bwlimit)
		if err != nil {
			return err
		}
	}

	if !backup.ContainerOnly() {
		// Backup snapshots as well.
		snaps, err := sourceContainer.Snapshots()
		if err != nil {
			return nil
		}

		for _, ct := range snaps {
			snapshotMntPoint := getSnapshotMountPoint(sourcePool, ct.Name())
			_, snapName, _ := containerGetParentAndSnapshotName(ct.Name())
			target := fmt.Sprintf("%s/%s", targetBackupSnapshotsMntPoint, snapName)

			err = rsync(snapshotMntPoint, target, bwlimit)
			if err != nil {
				return err
			}
		}
	}

	logger.Debugf("Created DIR storage volume for backup \"%s\" on storage pool \"%s\"",
		backup.Name(), s.pool.Name)
	return nil
}

func (s *storageDir) ContainerBackupDelete(name string) error {
	logger.Debugf("Deleting DIR storage volume for backup \"%s\" on storage pool \"%s\"",
		name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	err = dirBackupDeleteInternal(s.pool.Name, name)
	if err != nil {
		return err
	}

	logger.Debugf("Deleted DIR storage volume for backup \"%s\" on storage pool \"%s\"",
		name, s.pool.Name)
	return nil
}

func dirBackupDeleteInternal(poolName string, backupName string) error {
	backupContainerMntPoint := getBackupMountPoint(poolName, backupName)
	if shared.PathExists(backupContainerMntPoint) {
		err := os.RemoveAll(backupContainerMntPoint)
		if err != nil {
			return err
		}
	}

	sourceContainerName, _, _ := containerGetParentAndSnapshotName(backupName)
	backupContainerPath := getBackupMountPoint(poolName, sourceContainerName)
	empty, _ := shared.PathIsEmpty(backupContainerPath)
	if empty == true {
		err := os.Remove(backupContainerPath)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageDir) ContainerBackupRename(backup backup, newName string) error {
	logger.Debugf("Renaming DIR storage volume for backup \"%s\" from %s to %s",
		backup.Name(), backup.Name(), newName)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	oldBackupMntPoint := getBackupMountPoint(s.pool.Name, backup.Name())
	newBackupMntPoint := getBackupMountPoint(s.pool.Name, newName)

	// Rename directory
	if shared.PathExists(oldBackupMntPoint) {
		err := os.Rename(oldBackupMntPoint, newBackupMntPoint)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Renamed DIR storage volume for backup \"%s\" from %s to %s",
		backup.Name(), backup.Name(), newName)
	return nil
}

func (s *storageDir) ContainerBackupDump(backup backup) ([]byte, error) {
	_, err := s.StoragePoolMount()
	if err != nil {
		return nil, err
	}

	source := s.pool.Config["source"]
	if source == "" {
		return nil, fmt.Errorf("no \"source\" property found for the storage pool")
	}

	backupMntPoint := getBackupMountPoint(s.pool.Name, backup.Name())

	args := []string{"-cJf", "-", "--xattrs", "-C", backupMntPoint, "--transform", "s,^./,backup/,"}
	if backup.ContainerOnly() {
		// Exclude snapshots directory
		args = append(args, "--exclude", fmt.Sprintf("%s/snapshots", backup.Name()))
	}
	args = append(args, ".")

	var buffer bytes.Buffer
	err = shared.RunCommandWithFds(nil, &buffer, "tar", args...)
	if err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (s *storageDir) ContainerBackupLoad(info backupInfo, data io.ReadSeeker) error {
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	// Create mountpoints
	containerMntPoint := getContainerMountPoint(s.pool.Name, info.Name)
	err = createContainerMountpoint(containerMntPoint, containerPath(info.Name, false), info.Privileged)
	if err != nil {
		return err
	}

	// Extract container
	data.Seek(0, 0)
	err = shared.RunCommandWithFds(data, nil, "tar", "-xJf", "-", "--strip-components=2", "--xattrs-include=*", "-C", containerMntPoint, "backup/container")
	if err != nil {
		return err
	}

	if len(info.Snapshots) > 0 {
		// Create mountpoints
		snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, info.Name)
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", info.Name)
		snapshotMntPointSymlink := shared.VarPath("snapshots", info.Name)
		err := createSnapshotMountpoint(snapshotMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return err
		}

		// Extract snapshots
		data.Seek(0, 0)
		err = shared.RunCommandWithFds(data, nil, "tar", "-xJf", "-", "--strip-components=2", "--xattrs-include=*", "-C", snapshotMntPoint, "backup/snapshots")
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageDir) ImageCreate(fingerprint string) error {
	return nil
}

func (s *storageDir) ImageDelete(fingerprint string) error {
	err := s.deleteImageDbPoolVolume(fingerprint)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageDir) ImageMount(fingerprint string) (bool, error) {
	return true, nil
}

func (s *storageDir) ImageUmount(fingerprint string) (bool, error) {
	return true, nil
}

func (s *storageDir) MigrationType() migration.MigrationFSType {
	return migration.MigrationFSType_RSYNC
}

func (s *storageDir) PreservesInodes() bool {
	return false
}

func (s *storageDir) MigrationSource(container container, containerOnly bool) (MigrationStorageSourceDriver, error) {
	return rsyncMigrationSource(container, containerOnly)
}

func (s *storageDir) MigrationSink(live bool, container container, snapshots []*migration.Snapshot, conn *websocket.Conn, srcIdmap *idmap.IdmapSet, op *operation, containerOnly bool) error {
	return rsyncMigrationSink(live, container, snapshots, conn, srcIdmap, op, containerOnly)
}

func (s *storageDir) StorageEntitySetQuota(volumeType int, size int64, data interface{}) error {
	return fmt.Errorf("the directory container backend doesn't support quotas")
}

func (s *storageDir) StoragePoolResources() (*api.ResourcesStoragePool, error) {
	_, err := s.StoragePoolMount()
	if err != nil {
		return nil, err
	}

	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)

	return storageResource(poolMntPoint)
}

func (s *storageDir) StoragePoolVolumeCopy(source *api.StorageVolumeSource) error {
	logger.Infof("Copying DIR storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)
	successMsg := fmt.Sprintf("Copied DIR storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)

	if s.pool.Name != source.Pool {
		// setup storage for the source volume
		srcStorage, err := storagePoolVolumeInit(s.s, source.Pool, source.Name, storagePoolVolumeTypeCustom)
		if err != nil {
			logger.Errorf("Failed to initialize DIR storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
			return err
		}

		ourMount, err := srcStorage.StoragePoolVolumeMount()
		if err != nil {
			logger.Errorf("Failed to mount DIR storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
			return err
		}
		if ourMount {
			defer srcStorage.StoragePoolVolumeUmount()
		}
	}

	err := s.StoragePoolVolumeCreate()
	if err != nil {
		logger.Errorf("Failed to create DIR storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	srcMountPoint := getStoragePoolVolumeMountPoint(source.Pool, source.Name)
	dstMountPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	bwlimit := s.pool.Config["rsync.bwlimit"]
	_, err = rsyncLocalCopy(srcMountPoint, dstMountPoint, bwlimit)
	if err != nil {
		os.RemoveAll(dstMountPoint)
		logger.Errorf("Failed to rsync into DIR storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	logger.Infof(successMsg)
	return nil
}

func (s *storageDir) StorageMigrationSource() (MigrationStorageSourceDriver, error) {
	return rsyncStorageMigrationSource()
}

func (s *storageDir) StorageMigrationSink(conn *websocket.Conn, op *operation, storage storage) error {
	return rsyncStorageMigrationSink(conn, op, storage)
}

func (s *storageDir) GetStoragePool() *api.StoragePool {
	return s.pool
}

func (s *storageDir) GetStoragePoolVolume() *api.StorageVolume {
	return s.volume
}

func (s *storageDir) GetState() *state.State {
	return s.s
}

func (s *storageDir) StoragePoolVolumeSnapshotCreate(target *api.StorageVolumeSnapshotsPost) error {
	logger.Infof("Creating DIR storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	sourceName, _, ok := containerGetParentAndSnapshotName(target.Name)
	if !ok {
		return fmt.Errorf("Not a snapshot name")
	}

	targetPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, target.Name)
	err = os.MkdirAll(targetPath, 0711)
	if err != nil {
		return err
	}

	sourcePath := getStoragePoolVolumeMountPoint(s.pool.Name, sourceName)
	bwlimit := s.pool.Config["rsync.bwlimit"]
	msg, err := rsyncLocalCopy(sourcePath, targetPath, bwlimit)
	if err != nil {
		return fmt.Errorf("Failed to rsync: %s: %s", string(msg), err)
	}

	logger.Infof("Created DIR storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolVolumeSnapshotDelete() error {
	logger.Infof("Deleting DIR storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	storageVolumePath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	err := os.RemoveAll(storageVolumePath)
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
		logger.Errorf(`Failed to delete database entry for DIR storage volume "%s" on storage pool "%s"`,
			s.volume.Name, s.pool.Name)
	}

	logger.Infof("Deleted DIR storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolVolumeSnapshotRename(newName string) error {
	logger.Infof("Renaming DIR storage volume on storage pool \"%s\" from \"%s\" to \"%s\"", s.pool.Name, s.volume.Name, newName)

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

	logger.Infof("Renamed DIR storage volume on storage pool \"%s\" from \"%s\" to \"%s\"", s.pool.Name, s.volume.Name, newName)

	return s.s.Cluster.StoragePoolVolumeRename(s.volume.Name, fullSnapshotName, storagePoolVolumeTypeCustom, s.poolID)
}
