package shared

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type ContainerState struct {
	Status     string                           `json:"status"`
	StatusCode StatusCode                       `json:"status_code"`
	Disk       map[string]ContainerStateDisk    `json:"disk"`
	Memory     ContainerStateMemory             `json:"memory"`
	Network    map[string]ContainerStateNetwork `json:"network"`
	Pid        int64                            `json:"pid"`
	Processes  int64                            `json:"processes"`
}

type ContainerStateDisk struct {
	Usage int64 `json:"usage"`
}

type ContainerStateMemory struct {
	Usage         int64 `json:"usage"`
	UsagePeak     int64 `json:"usage_peak"`
	SwapUsage     int64 `json:"swap_usage"`
	SwapUsagePeak int64 `json:"swap_usage_peak"`
}

type ContainerStateNetwork struct {
	Addresses []ContainerStateNetworkAddress `json:"addresses"`
	Counters  ContainerStateNetworkCounters  `json:"counters"`
	Hwaddr    string                         `json:"hwaddr"`
	HostName  string                         `json:"host_name"`
	Mtu       int                            `json:"mtu"`
	State     string                         `json:"state"`
	Type      string                         `json:"type"`
}

type ContainerStateNetworkAddress struct {
	Family  string `json:"family"`
	Address string `json:"address"`
	Netmask string `json:"netmask"`
	Scope   string `json:"scope"`
}

type ContainerStateNetworkCounters struct {
	BytesReceived   int64 `json:"bytes_received"`
	BytesSent       int64 `json:"bytes_sent"`
	PacketsReceived int64 `json:"packets_received"`
	PacketsSent     int64 `json:"packets_sent"`
}

type ContainerExecControl struct {
	Command string            `json:"command"`
	Args    map[string]string `json:"args"`
}

type SnapshotInfo struct {
	Architecture    string            `json:"architecture"`
	Config          map[string]string `json:"config"`
	CreationDate    time.Time         `json:"created_at"`
	Devices         Devices           `json:"devices"`
	Ephemeral       bool              `json:"ephemeral"`
	ExpandedConfig  map[string]string `json:"expanded_config"`
	ExpandedDevices Devices           `json:"expanded_devices"`
	LastUsedDate    time.Time         `json:"last_used_at"`
	Name            string            `json:"name"`
	Profiles        []string          `json:"profiles"`
	Stateful        bool              `json:"stateful"`
}

type ContainerInfo struct {
	Architecture    string            `json:"architecture"`
	Config          map[string]string `json:"config"`
	CreationDate    time.Time         `json:"created_at"`
	Devices         Devices           `json:"devices"`
	Ephemeral       bool              `json:"ephemeral"`
	ExpandedConfig  map[string]string `json:"expanded_config"`
	ExpandedDevices Devices           `json:"expanded_devices"`
	LastUsedDate    time.Time         `json:"last_used_at"`
	Name            string            `json:"name"`
	Profiles        []string          `json:"profiles"`
	Stateful        bool              `json:"stateful"`
	Status          string            `json:"status"`
	StatusCode      StatusCode        `json:"status_code"`
}

func (c ContainerInfo) IsActive() bool {
	switch c.StatusCode {
	case Stopped:
		return false
	case Error:
		return false
	default:
		return true
	}
}

/*
 * BriefContainerState contains a subset of the fields in
 * ContainerState, namely those which a user may update
 */
type BriefContainerInfo struct {
	Name      string            `json:"name"`
	Profiles  []string          `json:"profiles"`
	Config    map[string]string `json:"config"`
	Devices   Devices           `json:"devices"`
	Ephemeral bool              `json:"ephemeral"`
}

func (c *ContainerInfo) Brief() BriefContainerInfo {
	retstate := BriefContainerInfo{Name: c.Name,
		Profiles:  c.Profiles,
		Config:    c.Config,
		Devices:   c.Devices,
		Ephemeral: c.Ephemeral}
	return retstate
}

func (c *ContainerInfo) BriefExpanded() BriefContainerInfo {
	retstate := BriefContainerInfo{Name: c.Name,
		Profiles:  c.Profiles,
		Config:    c.ExpandedConfig,
		Devices:   c.ExpandedDevices,
		Ephemeral: c.Ephemeral}
	return retstate
}

type ContainerAction string

const (
	Stop     ContainerAction = "stop"
	Start    ContainerAction = "start"
	Restart  ContainerAction = "restart"
	Freeze   ContainerAction = "freeze"
	Unfreeze ContainerAction = "unfreeze"
)

type ProfileConfig struct {
	Name        string            `json:"name"`
	Config      map[string]string `json:"config"`
	Description string            `json:"description"`
	Devices     Devices           `json:"devices"`
}

func isInt64(value string) error {
	if value == "" {
		return nil
	}

	_, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("Invalid value for an integer: %s", value)
	}

	return nil
}

func isBool(value string) error {
	if value == "" {
		return nil
	}

	if !StringInSlice(strings.ToLower(value), []string{"true", "false", "yes", "no", "1", "0", "on", "off"}) {
		return fmt.Errorf("Invalid value for a boolean: %s", value)
	}

	return nil
}

func isOneOf(value string, valid []string) error {
	if value == "" {
		return nil
	}

	if !StringInSlice(value, valid) {
		return fmt.Errorf("Invalid value: %s (not one of %s)", value, valid)
	}

	return nil
}

func isAny(value string) error {
	return nil
}

// KnownContainerConfigKeys maps all fully defined, well-known config keys
// to an appropriate checker function, which validates whether or not a
// given value is syntactically legal.
var KnownContainerConfigKeys = map[string]func(value string) error{
	"boot.autostart":             isBool,
	"boot.autostart.delay":       isInt64,
	"boot.autostart.priority":    isInt64,
	"boot.host_shutdown_timeout": isInt64,

	"limits.cpu":           isAny,
	"limits.disk.priority": isInt64,
	"limits.memory":        isAny,
	"limits.memory.enforce": func(value string) error {
		return isOneOf(value, []string{"soft", "hard"})
	},
	"limits.memory.swap":          isBool,
	"limits.memory.swap.priority": isInt64,
	"limits.network.priority":     isInt64,
	"limits.processes":            isInt64,

	"linux.kernel_modules": isAny,

	"security.privileged":                 isBool,
	"security.nesting":                    isBool,
	"security.syscalls.blacklist_default": isBool,
	"security.syscalls.blacklist_compat":  isBool,
	"security.syscalls.blacklist":         isAny,
	"security.syscalls.whitelist":         isAny,

	// Caller is responsible for full validation of any raw.* value
	"raw.apparmor": isAny,
	"raw.lxc":      isAny,
	"raw.seccomp":  isAny,

	"volatile.apply_template":   isAny,
	"volatile.base_image":       isAny,
	"volatile.last_state.idmap": isAny,
	"volatile.last_state.power": isAny,
}

// ConfigKeyChecker returns a function that will check whether or not
// a provide value is valid for the associate config key.  Returns an
// error if the key is not known.  The checker function only performs
// syntactic checking of the value, semantic and usage checking must
// be done by the caller.  User defined keys are always considered to
// be valid, e.g. user.* and environment.* keys.
func ConfigKeyChecker(key string) (func(value string) error, error) {
	if f, ok := KnownContainerConfigKeys[key]; ok {
		return f, nil
	}

	if strings.HasPrefix(key, "volatile.") {
		if strings.HasSuffix(key, ".hwaddr") {
			return isAny, nil
		}

		if strings.HasSuffix(key, ".name") {
			return isAny, nil
		}
	}

	if strings.HasPrefix(key, "environment.") {
		return isAny, nil
	}

	if strings.HasPrefix(key, "user.") {
		return isAny, nil
	}

	return nil, fmt.Errorf("Bad key: %s", key)
}
