package drivers

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
	"golang.org/x/sys/unix"
)

var errBtrfsNoQuota = fmt.Errorf("Quotas disabled on filesystem")
var errBtrfsNoQGroup = fmt.Errorf("Unable to find quota group")

func (d *btrfs) load() error {
	if btrfsLoaded {
		return nil
	}

	// Validate the required binaries.
	for _, tool := range []string{"btrfs"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			return fmt.Errorf("Required tool '%s' is missing", tool)
		}
	}

	// Detect and record the version.
	if btrfsVersion == "" {
		out, err := shared.RunCommand("btrfs", "version")
		if err != nil {
			return err
		}

		count, err := fmt.Sscanf(strings.SplitN(out, " ", 2)[1], "v%s\n", &btrfsVersion)
		if err != nil || count != 1 {
			return fmt.Errorf("The 'btrfs' tool isn't working properly")
		}
	}

	btrfsLoaded = true
	return nil
}

func (d *btrfs) createSubvolume(subvol string) error {
	parentDestPath := filepath.Dir(subvol)
	if !shared.PathExists(parentDestPath) {
		err := os.MkdirAll(parentDestPath, 0711)
		if err != nil {
			return err
		}
	}

	_, err := shared.RunCommand(
		"btrfs",
		"subvolume",
		"create",
		subvol)
	if err != nil {
		logger.Errorf("Failed to create BTRFS subvolume \"%s\": %v", subvol, err)
		return err
	}

	return nil
}

func (d *btrfs) getSubvolume(path string) ([]string, error) {
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
		if d.isSubvolume(fpath) {
			result = append(result, strings.TrimPrefix(fpath, path))
		}

		return nil
	})

	return result, nil
}

func (d *btrfs) getSubvolumeSnapshots(path string) ([]string, error) {
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

		// Check if a btrfs subvolume snapshot
		if d.isSubvolume(fpath) {
			result = append(result, strings.TrimPrefix(fpath, path))
		}

		return nil
	})

	return result, nil
}

// isSubvolume returns true if the given Path is a btrfs subvolume else
// false.
func (d *btrfs) isSubvolume(subvolPath string) bool {
	fs := unix.Stat_t{}
	err := unix.Lstat(subvolPath, &fs)
	if err != nil {
		return false
	}

	// Check if BTRFS_FIRST_FREE_OBJECTID
	if fs.Ino != 256 {
		return false
	}

	return true
}

func (d *btrfs) lookupFsUUID(fs string) (string, error) {
	output, err := shared.RunCommand(
		"btrfs",
		"filesystem",
		"show",
		"--raw",
		fs)
	if err != nil {
		return "", fmt.Errorf("failed to detect UUID")
	}

	outputString := output
	idx := strings.Index(outputString, "uuid: ")
	outputString = outputString[idx+6:]
	outputString = strings.TrimSpace(outputString)
	idx = strings.Index(outputString, "\t")
	outputString = outputString[:idx]
	outputString = strings.Trim(outputString, "\n")

	return outputString, nil
}

// deleteSubvolumes is the recursive variant on btrfsSubvolumeDelete,
// it first deletes subvolumes of the subvolume and then the
// subvolume itself.
func (d *btrfs) deleteSubvolumes(subvol string) error {
	// Delete subsubvols.
	subsubvols, err := d.getSubvolume(subvol)
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(subsubvols)))

	for _, subsubvol := range subsubvols {
		err := d.deleteSubvolume(path.Join(subvol, subsubvol))
		if err != nil {
			return err
		}
	}

	// Delete the subvol itself
	err = d.deleteSubvolume(subvol)
	if err != nil {
		return err
	}

	return nil
}

func (d *btrfs) deleteSubvolume(subvol string) error {
	// Attempt (but don't fail on) to delete any qgroup on the subvolume
	qgroup, err := d.getSubvolumeQGroup(subvol)
	if err == nil {
		shared.RunCommand(
			"btrfs",
			"qgroup",
			"destroy",
			qgroup,
			subvol)
	}

	// Attempt to make the subvolume writable
	shared.RunCommand("btrfs", "property", "set", subvol, "ro", "false")

	// Delete the subvolume itself
	_, err = shared.RunCommand(
		"btrfs",
		"subvolume",
		"delete",
		subvol)

	return err
}

func (d *btrfs) getSubvolumeQGroup(subvol string) (string, error) {
	output, err := shared.RunCommand(
		"btrfs",
		"qgroup",
		"show",
		"-e",
		"-f",
		subvol)

	if err != nil {
		return "", errBtrfsNoQuota
	}

	var qgroup string
	for _, line := range strings.Split(output, "\n") {
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
		return "", errBtrfsNoQGroup
	}

	return qgroup, nil
}

func (d *btrfs) isFilesystem(path string) bool {
	_, err := shared.RunCommand("btrfs", "filesystem", "show", path)
	if err != nil {
		return false
	}

	return true
}

func (d *btrfs) getSubvolumeQGroupUsage(subvol string) (int64, error) {
	output, err := shared.RunCommand(
		"btrfs",
		"qgroup",
		"show",
		"-e",
		"-f",
		subvol)

	if err != nil {
		return -1, fmt.Errorf("BTRFS quotas not supported. Try enabling them with \"btrfs quota enable\"")
	}

	for _, line := range strings.Split(output, "\n") {
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

func (d *btrfs) createSubvolumesSnapshot(source string, dest string, readonly bool, recursive bool, userns bool) error {
	// Now snapshot all subvolumes of the root.
	if recursive {
		// Get a list of subvolumes of the root
		subsubvols, err := d.getSubvolume(source)
		if err != nil {
			return err
		}
		sort.Sort(sort.StringSlice(subsubvols))

		if len(subsubvols) > 0 && readonly {
			// A root with subvolumes can never be readonly,
			// also don't make subvolumes readonly.
			readonly = false

			logger.Warnf("Subvolumes detected, ignoring ro flag")
		}

		// First snapshot the root
		err = d.createSubvolumeSnapshot(source, dest, readonly, userns)
		if err != nil {
			return err
		}

		for _, subsubvol := range subsubvols {
			// Clear the target for the subvol to use
			os.Remove(path.Join(dest, subsubvol))

			err := d.createSubvolumeSnapshot(path.Join(source, subsubvol), path.Join(dest, subsubvol), readonly, userns)
			if err != nil {
				return err
			}
		}
	} else {
		err := d.createSubvolumeSnapshot(source, dest, readonly, userns)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *btrfs) createSubvolumeSnapshot(source string, dest string, readonly bool, userns bool) error {
	var output string
	var err error
	if readonly && !userns {
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
		return fmt.Errorf(
			"subvolume snapshot failed, source=%s, dest=%s, output=%s",
			source,
			dest,
			output,
		)
	}

	return err
}

func (d *btrfs) btrfsMigrationSend(btrfsPath string, btrfsParent string, conn io.ReadWriteCloser, tracker *ioprogress.ProgressTracker) error {
	args := []string{"send"}

	if btrfsParent != "" {
		args = append(args, "-p", btrfsParent)
	}

	args = append(args, btrfsPath)

	cmd := exec.Command("btrfs", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stdoutPipe := stdout
	if tracker != nil {
		stdoutPipe = &ioprogress.ProgressReader{
			ReadCloser: stdout,
			Tracker:    tracker,
		}
	}

	chStdoutPipe := make(chan error, 1)

	go func() {
		_, err := io.Copy(conn, stdoutPipe)
		chStdoutPipe <- err
		conn.Close()
	}()

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		logger.Errorf("Problem reading btrfs send stderr: %s", err)
	}

	errs := []error{}
	chStdoutPipeErr := <-chStdoutPipe

	err = cmd.Wait()
	if err != nil {
		errs = append(errs, err)

		if chStdoutPipeErr != nil {
			errs = append(errs, chStdoutPipeErr)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("Btrfs send failed: %v (%s)", errs, string(output))
	}

	return nil
}

func (d *btrfs) btrfsMigrationRecv(snapName string, btrfsPath string, targetPath string, isSnapshot bool, conn io.ReadWriteCloser, writeWrapper func(io.WriteCloser) io.WriteCloser) error {
	args := []string{"receive", "-e"}

	if isSnapshot {
		args = append(args, targetPath)
	} else {
		//
		args = append(args, btrfsPath)
	}

	cmd := exec.Command("btrfs", args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	chCopyConn := make(chan error, 1)

	go func() {
		_, err = io.Copy(stdin, conn)
		stdin.Close()
		chCopyConn <- err
	}()

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		logger.Debugf("Problem reading btrfs receive stderr %s", err)
	}

	errs := []error{}
	chCopyConnErr := <-chCopyConn

	err = cmd.Wait()
	if err != nil {
		errs = append(errs, err)

		if chCopyConnErr != nil {
			errs = append(errs, chCopyConnErr)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("Problem with btrfs receive: (%v) %s", errs, string(output))
	}

	// If we received a snapshot, it is already at the correct location.
	if isSnapshot {
		return nil
	}

	receivedSnapshot := fmt.Sprintf("%s/.migration-send", btrfsPath)
	// handle older lxd versions
	if !shared.PathExists(receivedSnapshot) {
		receivedSnapshot = fmt.Sprintf("%s/.root", btrfsPath)
	}

	err = d.makeSubvolumeRW(receivedSnapshot)
	if err != nil {
		return err
	}

	err = os.Rename(receivedSnapshot, targetPath)
	if err != nil {
		return err
	}

	return nil
}

func (d *btrfs) makeSubvolumeRW(path string) error {
	_, err := shared.RunCommand("btrfs", "property", "set", "-ts", path, "ro", "false")
	return err
}

func (d *btrfs) createBackup(cur string, prev string, target string) error {
	args := []string{"send"}
	if prev != "" {
		args = append(args, "-p", prev)
	}
	args = append(args, cur)

	eater, err := os.OpenFile(target, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer eater.Close()

	btrfsSendCmd := exec.Command("btrfs", args...)
	btrfsSendCmd.Stdout = eater

	err = btrfsSendCmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func (d *btrfs) restoreBackupVolumeOptimized(vol Volume, snapshots []string, srcData io.ReadSeeker, op *operations.Operation) (func(vol Volume) error, func(), error) {
	revert := true
	revertSubvolumes := []string{}

	revertHook := func() {
		for _, subvol := range revertSubvolumes {
			d.deleteSubvolume(subvol)
		}
	}

	defer func() {
		if revert {
			revertHook()
		}
	}()

	unpackDir, err := ioutil.TempDir(GetVolumeMountPath(d.name, vol.volType, ""), vol.name)
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(unpackDir)

	err = os.Chmod(unpackDir, 0100)
	if err != nil {
		return nil, nil, err
	}

	// Find the compression algorithm used for backup source data.
	srcData.Seek(0, 0)
	tarArgs, _, _, err := shared.DetectCompressionFile(srcData)
	if err != nil {
		return nil, nil, err
	}

	// Prepare tar arguments
	args := append(tarArgs, []string{
		"-",
		"--strip-components=1",
		"-C", unpackDir, "backup",
	}...)

	// Extract instance.
	srcData.Seek(0, 0)
	err = shared.RunCommandWithFds(srcData, nil, "tar", args...)
	if err != nil {
		return nil, nil, err
	}

	snapshotsDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)

	if len(snapshots) > 0 {
		// Create snapshots directory
		err := os.MkdirAll(snapshotsDir, 0711)
		if err != nil {
			return nil, nil, err
		}
	}

	for _, snap := range snapshots {
		snapshotBackupFile := filepath.Join(unpackDir, "snapshots", fmt.Sprintf("%s.bin", snap))

		feeder, err := os.Open(snapshotBackupFile)
		if err != nil {
			return nil, nil, err
		}

		cmd := exec.Command("btrfs", "receive", "-e", snapshotsDir)
		cmd.Stdin = feeder

		output, err := cmd.CombinedOutput()
		feeder.Close()
		if err != nil {
			logger.Errorf("Failed to receive contents of btrfs backup: %s", string(output))
			return nil, nil, err
		}

		revertSubvolumes = append(revertSubvolumes, GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, snap)))
	}

	containerBackupFile := fmt.Sprintf("%s/container.bin", unpackDir)
	feeder, err := os.Open(containerBackupFile)
	if err != nil {
		return nil, nil, err
	}
	defer feeder.Close()

	cmd := exec.Command("btrfs", "receive", "-e", unpackDir)
	cmd.Stdin = feeder

	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Errorf("Failed to receive contents of btrfs backup: %s", string(output))
		return nil, nil, err
	}

	receivedSubvolume := filepath.Join(unpackDir, ".backup")
	defer d.deleteSubvolume(receivedSubvolume)

	err = d.createSubvolumeSnapshot(receivedSubvolume, vol.MountPath(), false, d.state.OS.RunningInUserNS)
	if err != nil {
		return nil, nil, err
	}

	revertSubvolumes = append(revertSubvolumes, vol.MountPath())
	revert = false

	return nil, revertHook, nil
}

func (d *btrfs) restoreBackupVolume(vol Volume, snapshots []string, srcData io.ReadSeeker, op *operations.Operation) (func(vol Volume) error, func(), error) {
	revert := true
	revertSubvolumes := []string{}

	revertHook := func() {
		for _, subvol := range revertSubvolumes {
			d.deleteSubvolume(subvol)
		}
	}

	defer func() {
		if revert {
			revertHook()
		}
	}()

	// Find the compression algorithm used for backup source data.
	srcData.Seek(0, 0)
	tarArgs, _, _, err := shared.DetectCompressionFile(srcData)
	if err != nil {
		return nil, nil, err
	}

	err = d.CreateVolume(vol, nil, nil)
	if err != nil {
		return nil, nil, err
	}

	if len(snapshots) > 0 {
		// Create new snapshots directory.
		snapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)

		err := os.MkdirAll(snapshotDir, 0711)
		if err != nil {
			return nil, nil, err
		}
	}

	for _, snap := range snapshots {
		snapshotBackupPath := fmt.Sprintf("backup/snapshots/%s", snap)

		// Prepare tar arguments
		args := append(tarArgs, []string{
			"-",
			"--recursive-unlink",
			"--xattrs-include=*",
			"--strip-components=3",
			"-C", vol.MountPath(), snapshotBackupPath,
		}...)

		// Extract snapshots
		srcData.Seek(0, 0)
		err = shared.RunCommandWithFds(srcData, nil, "tar", args...)
		if err != nil {
			return nil, nil, err
		}

		snapVol, err := vol.NewSnapshot(snap)
		if err != nil {
			return nil, nil, err
		}

		err = d.CreateVolumeSnapshot(snapVol, op)
		if err != nil {
			return nil, nil, err
		}
	}

	// Prepare tar arguments
	args := append(tarArgs, []string{
		"-",
		"--strip-components=2",
		"--xattrs-include=*",
		"-C", vol.MountPath(), "backup/container",
	}...)

	// Extract container
	srcData.Seek(0, 0)
	err = shared.RunCommandWithFds(srcData, nil, "tar", args...)
	if err != nil {
		return nil, nil, err
	}

	revert = false

	return nil, revertHook, nil
}

func (d *btrfs) getMountOptions() string {
	if d.config["btrfs.mount_options"] != "" {
		return d.config["btrfs.mount_options"]
	}

	return "user_subvol_rm_allowed"
}

func (d *btrfs) backupVolumeWithBtrfs(vol Volume, targetPath string, snapshots bool, op *operations.Operation) error {
	// Handle snapshots
	finalParent := ""

	if snapshots {
		snapshotsPath := fmt.Sprintf("%s/snapshots", targetPath)

		// Retrieve the snapshots
		volSnapshots, err := d.VolumeSnapshots(vol, op)
		if err != nil {
			return err
		}

		// Create the snapshot path
		if len(volSnapshots) > 0 {
			err = os.MkdirAll(snapshotsPath, 0711)
			if err != nil {
				return err
			}
		}

		for i, snap := range volSnapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snap)

			// Figure out previous and current subvolumes
			prev := ""
			if i > 0 {
				// /var/lib/lxd/storage-pools/<pool>/containers-snapshots/<container>/<snapshot>

				prev = GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, volSnapshots[i-1]))
			}

			cur := GetVolumeMountPath(d.name, vol.volType, fullSnapshotName)

			// Make a binary btrfs backup
			target := fmt.Sprintf("%s/%s.bin", snapshotsPath, snap)

			err := d.createBackup(cur, prev, target)
			if err != nil {
				return err
			}

			finalParent = cur
		}
	}

	// Make a temporary copy of the container
	sourceVolume := vol.MountPath()
	containersPath := GetVolumeMountPath(d.name, vol.volType, "")

	tmpContainerMntPoint, err := ioutil.TempDir(containersPath, vol.name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	err = os.Chmod(tmpContainerMntPoint, 0100)
	if err != nil {
		return err
	}

	targetVolume := fmt.Sprintf("%s/.backup", tmpContainerMntPoint)

	err = d.createSubvolumesSnapshot(sourceVolume, targetVolume, true, true, d.state.OS.RunningInUserNS)
	if err != nil {
		return err
	}
	defer d.deleteSubvolumes(targetVolume)

	// Dump the container to a file
	fsDump := fmt.Sprintf("%s/container.bin", targetPath)

	err = d.createBackup(targetVolume, finalParent, fsDump)
	if err != nil {
		return err
	}

	return nil
}

func (d *btrfs) backupVolumeWithRsync(vol Volume, targetPath string, snapshots bool, op *operations.Operation) error {
	bwlimit := d.config["rsync.bwlimit"]

	// Handle snapshots
	if snapshots {
		snapshotsPath := fmt.Sprintf("%s/snapshots", targetPath)

		// Retrieve the snapshots
		volSnapshots, err := d.VolumeSnapshots(vol, op)
		if err != nil {
			return err
		}

		// Create the snapshot path
		if len(volSnapshots) > 0 {
			err = os.MkdirAll(snapshotsPath, 0711)
			if err != nil {
				return err
			}
		}

		for _, snap := range volSnapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snap)

			snapshotMntPoint := GetVolumeMountPath(d.name, vol.volType, fullSnapshotName)
			target := fmt.Sprintf("%s/%s", snapshotsPath, snap)

			// Copy the snapshot
			_, err = rsync.LocalCopy(snapshotMntPoint, target, bwlimit, false)
			if err != nil {
				return err
			}
		}
	}

	// Make a temporary copy of the instance
	sourceVolume := vol.MountPath()
	containersPath := GetVolumeMountPath(d.name, vol.volType, "")

	tmpContainerMntPoint, err := ioutil.TempDir(containersPath, vol.name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	err = os.Chmod(tmpContainerMntPoint, 0100)
	if err != nil {
		return err
	}

	targetVolume := fmt.Sprintf("%s/.backup", tmpContainerMntPoint)

	err = d.createSubvolumesSnapshot(sourceVolume, targetVolume, true, true, d.state.OS.RunningInUserNS)
	if err != nil {
		return err
	}
	defer d.deleteSubvolumes(targetVolume)

	// Copy the instance
	containerPath := fmt.Sprintf("%s/container", targetPath)

	_, err = rsync.LocalCopy(targetVolume, containerPath, bwlimit, false)
	if err != nil {
		return err
	}

	return nil
}

func (d *btrfs) createVolumeFromMigrationWithRsync(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	var err error

	if !d.isSubvolume(vol.MountPath()) {
		// Create the main volume
		err := d.CreateVolume(vol, preFiller, op)
		if err != nil {
			return err
		}
	}

	// Create slice of snapshots created if revert needed later.
	revertSnaps := []string{}
	defer func() {
		if revertSnaps == nil {
			return
		}

		// Remove any paths created if we are reverting.
		for _, snapName := range revertSnaps {
			snapVol, _ := vol.NewSnapshot(snapName)
			d.DeleteVolumeSnapshot(snapVol, op)
		}

		os.RemoveAll(vol.MountPath())
	}()

	// Ensure the volume is mounted.
	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		path := shared.AddSlash(mountPath)

		// Snapshots are sent first by the sender, so create these first.
		for _, snapName := range volTargetArgs.Snapshots {
			// Receive the snapshot
			var wrapper *ioprogress.ProgressTracker
			if volTargetArgs.TrackProgress {
				wrapper = migration.ProgressTracker(op, "fs_progress", snapName)
			}

			err = rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
			if err != nil {
				return err
			}

			// Don't create the snapshot if it already exists.
			if d.isSubvolume(GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, snapName))) {
				continue
			}

			// Create the snapshot itself.
			snapVol, err := vol.NewSnapshot(snapName)
			if err != nil {
				return err
			}
			err = d.CreateVolumeSnapshot(snapVol, op)
			if err != nil {
				return err
			}

			// Setup the revert.
			revertSnaps = append(revertSnaps, snapName)
		}

		// Receive the main volume from sender.
		var wrapper *ioprogress.ProgressTracker
		if volTargetArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
		}

		return rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
	}, op)
	if err != nil {
		return err
	}

	revertSnaps = nil
	return nil
}

func (d *btrfs) createVolumeFromMigrationWithBtrfs(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	snapshotsDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)

	err := os.MkdirAll(snapshotsDir, 0711)
	if err != nil {
		return err
	}

	for _, snap := range volTargetArgs.Snapshots {
		fullSnapshotName := GetSnapshotVolumeName(vol.name, snap)
		wrapper := migration.ProgressWriter(op, "fs_progress", fullSnapshotName)

		err = d.btrfsMigrationRecv(fullSnapshotName, "", snapshotsDir, true, conn, wrapper)
		if err != nil {
			return err
		}
	}

	// Get instances directory (e.g. /var/lib/lxd/storage-pools/btrfs/containers)
	instancesPath := GetVolumeMountPath(d.name, vol.volType, "")

	// Create a temporary directory which will act as the parent directory
	// of the received ro snapshot.
	tmpVolumesMountPoint, err := ioutil.TempDir(instancesPath, vol.name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpVolumesMountPoint)

	err = os.Chmod(tmpVolumesMountPoint, 0100)
	if err != nil {
		return err
	}

	wrapper := migration.ProgressWriter(op, "fs_progress", vol.name)

	err = d.btrfsMigrationRecv(vol.name, tmpVolumesMountPoint, vol.MountPath(), false, conn, wrapper)
	if err != nil {
		return err
	}

	return nil
}

func (d *btrfs) migrateVolumeWithRsync(vol Volume, conn io.ReadWriteCloser, volSrcArgs migration.VolumeSourceArgs, op *operations.Operation) error {
	bwlimit := d.config["rsync.bwlimit"]

	for _, snapName := range volSrcArgs.Snapshots {
		snapshot, err := vol.NewSnapshot(snapName)
		if err != nil {
			return err
		}

		// Send snapshot to recipient (ensure local snapshot volume is mounted if needed).
		err = snapshot.MountTask(func(mountPath string, op *operations.Operation) error {
			var wrapper *ioprogress.ProgressTracker
			if volSrcArgs.TrackProgress {
				wrapper = migration.ProgressTracker(op, "fs_progress", snapshot.name)
			}

			path := shared.AddSlash(mountPath)
			return rsync.Send(snapshot.name, path, conn, wrapper, volSrcArgs.MigrationType.Features, bwlimit, d.state.OS.ExecPath)
		}, op)
		if err != nil {
			return err
		}
	}

	// Send volume to recipient (ensure local volume is mounted if needed).
	return vol.MountTask(func(mountPath string, op *operations.Operation) error {
		var wrapper *ioprogress.ProgressTracker
		if volSrcArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
		}

		path := shared.AddSlash(mountPath)
		return rsync.Send(vol.name, path, conn, wrapper, volSrcArgs.MigrationType.Features, bwlimit, d.state.OS.ExecPath)
	}, op)
}

func (d *btrfs) migrateVolumeWithBtrfs(vol Volume, conn io.ReadWriteCloser, volSrcArgs migration.VolumeSourceArgs, op *operations.Operation) error {
	for i, snapName := range volSrcArgs.Snapshots {
		snapshot, _ := vol.NewSnapshot(snapName)

		prevSnapshotPath := ""

		if i > 0 {
			prevSnapshotPath = GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, volSrcArgs.Snapshots[i-1]))
		}

		// Send snapshot to recipient (ensure local snapshot volume is mounted if needed).
		err := snapshot.MountTask(func(mountPath string, op *operations.Operation) error {
			var wrapper *ioprogress.ProgressTracker

			if volSrcArgs.TrackProgress {
				wrapper = migration.ProgressTracker(op, "fs_progress", snapshot.name)
			}

			return d.btrfsMigrationSend(mountPath, prevSnapshotPath, conn, wrapper)
		}, op)
		if err != nil {
			return err
		}
	}

	// Get instances directory (e.g. /var/lib/lxd/storage-pools/btrfs/containers)
	instancesPath := GetVolumeMountPath(d.name, vol.volType, "")

	// Create a temporary directory which will act as the parent directory
	// of the ro snapshot.
	tmpVolumesMountPoint, err := ioutil.TempDir(instancesPath, vol.name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpVolumesMountPoint)

	err = os.Chmod(tmpVolumesMountPoint, 0100)
	if err != nil {
		return err
	}

	// Set the name of the ro snapshot.
	migrationSendSnapshot := filepath.Join(tmpVolumesMountPoint, ".migration-send")

	// Make ro snapshot of the subvolume as rw subvolumes cannot be sent.
	err = d.createSubvolumeSnapshot(vol.MountPath(), migrationSendSnapshot, true, d.state.OS.RunningInUserNS)
	if err != nil {
		return err
	}
	defer d.deleteSubvolumes(migrationSendSnapshot)

	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		var wrapper *ioprogress.ProgressTracker
		if volSrcArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
		}

		btrfsParent := ""
		if len(volSrcArgs.Snapshots) > 0 {
			btrfsParent = GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, volSrcArgs.Snapshots[len(volSrcArgs.Snapshots)-1]))
		}

		// Send the snapshot
		return d.btrfsMigrationSend(migrationSendSnapshot, btrfsParent, conn, wrapper)
	}, op)

	if err != nil {
		return err
	}

	return nil
}
