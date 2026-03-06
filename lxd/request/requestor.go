package request

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// RequestorAuditor is a subset of methods implemented by [Requestor].
// It is used for auditing, request forwarding within the cluster, and within operations.
// Permission checks cannot be performed with the requestor auditor, save only for checking if two requestors are equal.
type RequestorAuditor interface {
	OriginAddress() string
	CallerUsername() string
	CallerProtocol() string
	EventLifecycleRequestor() *api.EventLifecycleRequestor
	OperationRequestor() *api.OperationRequestor
	CallerIsEqual(requestor RequestorAuditor) bool
	CallerIdentityID() int64
}

// RequestorHook is the signature of a hook that is passed into calls to [SetRequestor].
// This allows the caller to specify how to get authorization information about an identity that has successfully authenticated.
type RequestorHook func(ctx context.Context, authenticationMethod string, identifier string) (result *RequestorHookResult, err error)

// RequestorHookResult contains identity and access management details returned by the [RequestorHook].
type RequestorHookResult struct {
	IdentityID             int64
	IdentityType           identity.Type
	AuthGroups             []string
	IdentityProviderGroups []string
	EffectiveAuthGroups    []string
	Projects               []string
}

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

	// ExpiresAt is the expiration time of the credential used to authenticate the caller.
	// It is set only when the client is trusted and the authentication method is either
	// [api.AuthenticationMethodBearer] or [api.AuthenticationMethodTLS].
	ExpiresAt *time.Time
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
	identityID                      int64
	authGroups                      []string
	mappedAuthGroups                []string
	projects                        []string
	identityType                    identity.Type
	expiresAt                       *time.Time
}

// IsClusterNotification returns true if this an API request coming from a
// cluster node that is notifying us of some user-initiated API request that
// needs some action to be taken on this node as well.
func (r *Requestor) IsClusterNotification() bool {
	return r.ClientType().IsClusterNotification()
}

// IsTrusted returns true if the caller is authenticated and false otherwise.
func (r *Requestor) IsTrusted() bool {
	return r.trusted
}

// IsAdmin returns true if the caller is an administrator and false otherwise.
func (r *Requestor) IsAdmin() bool {
	if slices.Contains([]string{ProtocolUnix, ProtocolCluster, ProtocolPKI}, r.CallerProtocol()) {
		return true
	}

	if r.identityType == nil {
		return false
	}

	return r.identityType.IsAdmin()
}

// ExpiresAt returns the expiration date of the credential used to authenticate the caller.
// Returns nil if the caller is not authenticated using a bearer token or TLS.
func (r *Requestor) ExpiresAt() *time.Time {
	return r.expiresAt
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

// CallerAuthorizationGroupNames returns the LXD authorization groups that the requestor belongs to.
func (r *Requestor) CallerAuthorizationGroupNames() []string {
	return r.authGroups
}

// CallerEffectiveAuthorizationGroupNames returns a list of all authorization groups that the identity belongs to either directly or via a mapped identity provider group.
func (r *Requestor) CallerEffectiveAuthorizationGroupNames() []string {
	effectiveGroups := r.CallerAuthorizationGroupNames()
	for _, mappedGroup := range r.mappedAuthGroups {
		if !slices.Contains(effectiveGroups, mappedGroup) {
			effectiveGroups = append(effectiveGroups, mappedGroup)
		}
	}

	return effectiveGroups
}

// CallerAllowedProjectNames returns a list of names of projects that the caller has access to.
func (r *Requestor) CallerAllowedProjectNames() []string {
	return r.projects
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
func (r *Requestor) CallerIsEqual(requestor RequestorAuditor) bool {
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

// CallerIdentityType returns the identity.Type corresponding to the CallerIdentity. It may be nil (e.g. if the protocol is ProtocolUnix).
func (r *Requestor) CallerIdentityType() (identity.Type, error) {
	if r.identityType == nil {
		return nil, errors.New("No identity type present in request details")
	}

	return r.identityType, nil
}

// CallerIdentityID returns the database ID of the calling identity (it is always zero if the calling identity is using
// an admin protocol - cluster, unix, pki).
func (r *Requestor) CallerIdentityID() int64 {
	return r.identityID
}

// IsForwarded returns true if the request was forwarded from another cluster member and false otherwise.
func (r *Requestor) IsForwarded() bool {
	return r.forwardedOriginAddress != ""
}

// RequestorForwardProxy returns a proxy function that adds the requestor details as headers to be inspected by the receiving cluster member.
func RequestorForwardProxy(r RequestorAuditor) func(req *http.Request) (*url.URL, error) {
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

	// Requests can only be forwarded from other cluster members.
	if r.protocol != ProtocolCluster {
		// No forwarding headers may be set if the protocol is not ProtocolCluster.
		if forwardedAddress != "" || forwardedUsername != "" || forwardedProtocol != "" {
			return errors.New("Received forwarded request information from non-cluster member")
		}

		return nil
	}

	r.forwardedOriginAddress = forwardedAddress
	r.forwardedUsername = forwardedUsername
	r.forwardedProtocol = forwardedProtocol

	// If this request was forwarded, then [RequestorArgs.Trusted] will have been set to true because we've
	// authenticated the certificate of the forwarding cluster member. However, if the forwarding member did not
	// include a username or protocol header, this can only be because the original request was not authenticated!!
	//
	// In this case, set trusted to false. This means that an untrusted request will remain untrusted throughout
	// the cluster (provided the request context is used appropriately).
	if forwardedAddress != "" && (forwardedUsername == "" || forwardedProtocol == "") {
		r.trusted = false
	}

	return nil
}

// setIdentity validates and sets the [identity.CacheEntry] in the Requestor.
// It must only be called when Requestor.trusted is true, and after setForwardingDetails has been called.
func (r *Requestor) setIdentity(ctx context.Context, hook RequestorHook) error {
	// If the caller is already an admin by virtue of their protocol, there is no reason to run the DB hook.
	if r.IsAdmin() {
		return nil
	}

	method := r.CallerProtocol()
	if method == ProtocolDevLXD {
		// For a trusted devlxd request, the only authentication method that can have been used is a bearer token.
		method = api.AuthenticationMethodBearer
	}

	// Expect the method to a remote API method at this point.
	err := identity.ValidateAuthenticationMethod(method)
	if err != nil {
		return fmt.Errorf("Received unexpected caller protocol %q: %w", r.CallerProtocol(), err)
	}

	if hook == nil {
		return errors.New("Requestor hook must be set")
	}

	// Get the identity details.
	res, err := hook(ctx, method, r.CallerUsername())
	if err != nil {
		return fmt.Errorf("Failed to get identity details: %w", err)
	}

	r.identityID = res.IdentityID
	r.identityType = res.IdentityType
	r.authGroups = res.AuthGroups
	r.mappedAuthGroups = res.EffectiveAuthGroups
	r.identityProviderGroups = res.IdentityProviderGroups
	r.projects = res.Projects

	return nil
}

// SetRequestor validates the given RequestorArgs against the request, then populates the additional fields
// that requestor contains and sets a requestor in the context.
func SetRequestor(req *http.Request, hook RequestorHook, args RequestorArgs) error {
	clientType := userAgentClientType(req.Header.Get("User-Agent"))

	// Cluster notification with wrong certificate.
	if clientType != ClientTypeNormal && !slices.Contains([]string{ProtocolCluster, ProtocolUnix}, args.Protocol) {
		// XXX: We allow ProtocolUnix because initDataNodeApply() in lxd/init.go uses a local client to join a cluster. initDataNodeApply() is used by 'lxd init' and PUT /1.0/cluster.
		return errors.New("Cluster notification isn't using trusted server certificate")
	}

	r := &Requestor{
		trusted:       args.Trusted,
		originAddress: req.RemoteAddr,
		username:      args.Username,
		protocol:      args.Protocol,
		clientType:    clientType,
		expiresAt:     args.ExpiresAt,
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

	err = r.setIdentity(req.Context(), hook)
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

// GetRequestorAuditor gets an RequestorAuditor from the context.
func GetRequestorAuditor(ctx context.Context) (RequestorAuditor, error) {
	r, ok := ctx.Value(ctxRequestor).(RequestorAuditor)
	if !ok {
		return nil, ErrRequestorNotPresent
	}

	return r, nil
}

// WithRequestor is used to set the requestor in the given context.
// This is used by operations to set the requestor in the context of an async task.
func WithRequestor(ctx context.Context, requestor RequestorAuditor) context.Context {
	return context.WithValue(ctx, ctxRequestor, requestor)
}
