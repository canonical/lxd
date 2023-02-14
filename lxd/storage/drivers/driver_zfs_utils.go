package drivers

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pborman/uuid"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/units"
)

const (
	// zfsBlockVolSuffix suffix used for block content type volumes.
	zfsBlockVolSuffix = ".block"

	// zfsMinBlockSize is a minimum value for recordsize and volblocksize properties.
	zfsMinBlocksize = 512

	// zfsMinBlockSize is a maximum value for recordsize and volblocksize properties.
	zfsMaxBlocksize = 16 * 1024 * 1024

	// zfsMaxVolBlocksize is a maximum value for volblocksize property.
	zfsMaxVolBlocksize = 128 * 1024
)

func (d *zfs) dataset(vol Volume, deleted bool) string {
	name, snapName, _ := api.GetParentAndSnapshotName(vol.name)

	if vol.volType == VolumeTypeImage && d.isBlockBacked(vol) {
		name = fmt.Sprintf("%s_%s", name, vol.ConfigBlockFilesystem())
	}

	if (vol.volType == VolumeTypeVM || vol.volType == VolumeTypeImage) && vol.contentType == ContentTypeBlock {
		name = fmt.Sprintf("%s%s", name, zfsBlockVolSuffix)
	}

	if snapName != "" {
		if deleted {
			name = fmt.Sprintf("%s@deleted-%s", name, uuid.New())
		} else {
			name = fmt.Sprintf("%s@snapshot-%s", name, snapName)
		}
	} else if deleted {
		if vol.volType != VolumeTypeImage {
			name = uuid.New()
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
	size = d.roundVolumeBlockSizeBytes(size)

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

func (d *zfs) datasetExists(dataset string) (bool, error) {
	out, err := shared.RunCommand("zfs", "get", "-H", "-o", "name", "name", dataset)
	if err != nil {
		return false, nil
	}

	return strings.TrimSpace(out) == dataset, nil
}

func (d *zfs) deleteDatasetRecursive(dataset string) error {
	// Locate the origin snapshot (if any).
	origin, err := d.getDatasetProperty(dataset, "origin")
	if err != nil {
		return err
	}

	// Delete the dataset (and any snapshots left).
	_, err = shared.TryRunCommand("zfs", "destroy", "-r", dataset)
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

func (d *zfs) setBlocksizeFromConfig(vol Volume) error {
	size := vol.ExpandedConfig("zfs.blocksize")
	if size == "" {
		return nil
	}

	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	return d.setBlocksize(vol, sizeBytes)
}

func (d *zfs) setBlocksize(vol Volume, size int64) error {
	if vol.contentType != ContentTypeFS {
		return nil
	}

	err := d.setDatasetProperties(d.dataset(vol, false), fmt.Sprintf("recordsize=%d", size))
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
		out, err := os.ReadFile("/sys/module/zfs/version")
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
		_ = conn.Close()
		_ = stderr.Close()
	}()

	// Run the command.
	err = cmd.Start()
	if err != nil {
		return err
	}

	// Read any error.
	output, _ := io.ReadAll(stderr)

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
	if vol.ContentType() == ContentTypeBlock || d.isBlockBacked(vol) {
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
		_ = stdin.Close()
		chCopyConn <- err
	}()

	// Run the command.
	err = cmd.Start()
	if err != nil {
		return err
	}

	// Read any error.
	output, _ := io.ReadAll(stderr)

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

// ValidateZfsBlocksize validates blocksize property value on the pool.
func ValidateZfsBlocksize(value string) error {
	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(value)
	if err != nil {
		return err
	}

	if sizeBytes < zfsMinBlocksize || sizeBytes > zfsMaxBlocksize || (sizeBytes&(sizeBytes-1)) != 0 {
		return fmt.Errorf("Value should be between 512 and 16MiB, and be power of 2")
	}

	return nil
}

// ZFSDataset is the structure used to store information about a dataset.
type ZFSDataset struct {
	Name string `json:"name" yaml:"name"`
	GUID string `json:"guid" yaml:"guid"`
}

// ZFSMetaDataHeader is the meta data header about the datasets being sent/stored.
type ZFSMetaDataHeader struct {
	SnapshotDatasets []ZFSDataset `json:"snapshot_datasets" yaml:"snapshot_datasets"`
}

func (d *zfs) datasetHeader(vol Volume, snapshots []string) (*ZFSMetaDataHeader, error) {
	migrationHeader := ZFSMetaDataHeader{
		SnapshotDatasets: make([]ZFSDataset, len(snapshots)),
	}

	for i, snapName := range snapshots {
		snapVol, _ := vol.NewSnapshot(snapName)

		guid, err := d.getDatasetProperty(d.dataset(snapVol, false), "guid")
		if err != nil {
			return nil, err
		}

		migrationHeader.SnapshotDatasets[i].Name = snapName
		migrationHeader.SnapshotDatasets[i].GUID = guid
	}

	return &migrationHeader, nil
}

func (d *zfs) randomVolumeName(vol Volume) string {
	return fmt.Sprintf("%s_%s", vol.name, uuid.New())
}
