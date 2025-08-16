package request

import (
	"errors"
)

// ErrRequestorNotPresent is a sentinel error used when getting the Requestor from the request context.
var ErrRequestorNotPresent = errors.New("No requestor was found in the given context")
