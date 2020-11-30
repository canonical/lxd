package drivers

import (
	"fmt"
	"io"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pborman/uuid"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/ioprogress"
)

func (d *zfs) dataset(vol Volume, deleted bool) string {
	name, snapName, _ := shared.InstanceGetParentAndSnapshotName(vol.name)
	if (vol.volType == VolumeTypeVM || vol.volType == VolumeTypeImage) && vol.contentType == ContentTypeBlock {
		name = fmt.Sprintf("%s.block", name)
	}

	if snapName != "" {
		if deleted {
			name = fmt.Sprintf("%s@deleted-%s", name, uuid.NewRandom().String())
		} else {
			name = fmt.Sprintf("%s@snapshot-%s", name, snapName)
		}
	} else if deleted {
		if vol.volType != VolumeTypeImage {
			name = uuid.NewRandom().String()
		}

		return filepath.Join(d.config["zfs.pool_name"], "deleted", string(vol.volType), name)
	}

	return filepath.Join(d.config["zfs.pool_name"], string(vol.volType), name)
}

func (d *zfs) createDataset(dataset string, options ...string) error {
	args := []string{"create"}
	for _, option := range options {
		args = append(args, "-o")
		args = append(args, option)
	}
	args = append(args, dataset)

	_, err := shared.RunCommand("zfs", args...)
	if err != nil {
		return err
	}

	return nil
}

func (d *zfs) createVolume(dataset string, size int64, options ...string) error {
	size = (size / MinBlockBoundary) * MinBlockBoundary

	args := []string{"create", "-s", "-V", fmt.Sprintf("%d", size)}
	for _, option := range options {
		args = append(args, "-o")
		args = append(args, option)
	}
	args = append(args, dataset)

	_, err := shared.RunCommand("zfs", args...)
	if err != nil {
		return err
	}

	return nil
}

func (d *zfs) checkDataset(dataset string) bool {
	out, err := shared.RunCommand("zfs", "get", "-H", "-o", "name", "name", dataset)
	if err != nil {
		return false
	}

	return strings.TrimSpace(out) == dataset
}

func (d *zfs) deleteDatasetRecursive(dataset string) error {
	// Locate the origin snapshot (if any).
	origin, err := d.getDatasetProperty(dataset, "origin")
	if err != nil {
		return err
	}

	// Delete the dataset (and any snapshots left).
	_, err = shared.RunCommand("zfs", "destroy", "-r", dataset)
	if err != nil {
		return err
	}

	// Check if the origin can now be deleted.
	if origin != "" && origin != "-" {
		if strings.HasPrefix(origin, filepath.Join(d.config["zfs.pool_name"], "deleted")) {
			// Strip the snapshot name when dealing with a deleted volume.
			dataset = strings.SplitN(origin, "@", 2)[0]
		} else if strings.Contains(origin, "@deleted-") || strings.Contains(origin, "@copy-") {
			// Handle deleted snapshots.
			dataset = origin
		} else {
			// Origin is still active.
			dataset = ""
		}

		if dataset != "" {
			// Get all clones.
			clones, err := d.getClones(dataset)
			if err != nil {
				return err
			}

			if len(clones) == 0 {
				// Delete the origin.
				err = d.deleteDatasetRecursive(dataset)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (d *zfs) getClones(dataset string) ([]string, error) {
	out, err := shared.RunCommand("zfs", "get", "-H", "-p", "-r", "-o", "value", "clones", dataset)
	if err != nil {
		return nil, err
	}

	clones := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == dataset || line == "" || line == "-" {
			continue
		}

		line = strings.TrimPrefix(line, fmt.Sprintf("%s/", dataset))
		clones = append(clones, line)
	}

	return clones, nil
}

func (d *zfs) getDatasets(dataset string) ([]string, error) {
	out, err := shared.RunCommand("zfs", "get", "-H", "-r", "-o", "name", "name", dataset)
	if err != nil {
		return nil, err
	}

	children := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == dataset || line == "" {
			continue
		}

		line = strings.TrimPrefix(line, dataset)
		line = strings.TrimPrefix(line, "/")
		children = append(children, line)
	}

	return children, nil
}

func (d *zfs) setDatasetProperties(dataset string, options ...string) error {
	if len(zfsVersion) >= 3 && zfsVersion[0:3] == "0.6" {
		// Slow path for ZFS 0.6
		for _, option := range options {
			_, err := shared.RunCommand("zfs", "set", option, dataset)
			if err != nil {
				return err
			}
		}

		return nil
	}

	args := []string{"set"}
	args = append(args, options...)
	args = append(args, dataset)

	_, err := shared.RunCommand("zfs", args...)
	if err != nil {
		return err
	}

	return nil
}

func (d *zfs) getDatasetProperty(dataset string, key string) (string, error) {
	output, err := shared.RunCommand("zfs", "get", "-H", "-p", "-o", "value", key, dataset)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(output), nil
}

// version returns the ZFS version based on package or kernel module version.
func (d *zfs) version() (string, error) {
	// This function is only really ever relevant on Ubuntu as the only
	// distro that ships out of sync tools and kernel modules
	out, err := shared.RunCommand("dpkg-query", "--showformat=${Version}", "--show", "zfsutils-linux")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	// Loaded kernel module version
	if shared.PathExists("/sys/module/zfs/version") {
		out, err := ioutil.ReadFile("/sys/module/zfs/version")
		if err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}

	// Module information version
	out, err = shared.RunCommand("modinfo", "-F", "version", "zfs")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	return "", fmt.Errorf("Could not determine ZFS module version")
}

// initialDatasets returns the list of all expected datasets.
func (d *zfs) initialDatasets() []string {
	entries := []string{"deleted"}

	// Iterate over the listed supported volume types.
	for _, volType := range d.Info().VolumeTypes {
		entries = append(entries, BaseDirectories[volType][0])
		entries = append(entries, filepath.Join("deleted", BaseDirectories[volType][0]))
	}

	return entries
}

func (d *zfs) sendDataset(dataset string, parent string, volSrcArgs *migration.VolumeSourceArgs, conn io.ReadWriteCloser, tracker *ioprogress.ProgressTracker) error {
	// Assemble zfs send command.
	args := []string{"send"}
	if shared.StringInSlice("compress", volSrcArgs.MigrationType.Features) {
		args = append(args, "-c")
		args = append(args, "-L")
	}
	if parent != "" {
		args = append(args, "-i", parent)
	}
	args = append(args, dataset)
	cmd := exec.Command("zfs", args...)

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
	output, _ := ioutil.ReadAll(stderr)

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
		return fmt.Errorf("zfs send failed: %v (%s)", errs, string(output))
	}

	return nil
}

func (d *zfs) receiveDataset(vol Volume, conn io.ReadWriteCloser, writeWrapper func(io.WriteCloser) io.WriteCloser) error {
	// Assemble zfs receive command.
	cmd := exec.Command("zfs", "receive", "-x", "mountpoint", "-F", "-u", d.dataset(vol, false))
	if vol.ContentType() == ContentTypeBlock {
		cmd = exec.Command("zfs", "receive", "-F", "-u", d.dataset(vol, false))
	}

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
	output, _ := ioutil.ReadAll(stderr)

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
		return fmt.Errorf("Problem with zfs receive: (%v) %s", errs, string(output))
	}

	return nil
}
