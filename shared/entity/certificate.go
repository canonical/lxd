package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeCertificate is an instantiated Certificate for convenience.
var TypeCertificate = Certificate{}

// TypeNameCertificate is the TypeName for Certificate entities.
const TypeNameCertificate TypeName = "certificate"

// Certificate is an implementation of Type for Certificate entities.
type Certificate struct{}

// RequiresProject returns false for entity type Certificate.
func (t Certificate) RequiresProject() bool {
	return false
}

// Name returns entity.TypeNameCertificate.
func (t Certificate) Name() TypeName {
	return TypeNameCertificate
}

// PathTemplate returns the path template for entity type Certificate.
func (t Certificate) PathTemplate() []string {
	return []string{"certificates", pathPlaceholder}
}

// URL returns a URL for entity type Certificate.
func (t Certificate) URL(certificateFingerprint string) *api.URL {
	return urlMust(t, "", "", certificateFingerprint)
}

// String implements fmt.Stringer for Certificate entities.
func (t Certificate) String() string {
	return string(t.Name())
}
