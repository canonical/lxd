package main

import (
	"fmt"
	"os"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

type storageCeph struct {
	ClusterName string
	OSDPoolName string
	PGNum       string
	storageShared
}

func (s *storageCeph) StorageCoreInit() error {
	s.sType = storageTypeCeph
	typeName, err := storageTypeToString(s.sType)
	if err != nil {
		return err
	}
	s.sTypeName = typeName

	_, err = shared.RunCommand("ceph", "version")
	if err != nil {
		return fmt.Errorf("Error getting CEPH version: %s", err)
	}

	logger.Debugf("Initializing a CEPH driver.")
	return nil
}

func (s *storageCeph) StoragePoolInit() error {
	var err error

	err = s.StorageCoreInit()
	if err != nil {
		return err
	}

	// set cluster name
	if s.pool.Config["ceph.cluster_name"] != "" {
		s.ClusterName = s.pool.Config["ceph.cluster_name"]
	} else {
		s.ClusterName = "ceph"
	}

	// set osd pool name
	if s.pool.Config["ceph.osd.pool_name"] != "" {
		s.OSDPoolName = s.pool.Config["ceph.osd.pool_name"]
	} else {
		s.OSDPoolName = s.pool.Name
	}

	// set default placement group number
	if s.pool.Config["ceph.osd.pg_num"] != "" {
		_, err = shared.ParseByteSizeString(s.pool.Config["ceph.osd.pg_num"])
		if err != nil {
			return err
		}
		s.PGNum = s.pool.Config["ceph.osd.pg_num"]
	} else {
		s.PGNum = "32"
	}

	return nil
}

func (s *storageCeph) StoragePoolCheck() error {
	logger.Debugf("Checking CEPH storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageCeph) StoragePoolCreate() error {
	logger.Infof("Creating CEPH storage pool \"%s\".", s.pool.Name)

	// test if pool already exists
	if cephOSDPoolExists(s.ClusterName, s.OSDPoolName) {
		return fmt.Errorf("CEPH osd storage pool \"%s\" already exists in cluster \"%s\"", s.OSDPoolName, s.ClusterName)
	}

	msg, err := shared.TryRunCommand("ceph", "--cluster", s.ClusterName, "osd", "pool", "create", s.OSDPoolName, s.PGNum)
	if err != nil {
		return fmt.Errorf("failed to create CEPH osd storage pool \"%s\" in cluster \"%s\": %s", s.OSDPoolName, s.ClusterName, msg)
	}

	if s.pool.Config["source"] == "" {
		s.pool.Config["source"] = s.OSDPoolName
	}

	// set immutable ceph.cluster_name property
	if s.pool.Config["ceph.cluster_name"] == "" {
		s.pool.Config["ceph.cluster_name"] = "ceph"
	}

	// set immutable ceph.osd.pool_name property
	if s.pool.Config["ceph.osd.pool_name"] == "" {
		s.pool.Config["ceph.osd.pool_name"] = s.pool.Name
	}

	if s.pool.Config["ceph.osd.pg_num"] == "" {
		s.pool.Config["ceph.osd.pg_num"] = "32"
	}

	// Create the mountpoint for the storage pool.
	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	err = os.MkdirAll(poolMntPoint, 0711)
	if err != nil {
		// Destroy the pool.
		warn := cephOSDPoolDestroy(s.ClusterName, s.OSDPoolName)
		if warn != nil {
			logger.Warnf("failed to destroy ceph storage pool \"%s\"", s.OSDPoolName)
		}
		return err
	}

	logger.Infof("Created CEPH storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageCeph) StoragePoolDelete() error {
	logger.Infof("Deleting CEPH storage pool \"%s\".", s.pool.Name)

	// test if pool exists
	if !cephOSDPoolExists(s.ClusterName, s.OSDPoolName) {
		return fmt.Errorf("CEPH osd storage pool \"%s\" does not exist in cluster \"%s\"", s.OSDPoolName, s.ClusterName)
	}

	// Delete the osd pool.
	err := cephOSDPoolDestroy(s.ClusterName, s.OSDPoolName)
	if err != nil {
		return err
	}

	// Delete the mountpoint for the storage pool.
	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	err = os.RemoveAll(poolMntPoint)
	if err != nil {
		return err
	}

	logger.Infof("Deleted CEPH storage pool \"%s\".", s.pool.Name)
	return nil
}

func (s *storageCeph) StoragePoolMount() (bool, error) {
	// Yay, osd pools are not mounted.
	return true, nil
}

func (s *storageCeph) StoragePoolUmount() (bool, error) {
	// Yay, osd pools are not mounted.
	return true, nil
}

func (s *storageCeph) GetStoragePoolWritable() api.StoragePoolPut {
	return s.pool.StoragePoolPut
}

func (s *storageCeph) GetStoragePoolVolumeWritable() api.StorageVolumePut {
	return api.StorageVolumePut{}
}

func (s *storageCeph) SetStoragePoolWritable(writable *api.StoragePoolPut) {
	s.pool.StoragePoolPut = *writable
}

func (s *storageCeph) SetStoragePoolVolumeWritable(writable *api.StorageVolumePut) {
	s.volume.StorageVolumePut = *writable
}

func (s *storageCeph) GetContainerPoolInfo() (int64, string) {
	return s.poolID, s.pool.Name
}

func (s *storageCeph) StoragePoolVolumeCreate() error {
	return nil
}

func (s *storageCeph) StoragePoolVolumeDelete() error {
	return nil
}

func (s *storageCeph) StoragePoolVolumeMount() (bool, error) {
	return true, nil
}

func (s *storageCeph) StoragePoolVolumeUmount() (bool, error) {
	return true, nil
}

func (s *storageCeph) StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error {
	return nil
}

func (s *storageCeph) StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error {
	return nil
}

func (s *storageCeph) ContainerStorageReady(name string) bool {
	return true
}

func (s *storageCeph) ContainerCreate(container container) error {
	return nil
}

func (s *storageCeph) ContainerCreateFromImage(container container, fingerprint string) error {
	logger.Debugf("Creating RBD storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	containerPath := container.Path()
	containerName := container.Name()
	containerPoolVolumeMntPoint := getContainerMountPoint(s.pool.Name, containerName)

	imageStoragePoolLockID := getImageCreateLockID(s.pool.Name, fingerprint)
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
		if !cephRBDVolumeExists(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage) {
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

	err := cephRBDCloneCreate(s.ClusterName, s.OSDPoolName, fingerprint,
		storagePoolVolumeTypeNameImage, "readonly", s.OSDPoolName,
		containerName, storagePoolVolumeTypeNameContainer)
	if err != nil {
		logger.Errorf("Failed to clone new RBD storage volume for container \"%s\"", containerName)
		return err
	}
	revert := true
	defer func() {
		if !revert {
			return
		}
		s.ContainerDelete(container)
	}()

	err = cephRBDVolumeMap(s.ClusterName, s.OSDPoolName, containerName, storagePoolVolumeTypeNameContainer)
	if err != nil {
		logger.Errorf("Failed to map RBD storage volume for container \"%s\"", containerName)
		return err
	}

	privileged := container.IsPrivileged()
	err = createContainerMountpoint(containerPoolVolumeMntPoint, containerPath, privileged)
	if err != nil {
		logger.Errorf("Failed to create mountpoint for container \"%s\" for RBD storage volume", containerName)
		return err
	}

	ourMount, err := s.ContainerMount(container)
	if err != nil {
		return err
	}
	if ourMount {
		defer s.ContainerUmount(containerName, containerPath)
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

	logger.Debugf("Created RBD storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageCeph) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageCeph) ContainerDelete(container container) error {
	logger.Debugf("Deleting RBD storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	// umount
	containerName := container.Name()
	containerPath := container.Path()
	_, err := s.ContainerUmount(containerName, containerPath)
	if err != nil {
		return err
	}

	err = cephRBDVolumeDelete(s.ClusterName, s.OSDPoolName, containerName, storagePoolVolumeTypeNameContainer)
	if err != nil {
		logger.Errorf("Failed to delete container")
		return err
	}

	containerMntPoint := getContainerMountPoint(s.pool.Name, containerName)
	err = deleteContainerMountpoint(containerMntPoint, containerPath, s.GetStorageTypeName())
	if err != nil {
		logger.Errorf("Failed to delete mountpoint for container \"%s\" for RBD storage volume", containerName)
		return err
	}

	logger.Debugf("Deleted RBD storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageCeph) ContainerCopy(target container, source container, containerOnly bool) error {
	logger.Debugf("Copying RBD container storage %s -> %s", source.Name(), target.Name())

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
		return fmt.Errorf("Copying containers between different storage pools is not implemented")
	}

	snapshots, err := source.Snapshots()
	if err != nil {
		return err
	}

	if containerOnly || len(snapshots) == 0 {
		if s.pool.Config["ceph.rbd.clone_copy"] != "" && !shared.IsTrue(s.pool.Config["ceph.rbd.clone_copy"]) {
			err = s.copyWithoutSnapshotsFull(target, source)
		} else {
			err = s.copyWithoutSnapshotsSparse(target, source)
		}
		if err != nil {
			logger.Errorf("Failed to copy RBD container storage %s -> %s", source.Name(), target.Name())
			return err
		}

		logger.Debugf("Copied RBD container storage %s -> %s", source.Name(), target.Name())
		return nil
	}

	logger.Debugf("Copied RBD container storage %s -> %s", source.Name(), target.Name())
	return nil
}

func (s *storageCeph) ContainerMount(c container) (bool, error) {
	name := c.Name()
	logger.Debugf("Mounting RBD storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

	RBDFilesystem := s.getRBDFilesystem()
	RBDDevPath := getRBDDevPath(s.OSDPoolName, storagePoolVolumeTypeNameContainer, name)
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
		logger.Debugf("RBD storage volume for container \"%s\" on storage pool \"%s\" appears to be already mounted", s.volume.Name, s.pool.Name)
		return false, nil
	}

	lxdStorageOngoingOperationMap[containerMountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	var mounterr error
	ourMount := false
	if !shared.IsMountPoint(containerMntPoint) {
		mountFlags, mountOptions := lxdResolveMountoptions(s.getRBDMountOptions())
		mounterr = tryMount(RBDDevPath, containerMntPoint, RBDFilesystem, mountFlags, mountOptions)
		ourMount = true
	}

	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerMountLockID]; ok {
		close(waitChannel)
		delete(lxdStorageOngoingOperationMap, containerMountLockID)
	}
	lxdStorageMapLock.Unlock()

	if mounterr != nil {
		logger.Errorf("Failed to mount RBD storage volume for container \"%s\": %s", s.volume.Name, mounterr)
		return false, mounterr
	}

	logger.Debugf("Mounted RBD storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageCeph) ContainerUmount(name string, path string) (bool, error) {
	logger.Debugf("Unmounting RBD storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)

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
		logger.Debugf("RBD storage volume for container \"%s\" on storage pool \"%s\" appears to be already unmounted", s.volume.Name, s.pool.Name)
		return false, nil
	}

	lxdStorageOngoingOperationMap[containerUmountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	var mounterr error
	ourUmount := false
	if shared.IsMountPoint(containerMntPoint) {
		mounterr = tryUnmount(containerMntPoint, 0)
		ourUmount = true
	}

	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerUmountLockID]; ok {
		close(waitChannel)
		delete(lxdStorageOngoingOperationMap, containerUmountLockID)
	}
	lxdStorageMapLock.Unlock()

	if mounterr != nil {
		logger.Errorf("Failed to unmount RBD storage volume for container \"%s\": %s", s.volume.Name, mounterr)
		return false, mounterr
	}

	logger.Debugf("Unmounted RBD storage volume for container \"%s\" on storage pool \"%s\".", s.volume.Name, s.pool.Name)
	return ourUmount, nil
}

func (s *storageCeph) ContainerRename(
	container container, newName string) error {

	return nil
}

func (s *storageCeph) ContainerRestore(
	container container, sourceContainer container) error {

	return nil
}

func (s *storageCeph) ContainerGetUsage(
	container container) (int64, error) {

	return 0, nil
}
func (s *storageCeph) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	targetContainerName := snapshotContainer.Name()
	logger.Debugf("Creating RBD storage volume for snapshot \"%s\" on storage pool \"%s\".", targetContainerName, s.pool.Name)

	sourceContainerName := sourceContainer.Name()
	_, targetSnapshotOnlyName, _ := containerGetParentAndSnapshotName(targetContainerName)
	targetSnapshotName := fmt.Sprintf("snapshot_%s", targetSnapshotOnlyName)
	err := cephRBDSnapshotCreate(s.ClusterName, s.OSDPoolName, sourceContainerName, storagePoolVolumeTypeNameContainer, targetSnapshotName)
	if err != nil {
		logger.Errorf("Failed to create snapshot for RBD storage volume for image \"%s\" on storage pool \"%s\": %s", targetContainerName, s.pool.Name, err)
		return err
	}

	targetContainerMntPoint := getSnapshotMountPoint(s.pool.Name, targetContainerName)
	sourceName, _, _ := containerGetParentAndSnapshotName(sourceContainerName)
	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "snapshots", sourceName)
	snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
	err = createSnapshotMountpoint(
		targetContainerMntPoint,
		snapshotMntPointSymlinkTarget,
		snapshotMntPointSymlink)
	if err != nil {
		logger.Errorf(`Failed to create mountpoint "%s", snapshot `+
			`symlink target "%s", snapshot mountpoint symlink"%s" `+
			`for RBD storage volume "%s" on storage pool "%s": %s`,
			targetContainerMntPoint, snapshotMntPointSymlinkTarget,
			snapshotMntPointSymlink, s.volume.Name, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Created mountpoint "%s", snapshot symlink target `+
		`"%s", snapshot mountpoint symlink"%s" for RBD storage `+
		`volume "%s" on storage pool "%s"`, targetContainerMntPoint,
		snapshotMntPointSymlinkTarget, snapshotMntPointSymlink,
		s.volume.Name, s.pool.Name)

	logger.Debugf("Created RBD storage volume for snapshot \"%s\" on storage pool \"%s\".", targetContainerName, s.pool.Name)
	return nil
}

func (s *storageCeph) ContainerSnapshotDelete(snapshotContainer container) error {
	logger.Debugf("Deleting RBD storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	sourceContainerName, sourceContainerSnapOnlyName, _ := containerGetParentAndSnapshotName(snapshotContainer.Name())
	snapshotName := fmt.Sprintf("snapshot_%s", sourceContainerSnapOnlyName)

	_, err := cephRBDSnapshotListClones(s.ClusterName, s.OSDPoolName, sourceContainerName, storagePoolVolumeTypeNameContainer, snapshotName)
	if err != nil {
		if err != NoSuchObjectError {
			logger.Errorf("Failed to list clones of RBD storage volume for snapshot \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
			return err
		}

		// delete snapshot
		err = cephRBDSnapshotDelete(s.ClusterName, s.OSDPoolName, sourceContainerName, storagePoolVolumeTypeNameContainer, snapshotName)
		if err != nil {
			logger.Errorf("failed to create snapshot for RBD storage volume for image \"%s\" on storage pool \"%s\": %s", sourceContainerName, s.pool.Name, err)
			return err
		}
	} else {
		deletedSnapshotName := fmt.Sprintf("zombie_%s", snapshotName)
		// mark deleted
		err := cephRBDVolumeSnapshotRename(s.ClusterName, s.OSDPoolName, sourceContainerName, storagePoolVolumeTypeNameContainer, snapshotName, deletedSnapshotName)
		if err != nil {
			logger.Errorf("Failed to mark RBD storage volume for image \"%s\" on storage pool \"%s\" deleted: %s -> %s", s.pool.Name, snapshotName, deletedSnapshotName)
			return err
		}
	}

	snapshotContainerName := snapshotContainer.Name()
	snapshotContainerMntPoint := getSnapshotMountPoint(s.pool.Name, snapshotContainerName)
	if shared.PathExists(snapshotContainerMntPoint) {
		err := os.RemoveAll(snapshotContainerMntPoint)
		if err != nil {
			return err
		}
	}

	// check if snapshot directory is empty
	snapshotContainerPath := getSnapshotMountPoint(s.pool.Name, sourceContainerName)
	empty, _ := shared.PathIsEmpty(snapshotContainerPath)
	if empty == true {
		// remove snapshot directory for container
		err := os.Remove(snapshotContainerPath)
		if err != nil {
			return err
		}

		// remove the snapshot symlink if possible
		snapshotSymlink := shared.VarPath("snapshots", sourceContainerName)
		if shared.PathExists(snapshotSymlink) {
			err := os.Remove(snapshotSymlink)
			if err != nil {
				return err
			}
		}
	}

	logger.Debugf("Deleted RBD storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageCeph) ContainerSnapshotRename(
	snapshotContainer container, newName string) error {

	return nil
}

func (s *storageCeph) ContainerSnapshotStart(container container) (bool, error) {
	return true, nil
}

func (s *storageCeph) ContainerSnapshotStop(container container) (bool, error) {
	return true, nil
}

func (s *storageCeph) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	return nil
}

func (s *storageCeph) ImageCreate(fingerprint string) error {
	logger.Debugf("Creating RBD storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

	// create image mountpoint
	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if !shared.PathExists(imageMntPoint) {
		err := os.MkdirAll(imageMntPoint, 0700)
		if err != nil {
			logger.Errorf("failed to create mountpoint RBD storage volume for image \"%s\" on storage pool \"%s\": %s", fingerprint, s.pool.Name, err)
			return err
		}
	}

	prefixedType := fmt.Sprintf("zombie_%s", storagePoolVolumeTypeNameImage)
	ok := cephRBDVolumeExists(s.ClusterName, s.OSDPoolName, fingerprint, prefixedType)
	if !ok {
		logger.Debugf("RBD storage volume for image \"%s\" on storage pool \"%s\" does not exist", fingerprint, s.pool.Name)
		// get size
		RBDSize, err := s.getRBDSize()
		if err != nil {
			logger.Errorf("failed to retrieve size of RBD storage volume for image \"%s\" on storage pool \"%s\": %s", fingerprint, s.pool.Name, err)
			return err
		}

		// create volume
		err = cephRBDVolumeCreate(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage, RBDSize)
		if err != nil {
			logger.Errorf("failed to create RBD storage volume for image \"%s\" on storage pool \"%s\": %s", fingerprint, s.pool.Name, err)
			return err
		}

		err = cephRBDVolumeMap(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage)
		if err != nil {
			logger.Errorf("failed to map RBD storage volume for image \"%s\" on storage pool \"%s\": %s", fingerprint, s.pool.Name, err)
			return err
		}

		// get filesystem
		RBDFilesystem := s.getRBDFilesystem()
		// get rbd device path
		RBDDevPath := getRBDDevPath(s.OSDPoolName, storagePoolVolumeTypeNameImage, fingerprint)
		msg, err := makeFSType(RBDDevPath, RBDFilesystem)
		if err != nil {
			logger.Errorf("failed to create filesystem RBD storage volume for image \"%s\" on storage pool \"%s\": %s", fingerprint, s.pool.Name, msg)
			return err
		}

		// mount image
		_, err = s.ImageMount(fingerprint)
		if err != nil {
			return err
		}

		// rsync contents into image
		imagePath := shared.VarPath("images", fingerprint)
		err = unpackImage(s.d, imagePath, imageMntPoint, storageTypeCeph)
		if err != nil {
			logger.Errorf("failed to unpack image for RBD storage volume for image \"%s\" on storage pool \"%s\": %s", fingerprint, s.pool.Name, err)
			return err
		}

		// umount image
		s.ImageUmount(fingerprint)

		// unmap
		err = cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage)
		if err != nil {
			logger.Errorf("Failed to unmap RBD storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
			return err
		}

		// make snapshot of volume
		err = cephRBDSnapshotCreate(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage, "readonly")
		if err != nil {
			logger.Errorf("failed to create snapshot for RBD storage volume for image \"%s\" on storage pool \"%s\": %s", fingerprint, s.pool.Name, err)
			return err
		}

		// protect volume so we can create clones of it
		err = cephRBDSnapshotProtect(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage, "readonly")
		if err != nil {
			logger.Errorf("failed to protect snapshot for RBD storage volume for image \"%s\" on storage pool \"%s\": %s", fingerprint, s.pool.Name, err)
			return err
		}
	} else {
		logger.Debugf("RBD storage volume for image \"%s\" on storage pool \"%s\" does exist", fingerprint, s.pool.Name)
		// unmark deleted
		err := cephRBDVolumeUnmarkDeleted(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage)
		if err != nil {
			logger.Errorf("Failed to unmark RBD storage volume for image \"%s\" on storage pool \"%s\" as deleted", fingerprint, s.pool.Name)
			return err
		}
	}

	err := s.createImageDbPoolVolume(fingerprint)
	if err != nil {
		logger.Errorf("failed to create db entry for RBD storage volume for image \"%s\" on storage pool \"%s\": %s", fingerprint, s.pool.Name, err)
		return err
	}

	logger.Debugf("Created RBD storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return nil
}

func (s *storageCeph) ImageDelete(fingerprint string) error {
	logger.Debugf("Deleting RBD storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

	// try to umount but don't fail
	s.ImageUmount(fingerprint)

	// check if image has dependent snapshots
	_, err := cephRBDSnapshotListClones(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage, "readonly")
	if err != nil {
		if err != NoSuchObjectError {
			logger.Errorf("Failed to list clones of RBD storage volume for image \"%s\" on storage pool \"%s\": %s", fingerprint, s.pool.Name, err)
			return err
		}

		// unprotect snapshot
		err = cephRBDSnapshotUnprotect(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage, "readonly")
		if err != nil {
			logger.Errorf("Failed to unprotect snapshot for RBD storage volume for image \"%s\" on storage pool \"%s\": %s", fingerprint, s.pool.Name, err)
			return err
		}

		// delete snapshots
		err = cephRBDSnapshotsPurge(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage)
		if err != nil {
			logger.Errorf("Failed to delete snapshot for RBD storage volume for image \"%s\" on storage pool \"%s\": %s", fingerprint, s.pool.Name, err)
			return err
		}

		// unmap
		err = cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage)
		if err != nil {
			logger.Errorf("Failed to unmap RBD storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
			return err
		}

		// delete volume
		err = cephRBDVolumeDelete(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage)
		if err != nil {
			logger.Errorf("Failed to delete RBD storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
			return err
		}
	} else {
		// unmap
		err = cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage)
		if err != nil {
			logger.Errorf("Failed to unmap RBD storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
			return err
		}

		// mark deleted
		err := cephRBDVolumeMarkDeleted(s.ClusterName, s.OSDPoolName, fingerprint, storagePoolVolumeTypeNameImage)
		if err != nil {
			logger.Errorf("Failed to mark RBD storage volume for image \"%s\" on storage pool \"%s\" deleted", fingerprint, s.pool.Name)
			return err
		}
	}

	err = s.deleteImageDbPoolVolume(fingerprint)
	if err != nil {
		logger.Errorf("Failed to delete db entry for RBD storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
		return err
	}

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if shared.PathExists(imageMntPoint) {
		err := os.Remove(imageMntPoint)
		if err != nil {
			logger.Errorf("Failed to delete image mountpoint for RBD storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
			return err
		}
	}

	logger.Debugf("Deleted RBD storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return nil
}

func (s *storageCeph) ImageMount(fingerprint string) (bool, error) {
	logger.Debugf("Mounting RBD storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if shared.IsMountPoint(imageMntPoint) {
		return false, nil
	}

	RBDFilesystem := s.getRBDFilesystem()
	RBDMountOptions := s.getRBDMountOptions()
	mountFlags, mountOptions := lxdResolveMountoptions(RBDMountOptions)
	RBDDevPath := getRBDDevPath(s.OSDPoolName, storagePoolVolumeTypeNameImage, fingerprint)
	err := tryMount(RBDDevPath, imageMntPoint, RBDFilesystem, mountFlags, mountOptions)
	if err != nil {
		logger.Errorf("Failed to mount RBD device %s onto %s: %s", RBDDevPath, imageMntPoint, err)
		return false, err
	}

	logger.Debugf("Mounted RBD storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return true, nil
}

func (s *storageCeph) ImageUmount(fingerprint string) (bool, error) {
	logger.Debugf("Unmounting RBD storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if !shared.IsMountPoint(imageMntPoint) {
		return false, nil
	}

	err := tryUnmount(imageMntPoint, 0)
	if err != nil {
		return false, err
	}

	logger.Debugf("Unmounted RBD storage volume for image \"%s\" on storage pool \"%s\".", fingerprint, s.pool.Name)
	return true, nil
}

func (s *storageCeph) MigrationType() MigrationFSType {
	return MigrationFSType_RSYNC
}

func (s *storageCeph) PreservesInodes() bool {
	return false
}

func (s *storageCeph) MigrationSource(container container, containerOnly bool) (MigrationStorageSourceDriver, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *storageCeph) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation, containerOnly bool) error {
	return nil
}

func (s *storageCeph) StorageEntitySetQuota(volumeType int, size int64, data interface{}) error {
	return fmt.Errorf("RBD storage volume quota are not supported")
}
