package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/pborman/uuid"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"

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

	output, err := shared.RunCommand("btrfs", "version")
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
	if shared.PathExists(cPath) {
		err := os.RemoveAll(cPath)
		if err != nil {
			s.log.Error("ContainerDelete: failed", log.Ctx{"cPath": cPath, "err": err})
			return fmt.Errorf("Error cleaning up %s: %s", cPath, err)
		}
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

func (s *storageBtrfs) ContainerStart(name string, path string) error {
	return nil
}

func (s *storageBtrfs) ContainerStop(name string, path string) error {
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
			return s.subvolsDelete(sourceBackupPath)
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

	output, err := shared.RunCommand(
		"btrfs",
		"qgroup",
		"limit",
		"-e", fmt.Sprintf("%d", size),
		subvol)

	if err != nil {
		return fmt.Errorf("Failed to set btrfs quota: %s", output)
	}

	return nil
}

func (s *storageBtrfs) ContainerGetUsage(container container) (int64, error) {
	return s.subvolQGroupUsage(container.Path())
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

	if err := unpackImage(s.d, imagePath, subvol); err != nil {
		s.subvolsDelete(subvol)
		return err
	}

	return nil
}

func (s *storageBtrfs) ImageDelete(fingerprint string) error {
	imagePath := shared.VarPath("images", fingerprint)
	subvol := fmt.Sprintf("%s.btrfs", imagePath)

	if s.isSubvolume(subvol) {
		if err := s.subvolsDelete(subvol); err != nil {
			return err
		}
	}

	return nil
}

func (s *storageBtrfs) subvolCreate(subvol string) error {
	parentDestPath := filepath.Dir(subvol)
	if !shared.PathExists(parentDestPath) {
		if err := os.MkdirAll(parentDestPath, 0700); err != nil {
			return err
		}
	}

	output, err := shared.RunCommand(
		"btrfs",
		"subvolume",
		"create",
		subvol)
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
	output, err := shared.RunCommand(
		"btrfs",
		"qgroup",
		"show",
		subvol,
		"-e",
		"-f")

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

func (s *storageBtrfs) subvolQGroupUsage(subvol string) (int64, error) {
	output, err := shared.RunCommand(
		"btrfs",
		"qgroup",
		"show",
		subvol,
		"-e",
		"-f")

	if err != nil {
		return -1, fmt.Errorf("btrfs quotas not supported. Try enabling them with 'btrfs quota enable'.")
	}

	for _, line := range strings.Split(string(output), "\n") {
		if line == "" || strings.HasPrefix(line, "qgroupid") || strings.HasPrefix(line, "---") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}

		usage, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			continue
		}

		return usage, nil
	}

	return -1, fmt.Errorf("Unable to find current qgroup usage")
}

func (s *storageBtrfs) subvolDelete(subvol string) error {
	// Attempt (but don't fail on) to delete any qgroup on the subvolume
	qgroup, err := s.subvolQGroup(subvol)
	if err == nil {
		output, err := shared.RunCommand(
			"btrfs",
			"qgroup",
			"destroy",
			qgroup,
			subvol)

		if err != nil {
			s.log.Warn(
				"subvolume qgroup delete failed",
				log.Ctx{"subvol": subvol, "output": string(output)},
			)
		}
	}

	// Delete the subvolume itself
	output, err := shared.RunCommand(
		"btrfs",
		"subvolume",
		"delete",
		subvol,
	)

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
	sort.Sort(sort.Reverse(sort.StringSlice(subsubvols)))

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

	var output string
	var err error
	if readonly {
		output, err = shared.RunCommand(
			"btrfs",
			"subvolume",
			"snapshot",
			"-r",
			source,
			dest)
	} else {
		output, err = shared.RunCommand(
			"btrfs",
			"subvolume",
			"snapshot",
			source,
			dest)
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
	sort.Sort(sort.StringSlice(subsubvols))

	if len(subsubvols) > 0 && readonly {
		// A root with subvolumes can never be readonly,
		// also don't make subvolumes readonly.
		readonly = false

		s.log.Warn(
			"Subvolumes detected, ignoring ro flag",
			log.Ctx{"source": source, "dest": dest})
	}

	// First snapshot the root
	err = s.subvolSnapshot(source, dest, readonly)
	if err != nil {
		return err
	}

	// Now snapshot all subvolumes of the root.
	for _, subsubvol := range subsubvols {
		// Clear the target for the subvol to use
		os.Remove(path.Join(dest, subsubvol))

		err := s.subvolSnapshot(path.Join(source, subsubvol), path.Join(dest, subsubvol), readonly)
		if err != nil {
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
	fs := syscall.Stat_t{}
	err := syscall.Lstat(subvolPath, &fs)
	if err != nil {
		return false
	}

	// Check if BTRFS_FIRST_FREE_OBJECTID
	if fs.Ino != 256 {
		return false
	}

	return true
}

// getSubVolumes returns a list of relative subvolume paths of "path".
func (s *storageBtrfs) getSubVolumes(path string) ([]string, error) {
	result := []string{}

	if !strings.HasSuffix(path, "/") {
		path = path + "/"
	}

	// Unprivileged users can't get to fs internals
	filepath.Walk(path, func(fpath string, fi os.FileInfo, err error) error {
		// Skip walk errors
		if err != nil {
			return nil
		}

		// Ignore the base path
		if strings.TrimRight(fpath, "/") == strings.TrimRight(path, "/") {
			return nil
		}

		// Subvolumes can only be directories
		if !fi.IsDir() {
			return nil
		}

		// Check if a btrfs subvolume
		if s.isSubvolume(fpath) {
			result = append(result, strings.TrimPrefix(fpath, path))
		}

		return nil
	})

	return result, nil
}

type btrfsMigrationSourceDriver struct {
	container          container
	snapshots          []container
	btrfsSnapshotNames []string
	btrfs              *storageBtrfs
	runningSnapName    string
	stoppedSnapName    string
}

func (s *btrfsMigrationSourceDriver) Snapshots() []container {
	return s.snapshots
}

func (s *btrfsMigrationSourceDriver) send(conn *websocket.Conn, btrfsPath string, btrfsParent string) error {
	args := []string{"send", btrfsPath}
	if btrfsParent != "" {
		args = append(args, "-p", btrfsParent)
	}

	cmd := exec.Command("btrfs", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	<-shared.WebsocketSendStream(conn, stdout, 4*1024*1024)

	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		logger.Error("problem reading btrfs send stderr", log.Ctx{"err": err})
	}

	err = cmd.Wait()
	if err != nil {
		logger.Error("problem with btrfs send", log.Ctx{"output": string(output)})
	}

	return err
}

func (s *btrfsMigrationSourceDriver) SendWhileRunning(conn *websocket.Conn) error {
	if s.container.IsSnapshot() {
		tmpPath := containerPath(fmt.Sprintf("%s/.migration-send-%s", s.container.Name(), uuid.NewRandom().String()), true)
		err := os.MkdirAll(tmpPath, 0700)
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpPath)

		btrfsPath := fmt.Sprintf("%s/.root", tmpPath)
		if err := s.btrfs.subvolsSnapshot(s.container.Path(), btrfsPath, true); err != nil {
			return err
		}

		defer s.btrfs.subvolsDelete(btrfsPath)

		return s.send(conn, btrfsPath, "")
	}

	for i, snap := range s.snapshots {
		prev := ""
		if i > 0 {
			prev = s.snapshots[i-1].Path()
		}

		if err := s.send(conn, snap.Path(), prev); err != nil {
			return err
		}
	}

	/* We can't send running fses, so let's snapshot the fs and send
	 * the snapshot.
	 */
	tmpPath := containerPath(fmt.Sprintf("%s/.migration-send-%s", s.container.Name(), uuid.NewRandom().String()), true)
	err := os.MkdirAll(tmpPath, 0700)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpPath)

	s.runningSnapName = fmt.Sprintf("%s/.root", tmpPath)
	if err := s.btrfs.subvolsSnapshot(s.container.Path(), s.runningSnapName, true); err != nil {
		return err
	}
	defer s.btrfs.subvolsDelete(s.runningSnapName)

	btrfsParent := ""
	if len(s.btrfsSnapshotNames) > 0 {
		btrfsParent = s.btrfsSnapshotNames[len(s.btrfsSnapshotNames)-1]
	}

	return s.send(conn, s.runningSnapName, btrfsParent)
}

func (s *btrfsMigrationSourceDriver) SendAfterCheckpoint(conn *websocket.Conn) error {
	tmpPath := containerPath(fmt.Sprintf("%s/.migration-send-%s", s.container.Name(), uuid.NewRandom().String()), true)
	err := os.MkdirAll(tmpPath, 0700)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpPath)

	s.stoppedSnapName = fmt.Sprintf("%s/.root", tmpPath)
	err = s.btrfs.subvolsSnapshot(s.container.Path(), s.stoppedSnapName, true)
	if err != nil {
		return err
	}

	return s.send(conn, s.stoppedSnapName, s.runningSnapName)
}

func (s *btrfsMigrationSourceDriver) Cleanup() {
	if s.stoppedSnapName != "" {
		s.btrfs.subvolsDelete(s.stoppedSnapName)
	}

	if s.runningSnapName != "" {
		s.btrfs.subvolsDelete(s.runningSnapName)
	}
}

func (s *storageBtrfs) MigrationType() MigrationFSType {
	if runningInUserns {
		return MigrationFSType_RSYNC
	}

	return MigrationFSType_BTRFS
}

func (s *storageBtrfs) PreservesInodes() bool {
	if runningInUserns {
		return false
	} else {
		return true
	}
}

func (s *storageBtrfs) MigrationSource(c container) (MigrationStorageSourceDriver, error) {
	if runningInUserns {
		return rsyncMigrationSource(c)
	}

	/* List all the snapshots in order of reverse creation. The idea here
	 * is that we send the oldest to newest snapshot, hopefully saving on
	 * xfer costs. Then, after all that, we send the container itself.
	 */
	snapshots, err := c.Snapshots()
	if err != nil {
		return nil, err
	}

	driver := &btrfsMigrationSourceDriver{
		container:          c,
		snapshots:          snapshots,
		btrfsSnapshotNames: []string{},
		btrfs:              s,
	}

	for _, snap := range snapshots {
		btrfsPath := snap.Path()
		driver.btrfsSnapshotNames = append(driver.btrfsSnapshotNames, btrfsPath)
	}

	return driver, nil
}

func (s *storageBtrfs) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet) error {
	if runningInUserns {
		return rsyncMigrationSink(live, container, snapshots, conn, srcIdmap)
	}

	cName := container.Name()

	snapshotsPath := shared.VarPath(fmt.Sprintf("snapshots/%s", cName))
	if !shared.PathExists(snapshotsPath) {
		err := os.MkdirAll(shared.VarPath(fmt.Sprintf("snapshots/%s", cName)), 0700)
		if err != nil {
			return err
		}
	}

	btrfsRecv := func(btrfsPath string, targetPath string, isSnapshot bool) error {
		args := []string{"receive", "-e", btrfsPath}
		cmd := exec.Command("btrfs", args...)

		// Remove the existing pre-created subvolume
		err := s.subvolsDelete(targetPath)
		if err != nil {
			logger.Errorf("Failed to delete pre-created BTRFS subvolume: %s.", btrfsPath)
			return err
		}

		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			return err
		}

		err = cmd.Start()
		if err != nil {
			return err
		}

		<-shared.WebsocketRecvStream(stdin, conn)

		output, err := ioutil.ReadAll(stderr)
		if err != nil {
			logger.Debugf("problem reading btrfs receive stderr %s", err)
		}

		err = cmd.Wait()
		if err != nil {
			logger.Error("problem with btrfs receive", log.Ctx{"output": string(output)})
			return err
		}

		if !isSnapshot {
			cPath := containerPath(fmt.Sprintf("%s/.root", cName), true)

			err := s.subvolsSnapshot(cPath, targetPath, false)
			if err != nil {
				logger.Error("problem with btrfs snapshot", log.Ctx{"err": err})
				return err
			}

			err = s.subvolsDelete(cPath)
			if err != nil {
				logger.Error("problem with btrfs delete", log.Ctx{"err": err})
				return err
			}
		}

		return nil
	}

	for _, snap := range snapshots {
		args := snapshotProtobufToContainerArgs(container.Name(), snap)
		s, err := containerCreateEmptySnapshot(container.Daemon(), args)
		if err != nil {
			return err
		}

		if err := btrfsRecv(containerPath(cName, true), s.Path(), true); err != nil {
			return err
		}
	}

	/* finally, do the real container */
	if err := btrfsRecv(containerPath(cName, true), container.Path(), false); err != nil {
		return err
	}

	if live {
		if err := btrfsRecv(containerPath(cName, true), container.Path(), false); err != nil {
			return err
		}
	}

	// Cleanup
	if ok, _ := shared.PathIsEmpty(snapshotsPath); ok {
		err := os.Remove(snapshotsPath)
		if err != nil {
			return err
		}
	}

	return nil
}
