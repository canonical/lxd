package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeImageAlias is an instantiated ImageAlias for convenience.
var TypeImageAlias = ImageAlias{}

// TypeNameImageAlias is the TypeName for ImageAlias entities.
const TypeNameImageAlias TypeName = "image_alias"

// ImageAlias is an implementation of Type for ImageAlias entities.
type ImageAlias struct{}

// RequiresProject returns true for entity type ImageAlias.
func (t ImageAlias) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameImageAlias.
func (t ImageAlias) Name() TypeName {
	return TypeNameImageAlias
}

// PathTemplate returns the path template for entity type ImageAlias.
func (t ImageAlias) PathTemplate() []string {
	return []string{"images", "aliases", pathPlaceholder}
}

// URL returns a URL for entity type ImageAlias.
func (t ImageAlias) URL(projectName string, imageAliasName string) *api.URL {
	return urlMust(t, projectName, "", imageAliasName)
}

// String implements fmt.Stringer for ImageAlias entities.
func (t ImageAlias) String() string {
	return string(t.Name())
}
