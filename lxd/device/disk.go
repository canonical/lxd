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

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/units"
)

type diskBlockLimit struct {
	readBps   int64
	readIops  int64
	writeBps  int64
	writeIops int64
}

type disk struct {
	deviceCommon
}

// isRequired indicates whether the device config requires this device to start OK.
func (d *disk) isRequired() bool {
	// Defaults to required.
	if (d.config["required"] == "" || shared.IsTrue(d.config["required"])) && !shared.IsTrue(d.config["optional"]) {
		return true
	}

	return false
}

// validateConfig checks the supplied config for correctness.
func (d *disk) validateConfig() error {
	if d.instance.Type() != instance.TypeContainer {
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
		"path":         shared.IsNotEmpty,
		"required":     shared.IsBool,
		"optional":     shared.IsBool, // "optional" is deprecated, replaced by "required".
		"readonly":     shared.IsBool,
		"recursive":    shared.IsBool,
		"shift":        shared.IsBool,
		"source":       shared.IsAny,
		"limits.read":  shared.IsAny,
		"limits.write": shared.IsAny,
		"limits.max":   shared.IsAny,
		"size":         shared.IsAny,
		"pool":         shared.IsAny,
		"propagation":  validatePropagation,
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

	// Check no other devices also have the same path as us. Use LocalDevices for this check so
	// that we can check before the config is expanded or when a profile is being checked.
	// Don't take into account the device names, only count active devices that point to the
	// same path, so that if merged profiles share the same the path and then one is removed
	// this can still be cleanly removed.
	pathCount := 0
	for _, devConfig := range d.instance.LocalDevices() {
		if devConfig["type"] == "disk" && devConfig["path"] == d.config["path"] {
			pathCount++
			if pathCount > 1 {
				return fmt.Errorf("More than one disk device uses the same path: %s", d.config["path"])

			}
		}
	}

	// When we want to attach a storage volume created via the storage api the "source" only
	// contains the name of the storage volume, not the path where it is mounted. So only check
	// for the existence of "source" when "pool" is empty.
	if d.config["pool"] == "" && d.config["source"] != "" && d.isRequired() && !shared.PathExists(shared.HostPath(d.config["source"])) {
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
		// and not a profile device (check for non-empty instance name), and we have least
		// one expanded device (this is so we only do this expensive check after devices
		// have been expanded).
		if d.instance.Name() != "" && len(d.instance.ExpandedDevices()) > 0 && d.config["source"] != "" && d.config["path"] != "/" {
			isAvailable, err := d.state.Cluster.StorageVolumeIsAvailable(d.config["pool"], d.config["source"])
			if err != nil {
				return fmt.Errorf("Check if volume is available: %v", err)
			}
			if !isAvailable {
				return fmt.Errorf("Storage volume %q is already attached to a container on a different node", d.config["source"])
			}
		}
	}

	return nil
}

// getDevicePath returns the absolute path on the host for this device.
func (d *disk) getDevicePath() string {
	relativeDestPath := strings.TrimPrefix(d.config["path"], "/")
	devName := fmt.Sprintf("disk.%s.%s", strings.Replace(d.name, "/", "-", -1), strings.Replace(relativeDestPath, "/", "-", -1))
	return filepath.Join(d.instance.DevicesPath(), devName)
}

// validateEnvironment checks the runtime environment for correctness.
func (d *disk) validateEnvironment() error {
	if shared.IsTrue(d.config["shift"]) && !d.state.OS.Shiftfs {
		return fmt.Errorf("shiftfs is required by disk entry but isn't supported on system")
	}

	return nil
}

// CanHotPlug returns whether the device can be managed whilst the instance is running, it also
// returns a list of fields that can be updated without triggering a device remove & add.
func (d *disk) CanHotPlug() (bool, []string) {
	return true, []string{"limits.max", "limits.read", "limits.write", "size"}
}

// Start is run when the device is added to the container.
func (d *disk) Start() (*RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	runConf := RunConfig{}

	err = d.generateLimits(&runConf)
	if err != nil {
		return nil, err
	}

	isReadOnly := shared.IsTrue(d.config["readonly"])

	// Deal with a rootfs.
	if shared.IsRootDiskDevice(d.config) {
		// Set the rootfs path.
		rootfs := RootFSEntryItem{
			Path: d.instance.RootfsPath(),
		}

		// Read-only rootfs (unlikely to work very well).
		if isReadOnly {
			rootfs.Opts = append(rootfs.Opts, "ro")
		}

		v := d.volatileGet()

		// Handle previous requests for setting new quotas.
		if v["volatile.apply_quota"] != "" {
			applied, err := d.applyQuota(v["volatile.apply_quota"])
			if err != nil || !applied {
				return nil, err
			}

			// Remove volatile apply_quota key if successful.
			err = d.volatileSet(map[string]string{"volatile.apply_quota": ""})
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

		ownerShift := MountOwnerShiftNone
		if shared.IsTrue(d.config["shift"]) {
			ownerShift = MountOwnerShiftDynamic
		}

		// If ownerShift is none and pool is specified then check whether the pool itself
		// has owner shifting enabled, and if so enable shifting on this device too.
		if ownerShift == MountOwnerShiftNone && d.config["pool"] != "" {
			poolID, _, err := d.state.Cluster.StoragePoolGet(d.config["pool"])
			if err != nil {
				return nil, err
			}

			_, volume, err := d.state.Cluster.StoragePoolNodeVolumeGetTypeByProject(d.instance.Project(), d.config["source"], db.StoragePoolVolumeTypeCustom, poolID)
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
			runConf.Mounts = append(runConf.Mounts, MountEntryItem{
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

// postStart is run after the instance is started.
func (d *disk) postStart() error {
	devPath := d.getDevicePath()

	// Unmount the host side.
	err := unix.Unmount(devPath, unix.MNT_DETACH)
	if err != nil {
		return err
	}

	return nil
}

// Update applies configuration changes to a started device.
func (d *disk) Update(oldDevices config.Devices, isRunning bool) error {
	if shared.IsRootDiskDevice(d.config) {
		// Make sure we have a valid root disk device (and only one).
		expandedDevices := d.instance.ExpandedDevices()
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
			applied, err := d.applyQuota(newRootDiskDeviceSize)
			if err != nil {
				return err
			}

			if !applied {
				// Save volatile apply_quota key for next boot if cannot apply now.
				err = d.volatileSet(map[string]string{"volatile.apply_quota": newRootDiskDeviceSize})
				if err != nil {
					return err
				}
			}
		}
	}

	runConf := RunConfig{}

	err := d.generateLimits(&runConf)
	if err != nil {
		return err
	}

	err = d.instance.DeviceEventHandler(&runConf)
	if err != nil {
		return err
	}

	return nil
}

func (d *disk) applyQuota(newSize string) (bool, error) {
	newSizeBytes, err := units.ParseByteSizeString(newSize)
	if err != nil {
		return false, err
	}

	applied, err := StorageRootFSApplyQuota(d.instance, newSizeBytes)
	if err != nil {
		return applied, err
	}

	return applied, nil
}

// generateLimits adds a set of cgroup rules to apply specified limits to the supplied RunConfig.
func (d *disk) generateLimits(runConf *RunConfig) error {
	// Disk priority limits.
	diskPriority := d.instance.ExpandedConfig()["limits.disk.priority"]
	if diskPriority != "" {
		if d.state.OS.CGroupBlkioWeightController {
			if diskPriority != "" {
				priorityInt, err := strconv.Atoi(diskPriority)
				if err != nil {
					return err
				}

				priority := priorityInt * 100

				// Minimum valid value is 10
				if priority == 0 {
					priority = 10
				}

				runConf.CGroups = append(runConf.CGroups, RunConfigItem{
					Key:   "blkio.weight",
					Value: fmt.Sprintf("%d", priority),
				})
			}
		} else {
			return fmt.Errorf("Cannot apply limits.disk.priority as blkio.weight cgroup controller is missing")
		}
	}

	// Disk throttle limits.
	hasDiskLimits := false
	for _, dev := range d.instance.ExpandedDevices() {
		if dev["type"] != "disk" {
			continue
		}

		if dev["limits.read"] != "" || dev["limits.write"] != "" || dev["limits.max"] != "" {
			hasDiskLimits = true
		}
	}

	if hasDiskLimits {
		if !d.state.OS.CGroupBlkioController {
			return fmt.Errorf("Cannot apply disk limits as blkio cgroup controller is missing")
		}

		diskLimits, err := d.getDiskLimits()
		if err != nil {
			return err
		}

		for block, limit := range diskLimits {
			if limit.readBps > 0 {
				runConf.CGroups = append(runConf.CGroups, RunConfigItem{
					Key:   "blkio.throttle.read_bps_device",
					Value: fmt.Sprintf("%s %d", block, limit.readBps),
				})
			}

			if limit.readIops > 0 {
				runConf.CGroups = append(runConf.CGroups, RunConfigItem{
					Key:   "blkio.throttle.read_iops_device",
					Value: fmt.Sprintf("%s %d", block, limit.readIops),
				})
			}

			if limit.writeBps > 0 {
				runConf.CGroups = append(runConf.CGroups, RunConfigItem{
					Key:   "blkio.throttle.write_bps_device",
					Value: fmt.Sprintf("%s %d", block, limit.writeBps),
				})
			}

			if limit.writeIops > 0 {
				runConf.CGroups = append(runConf.CGroups, RunConfigItem{
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
	devPath := d.getDevicePath()
	srcPath := shared.HostPath(d.config["source"])

	// Check if read-only.
	isRequired := d.isRequired()
	isReadOnly := shared.IsTrue(d.config["readonly"])
	isRecursive := shared.IsTrue(d.config["recursive"])

	isFile := false
	if d.config["pool"] == "" {
		isFile = !shared.IsDir(srcPath) && !IsBlockdev(srcPath)
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

		err := StorageVolumeMount(d.state, d.config["pool"], volumeName, volumeTypeName, d.instance)
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

	// Check if the source exists.
	if !shared.PathExists(srcPath) {
		if !isRequired {
			return "", nil
		}
		return "", fmt.Errorf("Source path %s doesn't exist for device %s", srcPath, d.name)
	}

	// Create the devices directory if missing.
	if !shared.PathExists(d.instance.DevicesPath()) {
		err := os.Mkdir(d.instance.DevicesPath(), 0711)
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
	err := DiskMount(srcPath, devPath, isReadOnly, isRecursive, d.config["propagation"])
	if err != nil {
		return "", err
	}

	return devPath, nil
}

// Stop is run when the device is removed from the instance.
func (d *disk) Stop() (*RunConfig, error) {
	runConf := RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	// Figure out the paths
	relativeDestPath := strings.TrimPrefix(d.config["path"], "/")
	devPath := d.getDevicePath()

	// The disk device doesn't exist do nothing.
	if !shared.PathExists(devPath) {
		return nil, nil
	}

	// Request an unmount of the device inside the instance.
	runConf.Mounts = append(runConf.Mounts, MountEntryItem{
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

	devPath := d.getDevicePath()

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
	for _, dev := range d.instance.ExpandedDevices() {
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
		source := shared.HostPath(dev["source"])
		if source == "" {
			source = d.instance.RootfsPath()
		}

		// Don't try to resolve the block device behind a non-existing path
		if !shared.PathExists(source) {
			continue
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
			return nil, fmt.Errorf("Failed to query zfs filesystem information for %s: %s", dev[1], output)
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
			return nil, fmt.Errorf("Failed to query btrfs filesystem information for %s: %s", dev[1], output)
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
