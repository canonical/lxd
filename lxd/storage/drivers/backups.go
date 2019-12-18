package drivers

import (
	"fmt"
	"io"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/shared"
)

// genericBackupUnpack unpacks a non-optimized backup tarball through a storage driver.
func genericBackupUnpack(d Driver, poolName string, vol Volume, snapshots []string, srcData io.ReadSeeker, op *operations.Operation) (func(vol Volume) error, func(), error) {
	revert := true

	// Define a revert function that will be used both to revert if an error occurs inside this
	// function but also return it for use from the calling functions if no error internally.
	revertHook := func() {
		for _, snapName := range snapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, poolName, vol.volType, vol.contentType, fullSnapshotName, vol.config)
			d.DeleteVolumeSnapshot(snapVol, op)
		}

		// And lastly the main volume.
		d.DeleteVolume(vol, op)
	}

	// Only execute the revert function if we have had an error internally and revert is true.
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

	// Create the main volume.
	err = d.CreateVolume(vol, nil, nil)
	if err != nil {
		return nil, nil, err
	}

	if len(snapshots) > 0 {
		// Create new snapshots directory.
		err := createParentSnapshotDirIfMissing(poolName, vol.volType, vol.name)
		if err != nil {
			return nil, nil, err
		}
	}

	for _, snapName := range snapshots {
		// Prepare tar arguments.
		args := append(tarArgs, []string{
			"-",
			"--recursive-unlink",
			"--xattrs-include=*",
			"--strip-components=3",
			"-C", vol.MountPath(), fmt.Sprintf("backup/snapshots/%s", snapName),
		}...)

		// Extract snapshots.
		srcData.Seek(0, 0)
		err = shared.RunCommandWithFds(srcData, nil, "tar", args...)
		if err != nil {
			return nil, nil, err
		}

		fullSnapshotName := GetSnapshotVolumeName(vol.name, snapName)
		snapVol := NewVolume(d, poolName, vol.volType, vol.contentType, fullSnapshotName, vol.config)
		err = d.CreateVolumeSnapshot(snapVol, op)
		if err != nil {
			return nil, nil, err
		}
	}

	// Prepare tar extraction arguments.
	args := append(tarArgs, []string{
		"-",
		"--recursive-unlink",
		"--strip-components=2",
		"--xattrs-include=*",
		"-C", vol.MountPath(), "backup/container",
	}...)

	// Extract instance.
	srcData.Seek(0, 0)
	err = shared.RunCommandWithFds(srcData, nil, "tar", args...)
	if err != nil {
		return nil, nil, err
	}

	revert = false

	return nil, revertHook, nil
}
