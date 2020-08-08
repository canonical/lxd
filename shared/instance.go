package shared

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"gopkg.in/robfig/cron.v2"

	"github.com/lxc/lxd/shared/units"
	"github.com/lxc/lxd/shared/validate"
)

type InstanceAction string

const (
	Stop     InstanceAction = "stop"
	Start    InstanceAction = "start"
	Restart  InstanceAction = "restart"
	Freeze   InstanceAction = "freeze"
	Unfreeze InstanceAction = "unfreeze"
)

// IsRootDiskDevice returns true if the given device representation is configured as root disk for
// a container. It typically get passed a specific entry of api.Instance.Devices.
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

// GetRootDiskDevice returns the container device that is configured as root disk
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

	return "", nil, fmt.Errorf("No root device could be found")
}

// HugePageSizeKeys is a list of known hugepage size configuration keys.
var HugePageSizeKeys = [...]string{"limits.hugepages.64KB", "limits.hugepages.1MB", "limits.hugepages.2MB", "limits.hugepages.1GB"}

// HugePageSizeSuffix contains the list of known hugepage size suffixes.
var HugePageSizeSuffix = [...]string{"64KB", "1MB", "2MB", "1GB"}

// KnownInstanceConfigKeys maps all fully defined, well-known config keys
// to an appropriate checker function, which validates whether or not a
// given value is syntactically legal.
var KnownInstanceConfigKeys = map[string]func(value string) error{
	"boot.autostart":             validate.Optional(validate.IsBool),
	"boot.autostart.delay":       validate.Optional(validate.IsInt64),
	"boot.autostart.priority":    validate.Optional(validate.IsInt64),
	"boot.stop.priority":         validate.Optional(validate.IsInt64),
	"boot.host_shutdown_timeout": validate.Optional(validate.IsInt64),

	"limits.cpu": func(value string) error {
		if value == "" {
			return nil
		}

		// Validate the character set
		match, _ := regexp.MatchString("^[-,0-9]*$", value)
		if !match {
			return fmt.Errorf("Invalid CPU limit syntax")
		}

		// Validate first character
		if strings.HasPrefix(value, "-") || strings.HasPrefix(value, ",") {
			return fmt.Errorf("CPU limit can't start with a separator")
		}

		// Validate last character
		if strings.HasSuffix(value, "-") || strings.HasSuffix(value, ",") {
			return fmt.Errorf("CPU limit can't end with a separator")
		}

		return nil
	},
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
	"limits.cpu.priority": validate.Optional(validate.IsPriority),

	"limits.disk.priority": validate.Optional(validate.IsPriority),

	"limits.hugepages.64KB": validate.Optional(validate.IsSize),
	"limits.hugepages.1MB":  validate.Optional(validate.IsSize),
	"limits.hugepages.2MB":  validate.Optional(validate.IsSize),
	"limits.hugepages.1GB":  validate.Optional(validate.IsSize),

	"limits.memory": func(value string) error {
		if value == "" {
			return nil
		}

		if strings.HasSuffix(value, "%") {
			_, err := strconv.ParseInt(strings.TrimSuffix(value, "%"), 10, 64)
			if err != nil {
				return err
			}

			return nil
		}

		_, err := units.ParseByteSizeString(value)
		if err != nil {
			return err
		}

		return nil
	},
	"limits.memory.enforce": func(value string) error {
		return validate.IsOneOf(value, []string{"soft", "hard"})
	},
	"limits.memory.swap":          validate.Optional(validate.IsBool),
	"limits.memory.swap.priority": validate.Optional(validate.IsPriority),
	"limits.memory.hugepages":     validate.Optional(validate.IsBool),

	"limits.network.priority": validate.Optional(validate.IsPriority),

	"limits.processes": validate.Optional(validate.IsInt64),

	"linux.kernel_modules": validate.IsAny,

	"migration.incremental.memory":            validate.Optional(validate.IsBool),
	"migration.incremental.memory.iterations": validate.Optional(validate.IsUint32),
	"migration.incremental.memory.goal":       validate.Optional(validate.IsUint32),

	"nvidia.runtime":             validate.Optional(validate.IsBool),
	"nvidia.driver.capabilities": validate.IsAny,
	"nvidia.require.cuda":        validate.IsAny,
	"nvidia.require.driver":      validate.IsAny,

	"security.nesting":       validate.Optional(validate.IsBool),
	"security.privileged":    validate.Optional(validate.IsBool),
	"security.devlxd":        validate.Optional(validate.IsBool),
	"security.devlxd.images": validate.Optional(validate.IsBool),

	"security.protection.delete": validate.Optional(validate.IsBool),
	"security.protection.shift":  validate.Optional(validate.IsBool),

	"security.idmap.base":     validate.Optional(validate.IsUint32),
	"security.idmap.isolated": validate.Optional(validate.IsBool),
	"security.idmap.size":     validate.Optional(validate.IsUint32),

	"security.secureboot": validate.Optional(validate.IsBool),

	"security.syscalls.allow":                   validate.IsAny,
	"security.syscalls.blacklist_default":       validate.Optional(validate.IsBool),
	"security.syscalls.blacklist_compat":        validate.Optional(validate.IsBool),
	"security.syscalls.blacklist":               validate.IsAny,
	"security.syscalls.deny_default":            validate.Optional(validate.IsBool),
	"security.syscalls.deny_compat":             validate.Optional(validate.IsBool),
	"security.syscalls.deny":                    validate.IsAny,
	"security.syscalls.intercept.bpf":           validate.Optional(validate.IsBool),
	"security.syscalls.intercept.bpf.devices":   validate.Optional(validate.IsBool),
	"security.syscalls.intercept.mknod":         validate.Optional(validate.IsBool),
	"security.syscalls.intercept.mount":         validate.Optional(validate.IsBool),
	"security.syscalls.intercept.mount.allowed": validate.IsAny,
	"security.syscalls.intercept.mount.fuse":    validate.IsAny,
	"security.syscalls.intercept.mount.shift":   validate.Optional(validate.IsBool),
	"security.syscalls.intercept.setxattr":      validate.Optional(validate.IsBool),
	"security.syscalls.whitelist":               validate.IsAny,

	"snapshots.schedule": func(value string) error {
		if value == "" {
			return nil
		}

		if len(strings.Split(value, " ")) != 5 {
			return fmt.Errorf("Schedule must be of the form: <minute> <hour> <day-of-month> <month> <day-of-week>")
		}

		_, err := cron.Parse(fmt.Sprintf("* %s", value))
		if err != nil {
			return errors.Wrap(err, "Error parsing schedule")
		}

		return nil
	},
	"snapshots.schedule.stopped": validate.Optional(validate.IsBool),
	"snapshots.pattern":          validate.IsAny,
	"snapshots.expiry": func(value string) error {
		// Validate expression
		_, err := GetSnapshotExpiry(time.Time{}, value)
		return err
	},

	// Caller is responsible for full validation of any raw.* value
	"raw.apparmor": validate.IsAny,
	"raw.idmap":    validate.IsAny,
	"raw.lxc":      validate.IsAny,
	"raw.qemu":     validate.IsAny,
	"raw.seccomp":  validate.IsAny,

	"volatile.apply_template":   validate.IsAny,
	"volatile.base_image":       validate.IsAny,
	"volatile.last_state.idmap": validate.IsAny,
	"volatile.last_state.power": validate.IsAny,
	"volatile.idmap.base":       validate.IsAny,
	"volatile.idmap.current":    validate.IsAny,
	"volatile.idmap.next":       validate.IsAny,
	"volatile.apply_quota":      validate.IsAny,
}

// ConfigKeyChecker returns a function that will check whether or not
// a provide value is valid for the associate config key.  Returns an
// error if the key is not known.  The checker function only performs
// syntactic checking of the value, semantic and usage checking must
// be done by the caller.  User defined keys are always considered to
// be valid, e.g. user.* and environment.* keys.
func ConfigKeyChecker(key string) (func(value string) error, error) {
	if f, ok := KnownInstanceConfigKeys[key]; ok {
		return f, nil
	}

	if strings.HasPrefix(key, "volatile.") {
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

		if strings.HasSuffix(key, ".apply_quota") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, "vm.uuid") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".ceph_rbd") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".driver") {
			return validate.IsAny, nil
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

	return nil, fmt.Errorf("Unknown configuration key: %s", key)
}

// InstanceGetParentAndSnapshotName returns the parent instance name, snapshot name,
// and whether it actually was a snapshot name.
func InstanceGetParentAndSnapshotName(name string) (string, string, bool) {
	fields := strings.SplitN(name, SnapshotDelimiter, 2)
	if len(fields) == 1 {
		return name, "", false
	}

	return fields[0], fields[1], true
}
