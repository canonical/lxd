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
	originAddress                   string
	username                        string
	protocol                        string
	identityProviderGroups          []string
	forwardedOriginAddress          string
	forwardedUsername               string
	forwardedProtocol               string
	forwardedIdentityProviderGroups []string
	clientType                      ClientType
	identity                        *identity.CacheEntry
	identityType                    identity.Type
}

// IsClusterNotification returns true if this an API request coming from a
// cluster node that is notifying us of some user-initiated API request that
// needs some action to be taken on this node as well.
func (r *Requestor) IsClusterNotification() bool {
	return r.ClientType() == ClientTypeNotifier
}

// IsTrusted returns true if the caller is authenticated and false otherwise.
func (r *Requestor) IsTrusted() bool {
	return r.trusted
}

// IsAdmin returns true if the caller is an administrator and false otherwise.
func (r *Requestor) IsAdmin() bool {
	if slices.Contains([]string{ProtocolUnix, ProtocolPKI}, r.CallerProtocol()) {
		return true
	}

	if r.identityType == nil {
		return false
	}

	return r.identityType.IsAdmin()
}

// OriginAddress returns the original address of the caller.
func (r *Requestor) OriginAddress() string {
	if r.forwardedOriginAddress != "" {
		return r.forwardedOriginAddress
	}

	return r.originAddress
}

// CallerUsername returns the original caller Username.
func (r *Requestor) CallerUsername() string {
	if r.forwardedUsername != "" {
		return r.forwardedUsername
	}

	return r.username
}

// CallerProtocol returns the original caller protocol.
func (r *Requestor) CallerProtocol() string {
	if r.forwardedProtocol != "" {
		return r.forwardedProtocol
	}

	return r.protocol
}

// CallerIdentityProviderGroups returns the original caller identity provider groups.
func (r *Requestor) CallerIdentityProviderGroups() []string {
	if r.forwardedIdentityProviderGroups != nil {
		return r.forwardedIdentityProviderGroups
	}

	return r.identityProviderGroups
}

// ClientType returns the client type, which is derived from the "User-Agent" request header.
func (r *Requestor) ClientType() ClientType {
	return r.clientType
}

// EventLifecycleRequestor returns an api.EventLifecycleRequestor representing the original caller.
func (r *Requestor) EventLifecycleRequestor() *api.EventLifecycleRequestor {
	return &api.EventLifecycleRequestor{
		Username: r.CallerUsername(),
		Protocol: r.CallerProtocol(),
		Address:  r.OriginAddress(),
	}
}

// CallerIsEqual returns true if the given Requestor is the same caller as this Requestor.
func (r *Requestor) CallerIsEqual(requestor *Requestor) bool {
	if requestor == nil {
		return false
	}

	return requestor.CallerUsername() == r.CallerUsername() && requestor.CallerProtocol() == r.CallerProtocol()
}

// OperationRequestor returns an [api.OperationRequestor] representing the original caller.
func (r *Requestor) OperationRequestor() *api.OperationRequestor {
	return &api.OperationRequestor{
		Username: r.CallerUsername(),
		Protocol: r.CallerProtocol(),
		Address:  r.OriginAddress(),
	}
}

// CallerIdentity returns the identity.CacheEntry for the caller. It may be nil (e.g. if the protocol is ProtocolUnix).
func (r *Requestor) CallerIdentity() *identity.CacheEntry {
	return r.identity
}

// CallerIdentityType returns the identity.Type corresponding to the CallerIdentity. It may be nil (e.g. if the protocol is ProtocolUnix).
func (r *Requestor) CallerIdentityType() identity.Type {
	return r.identityType
}

// IsForwarded returns true if the request was forwarded from another cluster member and false otherwise.
func (r *Requestor) IsForwarded() bool {
	return r.forwardedOriginAddress != ""
}

// ForwardProxy returns a proxy function that adds the requestor details as headers to be inspected by the receiving cluster member.
func (r *Requestor) ForwardProxy() func(req *http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		req.Header.Add(headerForwardedAddress, r.OriginAddress())

		username := r.CallerUsername()
		if username != "" {
			req.Header.Add(headerForwardedUsername, username)
		}

		protocol := r.CallerProtocol()
		if protocol != "" {
			req.Header.Add(headerForwardedProtocol, protocol)
		}

		identityProviderGroups := r.CallerIdentityProviderGroups()
		if identityProviderGroups != nil {
			b, err := json.Marshal(identityProviderGroups)
			if err == nil {
				req.Header.Add(headerForwardedIdentityProviderGroups, string(b))
			}
		}

		return shared.ProxyFromEnvironment(req)
	}
}

// ClusterMemberTLSCertificateFingerprint returns the TLS certificate fingerprint of the cluster member that
// sent the request. It returns an error if the request was not sent by another cluster member.
func (r *Requestor) ClusterMemberTLSCertificateFingerprint() (string, error) {
	if r.protocol != ProtocolCluster {
		return "", ErrRequestNotInternal
	}

	return r.username, nil
}

// setForwardingDetails validates and sets forwarding details from the request headers.
func (r *Requestor) setForwardingDetails(req *http.Request) error {
	forwardedAddress := req.Header.Get(headerForwardedAddress)
	forwardedUsername := req.Header.Get(headerForwardedUsername)
	forwardedProtocol := req.Header.Get(headerForwardedProtocol)
	forwardedIdentityProviderGroupsJSON := req.Header.Get(headerForwardedIdentityProviderGroups)

	// Requests can only be forwarded from other cluster members.
	if r.protocol != ProtocolCluster {
		// No forwarding headers may be set if the protocol is not ProtocolCluster.
		if forwardedAddress != "" || forwardedUsername != "" || forwardedProtocol != "" || forwardedIdentityProviderGroupsJSON != "" {
			return errors.New("Received forwarded request information from non-cluster member")
		}

		return nil
	}

	var forwardedIdentityProviderGroups []string
	if forwardedIdentityProviderGroupsJSON != "" {
		err := json.Unmarshal([]byte(forwardedIdentityProviderGroupsJSON), &forwardedIdentityProviderGroups)
		if err != nil {
			return fmt.Errorf("Failed to extract forwarded identity provider groups from request header: %w", err)
		}
	}

	r.forwardedOriginAddress = forwardedAddress
	r.forwardedUsername = forwardedUsername
	r.forwardedProtocol = forwardedProtocol
	r.forwardedIdentityProviderGroups = forwardedIdentityProviderGroups
	return nil
}

// setIdentity validates and sets the [identity.CacheEntry] in the Requestor.
// It must only be called when Requestor.trusted is true, and after setForwardingDetails has been called.
func (r *Requestor) setIdentity(cache *identity.Cache) error {
	callerProtocol := r.CallerProtocol()
	callerUsername := r.CallerUsername()

	// No identity cache entry for ProtocolUnix
	if callerProtocol == ProtocolUnix {
		return nil
	}

	// Validate identity is not present if using PKI.
	if callerProtocol == ProtocolPKI {
		_, err := cache.Get(api.AuthenticationMethodTLS, callerUsername)
		if err == nil {
			// If the protocol is PKI but a matching identity is found in the cache, TLS authentication has not fulfilled
			// its contract of only setting this protocol when `core.trust_ca_certifates` is true and the identity is not
			// present in the cache. It is also possible that the identity was not present on another cluster member, but
			// is present on this one.
			return errors.New("Caller authenticated as a trusted CA certificate but an identity cache entry was found")
		}

		return nil
	}

	method := callerProtocol
	switch callerProtocol {
	case ProtocolCluster:
		// If the protocol was cluster, the authentication method is TLS (e.g. mTLS between cluster members).
		method = api.AuthenticationMethodTLS
	case ProtocolDevLXD:
		// For a trusted devlxd request, the only authentication method that can have been used is a bearer token.
		method = api.AuthenticationMethodBearer
	}

	// Expect the method to a remote API method at this point.
	err := identity.ValidateAuthenticationMethod(method)
	if err != nil {
		return fmt.Errorf("Received unexpected caller protocol %q: %w", callerProtocol, err)
	}

	// Get the identity.
	id, err := cache.Get(method, callerUsername)
	if err != nil {
		return fmt.Errorf("Failed to get caller identity: %w", err)
	}

	idType, err := identity.New(id.IdentityType)
	if err != nil {
		return fmt.Errorf("Invalid identity type %q found in identity cache", id.IdentityType)
	}

	r.identity = id
	r.identityType = idType

	return nil
}

// SetRequestor validates the given RequestorArgs against the request, then populates the additional fields
// that requestor contains and sets a requestor in the context.
func SetRequestor(req *http.Request, identityCache *identity.Cache, args RequestorArgs) error {
	clientType := userAgentClientType(req.Header.Get("User-Agent"))

	// Cluster notification with wrong certificate.
	if clientType != ClientTypeNormal && !slices.Contains([]string{ProtocolCluster, ProtocolUnix}, args.Protocol) {
		// XXX: We allow ProtocolUnix because initDataNodeApply() in lxd/init.go uses a local client to join a cluster. initDataNodeApply() is used by 'lxd init' and PUT /1.0/cluster.
		return errors.New("Cluster notification isn't using trusted server certificate")
	}

	r := &Requestor{
		trusted:                args.Trusted,
		originAddress:          req.RemoteAddr,
		username:               args.Username,
		protocol:               args.Protocol,
		identityProviderGroups: args.IdentityProviderGroups,
		clientType:             clientType,
	}

	err := r.setForwardingDetails(req)
	if err != nil {
		return err
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
		// The protocol is empty when calls made to the main API are untrusted.
		if !slices.Contains([]string{ProtocolDevLXD, ""}, callerProtocol) {
			return errors.New("Unsupported protocol set for untrusted request")
		}

		SetContextValue(req, ctxRequestor, r)
		return nil
	}

	// Trusted

	// There must be a protocol.
	if callerProtocol == "" {
		return errors.New("Caller is trusted but no protocol was set")
	}

	// There must be a username.
	if callerUsername == "" {
		return errors.New("Caller is trusted but no username was set")
	}

	err = r.setIdentity(identityCache)
	if err != nil {
		return err
	}

	SetContextValue(req, ctxRequestor, r)
	return nil
}

// GetRequestor gets a Requestor from the request context.
func GetRequestor(ctx context.Context) (*Requestor, error) {
	r, ok := ctx.Value(ctxRequestor).(*Requestor)
	if !ok {
		return nil, ErrRequestorNotPresent
	}

	return r, nil
}
