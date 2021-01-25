package api

// NetworkACLRule represents a single rule in an ACL ruleset.
// API extension: network_acl
type NetworkACLRule struct {
	Action          string `json:"action" yaml:"action"`
	Source          string `json:"source" yaml:"source"`
	Destination     string `json:"destination" yaml:"destination"`
	Protocol        string `json:"protocol" yaml:"protocol"`
	SourcePort      string `json:"source_port" yaml:"source_port"`
	DestinationPort string `json:"destination_port" yaml:"destination_port"`
	ICMPType        string `json:"icmp_type" yaml:"icmp_type"`
	ICMPCode        string `json:"icmp_code" yaml:"icmp_code"`
	Description     string `json:"description" yaml:"description"`
	State           string `json:"state" yaml:"state"`
}

// NetworkACLPost used for renaming an ACL.
// API extension: network_acl
type NetworkACLPost struct {
	Name string `json:"name" yaml:"name"` // Name of ACL.
}

// NetworkACLPut used for updating an ACL.
// API extension: network_acl
type NetworkACLPut struct {
	Description string            `json:"description" yaml:"description"` // Friendly description of ACL.
	Egress      []NetworkACLRule  `json:"egress" yaml:"egress"`           // Egress rules (order independent).
	Ingress     []NetworkACLRule  `json:"ingress" yaml:"ingress"`         // Ingress rules (order independent).
	Config      map[string]string `json:"config" yaml:"config"`           // Used for custom settings.
}

// NetworkACL used for displaying an ACL.
// API extension: network_acl
type NetworkACL struct {
	NetworkACLPost `yaml:",inline"`
	NetworkACLPut  `yaml:",inline"`

	UsedBy []string `json:"used_by" yaml:"used_by"` // Resources that use the ACL.
}

// Writable converts a full NetworkACL struct into a NetworkACLPut struct (filters read-only fields).
func (acl *NetworkACL) Writable() NetworkACLPut {
	return acl.NetworkACLPut
}

// NetworkACLsPost used for creating an ACL.
// API extension: network_acl
type NetworkACLsPost struct {
	NetworkACLPost `yaml:",inline"`
	NetworkACLPut  `yaml:",inline"`
}
