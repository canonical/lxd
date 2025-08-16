package request

import (
	"context"
	"net/http"

	"github.com/canonical/lxd/lxd/identity"
)

// RequestorArgs contains information that is gathered when the requestor is initially authenticated.
type RequestorArgs struct {
	// Trusted indicates whether the request was authenticated or not. This is always set (and is false by default).
	Trusted bool

	// Username is the caller username. If the request was forwarded this may be the certificate fingerprint of another
	// cluster member. It is only set if the Trusted is true.
	Username string

	// Protocol is the caller protocol. If the request was forwarded this may be the certificate fingerprint of another
	// cluster member. It is only set if the Trusted is true.
	Protocol string

	// IdentityProviderGroups contains identity provider groups. These are only set if the caller protocol is
	// [api.AuthenticationMethodOIDC]. They are centrally defined groups that may map to LXD groups via identity
	// provider group mappings.
	IdentityProviderGroups []string
}

// Requestor contains all fields from RequestorArgs, unexported. Plus additional fields gathered from request headers
// set when a request is forwarded between cluster members. It also contains an [identity.CacheEntry] and an
// [identity.Type], which are set during SetRequestor.
type Requestor struct {
	trusted                         bool
	sourceAddress                   string
	username                        string
	protocol                        string
	identityProviderGroups          []string
	forwardedSourceAddress          string
	forwardedUsername               string
	forwardedProtocol               string
	forwardedIdentityProviderGroups []string
	identity                        *identity.CacheEntry
	identityType                    identity.Type
}

// InitContextInfo sets an empty Info in the request context.
func InitContextInfo(r *http.Request) *RequestorArgs {
	info := &RequestorArgs{}
	SetContextValue(r, CtxRequestInfo, info)
	return info
}

// GetContextInfo gets the request information from the request context.
func GetContextInfo(ctx context.Context) *RequestorArgs {
	info, ok := ctx.Value(CtxRequestInfo).(*RequestorArgs)
	if !ok {
		return nil
	}

	return info
}

// IsRequestContext checks if the given context is a request context.
// This is determined by checking the presence of the request information in the context.
func IsRequestContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}

	return GetContextInfo(ctx) != nil
}
