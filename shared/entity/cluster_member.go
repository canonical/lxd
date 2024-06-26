package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeClusterMember is an instantiated ClusterMember for convenience.
var TypeClusterMember = ClusterMember{}

// TypeNameClusterMember is the TypeName for ClusterMember entities.
const TypeNameClusterMember TypeName = "cluster_member"

// ClusterMember is an implementation of Type for ClusterMember entities.
type ClusterMember struct{}

// RequiresProject returns false for entity type ClusterMember.
func (t ClusterMember) RequiresProject() bool {
	return false
}

// Name returns entity.TypeNameClusterMember.
func (t ClusterMember) Name() TypeName {
	return TypeNameClusterMember
}

// PathTemplate returns the path template for entity type ClusterMember.
func (t ClusterMember) PathTemplate() []string {
	return []string{"cluster", "members", pathPlaceholder}
}

// URL returns a URL for entity type ClusterMember.
func (t ClusterMember) URL(clusterMemberName string) *api.URL {
	return urlMust(t, "", "", clusterMemberName)
}

// String implements fmt.Stringer for ClusterMember entities.
func (t ClusterMember) String() string {
	return string(t.Name())
}
