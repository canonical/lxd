package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// dir represents the dir storage backend.
type dir struct {
	driverCommon
}

// init initializes the storage backend.
func (s *dir) init() error {
	s.sTypeVersion = "1"

	return nil
}

// StoragePoolCheck check the storage pool.
func (s *dir) StoragePoolCheck() error {
	// Nothing to do
	return nil
}

// StoragePoolCreate creates a storage pool.
func (s *dir) StoragePoolCreate() error {
	poolMntPoint := GetStoragePoolMountPoint(s.pool.Name)

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

	revert = false

	return nil
}

// StoragePoolDelete deletes a storage pool.
func (s *dir) StoragePoolDelete() error {
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
		storagePoolSymlink := GetStoragePoolMountPoint(s.pool.Name)
		if !shared.PathExists(storagePoolSymlink) {
			return nil
		}

		err := os.Remove(storagePoolSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

// StoragePoolMount mounts a storage pool.
func (s *dir) StoragePoolMount() (bool, error) {
	cleanupFunc := LockPoolMount(s.pool.Name)
	if cleanupFunc == nil {
		return false, nil
	}
	defer cleanupFunc()

	source := shared.HostPath(s.pool.Config["source"])
	if source == "" {
		return false, fmt.Errorf("no \"source\" property found for the storage pool")
	}

	cleanSource := filepath.Clean(source)
	poolMntPoint := GetStoragePoolMountPoint(s.pool.Name)

	if cleanSource == poolMntPoint {
		return true, nil
	}

	mountSource := cleanSource
	mountFlags := unix.MS_BIND

	if shared.IsMountPoint(poolMntPoint) {
		return false, nil
	}

	err := unix.Mount(mountSource, poolMntPoint, "", uintptr(mountFlags), "")
	if err != nil {
		logger.Errorf(`Failed to mount DIR storage pool "%s" onto "%s": %s`, mountSource, poolMntPoint, err)
		return false, err
	}

	return true, nil
}

// StoragePoolUmount unmounts a storage pool.
func (s *dir) StoragePoolUmount() (bool, error) {
	cleanupFunc := LockPoolUmount(s.pool.Name)
	if cleanupFunc == nil {
		return false, nil
	}
	defer cleanupFunc()

	source := s.pool.Config["source"]
	if source == "" {
		return false, fmt.Errorf("no \"source\" property found for the storage pool")
	}

	cleanSource := filepath.Clean(source)
	poolMntPoint := GetStoragePoolMountPoint(s.pool.Name)

	if cleanSource == poolMntPoint {
		return true, nil
	}

	if !shared.IsMountPoint(poolMntPoint) {
		return false, nil
	}

	err := unix.Unmount(poolMntPoint, 0)
	if err != nil {
		return false, err
	}

	return true, nil
}

// StoragePoolResources returns the available resources of the storage pool.
func (s *dir) StoragePoolResources() (*api.ResourcesStoragePool, error) {
	ourMount, err := s.StoragePoolMount()
	if err != nil {
		return nil, err
	}

	if ourMount {
		defer s.StoragePoolUmount()
	}

	return GetStorageResource(GetStoragePoolMountPoint(s.pool.Name))
}

// StoragePoolUpdate updates the storage pool.
func (s *dir) StoragePoolUpdate(writable *api.StoragePoolPut,
	changedConfig []string) error {
	// Nothing to do
	return nil
}

// VolumeUpdate updates the storage volume.
func (s *dir) VolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error {
	// Nothing to do
	return nil
}

// VolumeRestore restores a storage volume from a snapshot.
func (s *dir) VolumeRestore(project string, sourceName string, targetName string, volumeType VolumeType) error {
	var sourceMountPoint string
	var targetMountPoint string

	switch volumeType {
	case VolumeTypeContainer, VolumeTypeContainerSnapshot:
		sourceMountPoint = GetSnapshotMountPoint(project, s.pool.Name, sourceName)
		targetMountPoint = GetContainerMountPoint(project, s.pool.Name, targetName)
	case VolumeTypeCustom, VolumeTypeCustomSnapshot:
		sourceMountPoint = GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, sourceName)
		targetMountPoint = GetStoragePoolVolumeMountPoint(s.pool.Name, targetName)
	default:
		return fmt.Errorf("Unsupported volume type: %v", volumeType)
	}

	return s.rsync(sourceMountPoint, targetMountPoint)
}

// VolumeCreate creates a new storage volume.
func (s *dir) VolumeCreate(project string, volumeName string, volumeType VolumeType) error {
	var volumePath string

	switch volumeType {
	case VolumeTypeContainer:
		volumePath = GetContainerMountPoint(project, s.pool.Name, volumeName)
	case VolumeTypeImage:
		volumePath = GetImageMountPoint(s.pool.Name, volumeName)
	case VolumeTypeCustom:
		volumePath = GetStoragePoolVolumeMountPoint(s.pool.Name, volumeName)
	default:
		return fmt.Errorf("Unsupported volume type: %v", volumeType)
	}

	err := os.MkdirAll(volumePath, 0711)
	if err != nil {
		return err
	}

	return nil
}

// VolumeCopy copies the storage volume including the provided snapshots.
func (s *dir) VolumeCopy(project string, source string, target string, snapshots []string, volumeType VolumeType) error {
	var sourceMountPoint string
	var targetMountPoint string

	switch volumeType {
	case VolumeTypeContainer:
		for _, snap := range snapshots {
			sourceMountPoint = GetSnapshotMountPoint(project, s.pool.Name, fmt.Sprintf("%s/%s", source, snap))
			targetMountPoint = GetSnapshotMountPoint(project, s.pool.Name, fmt.Sprintf("%s/%s", target, snap))

			err := s.rsync(sourceMountPoint, targetMountPoint)
			if err != nil {
				return err
			}
		}

		sourceMountPoint = GetContainerMountPoint(project, s.pool.Name, source)
		targetMountPoint = GetContainerMountPoint(project, s.pool.Name, target)
	case VolumeTypeCustom:
		for _, snap := range snapshots {
			sourceMountPoint = GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, fmt.Sprintf("%s/%s", source, snap))
			targetMountPoint = GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, fmt.Sprintf("%s/%s", target, snap))

			err := s.rsync(sourceMountPoint, targetMountPoint)
			if err != nil {
				return err
			}
		}

		sourceMountPoint = GetStoragePoolVolumeMountPoint(s.pool.Name, source)
		targetMountPoint = GetStoragePoolVolumeMountPoint(s.pool.Name, target)
	default:
		return fmt.Errorf("Unsupported volume type: %v", volumeType)
	}

	return s.rsync(sourceMountPoint, targetMountPoint)
}

// VolumeDelete removes the given storage volume.
func (s *dir) VolumeDelete(project string, volumeName string, recursive bool, volumeType VolumeType) error {
	var path string

	switch volumeType {
	case VolumeTypeContainer:
		path = GetContainerMountPoint(project, s.pool.Name, volumeName)
	case VolumeTypeCustom:
		path = GetStoragePoolVolumeMountPoint(s.pool.Name, volumeName)
	default:
		return fmt.Errorf("Unsupported volume type: %v", volumeType)
	}

	err := os.RemoveAll(path)
	if err != nil {
		return err
	}

	return nil
}

// VolumeRename renames the given storage volume.
func (s *dir) VolumeRename(project string, oldName string, newName string, snapshots []string,
	volumeType VolumeType) error {
	var oldPath string
	var newPath string

	switch volumeType {
	case VolumeTypeContainer:
		oldPath = GetContainerMountPoint(project, s.pool.Name, oldName)
		newPath = GetContainerMountPoint(project, s.pool.Name, newName)
	case VolumeTypeCustom:
		oldPath = GetStoragePoolVolumeMountPoint(s.pool.Name, oldName)
		newPath = GetStoragePoolVolumeMountPoint(s.pool.Name, newName)
	default:
		return fmt.Errorf("Unsupported volume type: %v", volumeType)
	}

	if shared.PathExists(newPath) {
		// Nothing to do
		return nil
	}

	return os.Rename(oldPath, newPath)
}

// VolumeMount mounts the given storage volume.
func (s *dir) VolumeMount(project string, name string, volumeType VolumeType) (bool, error) {
	// Nothing to do
	return true, nil
}

// VolumeUmount unmounts the given storage volume.
func (s *dir) VolumeUmount(project string, name string, volumeType VolumeType) (bool, error) {
	// Nothing to do
	return true, nil
}

// VolumeGetUsage returns the usage of the given storage volume.
func (s *dir) VolumeGetUsage(project, name string,
	path string) (int64, error) {
	return -1, fmt.Errorf("The directory container backend doesn't support quotas")
}

// VolumeSetQuota sets quotas for the given storage volume.
func (s *dir) VolumeSetQuota(project, name string,
	size int64, userns bool, volumeType VolumeType) error {
	return nil
}

// VolumeSnapshotCreate creates a snapshot of the given storage volume.
func (s *dir) VolumeSnapshotCreate(project string, source string, target string, volumeType VolumeType) error {
	var sourceMountPoint string
	var targetMountPoint string

	switch volumeType {
	case VolumeTypeContainerSnapshot:
		sourceMountPoint = GetContainerMountPoint(project, s.pool.Name, source)
		targetMountPoint = GetSnapshotMountPoint(project, s.pool.Name, target)
	case VolumeTypeCustomSnapshot:
		sourceMountPoint = GetStoragePoolVolumeMountPoint(s.pool.Name, source)
		targetMountPoint = GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, target)

		err := os.MkdirAll(targetMountPoint, 0711)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("Unsupported volume type: %v", volumeType)
	}

	if source == "" {
		// Nothing to do
		return nil
	}

	return s.rsync(sourceMountPoint, targetMountPoint)
}

// VolumeSnapshotCopy copies the given storage volume snapshot.
func (s *dir) VolumeSnapshotCopy(project string, source string, target string, volumeType VolumeType) error {
	var sourceMountPoint string
	var targetMountPoint string

	switch volumeType {
	case VolumeTypeContainerSnapshot:
		sourceMountPoint = GetSnapshotMountPoint(project, s.pool.Name, source)

		if shared.IsSnapshot(target) {
			targetMountPoint = GetSnapshotMountPoint(project, s.pool.Name, target)
		} else {
			targetMountPoint = GetContainerMountPoint(project, s.pool.Name, target)
		}
	case VolumeTypeCustomSnapshot:
		sourceMountPoint = GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, source)

		if shared.IsSnapshot(target) {
			targetMountPoint = GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, target)
		} else {
			targetMountPoint = GetStoragePoolVolumeMountPoint(s.pool.Name, target)
		}
	default:
		return fmt.Errorf("Unsupported volume type: %v", volumeType)
	}

	return s.rsync(sourceMountPoint, targetMountPoint)
}

// VolumeSnapshotDelete removes the given storage volume snapshot.
func (s *dir) VolumeSnapshotDelete(project string, volumeName string, recursive bool, volumeType VolumeType) error {
	var path string

	switch volumeType {
	case VolumeTypeCustomSnapshot:
		path = GetStoragePoolVolumeSnapshotMountPoint(s.pool.Name, volumeName)
	case VolumeTypeContainerSnapshot:
		path = GetSnapshotMountPoint(project, s.pool.Name, volumeName)
	default:
		return fmt.Errorf("Unsupported volume type: %v", volumeType)
	}

	err := os.RemoveAll(path)
	if err != nil {
		return err
	}

	return nil
}

// VolumeSnapshotRename rename the given storage volume snapshot.
func (s *dir) VolumeSnapshotRename(project string, oldName string, newName string, volumeType VolumeType) error {
	switch volumeType {
	case VolumeTypeContainerSnapshot:
		// Nothing to do as this is handled by the storage itself
	case VolumeTypeCustomSnapshot:
		// Nothing to do as this is handled by the storage itself
	default:
		return fmt.Errorf("Unsupported volume type: %v", volumeType)

	}

	return nil
}

// VolumeBackupCreate creates a backup of the given storage volume.
func (s *dir) VolumeBackupCreate(path string, projectName string, source string,
	snapshots []string, optimized bool) error {
	snapshotsPath := fmt.Sprintf("%s/snapshots", path)

	if len(snapshots) > 0 {
		err := os.MkdirAll(snapshotsPath, 0711)
		if err != nil {
			return err
		}
	}
	for _, snap := range snapshots {
		fullSnapshotName := fmt.Sprintf("%s/%s", s.volume.Name, snap)
		snapshotMntPoint := GetSnapshotMountPoint(projectName, s.pool.Name, fullSnapshotName)
		target := fmt.Sprintf("%s/%s", snapshotsPath, snap)

		// Copy the snapshot
		err := s.rsync(snapshotMntPoint, target)
		if err != nil {
			return err
		}
	}

	// Copy the container
	sourcePath := ContainerPath(project.Prefix(projectName, source), false)
	targetPath := fmt.Sprintf("%s/container", path)
	err := s.rsync(sourcePath, targetPath)
	if err != nil {
		return err
	}

	return nil
}

// VolumeBackupLoad loads a volume backup.
func (s *dir) VolumeBackupLoad(backupDir string, project string,
	containerName string, snapshots []string, privileged bool, optimized bool) error {
	if optimized {
		return fmt.Errorf("Dir storage doesn't support binary backups")
	}

	// Nothing to do
	return nil
}

// VolumePrepareRestore prepares a storage volume restore.
func (s *dir) VolumePrepareRestore(sourceName string, targetName string, targetSnapshots []string, f func() error) error {
	// Nothing to do
	return nil
}

// VolumeReady returns whether the given volume is ready.
func (s *dir) VolumeReady(project string, name string) bool {
	containerMntPoint := GetContainerMountPoint(project, s.pool.Name, name)
	ok, _ := shared.PathIsEmpty(containerMntPoint)
	return !ok
}
