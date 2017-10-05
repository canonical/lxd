package api

// Resources represents the system resources avaible for LXD
// API extension: resources
type Resources struct {
	CPU    ResourcesCPU    `json:"cpu,omitempty" yaml:"cpu,omitempty"`
	Memory ResourcesMemory `json:"memory,omitempty" yaml:"memory,omitempty"`
}

// ResourcesCPUSocket represents a cpu socket on the system
// API extension: resources
type ResourcesCPUSocket struct {
	Cores          uint64 `json:"cores" yaml:"cores"`
	Frequency      uint64 `json:"frequency,omitempty" yaml:"frequency,omitempty"`
	FrequencyTurbo uint64 `json:"frequency_turbo,omitempty" yaml:"frequency_turbo,omitempty"`
	Name           string `json:"name,omitempty" yaml:"name,omitempty"`
	Vendor         string `json:"vendor,omitempty" yaml:"vendor,omitempty"`
	Threads        uint64 `json:"threads" yaml:"threads"`
}

// ResourcesCPU represents the cpu resources available on the system
// API extension: resources
type ResourcesCPU struct {
	Sockets []ResourcesCPUSocket `json:"sockets" yaml:"sockets"`
	Total   uint64               `json:"total" yaml:"total"`
}

// ResourcesMemory represents the memory resources available on the system
// API extension: resources
type ResourcesMemory struct {
	Used  uint64 `json:"used" yaml:"used"`
	Total uint64 `json:"total" yaml:"total"`
}
