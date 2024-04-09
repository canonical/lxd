package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeIdentityProviderGroup is an instantiated IdentityProviderGroup for convenience.
var TypeIdentityProviderGroup = IdentityProviderGroup{}

// TypeNameIdentityProviderGroup is the TypeName for IdentityProviderGroup entities.
const TypeNameIdentityProviderGroup TypeName = "identity_provider_group"

// IdentityProviderGroup is an implementation of Type for IdentityProviderGroup entities.
type IdentityProviderGroup struct{}

// RequiresProject returns false for entity type IdentityProviderGroup.
func (t IdentityProviderGroup) RequiresProject() bool {
	return false
}

// Name returns entity.TypeNameIdentityProviderGroup.
func (t IdentityProviderGroup) Name() TypeName {
	return TypeNameIdentityProviderGroup
}

// PathTemplate returns the path template for entity type IdentityProviderGroup.
func (t IdentityProviderGroup) PathTemplate() []string {
	return []string{"auth", "identity-provider-groups", pathPlaceholder}
}

// URL returns a URL for entity type IdentityProviderGroup.
func (t IdentityProviderGroup) URL(identityProviderGroupName string) *api.URL {
	return urlMust(t, "", "", identityProviderGroupName)
}

// String implements fmt.Stringer for IdentityProviderGroup entities.
func (t IdentityProviderGroup) String() string {
	return string(t.Name())
}
