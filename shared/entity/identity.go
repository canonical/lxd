package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeIdentity is an instantiated Identity for convenience.
var TypeIdentity = Identity{}

// TypeNameIdentity is the TypeName for Identity entities.
const TypeNameIdentity TypeName = "identity"

// Identity is an implementation of Type for Identity entities.
type Identity struct{}

// RequiresProject returns false for entity type Identity.
func (t Identity) RequiresProject() bool {
	return false
}

// Name returns entity.TypeNameIdentity.
func (t Identity) Name() TypeName {
	return TypeNameIdentity
}

// PathTemplate returns the path template for entity type Identity.
func (t Identity) PathTemplate() []string {
	return []string{"auth", "identities", pathPlaceholder, pathPlaceholder}
}

// URL returns a URL for entity type Identity.
func (t Identity) URL(authenticationMethod string, identifier string) *api.URL {
	return urlMust(t, "", "", authenticationMethod, identifier)
}

// String implements fmt.Stringer for Identity entities.
func (t Identity) String() string {
	return string(t.Name())
}
