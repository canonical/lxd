package cdi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	"tags.cncf.io/container-device-interface/specs-go"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

// specDevToNativeDev builds a list of unix-char devices to be created from a CDI spec.
func specDevToNativeDev(configDevices *ConfigDevices, d specs.DeviceNode) error {
	if d.Path == "" {
		return fmt.Errorf("Device path is empty in the CDI device node: %v", d)
	}

	hostPath := d.HostPath
	if hostPath == "" {
		hostPath = d.Path // When the hostPath is empty, the path is the device path in the container.
	}

	if d.Major == 0 || d.Minor == 0 {
		stat := unix.Stat_t{}
		err := unix.Stat(hostPath, &stat)
		if err != nil {
			return err
		}

		d.Major = int64(unix.Major(uint64(stat.Rdev)))
		d.Minor = int64(unix.Minor(uint64(stat.Rdev)))
	}

	configDevices.UnixCharDevs = append(configDevices.UnixCharDevs, map[string]string{"type": "unix-char", "source": hostPath, "path": d.Path, "major": fmt.Sprintf("%d", d.Major), "minor": fmt.Sprintf("%d", d.Minor)})
	return nil
}

// specMountToNativeDev builds a list of disk mounts to be created from a CDI spec.
func specMountToNativeDev(configDevices *ConfigDevices, cdiID ID, mounts []*specs.Mount) ([]SymlinkEntry, error) {
	if len(mounts) == 0 {
		return nil, fmt.Errorf("CDI mounts are empty")
	}

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
			if !shared.ValueInSlice(opt, chosenOpts) {
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
			indirectSymlinks = append(indirectSymlinks, SymlinkEntry{Target: strings.TrimPrefix(evaluatedHostPath, rootPath), Link: mount.ContainerPath})
			mount.ContainerPath = strings.TrimPrefix(evaluatedHostPath, rootPath)
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
			return nil, fmt.Errorf("No CSV files detected for Tegra iGPU")
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
func specHookToLXDCDIHook(hook *specs.Hook, hooks *Hooks, l logger.Logger) error {
	if hook == nil {
		l.Warn("CDI hook is nil")
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
func applyContainerEdits(edits specs.ContainerEdits, configDevices *ConfigDevices, hooks *Hooks, existingMounts []*specs.Mount, l logger.Logger) ([]*specs.Mount, error) {
	for _, d := range edits.DeviceNodes {
		if d == nil {
			l.Warn("One CDI DeviceNode is nil")
			continue
		}

		err := specDevToNativeDev(configDevices, *d)
		if err != nil {
			return nil, err
		}
	}

	for _, hook := range edits.Hooks {
		err := specHookToLXDCDIHook(hook, hooks, l)
		if err != nil {
			return nil, err
		}
	}

	return append(existingMounts, edits.Mounts...), nil
}

// GenerateFromCDI does several things:
//
//  1. It generates a CDI specification from a CDI ID and an instance.
//     According the the specified 'vendor', 'class' and 'name' (this assembled triplet is called a fully-qualified CDI ID. We'll just call it ID in the context of this package), the CDI specification is generated.
//     The CDI specification is a JSON-like format. It is divided into two parts: the 'specific device' configuration and the 'general device' configuration.
//     - The 'specific device' configuration: this is a list of 'container edits' that can be added to the container runtime.
//     According to the CDI ID (vendor, class, name), we only select the 'container edits' that matches the CDI ID.
//     The 'container edits' are a list of device nodes, hooks and mounts that must be added to the container runtime.
//     - The 'general device' configuration: this is a single 'container edits' entry runtime that must be passed to the container runtime in ant case. Which unix char devices need to be passed
//     (e.g, special GPU memory controller device, etc.)? Which user space libraries need to be mounted (e.g, CUDA libraries for NVIDIA, etc.)?
//     Which hooks need to be executed (e.g, symlinks to create, folder entries to add to ldcache, etc.))?
//     In our case, these edits will be interpreted either as disk or unix char mounts passed to the container.
//     The hooks will be centralized in a single resource file that will be read and executed as a LXC `lxc.hook.mount` hook,
//     through LXD's `callhook` command.
//  2. We first process the 'specific device' configuration: we convert this information into a map of devices
//     (keyed by their path given in the spec, it mapped to a map of device properties). We also collect the specific mounts (but we do not process them yet) and hooks.
//  3. We then process the 'general device' configuration in the same fashion.
//  4. Now we process all the mounts we collected from the spec in order to turn them into disk devices.
//     This operations generate a side effect: it generates a list of indirect symlinks (see `specMountToNativeDev`)
//  5. Merge all the hooks (direct + indirect) into a single list of hooks.
func GenerateFromCDI(inst instance.Instance, cdiID ID, l logger.Logger) (*ConfigDevices, *Hooks, error) {
	// 1. Generate the CDI specification
	spec, err := generateSpec(cdiID, inst)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to generate CDI spec: %w", err)
	}

	// Initialize the hooks as empty
	hooks := &Hooks{ContainerRootFS: inst.RootfsPath()}
	mounts := make([]*specs.Mount, 0)
	configDevices := &ConfigDevices{UnixCharDevs: make([]map[string]string, 0), BindMounts: make([]map[string]string, 0)}

	// 2. Process the specific device configuration
	for _, device := range spec.Devices {
		if device.Name == cdiID.Name {
			mounts, err = applyContainerEdits(device.ContainerEdits, configDevices, hooks, mounts, l)
			if err != nil {
				return nil, nil, err
			}

			break
		}
	}

	// 3. Process general device configuration
	mounts, err = applyContainerEdits(spec.ContainerEdits, configDevices, hooks, mounts, l)
	if err != nil {
		return nil, nil, err
	}

	// 4. Process the mounts
	indirectSymlinks, err := specMountToNativeDev(configDevices, cdiID, mounts)
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
