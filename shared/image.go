package shared

type ImageProperty struct {
	Imagetype int
	Key       string
	Value     string
}

type ImageProperties []ImageProperty

type ImageAlias struct {
	Name        string `json:"target"`
	Description string `json:"description"`
}

type ImageAliases []ImageAlias

type ImageInfo struct {
	Fingerprint string          `json:"fingerprint"`
	Properties  ImageProperties `json:"properties"`
	Aliases     ImageAliases    `json:"aliases"`
	Public      int             `json:"public"`
}

type ImageBaseInfo struct {
	Id          int
	Fingerprint string
	Filename    string
	Size        int64
	Public      int
}
