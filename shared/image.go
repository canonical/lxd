package shared

type ImageProperties map[string]string

type ImageAlias struct {
	Name        string `json:"target"`
	Description string `json:"description"`
}

type ImageAliases []ImageAlias

type ImageInfo struct {
	Aliases      ImageAliases      `json:"aliases"`
	Architecture int               `json:"architecture"`
	Fingerprint  string            `json:"fingerprint"`
	Filename     string            `json:"filename"`
	Properties   map[string]string `json:"properties"`

	// FIXME: This is an interface{] instead of a bool for backward compatibility
	Public interface{} `json:"public"`

	Size         int64 `json:"size"`
	CreationDate int64 `json:"created_at"`
	ExpiryDate   int64 `json:"expires_at"`
	UploadDate   int64 `json:"uploaded_at"`
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

		// FIXME: InterfaceToBool is there for backward compatibility
		Public: InterfaceToBool(i.Public)}
	return retstate
}

type ImageBaseInfo struct {
	Id          int
	Fingerprint string
	Filename    string
	Size        int64

	// FIXME: This is an interface{] instead of a bool for backward compatibility
	Public interface{}

	Architecture int
	CreationDate int64
	ExpiryDate   int64
	UploadDate   int64
}
