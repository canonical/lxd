package drivers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/archive"
	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
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

		revert.Add(func() { _ = os.Remove(vol.MountPath()) })
	}

	// Look for previously deleted images.
	if vol.volType == VolumeTypeImage {
		exists, err := d.datasetExists(d.dataset(vol, true))
		if err != nil {
			return err
		}

		if exists {
			canRestore := true

			if vol.IsBlockBacked() && (vol.contentType == ContentTypeBlock || d.isBlockBacked(vol)) {
				// For block volumes check if the cached image volume is larger than the current pool volume.size
				// setting (if so we won't be able to resize the snapshot to that the smaller size later).
				volSize, err := d.getDatasetProperty(d.dataset(vol, true), "volsize")
				if err != nil {
					return err
				}

				volSizeBytes, err := strconv.ParseInt(volSize, 10, 64)
				if err != nil {
					return err
				}

				poolVolSize := d.Info().DefaultBlockSize
				if vol.poolConfig["volume.size"] != "" {
					poolVolSize = vol.poolConfig["volume.size"]
				}

				poolVolSizeBytes, err := units.ParseByteSizeString(poolVolSize)
				if err != nil {
					return err
				}

				// Round to block boundary.
				poolVolSizeBytes = d.roundVolumeBlockSizeBytes(vol, poolVolSizeBytes)

				// If the cached volume size is different than the pool volume size, then we can't use the
				// deleted cached image volume and instead we will rename it to a random UUID so it can't
				// be restored in the future and a new cached image volume will be created instead.
				if volSizeBytes != poolVolSizeBytes {
					d.logger.Debug("Renaming deleted cached image volume so that regeneration is used", logger.Ctx{"fingerprint": vol.Name()})
					randomVol := NewVolume(d, d.name, vol.volType, vol.contentType, d.randomVolumeName(vol), vol.config, vol.poolConfig)

					_, err := shared.RunCommandContext(context.TODO(), "/proc/self/exe", "forkzfs", "--", "rename", d.dataset(vol, true), d.dataset(randomVol, true))
					if err != nil {
						return err
					}

					if vol.IsVMBlock() {
						fsVol := vol.NewVMBlockFilesystemVolume()
						randomFsVol := randomVol.NewVMBlockFilesystemVolume()

						_, err := shared.RunCommandContext(context.TODO(), "/proc/self/exe", "forkzfs", "--", "rename", d.dataset(fsVol, true), d.dataset(randomFsVol, true))
						if err != nil {
							return err
						}
					}

					// We have renamed the deleted cached image volume, so we don't want to try and
					// restore it.
					canRestore = false
				}
			}

			// Restore the image.
			if canRestore {
				d.logger.Debug("Restoring previously deleted cached image volume", logger.Ctx{"fingerprint": vol.Name()})
				_, err := shared.RunCommandContext(context.TODO(), "/proc/self/exe", "forkzfs", "--", "rename", d.dataset(vol, true), d.dataset(vol, false))
				if err != nil {
					return err
				}

				if vol.IsVMBlock() {
					fsVol := vol.NewVMBlockFilesystemVolume()

					_, err := shared.RunCommandContext(context.TODO(), "/proc/self/exe", "forkzfs", "--", "rename", d.dataset(fsVol, true), d.dataset(fsVol, false))
					if err != nil {
						return err
					}
				}

				revert.Success()
				return nil
			}
		}
	}

	// After this point we'll have a volume, so setup revert.
	revert.Add(func() { _ = d.DeleteVolume(vol, op) })

	if vol.contentType == ContentTypeFS && !d.isBlockBacked(vol) {
		// Create the filesystem dataset.
		err := d.createDataset(d.dataset(vol, false), "mountpoint=legacy", "canmount=noauto")
		if err != nil {
			return err
		}

		// Apply the size limit.
		err = d.SetVolumeQuota(vol, vol.ConfigSize(), false, op)
		if err != nil {
			return err
		}

		// Apply the blocksize.
		err = d.setBlocksizeFromConfig(vol)
		if err != nil {
			return err
		}
	} else {
		var opts []string

		if vol.contentType == ContentTypeFS {
			// Use volmode=dev so volume is visible as we need to run makeFSType.
			opts = []string{"volmode=dev"}
		} else {
			// Use volmode=none so volume is invisible until mounted.
			opts = []string{"volmode=none"}
		}

		// Add custom property lxd:content_type which allows distinguishing between regular volumes, block_mode enabled volumes, and ISO volumes.
		if vol.volType == VolumeTypeCustom {
			opts = append(opts, "lxd:content_type="+string(vol.contentType))
		}

		// Avoid double caching in the ARC cache and in the guest OS filesystem cache.
		if vol.volType == VolumeTypeVM {
			opts = append(opts, "primarycache=metadata", "secondarycache=metadata")
		}

		loopPath := loopFilePath(d.name)
		if d.config["source"] == loopPath {
			// Create the volume dataset with sync disabled (to avoid kernel lockups when using a disk based pool).
			opts = append(opts, "sync=disabled")
		}

		blockSize := vol.ExpandedConfig("zfs.blocksize")
		if blockSize != "" {
			// Convert to bytes.
			sizeBytes, err := units.ParseByteSizeString(blockSize)
			if err != nil {
				return err
			}

			// zfs.blocksize can have value in range from 512 to 16MiB because it's used for volblocksize and recordsize
			// volblocksize maximum value is 128KiB so if the value of zfs.blocksize is bigger set it to 128KiB.
			if sizeBytes > zfsMaxVolBlocksize {
				sizeBytes = zfsMaxVolBlocksize
			}

			opts = append(opts, fmt.Sprintf("volblocksize=%d", sizeBytes))
		}

		sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
		if err != nil {
			return err
		}

		sizeBytes = d.roundVolumeBlockSizeBytes(vol, sizeBytes)

		// Create the volume dataset.
		err = d.createVolume(d.dataset(vol, false), sizeBytes, opts...)
		if err != nil {
			return err
		}

		if vol.contentType == ContentTypeFS {
			devPath, err := d.GetVolumeDiskPath(vol)
			if err != nil {
				return err
			}

			zfsFilesystem := vol.ConfigBlockFilesystem()

			_, err = makeFSType(devPath, zfsFilesystem, nil)
			if err != nil {
				return err
			}

			err = d.setDatasetProperties(d.dataset(vol, false), "volmode=none")
			if err != nil {
				return err
			}
		}
	}

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.CreateVolume(fsVol, nil, op)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.DeleteVolume(fsVol, op) })
	}

	err := vol.MountTask(func(mountPath string, op *operations.Operation) error {
		// Run the volume filler function if supplied.
		if filler != nil && filler.Fill != nil {
			var err error
			var devPath string

			if IsContentBlock(vol.contentType) {
				// Get the device path.
				devPath, err = d.GetVolumeDiskPath(vol)
				if err != nil {
					return err
				}
			}

			allowUnsafeResize := false
			if vol.volType == VolumeTypeImage {
				// Allow filler to resize initial image volume as needed.
				// Some storage drivers don't normally allow image volumes to be resized due to
				// them having read-only snapshots that cannot be resized. However when creating
				// the initial image volume and filling it before the snapshot is taken resizing
				// can be allowed and is required in order to support unpacking images larger than
				// the default volume size. The filler function is still expected to obey any
				// volume size restrictions configured on the pool.
				// Unsafe resize is also needed to disable filesystem resize safety checks.
				// This is safe because if for some reason an error occurs the volume will be
				// discarded rather than leaving a corrupt filesystem.
				allowUnsafeResize = true
			}

			// Run the filler.
			err = d.runFiller(vol, devPath, filler, allowUnsafeResize)
			if err != nil {
				return err
			}

			// Move the GPT alt header to end of disk if needed.
			if vol.IsVMBlock() {
				err = d.moveGPTAltHeader(devPath)
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
		_, err := shared.RunCommandContext(context.TODO(), "zfs", "snapshot", "-r", d.dataset(vol, false)+"@readonly")
		if err != nil {
			return err
		}

		if vol.contentType == ContentTypeBlock {
			// Re-create the FS config volume's readonly snapshot now that the filler function has run
			// and unpacked into both config and block volumes.
			fsVol := vol.NewVMBlockFilesystemVolume()

			_, err := shared.RunCommandContext(context.TODO(), "zfs", "destroy", "-r", d.dataset(fsVol, false)+"@readonly")
			if err != nil {
				return err
			}

			_, err = shared.RunCommandContext(context.TODO(), "zfs", "snapshot", "-r", d.dataset(fsVol, false)+"@readonly")
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
func (d *zfs) CreateVolumeFromBackup(vol VolumeCopy, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	// Handle the non-optimized tarballs through the generic unpacker.
	if !*srcBackup.OptimizedStorage {
		return genericVFSBackupUnpack(d, d.state, vol, srcBackup.Snapshots, srcData, op)
	}

	volExists, err := d.HasVolume(vol.Volume)
	if err != nil {
		return nil, nil, err
	}

	if volExists {
		return nil, nil, errors.New("Cannot restore volume, already exists on target")
	}

	revert := revert.New()
	defer revert.Fail()

	// Define a revert function that will be used both to revert if an error occurs inside this
	// function but also return it for use from the calling functions if no error internally.
	revertHook := func() {
		for _, snapName := range srcBackup.Snapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, d.name, vol.volType, vol.contentType, fullSnapshotName, vol.config, vol.poolConfig)
			_ = d.DeleteVolumeSnapshot(snapVol, op)
		}

		// And lastly the main volume.
		_ = d.DeleteVolume(vol.Volume, op)
	}

	// Only execute the revert function if we have had an error internally.
	revert.Add(revertHook)

	// Define function to unpack a volume from a backup tarball file.
	unpackVolume := func(v Volume, r io.ReadSeeker, unpacker []string, srcFile string, target string) error {
		d.Logger().Debug("Unpacking optimized volume", logger.Ctx{"source": srcFile, "target": target})

		targetPath := shared.VarPath("storage-pools", target)
		tr, cancelFunc, err := archive.CompressedTarReader(d.state, context.Background(), r, unpacker, targetPath)
		if err != nil {
			return err
		}

		defer cancelFunc()

		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break // End of archive.
			}

			if err != nil {
				return err
			}

			if hdr.Name == srcFile {
				// Extract the backup.
				if v.ContentType() == ContentTypeBlock || d.isBlockBacked(v) {
					err = shared.RunCommandWithFds(context.TODO(), tr, nil, "zfs", "receive", "-F", target)
				} else {
					err = shared.RunCommandWithFds(context.TODO(), tr, nil, "zfs", "receive", "-x", "mountpoint", "-F", target)
				}

				if err != nil {
					return err
				}

				cancelFunc()
				return nil
			}
		}

		return fmt.Errorf("Could not find %q", srcFile)
	}

	var postHook VolumePostHook

	// Create a list of actual volumes to unpack.
	var vols []Volume
	if vol.IsVMBlock() {
		vols = append(vols, vol.NewVMBlockFilesystemVolume())
	}

	vols = append(vols, vol.Volume)

	for _, v := range vols {
		// Find the compression algorithm used for backup source data.
		_, err := srcData.Seek(0, io.SeekStart)
		if err != nil {
			return nil, nil, err
		}

		_, _, unpacker, err := shared.DetectCompressionFile(srcData)
		if err != nil {
			return nil, nil, err
		}

		if len(srcBackup.Snapshots) > 0 {
			// Create new snapshots directory.
			err := createParentSnapshotDirIfMissing(d.name, v.volType, v.name)
			if err != nil {
				return nil, nil, err
			}
		}

		// Restore backups from oldest to newest.
		for _, snapName := range srcBackup.Snapshots {
			// Defend against path traversal attacks.
			err := instancetype.ValidSnapName(snapName)
			if err != nil {
				return nil, nil, fmt.Errorf("Invalid snapshot name %q: %w", snapName, err)
			}

			prefix := "snapshots"
			fileName := snapName + ".bin"
			switch v.volType {
			case VolumeTypeVM:
				prefix = "virtual-machine-snapshots"
				if v.contentType == ContentTypeFS {
					fileName = snapName + "-config.bin"
				}

			case VolumeTypeCustom:
				prefix = "volume-snapshots"
			}

			srcFile := "backup/" + prefix + "/" + fileName
			dstSnapshot := d.dataset(v, false) + "@snapshot-" + snapName
			err = unpackVolume(v, srcData, unpacker, srcFile, dstSnapshot)
			if err != nil {
				return nil, nil, err
			}
		}

		// Extract main volume.
		fileName := "container.bin"
		switch v.volType {
		case VolumeTypeVM:
			if v.contentType == ContentTypeFS {
				fileName = "virtual-machine-config.bin"
			} else {
				fileName = "virtual-machine.bin"
			}

		case VolumeTypeCustom:
			fileName = "volume.bin"
		}

		err = unpackVolume(v, srcData, unpacker, "backup/"+fileName, d.dataset(v, false))
		if err != nil {
			return nil, nil, err
		}

		// Strip internal snapshots.
		entries, err := d.getDatasets(d.dataset(v, false), "snapshot")
		if err != nil {
			return nil, nil, err
		}

		// Remove only the internal snapshots.
		for _, entry := range entries {
			if strings.Contains(entry, "@snapshot-") {
				continue
			}

			if strings.Contains(entry, "@") {
				_, err := shared.RunCommandContext(context.TODO(), "zfs", "destroy", d.dataset(v, false)+entry)
				if err != nil {
					return nil, nil, err
				}
			}
		}

		// Re-apply the base mount options.
		if v.contentType == ContentTypeFS {
			if zfsDelegate {
				// Unset the zoned property so the mountpoint property can be updated.
				err := d.setDatasetProperties(d.dataset(v, false), "zoned=off")
				if err != nil {
					return nil, nil, err
				}
			}

			err := d.setDatasetProperties(d.dataset(v, false), "mountpoint=legacy", "canmount=noauto")
			if err != nil {
				return nil, nil, err
			}

			// Apply the blocksize.
			err = d.setBlocksizeFromConfig(v)
			if err != nil {
				return nil, nil, err
			}
		}

		// Only mount instance filesystem volumes for backup.yaml access.
		if v.volType != VolumeTypeCustom && v.contentType != ContentTypeBlock {
			// The import requires a mounted volume, so mount it and have it unmounted as a post hook.
			err = d.MountVolume(v, op)
			if err != nil {
				return nil, nil, err
			}

			revert.Add(func() { _, _ = d.UnmountVolume(v, false, op) })

			postHook = func(postVol Volume) error {
				_, err := d.UnmountVolume(postVol, false, op)
				return err
			}
		}
	}

	cleanup := revert.Clone().Fail // Clone before calling revert.Success() so we can return the Fail func.
	revert.Success()
	return postHook, cleanup, nil
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *zfs) CreateVolumeFromCopy(vol VolumeCopy, srcVol VolumeCopy, allowInconsistent bool, op *operations.Operation) error {
	var err error

	// Revert handling
	revert := revert.New()
	defer revert.Fail()

	if vol.contentType == ContentTypeFS {
		// Create mountpoint.
		err = vol.EnsureMountPath()
		if err != nil {
			return err
		}

		revert.Add(func() { _ = os.Remove(vol.MountPath()) })
	}

	// For VMs, also copy the filesystem dataset.
	if vol.IsVMBlock() {
		// For VMs, also copy the filesystem volume.
		// We can pass the regular volume's snapshots as only their presence is relevant.
		srcFSVol := NewVolumeCopy(srcVol.NewVMBlockFilesystemVolume(), srcVol.Snapshots...)
		fsVol := NewVolumeCopy(vol.NewVMBlockFilesystemVolume(), vol.Snapshots...)

		err = d.CreateVolumeFromCopy(fsVol, srcFSVol, false, op)
		if err != nil {
			return err
		}

		// Delete on revert.
		revert.Add(func() { _ = d.DeleteVolume(fsVol.Volume, op) })
	}

	// Retrieve snapshots on the source.
	snapshots := []string{}
	if !srcVol.IsSnapshot() && len(vol.Snapshots) > 0 {
		snapshots, err = d.VolumeSnapshots(srcVol.Volume, op)
		if err != nil {
			return err
		}
	}

	// When not allowing inconsistent copies and the volume has a mounted filesystem, we must ensure it is
	// consistent by syncing and freezing the filesystem to ensure unwritten pages are flushed and that no
	// further modifications occur while taking the source snapshot.
	var unfreezeFS func() error
	sourcePath := srcVol.MountPath()
	if !allowInconsistent && srcVol.contentType == ContentTypeFS && srcVol.IsBlockBacked() && filesystem.IsMountPoint(sourcePath) {
		unfreezeFS, err = d.filesystemFreeze(sourcePath)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = unfreezeFS() })
	}

	var srcSnapshot string
	if srcVol.volType == VolumeTypeImage {
		srcSnapshot = d.dataset(srcVol.Volume, false) + "@readonly"
	} else if srcVol.IsSnapshot() {
		srcSnapshot = d.dataset(srcVol.Volume, false)
	} else {
		// Create a new snapshot for copy.
		srcSnapshot = d.dataset(srcVol.Volume, false) + "@copy-" + uuid.New().String()

		_, err := shared.RunCommandContext(context.TODO(), "zfs", "snapshot", "-r", srcSnapshot)
		if err != nil {
			return err
		}

		// If zfs.clone_copy is disabled delete the snapshot at the end.
		if shared.IsFalse(d.config["zfs.clone_copy"]) || len(snapshots) > 0 {
			// Delete the snapshot at the end.
			defer func() {
				// Delete snapshot (or mark for deferred deletion if cannot be deleted currently).
				_, err := shared.RunCommandContext(context.TODO(), "zfs", "destroy", "-r", "-d", srcSnapshot)
				if err != nil {
					d.logger.Warn("Failed deleting temporary snapshot for copy", logger.Ctx{"snapshot": srcSnapshot, "err": err})
				}
			}()
		} else {
			// Delete the snapshot on revert.
			revert.Add(func() {
				// Delete snapshot (or mark for deferred deletion if cannot be deleted currently).
				_, err := shared.RunCommandContext(context.TODO(), "zfs", "destroy", "-r", "-d", srcSnapshot)
				if err != nil {
					d.logger.Warn("Failed deleting temporary snapshot for copy", logger.Ctx{"snapshot": srcSnapshot, "err": err})
				}
			})
		}
	}

	// Now that source snapshot has been taken we can safely unfreeze the source filesystem.
	if unfreezeFS != nil {
		_ = unfreezeFS()
	}

	// Delete the volume created on failure.
	revert.Add(func() { _ = d.DeleteVolume(vol.Volume, op) })

	// If zfs.clone_copy is disabled or source volume has snapshots, then use full copy mode.
	if shared.IsFalse(d.config["zfs.clone_copy"]) || len(snapshots) > 0 {
		snapName := strings.SplitN(srcSnapshot, "@", 2)[1]

		// Send/receive the snapshot.
		var sender *exec.Cmd
		var receiver *exec.Cmd
		if vol.ContentType() == ContentTypeBlock || d.isBlockBacked(vol.Volume) {
			receiver = exec.Command("zfs", "receive", d.dataset(vol.Volume, false))
		} else {
			receiver = exec.Command("zfs", "receive", "-x", "mountpoint", d.dataset(vol.Volume, false))
		}

		// Handle transferring snapshots.
		if len(snapshots) > 0 {
			args := []string{"send", "-R"}

			// Use raw flag is supported, this is required to send/receive encrypted volumes (and enables compression).
			if zfsRaw {
				args = append(args, "-w")
			}

			args = append(args, srcSnapshot)

			sender = exec.Command("zfs", args...)
		} else {
			args := []string{"send"}

			// Check if nesting is required.
			if d.needsRecursion(d.dataset(srcVol.Volume, false)) {
				args = append(args, "-R")

				if zfsRaw {
					args = append(args, "-w")
				}
			}

			if d.config["zfs.clone_copy"] == "rebase" {
				var err error
				origin := d.dataset(srcVol.Volume, false)
				for {
					fields := strings.SplitN(origin, "@", 2)

					// If the origin is a @readonly snapshot under a /images/ path (/images or deleted/images), we're done.
					if len(fields) > 1 && strings.Contains(fields[0], "/images/") && fields[1] == "readonly" {
						break
					}

					origin, err = d.getDatasetProperty(origin, "origin")
					if err != nil {
						return err
					}

					if origin == "" || origin == "-" {
						origin = ""
						break
					}
				}

				if origin != "" && origin != srcSnapshot {
					args = append(args, "-i", origin)
					args = append(args, srcSnapshot)
					sender = exec.Command("zfs", args...)
				} else {
					args = append(args, srcSnapshot)
					sender = exec.Command("zfs", args...)
				}
			} else {
				args = append(args, srcSnapshot)
				sender = exec.Command("zfs", args...)
			}
		}

		// Configure the pipes.
		receiver.Stdin, _ = sender.StdoutPipe()
		receiver.Stdout = os.Stdout

		var recvStderr bytes.Buffer
		receiver.Stderr = &recvStderr

		var sendStderr bytes.Buffer
		sender.Stderr = &sendStderr

		// Run the transfer.
		err := receiver.Start()
		if err != nil {
			return fmt.Errorf("Failed starting ZFS receive: %w", err)
		}

		err = sender.Start()
		if err != nil {
			_ = receiver.Process.Kill()
			return fmt.Errorf("Failed starting ZFS send: %w", err)
		}

		senderErr := make(chan error)
		go func() {
			err := sender.Wait()
			if err != nil {
				_ = receiver.Process.Kill()

				// This removes any newlines in the error message.
				msg := strings.ReplaceAll(strings.TrimSpace(sendStderr.String()), "\n", " ")

				senderErr <- fmt.Errorf("Failed ZFS send: %w (%s)", err, msg)
				return
			}

			senderErr <- nil
		}()

		err = receiver.Wait()
		if err != nil {
			_ = sender.Process.Kill()

			// This removes any newlines in the error message.
			msg := strings.ReplaceAll(strings.TrimSpace(recvStderr.String()), "\n", " ")

			return fmt.Errorf("Failed ZFS receive: %w (%s)", err, msg)
		}

		err = <-senderErr
		if err != nil {
			return err
		}

		// Delete the snapshot.
		_, err = shared.RunCommandContext(context.TODO(), "zfs", "destroy", "-r", d.dataset(vol.Volume, false)+"@"+snapName)
		if err != nil {
			return err
		}

		// Cleanup unexpected snapshots.
		if len(snapshots) > 0 {
			children, err := d.getDatasets(d.dataset(vol.Volume, false), "snapshot")
			if err != nil {
				return err
			}

			for _, entry := range children {
				// Check if expected snapshot.
				if strings.Contains(entry, "@snapshot-") {
					name := strings.Split(entry, "@snapshot-")[1]
					if slices.Contains(snapshots, name) {
						continue
					}
				}

				// Delete the rest.
				_, err := shared.RunCommandContext(context.TODO(), "zfs", "destroy", d.dataset(vol.Volume, false)+entry)
				if err != nil {
					return err
				}
			}
		}
	} else {
		// Perform volume clone.
		args := []string{"clone"}

		if vol.contentType == ContentTypeBlock {
			// Use volmode=none so volume is invisible until mounted.
			args = append(args, "-o", "volmode=none")
		}

		args = append(args, srcSnapshot, d.dataset(vol.Volume, false))

		// Clone the snapshot.
		_, err := shared.RunCommandContext(context.TODO(), "zfs", args...)
		if err != nil {
			return err
		}
	}

	// Apply the properties.
	if vol.contentType == ContentTypeFS {
		if !d.isBlockBacked(srcVol.Volume) {
			err := d.setDatasetProperties(d.dataset(vol.Volume, false), "mountpoint=legacy", "canmount=noauto")
			if err != nil {
				return err
			}

			// Apply the blocksize.
			err = d.setBlocksizeFromConfig(vol.Volume)
			if err != nil {
				return err
			}
		}

		if d.isBlockBacked(srcVol.Volume) && renegerateFilesystemUUIDNeeded(vol.ConfigBlockFilesystem()) {
			_, err := d.activateVolume(vol.Volume)
			if err != nil {
				return err
			}

			volPath, err := d.GetVolumeDiskPath(vol.Volume)
			if err != nil {
				return err
			}

			d.logger.Debug("Regenerating filesystem UUID", logger.Ctx{"dev": volPath, "fs": vol.ConfigBlockFilesystem()})
			err = regenerateFilesystemUUID(vol.ConfigBlockFilesystem(), volPath)
			if err != nil {
				return err
			}
		}

		// Mount the volume and ensure the permissions are set correctly inside the mounted volume.
		err := vol.MountTask(func(_ string, _ *operations.Operation) error {
			return vol.EnsureMountPath()
		}, op)
		if err != nil {
			return err
		}
	}

	// Pass allowUnsafeResize as true when resizing block backed filesystem volumes because we want to allow
	// the filesystem to be shrunk as small as possible without needing the safety checks that would prevent
	// leaving the filesystem in an inconsistent state if the resize couldn't be completed. This is because if
	// the resize fails we will delete the volume anyway so don't have to worry about it being inconsistent.
	var allowUnsafeResize bool
	if d.isBlockBacked(vol.Volume) && vol.contentType == ContentTypeFS {
		allowUnsafeResize = true
	}

	// Resize volume to the size specified. Only uses volume "size" property and does not use pool/defaults
	// to give the caller more control over the size being used.
	err = d.SetVolumeQuota(vol.Volume, vol.config["size"], allowUnsafeResize, op)
	if err != nil {
		return err
	}

	// All done.
	revert.Success()
	return nil
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *zfs) CreateVolumeFromMigration(vol VolumeCopy, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	// Handle simple rsync and block_and_rsync through generic.
	if volTargetArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC || volTargetArgs.MigrationType.FSType == migration.MigrationFSType_BLOCK_AND_RSYNC {
		_, err := genericVFSCreateVolumeFromMigration(d, nil, vol, conn, volTargetArgs, preFiller, op)
		return err
	} else if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_ZFS {
		return ErrNotSupported
	}

	var migrationHeader ZFSMetaDataHeader

	// If no snapshots have been provided it can mean two things:
	// 1) The target has no snapshots
	// 2) Snapshots shouldn't be copied (--instance-only flag)
	volumeOnly := len(volTargetArgs.Snapshots) == 0

	if slices.Contains(volTargetArgs.MigrationType.Features, migration.ZFSFeatureMigrationHeader) {
		// The source will send all of its snapshots with their respective GUID.
		buf, err := io.ReadAll(conn)
		if err != nil {
			return fmt.Errorf("Failed reading ZFS migration header: %w", err)
		}

		err = json.Unmarshal(buf, &migrationHeader)
		if err != nil {
			return fmt.Errorf("Failed decoding ZFS migration header: %w", err)
		}
	}

	// If we're refreshing, send back all snapshots of the target.
	if volTargetArgs.Refresh && slices.Contains(volTargetArgs.MigrationType.Features, migration.ZFSFeatureMigrationHeader) {
		snapshots, err := vol.Volume.Snapshots(op)
		if err != nil {
			return fmt.Errorf("Failed getting volume snapshots: %w", err)
		}

		// If there are no snapshots on the target, there's no point in doing an optimized
		// refresh.
		if len(snapshots) == 0 {
			volTargetArgs.Refresh = false
		}

		var respSnapshots []ZFSDataset
		var syncSnapshotNames []string

		// Get the GUIDs of all target snapshots.
		for _, snapVol := range snapshots {
			guid, err := d.getDatasetProperty(d.dataset(snapVol, false), "guid")
			if err != nil {
				return err
			}

			_, snapName, _ := api.GetParentAndSnapshotName(snapVol.name)

			respSnapshots = append(respSnapshots, ZFSDataset{Name: snapName, GUID: guid})
		}

		// Generate list of snapshots which need to be synced, i.e. are available on the source but not on the target.
		for _, srcSnapshot := range migrationHeader.SnapshotDatasets {
			found := false

			for _, dstSnapshot := range respSnapshots {
				if srcSnapshot.GUID == dstSnapshot.GUID {
					found = true
					break
				}
			}

			if !found {
				syncSnapshotNames = append(syncSnapshotNames, srcSnapshot.Name)
			}
		}

		// The following scenario will result in a failure:
		// - The source has more than one snapshot
		// - The target has at least one of these snapshot, but not the very first
		//
		// It will fail because the source tries sending the first snapshot using `zfs send <first>`.
		// Since the target does have snapshots, `zfs receive` will fail with:
		//     cannot receive new filesystem stream: destination has snapshots
		//
		// We therefore need to check the snapshots, and delete all target snapshots if the above
		// scenario is true.
		if !volumeOnly && len(respSnapshots) > 0 && len(migrationHeader.SnapshotDatasets) > 0 && respSnapshots[0].GUID != migrationHeader.SnapshotDatasets[0].GUID {
			for _, snapVol := range snapshots {
				// Delete
				err = d.DeleteVolume(snapVol, op)
				if err != nil {
					return err
				}
			}

			// Let the source know that we don't have any snapshots.
			respSnapshots = []ZFSDataset{}

			// Let the source know that we need all snapshots.
			syncSnapshotNames = []string{}

			for _, dataset := range migrationHeader.SnapshotDatasets {
				syncSnapshotNames = append(syncSnapshotNames, dataset.Name)
			}
		} else {
			// Delete local snapshots which exist on the target but not on the source.
			for _, snapVol := range snapshots {
				targetOnlySnapshot := true
				_, snapName, _ := api.GetParentAndSnapshotName(snapVol.name)

				for _, migrationSnap := range migrationHeader.SnapshotDatasets {
					if snapName == migrationSnap.Name {
						targetOnlySnapshot = false
						break
					}
				}

				if targetOnlySnapshot {
					// Delete
					err = d.DeleteVolume(snapVol, op)
					if err != nil {
						return err
					}
				}
			}
		}

		migrationHeader = ZFSMetaDataHeader{}
		migrationHeader.SnapshotDatasets = respSnapshots

		// Send back all target snapshots with their GUIDs.
		headerJSON, err := json.Marshal(migrationHeader)
		if err != nil {
			return fmt.Errorf("Failed encoding ZFS migration header: %w", err)
		}

		_, err = conn.Write(headerJSON)
		if err != nil {
			return fmt.Errorf("Failed sending ZFS migration header: %w", err)
		}

		err = conn.Close() // End the frame.
		if err != nil {
			return fmt.Errorf("Failed closing ZFS migration header frame: %w", err)
		}

		// Don't pass the snapshots if it's volume only.
		if !volumeOnly {
			volTargetArgs.Snapshots = syncSnapshotNames
		}
	}

	return d.createVolumeFromMigrationOptimized(vol.Volume, conn, volTargetArgs, volumeOnly, preFiller, op)
}

func (d *zfs) createVolumeFromMigrationOptimized(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, volumeOnly bool, preFiller *VolumeFiller, op *operations.Operation) error {
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.createVolumeFromMigrationOptimized(fsVol, conn, volTargetArgs, volumeOnly, preFiller, op)
		if err != nil {
			return err
		}
	}

	var snapshots []Volume
	var err error

	// Rollback to the latest identical snapshot if performing a refresh.
	if volTargetArgs.Refresh {
		snapshots, err = vol.Snapshots(op)
		if err != nil {
			return err
		}

		if len(snapshots) > 0 {
			lastIdenticalSnapshot := snapshots[len(snapshots)-1]
			err = d.restoreVolume(vol, lastIdenticalSnapshot, true, op)
			if err != nil {
				return err
			}
		}
	}

	revert := revert.New()
	defer revert.Fail()

	// Handle zfs send/receive migration.
	if len(volTargetArgs.Snapshots) > 0 {
		// Create the parent directory.
		err := createParentSnapshotDirIfMissing(d.name, vol.volType, vol.name)
		if err != nil {
			return err
		}

		// Transfer the snapshots.
		for _, snapName := range volTargetArgs.Snapshots {
			snapVol, err := vol.NewSnapshot(snapName)
			if err != nil {
				return err
			}

			wrapper := migration.ProgressWriter(op, "fs_progress", snapVol.Name())

			err = d.receiveDataset(snapVol, conn, wrapper)
			if err != nil {
				_ = d.DeleteVolume(snapVol, op)
				return fmt.Errorf("Failed receiving snapshot volume %q: %w", snapVol.Name(), err)
			}

			revert.Add(func() {
				_ = d.DeleteVolumeSnapshot(snapVol, op)
			})
		}
	}

	if !volTargetArgs.Refresh {
		revert.Add(func() {
			_ = d.DeleteVolume(vol, op)
		})
	}

	// Transfer the main volume.
	wrapper := migration.ProgressWriter(op, "fs_progress", vol.name)
	err = d.receiveDataset(vol, conn, wrapper)
	if err != nil {
		return fmt.Errorf("Failed receiving volume %q: %w", vol.Name(), err)
	}

	// Strip internal snapshots.
	entries, err := d.getDatasets(d.dataset(vol, false), "snapshot")
	if err != nil {
		return err
	}

	// keepDataset returns whether to keep the data set or delete it. Data sets that are non-snapshots or
	// snapshots that match the requested snapshots in volTargetArgs.Snapshots are kept. Any other snapshot
	// data sets should be removed.
	keepDataset := func(dataSetName string) bool {
		// Keep non-snapshot data sets and snapshots that don't have the LXD snapshot prefix indicator.
		dataSetSnapshotPrefix := "@snapshot-"
		if !strings.HasPrefix(dataSetName, "@") || !strings.HasPrefix(dataSetName, dataSetSnapshotPrefix) {
			return false
		}

		// Check if snapshot data set matches one of the requested snapshots in volTargetArgs.Snapshots.
		// If so, then keep it, otherwise request it be removed.
		entrySnapName := strings.TrimPrefix(dataSetName, dataSetSnapshotPrefix)
		return slices.Contains(volTargetArgs.Snapshots, entrySnapName) // Delete any other snapshot data sets that have been transferred.
	}

	if volTargetArgs.Refresh {
		// Only delete the latest migration snapshot.
		_, err := shared.RunCommandContext(context.TODO(), "zfs", "destroy", "-r", d.dataset(vol, false)+entries[len(entries)-1])
		if err != nil {
			return err
		}
	} else {
		// Remove any snapshots that were transferred but are not needed.
		for _, entry := range entries {
			if !keepDataset(entry) {
				_, err := shared.RunCommandContext(context.TODO(), "zfs", "destroy", d.dataset(vol, false)+entry)
				if err != nil {
					return err
				}
			}
		}
	}

	if vol.contentType == ContentTypeFS {
		// Create mountpoint.
		err := vol.EnsureMountPath()
		if err != nil {
			return err
		}

		if !d.isBlockBacked(vol) {
			// Re-apply the base mount options.
			if zfsDelegate {
				// Unset the zoned property so the mountpoint property can be updated.
				err := d.setDatasetProperties(d.dataset(vol, false), "zoned=off")
				if err != nil {
					return err
				}
			}

			err = d.setDatasetProperties(d.dataset(vol, false), "mountpoint=legacy", "canmount=noauto")
			if err != nil {
				return err
			}

			// Apply the size limit.
			err = d.SetVolumeQuota(vol, vol.ConfigSize(), false, op)
			if err != nil {
				return err
			}

			// Apply the blocksize.
			err = d.setBlocksizeFromConfig(vol)
			if err != nil {
				return err
			}
		}

		if d.isBlockBacked(vol) && renegerateFilesystemUUIDNeeded(vol.ConfigBlockFilesystem()) {
			// Activate volume if needed.
			activated, err := d.activateVolume(vol)
			if err != nil {
				return err
			}

			if activated {
				defer func() { _, _ = d.deactivateVolume(vol) }()
			}

			volPath, err := d.GetVolumeDiskPath(vol)
			if err != nil {
				return err
			}

			d.logger.Debug("Regenerating filesystem UUID", logger.Ctx{"dev": volPath, "fs": vol.ConfigBlockFilesystem()})
			err = regenerateFilesystemUUID(vol.ConfigBlockFilesystem(), volPath)
			if err != nil {
				return err
			}
		}
	}

	revert.Success()
	return nil
}

// RefreshVolume updates an existing volume to match the state of another.
func (d *zfs) RefreshVolume(vol VolumeCopy, srcVol VolumeCopy, refreshSnapshots []string, allowInconsistent bool, op *operations.Operation) error {
	var err error
	var targetSnapshots []Volume
	var srcSnapshotsAll []Volume

	if !srcVol.IsSnapshot() {
		// Get target snapshots
		targetSnapshots, err = vol.Volume.Snapshots(op)
		if err != nil {
			return fmt.Errorf("Failed to get target snapshots: %w", err)
		}

		srcSnapshotsAll, err = srcVol.Volume.Snapshots(op)
		if err != nil {
			return fmt.Errorf("Failed to get source snapshots: %w", err)
		}
	}

	// If there are no target or source snapshots, perform a simple copy using zfs.
	// We cannot use generic vfs volume copy here, as zfs will complain if a generic
	// copy/refresh is followed by an optimized refresh.
	if len(targetSnapshots) == 0 || len(srcSnapshotsAll) == 0 {
		err = d.DeleteVolume(vol.Volume, op)
		if err != nil {
			return err
		}

		return d.CreateVolumeFromCopy(vol, srcVol, false, op)
	}

	transfer := func(src Volume, target Volume, origin Volume) error {
		var sender *exec.Cmd

		receiver := exec.Command("zfs", "receive", d.dataset(target, false))

		args := []string{"send"}

		// Check if nesting is required.
		if d.needsRecursion(d.dataset(src, false)) {
			args = append(args, "-R")

			if zfsRaw {
				args = append(args, "-w")
			}
		}

		if origin.Name() != src.Name() {
			args = append(args, "-i", d.dataset(origin, false), d.dataset(src, false))
			sender = exec.Command("zfs", args...)
		} else {
			args = append(args, d.dataset(src, false))
			sender = exec.Command("zfs", args...)
		}

		// Configure the pipes.
		receiver.Stdin, _ = sender.StdoutPipe()
		receiver.Stdout = os.Stdout

		var recvStderr bytes.Buffer
		receiver.Stderr = &recvStderr

		var sendStderr bytes.Buffer
		sender.Stderr = &sendStderr

		// Run the transfer.
		err := receiver.Start()
		if err != nil {
			return fmt.Errorf("Failed starting ZFS receive: %w", err)
		}

		err = sender.Start()
		if err != nil {
			_ = receiver.Process.Kill()
			return fmt.Errorf("Failed starting ZFS send: %w", err)
		}

		senderErr := make(chan error)
		go func() {
			err := sender.Wait()
			if err != nil {
				_ = receiver.Process.Kill()

				// This removes any newlines in the error message.
				msg := strings.ReplaceAll(strings.TrimSpace(sendStderr.String()), "\n", " ")

				senderErr <- fmt.Errorf("Failed ZFS send: %w (%s)", err, msg)
				return
			}

			senderErr <- nil
		}()

		err = receiver.Wait()
		if err != nil {
			_ = sender.Process.Kill()

			// This removes any newlines in the error message.
			msg := strings.ReplaceAll(strings.TrimSpace(recvStderr.String()), "\n", " ")

			if strings.Contains(msg, "does not match incremental source") {
				return ErrSnapshotDoesNotMatchIncrementalSource
			}

			return fmt.Errorf("Failed ZFS receive: %w (%s)", err, msg)
		}

		err = <-senderErr
		if err != nil {
			return err
		}

		return nil
	}

	// This represents the most recent identical snapshot of the source volume and target volume.
	lastIdenticalSnapshot := targetSnapshots[len(targetSnapshots)-1]
	_, lastIdenticalSnapshotOnlyName, _ := api.GetParentAndSnapshotName(lastIdenticalSnapshot.Name())

	// Rollback target volume to the latest identical snapshot
	err = d.RestoreVolume(vol.Volume, lastIdenticalSnapshot, op)
	if err != nil {
		return fmt.Errorf("Failed to restore volume: %w", err)
	}

	// Create all missing snapshots on the target using an incremental stream
	for i, refreshSnapshot := range refreshSnapshots {
		var originSnap Volume

		if i == 0 {
			originSnap, err = srcVol.NewSnapshot(lastIdenticalSnapshotOnlyName)
			if err != nil {
				return fmt.Errorf("Failed to create new snapshot volume: %w", err)
			}
		} else {
			originSnap, err = srcVol.NewSnapshot(refreshSnapshots[i-1])
			if err != nil {
				return fmt.Errorf("Failed to create new snapshot volume: %w", err)
			}
		}

		snap, err := srcVol.NewSnapshot(refreshSnapshot)
		if err != nil {
			return err
		}

		err = transfer(snap, vol.Volume, originSnap)
		if err != nil {
			// Don't fail here. If it's not possible to perform an optimized refresh, do a generic
			// refresh instead.
			if errors.Is(err, ErrSnapshotDoesNotMatchIncrementalSource) {
				d.logger.Debug("Unable to perform an optimized refresh, doing a generic refresh", logger.Ctx{"err": err})
				_, err := genericVFSCopyVolume(d, nil, vol, srcVol, refreshSnapshots, true, allowInconsistent, op)
				return err
			}

			return fmt.Errorf("Failed to transfer snapshot %q: %w", snap.name, err)
		}

		if snap.IsVMBlock() {
			srcFSVol := snap.NewVMBlockFilesystemVolume()
			targetFSVol := vol.NewVMBlockFilesystemVolume()
			originFSVol := originSnap.NewVMBlockFilesystemVolume()

			err = transfer(srcFSVol, targetFSVol, originFSVol)
			if err != nil {
				// Don't fail here. If it's not possible to perform an optimized refresh, do a generic
				// refresh instead.
				if errors.Is(err, ErrSnapshotDoesNotMatchIncrementalSource) {
					d.logger.Debug("Unable to perform an optimized refresh, doing a generic refresh", logger.Ctx{"err": err})
					_, err := genericVFSCopyVolume(d, nil, vol, srcVol, refreshSnapshots, true, allowInconsistent, op)
					return err
				}

				return fmt.Errorf("Failed to transfer snapshot %q: %w", snap.name, err)
			}
		}
	}

	// Create temporary snapshot of the source volume.
	snapUUID := uuid.New().String()

	srcSnap, err := srcVol.NewSnapshot(snapUUID)
	if err != nil {
		return err
	}

	err = d.CreateVolumeSnapshot(srcSnap, op)
	if err != nil {
		return err
	}

	latestSnapVol := srcSnapshotsAll[len(srcSnapshotsAll)-1]

	err = transfer(srcSnap, vol.Volume, latestSnapVol)
	if err != nil {
		// Don't fail here. If it's not possible to perform an optimized refresh, do a generic
		// refresh instead.
		if errors.Is(err, ErrSnapshotDoesNotMatchIncrementalSource) {
			d.logger.Debug("Unable to perform an optimized refresh, doing a generic refresh", logger.Ctx{"err": err})
			_, err := genericVFSCopyVolume(d, nil, vol, srcVol, refreshSnapshots, true, allowInconsistent, op)
			return err
		}

		return fmt.Errorf("Failed to transfer main volume: %w", err)
	}

	if srcSnap.IsVMBlock() {
		srcFSVol := srcSnap.NewVMBlockFilesystemVolume()
		targetFSVol := vol.NewVMBlockFilesystemVolume()
		originFSVol := latestSnapVol.NewVMBlockFilesystemVolume()

		err = transfer(srcFSVol, targetFSVol, originFSVol)
		if err != nil {
			// Don't fail here. If it's not possible to perform an optimized refresh, do a generic
			// refresh instead.
			if errors.Is(err, ErrSnapshotDoesNotMatchIncrementalSource) {
				d.logger.Debug("Unable to perform an optimized refresh, doing a generic refresh", logger.Ctx{"err": err})
				_, err := genericVFSCopyVolume(d, nil, vol, srcVol, refreshSnapshots, true, allowInconsistent, op)
				return err
			}

			return fmt.Errorf("Failed to transfer main volume: %w", err)
		}
	}

	// Restore target volume from main source snapshot.
	err = d.RestoreVolume(vol.Volume, srcSnap, op)
	if err != nil {
		return err
	}

	// Delete temporary source snapshot.
	err = d.DeleteVolumeSnapshot(srcSnap, op)
	if err != nil {
		return err
	}

	// Delete temporary target snapshot.
	targetSnap, err := vol.NewSnapshot(snapUUID)
	if err != nil {
		return err
	}

	err = d.DeleteVolumeSnapshot(targetSnap, op)
	if err != nil {
		return err
	}

	return nil
}

// DeleteVolume deletes a volume of the storage device. If any snapshots of the volume remain then
// this function will return an error.
// For image volumes, both filesystem and block volumes will be removed.
func (d *zfs) DeleteVolume(vol Volume, op *operations.Operation) error {
	if vol.volType == VolumeTypeImage {
		// We need to clone vol the otherwise changing `zfs.block_mode`
		// in tmpVol will also change it in vol.
		tmpVol := vol.Clone()

		for _, filesystem := range blockBackedAllowedFilesystems {
			tmpVol.config["block.filesystem"] = filesystem

			err := d.deleteVolume(tmpVol, op)
			if err != nil {
				return err
			}
		}
	}

	return d.deleteVolume(vol, op)
}

func (d *zfs) deleteVolume(vol Volume, op *operations.Operation) error {
	// Check that we have a dataset to delete.
	exists, err := d.datasetExists(d.dataset(vol, false))
	if err != nil {
		return err
	}

	if exists {
		// Handle clones.
		clones, err := d.getClones(d.dataset(vol, false))
		if err != nil {
			return err
		}

		if len(clones) > 0 {
			// Move to the deleted path.
			_, err := shared.RunCommandContext(context.TODO(), "/proc/self/exe", "forkzfs", "--", "rename", d.dataset(vol, false), d.dataset(vol, true))
			if err != nil {
				return err
			}
		} else {
			err := d.deleteDatasetRecursive(d.dataset(vol, false))
			if err != nil {
				return err
			}
		}
	}

	if vol.contentType == ContentTypeFS {
		// Delete the mountpoint if present.
		err := os.Remove(vol.MountPath())
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Failed to remove '%s': %w", vol.MountPath(), err)
		}

		// Delete the snapshot storage.
		err = os.RemoveAll(GetVolumeSnapshotDir(d.name, vol.volType, vol.name))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Failed to remove '%s': %w", GetVolumeSnapshotDir(d.name, vol.volType, vol.name), err)
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
func (d *zfs) HasVolume(vol Volume) (bool, error) {
	// Check if the dataset exists.
	return d.datasetExists(d.dataset(vol, false))
}

// commonVolumeRules returns validation rules which are common for pool and volume.
func (d *zfs) commonVolumeRules() map[string]func(value string) error {
	return map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-zfs; group=volume-conf; key=block.filesystem)
		// Valid options are: `btrfs`, `ext4`, `xfs`
		// If not set, `ext4` is assumed.
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem` (`zfs.block_mode` enabled)
		//  defaultdesc: same as `volume.block.filesystem`
		//  shortdesc: File system of the storage volume
		//  scope: global
		"block.filesystem": validate.Optional(validate.IsOneOf(blockBackedAllowedFilesystems...)),
		// lxdmeta:generate(entities=storage-zfs; group=volume-conf; key=block.mount_options)
		//
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem` (`zfs.block_mode` enabled)
		//  defaultdesc: same as `volume.block.mount_options`
		//  shortdesc: Mount options for block-backed file system volumes
		//  scope: global
		"block.mount_options": validate.IsAny,
		// lxdmeta:generate(entities=storage-zfs; group=volume-conf; key=zfs.block_mode)
		// `zfs.block_mode` can be set only for custom storage volumes.
		// To enable ZFS block mode for all storage volumes in the pool, including instance volumes,
		// use `volume.zfs.block_mode`.
		// ---
		//  type: bool
		//  defaultdesc: same as `volume.zfs.block_mode`
		//  shortdesc: Whether to use a formatted `zvol` rather than a dataset
		//  scope: global
		"zfs.block_mode": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-zfs; group=volume-conf; key=zfs.blocksize)
		// The size must be between 512 bytes and 16 MiB and must be a power of 2.
		// For a block volume, a maximum value of 128 KiB will be used even if a higher value is set.
		//
		// Depending on the value of {config:option}`storage-zfs-volume-conf:zfs.block_mode`,
		// the specified size is used to set either `volblocksize` or `recordsize` in ZFS.
		// ---
		//  type: string
		//  defaultdesc: same as `volume.zfs.blocksize`
		//  shortdesc: Size of the ZFS block
		//  scope: global
		"zfs.blocksize": validate.Optional(ValidateZfsBlocksize),
		// lxdmeta:generate(entities=storage-zfs; group=volume-conf; key=zfs.remove_snapshots)
		//
		// ---
		//  type: bool
		//  defaultdesc: same as `volume.zfs.remove_snapshots` or `false`
		//  shortdesc: Remove snapshots as needed
		//  scope: global
		"zfs.remove_snapshots": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-zfs; group=volume-conf; key=zfs.reserve_space)
		//
		// ---
		//  type: bool
		//  defaultdesc: same as `volume.zfs.reserve_space` or `false`
		//  shortdesc: Use `reservation`/`refreservation` along with `quota`/`refquota`
		//  scope: global
		"zfs.reserve_space": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-zfs; group=volume-conf; key=zfs.use_refquota)
		//
		// ---
		//  type: bool
		//  defaultdesc: same as `volume.zfs.use_refquota` or `false`
		//  shortdesc: Use `refquota` instead of `quota` for space
		//  scope: global
		"zfs.use_refquota": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-zfs; group=volume-conf; key=zfs.delegate)
		// This option controls whether to delegate the ZFS dataset and anything underneath it to the
		// container or containers that use it. When used in conjunction with
		// {config:option}`instance-security:security.nesting`, this allows
		// using the `zfs` command in the container.
		// ---
		//  type: bool
		//  condition: ZFS 2.2 or higher
		//  defaultdesc: same as `volume.zfs.delegate`
		//  shortdesc: Whether to delegate the ZFS dataset
		//  scope: global
		"zfs.delegate": validate.Optional(validate.IsBool),
	}
}

// ValidateVolume validates the supplied volume config.
func (d *zfs) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	commonRules := d.commonVolumeRules()

	// Disallow block.* settings for regular custom block volumes. These settings only make sense
	// when using custom filesystem volumes with block mode enabled. LXD will create the filesystem
	// for these volumes, and use the mount options. When attaching a regular block volumes to a VM,
	// these are not mounted by LXD and therefore don't need these config keys.
	if vol.IsVMBlock() || vol.volType == VolumeTypeCustom && vol.contentType == ContentTypeBlock {
		delete(commonRules, "zfs.block_mode")
		delete(commonRules, "block.filesystem")
		delete(commonRules, "block.mount_options")
	} else if !vol.IsBlockBacked() && slices.Contains([]VolumeType{VolumeTypeCustom, VolumeTypeBucket}, vol.volType) {
		delete(commonRules, "block.filesystem")
		delete(commonRules, "block.mount_options")
	}

	return d.validateVolume(vol, commonRules, removeUnknownKeys)
}

// UpdateVolume applies config changes to the volume.
func (d *zfs) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	// Mangle the current volume to its old values.
	old := make(map[string]string)
	for k, v := range changedConfig {
		if k == "size" || k == "zfs.use_refquota" || k == "zfs.reserve_space" {
			old[k] = vol.config[k]
			vol.config[k] = v
		}

		if k == "zfs.blocksize" {
			// Convert to bytes.
			sizeBytes, err := units.ParseByteSizeString(v)
			if err != nil {
				return err
			}

			err = d.setBlocksize(vol, sizeBytes)
			if err != nil {
				return err
			}
		}
	}

	defer func() {
		maps.Copy(vol.config, old)
	}()

	// If any of the relevant keys changed, re-apply the quota.
	if len(old) != 0 {
		err := d.SetVolumeQuota(vol, vol.ExpandedConfig("size"), false, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetVolumeUsage returns the disk space used by the volume.
func (d *zfs) GetVolumeUsage(vol Volume) (int64, error) {
	// Determine what key to use.
	key := "used"

	// If volume isn't snapshot then we can take into account the zfs.use_refquota setting.
	// Snapshots should also use the "used" ZFS property because the snapshot usage size represents the CoW
	// usage not the size of the snapshot volume.
	if !vol.IsSnapshot() {
		if shared.IsTrue(vol.ExpandedConfig("zfs.use_refquota")) {
			key = "referenced"
		}

		// Shortcut for mounted refquota filesystems.
		if key == "referenced" && vol.contentType == ContentTypeFS && filesystem.IsMountPoint(vol.MountPath()) {
			var stat unix.Statfs_t
			err := unix.Statfs(vol.MountPath(), &stat)
			if err != nil {
				return -1, err
			}

			return int64(stat.Blocks-stat.Bfree) * int64(stat.Bsize), nil
		}
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

// SetVolumeQuota sets the quota/reservation on the volume.
// Does nothing if supplied with an empty/zero size for block volumes.
func (d *zfs) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	inUse := vol.MountInUse()

	// Handle volume datasets.
	if d.isBlockBacked(vol) && vol.contentType == ContentTypeFS || IsContentBlock(vol.contentType) {
		// Do nothing if size isn't specified.
		if sizeBytes <= 0 {
			return nil
		}

		sizeBytes = d.roundVolumeBlockSizeBytes(vol, sizeBytes)

		oldSizeBytesStr, err := d.getDatasetProperty(d.dataset(vol, false), "volsize")
		if err != nil {
			return err
		}

		oldVolSizeBytesInt, err := strconv.ParseInt(oldSizeBytesStr, 10, 64)
		if err != nil {
			return err
		}

		oldVolSizeBytes := int64(oldVolSizeBytesInt)

		if oldVolSizeBytes == sizeBytes {
			return nil
		}

		if vol.contentType == ContentTypeFS {
			// Activate volume if needed.
			activated, err := d.activateVolume(vol)
			if err != nil {
				return err
			}

			if activated {
				defer func() { _, _ = d.deactivateVolume(vol) }()
			}

			if vol.volType == VolumeTypeImage {
				return fmt.Errorf("Image volumes cannot be resized: %w", ErrCannotBeShrunk)
			}

			fsType := vol.ConfigBlockFilesystem()

			volDevPath, err := d.GetVolumeDiskPath(vol)
			if err != nil {
				return err
			}

			l := d.logger.AddContext(logger.Ctx{"dev": volDevPath, "size": fmt.Sprintf("%db", sizeBytes)})

			if sizeBytes < oldVolSizeBytes {
				if !filesystemTypeCanBeShrunk(fsType) {
					return fmt.Errorf("Filesystem %q cannot be shrunk: %w", fsType, ErrCannotBeShrunk)
				}

				if inUse {
					return ErrInUse // We don't allow online shrinking of filesystem block volumes.
				}

				// Shrink filesystem first.
				// Pass allowUnsafeResize to allow disabling of filesystem resize safety checks.
				err = shrinkFileSystem(fsType, volDevPath, vol, sizeBytes, allowUnsafeResize)
				if err != nil {
					return err
				}

				l.Debug("ZFS volume filesystem shrunk")

				// Shrink the block device.
				err = d.setDatasetProperties(d.dataset(vol, false), fmt.Sprintf("volsize=%d", sizeBytes))
				if err != nil {
					return err
				}
			} else if sizeBytes > oldVolSizeBytes {
				// Grow block device first.
				err = d.setDatasetProperties(d.dataset(vol, false), fmt.Sprintf("volsize=%d", sizeBytes))
				if err != nil {
					return err
				}

				// Grow the filesystem to fill block device.
				err = growFileSystem(fsType, volDevPath, vol)
				if err != nil {
					return err
				}

				l.Debug("ZFS volume filesystem grown")
			}
		} else {
			// Block image volumes cannot be resized because they have a readonly snapshot that doesn't get
			// updated when the volume's size is changed, and this is what instances are created from.
			// During initial volume fill allowUnsafeResize is enabled because snapshot hasn't been taken yet.
			if !allowUnsafeResize && vol.volType == VolumeTypeImage {
				return ErrNotSupported
			}

			// Only perform pre-resize checks if we are not in "unsafe" mode.
			// In unsafe mode we expect the caller to know what they are doing and understand the risks.
			if !allowUnsafeResize {
				if sizeBytes < oldVolSizeBytes {
					return fmt.Errorf("Block volumes cannot be shrunk: %w", ErrCannotBeShrunk)
				}
			}

			err = d.setDatasetProperties(d.dataset(vol, false), fmt.Sprintf("volsize=%d", sizeBytes))
			if err != nil {
				return err
			}
		}

		// Move the VM GPT alt header to end of disk if needed (not needed in unsafe resize mode as
		// it is expected the caller will do all necessary post resize actions themselves).
		if vol.IsVMBlock() && !allowUnsafeResize {
			err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
				devPath, err := d.GetVolumeDiskPath(vol)
				if err != nil {
					return err
				}

				return d.moveGPTAltHeader(devPath)
			}, op)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Clear the existing quota.
	for _, property := range []string{"quota", "refquota", "reservation", "refreservation"} {
		err = d.setDatasetProperties(d.dataset(vol, false), property+"=none")
		if err != nil {
			return err
		}
	}

	value := strconv.FormatInt(sizeBytes, 10)
	if sizeBytes == 0 {
		return nil
	}

	// Apply the new quota.
	quotaKey := "quota"
	reservationKey := "reservation"
	if shared.IsTrue(vol.ExpandedConfig("zfs.use_refquota")) {
		quotaKey = "refquota"
		reservationKey = "refreservation"
	}

	err = d.setDatasetProperties(d.dataset(vol, false), quotaKey+"="+value)
	if err != nil {
		return err
	}

	if shared.IsTrue(vol.ExpandedConfig("zfs.reserve_space")) {
		err = d.setDatasetProperties(d.dataset(vol, false), reservationKey+"="+value)
		if err != nil {
			return err
		}
	}

	return nil
}

// tryGetVolumeDiskPathFromDataset attempts to find the path of the block device for the given dataset.
// It keeps retrying every half a second until the context is canceled or expires.
func (d *zfs) tryGetVolumeDiskPathFromDataset(ctx context.Context, dataset string) (string, error) {
	for {
		if ctx.Err() != nil {
			return "", fmt.Errorf("Failed to locate zvol for %q: %w", dataset, ctx.Err())
		}

		diskPath, err := d.getVolumeDiskPathFromDataset(dataset)
		if err == nil {
			return diskPath, nil
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func (d *zfs) getVolumeDiskPathFromDataset(dataset string) (string, error) {
	zvolUdevLink := filepath.Join("/dev/zvol", dataset)

	// Shortcut for udev.
	if shared.PathExists(zvolUdevLink) && shared.IsBlockdevPath(zvolUdevLink) {
		return zvolUdevLink, nil
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
	entries, err := os.ReadDir("/dev")
	if err != nil {
		return "", fmt.Errorf("Failed to read /dev: %w", err)
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
		output, err := shared.RunCommandContext(context.TODO(), zvolid, entryPath)
		if err != nil {
			continue
		}

		if strings.TrimSpace(output) == dataset && shared.IsBlockdevPath(entryPath) {
			return entryPath, nil
		}
	}

	return "", fmt.Errorf("Could not locate a zvol for %s", dataset)
}

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *zfs) GetVolumeDiskPath(vol Volume) (string, error) {
	// Wait up to 30 seconds for the device to appear.
	// Don't use d.state.ShutdownCtx here as this is used during instance stop during LXD shutdown after it is
	// canceled.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return d.tryGetVolumeDiskPathFromDataset(ctx, d.dataset(vol, false))
}

// ListVolumes returns a list of LXD volumes in storage pool.
func (d *zfs) ListVolumes() ([]Volume, error) {
	vols := make(map[string]Volume)

	// Get just filesystem and volume datasets, not snapshots.
	// The ZFS driver uses two approaches to indicating block volumes; firstly for VM and image volumes it
	// creates both a filesystem dataset and an associated volume ending in zfsBlockVolSuffix.
	// However for custom block volumes it does not also end the volume name in zfsBlockVolSuffix (unlike the
	// LVM and Ceph drivers), so we must also retrieve the dataset type here and look for "volume" types
	// which also indicate this is a block volume.
	cmd := exec.Command("zfs", "list", "-H", "-o", "name,type,lxd:content_type", "-r", "-t", "filesystem,volume", d.config["zfs.pool_name"])
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Splitting fields on tab should be safe as ZFS doesn't appear to allow tabs in dataset names.
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return nil, fmt.Errorf("Unexpected volume line %q", line)
		}

		zfsVolName := parts[0]
		zfsContentType := parts[1]
		lxdContentType := parts[2]

		var volType VolumeType
		var volName string

		for _, volumeType := range d.Info().VolumeTypes {
			prefix := d.config["zfs.pool_name"] + "/" + string(volumeType) + "/"
			if strings.HasPrefix(zfsVolName, prefix) {
				volType = volumeType
				volName = strings.TrimPrefix(zfsVolName, prefix)
			}
		}

		if volType == "" {
			d.logger.Debug("Ignoring unrecognised volume type", logger.Ctx{"name": zfsVolName})
			continue // Ignore unrecognised volume.
		}

		// Detect if a volume is block content type using only the dataset type.
		isBlock := zfsContentType == "volume"

		if volType == VolumeTypeVM && !isBlock {
			continue // Ignore VM filesystem volumes as we will just return the VM's block volume.
		}

		contentType := ContentTypeFS
		if isBlock {
			contentType = ContentTypeBlock
		}

		if volType == VolumeTypeCustom && isBlock && strings.HasSuffix(volName, zfsISOVolSuffix) {
			contentType = ContentTypeISO
			volName = strings.TrimSuffix(volName, zfsISOVolSuffix)
		} else if volType == VolumeTypeVM || isBlock {
			volName = strings.TrimSuffix(volName, zfsBlockVolSuffix)
		}

		// If a new volume has been found, or the volume will replace an existing image filesystem volume
		// then proceed to add the volume to the map. We allow image volumes to overwrite existing
		// filesystem volumes of the same name so that for VM images we only return the block content type
		// volume (so that only the single "logical" volume is returned).
		existingVol, foundExisting := vols[volName]
		if !foundExisting || (existingVol.Type() == VolumeTypeImage && existingVol.ContentType() == ContentTypeFS) {
			v := NewVolume(d, d.name, volType, contentType, volName, make(map[string]string), d.config)

			if isBlock {
				// Get correct content type from lxd:content_type property.
				if lxdContentType != "-" {
					v.contentType = ContentType(lxdContentType)
				}

				if v.contentType == ContentTypeBlock {
					v.SetMountFilesystemProbe(true)
				}
			}

			vols[volName] = v
			continue
		}

		return nil, fmt.Errorf("Unexpected duplicate volume %q found", volName)
	}

	errMsg, err := io.ReadAll(stderr)
	if err != nil {
		return nil, err
	}

	err = cmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("Failed getting volume list: %v: %w", strings.TrimSpace(string(errMsg)), err)
	}

	volList := make([]Volume, 0, len(vols))
	for _, v := range vols {
		volList = append(volList, v)
	}

	return volList, nil
}

// activateVolume activates a ZFS volume if not already active. Returns true if activated, false if not.
func (d *zfs) activateVolume(vol Volume) (bool, error) {
	if !IsContentBlock(vol.contentType) && !vol.IsBlockBacked() {
		return false, nil // Nothing to do for non-block or non-block backed volumes.
	}

	revert := revert.New()
	defer revert.Fail()

	dataset := d.dataset(vol, false)

	// Check if already active.
	current, err := d.getDatasetProperty(dataset, "volmode")
	if err != nil {
		return false, err
	}

	if current != "dev" {
		// For block backed volumes, we make their associated device appear.
		err = d.setDatasetProperties(dataset, "volmode=dev")
		if err != nil {
			return false, err
		}

		revert.Add(func() { _ = d.setDatasetProperties(dataset, "volmode="+current) })

		_, err := d.GetVolumeDiskPath(vol)
		if err != nil {
			return false, fmt.Errorf("Failed to activate volume: %v", err)
		}

		d.logger.Debug("Activated ZFS volume", logger.Ctx{"volName": vol.Name(), "dev": dataset})

		revert.Success()
		return true, nil
	}

	return false, nil
}

// deactivateVolume deactivates a ZFS volume if activate. Returns true if deactivated, false if not.
func (d *zfs) deactivateVolume(vol Volume) (bool, error) {
	if vol.contentType != ContentTypeBlock && !vol.IsBlockBacked() {
		return false, nil // Nothing to do for non-block and non-block backed volumes.
	}

	dataset := d.dataset(vol, false)

	// Check if currently active.
	current, err := d.getDatasetProperty(dataset, "volmode")
	if err != nil {
		return false, err
	}

	if current == "dev" {
		devPath, err := d.GetVolumeDiskPath(vol)
		if err != nil {
			return false, fmt.Errorf("Failed locating zvol for deactivation: %w", err)
		}

		// We cannot wait longer than the operationlock.TimeoutShutdown to avoid continuing
		// the unmount process beyond the ongoing request.
		waitDuration := time.Minute * 5
		waitUntil := time.Now().Add(waitDuration)
		i := 0
		for {
			// Sometimes it takes multiple attempts for ZFS to actually apply this.
			err = d.setDatasetProperties(dataset, "volmode=none")
			if err != nil {
				return false, err
			}

			if !shared.PathExists(devPath) {
				d.logger.Debug("Deactivated ZFS volume", logger.Ctx{"volName": vol.name, "dev": dataset})
				break
			}

			if time.Now().After(waitUntil) {
				return false, fmt.Errorf("Failed to deactivate zvol after %v", waitDuration)
			}

			// Wait for ZFS a chance to flush and udev to remove the device path.
			d.logger.Debug("Waiting for ZFS volume to deactivate", logger.Ctx{"volName": vol.name, "dev": dataset, "path": devPath, "attempt": i})

			if i <= 5 {
				// Retry more quickly early on.
				time.Sleep(time.Second * time.Duration(i))
			} else {
				time.Sleep(time.Second * time.Duration(5))
			}

			i++
		}

		return true, nil
	}

	return false, nil
}

// MountVolume mounts a volume and increments ref counter. Please call UnmountVolume() when done with the volume.
func (d *zfs) MountVolume(vol Volume, op *operations.Operation) error {
	unlock, err := vol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	revert := revert.New()
	defer revert.Fail()

	dataset := d.dataset(vol, false)
	mountPath := vol.MountPath()

	// Check if filesystem volume already mounted.
	if vol.contentType == ContentTypeFS && !d.isBlockBacked(vol) {
		if !filesystem.IsMountPoint(mountPath) {
			err := d.setDatasetProperties(dataset, "mountpoint=legacy", "canmount=noauto")
			if err != nil {
				return err
			}

			if zfsDelegate && shared.IsTrue(vol.config["zfs.delegate"]) {
				err = d.setDatasetProperties(dataset, "zoned=on")
				if err != nil {
					return err
				}
			}

			err = vol.EnsureMountPath()
			if err != nil {
				return err
			}

			var volOptions []string

			props, _ := d.getDatasetProperties(dataset, "atime", "relatime")

			if props["atime"] == "off" {
				volOptions = append(volOptions, "noatime")
			} else if props["relatime"] == "off" {
				volOptions = append(volOptions, "strictatime")
			}

			mountFlags, mountOptions := filesystem.ResolveMountOptions(volOptions)

			// Mount the dataset.
			err = TryMount(context.TODO(), dataset, mountPath, "zfs", mountFlags, mountOptions)
			if err != nil {
				return err
			}

			d.logger.Debug("Mounted ZFS dataset", logger.Ctx{"volName": vol.name, "dev": dataset, "path": mountPath})
		}
	} else {
		// For block devices, we make them appear.
		activated, err := d.activateVolume(vol)
		if err != nil {
			return err
		}

		if activated {
			revert.Add(func() { _, _ = d.deactivateVolume(vol) })
		}

		if !IsContentBlock(vol.contentType) && d.isBlockBacked(vol) && !filesystem.IsMountPoint(mountPath) {
			volPath, err := d.GetVolumeDiskPath(vol)
			if err != nil {
				return err
			}

			err = vol.EnsureMountPath()
			if err != nil {
				return err
			}

			mountFlags, mountOptions := filesystem.ResolveMountOptions(strings.Split(vol.ConfigBlockMountOptions(), ","))

			err = TryMount(context.TODO(), volPath, mountPath, vol.ConfigBlockFilesystem(), mountFlags, mountOptions)
			if err != nil {
				return err
			}

			d.logger.Debug("Mounted ZFS volume", logger.Ctx{"volName": vol.name, "dev": dataset, "path": mountPath})
		}

		if vol.IsVMBlock() {
			// For VMs, also mount the filesystem dataset.
			fsVol := vol.NewVMBlockFilesystemVolume()
			err = d.MountVolume(fsVol, op)
			if err != nil {
				return err
			}
		}
	}

	vol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolume() when done.
	revert.Success()
	return nil
}

// UnmountVolume unmounts volume if mounted and not in use. Returns true if this unmounted the volume.
// keepBlockDev indicates if backing block device should be not be deactivated when volume is unmounted.
func (d *zfs) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	unlock, err := vol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	ourUnmount := false
	dataset := d.dataset(vol, false)
	mountPath := vol.MountPath()

	refCount := vol.MountRefCountDecrement()

	if vol.contentType == ContentTypeFS && filesystem.IsMountPoint(mountPath) {
		if refCount > 0 {
			d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": vol.name, "refCount": refCount})
			return false, ErrInUse
		}

		// Unmount the dataset.
		err = TryUnmount(mountPath, 0)
		if err != nil {
			return false, err
		}

		blockBacked := d.isBlockBacked(vol)
		if blockBacked {
			d.logger.Debug("Unmounted ZFS volume", logger.Ctx{"volName": vol.name, "dev": dataset, "path": mountPath})
		} else {
			d.logger.Debug("Unmounted ZFS dataset", logger.Ctx{"volName": vol.name, "dev": dataset, "path": mountPath})
		}

		if !blockBacked && zfsDelegate && shared.IsTrue(vol.config["zfs.delegate"]) {
			err = d.setDatasetProperties(dataset, "zoned=off")
			if err != nil {
				return false, err
			}
		}

		if blockBacked && !keepBlockDev {
			// For block devices, we make them disappear if active.
			_, err = d.deactivateVolume(vol)
			if err != nil {
				return false, err
			}
		}

		ourUnmount = true
	} else if vol.contentType == ContentTypeBlock {
		// For VMs, also unmount the filesystem dataset.
		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()
			ourUnmount, err = d.UnmountVolume(fsVol, false, op)
			if err != nil {
				return false, err
			}
		}

		if !keepBlockDev {
			if refCount > 0 {
				d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": vol.name, "refCount": refCount})
				return false, ErrInUse
			}

			// For block devices, we make them disappear if active.
			ourUnmount, err = d.deactivateVolume(vol)
			if err != nil {
				return false, err
			}
		}
	}

	return ourUnmount, nil
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
		_ = genericVFSRenameVolume(d, newVol, vol.name, op)
	})

	// Rename the ZFS datasets.
	_, err = shared.RunCommandContext(context.TODO(), "zfs", "rename", d.dataset(vol, false), d.dataset(newVol, false))
	if err != nil {
		return err
	}

	revert.Add(func() {
		_, _ = shared.RunCommandContext(context.TODO(), "zfs", "rename", d.dataset(newVol, false), d.dataset(vol, false))
	})

	// Ensure the volume has correct mountpoint settings.
	if vol.contentType == ContentTypeFS && !d.isBlockBacked(vol) {
		err = d.setDatasetProperties(d.dataset(newVol, false), "mountpoint=legacy", "canmount=noauto")
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
			_ = d.RenameVolume(newFsVol, vol.name, op)
		})
	}

	// All done.
	revert.Success()

	return nil
}

// CanDelegateVolume checks whether the volume may be delegated.
func (d *zfs) CanDelegateVolume(vol Volume) bool {
	// Not applicable for block backed volumes.
	if d.isBlockBacked(vol) {
		return false
	}

	// Check that the volume has it enabled.
	if shared.IsFalseOrEmpty(vol.Config()["zfs.delegate"]) {
		return false
	}

	return true
}

// DelegateVolume allows for the volume to be managed by the instance itself.
func (d *zfs) DelegateVolume(vol Volume, pid int) error {
	if !d.CanDelegateVolume(vol) {
		return nil
	}

	// Check that the current ZFS version supports it.
	if !zfsDelegate {
		return errors.New("Local ZFS version doesn't support delegation")
	}

	// Set the property.
	err := d.delegateDataset(vol, pid)
	if err != nil {
		return err
	}

	return nil
}

// MigrateVolume sends a volume for migration.
func (d *zfs) MigrateVolume(vol VolumeCopy, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	if !volSrcArgs.AllowInconsistent && vol.contentType == ContentTypeFS && vol.IsBlockBacked() {
		// When migrating using zfs volumes (not datasets), ensure that the filesystem is synced
		// otherwise the source and target volumes may differ. Tests have shown that only calling
		// os.SyncFS() doesn't suffice. A freeze and unfreeze is needed.
		err := vol.MountTask(func(mountPath string, op *operations.Operation) error {
			unfreezeFS, err := d.filesystemFreeze(mountPath)
			if err != nil {
				return err
			}

			return unfreezeFS()
		}, op)
		if err != nil {
			return err
		}
	}

	// Handle simple rsync and block_and_rsync through generic.
	if volSrcArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC || volSrcArgs.MigrationType.FSType == migration.MigrationFSType_BLOCK_AND_RSYNC {
		// If volume is filesystem type, create a fast snapshot to ensure migration is consistent.
		// TODO add support for temporary snapshots of block volumes here.
		if vol.contentType == ContentTypeFS && !vol.IsSnapshot() {
			snapshotPath, cleanup, err := d.readonlySnapshot(vol.Volume)
			if err != nil {
				return err
			}

			// Clean up the snapshot.
			defer cleanup()

			// Set the path of the volume to the path of the fast snapshot so the migration reads from there instead.
			vol.mountCustomPath = snapshotPath
		}

		return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
	} else if volSrcArgs.MigrationType.FSType != migration.MigrationFSType_ZFS {
		return ErrNotSupported
	}

	// Handle zfs send/receive migration.
	if volSrcArgs.MultiSync || volSrcArgs.FinalSync {
		// This is not needed if the migration is performed using zfs send/receive.
		return errors.New("MultiSync should not be used with optimized migration")
	}

	var srcMigrationHeader *ZFSMetaDataHeader

	// The target will validate the GUIDs and if successful proceed with the refresh.
	if slices.Contains(volSrcArgs.MigrationType.Features, migration.ZFSFeatureMigrationHeader) {
		snapshots, err := d.VolumeSnapshots(vol.Volume, op)
		if err != nil {
			return err
		}

		// Fill the migration header with the snapshot names and dataset GUIDs.
		srcMigrationHeader, err = d.datasetHeader(vol.Volume, snapshots)
		if err != nil {
			return err
		}

		headerJSON, err := json.Marshal(srcMigrationHeader)
		if err != nil {
			return fmt.Errorf("Failed encoding ZFS migration header: %w", err)
		}

		// Send the migration header to the target.
		_, err = conn.Write(headerJSON)
		if err != nil {
			return fmt.Errorf("Failed sending ZFS migration header: %w", err)
		}

		err = conn.Close() // End the frame.
		if err != nil {
			return fmt.Errorf("Failed closing ZFS migration header frame: %w", err)
		}
	}

	// If we haven't negotiated zvol support, ensure volume is not a zvol.
	if !slices.Contains(volSrcArgs.MigrationType.Features, migration.ZFSFeatureZvolFilesystems) && d.isBlockBacked(vol.Volume) {
		return errors.New("Filesystem zvol detected in source but target does not support receiving zvols")
	}

	incrementalStream := true
	var migrationHeader ZFSMetaDataHeader

	if volSrcArgs.Refresh && slices.Contains(volSrcArgs.MigrationType.Features, migration.ZFSFeatureMigrationHeader) {
		buf, err := io.ReadAll(conn)
		if err != nil {
			return fmt.Errorf("Failed reading ZFS migration header: %w", err)
		}

		err = json.Unmarshal(buf, &migrationHeader)
		if err != nil {
			return fmt.Errorf("Failed decoding ZFS migration header: %w", err)
		}

		// If the target has no snapshots we cannot use incremental streams and will do a normal copy operation instead.
		if len(migrationHeader.SnapshotDatasets) == 0 {
			incrementalStream = false
			volSrcArgs.Refresh = false
		}

		volSrcArgs.Snapshots = []string{}

		// Override volSrcArgs.Snapshots to only include snapshots which need to be sent.
		if !volSrcArgs.VolumeOnly {
			for _, srcDataset := range srcMigrationHeader.SnapshotDatasets {
				found := false

				for _, dstDataset := range migrationHeader.SnapshotDatasets {
					if srcDataset.GUID == dstDataset.GUID {
						found = true
						break
					}
				}

				if !found {
					volSrcArgs.Snapshots = append(volSrcArgs.Snapshots, srcDataset.Name)
				}
			}
		}
	}

	return d.migrateVolumeOptimized(vol.Volume, conn, volSrcArgs, incrementalStream, op)
}

func (d *zfs) migrateVolumeOptimized(vol Volume, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, incremental bool, op *operations.Operation) error {
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.migrateVolumeOptimized(fsVol, conn, volSrcArgs, incremental, op)
		if err != nil {
			return err
		}
	}

	// Handle zfs send/receive migration.
	var finalParent string

	// Transfer the snapshots first.
	for i, snapName := range volSrcArgs.Snapshots {
		snapshot, _ := vol.NewSnapshot(snapName)

		// Figure out parent and current subvolumes.
		parent := ""
		if i == 0 && volSrcArgs.Refresh {
			snapshots, err := vol.Snapshots(op)
			if err != nil {
				return err
			}

			for k, snap := range snapshots {
				if k == 0 {
					continue
				}

				if snap.name == vol.name+"/"+snapName {
					parent = d.dataset(snapshots[k-1], false)
					break
				}
			}
		} else if i > 0 {
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

	// Setup progress tracking.
	var wrapper *ioprogress.ProgressTracker
	if volSrcArgs.TrackProgress {
		wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
	}

	srcSnapshot := d.dataset(vol, false)
	if !vol.IsSnapshot() {
		// Create a temporary read-only snapshot.
		srcSnapshot = d.dataset(vol, false) + "@migration-" + uuid.New().String()
		_, err := shared.RunCommandContext(context.TODO(), "zfs", "snapshot", "-r", srcSnapshot)
		if err != nil {
			return err
		}

		defer func() {
			// Delete snapshot (or mark for deferred deletion if cannot be deleted currently).
			_, err := shared.RunCommandContext(context.TODO(), "zfs", "destroy", "-r", "-d", srcSnapshot)
			if err != nil {
				d.logger.Warn("Failed deleting temporary snapshot for migration", logger.Ctx{"snapshot": srcSnapshot, "err": err})
			}
		}()
	}

	// Get parent snapshot of the main volume which can then be used to send an incremental stream.
	if volSrcArgs.Refresh && incremental {
		localSnapshots, err := vol.Snapshots(op)
		if err != nil {
			return err
		}

		if len(localSnapshots) > 0 {
			finalParent = d.dataset(localSnapshots[len(localSnapshots)-1], false)
		}
	}

	// Send the volume itself.
	err := d.sendDataset(srcSnapshot, finalParent, volSrcArgs, conn, wrapper)
	if err != nil {
		return err
	}

	return nil
}

func (d *zfs) readonlySnapshot(vol Volume) (string, revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	poolPath := GetPoolMountPath(d.name)
	tmpDir, err := os.MkdirTemp(poolPath, "backup.")
	if err != nil {
		return "", nil, err
	}

	revert.Add(func() {
		_ = os.RemoveAll(tmpDir)
	})

	err = os.Chmod(tmpDir, 0100)
	if err != nil {
		return "", nil, err
	}

	snapshotOnlyName := "temp_ro-" + uuid.New().String()

	snapVol, err := vol.NewSnapshot(snapshotOnlyName)
	if err != nil {
		return "", nil, err
	}

	snapshotDataset := d.dataset(vol, false) + "@" + snapshotOnlyName

	// Create a temporary snapshot.
	_, err = shared.RunCommandContext(context.TODO(), "zfs", "snapshot", "-r", snapshotDataset)
	if err != nil {
		return "", nil, err
	}

	revert.Add(func() {
		// Delete snapshot (or mark for deferred deletion if cannot be deleted currently).
		_, err := shared.RunCommandContext(context.TODO(), "zfs", "destroy", "-r", "-d", snapshotDataset)
		if err != nil {
			d.logger.Warn("Failed deleting read-only snapshot", logger.Ctx{"snapshot": snapshotDataset, "err": err})
		}
	})

	hook, err := d.mountVolumeSnapshot(snapVol, snapshotDataset, tmpDir, nil)
	if err != nil {
		return "", nil, err
	}

	revert.Add(hook)

	cleanup := revert.Clone().Fail
	revert.Success()
	return tmpDir, cleanup, nil
}

// BackupVolume creates an exported version of a volume.
func (d *zfs) BackupVolume(vol VolumeCopy, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error {
	// Handle the non-optimized tarballs through the generic packer.
	if !optimized {
		// Because the generic backup method will not take a consistent backup if files are being modified
		// as they are copied to the tarball, as ZFS allows us to take a quick snapshot without impacting
		// the parent volume we do so here to ensure the backup taken is consistent.
		if vol.contentType == ContentTypeFS && !d.isBlockBacked(vol.Volume) {
			snapshotPath, cleanup, err := d.readonlySnapshot(vol.Volume)
			if err != nil {
				return err
			}

			// Clean up the snapshot.
			defer cleanup()

			// Set the path of the volume to the path of the fast snapshot so the migration reads from there instead.
			vol.mountCustomPath = snapshotPath
		}

		return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
	}

	// Optimized backup.

	if len(snapshots) > 0 {
		// Check requested snapshot match those in storage.
		err := d.CheckVolumeSnapshots(vol.Volume, vol.Snapshots, op)
		if err != nil {
			return err
		}
	}

	// Backup VM config volumes first.
	if vol.IsVMBlock() {
		fsVol := NewVolumeCopy(vol.NewVMBlockFilesystemVolume())
		err := d.BackupVolume(fsVol, tarWriter, optimized, snapshots, op)
		if err != nil {
			return err
		}
	}

	// Handle the optimized tarballs.
	sendToFile := func(path string, parent string, fileName string) error {
		// Prepare zfs send arguments.
		args := []string{"send"}

		// Check if nesting is required.
		if d.needsRecursion(path) {
			args = append(args, "-R")

			if zfsRaw {
				args = append(args, "-w")
			}
		}

		if parent != "" {
			args = append(args, "-i", parent)
		}

		args = append(args, path)

		// Create temporary file to store output of ZFS send.
		tmpFile, err := os.CreateTemp(d.state.BackupsStoragePath(), backup.WorkingDirPrefix+"_zfs")
		if err != nil {
			return fmt.Errorf("Failed to open temporary file for ZFS backup: %w", err)
		}

		defer func() { _ = tmpFile.Close() }()
		defer func() { _ = os.Remove(tmpFile.Name()) }()

		// Write the subvolume to the file.
		d.logger.Debug("Generating optimized volume file", logger.Ctx{"sourcePath": path, "file": tmpFile.Name(), "name": fileName})

		// Write the subvolume to the file.
		err = shared.RunCommandWithFds(context.TODO(), nil, tmpFile, "zfs", args...)
		if err != nil {
			return err
		}

		// Get info (importantly size) of the generated file for tarball header.
		tmpFileInfo, err := os.Lstat(tmpFile.Name())
		if err != nil {
			return err
		}

		err = tarWriter.WriteFile(fileName, tmpFile.Name(), tmpFileInfo, false)
		if err != nil {
			return err
		}

		return tmpFile.Close()
	}

	// Handle snapshots.
	finalParent := ""
	if len(snapshots) > 0 {
		for i, snapName := range snapshots {
			snapshot, _ := vol.NewSnapshot(snapName)

			// Figure out parent and current subvolumes.
			parent := ""
			if i > 0 {
				oldSnapshot, _ := vol.NewSnapshot(snapshots[i-1])
				parent = d.dataset(oldSnapshot, false)
			}

			// Make a binary zfs backup.
			prefix := "snapshots"
			fileName := snapName + ".bin"
			switch vol.volType {
			case VolumeTypeVM:
				prefix = "virtual-machine-snapshots"
				if vol.contentType == ContentTypeFS {
					fileName = snapName + "-config.bin"
				}

			case VolumeTypeCustom:
				prefix = "volume-snapshots"
			}

			target := "backup/" + prefix + "/" + fileName
			err := sendToFile(d.dataset(snapshot, false), parent, target)
			if err != nil {
				return err
			}

			finalParent = d.dataset(snapshot, false)
		}
	}

	// Create a temporary read-only snapshot.
	srcSnapshot := d.dataset(vol.Volume, false) + "@backup-" + uuid.New().String()
	_, err := shared.RunCommandContext(context.TODO(), "zfs", "snapshot", "-r", srcSnapshot)
	if err != nil {
		return err
	}

	defer func() {
		// Delete snapshot (or mark for deferred deletion if cannot be deleted currently).
		_, err := shared.RunCommandContext(context.TODO(), "zfs", "destroy", "-r", "-d", srcSnapshot)
		if err != nil {
			d.logger.Warn("Failed deleting temporary snapshot for backup", logger.Ctx{"snapshot": srcSnapshot, "err": err})
		}
	}()

	// Dump the container to a file.
	fileName := "container.bin"
	switch vol.volType {
	case VolumeTypeVM:
		if vol.contentType == ContentTypeFS {
			fileName = "virtual-machine-config.bin"
		} else {
			fileName = "virtual-machine.bin"
		}

	case VolumeTypeCustom:
		fileName = "volume.bin"
	}

	err = sendToFile(srcSnapshot, finalParent, "backup/"+fileName)
	if err != nil {
		return err
	}

	return nil
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *zfs) CreateVolumeSnapshot(vol Volume, op *operations.Operation) error {
	parentName, _, _ := api.GetParentAndSnapshotName(vol.name)

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
	_, err = shared.RunCommandContext(context.TODO(), "zfs", "snapshot", "-r", d.dataset(vol, false))
	if err != nil {
		return err
	}

	revert.Add(func() { _ = d.DeleteVolumeSnapshot(vol, op) })

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.CreateVolumeSnapshot(fsVol, op)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.DeleteVolumeSnapshot(fsVol, op) })
	}

	// All done.
	revert.Success()

	return nil
}

// DeleteVolumeSnapshot removes a snapshot from the storage device.
func (d *zfs) DeleteVolumeSnapshot(vol Volume, op *operations.Operation) error {
	parentName, _, _ := api.GetParentAndSnapshotName(vol.name)

	// Handle clones.
	clones, err := d.getClones(d.dataset(vol, false))
	if err != nil {
		return err
	}

	if len(clones) > 0 {
		// Move to the deleted path.
		_, err := shared.RunCommandContext(context.TODO(), "zfs", "rename", d.dataset(vol, false), d.dataset(vol, true))
		if err != nil {
			return err
		}
	} else {
		// Delete the snapshot.
		_, err := shared.RunCommandContext(context.TODO(), "zfs", "destroy", "-r", d.dataset(vol, false))
		if err != nil {
			return err
		}
	}

	// Delete the mountpoint.
	err = os.Remove(vol.MountPath())
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove '%s': %w", vol.MountPath(), err)
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
func (d *zfs) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	unlock, err := snapVol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	_, err = d.mountVolumeSnapshot(snapVol, d.dataset(snapVol, false), snapVol.MountPath(), op)
	if err != nil {
		return err
	}

	snapVol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolumeSnapshot() when done.
	return nil
}

func (d *zfs) mountVolumeSnapshot(snapVol Volume, snapshotDataset string, mountPath string, op *operations.Operation) (revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Check if filesystem volume already mounted.
	if snapVol.contentType == ContentTypeFS && !d.isBlockBacked(snapVol) {
		if !filesystem.IsMountPoint(mountPath) {
			err := snapVol.EnsureMountPath()
			if err != nil {
				return nil, err
			}

			// Mount the snapshot directly (not possible through tools).
			err = TryMount(context.TODO(), snapshotDataset, mountPath, "zfs", unix.MS_RDONLY, "")
			if err != nil {
				return nil, err
			}

			d.logger.Debug("Mounted ZFS snapshot dataset", logger.Ctx{"dev": snapshotDataset, "path": mountPath})
		}
	} else {
		// For block devices, we make them appear by enabling volmode=dev and snapdev=visible on the parent volume.
		// Ensure snap volume parent is activated to avoid issues activating the snapshot volume device.
		parent, snapshotOnlyName, _ := api.GetParentAndSnapshotName(snapVol.Name())
		parentVol := NewVolume(d, d.Name(), snapVol.volType, snapVol.contentType, parent, snapVol.config, snapVol.poolConfig)

		err := d.MountVolume(parentVol, op)
		if err != nil {
			return nil, err
		}

		revert.Add(func() { _, _ = d.UnmountVolume(parentVol, false, op) })

		parentDataset := d.dataset(parentVol, false)

		// Check if parent already active.
		parentVolMode, err := d.getDatasetProperty(parentDataset, "volmode")
		if err != nil {
			return nil, err
		}

		// Order is important here, the parent volmode=dev must be set before snapdev=visible otherwise
		// it won't take effect.
		if parentVolMode != "dev" {
			return nil, errors.New("Parent block volume needs to be mounted first")
		}

		// Check if snapdev already set visible.
		parentSnapdevMode, err := d.getDatasetProperty(parentDataset, "snapdev")
		if err != nil {
			return nil, err
		}

		if parentSnapdevMode != "visible" {
			err = d.setDatasetProperties(parentDataset, "snapdev=visible")
			if err != nil {
				return nil, err
			}

			// Wait half a second to give udev a chance to kick in.
			time.Sleep(500 * time.Millisecond)

			d.logger.Debug("Activated ZFS snapshot volume", logger.Ctx{"dev": snapshotDataset})
		}

		if snapVol.contentType != ContentTypeBlock && d.isBlockBacked(snapVol) && !filesystem.IsMountPoint(mountPath) {
			err = snapVol.EnsureMountPath()
			if err != nil {
				return nil, err
			}

			mountVol := snapVol
			mountFlags, mountOptions := filesystem.ResolveMountOptions(strings.Split(mountVol.ConfigBlockMountOptions(), ","))

			dataset := snapshotDataset

			// Regenerate filesystem UUID if needed. This is because some filesystems do not allow mounting
			// multiple volumes that share the same UUID. As snapshotting a volume will copy its UUID we need
			// to potentially regenerate the UUID of the snapshot now that we are trying to mount it.
			// This is done at mount time rather than snapshot time for 2 reasons; firstly snapshots need to be
			// as fast as possible, and on some filesystems regenerating the UUID is a slow process, secondly
			// we do not want to modify a snapshot in case it is corrupted for some reason, so at mount time
			// we take another snapshot of the snapshot, regenerate the temporary snapshot's UUID and then
			// mount that.
			regenerateFSUUID := renegerateFilesystemUUIDNeeded(snapVol.ConfigBlockFilesystem())
			if regenerateFSUUID {
				// Instantiate a new volume to be the temporary writable snapshot.
				tmpVolName := snapVol.name + tmpVolSuffix
				tmpVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, tmpVolName, snapVol.config, snapVol.poolConfig)

				dataset = parentDataset + "_" + snapshotOnlyName + tmpVolSuffix

				// Clone snapshot.
				_, err = shared.RunCommandContext(context.TODO(), "zfs", "clone", snapshotDataset, dataset)
				if err != nil {
					return nil, err
				}

				// Delete on revert.
				revert.Add(func() { _ = d.deleteDatasetRecursive(dataset) })

				err := d.setDatasetProperties(dataset, "volmode=dev")
				if err != nil {
					return nil, err
				}

				defer func() {
					_ = d.setDatasetProperties(dataset, "volmode=none")
				}()

				// Wait half a second to give udev a chance to kick in.
				time.Sleep(500 * time.Millisecond)

				d.logger.Debug("Activated ZFS volume", logger.Ctx{"dev": dataset})

				// We are going to mount the temporary volume instead.
				mountVol = tmpVol
			}

			volPath, err := d.getVolumeDiskPathFromDataset(dataset)
			if err != nil {
				return nil, err
			}

			tmpVolFsType := mountVol.ConfigBlockFilesystem()

			if regenerateFSUUID {
				// When mounting XFS filesystems temporarily we can use the nouuid option rather than fully
				// regenerating the filesystem UUID.
				if tmpVolFsType == "xfs" {
					idx := strings.Index(mountOptions, "nouuid")
					if idx < 0 {
						mountOptions += ",nouuid"
					}
				} else {
					d.logger.Debug("Regenerating filesystem UUID", logger.Ctx{"dev": volPath, "fs": tmpVolFsType})
					err = regenerateFilesystemUUID(mountVol.ConfigBlockFilesystem(), volPath)
					if err != nil {
						return nil, err
					}
				}
			} else {
				// ext4 will replay the journal if the filesystem is dirty.
				// To prevent this kind of write access, we mount the ext4 filesystem
				// with the ro,noload mount options.
				// The noload option prevents the journal from being loaded on mounting.
				if tmpVolFsType == "ext4" {
					idx := strings.Index(mountOptions, "noload")
					if idx < 0 {
						mountOptions += ",noload"
					}
				}
			}

			err = TryMount(context.TODO(), volPath, mountPath, mountVol.ConfigBlockFilesystem(), mountFlags|unix.MS_RDONLY, mountOptions)
			if err != nil {
				return nil, fmt.Errorf("Failed mounting volume snapshot: %w", err)
			}
		}

		if snapVol.IsVMBlock() {
			// For VMs, also mount the filesystem dataset.
			fsVol := snapVol.NewVMBlockFilesystemVolume()
			err = d.MountVolumeSnapshot(fsVol, op)
			if err != nil {
				return nil, err
			}
		}
	}

	d.logger.Debug("Mounted ZFS snapshot dataset", logger.Ctx{"dev": snapshotDataset, "path": mountPath})

	revert.Add(func() {
		_, err := forceUnmount(mountPath)
		if err != nil {
			return
		}

		d.logger.Debug("Unmounted ZFS snapshot dataset", logger.Ctx{"dev": snapshotDataset, "path": mountPath})
	})

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, nil
}

// UnmountVolumeSnapshot simulates unmounting a volume snapshot.
func (d *zfs) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	unlock, err := snapVol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	ourUnmount := false
	mountPath := snapVol.MountPath()
	snapshotDataset := d.dataset(snapVol, false)

	refCount := snapVol.MountRefCountDecrement()

	// For block devices, we make them disappear.
	if snapVol.contentType == ContentTypeBlock || snapVol.contentType == ContentTypeFS && d.isBlockBacked(snapVol) {
		// For VMs, also mount the filesystem dataset.
		if snapVol.IsVMBlock() {
			fsSnapVol := snapVol.NewVMBlockFilesystemVolume()
			ourUnmount, err = d.UnmountVolumeSnapshot(fsSnapVol, op)
			if err != nil {
				return false, err
			}
		}

		if snapVol.contentType == ContentTypeFS && d.isBlockBacked(snapVol) && filesystem.IsMountPoint(mountPath) {
			if refCount > 0 {
				d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": snapVol.name, "refCount": refCount})
				return false, ErrInUse
			}

			_, err := forceUnmount(mountPath)
			if err != nil {
				return false, err
			}

			d.logger.Debug("Unmounted ZFS snapshot dataset", logger.Ctx{"dev": snapshotDataset, "path": mountPath})
			ourUnmount = true

			parent, snapshotOnlyName, _ := api.GetParentAndSnapshotName(snapVol.Name())
			parentVol := NewVolume(d, d.Name(), snapVol.volType, snapVol.contentType, parent, snapVol.config, snapVol.poolConfig)
			parentDataset := d.dataset(parentVol, false)
			dataset := parentDataset + "_" + snapshotOnlyName + tmpVolSuffix

			exists, err := d.datasetExists(dataset)
			if err != nil {
				return true, fmt.Errorf("Failed to check existence of temporary ZFS snapshot volume %q: %w", dataset, err)
			}

			if exists {
				err = d.deleteDatasetRecursive(dataset)
				if err != nil {
					return true, err
				}
			}
		}

		parent, _, _ := api.GetParentAndSnapshotName(snapVol.Name())
		parentVol := NewVolume(d, d.Name(), snapVol.volType, snapVol.contentType, parent, snapVol.config, snapVol.poolConfig)
		parentDataset := d.dataset(parentVol, false)

		current, err := d.getDatasetProperty(parentDataset, "snapdev")
		if err != nil {
			return false, err
		}

		if current == "visible" {
			if refCount > 0 {
				d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": snapVol.name, "refCount": refCount})
				return false, ErrInUse
			}

			err := d.setDatasetProperties(parentDataset, "snapdev=hidden")
			if err != nil {
				return false, err
			}

			d.logger.Debug("Deactivated ZFS snapshot volume", logger.Ctx{"dev": snapshotDataset})

			// Ensure snap volume parent is deactivated in case we activated it when mounting snapshot.
			_, err = d.UnmountVolume(parentVol, false, op)
			if err != nil {
				return false, err
			}

			ourUnmount = true
		}
	} else if snapVol.contentType == ContentTypeFS && filesystem.IsMountPoint(mountPath) {
		if refCount > 0 {
			d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": snapVol.name, "refCount": refCount})
			return false, ErrInUse
		}

		_, err := forceUnmount(mountPath)
		if err != nil {
			return false, err
		}

		d.logger.Debug("Unmounted ZFS snapshot dataset", logger.Ctx{"dev": snapshotDataset, "path": mountPath})
		ourUnmount = true
	}

	return ourUnmount, nil
}

// VolumeSnapshots returns a list of snapshots for the volume (in no particular order).
func (d *zfs) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	// Get all children datasets.
	entries, err := d.getDatasets(d.dataset(vol, false), "snapshot")
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
func (d *zfs) RestoreVolume(vol Volume, snapVol Volume, op *operations.Operation) error {
	return d.restoreVolume(vol, snapVol, false, op)
}

func (d *zfs) restoreVolume(vol Volume, snapVol Volume, migration bool, op *operations.Operation) error {
	// Get the list of snapshots.
	entries, err := d.getDatasets(d.dataset(vol, false), "snapshot")
	if err != nil {
		return err
	}

	_, snapshotName, _ := api.GetParentAndSnapshotName(snapVol.name)

	// Check if more recent snapshots exist.
	idx := -1
	snapshots := []string{}
	for i, entry := range entries {
		if entry == "@snapshot-"+snapshotName {
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
			return fmt.Errorf("Snapshot %q cannot be restored due to subsequent internal snapshot(s) (from a copy)", snapshotName)
		}
	}

	// Check if snapshot removal is allowed.
	if len(snapshots) > 0 {
		if shared.IsFalseOrEmpty(vol.ExpandedConfig("zfs.remove_snapshots")) {
			return fmt.Errorf("Snapshot %q cannot be restored due to subsequent snapshot(s). Set zfs.remove_snapshots to override", snapshotName)
		}

		// Setup custom error to tell the backend what to delete.
		err := ErrDeleteSnapshots{}
		err.Snapshots = snapshots
		return err
	}

	// Restore the snapshot.
	datasets, err := d.getDatasets(d.dataset(vol, false), "snapshot")
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		if !strings.HasSuffix(dataset, "@snapshot-"+snapshotName) {
			continue
		}

		_, err = shared.RunCommandContext(context.TODO(), "zfs", "rollback", d.dataset(vol, false)+dataset)
		if err != nil {
			return err
		}
	}

	if vol.contentType == ContentTypeFS && d.isBlockBacked(vol) && renegerateFilesystemUUIDNeeded(vol.ConfigBlockFilesystem()) {
		_, err = d.activateVolume(vol)
		if err != nil {
			return err
		}

		defer func() { _, _ = d.deactivateVolume(vol) }()

		volPath, err := d.GetVolumeDiskPath(vol)
		if err != nil {
			return err
		}

		d.logger.Debug("Regenerating filesystem UUID", logger.Ctx{"dev": volPath, "fs": vol.ConfigBlockFilesystem()})
		err = regenerateFilesystemUUID(vol.ConfigBlockFilesystem(), volPath)
		if err != nil {
			return err
		}
	}

	// For VM images, restore the associated filesystem dataset too.
	if !migration && vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		fsSnapVol := snapVol.NewVMBlockFilesystemVolume()
		err := d.restoreVolume(fsVol, fsSnapVol, migration, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *zfs) RenameVolumeSnapshot(vol Volume, newSnapshotName string, op *operations.Operation) error {
	parentName, _, _ := api.GetParentAndSnapshotName(vol.name)
	newVol := NewVolume(d, d.name, vol.volType, vol.contentType, parentName+"/"+newSnapshotName, vol.config, vol.poolConfig)

	// Revert handling.
	revert := revert.New()
	defer revert.Fail()

	// First rename the VFS paths.
	err := genericVFSRenameVolumeSnapshot(d, vol, newSnapshotName, op)
	if err != nil {
		return err
	}

	revert.Add(func() {
		_ = genericVFSRenameVolumeSnapshot(d, newVol, vol.name, op)
	})

	// Rename the ZFS datasets.
	_, err = shared.RunCommandContext(context.TODO(), "zfs", "rename", d.dataset(vol, false), d.dataset(newVol, false))
	if err != nil {
		return err
	}

	revert.Add(func() {
		_, _ = shared.RunCommandContext(context.TODO(), "zfs", "rename", d.dataset(newVol, false), d.dataset(vol, false))
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
			_ = d.RenameVolumeSnapshot(newFsVol, vol.name, op)
		})
	}

	// All done.
	revert.Success()

	return nil
}

// FillVolumeConfig populate volume with default config.
func (d *zfs) FillVolumeConfig(vol Volume) error {
	var excludedKeys []string

	// Copy volume.* configuration options from pool.
	// If vol has a source, ignore the block mode related config keys from the pool.
	if vol.hasSource || vol.IsVMBlock() || vol.volType == VolumeTypeCustom && vol.contentType == ContentTypeBlock {
		excludedKeys = []string{"zfs.block_mode", "block.filesystem", "block.mount_options"}
	} else if vol.volType == VolumeTypeCustom && !vol.IsBlockBacked() {
		excludedKeys = []string{"block.filesystem", "block.mount_options"}
	}

	err := d.fillVolumeConfig(&vol, excludedKeys...)
	if err != nil {
		return err
	}

	// Only validate filesystem config keys for filesystem volumes.
	if d.isBlockBacked(vol) && vol.ContentType() == ContentTypeFS {
		// Inherit block mode from pool if not set.
		if vol.config["zfs.block_mode"] == "" {
			vol.config["zfs.block_mode"] = d.config["volume.zfs.block_mode"]
		}

		// Inherit filesystem from pool if not set.
		if vol.config["block.filesystem"] == "" {
			vol.config["block.filesystem"] = d.config["volume.block.filesystem"]
		}

		// Default filesystem if neither volume nor pool specify an override.
		if vol.config["block.filesystem"] == "" {
			// Unchangeable volume property: Set unconditionally.
			vol.config["block.filesystem"] = DefaultFilesystem
		}

		// Inherit filesystem mount options from pool if not set.
		if vol.config["block.mount_options"] == "" {
			vol.config["block.mount_options"] = d.config["volume.block.mount_options"]
		}

		// Default filesystem mount options if neither volume nor pool specify an override.
		if vol.config["block.mount_options"] == "" {
			// Unchangeable volume property: Set unconditionally.
			vol.config["block.mount_options"] = "discard"
		}
	}

	return nil
}

func (d *zfs) isBlockBacked(vol Volume) bool {
	return shared.IsTrue(vol.Config()["zfs.block_mode"])
}
