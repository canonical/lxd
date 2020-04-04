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

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
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

func (d *btrfs) snapshotSubvolume(path string, dest string, readonly bool, recursion bool) error {
	// Single subvolume deletion.
	snapshot := func(path string, dest string) error {
		if readonly && !d.state.OS.RunningInUserNS {
			_, err := shared.RunCommand("btrfs", "subvolume", "snapshot", "-r", path, dest)
			if err != nil {
				return err
			}

			return nil
		}

		_, err := shared.RunCommand("btrfs", "subvolume", "snapshot", path, dest)
		if err != nil {
			return err
		}

		return nil
	}

	// Now snapshot all subvolumes of the root.
	if recursion {
		// Get the subvolumes list.
		subsubvols, err := d.getSubvolumes(path)
		if err != nil {
			return err
		}
		sort.Sort(sort.StringSlice(subsubvols))

		if len(subsubvols) > 0 && readonly {
			// Creating subvolumes requires the parent to be writable.
			readonly = false
		}

		// First snapshot the root.
		err = snapshot(path, dest)
		if err != nil {
			return err
		}

		for _, subsubvol := range subsubvols {
			// Clear the target for the subvol to use.
			os.Remove(filepath.Join(dest, subsubvol))

			err := snapshot(filepath.Join(path, subsubvol), filepath.Join(dest, subsubvol))
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Handle standalone volume.
	err := snapshot(path, dest)
	if err != nil {
		return err
	}

	return nil
}

func (d *btrfs) deleteSubvolume(path string, recursion bool) error {
	// Single subvolume deletion.
	destroy := func(path string) error {
		// Attempt (but don't fail on) to delete any qgroup on the subvolume.
		qgroup, _, err := d.getQGroup(path)
		if err == nil {
			shared.RunCommand("btrfs", "qgroup", "destroy", qgroup, path)
		}

		// Attempt to make the subvolume writable.
		shared.RunCommand("btrfs", "property", "set", path, "ro", "false")

		// Temporarily change ownership & mode to help with nesting.
		os.Chmod(path, 0700)
		os.Chown(path, 0, 0)

		// Delete the subvolume itself.
		_, err = shared.RunCommand("btrfs", "subvolume", "delete", path)

		return err
	}

	// Delete subsubvols.
	if recursion {
		// Get the subvolumes list.
		subsubvols, err := d.getSubvolumes(path)
		if err != nil {
			return err
		}
		sort.Sort(sort.Reverse(sort.StringSlice(subsubvols)))

		for _, subsubvol := range subsubvols {
			err := destroy(filepath.Join(path, subsubvol))
			if err != nil {
				return err
			}
		}
	}

	// Delete the subvol itself.
	err := destroy(path)
	if err != nil {
		return err
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

func (d *btrfs) receiveSubvolume(path string, targetPath string, conn io.ReadWriteCloser, writeWrapper func(io.WriteCloser) io.WriteCloser) error {
	// Assemble btrfs send command.
	cmd := exec.Command("btrfs", "receive", "-e", path)

	// Prepare stdin/stderr.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	// Forward input through stdin.
	chCopyConn := make(chan error, 1)
	go func() {
		_, err = io.Copy(stdin, conn)
		stdin.Close()
		chCopyConn <- err
	}()

	// Run the command.
	err = cmd.Start()
	if err != nil {
		return err
	}

	// Read any error.
	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		logger.Debugf("Problem reading btrfs receive stderr %s", err)
	}

	// Handle errors.
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

	// If we receive and target paths match, we're done.
	if path == targetPath {
		return nil
	}

	// Handle older LXD versions.
	receivedSnapshot := fmt.Sprintf("%s/.migration-send", path)
	if !shared.PathExists(receivedSnapshot) {
		receivedSnapshot = fmt.Sprintf("%s/.root", path)
	}

	// Mark the received subvolume writable.
	_, err = shared.RunCommand("btrfs", "property", "set", "-ts", receivedSnapshot, "ro", "false")
	if err != nil {
		return err
	}

	// And move it to the target path.
	err = os.Rename(receivedSnapshot, targetPath)
	if err != nil {
		return errors.Wrapf(err, "Failed to rename '%s' to '%s'", receivedSnapshot, targetPath)
	}

	return nil
}
