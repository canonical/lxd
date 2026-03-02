package operations

import (
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
)

// opRequestor implements a subset of methods from [request.Requestor].
// It is used as the operation requestor so that authorization checks cannot be called using the operation context.
// This forces us to perform any authorization checks ahead of running an operation, so that we fail fast.
// In addition, this simplifies recreation of the requestor when loading an operation from the database.
type opRequestor struct {
	identityID int64
	r          *api.OperationRequestor
}

// OriginAddress returns the original address of the requestor. It may be empty if an operation is reconstructed from
// the database.
func (o *opRequestor) OriginAddress() string {
	if o == nil || o.r == nil {
		return ""
	}

	return o.r.Address
}

// CallerIdentityID returns the ID of the identity that initiated the operation.
func (o *opRequestor) CallerIdentityID() int64 {
	if o == nil {
		return -1
	}

	return o.identityID
}

// CallerProtocol returns the protocol of the identity that initiated the operation.
func (o *opRequestor) CallerProtocol() string {
	if o == nil || o.r == nil {
		return ""
	}

	return o.r.Protocol
}

// CallerUsername returns the username of the identity that initiated the operation.
func (o *opRequestor) CallerUsername() string {
	if o == nil || o.r == nil {
		return ""
	}

	return o.r.Username
}

// CallerIsEqual returns true if the calling [request.Requestor] is the same identity as the operation requestor.
func (o *opRequestor) CallerIsEqual(caller request.RequestorAuditor) bool {
	if o == nil || o.r == nil || caller == nil {
		return false
	}

	return o.r.Username == caller.CallerUsername() && o.r.Protocol == caller.CallerProtocol()
}

// EventLifecycleRequestor returns the [api.EventLifecycleRequestor] for the operation.
func (o *opRequestor) EventLifecycleRequestor() *api.EventLifecycleRequestor {
	if o == nil || o.r == nil {
		return nil
	}

	return &api.EventLifecycleRequestor{
		Username: o.r.Username,
		Protocol: o.r.Protocol,
		Address:  o.r.Address,
	}
}

// OperationRequestor returns the [api.OperationRequestor] for the operation.
func (o *opRequestor) OperationRequestor() *api.OperationRequestor {
	if o == nil {
		return nil
	}

	return o.r
}
