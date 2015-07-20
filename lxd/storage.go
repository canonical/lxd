package main

import (
	"fmt"
	"os"
	"os/exec"
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

// storageType defines the type of a storage
type storageType int

const (
	storageTypeBtrfs storageType = iota
	storageTypeLvm
)

func storageTypeToString(sType storageType) string {
	switch sType {
	case storageTypeBtrfs:
		return "btrfs"
	case storageTypeLvm:
		return "lvm"
	}

	return "default"
}

type storage interface {
	Init() (storage, error)

	GetStorageType() storageType
	GetStorageTypeName() string

	ContainerCreate(container *lxdContainer, imageFingerprint string) error
	ContainerDelete(container *lxdContainer) error
	ContainerCopy(container *lxdContainer, sourceContainer *lxdContainer) error
	ContainerStart(container *lxdContainer) error
	ContainerStop(container *lxdContainer) error

	ContainerSnapshotCreate(container *lxdContainer, snapshotName string) error
	ContainerSnapshotDelete(container *lxdContainer, snapshotName string) error

	ImageCreate(fingerprint string) error
	ImageDelete(fingerprint string) error
}

func newStorage(d *Daemon, sType storageType) (storage, error) {
	var s storage

	switch sType {
	case storageTypeBtrfs:
		s = &storageLogWrapper{w: &storageBtrfs{d: d, sType: sType}}
	case storageTypeLvm:
		s = &storageLogWrapper{w: &storageLvm{d: d, sType: sType}}
	default:
		s = &storageLogWrapper{w: &storageDir{d: d, sType: sType}}
	}

	return s.Init()
}

type storageShared struct {
	sTypeName string

	log log.Logger
}

func (ss *storageShared) initShared() error {
	ss.log = shared.Log.New(
		log.Ctx{"driver": fmt.Sprintf("storage/%s", ss.sTypeName)},
	)
	return nil
}

func (ss *storageShared) GetStorageTypeName() string {
	return ss.sTypeName
}

// rsyncCopy copies a directory using rsync (with the --devices option).
func (ss *storageShared) rsyncCopy(source string, dest string) (string, error) {
	if err := os.MkdirAll(dest, 0700); err != nil {
		return "", err
	}

	output, err := exec.Command(
		"rsync",
		"-a",
		"--devices",
		shared.AddSlash(source),
		dest).CombinedOutput()

	return string(output), err
}

type storageLogWrapper struct {
	w   storage
	log log.Logger
}

func (lw *storageLogWrapper) Init() (storage, error) {
	_, err := lw.w.Init()
	lw.log = shared.Log.New(
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

func (lw *storageLogWrapper) ContainerCreate(
	container *lxdContainer, imageFingerprint string) error {

	lw.log.Debug(
		"ContainerCreate",
		log.Ctx{
			"imageFingerprint": imageFingerprint,
			"name":             container.name,
			"isPrivileged":     container.isPrivileged})
	return lw.w.ContainerCreate(container, imageFingerprint)
}

func (lw *storageLogWrapper) ContainerDelete(container *lxdContainer) error {
	lw.log.Debug("ContainerDelete", log.Ctx{"container": container.name})
	return lw.w.ContainerDelete(container)
}

func (lw *storageLogWrapper) ContainerCopy(
	container *lxdContainer, sourceContainer *lxdContainer) error {

	lw.log.Debug(
		"ContainerCopy",
		log.Ctx{
			"container": container.name,
			"source":    sourceContainer.name})
	return lw.w.ContainerCopy(container, sourceContainer)
}

func (lw *storageLogWrapper) ContainerStart(container *lxdContainer) error {
	lw.log.Debug("ContainerStart", log.Ctx{"container": container.name})
	return lw.w.ContainerStart(container)
}

func (lw *storageLogWrapper) ContainerStop(container *lxdContainer) error {
	lw.log.Debug("ContainerStop", log.Ctx{"container": container.name})
	return lw.w.ContainerStop(container)
}

func (lw *storageLogWrapper) ContainerSnapshotCreate(
	container *lxdContainer, snapshotName string) error {

	lw.log.Debug("ContainerSnapshotCreate",
		log.Ctx{"container": container.name, "snapshotName": snapshotName})
	return lw.w.ContainerSnapshotCreate(container, snapshotName)
}
func (lw *storageLogWrapper) ContainerSnapshotDelete(
	container *lxdContainer, snapshotName string) error {

	lw.log.Debug("ContainerSnapshotDelete",
		log.Ctx{"container": container.name, "snapshotName": snapshotName})
	return lw.w.ContainerSnapshotDelete(container, snapshotName)
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
