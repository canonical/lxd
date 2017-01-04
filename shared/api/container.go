package api

import (
	"time"
)

// ContainersPost represents the fields available for a new LXD container
type ContainersPost struct {
	ContainerPut `yaml:",inline"`

	Name   string          `json:"name"`
	Source ContainerSource `json:"source"`
}

// ContainerPost represents the fields required to rename/move a LXD container
type ContainerPost struct {
	Migration bool   `json:"migration"`
	Name      string `json:"name"`
}

// ContainerPut represents the modifiable fields of a LXD container
type ContainerPut struct {
	Architecture string                       `json:"architecture"`
	Config       map[string]string            `json:"config"`
	Devices      map[string]map[string]string `json:"devices"`
	Ephemeral    bool                         `json:"ephemeral"`
	Profiles     []string                     `json:"profiles"`
	Restore      string                       `json:"restore,omitempty" yaml:"restore,omitempty"`
}

// Container represents a LXD container
type Container struct {
	ContainerPut `yaml:",inline"`

	CreatedAt       time.Time                    `json:"created_at"`
	ExpandedConfig  map[string]string            `json:"expanded_config"`
	ExpandedDevices map[string]map[string]string `json:"expanded_devices"`
	Name            string                       `json:"name"`
	Stateful        bool                         `json:"stateful"`
	Status          string                       `json:"status"`
	StatusCode      StatusCode                   `json:"status_code"`
}

// Writable converts a full Container struct into a ContainerPut struct (filters read-only fields)
func (c *Container) Writable() ContainerPut {
	return c.ContainerPut
}

// IsActive checks whether the container state indicates the container is active
func (c Container) IsActive() bool {
	switch c.StatusCode {
	case Stopped:
		return false
	case Error:
		return false
	default:
		return true
	}
}

// ContainerSource represents the creation source for a new container
type ContainerSource struct {
	Type        string `json:"type"`
	Certificate string `json:"certificate"`

	// For "image" type
	Alias       string            `json:"alias,omitempty"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
	Server      string            `json:"server,omitempty"`
	Secret      string            `json:"secret,omitempty"`
	Protocol    string            `json:"protocol,omitempty"`

	// For "migration" and "copy" types
	BaseImage string `json:"base-image,omitempty"`

	// For "migration" type
	Mode       string            `json:"mode,omitempty"`
	Operation  string            `json:"operation,omitempty"`
	Websockets map[string]string `json:"secrets,omitempty"`

	// For "copy" type
	Source string `json:"source,omitempty"`
}
