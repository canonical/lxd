package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeImage is an instantiated Image for convenience.
var TypeImage = Image{}

// TypeNameImage is the TypeName for Image entities.
const TypeNameImage TypeName = "image"

// Image is an implementation of Type for Image entities.
type Image struct{}

// RequiresProject returns true for entity type Image.
func (t Image) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameImage.
func (t Image) Name() TypeName {
	return TypeNameImage
}

// PathTemplate returns the path template for entity type Image.
func (t Image) PathTemplate() []string {
	return []string{"images", pathPlaceholder}
}

// URL returns a URL for entity type Image.
func (t Image) URL(projectName string, imageFingerprint string) *api.URL {
	return urlMust(t, projectName, "", imageFingerprint)
}

// String implements fmt.Stringer for Image entities.
func (t Image) String() string {
	return string(t.Name())
}
