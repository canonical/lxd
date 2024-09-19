package api

// MetadataConfiguration contains metadata about the LXD server configuration options.
//
// swagger:model
//
// API extension: metadata_configuration.
type MetadataConfiguration struct {
	// Configs contains all server configuration metadata.
	Configs map[string]map[string]MetadataConfigurationConfigKeys `json:"configs" yaml:"configs"`

	// Entities contains all authorization related metadata.
	//
	// API extension: metadata_configuration_entity_types
	Entities map[string]MetadataConfigurationEntity `json:"entities" yaml:"entities"`
}

// MetadataConfigurationConfigKeys contains metadata about LXD server configuration options.
//
// swagger:model
//
// API extension: metadata_configuration.
type MetadataConfigurationConfigKeys struct {
	Keys []map[string]MetadataConfigurationConfigKey `json:"keys" yaml:"keys"`
}

// MetadataConfigurationConfigKey contains metadata about a LXD server configuration option.
//
// swagger:model
//
// API extension: metadata_configuration.
type MetadataConfigurationConfigKey struct {
	// DefaultDescription contains a description of the configuration key.
	//
	// Example: A general description of a configuration key.
	DefaultDescription string `json:"defaultdesc" yaml:"defaultdesc"`

	// LongDescription contains a long-form description of the configuration key.
	//
	// Example: A much more in-depth description of the configuration key, including where and how it is used.
	LongDescription string `json:"longdesc" yaml:"longdesc"`

	// ShortDescription contains a short-form description of the configuration key.
	//
	// Example: A key for doing X.
	ShortDescription string `json:"shortdesc" yaml:"shortdesc"`

	// Type describes the type of the key.
	//
	// Example: Comma delimited CIDR format subnets.
	Type string `json:"type" yaml:"type"`

	// Condition describes conditions under which the configuration key can be applied.
	//
	// Example: Virtual machines only.
	Condition string `json:"condition" yaml:"condition"`

	// Required describes conditions under which the configuration key is required.
	//
	// Example: On device creation.
	Required string `json:"required" yaml:"required"`

	// Managed describes whether the configuration key is managed by LXD.
	//
	// Example: yes.
	Managed string `json:"managed"`
}

// MetadataConfigurationEntity contains metadata about LXD server entities and available entitlements for authorization.
//
// swagger:model
//
// API extension: metadata_configuration_entity_types.
type MetadataConfigurationEntity struct {
	// ProjectSpecific indicates whether the entity is project specific.
	//
	// Example: true
	ProjectSpecific bool `json:"project_specific" yaml:"project_specific"`

	// Entitlements contains a list of entitlements that apply to a specific entity type.
	Entitlements []MetadataConfigurationEntityEntitlement `json:"entitlements" yaml:"entitlements"`
}

// MetadataConfigurationEntityEntitlement contains metadata about a LXD server entitlement.
//
// swagger:model
//
// API extension: metadata_configuration_entity_types.
type MetadataConfigurationEntityEntitlement struct {
	// Name contains the name of the entitlement.
	//
	// Example: can_edit
	Name string `json:"name" yaml:"name"`

	// Description describes the entitlement.
	//
	// Example: Grants permission to do X, Y, and Z.
	Description string `json:"description" yaml:"description"`
}
