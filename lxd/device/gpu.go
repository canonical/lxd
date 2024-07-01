package device

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tags.cncf.io/container-device-interface/specs-go"

	"github.com/canonical/lxd/lxd/device/cdi"
	"github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/validate"
)

func gpuValidationRules(requiredFields []string, optionalFields []string) map[string]func(value string) error {
	// Define a set of default validators for each field name.
	defaultValidators := map[string]func(value string) error{
		// lxdmeta:generate(entities=device-gpu-{physical+mdev+mig}; group=device-conf; key=vendorid)
		//
		// ---
		//  type: string
		//  shortdesc: Vendor ID of the GPU device

		// lxdmeta:generate(entities=device-gpu-sriov; group=device-conf; key=vendorid)
		//
		// ---
		//  type: string
		//  shortdesc: Vendor ID of the parent GPU device
		"vendorid": validate.Optional(validate.IsDeviceID),
		// lxdmeta:generate(entities=device-gpu-{physical+mdev+mig}; group=device-conf; key=productid)
		//
		// ---
		//  type: string
		//  shortdesc: Product ID of the GPU device

		// lxdmeta:generate(entities=device-gpu-sriov; group=device-conf; key=productid)
		//
		// ---
		//  type: string
		//  shortdesc: Product ID of the parent GPU device
		"productid": validate.Optional(validate.IsDeviceID),
		// lxdmeta:generate(entities=device-gpu-physical; group=device-conf; key=id)
		// The ID can either be the DRM card ID of the GPU device or a fully-qualified Container Device Interface (CDI) name.
		// The use of a fully-qualified CDI name is only allowed for a device added to a container, not a VM.
		// Here are some examples of fully-qualified CDI names:
		//
		// - `nvidia.com/gpu=gpu0` : this instructs LXD to operate a discrete GPU (dGPU) passthrough of brand NVIDIA with the first discovered GPU on your system. You can use the `nvidia-smi` tool on your host to know which identifier to use.
		// - `nvidia.com/gpu=igpu0` : this instructs LXD to operate an integrated GPU (iGPU) passthrough of brand NVIDIA with the first discovered iGPU on your system. An iGPU won't be detected by the `nvidia-smi` tool, so we encourage the user to use the `nvidia-ctk` tool through `nvidia-ctk cdi generate` to list the available iGPU devices.
		// - `nvidia.com/gpu=all` : this instructs LXD to pass all the host GPUs of brand NVIDIA through the container.
		// ---
		//  type: string
		//  shortdesc: ID of the GPU device

		// lxdmeta:generate(entities=device-gpu-{mdev+mig}; group=device-conf; key=id)
		//
		// ---
		//  type: string
		//  shortdesc: DRM card ID of the GPU device

		// lxdmeta:generate(entities=device-gpu-sriov; group=device-conf; key=id)
		//
		// ---
		//  type: string
		//  shortdesc: DRM card ID of the parent GPU device
		"id": validate.IsAny,
		// lxdmeta:generate(entities=device-gpu-{physical+mdev+mig}; group=device-conf; key=pci)
		//
		// ---
		//  type: string
		//  shortdesc: PCI address of the GPU device

		// lxdmeta:generate(entities=device-gpu-sriov; group=device-conf; key=pci)
		//
		// ---
		//  type: string
		//  shortdesc: PCI address of the parent GPU device
		"pci": validate.IsPCIAddress,
		// lxdmeta:generate(entities=device-gpu-physical; group=device-conf; key=uid)
		//
		// ---
		//  type: integer
		//  defaultdesc: `0`
		//  condition: container
		//  shortdesc: UID of the device owner in the container
		"uid": unixValidUserID,
		// lxdmeta:generate(entities=device-gpu-physical; group=device-conf; key=gid)
		//
		// ---
		//  type: integer
		//  defaultdesc: `0`
		//  condition: container
		//  shortdesc: GID of the device owner in the container
		"gid": unixValidUserID,
		// lxdmeta:generate(entities=device-gpu-physical; group=device-conf; key=mode)
		//
		// ---
		//  type: integer
		//  defaultdesc: `0660`
		//  condition: container
		//  shortdesc: Mode of the device in the container
		"mode": unixValidOctalFileMode,
		// lxdmeta:generate(entities=device-gpu-mig; group=device-conf; key=mig.gi)
		//
		// ---
		//  type: integer
		//  shortdesc: Existing MIG GPU instance ID
		"mig.gi": validate.IsUint8,
		// lxdmeta:generate(entities=device-gpu-mig; group=device-conf; key=mig.ci)
		//
		// ---
		//  type: integer
		//  shortdesc: Existing MIG compute instance ID
		"mig.ci": validate.IsUint8,
		// lxdmeta:generate(entities=device-gpu-mig; group=device-conf; key=mig.uuid)
		// You can omit the `MIG-` prefix when specifying this option.
		// ---
		//  type: string
		//  shortdesc: Existing MIG device UUID
		"mig.uuid": gpuValidMigUUID,
		// lxdmeta:generate(entities=device-gpu-mdev; group=device-conf; key=mdev)
		// For example: `i915-GVTg_V5_4`
		// ---
		//  type: string
		//  defaultdesc: `0`
		//  required: yes
		//  shortdesc: The `mdev` profile to use
		"mdev": validate.IsAny,
	}

	validators := map[string]func(value string) error{}

	for _, k := range optionalFields {
		defaultValidator := defaultValidators[k]

		// If field doesn't have a known validator, it is an unknown field, skip.
		if defaultValidator == nil {
			continue
		}

		// Wrap the default validator in an empty check as field is optional.
		validators[k] = func(value string) error {
			if value == "" {
				return nil
			}

			return defaultValidator(value)
		}
	}

	// Add required fields last, that way if they are specified in both required and optional
	// field sets, the required one will overwrite the optional validators.
	for _, k := range requiredFields {
		defaultValidator := defaultValidators[k]

		// If field doesn't have a known validator, it is an unknown field, skip.
		if defaultValidator == nil {
			continue
		}

		// Wrap the default validator in a not empty check as field is required.
		validators[k] = func(value string) error {
			err := validate.IsNotEmpty(value)
			if err != nil {
				return err
			}

			return defaultValidator(value)
		}
	}

	return validators
}

// Check if the device matches the given GPU card.
// It matches based on vendorid, pci, productid or id setting of the device.
func gpuSelected(device config.Device, gpu api.ResourcesGPUCard) bool {
	return !((device["vendorid"] != "" && gpu.VendorID != device["vendorid"]) ||
		(device["pci"] != "" && gpu.PCIAddress != device["pci"]) ||
		(device["productid"] != "" && gpu.ProductID != device["productid"]) ||
		(device["id"] != "" && (gpu.DRM == nil || fmt.Sprintf("%d", gpu.DRM.ID) != device["id"])))
}

// constructCDIDevNodeList builds a list of unix-char devices to be created from a CDI spec.
func constructCDIDevNodeList(nativeDevices map[string]map[string]string, d *specs.DeviceNode) error {
	if d == nil {
		return fmt.Errorf("CDI device node is nil")
	}

	if d.HostPath == "" && d.Path == "" {
		return fmt.Errorf("both hostPath and path are empty in the CDI device node: %v", *d)
	}

	var devPath string
	var hostPath string
	if d.HostPath != "" && d.Path == "" {
		devPath = d.HostPath
		hostPath = d.HostPath
	} else if d.HostPath == "" && d.Path != "" {
		// When the hostPath is empty, the path is the device path in the container.
		devPath = d.Path
		hostPath = d.Path
	} else {
		devPath = d.Path
		hostPath = d.HostPath
	}

	// choose a meaningfull device name based on the devPath
	deviceName := strings.Join(strings.Split(devPath, "/"), "__")
	nativeDevices[deviceName] = map[string]string{"type": "unix-char", "source": hostPath, "path": devPath}
	return nil
}

// configureCDIDevices will start or stop a unix device in the container rootfs based on the CDI device node
// and update the device run config. It will also start or stop a disk device per CDI mount and update the run config.
func configureCDIDevices(s *state.State, inst instance.Instance, nativeDevices map[string]map[string]string, chardevHandlerFunc func(*unixCommon, *config.RunConfig) error, diskdevHandlerFunc func(*disk, *config.RunConfig) error, rootRunConf *config.RunConfig) error {
	var err error
	devicesConfig := config.NewDevices(nativeDevices)
	revert := revert.New()
	defer revert.Fail()

	for deviceName, conf := range devicesConfig {
		switch conf["type"] {
		case "unix-char":
			charDev := &unixCommon{
				deviceCommon: deviceCommon{
					state:  s,
					inst:   inst,
					name:   deviceName,
					config: conf,
				},
			}

			err = chardevHandlerFunc(charDev, rootRunConf)
			if err != nil {
				return err
			}

		case "disk":
			disk := &disk{
				deviceCommon: deviceCommon{
					state:  s,
					inst:   inst,
					name:   deviceName,
					config: conf,
				},
			}

			err = diskdevHandlerFunc(disk, rootRunConf)
			if err != nil {
				return err
			}

		default:
			return fmt.Errorf("CDI device of wrong type: %s", conf["type"])
		}
	}

	revert.Success()
	return nil
}

// constructCDIMountList builds a list of disk devices to be created from a CDI spec.
func constructCDIMountList(nativeDevices map[string]map[string]string, cdiID cdi.ID, mounts []*specs.Mount, instPrivileged bool) ([]cdi.SymlinkEntry, error) {
	if len(mounts) == 0 {
		return nil, fmt.Errorf("CDI mounts are empty")
	}

	indirectSymlinks := make([]cdi.SymlinkEntry, 0)
	var splittedDevName []string
	var chosenOpts []string
	var deviceName string
	var err error

	shift := "false"
	if instPrivileged {
		shift = "true"
	}

	rootPath := ""
	if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
	}

	for _, mount := range mounts {
		if mount.HostPath == "" || mount.ContainerPath == "" {
			return nil, fmt.Errorf("hostPath or containerPath is empty in the CDI mount: %v", *mount)
		}

		chosenOpts = []string{}
		for _, opt := range mount.Options {
			if !shared.ValueInSlice(opt, chosenOpts) {
				chosenOpts = append(chosenOpts, opt)
			}
		}

		chosenOptsStr := strings.Join(chosenOpts, ",")
		splittedDevName = strings.Split(mount.HostPath, "/")
		deviceName = splittedDevName[len(splittedDevName)-1]

		// mount.HostPath can be a symbolic link, so we need to evaluate it
		evaluatedHostPath, err := filepath.EvalSymlinks(mount.HostPath)
		if err != nil {
			return nil, err
		}

		if evaluatedHostPath != mount.HostPath && mount.ContainerPath == strings.TrimPrefix(mount.HostPath, rootPath) {
			indirectSymlinks = append(indirectSymlinks, cdi.SymlinkEntry{Target: strings.TrimPrefix(evaluatedHostPath, rootPath), Link: mount.ContainerPath})
			mount.ContainerPath = strings.TrimPrefix(evaluatedHostPath, rootPath)
		}

		nativeDevices[deviceName] = map[string]string{
			"type":              "disk",
			"source":            evaluatedHostPath,
			"path":              mount.ContainerPath,
			"shift":             shift,
			"raw.mount.options": chosenOptsStr,
		}
	}

	// If the user desires to run a nested docker container inside a LXD container,
	// the Tegra CSV files also need to be mounted so that the nvidia docker runtime
	// can be auto-enabled as 'csv' mode.
	if cdiID.Vendor() == cdi.Nvidia && cdiID.DeviceType() == cdi.IGPU {
		tegraCSVFilesCandidates := cdi.DefaultNvidiaTegraCSVFiles()
		tegraCSVFiles := make([]string, 0)
		for _, candidate := range tegraCSVFilesCandidates {
			_, err = os.Stat(filepath.Join(rootPath, candidate))
			if err == nil {
				tegraCSVFiles = append(tegraCSVFiles, filepath.Join(rootPath, candidate))
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
			splittedDevName = strings.Split(tegraFile, "/")
			deviceName = splittedDevName[len(splittedDevName)-1]
			nativeDevices[deviceName] = map[string]string{
				"type":     "disk",
				"source":   tegraFile,
				"path":     strings.TrimPrefix(tegraFile, rootPath),
				"shift":    shift,
				"readonly": "true",
			}
		}
	}

	return indirectSymlinks, nil
}

// constructCDIHook will create a hook entry in a Hooks struct based on the CDI hook.
func constructCDIHook(hook *specs.Hook, hooks *cdi.Hooks) error {
	if hook == nil {
		return fmt.Errorf("CDI hook is nil")
	}

	if len(hook.Args) < 5 {
		return fmt.Errorf("CDI hook %q has not enough arguments", fmt.Sprintf("%s %v", hook.HookName, hook.Args))
	}

	rootPath := ""
	if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
	}

	switch hook.Args[2] {
	case "create-symlinks":
		for i := 4; i < len(hook.Args); i += 2 {
			entry := strings.Split(hook.Args[i], "::")
			if len(entry) != 2 {
				return fmt.Errorf("invalid symlink entry %q", hook.Args[i])
			}

			// `Link` is always an absolute path and `Target` (a `Link` points to a `Target`) is relative
			// to the `Link` location in the CDI spec. A resolving operation will be needed to have the absolute
			// path of the `Target`
			hooks.Symlinks = append(hooks.Symlinks, cdi.SymlinkEntry{Link: strings.TrimPrefix(entry[0], rootPath), Target: strings.TrimPrefix(entry[1], rootPath)})
		}

	case "update-ldcache":
		for i := 4; i < len(hook.Args); i += 2 {
			hooks.LDCacheUpdates = append(hooks.LDCacheUpdates, hook.Args[i])
		}

	case "chmod":
		// The CDI 'chmod' hook is not relevant for us.
		return nil
	default:
		return fmt.Errorf("unsupported CDI hook function %q", hook.Args[2])
	}

	return nil
}

// resolveTargetRelativeToLink converts a target relative to a link path into an absolute path.
func resolveTargetRelativeToLink(link, target string) (string, error) {
	if !filepath.IsAbs(link) {
		return "", fmt.Errorf("link must be an absolute path")
	}

	if filepath.IsAbs(target) {
		return target, nil
	}

	linkDir := filepath.Dir(link)
	absTarget := filepath.Join(linkDir, target)
	cleanPath := filepath.Clean(absTarget)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", err
	}

	return absPath, nil
}

// generateSymlinksCmd generate the shell command to be executed inside the container
// to create all the necessary symlinks and update the linker cache.
func generateSymlinksCmd(hooks *cdi.Hooks) (string, error) {
	symlinksCmd := ""
	l := len(hooks.Symlinks)

	for i, hook := range hooks.Symlinks {
		// Resolve hook link from target
		absTarget, err := resolveTargetRelativeToLink(hook.Link, hook.Target)
		if err != nil {
			return "", err
		}

		symlinksCmd += fmt.Sprintf("mkdir -p %s; ln -s %s %s", filepath.Dir(hook.Link), absTarget, hook.Link)
		if i+1 < l {
			symlinksCmd += "; "
		}
	}

	// Then remove the linker cache and regenerate it
	symlinksCmd += "; rm /etc/ld.so.cache; ldconfig"
	return symlinksCmd, nil
}

// constructCDIDevices construct all the CDI resources (device nodes, mounts and hooks).
func constructCDIDevices(inst instance.Instance, cdiID cdi.ID) (map[string]map[string]string, *cdi.Hooks, error) {
	// Generate the CDI specification
	spec, err := cdi.GenerateSpec(cdiID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CDI spec: %w", err)
	}

	var targetName string
	locator := cdiID.Locator()
	if locator == nil {
		targetName = string(cdi.All)
	} else if len(locator) == 1 {
		// In the official CDI specification, and iGPU is represented as "gpu<idx>"
		// So we use the cdi.SimpleGPU constant to represent it like a classic GPU.
		targetName = fmt.Sprintf("%s%s", cdi.SimpleGPU, locator[0])
	} else if len(locator) == 2 && cdiID.DeviceType() == cdi.MIG {
		targetName = fmt.Sprintf("%s%s:%s", cdi.MIG, locator[0], locator[1])
	} else {
		return nil, nil, fmt.Errorf("invalid CDI locator %v", locator)
	}

	// Initialize the hooks as empty
	hooks := &cdi.Hooks{ContainerRootFS: inst.RootfsPath()}
	mounts := make([]*specs.Mount, 0)
	nativeDevices := make(map[string]map[string]string)

	// Translate the CDI specification into device run config items
	// First, get the device specific configuration
	for _, device := range spec.Devices {
		if device.Name == targetName {
			for _, d := range device.ContainerEdits.DeviceNodes {
				err = constructCDIDevNodeList(nativeDevices, d)
				if err != nil {
					return nil, nil, err
				}
			}

			for _, hook := range device.ContainerEdits.Hooks {
				err = constructCDIHook(hook, hooks)
				if err != nil {
					return nil, nil, err
				}
			}

			mounts = append(mounts, device.ContainerEdits.Mounts...)
			break
		}
	}

	// Then, get the common configuration
	for _, generalDeviceNode := range spec.ContainerEdits.DeviceNodes {
		err = constructCDIDevNodeList(nativeDevices, generalDeviceNode)
		if err != nil {
			return nil, nil, err
		}
	}

	for _, generalHook := range spec.ContainerEdits.Hooks {
		err = constructCDIHook(generalHook, hooks)
		if err != nil {
			return nil, nil, err
		}
	}

	mounts = append(mounts, spec.ContainerEdits.Mounts...)

	// CDI Mounts are converted to LXD `disk` devices
	// If the instance is privileged, then we shift the disk device
	instConf := inst.ExpandedConfig()
	privileged := false
	if instConf["security.privileged"] == "true" {
		privileged = true
	}

	indirectSymlinks, err := constructCDIMountList(nativeDevices, cdiID, mounts, privileged)
	if err != nil {
		return nil, nil, err
	}

	// merge the indirectSymlinks to the list of symlinks to be create in the hooks
	hooks.Symlinks = append(hooks.Symlinks, indirectSymlinks...)

	return nativeDevices, hooks, nil
}

// startWithCDISpec will generate a CDI specification from a cdiID and translate its content
// into disk and unix-char devices, start them and return a reference to these underlying devices.
func startWithCDISpec(s *state.State, inst instance.Instance, cdiID cdi.ID, runConf *config.RunConfig) error {
	nativeDevices, hooks, err := constructCDIDevices(inst, cdiID)
	if err != nil {
		return err
	}

	startCharDevFunc := func(charDev *unixCommon, rootRunConf *config.RunConfig) error {
		charDevRunConf, err := charDev.Start()
		if err != nil {
			return fmt.Errorf("unix-char device (created through CDI) could not be started: %w", err)
		}

		rootRunConf.CGroups = append(rootRunConf.CGroups, charDevRunConf.CGroups...)
		rootRunConf.Mounts = append(rootRunConf.Mounts, charDevRunConf.Mounts...)
		rootRunConf.PostHooks = append(rootRunConf.PostHooks, charDevRunConf.PostHooks...)
		return nil
	}

	startDiskDevFunc := func(diskDev *disk, rootRunConf *config.RunConfig) error {
		err = diskDev.validateConfig(inst)
		if err != nil {
			return fmt.Errorf("disk device (created through CDI) config is not valid: %w", err)
		}

		err = diskDev.validateEnvironment()
		if err != nil {
			return fmt.Errorf("disk device (created through CDI) environment is not valid: %w", err)
		}

		diskRunConf, err := diskDev.startContainer()
		if err != nil {
			return fmt.Errorf("disk device (created through CDI) could not be started: %w", err)
		}

		rootRunConf.Mounts = append(rootRunConf.Mounts, diskRunConf.Mounts...)
		rootRunConf.PostHooks = append(rootRunConf.PostHooks, diskRunConf.PostHooks...)
		return nil
	}

	// configure all the devices (unix-char (representing the card and non-card devices) + disk (representing thee mounts))
	err = configureCDIDevices(s, inst, nativeDevices, startCharDevFunc, startDiskDevFunc, runConf)
	if err != nil {
		return err
	}

	// Finally, generate the hook command that will be executed
	// as a post start operation inside the container
	cmd, err := generateSymlinksCmd(hooks)
	if err != nil {
		return err
	}

	// Update the GPU device run config with the path to the hooks file
	runConf.GPUDevice = append(runConf.GPUDevice,
		[]config.RunConfigItem{
			{Key: cdi.CDIHookCmdKey, Value: cmd},
		}...)

	return nil
}

// stopWithCDISpec stops all the underlying unix-char and disk devices generated through a CDI specification.
// It also register all the postStop hooks.
func stopWithCDISpec(s *state.State, inst instance.Instance, cdiID cdi.ID, runConf *config.RunConfig) error {
	nativeDevices, _, err := constructCDIDevices(inst, cdiID)
	if err != nil {
		return err
	}

	stopCharDevFunc := func(charDev *unixCommon, rootRunConf *config.RunConfig) error {
		charDevRunConf, err := charDev.Stop()
		if err != nil {
			return fmt.Errorf("unix-char device (created through CDI) could not be stopped: %w", err)
		}

		rootRunConf.CGroups = append(rootRunConf.CGroups, charDevRunConf.CGroups...)
		rootRunConf.Mounts = append(rootRunConf.Mounts, charDevRunConf.Mounts...)
		rootRunConf.PostHooks = append(rootRunConf.PostHooks, charDevRunConf.PostHooks...)
		return nil
	}

	stopDiskDevFunc := func(diskDev *disk, rootRunConf *config.RunConfig) error {
		err = diskDev.validateConfig(inst)
		if err != nil {
			return fmt.Errorf("disk device (created through CDI) config is not valid: %w", err)
		}

		err = diskDev.validateEnvironment()
		if err != nil {
			return fmt.Errorf("disk device (created through CDI) environment is not valid: %w", err)
		}

		diskRunConf, err := diskDev.Stop()
		if err != nil {
			return fmt.Errorf("disk device (created through CDI) could not be stopped: %w", err)
		}

		rootRunConf.PostHooks = append(rootRunConf.PostHooks, diskRunConf.PostHooks...)
		rootRunConf.Mounts = append(rootRunConf.Mounts, diskRunConf.Mounts...)
		return nil
	}

	err = configureCDIDevices(s, inst, nativeDevices, stopCharDevFunc, stopDiskDevFunc, runConf)
	if err != nil {
		return err
	}

	return nil
}
