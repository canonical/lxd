package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"

	log "gopkg.in/inconshreveable/log15.v2"
)

// storageRsyncCopy copies a directory using rsync (with the --devices option).
func storageRsyncCopy(source string, dest string) (string, error) {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return "", err
	}

	rsyncVerbosity := "-q"
	if debug {
		rsyncVerbosity = "-vi"
	}

	output, err := shared.RunCommand(
		"rsync",
		"-a",
		"-HAX",
		"--sparse",
		"--devices",
		"--delete",
		"--checksum",
		"--numeric-ids",
		rsyncVerbosity,
		shared.AddSlash(source),
		dest)
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 24 {
					return string(output), nil
				}
			}
		}
		return string(output), err
	}

	return string(output), nil
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
	ContainerStart(name string, path string) error
	ContainerStop(name string, path string) error
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
	MigrationSink(live bool, container container, objects []*Snapshot, conn *websocket.Conn, srcIdmap *idmap.IdmapSet) error
}

func newStorage(d *Daemon, sType storageType) (storage, error) {
	var nilmap map[string]interface{}
	return newStorageWithConfig(d.State(), d.Storage, sType, nilmap)
}

func newStorageWithConfig(s *state.State, st storage, sType storageType, config map[string]interface{}) (storage, error) {
	if s.OS.MockMode {
		return st, nil
	}

	shared := storageShared{s: s}
	var w storage

	switch sType {
	case storageTypeBtrfs:
		if st != nil && st.GetStorageType() == storageTypeBtrfs {
			return st, nil
		}
		btrfs := &storageBtrfs{storageShared: shared}
		w = &storageLogWrapper{w: btrfs}
		btrfs.storage = w
	case storageTypeZfs:
		if st != nil && st.GetStorageType() == storageTypeZfs {
			return st, nil
		}
		zfs := &storageZfs{storageShared: shared}
		w = &storageLogWrapper{w: zfs}
		zfs.storage = w
	case storageTypeLvm:
		if st != nil && st.GetStorageType() == storageTypeLvm {
			return st, nil
		}
		lvm := &storageLvm{storageShared: shared}
		w = &storageLogWrapper{w: lvm}
		lvm.storage = w
	default:
		if st != nil && st.GetStorageType() == storageTypeDir {
			return st, nil
		}
		dir := &storageDir{storageShared: shared}
		w = &storageLogWrapper{w: dir}
		dir.storage = w
	}

	return w.Init(config)
}

func storageForFilename(s *state.State, storage storage, filename string) (storage, error) {
	var filesystem string
	var err error

	config := make(map[string]interface{})
	storageType := storageTypeDir

	if s.OS.MockMode {
		return newStorageWithConfig(s, storage, storageTypeMock, config)
	}

	if shared.PathExists(filename) {
		filesystem, err = util.FilesystemDetect(filename)
		if err != nil {
			return nil, fmt.Errorf("couldn't detect filesystem for '%s': %v", filename, err)
		}

		if filesystem == "btrfs" {
			if !(*storageBtrfs).isSubvolume(nil, filename) {
				filesystem = ""
			}
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

	return newStorageWithConfig(s, storage, storageType, config)
}

func storageForImage(s *state.State, storage storage, imgInfo *api.Image) (storage, error) {
	imageFilename := shared.VarPath("images", imgInfo.Fingerprint)
	return storageForFilename(s, storage, imageFilename)
}

type storageShared struct {
	sType        storageType
	sTypeName    string
	sTypeVersion string

	s *state.State

	storage storage

	log logger.Logger
}

func (ss *storageShared) initShared() error {
	ss.log = logging.AddContext(
		logger.Log,
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

	logger.Debug("Shifting root filesystem",
		log.Ctx{"container": c.Name(), "rootfs": rpath})

	idmapset, err := c.IdmapSet()
	if err != nil {
		return err
	}

	if idmapset == nil {
		return fmt.Errorf("IdmapSet of container '%s' is nil", c.Name())
	}

	err = idmapset.ShiftRootfs(rpath)
	if err != nil {
		logger.Debugf("Shift of rootfs %s failed: %s", rpath, err)
		return err
	}

	/* Set an acl so the container root can descend the container dir */
	// TODO: i changed this so it calls ss.setUnprivUserAcl, which does
	// the acl change only if the container is not privileged, think thats right.
	return ss.setUnprivUserAcl(c, dpath)
}

func (ss *storageShared) setUnprivUserAcl(c container, destPath string) error {
	idmapset, err := c.IdmapSet()
	if err != nil {
		return err
	}

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
	_, err = shared.RunCommand("setfacl", "-m", acl, destPath)
	if err != nil {
		_, err := shared.RunCommand("chmod", "+x", destPath)
		if err != nil {
			return fmt.Errorf("Failed to chmod the container path.")
		}
	}

	return nil
}

type storageLogWrapper struct {
	w   storage
	log logger.Logger
}

func (lw *storageLogWrapper) Init(config map[string]interface{}) (storage, error) {
	_, err := lw.w.Init(config)
	lw.log = logging.AddContext(
		logger.Log,
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

func (lw *storageLogWrapper) ContainerStart(name string, path string) error {
	lw.log.Debug("ContainerStart", log.Ctx{"container": name})
	return lw.w.ContainerStart(name, path)
}

func (lw *storageLogWrapper) ContainerStop(name string, path string) error {
	lw.log.Debug("ContainerStop", log.Ctx{"container": name})
	return lw.w.ContainerStop(name, path)
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

func (lw *storageLogWrapper) MigrationSink(live bool, container container, objects []*Snapshot, conn *websocket.Conn, srcIdmap *idmap.IdmapSet) error {
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

func ShiftIfNecessary(container container, srcIdmap *idmap.IdmapSet) error {
	dstIdmap, err := container.IdmapSet()
	if err != nil {
		return err
	}

	if dstIdmap == nil {
		dstIdmap = new(idmap.IdmapSet)
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
	ctName, _, _ := containerGetParentAndSnapshotName(s.container.Name())
	for _, send := range s.snapshots {
		if err := send.StorageStart(); err != nil {
			return err
		}
		defer send.StorageStop()

		path := send.Path()
		if err := RsyncSend(ctName, shared.AddSlash(path), conn); err != nil {
			return err
		}
	}

	return RsyncSend(ctName, shared.AddSlash(s.container.Path()), conn)
}

func (s rsyncStorageSourceDriver) SendAfterCheckpoint(conn *websocket.Conn) error {
	ctName, _, _ := containerGetParentAndSnapshotName(s.container.Name())

	/* resync anything that changed between our first send and the checkpoint */
	return RsyncSend(ctName, shared.AddSlash(s.container.Path()), conn)
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

func snapshotProtobufToContainerArgs(containerName string, snap *Snapshot) db.ContainerArgs {
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
	return db.ContainerArgs{
		Name:         name,
		Ctype:        db.CTypeSnapshot,
		Config:       config,
		Profiles:     snap.Profiles,
		Ephemeral:    snap.GetEphemeral(),
		Devices:      devices,
		Architecture: int(snap.GetArchitecture()),
		Stateful:     snap.GetStateful(),
	}
}

func rsyncMigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *idmap.IdmapSet) error {
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
			s, err := containerCreateEmptySnapshot(container.StateObject(), container.Storage(), args)
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
		if err := container.StorageStart(); err != nil {
			return err
		}
		defer container.StorageStop()

		for _, snap := range snapshots {
			if err := RsyncRecv(shared.AddSlash(container.Path()), conn); err != nil {
				return err
			}

			if err := ShiftIfNecessary(container, srcIdmap); err != nil {
				return err
			}

			args := snapshotProtobufToContainerArgs(container.Name(), snap)
			_, err := containerCreateAsSnapshot(container.StateObject(), container.Storage(), args, container)
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

func SetupStorageDriver(d *Daemon) error {
	var err error

	lvmVgName := daemonConfig["storage.lvm_vg_name"].Get()
	zfsPoolName := daemonConfig["storage.zfs_pool_name"].Get()

	if lvmVgName != "" {
		d.Storage, err = newStorage(d, storageTypeLvm)
		if err != nil {
			logger.Errorf("Could not initialize storage type LVM: %s - falling back to dir", err)
		} else {
			return nil
		}
	} else if zfsPoolName != "" {
		d.Storage, err = newStorage(d, storageTypeZfs)
		if err != nil {
			logger.Errorf("Could not initialize storage type ZFS: %s - falling back to dir", err)
		} else {
			return nil
		}
	} else if d.os.BackingFS == "btrfs" {
		d.Storage, err = newStorage(d, storageTypeBtrfs)
		if err != nil {
			logger.Errorf("Could not initialize storage type btrfs: %s - falling back to dir", err)
		} else {
			return nil
		}
	}

	d.Storage, err = newStorage(d, storageTypeDir)

	return err
}
