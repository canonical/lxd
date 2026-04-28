package api

import (
	"net/url"
	"strings"
)

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

	// Protocol used by the image registry ("SimpleStreams" or "LXD")
	// Example: simplestreams
	Protocol string `json:"protocol" yaml:"protocol"`
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
	// Description of the image registry
	// Example: My new image registry
	Description string `json:"description" yaml:"description"`

	// Image registry configuration map
	// Example: { "user.*": "" }
	Config map[string]string `json:"config" yaml:"config"`
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

	// Description of the image registry
	// Example: My new image registry
	Description string `json:"description" yaml:"description"`

	// Protocol used by image registry ("SimpleStreams" or "LXD")
	// Example: simplestreams
	Protocol string `json:"protocol" yaml:"protocol"`

	// Whether the image registry is public
	// Example: true
	Public bool `json:"public" yaml:"public"`

	// Whether the image registry is built-in
	// Example: false
	Builtin bool `json:"builtin" yaml:"builtin"`

	// Image registry configuration map
	// Example: { "user.*": "" }
	Config map[string]string `json:"config" yaml:"config"`
}

// Writable converts a full [ImageRegistry] struct into a [ImageRegistryPut] struct (filters read-only fields).
func (registry *ImageRegistry) Writable() ImageRegistryPut {
	return ImageRegistryPut{
		Description: registry.Description,
		Config:      registry.Config,
	}
}

// SetWritable sets applicable values from [ImageRegistryPut] struct to [ImageRegistry] struct.
func (registry *ImageRegistry) SetWritable(put ImageRegistryPut) {
	registry.Description = put.Description
	registry.Config = put.Config
}

// Etag returns the values used for etag generation.
func (registry *ImageRegistry) Etag() []any {
	return []any{registry.Name, registry.Description, registry.Protocol, registry.Config}
}

// transitionalSimpleStreamsHosts contains hostnames whose SimpleStreams URLs
// are allowed through the deprecated Server/Protocol backward-compatibility
// path. Only HTTPS URLs on these hosts are accepted.
var transitionalSimpleStreamsHosts = []string{
	"cloud-images.ubuntu.com",
	"images.lxd.canonical.com",
}

// IsTransitionalSimpleStreamsURL reports whether raw URL is a SimpleStreams
// endpoint eligible for the transitional backward-compatibility path. The URL
// must use the HTTPS scheme and its hostname must appear in the allowlist.
func IsTransitionalSimpleStreamsURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	if u.Scheme != "https" {
		return false
	}

	host := strings.ToLower(u.Hostname())
	for _, allowed := range transitionalSimpleStreamsHosts {
		if host == allowed {
			return true
		}
	}

	return false
}

// TransitionalRegistryName derives a deterministic image registry name from a
// SimpleStreams URL. The scheme is stripped, the hostname is lower-cased, and
// non-empty path segments are joined with "-". The result is suitable for use
// as an image registry name (ASCII, no "/" or ":").
//
// Returns an empty string if rawURL cannot be parsed.
func TransitionalRegistryName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}

	// Collect non-empty path segments.
	var segments []string
	for _, seg := range strings.Split(u.Path, "/") {
		if seg != "" {
			segments = append(segments, seg)
		}
	}

	if len(segments) == 0 {
		return host
	}

	return host + "-" + strings.Join(segments, "-")
}
