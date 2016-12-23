package api

import (
	"time"
)

// ImagesPost represents the fields available for a new LXD image
type ImagesPost struct {
	ImagePut `yaml:",inline"`

	Filename string            `json:"filename"`
	Source   map[string]string `json:"source"`
}

// ImagePut represents the modifiable fields of a LXD image
type ImagePut struct {
	AutoUpdate bool              `json:"auto_update"`
	Properties map[string]string `json:"properties"`
	Public     bool              `json:"public"`
}

// Image represents a LXD image
type Image struct {
	ImagePut `yaml:",inline"`

	Aliases      []ImageAlias `json:"aliases"`
	Architecture string       `json:"architecture"`
	Cached       bool         `json:"cached"`
	Filename     string       `json:"filename"`
	Fingerprint  string       `json:"fingerprint"`
	Size         int64        `json:"size"`
	UpdateSource *ImageSource `json:"update_source,omitempty"`

	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	UploadedAt time.Time `json:"uploaded_at"`
}

// Writable converts a full Image struct into a ImagePut struct (filters read-only fields)
func (img *Image) Writable() ImagePut {
	return img.ImagePut
}

// ImageAlias represents an alias from the alias list of a LXD image
type ImageAlias struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ImageSource represents the source of a LXD image
type ImageSource struct {
	Alias       string `json:"alias"`
	Certificate string `json:"certificate"`
	Protocol    string `json:"protocol"`
	Server      string `json:"server"`
}

// ImageAliasesPost represents a new LXD image alias
type ImageAliasesPost struct {
	ImageAliasesEntry `yaml:",inline"`
}

// ImageAliasesEntryPost represents the required fields to rename a LXD image alias
type ImageAliasesEntryPost struct {
	Name string `json:"name"`
}

// ImageAliasesEntryPut represents the modifiable fields of a LXD image alias
type ImageAliasesEntryPut struct {
	Description string `json:"description"`
	Target      string `json:"target"`
}

// ImageAliasesEntry represents a LXD image alias
type ImageAliasesEntry struct {
	ImageAliasesEntryPut `yaml:",inline"`

	Name string `json:"name"`
}
