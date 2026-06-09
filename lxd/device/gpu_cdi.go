package device

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/device/cdi"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/idmap"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
)

// cdiHooksFilePath returns the path to the CDI hooks file for the named device.
func cdiHooksFilePath(devicesPath, devName string) string {
	return filepath.Join(devicesPath, devName+cdi.CDIHooksFileSuffix)
}

// cdiConfigDevicesFilePath returns the path to the CDI config devices file for the named device.
func cdiConfigDevicesFilePath(devicesPath, devName string) string {
	return filepath.Join(devicesPath, devName+cdi.CDIConfigDevicesFileSuffix)
}

// startCDIDevices starts all the devices given in a CDI specification:
// * `unix-char` (representing the card and non-card devices)
// * `disk` (representing the mounts).
func startCDIDevices(d *deviceCommon, configDevices cdi.ConfigDevices, runConf *deviceConfig.RunConfig) error {
	srcFDHandlers := make([]*os.File, 0)
	defer func() {
		for _, f := range srcFDHandlers {
			_ = f.Close()
		}
	}()

	hooksFilePath := cdiHooksFilePath(d.inst.DevicesPath(), d.name)
	deviceConfigFilePath := cdiConfigDevicesFilePath(d.inst.DevicesPath(), d.name)
	devicesPath := d.inst.DevicesPath()

	// Check if there are any remaining CDI devices in the instance devices directory.
	// If there are, we need to remove them. These can be present in the case where the device stop hook was not called
	// (e.g. due to an abrupt host shutdown).
	err := filepath.WalkDir(devicesPath, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if e.IsDir() {
			return nil
		}

		// Remove the CDI device files (both unix-char and disk devices as long as JSON CDI metadata files).
		if strings.HasPrefix(e.Name(), filesystem.PathNameEncode(cdi.CDIUnixPrefix+"."+d.name+".")) ||
			strings.HasPrefix(e.Name(), filesystem.PathNameEncode(cdi.CDIDiskPrefix+"."+d.name+".")) ||
			path == hooksFilePath || path == deviceConfigFilePath {
			err = os.Remove(path)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	for _, conf := range configDevices.UnixCharDevs {
		if conf["source"] == "" {
			return fmt.Errorf("The source of the unix-char device %v used for CDI is empty", conf)
		}

		if conf["major"] == "" || conf["minor"] == "" {
			return fmt.Errorf("The major or minor of the unix-char device %v used for CDI is empty", conf)
		}

		major, err := strconv.ParseUint(conf["major"], 10, 32)
		if err != nil {
			return fmt.Errorf("Failed parsing major number %q when starting CDI device: %w", conf["major"], err)
		}

		minor, err := strconv.ParseUint(conf["minor"], 10, 32)
		if err != nil {
			return fmt.Errorf("Failed parsing minor number %q when starting CDI device: %w", conf["minor"], err)
		}

		uid := conf["uid"]
		if uid != "" {
			d.config["uid"] = uid
		}

		gid := conf["gid"]
		if gid != "" {
			d.config["gid"] = gid
		}

		// Here putting a `cdi.CDIUnixPrefix` prefix with 'd.name' as a device name will create an directory entry like:
		// <lxd_var_path>/devices/<instance_name>/<cdi.CDIUnixPrefix>.<gpu_device_name>.<path_encoded_relative_dest_path>
		// 'unixDeviceSetupCharNum' is already checking for dupe entries so we have no validation to do here.
		err = unixDeviceSetupCharNum(d.state, devicesPath, cdi.CDIUnixPrefix, d.name, conf, uint32(major), uint32(minor), conf["path"], false, runConf)
		if err != nil {
			return err
		}
	}

	// Create the devices directory if missing.
	err = os.Mkdir(devicesPath, 0711)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}

	for _, conf := range configDevices.BindMounts {
		if conf["source"] == "" {
			return fmt.Errorf("The source of the disk device %v used for CDI is empty", conf)
		}

		srcPath := shared.HostPath(conf["source"])
		destPath := conf["path"]
		relativeDestPath := strings.TrimPrefix(destPath, "/")

		// Deduplicate CDI bind mounts by target path across devices.
		// Multiple CDI GPU devices can require the same runtime files (e.g. /run/nvidia-persistenced/socket).
		// Only create one mount entry per target path to avoid duplicate lxc.mount.entry conflicts.
		duplicate := false
		dents, err := os.ReadDir(devicesPath)
		if err == nil {
			for _, e := range dents {
				decoded := filesystem.PathNameDecode(e.Name())
				if strings.HasPrefix(decoded, cdi.CDIDiskPrefix+".") && strings.HasSuffix(decoded, "."+relativeDestPath) {
					duplicate = true
					break
				}
			}
		}

		if duplicate {
			continue
		}

		// This time, the created path will be like:
		// <lxd_var_path>/devices/<instance_name>/<cdi.CDIDiskPrefix>.<gpu_device_name>.<path_encoded_relative_dest_path>
		deviceName := filesystem.PathNameEncode(deviceJoinPath(cdi.CDIDiskPrefix, d.name, relativeDestPath))
		devPath := filepath.Join(devicesPath, deviceName)

		ownerShift := deviceConfig.MountOwnerShiftNone
		if idmap.CanIdmapMount(devPath, "") {
			ownerShift = deviceConfig.MountOwnerShiftDynamic
		}

		options := []string{"bind"}
		mntOptions := shared.SplitNTrimSpace(conf["raw.mount.options"], ",", -1, true)
		fsName := "none"

		fileInfo, err := os.Stat(srcPath)
		if err != nil {
			return fmt.Errorf("Failed accessing source path %q: %w", srcPath, err)
		}

		isFile := !fileInfo.Mode().IsDir()

		f, err := os.OpenFile(srcPath, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("Failed opening source path %q: %w", srcPath, err)
		}

		srcPath = fmt.Sprintf("/proc/self/fd/%d", f.Fd())
		srcFDHandlers = append(srcFDHandlers, f)

		// Clean any existing entry.
		err = os.Remove(devPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}

		// Create the mount point.
		if isFile {
			f, err := os.Create(devPath)
			if err != nil {
				return err
			}

			srcFDHandlers = append(srcFDHandlers, f)
		} else {
			err := os.Mkdir(devPath, 0700)
			if err != nil {
				return err
			}
		}

		// Mount the fs.
		err = DiskMount(srcPath, devPath, false, "", mntOptions, fsName)
		if err != nil {
			return err
		}

		if isFile {
			options = append(options, "create=file")
		} else {
			options = append(options, "create=dir")
		}

		runConf.Mounts = append(runConf.Mounts, deviceConfig.MountEntryItem{
			DevName:    deviceName,
			DevSource:  deviceConfig.DevSourcePath{Path: devPath},
			TargetPath: relativeDestPath,
			FSType:     "none",
			Opts:       options,
			OwnerShift: ownerShift,
		})

		runConf.PostHooks = append(runConf.PostHooks, func() error {
			return unix.Unmount(devPath, unix.MNT_DETACH)
		})
	}

	// Serialize the config devices inside the devices directory.
	f, err := os.Create(cdiConfigDevicesFilePath(devicesPath, d.name))
	if err != nil {
		return fmt.Errorf("Could not create the CDI config devices file: %w", err)
	}

	defer f.Close()
	err = json.NewEncoder(f).Encode(configDevices)
	if err != nil {
		return fmt.Errorf("Could not write to the CDI config devices file: %w", err)
	}

	return nil
}

// stopCDIDevices removes unix-char devices and unmounts disk bind mounts described by configDevices.
func stopCDIDevices(d *deviceCommon, configDevices cdi.ConfigDevices, runConf *deviceConfig.RunConfig) error {
	// Remove ALL the underlying unix-char dev entries created when the CDI device started.
	err := unixDeviceRemove(d.inst.DevicesPath(), cdi.CDIUnixPrefix, d.name, "", runConf)
	if err != nil {
		return err
	}

	for _, conf := range configDevices.BindMounts {
		relativeDestPath := strings.TrimPrefix(conf["path"], "/")
		devPath := filepath.Join(d.inst.DevicesPath(), filesystem.PathNameEncode(deviceJoinPath(cdi.CDIDiskPrefix, d.name, relativeDestPath)))
		runConf.PostHooks = append(runConf.PostHooks, func() error {
			// Clean any existing device mount entry. Should occur first before custom volume unmounts.
			err := DiskMountClear(devPath)
			if err != nil {
				return err
			}

			return nil
		})

		// The disk device doesn't exist, do nothing.
		if !shared.PathExists(devPath) {
			continue
		}

		// Request an unmount of the device inside the instance.
		runConf.Mounts = append(runConf.Mounts, deviceConfig.MountEntryItem{
			TargetPath: relativeDestPath,
		})
	}

	return nil
}

// applyCDIDeviceToContainer runs the full CDI device setup for a container: it generates the CDI
// spec from cdiID, sets up unix-char and disk devices, writes the hooks file, and registers a
// PostHook to apply the hooks after the container starts.
func applyCDIDeviceToContainer(d *deviceCommon, cdiID cdi.ID, runConf *deviceConfig.RunConfig) error {
	c, ok := d.inst.(instance.Container)
	if !ok {
		return fmt.Errorf("Failed casting instance %q to container", d.inst.Name())
	}

	configDevices, hooks, err := cdi.GenerateFromCDI(d.state.OS.InUbuntuCore(), d.inst, cdiID)
	if err != nil {
		return err
	}

	err = startCDIDevices(d, *configDevices, runConf)
	if err != nil {
		return err
	}

	hooksFile := cdiHooksFilePath(d.inst.DevicesPath(), d.name)
	f, err := os.Create(hooksFile)
	if err != nil {
		return fmt.Errorf("Could not create the CDI hooks file: %w", err)
	}

	defer f.Close()
	err = json.NewEncoder(f).Encode(hooks)
	if err != nil {
		return fmt.Errorf("Could not write to the CDI hooks file: %w", err)
	}

	runConf.PostHooks = append(runConf.PostHooks, func() error {
		return cdi.ApplyHooksToContainer(hooksFile, c)
	})

	return nil
}

// postStopCDIDevice cleans up CDI device files after a device is stopped.
// If allowMissingFiles is true, missing hooks and config files are silently ignored.
// This is needed for instances started before the CDI migration for gputype=mig.
func postStopCDIDevice(d *deviceCommon, allowMissingFiles bool) error {
	err := unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), cdi.CDIUnixPrefix, d.name, "")
	if err != nil {
		return fmt.Errorf("Failed deleting files for CDI device %q: %w", d.name, err)
	}

	hooksFile := cdiHooksFilePath(d.inst.DevicesPath(), d.name)
	err = os.Remove(hooksFile)
	if err != nil && (!allowMissingFiles || !errors.Is(err, fs.ErrNotExist)) {
		return fmt.Errorf("Failed deleting CDI hooks file for device %q: %w", d.name, err)
	}

	configDevicesFile := cdiConfigDevicesFilePath(d.inst.DevicesPath(), d.name)
	err = os.Remove(configDevicesFile)
	if err != nil && (!allowMissingFiles || !errors.Is(err, fs.ErrNotExist)) {
		return fmt.Errorf("Failed deleting CDI config devices file for device %q: %w", d.name, err)
	}

	return nil
}
