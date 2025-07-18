package api

// AgentDeviceRemove represents the fields of an device removal request that needs to occur inside the VM agent.
//
// swagger:model
//
// API extension: disk_state_created.
type AgentDeviceRemove struct {
	// Type of device ('disk', 'nic', etc.)
	Type string `json:"type" yaml:"type"`
	// Device configuration map
	Config map[string]string `json:"config" yaml:"config"`
	// Device name
	Name string `json:"name" yaml:"name"`
	// Optional device volatile configuration keys
	Volatile map[string]string `json:"volatile" yaml:"volatile"`
}
