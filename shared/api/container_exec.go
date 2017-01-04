package api

// ContainerExecControl represents a message on the container exec "control" socket
type ContainerExecControl struct {
	Command string            `json:"command"`
	Args    map[string]string `json:"args"`
	Signal  int               `json:"signal"`
}

// ContainerExecPost represents a LXD container exec request
type ContainerExecPost struct {
	Command     []string          `json:"command"`
	WaitForWS   bool              `json:"wait-for-websocket"`
	Interactive bool              `json:"interactive"`
	Environment map[string]string `json:"environment"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`

	// API extension: container_exec_recording
	RecordOutput bool `json:"record-output"`
}
