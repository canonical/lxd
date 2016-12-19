package api

// Network represents a LXD network
type Network struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	UsedBy []string `json:"used_by"`
}
