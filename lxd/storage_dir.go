package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/operation"
	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

type storageDir struct {
	d *Daemon

	storageShared
}

func (s *storageDir) Init(config map[string]interface{}) (storage, error) {
	s.sType = storageTypeDir
	s.sTypeName = storageTypeToString(s.sType)
	if err := s.initShared(); err != nil {
		return s, err
	}

	return s, nil
}

func (s *storageDir) ContainerCreate(container container) error {
	cPath := container.Path()
	if err := os.MkdirAll(cPath, 0755); err != nil {
		return fmt.Errorf("Error creating containers directory")
	}

	if container.IsPrivileged() {
		if err := os.Chmod(cPath, 0700); err != nil {
			return err
		}
	}

	return container.TemplateApply("create")
}

func (s *storageDir) ContainerCreateFromImage(
	container container, imageFingerprint string) error {

	rootfsPath := container.RootfsPath()
	if err := os.MkdirAll(rootfsPath, 0755); err != nil {
		return fmt.Errorf("Error creating rootfs directory")
	}

	if container.IsPrivileged() {
		if err := os.Chmod(container.Path(), 0700); err != nil {
			return err
		}
	}

	imagePath := shared.VarPath("images", imageFingerprint)
	if err := unpackImage(s.d, imagePath, container.Path()); err != nil {
		s.ContainerDelete(container)
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

func (s *storageDir) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageDir) ContainerDelete(container container) error {
	cPath := container.Path()

	if !shared.PathExists(cPath) {
		return nil
	}

	err := os.RemoveAll(cPath)
	if err != nil {
		// RemovaAll fails on very long paths, so attempt an rm -Rf
		output, err := exec.Command("rm", "-Rf", cPath).CombinedOutput()
		if err != nil {
			s.log.Error("ContainerDelete: failed", log.Ctx{"cPath": cPath, "output": output})
			return fmt.Errorf("Error cleaning up %s: %s", cPath, string(output))
		}
	}

	return nil
}

func (s *storageDir) ContainerCopy(
	container container, sourceContainer container) error {

	oldPath := sourceContainer.Path()
	newPath := container.Path()

	/*
	 * Copy by using rsync
	 */
	output, err := storageRsyncCopy(oldPath, newPath)
	if err != nil {
		s.ContainerDelete(container)
		s.log.Error("ContainerCopy: rsync failed", log.Ctx{"output": string(output)})
		return fmt.Errorf("rsync failed: %s", string(output))
	}

	err = s.setUnprivUserAcl(sourceContainer, container.Path())
	if err != nil {
		return err
	}

	return container.TemplateApply("copy")
}

func (s *storageDir) ContainerStart(name string, path string) error {
	return nil
}

func (s *storageDir) ContainerStop(name string, path string) error {
	return nil
}

func (s *storageDir) ContainerRename(container container, newName string) error {
	oldName := container.Name()

	oldPath := container.Path()
	newPath := containerPath(newName, false)

	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}

	if shared.PathExists(shared.VarPath(fmt.Sprintf("snapshots/%s", oldName))) {
		err := os.Rename(shared.VarPath(fmt.Sprintf("snapshots/%s", oldName)), shared.VarPath(fmt.Sprintf("snapshots/%s", newName)))
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageDir) ContainerRestore(
	container container, sourceContainer container) error {

	targetPath := container.Path()
	sourcePath := sourceContainer.Path()

	// Restore using rsync
	output, err := storageRsyncCopy(
		sourcePath,
		targetPath)

	if err != nil {
		s.log.Error(
			"ContainerRestore: rsync failed",
			log.Ctx{"output": string(output)})

		return err
	}

	// Now allow unprivileged users to access its data.
	if err := s.setUnprivUserAcl(sourceContainer, targetPath); err != nil {
		return err
	}

	return nil
}

func (s *storageDir) ContainerSetQuota(container container, size int64) error {
	return fmt.Errorf("The directory container backend doesn't support quotas.")
}

func (s *storageDir) ContainerGetUsage(container container) (int64, error) {
	return -1, fmt.Errorf("The directory container backend doesn't support quotas.")
}

func (s *storageDir) ContainerSnapshotCreate(
	snapshotContainer container, sourceContainer container) error {

	oldPath := sourceContainer.Path()
	newPath := snapshotContainer.Path()

	/*
	 * Copy by using rsync
	 */
	rsync := func(snapshotContainer container, oldPath string, newPath string) error {
		output, err := storageRsyncCopy(oldPath, newPath)
		if err != nil {
			s.ContainerDelete(snapshotContainer)
			s.log.Error("ContainerSnapshotCreate: rsync failed",
				log.Ctx{"output": string(output)})

			return fmt.Errorf("rsync failed: %s", string(output))
		}
		return nil
	}

	if err := rsync(snapshotContainer, oldPath, newPath); err != nil {
		return err
	}

	if sourceContainer.IsRunning() {
		/* This is done to ensure consistency when snapshotting. But we
		 * probably shouldn't fail just because of that.
		 */
		s.log.Debug("ContainerSnapshotCreate: trying to freeze and rsync again to ensure consistency.")
		if err := sourceContainer.Freeze(); err != nil {
			s.log.Warn("ContainerSnapshotCreate: trying to freeze and rsync again failed.")
			return nil
		}

		if err := rsync(snapshotContainer, oldPath, newPath); err != nil {
			return err
		}

		defer sourceContainer.Unfreeze()
	}

	return nil
}

func (s *storageDir) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	return os.MkdirAll(snapshotContainer.Path(), 0700)
}

func (s *storageDir) ContainerSnapshotDelete(
	snapshotContainer container) error {
	err := s.ContainerDelete(snapshotContainer)
	if err != nil {
		return fmt.Errorf("Error deleting snapshot %s: %s", snapshotContainer.Name(), err)
	}

	oldPathParent := filepath.Dir(snapshotContainer.Path())
	if ok, _ := shared.PathIsEmpty(oldPathParent); ok {
		os.Remove(oldPathParent)
	}

	return nil
}

func (s *storageDir) ContainerSnapshotRename(
	snapshotContainer container, newName string) error {

	oldPath := snapshotContainer.Path()
	newPath := containerPath(newName, true)

	// Create the new parent.
	if strings.Contains(snapshotContainer.Name(), "/") {
		if !shared.PathExists(filepath.Dir(newPath)) {
			os.MkdirAll(filepath.Dir(newPath), 0700)
		}
	}

	// Now rename the snapshot.
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}

	// Remove the old parent (on container rename) if its empty.
	if strings.Contains(snapshotContainer.Name(), "/") {
		if ok, _ := shared.PathIsEmpty(filepath.Dir(oldPath)); ok {
			os.Remove(filepath.Dir(oldPath))
		}
	}

	return nil
}

func (s *storageDir) ContainerSnapshotStart(container container) error {
	return nil
}

func (s *storageDir) ContainerSnapshotStop(container container) error {
	return nil
}

func (s *storageDir) ImageCreate(fingerprint string) error {
	return nil
}

func (s *storageDir) ImageDelete(fingerprint string) error {
	return nil
}

func (s *storageDir) MigrationType() MigrationFSType {
	return MigrationFSType_RSYNC
}

func (s *storageDir) PreservesInodes() bool {
	return false
}

func (s *storageDir) MigrationSource(container container) (MigrationStorageSourceDriver, error) {
	return rsyncMigrationSource(container)
}

func (s *storageDir) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation.Operation) error {
	return rsyncMigrationSink(live, container, snapshots, conn, srcIdmap, op)
}
