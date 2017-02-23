package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/pborman/uuid"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"

	log "gopkg.in/inconshreveable/log15.v2"
)

var btrfsMntOptions = "user_subvol_rm_allowed"

type storageBtrfs struct {
	storageShared
}

// ${LXD_DIR}/storage-pools/<pool>/containers
func (s *storageBtrfs) getContainerSubvolumePath(poolName string) string {
	return shared.VarPath("storage-pools", poolName, "containers")
}

// ${LXD_DIR}/storage-pools/<pool>/snapshots
func (s *storageBtrfs) getSnapshotSubvolumePath(poolName string, containerName string) string {
	return shared.VarPath("storage-pools", poolName, "snapshots", containerName)
}

// ${LXD_DIR}/storage-pools/<pool>/images
func (s *storageBtrfs) getImageSubvolumePath(poolName string) string {
	return shared.VarPath("storage-pools", poolName, "images")
}

// ${LXD_DIR}/storage-pools/<pool>/custom
func (s *storageBtrfs) getCustomSubvolumePath(poolName string) string {
	return shared.VarPath("storage-pools", poolName, "custom")
}

func (s *storageBtrfs) StorageCoreInit() (*storageCore, error) {
	sCore := storageCore{}
	sCore.sType = storageTypeBtrfs
	typeName, err := storageTypeToString(sCore.sType)
	if err != nil {
		return nil, err
	}
	sCore.sTypeName = typeName

	out, err := exec.LookPath("btrfs")
	if err != nil || len(out) == 0 {
		return nil, fmt.Errorf("The 'btrfs' tool isn't available")
	}

	output, err := exec.Command("btrfs", "version").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("The 'btrfs' tool isn't working properly")
	}

	count, err := fmt.Sscanf(strings.SplitN(string(output), " ", 2)[1], "v%s\n", &sCore.sTypeVersion)
	if err != nil || count != 1 {
		return nil, fmt.Errorf("The 'btrfs' tool isn't working properly")
	}

	err = sCore.initShared()
	if err != nil {
		return nil, err
	}

	s.storageCore = sCore

	return &sCore, nil
}

func (s *storageBtrfs) StoragePoolInit(config map[string]interface{}) (storage, error) {
	_, err := s.StorageCoreInit()
	if err != nil {
		return s, err
	}

	return s, nil
}

func (s *storageBtrfs) StoragePoolCheck() error {
	// FIXEM(brauner): Think of something smart or useful (And then think
	// again if it is worth implementing it. :)).
	return nil
}

func (s *storageBtrfs) StoragePoolCreate() error {
	isBlockDev := false
	source := s.pool.Config["source"]
	if source == "" {
		source = filepath.Join(shared.VarPath("disks"), fmt.Sprintf("%s.img", s.pool.Name))
		s.pool.Config["source"] = source

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

		output, err := exec.Command(
			"mkfs.btrfs",
			"-L", s.pool.Name, source).CombinedOutput()
		if err != nil {
			return fmt.Errorf("Failed to create the BTRFS pool: %s", output)
		}
	} else {
		// Unset size property since it doesn't make sense.
		s.pool.Config["size"] = ""

		if filepath.IsAbs(source) {
			isBlockDev = shared.IsBlockdevPath(source)
			if isBlockDev {
				output, err := exec.Command(
					"mkfs.btrfs",
					"-L", s.pool.Name, source).CombinedOutput()
				if err != nil {
					return fmt.Errorf("Failed to create the BTRFS pool: %s", output)
				}
			} else {
				if isBtrfsSubVolume(source) || s.d.BackingFs == "btrfs" {
					err := btrfsSubVolumeCreate(source)
					if err != nil {
						return err
					}
				} else {
					return fmt.Errorf("Custom loop file locations are not supported.")
				}
			}
		} else {
			return fmt.Errorf("Invalid \"source\" property.")
		}
	}

	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)
	if !shared.PathExists(poolMntPoint) {
		err := os.MkdirAll(poolMntPoint, 0711)
		if err != nil {
			return err
		}
	}

	var err1 error
	var devUUID string
	if isBlockDev && filepath.IsAbs(source) {
		devUUID, _ = shared.LookupUUIDByBlockDevPath(source)
		// The symlink might not have been created even with the delay
		// we granted it above. So try to call btrfs filesystem show and
		// parse it out. (I __hate__ this!)
		if devUUID == "" {
			s.log.Warn("Failed to detect UUID by looking at /dev/disk/by-uuid.")
			devUUID, err1 = s.btrfsLookupFsUUID(source)
			if err1 != nil {
				s.log.Error("Failed to detect UUID by parsing filesystem info.")
				return err1
			}
		}
		s.pool.Config["source"] = devUUID

		// If the symlink in /dev/disk/by-uuid hasn't been created yet
		// aka we only detected it by parsing btrfs filesystem show, we
		// cannot call StoragePoolMount() since it will try to do the
		// reverse operation. So instead we shamelessly mount using the
		// block device path at the time of pool creation.
		err1 = syscall.Mount(source, poolMntPoint, "btrfs", 0, btrfsMntOptions)
	} else {
		_, err1 = s.StoragePoolMount()
	}
	if err1 != nil {
		return err1
	}

	// Enable quotas
	output, err := exec.Command(
		"btrfs", "quota", "enable", poolMntPoint).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Failed to enable quotas on BTRFS pool: %s", output)
	}

	// Create default subvolumes.
	dummyDir := getContainerMountPoint(s.pool.Name, "")
	err = btrfsSubVolumeCreate(dummyDir)
	if err != nil {
		return fmt.Errorf("Could not create btrfs subvolume: %s", dummyDir)
	}

	dummyDir = getSnapshotMountPoint(s.pool.Name, "")
	err = btrfsSubVolumeCreate(dummyDir)
	if err != nil {
		return fmt.Errorf("Could not create btrfs subvolume: %s", dummyDir)
	}

	dummyDir = getImageMountPoint(s.pool.Name, "")
	err = btrfsSubVolumeCreate(dummyDir)
	if err != nil {
		return fmt.Errorf("Could not create btrfs subvolume: %s", dummyDir)
	}

	dummyDir = getStoragePoolVolumeMountPoint(s.pool.Name, "")
	err = btrfsSubVolumeCreate(dummyDir)
	if err != nil {
		return fmt.Errorf("Could not create btrfs subvolume: %s", dummyDir)
	}

	return nil
}

func (s *storageBtrfs) StoragePoolDelete() error {
	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("No \"source\" property found for the storage pool.")
	}

	// Delete default subvolumes.
	dummyDir := getContainerMountPoint(s.pool.Name, "")
	btrfsSubVolumesDelete(dummyDir)

	dummyDir = getSnapshotMountPoint(s.pool.Name, "")
	btrfsSubVolumesDelete(dummyDir)

	dummyDir = getImageMountPoint(s.pool.Name, "")
	btrfsSubVolumesDelete(dummyDir)

	dummyDir = getStoragePoolVolumeMountPoint(s.pool.Name, "")
	btrfsSubVolumesDelete(dummyDir)

	_, err := s.StoragePoolUmount()
	if err != nil {
		return err
	}

	// This is a UUID. Check whether we can find the block device.
	if !filepath.IsAbs(source) {
		// Try to lookup the disk device by UUID but don't fail. If we
		// don't find one this might just mean we have been given the
		// UUID of a subvolume.
		byUUID := fmt.Sprintf("/dev/disk/by-uuid/%s", source)
		diskPath, err := os.Readlink(byUUID)
		msg := ""
		if err == nil {
			msg = fmt.Sprintf("Removing disk device %s with UUID: %s.", diskPath, source)
			diskPath = fmt.Sprintf("/dev/%s", strings.Trim(diskPath, "../../"))
		} else {
			msg = fmt.Sprintf("Failed to lookup disk device with UUID: %s: %s.", source, err)
		}
		s.log.Debug(msg)
	} else {
		var err error
		if s.d.BackingFs == "btrfs" {
			err = btrfsSubVolumesDelete(source)
		} else {
			// This is a loop file --> simply remove it.
			err = os.Remove(source)
		}
		if err != nil {
			return err
		}
	}

	// Remove the mountpoint for the storage pool.
	os.RemoveAll(getStoragePoolMountPoint(s.pool.Name))

	return nil
}

func (s *storageBtrfs) StoragePoolMount() (bool, error) {
	source := s.pool.Config["source"]
	if source == "" {
		return false, fmt.Errorf("No \"source\" property found for the storage pool.")
	}

	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)

	poolMountLockID := getPoolMountLockID(s.pool.Name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[poolMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			s.log.Warn("Received value over semaphore. This should not have happened.")
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

	// Check whether the mount poolMntPoint exits.
	if !shared.PathExists(poolMntPoint) {
		err := os.MkdirAll(poolMntPoint, 0711)
		if err != nil {
			return false, err
		}
	}

	if shared.IsMountPoint(poolMntPoint) {
		return false, nil
	}

	mountSource := source
	if filepath.IsAbs(source) {
		if !shared.IsBlockdevPath(source) && s.d.BackingFs != "btrfs" {
			loopF, err := prepareLoopDev(source)
			if err != nil {
				return false, fmt.Errorf("Could not prepare loop device.")
			}
			mountSource = loopF.Name()
			defer loopF.Close()
		} else {
			return false, nil
		}
	} else {
		// Try to lookup the disk device by UUID but don't fail. If we
		// don't find one this might just mean we have been given the
		// UUID of a subvolume.
		byUUID := fmt.Sprintf("/dev/disk/by-uuid/%s", source)
		diskPath, err := os.Readlink(byUUID)
		if err == nil {
			mountSource = fmt.Sprintf("/dev/%s", strings.Trim(diskPath, "../../"))
		} else {
			// We have very likely been given a subvolume UUID. In
			// this case we should simply assume that the user has
			// mounted the parent of the subvolume or the subvolume
			// itself. Otherwise this becomes a really messy
			// detection task.
			return false, nil
		}

	}

	// This is a block device.
	err := syscall.Mount(mountSource, poolMntPoint, "btrfs", 0, btrfsMntOptions)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (s *storageBtrfs) StoragePoolUmount() (bool, error) {
	poolMntPoint := getStoragePoolMountPoint(s.pool.Name)

	poolUmountLockID := getPoolUmountLockID(s.pool.Name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[poolUmountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			s.log.Warn("Received value over semaphore. This should not have happened.")
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

	if shared.IsMountPoint(poolMntPoint) {
		err := syscall.Unmount(poolMntPoint, 0)
		if err != nil {
			return false, err
		}
	}

	return true, nil
}

func (s *storageBtrfs) StoragePoolUpdate(changedConfig []string) error {
	return fmt.Errorf("Btrfs storage properties cannot be changed.")
}

func (s *storageBtrfs) GetStoragePoolWritable() api.StoragePoolPut {
	return s.pool.Writable()
}

func (s *storageBtrfs) SetStoragePoolWritable(writable *api.StoragePoolPut) {
	s.pool.StoragePoolPut = *writable
}

func (s *storageBtrfs) ContainerPoolGet() string {
	return s.pool.Name
}

func (s *storageBtrfs) ContainerPoolIDGet() int64 {
	return s.poolID
}

// Functions dealing with storage volumes.
func (s *storageBtrfs) StoragePoolVolumeCreate() error {
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Create subvolume path on the storage pool.
	customSubvolumePath := s.getCustomSubvolumePath(s.pool.Name)
	if !shared.PathExists(customSubvolumePath) {
		err := os.MkdirAll(customSubvolumePath, 0700)
		if err != nil {
			return err
		}
	}

	// Create subvolume.
	customSubvolumeName := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	err = btrfsSubVolumeCreate(customSubvolumeName)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageBtrfs) StoragePoolVolumeDelete() error {
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Delete subvolume.
	customSubvolumeName := getStoragePoolVolumeMountPoint(s.pool.Name, s.volume.Name)
	err = btrfsSubVolumesDelete(customSubvolumeName)
	if err != nil {
		return err
	}

	// Delete the mountpoint.
	if shared.PathExists(customSubvolumeName) {
		err = os.Remove(customSubvolumeName)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageBtrfs) StoragePoolVolumeMount() (bool, error) {
	// The storage pool must be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return false, err
	}

	return true, nil
}

func (s *storageBtrfs) StoragePoolVolumeUmount() (bool, error) {
	return true, nil
}

func (s *storageBtrfs) StoragePoolVolumeUpdate(changedConfig []string) error {
	return fmt.Errorf("Btrfs storage properties cannot be changed.")
}

func (s *storageBtrfs) GetStoragePoolVolumeWritable() api.StorageVolumePut {
	return s.volume.Writable()
}

func (s *storageBtrfs) SetStoragePoolVolumeWritable(writable *api.StorageVolumePut) {
	s.volume.StorageVolumePut = *writable
}

// Functions dealing with container storage.
func (s *storageBtrfs) ContainerStorageReady(name string) bool {
	containerMntPoint := getContainerMountPoint(s.pool.Name, name)
	return isBtrfsSubVolume(containerMntPoint)
}

func (s *storageBtrfs) ContainerCreate(container container) error {
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// We can only create the btrfs subvolume under the mounted storage
	// pool. The on-disk layout for containers on a btrfs storage pool will
	// thus be
	// ${LXD_DIR}/storage-pools/<pool>/containers/. The btrfs tool will
	// complain if the intermediate path does not exist, so create it if it
	// doesn't already.
	containerSubvolumePath := s.getContainerSubvolumePath(s.pool.Name)
	if !shared.PathExists(containerSubvolumePath) {
		err := os.MkdirAll(containerSubvolumePath, 0711)
		if err != nil {
			return err
		}
	}

	// Create empty subvolume for container.
	containerSubvolumeName := getContainerMountPoint(s.pool.Name, container.Name())
	err = btrfsSubVolumeCreate(containerSubvolumeName)
	if err != nil {
		return err
	}

	// Create the mountpoint for the container at:
	// ${LXD_DIR}/containers/<name>
	err = createContainerMountpoint(containerSubvolumeName, container.Path(), container.IsPrivileged())
	if err != nil {
		return err
	}

	return container.TemplateApply("create")
}

// And this function is why I started hating on btrfs...
func (s *storageBtrfs) ContainerCreateFromImage(container container, fingerprint string) error {
	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("No \"source\" property found for the storage pool.")
	}

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// We can only create the btrfs subvolume under the mounted storage
	// pool. The on-disk layout for containers on a btrfs storage pool will
	// thus be
	// ${LXD_DIR}/storage-pools/<pool>/containers/. The btrfs tool will
	// complain if the intermediate path does not exist, so create it if it
	// doesn't already.
	containerSubvolumePath := s.getContainerSubvolumePath(s.pool.Name)
	if !shared.PathExists(containerSubvolumePath) {
		err := os.MkdirAll(containerSubvolumePath, 0711)
		if err != nil {
			return err
		}
	}

	// Mountpoint of the image:
	// ${LXD_DIR}/images/<fingerprint>
	imageMntPoint := getImageMountPoint(s.pool.Name, fingerprint)
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
		if !shared.PathExists(imageMntPoint) || !isBtrfsSubVolume(imageMntPoint) {
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

	// Create a rw snapshot at
	// ${LXD_DIR}/storage-pools/<pool>/containers/<name>
	// from the mounted ro image snapshot mounted at
	// ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>
	containerSubvolumeName := getContainerMountPoint(s.pool.Name, container.Name())
	err = s.btrfsPoolVolumesSnapshot(imageMntPoint, containerSubvolumeName, false)
	if err != nil {
		return err
	}

	// Create the mountpoint for the container at:
	// ${LXD_DIR}/containers/<name>
	err = createContainerMountpoint(containerSubvolumeName, container.Path(), container.IsPrivileged())
	if err != nil {
		return err
	}

	if !container.IsPrivileged() {
		if err = s.shiftRootfs(container); err != nil {
			s.ContainerDelete(container)
			return err
		}
	}

	return container.TemplateApply("create")
}

func (s *storageBtrfs) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageBtrfs) ContainerDelete(container container) error {
	// The storage pool needs to be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Delete the subvolume.
	containerSubvolumeName := getContainerMountPoint(s.pool.Name, container.Name())
	err = btrfsSubVolumesDelete(containerSubvolumeName)
	if err != nil {
		return err
	}

	// Delete the container's symlink to the subvolume.
	err = deleteContainerMountpoint(containerSubvolumeName, container.Path(), s.GetStorageTypeName())
	if err != nil {
		return err
	}

	// Delete potential snapshot mountpoints.
	snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, container.Name())
	if shared.PathExists(snapshotMntPoint) {
		err := os.RemoveAll(snapshotMntPoint)
		if err != nil {
			return err
		}
	}

	// Delete potential symlink
	// ${LXD_DIR}/snapshots/<container_name> -> ${POOL}/snapshots/<container_name>
	snapshotSymlink := shared.VarPath("snapshots", container.Name())
	if shared.PathExists(snapshotSymlink) {
		err := os.Remove(snapshotSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageBtrfs) ContainerCopy(container container, sourceContainer container) error {
	// The storage pool needs to be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	err = sourceContainer.StorageStart()
	if err != nil {
		return err
	}
	defer sourceContainer.StorageStop()

	sourcePool := sourceContainer.Storage().ContainerPoolGet()
	sourceContainerSubvolumeName := ""
	if sourceContainer.IsSnapshot() {
		sourceContainerSubvolumeName = getSnapshotMountPoint(sourcePool, sourceContainer.Name())
	} else {
		sourceContainerSubvolumeName = getContainerMountPoint(sourcePool, sourceContainer.Name())
	}

	targetContainerSubvolumeName := ""
	if container.IsSnapshot() {
		targetContainerSubvolumeName = getSnapshotMountPoint(s.pool.Name, container.Name())
	} else {
		targetContainerSubvolumeName = getContainerMountPoint(s.pool.Name, container.Name())
	}
	if s.ContainerPoolGet() == sourcePool {
		// COMMNET(brauner): They are on the same storage pool which
		// means they both use btrfs. So we can simply create a new
		// snapshot of the source container. For this we only mount the
		// btrfs storage pool and snapshot the subvolume names. No
		// f*cking around with mounting the containers.
		ourMount, err := s.StoragePoolMount()
		if err != nil {
			return err
		}
		if ourMount {
			defer s.StoragePoolUmount()
		}

		err = s.btrfsPoolVolumesSnapshot(sourceContainerSubvolumeName, targetContainerSubvolumeName, false)
		if err != nil {
			return err
		}

		err = createContainerMountpoint(targetContainerSubvolumeName, container.Path(), container.IsPrivileged())
		if err != nil {
			return err
		}

		// Make sure that template apply finds the
		// container mounted.
		ourMount, err = s.ContainerMount(container.Name(), container.Path())
		if err != nil {
			return err
		}
		if ourMount {
			defer s.ContainerUmount(container.Name(), container.Path())
		}
	} else {
		// Create an empty btrfs storage volume.
		err = s.ContainerCreate(container)
		if err != nil {
			return err
		}

		// Use rsync to fill the empty volume.
		output, err := storageRsyncCopy(sourceContainerSubvolumeName, targetContainerSubvolumeName)
		if err != nil {
			s.ContainerDelete(container)
			s.log.Error("ContainerCopy: rsync failed", log.Ctx{"output": string(output)})
			return fmt.Errorf("rsync failed: %s", string(output))
		}
	}

	err = s.setUnprivUserAcl(sourceContainer, targetContainerSubvolumeName)
	if err != nil {
		s.ContainerDelete(container)
		return err
	}

	return container.TemplateApply("copy")
}

func (s *storageBtrfs) ContainerMount(name string, path string) (bool, error) {
	// The storage pool must be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return false, err
	}

	return true, nil
}

func (s *storageBtrfs) ContainerUmount(name string, path string) (bool, error) {
	return true, nil
}

func (s *storageBtrfs) ContainerRename(container container, newName string) error {
	// The storage pool must be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	oldContainerSubvolumeName := getContainerMountPoint(s.pool.Name, container.Name())
	newContainerSubvolumeName := getContainerMountPoint(s.pool.Name, newName)
	err = os.Rename(oldContainerSubvolumeName, newContainerSubvolumeName)
	if err != nil {
		return err
	}

	newSymlink := shared.VarPath("containers", newName)
	err = renameContainerMountpoint(oldContainerSubvolumeName, container.Path(), newContainerSubvolumeName, newSymlink)
	if err != nil {
		return err
	}

	oldSnapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, container.Name())
	newSnapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, newName)
	if shared.PathExists(oldSnapshotSubvolumeName) {
		err = os.Rename(oldSnapshotSubvolumeName, newSnapshotSubvolumeName)
		if err != nil {
			return err
		}
	}

	oldSnapshotSymlink := shared.VarPath("snapshots", container.Name())
	newSnapshotSymlink := shared.VarPath("snapshots", newName)
	if shared.PathExists(oldSnapshotSymlink) {
		err := os.Remove(oldSnapshotSymlink)
		if err != nil {
			return err
		}

		err = os.Symlink(newSnapshotSubvolumeName, newSnapshotSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageBtrfs) ContainerRestore(container container, sourceContainer container) error {
	// The storage pool must be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Create a backup so we can revert.
	targetContainerSubvolumeName := getContainerMountPoint(s.pool.Name, container.Name())
	backupTargetContainerSubvolumeName := fmt.Sprintf("%s.back", targetContainerSubvolumeName)
	err = os.Rename(targetContainerSubvolumeName, backupTargetContainerSubvolumeName)
	if err != nil {
		return err
	}
	undo := true
	defer func() {
		if undo {
			os.Rename(backupTargetContainerSubvolumeName, targetContainerSubvolumeName)
		}
	}()

	err = sourceContainer.StorageStart()
	if err != nil {
		return err
	}
	defer sourceContainer.StorageStop()

	// Mount the source container.
	srcContainerStorage := sourceContainer.Storage()
	sourcePool := srcContainerStorage.ContainerPoolGet()
	sourceContainerSubvolumeName := ""
	if sourceContainer.IsSnapshot() {
		sourceContainerSubvolumeName = getSnapshotMountPoint(sourcePool, sourceContainer.Name())
	} else {
		sourceContainerSubvolumeName = getContainerMountPoint(sourcePool, sourceContainer.Name())
	}

	var failure error
	if s.ContainerPoolGet() == sourcePool {
		// They are on the same storage pool, so we can simply snapshot.
		err := s.btrfsPoolVolumesSnapshot(sourceContainerSubvolumeName, targetContainerSubvolumeName, false)
		if err != nil {
			failure = err
		}
	} else {
		err := btrfsSubVolumeCreate(targetContainerSubvolumeName)
		if err == nil {
			// Use rsync to fill the empty volume.  Sync by using
			// the subvolume name.
			output, err := storageRsyncCopy(sourceContainerSubvolumeName, targetContainerSubvolumeName)
			if err != nil {
				s.ContainerDelete(container)
				s.log.Error("ContainerRestore: rsync failed", log.Ctx{"output": string(output)})
				failure = err
			}
		} else {
			failure = err
		}
	}

	// Now allow unprivileged users to access its data.
	err = s.setUnprivUserAcl(sourceContainer, targetContainerSubvolumeName)
	if err != nil {
		failure = err
	}

	if failure == nil {
		undo = false

		if s.ContainerPoolGet() == srcContainerStorage.ContainerPoolGet() {
			// Remove the backup, we made
			return btrfsSubVolumesDelete(backupTargetContainerSubvolumeName)
		}
		os.RemoveAll(backupTargetContainerSubvolumeName)
	}

	return failure
}

func (s *storageBtrfs) ContainerSetQuota(container container, size int64) error {
	subvol := container.Path()

	_, err := btrfsSubVolumeQGroup(subvol)
	if err != nil {
		return err
	}

	output, err := exec.Command(
		"btrfs",
		"qgroup",
		"limit",
		"-e", fmt.Sprintf("%d", size),
		subvol).CombinedOutput()

	if err != nil {
		return fmt.Errorf("Failed to set btrfs quota: %s", output)
	}

	return nil
}

func (s *storageBtrfs) ContainerGetUsage(container container) (int64, error) {
	return s.btrfsPoolVolumeQGroupUsage(container.Path())
}

func (s *storageBtrfs) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// We can only create the btrfs subvolume under the mounted storage
	// pool. The on-disk layout for snapshots on a btrfs storage pool will
	// thus be
	// ${LXD_DIR}/storage-pools/<pool>/snapshots/. The btrfs tool will
	// complain if the intermediate path does not exist, so create it if it
	// doesn't already.
	snapshotSubvolumePath := s.getSnapshotSubvolumePath(s.pool.Name, sourceContainer.Name())
	if !shared.PathExists(snapshotSubvolumePath) {
		err := os.MkdirAll(snapshotSubvolumePath, 0711)
		if err != nil {
			return err
		}
	}

	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "snapshots", s.volume.Name)
	snapshotMntPointSymlink := shared.VarPath("snapshots", sourceContainer.Name())
	if !shared.PathExists(snapshotMntPointSymlink) {
		err := createContainerMountpoint(snapshotMntPointSymlinkTarget, snapshotMntPointSymlink, snapshotContainer.IsPrivileged())
		if err != nil {
			return err
		}
	}

	srcContainerSubvolumeName := getContainerMountPoint(s.pool.Name, sourceContainer.Name())
	snapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, snapshotContainer.Name())
	err = s.btrfsPoolVolumesSnapshot(srcContainerSubvolumeName, snapshotSubvolumeName, true)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageBtrfs) ContainerSnapshotDelete(snapshotContainer container) error {
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	snapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, snapshotContainer.Name())
	err = btrfsSubVolumesDelete(snapshotSubvolumeName)
	if err != nil {
		return err
	}

	sourceSnapshotMntPoint := shared.VarPath("snapshots", snapshotContainer.Name())
	os.Remove(sourceSnapshotMntPoint)
	os.Remove(snapshotSubvolumeName)

	sourceFields := strings.SplitN(snapshotContainer.Name(), shared.SnapshotDelimiter, 2)
	sourceName := sourceFields[0]
	snapshotSubvolumePath := s.getSnapshotSubvolumePath(s.pool.Name, sourceName)
	os.Remove(snapshotSubvolumePath)
	if !shared.PathExists(snapshotSubvolumePath) {
		snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
		os.Remove(snapshotMntPointSymlink)
	}

	return nil
}

func (s *storageBtrfs) ContainerSnapshotStart(container container) error {
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	snapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, container.Name())
	roSnapshotSubvolumeName := fmt.Sprintf("%s.ro", snapshotSubvolumeName)
	if shared.PathExists(roSnapshotSubvolumeName) {
		return fmt.Errorf("The snapshot is already mounted read-write.")
	}

	err = os.Rename(snapshotSubvolumeName, roSnapshotSubvolumeName)
	if err != nil {
		return err
	}

	err = s.btrfsPoolVolumesSnapshot(roSnapshotSubvolumeName, snapshotSubvolumeName, false)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageBtrfs) ContainerSnapshotStop(container container) error {
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	snapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, container.Name())
	roSnapshotSubvolumeName := fmt.Sprintf("%s.ro", snapshotSubvolumeName)
	if !shared.PathExists(roSnapshotSubvolumeName) {
		return fmt.Errorf("The snapshot isn't currently mounted read-write.")
	}

	err = btrfsSubVolumesDelete(snapshotSubvolumeName)
	if err != nil {
		return err
	}

	err = os.Rename(roSnapshotSubvolumeName, snapshotSubvolumeName)
	if err != nil {
		return err
	}

	return nil
}

// ContainerSnapshotRename renames a snapshot of a container.
func (s *storageBtrfs) ContainerSnapshotRename(snapshotContainer container, newName string) error {
	// The storage pool must be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Unmount the snapshot if it is mounted otherwise we'll get EBUSY.
	// Rename the subvolume on the storage pool.
	oldSnapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, snapshotContainer.Name())
	newSnapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, newName)
	err = os.Rename(oldSnapshotSubvolumeName, newSnapshotSubvolumeName)
	if err != nil {
		return err
	}

	return nil
}

// Needed for live migration where an empty snapshot needs to be created before
// rsyncing into it.
func (s *storageBtrfs) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	// Mount the storage pool.
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Create the snapshot subvole path on the storage pool.
	sourceFields := strings.SplitN(snapshotContainer.Name(), shared.SnapshotDelimiter, 2)
	sourceName := sourceFields[0]
	snapshotSubvolumePath := s.getSnapshotSubvolumePath(s.pool.Name, sourceName)
	snapshotSubvolumeName := getSnapshotMountPoint(s.pool.Name, snapshotContainer.Name())
	if !shared.PathExists(snapshotSubvolumePath) {
		err := os.MkdirAll(snapshotSubvolumePath, 0711)
		if err != nil {
			return err
		}
	}

	err = btrfsSubVolumeCreate(snapshotSubvolumeName)
	if err != nil {
		return err
	}

	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "snapshots", sourceName)
	snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
	if !shared.PathExists(snapshotMntPointSymlink) {
		err := createContainerMountpoint(snapshotMntPointSymlinkTarget, snapshotMntPointSymlink, snapshotContainer.IsPrivileged())
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageBtrfs) ImageCreate(fingerprint string) error {
	// Create the subvolume.
	source := s.pool.Config["source"]
	if source == "" {
		return fmt.Errorf("No \"source\" property found for the storage pool.")
	}

	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	err = s.createImageDbPoolVolume(fingerprint)
	if err != nil {
		return err
	}

	// We can only create the btrfs subvolume under the mounted storage
	// pool. The on-disk layout for images on a btrfs storage pool will thus
	// be
	// ${LXD_DIR}/storage-pools/<pool>/images/. The btrfs tool will
	// complain if the intermediate path does not exist, so create it if it
	// doesn't already.
	imageSubvolumePath := s.getImageSubvolumePath(s.pool.Name)
	if !shared.PathExists(imageSubvolumePath) {
		err := os.MkdirAll(imageSubvolumePath, 0700)
		if err != nil {
			return err
		}
	}

	// Create a temporary rw btrfs subvolume. From this rw subvolume we'll
	// create a ro snapshot below. The path with which we do this is
	// ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>@<pool>_tmp.
	imageSubvolumeName := getImageMountPoint(s.pool.Name, fingerprint)
	tmpImageSubvolumeName := fmt.Sprintf("%s_tmp", imageSubvolumeName)
	err = btrfsSubVolumeCreate(tmpImageSubvolumeName)
	if err != nil {
		return err
	}
	// Delete volume on error.
	undo := true
	defer func() {
		if undo {
			btrfsSubVolumesDelete(tmpImageSubvolumeName)
		}
	}()

	// Unpack the image in imageMntPoint.
	imagePath := shared.VarPath("images", fingerprint)
	err = unpackImage(s.d, imagePath, tmpImageSubvolumeName, storageTypeBtrfs)
	if err != nil {
		return err
	}

	// Now create a read-only snapshot of the subvolume.
	// The path with which we do this is
	// ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>.
	err = s.btrfsPoolVolumesSnapshot(tmpImageSubvolumeName, imageSubvolumeName, true)
	if err != nil {
		return err
	}

	defer func() {
		if undo {
			btrfsSubVolumesDelete(imageSubvolumeName)
		}
	}()

	err = btrfsSubVolumesDelete(tmpImageSubvolumeName)
	if err != nil {
		return err
	}

	undo = false

	return nil
}

func (s *storageBtrfs) ImageDelete(fingerprint string) error {
	_, err := s.StoragePoolMount()
	if err != nil {
		return err
	}

	// Delete the btrfs subvolume. The path with which we
	// do this is ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>.
	imageSubvolumeName := getImageMountPoint(s.pool.Name, fingerprint)
	err = btrfsSubVolumesDelete(imageSubvolumeName)
	if err != nil {
		return err
	}

	err = s.deleteImageDbPoolVolume(fingerprint)
	if err != nil {
		return err
	}

	// Now delete the mountpoint for the image:
	// ${LXD_DIR}/images/<fingerprint>.
	if shared.PathExists(imageSubvolumeName) {
		err := os.RemoveAll(imageSubvolumeName)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageBtrfs) ImageMount(fingerprint string) (bool, error) {
	// The storage pool must be mounted.
	_, err := s.StoragePoolMount()
	if err != nil {
		return false, err
	}

	return true, nil
}

func (s *storageBtrfs) ImageUmount(fingerprint string) (bool, error) {
	return true, nil
}

func btrfsSubVolumeCreate(subvol string) error {
	parentDestPath := filepath.Dir(subvol)
	if !shared.PathExists(parentDestPath) {
		if err := os.MkdirAll(parentDestPath, 0711); err != nil {
			return err
		}
	}

	output, err := exec.Command(
		"btrfs",
		"subvolume",
		"create",
		subvol).CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"btrfs subvolume create failed, subvol=%s, output%s",
			subvol,
			string(output),
		)
	}

	return nil
}

func btrfsSubVolumeQGroup(subvol string) (string, error) {
	output, err := exec.Command(
		"btrfs",
		"qgroup",
		"show",
		subvol,
		"-e",
		"-f").CombinedOutput()

	if err != nil {
		return "", fmt.Errorf("btrfs quotas not supported. Try enabling them with 'btrfs quota enable'.")
	}

	var qgroup string
	for _, line := range strings.Split(string(output), "\n") {
		if line == "" || strings.HasPrefix(line, "qgroupid") || strings.HasPrefix(line, "---") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}

		qgroup = fields[0]
	}

	if qgroup == "" {
		return "", fmt.Errorf("Unable to find quota group")
	}

	return qgroup, nil
}

func (s *storageBtrfs) btrfsPoolVolumeQGroupUsage(subvol string) (int64, error) {
	output, err := exec.Command(
		"btrfs",
		"qgroup",
		"show",
		subvol,
		"-e",
		"-f").CombinedOutput()

	if err != nil {
		return -1, fmt.Errorf("btrfs quotas not supported. Try enabling them with 'btrfs quota enable'.")
	}

	for _, line := range strings.Split(string(output), "\n") {
		if line == "" || strings.HasPrefix(line, "qgroupid") || strings.HasPrefix(line, "---") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}

		usage, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			continue
		}

		return usage, nil
	}

	return -1, fmt.Errorf("Unable to find current qgroup usage")
}

func btrfsSubVolumeDelete(subvol string) error {
	// Attempt (but don't fail on) to delete any qgroup on the subvolume
	qgroup, err := btrfsSubVolumeQGroup(subvol)
	if err == nil {
		exec.Command(
			"btrfs",
			"qgroup",
			"destroy",
			qgroup,
			subvol).Run()
	}

	// Attempt to make the subvolume writable
	exec.Command("btrfs", "property", "set", subvol, "ro", "false").CombinedOutput()

	// Delete the subvolume itself
	err = exec.Command(
		"btrfs",
		"subvolume",
		"delete",
		subvol,
	).Run()

	return nil
}

// btrfsPoolVolumesDelete is the recursive variant on btrfsPoolVolumeDelete,
// it first deletes subvolumes of the subvolume and then the
// subvolume itself.
func btrfsSubVolumesDelete(subvol string) error {
	// Delete subsubvols.
	subsubvols, err := btrfsSubVolumesGet(subvol)
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(subsubvols)))

	for _, subsubvol := range subsubvols {
		if err := btrfsSubVolumeDelete(path.Join(subvol, subsubvol)); err != nil {
			return err
		}
	}

	// Delete the subvol itself
	if err := btrfsSubVolumeDelete(subvol); err != nil {
		return err
	}

	return nil
}

/*
 * btrfsSnapshot creates a snapshot of "source" to "dest"
 * the result will be readonly if "readonly" is True.
 */
func btrfsSnapshot(source string, dest string, readonly bool) error {
	var output []byte
	var err error
	if readonly {
		output, err = exec.Command(
			"btrfs",
			"subvolume",
			"snapshot",
			"-r",
			source,
			dest).CombinedOutput()
	} else {
		output, err = exec.Command(
			"btrfs",
			"subvolume",
			"snapshot",
			source,
			dest).CombinedOutput()
	}
	if err != nil {
		return fmt.Errorf(
			"subvolume snapshot failed, source=%s, dest=%s, output=%s",
			source,
			dest,
			string(output),
		)
	}

	return err
}

func (s *storageBtrfs) btrfsPoolVolumeSnapshot(source string, dest string, readonly bool) error {
	return btrfsSnapshot(source, dest, readonly)
}

func (s *storageBtrfs) btrfsPoolVolumesSnapshot(source string, dest string, readonly bool) error {
	// Get a list of subvolumes of the root
	subsubvols, err := btrfsSubVolumesGet(source)
	if err != nil {
		return err
	}
	sort.Sort(sort.StringSlice(subsubvols))

	if len(subsubvols) > 0 && readonly {
		// A root with subvolumes can never be readonly,
		// also don't make subvolumes readonly.
		readonly = false

		s.log.Warn(
			"Subvolumes detected, ignoring ro flag",
			log.Ctx{"source": source, "dest": dest})
	}

	// First snapshot the root
	if err := s.btrfsPoolVolumeSnapshot(source, dest, readonly); err != nil {
		return err
	}

	// Now snapshot all subvolumes of the root.
	for _, subsubvol := range subsubvols {
		// Clear the target for the subvol to use
		os.Remove(path.Join(dest, subsubvol))

		if err := s.btrfsPoolVolumeSnapshot(path.Join(source, subsubvol), path.Join(dest, subsubvol), readonly); err != nil {
			return err
		}
	}

	return nil
}

/*
 * isBtrfsSubVolume returns true if the given Path is a btrfs subvolume
 * else false.
 */
func isBtrfsSubVolume(subvolPath string) bool {
	fs := syscall.Stat_t{}
	err := syscall.Lstat(subvolPath, &fs)
	if err != nil {
		return false
	}

	// Check if BTRFS_FIRST_FREE_OBJECTID
	if fs.Ino != 256 {
		return false
	}

	return true
}

func btrfsSubVolumesGet(path string) ([]string, error) {
	result := []string{}

	if !strings.HasSuffix(path, "/") {
		path = path + "/"
	}

	// Unprivileged users can't get to fs internals
	filepath.Walk(path, func(fpath string, fi os.FileInfo, err error) error {
		// Skip walk errors
		if err != nil {
			return nil
		}

		// Ignore the base path
		if strings.TrimRight(fpath, "/") == strings.TrimRight(path, "/") {
			return nil
		}

		// Subvolumes can only be directories
		if !fi.IsDir() {
			return nil
		}

		// Check if a btrfs subvolume
		if isBtrfsSubVolume(fpath) {
			result = append(result, strings.TrimPrefix(fpath, path))
		}

		return nil
	})

	return result, nil
}

type btrfsMigrationSourceDriver struct {
	container          container
	snapshots          []container
	btrfsSnapshotNames []string
	btrfs              *storageBtrfs
	runningSnapName    string
	stoppedSnapName    string
}

func (s *btrfsMigrationSourceDriver) Snapshots() []container {
	return s.snapshots
}

func (s *btrfsMigrationSourceDriver) send(conn *websocket.Conn, btrfsPath string, btrfsParent string, readWrapper func(io.ReadCloser) io.ReadCloser) error {
	args := []string{"send", btrfsPath}
	if btrfsParent != "" {
		args = append(args, "-p", btrfsParent)
	}

	cmd := exec.Command("btrfs", args...)

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
		shared.LogErrorf("Problem reading btrfs send stderr: %s.")
	}

	err = cmd.Wait()
	if err != nil {
		shared.LogErrorf("Problem with btrfs send: %s.", string(output))
	}
	return err
}

func (s *btrfsMigrationSourceDriver) SendWhileRunning(conn *websocket.Conn, op *operation) error {
	containerPool := s.container.Storage().ContainerPoolGet()
	containerName := s.container.Name()
	containersPath := getContainerMountPoint(containerPool, "")
	sourceName := containerName

	// Deal with sending a snapshot to create a container on another LXD
	// instance.
	if s.container.IsSnapshot() {
		sourceFields := strings.SplitN(containerName, shared.SnapshotDelimiter, 2)
		sourceName = sourceFields[0]
		snapshotsPath := getSnapshotMountPoint(containerPool, sourceName)
		tmpContainerMntPoint, err := ioutil.TempDir(snapshotsPath, sourceName)
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpContainerMntPoint)

		err = os.Chmod(tmpContainerMntPoint, 0700)
		if err != nil {
			return err
		}

		migrationSendSnapshot := fmt.Sprintf("%s/.migration-send", tmpContainerMntPoint)
		snapshotMntPoint := getSnapshotMountPoint(containerPool, containerName)
		if s.container.IsSnapshot() {
		}
		err = s.btrfs.btrfsPoolVolumesSnapshot(snapshotMntPoint, migrationSendSnapshot, true)
		if err != nil {
			return err
		}
		defer btrfsSubVolumesDelete(migrationSendSnapshot)

		wrapper := StorageProgressReader(op, "fs_progress", containerName)
		return s.send(conn, migrationSendSnapshot, "", wrapper)
	}

	for i, snap := range s.snapshots {
		prev := ""
		if i > 0 {
			prev = getSnapshotMountPoint(containerPool, s.snapshots[i-1].Name())
		}

		snapMntPoint := getSnapshotMountPoint(containerPool, snap.Name())
		wrapper := StorageProgressReader(op, "fs_progress", snap.Name())
		if err := s.send(conn, snapMntPoint, prev, wrapper); err != nil {
			return err
		}
	}

	tmpContainerMntPoint, err := ioutil.TempDir(containersPath, containerName)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	err = os.Chmod(tmpContainerMntPoint, 0700)
	if err != nil {
		return err
	}

	migrationSendSnapshot := fmt.Sprintf("%s/.migration-send", tmpContainerMntPoint)
	containerMntPoint := getContainerMountPoint(containerPool, sourceName)
	if s.container.IsSnapshot() {
	}
	err = s.btrfs.btrfsPoolVolumesSnapshot(containerMntPoint, migrationSendSnapshot, true)
	if err != nil {
		return err
	}
	defer btrfsSubVolumesDelete(migrationSendSnapshot)

	wrapper := StorageProgressReader(op, "fs_progress", containerName)
	return s.send(conn, migrationSendSnapshot, "", wrapper)
}

func (s *btrfsMigrationSourceDriver) SendAfterCheckpoint(conn *websocket.Conn) error {
	tmpPath := containerPath(fmt.Sprintf("%s/.migration-send-%s", s.container.Name(), uuid.NewRandom().String()), true)
	err := os.MkdirAll(tmpPath, 0700)
	if err != nil {
		return err
	}

	s.stoppedSnapName = fmt.Sprintf("%s/.root", tmpPath)
	if err := s.btrfs.btrfsPoolVolumesSnapshot(s.container.Path(), s.stoppedSnapName, true); err != nil {
		return err
	}

	return s.send(conn, s.stoppedSnapName, s.runningSnapName, nil)
}

func (s *btrfsMigrationSourceDriver) Cleanup() {
	if s.stoppedSnapName != "" {
		btrfsSubVolumesDelete(s.stoppedSnapName)
	}

	if s.runningSnapName != "" {
		btrfsSubVolumesDelete(s.runningSnapName)
	}
}

func (s *storageBtrfs) MigrationType() MigrationFSType {
	if runningInUserns {
		return MigrationFSType_RSYNC
	} else {
		return MigrationFSType_BTRFS
	}
}

func (s *storageBtrfs) PreservesInodes() bool {
	if runningInUserns {
		return false
	} else {
		return true
	}
}

func (s *storageBtrfs) MigrationSource(c container) (MigrationStorageSourceDriver, error) {
	if runningInUserns {
		return rsyncMigrationSource(c)
	}

	/* List all the snapshots in order of reverse creation. The idea here
	 * is that we send the oldest to newest snapshot, hopefully saving on
	 * xfer costs. Then, after all that, we send the container itself.
	 */
	snapshots, err := c.Snapshots()
	if err != nil {
		return nil, err
	}

	driver := &btrfsMigrationSourceDriver{
		container:          c,
		snapshots:          snapshots,
		btrfsSnapshotNames: []string{},
		btrfs:              s,
	}

	for _, snap := range snapshots {
		btrfsPath := snap.Path()
		driver.btrfsSnapshotNames = append(driver.btrfsSnapshotNames, btrfsPath)
	}

	return driver, nil
}

func (s *storageBtrfs) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation) error {
	if runningInUserns {
		return rsyncMigrationSink(live, container, snapshots, conn, srcIdmap, op)
	}

	btrfsRecv := func(snapName string, btrfsPath string, targetPath string, isSnapshot bool, writeWrapper func(io.WriteCloser) io.WriteCloser) error {
		args := []string{"receive", "-e", btrfsPath}
		cmd := exec.Command("btrfs", args...)

		// Remove the existing pre-created subvolume
		err := btrfsSubVolumesDelete(targetPath)
		if err != nil {
			return err
		}

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
			s.log.Debug(fmt.Sprintf("problem reading btrfs receive stderr %s", err))
		}

		err = cmd.Wait()
		if err != nil {
			s.log.Error("problem with btrfs receive", log.Ctx{"output": string(output)})
			return err
		}

		if !isSnapshot {
			btrfsPath = fmt.Sprintf("%s/.migration-send", btrfsPath)
			err = s.btrfsPoolVolumesSnapshot(btrfsPath, targetPath, false)
		} else {
			btrfsPath = fmt.Sprintf("%s/%s", btrfsPath, snapName)
			err = s.btrfsPoolVolumesSnapshot(btrfsPath, targetPath, true)
		}
		if err != nil {
			s.log.Error("problem with btrfs snapshot", log.Ctx{"err": err})
			return err
		}

		err = btrfsSubVolumesDelete(btrfsPath)
		if err != nil {
			s.log.Error("problem with btrfs delete", log.Ctx{"err": err})
			return err
		}

		os.RemoveAll(btrfsPath)

		return nil
	}

	containerPool := container.Storage().ContainerPoolGet()
	containersPath := getSnapshotMountPoint(containerPool, container.Name())
	if len(snapshots) > 0 {
		err := os.MkdirAll(containersPath, 0700)
		if err != nil {
			return err
		}

		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", containerPool, "snapshots", container.Name())
		snapshotMntPointSymlink := shared.VarPath("snapshots", container.Name())
		if !shared.PathExists(snapshotMntPointSymlink) {
			err := os.Symlink(snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
			if err != nil {
				return err
			}
		}
	}

	for _, snap := range snapshots {
		args := snapshotProtobufToContainerArgs(container.Name(), snap)
		// Unset the pool of the orginal container and let
		// containerLXCCreate figure out on which pool to  send it.
		// Later we might make this more flexible.
		for k, v := range args.Devices {
			if v["type"] == "disk" && v["path"] == "/" {
				args.Devices[k]["pool"] = ""
			}
		}
		containerMntPoint := getSnapshotMountPoint(containerPool, args.Name)
		_, err := containerCreateEmptySnapshot(container.Daemon(), args)
		if err != nil {
			return err
		}
		tmpContainerMntPoint, err := ioutil.TempDir(containersPath, container.Name())
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpContainerMntPoint)

		err = os.Chmod(tmpContainerMntPoint, 0700)
		if err != nil {
			return err
		}

		wrapper := StorageProgressWriter(op, "fs_progress", *snap.Name)
		err = btrfsRecv(*snap.Name, tmpContainerMntPoint, containerMntPoint, true, wrapper)
		if err != nil {
			return err
		}
	}

	containersMntPoint := getContainerMountPoint(s.pool.Name, "")
	err := createContainerMountpoint(containersMntPoint, container.Path(), container.IsPrivileged())
	if err != nil {
		return err
	}

	/* finally, do the real container */
	wrapper := StorageProgressWriter(op, "fs_progress", container.Name())
	tmpContainerMntPoint, err := ioutil.TempDir(containersMntPoint, container.Name())
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	err = os.Chmod(tmpContainerMntPoint, 0700)
	if err != nil {
		return err
	}

	containerMntPoint := getContainerMountPoint(s.pool.Name, container.Name())
	err = btrfsRecv("", tmpContainerMntPoint, containerMntPoint, false, wrapper)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageBtrfs) btrfsLookupFsUUID(fs string) (string, error) {
	output, err := exec.Command(
		"btrfs",
		"filesystem",
		"show",
		"--raw",
		fs).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Failed to detect UUID.")
	}

	outputString := string(output)
	idx := strings.Index(outputString, "uuid: ")
	outputString = outputString[idx+6:]
	outputString = strings.TrimSpace(outputString)
	idx = strings.Index(outputString, "\t")
	outputString = outputString[:idx]
	outputString = strings.Trim(outputString, "\n")

	return outputString, nil
}
