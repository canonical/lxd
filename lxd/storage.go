package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logging"

	log "gopkg.in/inconshreveable/log15.v2"
)

// lxdStorageLockMap is a hashmap that allows functions to check whether the
// operation they are about to perform is already in progress. If it is the
// channel can be used to wait for the operation to finish. If it is not, the
// function that wants to perform the operation should store its code in the
// hashmap.
// Note that any access to this map must be done while holding a lock.
var lxdStorageOngoingOperationMap = map[string]chan bool{}

// lxdStorageMapLock is used to access lxdStorageOngoingOperationMap.
var lxdStorageMapLock sync.Mutex

// The following functions are used to construct simple operation codes that are
// unique.
func getPoolMountLockID(poolName string) string {
	return fmt.Sprintf("mount/pool/%s", poolName)
}

func getPoolUmountLockID(poolName string) string {
	return fmt.Sprintf("umount/pool/%s", poolName)
}

func getImageCreateLockID(poolName string, fingerprint string) string {
	return fmt.Sprintf("create/image/%s/%s", poolName, fingerprint)
}

func getContainerMountLockID(poolName string, containerName string) string {
	return fmt.Sprintf("mount/container/%s/%s", poolName, containerName)
}

func getContainerUmountLockID(poolName string, containerName string) string {
	return fmt.Sprintf("umount/container/%s/%s", poolName, containerName)
}

// Filesystem magic numbers
const (
	filesystemSuperMagicTmpfs = 0x01021994
	filesystemSuperMagicExt4  = 0xEF53
	filesystemSuperMagicXfs   = 0x58465342
	filesystemSuperMagicNfs   = 0x6969
	filesystemSuperMagicZfs   = 0x2fc12fc1
)

// filesystemDetect returns the filesystem on which the passed-in path sits.
func filesystemDetect(path string) (string, error) {
	fs := syscall.Statfs_t{}

	err := syscall.Statfs(path, &fs)
	if err != nil {
		return "", err
	}

	switch fs.Type {
	case filesystemSuperMagicBtrfs:
		return "btrfs", nil
	case filesystemSuperMagicZfs:
		return "zfs", nil
	case filesystemSuperMagicTmpfs:
		return "tmpfs", nil
	case filesystemSuperMagicExt4:
		return "ext4", nil
	case filesystemSuperMagicXfs:
		return "xfs", nil
	case filesystemSuperMagicNfs:
		return "nfs", nil
	default:
		shared.LogDebugf("Unknown backing filesystem type: 0x%x", fs.Type)
		return string(fs.Type), nil
	}
}

// storageRsyncCopy copies a directory using rsync (with the --devices option).
func storageRsyncCopy(source string, dest string) (string, error) {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return "", err
	}

	rsyncVerbosity := "-q"
	if debug {
		rsyncVerbosity = "-vi"
	}

	output, err := exec.Command(
		"rsync",
		"-a",
		"-HAX",
		"--devices",
		"--delete",
		"--checksum",
		"--numeric-ids",
		rsyncVerbosity,
		shared.AddSlash(source),
		dest).CombinedOutput()

	return string(output), err
}

// storageType defines the type of a storage
type storageType int

const (
	storageTypeBtrfs storageType = iota
	storageTypeZfs
	storageTypeLvm
	storageTypeDir
	storageTypeMock
)

var supportedStorageTypes = []string{"btrfs", "zfs", "lvm", "dir"}

func storageTypeToString(sType storageType) (string, error) {
	switch sType {
	case storageTypeBtrfs:
		return "btrfs", nil
	case storageTypeZfs:
		return "zfs", nil
	case storageTypeLvm:
		return "lvm", nil
	case storageTypeMock:
		return "mock", nil
	case storageTypeDir:
		return "dir", nil
	}

	return "", fmt.Errorf("Invalid storage type.")
}

func storageStringToType(sName string) (storageType, error) {
	switch sName {
	case "btrfs":
		return storageTypeBtrfs, nil
	case "zfs":
		return storageTypeZfs, nil
	case "lvm":
		return storageTypeLvm, nil
	case "mock":
		return storageTypeMock, nil
	case "dir":
		return storageTypeDir, nil
	}

	return -1, fmt.Errorf("Invalid storage type name.")
}

type MigrationStorageSourceDriver interface {
	/* snapshots for this container, if any */
	Snapshots() []container

	/* send any bits of the container/snapshots that are possible while the
	 * container is still running.
	 */
	SendWhileRunning(conn *websocket.Conn, op *operation) error

	/* send the final bits (e.g. a final delta snapshot for zfs, btrfs, or
	 * do a final rsync) of the fs after the container has been
	 * checkpointed. This will only be called when a container is actually
	 * being live migrated.
	 */
	SendAfterCheckpoint(conn *websocket.Conn) error

	/* Called after either success or failure of a migration, can be used
	 * to clean up any temporary snapshots, etc.
	 */
	Cleanup()
}

type storageCoreInfo interface {
	StorageCoreInit() (*storageCore, error)
	GetStorageType() storageType
	GetStorageTypeName() string
	GetStorageTypeVersion() string
}

// FIXME(brauner): Split up this interace into sub-interfaces, that can be
// combined into this single big interface but can also be individually
// initialized. Suggestion:
// - type storagePool interface
// - type storagePoolVolume interface
// - type storageContainer interface
// - type storageImage interface
// Also, minimize the number of functions needed. Both should be straightforward
// tasks.
type storage interface {
	storageCoreInfo

	// Functions dealing with storage pool.
	StoragePoolInit(config map[string]interface{}) (storage, error)
	StoragePoolCheck() error
	StoragePoolCreate() error
	StoragePoolDelete() error
	StoragePoolMount() (bool, error)
	StoragePoolUmount() (bool, error)
	StoragePoolUpdate(changedConfig []string) error
	GetStoragePoolWritable() api.StoragePoolPut
	SetStoragePoolWritable(writable *api.StoragePoolPut)

	// Functions dealing with storage volumes.
	StoragePoolVolumeCreate() error
	StoragePoolVolumeDelete() error
	StoragePoolVolumeMount() (bool, error)
	StoragePoolVolumeUmount() (bool, error)
	StoragePoolVolumeUpdate(changedConfig []string) error
	GetStoragePoolVolumeWritable() api.StorageVolumePut
	SetStoragePoolVolumeWritable(writable *api.StorageVolumePut)

	// ContainerCreate creates an empty container (no rootfs/metadata.yaml)
	ContainerCreate(container container) error

	// ContainerCreateFromImage creates a container from a image.
	ContainerCreateFromImage(container container, imageFingerprint string) error

	ContainerCanRestore(container container, sourceContainer container) error
	ContainerDelete(container container) error
	ContainerCopy(container container, sourceContainer container) error
	ContainerMount(name string, path string) (bool, error)
	ContainerUmount(name string, path string) (bool, error)
	ContainerRename(container container, newName string) error
	ContainerRestore(container container, sourceContainer container) error
	ContainerSetQuota(container container, size int64) error
	ContainerGetUsage(container container) (int64, error)
	ContainerPoolGet() string
	ContainerPoolIDGet() int64

	ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error
	ContainerSnapshotDelete(snapshotContainer container) error
	ContainerSnapshotRename(snapshotContainer container, newName string) error
	ContainerSnapshotStart(container container) error
	ContainerSnapshotStop(container container) error

	/* for use in migrating snapshots */
	ContainerSnapshotCreateEmpty(snapshotContainer container) error

	ImageCreate(fingerprint string) error
	ImageDelete(fingerprint string) error
	ImageMount(fingerprint string) (bool, error)
	ImageUmount(fingerprint string) (bool, error)

	MigrationType() MigrationFSType
	/* does this storage backend preserve inodes when it is moved across
	 * LXD hosts?
	 */
	PreservesInodes() bool

	// Get the pieces required to migrate the source. This contains a list
	// of the "object" (i.e. container or snapshot, depending on whether or
	// not it is a snapshot name) to be migrated in order, and a channel
	// for arguments of the specific migration command. We use a channel
	// here so we don't have to invoke `zfs send` or `rsync` or whatever
	// and keep its stdin/stdout open for each snapshot during the course
	// of migration, we can do it lazily.
	//
	// N.B. that the order here important: e.g. in btrfs/zfs, snapshots
	// which are parents of other snapshots should be sent first, to save
	// as much transfer as possible. However, the base container is always
	// sent as the first object, since that is the grandparent of every
	// snapshot.
	//
	// We leave sending containers which are snapshots of other containers
	// already present on the target instance as an exercise for the
	// enterprising developer.
	MigrationSource(container container) (MigrationStorageSourceDriver, error)
	MigrationSink(live bool, container container, objects []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation) error
}

func storageWrapperInit(d *Daemon, poolName string, volumeName string, volumeType int) (*storageLogWrapper, error) {
	var s storageLogWrapper

	// Load the storage pool.
	poolID, pool, err := dbStoragePoolGet(d.db, poolName)
	if err != nil {
		return nil, err
	}

	driver := pool.Driver
	if driver == "" {
		// This shouldn't actually be possible but better safe than
		// sorry.
		return nil, fmt.Errorf("No storage driver was provided.")
	}

	// Load the storage volume.
	volume := &api.StorageVolume{}
	if volumeName != "" && volumeType >= 0 {
		_, volume, err = dbStoragePoolVolumeGetType(d.db, volumeName, volumeType, poolID)
		if err != nil {
			return nil, err
		}
	}

	sType, err := storageStringToType(driver)
	if err != nil {
		return nil, err
	}

	switch sType {
	case storageTypeBtrfs:
		btrfs := storageBtrfs{}
		btrfs.poolID = poolID
		btrfs.pool = pool
		btrfs.volume = volume
		btrfs.d = d
		s = storageLogWrapper{w: &btrfs}
	case storageTypeZfs:
		zfs := storageZfs{}
		zfs.poolID = poolID
		zfs.pool = pool
		zfs.volume = volume
		zfs.d = d
		s = storageLogWrapper{w: &zfs}
	case storageTypeLvm:
		lvm := storageLvm{}
		lvm.poolID = poolID
		lvm.pool = pool
		lvm.volume = volume
		lvm.d = d
		s = storageLogWrapper{w: &lvm}
	case storageTypeDir:
		dir := storageDir{}
		dir.poolID = poolID
		dir.pool = pool
		dir.volume = volume
		dir.d = d
		s = storageLogWrapper{w: &dir}
	case storageTypeMock:
		mock := storageMock{}
		mock.poolID = poolID
		mock.pool = pool
		mock.volume = volume
		mock.d = d
		s = storageLogWrapper{w: &mock}
	}

	return &s, nil
}

func storagePoolInit(d *Daemon, poolName string) (storage, error) {
	var config map[string]interface{}

	wrapper, err := storageWrapperInit(d, poolName, "", -1)
	if err != nil {
		return nil, err
	}

	storage, err := wrapper.StoragePoolInit(config)
	if err != nil {
		return nil, err
	}

	return storage, nil
}

func storagePoolCoreInit(poolDriver string) (*storageCore, error) {
	sType, err := storageStringToType(poolDriver)
	if err != nil {
		return nil, err
	}

	var s storage
	switch sType {
	case storageTypeBtrfs:
		btrfs := storageBtrfs{}
		s = &storageLogWrapper{w: &btrfs}
	case storageTypeZfs:
		zfs := storageZfs{}
		s = &storageLogWrapper{w: &zfs}
	case storageTypeLvm:
		lvm := storageLvm{}
		s = &storageLogWrapper{w: &lvm}
	case storageTypeDir:
		dir := storageDir{}
		s = &storageLogWrapper{w: &dir}
	case storageTypeMock:
		mock := storageMock{}
		s = &storageLogWrapper{w: &mock}
	default:
		return nil, fmt.Errorf("Unknown storage pool driver \"%s\".", poolDriver)
	}

	return s.StorageCoreInit()
}

func storagePoolVolumeImageInit(d *Daemon, poolName string, imageFingerprint string) (storage, error) {
	return storagePoolVolumeInit(d, poolName, imageFingerprint, storagePoolVolumeTypeImage)
}

func storagePoolVolumeContainerCreateInit(d *Daemon, poolName string, containerName string) (storage, error) {
	return storagePoolVolumeInit(d, poolName, containerName, storagePoolVolumeTypeContainer)
}

func storagePoolVolumeContainerLoadInit(d *Daemon, containerName string) (storage, error) {
	// Get the storage pool of a given container.
	poolName, err := dbContainerPool(d.db, containerName)
	if err != nil {
		return nil, err
	}

	return storagePoolVolumeInit(d, poolName, containerName, storagePoolVolumeTypeContainer)
}

func storagePoolVolumeInit(d *Daemon, poolName string, volumeName string, volumeType int) (storage, error) {
	var config map[string]interface{}

	// No need to detect storage here, its a new container.
	wrapper, err := storageWrapperInit(d, poolName, volumeName, volumeType)
	if err != nil {
		return nil, err
	}

	storage, err := wrapper.StoragePoolInit(config)
	if err != nil {
		return nil, err
	}

	err = storage.StoragePoolCheck()
	if err != nil {
		return nil, err
	}

	return storage, nil
}

type storageCore struct {
	sType        storageType
	sTypeName    string
	sTypeVersion string
	log          shared.Logger
}

func (sc *storageCore) initShared() error {
	sc.log = logging.AddContext(
		shared.Log,
		log.Ctx{"driver": fmt.Sprintf("storage/%s", sc.sTypeName)},
	)
	return nil
}

// Return a storageCore struct that implements a storageCore interface. This
// minimal interface only allows to retrieve basic information about the storage
// type in question.
func (lw *storageLogWrapper) StorageCoreInit() (*storageCore, error) {
	sCore, err := lw.w.StorageCoreInit()
	lw.log = logging.AddContext(
		shared.Log,
		log.Ctx{"driver": fmt.Sprintf("storage/%s", sCore.GetStorageTypeName())},
	)

	lw.log.Debug("StorageCoreInit")
	return sCore, err
}

func (sc *storageCore) GetStorageType() storageType {
	return sc.sType
}

func (sc *storageCore) GetStorageTypeName() string {
	return sc.sTypeName
}

func (sc *storageCore) GetStorageTypeVersion() string {
	return sc.sTypeVersion
}

type storageShared struct {
	storageCore

	d *Daemon

	poolID int64
	pool   *api.StoragePool

	volume *api.StorageVolume
}

// {LXD_DIR}/storage-pools/<pool>
func getStoragePoolMountPoint(poolName string) string {
	return shared.VarPath("storage-pools", poolName)
}

// ${LXD_DIR}/storage-pools/<pool>containers/<container_name>
func getContainerMountPoint(poolName string, containerName string) string {
	return shared.VarPath("storage-pools", poolName, "containers", containerName)
}

// ${LXD_DIR}/storage-pools/<pool>/snapshots/<snapshot_name>
func getSnapshotMountPoint(poolName string, snapshotName string) string {
	return shared.VarPath("storage-pools", poolName, "snapshots", snapshotName)
}

// ${LXD_DIR}/storage-pools/<pool>/images/<fingerprint>
func getImageMountPoint(poolName string, fingerprint string) string {
	return shared.VarPath("storage-pools", poolName, "images", fingerprint)
}

// ${LXD_DIR}/storage-pools/<pool>/custom/<storage_volume>
func getStoragePoolVolumeMountPoint(poolName string, volumeName string) string {
	return shared.VarPath("storage-pools", poolName, "custom", volumeName)
}

func createContainerMountpoint(mountPoint string, mountPointSymlink string, privileged bool) error {
	var mode os.FileMode
	if privileged {
		mode = 0700
	} else {
		mode = 0755
	}

	mntPointSymlinkExist := shared.PathExists(mountPointSymlink)
	mntPointSymlinkTargetExist := shared.PathExists(mountPoint)

	var err error
	if !mntPointSymlinkTargetExist {
		err = os.MkdirAll(mountPoint, 0755)
		if err != nil {
			return err
		}

		err = os.Chmod(mountPoint, mode)
	} else {
		err = os.Chmod(mountPoint, mode)
	}
	if err != nil {
		return err
	}

	if !mntPointSymlinkExist {
		err := os.Symlink(mountPoint, mountPointSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

func deleteContainerMountpoint(mountPoint string, mountPointSymlink string, storageTypeName string) error {
	mntPointSuffix := storageTypeName
	oldStyleMntPointSymlink := fmt.Sprintf("%s.%s", mountPointSymlink, mntPointSuffix)

	if shared.PathExists(mountPointSymlink) {
		err := os.Remove(mountPointSymlink)
		if err != nil {
			return err
		}
	}

	if shared.PathExists(oldStyleMntPointSymlink) {
		err := os.Remove(oldStyleMntPointSymlink)
		if err != nil {
			return err
		}
	}

	if shared.PathExists(mountPoint) {
		err := os.Remove(mountPoint)
		if err != nil {
			return err
		}
	}

	return nil
}

func renameContainerMountpoint(oldMountPoint string, oldMountPointSymlink string, newMountPoint string, newMountPointSymlink string) error {
	if shared.PathExists(oldMountPoint) {
		err := os.Rename(oldMountPoint, newMountPoint)
		if err != nil {
			return err
		}
	}

	// Rename the symlink target.
	if shared.PathExists(oldMountPointSymlink) {
		err := os.Remove(oldMountPointSymlink)
		if err != nil {
			return err
		}
	}

	// Create the new symlink.
	err := os.Symlink(newMountPoint, newMountPointSymlink)
	if err != nil {
		return err
	}

	return nil
}

func createSnapshotMountpoint(snapshotMountpoint string, snapshotsSymlinkTarget string, snapshotsSymlink string) error {
	snapshotMntPointExists := shared.PathExists(snapshotMountpoint)
	mntPointSymlinkExist := shared.PathExists(snapshotsSymlink)

	if !snapshotMntPointExists {
		err := os.MkdirAll(snapshotMountpoint, 0711)
		if err != nil {
			return err
		}
	}

	if !mntPointSymlinkExist {
		err := os.Symlink(snapshotsSymlinkTarget, snapshotsSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

func deleteSnapshotMountpoint(snapshotMountpoint string, snapshotsSymlinkTarget string, snapshotsSymlink string) error {
	if shared.PathExists(snapshotMountpoint) {
		err := os.Remove(snapshotMountpoint)
		if err != nil {
			return err
		}
	}

	couldRemove := false
	if shared.PathExists(snapshotsSymlinkTarget) {
		err := os.Remove(snapshotsSymlinkTarget)
		if err == nil {
			couldRemove = true
		}
	}

	if couldRemove && shared.PathExists(snapshotsSymlink) {
		err := os.Remove(snapshotsSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

func (ss *storageShared) shiftRootfs(c container) error {
	dpath := c.Path()
	rpath := c.RootfsPath()

	shared.LogDebug("Shifting root filesystem",
		log.Ctx{"name": c.Name(), "rootfs": rpath})

	idmapset := c.IdmapSet()

	if idmapset == nil {
		return fmt.Errorf("IdmapSet of container '%s' is nil", c.Name())
	}

	err := idmapset.ShiftRootfs(rpath)
	if err != nil {
		shared.LogDebugf("Shift of rootfs %s failed: %s", rpath, err)
		return err
	}

	/* Set an acl so the container root can descend the container dir */
	// TODO: i changed this so it calls ss.setUnprivUserAcl, which does
	// the acl change only if the container is not privileged, think thats right.
	return ss.setUnprivUserAcl(c, dpath)
}

func (ss *storageShared) setUnprivUserAcl(c container, destPath string) error {
	idmapset := c.IdmapSet()

	// Skip for privileged containers
	if idmapset == nil {
		return nil
	}

	// Make sure the map is valid. Skip if container uid 0 == host uid 0
	uid, _ := idmapset.ShiftIntoNs(0, 0)
	switch uid {
	case -1:
		return fmt.Errorf("Container doesn't have a uid 0 in its map")
	case 0:
		return nil
	}

	// Attempt to set a POSIX ACL first. Fallback to chmod if the fs doesn't support it.
	acl := fmt.Sprintf("%d:rx", uid)
	_, err := exec.Command("setfacl", "-m", acl, destPath).CombinedOutput()
	if err != nil {
		_, err := exec.Command("chmod", "+x", destPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("Failed to chmod the container path: %s.", err)
		}
	}

	return nil
}

func (ss *storageShared) createImageDbPoolVolume(fingerprint string) error {
	// Create a db entry for the storage volume of the image.
	volumeConfig := map[string]string{}
	_, err := dbStoragePoolVolumeCreate(ss.d.db, fingerprint, storagePoolVolumeTypeImage, ss.poolID, volumeConfig)
	if err != nil {
		return err
	}

	return nil
}

func (ss *storageShared) deleteImageDbPoolVolume(fingerprint string) error {
	err := dbStoragePoolVolumeDelete(ss.d.db, fingerprint, storagePoolVolumeTypeImage, ss.poolID)
	if err != nil {
		return err
	}

	return nil
}

type storageLogWrapper struct {
	w   storage
	log shared.Logger
}

func (lw *storageLogWrapper) StoragePoolInit(config map[string]interface{}) (storage, error) {
	_, err := lw.w.StoragePoolInit(config)
	lw.log = logging.AddContext(
		shared.Log,
		log.Ctx{"driver": fmt.Sprintf("storage/%s", lw.w.GetStorageTypeName())},
	)

	lw.log.Debug("StoragePoolInit")
	return lw, err
}

func (lw *storageLogWrapper) StoragePoolCheck() error {
	lw.log.Debug("StoragePoolCheck")
	return lw.w.StoragePoolCheck()
}

func (lw *storageLogWrapper) GetStorageType() storageType {
	return lw.w.GetStorageType()
}

func (lw *storageLogWrapper) GetStorageTypeName() string {
	return lw.w.GetStorageTypeName()
}

func (lw *storageLogWrapper) GetStorageTypeVersion() string {
	return lw.w.GetStorageTypeVersion()
}

func (lw *storageLogWrapper) StoragePoolCreate() error {
	lw.log.Debug("StoragePoolCreate")
	return lw.w.StoragePoolCreate()
}

func (lw *storageLogWrapper) StoragePoolVolumeCreate() error {
	lw.log.Debug("StoragePoolVolumeCreate")
	return lw.w.StoragePoolVolumeCreate()
}

func (lw *storageLogWrapper) StoragePoolVolumeDelete() error {
	lw.log.Debug("StoragePoolVolumeDelete")
	return lw.w.StoragePoolVolumeDelete()
}

func (lw *storageLogWrapper) StoragePoolMount() (bool, error) {
	lw.log.Debug("StoragePoolMount")
	return lw.w.StoragePoolMount()
}

func (lw *storageLogWrapper) StoragePoolUmount() (bool, error) {
	lw.log.Debug("StoragePoolUmount")
	return lw.w.StoragePoolUmount()
}

func (lw *storageLogWrapper) StoragePoolVolumeMount() (bool, error) {
	lw.log.Debug("StoragePoolVolumeMount")
	return lw.w.StoragePoolVolumeMount()
}

func (lw *storageLogWrapper) StoragePoolVolumeUmount() (bool, error) {
	lw.log.Debug("StoragePoolVolumeUmount")
	return lw.w.StoragePoolVolumeUmount()
}

func (lw *storageLogWrapper) StoragePoolDelete() error {
	lw.log.Debug("StoragePoolDelete")
	return lw.w.StoragePoolDelete()
}

func (lw *storageLogWrapper) StoragePoolUpdate(changedConfig []string) error {
	lw.log.Debug("StoragePoolUpdate")
	return lw.w.StoragePoolUpdate(changedConfig)
}

func (lw *storageLogWrapper) StoragePoolVolumeUpdate(changedConfig []string) error {
	lw.log.Debug("StoragePoolVolumeUpdate")
	return lw.w.StoragePoolVolumeUpdate(changedConfig)
}

func (lw *storageLogWrapper) GetStoragePoolWritable() api.StoragePoolPut {
	return lw.w.GetStoragePoolWritable()
}

func (lw *storageLogWrapper) GetStoragePoolVolumeWritable() api.StorageVolumePut {
	return lw.w.GetStoragePoolVolumeWritable()
}

func (lw *storageLogWrapper) SetStoragePoolWritable(writable *api.StoragePoolPut) {
	lw.w.SetStoragePoolWritable(writable)
}

func (lw *storageLogWrapper) SetStoragePoolVolumeWritable(writable *api.StorageVolumePut) {
	lw.w.SetStoragePoolVolumeWritable(writable)
}

func (lw *storageLogWrapper) ContainerPoolGet() string {
	return lw.w.ContainerPoolGet()
}

func (lw *storageLogWrapper) ContainerPoolIDGet() int64 {
	return lw.w.ContainerPoolIDGet()
}

func (lw *storageLogWrapper) ContainerCreate(container container) error {
	lw.log.Debug(
		"ContainerCreate",
		log.Ctx{
			"name":       container.Name(),
			"privileged": container.IsPrivileged()})
	return lw.w.ContainerCreate(container)
}

func (lw *storageLogWrapper) ContainerCreateFromImage(
	container container, imageFingerprint string) error {

	lw.log.Debug(
		"ContainerCreateFromImage",
		log.Ctx{
			"fingerprint": imageFingerprint,
			"name":        container.Name(),
			"privileged":  container.IsPrivileged()})
	return lw.w.ContainerCreateFromImage(container, imageFingerprint)
}

func (lw *storageLogWrapper) ContainerCanRestore(container container, sourceContainer container) error {
	lw.log.Debug("ContainerCanRestore", log.Ctx{"name": container.Name()})
	return lw.w.ContainerCanRestore(container, sourceContainer)
}

func (lw *storageLogWrapper) ContainerDelete(container container) error {
	lw.log.Debug("ContainerDelete", log.Ctx{"name": container.Name()})
	return lw.w.ContainerDelete(container)
}

func (lw *storageLogWrapper) ContainerCopy(
	container container, sourceContainer container) error {

	lw.log.Debug(
		"ContainerCopy",
		log.Ctx{
			"target": container.Name(),
			"source": sourceContainer.Name()})
	return lw.w.ContainerCopy(container, sourceContainer)
}

func (lw *storageLogWrapper) ContainerMount(name string, path string) (bool, error) {
	lw.log.Debug("ContainerMount", log.Ctx{"container": name})
	return lw.w.ContainerMount(name, path)
}

func (lw *storageLogWrapper) ContainerUmount(name string, path string) (bool, error) {
	lw.log.Debug("ContainerUmount", log.Ctx{"name": name})
	return lw.w.ContainerUmount(name, path)
}

func (lw *storageLogWrapper) ContainerRename(
	container container, newName string) error {

	lw.log.Debug(
		"ContainerRename",
		log.Ctx{
			"oldname": container.Name(),
			"newname": newName})
	return lw.w.ContainerRename(container, newName)
}

func (lw *storageLogWrapper) ContainerRestore(
	container container, sourceContainer container) error {

	lw.log.Debug(
		"ContainerRestore",
		log.Ctx{
			"target": container.Name(),
			"source": sourceContainer.Name()})
	return lw.w.ContainerRestore(container, sourceContainer)
}

func (lw *storageLogWrapper) ContainerSetQuota(
	container container, size int64) error {

	lw.log.Debug(
		"ContainerSetQuota",
		log.Ctx{
			"name": container.Name(),
			"size": size})
	return lw.w.ContainerSetQuota(container, size)
}

func (lw *storageLogWrapper) ContainerGetUsage(
	container container) (int64, error) {

	lw.log.Debug(
		"ContainerGetUsage",
		log.Ctx{
			"name": container.Name()})
	return lw.w.ContainerGetUsage(container)
}

func (lw *storageLogWrapper) ContainerSnapshotCreate(
	snapshotContainer container, sourceContainer container) error {

	lw.log.Debug("ContainerSnapshotCreate",
		log.Ctx{
			"target": snapshotContainer.Name(),
			"source": sourceContainer.Name()})

	return lw.w.ContainerSnapshotCreate(snapshotContainer, sourceContainer)
}

func (lw *storageLogWrapper) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	lw.log.Debug("ContainerSnapshotCreateEmpty",
		log.Ctx{
			"name": snapshotContainer.Name()})

	return lw.w.ContainerSnapshotCreateEmpty(snapshotContainer)
}

func (lw *storageLogWrapper) ContainerSnapshotDelete(
	snapshotContainer container) error {

	lw.log.Debug("ContainerSnapshotDelete",
		log.Ctx{"name": snapshotContainer.Name()})
	return lw.w.ContainerSnapshotDelete(snapshotContainer)
}

func (lw *storageLogWrapper) ContainerSnapshotRename(
	snapshotContainer container, newName string) error {

	lw.log.Debug("ContainerSnapshotRename",
		log.Ctx{
			"oldname": snapshotContainer.Name(),
			"newname": newName})
	return lw.w.ContainerSnapshotRename(snapshotContainer, newName)
}

func (lw *storageLogWrapper) ContainerSnapshotStart(container container) error {
	lw.log.Debug("ContainerSnapshotStart", log.Ctx{"name": container.Name()})
	return lw.w.ContainerSnapshotStart(container)
}

func (lw *storageLogWrapper) ContainerSnapshotStop(container container) error {
	lw.log.Debug("ContainerSnapshotStop", log.Ctx{"name": container.Name()})
	return lw.w.ContainerSnapshotStop(container)
}

func (lw *storageLogWrapper) ImageCreate(fingerprint string) error {
	lw.log.Debug("ImageCreate", log.Ctx{"fingerprint": fingerprint})
	return lw.w.ImageCreate(fingerprint)
}

func (lw *storageLogWrapper) ImageDelete(fingerprint string) error {
	lw.log.Debug("ImageDelete", log.Ctx{"fingerprint": fingerprint})
	return lw.w.ImageDelete(fingerprint)
}

func (lw *storageLogWrapper) ImageMount(fingerprint string) (bool, error) {
	lw.log.Debug("ImageMount", log.Ctx{"fingerprint": fingerprint})
	return lw.w.ImageMount(fingerprint)
}

func (lw *storageLogWrapper) ImageUmount(fingerprint string) (bool, error) {
	lw.log.Debug("ImageUmount", log.Ctx{"fingerprint": fingerprint})
	return lw.w.ImageUmount(fingerprint)
}

func (lw *storageLogWrapper) MigrationType() MigrationFSType {
	return lw.w.MigrationType()
}

func (lw *storageLogWrapper) PreservesInodes() bool {
	return lw.w.PreservesInodes()
}

func (lw *storageLogWrapper) MigrationSource(container container) (MigrationStorageSourceDriver, error) {
	lw.log.Debug("MigrationSource", log.Ctx{"name": container.Name()})
	return lw.w.MigrationSource(container)
}

func (lw *storageLogWrapper) MigrationSink(live bool, container container, objects []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation) error {
	objNames := []string{}
	for _, obj := range objects {
		objNames = append(objNames, obj.GetName())
	}

	lw.log.Debug("MigrationSink", log.Ctx{
		"live":         live,
		"name":         container.Name(),
		"objects":      objNames,
		"source idmap": *srcIdmap,
		"op":           op,
	})

	return lw.w.MigrationSink(live, container, objects, conn, srcIdmap, op)
}

func ShiftIfNecessary(container container, srcIdmap *shared.IdmapSet) error {
	dstIdmap := container.IdmapSet()
	if dstIdmap == nil {
		dstIdmap = new(shared.IdmapSet)
	}

	if !reflect.DeepEqual(srcIdmap, dstIdmap) {
		var jsonIdmap string
		if srcIdmap != nil {
			idmapBytes, err := json.Marshal(srcIdmap.Idmap)
			if err != nil {
				return err
			}
			jsonIdmap = string(idmapBytes)
		} else {
			jsonIdmap = "[]"
		}

		err := container.ConfigKeySet("volatile.last_state.idmap", jsonIdmap)
		if err != nil {
			return err
		}
	}

	return nil
}

type rsyncStorageSourceDriver struct {
	container container
	snapshots []container
}

func (s rsyncStorageSourceDriver) Snapshots() []container {
	return s.snapshots
}

func (s rsyncStorageSourceDriver) SendWhileRunning(conn *websocket.Conn, op *operation) error {
	for _, send := range s.snapshots {
		if err := send.StorageStart(); err != nil {
			return err
		}
		defer send.StorageStop()

		path := send.Path()
		wrapper := StorageProgressReader(op, "fs_progress", send.Name())
		if err := RsyncSend(shared.AddSlash(path), conn, wrapper); err != nil {
			return err
		}
	}

	wrapper := StorageProgressReader(op, "fs_progress", s.container.Name())
	return RsyncSend(shared.AddSlash(s.container.Path()), conn, wrapper)
}

func (s rsyncStorageSourceDriver) SendAfterCheckpoint(conn *websocket.Conn) error {
	/* resync anything that changed between our first send and the checkpoint */
	return RsyncSend(shared.AddSlash(s.container.Path()), conn, nil)
}

func (s rsyncStorageSourceDriver) Cleanup() {
	/* no-op */
}

func rsyncMigrationSource(container container) (MigrationStorageSourceDriver, error) {
	snapshots, err := container.Snapshots()
	if err != nil {
		return nil, err
	}

	return rsyncStorageSourceDriver{container, snapshots}, nil
}

func snapshotProtobufToContainerArgs(containerName string, snap *Snapshot) containerArgs {
	config := map[string]string{}

	for _, ent := range snap.LocalConfig {
		config[ent.GetKey()] = ent.GetValue()
	}

	devices := types.Devices{}
	for _, ent := range snap.LocalDevices {
		props := map[string]string{}
		for _, prop := range ent.Config {
			props[prop.GetKey()] = prop.GetValue()
		}

		devices[ent.GetName()] = props
	}

	name := containerName + shared.SnapshotDelimiter + snap.GetName()
	return containerArgs{
		Name:         name,
		Ctype:        cTypeSnapshot,
		Config:       config,
		Profiles:     snap.Profiles,
		Ephemeral:    snap.GetEphemeral(),
		Devices:      devices,
		Architecture: int(snap.GetArchitecture()),
		Stateful:     snap.GetStateful(),
	}
}

func rsyncMigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation) error {
	if err := container.StorageStart(); err != nil {
		return err
	}
	defer container.StorageStop()

	isDirBackend := container.Storage().GetStorageType() == storageTypeDir
	if isDirBackend {
		for _, snap := range snapshots {
			args := snapshotProtobufToContainerArgs(container.Name(), snap)
			// Unset the pool of the orginal container and let
			// containerLXCCreate figure out on which pool to  send
			// it. Later we might make this more flexible.
			for k, v := range args.Devices {
				if v["type"] == "disk" && v["path"] == "/" {
					args.Devices[k]["pool"] = ""
				}
			}
			s, err := containerCreateEmptySnapshot(container.Daemon(), args)
			if err != nil {
				return err
			}

			wrapper := StorageProgressWriter(op, "fs_progress", s.Name())
			if err := RsyncRecv(shared.AddSlash(s.Path()), conn, wrapper); err != nil {
				return err
			}

			if err := ShiftIfNecessary(container, srcIdmap); err != nil {
				return err
			}
		}

		wrapper := StorageProgressWriter(op, "fs_progress", container.Name())
		if err := RsyncRecv(shared.AddSlash(container.Path()), conn, wrapper); err != nil {
			return err
		}
	} else {
		for _, snap := range snapshots {
			args := snapshotProtobufToContainerArgs(container.Name(), snap)
			// Unset the pool of the orginal container and let
			// containerLXCCreate figure out on which pool to  send
			// it. Later we might make this more flexible.
			for k, v := range args.Devices {
				if v["type"] == "disk" && v["path"] == "/" {
					args.Devices[k]["pool"] = ""
				}
			}
			wrapper := StorageProgressWriter(op, "fs_progress", snap.GetName())
			if err := RsyncRecv(shared.AddSlash(container.Path()), conn, wrapper); err != nil {
				return err
			}

			if err := ShiftIfNecessary(container, srcIdmap); err != nil {
				return err
			}

			_, err := containerCreateAsSnapshot(container.Daemon(), args, container)
			if err != nil {
				return err
			}
		}

		wrapper := StorageProgressWriter(op, "fs_progress", container.Name())
		if err := RsyncRecv(shared.AddSlash(container.Path()), conn, wrapper); err != nil {
			return err
		}
	}

	if live {
		/* now receive the final sync */
		wrapper := StorageProgressWriter(op, "fs_progress", container.Name())
		if err := RsyncRecv(shared.AddSlash(container.Path()), conn, wrapper); err != nil {
			return err
		}
	}

	if err := ShiftIfNecessary(container, srcIdmap); err != nil {
		return err
	}

	return nil
}

// Useful functions for unreliable backends
func tryExec(name string, arg ...string) ([]byte, error) {
	var err error
	var output []byte

	for i := 0; i < 20; i++ {
		output, err = exec.Command(name, arg...).CombinedOutput()
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	return output, err
}

func tryMount(src string, dst string, fs string, flags uintptr, options string) error {
	var err error

	for i := 0; i < 20; i++ {
		err = syscall.Mount(src, dst, fs, flags, options)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return err
	}

	return nil
}

func tryUnmount(path string, flags int) error {
	var err error

	for i := 0; i < 20; i++ {
		err = syscall.Unmount(path, flags)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil && err == syscall.EBUSY {
		return err
	}

	return nil
}

func progressWrapperRender(op *operation, key string, description string, progressInt int64, speedInt int64) {
	meta := op.metadata
	if meta == nil {
		meta = make(map[string]interface{})
	}

	progress := fmt.Sprintf("%s (%s/s)", shared.GetByteSizeString(progressInt, 2), shared.GetByteSizeString(speedInt, 2))
	if description != "" {
		progress = fmt.Sprintf("%s: %s (%s/s)", description, shared.GetByteSizeString(progressInt, 2), shared.GetByteSizeString(speedInt, 2))
	}

	if meta[key] != progress {
		meta[key] = progress
		op.UpdateMetadata(meta)
	}
}

func StorageProgressReader(op *operation, key string, description string) func(io.ReadCloser) io.ReadCloser {
	return func(reader io.ReadCloser) io.ReadCloser {
		if op == nil {
			return reader
		}

		progress := func(progressInt int64, speedInt int64) {
			progressWrapperRender(op, key, description, progressInt, speedInt)
		}

		readPipe := &ioprogress.ProgressReader{
			ReadCloser: reader,
			Tracker: &ioprogress.ProgressTracker{
				Handler: progress,
			},
		}

		return readPipe
	}
}

func StorageProgressWriter(op *operation, key string, description string) func(io.WriteCloser) io.WriteCloser {
	return func(writer io.WriteCloser) io.WriteCloser {
		if op == nil {
			return writer
		}

		progress := func(progressInt int64, speedInt int64) {
			progressWrapperRender(op, key, description, progressInt, speedInt)
		}

		writePipe := &ioprogress.ProgressWriter{
			WriteCloser: writer,
			Tracker: &ioprogress.ProgressTracker{
				Handler: progress,
			},
		}

		return writePipe
	}
}
