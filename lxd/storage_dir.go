package main

import (
	"fmt"
	"os"
	"os/exec"

	log "gopkg.in/inconshreveable/log15.v2"
)

type storageDir struct {
	d     *Daemon
	sType storageType

	storageShared
}

func (s *storageDir) Init() (storage, error) {
	s.sTypeName = storageTypeToString(s.sType)
	if err := s.initShared(); err != nil {
		return s, err
	}

	return s, nil
}

func (s *storageDir) GetStorageType() storageType {
	return s.sType
}

func (s *storageDir) ContainerCreate(
	container *lxdContainer, imageFingerprint string) error {

	rootfsPath := container.RootfsPathGet()
	if err := os.MkdirAll(rootfsPath, 0700); err != nil {
		return fmt.Errorf("Error creating rootfs directory")
	}

	if err := extractImage(imageFingerprint, container.NameGet(), s.d); err != nil {
		os.RemoveAll(rootfsPath)
		return err
	}

	if !container.isPrivileged() {
		if err := shiftRootfs(container, s.d); err != nil {
			os.RemoveAll(rootfsPath)
			return err
		}
	}

	return templateApply(container, "create")
}

func (s *storageDir) ContainerDelete(container *lxdContainer) error {
	cPath := container.PathGet()

	err := os.RemoveAll(cPath)
	if err != nil {
		s.log.Error("ContainerDelete: failed", log.Ctx{"cPath": cPath, "err": err})
		return fmt.Errorf("Error cleaning up %s: %s", cPath, err)
	}

	return nil
}

func (s *storageDir) ContainerCopy(
	container *lxdContainer, sourceContainer *lxdContainer) error {

	oldPath := sourceContainer.RootfsPathGet()
	newPath := container.RootfsPathGet()

	/*
	 * Copy by using rsync
	 */
	output, err := s.rsyncCopy(oldPath, newPath)
	if err != nil {
		s.ContainerDelete(container)
		s.log.Error("ContainerCopy: rsync failed", log.Ctx{"output": output})
		return fmt.Errorf("rsync failed: %s", output)
	}

	if !sourceContainer.isPrivileged() {
		err := setUnprivUserAcl(sourceContainer, container.PathGet())
		if err != nil {
			s.log.Error(
				"ContainerCopy: adding acl for container root: falling back to chmod")
			output, err := exec.Command(
				"chmod", "+x", container.PathGet()).CombinedOutput()
			if err != nil {
				s.ContainerDelete(container)
				s.log.Error(
					"ContainerCopy: chmoding the container root", log.Ctx{"output": output})
				return err
			}
		}
	}

	return templateApply(container, "copy")
}

func (s *storageDir) ContainerStart(container *lxdContainer) error {
	return nil
}

func (s *storageDir) ContainerStop(container *lxdContainer) error {
	return nil
}

func (s *storageDir) ContainerSnapshotCreate(
	container *lxdContainer, snapshotName string) error {

	return nil
}
func (s *storageDir) ContainerSnapshotDelete(
	container *lxdContainer, snapshotName string) error {

	return nil
}

func (s *storageDir) ImageCreate(fingerprint string) error {
	return nil
}

func (s *storageDir) ImageDelete(fingerprint string) error {
	return nil
}
