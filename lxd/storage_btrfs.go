package main

import (
	"fmt"
	"os"
	"os/exec"
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

func (s *storageBtrfs) ContainerCreate(
	container *lxdContainer, imageFingerprint string) error {

	imageSubvol := fmt.Sprintf(
		"%s.btrfs",
		shared.VarPath("images", imageFingerprint))

	if !shared.PathExists(imageSubvol) {
		if err := s.ImageCreate(imageFingerprint); err != nil {
			return err
		}
	}

	err := s.subvolSnapshot(imageSubvol, container.PathGet(), false)
	if err != nil {
		return err
	}

	if !container.isPrivileged() {
		err = shiftRootfs(container, s.d)
		if err != nil {
			return err
		}
	} else {
		if err := os.Chmod(container.PathGet(), 0700); err != nil {
			return err
		}
	}

	return templateApply(container, "create")
}

func (s *storageBtrfs) ContainerDelete(container *lxdContainer) error {
	cPath := container.PathGet()
	if s.isSubvolume(cPath) {
		return s.subvolDelete(cPath)
	}

	err := os.RemoveAll(cPath)
	if err != nil {
		s.log.Error("ContainerDelete: failed", log.Ctx{"cPath": cPath, "err": err})
		return fmt.Errorf("Error cleaning up %s: %s", cPath, err)
	}

	return nil
}

func (s *storageBtrfs) ContainerCopy(container *lxdContainer, sourceContainer *lxdContainer) error {

	subvol := sourceContainer.PathGet()
	dpath := container.PathGet()

	if s.isSubvolume(subvol) {
		err := s.subvolSnapshot(subvol, dpath, false)
		if err != nil {
			return err
		}
	} else {
		/*
		 * Copy by using rsync
		 */
		output, err := s.rsyncCopy(
			sourceContainer.RootfsPathGet(),
			container.RootfsPathGet())
		if err != nil {
			os.RemoveAll(container.PathGet())
			s.log.Error("ContainerCopy: rsync failed", log.Ctx{"output": output})
			return fmt.Errorf("rsync failed: %s", output)
		}
	}

	if !sourceContainer.isPrivileged() {
		err := setUnprivUserAcl(sourceContainer, dpath)
		if err != nil {
			s.log.Error(
				"ContainerCopy: adding acl for container root: falling back to chmod")
			output, err := exec.Command(
				"chmod", "+x", dpath).CombinedOutput()
			if err != nil {
				s.log.Error(
					"ContainerCopy: chmoding the container root", log.Ctx{"output": output})
				return err
			}
		}
	}

	return templateApply(container, "copy")
}

func (s *storageBtrfs) ContainerStart(container *lxdContainer) error {
	return nil
}

func (s *storageBtrfs) ContainerStop(container *lxdContainer) error {
	return nil
}

func (s *storageBtrfs) ContainerSnapshotCreate(
	container *lxdContainer, snapshotName string) error {

	return nil
}
func (s *storageBtrfs) ContainerSnapshotDelete(
	container *lxdContainer, snapshotName string) error {

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
		s.log.Debug(
			"subvolume delete failed",
			log.Ctx{"subvol": subvol, "output": output},
		)
		return fmt.Errorf(
			"btrfs subvolume delete failed, subvol=%s, output=%s",
			subvol,
			output,
		)
	}
	return nil
}

/*
 * subvolSnapshot creates a snapshot of "source" to "dest"
 * the result will be readonly if "readonly" is True.
 */
func (s *storageBtrfs) subvolSnapshot(source string, dest string, readonly bool) error {
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
