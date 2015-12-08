package shared

import (
	"strconv"
)

type Ip struct {
	Interface string `json:"interface"`
	Protocol  string `json:"protocol"`
	Address   string `json:"address"`
	HostVeth  string `json:"host_veth"`
}

type ContainerStatus struct {
	Status       string     `json:"status"`
	StatusCode   StatusCode `json:"status_code"`
	Init         int        `json:"init"`
	Processcount int        `json:"processcount"`
	Ips          []Ip       `json:"ips"`
}

type ContainerExecControl struct {
	Command string            `json:"command"`
	Args    map[string]string `json:"args"`
}

type ContainerState struct {
	Architecture    int               `json:"architecture"`
	Config          map[string]string `json:"config"`
	Devices         Devices           `json:"devices"`
	Ephemeral       bool              `json:"ephemeral"`
	ExpandedConfig  map[string]string `json:"expanded_config"`
	ExpandedDevices Devices           `json:"expanded_devices"`
	Name            string            `json:"name"`
	Profiles        []string          `json:"profiles"`
	Status          ContainerStatus   `json:"status"`
}

/*
 * BriefContainerState contains a subset of the fields in
 * ContainerState, namely those which a user may update
 */
type BriefContainerState struct {
	Name      string            `json:"name"`
	Profiles  []string          `json:"profiles"`
	Config    map[string]string `json:"config"`
	Devices   Devices           `json:"devices"`
	Ephemeral bool              `json:"ephemeral"`
}

func (c *ContainerState) BriefState() BriefContainerState {
	retstate := BriefContainerState{Name: c.Name,
		Profiles:  c.Profiles,
		Config:    c.Config,
		Devices:   c.Devices,
		Ephemeral: c.Ephemeral}
	return retstate
}

func (c *ContainerState) BriefStateExpanded() BriefContainerState {
	retstate := BriefContainerState{Name: c.Name,
		Profiles:  c.Profiles,
		Config:    c.ExpandedConfig,
		Devices:   c.ExpandedDevices,
		Ephemeral: c.Ephemeral}
	return retstate
}

type ContainerInfo struct {
	State ContainerState `json:"state"`
	Snaps []string       `json:"snaps"`
}

type ContainerInfoList []ContainerInfo

func (slice ContainerInfoList) Len() int {
	return len(slice)
}

func (slice ContainerInfoList) Less(i, j int) bool {
	iOrder := slice[i].State.ExpandedConfig["boot.autostart.priority"]
	jOrder := slice[j].State.ExpandedConfig["boot.autostart.priority"]

	if iOrder != jOrder {
		iOrderInt, _ := strconv.Atoi(iOrder)
		jOrderInt, _ := strconv.Atoi(jOrder)
		return iOrderInt > jOrderInt
	}

	return slice[i].State.Name < slice[j].State.Name
}

func (slice ContainerInfoList) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

type ContainerAction string

const (
	Stop     ContainerAction = "stop"
	Start    ContainerAction = "start"
	Restart  ContainerAction = "restart"
	Freeze   ContainerAction = "freeze"
	Unfreeze ContainerAction = "unfreeze"
)

type ProfileConfig struct {
	Name    string            `json:"name"`
	Config  map[string]string `json:"config"`
	Devices Devices           `json:"devices"`
}
