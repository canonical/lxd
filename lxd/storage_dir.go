package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"

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

	rootfsPath := fmt.Sprintf("%s/rootfs", s.containerGetPath(container.name))
	if err := os.MkdirAll(rootfsPath, 0700); err != nil {
		return fmt.Errorf("Error creating rootfs directory")
	}

	if err := extractImage(imageFingerprint, container.name, s.d); err != nil {
		return err
	}

	if !container.isPrivileged() {
		if err := shiftRootfs(container, s.d); err != nil {
			return err
		}
	}

	return templateApply(container, "create")
}

func (s *storageDir) ContainerDelete(container *lxdContainer) error {
	cPath := s.containerGetPath(container.name)

	err := os.RemoveAll(cPath)
	if err != nil {
		s.log.Error("ContainerDelete: failed", log.Ctx{"cPath": cPath, "err": err})
		return fmt.Errorf("Error cleaning up %s: %s", cPath, err)
	}

	return nil
}

func (s *storageDir) ContainerCopy(
	container *lxdContainer, sourceContainer *lxdContainer) error {

	oldPath := migration.AddSlash(shared.VarPath("lxc", sourceContainer.name, "rootfs"))
	if shared.IsSnapshot(sourceContainer.name) {
		snappieces := strings.SplitN(sourceContainer.name, "/", 2)
		oldPath = migration.AddSlash(shared.VarPath("lxc",
			snappieces[0],
			"snapshots",
			snappieces[1],
			"rootfs"))
	}

	newPath := filepath.Join(s.containerGetPath(container.name), "rootfs")

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
		err := setUnprivUserAcl(sourceContainer, s.containerGetPath(container.name))
		if err != nil {
			s.log.Error(
				"ContainerCopy: adding acl for container root: falling back to chmod")
			output, err := exec.Command(
				"chmod", "+x", s.containerGetPath(container.name)).CombinedOutput()
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
