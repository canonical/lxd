package instancetype

// VMAgentMount defines mounts to perform inside VM via agent.
type VMAgentMount struct {
	Source     string
	TargetPath string
	FSType     string
	Opts       []string
}
