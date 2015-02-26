package shared

// This package is intended for rendering ContainerState for use between lxc
// and lxd.

import (
	"gopkg.in/lxc/go-lxc.v2"
)

type ContainerStatus struct {
	State     string    `json:"status"`
	StateCode lxc.State `json:"status_code"`
}

func NewStatus(state lxc.State) ContainerStatus {
	return ContainerStatus{state.String(), state}
}

type ContainerState struct {
	Name     string            `json:"name"`
	Profiles []string          `json:"profiles"`
	Config   map[string]string `json:"config"`
	Userdata []byte            `json:"userdata"`
	Status   ContainerStatus   `json:"status"`
}

func (c *ContainerState) State() lxc.State {
	return lxc.StateMap[c.Status.State]
}

type ContainerAction string

const (
	Stop     ContainerAction = "stop"
	Start    ContainerAction = "start"
	Restart  ContainerAction = "restart"
	Freeze   ContainerAction = "freeze"
	Unfreeze ContainerAction = "unfreeze"
)
