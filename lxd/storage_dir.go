package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
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

	logger.Debugf("Initializing a DIR driver.")
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
	logger.Debugf("Checking DIR storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolCreate() error {
	logger.Infof("Creating DIR storage pool \"%s\".", s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		source = filepath.Join(shared.VarPath("storage-pools"), s.pool.Name)
		s.pool.Config["source"] = source
	} else {
		cleanSource := filepath.Clean(source)
		lxdDir := shared.VarPath()
		poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
		if strings.HasPrefix(cleanSource, lxdDir) && cleanSource != poolMntPoint {
			return fmt.Errorf("DIR storage pool requests in LXD directory \"%s\" are only valid under \"%s\"\n(e.g. source=%s)", shared.VarPath(), shared.VarPath("storage-pools"), poolMntPoint)
		}
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
	}

	prefix := shared.VarPath("storage-pools")
	if !strings.HasPrefix(source, prefix) {
		// symlink from storage-pools to pool x
		storagePoolSymlink := getStoragePoolMountPoint(s.pool.Name)
		err := os.Symlink(source, storagePoolSymlink)
		if err != nil {
			return err
		}
	}

	err := s.StoragePoolCheck()
	if err != nil {
		return err
	}

	revert = false

	logger.Infof("Created DIR storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolDelete() error {
	logger.Infof("Deleting DIR storage pool \"%s\".", s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
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

	logger.Infof("Deleted DIR storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolMount() (bool, error) {
	return true, nil
}

func (s *storageDir) StoragePoolUmount() (bool, error) {
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

func (s *storageDir) GetContainerPoolInfo() (int64, string) {
	return s.poolID, s.pool.Name
}

func (s *storageDir) StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error {
	if shared.StringInSlice("rsync.bwlimit", changedConfig) {
		return nil
	}

	return fmt.Errorf("storage property cannot be changed")
}

// Functions dealing with storage pools.
func (s *storageDir) StoragePoolVolumeCreate() error {
	logger.Infof("Creating DIR storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	storageVolumePath := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	err := os.MkdirAll(storageVolumePath, 0711)
	if err != nil {
		return err
	}

	logger.Infof("Created DIR storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolVolumeDelete() error {
	logger.Infof("Deleting DIR storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

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

	err = dbStoragePoolVolumeDelete(
		s.d.db,
		s.volume.Name,
		storagePoolVolumeTypeCustom,
		s.poolID)
	if err != nil {
		logger.Errorf(`Failed to delete database entry for ZFS `+
			`storage volume "%s" on storage pool "%s"`,
			s.volume.Name, s.pool.Name)
	}

	logger.Infof("Deleted DIR storage volume \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) StoragePoolVolumeMount() (bool, error) {
	return true, nil
}

func (s *storageDir) StoragePoolVolumeUmount() (bool, error) {
	return true, nil
}

func (s *storageDir) StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error {
	return fmt.Errorf("dir storage properties cannot be changed")
}

func (s *storageDir) ContainerStorageReady(name string) bool {
	containerMntPoint := getContainerMountPoint(s.pool.Name, name)
	ok, _ := shared.PathIsEmpty(containerMntPoint)
	return !ok
}

func (s *storageDir) ContainerCreate(container container) error {
	logger.Debugf("Creating empty DIR storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	containerMntPoint := getContainerMountPoint(s.pool.Name, container.Name())
	err := createContainerMountpoint(containerMntPoint, container.Path(), container.IsPrivileged())
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

	logger.Debugf("Created empty DIR storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) ContainerCreateFromImage(container container, imageFingerprint string) error {
	logger.Debugf("Creating DIR storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	privileged := container.IsPrivileged()
	containerName := container.Name()
	containerMntPoint := getContainerMountPoint(s.pool.Name, containerName)
	err := createContainerMountpoint(containerMntPoint, container.Path(), privileged)
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
	err = unpackImage(s.d, imagePath, containerMntPoint, storageTypeDir)
	if err != nil {
		return err
	}

	if !privileged {
		err := s.shiftRootfs(container)
		if err != nil {
			return err
		}
	}

	err = container.TemplateApply("create")
	if err != nil {
		return err
	}

	revert = false

	logger.Debugf("Created DIR storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageDir) ContainerDelete(container container) error {
	logger.Debugf("Deleting DIR storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
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

	err := deleteContainerMountpoint(containerMntPoint, container.Path(), s.GetStorageTypeName())
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
	// ${LXD_DIR}/snapshots/<container_name> -> ${POOL}/snapshots/<container_name>
	snapshotSymlink := shared.VarPath("snapshots", container.Name())
	if shared.PathExists(snapshotSymlink) {
		err := os.Remove(snapshotSymlink)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Deleted DIR storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) copyContainer(target container, source container) error {
	sourceContainerMntPoint := getContainerMountPoint(s.pool.Name, source.Name())
	if source.IsSnapshot() {
		sourceContainerMntPoint = getSnapshotMountPoint(s.pool.Name, source.Name())
	}
	targetContainerMntPoint := getContainerMountPoint(s.pool.Name, target.Name())

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

func (s *storageDir) copySnapshot(target container, source container) error {
	sourceName := source.Name()
	targetName := target.Name()
	sourceContainerMntPoint := getSnapshotMountPoint(s.pool.Name, sourceName)
	targetContainerMntPoint := getSnapshotMountPoint(s.pool.Name, targetName)

	targetParentName, _, _ := containerGetParentAndSnapshotName(target.Name())
	containersPath := getSnapshotMountPoint(s.pool.Name, targetParentName)
	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "snapshots", targetParentName)
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
	logger.Debugf("Copying DIR container storage %s -> %s.", source.Name(), target.Name())

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
		logger.Debugf("Copied DIR container storage %s -> %s.", source.Name(), target.Name())
		return nil
	}

	snapshots, err := source.Snapshots()
	if err != nil {
		return err
	}

	if len(snapshots) == 0 {
		logger.Debugf("Copied DIR container storage %s -> %s.", source.Name(), target.Name())
		return nil
	}

	for _, snap := range snapshots {
		sourceSnapshot, err := containerLoadByName(s.d, snap.Name())
		if err != nil {
			return err
		}

		_, snapOnlyName, _ := containerGetParentAndSnapshotName(snap.Name())
		newSnapName := fmt.Sprintf("%s/%s", target.Name(), snapOnlyName)
		targetSnapshot, err := containerLoadByName(s.d, newSnapName)
		if err != nil {
			return err
		}

		err = s.copySnapshot(targetSnapshot, sourceSnapshot)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Copied DIR container storage %s -> %s.", source.Name(), target.Name())
	return nil
}

func (s *storageDir) ContainerMount(c container) (bool, error) {
	return true, nil
}

func (s *storageDir) ContainerUmount(name string, path string) (bool, error) {
	return true, nil
}

func (s *storageDir) ContainerRename(container container, newName string) error {
	logger.Debugf("Renaming DIR storage volume for container \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newName)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	oldContainerMntPoint := getContainerMountPoint(s.pool.Name, container.Name())
	oldContainerSymlink := shared.VarPath("containers", container.Name())
	newContainerMntPoint := getContainerMountPoint(s.pool.Name, newName)
	newContainerSymlink := shared.VarPath("containers", newName)
	err := renameContainerMountpoint(oldContainerMntPoint, oldContainerSymlink, newContainerMntPoint, newContainerSymlink)
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
		// ${LXD_DIR}/snapshots/<new_container_name> -> ${POOL}/snapshots/<new_container_name>
		err = os.Symlink(newSnapshotsMntPoint, newSnapshotSymlink)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Renamed DIR storage volume for container \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newName)
	return nil
}

func (s *storageDir) ContainerRestore(container container, sourceContainer container) error {
	logger.Debugf("Restoring DIR storage volume for container \"%s\" from %s -> %s.", s.volume.Name, sourceContainer.Name(), container.Name())

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

	logger.Debugf("Restored DIR storage volume for container \"%s\" from %s -> %s.", s.volume.Name, sourceContainer.Name(), container.Name())
	return nil
}

func (s *storageDir) ContainerGetUsage(container container) (int64, error) {
	return -1, fmt.Errorf("the directory container backend doesn't support quotas")
}

func (s *storageDir) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	logger.Debugf("Creating DIR storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	// Create the path for the snapshot.
	targetContainerName := snapshotContainer.Name()
	targetContainerMntPoint := getSnapshotMountPoint(s.pool.Name, targetContainerName)
	err := os.MkdirAll(targetContainerMntPoint, 0711)
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

	_, sourcePool := sourceContainer.Storage().GetContainerPoolInfo()
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
		logger.Debugf("Trying to freeze and rsync again to ensure consistency.")

		err := sourceContainer.Freeze()
		if err != nil {
			logger.Errorf("Trying to freeze and rsync again failed.")
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
	// ${LXD_DIR}/snapshots/<source_container_name> -> ${POOL_PATH}/snapshots/<source_container_name>
	// exists and if not create it.
	sourceContainerSymlink := shared.VarPath("snapshots", sourceContainerName)
	sourceContainerSymlinkTarget := getSnapshotMountPoint(sourcePool, sourceContainerName)
	if !shared.PathExists(sourceContainerSymlink) {
		err = os.Symlink(sourceContainerSymlinkTarget, sourceContainerSymlink)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Created DIR storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	logger.Debugf("Creating empty DIR storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	// Create the path for the snapshot.
	targetContainerName := snapshotContainer.Name()
	targetContainerMntPoint := getSnapshotMountPoint(s.pool.Name, targetContainerName)
	err := os.MkdirAll(targetContainerMntPoint, 0711)
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
	// ${LXD_DIR}/snapshots/<source_container_name> -> ${POOL_PATH}/snapshots/<source_container_name>
	// exists and if not create it.
	sourceContainerName, _, _ := containerGetParentAndSnapshotName(targetContainerName)
	sourceContainerSymlink := shared.VarPath("snapshots", sourceContainerName)
	sourceContainerSymlinkTarget := getSnapshotMountPoint(s.pool.Name, sourceContainerName)
	if !shared.PathExists(sourceContainerSymlink) {
		err := os.Symlink(sourceContainerSymlinkTarget, sourceContainerSymlink)
		if err != nil {
			return err
		}
	}

	revert = false

	logger.Debugf("Created empty DIR storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) ContainerSnapshotDelete(snapshotContainer container) error {
	logger.Debugf("Deleting DIR storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
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
	sourceContainerName, _, _ := containerGetParentAndSnapshotName(snapshotContainerName)
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

	logger.Debugf("Deleted DIR storage volume for snapshot \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageDir) ContainerSnapshotRename(snapshotContainer container, newName string) error {
	logger.Debugf("Renaming DIR storage volume for snapshot \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newName)

	// Rename the mountpoint for the snapshot:
	// ${POOL}/snapshots/<old_snapshot_name> to ${POOL}/snapshots/<new_snapshot_name>
	oldSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, snapshotContainer.Name())
	newSnapshotMntPoint := getSnapshotMountPoint(s.pool.Name, newName)
	err := os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
	if err != nil {
		return err
	}

	logger.Debugf("Renamed DIR storage volume for snapshot \"%s\" from %s -> %s.", s.volume.Name, s.volume.Name, newName)
	return nil
}

func (s *storageDir) ContainerSnapshotStart(container container) (bool, error) {
	return true, nil
}

func (s *storageDir) ContainerSnapshotStop(container container) (bool, error) {
	return true, nil
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

func (s *storageDir) MigrationType() MigrationFSType {
	return MigrationFSType_RSYNC
}

func (s *storageDir) PreservesInodes() bool {
	return false
}

func (s *storageDir) MigrationSource(container container, containerOnly bool) (MigrationStorageSourceDriver, error) {
	return rsyncMigrationSource(container, containerOnly)
}

func (s *storageDir) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation, containerOnly bool) error {
	return rsyncMigrationSink(live, container, snapshots, conn, srcIdmap, op, containerOnly)
}

func (s *storageDir) StorageEntitySetQuota(volumeType int, size int64, data interface{}) error {
	return fmt.Errorf("the directory container backend doesn't support quotas")
}
