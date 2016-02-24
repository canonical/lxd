package shared

import (
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
	CreationDate time.Time `json:"created_at"`
	Name         string    `json:"name"`
	Stateful     bool      `json:"stateful"`
}

type ContainerInfo struct {
	Architecture    string            `json:"architecture"`
	Config          map[string]string `json:"config"`
	CreationDate    time.Time         `json:"created_at"`
	Devices         Devices           `json:"devices"`
	Ephemeral       bool              `json:"ephemeral"`
	ExpandedConfig  map[string]string `json:"expanded_config"`
	ExpandedDevices Devices           `json:"expanded_devices"`
	Name            string            `json:"name"`
	Profiles        []string          `json:"profiles"`
	Status          string            `json:"status"`
	StatusCode      StatusCode        `json:"status_code"`
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
	Name    string            `json:"name"`
	Config  map[string]string `json:"config"`
	Devices Devices           `json:"devices"`
}
