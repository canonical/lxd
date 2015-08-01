package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

type storageBtrfs struct {
	d     *Daemon
	sType storageType

	storageShared
}

func (s *storageBtrfs) Init(config map[string]interface{}) (storage, error) {
	s.sTypeName = storageTypeToString(s.sType)
	if err := s.initShared(); err != nil {
		return s, err
	}

	out, err := exec.LookPath("btrfs")
	if err != nil || len(out) == 0 {
		return s, fmt.Errorf("The 'btrfs' tool isn't available")
	}

	return s, nil
}

func (s *storageBtrfs) GetStorageType() storageType {
	return s.sType
}

func (s *storageBtrfs) ContainerCreate(container container) error {
	err := s.subvolCreate(container.PathGet(""))
	if err != nil {
		return err
	}

	if container.IsPrivileged() {
		if err := os.Chmod(container.PathGet(""), 0700); err != nil {
			return err
		}
	}

	return container.TemplateApply("create")
}

func (s *storageBtrfs) ContainerCreateFromImage(
	container container, imageFingerprint string) error {

	imageSubvol := fmt.Sprintf(
		"%s.btrfs",
		shared.VarPath("images", imageFingerprint))

	// Create the btrfs subvol of the image first if it doesn exists.
	if !shared.PathExists(imageSubvol) {
		if err := s.ImageCreate(imageFingerprint); err != nil {
			return err
		}
	}

	// Now make a snapshot of the image subvol
	err := s.subvolSnapshot(imageSubvol, container.PathGet(""), false)
	if err != nil {
		return err
	}

	if !container.IsPrivileged() {
		if err = s.shiftRootfs(container); err != nil {
			s.ContainerDelete(container)
			return err
		}
	} else {
		if err := os.Chmod(container.PathGet(""), 0700); err != nil {
			return err
		}
	}

	return container.TemplateApply("create")
}

func (s *storageBtrfs) ContainerDelete(container container) error {
	cPath := container.PathGet("")

	// First remove the subvol (if it was one).
	if s.isSubvolume(cPath) {
		if err := s.subvolDelete(cPath); err != nil {
			return err
		}
	}

	// Then the directory (if it still exists).
	err := os.RemoveAll(cPath)
	if err != nil {
		s.log.Error("ContainerDelete: failed", log.Ctx{"cPath": cPath, "err": err})
		return fmt.Errorf("Error cleaning up %s: %s", cPath, err)
	}

	// If its name contains a "/" also remove the parent,
	// this should only happen with snapshot containers
	if strings.Contains(container.NameGet(), "/") {
		oldPathParent := filepath.Dir(container.PathGet(""))
		shared.Log.Debug(
			"Trying to remove the parent path",
			log.Ctx{"container": container.NameGet(), "parent": oldPathParent})

		if ok, _ := shared.PathIsEmpty(oldPathParent); ok {
			os.Remove(oldPathParent)
		} else {
			shared.Log.Debug(
				"Cannot remove the parent of this container its not empty",
				log.Ctx{"container": container.NameGet(), "parent": oldPathParent})
		}
	}

	return nil
}

func (s *storageBtrfs) ContainerCopy(container container, sourceContainer container) error {

	subvol := sourceContainer.PathGet("")
	dpath := container.PathGet("")

	if s.isSubvolume(subvol) {
		err := s.subvolSnapshot(subvol, dpath, false)
		if err != nil {
			return err
		}
	} else {
		/*
		 * Copy by using rsync
		 */
		output, err := storageRsyncCopy(
			sourceContainer.RootfsPathGet(),
			container.RootfsPathGet())
		if err != nil {
			s.ContainerDelete(container)

			s.log.Error("ContainerCopy: rsync failed", log.Ctx{"output": output})
			return fmt.Errorf("rsync failed: %s", output)
		}
	}

	if err := s.setUnprivUserAcl(sourceContainer, dpath); err != nil {
		return err
	}

	return container.TemplateApply("copy")
}

func (s *storageBtrfs) ContainerStart(container container) error {
	return nil
}

func (s *storageBtrfs) ContainerStop(container container) error {
	return nil
}

func (s *storageBtrfs) ContainerRename(
	container container, newName string) error {

	oldPath := container.PathGet("")
	newPath := container.PathGet(newName)

	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}

	// TODO: No TemplateApply here?
	return nil
}

func (s *storageBtrfs) ContainerRestore(
	container container, sourceContainer container) error {

	targetSubVol := container.PathGet("")
	sourceSubVol := sourceContainer.PathGet("")
	sourceBackupPath := container.PathGet("") + ".back"

	// Create a backup of the container
	err := os.Rename(container.PathGet(""), sourceBackupPath)
	if err != nil {
		return err
	}

	var failure error
	if s.isSubvolume(sourceSubVol) {
		// Restore using btrfs snapshots.
		err := s.subvolSnapshot(sourceSubVol, targetSubVol, false)
		if err != nil {
			failure = err
		}
	} else {
		// Restore using rsync but create a btrfs subvol.
		if err := s.subvolCreate(targetSubVol); err == nil {
			output, err := storageRsyncCopy(
				sourceSubVol,
				targetSubVol)

			if err != nil {
				s.log.Error(
					"ContainerRestore: rsync failed",
					log.Ctx{"output": output})

				failure = err
			}
		} else {
			failure = err
		}
	}

	// Now allow unprivileged users to access its data.
	if err := s.setUnprivUserAcl(sourceContainer, targetSubVol); err != nil {
		failure = err
	}

	if failure != nil {
		// Restore original container
		s.ContainerDelete(container)
		os.Rename(sourceBackupPath, container.PathGet(""))
	} else {
		// Remove the backup, we made
		if s.isSubvolume(sourceBackupPath) {
			return s.subvolDelete(sourceBackupPath)
		}
		os.RemoveAll(sourceBackupPath)
	}

	return failure
}

func (s *storageBtrfs) ContainerSnapshotCreate(
	snapshotContainer container, sourceContainer container) error {

	subvol := sourceContainer.PathGet("")
	dpath := snapshotContainer.PathGet("")

	if s.isSubvolume(subvol) {
		// Create a readonly snapshot of the source.
		err := s.subvolSnapshot(subvol, dpath, true)
		if err != nil {
			s.ContainerDelete(snapshotContainer)
			return err
		}
	} else {
		/*
		 * Copy by using rsync
		 */
		output, err := storageRsyncCopy(
			subvol,
			dpath)
		if err != nil {
			s.ContainerDelete(snapshotContainer)

			s.log.Error(
				"ContainerSnapshotCreate: rsync failed",
				log.Ctx{"output": output})
			return fmt.Errorf("rsync failed: %s", output)
		}
	}

	return nil
}
func (s *storageBtrfs) ContainerSnapshotDelete(
	snapshotContainer container) error {

	return s.ContainerDelete(snapshotContainer)
}

// ContainerSnapshotRename renames a snapshot of a container.
func (s *storageBtrfs) ContainerSnapshotRename(
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

func (s *storageBtrfs) ImageCreate(fingerprint string) error {
	imagePath := shared.VarPath("images", fingerprint)
	subvol := fmt.Sprintf("%s.btrfs", imagePath)

	if err := s.subvolCreate(subvol); err != nil {
		return err
	}

	if err := untarImage(imagePath, subvol); err != nil {
		return err
	}

	return nil
}

func (s *storageBtrfs) ImageDelete(fingerprint string) error {
	imagePath := shared.VarPath("images", fingerprint)
	subvol := fmt.Sprintf("%s.btrfs", imagePath)

	return s.subvolDelete(subvol)
}

func (s *storageBtrfs) subvolCreate(subvol string) error {
	parentDestPath := filepath.Dir(subvol)
	if !shared.PathExists(parentDestPath) {
		if err := os.MkdirAll(parentDestPath, 0700); err != nil {
			return err
		}
	}

	output, err := exec.Command(
		"btrfs",
		"subvolume",
		"create",
		subvol).CombinedOutput()
	if err != nil {
		s.log.Debug(
			"subvolume create failed",
			log.Ctx{"subvol": subvol, "output": output},
		)
		return fmt.Errorf(
			"btrfs subvolume create failed, subvol=%s, output%s",
			subvol,
			output,
		)
	}

	return nil
}

func (s *storageBtrfs) subvolDelete(subvol string) error {
	output, err := exec.Command(
		"btrfs",
		"subvolume",
		"delete",
		subvol,
	).CombinedOutput()

	if err != nil {
		s.log.Warn(
			"subvolume delete failed",
			log.Ctx{"subvol": subvol, "output": output},
		)
	}
	return nil
}

/*
 * subvolSnapshot creates a snapshot of "source" to "dest"
 * the result will be readonly if "readonly" is True.
 */
func (s *storageBtrfs) subvolSnapshot(source string, dest string, readonly bool) error {
	parentDestPath := filepath.Dir(dest)
	if !shared.PathExists(parentDestPath) {
		if err := os.MkdirAll(parentDestPath, 0700); err != nil {
			return err
		}
	}

	var out []byte
	var err error
	if readonly {
		out, err = exec.Command(
			"btrfs",
			"subvolume",
			"snapshot",
			"-r",
			source,
			dest).CombinedOutput()
	} else {
		out, err = exec.Command(
			"btrfs",
			"subvolume",
			"snapshot",
			source,
			dest).CombinedOutput()
	}
	if err != nil {
		s.log.Error(
			"subvolume snapshot failed",
			log.Ctx{"source": source, "dest": dest, "output": out},
		)
		return fmt.Errorf(
			"subvolume snapshot failed, source=%s, dest=%s, output=%s",
			source,
			dest,
			out,
		)
	}

	return err
}

/*
 * isSubvolume returns true if the given Path is a btrfs subvolume
 * else false.
 */
func (s *storageBtrfs) isSubvolume(subvolPath string) bool {
	out, err := exec.Command(
		"btrfs",
		"subvolume",
		"show",
		subvolPath).CombinedOutput()
	if err != nil || strings.HasPrefix(string(out), "ERROR: ") {
		return false
	}

	return true
}
