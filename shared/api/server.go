package api

// ServerEnvironment represents the read-only environment fields of a LXD server.
type ServerEnvironment struct {
	// List of addresses the server is listening on
	// Example: [":8443"]
	Addresses []string `json:"addresses" yaml:"addresses" diff:"addresses(severity=warning)"`

	// List of architectures supported by the server
	// Example: ["x86_64", "i686"]
	Architectures []string `json:"architectures" yaml:"architectures" diff:"architectures(severity=critical)"`

	// Server certificate as PEM encoded X509
	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate" diff:"-"`

	// Server certificate fingerprint as SHA256
	// Example: fd200419b271f1dc2a5591b693cc5774b7f234e1ff8c6b78ad703b6888fe2b69
	CertificateFingerprint string `json:"certificate_fingerprint" yaml:"certificate_fingerprint" diff:"-"`

	// List of supported instance drivers (separate by " | ")
	// Example: lxc | qemu
	Driver string `json:"driver" yaml:"driver" diff:"driver(severity=critical)"`

	// List of supported instance driver versions (separate by " | ")
	// Example: 4.0.7 | 5.2.0
	DriverVersion string `json:"driver_version" yaml:"driver_version" diff:"driver_version(severity=warning)"`

	// List of supported instance types
	// Example: ["container", "virtual-machine"]
	//
	// API extension: server_instance_type_info
	InstanceTypes []string `json:"instance_types" yaml:"instance_types" diff:"instance_types(severity=critical)"`

	// Current firewall driver
	// Example: nftables
	//
	// API extension: firewall_driver
	Firewall string `json:"firewall" yaml:"firewall" diff:"firewall(severity=warning)"`

	// OS kernel name
	// Example: Linux
	Kernel string `json:"kernel" yaml:"kernel" diff:"kernel(severity=critical)"`

	// OS kernel architecture
	// Example: x86_64
	KernelArchitecture string `json:"kernel_architecture" yaml:"kernel_architecture" diff:"kernel_architecture(severity=critical)"`

	// Map of kernel features that were tested on startup
	// Example: {"netnsid_getifaddrs": "true", "seccomp_listener": "true"}
	//
	// API extension: kernel_features
	KernelFeatures map[string]string `json:"kernel_features" yaml:"kernel_features" diff:"kernel_features(severity=critical)"`

	// Kernel version
	// Example: 5.4.0-36-generic
	KernelVersion string `json:"kernel_version" yaml:"kernel_version" diff:"kernel_version(severity=critical)"`

	// Map of LXC features that were tested on startup
	// Example: {"cgroup2": "true", "devpts_fd": "true", "pidfd": "true"}
	//
	// API extension: lxc_features
	LXCFeatures map[string]string `json:"lxc_features" yaml:"lxc_features" diff:"lxc_features(severity=critical)"`

	// Name of the operating system (Linux distribution)
	// Example: Ubuntu
	//
	// API extension: api_os
	OSName string `json:"os_name" yaml:"os_name" diff:"os_name(severity=warning)"`

	// Version of the operating system (Linux distribution)
	// Example: 24.04
	//
	// API extension: api_os
	OSVersion string `json:"os_version" yaml:"os_version" diff:"os_version(severity=warning)"`

	// Current project name
	// Example: default
	//
	// API extension: projects
	Project string `json:"project" yaml:"project" diff:"-"`

	// Server implementation name
	// Example: lxd
	Server string `json:"server" yaml:"server" diff:"server(severity=warning)"`

	// Whether the server is part of a cluster
	// Example: false
	//
	// API extension: clustering
	ServerClustered bool `json:"server_clustered" yaml:"server_clustered" diff:"server_clustered(severity=critical)"`

	// Mode that the event distribution subsystem is operating in on this server.
	// Either "full-mesh", "hub-server" or "hub-client".
	// Example: full-mesh
	//
	// API extension: event_hub
	ServerEventMode string `json:"server_event_mode" yaml:"server_event_mode" diff:"server_event_mode"`

	// Server hostname
	// Example: castiana
	//
	// API extension: clustering
	ServerName string `json:"server_name" yaml:"server_name" diff:"server_name(severity=warning)"`

	// PID of the LXD process
	// Example: 1453969
	ServerPid int `json:"server_pid" yaml:"server_pid" diff:"-"`

	// Server version
	// Example: 4.11
	ServerVersion string `json:"server_version" yaml:"server_version" diff:"server_version(severity=critical)"`

	// Whether the version is an LTS release
	// Example: false
	ServerLTS bool `json:"server_lts" yaml:"server_lts" diff:"server_lts(severity=warning)"`

	// List of active storage drivers (separate by " | ")
	// Example: dir | zfs
	Storage string `json:"storage" yaml:"storage" diff:"storage(severity=critical)"`

	// List of active storage driver versions (separate by " | ")
	// Example: 1 | 0.8.4-1ubuntu11
	StorageVersion string `json:"storage_version" yaml:"storage_version" diff:"storage_version(severity=critical)"`

	// List of supported storage drivers
	StorageSupportedDrivers []ServerStorageDriverInfo `json:"storage_supported_drivers" yaml:"storage_supported_drivers" diff:"storage_supported_drivers(severity=critical)"`
}

// ServerStorageDriverInfo represents the read-only info about a storage driver
//
// swagger:model
//
// API extension: server_supported_storage_drivers.
type ServerStorageDriverInfo struct {
	// Name of the driver
	// Example: zfs
	//
	// API extension: server_supported_storage_drivers
	Name string `diff:"name"`

	// Version of the driver
	// Example: 0.8.4-1ubuntu11
	//
	// API extension: server_supported_storage_drivers
	Version string `diff:"version"`

	// Whether the driver has remote volumes
	// Example: false
	//
	// API extension: server_supported_storage_drivers
	Remote bool `diff:"remote"`
}

// ServerPut represents the modifiable fields of a LXD server configuration
//
// swagger:model
type ServerPut struct {
	// Server configuration map (refer to doc/server.md)
	// Example: {"core.https_address": ":8443"}
	Config map[string]any `json:"config" yaml:"config" diff:"config"`
}

// ServerUntrusted represents a LXD server for an untrusted client
//
// swagger:model
type ServerUntrusted struct {
	// List of supported API extensions
	// Read only: true
	// Example: ["etag", "patch", "network", "storage"]
	APIExtensions []string `json:"api_extensions" yaml:"api_extensions" diff:"api_extensions(severity=critical)"`

	// Support status of the current API (one of "devel", "stable" or "deprecated")
	// Read only: true
	// Example: stable
	APIStatus string `json:"api_status" yaml:"api_status" diff:"api_status(severity=critical)"`

	// API version number
	// Read only: true
	// Example: 1.0
	APIVersion string `json:"api_version" yaml:"api_version" diff:"api_version(severity=critical)"`

	// Whether the client is trusted (one of "trusted" or "untrusted")
	// Read only: true
	// Example: untrusted
	Auth string `json:"auth" yaml:"auth" diff:"-"`

	// Whether the server is public-only (only public endpoints are implemented)
	// Read only: true
	// Example: false
	Public bool `json:"public" yaml:"public" diff:"public(severity=critical)"`

	// List of supported authentication methods
	// Read only: true
	// Example: ["tls", "oidc"]
	//
	// API extension: oidc
	AuthMethods []string `json:"auth_methods" yaml:"auth_methods" diff:"auth_methods(severity=critical)"`
}

// Server represents a LXD server
//
// swagger:model
type Server struct {
	ServerPut       `yaml:",inline"`
	ServerUntrusted `yaml:",inline"`

	// The current user username as seen by LXD
	// Read only: true
	// Example: uid=201105
	//
	// API extension: auth_user
	AuthUserName string `json:"auth_user_name" yaml:"auth_user_name" diff:"-"`

	// The current user login method as seen by LXD
	// Read only: true
	// Example: unix
	//
	// API extension: auth_user
	AuthUserMethod string `json:"auth_user_method" yaml:"auth_user_method" diff:"-"`

	// Read-only status/configuration information
	// Read only: true
	Environment ServerEnvironment `json:"environment" yaml:"environment" diff:"environment"`
}

// Writable converts a full Server struct into a ServerPut struct (filters read-only fields).
func (srv *Server) Writable() ServerPut {
	return srv.ServerPut
}
