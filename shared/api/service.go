package api

// ServiceType represents the types of supported services.
type ServiceType int

const (
	// TypeLXD represents a LXD remote cluster.
	TypeLXD ServiceType = 0
	// TypeSimpleStreams represents a LXD server side image server.
	TypeSimpleStreams ServiceType = 1
)

// ServiceTypeNames associates a service type code to its name.
var ServiceTypeNames = map[ServiceType]string{
	0: "lxd",
	1: "simplestreams",
}

// Service represents high-level information about a service.
//
// swagger:model
//
// API extension: services.
type Service struct {
	// The name of the service
	// Example: lxd02
	Name string `json:"name" yaml:"name"`

	// The service endpoint addresses
	// Example: [10.0.0.1:8443, 10.0.0.2:8443]
	Addresses []string `json:"addresses" yaml:"addresses"`

	// The type of the service
	// Example: lxd
	Type ServiceType `json:"type" yaml:"type"`

	// Service configuration map (refer to doc/service.md)
	// Example: {"addresses": ["10.0.0.1:8443", "10.0.0.1:8443"]}
	Config map[string]string `json:"config" yaml:"config"`

	// Description of the service
	// Example: Backup LXD cluster
	Description string `json:"description" yaml:"description"`
}

// ServiceConfigKey represents a single config key.
//
// swagger:model
//
// API extension: services.
type ServiceConfigKey struct {
	// The name of the object requiring this key
	// Example: local
	Name string `json:"name" yaml:"name"`

	// The name of the key
	// Example:
	Key string `json:"key" yaml:"key"`

	// The value on the service
	// Example:
	Value string `json:"value" yaml:"value"`
}

// ServicePut represents the modifiable fields of a service.
//
// swagger:model
//
// API extension: services.
type ServicePut struct {
	// Service configuration map (refer to doc/service.md)
	// Example: {"addresses": ["10.0.0.1:8443", "10.0.0.1:8443"]}
	Config map[string]string `json:"config" yaml:"config"`

	// Description of the service
	// Example: Backup LXD cluster
	Description string `json:"description" yaml:"description"`

	// The service endpoint addresses
	// Example: [10.0.0.1:8443, 10.0.0.2:8443]
	Addresses []string `json:"addresses" yaml:"addresses"`
}

// ServicePost represents the fields required to add a service using a join token to establish trust.
//
// swagger:model
//
// API extension: services.
type ServicePost struct {
	ServicePut `yaml:",inline"`

	// The name of the service
	// Example: service_b
	Name string `json:"name" yaml:"name"`

	// API extension: explicit_trust_token
	TrustToken string `json:"trust_token" yaml:"trust_token"`

	// The type of the service
	// Example: lxd
	Type string `json:"type" yaml:"type"`

	// The name of the created identity for the service
	// Example: tls/service_b
	IdentityName string `json:"identity_name" yaml:"identity_name"`

	// Service description
	// Example: My backup service
	Description string `json:"description" yaml:"description"`

	// Optional address to use for connecting to the service (overrides addresses in the trust token)
	// Example: 198.51.100.2
	Address string `json:"address" yaml:"address"`
}

// String returns a suitable string representation for the service type code.
func (s ServiceType) String() string {
	return ServiceTypeNames[s]
}

// Writable converts a full Service struct into a ServicePut struct (filters read-only fields).
func (service *Service) Writable() ServicePut {
	return ServicePut{
		Config:      service.Config,
		Description: service.Description,
		Addresses:   service.Addresses,
	}
}
