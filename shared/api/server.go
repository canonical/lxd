package api

// ServerEnvironment represents the read-only environment fields of a LXD server
type ServerEnvironment struct {
	// Example: [":8443"]
	Addresses []string `json:"addresses" yaml:"addresses"`

	// Example: ["x86_64", "i686"]
	Architectures []string `json:"architectures" yaml:"architectures"`

	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate"`

	// Example: fd200419b271f1dc2a5591b693cc5774b7f234e1ff8c6b78ad703b6888fe2b69
	CertificateFingerprint string `json:"certificate_fingerprint" yaml:"certificate_fingerprint"`

	// Example: lxc | qemu
	Driver string `json:"driver" yaml:"driver"`

	// Example: 4.0.7 | 5.2.0
	DriverVersion string `json:"driver_version" yaml:"driver_version"`

	// Example: nftables
	//
	// API extension: firewall_driver
	Firewall string `json:"firewall" yaml:"firewall"`

	// Example: Linux
	Kernel string `json:"kernel" yaml:"kernel"`

	// Example: x86_64
	KernelArchitecture string `json:"kernel_architecture" yaml:"kernel_architecture"`

	// Example: {"netnsid_getifaddrs": "true", "seccomp_listener": "true"}
	//
	// API extension: kernel_features
	KernelFeatures map[string]string `json:"kernel_features" yaml:"kernel_features"`

	// Example: 5.4.0-36-generic
	KernelVersion string `json:"kernel_version" yaml:"kernel_version"`

	// Example: {"cgroup2": "true", "devpts_fd": "true", "pidfd": "true"}
	//
	// API extension: lxc_features
	LXCFeatures map[string]string `json:"lxc_features" yaml:"lxc_features"`

	// Example: Ubuntu
	//
	// API extension: api_os
	OSName string `json:"os_name" yaml:"os_name"`

	// Example: 20.04
	//
	// API extension: api_os
	OSVersion string `json:"os_version" yaml:"os_version"`

	// Example: default
	//
	// API extension: projects
	Project string `json:"project" yaml:"project"`

	// Example: lxd
	Server string `json:"server" yaml:"server"`

	// Example: false
	//
	// API extension: clustering
	ServerClustered bool `json:"server_clustered" yaml:"server_clustered"`

	// Example: castiana
	//
	// API extension: clustering
	ServerName string `json:"server_name" yaml:"server_name"`

	// Example: 1453969
	ServerPid int `json:"server_pid" yaml:"server_pid"`

	// Example: 4.11
	ServerVersion string `json:"server_version" yaml:"server_version"`

	// Example: dir | zfs
	Storage string `json:"storage" yaml:"storage"`

	// Example: 1 | 0.8.4-1ubuntu11
	StorageVersion string `json:"storage_version" yaml:"storage_version"`
}

// ServerPut represents the modifiable fields of a LXD server configuration
//
// swagger:model
type ServerPut struct {
	// Example: {"core.https_address": ":8443", "core.trust_password": true}
	Config map[string]interface{} `json:"config" yaml:"config"`
}

// ServerUntrusted represents a LXD server for an untrusted client
//
// swagger:model
type ServerUntrusted struct {
	// Example: ["etag", "patch", "network", "storage"]
	APIExtensions []string `json:"api_extensions" yaml:"api_extensions"`

	// Example: stable
	APIStatus string `json:"api_status" yaml:"api_status"`

	// Example: 1.0
	APIVersion string `json:"api_version" yaml:"api_version"`

	// Example: untrusted
	Auth string `json:"auth" yaml:"auth"`

	// Example: true
	Public bool `json:"public" yaml:"public"`

	// Example: ["tls", "candid"]
	//
	// API extension: macaroon_authentication
	AuthMethods []string `json:"auth_methods" yaml:"auth_methods"`
}

// Server represents a LXD server
//
// swagger:model
type Server struct {
	ServerPut       `yaml:",inline"`
	ServerUntrusted `yaml:",inline"`

	Environment ServerEnvironment `json:"environment" yaml:"environment"`
}

// Writable converts a full Server struct into a ServerPut struct (filters read-only fields)
func (srv *Server) Writable() ServerPut {
	return srv.ServerPut
}
