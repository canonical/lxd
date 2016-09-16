package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logging"

	log "gopkg.in/inconshreveable/log15.v2"
)

/* Some interesting filesystems */
const (
	filesystemSuperMagicTmpfs = 0x01021994
	filesystemSuperMagicExt4  = 0xEF53
	filesystemSuperMagicXfs   = 0x58465342
	filesystemSuperMagicNfs   = 0x6969
	filesystemSuperMagicZfs   = 0x2fc12fc1
)

/*
 * filesystemDetect returns the filesystem on which
 * the passed-in path sits
 */
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

func storageTypeToString(sType storageType) string {
	switch sType {
	case storageTypeBtrfs:
		return "btrfs"
	case storageTypeZfs:
		return "zfs"
	case storageTypeLvm:
		return "lvm"
	case storageTypeMock:
		return "mock"
	}

	return "dir"
}

type MigrationStorageSourceDriver interface {
	/* snapshots for this container, if any */
	Snapshots() []container

	/* send any bits of the container/snapshots that are possible while the
	 * container is still running.
	 */
	SendWhileRunning(conn *websocket.Conn) error

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

type storage interface {
	Init(config map[string]interface{}) (storage, error)

	GetStorageType() storageType
	GetStorageTypeName() string
	GetStorageTypeVersion() string

	// ContainerCreate creates an empty container (no rootfs/metadata.yaml)
	ContainerCreate(container container) error

	// ContainerCreateFromImage creates a container from a image.
	ContainerCreateFromImage(container container, imageFingerprint string) error

	ContainerCanRestore(container container, sourceContainer container) error
	ContainerDelete(container container) error
	ContainerCopy(container container, sourceContainer container) error
	ContainerStart(container container) error
	ContainerStop(container container) error
	ContainerRename(container container, newName string) error
	ContainerRestore(container container, sourceContainer container) error
	ContainerSetQuota(container container, size int64) error
	ContainerGetUsage(container container) (int64, error)

	ContainerSnapshotCreate(
		snapshotContainer container, sourceContainer container) error
	ContainerSnapshotDelete(snapshotContainer container) error
	ContainerSnapshotRename(snapshotContainer container, newName string) error
	ContainerSnapshotStart(container container) error
	ContainerSnapshotStop(container container) error

	/* for use in migrating snapshots */
	ContainerSnapshotCreateEmpty(snapshotContainer container) error

	ImageCreate(fingerprint string) error
	ImageDelete(fingerprint string) error

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
	MigrationSink(live bool, container container, objects []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet) error
}

func newStorage(d *Daemon, sType storageType) (storage, error) {
	var nilmap map[string]interface{}
	return newStorageWithConfig(d, sType, nilmap)
}

func newStorageWithConfig(d *Daemon, sType storageType, config map[string]interface{}) (storage, error) {
	if d.MockMode {
		return d.Storage, nil
	}

	var s storage

	switch sType {
	case storageTypeBtrfs:
		if d.Storage != nil && d.Storage.GetStorageType() == storageTypeBtrfs {
			return d.Storage, nil
		}

		s = &storageLogWrapper{w: &storageBtrfs{d: d}}
	case storageTypeZfs:
		if d.Storage != nil && d.Storage.GetStorageType() == storageTypeZfs {
			return d.Storage, nil
		}

		s = &storageLogWrapper{w: &storageZfs{d: d}}
	case storageTypeLvm:
		if d.Storage != nil && d.Storage.GetStorageType() == storageTypeLvm {
			return d.Storage, nil
		}

		s = &storageLogWrapper{w: &storageLvm{d: d}}
	default:
		if d.Storage != nil && d.Storage.GetStorageType() == storageTypeDir {
			return d.Storage, nil
		}

		s = &storageLogWrapper{w: &storageDir{d: d}}
	}

	return s.Init(config)
}

func storageForFilename(d *Daemon, filename string) (storage, error) {
	var filesystem string
	var err error

	config := make(map[string]interface{})
	storageType := storageTypeDir

	if d.MockMode {
		return newStorageWithConfig(d, storageTypeMock, config)
	}

	if shared.PathExists(filename) {
		filesystem, err = filesystemDetect(filename)
		if err != nil {
			return nil, fmt.Errorf("couldn't detect filesystem for '%s': %v", filename, err)
		}
	}

	if shared.PathExists(filename + ".lv") {
		storageType = storageTypeLvm
		lvPath, err := os.Readlink(filename + ".lv")
		if err != nil {
			return nil, fmt.Errorf("couldn't read link dest for '%s': %v", filename+".lv", err)
		}
		vgname := filepath.Base(filepath.Dir(lvPath))
		config["vgName"] = vgname
	} else if shared.PathExists(filename + ".zfs") {
		storageType = storageTypeZfs
	} else if shared.PathExists(filename+".btrfs") || filesystem == "btrfs" {
		storageType = storageTypeBtrfs
	}

	return newStorageWithConfig(d, storageType, config)
}

func storageForImage(d *Daemon, imgInfo *shared.ImageInfo) (storage, error) {
	imageFilename := shared.VarPath("images", imgInfo.Fingerprint)
	return storageForFilename(d, imageFilename)
}

type storageShared struct {
	sType        storageType
	sTypeName    string
	sTypeVersion string

	log shared.Logger
}

func (ss *storageShared) initShared() error {
	ss.log = logging.AddContext(
		shared.Log,
		log.Ctx{"driver": fmt.Sprintf("storage/%s", ss.sTypeName)},
	)
	return nil
}

func (ss *storageShared) GetStorageType() storageType {
	return ss.sType
}

func (ss *storageShared) GetStorageTypeName() string {
	return ss.sTypeName
}

func (ss *storageShared) GetStorageTypeVersion() string {
	return ss.sTypeVersion
}

func (ss *storageShared) shiftRootfs(c container) error {
	dpath := c.Path()
	rpath := c.RootfsPath()

	shared.LogDebug("Shifting root filesystem",
		log.Ctx{"container": c.Name(), "rootfs": rpath})

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
			return fmt.Errorf("Failed to chmod the container path.")
		}
	}

	return nil
}

type storageLogWrapper struct {
	w   storage
	log shared.Logger
}

func (lw *storageLogWrapper) Init(config map[string]interface{}) (storage, error) {
	_, err := lw.w.Init(config)
	lw.log = logging.AddContext(
		shared.Log,
		log.Ctx{"driver": fmt.Sprintf("storage/%s", lw.w.GetStorageTypeName())},
	)

	lw.log.Debug("Init")
	return lw, err
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

func (lw *storageLogWrapper) ContainerCreate(container container) error {
	lw.log.Debug(
		"ContainerCreate",
		log.Ctx{
			"name":         container.Name(),
			"isPrivileged": container.IsPrivileged()})
	return lw.w.ContainerCreate(container)
}

func (lw *storageLogWrapper) ContainerCreateFromImage(
	container container, imageFingerprint string) error {

	lw.log.Debug(
		"ContainerCreateFromImage",
		log.Ctx{
			"imageFingerprint": imageFingerprint,
			"name":             container.Name(),
			"isPrivileged":     container.IsPrivileged()})
	return lw.w.ContainerCreateFromImage(container, imageFingerprint)
}

func (lw *storageLogWrapper) ContainerCanRestore(container container, sourceContainer container) error {
	lw.log.Debug("ContainerCanRestore", log.Ctx{"container": container.Name()})
	return lw.w.ContainerCanRestore(container, sourceContainer)
}

func (lw *storageLogWrapper) ContainerDelete(container container) error {
	lw.log.Debug("ContainerDelete", log.Ctx{"container": container.Name()})
	return lw.w.ContainerDelete(container)
}

func (lw *storageLogWrapper) ContainerCopy(
	container container, sourceContainer container) error {

	lw.log.Debug(
		"ContainerCopy",
		log.Ctx{
			"container": container.Name(),
			"source":    sourceContainer.Name()})
	return lw.w.ContainerCopy(container, sourceContainer)
}

func (lw *storageLogWrapper) ContainerStart(container container) error {
	lw.log.Debug("ContainerStart", log.Ctx{"container": container.Name()})
	return lw.w.ContainerStart(container)
}

func (lw *storageLogWrapper) ContainerStop(container container) error {
	lw.log.Debug("ContainerStop", log.Ctx{"container": container.Name()})
	return lw.w.ContainerStop(container)
}

func (lw *storageLogWrapper) ContainerRename(
	container container, newName string) error {

	lw.log.Debug(
		"ContainerRename",
		log.Ctx{
			"container": container.Name(),
			"newName":   newName})
	return lw.w.ContainerRename(container, newName)
}

func (lw *storageLogWrapper) ContainerRestore(
	container container, sourceContainer container) error {

	lw.log.Debug(
		"ContainerRestore",
		log.Ctx{
			"container": container.Name(),
			"source":    sourceContainer.Name()})
	return lw.w.ContainerRestore(container, sourceContainer)
}

func (lw *storageLogWrapper) ContainerSetQuota(
	container container, size int64) error {

	lw.log.Debug(
		"ContainerSetQuota",
		log.Ctx{
			"container": container.Name(),
			"size":      size})
	return lw.w.ContainerSetQuota(container, size)
}

func (lw *storageLogWrapper) ContainerGetUsage(
	container container) (int64, error) {

	lw.log.Debug(
		"ContainerGetUsage",
		log.Ctx{
			"container": container.Name()})
	return lw.w.ContainerGetUsage(container)
}

func (lw *storageLogWrapper) ContainerSnapshotCreate(
	snapshotContainer container, sourceContainer container) error {

	lw.log.Debug("ContainerSnapshotCreate",
		log.Ctx{
			"snapshotContainer": snapshotContainer.Name(),
			"sourceContainer":   sourceContainer.Name()})

	return lw.w.ContainerSnapshotCreate(snapshotContainer, sourceContainer)
}

func (lw *storageLogWrapper) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	lw.log.Debug("ContainerSnapshotCreateEmpty",
		log.Ctx{
			"snapshotContainer": snapshotContainer.Name()})

	return lw.w.ContainerSnapshotCreateEmpty(snapshotContainer)
}

func (lw *storageLogWrapper) ContainerSnapshotDelete(
	snapshotContainer container) error {

	lw.log.Debug("ContainerSnapshotDelete",
		log.Ctx{"snapshotContainer": snapshotContainer.Name()})
	return lw.w.ContainerSnapshotDelete(snapshotContainer)
}

func (lw *storageLogWrapper) ContainerSnapshotRename(
	snapshotContainer container, newName string) error {

	lw.log.Debug("ContainerSnapshotRename",
		log.Ctx{
			"snapshotContainer": snapshotContainer.Name(),
			"newName":           newName})
	return lw.w.ContainerSnapshotRename(snapshotContainer, newName)
}

func (lw *storageLogWrapper) ContainerSnapshotStart(container container) error {
	lw.log.Debug("ContainerSnapshotStart", log.Ctx{"container": container.Name()})
	return lw.w.ContainerSnapshotStart(container)
}

func (lw *storageLogWrapper) ContainerSnapshotStop(container container) error {
	lw.log.Debug("ContainerSnapshotStop", log.Ctx{"container": container.Name()})
	return lw.w.ContainerSnapshotStop(container)
}

func (lw *storageLogWrapper) ImageCreate(fingerprint string) error {
	lw.log.Debug(
		"ImageCreate",
		log.Ctx{"fingerprint": fingerprint})
	return lw.w.ImageCreate(fingerprint)
}

func (lw *storageLogWrapper) ImageDelete(fingerprint string) error {
	lw.log.Debug("ImageDelete", log.Ctx{"fingerprint": fingerprint})
	return lw.w.ImageDelete(fingerprint)

}

func (lw *storageLogWrapper) MigrationType() MigrationFSType {
	return lw.w.MigrationType()
}

func (lw *storageLogWrapper) PreservesInodes() bool {
	return lw.w.PreservesInodes()
}

func (lw *storageLogWrapper) MigrationSource(container container) (MigrationStorageSourceDriver, error) {
	lw.log.Debug("MigrationSource", log.Ctx{"container": container.Name()})
	return lw.w.MigrationSource(container)
}

func (lw *storageLogWrapper) MigrationSink(live bool, container container, objects []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet) error {
	objNames := []string{}
	for _, obj := range objects {
		objNames = append(objNames, obj.GetName())
	}

	lw.log.Debug("MigrationSink", log.Ctx{
		"live":      live,
		"container": container.Name(),
		"objects":   objNames,
		"srcIdmap":  *srcIdmap,
	})

	return lw.w.MigrationSink(live, container, objects, conn, srcIdmap)
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

func (s rsyncStorageSourceDriver) SendWhileRunning(conn *websocket.Conn) error {
	for _, send := range s.snapshots {
		if err := send.StorageStart(); err != nil {
			return err
		}
		defer send.StorageStop()

		path := send.Path()
		if err := RsyncSend(shared.AddSlash(path), conn); err != nil {
			return err
		}
	}

	return RsyncSend(shared.AddSlash(s.container.Path()), conn)
}

func (s rsyncStorageSourceDriver) SendAfterCheckpoint(conn *websocket.Conn) error {
	/* resync anything that changed between our first send and the checkpoint */
	return RsyncSend(shared.AddSlash(s.container.Path()), conn)
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

	devices := shared.Devices{}
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

func rsyncMigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet) error {
	isDirBackend := container.Storage().GetStorageType() == storageTypeDir

	if isDirBackend {
		if len(snapshots) > 0 {
			err := os.MkdirAll(shared.VarPath(fmt.Sprintf("snapshots/%s", container.Name())), 0700)
			if err != nil {
				return err
			}
		}
		for _, snap := range snapshots {
			args := snapshotProtobufToContainerArgs(container.Name(), snap)
			s, err := containerCreateEmptySnapshot(container.Daemon(), args)
			if err != nil {
				return err
			}

			if err := RsyncRecv(shared.AddSlash(s.Path()), conn); err != nil {
				return err
			}

			if err := ShiftIfNecessary(container, srcIdmap); err != nil {
				return err
			}
		}

		if err := RsyncRecv(shared.AddSlash(container.Path()), conn); err != nil {
			return err
		}
	} else {
		for _, snap := range snapshots {
			if err := RsyncRecv(shared.AddSlash(container.Path()), conn); err != nil {
				return err
			}

			if err := ShiftIfNecessary(container, srcIdmap); err != nil {
				return err
			}

			args := snapshotProtobufToContainerArgs(container.Name(), snap)
			_, err := containerCreateAsSnapshot(container.Daemon(), args, container)
			if err != nil {
				return err
			}
		}

		if err := RsyncRecv(shared.AddSlash(container.Path()), conn); err != nil {
			return err
		}
	}

	if live {
		/* now receive the final sync */
		if err := RsyncRecv(shared.AddSlash(container.Path()), conn); err != nil {
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

	if err != nil {
		return err
	}

	return nil
}
