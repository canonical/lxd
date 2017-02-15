package api

// Network represents a LXD network
type Network struct {
	Name   string   `json:"name" yaml:"name"`
	Type   string   `json:"type" yaml:"type"`
	UsedBy []string `json:"used_by" yaml:"used_by"`
}
