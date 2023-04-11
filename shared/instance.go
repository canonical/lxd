package shared

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared/units"
	"github.com/lxc/lxd/shared/validate"
)

// InstanceAction indicates the type of action being performed.
type InstanceAction string

// InstanceAction types.
const (
	Stop     InstanceAction = "stop"
	Start    InstanceAction = "start"
	Restart  InstanceAction = "restart"
	Freeze   InstanceAction = "freeze"
	Unfreeze InstanceAction = "unfreeze"
)

// ConfigVolatilePrefix indicates the prefix used for volatile config keys.
const ConfigVolatilePrefix = "volatile."

// IsRootDiskDevice returns true if the given device representation is configured as root disk for
// an instance. It typically get passed a specific entry of api.Instance.Devices.
func IsRootDiskDevice(device map[string]string) bool {
	// Root disk devices also need a non-empty "pool" property, but we can't check that here
	// because this function is used with clients talking to older servers where there was no
	// concept of a storage pool, and also it is used for migrating from old to new servers.
	// The validation of the non-empty "pool" property is done inside the disk device itself.
	if device["type"] == "disk" && device["path"] == "/" && device["source"] == "" {
		return true
	}

	return false
}

// ErrNoRootDisk means there is no root disk device found.
var ErrNoRootDisk = fmt.Errorf("No root device could be found")

// GetRootDiskDevice returns the instance device that is configured as root disk.
// Returns the device name and device config map.
func GetRootDiskDevice(devices map[string]map[string]string) (string, map[string]string, error) {
	var devName string
	var dev map[string]string

	for n, d := range devices {
		if IsRootDiskDevice(d) {
			if devName != "" {
				return "", nil, fmt.Errorf("More than one root device found")
			}

			devName = n
			dev = d
		}
	}

	if devName != "" {
		return devName, dev, nil
	}

	return "", nil, ErrNoRootDisk
}

// HugePageSizeKeys is a list of known hugepage size configuration keys.
var HugePageSizeKeys = [...]string{"limits.hugepages.64KB", "limits.hugepages.1MB", "limits.hugepages.2MB", "limits.hugepages.1GB"}

// HugePageSizeSuffix contains the list of known hugepage size suffixes.
var HugePageSizeSuffix = [...]string{"64KB", "1MB", "2MB", "1GB"}

// InstanceConfigKeysAny is a map of config key to validator. (keys applying to containers AND virtual machines).
var InstanceConfigKeysAny = map[string]func(value string) error{
	// :lxddoc(InstanceConfig.boot.autostart)
	"boot.autostart": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfig.boot.autostart.delay)
	"boot.autostart.delay": validate.Optional(validate.IsInt64),
	// :lxddoc(InstanceConfig.boot.autostart.priority)
	"boot.autostart.priority": validate.Optional(validate.IsInt64),
	// :lxddoc(InstanceConfig.boot.stop.priority)
	"boot.stop.priority": validate.Optional(validate.IsInt64),
	// :lxddoc(InstanceConfig.boot.host_shutdown_timeout)
	"boot.host_shutdown_timeout": validate.Optional(validate.IsInt64),

	// :lxddoc(InstanceConfig.cloud-init.network-config)
	"cloud-init.network-config": validate.Optional(validate.IsYAML),
	// :lxddoc(InstanceConfig.cloud-init.user-data)
	"cloud-init.user-data": validate.Optional(validate.IsCloudInitUserData),
	// :lxddoc(InstanceConfig.cloud-init.vendor-data)
	"cloud-init.vendor-data": validate.Optional(validate.IsCloudInitUserData),

	// :lxddoc(InstanceConfig.cluster.evacuate)
	"cluster.evacuate": validate.Optional(validate.IsOneOf("auto", "migrate", "live-migrate", "stop")),

	// :lxddoc(InstanceConfig.limits.cpu)
	"limits.cpu": validate.Optional(validate.IsValidCPUSet),
	// :lxddoc(InstanceConfig.limits.disk.priority)
	"limits.disk.priority": validate.Optional(validate.IsPriority),
	// :lxddoc(InstanceConfig.limits.memory)
	"limits.memory": func(value string) error {
		if value == "" {
			return nil
		}

		if strings.HasSuffix(value, "%") {
			num, err := strconv.ParseInt(strings.TrimSuffix(value, "%"), 10, 64)
			if err != nil {
				return err
			}

			if num == 0 {
				return errors.New("Memory limit can't be 0%")
			}

			return nil
		}

		num, err := units.ParseByteSizeString(value)
		if err != nil {
			return err
		}

		if num == 0 {
			return fmt.Errorf("Memory limit can't be 0")
		}

		return nil
	},
	// :lxddoc(InstanceConfig.limits.network.priority)
	"limits.network.priority": validate.Optional(validate.IsPriority),

	// Caller is responsible for full validation of any raw.* value.
	// :lxddoc(InstanceConfig.raw.apparmor)
	"raw.apparmor": validate.IsAny,

	// :lxddoc(InstanceConfig.security.devlxd)
	"security.devlxd": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfig.security.protection.delete)
	"security.protection.delete": validate.Optional(validate.IsBool),

	// :lxddoc(InstanceConfig.snapshots.schedule)
	"snapshots.schedule": validate.Optional(validate.IsCron([]string{"@hourly", "@daily", "@midnight", "@weekly", "@monthly", "@annually", "@yearly", "@startup", "@never"})),
	// :lxddoc(InstanceConfig.snapshots.schedule.stopped)
	"snapshots.schedule.stopped": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfig.snapshots.pattern)
	"snapshots.pattern": validate.IsAny,
	// :lxddoc(InstanceConfig.snapshots.expiry)
	"snapshots.expiry": func(value string) error {
		// Validate expression
		_, err := GetExpiry(time.Time{}, value)
		return err
	},

	// Volatile keys.
	// :lxddoc(InstanceConfig.volatile.apply_template)
	"volatile.apply_template": validate.IsAny,
	// :lxddoc(InstanceConfig.volatile.base_image)
	"volatile.base_image": validate.IsAny,
	// :lxddoc(InstanceConfig.volatile.cloud-init.instance-id)
	"volatile.cloud-init.instance-id": validate.Optional(validate.IsUUID),
	// :lxddoc(InstanceConfig.volatile.evacuate.origin)
	"volatile.evacuate.origin": validate.IsAny,
	// :lxddoc(InstanceConfig.volatile.last_state.idmap)
	"volatile.last_state.idmap": validate.IsAny,
	// :lxddoc(InstanceConfig.volatile.last_state.power)
	"volatile.last_state.power": validate.IsAny,
	// :lxddoc(InstanceConfig.volatile.last_state.ready)
	"volatile.last_state.ready": validate.IsBool,
	// :lxddoc(InstanceConfig.volatile.idmap.base)
	"volatile.idmap.base": validate.IsAny,
	// :lxddoc(InstanceConfig.volatile.idmap.current)
	"volatile.idmap.current": validate.IsAny,
	// :lxddoc(InstanceConfig.volatile.idmap.next)
	"volatile.idmap.next": validate.IsAny,
	// :lxddoc(InstanceConfig.volatile.apply_quota)
	"volatile.apply_quota": validate.IsAny,
	// :lxddoc(InstanceConfig.volatile.uuid)
	"volatile.uuid": validate.Optional(validate.IsUUID),
	// :lxddoc(InstanceConfig.volatile.vsock_id)
	"volatile.vsock_id": validate.Optional(validate.IsInt64),
	// :lxddoc(InstanceConfig.volatile.uuid.generation)
	"volatile.uuid.generation": validate.Optional(validate.IsUUID),

	// Caller is responsible for full validation of any raw.* value.
	// :lxddoc(InstanceConfig.raw.idmap)
	"raw.idmap": validate.IsAny,
}

// InstanceConfigKeysContainer is a map of config key to validator. (keys applying to containers only).
var InstanceConfigKeysContainer = map[string]func(value string) error{
	// :lxddoc(InstanceConfigContainer.limits.cpu.allowance)
	"limits.cpu.allowance": func(value string) error {
		if value == "" {
			return nil
		}

		if strings.HasSuffix(value, "%") {
			// Percentage based allocation
			_, err := strconv.Atoi(strings.TrimSuffix(value, "%"))
			if err != nil {
				return err
			}

			return nil
		}

		// Time based allocation
		fields := strings.SplitN(value, "/", 2)
		if len(fields) != 2 {
			return fmt.Errorf("Invalid allowance: %s", value)
		}

		_, err := strconv.Atoi(strings.TrimSuffix(fields[0], "ms"))
		if err != nil {
			return err
		}

		_, err = strconv.Atoi(strings.TrimSuffix(fields[1], "ms"))
		if err != nil {
			return err
		}

		return nil
	},
	// :lxddoc(InstanceConfigContainer.limits.cpu.priority)
	"limits.cpu.priority": validate.Optional(validate.IsPriority),
	// :lxddoc(InstanceConfigContainer.limits.hugepages.64KB)
	"limits.hugepages.64KB": validate.Optional(validate.IsSize),
	// :lxddoc(InstanceConfigContainer.limits.hugepages.1MB)
	"limits.hugepages.1MB": validate.Optional(validate.IsSize),
	// :lxddoc(InstanceConfigContainer.limits.hugepages.2MB)
	"limits.hugepages.2MB": validate.Optional(validate.IsSize),
	// :lxddoc(InstanceConfigContainer.limits.hugepages.1GB)
	"limits.hugepages.1GB": validate.Optional(validate.IsSize),
	// :lxddoc(InstanceConfigContainer.limits.memory.enforce)
	"limits.memory.enforce": validate.Optional(validate.IsOneOf("soft", "hard")),

	// :lxddoc(InstanceConfigContainer.limits.memory.swap)
	"limits.memory.swap": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.limits.memory.swap.priority)
	"limits.memory.swap.priority": validate.Optional(validate.IsPriority),
	// :lxddoc(InstanceConfigContainer.limits.processes)
	"limits.processes": validate.Optional(validate.IsInt64),

	// :lxddoc(InstanceConfigContainer.linux.kernel_modules)
	"linux.kernel_modules": validate.IsAny,

	// :lxddoc(InstanceConfigContainer.migration.incremental.memory)
	"migration.incremental.memory": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.migration.incremental.memory.iterations)
	"migration.incremental.memory.iterations": validate.Optional(validate.IsUint32),
	// :lxddoc(InstanceConfigContainer.migration.incremental.memory.goal)
	"migration.incremental.memory.goal": validate.Optional(validate.IsUint32),

	// :lxddoc(InstanceConfigContainer.nvidia.runtime)
	"nvidia.runtime": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.nvidia.driver.capabilities)
	"nvidia.driver.capabilities": validate.IsAny,
	// :lxddoc(InstanceConfigContainer.nvidia.require.cuda)
	"nvidia.require.cuda": validate.IsAny,
	// :lxddoc(InstanceConfigContainer.nvidia.require.driver)
	"nvidia.require.driver": validate.IsAny,

	// Caller is responsible for full validation of any raw.* value.
	// :lxddoc(InstanceConfigContainer.raw.lxc)
	"raw.lxc": validate.IsAny,
	// :lxddoc(InstanceConfigContainer.raw.seccomp)
	"raw.seccomp": validate.IsAny,

	// :lxddoc(InstanceConfigContainer.security.devlxd.images)
	"security.devlxd.images": validate.Optional(validate.IsBool),

	// :lxddoc(InstanceConfigContainer.security.idmap.base)
	"security.idmap.base": validate.Optional(validate.IsUint32),
	// :lxddoc(InstanceConfigContainer.security.idmap.isolated)
	"security.idmap.isolated": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.idmap.size)
	"security.idmap.size": validate.Optional(validate.IsUint32),

	// :lxddoc(InstanceConfigContainer.security.nesting)
	"security.nesting": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.privileged)
	"security.privileged": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.protection.shift)
	"security.protection.shift": validate.Optional(validate.IsBool),

	// :lxddoc(InstanceConfigContainer.security.syscalls.allow)
	"security.syscalls.allow": validate.IsAny,
	// :lxddoc(InstanceConfigContainer.security.syscalls.blacklist_default)
	"security.syscalls.blacklist_default": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.syscalls.blacklist_compat)
	"security.syscalls.blacklist_compat": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.syscalls.blacklist)
	"security.syscalls.blacklist": validate.IsAny,
	// :lxddoc(InstanceConfigContainer.security.syscalls.deny_default)
	"security.syscalls.deny_default": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.syscalls.deny_compat)
	"security.syscalls.deny_compat": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.syscalls.deny)
	"security.syscalls.deny": validate.IsAny,
	// :lxddoc(InstanceConfigContainer.security.syscalls.intercept.bpf)
	"security.syscalls.intercept.bpf": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.syscalls.intercept.bpf.devices)
	"security.syscalls.intercept.bpf.devices": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.syscalls.intercept.mknod)
	"security.syscalls.intercept.mknod": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.syscalls.intercept.mount)
	"security.syscalls.intercept.mount": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.syscalls.intercept.mount.allowed)
	"security.syscalls.intercept.mount.allowed": validate.IsAny,
	// :lxddoc(InstanceConfigContainer.security.syscalls.intercept.mount.fuse)
	"security.syscalls.intercept.mount.fuse": validate.IsAny,
	// :lxddoc(InstanceConfigContainer.security.syscalls.intercept.mount.shift)
	"security.syscalls.intercept.mount.shift": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.syscalls.intercept.sched_setscheduler)
	"security.syscalls.intercept.sched_setscheduler": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.syscalls.intercept.setxattr)
	"security.syscalls.intercept.setxattr": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.syscalls.intercept.sysinfo)
	"security.syscalls.intercept.sysinfo": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigContainer.security.syscalls.whitelist)
	"security.syscalls.whitelist": validate.IsAny,
}

// InstanceConfigKeysVM is a map of config key to validator. (keys applying to VM only).
var InstanceConfigKeysVM = map[string]func(value string) error{
	// :lxddoc(InstanceConfigVM.limits.memory.hugepages)
	"limits.memory.hugepages": validate.Optional(validate.IsBool),

	// :lxddoc(InstanceConfigVM.migration.stateful)
	"migration.stateful": validate.Optional(validate.IsBool),

	// Caller is responsible for full validation of any raw.* value.
	// :lxddoc(InstanceConfigVM.raw.qemu)
	"raw.qemu": validate.IsAny,
	// :lxddoc(InstanceConfigVM.raw.qemu.conf)
	"raw.qemu.conf": validate.IsAny,

	// :lxddoc(InstanceConfigVM.security.agent.metrics)
	"security.agent.metrics": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigVM.security.secureboot)
	"security.secureboot": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigVM.security.sev)
	"security.sev": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigVM.security.sev.policy.es)
	"security.sev.policy.es": validate.Optional(validate.IsBool),
	// :lxddoc(InstanceConfigVM.security.sev.session.dh)
	"security.sev.session.dh": validate.Optional(validate.IsAny),
	// :lxddoc(InstanceConfigVM.security.sev.session.data)
	"security.sev.session.data": validate.Optional(validate.IsAny),

	// :lxddoc(InstanceConfigVM.agent.nic_config)
	"agent.nic_config": validate.Optional(validate.IsBool),

	// :lxddoc(InstanceConfigVM.volatile.apply_nvram)
	"volatile.apply_nvram": validate.Optional(validate.IsBool),
}

// ConfigKeyChecker returns a function that will check whether or not
// a provide value is valid for the associate config key.  Returns an
// error if the key is not known.  The checker function only performs
// syntactic checking of the value, semantic and usage checking must
// be done by the caller.  User defined keys are always considered to
// be valid, e.g. user.* and environment.* keys.
func ConfigKeyChecker(key string, instanceType instancetype.Type) (func(value string) error, error) {
	f, ok := InstanceConfigKeysAny[key]
	if ok {
		return f, nil
	}

	if instanceType == instancetype.Any || instanceType == instancetype.Container {
		f, ok := InstanceConfigKeysContainer[key]
		if ok {
			return f, nil
		}
	}

	if instanceType == instancetype.Any || instanceType == instancetype.VM {
		f, ok := InstanceConfigKeysVM[key]
		if ok {
			return f, nil
		}
	}

	if strings.HasPrefix(key, ConfigVolatilePrefix) {
		if strings.HasSuffix(key, ".hwaddr") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".name") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".host_name") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".mtu") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".created") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".id") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".vlan") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".spoofcheck") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".last_state.vf.parent") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".apply_quota") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".ceph_rbd") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".driver") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".uuid") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".last_state.ready") {
			return validate.IsBool, nil
		}
	}

	if strings.HasPrefix(key, "environment.") {
		return validate.IsAny, nil
	}

	if strings.HasPrefix(key, "user.") {
		return validate.IsAny, nil
	}

	if strings.HasPrefix(key, "image.") {
		return validate.IsAny, nil
	}

	if strings.HasPrefix(key, "limits.kernel.") &&
		(len(key) > len("limits.kernel.")) {
		return validate.IsAny, nil
	}

	if (instanceType == instancetype.Any || instanceType == instancetype.Container) &&
		strings.HasPrefix(key, "linux.sysctl.") {
		return validate.IsAny, nil
	}

	return nil, fmt.Errorf("Unknown configuration key: %s", key)
}

// InstanceIncludeWhenCopying is used to decide whether to include a config item or not when copying an instance.
// The remoteCopy argument indicates if the copy is remote (i.e between LXD nodes) as this affects the keys kept.
func InstanceIncludeWhenCopying(configKey string, remoteCopy bool) bool {
	if configKey == "volatile.base_image" {
		return true // Include volatile.base_image always as it can help optimize copies.
	}

	if configKey == "volatile.last_state.idmap" && !remoteCopy {
		return true // Include volatile.last_state.idmap when doing local copy to avoid needless remapping.
	}

	if strings.HasPrefix(configKey, ConfigVolatilePrefix) {
		return false // Exclude all other volatile keys.
	}

	return true // Keep all other keys.
}
