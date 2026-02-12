package instancetype

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
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
	// lxddoc:generate(group=instance-boot, key=boot.autostart)
	//
	// ---
	//  type: bool
	//  liveupdate: no
	//  shortdesc: Controls whether to always start the instance when LXD starts (if not set, restore the last state)
	"boot.autostart": validate.Optional(validate.IsBool),
	// lxddoc:generate(group=instance-boot, key=boot.autostart.delay)
	//
	// ---
	//  type: integer
	//  default: 0
	//  liveupdate: no
	//  shortdesc: Number of seconds to wait after the instance started before starting the next one
	"boot.autostart.delay": validate.Optional(validate.IsInt64),
	// lxddoc:generate(group=instance-boot, key=boot.autostart.priority)
	//
	// ---
	//  type: integer
	//  default: 0
	//  liveupdate: no
	//  shortdesc: What order to start the instances in (starting with the highest value)
	"boot.autostart.priority": validate.Optional(validate.IsInt64),
	// lxddoc:generate(group=instance-boot, key=boot.stop.priority)
	//
	// ---
	//  type: integer
	//  default: 0
	//  liveupdate: no
	//  shortdesc: What order to shut down the instances in (starting with the highest value)
	"boot.stop.priority": validate.Optional(validate.IsInt64),
	// lxddoc:generate(group=instance-boot, key=boot.host_shutdown_timeout)
	//
	// ---
	//  type: integer
	//  default: 30
	//  liveupdate: yes
	//  shortdesc: Seconds to wait for the instance to shut down before it is force-stopped
	"boot.host_shutdown_timeout": validate.Optional(validate.IsInt64),

	// lxddoc:generate(group=instance-cloud-init, key=cloud-init.network-config)
	//
	// ---
	//  type: string
	//  default: `DHCP on eth0`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Network configuration for `cloud-init` (content is used as seed value)
	"cloud-init.network-config": validate.Optional(validate.IsYAML),
	// lxddoc:generate(group=instance-cloud-init, key=cloud-init.user-data)
	//
	// ---
	//  type: string
	//  default: `#cloud-config`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: User data for `cloud-init` (content is used as seed value)
	"cloud-init.user-data": validate.Optional(validate.IsCloudInitUserData),
	// lxddoc:generate(group=instance-cloud-init, key=cloud-init.vendor-data)
	//
	// ---
	//  type: string
	//  default: `#cloud-config`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Vendor data for `cloud-init` (content is used as seed value)
	"cloud-init.vendor-data": validate.Optional(validate.IsCloudInitUserData),

	// lxddoc:generate(group=instance-cloud-init, key=user.network-config)
	//
	// ---
	//  type: string
	//  default: `DHCP on eth0`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Legacy version of `cloud-init.network-config`

	// lxddoc:generate(group=instance-cloud-init, key=user.user-data)
	//
	// ---
	//  type: string
	//  default: `#cloud-config`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Legacy version of `cloud-init.user-data`

	// lxddoc:generate(group=instance-cloud-init, key=user.vendor-data)
	//
	// ---
	//  type: string
	//  default: `#cloud-config`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Legacy version of `cloud-init.vendor-data`

	// lxddoc:generate(group=instance-miscellaneous, key=cluster.evacuate)
	//
	// ---
	//  type: string
	//  default: `auto`
	//  liveupdate: no
	//  shortdesc: Controls what to do when evacuating the instance (`auto`, `migrate`, `live-migrate`, or `stop`)
	"cluster.evacuate": validate.Optional(validate.IsOneOf("auto", "migrate", "live-migrate", "stop")),

	// lxddoc:generate(group=instance-resource-limits, key=limits.cpu)
	//
	// ---
	//  type: string
	//  default: for VMs: 1 CPU
	//  liveupdate: yes
	//  shortdesc: Number or range of CPUs to expose to the instance; see {ref}`instance-options-limits-cpu`
	"limits.cpu": validate.Optional(validate.IsValidCPUSet),
	// lxddoc:generate(group=instance-resource-limits, key=limits.cpu.nodes)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  shortdesc: Comma-separated list of NUMA node IDs or ranges to place the instance CPUs on; see {ref}`instance-options-limits-cpu-container`
	"limits.cpu.nodes": validate.Optional(validate.IsValidCPUSet),
	// lxddoc:generate(group=instance-resource-limits, key=limits.disk.priority)
	//
	// ---
	//  type: integer
	//  default: `5` (medium)
	//  liveupdate: yes
	//  shortdesc: Controls how much priority to give to the instance’s I/O requests when under load (integer between 0 and 10)
	"limits.disk.priority": validate.Optional(validate.IsPriority),
	// lxddoc:generate(group=instance-resource-limits, key=limits.memory)
	//
	// ---
	//  type: string
	//  default: for VMs: `1Gib`
	//  liveupdate: yes
	//  shortdesc: Percentage of the host's memory or fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`)
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
	// lxddoc:generate(group=instance-resource-limits, key=limits.network.priority)
	//
	// ---
	//  type: integer
	//  default: `0` (minimum)
	//  liveupdate: yes
	//  shortdesc: Controls how much priority to give to the instance’s network requests when under load (integer between 0 and 10)
	"limits.network.priority": validate.Optional(validate.IsPriority),

	// Caller is responsible for full validation of any raw.* value.

	// lxddoc:generate(group=instance-raw, key=raw.apparmor)
	//
	// ---
	//  type: blob
	//  liveupdate: yes
	//  shortdesc: AppArmor profile entries to be appended to the generated profile
	"raw.apparmor": validate.IsAny,
	// lxddoc:generate(group=instance-raw, key=raw.idmap)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: unprivileged container
	//  shortdesc: Raw idmap configuration (for example, `both 1000 1000`)
	"raw.idmap": validate.IsAny,

	"security.devlxd":            validate.Optional(validate.IsBool),
	"security.protection.delete": validate.Optional(validate.IsBool),

	// lxddoc:generate(group=instance-snapshots, key=snapshots.schedule)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  shortdesc: Cron expression (`<minute> <hour> <dom> <month> <dow>`), a comma-separated list of schedule aliases (`@hourly`, `@daily`, `@midnight`, `@weekly`, `@monthly`, `@annually`, `@yearly`), or empty to disable automatic snapshots (the default)
	"snapshots.schedule": validate.Optional(validate.IsCron([]string{"@hourly", "@daily", "@midnight", "@weekly", "@monthly", "@annually", "@yearly", "@startup", "@never"})),
	// lxddoc:generate(group=instance-snapshots, key=snapshots.schedule.stopped)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  shortdesc: Controls whether to automatically snapshot stopped instances
	"snapshots.schedule.stopped": validate.Optional(validate.IsBool),
	// lxddoc:generate(group=instance-snapshots, key=snapshots.pattern)
	//
	// ---
	//  type: string
	//  default: `snap%d`
	//  liveupdate: no
	//  shortdesc: Pongo2 template string that represents the snapshot name (used for scheduled snapshots and unnamed snapshots); see {ref}`instance-options-snapshots-names`
	"snapshots.pattern": validate.IsAny,
	// lxddoc:generate(group=instance-snapshots, key=snapshots.expiry)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  shortdesc: Controls when snapshots are to be deleted (expects an expression like `1M 2H 3d 4w 5m 6y`)
	"snapshots.expiry": func(value string) error {
		// Validate expression
		_, err := shared.GetExpiry(time.Time{}, value)
		return err
	},

	// Volatile keys.
	"volatile.apply_template":         validate.IsAny,
	"volatile.base_image":             validate.IsAny,
	"volatile.cloud-init.instance-id": validate.Optional(validate.IsUUID),
	"volatile.evacuate.origin":        validate.IsAny,
	"volatile.last_state.power":       validate.IsAny,
	"volatile.apply_quota":            validate.IsAny,
	"volatile.uuid":                   validate.Optional(validate.IsUUID),
	"volatile.uuid.generation":        validate.Optional(validate.IsUUID),
}

// InstanceConfigKeysContainer is a map of config key to validator. (keys applying to containers only).
var InstanceConfigKeysContainer = map[string]func(value string) error{
	// lxddoc:generate(group=instance-resource-limits, key=limits.cpu.allowance)
	//
	// ---
	//  type: string
	//  default: 100%
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Controls how much of the CPU can be used: either a percentage (`50%`) for a soft limit or a chunk of time (`25ms/100ms`) for a hard limit; see {ref}`instance-options-limits-cpu-container`
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
	// lxddoc:generate(group=instance-resource-limits, key=limits.cpu.priority)
	//
	// ---
	//  type: integer
	//  default: `10` (maximum)
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: CPU scheduling priority compared to other instances sharing the same CPUs when overcommitting resources (integer between 0 and 10); see {ref}`instance-options-limits-cpu-container`
	"limits.cpu.priority": validate.Optional(validate.IsPriority),
	// lxddoc:generate(group=instance-resource-limits, key=limits.hugepages.64KB)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 64 KB huge pages; see {ref}`instance-options-limits-hugepages`
	"limits.hugepages.64KB": validate.Optional(validate.IsSize),
	// lxddoc:generate(group=instance-resource-limits, key=limits.hugepages.1MB)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 1 MB huge pages; see {ref}`instance-options-limits-hugepages`
	"limits.hugepages.1MB": validate.Optional(validate.IsSize),
	// lxddoc:generate(group=instance-resource-limits, key=limits.hugepages.2MB)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 2 MB huge pages; see {ref}`instance-options-limits-hugepages`
	"limits.hugepages.2MB": validate.Optional(validate.IsSize),
	// lxddoc:generate(group=instance-resource-limits, key=limits.hugepages.1GB)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 1 GB huge pages; see {ref}`instance-options-limits-hugepages`
	"limits.hugepages.1GB": validate.Optional(validate.IsSize),
	// lxddoc:generate(group=instance-resource-limits, key=limits.memory.enforce)
	//
	// ---
	//  type: string
	//  default: `hard`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: If `hard`, the instance cannot exceed its memory limit; if `soft`, the instance can exceed its memory limit when extra host memory is available
	"limits.memory.enforce": validate.Optional(validate.IsOneOf("soft", "hard")),

	// lxddoc:generate(group=instance-resource-limits, key=limits.memory.swap)
	//
	// ---
	//  type: bool
	//  default: `true`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Controls whether to encourage/discourage swapping less used pages for this instance
	"limits.memory.swap": validate.Optional(validate.IsBool),
	// lxddoc:generate(group=instance-resource-limits, key=limits.memory.swap.priority)
	//
	// ---
	//  type: integer
	//  default: `10` (maximum)
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Prevents the instance from being swapped to disk (integer between 0 and 10; the higher the value, the less likely the instance is to be swapped to disk)
	"limits.memory.swap.priority": validate.Optional(validate.IsPriority),
	// lxddoc:generate(group=instance-resource-limits, key=limits.processes)
	//
	// ---
	//  type: integer
	//  default: -(max)
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Maximum number of processes that can run in the instance
	"limits.processes": validate.Optional(validate.IsInt64),

	// lxddoc:generate(group=instance-miscellaneous, key=linux.kernel_modules)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Comma-separated list of kernel modules to load before starting the instance
	"linux.kernel_modules": validate.IsAny,

	// lxddoc:generate(group=instance-migration, key=migration.incremental.memory)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Controls whether to use incremental memory transfer of the instance’s memory to reduce downtime
	"migration.incremental.memory": validate.Optional(validate.IsBool),
	// lxddoc:generate(group=instance-migration, key=migration.incremental.memory.iterations)
	//
	// ---
	//  type: integer
	//  default: `10`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Maximum number of transfer operations to go through before stopping the instance
	"migration.incremental.memory.iterations": validate.Optional(validate.IsUint32),
	// lxddoc:generate(group=instance-migration, key=migration.incremental.memory.goal)
	//
	// ---
	//  type: integer
	//  default: `70`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Percentage of memory to have in sync before stopping the instance
	"migration.incremental.memory.goal": validate.Optional(validate.IsUint32),

	// lxddoc:generate(group=instance-nvidia, key=nvidia.runtime)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls whether to pass the host NVIDIA and CUDA runtime libraries into the instance
	"nvidia.runtime": validate.Optional(validate.IsBool),
	// lxddoc:generate(group=instance-nvidia, key=nvidia.driver.capabilities)
	//
	// ---
	//  type: string
	//  default: `compute,utility`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: What driver capabilities the instance needs (sets `libnvidia-container NVIDIA_DRIVER_CAPABILITIES`)
	"nvidia.driver.capabilities": validate.IsAny,
	// lxddoc:generate(group=instance-nvidia, key=nvidia.require.cuda)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Version expression for the required CUDA version (sets `libnvidia-container NVIDIA_REQUIRE_CUDA`)
	"nvidia.require.cuda": validate.IsAny,
	// lxddoc:generate(group=instance-nvidia, key=nvidia.require.driver)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Version expression for the required driver version (sets `libnvidia-container NVIDIA_REQUIRE_DRIVER`)
	"nvidia.require.driver": validate.IsAny,

	// Caller is responsible for full validation of any raw.* value.

	// lxddoc:generate(group=instance-raw, key=raw.lxc)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Raw LXC configuration to be appended to the generated one
	"raw.lxc": validate.IsAny,
	// lxddoc:generate(group=instance-raw, key=raw.seccomp)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Raw Seccomp configuration
	"raw.seccomp": validate.IsAny,

	"security.devlxd.images": validate.Optional(validate.IsBool),

	"security.idmap.base":     validate.Optional(validate.IsUint32),
	"security.idmap.isolated": validate.Optional(validate.IsBool),
	"security.idmap.size":     validate.Optional(validate.IsUint32),

	"security.nesting":          validate.Optional(validate.IsBool),
	"security.privileged":       validate.Optional(validate.IsBool),
	"security.protection.shift": validate.Optional(validate.IsBool),

	"security.syscalls.allow":                        validate.IsAny,
	"security.syscalls.blacklist_default":            validate.Optional(validate.IsBool),
	"security.syscalls.blacklist_compat":             validate.Optional(validate.IsBool),
	"security.syscalls.blacklist":                    validate.IsAny,
	"security.syscalls.deny_default":                 validate.Optional(validate.IsBool),
	"security.syscalls.deny_compat":                  validate.Optional(validate.IsBool),
	"security.syscalls.deny":                         validate.IsAny,
	"security.syscalls.intercept.bpf":                validate.Optional(validate.IsBool),
	"security.syscalls.intercept.bpf.devices":        validate.Optional(validate.IsBool),
	"security.syscalls.intercept.mknod":              validate.Optional(validate.IsBool),
	"security.syscalls.intercept.mount":              validate.Optional(validate.IsBool),
	"security.syscalls.intercept.mount.allowed":      validate.IsAny,
	"security.syscalls.intercept.mount.fuse":         validate.IsAny,
	"security.syscalls.intercept.mount.shift":        validate.Optional(validate.IsBool),
	"security.syscalls.intercept.sched_setscheduler": validate.Optional(validate.IsBool),
	"security.syscalls.intercept.setxattr":           validate.Optional(validate.IsBool),
	"security.syscalls.whitelist":                    validate.IsAny,

	"volatile.last_state.idmap": validate.IsAny,
	"volatile.idmap.base":       validate.IsAny,
	"volatile.idmap.current":    validate.IsAny,
	"volatile.idmap.next":       validate.IsAny,
}

// InstanceConfigKeysVM is a map of config key to validator. (keys applying to VM only).
var InstanceConfigKeysVM = map[string]func(value string) error{
	// lxddoc:generate(group=instance-resource-limits, key=limits.memory.hugepages)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Controls whether to back the instance using huge pages rather than regular system memory
	"limits.memory.hugepages": validate.Optional(validate.IsBool),

	// lxddoc:generate(group=instance-migration, key=migration.stateful)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Controls whether to allow for stateful stop/start and snapshots (enabling this prevents the use of some features that are incompatible with it)
	"migration.stateful": validate.Optional(validate.IsBool),

	// Caller is responsible for full validation of any raw.* value.

	// lxddoc:generate(group=instance-raw, key=raw.qemu)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Raw QEMU configuration to be appended to the generated command line
	"raw.qemu": validate.IsAny,
	// lxddoc:generate(group=instance-raw, key=raw.qemu.conf)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Addition/override to the generated `qemu.conf` file (see {ref}`instance-options-qemu`)
	"raw.qemu.conf": validate.IsAny,

	"security.agent.metrics": validate.Optional(validate.IsBool),
	"security.secureboot":    validate.Optional(validate.IsBool),

	// lxddoc:generate(group=instance-miscellaneous, key=user.*)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  shortdesc: Free-form user key/value storage (can be used in search)

	// lxddoc:generate(group=instance-miscellaneous, key=agent.nic_config)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Controls whether to set the name and MTU of the default network interfaces to be the same as the instance devices (this happens automatically for containers)
	"agent.nic_config": validate.Optional(validate.IsBool),

	"volatile.apply_nvram": validate.Optional(validate.IsBool),
	"volatile.vsock_id":    validate.Optional(validate.IsInt64),
}

// ConfigKeyChecker returns a function that will check whether or not
// a provide value is valid for the associate config key.  Returns an
// error if the key is not known.  The checker function only performs
// syntactic checking of the value, semantic and usage checking must
// be done by the caller.  User defined keys are always considered to
// be valid, e.g. user.* and environment.* keys.
func ConfigKeyChecker(key string, instanceType Type) (func(value string) error, error) {
	f, ok := InstanceConfigKeysAny[key]
	if ok {
		return f, nil
	}

	if instanceType == Any || instanceType == Container {
		f, ok := InstanceConfigKeysContainer[key]
		if ok {
			return f, nil
		}
	}

	if instanceType == Any || instanceType == VM {
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

		if strings.HasSuffix(key, ".last_state.ip_addresses") {
			return validate.IsListOf(validate.IsNetworkAddress), nil
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
	}

	if strings.HasPrefix(key, "environment.") {
		return func(val string) error {
			if strings.Contains(val, "\n") {
				return errors.New("Environment variables cannot contain line breaks")
			}

			return nil
		}, nil
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

	if (instanceType == Any || instanceType == Container) &&
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
