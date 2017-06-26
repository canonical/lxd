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

func (s *storageCeph) ContainerCreateFromImage(
	container container, imageFingerprint string) error {

	return nil
}

func (s *storageCeph) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageCeph) ContainerDelete(container container) error {
	return nil
}

func (s *storageCeph) ContainerCopy(target container, source container, containerOnly bool) error {
	return nil
}

func (s *storageCeph) ContainerMount(c container) (bool, error) {
	return true, nil
}

func (s *storageCeph) ContainerUmount(name string, path string) (bool, error) {
	return true, nil
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
func (s *storageCeph) ContainerSnapshotCreate(
	snapshotContainer container, sourceContainer container) error {

	return nil
}
func (s *storageCeph) ContainerSnapshotDelete(
	snapshotContainer container) error {

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
	return nil
}

func (s *storageCeph) ImageDelete(fingerprint string) error {
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
