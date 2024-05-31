package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeClusterGroup is an instantiated ClusterGroup for convenience.
var TypeClusterGroup = ClusterGroup{}

// TypeNameClusterGroup is the TypeName for ClusterGroup entities.
const TypeNameClusterGroup TypeName = "cluster_group"

// ClusterGroup is an implementation of Type for ClusterGroup entities.
type ClusterGroup struct{}

// RequiresProject returns false for entity type ClusterGroup.
func (t ClusterGroup) RequiresProject() bool {
	return false
}

// Name returns entity.TypeNameClusterGroup.
func (t ClusterGroup) Name() TypeName {
	return TypeNameClusterGroup
}

// PathTemplate returns the path template for entity type ClusterGroup.
func (t ClusterGroup) PathTemplate() []string {
	return []string{"cluster", "groups", pathPlaceholder}
}

// URL returns a URL for entity type ClusterGroup.
func (t ClusterGroup) URL(clusterGroupName string) *api.URL {
	return urlMust(t, "", "", clusterGroupName)
}

// String implements fmt.Stringer for ClusterGroup entities.
func (t ClusterGroup) String() string {
	return string(t.Name())
}
