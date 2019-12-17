package drivers

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/units"
	"github.com/lxc/lxd/shared/version"
)

// LVMPysicalVolumeExists checks if an LVM Physical Volume exists.
func LVMPysicalVolumeExists(pvName string) (bool, error) {
	_, err := shared.RunCommand("pvs", "--noheadings", "-o", "lv_attr", pvName)
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 5 {
					// physical volume not found
					return false, nil
				}
			}
		}
		return false, fmt.Errorf("error checking for physical volume \"%s\"", pvName)
	}

	return true, nil
}

// LVMVolumeGroupExists checks if an LVM Volume Group exists.
func LVMVolumeGroupExists(vgName string) (bool, error) {
	_, err := shared.RunCommand("vgs", "--noheadings", "-o", "lv_attr", vgName)
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 5 {
					// volume group not found
					return false, nil
				}
			}
		}

		return false, fmt.Errorf("error checking for volume group \"%s\"", vgName)
	}

	return true, nil
}

// LVMGetLVCount gets the count of volumes in a volume group.
func LVMGetLVCount(vgName string) (int, error) {
	output, err := shared.TryRunCommand("vgs", "--noheadings", "-o", "lv_count", vgName)
	if err != nil {
		return -1, err
	}

	output = strings.TrimSpace(output)
	return strconv.Atoi(output)
}

// LVMThinpoolExists checks whether the specified thinpool exists in a volume group.
func LVMThinpoolExists(vgName string, poolName string) (bool, error) {
	output, err := shared.RunCommand("vgs", "--noheadings", "-o", "lv_attr", fmt.Sprintf("%s/%s", vgName, poolName))
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 5 {
					// pool LV was not found
					return false, nil
				}
			}
		}

		return false, fmt.Errorf("error checking for pool \"%s\"", poolName)
	}
	// Found LV named poolname, check type:
	attrs := strings.TrimSpace(string(output[:]))
	if strings.HasPrefix(attrs, "t") {
		return true, nil
	}

	return false, fmt.Errorf("pool named \"%s\" exists but is not a thin pool", poolName)
}

// LVMDevPath returns the path to the LVM volume device.
func LVMDevPath(projectName, lvmPool string, volumeDBTypeName string, lvmVolume string) string {
	lvmVolume = project.Prefix(projectName, lvmVolume)
	if volumeDBTypeName == "" {
		return fmt.Sprintf("/dev/%s/%s", lvmPool, lvmVolume)
	}

	return fmt.Sprintf("/dev/%s/%s_%s", lvmPool, volumeDBTypeName, lvmVolume)
}

// LVMVolumeExists checks whether the specified logical volume exists.
func LVMVolumeExists(lvDevPath string) (bool, error) {
	_, err := shared.RunCommand("lvs", "--noheadings", "-o", "lv_attr", lvDevPath)
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 5 {
					// logical volume not found.
					return false, nil
				}
			}
		}

		return false, fmt.Errorf("error checking for logical volume \"%s\"", lvDevPath)
	}

	return true, nil
}

// LVMCreateThinpool creates a thin pool logical volume.
func LVMCreateThinpool(lvmVersion string, vgName string, thinPoolName string) error {
	exists, err := LVMThinpoolExists(vgName, thinPoolName)
	if err != nil {
		return err
	}

	if exists {
		return nil
	}

	err = lvmCreateDefaultThinPool(lvmVersion, vgName, thinPoolName)
	if err != nil {
		return err
	}

	poolExists, err := LVMThinpoolExists(vgName, thinPoolName)
	if err != nil {
		return fmt.Errorf("Error checking for thin pool \"%s\" in \"%s\": %v", thinPoolName, vgName, err)
	}

	if !poolExists {
		return fmt.Errorf("Thin pool \"'%s\" does not exist in Volume Group \"%s\"", thinPoolName, vgName)
	}

	return nil
}

func lvmCreateDefaultThinPool(lvmVersion string, vgName string, thinPoolName string) error {
	isRecent, err := LVMVersionIsAtLeast(lvmVersion, "2.02.99")
	if err != nil {
		return fmt.Errorf("Error checking LVM version: %s", err)
	}

	// Create the thin pool
	lvmThinPool := fmt.Sprintf("%s/%s", vgName, thinPoolName)
	if isRecent {
		_, err = shared.TryRunCommand(
			"lvcreate",
			"-Wy", "--yes",
			"--poolmetadatasize", "1G",
			"-l", "100%FREE",
			"--thinpool", lvmThinPool)
	} else {
		_, err = shared.TryRunCommand(
			"lvcreate",
			"-Wy", "--yes",
			"--poolmetadatasize", "1G",
			"-L", "1G",
			"--thinpool", lvmThinPool)
	}

	if err != nil {
		return fmt.Errorf("Could not create LVM thin pool named %s: %v", thinPoolName, err)
	}

	if !isRecent {
		// Grow it to the maximum VG size (two step process required by old LVM)
		_, err = shared.TryRunCommand("lvextend", "--alloc", "anywhere", "-l", "100%FREE", lvmThinPool)

		if err != nil {
			return fmt.Errorf("Could not grow LVM thin pool named %s: %v", thinPoolName, err)
		}
	}

	return nil
}

// LVMVersionIsAtLeast checks whether the installed version of LVM is at least the specific version.
func LVMVersionIsAtLeast(sTypeVersion string, versionString string) (bool, error) {
	lvmVersionString := strings.Split(sTypeVersion, "/")[0]

	lvmVersion, err := version.Parse(lvmVersionString)
	if err != nil {
		return false, err
	}

	inVersion, err := version.Parse(versionString)
	if err != nil {
		return false, err
	}

	if lvmVersion.Compare(inVersion) < 0 {
		return false, nil
	}

	return true, nil
}

// LVMCreateLogicalVolume creates a logical volume.
func LVMCreateLogicalVolume(projectName, vgName string, thinPoolName string, lvName string, lvFsType string, lvSize string, volumeType string, makeThinLv bool) error {
	var output string
	var err error

	// Round the size to closest 512 bytes
	lvSizeInt, err := units.ParseByteSizeString(lvSize)
	if err != nil {
		return err
	}

	lvSizeInt = int64(lvSizeInt/512) * 512
	lvSizeString := units.GetByteSizeString(lvSizeInt, 0)

	lvmPoolVolumeName := lvmFullVolumeName(projectName, volumeType, lvName)
	if makeThinLv {
		targetVg := fmt.Sprintf("%s/%s", vgName, thinPoolName)
		_, err = shared.TryRunCommand("lvcreate", "-Wy", "--yes", "--thin", "-n", lvmPoolVolumeName, "--virtualsize", lvSizeString, targetVg)
	} else {
		_, err = shared.TryRunCommand("lvcreate", "-Wy", "--yes", "-n", lvmPoolVolumeName, "--size", lvSizeString, vgName)
	}
	if err != nil {
		return fmt.Errorf("Could not create thin LV named %s: %v", lvmPoolVolumeName, err)
	}

	fsPath := LVMDevPath(projectName, vgName, volumeType, lvName)

	output, err = MakeFSType(fsPath, lvFsType, nil)
	if err != nil {
		return fmt.Errorf("Error making filesystem on image LV: %v (%s)", err, output)
	}

	return nil
}

// lvmFullVolumeName returns the logical volume's full name with project and volume type prefix.
func lvmFullVolumeName(projectName, volumeType string, lvmVolume string) string {
	storageName := project.Prefix(projectName, lvmVolume)

	if volumeType != "" {
		storageName = fmt.Sprintf("%s_%s", volumeType, storageName)
	}

	return storageName
}
