package api

// InstanceExecControl represents a message on the instance exec "control" socket.
//
// API extension: instances
type InstanceExecControl struct {
	Command string            `json:"command" yaml:"command"`
	Args    map[string]string `json:"args" yaml:"args"`
	Signal  int               `json:"signal" yaml:"signal"`
}

// ContainerExecControl represents a message on the container exec "control" socket.
type ContainerExecControl InstanceExecControl

// InstanceExecPost represents a LXD instance exec request.
//
// API extension: instances
type InstanceExecPost struct {
	Command     []string          `json:"command" yaml:"command"`
	WaitForWS   bool              `json:"wait-for-websocket" yaml:"wait-for-websocket"`
	Interactive bool              `json:"interactive" yaml:"interactive"`
	Environment map[string]string `json:"environment" yaml:"environment"`
	Width       int               `json:"width" yaml:"width"`
	Height      int               `json:"height" yaml:"height"`

	// API extension: container_exec_recording
	RecordOutput bool `json:"record-output" yaml:"record-output"`

	// API extension: container_user_group_cwd
	User  uint32 `json:"user" yaml:"user"`
	Group uint32 `json:"group" yaml:"group"`
	Cwd   string `json:"cwd" yaml:"cwd"`
}

// ContainerExecPost represents a LXD container exec request.
type ContainerExecPost InstanceExecPost
