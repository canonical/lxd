package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared"
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

// IsTrusted returns true if the caller is authenticated and false otherwise.
func (r requestor) IsTrusted() bool {
	return r.trusted
}

// IsAdmin returns true if the caller is an administrator and false otherwise.
func (r requestor) IsAdmin() bool {
	if slices.Contains([]string{ProtocolUnix, ProtocolPKI}, r.CallerProtocol()) {
		return true
	}

	if r.identityType == nil {
		return false
	}

	return r.identityType.IsAdmin()
}

// CallerAddress returns the original caller address.
func (r requestor) CallerAddress() string {
	if r.forwardedSourceAddress != "" {
		return r.forwardedSourceAddress
	}

	return r.sourceAddress
}

// CallerUsername returns the original caller Username.
func (r requestor) CallerUsername() string {
	if r.forwardedUsername != "" {
		return r.forwardedUsername
	}

	return r.username
}

// CallerProtocol returns the original caller protocol.
func (r requestor) CallerProtocol() string {
	if r.forwardedProtocol != "" {
		return r.forwardedProtocol
	}

	return r.protocol
}

// CallerIdentityProviderGroups returns the original caller identity provider groups.
func (r requestor) CallerIdentityProviderGroups() []string {
	if r.forwardedIdentityProviderGroups != nil {
		return r.forwardedIdentityProviderGroups
	}

	return r.identityProviderGroups
}

// EventLifecycleRequestor returns an api.EventLifecycleRequestor representing the original caller.
func (r requestor) EventLifecycleRequestor() *api.EventLifecycleRequestor {
	return &api.EventLifecycleRequestor{
		Username: r.CallerUsername(),
		Protocol: r.CallerProtocol(),
		Address:  r.CallerAddress(),
	}
}

// CallerIdentity returns the identity.CacheEntry for the caller. It may be nil (e.g. if the protocol is ProtocolUnix).
func (r requestor) CallerIdentity() *identity.CacheEntry {
	return r.identity
}

// CallerIdentityType returns the identity.Type corresponding to the CallerIdentity. It may be nil (e.g. if the protocol is ProtocolUnix).
func (r requestor) CallerIdentityType() identity.Type {
	return r.identityType
}

// IsForwarded returns true if the request was forwarded from another cluster member and false otherwise.
func (r requestor) IsForwarded() bool {
	return r.forwardedSourceAddress != ""
}

// ForwardProxy returns a proxy function that adds the requestor details as headers to be inspected by the receiving cluster member.
func (r requestor) ForwardProxy() func(req *http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		req.Header.Add(HeaderForwardedAddress, r.CallerAddress())

		username := r.CallerUsername()
		if username != "" {
			req.Header.Add(HeaderForwardedUsername, username)
		}

		protocol := r.CallerProtocol()
		if protocol != "" {
			req.Header.Add(HeaderForwardedProtocol, protocol)
		}

		identityProviderGroups := r.CallerIdentityProviderGroups()
		if identityProviderGroups != nil {
			b, err := json.Marshal(identityProviderGroups)
			if err == nil {
				req.Header.Add(HeaderForwardedIdentityProviderGroups, string(b))
			}
		}

		return shared.ProxyFromEnvironment(req)
	}
}

// getForwardedRequestorDetails gets requestor details from the request headers. It should only be called when the request
// was sent from another cluster member.
func getForwardedRequestorDetails(r *http.Request) (username string, protocol string, address string, identityProviderGroups []string, err error) {
	address = r.Header.Get(HeaderForwardedAddress)
	username = r.Header.Get(HeaderForwardedUsername)
	protocol = r.Header.Get(HeaderForwardedProtocol)

	forwardedIdentityProviderGroupsJSON := r.Header.Get(HeaderForwardedIdentityProviderGroups)
	if forwardedIdentityProviderGroupsJSON != "" {
		err = json.Unmarshal([]byte(forwardedIdentityProviderGroupsJSON), &identityProviderGroups)
		if err != nil {
			return "", "", "", nil, fmt.Errorf("Failed to extract forwarded identity provider groups from request header: %w", err)
		}
	}

	return username, protocol, address, identityProviderGroups, nil
}

// SetRequestorDetails validates the given RequestorDetails against the request, then populates the additional fields
// that requestor contains and sets a requestor in the context.
func SetRequestorDetails(req *http.Request, identityCache *identity.Cache, details RequestorDetails) error {
	r := requestor{
		trusted:                details.Trusted,
		sourceAddress:          req.RemoteAddr,
		username:               details.Username,
		protocol:               details.Protocol,
		identityProviderGroups: details.IdentityProviderGroups,
	}

	// Requests can only be forwarded from other cluster members.
	if req.Header.Get(HeaderForwardedAddress) != "" && r.protocol != ProtocolCluster {
		return errors.New("Received forwarded request information from non-cluster member")
	}

	// Get forwarding details.
	var err error
	if r.protocol == ProtocolCluster {
		r.forwardedUsername, r.forwardedProtocol, r.forwardedSourceAddress, r.forwardedIdentityProviderGroups, err = getForwardedRequestorDetails(req)
		if err != nil {
			return fmt.Errorf("Failed to get requestor forwarding details: %w", err)
		}
	}

	callerUsername := r.CallerUsername()
	callerProtocol := r.CallerProtocol()

	// Handle untrusted case
	if !r.trusted {
		// If the caller is not trusted, there should not be a username.
		if callerUsername != "" {
			return errors.New("Caller is not trusted but a username was set")
		}

		// The only allowed protocols for the untrusted case are ProtocolDevLXD, or empty.
		if !slices.Contains([]string{ProtocolDevLXD, ""}, callerProtocol) {
			return errors.New("Unsupported protocol set for untrusted request")
		}

		SetContextValue(req, ctxRequestor, r)
		return nil
	}

	// Trusted

	// DevLXD requests must always be untrusted.
	if callerProtocol == ProtocolDevLXD {
		return errors.New("Received trusted request over DevLXD")
	}

	// There must be a protocol.
	if callerProtocol == "" {
		return errors.New("Caller is trusted but no protocol was set")
	}

	// There must be a username.
	if callerUsername == "" {
		return errors.New("Caller is trusted but no username was set")
	}

	// No identity cache entry for ProtocolUnix
	if callerProtocol == ProtocolUnix {
		SetContextValue(req, ctxRequestor, r)
		return nil
	}

	// Validate identity is not present if using PKI.
	if callerProtocol == ProtocolPKI {
		_, err := identityCache.Get(api.AuthenticationMethodTLS, callerUsername)
		if err == nil {
			// If the protocol is PKI but a matching identity is found in the cache, TLS authentication has not fulfilled
			// its contract of only setting this protocol when `core.trust_ca_certifates` is true and the identity is not
			// present in the cache. It is also possible that the identity was not present on another cluster member, but
			// is present on this one.
			return errors.New("Caller authenticated as a trusted CA certificate but an identity cache entry was found")
		}

		SetContextValue(req, ctxRequestor, r)
		return nil
	}

	// If the protocol was cluster, the authentication method is TLS.
	method := callerProtocol
	if callerProtocol == ProtocolCluster {
		method = api.AuthenticationMethodTLS
	}

	// Expect the method to a remote API method at this point.
	err = identity.ValidateAuthenticationMethod(method)
	if err != nil {
		return fmt.Errorf("Received unexpected caller protocol %q: %w", callerProtocol, err)
	}

	// Get the identity.
	id, err := identityCache.Get(method, callerUsername)
	if err != nil {
		return fmt.Errorf("Failed to get caller identity: %w", err)
	}

	idType, err := identity.New(id.IdentityType)
	if err != nil {
		return fmt.Errorf("Invalid identity type %q found in identity cache", id.IdentityType)
	}

	r.identity = id
	r.identityType = idType

	SetContextValue(req, ctxRequestor, r)
	return nil
}

// GetRequestor gets a Requestor from the request context.
func GetRequestor(ctx context.Context) (Requestor, error) {
	info, ok := ctx.Value(ctxRequestor).(requestor)
	if !ok {
		return nil, ErrRequestorNotPresent
	}

	return info, nil
}
