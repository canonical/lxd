package shared

/*
 * N.B. State is copied from lxc.State, but we re-export it here so that
 * client libraries don't have to import go-lxc and thus link against liblxc
 * for just some constants.
 */

// State type specifies possible container states.
type State int

const (
	// STOPPED means container is not running
	STOPPED State = iota + 1
	// STARTING means container is starting
	STARTING
	// RUNNING means container is running
	RUNNING
	// STOPPING means container is stopping
	STOPPING
	// ABORTING means container is aborting
	ABORTING
	// FREEZING means container is freezing
	FREEZING
	// FROZEN means containe is frozen
	FROZEN
	// THAWED means container is thawed
	THAWED
)

var StateMap = map[string]State{
	"STOPPED":  STOPPED,
	"STARTING": STARTING,
	"RUNNING":  RUNNING,
	"STOPPING": STOPPING,
	"ABORTING": ABORTING,
	"FREEZING": FREEZING,
	"FROZEN":   FROZEN,
	"THAWED":   THAWED,
}

type Ip struct {
	Interface string `json:"interface"`
	Protocol  string `json:"protocol"`
	Address   string `json:"address"`
}

type ContainerStatus struct {
	State     string `json:"status"`
	StateCode State  `json:"status_code"`
	Init      int    `json:"init"`
	Ips       []Ip   `json:"ips"`
}

type ContainerExecControl struct {
	Command string            `json:"command"`
	Args    map[string]string `json:"args"`
}

type Device map[string]string
type Devices map[string]Device

type ContainerState struct {
	Name            string            `json:"name"`
	Profiles        []string          `json:"profiles"`
	Config          map[string]string `json:"config"`
	ExpandedConfig  map[string]string `json:"expanded_config"`
	Userdata        []byte            `json:"userdata"`
	Status          ContainerStatus   `json:"status"`
	Devices         Devices           `json:"devices"`
	ExpandedDevices Devices           `json:"expanded_devices"`
	Ephemeral       bool              `json:"ephemeral"`
	Log             string            `json:"log"`
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

func (c *ContainerState) State() State {
	return StateMap[c.Status.State]
}

type ContainerInfo struct {
	State ContainerState `json:"state"`
	Snaps []string       `json:"snaps"`
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
