package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

/* Some interesting filesystems */
const (
	filesystemSuperMagicTmpfs = 0x01021994
	filesystemSuperMagicExt4  = 0xEF53
	filesystemSuperMagicXfs   = 0x58465342
	filesystemSuperMagicNfs   = 0x6969
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
	case filesystemSuperMagicTmpfs:
		return "tmpfs", nil
	case filesystemSuperMagicExt4:
		return "ext4", nil
	case filesystemSuperMagicXfs:
		return "xfs", nil
	case filesystemSuperMagicNfs:
		return "nfs", nil
	default:
		return string(fs.Type), nil
	}
}

// storageRsyncCopy copies a directory using rsync (with the --devices option).
func storageRsyncCopy(source string, dest string) (string, error) {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return "", err
	}

	rsyncVerbosity := "-q"
	if *debug {
		rsyncVerbosity = "-vi"
	}

	output, err := exec.Command(
		"rsync",
		"-a",
		"-HAX",
		"--devices",
		"--delete",
		"--checksum",
		rsyncVerbosity,
		shared.AddSlash(source),
		dest).CombinedOutput()

	return string(output), err
}

func storageUnprivUserAclSet(c container, dpath string) error {
	idmapset, err := c.IdmapSetGet()
	if err != nil {
		return err
	}

	if idmapset == nil {
		return nil
	}
	uid, _ := idmapset.ShiftIntoNs(0, 0)
	switch uid {
	case -1:
		shared.Debugf("No root id mapping")
		return nil
	case 0:
		return nil
	}
	acl := fmt.Sprintf("%d:rx", uid)
	output, err := exec.Command("setfacl", "-m", acl, dpath).CombinedOutput()
	if err != nil {
		shared.Debugf("Setfacl failed:\n%s", string(output))
	}
	return err
}

// storageType defines the type of a storage
type storageType int

const (
	storageTypeBtrfs storageType = iota
	storageTypeLvm
	storageTypeDir
	storageTypeMock
)

func storageTypeToString(sType storageType) string {
	switch sType {
	case storageTypeBtrfs:
		return "btrfs"
	case storageTypeLvm:
		return "lvm"
	case storageTypeMock:
		return "mock"
	}

	return "dir"
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

	ContainerDelete(container container) error
	ContainerCopy(container container, sourceContainer container) error
	ContainerStart(container container) error
	ContainerStop(container container) error
	ContainerRename(container container, newName string) error
	ContainerRestore(container container, sourceContainer container) error

	ContainerSnapshotCreate(
		snapshotContainer container, sourceContainer container) error
	ContainerSnapshotDelete(snapshotContainer container) error
	ContainerSnapshotRename(snapshotContainer container, newName string) error

	ImageCreate(fingerprint string) error
	ImageDelete(fingerprint string) error
}

func newStorage(d *Daemon, sType storageType) (storage, error) {
	var nilmap map[string]interface{}
	return newStorageWithConfig(d, sType, nilmap)
}

func newStorageWithConfig(d *Daemon, sType storageType, config map[string]interface{}) (storage, error) {
	if d.IsMock {
		return d.Storage, nil
	}

	var s storage

	switch sType {
	case storageTypeBtrfs:
		if d.Storage != nil && d.Storage.GetStorageType() == storageTypeBtrfs {
			return d.Storage, nil
		}

		s = &storageLogWrapper{w: &storageBtrfs{d: d}}
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
	config := make(map[string]interface{})
	storageType := storageTypeDir

	filesystem, err := filesystemDetect(filename)
	if err != nil {
		return nil, fmt.Errorf("couldn't detect filesystem for '%s': %v", filename, err)
	}

	lvLinkPath := filename + ".lv"
	if shared.PathExists(lvLinkPath) {
		storageType = storageTypeLvm
		lvPath, err := os.Readlink(lvLinkPath)
		if err != nil {
			return nil, fmt.Errorf("couldn't read link dest for '%s': %v", lvLinkPath, err)
		}
		vgname := filepath.Base(filepath.Dir(lvPath))
		config["vgName"] = vgname

	} else if filesystem == "btrfs" {
		storageType = storageTypeBtrfs
	}
	return newStorageWithConfig(d, storageType, config)
}

func storageForImage(d *Daemon, imgInfo *shared.ImageBaseInfo) (storage, error) {
	imageFilename := shared.VarPath("images", imgInfo.Fingerprint)
	return storageForFilename(d, imageFilename)
}

type storageShared struct {
	sType        storageType
	sTypeName    string
	sTypeVersion string

	log log.Logger
}

func (ss *storageShared) initShared() error {
	ss.log = shared.Log.New(
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
	dpath := c.PathGet("")
	rpath := c.RootfsPathGet()

	shared.Log.Debug("Shifting root filesystem",
		log.Ctx{"container": c.NameGet(), "rootfs": rpath})

	idmapset, err := c.IdmapSetGet()
	if err != nil {
		return err
	}

	if idmapset == nil {
		return fmt.Errorf("IdmapSet of container '%s' is nil", c.NameGet())
	}

	err = idmapset.ShiftRootfs(rpath)
	if err != nil {
		shared.Debugf("Shift of rootfs %s failed: %s", rpath, err)
		return err
	}

	/* Set an acl so the container root can descend the container dir */
	// TODO: i changed this so it calls ss.setUnprivUserAcl, which does
	// the acl change only if the container is not privileged, think thats right.
	return ss.setUnprivUserAcl(c, dpath)
}

func (ss *storageShared) setUnprivUserAcl(c container, destPath string) error {

	if !c.IsPrivileged() {
		err := storageUnprivUserAclSet(c, destPath)
		if err != nil {
			ss.log.Error(
				"Adding acl for container root: falling back to chmod",
				log.Ctx{"destPath": destPath})

			output, err := exec.Command(
				"chmod", "+x", destPath).CombinedOutput()

			if err != nil {
				ss.log.Error(
					"chmoding the container root",
					log.Ctx{
						"destPath": destPath,
						"output":   string(output)})

				return err
			}
		}
	}

	return nil
}

type storageLogWrapper struct {
	w   storage
	log log.Logger
}

func (lw *storageLogWrapper) Init(config map[string]interface{}) (storage, error) {
	_, err := lw.w.Init(config)
	lw.log = shared.Log.New(
		log.Ctx{"driver": fmt.Sprintf("storage/%s", lw.w.GetStorageTypeName())},
	)

	lw.log.Info("Init")
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
			"name":         container.NameGet(),
			"isPrivileged": container.IsPrivileged()})
	return lw.w.ContainerCreate(container)
}

func (lw *storageLogWrapper) ContainerCreateFromImage(
	container container, imageFingerprint string) error {

	lw.log.Debug(
		"ContainerCreateFromImage",
		log.Ctx{
			"imageFingerprint": imageFingerprint,
			"name":             container.NameGet(),
			"isPrivileged":     container.IsPrivileged()})
	return lw.w.ContainerCreateFromImage(container, imageFingerprint)
}

func (lw *storageLogWrapper) ContainerDelete(container container) error {
	lw.log.Debug("ContainerDelete", log.Ctx{"container": container.NameGet()})
	return lw.w.ContainerDelete(container)
}

func (lw *storageLogWrapper) ContainerCopy(
	container container, sourceContainer container) error {

	lw.log.Debug(
		"ContainerCopy",
		log.Ctx{
			"container": container.NameGet(),
			"source":    sourceContainer.NameGet()})
	return lw.w.ContainerCopy(container, sourceContainer)
}

func (lw *storageLogWrapper) ContainerStart(container container) error {
	lw.log.Debug("ContainerStart", log.Ctx{"container": container.NameGet()})
	return lw.w.ContainerStart(container)
}

func (lw *storageLogWrapper) ContainerStop(container container) error {
	lw.log.Debug("ContainerStop", log.Ctx{"container": container.NameGet()})
	return lw.w.ContainerStop(container)
}

func (lw *storageLogWrapper) ContainerRename(
	container container, newName string) error {

	lw.log.Debug(
		"ContainerRename",
		log.Ctx{
			"container": container.NameGet(),
			"newName":   newName})
	return lw.w.ContainerRename(container, newName)
}

func (lw *storageLogWrapper) ContainerRestore(
	container container, sourceContainer container) error {

	lw.log.Debug(
		"ContainerRestore",
		log.Ctx{
			"container": container.NameGet(),
			"source":    sourceContainer.NameGet()})
	return lw.w.ContainerRestore(container, sourceContainer)
}

func (lw *storageLogWrapper) ContainerSnapshotCreate(
	snapshotContainer container, sourceContainer container) error {

	lw.log.Debug("ContainerSnapshotCreate",
		log.Ctx{
			"snapshotContainer": snapshotContainer.NameGet(),
			"sourceContainer":   sourceContainer.NameGet()})

	return lw.w.ContainerSnapshotCreate(snapshotContainer, sourceContainer)
}
func (lw *storageLogWrapper) ContainerSnapshotDelete(
	snapshotContainer container) error {

	lw.log.Debug("ContainerSnapshotDelete",
		log.Ctx{"snapshotContainer": snapshotContainer.NameGet()})
	return lw.w.ContainerSnapshotDelete(snapshotContainer)
}

func (lw *storageLogWrapper) ContainerSnapshotRename(
	snapshotContainer container, newName string) error {

	lw.log.Debug("ContainerSnapshotRename",
		log.Ctx{
			"snapshotContainer": snapshotContainer.NameGet(),
			"newName":           newName})
	return lw.w.ContainerSnapshotRename(snapshotContainer, newName)
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
