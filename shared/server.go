package shared

type ServerStateEnvironment struct {
	Addresses              []string `json:"addresses"`
	Architectures          []string `json:"architectures"`
	Certificate            string   `json:"certificate"`
	CertificateFingerprint string   `json:"certificate_fingerprint"`
	Driver                 string   `json:"driver"`
	DriverVersion          string   `json:"driver_version"`
	Kernel                 string   `json:"kernel"`
	KernelArchitecture     string   `json:"kernel_architecture"`
	KernelVersion          string   `json:"kernel_version"`
	Server                 string   `json:"server"`
	ServerPid              int      `json:"server_pid"`
	ServerVersion          string   `json:"server_version"`
	Storage                string   `json:"storage"`
	StorageVersion         string   `json:"storage_version"`
}

type ServerState struct {
	APIExtensions []string               `json:"api_extensions"`
	APIStatus     string                 `json:"api_status"`
	APIVersion    string                 `json:"api_version"`
	Auth          string                 `json:"auth"`
	Environment   ServerStateEnvironment `json:"environment"`
	Config        map[string]interface{} `json:"config"`
	Public        bool                   `json:"public"`
}

type BriefServerState struct {
	Config map[string]interface{} `json:"config"`
}

func (c *ServerState) Brief() BriefServerState {
	retstate := BriefServerState{Config: c.Config}
	return retstate
}
