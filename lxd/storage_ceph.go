package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"

	"github.com/pborman/uuid"
)

type storageCeph struct {
	ClusterName string
	OSDPoolName string
	UserName    string
	PGNum       string
	storageShared
}

var cephVersion = ""

func (s *storageCeph) StorageCoreInit() error {
	s.sType = storageTypeCeph
	typeName, err := storageTypeToString(s.sType)
	if err != nil {
		return err
	}
	s.sTypeName = typeName

	if cephVersion != "" {
		s.sTypeVersion = cephVersion
		return nil
	}

	msg, err := shared.RunCommand("rbd", "--version")
	if err != nil {
		return fmt.Errorf("Error getting CEPH version: %s", err)
	}
	s.sTypeVersion = strings.TrimSpace(msg)
	cephVersion = s.sTypeVersion

	return nil
}

func (s *storageCeph) StoragePoolInit() error {
	var err error

	err = s.StorageCoreInit()
	if err != nil {
		return errors.Wrap(err, "Storage pool init")
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
	}

	// set ceph user name
	if s.pool.Config["ceph.user.name"] != "" {
		s.UserName = s.pool.Config["ceph.user.name"]
	} else {
		s.UserName = "admin"
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
	logger.Debugf(`Checking CEPH storage pool "%s" (noop)`, s.pool.Name)
	logger.Debugf(`Checked CEPH storage pool "%s" (noop)`, s.pool.Name)
	return nil
}

func (s *storageCeph) StoragePoolCreate() error {
	logger.Infof(`Creating CEPH OSD storage pool "%s" in cluster "%s"`,
		s.pool.Name, s.ClusterName)

	revert := true

	s.pool.Config["volatile.initial_source"] = s.pool.Config["source"]

	// sanity check
	if s.pool.Config["source"] != "" &&
		s.pool.Config["ceph.osd.pool_name"] != "" &&
		s.pool.Config["source"] != s.pool.Config["ceph.osd.pool_name"] {
		msg := fmt.Sprintf(`The "source" and "ceph.osd.pool_name" ` +
			`property must not differ for CEPH OSD storage pools`)
		logger.Errorf(msg)
		return fmt.Errorf(msg)
	}

	// use an existing OSD pool
	if s.pool.Config["source"] != "" {
		s.OSDPoolName = s.pool.Config["source"]
		s.pool.Config["ceph.osd.pool_name"] = s.pool.Config["source"]
	}

	if s.pool.Config["ceph.osd.pool_name"] == "" {
		s.pool.Config["ceph.osd.pool_name"] = s.pool.Name
		s.pool.Config["source"] = s.pool.Name
		s.OSDPoolName = s.pool.Name
	}

	if !cephOSDPoolExists(s.ClusterName, s.OSDPoolName, s.UserName) {
		logger.Debugf(`CEPH OSD storage pool "%s" does not exist`, s.OSDPoolName)

		// create new osd pool
		msg, err := shared.TryRunCommand("ceph", "--name",
			fmt.Sprintf("client.%s", s.UserName), "--cluster",
			s.ClusterName, "osd", "pool", "create", s.OSDPoolName,
			s.PGNum)
		if err != nil {
			logger.Errorf(`Failed to create CEPH osd storage pool "%s" in cluster "%s": %s`, s.OSDPoolName, s.ClusterName, msg)
			return err
		}
		logger.Debugf(`Created CEPH osd storage pool "%s" in cluster "%s"`, s.OSDPoolName, s.ClusterName)

		defer func() {
			if !revert {
				return
			}

			err := cephOSDPoolDestroy(s.ClusterName, s.OSDPoolName,
				s.UserName)
			if err != nil {
				logger.Warnf(`Failed to delete ceph storage pool "%s" in cluster "%s": %s`, s.OSDPoolName, s.ClusterName, err)
			}
		}()

	} else {
		logger.Debugf(`CEPH OSD storage pool "%s" does exist`, s.OSDPoolName)

		// use existing osd pool
		msg, err := shared.RunCommand("ceph", "--name",
			fmt.Sprintf("client.%s", s.UserName),
			"--cluster", s.ClusterName, "osd", "pool", "get",
			s.OSDPoolName, "pg_num")
		if err != nil {
			logger.Errorf(`Failed to retrieve number of placement groups for CEPH osd storage pool "%s" in cluster "%s": %s`, s.OSDPoolName, s.ClusterName, msg)
			return err
		}
		logger.Debugf(`Retrieved number of placement groups or CEPH osd storage pool "%s" in cluster "%s"`, s.OSDPoolName, s.ClusterName)

		idx := strings.Index(msg, "pg_num:")
		if idx == -1 {
			logger.Errorf(`Failed to parse number of placement groups for CEPH osd storage pool "%s" in cluster "%s": %s`, s.OSDPoolName, s.ClusterName, msg)
		}

		msg = msg[(idx + len("pg_num:")):]
		msg = strings.TrimSpace(msg)
		// It is ok to update the pool configuration since storage pool
		// creation via API is implemented such that the storage pool is
		// checked for a changed config after this function returns and
		// if so the db for it is updated.
		s.pool.Config["ceph.osd.pg_num"] = msg
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
	err := os.MkdirAll(poolMntPoint, 0711)
	if err != nil {
		logger.Errorf(`Failed to create mountpoint "%s" for ceph storage pool "%s" in cluster "%s": %s`, poolMntPoint, s.OSDPoolName, s.ClusterName, err)
		return err
	}
	logger.Debugf(`Created mountpoint "%s" for ceph storage pool "%s" in cluster "%s"`, poolMntPoint, s.OSDPoolName, s.ClusterName)

	defer func() {
		if !revert {
			return
		}

		err := os.Remove(poolMntPoint)
		if err != nil {
			logger.Errorf(`Failed to delete mountpoint "%s" for ceph storage pool "%s" in cluster "%s": %s`, poolMntPoint, s.OSDPoolName, s.ClusterName, err)
		}
	}()

	ok := cephRBDVolumeExists(s.ClusterName, s.OSDPoolName, s.OSDPoolName,
		"lxd", s.UserName)
	s.pool.Config["volatile.pool.pristine"] = "false"
	if !ok {
		s.pool.Config["volatile.pool.pristine"] = "true"
		// Create dummy storage volume. Other LXD instances will use
		// this to detect whether this osd pool is already in use by
		// another LXD instance.
		err = cephRBDVolumeCreate(s.ClusterName, s.OSDPoolName,
			s.OSDPoolName, "lxd", "0", s.UserName)
		if err != nil {
			logger.Errorf(`Failed to create RBD storage volume "%s" on storage pool "%s": %s`, s.pool.Name, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Created RBD storage volume "%s" on storage pool "%s"`, s.pool.Name, s.pool.Name)
	} else {
		msg := fmt.Sprintf(`CEPH OSD storage pool "%s" in cluster `+
			`"%s" seems to be in use by another LXD instace`,
			s.pool.Name, s.ClusterName)
		if s.pool.Config["ceph.osd.force_reuse"] == "" ||
			!shared.IsTrue(s.pool.Config["ceph.osd.force_reuse"]) {
			msg += `. Set "ceph.osd.force_reuse=true" to force ` +
				`LXD to reuse the pool`
			logger.Errorf(msg)
			return fmt.Errorf(msg)
		}
		logger.Warnf(msg)
	}

	logger.Infof(`Created CEPH OSD storage pool "%s" in cluster "%s"`,
		s.pool.Name, s.ClusterName)

	revert = false

	return nil
}

func (s *storageCeph) StoragePoolDelete() error {
	logger.Infof(`Deleting CEPH OSD storage pool "%s" in cluster "%s"`,
		s.pool.Name, s.ClusterName)

	// test if pool exists
	poolExists := cephOSDPoolExists(s.ClusterName, s.OSDPoolName, s.UserName)
	if !poolExists {
		logger.Warnf(`CEPH osd storage pool "%s" does not exist in cluster "%s"`, s.OSDPoolName, s.ClusterName)
	}

	// Check whether we own the pool and only remove in this case.
	if s.pool.Config["volatile.pool.pristine"] != "" &&
		shared.IsTrue(s.pool.Config["volatile.pool.pristine"]) {
		logger.Debugf(`Detected that this LXD instance is the owner of the CEPH osd storage pool "%s" in cluster "%s"`, s.OSDPoolName, s.ClusterName)

		// Delete the osd pool.
		if poolExists {
			err := cephOSDPoolDestroy(s.ClusterName, s.OSDPoolName,
				s.UserName)
			if err != nil {
				logger.Errorf(`Failed to delete CEPH OSD storage pool "%s" in cluster "%s": %s`, s.pool.Name, s.ClusterName, err)
				return err
			}
		}
		logger.Debugf(`Deleted CEPH OSD storage pool "%s" in cluster "%s"`,
			s.pool.Name, s.ClusterName)
	}

	// Delete the mountpoint for the storage pool.
	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	if shared.PathExists(poolMntPoint) {
		err := os.RemoveAll(poolMntPoint)
		if err != nil {
			logger.Errorf(`Failed to delete mountpoint "%s" for CEPH osd storage pool "%s" in cluster "%s": %s`, poolMntPoint, s.OSDPoolName, s.ClusterName, err)
			return err
		}
		logger.Debugf(`Deleted mountpoint "%s" for CEPH osd storage pool "%s" in cluster "%s"`, poolMntPoint, s.OSDPoolName, s.ClusterName)
	}

	logger.Infof(`Deleted CEPH OSD storage pool "%s" in cluster "%s"`,
		s.pool.Name, s.ClusterName)
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
	return s.volume.Writable()
}

func (s *storageCeph) SetStoragePoolWritable(writable *api.StoragePoolPut) {
	s.pool.StoragePoolPut = *writable
}

func (s *storageCeph) SetStoragePoolVolumeWritable(writable *api.StorageVolumePut) {
	s.volume.StorageVolumePut = *writable
}

func (s *storageCeph) GetContainerPoolInfo() (int64, string, string) {
	return s.poolID, s.pool.Name, s.OSDPoolName
}

func (s *storageCeph) StoragePoolVolumeCreate() error {
	logger.Debugf(`Creating RBD storage volume "%s" on storage pool "%s"`,
		s.volume.Name, s.pool.Name)

	revert := true

	// get size
	RBDSize, err := s.getRBDSize()
	if err != nil {
		logger.Errorf(`Failed to retrieve size of RBD storage volume "%s" on storage pool "%s": %s`, s.volume.Name, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Retrieved size "%s" of RBD storage volume "%s" on storage pool "%s"`, RBDSize, s.volume.Name, s.pool.Name)

	// create volume
	err = cephRBDVolumeCreate(s.ClusterName, s.OSDPoolName, s.volume.Name,
		storagePoolVolumeTypeNameCustom, RBDSize, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to create RBD storage volume "%s" on storage pool "%s": %s`, s.volume.Name, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Created RBD storage volume "%s" on storage pool "%s"`,
		s.volume.Name, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		err := cephRBDVolumeDelete(s.ClusterName, s.OSDPoolName,
			s.volume.Name, storagePoolVolumeTypeNameCustom, s.UserName)
		if err != nil {
			logger.Warnf(`Failed to delete RBD storage volume "%s" on storage pool "%s": %s`, s.volume.Name, s.pool.Name, err)
		}
	}()

	RBDDevPath, err := cephRBDVolumeMap(s.ClusterName, s.OSDPoolName,
		s.volume.Name, storagePoolVolumeTypeNameCustom, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to map RBD storage volume for "%s" on storage pool "%s": %s`, s.volume.Name, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Mapped RBD storage volume for "%s" on storage pool "%s"`,
		s.volume.Name, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		err := cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName,
			s.volume.Name, storagePoolVolumeTypeNameCustom,
			s.UserName, true)
		if err != nil {
			logger.Warnf(`Failed to unmap RBD storage volume "%s" on storage pool "%s": %s`, s.volume.Name, s.pool.Name, err)
		}
	}()

	// get filesystem
	RBDFilesystem := s.getRBDFilesystem()
	logger.Debugf(`Retrieved filesystem type "%s" of RBD storage volume "%s" on storage pool "%s"`, RBDFilesystem, s.volume.Name, s.pool.Name)

	msg, err := makeFSType(RBDDevPath, RBDFilesystem, nil)
	if err != nil {
		logger.Errorf(`Failed to create filesystem type "%s" on device path "%s" for RBD storage volume "%s" on storage pool "%s": %s`, RBDFilesystem, RBDDevPath, s.volume.Name, s.pool.Name, msg)
		return err
	}
	logger.Debugf(`Created filesystem type "%s" on device path "%s" for RBD storage volume "%s" on storage pool "%s"`, RBDFilesystem, RBDDevPath, s.volume.Name, s.pool.Name)

	volumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	err = os.MkdirAll(volumeMntPoint, 0711)
	if err != nil {
		logger.Errorf(`Failed to create mountpoint "%s" for RBD storage volume "%s" on storage pool "%s": %s"`, volumeMntPoint, s.volume.Name, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Created mountpoint "%s" for RBD storage volume "%s" on storage pool "%s"`, volumeMntPoint, s.volume.Name, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		err := os.Remove(volumeMntPoint)
		if err != nil {
			logger.Warnf(`Failed to delete mountpoint "%s" for RBD storage volume "%s" on storage pool "%s": %s"`, volumeMntPoint, s.volume.Name, s.pool.Name, err)
		}
	}()

	// Apply quota
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

	logger.Debugf(`Created RBD storage volume "%s" on storage pool "%s"`,
		s.volume.Name, s.pool.Name)

	revert = false

	return nil
}

func (s *storageCeph) StoragePoolVolumeDelete() error {
	logger.Debugf(`Deleting RBD storage volume "%s" on storage pool "%s"`,
		s.volume.Name, s.pool.Name)

	volumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	if shared.IsMountPoint(volumeMntPoint) {
		err := tryUnmount(volumeMntPoint, syscall.MNT_DETACH)
		if err != nil {
			logger.Errorf(`Failed to unmount RBD storage volume "%s" on storage pool "%s": %s`, s.volume.Name, s.pool.Name, err)
		}
		logger.Debugf(`Unmounted RBD storage volume "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)
	}

	rbdVolumeExists := cephRBDVolumeExists(s.ClusterName, s.OSDPoolName,
		s.volume.Name, storagePoolVolumeTypeNameCustom, s.UserName)

	// delete
	if rbdVolumeExists {
		ret := cephContainerDelete(s.ClusterName, s.OSDPoolName, s.volume.Name,
			storagePoolVolumeTypeNameCustom, s.UserName)
		if ret < 0 {
			msg := fmt.Sprintf(`Failed to delete RBD storage volume "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)
			logger.Errorf(msg)
			return fmt.Errorf(msg)
		}
		logger.Debugf(`Deleted RBD storage volume "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)
	}

	err := s.s.Cluster.StoragePoolVolumeDelete(
		"default",
		s.volume.Name,
		storagePoolVolumeTypeCustom,
		s.poolID)
	if err != nil {
		logger.Errorf(`Failed to delete database entry for RBD storage volume "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)
	}
	logger.Debugf(`Deleted database entry for RBD storage volume "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)

	if shared.PathExists(volumeMntPoint) {
		err = os.Remove(volumeMntPoint)
		if err != nil {
			logger.Errorf(`Failed to delete mountpoint "%s" for RBD storage volume "%s" on storage pool "%s": %s"`, volumeMntPoint, s.volume.Name, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Deleted mountpoint "%s" for RBD storage volume "%s" on storage pool "%s"`, volumeMntPoint, s.volume.Name, s.pool.Name)
	}

	logger.Debugf(`Deleted RBD storage volume "%s" on storage pool "%s"`,
		s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageCeph) StoragePoolVolumeMount() (bool, error) {
	logger.Debugf(`Mounting RBD storage volume "%s" on storage pool "%s"`,
		s.volume.Name, s.pool.Name)

	RBDFilesystem := s.getRBDFilesystem()
	volumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	customMountLockID := getCustomMountLockID(s.pool.Name, s.volume.Name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[customMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf(`Received value over semaphore. This should not have happened`)
		}
		// Give the benefit of the doubt and assume that the other
		// thread actually succeeded in mounting the storage volume.
		logger.Debugf(`RBD storage volume "%s" on storage pool "%s" appears to be already mounted`, s.volume.Name, s.pool.Name)
		return false, nil
	}

	lxdStorageOngoingOperationMap[customMountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	var ret int
	var customerr error
	ourMount := false
	RBDDevPath := ""
	if !shared.IsMountPoint(volumeMntPoint) {
		RBDDevPath, ret = getRBDMappedDevPath(s.ClusterName, s.OSDPoolName,
			storagePoolVolumeTypeNameCustom, s.volume.Name, true,
			s.UserName)
		mountFlags, mountOptions := lxdResolveMountoptions(s.getRBDMountOptions())
		customerr = tryMount(
			RBDDevPath,
			volumeMntPoint,
			RBDFilesystem,
			mountFlags,
			mountOptions)
		ourMount = true
	}

	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[customMountLockID]; ok {
		close(waitChannel)
		delete(lxdStorageOngoingOperationMap, customMountLockID)
	}
	lxdStorageMapLock.Unlock()

	if customerr != nil || ret < 0 {
		logger.Errorf(`Failed to mount RBD storage volume "%s" on storage pool "%s": %s`, s.volume.Name, s.pool.Name, customerr)
		return false, customerr
	}

	logger.Debugf(`Mounted RBD storage volume "%s" on storage pool "%s"`,
		s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageCeph) StoragePoolVolumeUmount() (bool, error) {
	logger.Debugf(`Unmounting RBD storage volume "%s" on storage pool "%s"`,
		s.volume.Name, s.pool.Name)

	volumeMntPoint := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)

	customMountLockID := getCustomUmountLockID(s.pool.Name, s.volume.Name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[customMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf(`Received value over semaphore. This should not have happened`)
		}
		// Give the benefit of the doubt and assume that the other
		// thread actually succeeded in unmounting the storage volume.
		logger.Debugf(`RBD storage volume "%s" on storage pool "%s" appears to be already unmounted`, s.volume.Name, s.pool.Name)
		return false, nil
	}

	lxdStorageOngoingOperationMap[customMountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	var customerr error
	ourUmount := false
	if shared.IsMountPoint(volumeMntPoint) {
		customerr = tryUnmount(volumeMntPoint, syscall.MNT_DETACH)
		ourUmount = true
		logger.Debugf(`Path "%s" is a mountpoint for RBD storage volume "%s" on storage pool "%s"`, volumeMntPoint, s.volume.Name, s.pool.Name)
	}

	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[customMountLockID]; ok {
		close(waitChannel)
		delete(lxdStorageOngoingOperationMap, customMountLockID)
	}
	lxdStorageMapLock.Unlock()

	if customerr != nil {
		logger.Errorf(`Failed to unmount RBD storage volume "%s" on storage pool "%s": %s`, s.volume.Name, s.pool.Name, customerr)
		return false, customerr
	}

	logger.Debugf(`Unmounted RBD storage volume "%s" on storage pool "%s"`,
		s.volume.Name, s.pool.Name)
	return ourUmount, nil
}

func (s *storageCeph) StoragePoolVolumeUpdate(writable *api.StorageVolumePut, changedConfig []string) error {
	if writable.Restore != "" {
		logger.Infof(`Restoring CEPH storage volume "%s" from snapshot "%s"`,
			s.volume.Name, writable.Restore)

		ourUmount, err := s.StoragePoolVolumeUmount()
		if err != nil {
			return err
		}
		if ourUmount {
			defer s.StoragePoolVolumeMount()
		}

		prefixedSourceSnapOnlyName := fmt.Sprintf("snapshot_%s", writable.Restore)
		err = cephRBDVolumeRestore(s.ClusterName, s.OSDPoolName,
			s.volume.Name, storagePoolVolumeTypeNameCustom,
			prefixedSourceSnapOnlyName, s.UserName)
		if err != nil {
			return err
		}

		logger.Infof(`Restored CEPH storage volume "%s" from snapshot "%s"`,
			s.volume.Name, writable.Restore)
		return nil
	}

	logger.Infof(`Updating CEPH storage volume "%s"`, s.volume.Name)

	changeable := changeableStoragePoolVolumeProperties["ceph"]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		return updateStoragePoolVolumeError(unchangeable, "ceph")
	}

	if shared.StringInSlice("size", changedConfig) {
		if s.volume.Type != storagePoolVolumeTypeNameCustom {
			return updateStoragePoolVolumeError([]string{"size"}, "ceph")
		}

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

	logger.Infof(`Updated CEPH storage volume "%s"`, s.volume.Name)
	return nil
}

func (s *storageCeph) StoragePoolVolumeRename(newName string) error {
	logger.Infof(`Renaming CEPH storage volume on OSD storage pool "%s" from "%s" to "%s`,
		s.pool.Name, s.volume.Name, newName)

	_, err := s.StoragePoolVolumeUmount()
	if err != nil {
		return err
	}

	usedBy, err := storagePoolVolumeUsedByContainersGet(s.s, "default", s.volume.Name, storagePoolVolumeTypeNameCustom)
	if err != nil {
		return err
	}
	if len(usedBy) > 0 {
		return fmt.Errorf(`RBD storage volume "%s" on CEPH OSD storage pool "%s" is attached to containers`,
			s.volume.Name, s.pool.Name)
	}

	// unmap
	err = cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName,
		s.volume.Name, storagePoolVolumeTypeNameCustom,
		s.UserName, true)
	if err != nil {
		logger.Errorf(`Failed to unmap RBD storage volume for container "%s" on storage pool "%s": %s`, s.volume.Name, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Unmapped RBD storage volume for container "%s" on storage pool "%s"`,
		s.volume.Name, s.pool.Name)

	err = cephRBDVolumeRename(s.ClusterName, s.OSDPoolName,
		storagePoolVolumeTypeNameCustom, s.volume.Name,
		newName, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to rename RBD storage volume for container "%s" on storage pool "%s": %s`,
			s.volume.Name, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Renamed RBD storage volume for container "%s" on storage pool "%s"`,
		s.volume.Name, s.pool.Name)

	// map
	_, err = cephRBDVolumeMap(s.ClusterName, s.OSDPoolName,
		newName, storagePoolVolumeTypeNameCustom,
		s.UserName)
	if err != nil {
		logger.Errorf(`Failed to map RBD storage volume for container "%s" on storage pool "%s": %s`,
			newName, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Mapped RBD storage volume for container "%s" on storage pool "%s"`,
		newName, s.pool.Name)

	isSnapshot := strings.Contains(s.volume.Name, "/")

	var oldPath string
	var newPath string

	if isSnapshot {
		oldPath = getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
		newPath = getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, newName)
	} else {
		oldPath = getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
		newPath = getStoragePoolVolumeMountPoint(s.pool.Name, newName)
	}

	err = os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	logger.Infof(`Renamed CEPH storage volume on OSD storage pool "%s" from "%s" to "%s`,
		s.pool.Name, s.volume.Name, newName)

	return s.s.Cluster.StoragePoolVolumeRename("default", s.volume.Name, newName,
		storagePoolVolumeTypeCustom, s.poolID)
}

func (s *storageCeph) StoragePoolUpdate(writable *api.StoragePoolPut, changedConfig []string) error {
	logger.Infof(`Updating CEPH storage pool "%s"`, s.pool.Name)

	changeable := changeableStoragePoolProperties["ceph"]
	unchangeable := []string{}
	for _, change := range changedConfig {
		if !shared.StringInSlice(change, changeable) {
			unchangeable = append(unchangeable, change)
		}
	}

	if len(unchangeable) > 0 {
		return updateStoragePoolError(unchangeable, "ceph")
	}

	// "rsync.bwlimit" requires no on-disk modifications.
	// "volume.block.filesystem" requires no on-disk modifications.
	// "volume.block.mount_options" requires no on-disk modifications.
	// "volume.size" requires no on-disk modifications.

	logger.Infof(`Updated CEPH storage pool "%s"`, s.pool.Name)
	return nil
}

func (s *storageCeph) ContainerStorageReady(container container) bool {
	name := container.Name()
	logger.Debugf(`Checking if RBD storage volume for container "%s" on storage pool "%s" is ready`, name, s.pool.Name)

	ok := cephRBDVolumeExists(s.ClusterName, s.OSDPoolName, projectPrefix(container.Project(), name),
		storagePoolVolumeTypeNameContainer, s.UserName)
	if !ok {
		logger.Debugf(`RBD storage volume for container "%s" on storage pool "%s" does not exist`, name, s.pool.Name)
		return false
	}

	logger.Debugf(`RBD storage volume for container "%s" on storage pool "%s" is ready`, name, s.pool.Name)
	return true
}

func (s *storageCeph) ContainerCreate(container container) error {
	containerName := container.Name()
	err := s.doContainerCreate(container.Project(), containerName, container.IsPrivileged())
	if err != nil {
		return err
	}

	err = container.TemplateApply("create")
	if err != nil {
		logger.Errorf(`Failed to apply create template for container "%s": %s`, containerName, err)
		return err
	}
	logger.Debugf(`Applied create template for container "%s"`,
		containerName)

	logger.Debugf(`Created RBD storage volume for container "%s" on storage pool "%s"`, containerName, s.pool.Name)
	return nil
}

func (s *storageCeph) ContainerCreateFromImage(container container, fingerprint string) error {
	logger.Debugf(`Creating RBD storage volume for container "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)

	revert := true

	containerPath := container.Path()
	containerName := container.Name()
	containerPoolVolumeMntPoint := getContainerMountPoint(container.Project(), s.pool.Name,
		containerName)

	imageStoragePoolLockID := getImageCreateLockID(s.pool.Name, fingerprint)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[imageStoragePoolLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf(`Received value over semaphore. This should not have happened`)
		}
	} else {
		lxdStorageOngoingOperationMap[imageStoragePoolLockID] = make(chan bool)
		lxdStorageMapLock.Unlock()

		var imgerr error
		ok := cephRBDVolumeExists(s.ClusterName, s.OSDPoolName,
			fingerprint, storagePoolVolumeTypeNameImage, s.UserName)

		if ok {
			_, volume, err := s.s.Cluster.StoragePoolNodeVolumeGetType(fingerprint, db.StoragePoolVolumeTypeImage, s.poolID)
			if err != nil {
				return err
			}
			if volume.Config["block.filesystem"] != s.getRBDFilesystem() {
				// The storage pool volume.blockfilesystem property has changed, re-import the image
				err := s.ImageDelete(fingerprint)
				if err != nil {
					return err
				}
				ok = false
			}
		}

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

	volumeName := projectPrefix(container.Project(), containerName)
	err := cephRBDCloneCreate(s.ClusterName, s.OSDPoolName, fingerprint,
		storagePoolVolumeTypeNameImage, "readonly", s.OSDPoolName,
		volumeName, storagePoolVolumeTypeNameContainer, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to clone new RBD storage volume for container "%s": %s`, containerName, err)
		return err
	}
	logger.Debugf(`Cloned new RBD storage volume for container "%s"`,
		containerName)

	defer func() {
		if !revert {
			return
		}

		err := cephRBDVolumeDelete(s.ClusterName, s.OSDPoolName,
			containerName, storagePoolVolumeTypeNameContainer,
			s.UserName)
		if err != nil {
			logger.Warnf(`Failed to delete RBD storage volume for container "%s": %s`, containerName, err)
		}
	}()

	RBDDevPath, err := cephRBDVolumeMap(s.ClusterName, s.OSDPoolName,
		volumeName, storagePoolVolumeTypeNameContainer, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to map RBD storage volume for container "%s"`, containerName)
		return err
	}
	logger.Debugf(`Mapped RBD storage volume for container "%s"`,
		containerName)

	defer func() {
		if !revert {
			return
		}

		err := cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName,
			volumeName, storagePoolVolumeTypeNameContainer,
			s.UserName, true)
		if err != nil {
			logger.Warnf(`Failed to unmap RBD storage volume for container "%s": %s`, containerName, err)
		}
	}()

	// Generate a new xfs's UUID
	RBDFilesystem := s.getRBDFilesystem()

	msg, err := fsGenerateNewUUID(RBDFilesystem, RBDDevPath)
	if err != nil {
		logger.Errorf("Failed to create new \"%s\" UUID for container \"%s\" on storage pool \"%s\": %s", RBDFilesystem, containerName, s.pool.Name, msg)
		return err
	}

	privileged := container.IsPrivileged()
	err = createContainerMountpoint(containerPoolVolumeMntPoint,
		containerPath, privileged)
	if err != nil {
		logger.Errorf(`Failed to create mountpoint "%s" for container "%s" for RBD storage volume: %s`, containerPoolVolumeMntPoint, containerName, err)
		return err
	}
	logger.Debugf(`Created mountpoint "%s" for container "%s" for RBD storage volume`, containerPoolVolumeMntPoint, containerName)

	defer func() {
		if !revert {
			return
		}

		err := os.Remove(containerPoolVolumeMntPoint)
		if err != nil {
			logger.Warnf(`Failed to delete mountpoint "%s" for container "%s" for RBD storage volume: %s`, containerPoolVolumeMntPoint, containerName, err)
		}
	}()

	// Apply quota
	_, imageVol, err := s.s.Cluster.StoragePoolNodeVolumeGetType(fingerprint, db.StoragePoolVolumeTypeImage, s.poolID)
	if err != nil {
		return err
	}

	if s.volume.Config["size"] != "" && imageVol.Config["size"] != s.volume.Config["size"] {
		size, err := shared.ParseByteSizeString(s.volume.Config["size"])
		if err != nil {
			return err
		}

		newSize := s.volume.Config["size"]
		s.volume.Config["size"] = imageVol.Config["size"]
		err = s.StorageEntitySetQuota(storagePoolVolumeTypeContainer, size, container)
		if err != nil {
			return err
		}
		s.volume.Config["size"] = newSize
	}

	// Shift if needed
	ourMount, err := s.ContainerMount(container)
	if err != nil {
		return err
	}
	if ourMount {
		defer s.ContainerUmount(container, containerPath)
	}

	if !privileged {
		err := s.shiftRootfs(container, nil)
		if err != nil {
			logger.Errorf(`Failed to shift rootfs for container "%s": %s`, containerName, err)
			return err
		}
		logger.Debugf(`Shifted rootfs for container "%s"`, containerName)

		err = os.Chmod(containerPoolVolumeMntPoint, 0711)
		if err != nil {
			logger.Errorf(`Failed change mountpoint "%s" permissions to 0711 for container "%s" for RBD storage volume: %s`, containerPoolVolumeMntPoint, containerName, err)
			return err
		}
		logger.Debugf(`Changed mountpoint "%s" permissions to 0711 for container "%s" for RBD storage volume`, containerPoolVolumeMntPoint, containerName)
	} else {
		err := os.Chmod(containerPoolVolumeMntPoint, 0700)
		if err != nil {
			logger.Errorf(`Failed change mountpoint "%s" permissions to 0700 for container "%s" for RBD storage volume: %s`, containerPoolVolumeMntPoint, containerName, err)
			return err
		}
		logger.Debugf(`Changed mountpoint "%s" permissions to 0700 for container "%s" for RBD storage volume`, containerPoolVolumeMntPoint, containerName)
	}

	err = container.TemplateApply("create")
	if err != nil {
		logger.Errorf(`Failed to apply create template for container "%s": %s`, containerName, err)
		return err
	}
	logger.Debugf(`Applied create template for container "%s"`,
		containerName)

	logger.Debugf(`Created RBD storage volume for container "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)

	revert = false

	return nil
}

func (s *storageCeph) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageCeph) ContainerDelete(container container) error {
	containerName := container.Name()
	logger.Debugf(`Deleting RBD storage volume for container "%s" on storage pool "%s"`, containerName, s.pool.Name)

	// umount
	containerPath := container.Path()
	containerMntPoint := getContainerMountPoint(container.Project(), s.pool.Name, containerName)
	if shared.PathExists(containerMntPoint) {
		_, err := s.ContainerUmount(container, containerPath)
		if err != nil {
			logger.Errorf("Failed to unmount RBD storage volume for container %q on storage pool %q: %v", containerName, s.pool.Name, err)
			return err
		}
	}

	volumeName := projectPrefix(container.Project(), containerName)
	rbdVolumeExists := cephRBDVolumeExists(s.ClusterName, s.OSDPoolName,
		volumeName, storagePoolVolumeTypeNameContainer, s.UserName)

	// delete
	if rbdVolumeExists {
		ret := cephContainerDelete(s.ClusterName, s.OSDPoolName, volumeName,
			storagePoolVolumeTypeNameContainer, s.UserName)
		if ret < 0 {
			msg := fmt.Sprintf(`Failed to delete RBD storage volume for `+
				`container "%s" on storage pool "%s"`, containerName, s.pool.Name)
			logger.Errorf(msg)
			return fmt.Errorf(msg)
		}
	}

	err := deleteContainerMountpoint(containerMntPoint, containerPath,
		s.GetStorageTypeName())
	if err != nil {
		logger.Errorf(`Failed to delete mountpoint %s for RBD storage volume of container "%s" for RBD storage volume on storage pool "%s": %s`, containerMntPoint,
			containerName, s.pool.Name, err)
		return err
	}

	logger.Debugf(`Deleted RBD storage volume for container "%s" on storage pool "%s"`, containerName, s.pool.Name)
	return nil
}

// This function recreates an rbd container including its snapshots. It
// recreates the dependencies between the container and the snapshots:
// - create an empty rbd storage volume
// - for each snapshot dump the contents into the empty storage volume and
//   after each dump take a snapshot of the rbd storage volume
// - dump the container contents into the rbd storage volume.
func (s *storageCeph) doCrossPoolContainerCopy(target container, source container, containerOnly bool, refresh bool, refreshSnapshots []container) error {
	sourcePool, err := source.StoragePool()
	if err != nil {
		return err
	}

	// setup storage for the source volume
	srcStorage, err := storagePoolVolumeInit(s.s, source.Project(), sourcePool, source.Name(), storagePoolVolumeTypeContainer)
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

	targetPool, err := target.StoragePool()
	if err != nil {
		return err
	}

	var snapshots []container

	if refresh {
		snapshots = refreshSnapshots
	} else {
		snapshots, err = source.Snapshots()
		if err != nil {
			return err
		}

		// create the main container
		err = s.doContainerCreate(target.Project(), target.Name(), target.IsPrivileged())
		if err != nil {
			return err
		}
	}

	// mount container
	_, err = s.doContainerMount(target.Project(), target.Name())
	if err != nil {
		return err
	}

	destContainerMntPoint := getContainerMountPoint(target.Project(), targetPool, target.Name())
	bwlimit := s.pool.Config["rsync.bwlimit"]
	// Extract container
	if !containerOnly {
		for _, snap := range snapshots {
			srcSnapshotMntPoint := getSnapshotMountPoint(snap.Project(), sourcePool, snap.Name())
			_, err = rsyncLocalCopy(srcSnapshotMntPoint, destContainerMntPoint, bwlimit)
			if err != nil {
				return err
			}

			// This is costly but we need to ensure that all cached data has
			// been committed to disk. If we don't then the rbd snapshot of
			// the underlying filesystem can be inconsistent or - worst case
			// - empty.
			syscall.Sync()

			msg, fsFreezeErr := shared.TryRunCommand("fsfreeze", "--freeze", destContainerMntPoint)
			logger.Debugf("Trying to freeze the filesystem: %s: %s", msg, fsFreezeErr)

			// create snapshot
			_, snapOnlyName, _ := containerGetParentAndSnapshotName(snap.Name())
			err = s.doContainerSnapshotCreate(target.Project(), fmt.Sprintf("%s/%s", target.Name(), snapOnlyName), target.Name())
			if fsFreezeErr == nil {
				msg, fsFreezeErr := shared.TryRunCommand("fsfreeze", "--unfreeze", destContainerMntPoint)
				logger.Debugf("Trying to unfreeze the filesystem: %s: %s", msg, fsFreezeErr)
			}
			if err != nil {
				return err
			}
		}
	}

	srcContainerMntPoint := getContainerMountPoint(source.Project(), sourcePool, source.Name())
	_, err = rsyncLocalCopy(srcContainerMntPoint, destContainerMntPoint, bwlimit)
	if err != nil {
		if !refresh {
			s.StoragePoolVolumeDelete()
		}

		logger.Errorf("Failed to rsync into BTRFS storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	return nil
}

func (s *storageCeph) ContainerCopy(target container, source container,
	containerOnly bool) error {
	sourceContainerName := source.Name()
	logger.Debugf(`Copying RBD container storage %s to %s`, sourceContainerName, target.Name())

	revert := true

	ourStart, err := source.StorageStart()
	if err != nil {
		logger.Errorf(`Failed to initialize storage for container "%s": %s`, sourceContainerName, err)
		return err
	}
	logger.Debugf(`Initialized storage for container "%s"`,
		sourceContainerName)
	if ourStart {
		defer source.StorageStop()
	}

	_, sourcePool, _ := source.Storage().GetContainerPoolInfo()
	_, targetPool, _ := target.Storage().GetContainerPoolInfo()
	if sourcePool != targetPool {
		return s.doCrossPoolContainerCopy(target, source, containerOnly, false, nil)
	}

	snapshots, err := source.Snapshots()
	if err != nil {
		logger.Errorf(`Failed to retrieve snapshots of container "%s": %s`, sourceContainerName, err)
		return err
	}
	logger.Debugf(`Retrieved snapshots of container "%s"`,
		sourceContainerName)

	targetContainerName := target.Name()
	targetContainerMountPoint := getContainerMountPoint(target.Project(), s.pool.Name, targetContainerName)
	if containerOnly || len(snapshots) == 0 {
		if s.pool.Config["ceph.rbd.clone_copy"] != "" &&
			!shared.IsTrue(s.pool.Config["ceph.rbd.clone_copy"]) {
			err = s.copyWithoutSnapshotsFull(target, source)
		} else {
			err = s.copyWithoutSnapshotsSparse(target, source)
		}
		if err != nil {
			logger.Errorf(`Failed to copy RBD container storage %s to %s`, sourceContainerName, target.Name())
			return err
		}

		logger.Debugf(`Copied RBD container storage %s to %s`,
			sourceContainerName, target.Name())
		return nil
	} else {
		logger.Debugf(`Creating non-sparse copy of RBD storage volume for container "%s" to "%s" including snapshots`,
			sourceContainerName, targetContainerName)

		// create mountpoint for container
		targetContainerPath := target.Path()
		targetContainerMountPoint := getContainerMountPoint(
			target.Project(),
			s.pool.Name,
			targetContainerName)
		err = createContainerMountpoint(
			targetContainerMountPoint,
			targetContainerPath,
			target.IsPrivileged())
		if err != nil {
			logger.Errorf(`Failed to create mountpoint "%s" for RBD storage volume "%s" on storage pool "%s": %s"`, targetContainerMountPoint, s.volume.Name, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Created mountpoint "%s" for RBD storage volume "%s" on storage pool "%s"`, targetContainerMountPoint, s.volume.Name, s.pool.Name)

		defer func() {
			if !revert {
				return
			}

			err = deleteContainerMountpoint(
				targetContainerMountPoint,
				targetContainerPath,
				"")
			if err != nil {
				logger.Warnf(`Failed to delete mountpoint "%s" for RBD storage volume "%s" on storage pool "%s": %s"`, targetContainerMountPoint, s.volume.Name, s.pool.Name, err)
			}
		}()

		// create empty dummy volume
		err = cephRBDVolumeCreate(s.ClusterName, s.OSDPoolName,
			projectPrefix(target.Project(), targetContainerName), storagePoolVolumeTypeNameContainer,
			"0", s.UserName)
		if err != nil {
			logger.Errorf(`Failed to create RBD storage volume "%s" on storage pool "%s": %s`, targetContainerName, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Created RBD storage volume "%s" on storage pool "%s"`,
			targetContainerName, s.pool.Name)

		defer func() {
			if !revert {
				return
			}

			err := cephRBDVolumeDelete(s.ClusterName, s.OSDPoolName,
				projectPrefix(target.Project(), targetContainerName),
				storagePoolVolumeTypeNameContainer, s.UserName)
			if err != nil {
				logger.Warnf(`Failed to delete RBD storage volume "%s" on storage pool "%s": %s`, targetContainerName, s.pool.Name, err)
			}
		}()

		// receive over the dummy volume we created above
		targetVolumeName := fmt.Sprintf(
			"%s/container_%s",
			s.OSDPoolName,
			projectPrefix(target.Project(), targetContainerName))

		lastSnap := ""
		for i, snap := range snapshots {
			prev := ""
			if i > 0 {
				_, snapOnlyName, _ := containerGetParentAndSnapshotName(snapshots[i-1].Name())
				prev = fmt.Sprintf("snapshot_%s", snapOnlyName)
			}

			_, snapOnlyName, _ := containerGetParentAndSnapshotName(snap.Name())
			lastSnap = fmt.Sprintf("snapshot_%s", snapOnlyName)
			sourceVolumeName := fmt.Sprintf(
				"%s/container_%s@snapshot_%s",
				s.OSDPoolName,
				projectPrefix(source.Project(), sourceContainerName),
				snapOnlyName)

			err = s.copyWithSnapshots(
				sourceVolumeName,
				targetVolumeName,
				prev)
			if err != nil {
				logger.Errorf(`Failed to copy RBD container storage %s to %s`, sourceVolumeName,
					targetVolumeName)
				return err
			}
			logger.Debugf(`Copied RBD container storage %s to %s`,
				sourceVolumeName, targetVolumeName)

			defer func() {
				if !revert {
					return
				}

				err := cephRBDSnapshotDelete(s.ClusterName,
					s.OSDPoolName, projectPrefix(target.Project(), targetContainerName),
					storagePoolVolumeTypeNameContainer,
					snapOnlyName, s.UserName)
				if err != nil {
					logger.Warnf(`Failed to delete RBD container storage for snapshot "%s" of container "%s"`, snapOnlyName, targetContainerName)
				}
			}()

			// create snapshot mountpoint
			newTargetName := fmt.Sprintf("%s/%s", targetContainerName, snapOnlyName)
			containersPath := getSnapshotMountPoint(
				target.Project(),
				s.pool.Name,
				newTargetName)

			snapshotMntPointSymlinkTarget := shared.VarPath(
				"storage-pools",
				s.pool.Name,
				"containers-snapshots",
				projectPrefix(target.Project(), targetContainerName))

			snapshotMntPointSymlink := shared.VarPath(
				"snapshots",
				projectPrefix(target.Project(), targetContainerName))

			err := createSnapshotMountpoint(
				containersPath,
				snapshotMntPointSymlinkTarget,
				snapshotMntPointSymlink)
			if err != nil {
				logger.Errorf(`Failed to create mountpoint "%s", snapshot symlink target "%s", snapshot mountpoint symlink"%s" for RBD storage volume "%s" on storage pool "%s": %s`, containersPath, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink, s.volume.Name, s.pool.Name, err)
				return err
			}
			logger.Debugf(`Created mountpoint "%s", snapshot symlink target "%s", snapshot mountpoint symlink"%s" for RBD storage volume "%s" on storage pool "%s"`, containersPath, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink, s.volume.Name, s.pool.Name)

			defer func() {
				if !revert {
					return
				}

				err = deleteSnapshotMountpoint(
					containersPath,
					snapshotMntPointSymlinkTarget,
					snapshotMntPointSymlink)
				if err != nil {
					logger.Warnf(`Failed to delete mountpoint "%s", snapshot symlink target "%s", snapshot mountpoint symlink "%s" for RBD storage volume "%s" on storage pool "%s": %s`, containersPath, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink, s.volume.Name, s.pool.Name, err)
				}
			}()
		}

		// copy snapshot
		sourceVolumeName := fmt.Sprintf(
			"%s/container_%s",
			s.OSDPoolName,
			projectPrefix(source.Project(), sourceContainerName))
		err = s.copyWithSnapshots(
			sourceVolumeName,
			targetVolumeName,
			lastSnap)
		if err != nil {
			logger.Errorf(`Failed to copy RBD container storage %s to %s`, sourceVolumeName, targetVolumeName)
			return err
		}
		logger.Debugf(`Copied RBD container storage %s to %s`, sourceVolumeName, targetVolumeName)

		// Note, we don't need to register another cleanup function for
		// the actual container's storage volume since we already
		// registered one above for the dummy volume we created.

		// map the container's volume
		_, err = cephRBDVolumeMap(s.ClusterName, s.OSDPoolName,
			projectPrefix(target.Project(), targetContainerName), storagePoolVolumeTypeNameContainer,
			s.UserName)
		if err != nil {
			logger.Errorf(`Failed to map RBD storage volume for container "%s" on storage pool "%s": %s`, targetContainerName, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Mapped RBD storage volume for container "%s" on storage pool "%s"`, targetContainerName, s.pool.Name)

		logger.Debugf(`Created non-sparse copy of RBD storage volume for container "%s" to "%s" including snapshots`,
			sourceContainerName, targetContainerName)
	}

	ourMount, err := s.ContainerMount(target)
	if err != nil {
		return err
	}
	if ourMount {
		defer s.ContainerUmount(target, targetContainerMountPoint)
	}

	err = s.setUnprivUserACL(source, targetContainerMountPoint)
	if err != nil {
		return err
	}

	err = target.TemplateApply("copy")
	if err != nil {
		logger.Errorf(`Failed to apply copy template for container "%s": %s`, target.Name(), err)
		return err
	}
	logger.Debugf(`Applied copy template for container "%s"`, target.Name())

	logger.Debugf(`Copied RBD container storage %s to %s`, sourceContainerName, target.Name())

	revert = false

	return nil
}

func (s *storageCeph) ContainerRefresh(target container, source container, snapshots []container) error {
	logger.Debugf(`Refreshing RBD container storage for %s from %s`, target.Name(), source.Name())

	ourStart, err := source.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer source.StorageStop()
	}

	return s.doCrossPoolContainerCopy(target, source, len(snapshots) == 0, true, snapshots)
}

func (s *storageCeph) ContainerMount(c container) (bool, error) {
	logger.Debugf("Mounting RBD storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	ourMount, err := s.doContainerMount(c.Project(), c.Name())
	if err != nil {
		return false, err
	}

	logger.Debugf("Mounted RBD storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageCeph) ContainerUmount(c container, path string) (bool, error) {
	logger.Debugf("Unmounting RBD storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	name := c.Name()

	containerMntPoint := getContainerMountPoint(c.Project(), s.pool.Name, name)
	if shared.IsSnapshot(name) {
		containerMntPoint = getSnapshotMountPoint(c.Project(), s.pool.Name, name)
	}

	containerUmountLockID := getContainerUmountLockID(s.pool.Name, projectPrefix(c.Project(), name))
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerUmountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore, this should not have happened")
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

	logger.Debugf("Unmounted RBD storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return ourUmount, nil
}

func (s *storageCeph) ContainerRename(c container, newName string) error {
	oldName := c.Name()
	containerPath := c.Path()

	revert := true

	logger.Debugf(`Renaming RBD storage volume for container "%s" from "%s" to "%s"`, oldName, oldName, newName)

	// unmount
	_, err := s.ContainerUmount(c, containerPath)
	if err != nil {
		return err
	}

	// unmap
	err = cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName, oldName,
		storagePoolVolumeTypeNameContainer, s.UserName, true)
	if err != nil {
		logger.Errorf(`Failed to unmap RBD storage volume for container "%s" on storage pool "%s": %s`, oldName, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Unmapped RBD storage volume for container "%s" on storage pool "%s"`, oldName, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		_, err := cephRBDVolumeMap(s.ClusterName, s.OSDPoolName,
			oldName, storagePoolVolumeTypeNameContainer, s.UserName)
		if err != nil {
			logger.Warnf(`Failed to Map RBD storage volume for container "%s": %s`, oldName, err)
		}
	}()

	err = cephRBDVolumeRename(s.ClusterName, s.OSDPoolName,
		storagePoolVolumeTypeNameContainer, oldName, newName, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to rename RBD storage volume for container "%s" on storage pool "%s": %s`, oldName, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Renamed RBD storage volume for container "%s" on storage pool "%s"`, oldName, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		err = cephRBDVolumeRename(s.ClusterName, s.OSDPoolName,
			storagePoolVolumeTypeNameContainer, newName, oldName,
			s.UserName)
		if err != nil {
			logger.Warnf(`Failed to rename RBD storage volume for container "%s" on storage pool "%s": %s`, newName, s.pool.Name, err)
		}
	}()

	// map
	_, err = cephRBDVolumeMap(s.ClusterName, s.OSDPoolName, newName,
		storagePoolVolumeTypeNameContainer, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to map RBD storage volume for container "%s" on storage pool "%s": %s`, newName, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Mapped RBD storage volume for container "%s" on storage pool "%s"`, newName, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		err := cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName, newName,
			storagePoolVolumeTypeNameContainer, s.UserName, true)
		if err != nil {
			logger.Warnf(`Failed to unmap RBD storage volume for container "%s": %s`, newName, err)
		}
	}()

	// Create new mountpoint on the storage pool.
	oldContainerMntPoint := getContainerMountPoint(c.Project(), s.pool.Name, oldName)
	oldContainerMntPointSymlink := containerPath
	newContainerMntPoint := getContainerMountPoint(c.Project(), s.pool.Name, newName)
	newContainerMntPointSymlink := shared.VarPath("containers", projectPrefix(c.Project(), newName))
	err = renameContainerMountpoint(
		oldContainerMntPoint,
		oldContainerMntPointSymlink,
		newContainerMntPoint,
		newContainerMntPointSymlink)
	if err != nil {
		return err
	}

	defer func() {
		if !revert {
			return
		}

		renameContainerMountpoint(newContainerMntPoint,
			newContainerMntPointSymlink, oldContainerMntPoint,
			oldContainerMntPointSymlink)
	}()

	// Rename the snapshot mountpoint on the storage pool.
	oldSnapshotMntPoint := getSnapshotMountPoint(c.Project(), s.pool.Name, oldName)
	newSnapshotMntPoint := getSnapshotMountPoint(c.Project(), s.pool.Name, newName)
	if shared.PathExists(oldSnapshotMntPoint) {
		err := os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
		if err != nil {
			return err
		}
	}

	defer func() {
		if !revert {
			return
		}

		os.Rename(newSnapshotMntPoint, oldSnapshotMntPoint)
	}()

	// Remove old symlink.
	oldSnapshotPath := shared.VarPath("snapshots", projectPrefix(c.Project(), oldName))
	if shared.PathExists(oldSnapshotPath) {
		err := os.Remove(oldSnapshotPath)
		if err != nil {
			return err
		}
	}

	defer func() {
		if !revert {
			return
		}

		os.Symlink(oldSnapshotMntPoint, oldSnapshotPath)
	}()

	// Create new symlink.
	newSnapshotPath := shared.VarPath("snapshots", projectPrefix(c.Project(), newName))
	if shared.PathExists(newSnapshotPath) {
		err := os.Symlink(newSnapshotMntPoint, newSnapshotPath)
		if err != nil {
			return err
		}
	}

	logger.Debugf(`Renamed RBD storage volume for container "%s" from "%s" to "%s"`, oldName, oldName, newName)

	revert = false

	return nil
}

func (s *storageCeph) ContainerRestore(target container, source container) error {
	sourceName := source.Name()
	targetName := target.Name()

	logger.Debugf(`Restoring RBD storage volume for container "%s" from %s to %s`, targetName, sourceName, targetName)

	ourStop, err := source.StorageStop()
	if err != nil {
		return err
	}
	if ourStop {
		defer source.StorageStart()
	}

	ourStop, err = target.StorageStop()
	if err != nil {
		return err
	}
	if ourStop {
		defer target.StorageStart()
	}

	sourceContainerOnlyName, sourceSnapshotOnlyName, _ := containerGetParentAndSnapshotName(sourceName)
	prefixedSourceSnapOnlyName := fmt.Sprintf("snapshot_%s", sourceSnapshotOnlyName)
	err = cephRBDVolumeRestore(s.ClusterName, s.OSDPoolName,
		sourceContainerOnlyName, storagePoolVolumeTypeNameContainer,
		prefixedSourceSnapOnlyName, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to restore RBD storage volume for container "%s" from "%s": %s`, targetName, sourceName, err)
		return err
	}

	logger.Debugf(`Restored RBD storage volume for container "%s" from %s to %s`, targetName, sourceName, targetName)
	return nil
}

func (s *storageCeph) ContainerGetUsage(container container) (int64, error) {
	return -1, fmt.Errorf("RBD quotas are currently not supported")
}

func (s *storageCeph) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	containerMntPoint := getContainerMountPoint(sourceContainer.Project(), s.pool.Name, sourceContainer.Name())
	if shared.IsMountPoint(containerMntPoint) {
		// This is costly but we need to ensure that all cached data has
		// been committed to disk. If we don't then the rbd snapshot of
		// the underlying filesystem can be inconsistent or - worst case
		// - empty.
		syscall.Sync()

		msg, fsFreezeErr := shared.TryRunCommand("fsfreeze", "--freeze", containerMntPoint)
		logger.Debugf("Trying to freeze the filesystem: %s: %s", msg, fsFreezeErr)
		if fsFreezeErr == nil {
			defer shared.TryRunCommand("fsfreeze", "--unfreeze", containerMntPoint)
		}
	}

	return s.doContainerSnapshotCreate(sourceContainer.Project(), snapshotContainer.Name(), sourceContainer.Name())
}

func (s *storageCeph) ContainerSnapshotDelete(snapshotContainer container) error {
	logger.Debugf(`Deleting RBD storage volume for snapshot "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)

	snapshotContainerName := snapshotContainer.Name()
	sourceContainerName, sourceContainerSnapOnlyName, _ :=
		containerGetParentAndSnapshotName(snapshotContainerName)
	snapshotName := fmt.Sprintf("snapshot_%s", sourceContainerSnapOnlyName)

	rbdVolumeExists := cephRBDSnapshotExists(s.ClusterName, s.OSDPoolName,
		projectPrefix(snapshotContainer.Project(), sourceContainerName), storagePoolVolumeTypeNameContainer,
		snapshotName, s.UserName)

	if rbdVolumeExists {
		ret := cephContainerSnapshotDelete(s.ClusterName, s.OSDPoolName,
			projectPrefix(snapshotContainer.Project(), sourceContainerName),
			storagePoolVolumeTypeNameContainer, snapshotName, s.UserName)
		if ret < 0 {
			msg := fmt.Sprintf(`Failed to delete RBD storage volume for `+
				`snapshot "%s" on storage pool "%s"`,
				snapshotContainerName, s.pool.Name)
			logger.Errorf(msg)
			return fmt.Errorf(msg)
		}
	}

	snapshotContainerMntPoint := getSnapshotMountPoint(snapshotContainer.Project(), s.pool.Name,
		snapshotContainerName)
	if shared.PathExists(snapshotContainerMntPoint) {
		err := os.RemoveAll(snapshotContainerMntPoint)
		if err != nil {
			logger.Errorf(`Failed to delete mountpoint "%s" of RBD snapshot "%s" of container "%s" on storage pool "%s": %s`, snapshotContainerMntPoint, sourceContainerSnapOnlyName, sourceContainerName, s.OSDPoolName, err)
			return err
		}
		logger.Debugf(`Deleted mountpoint "%s" of RBD snapshot "%s" of container "%s" on storage pool "%s"`, snapshotContainerMntPoint, sourceContainerSnapOnlyName, sourceContainerName, s.OSDPoolName)
	}

	// check if snapshot directory is empty
	snapshotContainerPath := getSnapshotMountPoint(snapshotContainer.Project(), s.pool.Name,
		sourceContainerName)
	empty, _ := shared.PathIsEmpty(snapshotContainerPath)
	if empty == true {
		// remove snapshot directory for container
		err := os.Remove(snapshotContainerPath)
		if err != nil {
			logger.Errorf(`Failed to delete snapshot directory "%s" of RBD snapshot "%s" of container "%s" on storage pool "%s": %s`, snapshotContainerPath, sourceContainerSnapOnlyName, sourceContainerName, s.OSDPoolName, err)
			return err
		}
		logger.Debugf(`Deleted snapshot directory  "%s" of RBD snapshot "%s" of container "%s" on storage pool "%s"`, snapshotContainerPath, sourceContainerSnapOnlyName, sourceContainerName, s.OSDPoolName)

		// remove the snapshot symlink if possible
		snapshotSymlink := shared.VarPath("snapshots",
			projectPrefix(snapshotContainer.Project(), sourceContainerName))
		if shared.PathExists(snapshotSymlink) {
			err := os.Remove(snapshotSymlink)
			if err != nil {
				logger.Errorf(`Failed to delete snapshot symlink "%s" of RBD snapshot "%s" of container "%s" on storage pool "%s": %s`, snapshotSymlink, sourceContainerSnapOnlyName, sourceContainerName, s.OSDPoolName, err)
				return err
			}
			logger.Debugf(`Deleted snapshot symlink "%s" of RBD snapshot "%s" of container "%s" on storage pool "%s"`, snapshotSymlink, sourceContainerSnapOnlyName, sourceContainerName, s.OSDPoolName)
		}
	}

	logger.Debugf(`Deleted RBD storage volume for snapshot "%s" on storage pool "%s"`, s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageCeph) ContainerSnapshotRename(c container, newName string) error {
	oldName := c.Name()
	logger.Debugf(`Renaming RBD storage volume for snapshot "%s" from "%s" to "%s"`, oldName, oldName, newName)

	revert := true

	containerOnlyName, snapOnlyName, _ := containerGetParentAndSnapshotName(oldName)
	containerOnlyName = projectPrefix(c.Project(), containerOnlyName)
	oldSnapOnlyName := fmt.Sprintf("snapshot_%s", snapOnlyName)
	_, newSnapOnlyName, _ := containerGetParentAndSnapshotName(newName)
	newSnapOnlyName = fmt.Sprintf("snapshot_%s", newSnapOnlyName)
	err := cephRBDVolumeSnapshotRename(s.ClusterName, s.OSDPoolName,
		containerOnlyName, storagePoolVolumeTypeNameContainer, oldSnapOnlyName,
		newSnapOnlyName, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to rename RBD storage volume for snapshot "%s" from "%s" to "%s": %s`, oldName, oldName, newName, err)
		return err
	}

	defer func() {
		if !revert {
			return
		}

		err := cephRBDVolumeSnapshotRename(s.ClusterName, s.OSDPoolName,
			containerOnlyName, storagePoolVolumeTypeNameContainer,
			newSnapOnlyName, oldSnapOnlyName, s.UserName)
		if err != nil {
			logger.Warnf(`Failed to rename RBD storage volume for container "%s" on storage pool "%s": %s`, oldName, s.pool.Name, err)
		}
	}()

	oldSnapshotMntPoint := getSnapshotMountPoint(c.Project(), s.pool.Name, oldName)
	newSnapshotMntPoint := getSnapshotMountPoint(c.Project(), s.pool.Name, newName)
	err = os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
	if err != nil {
		logger.Errorf(`Failed to rename mountpoint for RBD storage volume for snapshot "%s" from "%s" to "%s": %s`, oldName, oldSnapshotMntPoint, newSnapshotMntPoint, err)
		return err
	}
	logger.Debugf(`Renamed mountpoint for RBD storage volume for snapshot "%s" from "%s" to "%s"`, oldName, oldSnapshotMntPoint, newSnapshotMntPoint)

	logger.Debugf(`Renamed RBD storage volume for snapshot "%s" from "%s" to "%s"`, oldName, oldName, newName)

	revert = false

	return nil
}

func (s *storageCeph) ContainerSnapshotStart(c container) (bool, error) {
	containerName := c.Name()
	logger.Debugf(`Initializing RBD storage volume for snapshot "%s" on storage pool "%s"`, containerName, s.pool.Name)

	revert := true

	containerOnlyName, snapOnlyName, _ := containerGetParentAndSnapshotName(containerName)
	containerOnlyName = projectPrefix(c.Project(), containerOnlyName)

	// protect
	prefixedSnapOnlyName := fmt.Sprintf("snapshot_%s", snapOnlyName)
	err := cephRBDSnapshotProtect(s.ClusterName, s.OSDPoolName,
		containerOnlyName, storagePoolVolumeTypeNameContainer,
		prefixedSnapOnlyName, s.UserName)
	if err != nil {
		logger.Errorf(`Failed to protect snapshot of RBD storage volume for container "%s" on storage pool "%s": %s`, containerName, s.pool.Name, err)
		return false, err
	}
	logger.Debugf(`Protected snapshot of RBD storage volume for container "%s" on storage pool "%s"`, containerName, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		err := cephRBDSnapshotUnprotect(s.ClusterName, s.OSDPoolName,
			containerOnlyName, storagePoolVolumeTypeNameContainer,
			prefixedSnapOnlyName, s.UserName)
		if err != nil {
			logger.Warnf(`Failed to unprotect snapshot of RBD storage volume for container "%s" on storage pool "%s": %s`, containerName, s.pool.Name, err)
		}
	}()

	cloneName := fmt.Sprintf("%s_%s_start_clone", containerOnlyName, snapOnlyName)
	// clone
	err = cephRBDCloneCreate(s.ClusterName, s.OSDPoolName,
		containerOnlyName, storagePoolVolumeTypeNameContainer,
		prefixedSnapOnlyName, s.OSDPoolName, cloneName, "snapshots",
		s.UserName)
	if err != nil {
		logger.Errorf(`Failed to create clone of RBD storage volume for container "%s" on storage pool "%s": %s`, containerName, s.pool.Name, err)
		return false, err
	}
	logger.Debugf(`Created clone of RBD storage volume for container "%s" on storage pool "%s"`, containerName, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		// delete
		err = cephRBDVolumeDelete(s.ClusterName, s.OSDPoolName,
			cloneName, "snapshots", s.UserName)
		if err != nil {
			logger.Errorf(`Failed to delete clone of RBD storage volume for container "%s" on storage pool "%s": %s`, containerName, s.pool.Name, err)
		}
	}()

	// map
	RBDDevPath, err := cephRBDVolumeMap(s.ClusterName, s.OSDPoolName,
		cloneName, "snapshots", s.UserName)
	if err != nil {
		logger.Errorf(`Failed to map RBD storage volume for container "%s" on storage pool "%s": %s`, containerName, s.pool.Name, err)
		return false, err
	}
	logger.Debugf(`Mapped RBD storage volume for container "%s" on storage pool "%s"`, containerName, s.pool.Name)

	defer func() {
		if !revert {
			return
		}

		err := cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName,
			cloneName, "snapshots", s.UserName, true)
		if err != nil {
			logger.Warnf(`Failed to unmap RBD storage volume for container "%s" on storage pool "%s": %s`, containerName, s.pool.Name, err)
		}
	}()

	containerMntPoint := getSnapshotMountPoint(c.Project(), s.pool.Name, containerName)
	RBDFilesystem := s.getRBDFilesystem()
	mountFlags, mountOptions := lxdResolveMountoptions(s.getRBDMountOptions())
	err = tryMount(
		RBDDevPath,
		containerMntPoint,
		RBDFilesystem,
		mountFlags,
		mountOptions)
	if err != nil {
		logger.Errorf("Failed to mount RBD device %s onto %s: %s",
			RBDDevPath, containerMntPoint, err)
		return false, err
	}
	logger.Debugf("Mounted RBD device %s onto %s", RBDDevPath,
		containerMntPoint)

	logger.Debugf(`Initialized RBD storage volume for snapshot "%s" on storage pool "%s"`, containerName, s.pool.Name)

	revert = false

	return true, nil
}

func (s *storageCeph) ContainerSnapshotStop(c container) (bool, error) {
	logger.Debugf(`Stopping RBD storage volume for snapshot "%s" on storage pool "%s"`, c.Name(), s.pool.Name)

	containerName := c.Name()
	containerMntPoint := getSnapshotMountPoint(c.Project(), s.pool.Name, containerName)

	// Check if already unmounted
	if !shared.IsMountPoint(containerMntPoint) {
		return false, nil
	}

	// Unmount
	err := tryUnmount(containerMntPoint, syscall.MNT_DETACH)
	if err != nil {
		logger.Errorf("Failed to unmount %s: %s", containerMntPoint, err)
		return false, err
	}

	logger.Debugf("Unmounted %s", containerMntPoint)

	containerOnlyName, snapOnlyName, _ := containerGetParentAndSnapshotName(containerName)
	containerOnlyName = projectPrefix(c.Project(), containerOnlyName)
	cloneName := fmt.Sprintf("%s_%s_start_clone", containerOnlyName, snapOnlyName)

	// Unmap the RBD volume
	err = cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName, cloneName, "snapshots", s.UserName, true)
	if err != nil {
		logger.Warnf(`Failed to unmap RBD storage volume for container "%s" on storage pool "%s": %s`, containerName, s.pool.Name, err)
	} else {
		logger.Debugf(`Unmapped RBD storage volume for container "%s" on storage pool "%s"`, containerName, s.pool.Name)
	}

	rbdVolumeExists := cephRBDVolumeExists(s.ClusterName, s.OSDPoolName, cloneName, "snapshots", s.UserName)
	if rbdVolumeExists {
		// Delete the temporary RBD volume
		err = cephRBDVolumeDelete(s.ClusterName, s.OSDPoolName, cloneName, "snapshots", s.UserName)
		if err != nil {
			logger.Errorf(`Failed to delete clone of RBD storage volume for container "%s" on storage pool "%s": %s`, containerName, s.pool.Name, err)
			return false, err
		}
		logger.Debugf(`Deleted clone of RBD storage volume for container "%s" on storage pool "%s"`, containerName, s.pool.Name)
	}

	logger.Debugf(`Stopped RBD storage volume for snapshot "%s" on storage pool "%s"`, containerName, s.pool.Name)
	return true, nil
}

func (s *storageCeph) ContainerSnapshotCreateEmpty(c container) error {
	logger.Debugf(`Creating empty RBD storage volume for snapshot "%s" on storage pool "%s" (noop)`, c.Name(), s.pool.Name)

	logger.Debugf(`Created empty RBD storage volume for snapshot "%s" on storage pool "%s" (noop)`, c.Name(), s.pool.Name)
	return nil
}

func (s *storageCeph) ContainerBackupCreate(backup backup, source container) error {
	// Start storage
	ourStart, err := source.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer source.StorageStop()
	}

	// Create a temporary path for the backup
	tmpPath, err := ioutil.TempDir(shared.VarPath("backups"), "lxd_backup_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpPath)

	// Generate the actual backup
	if !backup.containerOnly {
		snapshots, err := source.Snapshots()
		if err != nil {
			return err
		}

		for _, snap := range snapshots {
			err := s.cephRBDVolumeBackupCreate(tmpPath, backup, snap)
			if err != nil {
				return err
			}
		}
	}

	err = s.cephRBDVolumeBackupCreate(tmpPath, backup, source)
	if err != nil {
		return err
	}

	// Pack the backup
	err = backupCreateTarball(s.s, tmpPath, backup)
	if err != nil {
		return err
	}

	return nil
}

// This function recreates an rbd container including its snapshots. It
// recreates the dependencies between the container and the snapshots:
// - create an empty rbd storage volume
// - for each snapshot dump the contents into the empty storage volume and
//   after each dump take a snapshot of the rbd storage volume
// - dump the container contents into the rbd storage volume.
func (s *storageCeph) ContainerBackupLoad(info backupInfo, data io.ReadSeeker, tarArgs []string) error {
	// create the main container
	err := s.doContainerCreate(info.Project, info.Name, info.Privileged)
	if err != nil {
		return err
	}

	// mount container
	_, err = s.doContainerMount(info.Project, info.Name)
	if err != nil {
		return err
	}

	containerMntPoint := getContainerMountPoint(info.Project, s.pool.Name, info.Name)
	// Extract container
	for _, snap := range info.Snapshots {
		cur := fmt.Sprintf("backup/snapshots/%s", snap)

		// Prepare tar arguments
		args := append(tarArgs, []string{
			"-",
			"--recursive-unlink",
			"--strip-components=3",
			"--xattrs-include=*",
			"-C", containerMntPoint, cur,
		}...)

		// Extract snapshots
		data.Seek(0, 0)
		err = shared.RunCommandWithFds(data, nil, "tar", args...)
		if err != nil {
			logger.Errorf("Failed to untar \"%s\" into \"%s\": %s", cur, containerMntPoint, err)
			return err
		}

		// This is costly but we need to ensure that all cached data has
		// been committed to disk. If we don't then the rbd snapshot of
		// the underlying filesystem can be inconsistent or - worst case
		// - empty.
		syscall.Sync()

		msg, fsFreezeErr := shared.TryRunCommand("fsfreeze", "--freeze", containerMntPoint)
		logger.Debugf("Trying to freeze the filesystem: %s: %s", msg, fsFreezeErr)

		// create snapshot
		err = s.doContainerSnapshotCreate(info.Project, fmt.Sprintf("%s/%s", info.Name, snap), info.Name)
		if fsFreezeErr == nil {
			msg, fsFreezeErr := shared.TryRunCommand("fsfreeze", "--unfreeze", containerMntPoint)
			logger.Debugf("Trying to unfreeze the filesystem: %s: %s", msg, fsFreezeErr)
		}
		if err != nil {
			return err
		}
	}

	// Prepare tar arguments
	args := append(tarArgs, []string{
		"-",
		"--strip-components=2",
		"--xattrs-include=*",
		"-C", containerMntPoint, "backup/container",
	}...)

	// Extract container
	data.Seek(0, 0)
	err = shared.RunCommandWithFds(data, nil, "tar", args...)
	if err != nil {
		logger.Errorf("Failed to untar \"backup/container\" into \"%s\": %s", containerMntPoint, err)
		return err
	}

	return nil
}

func (s *storageCeph) ImageCreate(fingerprint string) error {
	logger.Debugf(`Creating RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

	revert := true

	// create image mountpoint
	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if !shared.PathExists(imageMntPoint) {
		err := os.MkdirAll(imageMntPoint, 0700)
		if err != nil {
			logger.Errorf(`Failed to create mountpoint "%s" for RBD storage volume for image "%s" on storage pool "%s": %s`, imageMntPoint, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Created mountpoint "%s" for RBD storage volume for image "%s" on storage pool "%s"`, imageMntPoint, fingerprint, s.pool.Name)
	}

	defer func() {
		if !revert {
			return
		}

		err := os.Remove(imageMntPoint)
		if err != nil {
			logger.Warnf(`Failed to delete mountpoint "%s" for RBD storage volume for image "%s" on storage pool "%s": %s`, imageMntPoint, fingerprint, s.pool.Name, err)
		}
	}()

	prefixedType := fmt.Sprintf("zombie_%s_%s",
		storagePoolVolumeTypeNameImage,
		s.volume.Config["block.filesystem"])
	ok := cephRBDVolumeExists(s.ClusterName, s.OSDPoolName, fingerprint,
		prefixedType, s.UserName)
	if !ok {
		logger.Debugf(`RBD storage volume for image "%s" on storage pool "%s" does not exist`, fingerprint, s.pool.Name)

		// get size
		RBDSize, err := s.getRBDSize()
		if err != nil {
			logger.Errorf(`Failed to retrieve size of RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Retrieve size "%s" of RBD storage volume for image "%s" on storage pool "%s"`, RBDSize, fingerprint, s.pool.Name)

		// create volume
		err = cephRBDVolumeCreate(s.ClusterName, s.OSDPoolName,
			fingerprint, storagePoolVolumeTypeNameImage, RBDSize,
			s.UserName)
		if err != nil {
			logger.Errorf(`Failed to create RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Created RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

		defer func() {
			if !revert {
				return
			}

			err := cephRBDVolumeDelete(s.ClusterName, s.OSDPoolName,
				fingerprint, storagePoolVolumeTypeNameImage,
				s.UserName)
			if err != nil {
				logger.Warnf(`Failed to delete RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			}
		}()

		RBDDevPath, err := cephRBDVolumeMap(s.ClusterName,
			s.OSDPoolName, fingerprint,
			storagePoolVolumeTypeNameImage, s.UserName)
		if err != nil {
			logger.Errorf(`Failed to map RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Mapped RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

		defer func() {
			if !revert {
				return
			}

			err := cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName,
				fingerprint, storagePoolVolumeTypeNameImage,
				s.UserName, true)
			if err != nil {
				logger.Warnf(`Failed to unmap RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			}
		}()

		// get filesystem
		RBDFilesystem := s.getRBDFilesystem()
		msg, err := makeFSType(RBDDevPath, RBDFilesystem, nil)
		if err != nil {
			logger.Errorf(`Failed to create filesystem "%s" for RBD storage volume for image "%s" on storage pool "%s": %s`, RBDFilesystem, fingerprint,
				s.pool.Name, msg)
			return err
		}
		logger.Debugf(`Created filesystem "%s" for RBD storage volume for image "%s" on storage pool "%s"`, RBDFilesystem, fingerprint, s.pool.Name)

		// mount image
		_, err = s.ImageMount(fingerprint)
		if err != nil {
			return err
		}

		// rsync contents into image
		imagePath := shared.VarPath("images", fingerprint)
		err = unpackImage(imagePath, imageMntPoint, storageTypeCeph, s.s.OS.RunningInUserNS)
		if err != nil {
			logger.Errorf(`Failed to unpack image for RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)

			// umount image
			s.ImageUmount(fingerprint)
			return err
		}
		logger.Debugf(`Unpacked image for RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

		// umount image
		s.ImageUmount(fingerprint)

		// unmap
		err = cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName,
			fingerprint, storagePoolVolumeTypeNameImage, s.UserName,
			true)
		if err != nil {
			logger.Errorf(`Failed to unmap RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Unmapped RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

		// make snapshot of volume
		err = cephRBDSnapshotCreate(s.ClusterName, s.OSDPoolName,
			fingerprint, storagePoolVolumeTypeNameImage, "readonly",
			s.UserName)
		if err != nil {
			logger.Errorf(`Failed to create snapshot for RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Created snapshot for RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

		defer func() {
			if !revert {
				return
			}

			err := cephRBDSnapshotDelete(s.ClusterName,
				s.OSDPoolName, fingerprint,
				storagePoolVolumeTypeNameImage, "readonly",
				s.UserName)
			if err != nil {
				logger.Warnf(`Failed to delete snapshot for RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			}
		}()

		// protect volume so we can create clones of it
		err = cephRBDSnapshotProtect(s.ClusterName, s.OSDPoolName,
			fingerprint, storagePoolVolumeTypeNameImage, "readonly",
			s.UserName)
		if err != nil {
			logger.Errorf(`Failed to protect snapshot for RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Protected snapshot for RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

		defer func() {
			if !revert {
				return
			}

			err := cephRBDSnapshotUnprotect(s.ClusterName,
				s.OSDPoolName, fingerprint,
				storagePoolVolumeTypeNameImage, "readonly",
				s.UserName)
			if err != nil {
				logger.Warnf(`Failed to unprotect snapshot for RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			}
		}()
	} else {
		logger.Debugf(`RBD storage volume for image "%s" on storage pool "%s" does exist`, fingerprint, s.pool.Name)

		// unmark deleted
		err := cephRBDVolumeUnmarkDeleted(s.ClusterName, s.OSDPoolName,
			fingerprint, storagePoolVolumeTypeNameImage, s.UserName,
			s.volume.Config["block.filesystem"], "")
		if err != nil {
			logger.Errorf(`Failed to unmark RBD storage volume for image "%s" on storage pool "%s" as zombie: %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Unmarked RBD storage volume for image "%s" on storage pool "%s" as zombie`, fingerprint, s.pool.Name)

		defer func() {
			if !revert {
				return
			}

			err := cephRBDVolumeMarkDeleted(s.ClusterName,
				s.OSDPoolName, storagePoolVolumeTypeNameImage,
				fingerprint, fingerprint, s.UserName,
				s.volume.Config["block.filesystem"])
			if err != nil {
				logger.Warnf(`Failed to mark RBD storage volume for image "%s" on storage pool "%s" as zombie: %s`, fingerprint, s.pool.Name, err)
			}
		}()
	}

	err := s.createImageDbPoolVolume(fingerprint)
	if err != nil {
		logger.Errorf(`Failed to create database entry for RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Createdd database entry for RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

	logger.Debugf(`Created RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

	revert = false

	return nil
}

func (s *storageCeph) ImageDelete(fingerprint string) error {
	logger.Debugf(`Deleting RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

	// try to umount but don't fail
	s.ImageUmount(fingerprint)

	// check if image has dependent snapshots
	_, err := cephRBDSnapshotListClones(s.ClusterName, s.OSDPoolName,
		fingerprint, storagePoolVolumeTypeNameImage, "readonly",
		s.UserName)
	if err != nil {
		if err != db.ErrNoSuchObject {
			logger.Errorf(`Failed to list clones of RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Retrieved no clones of RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

		// unprotect snapshot
		err = cephRBDSnapshotUnprotect(s.ClusterName, s.OSDPoolName,
			fingerprint, storagePoolVolumeTypeNameImage, "readonly",
			s.UserName)
		if err != nil {
			logger.Errorf(`Failed to unprotect snapshot for RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Unprotected snapshot for RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

		// delete snapshots
		err = cephRBDSnapshotsPurge(s.ClusterName, s.OSDPoolName,
			fingerprint, storagePoolVolumeTypeNameImage, s.UserName)
		if err != nil {
			logger.Errorf(`Failed to delete snapshot for RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Deleted snapshot for RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

		// unmap
		err = cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName,
			fingerprint, storagePoolVolumeTypeNameImage, s.UserName,
			true)
		if err != nil {
			logger.Errorf(`Failed to unmap RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Unmapped RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

		// delete volume
		err = cephRBDVolumeDelete(s.ClusterName, s.OSDPoolName,
			fingerprint, storagePoolVolumeTypeNameImage, s.UserName)
		if err != nil {
			logger.Errorf(`Failed to delete RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Deleted RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)
	} else {
		// unmap
		err = cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName,
			fingerprint, storagePoolVolumeTypeNameImage, s.UserName,
			true)
		if err != nil {
			logger.Errorf(`Failed to unmap RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Unmapped RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

		// mark deleted
		err := cephRBDVolumeMarkDeleted(s.ClusterName, s.OSDPoolName,
			storagePoolVolumeTypeNameImage, fingerprint,
			fingerprint, s.UserName,
			s.volume.Config["block.filesystem"])
		if err != nil {
			logger.Errorf(`Failed to mark RBD storage volume for image "%s" on storage pool "%s" as zombie: %s`, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Marked RBD storage volume for image "%s" on storage pool "%s" as zombie`, fingerprint, s.pool.Name)
	}

	err = s.deleteImageDbPoolVolume(fingerprint)
	if err != nil {
		logger.Errorf(`Failed to delete database entry for RBD storage volume for image "%s" on storage pool "%s": %s`, fingerprint, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Deleted database entry for RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if shared.PathExists(imageMntPoint) {
		err := os.Remove(imageMntPoint)
		if err != nil {
			logger.Errorf(`Failed to delete image mountpoint "%s" for RBD storage volume for image "%s" on storage pool "%s": %s`, imageMntPoint, fingerprint, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Deleted image mountpoint "%s" for RBD storage volume for image "%s" on storage pool "%s"`, imageMntPoint, fingerprint, s.pool.Name)
	}

	logger.Debugf(`Deleted RBD storage volume for image "%s" on storage pool "%s"`, fingerprint, s.pool.Name)
	return nil
}

func (s *storageCeph) ImageMount(fingerprint string) (bool, error) {
	logger.Debugf("Mounting RBD storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if shared.IsMountPoint(imageMntPoint) {
		return false, nil
	}

	RBDFilesystem := s.getRBDFilesystem()
	RBDMountOptions := s.getRBDMountOptions()
	mountFlags, mountOptions := lxdResolveMountoptions(RBDMountOptions)
	RBDDevPath, ret := getRBDMappedDevPath(s.ClusterName, s.OSDPoolName,
		storagePoolVolumeTypeNameImage, fingerprint, true, s.UserName)
	errMsg := fmt.Sprintf("Failed to mount RBD device %s onto %s",
		RBDDevPath, imageMntPoint)
	if ret < 0 {
		logger.Errorf(errMsg)
		return false, fmt.Errorf(errMsg)
	}

	err := tryMount(RBDDevPath, imageMntPoint, RBDFilesystem, mountFlags, mountOptions)
	if err != nil || ret < 0 {
		return false, err
	}

	logger.Debugf("Mounted RBD storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
	return true, nil
}

func (s *storageCeph) ImageUmount(fingerprint string) (bool, error) {
	logger.Debugf("Unmounting RBD storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)

	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
	if !shared.IsMountPoint(imageMntPoint) {
		return false, nil
	}

	err := tryUnmount(imageMntPoint, 0)
	if err != nil {
		return false, err
	}

	logger.Debugf("Unmounted RBD storage volume for image \"%s\" on storage pool \"%s\"", fingerprint, s.pool.Name)
	return true, nil
}

func (s *storageCeph) StorageEntitySetQuota(volumeType int, size int64, data interface{}) error {
	logger.Debugf(`Setting RBD quota for "%s"`, s.volume.Name)

	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return fmt.Errorf("Invalid storage type")
	}

	var ret int
	var c container
	fsType := s.getRBDFilesystem()
	mountpoint := ""
	RBDDevPath := ""
	volumeName := ""
	switch volumeType {
	case storagePoolVolumeTypeContainer:
		c = data.(container)
		ctName := c.Name()
		if c.IsRunning() {
			msg := fmt.Sprintf(`Cannot resize RBD storage volume `+
				`for container "%s" when it is running`,
				ctName)
			logger.Errorf(msg)
			return fmt.Errorf(msg)
		}

		RBDDevPath, ret = getRBDMappedDevPath(s.ClusterName,
			s.OSDPoolName, storagePoolVolumeTypeNameContainer,
			s.volume.Name, true, s.UserName)
		mountpoint = getContainerMountPoint("default", s.pool.Name, ctName)
		volumeName = ctName
	default:
		RBDDevPath, ret = getRBDMappedDevPath(s.ClusterName,
			s.OSDPoolName, storagePoolVolumeTypeNameCustom,
			s.volume.Name, true, s.UserName)
		mountpoint = getStoragePoolVolumeMountPoint(s.pool.Name,
			s.volume.Name)
		volumeName = s.volume.Name
	}
	if ret < 0 {
		return fmt.Errorf("Failed to get mapped RBD path")
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
		err = s.rbdShrink(RBDDevPath, size, fsType, mountpoint,
			volumeType, volumeName, data)
	} else if size > oldSize {
		err = s.rbdGrow(RBDDevPath, size, fsType, mountpoint,
			volumeType, volumeName, data)
	}
	if err != nil {
		return err
	}

	// Update the database
	s.volume.Config["size"] = shared.GetByteSizeString(size, 0)
	err = s.s.Cluster.StoragePoolVolumeUpdate(
		s.volume.Name,
		volumeType,
		s.poolID,
		s.volume.Description,
		s.volume.Config)
	if err != nil {
		return err
	}

	logger.Debugf(`Set RBD quota for "%s"`, s.volume.Name)
	return nil
}

func (s *storageCeph) StoragePoolResources() (*api.ResourcesStoragePool, error) {
	buf, err := shared.RunCommand(
		"ceph",
		"--name", fmt.Sprintf("client.%s", s.UserName),
		"--cluster", s.ClusterName,
		"df")
	if err != nil {
		return nil, err
	}

	msg := string(buf)
	idx := strings.Index(msg, "GLOBAL:")
	if idx < 0 {
		return nil, fmt.Errorf(`Output did not contain expected ` +
			`"GLOBAL:" string`)
	}

	msg = msg[len("GLOBAL:")+1:]
	msg = strings.TrimSpace(msg)

	if !strings.HasPrefix(msg, "SIZE") {
		return nil, fmt.Errorf(`Output "%s" did not contain expected `+
			`"SIZE" string`, msg)
	}

	lines := strings.Split(msg, "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf(`Output did not contain expected ` +
			`resource information`)
	}

	properties := strings.Fields(lines[1])
	if len(properties) < 3 {
		return nil, fmt.Errorf(`Output is missing size properties`)
	}

	totalStr := strings.TrimSpace(properties[0])
	usedStr := strings.TrimSpace(properties[2])

	if totalStr == "" {
		return nil, fmt.Errorf(`Failed to detect space available to ` +
			`the Ceph cluster`)
	}

	total, err := parseCephSize(totalStr)
	if err != nil {
		return nil, err
	}

	used := uint64(0)
	if usedStr != "" {
		used, err = parseCephSize(usedStr)
		if err != nil {
			return nil, err
		}
	}

	res := api.ResourcesStoragePool{}
	res.Space.Total = total
	res.Space.Used = used

	return &res, nil
}

func (s *storageCeph) StoragePoolVolumeCopy(source *api.StorageVolumeSource) error {
	logger.Infof("Copying RBD storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)
	successMsg := fmt.Sprintf("Copied RBD storage volume \"%s\" on storage pool \"%s\" as \"%s\" to storage pool \"%s\"", source.Name, source.Pool, s.volume.Name, s.pool.Name)

	isSnapshot := strings.Contains(s.volume.Name, "/")

	var srcMountPoint string
	var dstMountPoint string

	if isSnapshot {
		srcMountPoint = getStoragePoolVolumeSnapshotMountPoint(source.Pool, source.Name)
		dstMountPoint = getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	} else {
		srcMountPoint = getStoragePoolVolumeMountPoint(source.Pool, source.Name)
		dstMountPoint = getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	}

	if s.pool.Name == source.Pool {
		var oldVolumeName string
		var newVolumeName string

		if isSnapshot {
			_, srcSnapshotOnlyName, _ := containerGetParentAndSnapshotName(source.Name)
			_, dstSnapshotOnlyName, _ := containerGetParentAndSnapshotName(s.volume.Name)

			oldVolumeName = fmt.Sprintf("%s/snapshot_%s", s.OSDPoolName, srcSnapshotOnlyName)
			newVolumeName = fmt.Sprintf("%s/snapshot_%s", s.OSDPoolName, dstSnapshotOnlyName)
		} else {
			oldVolumeName = fmt.Sprintf("%s/custom_%s", s.OSDPoolName, source.Name)
			newVolumeName = fmt.Sprintf("%s/custom_%s", s.OSDPoolName, s.volume.Name)
		}

		if s.pool.Config["ceph.rbd.clone_copy"] != "" && !shared.IsTrue(s.pool.Config["ceph.rbd.clone_copy"]) {
			// create full copy
			err := cephRBDVolumeCopy(s.ClusterName, oldVolumeName, newVolumeName, s.UserName)
			if err != nil {
				logger.Errorf("Failed to create non-sparse copy of RBD storage volume \"%s\" on storage pool \"%s\": %s", source.Name, source.Pool, err)
				return err
			}
		} else {
			// create sparse copy
			snapshotName := uuid.NewRandom().String()

			// create snapshot of original volume
			err := cephRBDSnapshotCreate(s.ClusterName, s.OSDPoolName, source.Name, storagePoolVolumeTypeNameCustom, snapshotName, s.UserName)
			if err != nil {
				logger.Errorf("Failed to create snapshot of RBD storage volume \"%s\" on storage pool \"%s\": %s", source.Name, source.Pool, err)
				return err
			}

			// protect volume so we can create clones of it
			err = cephRBDSnapshotProtect(s.ClusterName, s.OSDPoolName, source.Name, storagePoolVolumeTypeNameCustom, snapshotName, s.UserName)
			if err != nil {
				logger.Errorf("Failed to protect snapshot for RBD storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
				return err
			}

			// create new clone
			err = cephRBDCloneCreate(s.ClusterName, s.OSDPoolName, source.Name, storagePoolVolumeTypeNameCustom, snapshotName, s.OSDPoolName, s.volume.Name, storagePoolVolumeTypeNameCustom, s.UserName)
			if err != nil {
				logger.Errorf("Failed to clone RBD storage volume \"%s\" on storage pool \"%s\": %s", source.Name, source.Pool, err)
				return err
			}
		}

		RBDDevPath, err := cephRBDVolumeMap(s.ClusterName, s.OSDPoolName, s.volume.Name, storagePoolVolumeTypeNameCustom, s.UserName)
		if err != nil {
			logger.Errorf("Failed to map RBD storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
			return err
		}

		// Generate a new xfs's UUID
		RBDFilesystem := s.getRBDFilesystem()
		msg, err := fsGenerateNewUUID(RBDFilesystem, RBDDevPath)
		if err != nil {
			logger.Errorf("Failed to create new UUID for filesystem \"%s\" for RBD storage volume \"%s\" on storage pool \"%s\": %s: %s", RBDFilesystem, s.volume.Name, s.pool.Name, msg, err)
			return err
		}

		var volumeMntPoint string

		if isSnapshot {
			volumeMntPoint = getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
		} else {
			volumeMntPoint = getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
		}
		err = os.MkdirAll(volumeMntPoint, 0711)
		if err != nil {
			logger.Errorf("Failed to create mountpoint \"%s\" for RBD storage volume \"%s\" on storage pool \"%s\": %s", volumeMntPoint, s.volume.Name, s.pool.Name, err)
			return err
		}

		logger.Infof(successMsg)
		return nil
	}

	// setup storage for the source volume
	srcStorage, err := storagePoolVolumeInit(s.s, "default", source.Pool, source.Name, storagePoolVolumeTypeCustom)
	if err != nil {
		logger.Errorf("Failed to initialize CEPH storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	ourMount, err := srcStorage.StoragePoolVolumeMount()
	if err != nil {
		logger.Errorf("Failed to mount CEPH storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	if ourMount {
		defer srcStorage.StoragePoolVolumeUmount()
	}

	err = s.StoragePoolVolumeCreate()
	if err != nil {
		logger.Errorf("Failed to create RBD storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	ourMount, err = s.StoragePoolVolumeMount()
	if err != nil {
		return err
	}
	if ourMount {
		defer s.StoragePoolVolumeUmount()
	}

	bwlimit := s.pool.Config["rsync.bwlimit"]
	_, err = rsyncLocalCopy(srcMountPoint, dstMountPoint, bwlimit)
	if err != nil {
		os.RemoveAll(dstMountPoint)
		logger.Errorf("Failed to rsync into RBD storage volume \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}

	logger.Infof(successMsg)
	return nil
}

func (s *rbdMigrationSourceDriver) SendStorageVolume(conn *websocket.Conn, op *operation, bwlimit string, storage storage) error {
	msg := fmt.Sprintf("Function not implemented")
	logger.Errorf(msg)
	return fmt.Errorf(msg)
}

func (s *storageCeph) StorageMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	return rsyncStorageMigrationSource(args)
}

func (s *storageCeph) StorageMigrationSink(conn *websocket.Conn, op *operation, args MigrationSinkArgs) error {
	return rsyncStorageMigrationSink(conn, op, args)
}

func (s *storageCeph) GetStoragePool() *api.StoragePool {
	return s.pool
}

func (s *storageCeph) GetStoragePoolVolume() *api.StorageVolume {
	return s.volume
}

func (s *storageCeph) GetState() *state.State {
	return s.s
}

func (s *storageCeph) StoragePoolVolumeSnapshotCreate(target *api.StorageVolumeSnapshotsPost) error {
	logger.Debugf("Creating RBD storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	// This is costly but we need to ensure that all cached data has
	// been committed to disk. If we don't then the rbd snapshot of
	// the underlying filesystem can be inconsistent or - worst case
	// - empty.
	syscall.Sync()
	sourcePath := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	msg, fsFreezeErr := shared.TryRunCommand("fsfreeze", "--freeze", sourcePath)
	logger.Debugf("Trying to freeze the filesystem: %s: %s", msg, fsFreezeErr)
	if fsFreezeErr == nil {
		defer shared.TryRunCommand("fsfreeze", "--unfreeze", sourcePath)
	}

	sourceOnlyName, snapshotOnlyName, _ := containerGetParentAndSnapshotName(target.Name)
	snapshotName := fmt.Sprintf("snapshot_%s", snapshotOnlyName)
	err := cephRBDSnapshotCreate(s.ClusterName, s.OSDPoolName, sourceOnlyName, storagePoolVolumeTypeNameCustom, snapshotName, s.UserName)
	if err != nil {
		logger.Errorf("Failed to create snapshot for RBD storage volume for image \"%s\" on storage pool \"%s\": %s", sourceOnlyName, s.pool.Name, err)
		return err
	}

	targetPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, target.Name)
	err = os.MkdirAll(targetPath, snapshotsDirMode)
	if err != nil {
		logger.Errorf("Failed to create mountpoint \"%s\" for RBD storage volume \"%s\" on storage pool \"%s\": %s", targetPath, s.volume.Name, s.pool.Name, err)
		return err
	}

	logger.Debugf("Created RBD storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageCeph) StoragePoolVolumeSnapshotDelete() error {
	logger.Infof("Deleting CEPH storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	sourceName, snapshotOnlyName, ok := containerGetParentAndSnapshotName(s.volume.Name)
	if !ok {
		return fmt.Errorf("Not a snapshot name")
	}
	snapshotName := fmt.Sprintf("snapshot_%s", snapshotOnlyName)

	rbdVolumeExists := cephRBDSnapshotExists(s.ClusterName, s.OSDPoolName, sourceName, storagePoolVolumeTypeNameCustom, snapshotName, s.UserName)
	if rbdVolumeExists {
		ret := cephContainerSnapshotDelete(s.ClusterName, s.OSDPoolName, sourceName, storagePoolVolumeTypeNameCustom, snapshotName, s.UserName)
		if ret < 0 {
			msg := fmt.Sprintf("Failed to delete RBD storage volume for snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
			logger.Errorf(msg)
			return fmt.Errorf(msg)
		}
	}

	storageVolumeSnapshotPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, sourceName)
	empty, err := shared.PathIsEmpty(storageVolumeSnapshotPath)
	if err == nil && empty {
		os.RemoveAll(storageVolumeSnapshotPath)
	}

	err = s.s.Cluster.StoragePoolVolumeDelete(
		"default",
		s.volume.Name,
		storagePoolVolumeTypeCustom,
		s.poolID)
	if err != nil {
		logger.Errorf(`Failed to delete database entry for DIR storage volume "%s" on storage pool "%s"`,
			s.volume.Name, s.pool.Name)
	}

	logger.Infof("Deleted CEPH storage volume snapshot \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageCeph) StoragePoolVolumeSnapshotRename(newName string) error {
	logger.Infof("Renaming CEPH storage volume on OSD storage pool \"%s\" from \"%s\" to \"%s\"", s.pool.Name, s.volume.Name, newName)

	// unmap
	err := cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName, s.volume.Name, storagePoolVolumeTypeNameCustom, s.UserName, true)
	if err != nil {
		logger.Errorf("Failed to unmap RBD storage volume for container \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}
	logger.Debugf("Unmapped RBD storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	sourceName, _, ok := containerGetParentAndSnapshotName(s.volume.Name)
	if !ok {
		return fmt.Errorf("Not a snapshot name")
	}

	fullSnapshotName := fmt.Sprintf("%s%s%s", sourceName, shared.SnapshotDelimiter, newName)
	err = cephRBDVolumeRename(s.ClusterName, s.OSDPoolName, storagePoolVolumeTypeNameCustom, s.volume.Name, fullSnapshotName, s.UserName)
	if err != nil {
		logger.Errorf("Failed to rename RBD storage volume for container \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, err)
		return err
	}
	logger.Debugf("Renamed RBD storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	oldPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, s.volume.Name)
	newPath := getStoragePoolVolumeSnapshotMountPoint(s.pool.Name, fullSnapshotName)
	err = os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	logger.Infof("Renamed CEPH storage volume on OSD storage pool \"%s\" from \"%s\" to \"%s\"", s.pool.Name, s.volume.Name, newName)

	return s.s.Cluster.StoragePoolVolumeRename("default", s.volume.Name, fullSnapshotName, storagePoolVolumeTypeCustom, s.poolID)
}
