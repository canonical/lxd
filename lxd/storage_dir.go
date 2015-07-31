package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

type storageDir struct {
	d     *Daemon
	sType storageType

	storageShared
}

func (s *storageDir) Init(config map[string]interface{}) (storage, error) {
	s.sTypeName = storageTypeToString(s.sType)
	if err := s.initShared(); err != nil {
		return s, err
	}

	return s, nil
}

func (s *storageDir) GetStorageType() storageType {
	return s.sType
}

func (s *storageDir) ContainerCreate(container container) error {
	cPath := container.PathGet("")
	if err := os.MkdirAll(cPath, 0755); err != nil {
		return fmt.Errorf("Error creating containers directory")
	}

	if container.IsPrivileged() {
		if err := os.Chmod(cPath, 0700); err != nil {
			return err
		}
	} else {
		if err := s.shiftRootfs(container); err != nil {
			s.ContainerDelete(container)
			return err
		}
	}

	return container.TemplateApply("create")
}

func (s *storageDir) ContainerCreateFromImage(
	container container, imageFingerprint string) error {

	rootfsPath := container.RootfsPathGet()
	if err := os.MkdirAll(rootfsPath, 0755); err != nil {
		return fmt.Errorf("Error creating rootfs directory")
	}

	if container.IsPrivileged() {
		if err := os.Chmod(container.PathGet(""), 0700); err != nil {
			return err
		}
	}

	imagePath := shared.VarPath("images", imageFingerprint)
	if err := untarImage(imagePath, container.PathGet("")); err != nil {
		os.RemoveAll(rootfsPath)
		return err
	}

	if !container.IsPrivileged() {
		if err := s.shiftRootfs(container); err != nil {
			s.ContainerDelete(container)
			return err
		}
	}

	return container.TemplateApply("create")
}

func (s *storageDir) ContainerDelete(container container) error {
	cPath := container.PathGet("")

	err := os.RemoveAll(cPath)
	if err != nil {
		s.log.Error("ContainerDelete: failed", log.Ctx{"cPath": cPath, "err": err})
		return fmt.Errorf("Error cleaning up %s: %s", cPath, err)
	}

	// If its name contains a "/" also remove the parent,
	// this should only happen with snapshot containers
	if strings.Contains(container.NameGet(), "/") {
		oldPathParent := filepath.Dir(container.PathGet(""))
		if ok, _ := shared.PathIsEmpty(oldPathParent); ok {
			os.Remove(oldPathParent)
		}
	}

	return nil
}

func (s *storageDir) ContainerCopy(
	container container, sourceContainer container) error {

	oldPath := sourceContainer.RootfsPathGet()
	newPath := container.RootfsPathGet()

	/*
	 * Copy by using rsync
	 */
	output, err := storageRsyncCopy(oldPath, newPath)
	if err != nil {
		s.ContainerDelete(container)
		s.log.Error("ContainerCopy: rsync failed", log.Ctx{"output": output})
		return fmt.Errorf("rsync failed: %s", output)
	}

	err = s.setUnprivUserAcl(sourceContainer, container.PathGet(""))
	if err != nil {
		return err
	}

	return container.TemplateApply("copy")
}

func (s *storageDir) ContainerStart(container container) error {
	return nil
}

func (s *storageDir) ContainerStop(container container) error {
	return nil
}

func (s *storageDir) ContainerRename(
	container container, newName string) error {

	oldPath := container.PathGet("")
	newPath := container.PathGet(newName)

	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}

	// TODO: No TemplateApply here?
	return nil
}

func (s *storageDir) ContainerRestore(
	container container, sourceContainer container) error {

	targetPath := container.PathGet("")
	sourcePath := sourceContainer.PathGet("")

	// Restore using rsync
	output, err := storageRsyncCopy(
		sourcePath,
		targetPath)

	if err != nil {
		s.log.Error(
			"ContainerRestore: rsync failed",
			log.Ctx{"output": output})

		return err
	}

	// Now allow unprivileged users to access its data.
	if err := s.setUnprivUserAcl(sourceContainer, targetPath); err != nil {
		return err
	}

	return nil
}

func (s *storageDir) ContainerSnapshotCreate(
	snapshotContainer container, sourceContainer container) error {

	oldPath := sourceContainer.PathGet("")
	newPath := snapshotContainer.PathGet("")

	/*
	 * Copy by using rsync
	 */
	output, err := storageRsyncCopy(oldPath, newPath)
	if err != nil {
		s.ContainerDelete(snapshotContainer)
		s.log.Error("ContainerSnapshotCreate: rsync failed",
			log.Ctx{"output": output})

		return fmt.Errorf("rsync failed: %s", output)
	}

	return nil
}
func (s *storageDir) ContainerSnapshotDelete(
	snapshotContainer container) error {

	return s.ContainerDelete(snapshotContainer)
}

func (s *storageDir) ContainerSnapshotRename(
	snapshotContainer container, newName string) error {

	oldPath := snapshotContainer.PathGet("")
	newPath := snapshotContainer.PathGet(newName)

	// Create the new parent.
	if strings.Contains(snapshotContainer.NameGet(), "/") {
		if !shared.PathExists(filepath.Dir(newPath)) {
			os.MkdirAll(filepath.Dir(newPath), 0700)
		}
	}

	// Now rename the snapshot.
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}

	// Remove the old parent (on container rename) if its empty.
	if strings.Contains(snapshotContainer.NameGet(), "/") {
		if ok, _ := shared.PathIsEmpty(filepath.Dir(oldPath)); ok {
			os.Remove(filepath.Dir(oldPath))
		}
	}

	return nil
}

func (s *storageDir) ImageCreate(fingerprint string) error {
	return nil
}

func (s *storageDir) ImageDelete(fingerprint string) error {
	return nil
}
