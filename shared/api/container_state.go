package api

// ContainerStatePut represents the modifiable fields of a LXD container's state
type ContainerStatePut struct {
	Action   string `json:"action"`
	Timeout  int    `json:"timeout"`
	Force    bool   `json:"force"`
	Stateful bool   `json:"stateful"`
}

// ContainerState represents a LXD container's state
type ContainerState struct {
	Status     string                           `json:"status"`
	StatusCode StatusCode                       `json:"status_code"`
	Disk       map[string]ContainerStateDisk    `json:"disk"`
	Memory     ContainerStateMemory             `json:"memory"`
	Network    map[string]ContainerStateNetwork `json:"network"`
	Pid        int64                            `json:"pid"`
	Processes  int64                            `json:"processes"`

	// API extension: container_cpu_time
	CPU ContainerStateCPU `json:"cpu"`
}

// ContainerStateDisk represents the disk information section of a LXD container's state
type ContainerStateDisk struct {
	Usage int64 `json:"usage"`
}

// ContainerStateCPU represents the cpu information section of a LXD container's state
//
// API extension: container_cpu_time
type ContainerStateCPU struct {
	Usage int64 `json:"usage"`
}

// ContainerStateMemory represents the memory information section of a LXD container's state
type ContainerStateMemory struct {
	Usage         int64 `json:"usage"`
	UsagePeak     int64 `json:"usage_peak"`
	SwapUsage     int64 `json:"swap_usage"`
	SwapUsagePeak int64 `json:"swap_usage_peak"`
}

// ContainerStateNetwork represents the network information section of a LXD container's state
type ContainerStateNetwork struct {
	Addresses []ContainerStateNetworkAddress `json:"addresses"`
	Counters  ContainerStateNetworkCounters  `json:"counters"`
	Hwaddr    string                         `json:"hwaddr"`
	HostName  string                         `json:"host_name"`
	Mtu       int                            `json:"mtu"`
	State     string                         `json:"state"`
	Type      string                         `json:"type"`
}

// ContainerStateNetworkAddress represents a network address as part of the network section of a LXD container's state
type ContainerStateNetworkAddress struct {
	Family  string `json:"family"`
	Address string `json:"address"`
	Netmask string `json:"netmask"`
	Scope   string `json:"scope"`
}

// ContainerStateNetworkCounters represents packet counters as part of the network section of a LXD container's state
type ContainerStateNetworkCounters struct {
	BytesReceived   int64 `json:"bytes_received"`
	BytesSent       int64 `json:"bytes_sent"`
	PacketsReceived int64 `json:"packets_received"`
	PacketsSent     int64 `json:"packets_sent"`
}
