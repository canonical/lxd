package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeNetworkACL is an instantiated NetworkACL for convenience.
var TypeNetworkACL = NetworkACL{}

// TypeNameNetworkACL is the TypeName for NetworkACL entities.
const TypeNameNetworkACL TypeName = "network_acl"

// NetworkACL is an implementation of Type for NetworkACL entities.
type NetworkACL struct{}

// RequiresProject returns true for entity type NetworkACL.
func (t NetworkACL) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameNetworkACL.
func (t NetworkACL) Name() TypeName {
	return TypeNameNetworkACL
}

// PathTemplate returns the path template for entity type NetworkACL.
func (t NetworkACL) PathTemplate() []string {
	return []string{"network-acls", pathPlaceholder}
}

// URL returns a URL for entity type NetworkACL.
func (t NetworkACL) URL(projectName string, networkACLName string) *api.URL {
	return urlMust(t, projectName, "", networkACLName)
}

// String implements fmt.Stringer for NetworkACL entities.
func (t NetworkACL) String() string {
	return string(t.Name())
}
