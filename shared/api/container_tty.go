package api

// ContainerTTYControl represents a message on the container tty "control" socket
type ContainerTTYControl struct {
	Command string            `json:"command" yaml:"command"`
	Args    map[string]string `json:"args" yaml:"args"`
	Signal  int               `json:"signal" yaml:"signal"`
}
