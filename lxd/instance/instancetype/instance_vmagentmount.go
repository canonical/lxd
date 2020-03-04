package instancetype

// VMAgentMount defines mounts to perform inside VM via agent.
type VMAgentMount struct {
	Source  string   `json:"source"`
	Target  string   `json:"target"`
	FSType  string   `json:"fstype"`
	Options []string `json:"options"`
}
