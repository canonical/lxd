package api

// ServerState represents the current server state.
//
// swagger:model
//
// API extension: server_state.
type ServerState struct {
	// Server uptime in seconds.
	// Example: 12345
	Uptime int64 `json:"uptime" yaml:"uptime"`

	// Load averages for the last 1, 5, and 15 minutes.
	// Example: [0.12, 0.34, 0.56]
	LoadAverages []float64 `json:"load_averages" yaml:"load_averages"`

	// Total system memory in bytes.
	// Example: 687194767360
	TotalRAM uint64 `json:"total_ram" yaml:"total_ram"`

	// Free system memory in bytes.
	// Example: 129744588800
	FreeRAM uint64 `json:"free_ram" yaml:"free_ram"`

	// Shared system memory in bytes.
	// Example: 1048576
	SharedRAM uint64 `json:"shared_ram" yaml:"shared_ram"`

	// Buffered system memory in bytes.
	// Example: 2147483648
	BufferRAM uint64 `json:"buffer_ram" yaml:"buffer_ram"`

	// Total swap memory in bytes.
	// Example: 2147479552
	TotalSwap uint64 `json:"total_swap" yaml:"total_swap"`

	// Free swap memory in bytes.
	// Example: 2147479552
	FreeSwap uint64 `json:"free_swap" yaml:"free_swap"`

	// Number of processes.
	// Example: 1234
	Processes uint64 `json:"processes" yaml:"processes"`

	// Total number of CPU threads.
	// Example: 8
	LogicalCPUs uint64 `json:"logical_cpus" yaml:"logical_cpus"`
}
