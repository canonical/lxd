package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeNetworkZone is an instantiated NetworkZone for convenience.
var TypeNetworkZone = NetworkZone{}

// TypeNameNetworkZone is the TypeName for NetworkZone entities.
const TypeNameNetworkZone TypeName = "network_zone"

// NetworkZone is an implementation of Type for NetworkZone entities.
type NetworkZone struct{}

// RequiresProject returns true for entity type NetworkZone.
func (t NetworkZone) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameNetworkZone.
func (t NetworkZone) Name() TypeName {
	return TypeNameNetworkZone
}

// PathTemplate returns the path template for entity type NetworkZone.
func (t NetworkZone) PathTemplate() []string {
	return []string{"network-zones", pathPlaceholder}
}

// URL returns a URL for entity type NetworkZone.
func (t NetworkZone) URL(projectName string, networkZoneName string) *api.URL {
	return urlMust(t, projectName, "", networkZoneName)
}

// String implements fmt.Stringer for NetworkZone entities.
func (t NetworkZone) String() string {
	return string(t.Name())
}
