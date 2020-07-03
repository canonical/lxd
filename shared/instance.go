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
)

type InstanceAction string

const (
	Stop     InstanceAction = "stop"
	Start    InstanceAction = "start"
	Restart  InstanceAction = "restart"
	Freeze   InstanceAction = "freeze"
	Unfreeze InstanceAction = "unfreeze"
)

func IsInt64(value string) error {
	if value == "" {
		return nil
	}

	_, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("Invalid value for an integer: %s", value)
	}

	return nil
}

func IsUint8(value string) error {
	if value == "" {
		return nil
	}

	_, err := strconv.ParseUint(value, 10, 8)
	if err != nil {
		return fmt.Errorf("Invalid value for an integer: %s. Must be between 0 and 255", value)
	}

	return nil
}

func IsUint32(value string) error {
	if value == "" {
		return nil
	}

	_, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return fmt.Errorf("Invalid value for uint32: %s: %v", value, err)
	}

	return nil
}

func IsPriority(value string) error {
	if value == "" {
		return nil
	}

	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("Invalid value for an integer: %s", value)
	}

	if valueInt < 0 || valueInt > 10 {
		return fmt.Errorf("Invalid value for a limit '%s'. Must be between 0 and 10", value)
	}

	return nil
}

func IsBool(value string) error {
	if value == "" {
		return nil
	}

	if !StringInSlice(strings.ToLower(value), []string{"true", "false", "yes", "no", "1", "0", "on", "off"}) {
		return fmt.Errorf("Invalid value for a boolean: %s", value)
	}

	return nil
}

func IsOneOf(value string, valid []string) error {
	if value == "" {
		return nil
	}

	if !StringInSlice(value, valid) {
		return fmt.Errorf("Invalid value: %s (not one of %s)", value, valid)
	}

	return nil
}

func IsAny(value string) error {
	return nil
}

func IsNotEmpty(value string) error {
	if value == "" {
		return fmt.Errorf("Required value")
	}

	return nil
}

// IsSize checks if string is valid size according to units.ParseByteSizeString.
func IsSize(value string) error {
	if value == "" {
		return nil
	}

	_, err := units.ParseByteSizeString(value)
	if err != nil {
		return err
	}

	return nil
}

// IsDeviceID validates string is four lowercase hex characters suitable as Vendor or Device ID.
func IsDeviceID(value string) error {
	if value == "" {
		return nil
	}

	regexHexLc, err := regexp.Compile("^[0-9a-f]+$")
	if err != nil {
		return err
	}

	if len(value) != 4 || !regexHexLc.MatchString(value) {
		return fmt.Errorf("Invalid value, must be four lower case hex characters")
	}

	return nil
}

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
	"boot.autostart":             IsBool,
	"boot.autostart.delay":       IsInt64,
	"boot.autostart.priority":    IsInt64,
	"boot.stop.priority":         IsInt64,
	"boot.host_shutdown_timeout": IsInt64,

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
	"limits.cpu.priority": IsPriority,

	"limits.disk.priority": IsPriority,

	"limits.hugepages.64KB": IsSize,
	"limits.hugepages.1MB":  IsSize,
	"limits.hugepages.2MB":  IsSize,
	"limits.hugepages.1GB":  IsSize,

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
		return IsOneOf(value, []string{"soft", "hard"})
	},
	"limits.memory.swap":          IsBool,
	"limits.memory.swap.priority": IsPriority,
	"limits.memory.hugepages":     IsBool,

	"limits.network.priority": IsPriority,

	"limits.processes": IsInt64,

	"linux.kernel_modules": IsAny,

	"migration.incremental.memory":            IsBool,
	"migration.incremental.memory.iterations": IsUint32,
	"migration.incremental.memory.goal":       IsUint32,

	"nvidia.runtime":             IsBool,
	"nvidia.driver.capabilities": IsAny,
	"nvidia.require.cuda":        IsAny,
	"nvidia.require.driver":      IsAny,

	"security.nesting":       IsBool,
	"security.privileged":    IsBool,
	"security.devlxd":        IsBool,
	"security.devlxd.images": IsBool,

	"security.protection.delete": IsBool,
	"security.protection.shift":  IsBool,

	"security.idmap.base":     IsUint32,
	"security.idmap.isolated": IsBool,
	"security.idmap.size":     IsUint32,

	"security.secureboot": IsBool,

	"security.syscalls.allow":                   IsAny,
	"security.syscalls.blacklist_default":       IsBool,
	"security.syscalls.blacklist_compat":        IsBool,
	"security.syscalls.blacklist":               IsAny,
	"security.syscalls.deny_default":            IsBool,
	"security.syscalls.deny_compat":             IsBool,
	"security.syscalls.deny":                    IsAny,
	"security.syscalls.intercept.mknod":         IsBool,
	"security.syscalls.intercept.mount":         IsBool,
	"security.syscalls.intercept.mount.allowed": IsAny,
	"security.syscalls.intercept.mount.fuse":    IsAny,
	"security.syscalls.intercept.mount.shift":   IsBool,
	"security.syscalls.intercept.setxattr":      IsBool,
	"security.syscalls.whitelist":               IsAny,

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
	"snapshots.schedule.stopped": IsBool,
	"snapshots.pattern":          IsAny,
	"snapshots.expiry": func(value string) error {
		// Validate expression
		_, err := GetSnapshotExpiry(time.Time{}, value)
		return err
	},

	// Caller is responsible for full validation of any raw.* value
	"raw.apparmor": IsAny,
	"raw.idmap":    IsAny,
	"raw.lxc":      IsAny,
	"raw.qemu":     IsAny,
	"raw.seccomp":  IsAny,

	"volatile.apply_template":   IsAny,
	"volatile.base_image":       IsAny,
	"volatile.last_state.idmap": IsAny,
	"volatile.last_state.power": IsAny,
	"volatile.idmap.base":       IsAny,
	"volatile.idmap.current":    IsAny,
	"volatile.idmap.next":       IsAny,
	"volatile.apply_quota":      IsAny,
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
			return IsAny, nil
		}

		if strings.HasSuffix(key, ".name") {
			return IsAny, nil
		}

		if strings.HasSuffix(key, ".host_name") {
			return IsAny, nil
		}

		if strings.HasSuffix(key, ".mtu") {
			return IsAny, nil
		}

		if strings.HasSuffix(key, ".created") {
			return IsAny, nil
		}

		if strings.HasSuffix(key, ".id") {
			return IsAny, nil
		}

		if strings.HasSuffix(key, ".vlan") {
			return IsAny, nil
		}

		if strings.HasSuffix(key, ".spoofcheck") {
			return IsAny, nil
		}

		if strings.HasSuffix(key, ".apply_quota") {
			return IsAny, nil
		}

		if strings.HasSuffix(key, "vm.uuid") {
			return IsAny, nil
		}

		if strings.HasSuffix(key, ".ceph_rbd") {
			return IsAny, nil
		}

		if strings.HasSuffix(key, ".driver") {
			return IsAny, nil
		}
	}

	if strings.HasPrefix(key, "environment.") {
		return IsAny, nil
	}

	if strings.HasPrefix(key, "user.") {
		return IsAny, nil
	}

	if strings.HasPrefix(key, "image.") {
		return IsAny, nil
	}

	if strings.HasPrefix(key, "limits.kernel.") &&
		(len(key) > len("limits.kernel.")) {
		return IsAny, nil
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
