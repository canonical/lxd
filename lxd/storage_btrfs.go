package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

type storageBtrfs struct {
	d *Daemon

	storageShared
}

func (s *storageBtrfs) Init(config map[string]interface{}) (storage, error) {
	s.sType = storageTypeBtrfs
	s.sTypeName = storageTypeToString(s.sType)
	if err := s.initShared(); err != nil {
		return s, err
	}

	out, err := exec.LookPath("btrfs")
	if err != nil || len(out) == 0 {
		return s, fmt.Errorf("The 'btrfs' tool isn't available")
	}

	output, err := exec.Command("btrfs", "version").CombinedOutput()
	if err != nil {
		return s, fmt.Errorf("The 'btrfs' tool isn't working properly")
	}

	count, err := fmt.Sscanf(strings.SplitN(string(output), " ", 2)[1], "v%s\n", &s.sTypeVersion)
	if err != nil || count != 1 {
		return s, fmt.Errorf("The 'btrfs' tool isn't working properly")
	}

	return s, nil
}

func (s *storageBtrfs) ContainerCreate(container container) error {
	cPath := container.Path()

	// MkdirAll the pardir of the BTRFS Subvolume.
	if err := os.MkdirAll(filepath.Dir(cPath), 0755); err != nil {
		return err
	}

	// Create the BTRFS Subvolume
	err := s.subvolCreate(cPath)
	if err != nil {
		return err
	}

	if container.IsPrivileged() {
		if err := os.Chmod(cPath, 0700); err != nil {
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
	err := s.subvolsSnapshot(imageSubvol, container.Path(), false)
	if err != nil {
		return err
	}

	if !container.IsPrivileged() {
		if err = s.shiftRootfs(container); err != nil {
			s.ContainerDelete(container)
			return err
		}
	} else {
		if err := os.Chmod(container.Path(), 0700); err != nil {
			return err
		}
	}

	return container.TemplateApply("create")
}

func (s *storageBtrfs) ContainerCanRestore(container container, sourceContainer container) error {
	return nil
}

func (s *storageBtrfs) ContainerDelete(container container) error {
	cPath := container.Path()

	// First remove the subvol (if it was one).
	if s.isSubvolume(cPath) {
		if err := s.subvolsDelete(cPath); err != nil {
			return err
		}
	}

	// Then the directory (if it still exists).
	err := os.RemoveAll(cPath)
	if err != nil {
		s.log.Error("ContainerDelete: failed", log.Ctx{"cPath": cPath, "err": err})
		return fmt.Errorf("Error cleaning up %s: %s", cPath, err)
	}

	return nil
}

func (s *storageBtrfs) ContainerCopy(container container, sourceContainer container) error {
	subvol := sourceContainer.Path()
	dpath := container.Path()

	if s.isSubvolume(subvol) {
		// Snapshot the sourcecontainer
		err := s.subvolsSnapshot(subvol, dpath, false)
		if err != nil {
			return err
		}
	} else {
		// Create the BTRFS Container.
		if err := s.ContainerCreate(container); err != nil {
			return err
		}

		/*
		 * Copy by using rsync
		 */
		output, err := storageRsyncCopy(
			sourceContainer.Path(),
			container.Path())
		if err != nil {
			s.ContainerDelete(container)

			s.log.Error("ContainerCopy: rsync failed", log.Ctx{"output": string(output)})
			return fmt.Errorf("rsync failed: %s", string(output))
		}
	}

	if err := s.setUnprivUserAcl(sourceContainer, dpath); err != nil {
		s.ContainerDelete(container)
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

func (s *storageBtrfs) ContainerRename(container container, newName string) error {
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

	// TODO: No TemplateApply here?
	return nil
}

func (s *storageBtrfs) ContainerRestore(
	container container, sourceContainer container) error {

	targetSubVol := container.Path()
	sourceSubVol := sourceContainer.Path()
	sourceBackupPath := container.Path() + ".back"

	// Create a backup of the container
	err := os.Rename(container.Path(), sourceBackupPath)
	if err != nil {
		return err
	}

	var failure error
	if s.isSubvolume(sourceSubVol) {
		// Restore using btrfs snapshots.
		err := s.subvolsSnapshot(sourceSubVol, targetSubVol, false)
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
					log.Ctx{"output": string(output)})

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
		os.Rename(sourceBackupPath, container.Path())
	} else {
		// Remove the backup, we made
		if s.isSubvolume(sourceBackupPath) {
			return s.subvolDelete(sourceBackupPath)
		}
		os.RemoveAll(sourceBackupPath)
	}

	return failure
}

func (s *storageBtrfs) ContainerSetQuota(container container, size int64) error {
	subvol := container.Path()

	_, err := s.subvolQGroup(subvol)
	if err != nil {
		return err
	}

	output, err := exec.Command(
		"btrfs",
		"qgroup",
		"limit",
		"-e", fmt.Sprintf("%d", size),
		subvol).CombinedOutput()

	if err != nil {
		return fmt.Errorf("Failed to set btrfs quota: %s", output)
	}

	return nil
}

func (s *storageBtrfs) ContainerSnapshotCreate(
	snapshotContainer container, sourceContainer container) error {

	subvol := sourceContainer.Path()
	dpath := snapshotContainer.Path()

	if s.isSubvolume(subvol) {
		// Create a readonly snapshot of the source.
		err := s.subvolsSnapshot(subvol, dpath, true)
		if err != nil {
			s.ContainerSnapshotDelete(snapshotContainer)
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
			s.ContainerSnapshotDelete(snapshotContainer)

			s.log.Error(
				"ContainerSnapshotCreate: rsync failed",
				log.Ctx{"output": string(output)})
			return fmt.Errorf("rsync failed: %s", string(output))
		}
	}

	return nil
}
func (s *storageBtrfs) ContainerSnapshotDelete(
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

func (s *storageBtrfs) ContainerSnapshotStart(container container) error {
	if shared.PathExists(container.Path() + ".ro") {
		return fmt.Errorf("The snapshot is already mounted read-write.")
	}

	err := os.Rename(container.Path(), container.Path()+".ro")
	if err != nil {
		return err
	}

	err = s.subvolsSnapshot(container.Path()+".ro", container.Path(), false)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageBtrfs) ContainerSnapshotStop(container container) error {
	if !shared.PathExists(container.Path() + ".ro") {
		return fmt.Errorf("The snapshot isn't currently mounted read-write.")
	}

	err := s.subvolsDelete(container.Path())
	if err != nil {
		return err
	}

	err = os.Rename(container.Path()+".ro", container.Path())
	if err != nil {
		return err
	}

	return nil
}

// ContainerSnapshotRename renames a snapshot of a container.
func (s *storageBtrfs) ContainerSnapshotRename(
	snapshotContainer container, newName string) error {

	oldPath := snapshotContainer.Path()
	newPath := containerPath(newName, true)

	// Create the new parent.
	if !shared.PathExists(filepath.Dir(newPath)) {
		os.MkdirAll(filepath.Dir(newPath), 0700)
	}

	// Now rename the snapshot.
	if !s.isSubvolume(oldPath) {
		if err := os.Rename(oldPath, newPath); err != nil {
			return err
		}
	} else {
		if err := s.subvolsSnapshot(oldPath, newPath, true); err != nil {
			return err
		}
		if err := s.subvolsDelete(oldPath); err != nil {
			return err
		}
	}

	// Remove the old parent (on container rename) if its empty.
	if ok, _ := shared.PathIsEmpty(filepath.Dir(oldPath)); ok {
		os.Remove(filepath.Dir(oldPath))
	}

	return nil
}

func (s *storageBtrfs) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	dpath := snapshotContainer.Path()
	return s.subvolCreate(dpath)
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
			log.Ctx{"subvol": subvol, "output": string(output)},
		)
		return fmt.Errorf(
			"btrfs subvolume create failed, subvol=%s, output%s",
			subvol,
			string(output),
		)
	}

	return nil
}

func (s *storageBtrfs) subvolQGroup(subvol string) (string, error) {
	output, err := exec.Command(
		"btrfs",
		"qgroup",
		"show",
		subvol,
		"-e",
		"-f").CombinedOutput()

	if err != nil {
		return "", fmt.Errorf("btrfs quotas not supported. Try enabling them with 'btrfs quota enable'.")
	}

	var qgroup string
	for _, line := range strings.Split(string(output), "\n") {
		if line == "" || strings.HasPrefix(line, "qgroupid") || strings.HasPrefix(line, "---") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}

		qgroup = fields[0]
	}

	if qgroup == "" {
		return "", fmt.Errorf("Unable to find quota group")
	}

	return qgroup, nil
}

func (s *storageBtrfs) subvolDelete(subvol string) error {
	// Attempt (but don't fail on) to delete any qgroup on the subvolume
	qgroup, err := s.subvolQGroup(subvol)
	if err == nil {
		output, err := exec.Command(
			"btrfs",
			"qgroup",
			"destroy",
			qgroup,
			subvol).CombinedOutput()

		if err != nil {
			s.log.Warn(
				"subvolume qgroup delete failed",
				log.Ctx{"subvol": subvol, "output": string(output)},
			)
		}
	}

	// Delete the subvolume itself
	output, err := exec.Command(
		"btrfs",
		"subvolume",
		"delete",
		subvol,
	).CombinedOutput()

	if err != nil {
		s.log.Warn(
			"subvolume delete failed",
			log.Ctx{"subvol": subvol, "output": string(output)},
		)
	}
	return nil
}

// subvolsDelete is the recursive variant on subvolDelete,
// it first deletes subvolumes of the subvolume and then the
// subvolume itself.
func (s *storageBtrfs) subvolsDelete(subvol string) error {
	// Delete subsubvols.
	subsubvols, err := s.getSubVolumes(subvol)
	if err != nil {
		return err
	}

	for _, subsubvol := range subsubvols {
		s.log.Debug(
			"Deleting subsubvol",
			log.Ctx{
				"subvol":    subvol,
				"subsubvol": subsubvol})

		if err := s.subvolDelete(path.Join(subvol, subsubvol)); err != nil {
			return err
		}
	}

	// Delete the subvol itself
	if err := s.subvolDelete(subvol); err != nil {
		return err
	}

	return nil
}

/*
 * subvolSnapshot creates a snapshot of "source" to "dest"
 * the result will be readonly if "readonly" is True.
 */
func (s *storageBtrfs) subvolSnapshot(
	source string, dest string, readonly bool) error {

	parentDestPath := filepath.Dir(dest)
	if !shared.PathExists(parentDestPath) {
		if err := os.MkdirAll(parentDestPath, 0700); err != nil {
			return err
		}
	}

	if shared.PathExists(dest) {
		if err := os.Remove(dest); err != nil {
			return err
		}
	}

	var output []byte
	var err error
	if readonly {
		output, err = exec.Command(
			"btrfs",
			"subvolume",
			"snapshot",
			"-r",
			source,
			dest).CombinedOutput()
	} else {
		output, err = exec.Command(
			"btrfs",
			"subvolume",
			"snapshot",
			source,
			dest).CombinedOutput()
	}
	if err != nil {
		s.log.Error(
			"subvolume snapshot failed",
			log.Ctx{"source": source, "dest": dest, "output": string(output)},
		)
		return fmt.Errorf(
			"subvolume snapshot failed, source=%s, dest=%s, output=%s",
			source,
			dest,
			string(output),
		)
	}

	return err
}

func (s *storageBtrfs) subvolsSnapshot(
	source string, dest string, readonly bool) error {

	// Get a list of subvolumes of the root
	subsubvols, err := s.getSubVolumes(source)
	if err != nil {
		return err
	}

	if len(subsubvols) > 0 && readonly {
		// A root with subvolumes can never be readonly,
		// also don't make subvolumes readonly.
		readonly = false

		s.log.Warn(
			"Subvolumes detected, ignoring ro flag",
			log.Ctx{"source": source, "dest": dest})
	}

	// First snapshot the root
	if err := s.subvolSnapshot(source, dest, readonly); err != nil {
		return err
	}

	// Now snapshot all subvolumes of the root.
	for _, subsubvol := range subsubvols {
		if err := s.subvolSnapshot(
			path.Join(source, subsubvol),
			path.Join(dest, subsubvol),
			readonly); err != nil {

			return err
		}
	}

	return nil
}

/*
 * isSubvolume returns true if the given Path is a btrfs subvolume
 * else false.
 */
func (s *storageBtrfs) isSubvolume(subvolPath string) bool {
	if runningInUserns {
		// subvolume show is restricted to real root, use a workaround

		fs := syscall.Statfs_t{}
		err := syscall.Statfs(subvolPath, &fs)
		if err != nil {
			return false
		}

		if fs.Type != filesystemSuperMagicBtrfs {
			return false
		}

		parentFs := syscall.Statfs_t{}
		err = syscall.Statfs(path.Dir(subvolPath), &parentFs)
		if err != nil {
			return false
		}

		if fs.Fsid == parentFs.Fsid {
			return false
		}

		return true
	}

	output, err := exec.Command(
		"btrfs",
		"subvolume",
		"show",
		subvolPath).CombinedOutput()
	if err != nil || strings.HasPrefix(string(output), "ERROR: ") {
		return false
	}

	return true
}

// getSubVolumes returns a list of relative subvolume paths of "path".
func (s *storageBtrfs) getSubVolumes(path string) ([]string, error) {
	result := []string{}

	if runningInUserns {
		if !strings.HasSuffix(path, "/") {
			path = path + "/"
		}

		// Unprivileged users can't get to fs internals
		filepath.Walk(path, func(fpath string, fi os.FileInfo, err error) error {
			if strings.TrimRight(fpath, "/") == strings.TrimRight(path, "/") {
				return nil
			}

			if err != nil {
				return nil
			}

			if !fi.IsDir() {
				return nil
			}

			if s.isSubvolume(fpath) {
				result = append(result, strings.TrimPrefix(fpath, path))
			}
			return nil
		})

		return result, nil
	}

	out, err := exec.Command(
		"btrfs",
		"inspect-internal",
		"rootid",
		path).CombinedOutput()
	if err != nil {
		return result, fmt.Errorf(
			"Unable to get btrfs rootid, path='%s', err='%s'",
			path,
			err)
	}
	rootid := strings.TrimRight(string(out), "\n")

	out, err = exec.Command(
		"btrfs",
		"inspect-internal",
		"subvolid-resolve",
		rootid, path).CombinedOutput()
	if err != nil {
		return result, fmt.Errorf(
			"Unable to resolve btrfs rootid, path='%s', err='%s'",
			path,
			err)
	}
	basePath := strings.TrimRight(string(out), "\n")

	out, err = exec.Command(
		"btrfs",
		"subvolume",
		"list",
		"-o",
		path).CombinedOutput()
	if err != nil {
		return result, fmt.Errorf(
			"Unable to list subvolumes, path='%s', err='%s'",
			path,
			err)
	}

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		cols := strings.Fields(line)
		result = append(result, cols[8][len(basePath):])
	}

	return result, nil
}

func (s *storageBtrfs) MigrationType() MigrationFSType {
	return MigrationFSType_RSYNC
}

func (s *storageBtrfs) MigrationSource(container container) ([]MigrationStorageSource, error) {
	return rsyncMigrationSource(container)
}

func (s *storageBtrfs) MigrationSink(container container, snapshots []container, conn *websocket.Conn) error {
	return rsyncMigrationSink(container, snapshots, conn)
}
