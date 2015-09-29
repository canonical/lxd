package shared

type ServerStateEnvironment struct {
	Addresses          []string `json:"addresses"`
	Architectures      []int    `json:"architectures"`
	Driver             string   `json:"driver"`
	DriverVersion      string   `json:"driver_version"`
	Kernel             string   `json:"kernel"`
	KernelArchitecture string   `json:"kernel_architecture"`
	KernelVersion      string   `json:"kernel_version"`
	Server             string   `json:"server"`
	ServerPid          int      `json:"server_pid"`
	ServerVersion      string   `json:"server_version"`
	Storage            string   `json:"storage"`
	StorageVersion     string   `json:"storage_version"`
}

type ServerState struct {
	APICompat   int                    `json:"api_compat"`
	Auth        string                 `json:"auth"`
	Environment ServerStateEnvironment `json:"environment"`
	Config      map[string]interface{} `json:"config"`
	Public      bool                   `json:"public"`
}

type BriefServerState struct {
	Config map[string]interface{} `json:"config"`
}

func (c *ServerState) BriefState() BriefServerState {
	retstate := BriefServerState{Config: c.Config}
	return retstate
}
