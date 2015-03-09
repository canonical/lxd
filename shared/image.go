package shared

type ImageProperty struct {
	Imagetype int
	Key       string
	Value     string
}

type ImageProperties []ImageProperty

type ImageAlias struct {
	Name        string
	Description string
}

type ImageAliases []ImageAlias

type ImageInfo struct {
	Fingerprint string
	Properties  ImageProperties
	Aliases     ImageAliases
	Public      int
}
