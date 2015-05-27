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
	Public       int               `json:"public"`
	Size         int64             `json:"size"`
	CreationDate int64             `json:"created_at"`
	ExpiryDate   int64             `json:"expires_at"`
	UploadDate   int64             `json:"uploaded_at"`
}

type ImageBaseInfo struct {
	Id           int
	Fingerprint  string
	Filename     string
	Size         int64
	Public       int
	Architecture int
	CreationDate int64
	ExpiryDate   int64
	UploadDate   int64
}
