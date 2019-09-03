package simplestreams

type SimpleStreamsIndex struct {
	Format  string                              `json:"format"`
	Index   map[string]SimpleStreamsIndexStream `json:"index"`
	Updated string                              `json:"updated,omitempty"`
}

type SimpleStreamsIndexStream struct {
	Updated  string   `json:"updated,omitempty"`
	DataType string   `json:"datatype"`
	Path     string   `json:"path"`
	Format   string   `json:"format,omitempty"`
	Products []string `json:"products"`
}
