package identity

import (
	"errors"

	"github.com/canonical/lxd/lxd/certificate"
)

// typeInfoCommon is a common implementation of the [Type] interface.
type typeInfoCommon struct{}

// IsAdmin returns false by default.
func (typeInfoCommon) IsAdmin() bool {
	return false
}

// IsFineGrained returns false by default.
func (typeInfoCommon) IsFineGrained() bool {
	return false
}

// IsPending returns false by default.
func (typeInfoCommon) IsPending() bool {
	return false
}

// LegacyCertificateType returns the legacy certificate type for this identity type.
// If (-1, error) is returned, it indicates that the identity type does not correspond to a legacy certificate type.
func (typeInfoCommon) LegacyCertificateType() (certificate.Type, error) {
	return -1, errors.New("Identity type is not a certificate")
}
