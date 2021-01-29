package api

import "strings"

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

// Normalise normalises the fields in the rule so that they are comparable with ones stored.
func (r *NetworkACLRule) Normalise() {
	r.Action = strings.TrimSpace(r.Action)
	r.Protocol = strings.TrimSpace(r.Protocol)
	r.ICMPType = strings.TrimSpace(r.ICMPType)
	r.ICMPCode = strings.TrimSpace(r.ICMPCode)
	r.Description = strings.TrimSpace(r.Description)
	r.State = strings.TrimSpace(r.State)

	// Remove whitespace from Source subject list.
	subjects := strings.Split(r.Source, ",")
	for i, s := range subjects {
		subjects[i] = strings.TrimSpace(s)
	}
	r.Source = strings.Join(subjects, ",")

	// Remove whitespace from Destination subject list.
	subjects = strings.Split(r.Destination, ",")
	for i, s := range subjects {
		subjects[i] = strings.TrimSpace(s)
	}
	r.Destination = strings.Join(subjects, ",")

	// Remove whitespace from SourcePort port list.
	ports := strings.Split(r.SourcePort, ",")
	for i, s := range ports {
		ports[i] = strings.TrimSpace(s)
	}
	r.SourcePort = strings.Join(ports, ",")

	// Remove whitespace from DestinationPort port list.
	ports = strings.Split(r.DestinationPort, ",")
	for i, s := range ports {
		ports[i] = strings.TrimSpace(s)
	}
	r.DestinationPort = strings.Join(ports, ",")
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
