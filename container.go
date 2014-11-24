package lxd

import (
	"gopkg.in/lxc/go-lxc.v2"
)

type ContainerStatus struct {
	State     string    `json:"state"`
	StateCode lxc.State `json:"state_code"`
}

func NewStatus(state lxc.State) ContainerStatus {
	return ContainerStatus{state.String(), state}
}

type Container struct {
	Name     string          `json:"name"`
	Profiles []string        `json:"profiles"`
	Config   []Jmap          `json:"config"`
	Userdata []byte          `json:"config"`
	Status   ContainerStatus `json:"status"`
}

func CtoD(c *lxc.Container) Container {
	d := Container{}

	d.Name = c.Name()
	d.Status = NewStatus(c.State())
	return d
}

type ContainerAction string

const (
	Stop     ContainerAction = "stop"
	Start    ContainerAction = "start"
	Restart  ContainerAction = "restart"
	Freeze   ContainerAction = "freeze"
	Unfreeze ContainerAction = "unfreeze"
)
