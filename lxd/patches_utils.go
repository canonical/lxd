package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/units"
)

// For 'btrfs' storage backend.
func btrfsSubVolumeCreate(subvol string) error {
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
		return err
	}

	return nil
}

func btrfsSubVolumeQGroup(subvol string) (string, error) {
	output, err := shared.RunCommand(
		"btrfs",
		"qgroup",
		"show",
		"-e",
		"-f",
		subvol)

	if err != nil {
		return "", fmt.Errorf("Quotas disabled on filesystem")
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
		return "", fmt.Errorf("Unable to find quota group")
	}

	return qgroup, nil
}

func btrfsSubVolumeDelete(subvol string) error {
	// Attempt (but don't fail on) to delete any qgroup on the subvolume
	qgroup, err := btrfsSubVolumeQGroup(subvol)
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

func btrfsSubVolumesDelete(subvol string) error {
	// Delete subsubvols.
	subsubvols, err := storageDrivers.BTRFSSubVolumesGet(subvol)
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(subsubvols)))

	for _, subsubvol := range subsubvols {
		err := btrfsSubVolumeDelete(path.Join(subvol, subsubvol))
		if err != nil {
			return err
		}
	}

	// Delete the subvol itself
	err = btrfsSubVolumeDelete(subvol)
	if err != nil {
		return err
	}

	return nil
}

func btrfsSnapshot(s *state.State, source string, dest string, readonly bool) error {
	var output string
	var err error
	if readonly && !s.OS.RunningInUserNS {
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

// For 'lvm' storage backend.
func lvmLVRename(vgName string, oldName string, newName string) error {
	_, err := shared.TryRunCommand("lvrename", vgName, oldName, newName)
	if err != nil {
		return fmt.Errorf("could not rename volume group from \"%s\" to \"%s\": %v", oldName, newName, err)
	}

	return nil
}

func lvmLVExists(lvName string) (bool, error) {
	_, err := shared.RunCommand("lvs", "--noheadings", "-o", "lv_attr", lvName)
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Err.(*exec.ExitError)
			if ok {
				if exitError.ExitCode() == 5 {
					// logical volume not found
					return false, nil
				}
			}
		}

		return false, fmt.Errorf("error checking for logical volume \"%s\"", lvName)
	}

	return true, nil
}

func lvmVGActivate(lvmVolumePath string) error {
	_, err := shared.TryRunCommand("vgchange", "-ay", lvmVolumePath)
	if err != nil {
		return fmt.Errorf("could not activate volume group \"%s\": %v", lvmVolumePath, err)
	}

	return nil
}

func lvmNameToLVName(containerName string) string {
	lvName := strings.Replace(containerName, "-", "--", -1)
	return strings.Replace(lvName, shared.SnapshotDelimiter, "-", -1)
}

func lvmDevPath(projectName, lvmPool string, volumeType string, lvmVolume string) string {
	lvmVolume = project.Instance(projectName, lvmVolume)
	if volumeType == "" {
		return fmt.Sprintf("/dev/%s/%s", lvmPool, lvmVolume)
	}

	return fmt.Sprintf("/dev/%s/%s_%s", lvmPool, volumeType, lvmVolume)
}

func lvmGetLVSize(lvPath string) (string, error) {
	msg, err := shared.TryRunCommand("lvs", "--noheadings", "-o", "size", "--nosuffix", "--units", "b", lvPath)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve size of logical volume: %s: %s", string(msg), err)
	}

	sizeString := string(msg)
	sizeString = strings.TrimSpace(sizeString)
	size, err := strconv.ParseInt(sizeString, 10, 64)
	if err != nil {
		return "", err
	}

	detectedSize := units.GetByteSizeString(size, 0)

	return detectedSize, nil
}

// volumeFillDefault fills default settings into a volume config.
// Deprecated. Please use FillInstanceConfig() on the storage pool.
func volumeFillDefault(config map[string]string, parentPool *api.StoragePool) error {
	if parentPool.Driver == "lvm" || parentPool.Driver == "ceph" {
		if config["block.filesystem"] == "" {
			config["block.filesystem"] = parentPool.Config["volume.block.filesystem"]
		}
		if config["block.filesystem"] == "" {
			// Unchangeable volume property: Set unconditionally.
			config["block.filesystem"] = storageDrivers.DefaultFilesystem
		}

		if config["block.mount_options"] == "" {
			config["block.mount_options"] = parentPool.Config["volume.block.mount_options"]
		}
		if config["block.mount_options"] == "" {
			// Unchangeable volume property: Set unconditionally.
			config["block.mount_options"] = "discard"
		}
	}

	return nil
}
