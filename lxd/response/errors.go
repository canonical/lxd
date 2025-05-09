package response

import (
	"errors"
)

// RequestForwarder handles the forwarding of the request.
type RequestForwarder func() Response

// RequestForwardRequiredError is an error that indicates that a request
// needs to be forwarded to another node in the cluster. This is used when
// the request cannot be handled locally and needs to be forwarded to
// another node in the cluster.
type RequestForwardRequiredError struct {
	// The address of the node to forward the request to.
	address string

	// The function to call to forward the request.
	doForward RequestForwarder
}

// NewRequestForwardRequiredError creates a new RequestForwardRequiredError with the given address
// and request forwarder.
func NewRequestForwardRequiredError(address string, forwarder RequestForwarder) error {
	if forwarder == nil {
		return errors.New("Invalid forward request: No request forwarder provided")
	}

	return &RequestForwardRequiredError{
		address:   address,
		doForward: forwarder,
	}
}

// Error returns the error as a string.
func (e RequestForwardRequiredError) Error() string {
	if e.address != "" {
		return "Request must be forwarded to a cluster member with address " + e.address
	}

	return "Request must be forwarded to another cluster member"
}

// ForwardedResponse returns a response that forwards the request to the
// specified address. This is used when the request cannot be handled locally
// and needs to be forwarded to another node in the cluster.
func (e RequestForwardRequiredError) ForwardedResponse() Response {
	return e.doForward()
}
