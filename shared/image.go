package shared

import (
	"time"
)

type ImageProperties map[string]string

type ImageAliasesEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Target      string `json:"target"`
}

type ImageAliases []ImageAliasesEntry

type ImageAlias struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ImageSource struct {
	Server      string `json:"server"`
	Protocol    string `json:"protocol"`
	Certificate string `json:"certificate"`
	Alias       string `json:"alias"`
}

type ImageInfo struct {
	Aliases      []ImageAlias      `json:"aliases"`
	Architecture string            `json:"architecture"`
	Cached       bool              `json:"cached"`
	Filename     string            `json:"filename"`
	Fingerprint  string            `json:"fingerprint"`
	Properties   map[string]string `json:"properties"`
	Public       bool              `json:"public"`
	Size         int64             `json:"size"`

	AutoUpdate bool         `json:"auto_update"`
	Source     *ImageSource `json:"update_source,omitempty"`

	CreationDate time.Time `json:"created_at"`
	ExpiryDate   time.Time `json:"expires_at"`
	LastUsedDate time.Time `json:"last_used_at"`
	UploadDate   time.Time `json:"uploaded_at"`
}

/*
 * BriefImageInfo contains a subset of the fields in
 * ImageInfo, namely those which a user may update
 */
type BriefImageInfo struct {
	AutoUpdate bool              `json:"auto_update"`
	Properties map[string]string `json:"properties"`
	Public     bool              `json:"public"`
}

func (i *ImageInfo) Brief() BriefImageInfo {
	retstate := BriefImageInfo{
		AutoUpdate: i.AutoUpdate,
		Properties: i.Properties,
		Public:     i.Public}
	return retstate
}
