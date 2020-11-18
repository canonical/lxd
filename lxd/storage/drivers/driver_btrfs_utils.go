package drivers

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"

	"github.com/grant-he/lxd/lxd/backup"
	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/ioprogress"
	"github.com/grant-he/lxd/shared/logger"
)

// Errors
var errBtrfsNoQuota = fmt.Errorf("Quotas disabled on filesystem")
var errBtrfsNoQGroup = fmt.Errorf("Unable to find quota group")

func (d *btrfs) getMountOptions() string {
	// Allow overriding the default options.
	if d.config["btrfs.mount_options"] != "" {
		return d.config["btrfs.mount_options"]
	}

	return "user_subvol_rm_allowed"
}

func (d *btrfs) isSubvolume(path string) bool {
	// Stat the path.
	fs := unix.Stat_t{}
	err := unix.Lstat(path, &fs)
	if err != nil {
		return false
	}

	// Check if BTRFS_FIRST_FREE_OBJECTID is the inode number.
	if fs.Ino != 256 {
		return false
	}

	return true
}

func (d *btrfs) getSubvolumes(path string) ([]string, error) {
	result := []string{}

	// Make sure the path has a trailing slash.
	if !strings.HasSuffix(path, "/") {
		path = path + "/"
	}

	// Walk through the entire tree looking for subvolumes.
	err := filepath.Walk(path, func(fpath string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Ignore the base path.
		if strings.TrimRight(fpath, "/") == strings.TrimRight(path, "/") {
			return nil
		}

		// Subvolumes can only be directories.
		if !fi.IsDir() {
			return nil
		}

		// Check if a subvolume.
		if d.isSubvolume(fpath) {
			result = append(result, strings.TrimPrefix(fpath, path))
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// snapshotSubvolume creates a snapshot of the specified path at the dest supplied. If recursion is true and
// sub volumes are found below the path then they are created at the relative location in dest.
func (d *btrfs) snapshotSubvolume(path string, dest string, recursion bool) error {
	// Single subvolume deletion.
	snapshot := func(path string, dest string) error {
		_, err := shared.RunCommand("btrfs", "subvolume", "snapshot", path, dest)
		if err != nil {
			return err
		}

		return nil
	}

	// First snapshot the root.
	err := snapshot(path, dest)
	if err != nil {
		return err
	}

	// Now snapshot all subvolumes of the root.
	if recursion {
		// Get the subvolumes list.
		subSubVols, err := d.getSubvolumes(path)
		if err != nil {
			return err
		}
		sort.Sort(sort.StringSlice(subSubVols))

		for _, subSubVol := range subSubVols {
			subSubVolSnapPath := filepath.Join(dest, subSubVol)

			// Clear the target for the subvol to use.
			os.Remove(subSubVolSnapPath)

			err := snapshot(filepath.Join(path, subSubVol), subSubVolSnapPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (d *btrfs) deleteSubvolume(rootPath string, recursion bool) error {
	// Single subvolume deletion.
	destroy := func(path string) error {
		// Attempt (but don't fail on) to delete any qgroup on the subvolume.
		qgroup, _, err := d.getQGroup(path)
		if err == nil {
			shared.RunCommand("btrfs", "qgroup", "destroy", qgroup, path)
		}

		// Temporarily change ownership & mode to help with nesting.
		os.Chmod(path, 0700)
		os.Chown(path, 0, 0)

		// Delete the subvolume itself.
		_, err = shared.RunCommand("btrfs", "subvolume", "delete", path)

		return err
	}

	err := d.setSubvolumeReadonlyProperty(rootPath, false)
	if err != nil {
		return errors.Wrapf(err, "Failed setting subvolume writable %q", rootPath)
	}

	// Delete subsubvols.
	if recursion {
		// Get the subvolumes list.
		subSubVols, err := d.getSubvolumes(rootPath)
		if err != nil {
			return err
		}

		// Perform a first pass and ensure all sub volumes are writable.
		sort.Sort(sort.StringSlice(subSubVols))
		for _, subSubVol := range subSubVols {
			subSubVolPath := filepath.Join(rootPath, subSubVol)
			err = d.setSubvolumeReadonlyProperty(subSubVolPath, false)
			if err != nil {
				return errors.Wrapf(err, "Failed setting subvolume writable %q", subSubVolPath)
			}
		}

		// Perform a second pass to delete subvolumes.
		sort.Sort(sort.Reverse(sort.StringSlice(subSubVols)))
		for _, subSubVol := range subSubVols {
			subSubVolPath := filepath.Join(rootPath, subSubVol)
			err := destroy(subSubVolPath)
			if err != nil {
				return errors.Wrapf(err, "Failed deleting subvolume %q", subSubVolPath)
			}
		}
	}

	// Delete the root subvol itself.
	err = destroy(rootPath)
	if err != nil {
		return errors.Wrapf(err, "Failed deleting subvolume %q", rootPath)
	}

	return nil
}

func (d *btrfs) getQGroup(path string) (string, int64, error) {
	// Try to get the qgroup details.
	output, err := shared.RunCommand("btrfs", "qgroup", "show", "-e", "-f", "--raw", path)
	if err != nil {
		return "", -1, errBtrfsNoQuota
	}

	// Parse to extract the qgroup identifier.
	var qgroup string
	usage := int64(-1)
	for _, line := range strings.Split(output, "\n") {
		if line == "" || strings.HasPrefix(line, "qgroupid") || strings.HasPrefix(line, "---") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}

		qgroup = fields[0]
		val, err := strconv.ParseInt(fields[2], 10, 64)
		if err == nil {
			usage = val
		}

		break
	}

	if qgroup == "" {
		return "", -1, errBtrfsNoQGroup
	}

	return qgroup, usage, nil
}

func (d *btrfs) sendSubvolume(path string, parent string, conn io.ReadWriteCloser, tracker *ioprogress.ProgressTracker) error {
	// Assemble btrfs send command.
	args := []string{"send"}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	args = append(args, path)
	cmd := exec.Command("btrfs", args...)

	// Prepare stdout/stderr.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	// Setup progress tracker.
	stdoutPipe := stdout
	if tracker != nil {
		stdoutPipe = &ioprogress.ProgressReader{
			ReadCloser: stdout,
			Tracker:    tracker,
		}
	}

	// Forward any output on stdout.
	chStdoutPipe := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, stdoutPipe)
		chStdoutPipe <- err
		conn.Close()
		cmd.Process.Kill() // This closes stderr.
	}()

	// Run the command.
	err = cmd.Start()
	if err != nil {
		return err
	}

	// Read any error.
	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		logger.Errorf("Problem reading btrfs send stderr: %s", err)
	}

	// Handle errors.
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

// setSubvolumeReadonlyProperty sets the readonly property on the subvolume to true or false.
func (d *btrfs) setSubvolumeReadonlyProperty(path string, readonly bool) error {
	// Silently ignore requests to set subvolume readonly property if running in a user namespace as we won't
	// be able to change it if it is readonly already, and making it readonly will mean we cannot undo it.
	if d.state.OS.RunningInUserNS {
		return nil
	}

	_, err := shared.RunCommand("btrfs", "property", "set", "-ts", path, "ro", fmt.Sprintf("%t", readonly))
	return err
}

// BTRFSSubVolume is the structure used to store information about a subvolume.
// Note: This is used by both migration and backup subsystems so do not modify without considering both!
type BTRFSSubVolume struct {
	Path     string `json:"path" yaml:"path"`         // Path inside the volume where the subvolume belongs (so / is the top of the volume tree).
	Snapshot string `json:"snapshot" yaml:"snapshot"` // Snapshot name the subvolume belongs to.
	Readonly bool   `json:"readonly" yaml:"readonly"` // Is the sub volume read only or not.
}

// getSubvolumesMetaData retrieves subvolume meta data with paths relative to the root volume.
// The first item in the returned list is the root subvolume itself.
func (d *btrfs) getSubvolumesMetaData(vol Volume) ([]BTRFSSubVolume, error) {
	var subVols []BTRFSSubVolume

	snapName := ""
	if vol.IsSnapshot() {
		_, snapName, _ = shared.InstanceGetParentAndSnapshotName(vol.name)
	}

	// Add main root volume to subvolumes list first.
	subVols = append(subVols, BTRFSSubVolume{
		Snapshot: snapName,
		Path:     string(filepath.Separator),
		Readonly: BTRFSSubVolumeIsRo(vol.MountPath()),
	})

	// Find any subvolumes in volume.
	subVolPaths, err := d.getSubvolumes(vol.MountPath())
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.StringSlice(subVolPaths))

	// Add any subvolumes under the root subvolume with relative path to root.
	for _, subVolPath := range subVolPaths {
		subVols = append(subVols, BTRFSSubVolume{
			Snapshot: snapName,
			Path:     fmt.Sprintf("%s%s", string(filepath.Separator), subVolPath),
			Readonly: BTRFSSubVolumeIsRo(filepath.Join(vol.MountPath(), subVolPath)),
		})
	}

	return subVols, nil
}

// BTRFSMetaDataHeader is the meta data header about the volumes being sent/stored.
// Note: This is used by both migration and backup subsystems so do not modify without considering both!
type BTRFSMetaDataHeader struct {
	Subvolumes []BTRFSSubVolume `json:"subvolumes" yaml:"subvolumes"` // Sub volumes inside the volume (including the top level ones).
}

// restorationHeader scans the volume and any specified snapshots, returning a header containing subvolume metadata
// for use in restoring a volume and its snapshots onto another system. The metadata returned represents how the
// subvolumes should be restored, not necessarily how they are on disk now. Most of the time this is the same,
// however in circumstances where the volume being scanned is itself a snapshot, the returned metadata will
// not report the volume as readonly or as being a snapshot, as the expectation is that this volume will be
// restored on the target system as a normal volume and not a snapshot.
func (d *btrfs) restorationHeader(vol Volume, snapshots []string) (*BTRFSMetaDataHeader, error) {
	var migrationHeader BTRFSMetaDataHeader

	// Add snapshots to volumes list.
	for _, snapName := range snapshots {
		snapVol, _ := vol.NewSnapshot(snapName)

		// Add snapshot root volume to volumes list.
		subVols, err := d.getSubvolumesMetaData(snapVol)
		if err != nil {
			return nil, err
		}

		migrationHeader.Subvolumes = append(migrationHeader.Subvolumes, subVols...)
	}

	// Add main root volume to volumes list.
	subVols, err := d.getSubvolumesMetaData(vol)
	if err != nil {
		return nil, err
	}

	// If vol is a snapshot itself, we force the volume as writable (even if it isn't on disk) and remove the
	// snapshot name indicator as the expectation is that this volume is going to be restored on the target
	// system as a normal (non-snapshot) writable volume.
	if vol.IsSnapshot() {
		subVols[0].Readonly = false
		for i := range subVols {
			subVols[i].Snapshot = ""
		}
	}

	migrationHeader.Subvolumes = append(migrationHeader.Subvolumes, subVols...)
	return &migrationHeader, nil
}

// loadOptimizedBackupHeader extracts optimized backup header from a given ReadSeeker.
func (d *btrfs) loadOptimizedBackupHeader(r io.ReadSeeker) (*BTRFSMetaDataHeader, error) {
	header := BTRFSMetaDataHeader{}

	// Extract.
	tr, cancelFunc, err := backup.TarReader(r)
	if err != nil {
		return nil, err
	}
	defer cancelFunc()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive.
		}
		if err != nil {
			return nil, errors.Wrapf(err, "Error reading backup file for optimized backup header file")
		}

		if hdr.Name == "backup/optimized_header.yaml" {
			err = yaml.NewDecoder(tr).Decode(&header)
			if err != nil {
				return nil, errors.Wrapf(err, "Error parsing optimized backup header file")
			}

			cancelFunc()
			return &header, nil
		}
	}

	return nil, fmt.Errorf("Optimized backup header file not found")
}

// receiveSubVolume receives a subvolume from an io.Reader into the receivePath, then sets it writable and returns
// the path to the received subvolume.
func (d *btrfs) receiveSubVolume(r io.Reader, receivePath string) (string, error) {
	// Check target path is empty before receive.
	files, err := ioutil.ReadDir(receivePath)
	if err != nil {
		return "", errors.Wrapf(err, "Failed listing contents of %q", receivePath)
	}
	if len(files) > 0 {
		return "", fmt.Errorf("Target path is not empty %q", receivePath)
	}

	err = shared.RunCommandWithFds(r, nil, "btrfs", "receive", "-e", receivePath)
	if err != nil {
		return "", err
	}

	// Check contents of target path is expected after receive.
	files, err = ioutil.ReadDir(receivePath)
	if err != nil {
		return "", errors.Wrapf(err, "Failed listing contents of %q", receivePath)
	}

	if len(files) != 1 {
		return "", fmt.Errorf("Unpack target path contains %d files, expected 1 file after unpack", len(files))
	}

	subVolPath := filepath.Join(receivePath, files[0].Name())

	// Set writable to allow subvolume to be moved (or deleted if needed) later.
	err = d.setSubvolumeReadonlyProperty(subVolPath, false)
	if err != nil {
		return "", err
	}

	return subVolPath, nil
}
