package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeNetwork is an instantiated Network for convenience.
var TypeNetwork = Network{}

// TypeNameNetwork is the TypeName for Network entities.
const TypeNameNetwork TypeName = "network"

// Network is an implementation of Type for Network entities.
type Network struct{}

// RequiresProject returns true for entity type Network.
func (t Network) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameNetwork.
func (t Network) Name() TypeName {
	return TypeNameNetwork
}

// PathTemplate returns the path template for entity type Network.
func (t Network) PathTemplate() []string {
	return []string{"networks", pathPlaceholder}
}

// URL returns a URL for entity type Network.
func (t Network) URL(projectName string, networkName string) *api.URL {
	return urlMust(t, projectName, "", networkName)
}

// String implements fmt.Stringer for Network entities.
func (t Network) String() string {
	return string(t.Name())
}
