package cdi

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
	"tags.cncf.io/container-device-interface/specs-go"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/shared"
)

// specDevToInstanceDev builds a list of unix-char devices to be created from a CDI spec.
func specDevToInstanceDev(configDevices *ConfigDevices, d specs.DeviceNode) error {
	if d.Path == "" {
		return fmt.Errorf("Device path is empty in the CDI device node: %v", d)
	}

	hostPath := d.HostPath
	if hostPath == "" {
		hostPath = d.Path // When the hostPath is empty, the path is the device path in the container.
	}

	// Only fetch major/minor from device if both are unspecified (0).
	// Some devices legitimately have minor=0, so we can't treat 0 as "unspecified" individually.
	if d.Major == 0 && d.Minor == 0 {
		stat := unix.Stat_t{}
		err := unix.Stat(hostPath, &stat)
		if err != nil {
			return err
		}

		d.Major = int64(unix.Major(uint64(stat.Rdev)))
		d.Minor = int64(unix.Minor(uint64(stat.Rdev)))
	}

	instanceDev := map[string]string{
		"type":   "unix-char",
		"source": hostPath,
		"path":   d.Path,
		"major":  strconv.FormatInt(d.Major, 10),
		"minor":  strconv.FormatInt(d.Minor, 10),
	}

	if d.UID != nil {
		instanceDev["uid"] = strconv.FormatUint(uint64(*d.UID), 10)
	}

	if d.GID != nil {
		instanceDev["gid"] = strconv.FormatUint(uint64(*d.GID), 10)
	}

	configDevices.UnixCharDevs = append(configDevices.UnixCharDevs, instanceDev)
	return nil
}

// specMountToInstanceDev builds a list of disk mounts to be created from a CDI spec.
func specMountToInstanceDev(configDevices *ConfigDevices, cdiID ID, mounts []*specs.Mount) ([]SymlinkEntry, error) {
	indirectSymlinks := make([]SymlinkEntry, 0)
	var chosenOpts []string

	rootPath := ""
	if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
	}

	for _, mount := range mounts {
		if mount.HostPath == "" || mount.ContainerPath == "" {
			return nil, fmt.Errorf("The hostPath or containerPath is empty in the CDI mount: %v", *mount)
		}

		chosenOpts = []string{}
		for _, opt := range mount.Options {
			if !slices.Contains(chosenOpts, opt) {
				chosenOpts = append(chosenOpts, opt)
			}
		}

		chosenOptsStr := strings.Join(chosenOpts, ",")

		// mount.HostPath can be a symbolic link, so we need to evaluate it
		evaluatedHostPath, err := filepath.EvalSymlinks(mount.HostPath)
		if err != nil {
			return nil, err
		}

		if evaluatedHostPath != mount.HostPath && mount.ContainerPath == strings.TrimPrefix(mount.HostPath, rootPath) {
			targetPath := strings.TrimPrefix(evaluatedHostPath, rootPath)
			indirectSymlinks = append(indirectSymlinks, SymlinkEntry{Target: targetPath, Link: mount.ContainerPath})
			mount.ContainerPath = targetPath
		}

		configDevices.BindMounts = append(
			configDevices.BindMounts,
			map[string]string{
				"type":              "disk",
				"source":            evaluatedHostPath,
				"path":              mount.ContainerPath,
				"raw.mount.options": chosenOptsStr,
			},
		)
	}

	// If the user desires to run a nested docker container inside a LXD container,
	// the Tegra CSV files also need to be mounted so that the NVIDIA docker runtime
	// can be auto-enabled as 'csv' mode.
	if cdiID.Vendor == NVIDIA && cdiID.Class == IGPU {
		tegraCSVFilesCandidates := defaultNvidiaTegraCSVFiles(rootPath)
		tegraCSVFiles := make([]string, 0)
		for _, candidate := range tegraCSVFilesCandidates {
			_, err := os.Stat(candidate)
			if err == nil {
				tegraCSVFiles = append(tegraCSVFiles, candidate)
			} else if os.IsNotExist(err) {
				continue
			} else {
				return nil, err
			}
		}

		if len(tegraCSVFiles) == 0 {
			return nil, errors.New("No CSV files detected for Tegra iGPU")
		}

		for _, tegraFile := range tegraCSVFiles {
			configDevices.BindMounts = append(
				configDevices.BindMounts,
				map[string]string{
					"type":     "disk",
					"source":   tegraFile,
					"path":     strings.TrimPrefix(tegraFile, rootPath),
					"readonly": "true",
				},
			)
		}
	}

	return indirectSymlinks, nil
}

// specHookToLXDCDIHook will translate a hook from a CDI spec into an entry in a `Hooks`.
// Some CDI hooks are not relevant for LXD and will be ignored.
func specHookToLXDCDIHook(hook *specs.Hook, hooks *Hooks) error {
	if hook == nil {
		return nil
	}

	rootPath := ""
	if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
	}

	if len(hook.Args) < 3 {
		return fmt.Errorf("Not enough arguments for CDI hook: %v", hook.Args)
	}

	processCreateSymlinksHook := func(args []string) error {
		// The list of arguments is either
		// `--link <target>::<link> --link <target>::<link> ...`
		// or `--link=<target>::<link> --link=<target>::<link> ...`
		// and we need to handle both cases as they are both valid.
		var targetWithLink string
		for i := 0; i < len(args); i += 1 {
			if args[i] == "--link" {
				continue
			}

			if strings.Contains(args[i], "=") {
				// We can assume the arg is `--link=<target>::<link>`
				splitted := strings.Split(args[i], "=")
				if len(splitted) != 2 {
					return fmt.Errorf("Invalid symlink arg %q", args[i])
				}

				targetWithLink = splitted[1]
			} else {
				// We can assume the arg is `<target>::<link>`
				targetWithLink = args[i]
			}

			entry := strings.Split(targetWithLink, "::")
			if len(entry) != 2 {
				return fmt.Errorf("Invalid symlink entry %q", targetWithLink)
			}

			// `Link` is always an absolute path and `Target` (a `Link` points to a `Target`) is relative
			// to the `Link` location in the CDI spec. A resolving operation will be needed to have the absolute
			// path of the `Target`
			hooks.Symlinks = append(hooks.Symlinks, SymlinkEntry{Target: strings.TrimPrefix(entry[0], rootPath), Link: strings.TrimPrefix(entry[1], rootPath)})
		}

		return nil
	}

	processUpdateLdcacheHook := func(args []string) error {
		// As above, the list of arguments is either
		// `--folder <folder> --folder <folder> ...`
		// or `--folder=<folder> --folder=<folder> ...`
		// and we need to handle both cases as they are both valid.
		var folder string
		for i := 0; i < len(args); i += 1 {
			if args[i] == "--folder" {
				continue
			}

			if strings.Contains(args[i], "=") {
				// We can assume the arg is `--folder=<folder>`
				splitted := strings.Split(args[i], "=")
				if len(splitted) != 2 {
					return fmt.Errorf("Invalid CDI folder arg %q", args[i])
				}

				folder = splitted[1]
			} else {
				// We can assume the arg is `<folder>`
				folder = args[i]
			}

			hooks.LDCacheUpdates = append(hooks.LDCacheUpdates, folder)
		}

		return nil
	}

	processHooks := map[string]func([]string) error{
		"create-symlinks": processCreateSymlinksHook,
		"update-ldcache":  processUpdateLdcacheHook,
	}

	for i, arg := range hook.Args {
		process, supported := processHooks[arg]
		if supported {
			if len(hook.Args) > i+1 {
				// We pass in only the arguments,
				// not the hook name which is not relevant in the process functions
				return process(hook.Args[i+1:])
			}
		}
	}

	return nil
}

// applyContainerEdits updates the configDevices and the hooks with CDI "container edits"
// (edits are user space libraries to mount and char device to pass to the container).
func applyContainerEdits(edits specs.ContainerEdits, configDevices *ConfigDevices, hooks *Hooks) error {
	for _, d := range edits.DeviceNodes {
		if d == nil {
			continue
		}

		err := specDevToInstanceDev(configDevices, *d)
		if err != nil {
			return err
		}
	}

	for _, hook := range edits.Hooks {
		err := specHookToLXDCDIHook(hook, hooks)
		if err != nil {
			return err
		}
	}

	return nil
}

// GenerateFromCDI does several things:
// 1. Generate a CDI specification from a CDI ID and an instance. According the
// the specified 'vendor', 'class' and 'name' (this assembled triplet is called
// a fully-qualified CDI ID. We'll just call it ID in the context of this
// package), the CDI specification is generated. The CDI specification is a
// JSON-like format that describes a per-device configuration and general
// container edits.
// - Per-device configuration represents a single device identified by the CDI
// ID (e.g. /dev/nvidia0) It contains device-specific 'container edits' (device nodes, hooks and
// mounts). We only select those that matches the provided CDI ID (which can be
// a device index, uuid or 'all'). The hooks will be centralized in a single
// resource file that will be read and executed as a LXC `lxc.hook.mount` hook,
// through LXD's `callhook` command.
// - General container edits are instructions on how to modify the container
// when a CDI device is used. These edits are not specific to a single device,
// rather they apply to all devices of a given class/vendor. These edits must be
// applied if one or multiple devices are getting passed through (e.g. /dev/nvidia-uvm)
// 2. Process per-device configuration(s): we convert this information into a
// map of devices (keyed by their path given in the spec, it mapped to a map of
// device properties). We also collect mounts (but we do not process them yet)
// and hooks.
// 3. Process general container edits in the same fashion.
// 4. Process all the mounts collected from the spec in order to turn
// them into disk devices. This operations generate a side effect: it generates
// a list of indirect symlinks (see `specMountToLXDDev`)
// 5. Merge all the hooks (direct + indirect) into a single list of hooks.
func GenerateFromCDI(isCore bool, inst instance.Instance, cdiID ID) (*ConfigDevices, *Hooks, error) {
	// 1. Generate the CDI specification
	spec, err := generateSpec(isCore, cdiID, inst)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to generate CDI spec: %w", err)
	}

	// Initialize the hooks as empty
	hooks := &Hooks{ContainerRootFS: inst.RootfsPath()}
	mounts := make([]*specs.Mount, 0)
	configDevices := &ConfigDevices{UnixCharDevs: make([]map[string]string, 0), BindMounts: make([]map[string]string, 0)}

	// 2. Process the specific device configuration
	for _, device := range spec.Devices {
		// If cdiID.Name is 'all', then the edits for all the visible devices will be
		// applied from the generated spec (it has a special case 'all' device entry).
		// Otherwise, only the edits for the specific device identified by cdiID.Name
		// will be applied.
		if device.Name == cdiID.Name {
			err := applyContainerEdits(device.ContainerEdits, configDevices, hooks)
			if err != nil {
				return nil, nil, err
			}

			mounts = append(mounts, device.ContainerEdits.Mounts...)
			break
		}
	}

	// 3. Process general container edits (device-independent)
	err = applyContainerEdits(spec.ContainerEdits, configDevices, hooks)
	if err != nil {
		return nil, nil, err
	}

	mounts = append(mounts, spec.ContainerEdits.Mounts...)

	// 4. Transform mounts into disk devices and collect indirect symlinks
	indirectSymlinks, err := specMountToInstanceDev(configDevices, cdiID, mounts)
	if err != nil {
		return nil, nil, err
	}

	// 5. merge the indirectSymlinks to the list of symlinks to be create in the hooks
	hooks.Symlinks = append(hooks.Symlinks, indirectSymlinks...)
	return configDevices, hooks, nil
}

// ReloadConfigDevicesFromDisk reads the paths to the CDI configuration devices file from the disk.
// This is useful in order to cache the CDI configuration devices file so that wee don't have to re-generate a CDI spec whhen stopping the container.
func ReloadConfigDevicesFromDisk(pathsToConfigDevicesFilePath string) (ConfigDevices, error) {
	// Load the config devices file from the disk
	pathsToCDIConfigDevicesFile, err := os.Open(pathsToConfigDevicesFilePath)
	if err != nil {
		return ConfigDevices{}, fmt.Errorf("Failed to open the paths to CDI conf file at %q: %w", pathsToConfigDevicesFilePath, err)
	}

	defer pathsToCDIConfigDevicesFile.Close()

	configDevices := &ConfigDevices{}
	err = json.NewDecoder(pathsToCDIConfigDevicesFile).Decode(configDevices)
	if err != nil {
		return ConfigDevices{}, fmt.Errorf("Failed to decode the paths to CDI conf file at %q: %w", pathsToConfigDevicesFilePath, err)
	}

	return *configDevices, nil
}
