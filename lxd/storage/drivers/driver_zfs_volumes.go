package drivers

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/units"
)

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied
// filler function.
func (d *zfs) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	// Revert handling
	revert := revert.New()
	defer revert.Fail()

	if vol.contentType == ContentTypeFS {
		// Create mountpoint.
		err := vol.EnsureMountPath()
		if err != nil {
			return err
		}

		revert.Add(func() { os.Remove(vol.MountPath()) })
	}

	// Look for previously deleted images.
	if vol.volType == VolumeTypeImage && d.checkDataset(d.dataset(vol, true)) {
		// Restore the image.
		_, err := shared.RunCommand("/proc/self/exe", "forkzfs", "--", "rename", d.dataset(vol, true), d.dataset(vol, false))
		if err != nil {
			return err
		}

		return nil
	}

	// After this point we'll have a volume, so setup revert.
	revert.Add(func() { d.DeleteVolume(vol, op) })

	if vol.contentType == ContentTypeFS {
		// Create the filesystem dataset.
		err := d.createDataset(d.dataset(vol, false), fmt.Sprintf("mountpoint=%s", vol.MountPath()), "canmount=noauto")
		if err != nil {
			return err
		}

		// Apply the size limit.
		size := vol.ExpandedConfig("size")
		if size != "" {
			err := d.SetVolumeQuota(vol, size, op)
			if err != nil {
				return err
			}
		}
	} else {
		// Convert the size.
		size := vol.ExpandedConfig("size")
		if size == "" {
			size = defaultBlockSize
		}

		sizeBytes, err := units.ParseByteSizeString(size)
		if err != nil {
			return err
		}

		// Use volmode=none so volume is invisible until mounted.
		opts := []string{"volmode=none"}

		loopPath := loopFilePath(d.name)
		if d.config["source"] == loopPath {
			// Create the volume dataset with sync disabled (to avoid kernel lockups when using a disk based pool).
			opts = append(opts, "sync=disabled")
		}

		// Create the volume dataset.
		err = d.createVolume(d.dataset(vol, false), sizeBytes, opts...)
		if err != nil {
			return err
		}
	}

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.CreateVolume(fsVol, nil, op)
		if err != nil {
			return err
		}

		revert.Add(func() { d.DeleteVolume(fsVol, op) })
	}

	err := vol.MountTask(func(mountPath string, op *operations.Operation) error {
		// Run the volume filler function if supplied.
		if filler != nil && filler.Fill != nil {
			if vol.contentType == ContentTypeFS {
				// Run the filler.
				err := filler.Fill(mountPath, "")
				if err != nil {
					return err
				}
			} else {
				// Get the device path.
				devPath, err := d.GetVolumeDiskPath(vol)
				if err != nil {
					return err
				}

				// Run the filler.
				err = filler.Fill(mountPath, devPath)
				if err != nil {
					return err
				}
			}
		}

		if vol.contentType == ContentTypeFS {
			// Run EnsureMountPath again after mounting and filling to ensure the mount directory has
			// the correct permissions set.
			err := vol.EnsureMountPath()
			if err != nil {
				return err
			}
		}

		return nil
	}, op)
	if err != nil {
		return err
	}

	// Setup snapshot and unset mountpoint on image.
	if vol.volType == VolumeTypeImage {
		// Create snapshot of the main dataset.
		_, err := shared.RunCommand("zfs", "snapshot", fmt.Sprintf("%s@readonly", d.dataset(vol, false)))
		if err != nil {
			return err
		}

		if vol.contentType == ContentTypeBlock {
			// Re-create the readonly snapshot, post-filling.
			fsVol := NewVolume(d, d.name, vol.volType, ContentTypeFS, vol.name, vol.config, vol.poolConfig)

			_, err := shared.RunCommand("zfs", "destroy", fmt.Sprintf("%s@readonly", d.dataset(fsVol, false)))
			if err != nil {
				return err
			}

			_, err = shared.RunCommand("zfs", "snapshot", fmt.Sprintf("%s@readonly", d.dataset(fsVol, false)))
			if err != nil {
				return err
			}
		}
	}

	// All done.
	revert.Success()

	return nil
}

// CreateVolumeFromBackup re-creates a volume from its exported state.
func (d *zfs) CreateVolumeFromBackup(vol Volume, snapshots []string, srcData io.ReadSeeker, optimized bool, op *operations.Operation) (func(vol Volume) error, func(), error) {
	// Handle the non-optimized tarballs through the generic unpacker.
	if !optimized {
		return genericBackupUnpack(d, vol, snapshots, srcData, op)
	}

	revert := revert.New()
	defer revert.Fail()

	// Define a revert function that will be used both to revert if an error occurs inside this
	// function but also return it for use from the calling functions if no error internally.
	revertHook := func() {
		for _, snapName := range snapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, d.name, vol.volType, vol.contentType, fullSnapshotName, vol.config, vol.poolConfig)
			d.DeleteVolumeSnapshot(snapVol, op)
		}

		// And lastly the main volume.
		d.DeleteVolume(vol, op)
	}

	// Only execute the revert function if we have had an error internally.
	revert.Add(revertHook)

	// Create a temporary directory to unpack the backup into.
	unpackDir, err := ioutil.TempDir(GetVolumeMountPath(d.name, vol.volType, ""), vol.name)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Failed to create temporary directory under '%s'", GetVolumeMountPath(d.name, vol.volType, ""))
	}
	defer os.RemoveAll(unpackDir)

	err = os.Chmod(unpackDir, 0100)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Failed to chmod '%s'", unpackDir)
	}

	// Find the compression algorithm used for backup source data.
	srcData.Seek(0, 0)
	tarArgs, _, _, err := shared.DetectCompressionFile(srcData)
	if err != nil {
		return nil, nil, err
	}

	// Prepare tar arguments.
	args := append(tarArgs, []string{
		"-",
		"--strip-components=1",
		"-C", unpackDir, "backup",
	}...)

	// Unpack the backup.
	srcData.Seek(0, 0)
	err = shared.RunCommandWithFds(srcData, nil, "tar", args...)
	if err != nil {
		return nil, nil, err
	}
	if len(snapshots) > 0 {
		// Create new snapshots directory.
		err := createParentSnapshotDirIfMissing(d.name, vol.volType, vol.name)
		if err != nil {
			return nil, nil, err
		}
	}

	// Restore backups from oldest to newest.
	for _, snapName := range snapshots {
		// Open the backup.
		feeder, err := os.Open(filepath.Join(unpackDir, "snapshots", fmt.Sprintf("%s.bin", snapName)))
		if err != nil {
			return nil, nil, errors.Wrapf(err, "Failed to open '%s'", filepath.Join(unpackDir, "snapshots", fmt.Sprintf("%s.bin", snapName)))
		}
		defer feeder.Close()

		// Extract the backup.
		dstSnapshot := fmt.Sprintf("%s@snapshot-%s", d.dataset(vol, false), snapName)
		err = shared.RunCommandWithFds(feeder, nil, "zfs", "receive", "-F", dstSnapshot)
		if err != nil {
			return nil, nil, err
		}
	}

	// Open the backup.
	feeder, err := os.Open(filepath.Join(unpackDir, "container.bin"))
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Failed to open '%s'", filepath.Join(unpackDir, "container.bin"))
	}
	defer feeder.Close()

	// Extrack the backup.
	err = shared.RunCommandWithFds(feeder, nil, "zfs", "receive", "-F", d.dataset(vol, false))
	if err != nil {
		return nil, nil, err
	}

	// Strip internal snapshots.
	entries, err := d.getDatasets(d.dataset(vol, false))
	if err != nil {
		return nil, nil, err
	}

	// Filter only the snapshots.
	for _, entry := range entries {
		if strings.HasPrefix(entry, "@snapshot-") {
			continue
		}

		if strings.HasPrefix(entry, "@") {
			_, err := shared.RunCommand("zfs", "destroy", fmt.Sprintf("%s%s", d.dataset(vol, false), entry))
			if err != nil {
				return nil, nil, err
			}
		}
	}

	// Re-apply the base mount options.
	if vol.contentType == ContentTypeFS {
		err := d.setDatasetProperties(d.dataset(vol, false), fmt.Sprintf("mountpoint=%s", vol.MountPath()), "canmount=noauto")
		if err != nil {
			return nil, nil, err
		}
	}

	// The import requires a mounted volume, so mount it and have it unmounted as a post hook.
	_, err = d.MountVolume(vol, op)
	if err != nil {
		return nil, nil, err
	}

	postHook := func(vol Volume) error {
		_, err := d.UnmountVolume(vol, op)
		return err
	}

	revert.Success()
	return postHook, revertHook, nil
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *zfs) CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, op *operations.Operation) error {
	// Revert handling
	revert := revert.New()
	defer revert.Fail()

	if vol.contentType == ContentTypeFS {
		// Create mountpoint.
		err := vol.EnsureMountPath()
		if err != nil {
			return err
		}

		revert.Add(func() { os.Remove(vol.MountPath()) })
	}

	// For VMs, also copy the filesystem dataset.
	if vol.volType == VolumeTypeVM && vol.contentType == ContentTypeBlock {
		fsVol := NewVolume(d, d.name, vol.volType, ContentTypeFS, vol.name, vol.config, vol.poolConfig)
		fsSrcVol := NewVolume(d, d.name, srcVol.volType, ContentTypeFS, srcVol.name, srcVol.config, srcVol.poolConfig)

		err := d.CreateVolumeFromCopy(fsVol, fsSrcVol, copySnapshots, op)
		if err != nil {
			return err
		}

		// Delete on revert.
		revert.Add(func() {
			d.DeleteVolume(fsVol, op)
		})
	}

	// Retrieve snapshots on the source.
	snapshots := []string{}
	if !srcVol.IsSnapshot() && copySnapshots {
		var err error
		snapshots, err = d.VolumeSnapshots(srcVol, op)
		if err != nil {
			return err
		}
	}

	var srcSnapshot string
	if srcVol.volType == VolumeTypeImage {
		srcSnapshot = fmt.Sprintf("%s@readonly", d.dataset(srcVol, false))
	} else if srcVol.IsSnapshot() {
		srcSnapshot = d.dataset(srcVol, false)
	} else {
		// Create a new snapshot for copy.
		srcSnapshot = fmt.Sprintf("%s@copy-%s", d.dataset(srcVol, false), uuid.NewRandom().String())

		_, err := shared.RunCommand("zfs", "snapshot", srcSnapshot)
		if err != nil {
			return err
		}

		// If using "zfs.clone_copy" delete the snapshot at the end.
		if (d.config["zfs.clone_copy"] != "" && !shared.IsTrue(d.config["zfs.clone_copy"])) || len(snapshots) > 0 {
			// Delete the snapshot at the end.
			defer shared.RunCommand("zfs", "destroy", srcSnapshot)
		} else {
			// Delete the snapshot on revert.
			revert.Add(func() {
				shared.RunCommand("zfs", "destroy", srcSnapshot)
			})
		}
	}

	// Handle zfs.clone_copy
	if (d.config["zfs.clone_copy"] != "" && !shared.IsTrue(d.config["zfs.clone_copy"])) || len(snapshots) > 0 {
		snapName := strings.SplitN(srcSnapshot, "@", 2)[1]

		// Send/receive the snapshot.
		var sender *exec.Cmd
		receiver := exec.Command("zfs", "receive", d.dataset(vol, false))

		// Handle transferring snapshots.
		if len(snapshots) > 0 {
			sender = exec.Command("zfs", "send", "-R", srcSnapshot)
		} else {
			sender = exec.Command("zfs", "send", srcSnapshot)
		}

		// Configure the pipes.
		receiver.Stdin, _ = sender.StdoutPipe()
		receiver.Stdout = os.Stdout
		receiver.Stderr = os.Stderr

		// Run the transfer.
		err := receiver.Start()
		if err != nil {
			return err
		}

		err = sender.Run()
		if err != nil {
			return err
		}

		err = receiver.Wait()
		if err != nil {
			return err
		}

		// Delete the snapshot.
		_, err = shared.RunCommand("zfs", "destroy", fmt.Sprintf("%s@%s", d.dataset(vol, false), snapName))
		if err != nil {
			return err
		}

		// Cleanup unexpected snapshots.
		if len(snapshots) > 0 {
			children, err := d.getDatasets(d.dataset(vol, false))
			if err != nil {
				return err
			}

			for _, entry := range children {
				// Check if expected snapshot.
				if strings.HasPrefix(entry, "@snapshot-") {
					name := strings.TrimPrefix(entry, "@snapshot-")
					if shared.StringInSlice(name, snapshots) {
						continue
					}
				}

				// Delete the rest.
				_, err := shared.RunCommand("zfs", "destroy", fmt.Sprintf("%s%s", d.dataset(vol, false), entry))
				if err != nil {
					return err
				}
			}
		}
	} else {
		args := []string{
			"clone",
			srcSnapshot,
			d.dataset(vol, false),
		}

		if vol.contentType == ContentTypeBlock {
			// Use volmode=none so volume is invisible until mounted.
			args = append(args, "-o", "volmode=none")
		}

		// Clone the snapshot.
		_, err := shared.RunCommand("zfs", args...)
		if err != nil {
			return err
		}
	}

	// Apply the properties.
	if vol.contentType == ContentTypeFS {
		err := d.setDatasetProperties(d.dataset(vol, false), fmt.Sprintf("mountpoint=%s", vol.MountPath()), "canmount=noauto")
		if err != nil {
			return err
		}
	}

	// All done.
	revert.Success()

	return nil
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *zfs) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, filler *VolumeFiller, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS {
		return ErrNotSupported
	}

	// Handle simple rsync through generic.
	if volTargetArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC {
		return genericCreateVolumeFromMigration(d, nil, vol, conn, volTargetArgs, filler, op)
	} else if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_ZFS {
		return ErrNotSupported
	}

	// Handle zfs send/receive migration.
	if len(volTargetArgs.Snapshots) > 0 {
		// Create the parent directory.
		err := createParentSnapshotDirIfMissing(d.name, vol.volType, vol.name)
		if err != nil {
			return err
		}

		// Transfer the snapshots.
		for _, snapName := range volTargetArgs.Snapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snapName)
			wrapper := migration.ProgressWriter(op, "fs_progress", fullSnapshotName)

			err = d.receiveDataset(d.dataset(vol, false), conn, wrapper)
			if err != nil {
				return err
			}
		}
	}

	// Transfer the main volume.
	wrapper := migration.ProgressWriter(op, "fs_progress", vol.name)
	err := d.receiveDataset(d.dataset(vol, false), conn, wrapper)
	if err != nil {
		return err
	}

	// Strip internal snapshots.
	entries, err := d.getDatasets(d.dataset(vol, false))
	if err != nil {
		return err
	}

	// Filter only the snapshots.
	for _, entry := range entries {
		if strings.HasPrefix(entry, "@snapshot-") {
			continue
		}

		if strings.HasPrefix(entry, "@") {
			_, err := shared.RunCommand("zfs", "destroy", fmt.Sprintf("%s%s", d.dataset(vol, false), entry))
			if err != nil {
				return err
			}
		}
	}

	if vol.contentType == ContentTypeFS {
		// Create mountpoint.
		err := vol.EnsureMountPath()
		if err != nil {
			return err
		}

		// Re-apply the base mount options.
		err = d.setDatasetProperties(d.dataset(vol, false), fmt.Sprintf("mountpoint=%s", vol.MountPath()), "canmount=noauto")
		if err != nil {
			return err
		}
	}

	return nil
}

// RefreshVolume updates an existing volume to match the state of another.
func (d *zfs) RefreshVolume(vol Volume, srcVol Volume, srcSnapshots []Volume, op *operations.Operation) error {
	return genericCopyVolume(d, nil, vol, srcVol, srcSnapshots, true, op)
}

// DeleteVolume deletes a volume of the storage device. If any snapshots of the volume remain then
// this function will return an error.
func (d *zfs) DeleteVolume(vol Volume, op *operations.Operation) error {
	// Check that we have a dataset to delete.
	if d.checkDataset(d.dataset(vol, false)) {
		// Handle clones.
		clones, err := d.getClones(d.dataset(vol, false))
		if err != nil {
			return err
		}

		if len(clones) > 0 {
			// Move to the deleted path.
			_, err := shared.RunCommand("/proc/self/exe", "forkzfs", "--", "rename", d.dataset(vol, false), d.dataset(vol, true))
			if err != nil {
				return err
			}
		} else {
			// Locate the origin snapshot (if any).
			origin, err := d.getDatasetProperty(d.dataset(vol, false), "origin")
			if err != nil {
				return err
			}

			// Delete the dataset (and any snapshots left).
			_, err = shared.RunCommand("zfs", "destroy", "-r", d.dataset(vol, false))
			if err != nil {
				return err
			}

			// Check if the origin can now be deleted.
			if origin != "" && origin != "-" {
				dataset := ""
				if strings.HasPrefix(origin, filepath.Join(d.config["zfs.pool_name"], "deleted")) {
					// Strip the snapshot name when dealing with a deleted volume.
					dataset = strings.SplitN(origin, "@", 2)[0]
				} else if strings.Contains(origin, "@deleted-") || strings.Contains(origin, "@copy-") {
					// Handle deleted snapshots.
					dataset = origin
				}

				if dataset != "" {
					// Get all clones.
					clones, err := d.getClones(dataset)
					if err != nil {
						return err
					}

					if len(clones) == 0 {
						// Delete the origin.
						_, err := shared.RunCommand("zfs", "destroy", "-r", dataset)
						if err != nil {
							return err
						}
					}
				}
			}
		}
	}

	if vol.contentType == ContentTypeFS {
		// Delete the mountpoint if present.
		err := os.Remove(vol.MountPath())
		if err != nil && !os.IsNotExist(err) {
			return errors.Wrapf(err, "Failed to remove '%s'", vol.MountPath())
		}

		// Delete the snapshot storage.
		err = os.RemoveAll(GetVolumeSnapshotDir(d.name, vol.volType, vol.name))
		if err != nil && !os.IsNotExist(err) {
			return errors.Wrapf(err, "Failed to remove '%s'", GetVolumeSnapshotDir(d.name, vol.volType, vol.name))
		}
	}

	// For VMs, also delete the filesystem dataset.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.DeleteVolume(fsVol, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *zfs) HasVolume(vol Volume) bool {
	// Check if the dataset exists.
	return d.checkDataset(d.dataset(vol, false))
}

// ValidateVolume validates the supplied volume config.
func (d *zfs) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	rules := map[string]func(value string) error{
		"zfs.remove_snapshots": shared.IsBool,
		"zfs.use_refquota":     shared.IsBool,
	}

	return d.validateVolume(vol, rules, removeUnknownKeys)
}

// UpdateVolume applies config changes to the volume.
func (d *zfs) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	for k, v := range changedConfig {
		if k == "size" {
			return d.SetVolumeQuota(vol, v, nil)
		}

		if k == "zfs.use_refquota" {
			// Get current value.
			cur := vol.ExpandedConfig("zfs.use_refquota")

			// Get current size.
			size := changedConfig["size"]
			if size == "" {
				size = vol.ExpandedConfig("size")
			}

			// Skip if no current quota.
			if size == "" {
				continue
			}

			// Skip if no change in effective value.
			if shared.IsTrue(v) == shared.IsTrue(cur) {
				continue
			}

			// Set new quota by temporarily modifying the volume config.
			vol.config["zfs.use_refquota"] = v
			err := d.SetVolumeQuota(vol, size, nil)
			vol.config["zfs.use_refquota"] = cur
			if err != nil {
				return err
			}

			// Unset old quota.
			err = d.SetVolumeQuota(vol, "", nil)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// GetVolumeUsage returns the disk space used by the volume.
func (d *zfs) GetVolumeUsage(vol Volume) (int64, error) {
	// Determine what key to use.
	key := "used"
	if shared.IsTrue(vol.ExpandedConfig("zfs.use_refquota")) {
		key = "referenced"
	}

	// Shortcut for refquota filesystems.
	if key == "referenced" && vol.contentType == ContentTypeFS && shared.IsMountPoint(vol.MountPath()) {
		var stat unix.Statfs_t
		err := unix.Statfs(vol.MountPath(), &stat)
		if err != nil {
			return -1, err
		}

		return int64(stat.Blocks-stat.Bfree) * int64(stat.Bsize), nil
	}

	// Get the current value.
	value, err := d.getDatasetProperty(d.dataset(vol, false), key)
	if err != nil {
		return -1, err
	}

	// Convert to int.
	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return -1, err
	}

	return valueInt, nil
}

func (d *zfs) SetVolumeQuota(vol Volume, size string, op *operations.Operation) error {
	if size == "" {
		size = "0"
	}

	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	// Handle volume datasets.
	if vol.contentType == ContentTypeBlock {
		sizeBytes = (sizeBytes / 8192) * 8192

		err := d.setDatasetProperties(d.dataset(vol, false), fmt.Sprintf("volsize=%d", sizeBytes))
		if err != nil {
			return err
		}

		return nil
	}

	// Handle filesystem datasets.
	key := "quota"
	if shared.IsTrue(vol.ExpandedConfig("zfs.use_refquota")) {
		key = "refquota"
	}

	value := fmt.Sprintf("%d", sizeBytes)
	if sizeBytes == 0 {
		value = "none"
	}

	err = d.setDatasetProperties(d.dataset(vol, false), fmt.Sprintf("%s=%s", key, value))
	if err != nil {
		return err
	}

	return nil
}

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *zfs) GetVolumeDiskPath(vol Volume) (string, error) {
	// Shortcut for udev.
	if tryExists(filepath.Join("/dev/zvol", d.dataset(vol, false))) {
		return filepath.Join("/dev/zvol", d.dataset(vol, false)), nil
	}

	// Locate zvol_id.
	zvolid := "/lib/udev/zvol_id"
	if !shared.PathExists(zvolid) {
		var err error

		zvolid, err = exec.LookPath("zvol_id")
		if err != nil {
			return "", err
		}
	}

	// List all the device nodes.
	entries, err := ioutil.ReadDir("/dev")
	if err != nil {
		return "", errors.Wrap(err, "Failed to read /dev")
	}

	for _, entry := range entries {
		entryName := entry.Name()

		// Ignore non-zvol devices.
		if !strings.HasPrefix(entryName, "zd") {
			continue
		}

		if strings.Contains(entryName, "p") {
			continue
		}

		// Resolve the dataset path.
		entryPath := filepath.Join("/dev", entryName)
		output, err := shared.RunCommand(zvolid, entryPath)
		if err != nil {
			continue
		}

		if strings.TrimSpace(output) == d.dataset(vol, false) {
			return entryPath, nil
		}
	}

	return "", fmt.Errorf("Could not locate a zvol for %s", d.dataset(vol, false))
}

// MountVolume simulates mounting a volume.
func (d *zfs) MountVolume(vol Volume, op *operations.Operation) (bool, error) {
	// For VMs, also mount the filesystem dataset.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		_, err := d.MountVolume(fsVol, op)
		if err != nil {
			return false, err
		}
	}

	// For block devices, we make them appear.
	if vol.contentType == ContentTypeBlock {
		current, err := d.getDatasetProperty(d.dataset(vol, false), "volmode")
		if err != nil {
			return false, err
		}

		// Check if already active.
		if current == "dev" {
			return false, nil
		}

		// Activate.
		err = d.setDatasetProperties(d.dataset(vol, false), "volmode=dev")
		if err != nil {
			return false, err
		}

		// Wait half a second to give udev a chance to kick in.
		time.Sleep(500 * time.Millisecond)

		return true, nil
	}

	// Check if not already mounted.
	if shared.IsMountPoint(vol.MountPath()) {
		return false, nil
	}

	// Mount the dataset.
	_, err := shared.RunCommand("zfs", "mount", d.dataset(vol, false))
	if err != nil {
		return false, err
	}

	return true, nil
}

// UnmountVolume simulates unmounting a volume.
func (d *zfs) UnmountVolume(vol Volume, op *operations.Operation) (bool, error) {
	// For VMs, also mount the filesystem dataset.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		_, err := d.UnmountVolume(fsVol, op)
		if err != nil {
			return false, err
		}
	}

	// For block devices, we make them disappear.
	if vol.contentType == ContentTypeBlock {
		err := d.setDatasetProperties(d.dataset(vol, false), "volmode=none")
		if err != nil {
			return false, err
		}

		return false, nil
	}

	// Check if still mounted.
	if !shared.IsMountPoint(vol.MountPath()) {
		return false, nil
	}

	// Mount the dataset.
	_, err := shared.RunCommand("zfs", "unmount", d.dataset(vol, false))
	if err != nil {
		return false, err
	}

	return true, nil
}

// RenameVolume renames a volume and its snapshots.
func (d *zfs) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	newVol := NewVolume(d, d.name, vol.volType, vol.contentType, newVolName, vol.config, vol.poolConfig)

	// Revert handling.
	revert := revert.New()
	defer revert.Fail()

	// First rename the VFS paths.
	err := genericVFSRenameVolume(d, vol, newVolName, op)
	if err != nil {
		return err
	}

	revert.Add(func() {
		genericVFSRenameVolume(d, newVol, vol.name, op)
	})

	// Rename the ZFS datasets.
	_, err = shared.RunCommand("zfs", "rename", d.dataset(vol, false), d.dataset(newVol, false))
	if err != nil {
		return err
	}

	revert.Add(func() {
		shared.RunCommand("zfs", "rename", d.dataset(newVol, false), d.dataset(vol, false))
	})

	// Update the mountpoints.
	if vol.contentType == ContentTypeFS {
		err = d.setDatasetProperties(d.dataset(newVol, false), fmt.Sprintf("mountpoint=%s", newVol.MountPath()))
		if err != nil {
			return err
		}
	}

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.RenameVolume(fsVol, newVolName, op)
		if err != nil {
			return err
		}

		revert.Add(func() {
			newFsVol := NewVolume(d, d.name, newVol.volType, ContentTypeFS, newVol.name, newVol.config, newVol.poolConfig)
			d.RenameVolume(newFsVol, vol.name, op)
		})
	}

	// All done.
	revert.Success()

	return nil
}

// MigrateVolume sends a volume for migration.
func (d *zfs) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS {
		return ErrNotSupported
	}

	// Handle simple rsync through generic.
	if volSrcArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC {
		return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
	} else if volSrcArgs.MigrationType.FSType != migration.MigrationFSType_ZFS {
		return ErrNotSupported
	}

	// Handle zfs send/receive migration.
	var finalParent string
	if !volSrcArgs.FinalSync {
		// Transfer the snapshots first.
		for i, snapName := range volSrcArgs.Snapshots {
			snapshot, _ := vol.NewSnapshot(snapName)

			// Figure out parent and current subvolumes.
			parent := ""
			if i > 0 {
				oldSnapshot, _ := vol.NewSnapshot(volSrcArgs.Snapshots[i-1])
				parent = d.dataset(oldSnapshot, false)
			}

			// Setup progress tracking.
			var wrapper *ioprogress.ProgressTracker
			if volSrcArgs.TrackProgress {
				wrapper = migration.ProgressTracker(op, "fs_progress", snapshot.name)
			}

			// Send snapshot to recipient (ensure local snapshot volume is mounted if needed).
			err := d.sendDataset(d.dataset(snapshot, false), parent, volSrcArgs, conn, wrapper)
			if err != nil {
				return err
			}

			finalParent = d.dataset(snapshot, false)
		}
	}

	// Setup progress tracking.
	var wrapper *ioprogress.ProgressTracker
	if volSrcArgs.TrackProgress {
		wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
	}

	srcSnapshot := d.dataset(vol, false)
	if !vol.IsSnapshot() {
		// Create a temporary read-only snapshot.
		srcSnapshot = fmt.Sprintf("%s@migration-%s", d.dataset(vol, false), uuid.NewRandom().String())
		_, err := shared.RunCommand("zfs", "snapshot", srcSnapshot)
		if err != nil {
			return err
		}

		if volSrcArgs.MultiSync {
			if volSrcArgs.FinalSync {
				finalParent = volSrcArgs.Data.(string)
				defer shared.RunCommand("zfs", "destroy", finalParent)
				defer shared.RunCommand("zfs", "destroy", srcSnapshot)
			} else {
				volSrcArgs.Data = srcSnapshot // Persist parent state for final sync.
			}
		} else {
			defer shared.RunCommand("zfs", "destroy", srcSnapshot)
		}
	}

	// Send the volume itself.
	err := d.sendDataset(srcSnapshot, finalParent, volSrcArgs, conn, wrapper)
	if err != nil {
		return err
	}

	return nil
}

// BackupVolume creates an exported version of a volume.
func (d *zfs) BackupVolume(vol Volume, targetPath string, optimized bool, snapshots bool, op *operations.Operation) error {
	// Handle the non-optimized tarballs through the generic packer.
	if !optimized {
		return genericVFSBackupVolume(d, vol, targetPath, snapshots, op)
	}

	// Handle the optimized tarballs.
	sendToFile := func(path string, parent string, file string) error {
		// Prepare zfs send arguments.
		args := []string{"send"}
		if parent != "" {
			args = append(args, "-i", parent)
		}
		args = append(args, path)

		// Create the file.
		fd, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			return errors.Wrapf(err, "Failed to open '%s'", file)
		}
		defer fd.Close()

		// Write the subvolume to the file.
		err = shared.RunCommandWithFds(nil, fd, "zfs", args...)
		if err != nil {
			return err
		}

		return nil
	}

	// Handle snapshots.
	finalParent := ""
	if snapshots {
		snapshotsPath := fmt.Sprintf("%s/snapshots", targetPath)

		// Retrieve the snapshots.
		volSnapshots, err := d.VolumeSnapshots(vol, op)
		if err != nil {
			return err
		}

		// Create the snapshot path.
		if len(volSnapshots) > 0 {
			err = os.MkdirAll(snapshotsPath, 0711)
			if err != nil {
				return errors.Wrapf(err, "Failed to create directory '%s'", snapshotsPath)
			}
		}

		for i, snapName := range volSnapshots {
			snapshot, _ := vol.NewSnapshot(snapName)

			// Figure out parent and current subvolumes.
			parent := ""
			if i > 0 {
				oldSnapshot, _ := vol.NewSnapshot(volSnapshots[i-1])
				parent = d.dataset(oldSnapshot, false)
			}

			// Make a binary zfs backup.
			target := fmt.Sprintf("%s/%s.bin", snapshotsPath, snapName)

			err := sendToFile(d.dataset(snapshot, false), parent, target)
			if err != nil {
				return err
			}

			finalParent = d.dataset(snapshot, false)
		}
	}

	// Create a temporary read-only snapshot.
	srcSnapshot := fmt.Sprintf("%s@backup-%s", d.dataset(vol, false), uuid.NewRandom().String())
	_, err := shared.RunCommand("zfs", "snapshot", srcSnapshot)
	if err != nil {
		return err
	}
	defer shared.RunCommand("zfs", "destroy", srcSnapshot)

	// Dump the container to a file.
	fsDump := fmt.Sprintf("%s/container.bin", targetPath)
	err = sendToFile(srcSnapshot, finalParent, fsDump)
	if err != nil {
		return err
	}

	return nil
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *zfs) CreateVolumeSnapshot(vol Volume, op *operations.Operation) error {
	parentName, _, _ := shared.InstanceGetParentAndSnapshotName(vol.name)

	// Revert handling.
	revert := revert.New()
	defer revert.Fail()

	// Create the parent directory.
	err := createParentSnapshotDirIfMissing(d.name, vol.volType, parentName)
	if err != nil {
		return err
	}

	// Create snapshot directory.
	err = vol.EnsureMountPath()
	if err != nil {
		return err
	}

	// Make the snapshot.
	_, err = shared.RunCommand("zfs", "snapshot", d.dataset(vol, false))
	if err != nil {
		return err
	}

	revert.Add(func() { d.DeleteVolumeSnapshot(vol, op) })

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.CreateVolumeSnapshot(fsVol, op)
		if err != nil {
			return err
		}

		revert.Add(func() { d.DeleteVolumeSnapshot(fsVol, op) })
	}

	// All done.
	revert.Success()

	return nil
}

// DeleteVolumeSnapshot removes a snapshot from the storage device.
func (d *zfs) DeleteVolumeSnapshot(vol Volume, op *operations.Operation) error {
	parentName, _, _ := shared.InstanceGetParentAndSnapshotName(vol.name)

	// Handle clones.
	clones, err := d.getClones(d.dataset(vol, false))
	if err != nil {
		return err
	}

	if len(clones) > 0 {
		// Move to the deleted path.
		_, err := shared.RunCommand("zfs", "rename", d.dataset(vol, false), d.dataset(vol, true))
		if err != nil {
			return err
		}
	} else {
		// Delete the snapshot.
		_, err := shared.RunCommand("zfs", "destroy", d.dataset(vol, false))
		if err != nil {
			return err
		}
	}

	// Delete the mountpoint.
	err = os.Remove(vol.MountPath())
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrapf(err, "Failed to remove '%s'", vol.MountPath())
	}

	// Remove the parent snapshot directory if this is the last snapshot being removed.
	err = deleteParentSnapshotDirIfEmpty(d.name, vol.volType, parentName)
	if err != nil {
		return err
	}

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.DeleteVolumeSnapshot(fsVol, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// MountVolumeSnapshot simulates mounting a volume snapshot.
func (d *zfs) MountVolumeSnapshot(vol Volume, op *operations.Operation) (bool, error) {
	// Ignore block devices for now.
	if vol.contentType == ContentTypeBlock {
		return false, ErrNotSupported
	}

	// Check if already mounted.
	if shared.IsMountPoint(vol.MountPath()) {
		return false, nil
	}

	// Mount the snapshot directly (not possible through tools).
	err := TryMount(d.dataset(vol, false), vol.MountPath(), "zfs", 0, "")
	if err != nil {
		return false, err
	}

	return true, nil
}

// UnmountVolume simulates unmounting a volume snapshot.
func (d *zfs) UnmountVolumeSnapshot(vol Volume, op *operations.Operation) (bool, error) {
	// Ignore block devices for now.
	if vol.contentType == ContentTypeBlock {
		return false, ErrNotSupported
	}

	return forceUnmount(vol.MountPath())
}

// VolumeSnapshots returns a list of snapshots for the volume.
func (d *zfs) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	// Get all children datasets.
	entries, err := d.getDatasets(d.dataset(vol, false))
	if err != nil {
		return nil, err
	}

	// Filter only the snapshots.
	snapshots := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry, "@snapshot-") {
			snapshots = append(snapshots, strings.TrimPrefix(entry, "@snapshot-"))
		}
	}

	return snapshots, nil
}

// RestoreVolume restores a volume from a snapshot.
func (d *zfs) RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error {
	snapVol := NewVolume(d, d.name, vol.volType, vol.contentType, fmt.Sprintf("%s/%s", vol.name, snapshotName), vol.config, vol.poolConfig)

	// Get the list of snapshots.
	entries, err := d.getDatasets(d.dataset(vol, false))
	if err != nil {
		return err
	}

	// Check if more recent snapshots exist.
	idx := -1
	snapshots := []string{}
	for i, entry := range entries {
		if entry == fmt.Sprintf("@snapshot-%s", snapshotName) {
			// Located the current snapshot.
			idx = i
			continue
		} else if idx < 0 {
			// Skip any previous snapshot.
			continue
		}

		if strings.HasPrefix(entry, "@snapshot-") {
			// Located a normal snapshot following ours.
			snapshots = append(snapshots, strings.TrimPrefix(entry, "@snapshot-"))
			continue
		}

		if strings.HasPrefix(entry, "@") {
			// Located an internal snapshot.
			return fmt.Errorf("Snapshot '%s' cannot be restored due to subsequent internal snapshot(s) (from a copy)", snapshotName)
		}
	}

	// Check if snapshot removal is allowed.
	if len(snapshots) > 0 {
		if !shared.IsTrue(vol.ExpandedConfig("zfs.remove_snapshots")) {
			return fmt.Errorf("Snapshot '%s' cannot be restored due to subsequent snapshot(s). Set zfs.remove_snapshots to override", snapshotName)
		}

		// Setup custom error to tell the backend what to delete.
		err := ErrDeleteSnapshots{}
		err.Snapshots = snapshots
		return err
	}

	// Restore the snapshot.
	_, err = shared.RunCommand("zfs", "rollback", d.dataset(snapVol, false))
	if err != nil {
		return err
	}

	// For VM images, restore the associated filesystem dataset too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.RestoreVolume(fsVol, snapshotName, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *zfs) RenameVolumeSnapshot(vol Volume, newSnapshotName string, op *operations.Operation) error {
	parentName, _, _ := shared.InstanceGetParentAndSnapshotName(vol.name)
	newVol := NewVolume(d, d.name, vol.volType, vol.contentType, fmt.Sprintf("%s/%s", parentName, newSnapshotName), vol.config, vol.poolConfig)

	// Revert handling.
	revert := revert.New()
	defer revert.Fail()

	// First rename the VFS paths.
	err := genericVFSRenameVolumeSnapshot(d, vol, newSnapshotName, op)
	if err != nil {
		return err
	}

	revert.Add(func() {
		genericVFSRenameVolumeSnapshot(d, newVol, vol.name, op)
	})

	// Rename the ZFS datasets.
	_, err = shared.RunCommand("zfs", "rename", d.dataset(vol, false), d.dataset(newVol, false))
	if err != nil {
		return err
	}

	revert.Add(func() {
		shared.RunCommand("zfs", "rename", d.dataset(newVol, false), d.dataset(vol, false))
	})

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.RenameVolumeSnapshot(fsVol, newSnapshotName, op)
		if err != nil {
			return err
		}

		revert.Add(func() {
			newFsVol := NewVolume(d, d.name, newVol.volType, ContentTypeFS, newVol.name, newVol.config, newVol.poolConfig)
			d.RenameVolumeSnapshot(newFsVol, vol.name, op)
		})
	}

	// All done.
	revert.Success()

	return nil
}
