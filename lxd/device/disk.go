package device

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/cgroup"
	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/units"
)

// Special disk "source" value used for generating a VM cloud-init config ISO.
const diskSourceCloudInit = "cloud-init:config"

type diskBlockLimit struct {
	readBps   int64
	readIops  int64
	writeBps  int64
	writeIops int64
}

type disk struct {
	deviceCommon
}

// isRequired indicates whether the supplied device config requires this device to start OK.
func (d *disk) isRequired(devConfig deviceConfig.Device) bool {
	// Defaults to required.
	if (devConfig["required"] == "" || shared.IsTrue(devConfig["required"])) && !shared.IsTrue(devConfig["optional"]) {
		return true
	}

	return false
}

// validateConfig checks the supplied config for correctness.
func (d *disk) validateConfig() error {
	if d.inst.Type() != instancetype.Container && d.inst.Type() != instancetype.VM {
		return ErrUnsupportedDevType
	}

	// Supported propagation types.
	// If an empty value is supplied the default behavior is to assume "private" mode.
	// These come from https://www.kernel.org/doc/Documentation/filesystems/sharedsubtree.txt
	propagationTypes := []string{"", "private", "shared", "slave", "unbindable", "rshared", "rslave", "runbindable", "rprivate"}
	validatePropagation := func(input string) error {
		if !shared.StringInSlice(d.config["bind"], propagationTypes) {
			return fmt.Errorf("Invalid propagation value. Must be one of: %s", strings.Join(propagationTypes, ", "))
		}

		return nil
	}

	rules := map[string]func(string) error{
		"required":          shared.IsBool,
		"optional":          shared.IsBool, // "optional" is deprecated, replaced by "required".
		"readonly":          shared.IsBool,
		"recursive":         shared.IsBool,
		"shift":             shared.IsBool,
		"source":            shared.IsAny,
		"limits.read":       shared.IsAny,
		"limits.write":      shared.IsAny,
		"limits.max":        shared.IsAny,
		"size":              shared.IsAny,
		"pool":              shared.IsAny,
		"propagation":       validatePropagation,
		"raw.mount.options": shared.IsAny,
		"ceph.cluster_name": shared.IsAny,
		"ceph.user_name":    shared.IsAny,
	}

	// VMs don't use the "path" property, but containers need it, so if we are validating a profile that can
	// be used for all instance types, we must allow any value.
	if d.inst.Name() == instance.ProfileValidationName {
		rules["path"] = shared.IsAny
	} else if d.inst.Type() == instancetype.Container || d.config["path"] == "/" {
		// If we are validating a container or the root device is being validated, then require the value.
		rules["path"] = shared.IsNotEmpty
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	if d.config["required"] != "" && d.config["optional"] != "" {
		return fmt.Errorf("Cannot use both \"required\" and deprecated \"optional\" properties at the same time")
	}

	if d.config["source"] == "" && d.config["path"] != "/" {
		return fmt.Errorf("Disk entry is missing the required \"source\" property")
	}

	if d.config["path"] == "/" && d.config["source"] != "" {
		return fmt.Errorf("Root disk entry may not have a \"source\" property set")
	}

	if d.config["path"] == "/" && d.config["pool"] == "" {
		return fmt.Errorf("Root disk entry must have a \"pool\" property set")
	}

	if d.config["size"] != "" && d.config["path"] != "/" {
		return fmt.Errorf("Only the root disk may have a size quota")
	}

	if d.config["recursive"] != "" && (d.config["path"] == "/" || !shared.IsDir(shared.HostPath(d.config["source"]))) {
		return fmt.Errorf("The recursive option is only supported for additional bind-mounted paths")
	}

	if !(strings.HasPrefix(d.config["source"], "ceph:") || strings.HasPrefix(d.config["source"], "cephfs:")) && (d.config["ceph.cluster_name"] != "" || d.config["ceph.user_name"] != "") {
		return fmt.Errorf("Invalid options ceph.cluster_name/ceph.user_name for source: %s", d.config["source"])
	}

	// Check no other devices also have the same path as us. Use LocalDevices for this check so
	// that we can check before the config is expanded or when a profile is being checked.
	// Don't take into account the device names, only count active devices that point to the
	// same path, so that if merged profiles share the same the path and then one is removed
	// this can still be cleanly removed.
	pathCount := 0
	for _, devConfig := range d.inst.LocalDevices() {
		if devConfig["type"] == "disk" && d.config["path"] != "" && devConfig["path"] == d.config["path"] {
			pathCount++
			if pathCount > 1 {
				return fmt.Errorf("More than one disk device uses the same path %q", d.config["path"])

			}
		}
	}

	// When we want to attach a storage volume created via the storage api the "source" only
	// contains the name of the storage volume, not the path where it is mounted. So only check
	// for the existence of "source" when "pool" is empty.
	if d.config["pool"] == "" && d.config["source"] != "" && d.config["source"] != diskSourceCloudInit && d.isRequired(d.config) && !shared.PathExists(shared.HostPath(d.config["source"])) &&
		!strings.HasPrefix(d.config["source"], "ceph:") && !strings.HasPrefix(d.config["source"], "cephfs:") {
		return fmt.Errorf("Missing source '%s' for disk '%s'", d.config["source"], d.name)
	}

	if d.config["pool"] != "" {
		if d.config["shift"] != "" {
			return fmt.Errorf("The \"shift\" property cannot be used with custom storage volumes")
		}

		if filepath.IsAbs(d.config["source"]) {
			return fmt.Errorf("Storage volumes cannot be specified as absolute paths")
		}

		_, err := d.state.Cluster.StoragePoolGetID(d.config["pool"])
		if err != nil {
			return fmt.Errorf("The \"%s\" storage pool doesn't exist", d.config["pool"])
		}

		// Only check storate volume is available if we are validating an instance device
		// and not a profile device (check for ProfileValidationName name), and we have least
		// one expanded device (this is so we only do this expensive check after devices
		// have been expanded).
		if d.inst.Name() != instance.ProfileValidationName && len(d.inst.ExpandedDevices()) > 0 && d.config["source"] != "" && d.config["path"] != "/" {
			isAvailable, err := d.state.Cluster.StorageVolumeIsAvailable(d.config["pool"], d.config["source"])
			if err != nil {
				return fmt.Errorf("Check if volume is available: %v", err)
			}
			if !isAvailable {
				return fmt.Errorf("Storage volume %q is already attached to an instance on a different node", d.config["source"])
			}
		}
	}

	return nil
}

// getDevicePath returns the absolute path on the host for this instance and supplied device config.
func (d *disk) getDevicePath(devName string, devConfig deviceConfig.Device) string {
	relativeDestPath := strings.TrimPrefix(devConfig["path"], "/")
	devPath := deviceNameEncode(deviceJoinPath("disk", devName, relativeDestPath))
	return filepath.Join(d.inst.DevicesPath(), devPath)
}

// validateEnvironment checks the runtime environment for correctness.
func (d *disk) validateEnvironment() error {
	if shared.IsTrue(d.config["shift"]) && !d.state.OS.Shiftfs {
		return fmt.Errorf("shiftfs is required by disk entry but isn't supported on system")
	}

	if d.inst.Type() != instancetype.VM && d.config["source"] == diskSourceCloudInit {
		return fmt.Errorf("disks with source=%s are only supported by virtual machines", diskSourceCloudInit)
	}

	return nil
}

// CanHotPlug returns whether the device can be managed whilst the instance is running, it also
// returns a list of fields that can be updated without triggering a device remove & add.
func (d *disk) CanHotPlug() (bool, []string) {
	return true, []string{"limits.max", "limits.read", "limits.write", "size"}
}

// Start is run when the device is added to the instance.
func (d *disk) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	if d.inst.Type() == instancetype.VM {
		return d.startVM()
	}

	return d.startContainer()
}

// startContainer starts the disk device for a container instance.
func (d *disk) startContainer() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}
	isReadOnly := shared.IsTrue(d.config["readonly"])

	// Apply cgroups only after all the mounts have been processed.
	runConf.PostHooks = append(runConf.PostHooks, func() error {
		runConf := deviceConfig.RunConfig{}

		err := d.generateLimits(&runConf)
		if err != nil {
			return err
		}

		err = d.inst.DeviceEventHandler(&runConf)
		if err != nil {
			return err
		}

		return nil
	})

	// Deal with a rootfs.
	if shared.IsRootDiskDevice(d.config) {
		// Set the rootfs path.
		rootfs := deviceConfig.RootFSEntryItem{
			Path: d.inst.RootfsPath(),
		}

		// Read-only rootfs (unlikely to work very well).
		if isReadOnly {
			rootfs.Opts = append(rootfs.Opts, "ro")
		}

		v := d.volatileGet()

		// Handle previous requests for setting new quotas.
		if v["apply_quota"] != "" {
			err := d.applyQuota(v["apply_quota"])
			if err != nil {
				return nil, err
			}

			// Remove volatile apply_quota key if successful.
			err = d.volatileSet(map[string]string{"apply_quota": ""})
			if err != nil {
				return nil, err
			}
		}

		runConf.RootFS = rootfs
	} else {
		// Source path.
		srcPath := shared.HostPath(d.config["source"])

		// Destination path.
		destPath := d.config["path"]
		relativeDestPath := strings.TrimPrefix(destPath, "/")

		// Option checks.
		isRecursive := shared.IsTrue(d.config["recursive"])

		// If we want to mount a storage volume from a storage pool we created via our
		// storage api, we are always mounting a directory.
		isFile := false
		if d.config["pool"] == "" {
			isFile = !shared.IsDir(srcPath) && !IsBlockdev(srcPath)
		}

		ownerShift := deviceConfig.MountOwnerShiftNone
		if shared.IsTrue(d.config["shift"]) {
			ownerShift = deviceConfig.MountOwnerShiftDynamic
		}

		// If ownerShift is none and pool is specified then check whether the pool itself
		// has owner shifting enabled, and if so enable shifting on this device too.
		if ownerShift == deviceConfig.MountOwnerShiftNone && d.config["pool"] != "" {
			poolID, _, err := d.state.Cluster.StoragePoolGet(d.config["pool"])
			if err != nil {
				return nil, err
			}

			_, volume, err := d.state.Cluster.StoragePoolNodeVolumeGetTypeByProject(d.inst.Project(), d.config["source"], db.StoragePoolVolumeTypeCustom, poolID)
			if err != nil {
				return nil, err
			}

			if shared.IsTrue(volume.Config["security.shifted"]) {
				ownerShift = "dynamic"
			}
		}

		options := []string{}
		if isReadOnly {
			options = append(options, "ro")
		}

		if isRecursive {
			options = append(options, "rbind")
		} else {
			options = append(options, "bind")
		}

		if d.config["propagation"] != "" {
			options = append(options, d.config["propagation"])
		}

		if isFile {
			options = append(options, "create=file")
		} else {
			options = append(options, "create=dir")
		}

		sourceDevPath, err := d.createDevice()
		if err != nil {
			return nil, err
		}

		if sourceDevPath != "" {
			// Instruct LXD to perform the mount.
			runConf.Mounts = append(runConf.Mounts, deviceConfig.MountEntryItem{
				DevPath:    sourceDevPath,
				TargetPath: relativeDestPath,
				FSType:     "none",
				Opts:       options,
				OwnerShift: ownerShift,
			})

			// Unmount host-side mount once instance is started.
			runConf.PostHooks = append(runConf.PostHooks, d.postStart)
		}
	}

	return &runConf, nil
}

// startVM starts the disk device for a virtual machine instance.
func (d *disk) startVM() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}

	if shared.IsRootDiskDevice(d.config) {
		// The root disk device is special as it is given the first device ID and boot order in VM config.
		runConf.RootFS.Path = d.config["path"]
		return &runConf, nil
	} else if d.config["source"] == diskSourceCloudInit {
		// This is a special virtual disk source that can be attached to a VM to provide cloud-init config.
		isoPath, err := d.generateVMConfigDrive()
		if err != nil {
			return nil, err
		}

		runConf.Mounts = []deviceConfig.MountEntryItem{
			{
				DevPath:    isoPath,
				TargetPath: d.name,
			},
		}
		return &runConf, nil
	} else if d.config["source"] != "" {
		// This is a normal disk device or image.
		if !shared.PathExists(d.config["source"]) {
			return nil, fmt.Errorf("Cannot find disk source")
		}
		runConf.Mounts = []deviceConfig.MountEntryItem{
			{
				DevPath:    d.config["source"],
				TargetPath: d.name,
			},
		}
		return &runConf, nil
	}

	return nil, fmt.Errorf("Disk type not supported for VMs")
}

// postStart is run after the instance is started.
func (d *disk) postStart() error {
	devPath := d.getDevicePath(d.name, d.config)

	// Unmount the host side.
	err := unix.Unmount(devPath, unix.MNT_DETACH)
	if err != nil {
		return err
	}

	return nil
}

// Update applies configuration changes to a started device.
func (d *disk) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	if d.inst.Type() == instancetype.VM && !shared.IsRootDiskDevice(d.config) {
		return fmt.Errorf("Non-root disks not supported for VMs")
	}

	if shared.IsRootDiskDevice(d.config) {
		// Make sure we have a valid root disk device (and only one).
		expandedDevices := d.inst.ExpandedDevices()
		newRootDiskDeviceKey, _, err := shared.GetRootDiskDevice(expandedDevices.CloneNative())
		if err != nil {
			return errors.Wrap(err, "Detect root disk device")
		}

		// Retrieve the first old root disk device key, even if there are duplicates.
		oldRootDiskDeviceKey := ""
		for k, v := range oldDevices {
			if shared.IsRootDiskDevice(v) {
				oldRootDiskDeviceKey = k
				break
			}
		}

		// Check for pool change.
		oldRootDiskDevicePool := oldDevices[oldRootDiskDeviceKey]["pool"]
		newRootDiskDevicePool := expandedDevices[newRootDiskDeviceKey]["pool"]
		if oldRootDiskDevicePool != newRootDiskDevicePool {
			return fmt.Errorf("The storage pool of the root disk can only be changed through move")
		}

		// Deal with quota changes.
		oldRootDiskDeviceSize := oldDevices[oldRootDiskDeviceKey]["size"]
		newRootDiskDeviceSize := expandedDevices[newRootDiskDeviceKey]["size"]

		// Apply disk quota changes.
		if newRootDiskDeviceSize != oldRootDiskDeviceSize {
			err := d.applyQuota(newRootDiskDeviceSize)
			if err == storagePools.ErrRunningQuotaResizeNotSupported {
				// Save volatile apply_quota key for next boot if cannot apply now.
				err = d.volatileSet(map[string]string{"apply_quota": newRootDiskDeviceSize})
				if err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
		}
	}

	// Only apply IO limits if instance is running.
	if isRunning {
		runConf := deviceConfig.RunConfig{}
		err := d.generateLimits(&runConf)
		if err != nil {
			return err
		}

		err = d.inst.DeviceEventHandler(&runConf)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *disk) applyQuota(newSize string) error {
	return StorageRootFSApplyQuota(d.state, d.inst, newSize)
}

// generateLimits adds a set of cgroup rules to apply specified limits to the supplied RunConfig.
func (d *disk) generateLimits(runConf *deviceConfig.RunConfig) error {
	// Disk priority limits.
	diskPriority := d.inst.ExpandedConfig()["limits.disk.priority"]
	if diskPriority != "" {
		if d.state.OS.CGInfo.Supports(cgroup.BlkioWeight, nil) {
			priorityInt, err := strconv.Atoi(diskPriority)
			if err != nil {
				return err
			}

			priority := priorityInt * 100

			// Minimum valid value is 10
			if priority == 0 {
				priority = 10
			}

			runConf.CGroups = append(runConf.CGroups, deviceConfig.RunConfigItem{
				Key:   "blkio.weight",
				Value: fmt.Sprintf("%d", priority),
			})
		} else {
			return fmt.Errorf("Cannot apply limits.disk.priority as blkio.weight cgroup controller is missing")
		}
	}

	// Disk throttle limits.
	hasDiskLimits := false
	for _, dev := range d.inst.ExpandedDevices() {
		if dev["type"] != "disk" {
			continue
		}

		if dev["limits.read"] != "" || dev["limits.write"] != "" || dev["limits.max"] != "" {
			hasDiskLimits = true
		}
	}

	if hasDiskLimits {
		if !d.state.OS.CGInfo.Supports(cgroup.Blkio, nil) {
			return fmt.Errorf("Cannot apply disk limits as blkio cgroup controller is missing")
		}

		diskLimits, err := d.getDiskLimits()
		if err != nil {
			return err
		}

		for block, limit := range diskLimits {
			if limit.readBps > 0 {
				runConf.CGroups = append(runConf.CGroups, deviceConfig.RunConfigItem{
					Key:   "blkio.throttle.read_bps_device",
					Value: fmt.Sprintf("%s %d", block, limit.readBps),
				})
			}

			if limit.readIops > 0 {
				runConf.CGroups = append(runConf.CGroups, deviceConfig.RunConfigItem{
					Key:   "blkio.throttle.read_iops_device",
					Value: fmt.Sprintf("%s %d", block, limit.readIops),
				})
			}

			if limit.writeBps > 0 {
				runConf.CGroups = append(runConf.CGroups, deviceConfig.RunConfigItem{
					Key:   "blkio.throttle.write_bps_device",
					Value: fmt.Sprintf("%s %d", block, limit.writeBps),
				})
			}

			if limit.writeIops > 0 {
				runConf.CGroups = append(runConf.CGroups, deviceConfig.RunConfigItem{
					Key:   "blkio.throttle.write_iops_device",
					Value: fmt.Sprintf("%s %d", block, limit.writeIops),
				})
			}
		}
	}

	return nil
}

// createDevice creates a disk device mount on host.
func (d *disk) createDevice() (string, error) {
	// Paths.
	devPath := d.getDevicePath(d.name, d.config)
	srcPath := shared.HostPath(d.config["source"])

	isRequired := d.isRequired(d.config)
	isReadOnly := shared.IsTrue(d.config["readonly"])
	isRecursive := shared.IsTrue(d.config["recursive"])

	mntOptions := d.config["raw.mount.options"]
	fsName := "none"

	isFile := false
	if d.config["pool"] == "" {
		isFile = !shared.IsDir(srcPath) && !IsBlockdev(srcPath)
		if strings.HasPrefix(d.config["source"], "cephfs:") {
			// Get fs name and path from d.config.
			fields := strings.SplitN(d.config["source"], ":", 2)
			fields = strings.SplitN(fields[1], "/", 2)
			mdsName := fields[0]
			mdsPath := fields[1]

			// Apply the ceph configuration.
			userName := d.config["ceph.user_name"]
			if userName == "" {
				userName = "admin"
			}

			clusterName := d.config["ceph.cluster_name"]
			if clusterName == "" {
				clusterName = "ceph"
			}

			// Get the mount options.
			mntSrcPath, fsOptions, fsErr := diskCephfsOptions(clusterName, userName, mdsName, mdsPath)
			if fsErr != nil {
				return "", fsErr
			}

			// Join the options with any provided by the user.
			if mntOptions == "" {
				mntOptions = fsOptions
			} else {
				mntOptions += "," + fsOptions
			}

			fsName = "ceph"
			srcPath = mntSrcPath
			isFile = false
		} else if strings.HasPrefix(d.config["source"], "ceph:") {
			// Get the pool and volume names.
			fields := strings.SplitN(d.config["source"], ":", 2)
			fields = strings.SplitN(fields[1], "/", 2)
			poolName := fields[0]
			volumeName := fields[1]

			// Apply the ceph configuration.
			userName := d.config["ceph.user_name"]
			if userName == "" {
				userName = "admin"
			}

			clusterName := d.config["ceph.cluster_name"]
			if clusterName == "" {
				clusterName = "ceph"
			}

			// Map the RBD.
			rbdPath, err := diskCephRbdMap(clusterName, userName, poolName, volumeName)
			if err != nil {
				msg := fmt.Sprintf("Could not mount map Ceph RBD: %s.", err)
				if !isRequired {
					// Will fail the PathExists test below.
					logger.Warn(msg)
				} else {
					return "", fmt.Errorf(msg)
				}
			}

			// Record the device path.
			err = d.volatileSet(map[string]string{"ceph_rbd": rbdPath})
			if err != nil {
				return "", err
			}

			srcPath = rbdPath
			isFile = false
		}
	} else {
		// Deal with mounting storage volumes created via the storage api. Extract the name
		// of the storage volume that we are supposed to attach. We assume that the only
		// syntactically valid ways of specifying a storage volume are:
		// - <volume_name>
		// - <type>/<volume_name>
		// Currently, <type> must either be empty or "custom".
		// We do not yet support container mounts.

		if filepath.IsAbs(d.config["source"]) {
			return "", fmt.Errorf("When the \"pool\" property is set \"source\" must specify the name of a volume, not a path")
		}

		volumeTypeName := ""
		volumeName := filepath.Clean(d.config["source"])
		slash := strings.Index(volumeName, "/")
		if (slash > 0) && (len(volumeName) > slash) {
			// Extract volume name.
			volumeName = d.config["source"][(slash + 1):]
			// Extract volume type.
			volumeTypeName = d.config["source"][:slash]
		}

		switch volumeTypeName {
		case db.StoragePoolVolumeTypeNameContainer:
			return "", fmt.Errorf("Using container storage volumes is not supported")
		case "":
			// We simply received the name of a storage volume.
			volumeTypeName = db.StoragePoolVolumeTypeNameCustom
			fallthrough
		case db.StoragePoolVolumeTypeNameCustom:
			srcPath = shared.VarPath("storage-pools", d.config["pool"], volumeTypeName, volumeName)
		case db.StoragePoolVolumeTypeNameImage:
			return "", fmt.Errorf("Using image storage volumes is not supported")
		default:
			return "", fmt.Errorf("Unknown storage type prefix \"%s\" found", volumeTypeName)
		}

		err := StorageVolumeMount(d.state, d.config["pool"], volumeName, volumeTypeName, d.inst)
		if err != nil {
			msg := fmt.Sprintf("Could not mount storage volume \"%s\" of type \"%s\" on storage pool \"%s\": %s.", volumeName, volumeTypeName, d.config["pool"], err)
			if !isRequired {
				// Will fail the PathExists test below.
				logger.Warn(msg)
			} else {
				return "", fmt.Errorf(msg)
			}
		}
	}

	// Check if the source exists unless it is a cephfs.
	if fsName != "ceph" && !shared.PathExists(srcPath) {
		if !isRequired {
			return "", nil
		}
		return "", fmt.Errorf("Source path %s doesn't exist for device %s", srcPath, d.name)
	}

	// Create the devices directory if missing.
	if !shared.PathExists(d.inst.DevicesPath()) {
		err := os.Mkdir(d.inst.DevicesPath(), 0711)
		if err != nil {
			return "", err
		}
	}

	// Clean any existing entry.
	if shared.PathExists(devPath) {
		err := os.Remove(devPath)
		if err != nil {
			return "", err
		}
	}

	// Create the mount point.
	if isFile {
		f, err := os.Create(devPath)
		if err != nil {
			return "", err
		}

		f.Close()
	} else {
		err := os.Mkdir(devPath, 0700)
		if err != nil {
			return "", err
		}
	}

	// Mount the fs.
	err := DiskMount(srcPath, devPath, isReadOnly, isRecursive, d.config["propagation"], mntOptions, fsName)
	if err != nil {
		return "", err
	}

	return devPath, nil
}

// Stop is run when the device is removed from the instance.
func (d *disk) Stop() (*deviceConfig.RunConfig, error) {
	if d.inst.Type() == instancetype.VM {
		// Only root disks and cloud-init:config drives supported on VMs.
		if shared.IsRootDiskDevice(d.config) || d.config["source"] == diskSourceCloudInit {
			return &deviceConfig.RunConfig{}, nil
		}

		return nil, fmt.Errorf("Non-root disks not supported for VMs")
	}

	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	// Figure out the paths
	relativeDestPath := strings.TrimPrefix(d.config["path"], "/")
	devPath := d.getDevicePath(d.name, d.config)

	// The disk device doesn't exist do nothing.
	if !shared.PathExists(devPath) {
		return nil, nil
	}

	// Request an unmount of the device inside the instance.
	runConf.Mounts = append(runConf.Mounts, deviceConfig.MountEntryItem{
		TargetPath: relativeDestPath,
	})

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *disk) postStop() error {
	// Check if pool-specific action should be taken.
	if d.config["pool"] != "" {
		err := StorageVolumeUmount(d.state, d.config["pool"], d.config["source"], db.StoragePoolVolumeTypeCustom)
		if err != nil {
			return err
		}

	}

	devPath := d.getDevicePath(d.name, d.config)

	// Clean any existing entry.
	if shared.PathExists(devPath) {
		// Unmount the host side if not already.
		// Don't check for errors here as this is just to catch any existing mounts that
		// we not unmounted on the host after device was started.
		unix.Unmount(devPath, unix.MNT_DETACH)

		// Remove the host side.
		err := os.Remove(devPath)
		if err != nil {
			return err
		}
	}

	if strings.HasPrefix(d.config["source"], "ceph:") {
		v := d.volatileGet()
		err := diskCephRbdUnmap(v["ceph_rbd"])
		if err != nil {
			return err
		}
	}

	return nil
}

// getDiskLimits calculates Block I/O limits.
func (d *disk) getDiskLimits() (map[string]diskBlockLimit, error) {
	result := map[string]diskBlockLimit{}

	// Build a list of all valid block devices
	validBlocks := []string{}

	dents, err := ioutil.ReadDir("/sys/class/block/")
	if err != nil {
		return nil, err
	}

	for _, f := range dents {
		fPath := filepath.Join("/sys/class/block/", f.Name())
		if shared.PathExists(fmt.Sprintf("%s/partition", fPath)) {
			continue
		}

		if !shared.PathExists(fmt.Sprintf("%s/dev", fPath)) {
			continue
		}

		block, err := ioutil.ReadFile(fmt.Sprintf("%s/dev", fPath))
		if err != nil {
			return nil, err
		}

		validBlocks = append(validBlocks, strings.TrimSuffix(string(block), "\n"))
	}

	// Process all the limits
	blockLimits := map[string][]diskBlockLimit{}
	for devName, dev := range d.inst.ExpandedDevices() {
		if dev["type"] != "disk" {
			continue
		}

		// Apply max limit
		if dev["limits.max"] != "" {
			dev["limits.read"] = dev["limits.max"]
			dev["limits.write"] = dev["limits.max"]
		}

		// Parse the user input
		readBps, readIops, writeBps, writeIops, err := d.parseDiskLimit(dev["limits.read"], dev["limits.write"])
		if err != nil {
			return nil, err
		}

		// Set the source path
		source := d.getDevicePath(devName, dev)
		if dev["source"] == "" {
			source = d.inst.RootfsPath()
		}

		if !shared.PathExists(source) {
			// Require that device is mounted before resolving block device if required.
			if d.isRequired(dev) {
				return nil, fmt.Errorf("Block device path doesn't exist: %s", source)
			}

			continue // Do not resolve block device if device isn't mounted.
		}

		// Get the backing block devices (major:minor)
		blocks, err := d.getParentBlocks(source)
		if err != nil {
			if readBps == 0 && readIops == 0 && writeBps == 0 && writeIops == 0 {
				// If the device doesn't exist, there is no limit to clear so ignore the failure
				continue
			} else {
				return nil, err
			}
		}

		device := diskBlockLimit{readBps: readBps, readIops: readIops, writeBps: writeBps, writeIops: writeIops}
		for _, block := range blocks {
			blockStr := ""

			if shared.StringInSlice(block, validBlocks) {
				// Straightforward entry (full block device)
				blockStr = block
			} else {
				// Attempt to deal with a partition (guess its parent)
				fields := strings.SplitN(block, ":", 2)
				fields[1] = "0"
				if shared.StringInSlice(fmt.Sprintf("%s:%s", fields[0], fields[1]), validBlocks) {
					blockStr = fmt.Sprintf("%s:%s", fields[0], fields[1])
				}
			}

			if blockStr == "" {
				return nil, fmt.Errorf("Block device doesn't support quotas: %s", block)
			}

			if blockLimits[blockStr] == nil {
				blockLimits[blockStr] = []diskBlockLimit{}
			}
			blockLimits[blockStr] = append(blockLimits[blockStr], device)
		}
	}

	// Average duplicate limits
	for block, limits := range blockLimits {
		var readBpsCount, readBpsTotal, readIopsCount, readIopsTotal, writeBpsCount, writeBpsTotal, writeIopsCount, writeIopsTotal int64

		for _, limit := range limits {
			if limit.readBps > 0 {
				readBpsCount++
				readBpsTotal += limit.readBps
			}

			if limit.readIops > 0 {
				readIopsCount++
				readIopsTotal += limit.readIops
			}

			if limit.writeBps > 0 {
				writeBpsCount++
				writeBpsTotal += limit.writeBps
			}

			if limit.writeIops > 0 {
				writeIopsCount++
				writeIopsTotal += limit.writeIops
			}
		}

		device := diskBlockLimit{}

		if readBpsCount > 0 {
			device.readBps = readBpsTotal / readBpsCount
		}

		if readIopsCount > 0 {
			device.readIops = readIopsTotal / readIopsCount
		}

		if writeBpsCount > 0 {
			device.writeBps = writeBpsTotal / writeBpsCount
		}

		if writeIopsCount > 0 {
			device.writeIops = writeIopsTotal / writeIopsCount
		}

		result[block] = device
	}

	return result, nil
}

func (d *disk) parseDiskLimit(readSpeed string, writeSpeed string) (int64, int64, int64, int64, error) {
	parseValue := func(value string) (int64, int64, error) {
		var err error

		bps := int64(0)
		iops := int64(0)

		if value == "" {
			return bps, iops, nil
		}

		if strings.HasSuffix(value, "iops") {
			iops, err = strconv.ParseInt(strings.TrimSuffix(value, "iops"), 10, 64)
			if err != nil {
				return -1, -1, err
			}
		} else {
			bps, err = units.ParseByteSizeString(value)
			if err != nil {
				return -1, -1, err
			}
		}

		return bps, iops, nil
	}

	readBps, readIops, err := parseValue(readSpeed)
	if err != nil {
		return -1, -1, -1, -1, err
	}

	writeBps, writeIops, err := parseValue(writeSpeed)
	if err != nil {
		return -1, -1, -1, -1, err
	}

	return readBps, readIops, writeBps, writeIops, nil
}

func (d *disk) getParentBlocks(path string) ([]string, error) {
	var devices []string
	var dev []string

	// Expand the mount path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	expPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		expPath = absPath
	}

	// Find the source mount of the path
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	match := ""
	for scanner.Scan() {
		line := scanner.Text()
		rows := strings.Fields(line)

		if len(rows[4]) <= len(match) {
			continue
		}

		if expPath != rows[4] && !strings.HasPrefix(expPath, rows[4]) {
			continue
		}

		match = rows[4]

		// Go backward to avoid problems with optional fields
		dev = []string{rows[2], rows[len(rows)-2]}
	}

	if dev == nil {
		return nil, fmt.Errorf("Couldn't find a match /proc/self/mountinfo entry")
	}

	// Handle the most simple case
	if !strings.HasPrefix(dev[0], "0:") {
		return []string{dev[0]}, nil
	}

	// Deal with per-filesystem oddities. We don't care about failures here
	// because any non-special filesystem => directory backend.
	fs, _ := util.FilesystemDetect(expPath)

	if fs == "zfs" && shared.PathExists("/dev/zfs") {
		// Accessible zfs filesystems
		poolName := strings.Split(dev[1], "/")[0]

		output, err := shared.RunCommand("zpool", "status", "-P", "-L", poolName)
		if err != nil {
			return nil, fmt.Errorf("Failed to query zfs filesystem information for %s: %v", dev[1], err)
		}

		header := true
		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}

			if fields[1] != "ONLINE" {
				continue
			}

			if header {
				header = false
				continue
			}

			var path string
			if shared.PathExists(fields[0]) {
				if shared.IsBlockdevPath(fields[0]) {
					path = fields[0]
				} else {
					subDevices, err := d.getParentBlocks(fields[0])
					if err != nil {
						return nil, err
					}

					for _, dev := range subDevices {
						devices = append(devices, dev)
					}
				}
			} else {
				continue
			}

			if path != "" {
				_, major, minor, err := unixDeviceAttributes(path)
				if err != nil {
					continue
				}

				devices = append(devices, fmt.Sprintf("%d:%d", major, minor))
			}
		}

		if len(devices) == 0 {
			return nil, fmt.Errorf("Unable to find backing block for zfs pool: %s", poolName)
		}
	} else if fs == "btrfs" && shared.PathExists(dev[1]) {
		// Accessible btrfs filesystems
		output, err := shared.RunCommand("btrfs", "filesystem", "show", dev[1])
		if err != nil {
			return nil, fmt.Errorf("Failed to query btrfs filesystem information for %s: %v", dev[1], err)
		}

		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || fields[0] != "devid" {
				continue
			}

			_, major, minor, err := unixDeviceAttributes(fields[len(fields)-1])
			if err != nil {
				return nil, err
			}

			devices = append(devices, fmt.Sprintf("%d:%d", major, minor))
		}
	} else if shared.PathExists(dev[1]) {
		// Anything else with a valid path
		_, major, minor, err := unixDeviceAttributes(dev[1])
		if err != nil {
			return nil, err
		}

		devices = append(devices, fmt.Sprintf("%d:%d", major, minor))
	} else {
		return nil, fmt.Errorf("Invalid block device: %s", dev[1])
	}

	return devices, nil
}

// generateVMConfigDrive generates an ISO containing the cloud init config for a VM.
// Returns the path to the ISO.
func (d *disk) generateVMConfigDrive() (string, error) {
	scratchDir := filepath.Join(d.inst.DevicesPath(), deviceNameEncode(d.name))

	// Create config drive dir.
	err := os.MkdirAll(scratchDir, 0100)
	if err != nil {
		return "", err
	}

	instanceConfig := d.inst.ExpandedConfig()

	// Use an empty user-data file if no custom vendor-data supplied.
	vendorData := instanceConfig["user.vendor-data"]
	if vendorData == "" {
		vendorData = "#cloud-config"
	}

	err = ioutil.WriteFile(filepath.Join(scratchDir, "vendor-data"), []byte(vendorData), 0400)
	if err != nil {
		return "", err
	}

	// Use an empty user-data file if no custom user-data supplied.
	userData := instanceConfig["user.user-data"]
	if userData == "" {
		userData = "#cloud-config"
	}

	err = ioutil.WriteFile(filepath.Join(scratchDir, "user-data"), []byte(userData), 0400)
	if err != nil {
		return "", err
	}

	// Append any custom meta-data to our predefined meta-data config.
	metaData := fmt.Sprintf(`instance-id: %s
local-hostname: %s
%s
`, d.inst.Name(), d.inst.Name(), instanceConfig["user.meta-data"])

	err = ioutil.WriteFile(filepath.Join(scratchDir, "meta-data"), []byte(metaData), 0400)
	if err != nil {
		return "", err
	}

	// Finally convert the config drive dir into an ISO file. The cidata label is important
	// as this is what cloud-init uses to detect, mount the drive and run the cloud-init
	// templates on first boot. The vendor-data template then modifies the system so that the
	// config drive is mounted and the agent is started on subsequent boots.
	isoPath := filepath.Join(d.inst.Path(), "config.iso")
	_, err = shared.RunCommand("mkisofs", "-R", "-V", "cidata", "-o", isoPath, scratchDir)
	if err != nil {
		return "", err
	}

	// Remove the config drive folder.
	os.RemoveAll(scratchDir)

	return isoPath, nil
}
