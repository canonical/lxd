package api

// Supported image registry protocols.
const (
	// Image registry protocol "SimpleStreams".
	ImageRegistryProtocolSimpleStreams = "simplestreams"
	// Image registry protocol "LXD".
	ImageRegistryProtocolLXD = "lxd"
)

// ImageRegistriesPost represents the fields of a new image registry.
//
// swagger:model
//
// API extension: image_registries.
type ImageRegistriesPost struct {
	ImageRegistryPut `yaml:",inline"`

	// Registry name
	// Example: ubuntu
	Name string `json:"name" yaml:"name"`
}

// ImageRegistryPost represents the fields required to rename the image registry.
//
// swagger:model
//
// API extension: image_registries.
type ImageRegistryPost struct {
	// New name for the image registry
	// Example: bar
	Name string `json:"name" yaml:"name"`
}

// ImageRegistryPut represents the modifiable fields of an image registry.
//
// swagger:model
//
// API extension: image_registries.
type ImageRegistryPut struct {
	// Cluster link name for a private image registry using "LXD" protocol
	// Example: lxd01
	Cluster string `json:"cluster,omitempty" yaml:"cluster,omitempty"`

	// Source URL for the image registry
	// Example: https://cloud-images.ubuntu.com/releases/
	URL string `json:"url" yaml:"url"`

	// Flag that determines whether the image registry can be accessed without authentication
	Public bool `json:"public" yaml:"public"`

	// Protocol used by the image registry ("SimpleStreams" or "LXD")
	// Example: simplestreams
	Protocol string `json:"protocol" yaml:"protocol"`

	// Source project for an image registry using "LXD" protocol
	// Example: default
	SourceProject string `json:"source_project" yaml:"source_project"`
}

// ImageRegistry is used for displaying an image registry.
//
// swagger:model
//
// API extension: image_registries.
type ImageRegistry struct {
	WithEntitlements `yaml:",inline"`

	// Registry name
	// Example: ubuntu
	Name string `json:"name" yaml:"name"`

	// Associated cluster link name for image registries using "LXD" protocol
	// Example: lxd01
	Cluster string `json:"cluster,omitempty" yaml:"cluster,omitempty"`

	// Source URL for the image registry
	// Example: https://cloud-images.ubuntu.com/releases/
	URL string `json:"url" yaml:"url"`

	// Flag that determines whether the server can be accessed without auth
	Public bool `json:"public" yaml:"public"`

	// Protocol used by image registry ("SimpleStreams" or "LXD")
	// Example: simplestreams
	Protocol string `json:"protocol" yaml:"protocol"`

	// Source project for image registry using "LXD" protocol
	// Example: default
	SourceProject string `json:"source_project,omitempty" yaml:"source_project,omitempty"`
}

// Writable converts a full [ImageRegistry] struct into a [ImageRegistryPut] struct (filters read-only fields).
func (registry *ImageRegistry) Writable() ImageRegistryPut {
	return ImageRegistryPut{
		Cluster:       registry.Cluster,
		URL:           registry.URL,
		Public:        registry.Public,
		Protocol:      registry.Protocol,
		SourceProject: registry.SourceProject,
	}
}

// SetWritable sets applicable values from [ImageRegistryPut] struct to [ImageRegistry] struct.
func (registry *ImageRegistry) SetWritable(put ImageRegistryPut) {
	registry.Cluster = put.Cluster
	registry.URL = put.URL
	registry.Public = put.Public
	registry.Protocol = put.Protocol
	registry.SourceProject = put.SourceProject
}

// Etag returns the values used for etag generation.
func (registry *ImageRegistry) Etag() []any {
	return []any{registry.Name, registry.Cluster, registry.URL, registry.Public, registry.Protocol, registry.SourceProject}
}
