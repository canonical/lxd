package device

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/cgroup"
	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/subprocess"
	"github.com/lxc/lxd/shared/units"
	"github.com/lxc/lxd/shared/validate"
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
func (d *disk) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
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
		"required":          validate.Optional(validate.IsBool),
		"optional":          validate.Optional(validate.IsBool), // "optional" is deprecated, replaced by "required".
		"readonly":          validate.Optional(validate.IsBool),
		"recursive":         validate.Optional(validate.IsBool),
		"shift":             validate.Optional(validate.IsBool),
		"source":            validate.IsAny,
		"limits.read":       validate.IsAny,
		"limits.write":      validate.IsAny,
		"limits.max":        validate.IsAny,
		"size":              validate.IsAny,
		"pool":              validate.IsAny,
		"propagation":       validatePropagation,
		"raw.mount.options": validate.IsAny,
		"ceph.cluster_name": validate.IsAny,
		"ceph.user_name":    validate.IsAny,
		"boot.priority":     validate.Optional(validate.IsUint32),
		"path":              validate.IsAny,
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	if d.config["required"] != "" && d.config["optional"] != "" {
		return fmt.Errorf(`Cannot use both "required" and deprecated "optional" properties at the same time`)
	}

	if d.config["source"] == "" && d.config["path"] != "/" {
		return fmt.Errorf(`Disk entry is missing the required "source" property`)
	}

	if d.config["path"] == "/" && d.config["source"] != "" {
		return fmt.Errorf(`Root disk entry may not have a "source" property set`)
	}

	if d.config["path"] == "/" && d.config["pool"] == "" {
		return fmt.Errorf(`Root disk entry must have a "pool" property set`)
	}

	if d.config["size"] != "" && d.config["path"] != "/" {
		return fmt.Errorf("Only the root disk may have a size quota")
	}

	if d.config["recursive"] != "" && (d.config["path"] == "/" || !shared.IsDir(shared.HostPath(d.config["source"]))) {
		return fmt.Errorf("The recursive option is only supported for additional bind-mounted paths")
	}

	if shared.IsTrue(d.config["recursive"]) && shared.IsTrue(d.config["readonly"]) {
		return fmt.Errorf("Recursive read-only bind-mounts aren't currently supported by the kernel")
	}

	// Check ceph options are only used when ceph or cephfs type source is specified.
	if !(strings.HasPrefix(d.config["source"], "ceph:") || strings.HasPrefix(d.config["source"], "cephfs:")) && (d.config["ceph.cluster_name"] != "" || d.config["ceph.user_name"] != "") {
		return fmt.Errorf("Invalid options ceph.cluster_name/ceph.user_name for source %q", d.config["source"])
	}

	// Check no other devices also have the same path as us. Use LocalDevices for this check so
	// that we can check before the config is expanded or when a profile is being checked.
	// Don't take into account the device names, only count active devices that point to the
	// same path, so that if merged profiles share the same the path and then one is removed
	// this can still be cleanly removed.
	pathCount := 0
	for _, devConfig := range instConf.LocalDevices() {
		if devConfig["type"] == "disk" && d.config["path"] != "" && devConfig["path"] == d.config["path"] {
			pathCount++
			if pathCount > 1 {
				return fmt.Errorf("More than one disk device uses the same path %q", d.config["path"])

			}
		}
	}

	// Check that external disk source path exists. External disk sources have a non-empty "source" property
	// that contains the path of the external source, and do not have a "pool" property. We only check the
	// source path exists when the disk device is required, is not an external ceph/cephfs source and is not a
	// VM cloud-init drive. We only check this when an instance is loaded to avoid validating snapshot configs
	// that may contain older config that no longer exists which can prevent migrations.
	if d.inst != nil && d.config["pool"] == "" && d.config["source"] != "" && d.config["source"] != diskSourceCloudInit && d.isRequired(d.config) && !shared.PathExists(shared.HostPath(d.config["source"])) && !strings.HasPrefix(d.config["source"], "ceph:") && !strings.HasPrefix(d.config["source"], "cephfs:") {
		return fmt.Errorf("Missing source %q for disk %q", d.config["source"], d.name)
	}

	if d.config["pool"] != "" {
		if d.config["shift"] != "" {
			return fmt.Errorf(`The "shift" property cannot be used with custom storage volumes`)
		}

		if filepath.IsAbs(d.config["source"]) {
			return fmt.Errorf("Storage volumes cannot be specified as absolute paths")
		}

		// Only perform expensive instance custom volume checks when not validating a profile and after
		// device expansion has occurred (to avoid doing it twice during instance load).
		if instConf.Type() != instancetype.Any && len(instConf.ExpandedDevices()) > 0 && d.config["source"] != "" && d.config["path"] != "/" {
			poolID, err := d.state.Cluster.GetStoragePoolID(d.config["pool"])
			if err != nil {
				return fmt.Errorf("The %q storage pool doesn't exist", d.config["pool"])
			}

			// Derive the effective storage project name from the instance config's project.
			storageProjectName, err := project.StorageVolumeProject(d.state.Cluster, instConf.Project(), db.StoragePoolVolumeTypeCustom)
			if err != nil {
				return err
			}

			// GetLocalStoragePoolVolume returns a volume with an empty Location field for remote drivers.
			_, vol, err := d.state.Cluster.GetLocalStoragePoolVolume(storageProjectName, d.config["source"], db.StoragePoolVolumeTypeCustom, poolID)
			if err != nil {
				return errors.Wrapf(err, "Failed loading custom volume")
			}

			// Check storage volume is available to mount on this cluster member.
			remoteInstance, err := storagePools.VolumeUsedByExclusiveRemoteInstancesWithProfiles(d.state, d.config["pool"], storageProjectName, vol)
			if err != nil {
				return errors.Wrapf(err, "Failed checking if custom volume is exclusively attached to another instance")
			}

			if remoteInstance != nil {
				return fmt.Errorf("Custom volume is already attached to an instance on a different node")
			}

			// Check only block type volumes are attached to VM instances.
			contentType, err := storagePools.VolumeContentTypeNameToContentType(vol.ContentType)
			if err != nil {
				return err
			}

			if contentType == db.StoragePoolVolumeContentTypeBlock {
				if instConf.Type() != instancetype.VM {
					return fmt.Errorf("Custom block volumes cannot be used on containers")
				}

				if d.config["path"] != "" {
					return fmt.Errorf("Custom block volumes cannot have a path defined")
				}
			}
		}
	}

	return nil
}

// getDevicePath returns the absolute path on the host for this instance and supplied device config.
func (d *disk) getDevicePath(devName string, devConfig deviceConfig.Device) string {
	relativeDestPath := strings.TrimPrefix(devConfig["path"], "/")
	devPath := storageDrivers.PathNameEncode(deviceJoinPath("disk", devName, relativeDestPath))
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

// UpdatableFields returns a list of fields that can be updated without triggering a device remove & add.
func (d *disk) UpdatableFields() []string {
	return []string{"limits.max", "limits.read", "limits.write", "size"}
}

// Register calls mount for the disk volume (which should already be mounted) to reinitialise the reference counter
// for volumes attached to running instances on LXD restart.
func (d *disk) Register() error {
	d.logger.Debug("Initialising mounted disk ref counter")

	if d.config["path"] == "/" {
		pool, err := storagePools.GetPoolByInstance(d.state, d.inst)
		if err != nil {
			return err
		}

		// Try to mount the volume that should already be mounted to reinitialise the ref counter.
		_, err = pool.MountInstance(d.inst, nil)
		if err != nil {
			return err
		}
	} else if d.config["path"] != "/" && d.config["source"] != "" && d.config["pool"] != "" {
		pool, err := storagePools.GetPoolByName(d.state, d.config["pool"])
		if err != nil {
			return err
		}

		storageProjectName, err := project.StorageVolumeProject(d.state.Cluster, d.inst.Project(), db.StoragePoolVolumeTypeCustom)
		if err != nil {
			return err
		}

		// Try to mount the volume that should already be mounted to reinitialise the ref counter.
		err = pool.MountCustomVolume(storageProjectName, d.config["source"], nil)
		if err != nil {
			return err
		}
	}

	return nil
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
			poolID, _, err := d.state.Cluster.GetStoragePool(d.config["pool"])
			if err != nil {
				return nil, err
			}

			// Only custom volumes can be attached currently.
			storageProjectName, err := project.StorageVolumeProject(d.state.Cluster, d.inst.Project(), db.StoragePoolVolumeTypeCustom)
			if err != nil {
				return nil, err
			}

			_, volume, err := d.state.Cluster.GetLocalStoragePoolVolume(storageProjectName, d.config["source"], db.StoragePoolVolumeTypeCustom, poolID)
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
				DevName:    d.name,
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
	isRequired := d.isRequired(d.config)

	if shared.IsRootDiskDevice(d.config) {
		runConf.Mounts = []deviceConfig.MountEntryItem{
			{
				TargetPath: d.config["path"], // Indicator used that this is the root device.
				DevName:    d.name,
			},
		}

		return &runConf, nil
	} else if d.config["source"] == diskSourceCloudInit {
		// This is a special virtual disk source that can be attached to a VM to provide cloud-init config.
		isoPath, err := d.generateVMConfigDrive()
		if err != nil {
			return nil, err
		}

		runConf.Mounts = []deviceConfig.MountEntryItem{
			{
				DevPath: isoPath,
				DevName: d.name,
				FSType:  "iso9660",
			},
		}
		return &runConf, nil
	} else if d.config["source"] != "" {
		revert := revert.New()
		defer revert.Fail()

		if strings.HasPrefix(d.config["source"], "ceph:") {
			// Get the pool and volume names.
			fields := strings.SplitN(d.config["source"], ":", 2)
			fields = strings.SplitN(fields[1], "/", 2)
			poolName := fields[0]
			volumeName := fields[1]
			clusterName, userName := d.cephCreds()

			// Configuration values containing :, @, or = can be escaped with a leading \ character.
			// According to https://docs.ceph.com/docs/hammer/rbd/qemu-rbd/#usage
			optEscaper := strings.NewReplacer(":", `\:`, "@", `\@`, "=", `\=`)
			opts := []string{
				fmt.Sprintf("id=%s", optEscaper.Replace(userName)),
				fmt.Sprintf("conf=/etc/ceph/%s.conf", optEscaper.Replace(clusterName)),
			}

			runConf.Mounts = []deviceConfig.MountEntryItem{
				{
					DevPath: fmt.Sprintf("rbd:%s/%s:%s", optEscaper.Replace(poolName), optEscaper.Replace(volumeName), strings.Join(opts, ":")),
					DevName: d.name,
				},
			}
		} else {
			srcPath := shared.HostPath(d.config["source"])
			var err error

			// Mount the pool volume and update srcPath to mount path.
			if d.config["pool"] != "" {
				srcPath, err = d.mountPoolVolume(revert)
				if err != nil {
					if !isRequired {
						// Leave to the pathExists check below.
						logger.Warn(err.Error())
					} else {
						return nil, err
					}
				}
			} else if strings.HasPrefix(d.config["source"], "cephfs:") {
				// Mount the cephfs directory on the host and then treat as a normal directory to
				// share with the VM using 9p below.
				srcPath, err = d.createDevice()
				if err != nil {
					return nil, err
				}
			}

			if !shared.PathExists(srcPath) {
				if isRequired {
					return nil, fmt.Errorf("Source path %q doesn't exist for device %q", srcPath, d.name)
				}

				return &runConf, nil
			}

			// Default to block device or image file passthrough first.
			mount := deviceConfig.MountEntryItem{
				DevPath: srcPath,
				DevName: d.name,
			}

			// If the source being added is a directory, then we will be using 9p directory sharing to mount
			// the directory inside the VM, as such we need to indicate to the VM the target path to mount to.
			if shared.IsDir(srcPath) {
				mount.TargetPath = d.config["path"]
				mount.FSType = "9p"

				if shared.IsTrue(d.config["readonly"]) {
					// Don't use proxy in readonly mode.
					mount.Opts = append(mount.Opts, "ro")
				} else {
					sockPath := filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("%s.sock", d.name))
					mount.DevPath = sockPath // Use socket path as dev path so qemu connects to proxy.

					// Remove old socket if needed.
					os.Remove(sockPath)

					// Locate virtfs-proxy-helper.
					cmd, err := exec.LookPath("virtfs-proxy-helper")
					if err != nil {
						if shared.PathExists("/usr/lib/qemu/virtfs-proxy-helper") {
							cmd = "/usr/lib/qemu/virtfs-proxy-helper"
						}
					}

					if cmd == "" {
						return nil, fmt.Errorf("Required binary 'virtfs-proxy-helper' couldn't be found")
					}

					// Start the virtfs-proxy-helper process in non-daemon mode and as root so that
					// when the VM process is started as an unprivileged user, we can still share
					// directories that process cannot access.
					proc, err := subprocess.NewProcess(cmd, []string{"-n", "-u", "0", "-g", "0", "-s", sockPath, "-p", srcPath}, "", "")
					if err != nil {
						return nil, err
					}

					err = proc.Start()
					if err != nil {
						return nil, errors.Wrapf(err, "Failed to start virtfs-proxy-helper for device %q", d.name)
					}

					revert.Add(func() { proc.Stop() })

					pidPath := filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("%s.pid", d.name))
					err = proc.Save(pidPath)
					if err != nil {
						return nil, errors.Wrapf(err, "Failed to save virtfs-proxy-helper state for device %q", d.name)
					}

					// Wait for socket file to exist (as otherwise qemu can race the creation of this file).
					for i := 0; i < 10; i++ {
						if shared.PathExists(sockPath) {
							break
						}

						time.Sleep(50 * time.Millisecond)
					}
				}

				// Start virtiofsd as LXD prefers virtio-fs over 9p. The latter will only be used
				// as a fallback.

				// Create the socket in this directory instead of the devices directory. QEMU will otherwise
				// fail with "Permission Denied" which is probably caused by qemu being called with --chroot.
				sockPath := filepath.Join(d.inst.Path(), fmt.Sprintf("%s.sock", d.name))
				logPath := filepath.Join(d.inst.LogPath(), fmt.Sprintf("disk.%s.log", d.name))

				// Remove old socket if needed.
				os.Remove(sockPath)

				// Locate virtiofsd.
				cmd, err := exec.LookPath("virtiofsd")
				if err != nil {
					if shared.PathExists("/usr/lib/qemu/virtiofsd") {
						cmd = "/usr/lib/qemu/virtiofsd"
					}
				}

				if cmd != "" {
					// Start the virtiofsd process in non-daemon mode.
					proc, err := subprocess.NewProcess(cmd, []string{fmt.Sprintf("--socket-path=%s", sockPath), "-o", fmt.Sprintf("source=%s", srcPath)}, logPath, logPath)
					if err != nil {
						return nil, err
					}

					err = proc.Start()
					if err != nil {
						return nil, errors.Wrapf(err, "Failed to start virtiofsd for device %q", d.name)
					}

					revert.Add(func() { proc.Stop() })

					pidPath := filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("virtio-fs.%s.pid", d.name))

					err = proc.Save(pidPath)
					if err != nil {
						return nil, errors.Wrapf(err, "Failed to save virtiofsd state for device %q", d.name)
					}

					// Wait for socket file to exist
					for i := 0; i < 20; i++ {
						if shared.PathExists(sockPath) {
							break
						}

						time.Sleep(50 * time.Millisecond)
					}

					if !shared.PathExists(sockPath) {
						return nil, fmt.Errorf("virtiofsd failed to bind socket within 2s")
					}
				} else {
					logger.Warnf("Unable to use virtio-fs for device %q, using 9p as a fallback: virtiofsd missing", d.name)
				}
			}

			runConf.Mounts = []deviceConfig.MountEntryItem{mount}
		}

		revert.Success()
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

	// Only apply IO limits if instance is container and is running.
	if isRunning && d.inst.Type() == instancetype.Container {
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
	pool, err := storagePools.GetPoolByInstance(d.state, d.inst)
	if err != nil {
		return err
	}

	err = pool.SetInstanceQuota(d.inst, newSize, nil)
	if err != nil {
		return err
	}

	return nil
}

// generateLimits adds a set of cgroup rules to apply specified limits to the supplied RunConfig.
func (d *disk) generateLimits(runConf *deviceConfig.RunConfig) error {
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

		cg, err := cgroup.New(&cgroupWriter{runConf})
		if err != nil {
			return err
		}

		for block, limit := range diskLimits {
			if limit.readBps > 0 {
				err = cg.SetBlkioLimit(block, "read", "bps", limit.readBps)
				if err != nil {
					return err
				}
			}

			if limit.readIops > 0 {
				err = cg.SetBlkioLimit(block, "read", "iops", limit.readIops)
				if err != nil {
					return err
				}
			}

			if limit.writeBps > 0 {
				err = cg.SetBlkioLimit(block, "write", "bps", limit.writeBps)
				if err != nil {
					return err
				}
			}

			if limit.writeIops > 0 {
				err = cg.SetBlkioLimit(block, "write", "iops", limit.writeIops)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

type cgroupWriter struct {
	runConf *deviceConfig.RunConfig
}

func (w *cgroupWriter) Get(version cgroup.Backend, controller string, key string) (string, error) {
	return "", fmt.Errorf("This cgroup handler does not support reading")
}

func (w *cgroupWriter) Set(version cgroup.Backend, controller string, key string, value string) error {
	w.runConf.CGroups = append(w.runConf.CGroups, deviceConfig.RunConfigItem{
		Key:   key,
		Value: value,
	})

	return nil
}

// mountPoolVolume mounts the pool volume specified in d.config["source"] from pool specified in d.config["pool"]
// and return the mount path. If the instance type is container volume will be shifted if needed.
func (d *disk) mountPoolVolume(reverter *revert.Reverter) (string, error) {
	// Deal with mounting storage volumes created via the storage api. Extract the name of the storage volume
	// that we are supposed to attach. We assume that the only syntactically valid ways of specifying a
	// storage volume are:
	// - <volume_name>
	// - <type>/<volume_name>
	// Currently, <type> must either be empty or "custom".
	// We do not yet support instance mounts.
	if filepath.IsAbs(d.config["source"]) {
		return "", fmt.Errorf(`When the "pool" property is set "source" must specify the name of a volume, not a path`)
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

	var srcPath string

	// Check volume type name is custom.
	switch volumeTypeName {
	case db.StoragePoolVolumeTypeNameContainer:
		return "", fmt.Errorf("Using instance storage volumes is not supported")
	case "":
		// We simply received the name of a storage volume.
		volumeTypeName = db.StoragePoolVolumeTypeNameCustom
		fallthrough
	case db.StoragePoolVolumeTypeNameCustom:
		break
	case db.StoragePoolVolumeTypeNameImage:
		return "", fmt.Errorf("Using image storage volumes is not supported")
	default:
		return "", fmt.Errorf("Unknown storage type prefix %q found", volumeTypeName)
	}

	// Only custom volumes can be attached currently.
	storageProjectName, err := project.StorageVolumeProject(d.state.Cluster, d.inst.Project(), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return "", err
	}

	volStorageName := project.StorageVolume(storageProjectName, volumeName)
	srcPath = storageDrivers.GetVolumeMountPath(d.config["pool"], storageDrivers.VolumeTypeCustom, volStorageName)

	pool, err := storagePools.GetPoolByName(d.state, d.config["pool"])
	if err != nil {
		return "", err
	}

	err = pool.MountCustomVolume(storageProjectName, volumeName, nil)
	if err != nil {
		return "", errors.Wrapf(err, "Failed mounting storage volume %q of type %q on storage pool %q", volumeName, volumeTypeName, pool.Name())
	}
	reverter.Add(func() { pool.UnmountCustomVolume(storageProjectName, volumeName, nil) })

	_, vol, err := d.state.Cluster.GetLocalStoragePoolVolume(storageProjectName, volumeName, db.StoragePoolVolumeTypeCustom, pool.ID())
	if err != nil {
		return "", errors.Wrapf(err, "Failed to fetch local storage volume record")
	}

	if d.inst.Type() == instancetype.Container && vol.ContentType == db.StoragePoolVolumeContentTypeNameFS {
		err = d.storagePoolVolumeAttachShift(storageProjectName, pool.Name(), volumeName, db.StoragePoolVolumeTypeCustom, srcPath)
		if err != nil {
			return "", errors.Wrapf(err, "Failed shifting storage volume %q of type %q on storage pool %q", volumeName, volumeTypeName, pool.Name())
		}
	}

	if vol.ContentType == db.StoragePoolVolumeContentTypeNameBlock {
		srcPath, err = pool.GetCustomVolumeDisk(storageProjectName, volumeName)
		if err != nil {
			return "", errors.Wrapf(err, "Failed to get disk path")
		}
	}

	return srcPath, nil
}

// createDevice creates a disk device mount on host.
func (d *disk) createDevice() (string, error) {
	revert := revert.New()
	defer revert.Fail()

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
			clusterName, userName := d.cephCreds()

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
			clusterName, userName := d.cephCreds()

			// Map the RBD.
			rbdPath, err := diskCephRbdMap(clusterName, userName, poolName, volumeName)
			if err != nil {
				msg := fmt.Sprintf("Could not mount map Ceph RBD: %v", err)
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
		// Mount the pool volume.
		var err error
		srcPath, err = d.mountPoolVolume(revert)
		if err != nil {
			if !isRequired {
				// Leave to the pathExists check below.
				logger.Warn(err.Error())
			} else {
				return "", err
			}
		}
	}

	// Check if the source exists unless it is a cephfs.
	if fsName != "ceph" && !shared.PathExists(srcPath) {
		if !isRequired {
			return "", nil
		}
		return "", fmt.Errorf("Source path %q doesn't exist for device %q", srcPath, d.name)
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

	revert.Success()
	return devPath, nil
}

func (d *disk) storagePoolVolumeAttachShift(projectName, poolName, volumeName string, volumeType int, remapPath string) error {
	// Load the DB records.
	poolID, pool, err := d.state.Cluster.GetStoragePool(poolName)
	if err != nil {
		return err
	}

	_, volume, err := d.state.Cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	poolVolumePut := volume.Writable()

	// Check if unmapped.
	if shared.IsTrue(poolVolumePut.Config["security.unmapped"]) {
		// No need to look at containers and maps for unmapped volumes.
		return nil
	}

	// Get the on-disk idmap for the volume.
	var lastIdmap *idmap.IdmapSet
	if poolVolumePut.Config["volatile.idmap.last"] != "" {
		lastIdmap, err = idmap.JSONUnmarshal(poolVolumePut.Config["volatile.idmap.last"])
		if err != nil {
			logger.Errorf("Failed to unmarshal last idmapping: %q", poolVolumePut.Config["volatile.idmap.last"])
			return err
		}
	}

	var nextIdmap *idmap.IdmapSet
	nextJSONMap := "[]"
	if !shared.IsTrue(poolVolumePut.Config["security.shifted"]) {
		c := d.inst.(instance.Container)
		// Get the container's idmap.
		if c.IsRunning() {
			nextIdmap, err = c.CurrentIdmap()
		} else {
			nextIdmap, err = c.NextIdmap()
		}
		if err != nil {
			return err
		}

		if nextIdmap != nil {
			nextJSONMap, err = idmap.JSONMarshal(nextIdmap)
			if err != nil {
				return err
			}
		}
	}
	poolVolumePut.Config["volatile.idmap.next"] = nextJSONMap

	if !nextIdmap.Equals(lastIdmap) {
		logger.Debugf("Shifting storage volume")

		if !shared.IsTrue(poolVolumePut.Config["security.shifted"]) {
			volumeUsedBy := []instance.Instance{}
			err = storagePools.VolumeUsedByInstanceDevices(d.state, poolName, projectName, volume, true, func(dbInst db.Instance, project api.Project, profiles []api.Profile, usedByDevices []string) error {
				inst, err := instance.Load(d.state, db.InstanceToArgs(&dbInst), profiles)
				if err != nil {
					return err
				}

				volumeUsedBy = append(volumeUsedBy, inst)
				return nil
			})
			if err != nil {
				return err
			}

			if len(volumeUsedBy) > 1 {
				for _, inst := range volumeUsedBy {
					if inst.Type() != instancetype.Container {
						continue
					}

					ct := inst.(instance.Container)

					var ctNextIdmap *idmap.IdmapSet

					if ct.IsRunning() {
						ctNextIdmap, err = ct.CurrentIdmap()
					} else {
						ctNextIdmap, err = ct.NextIdmap()
					}
					if err != nil {
						return fmt.Errorf("Failed to retrieve idmap of container")
					}

					if !nextIdmap.Equals(ctNextIdmap) {
						return fmt.Errorf("Idmaps of container %q and storage volume %q are not identical", ct.Name(), volumeName)
					}
				}
			} else if len(volumeUsedBy) == 1 {
				// If we're the only one who's attached that container
				// we can shift the storage volume.
				// I'm not sure if we want some locking here.
				if volumeUsedBy[0].Name() != d.inst.Name() {
					return fmt.Errorf("Idmaps of container and storage volume are not identical")
				}
			}
		}

		// Unshift rootfs.
		if lastIdmap != nil {
			var err error

			if pool.Driver == "zfs" {
				err = lastIdmap.UnshiftRootfs(remapPath, storageDrivers.ShiftZFSSkipper)
			} else {
				err = lastIdmap.UnshiftRootfs(remapPath, nil)
			}

			if err != nil {
				logger.Errorf("Failed to unshift %q", remapPath)
				return err
			}

			logger.Debugf("Unshifted %q", remapPath)
		}

		// Shift rootfs.
		if nextIdmap != nil {
			var err error

			if pool.Driver == "zfs" {
				err = nextIdmap.ShiftRootfs(remapPath, storageDrivers.ShiftZFSSkipper)
			} else {
				err = nextIdmap.ShiftRootfs(remapPath, nil)
			}

			if err != nil {
				logger.Errorf("Failed to shift %q", remapPath)
				return err
			}

			logger.Debugf("Shifted %q", remapPath)
		}
		logger.Debugf("Shifted storage volume")
	}

	jsonIdmap := "[]"
	if nextIdmap != nil {
		var err error
		jsonIdmap, err = idmap.JSONMarshal(nextIdmap)
		if err != nil {
			logger.Errorf("Failed to marshal idmap")
			return err
		}
	}

	// Update last idmap.
	poolVolumePut.Config["volatile.idmap.last"] = jsonIdmap

	err = d.state.Cluster.UpdateStoragePoolVolume(projectName, volumeName, volumeType, poolID, poolVolumePut.Description, poolVolumePut.Config)
	if err != nil {
		return err
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *disk) Stop() (*deviceConfig.RunConfig, error) {
	if d.inst.Type() == instancetype.VM {
		return d.stopVM()
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

func (d *disk) stopVM() (*deviceConfig.RunConfig, error) {
	// VM disk dir shares uses virtfs-proxy-helper, so we should stop that if it is running.
	pidPath := filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("%s.pid", d.name))
	if shared.PathExists(pidPath) {
		proc, err := subprocess.ImportProcess(pidPath)
		if err != nil {
			return &deviceConfig.RunConfig{}, err
		}

		err = proc.Stop()
		if err != nil {
			return &deviceConfig.RunConfig{}, err
		}

		// Remove PID file and socket file.
		os.Remove(pidPath)
		os.Remove(filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("%s.sock", d.name)))
	}

	// And do the same for the virtiofsd export.
	pidPath = filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("virtio-fs.%s.pid", d.name))
	if shared.PathExists(pidPath) {
		proc, err := subprocess.ImportProcess(pidPath)
		if err != nil {
			return &deviceConfig.RunConfig{}, err
		}

		err = proc.Stop()
		// virtiofsd will terminate automatically once the VM has stopped. We therefore should only
		// return an error if it's a running process.
		if err != nil && err != subprocess.ErrNotRunning {
			return &deviceConfig.RunConfig{}, err
		}

		// Remove PID file and socket file.
		os.Remove(pidPath)
		os.Remove(filepath.Join(d.inst.Path(), fmt.Sprintf("%s.sock", d.name)))
	}

	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *disk) postStop() error {
	// Check if pool-specific action should be taken to unmount custom volume disks.
	if d.config["pool"] != "" && d.config["path"] != "/" {
		pool, err := storagePools.GetPoolByName(d.state, d.config["pool"])
		if err != nil {
			return err
		}

		// Only custom volumes can be attached currently.
		storageProjectName, err := project.StorageVolumeProject(d.state.Cluster, d.inst.Project(), db.StoragePoolVolumeTypeCustom)
		if err != nil {
			return err
		}

		_, err = pool.UnmountCustomVolume(storageProjectName, d.config["source"], nil)
		if err != nil {
			return err
		}

		return nil
	}

	devPath := d.getDevicePath(d.name, d.config)

	// Clean any existing entry.
	if shared.PathExists(devPath) {
		// Unmount the host side if not already.
		// Don't check for errors here as this is just to catch any existing mounts that we have not
		// unmounted on the host after device was started (such as when using cephfs with VM 9p share).
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
			logger.Errorf("Failed to unmap RBD volume %q for %q: %v", v["ceph_rbd"], project.Instance(d.inst.Project(), d.inst.Name()), err)
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
				return nil, fmt.Errorf("Block device path doesn't exist %q", source)
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
				return nil, fmt.Errorf("Block device doesn't support quotas %q", block)
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
			return nil, fmt.Errorf("Failed to query zfs filesystem information for %q: %v", dev[1], err)
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
			return nil, fmt.Errorf("Unable to find backing block for zfs pool %q", poolName)
		}
	} else if fs == "btrfs" && shared.PathExists(dev[1]) {
		// Accessible btrfs filesystems
		output, err := shared.RunCommand("btrfs", "filesystem", "show", dev[1])
		if err != nil {
			return nil, fmt.Errorf("Failed to query btrfs filesystem information for %q: %v", dev[1], err)
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
		return nil, fmt.Errorf("Invalid block device %q", dev[1])
	}

	return devices, nil
}

// generateVMConfigDrive generates an ISO containing the cloud init config for a VM.
// Returns the path to the ISO.
func (d *disk) generateVMConfigDrive() (string, error) {
	scratchDir := filepath.Join(d.inst.DevicesPath(), storageDrivers.PathNameEncode(d.name))

	// Check we have the mkisofs tool available.
	mkisofsPath, err := exec.LookPath("mkisofs")
	if err != nil {
		return "", err
	}

	// Create config drive dir.
	err = os.MkdirAll(scratchDir, 0100)
	if err != nil {
		return "", err
	}

	instanceConfig := d.inst.ExpandedConfig()

	// Use an empty vendor-data file if no custom vendor-data supplied.
	vendorData := instanceConfig["user.vendor-data"]
	if vendorData == "" {
		vendorData = "#cloud-config\n{}"
	}

	err = ioutil.WriteFile(filepath.Join(scratchDir, "vendor-data"), []byte(vendorData), 0400)
	if err != nil {
		return "", err
	}

	// Use an empty user-data file if no custom user-data supplied.
	userData := instanceConfig["user.user-data"]
	if userData == "" {
		userData = "#cloud-config\n{}"
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
	_, err = shared.RunCommand(mkisofsPath, "-R", "-V", "cidata", "-o", isoPath, scratchDir)
	if err != nil {
		return "", err
	}

	// Remove the config drive folder.
	os.RemoveAll(scratchDir)

	return isoPath, nil
}

// cephCreds returns cluster name and user name to use for ceph disks.
func (d *disk) cephCreds() (string, string) {
	// Apply the ceph configuration.
	userName := d.config["ceph.user_name"]
	if userName == "" {
		userName = "admin"
	}

	clusterName := d.config["ceph.cluster_name"]
	if clusterName == "" {
		clusterName = "ceph"
	}

	return clusterName, userName
}
