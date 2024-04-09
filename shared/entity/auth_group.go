package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeAuthGroup is an instantiated AuthGroup for convenience.
var TypeAuthGroup = AuthGroup{}

// TypeNameAuthGroup is the TypeName for AuthGroup entities. Note that the name "group" and not "auth_group". This is so
// that it corresponds with the OpenFGA model.
const TypeNameAuthGroup TypeName = "group"

// AuthGroup is an implementation of Type for AuthGroup entities.
type AuthGroup struct{}

// RequiresProject returns false for entity type AuthGroup.
func (t AuthGroup) RequiresProject() bool {
	return false
}

// Name returns entity.TypeNameAuthGroup.
func (t AuthGroup) Name() TypeName {
	return TypeNameAuthGroup
}

// PathTemplate returns the path template for entity type AuthGroup.
func (t AuthGroup) PathTemplate() []string {
	return []string{"auth", "groups", pathPlaceholder}
}

// URL returns a URL for entity type AuthGroup.
func (t AuthGroup) URL(groupName string) *api.URL {
	return urlMust(t, "", "", groupName)
}

// String implements fmt.Stringer for AuthGroup entities.
func (t AuthGroup) String() string {
	return string(t.Name())
}
