package api

// ContainerExecPost represents a LXD container exec request
type ContainerExecPost struct {
	Command     []string          `json:"command" yaml:"command"`
	WaitForWS   bool              `json:"wait-for-websocket" yaml:"wait-for-websocket"`
	Interactive bool              `json:"interactive" yaml:"interactive"`
	Environment map[string]string `json:"environment" yaml:"environment"`
	Width       int               `json:"width" yaml:"width"`
	Height      int               `json:"height" yaml:"height"`

	// API extension: container_exec_recording
	RecordOutput bool `json:"record-output" yaml:"record-output"`
}
