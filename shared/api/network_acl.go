package api

import (
	"strings"
)

// NetworkACLRule represents a single rule in an ACL ruleset.
// Refer to doc/network-acls.md for details.
//
// swagger:model
//
// API extension: network_acl.
type NetworkACLRule struct {
	// lxdmeta:generate(entities=network-acl; group=rule-properties; key=action)
	// Possible values are `allow`, `reject`, and `drop`.
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: Action to take for matching traffic

	// Action to perform on rule match
	// Example: allow
	Action string `json:"action" yaml:"action"`

	// lxdmeta:generate(entities=network-acl; group=rule-properties; key=source)
	// Sources can be specified as CIDR or IP ranges, source subject name selectors (for ingress rules), or be left empty for any.
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Comma-separated list of sources

	// Source address
	// Example: @internal
	Source string `json:"source,omitempty" yaml:"source,omitempty"`

	// lxdmeta:generate(entities=network-acl; group=rule-properties; key=destination)
	// Destinations can be specified as CIDR or IP ranges, destination subject name selectors (for egress rules), or be left empty for any.
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Comma-separated list of destinations

	// Destination address
	// Example: 8.8.8.8/32,8.8.4.4/32
	Destination string `json:"destination,omitempty" yaml:"destination,omitempty"`

	// lxdmeta:generate(entities=network-acl; group=rule-properties; key=protocol)
	// Possible values are `icmp4`, `icmp6`, `tcp`, and `udp`.
	// Leave the value empty to match any protocol.
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Protocol to match

	// Protocol
	// Example: udp
	Protocol string `json:"protocol,omitempty" yaml:"protocol,omitempty"`

	// lxdmeta:generate(entities=network-acl; group=rule-properties; key=source_port)
	// This option is valid only if the protocol is `udp` or `tcp`.
	// Specify a comma-separated list of ports or port ranges (start-end inclusive), or leave the value empty for any.
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Source ports or port ranges

	// Source port
	// Example: 1234
	SourcePort string `json:"source_port,omitempty" yaml:"source_port,omitempty"`

	// lxdmeta:generate(entities=network-acl; group=rule-properties; key=destination_port)
	// This option is valid only if the protocol is `udp` or `tcp`.
	// Specify a comma-separated list of ports or port ranges (start-end inclusive), or leave the value empty for any.
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Destination ports or port ranges

	// Destination port
	// Example: 53
	DestinationPort string `json:"destination_port,omitempty" yaml:"destination_port,omitempty"`

	// lxdmeta:generate(entities=network-acl; group=rule-properties; key=icmp_type)
	// This option is valid only if the protocol is `icmp4` or `icmp6`.
	// Specify the ICMP type number, or leave the value empty for any.
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Type of ICMP message

	// Type of ICMP message (for ICMP protocol)
	// Example: 8
	ICMPType string `json:"icmp_type,omitempty" yaml:"icmp_type,omitempty"`

	// lxdmeta:generate(entities=network-acl; group=rule-properties; key=icmp_code)
	// This option is valid only if the protocol is `icmp4` or `icmp6`.
	// Specify the ICMP code number, or leave the value empty for any.
	// ---
	//  type: string
	//  required: no
	//  shortdesc: ICMP message code

	// ICMP message code (for ICMP protocol)
	// Example: 0
	ICMPCode string `json:"icmp_code,omitempty" yaml:"icmp_code,omitempty"`

	// lxdmeta:generate(entities=network-acl; group=rule-properties; key=description)
	//
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Description of the rule

	// Description of the rule
	// Example: Allow DNS queries to Google DNS
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// lxdmeta:generate(entities=network-acl; group=rule-properties; key=state)
	// Possible values are `enabled`, `disabled`, and `logged`.
	// ---
	//  type: string
	//  required: yes
	//  defaultdesc: `enabled`
	//  shortdesc: State of the rule

	// State of the rule
	// Example: enabled
	State string `json:"state" yaml:"state"`
}

// Normalise normalises the fields in the rule so that they are comparable with ones stored.
func (r *NetworkACLRule) Normalise() {
	r.Action = strings.TrimSpace(r.Action)
	r.Protocol = strings.TrimSpace(r.Protocol)
	r.ICMPType = strings.TrimSpace(r.ICMPType)
	r.ICMPCode = strings.TrimSpace(r.ICMPCode)
	r.Description = strings.TrimSpace(r.Description)
	r.State = strings.TrimSpace(r.State)

	// Remove space from Source subject list.
	subjects := strings.Split(r.Source, ",")
	for i, s := range subjects {
		subjects[i] = strings.TrimSpace(s)
	}

	r.Source = strings.Join(subjects, ",")

	// Remove space from Destination subject list.
	subjects = strings.Split(r.Destination, ",")
	for i, s := range subjects {
		subjects[i] = strings.TrimSpace(s)
	}

	r.Destination = strings.Join(subjects, ",")

	// Remove space from SourcePort port list.
	ports := strings.Split(r.SourcePort, ",")
	for i, s := range ports {
		ports[i] = strings.TrimSpace(s)
	}

	r.SourcePort = strings.Join(ports, ",")

	// Remove space from DestinationPort port list.
	ports = strings.Split(r.DestinationPort, ",")
	for i, s := range ports {
		ports[i] = strings.TrimSpace(s)
	}

	r.DestinationPort = strings.Join(ports, ",")
}

// NetworkACLPost used for renaming an ACL.
//
// swagger:model
//
// API extension: network_acl.
type NetworkACLPost struct {
	// lxdmeta:generate(entities=network-acl; group=acl-properties; key=name)
	//
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: Unique name of the network ACL in the project

	// The new name for the ACL
	// Example: bar
	Name string `json:"name" yaml:"name"` // Name of ACL.
}

// NetworkACLPut used for updating an ACL.
//
// swagger:model
//
// API extension: network_acl.
type NetworkACLPut struct {
	// lxdmeta:generate(entities=network-acl; group=acl-properties; key=description)
	//
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Description of the network ACL

	// Description of the ACL
	// Example: Web servers
	Description string `json:"description" yaml:"description"`

	// lxdmeta:generate(entities=network-acl; group=acl-properties; key=egress)
	//
	// ---
	//  type: rule list
	//  required: no
	//  shortdesc: Egress traffic rules

	// List of egress rules (order independent)
	Egress []NetworkACLRule `json:"egress" yaml:"egress"`

	// lxdmeta:generate(entities=network-acl; group=acl-properties; key=ingress)
	//
	// ---
	//  type: rule list
	//  required: no
	//  shortdesc: Ingress traffic rules

	// List of ingress rules (order independent)
	Ingress []NetworkACLRule `json:"ingress" yaml:"ingress"`

	// lxdmeta:generate(entities=network-acl; group=acl-properties; key=config)
	// The only supported keys are `user.*` custom keys.
	// ---
	//  type: string set
	//  required: no
	//  shortdesc: User-provided free-form key/value pairs

	// ACL configuration map (refer to doc/network-acls.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`
}

// NetworkACL used for displaying an ACL.
//
// swagger:model
//
// API extension: network_acl.
type NetworkACL struct {
	WithEntitlements `yaml:",inline"`

	// The new name for the ACL
	// Example: bar
	Name string `json:"name" yaml:"name"` // Name of ACL.

	// Description of the ACL
	// Example: Web servers
	Description string `json:"description" yaml:"description"`

	// List of egress rules (order independent)
	Egress []NetworkACLRule `json:"egress" yaml:"egress"`

	// List of ingress rules (order independent)
	Ingress []NetworkACLRule `json:"ingress" yaml:"ingress"`

	// ACL configuration map (refer to doc/network-acls.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`

	// List of URLs of objects using this profile
	// Read only: true
	// Example: ["/1.0/instances/c1", "/1.0/instances/v1", "/1.0/networks/lxdbr0"]
	UsedBy []string `json:"used_by" yaml:"used_by"` // Resources that use the ACL.

	// Project name
	// Example: project1
	//
	// API extension: network_acls_all_projects
	Project string `json:"project" yaml:"project"` // Project the ACL belongs to.
}

// Writable converts a full NetworkACL struct into a NetworkACLPut struct (filters read-only fields).
func (acl *NetworkACL) Writable() NetworkACLPut {
	return NetworkACLPut{
		Description: acl.Description,
		Ingress:     acl.Ingress,
		Egress:      acl.Egress,
		Config:      acl.Config,
	}
}

// SetWritable sets applicable values from NetworkACLPut struct to NetworkACL struct.
func (acl *NetworkACL) SetWritable(put NetworkACLPut) {
	acl.Description = put.Description
	acl.Ingress = put.Ingress
	acl.Egress = put.Egress
	acl.Config = put.Config
}

// NetworkACLsPost used for creating an ACL.
//
// swagger:model
//
// API extension: network_acl.
type NetworkACLsPost struct {
	NetworkACLPost `yaml:",inline"`
	NetworkACLPut  `yaml:",inline"`
}
