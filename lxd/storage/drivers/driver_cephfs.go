package drivers

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/units"
)

var cephfsVersion string

type cephfs struct {
	common
}

func (d *cephfs) Info() Info {
	// Detect and record the version.
	if cephfsVersion == "" {
		msg, err := shared.RunCommand("rbd", "--version")
		if err != nil {
			cephfsVersion = "unknown"
		} else {
			cephfsVersion = strings.TrimSpace(msg)
		}
	}

	return Info{
		Name:               "cephfs",
		Version:            cephfsVersion,
		Usable:             true,
		Remote:             true,
		OptimizedImages:    false,
		PreservesInodes:    false,
		VolumeTypes:        []VolumeType{VolumeTypeCustom},
		BlockBacking:       false,
		RunningQuotaResize: true,
	}
}

func (d *cephfs) HasVolume(volType VolumeType, volName string) bool {
	if shared.PathExists(GetVolumeMountPath(d.name, volType, volName)) {
		return true
	}

	return false
}

func (d *cephfs) Create() error {
	if d.config["source"] == "" {
		return fmt.Errorf("Missing required source name/path")
	}

	if d.config["cephfs.path"] != "" && d.config["cephfs.path"] != d.config["source"] {
		return fmt.Errorf("cephfs.path must match the source")
	}

	if d.config["cephfs.cluster_name"] == "" {
		d.config["cephfs.cluster_name"] = "ceph"
	}

	if d.config["cephfs.user.name"] == "" {
		d.config["cephfs.user.name"] = "admin"
	}

	d.config["cephfs.path"] = d.config["source"]

	// Parse the namespace / path.
	fields := strings.SplitN(d.config["cephfs.path"], "/", 2)
	fsName := fields[0]
	fsPath := "/"
	if len(fields) > 1 {
		fsPath = fields[1]
	}

	// Check that the filesystem exists.
	if !d.fsExists(d.config["cephfs.cluster_name"], d.config["cephfs.user.name"], fsName) {
		return fmt.Errorf("The requested '%v' CEPHFS doesn't exist", fsName)
	}

	// Create a temporary mountpoint.
	mountPath, err := ioutil.TempDir("", "lxd_cephfs_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mountPath)

	err = os.Chmod(mountPath, 0700)
	if err != nil {
		return err
	}

	mountPoint := filepath.Join(mountPath, "mount")

	err = os.Mkdir(mountPoint, 0700)
	if err != nil {
		return err
	}

	// Get the credentials and host.
	monAddresses, userSecret, err := d.getConfig(d.config["cephfs.cluster_name"], d.config["cephfs.user.name"])
	if err != nil {
		return err
	}

	connected := false
	for _, monAddress := range monAddresses {
		uri := fmt.Sprintf("%s:6789:/", monAddress)
		err = tryMount(uri, mountPoint, "ceph", 0, fmt.Sprintf("name=%v,secret=%v,mds_namespace=%v", d.config["cephfs.user.name"], userSecret, fsName))
		if err != nil {
			continue
		}

		connected = true
		defer forceUnmount(mountPoint)
		break
	}

	if !connected {
		return err
	}

	// Create the path if missing.
	err = os.MkdirAll(filepath.Join(mountPoint, fsPath), 0755)
	if err != nil {
		return err
	}

	// Check that the existing path is empty.
	ok, _ := shared.PathIsEmpty(filepath.Join(mountPoint, fsPath))
	if !ok {
		return fmt.Errorf("Only empty CEPHFS paths can be used as a LXD storage pool")
	}

	return nil
}

func (d *cephfs) Delete(op *operations.Operation) error {
	// Parse the namespace / path.
	fields := strings.SplitN(d.config["cephfs.path"], "/", 2)
	fsName := fields[0]
	fsPath := "/"
	if len(fields) > 1 {
		fsPath = fields[1]
	}

	// Create a temporary mountpoint.
	mountPath, err := ioutil.TempDir("", "lxd_cephfs_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mountPath)

	err = os.Chmod(mountPath, 0700)
	if err != nil {
		return err
	}

	mountPoint := filepath.Join(mountPath, "mount")
	err = os.Mkdir(mountPoint, 0700)
	if err != nil {
		return err
	}

	// Get the credentials and host.
	monAddresses, userSecret, err := d.getConfig(d.config["cephfs.cluster_name"], d.config["cephfs.user.name"])
	if err != nil {
		return err
	}

	connected := false
	for _, monAddress := range monAddresses {
		uri := fmt.Sprintf("%s:6789:/", monAddress)
		err = tryMount(uri, mountPoint, "ceph", 0, fmt.Sprintf("name=%v,secret=%v,mds_namespace=%v", d.config["cephfs.user.name"], userSecret, fsName))
		if err != nil {
			continue
		}

		connected = true
		defer forceUnmount(mountPoint)
		break
	}

	if !connected {
		return err
	}

	if shared.PathExists(filepath.Join(mountPoint, fsPath)) {
		// Delete the usual directories.
		for _, dir := range []string{"custom", "custom-snapshots"} {
			if shared.PathExists(filepath.Join(mountPoint, fsPath, dir)) {
				err = os.Remove(filepath.Join(mountPoint, fsPath, dir))
				if err != nil {
					return err
				}
			}
		}

		// Confirm that the path is now empty.
		ok, _ := shared.PathIsEmpty(filepath.Join(mountPoint, fsPath))
		if !ok {
			return fmt.Errorf("Only empty CEPHFS paths can be used as a LXD storage pool")
		}

		// Delete the path itself.
		if fsPath != "" && fsPath != "/" {
			err = os.Remove(filepath.Join(mountPoint, fsPath))
			if err != nil {
				return err
			}
		}
	}

	// On delete, wipe everything in the directory.
	err = wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	// Make sure the existing pool is unmounted.
	_, err = d.Unmount()
	if err != nil {
		return err
	}

	return nil
}

func (d *cephfs) Mount() (bool, error) {
	// Check if already mounted.
	if shared.IsMountPoint(GetPoolMountPath(d.name)) {
		return false, nil
	}

	// Parse the namespace / path.
	fields := strings.SplitN(d.config["cephfs.path"], "/", 2)
	fsName := fields[0]
	fsPath := "/"
	if len(fields) > 1 {
		fsPath = fields[1]
	}

	// Get the credentials and host.
	monAddresses, secret, err := d.getConfig(d.config["cephfs.cluster_name"], d.config["cephfs.user.name"])
	if err != nil {
		return false, err
	}

	// Do the actual mount.
	connected := false
	for _, monAddress := range monAddresses {
		uri := fmt.Sprintf("%s:6789:/%s", monAddress, fsPath)
		err = tryMount(uri, GetPoolMountPath(d.name), "ceph", 0, fmt.Sprintf("name=%v,secret=%v,mds_namespace=%v", d.config["cephfs.user.name"], secret, fsName))
		if err != nil {
			continue
		}

		connected = true
		break
	}

	if !connected {
		return false, err
	}

	return true, nil
}

func (d *cephfs) Unmount() (bool, error) {
	return forceUnmount(GetPoolMountPath(d.name))
}

func (d *cephfs) GetResources() (*api.ResourcesStoragePool, error) {
	// Use the generic VFS resources.
	return vfsResources(GetPoolMountPath(d.name))
}

func (d *cephfs) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	return d.validateVolume(vol, nil, removeUnknownKeys)
}

func (d *cephfs) CreateVolume(vol Volume, filler func(path string) error, op *operations.Operation) error {
	if vol.volType != VolumeTypeCustom {
		return fmt.Errorf("Volume type not supported")
	}

	if vol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	volPath := vol.MountPath()

	err := os.MkdirAll(volPath, 0711)
	if err != nil {
		return err
	}

	revertPath := true
	defer func() {
		if revertPath {
			os.RemoveAll(volPath)
		}
	}()

	if filler != nil {
		err = filler(volPath)
		if err != nil {
			return err
		}
	}

	revertPath = false
	return nil
}

func (d *cephfs) CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, op *operations.Operation) error {
	if vol.volType != VolumeTypeCustom || srcVol.volType != VolumeTypeCustom {
		return fmt.Errorf("Volume type not supported")
	}

	if vol.contentType != ContentTypeFS || srcVol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	bwlimit := d.config["rsync.bwlimit"]

	// Create the main volume path.
	volPath := vol.MountPath()
	err := vol.CreateMountPath()
	if err != nil {
		return err
	}

	// Create slice of snapshots created if revert needed later.
	revertSnaps := []string{}
	defer func() {
		if revertSnaps == nil {
			return
		}

		// Remove any paths created if we are reverting.
		for _, snapName := range revertSnaps {
			d.DeleteVolumeSnapshot(vol.volType, vol.name, snapName, op)
		}

		os.RemoveAll(volPath)
	}()

	// Ensure the volume is mounted.
	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		// If copyring snapshots is indicated, check the source isn't itself a snapshot.
		if copySnapshots && !srcVol.IsSnapshot() {
			// Get the list of snapshots from the source.
			srcSnapshots, err := srcVol.Snapshots(op)
			if err != nil {
				return err
			}

			for _, srcSnapshot := range srcSnapshots {
				_, snapName, _ := shared.ContainerGetParentAndSnapshotName(srcSnapshot.name)

				// Mount the source snapshot.
				err = srcSnapshot.MountTask(func(srcMountPath string, op *operations.Operation) error {
					// Copy the snapshot.
					_, err = rsync.LocalCopy(srcMountPath, mountPath, bwlimit, false)
					return err
				}, op)

				// Create the snapshot itself.
				err = d.CreateVolumeSnapshot(vol.volType, vol.name, snapName, op)
				if err != nil {
					return err
				}

				// Setup the revert.
				revertSnaps = append(revertSnaps, snapName)
			}
		}

		// Apply the volume quota if specified.
		err = d.SetVolumeQuota(vol.volType, vol.name, vol.config["size"], op)
		if err != nil {
			return err
		}

		// Copy source to destination (mounting each volume if needed).
		return srcVol.MountTask(func(srcMountPath string, op *operations.Operation) error {
			_, err := rsync.LocalCopy(srcMountPath, mountPath, bwlimit, false)
			return err
		}, op)
	}, op)
	if err != nil {
		return err
	}

	revertSnaps = nil // Don't revert.
	return nil
}

func (d *cephfs) DeleteVolume(volType VolumeType, volName string, op *operations.Operation) error {
	if volType != VolumeTypeCustom {
		return fmt.Errorf("Volume type not supported")
	}

	snapshots, err := d.VolumeSnapshots(volType, volName, op)
	if err != nil {
		return err
	}

	if len(snapshots) > 0 {
		return fmt.Errorf("Cannot remove a volume that has snapshots")
	}

	volPath := GetVolumeMountPath(d.name, volType, volName)

	// If the volume doesn't exist, then nothing more to do.
	if !shared.PathExists(volPath) {
		return nil
	}

	// Remove the volume from the storage device.
	err = os.RemoveAll(volPath)
	if err != nil {
		return err
	}

	// Although the volume snapshot directory should already be removed, lets remove it here
	// to just in case the top-level directory is left.
	snapshotDir, err := GetVolumeSnapshotDir(d.name, volType, volName)
	if err != nil {
		return err
	}

	err = os.RemoveAll(snapshotDir)
	if err != nil {
		return err
	}

	return nil
}

func (d *cephfs) RenameVolume(volType VolumeType, volName string, newName string, op *operations.Operation) error {
	if volType != VolumeTypeCustom {
		return fmt.Errorf("Volume type not supported")
	}

	vol := NewVolume(d, d.name, volType, ContentTypeFS, volName, nil)

	// Create new snapshots directory.
	snapshotDir, err := GetVolumeSnapshotDir(d.name, volType, newName)
	if err != nil {
		return err
	}

	err = os.MkdirAll(snapshotDir, 0711)
	if err != nil {
		return err
	}

	type volRevert struct {
		oldPath   string
		newPath   string
		isSymlink bool
	}

	// Create slice to record paths renamed if revert needed later.
	revertPaths := []volRevert{}
	defer func() {
		// Remove any paths rename if we are reverting.
		for _, vol := range revertPaths {
			if vol.isSymlink {
				os.Symlink(vol.oldPath, vol.newPath)
			} else {
				os.Rename(vol.newPath, vol.oldPath)
			}
		}

		// Remove the new snapshot directory if we are reverting.
		if len(revertPaths) > 0 {
			err = os.RemoveAll(snapshotDir)
		}
	}()

	// Rename the snapshot directory first.
	srcSnapshotDir, err := GetVolumeSnapshotDir(d.name, volType, volName)
	if err != nil {
		return err
	}

	if shared.PathExists(srcSnapshotDir) {
		targetSnapshotDir, err := GetVolumeSnapshotDir(d.name, volType, newName)
		if err != nil {
			return err
		}

		err = os.Rename(srcSnapshotDir, targetSnapshotDir)
		if err != nil {
			return err
		}

		revertPaths = append(revertPaths, volRevert{
			oldPath: srcSnapshotDir,
			newPath: targetSnapshotDir,
		})
	}

	// Rename any snapshots of the volume too.
	snapshots, err := vol.Snapshots(op)
	if err != nil {
		return err
	}

	sourcePath := GetVolumeMountPath(d.name, volType, newName)
	targetPath := GetVolumeMountPath(d.name, volType, newName)

	for _, snapshot := range snapshots {
		// Figure out the snapshot paths.
		_, snapName, _ := shared.ContainerGetParentAndSnapshotName(snapshot.name)
		oldCephSnapPath := filepath.Join(sourcePath, ".snap", snapName)
		newCephSnapPath := filepath.Join(targetPath, ".snap", snapName)
		oldPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(volName, snapName))
		newPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(newName, snapName))

		// Update the symlink.
		err = os.Symlink(newCephSnapPath, newPath)
		if err != nil {
			return err
		}

		revertPaths = append(revertPaths, volRevert{
			oldPath:   oldPath,
			newPath:   oldCephSnapPath,
			isSymlink: true,
		})
	}

	oldPath := GetVolumeMountPath(d.name, volType, volName)
	newPath := GetVolumeMountPath(d.name, volType, newName)
	err = os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}

	revertPaths = append(revertPaths, volRevert{
		oldPath: oldPath,
		newPath: newPath,
	})

	revertPaths = nil
	return nil
}

func (d *cephfs) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	value, ok := changedConfig["size"]
	if !ok {
		return nil
	}

	return d.SetVolumeQuota(vol.volType, vol.name, value, nil)
}

func (d *cephfs) GetVolumeUsage(volType VolumeType, volName string) (int64, error) {
	out, err := shared.RunCommand("getfattr", "-n", "ceph.quota.max_bytes", "--only-values", GetVolumeMountPath(d.name, volType, volName))
	if err != nil {
		return -1, err
	}

	size, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return -1, err
	}

	return size, nil
}

func (d *cephfs) SetVolumeQuota(volType VolumeType, volName, size string, op *operations.Operation) error {
	if size == "" || size == "0" {
		size = d.config["volume.size"]
	}

	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("setfattr", "-n", "ceph.quota.max_bytes", "-v", fmt.Sprintf("%d", sizeBytes), GetVolumeMountPath(d.name, volType, volName))
	return err
}

func (d *cephfs) MountVolume(volType VolumeType, volName string, op *operations.Operation) (bool, error) {
	if volType != VolumeTypeCustom {
		return false, fmt.Errorf("Volume type not supported")
	}

	return false, nil
}

func (d *cephfs) MountVolumeSnapshot(volType VolumeType, VolName, snapshotName string, op *operations.Operation) (bool, error) {
	if volType != VolumeTypeCustom {
		return false, fmt.Errorf("Volume type not supported")
	}

	return false, nil
}

func (d *cephfs) UnmountVolume(volType VolumeType, volName string, op *operations.Operation) (bool, error) {
	if volType != VolumeTypeCustom {
		return false, fmt.Errorf("Volume type not supported")
	}

	return false, nil
}

func (d *cephfs) UnmountVolumeSnapshot(volType VolumeType, volName, snapshotName string, op *operations.Operation) (bool, error) {
	if volType != VolumeTypeCustom {
		return false, fmt.Errorf("Volume type not supported")
	}

	return false, nil
}

func (d *cephfs) CreateVolumeSnapshot(volType VolumeType, volName string, newSnapshotName string, op *operations.Operation) error {
	if volType != VolumeTypeCustom {
		return fmt.Errorf("Volume type not supported")
	}

	// Create the snapshot.
	sourcePath := GetVolumeMountPath(d.name, volType, volName)
	cephSnapPath := filepath.Join(sourcePath, ".snap", newSnapshotName)

	err := os.Mkdir(cephSnapPath, 0711)
	if err != nil {
		return err
	}

	targetPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(volName, newSnapshotName))

	err = os.MkdirAll(filepath.Dir(targetPath), 0711)
	if err != nil {
		return err
	}

	err = os.Symlink(cephSnapPath, targetPath)
	if err != nil {
		return err
	}

	return nil
}

func (d *cephfs) DeleteVolumeSnapshot(volType VolumeType, volName string, snapshotName string, op *operations.Operation) error {
	if volType != VolumeTypeCustom {
		return fmt.Errorf("Volume type not supported")
	}

	// Delete the snapshot itself.
	sourcePath := GetVolumeMountPath(d.name, volType, volName)
	cephSnapPath := filepath.Join(sourcePath, ".snap", snapshotName)

	err := os.Remove(cephSnapPath)
	if err != nil {
		return err
	}

	// Remove the symlink.
	snapPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(volName, snapshotName))
	err = os.Remove(snapPath)
	if err != nil {
		return err
	}

	return nil
}

func (d *cephfs) RenameVolumeSnapshot(volType VolumeType, volName string, snapshotName string, newSnapshotName string, op *operations.Operation) error {
	if volType != VolumeTypeCustom {
		return fmt.Errorf("Volume type not supported")
	}

	sourcePath := GetVolumeMountPath(d.name, volType, volName)
	oldCephSnapPath := filepath.Join(sourcePath, ".snap", snapshotName)
	newCephSnapPath := filepath.Join(sourcePath, ".snap", newSnapshotName)

	err := os.Rename(oldCephSnapPath, newCephSnapPath)
	if err != nil {
		return err
	}

	// Re-generate the snapshot symlink.
	oldPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(volName, snapshotName))
	err = os.Remove(oldPath)
	if err != nil {
		return err
	}

	newPath := GetVolumeMountPath(d.name, volType, GetSnapshotVolumeName(volName, newSnapshotName))
	err = os.Symlink(newCephSnapPath, newPath)
	if err != nil {
		return err
	}

	return nil
}

func (d *cephfs) VolumeSnapshots(volType VolumeType, volName string, op *operations.Operation) ([]string, error) {
	if volType != VolumeTypeCustom {
		return nil, fmt.Errorf("Volume type not supported")
	}

	snapshotDir, err := GetVolumeSnapshotDir(d.name, volType, volName)
	if err != nil {
		return nil, err
	}

	snapshots := []string{}

	ents, err := ioutil.ReadDir(snapshotDir)
	if err != nil {
		// If the snapshots directory doesn't exist, there are no snapshots.
		if os.IsNotExist(err) {
			return snapshots, nil
		}

		return nil, err
	}

	for _, ent := range ents {
		fileInfo, err := os.Stat(filepath.Join(snapshotDir, ent.Name()))
		if err != nil {
			return nil, err
		}

		if !fileInfo.IsDir() {
			continue
		}

		snapshots = append(snapshots, ent.Name())
	}

	return snapshots, nil
}

func (d *cephfs) RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error {
	sourcePath := GetVolumeMountPath(d.name, vol.volType, vol.name)
	cephSnapPath := filepath.Join(sourcePath, ".snap", snapshotName)

	// Restore using rsync.
	bwlimit := d.config["rsync.bwlimit"]
	output, err := rsync.LocalCopy(cephSnapPath, vol.MountPath(), bwlimit, false)
	if err != nil {
		return fmt.Errorf("Failed to rsync volume: %s: %s", string(output), err)
	}

	return nil
}

func (d *cephfs) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs migration.VolumeSourceArgs, op *operations.Operation) error {
	if vol.volType != VolumeTypeCustom {
		return fmt.Errorf("Volume type not supported")
	}

	if vol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	if volSrcArgs.MigrationType.FSType != migration.MigrationFSType_RSYNC {
		return fmt.Errorf("Migration type not supported")
	}

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
			return rsync.Send(snapshot.name, path, conn, wrapper, nil, bwlimit, d.state.OS.ExecPath)
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
		return rsync.Send(vol.name, path, conn, wrapper, nil, bwlimit, d.state.OS.ExecPath)
	}, op)
}

func (d *cephfs) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, op *operations.Operation) error {
	if vol.volType != VolumeTypeCustom {
		return fmt.Errorf("Volume type not supported")
	}

	if vol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_RSYNC {
		return fmt.Errorf("Migration type not supported")
	}

	// Create the main volume path.
	volPath := vol.MountPath()
	err := vol.CreateMountPath()
	if err != nil {
		return err
	}

	// Create slice of snapshots created if revert needed later.
	revertSnaps := []string{}
	defer func() {
		if revertSnaps == nil {
			return
		}

		// Remove any paths created if we are reverting.
		for _, snapName := range revertSnaps {
			d.DeleteVolumeSnapshot(vol.volType, vol.name, snapName, op)
		}

		os.RemoveAll(volPath)
	}()

	// Ensure the volume is mounted.
	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		path := shared.AddSlash(mountPath)

		// Snapshots are sent first by the sender, so create these first.
		for _, snapName := range volTargetArgs.Snapshots {
			// Receive the snapshot.
			var wrapper *ioprogress.ProgressTracker
			if volTargetArgs.TrackProgress {
				wrapper = migration.ProgressTracker(op, "fs_progress", snapName)
			}

			err = rsync.Recv(path, conn, wrapper, nil)
			if err != nil {
				return err
			}

			// Create the snapshot itself.
			err = d.CreateVolumeSnapshot(vol.volType, vol.name, snapName, op)
			if err != nil {
				return err
			}

			// Setup the revert.
			revertSnaps = append(revertSnaps, snapName)
		}

		// Apply the volume quota if specified.
		err = d.SetVolumeQuota(vol.volType, vol.name, vol.config["size"], op)
		if err != nil {
			return err
		}

		// Receive the main volume from sender.
		var wrapper *ioprogress.ProgressTracker
		if volTargetArgs.TrackProgress {
			wrapper = migration.ProgressTracker(op, "fs_progress", vol.name)
		}

		return rsync.Recv(path, conn, wrapper, nil)
	}, op)
	if err != nil {
		return err
	}

	revertSnaps = nil
	return nil
}

func (d *cephfs) fsExists(clusterName string, userName string, fsName string) bool {
	_, err := shared.RunCommand("ceph", "--name", fmt.Sprintf("client.%s", userName), "--cluster", clusterName, "fs", "get", fsName)
	if err != nil {
		return false
	}

	return true
}

func (d *cephfs) getConfig(clusterName string, userName string) ([]string, string, error) {
	// Parse the CEPH configuration.
	cephConf, err := os.Open(fmt.Sprintf("/etc/ceph/%s.conf", clusterName))
	if err != nil {
		return nil, "", err
	}

	cephMon := []string{}

	scan := bufio.NewScanner(cephConf)
	for scan.Scan() {
		line := scan.Text()
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "mon_host") {
			fields := strings.SplitN(line, "=", 2)
			if len(fields) < 2 {
				continue
			}

			servers := strings.Split(fields[1], ",")
			for _, server := range servers {
				cephMon = append(cephMon, strings.TrimSpace(server))
			}
			break
		}
	}

	if len(cephMon) == 0 {
		return nil, "", fmt.Errorf("Couldn't find a CPEH mon")
	}

	// Parse the CEPH keyring.
	cephKeyring, err := os.Open(fmt.Sprintf("/etc/ceph/%v.client.%v.keyring", clusterName, userName))
	if err != nil {
		return nil, "", err
	}

	var cephSecret string

	scan = bufio.NewScanner(cephKeyring)
	for scan.Scan() {
		line := scan.Text()
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "key") {
			fields := strings.SplitN(line, "=", 2)
			if len(fields) < 2 {
				continue
			}

			cephSecret = strings.TrimSpace(fields[1])
			break
		}
	}

	if cephSecret == "" {
		return nil, "", fmt.Errorf("Couldn't find a keyring entry")
	}

	return cephMon, cephSecret, nil
}
