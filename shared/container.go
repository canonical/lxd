package shared

import ()

type Ip struct {
	Interface string `json:"interface"`
	Protocol  string `json:"protocol"`
	Address   string `json:"address"`
	HostVeth  string `json:"host_veth"`
}

type ContainerState struct {
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

type SnapshotInfo struct {
	CreationDate int64  `json:"creation_date"`
	Name         string `json:"name"`
	Stateful     bool   `json:"stateful"`
}

type ContainerInfo struct {
	Architecture    int               `json:"architecture"`
	Config          map[string]string `json:"config"`
	CreationDate    int64             `json:"creation_date"`
	Devices         Devices           `json:"devices"`
	Ephemeral       bool              `json:"ephemeral"`
	ExpandedConfig  map[string]string `json:"expanded_config"`
	ExpandedDevices Devices           `json:"expanded_devices"`
	Name            string            `json:"name"`
	Profiles        []string          `json:"profiles"`
	Status          string            `json:"status"`
	StatusCode      StatusCode        `json:"status_code"`
}

/*
 * BriefContainerState contains a subset of the fields in
 * ContainerState, namely those which a user may update
 */
type BriefContainerInfo struct {
	Name      string            `json:"name"`
	Profiles  []string          `json:"profiles"`
	Config    map[string]string `json:"config"`
	Devices   Devices           `json:"devices"`
	Ephemeral bool              `json:"ephemeral"`
}

func (c *ContainerInfo) Brief() BriefContainerInfo {
	retstate := BriefContainerInfo{Name: c.Name,
		Profiles:  c.Profiles,
		Config:    c.Config,
		Devices:   c.Devices,
		Ephemeral: c.Ephemeral}
	return retstate
}

func (c *ContainerInfo) BriefExpanded() BriefContainerInfo {
	retstate := BriefContainerInfo{Name: c.Name,
		Profiles:  c.Profiles,
		Config:    c.ExpandedConfig,
		Devices:   c.ExpandedDevices,
		Ephemeral: c.Ephemeral}
	return retstate
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
