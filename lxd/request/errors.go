package request

import (
	"errors"
)

// ErrRequestorNotPresent is a sentinel error used when getting the Requestor from the request context.
var ErrRequestorNotPresent = errors.New("No requestor was found in the given context")

// ErrRequestNotInternal is returned if Requestor.ClusterMemberTLSCertificateFingerprint is called and the request was not made by
// another cluster member.
var ErrRequestNotInternal = errors.New("The request was not made by another cluster member")
