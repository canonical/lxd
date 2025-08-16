package request

import (
	"context"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared/api"
)

// Requestor is a view of the original caller. An interface is used here to make the caller think twice about accessing
// the raw details (which can be done via type assertion if needed) because e.g. RequestorDetails.Username might be the
// fingerprint of another cluster member, and not the callers actual username.
type Requestor interface {
	// IsTrusted returns true if the caller is authenticated and false otherwise.
	IsTrusted() bool

	// IsAdmin returns true if the caller is an administrator and false otherwise.
	IsAdmin() bool

	// CallerAddress returns the original caller address.
	CallerAddress() string

	// CallerUsername returns the original caller username.
	CallerUsername() string

	// CallerProtocol returns the original caller protocol.
	CallerProtocol() string

	// CallerIdentityProviderGroups returns the original caller identity provider groups
	CallerIdentityProviderGroups() []string

	// CallerIdentity returns the identity.CacheEntry for the caller. It may be nil (e.g. if the protocol is ProtocolUnix).
	CallerIdentity() *identity.CacheEntry

	// CallerIdentityType returns the identity.Type corresponding to the CallerIdentity. It may be nil (e.g. if the protocol is ProtocolUnix).
	CallerIdentityType() identity.Type

	// EventLifecycleRequestor returns an api.EventLifecycleRequestor representing the original caller.
	EventLifecycleRequestor() *api.EventLifecycleRequestor

	// ForwardProxy returns a proxy function that adds the requestor details as headers to be inspected by the receiving cluster member.
	ForwardProxy() func(req *http.Request) (*url.URL, error)

	// IsForwarded returns true if the request was forwarded from another cluster member and false otherwise.
	IsForwarded() bool

	// ForwardingMemberFingerprint returns the fingerprint of the cluster member that forwarded the request. It returns
	// an error if the request was not sent by another cluster member.
	ForwardingMemberFingerprint() (string, error)
}

// RequestorDetails contains information that is gathered when the requestor is initially authenticated.
type RequestorDetails struct {
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

// requestor contains all fields from RequestorDetails, unexported. Plus additional fields gathered from request headers
// set when a request is forwarded between cluster members. It also contains an [identity.CacheEntry] and an
// [identity.Type]. It implements Requestor.
type requestor struct {
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
func InitContextInfo(r *http.Request) *RequestorDetails {
	info := &RequestorDetails{}
	SetContextValue(r, CtxRequestInfo, info)
	return info
}

// GetContextInfo gets the request information from the request context.
func GetContextInfo(ctx context.Context) *RequestorDetails {
	info, ok := ctx.Value(CtxRequestInfo).(*RequestorDetails)
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
