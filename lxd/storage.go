package main

import (
	"fmt"
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
	ContainerDelete(name string) error
	ContainerCopy(name string, source string) error
	ContainerStart(name string) error
	ContainerStop(name string) error

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

func (ss *storageShared) containerGetPath(name string) string {
	return shared.VarPath("lxc", name)
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

func (lw *storageLogWrapper) ContainerDelete(name string) error {
	lw.log.Debug("ContainerDelete", log.Ctx{"name": name})
	return lw.w.ContainerDelete(name)
}

func (lw *storageLogWrapper) ContainerCopy(name string, source string) error {
	lw.log.Debug(
		"ContainerCopy",
		log.Ctx{
			"name":   name,
			"source": source})
	return lw.w.ContainerCopy(name, source)
}

func (lw *storageLogWrapper) ContainerStart(name string) error {
	lw.log.Debug("ContainerStart", log.Ctx{"name": name})
	return lw.w.ContainerStart(name)
}

func (lw *storageLogWrapper) ContainerStop(name string) error {
	lw.log.Debug("ContainerStop", log.Ctx{"name": name})
	return lw.w.ContainerStop(name)
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
