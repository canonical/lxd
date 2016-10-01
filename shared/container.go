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
	CPU        ContainerStateCPU                `json:"cpu"`
	Disk       map[string]ContainerStateDisk    `json:"disk"`
	Memory     ContainerStateMemory             `json:"memory"`
	Network    map[string]ContainerStateNetwork `json:"network"`
	Pid        int64                            `json:"pid"`
	Processes  int64                            `json:"processes"`
}

type ContainerStateDisk struct {
	Usage int64 `json:"usage"`
}

type ContainerStateCPU struct {
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
	UsedBy      []string          `json:"used_by"`
}

type NetworkConfig struct {
	Name    string            `json:"name"`
	Config  map[string]string `json:"config"`
	Managed bool              `json:"managed"`
	Type    string            `json:"type"`
	UsedBy  []string          `json:"used_by"`
}

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

func IsPriority(value string) error {
	if value == "" {
		return nil
	}

	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("Invalid value for an integer: %s", value)
	}

	if valueInt < 0 || valueInt > 10 {
		return fmt.Errorf("Invalid value for a limit '%s'. Must be between 0 and 10.", value)
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

// KnownContainerConfigKeys maps all fully defined, well-known config keys
// to an appropriate checker function, which validates whether or not a
// given value is syntactically legal.
var KnownContainerConfigKeys = map[string]func(value string) error{
	"boot.autostart":             IsBool,
	"boot.autostart.delay":       IsInt64,
	"boot.autostart.priority":    IsInt64,
	"boot.host_shutdown_timeout": IsInt64,

	"limits.cpu":           IsAny,
	"limits.cpu.allowance": IsAny,
	"limits.cpu.priority":  IsPriority,

	"limits.disk.priority": IsPriority,

	"limits.memory": IsAny,
	"limits.memory.enforce": func(value string) error {
		return IsOneOf(value, []string{"soft", "hard"})
	},
	"limits.memory.swap":          IsBool,
	"limits.memory.swap.priority": IsPriority,

	"limits.network.priority": IsPriority,

	"limits.processes": IsInt64,

	"linux.kernel_modules": IsAny,

	"security.nesting":    IsBool,
	"security.privileged": IsBool,

	"security.syscalls.blacklist_default": IsBool,
	"security.syscalls.blacklist_compat":  IsBool,
	"security.syscalls.blacklist":         IsAny,
	"security.syscalls.whitelist":         IsAny,

	// Caller is responsible for full validation of any raw.* value
	"raw.apparmor": IsAny,
	"raw.lxc":      IsAny,
	"raw.seccomp":  IsAny,

	"volatile.apply_template":   IsAny,
	"volatile.base_image":       IsAny,
	"volatile.last_state.idmap": IsAny,
	"volatile.last_state.power": IsAny,
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
			return IsAny, nil
		}

		if strings.HasSuffix(key, ".name") {
			return IsAny, nil
		}
	}

	if strings.HasPrefix(key, "environment.") {
		return IsAny, nil
	}

	if strings.HasPrefix(key, "user.") {
		return IsAny, nil
	}

	return nil, fmt.Errorf("Bad key: %s", key)
}
