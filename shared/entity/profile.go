package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeProfile is an instantiated Profile for convenience.
var TypeProfile = Profile{}

// TypeNameProfile is the TypeName for Profile entities.
const TypeNameProfile TypeName = "profile"

// Profile is an implementation of Type for Profile entities.
type Profile struct{}

// RequiresProject returns true for entity type Profile.
func (t Profile) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameProfile.
func (t Profile) Name() TypeName {
	return TypeNameProfile
}

// PathTemplate returns the path template for entity type Profile.
func (t Profile) PathTemplate() []string {
	return []string{"profiles", pathPlaceholder}
}

// URL returns a URL for entity type Profile.
func (t Profile) URL(projectName string, profileName string) *api.URL {
	return urlMust(t, projectName, "", profileName)
}

// String implements fmt.Stringer for Profile entities.
func (t Profile) String() string {
	return string(t.Name())
}
