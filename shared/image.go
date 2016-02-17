package shared

type ImageProperties map[string]string

type ImageAlias struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Target      string `json:"target"`
}

type ImageAliases []ImageAlias

type ImageInfo struct {
	Aliases      ImageAliases      `json:"aliases"`
	Architecture int               `json:"architecture"`
	Fingerprint  string            `json:"fingerprint"`
	Filename     string            `json:"filename"`
	Properties   map[string]string `json:"properties"`
	Public       bool              `json:"public"`
	Size         int64             `json:"size"`
	CreationDate int64             `json:"created_at"`
	ExpiryDate   int64             `json:"expires_at"`
	UploadDate   int64             `json:"uploaded_at"`
}

/*
 * BriefImageInfo contains a subset of the fields in
 * ImageInfo, namely those which a user may update
 */
type BriefImageInfo struct {
	Properties map[string]string `json:"properties"`
	Public     bool              `json:"public"`
}

func (i *ImageInfo) BriefInfo() BriefImageInfo {
	retstate := BriefImageInfo{
		Properties: i.Properties,
		Public:     i.Public}
	return retstate
}

type ImageBaseInfo struct {
	Id           int
	Fingerprint  string
	Filename     string
	Size         int64
	Public       bool
	Architecture int
	CreationDate int64
	ExpiryDate   int64
	UploadDate   int64
}
