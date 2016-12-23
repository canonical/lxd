package api

// ServerEnvironment represents the read-only environment fields of a LXD server
type ServerEnvironment struct {
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

// ServerPut represents the modifiable fields of a LXD server configuration
type ServerPut struct {
	Config map[string]interface{} `json:"config"`
}

// ServerUntrusted represents a LXD server for an untrusted client
type ServerUntrusted struct {
	APIExtensions []string `json:"api_extensions"`
	APIStatus     string   `json:"api_status"`
	APIVersion    string   `json:"api_version"`
	Auth          string   `json:"auth"`
	Public        bool     `json:"public"`
}

// Server represents a LXD server
type Server struct {
	ServerPut       `yaml:",inline"`
	ServerUntrusted `yaml:",inline"`

	Environment ServerEnvironment `json:"environment"`
}

// Writable converts a full Server struct into a ServerPut struct (filters read-only fields)
func (srv *Server) Writable() ServerPut {
	return srv.ServerPut
}
