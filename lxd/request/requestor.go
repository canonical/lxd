package request

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared/api"
)

// RequestorAuditor is used for auditing and request forwarding within the cluster, and within operations.
// Permission checks cannot be performed with RequestorAuditor, save only for checking if two requestors are equal.
type RequestorAuditor struct {
	Username      string
	Protocol      string
	OriginAddress string
	IdentityID    *int64
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

// Requestor contains a [RequestorAuditor] and additional unexported fields used for authorization purposes.
// It is set in the request context after authentication via [SetRequestor]. It always represents the original API
// caller, regardless of whether the request is forwarded from another cluster member.
type Requestor struct {
	RequestorAuditor
	identityProviderGroups   []string
	clusterMemberFingerprint string
	clientType               ClientType
	authGroups               []string
	mappedAuthGroups         []string
	projects                 []string
	identityType             identity.Type
	expiresAt                *time.Time
	isForwarded              bool
	isTrusted                bool
}

// IsClusterNotification returns true if this an API request coming from a
// cluster node that is notifying us of some user-initiated API request that
// needs some action to be taken on this node as well.
func (r *Requestor) IsClusterNotification() bool {
	return r.ClientType().IsClusterNotification() && r.isInternal()
}

// IsTrusted returns true if the caller is authenticated and false otherwise.
func (r *Requestor) IsTrusted() bool {
	return r.isTrusted
}

// IsAdmin returns true if the caller is an administrator and false otherwise.
func (r *Requestor) IsAdmin() bool {
	if r == nil {
		return false
	}

	if slices.Contains([]string{ProtocolUnix, ProtocolCluster, ProtocolPKI}, r.Protocol) {
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
	return r.identityProviderGroups
}

// ClientType returns the client type, which is derived from the "User-Agent" request header.
func (r *Requestor) ClientType() ClientType {
	return r.clientType
}

// EventLifecycleRequestor returns an api.EventLifecycleRequestor representing the original caller.
func (r *RequestorAuditor) EventLifecycleRequestor() *api.EventLifecycleRequestor {
	return &api.EventLifecycleRequestor{
		Username: r.Username,
		Protocol: r.Protocol,
		Address:  r.OriginAddress,
	}
}

// CallerIsEqual returns true if the given Requestor is the same caller as this Requestor.
func (r *Requestor) CallerIsEqual(requestor *RequestorAuditor) bool {
	if requestor == nil {
		return false
	}

	return requestor.Username == r.Username && requestor.Protocol == r.Protocol
}

// OperationRequestor returns an [api.OperationRequestor] representing the original caller.
func (r *RequestorAuditor) OperationRequestor() *api.OperationRequestor {
	return &api.OperationRequestor{
		Username: r.Username,
		Protocol: r.Protocol,
		Address:  r.OriginAddress,
	}
}

// CallerIdentityType returns the identity.Type corresponding to the CallerIdentity. It may be nil (e.g. if the protocol is ProtocolUnix).
func (r *Requestor) CallerIdentityType() (identity.Type, error) {
	if r.identityType == nil {
		return nil, errors.New("No identity type present in request details")
	}

	return r.identityType, nil
}

// IsForwarded returns true if the request was forwarded from another cluster member and false otherwise.
func (r *Requestor) IsForwarded() bool {
	return r.isForwarded
}

func (r *Requestor) isInternal() bool {
	return r.clusterMemberFingerprint != ""
}

// SetRequestorHeaders adds the requestor details as forwarded headers on the
// given HTTP request so the receiving cluster member can identify the caller.
func SetRequestorHeaders(r *RequestorAuditor, req *http.Request) {
	req.Header.Add(headerForwardedAddress, r.OriginAddress)

	if r.Username != "" {
		req.Header.Add(headerForwardedUsername, r.Username)
	}

	if r.Protocol != "" {
		req.Header.Add(headerForwardedProtocol, r.Protocol)
	}
}

// ClusterMemberTLSCertificateFingerprint returns the TLS certificate fingerprint of the cluster member that
// sent the request. It returns an error if the request was not sent by another cluster member.
func (r *Requestor) ClusterMemberTLSCertificateFingerprint() (string, error) {
	if !r.isInternal() {
		return "", ErrRequestNotInternal
	}

	return r.clusterMemberFingerprint, nil
}

// setForwardingDetails validates and sets forwarding details from the request headers.
func (r *Requestor) setForwardingDetails(req *http.Request) error {
	forwardedAddress := req.Header.Get(headerForwardedAddress)
	forwardedUsername := req.Header.Get(headerForwardedUsername)
	forwardedProtocol := req.Header.Get(headerForwardedProtocol)

	// Requests can only be forwarded from other cluster members.
	if r.Protocol != ProtocolCluster {
		// No forwarding headers may be set if the protocol is not ProtocolCluster.
		if forwardedAddress != "" || forwardedUsername != "" || forwardedProtocol != "" {
			return errors.New("Received forwarded request information from non-cluster member")
		}

		return nil
	}

	// The protocol is ProtocolCluster, so set the fingerprint of the calling cluster member.
	r.clusterMemberFingerprint = r.Username

	// If the forwarded address is not set, then the request was not forwarded and no forwarding fields need to be
	// set on the requestor.
	if forwardedAddress == "" {
		return nil
	}

	// The request was forwarded, so set isForwarded to true.
	r.isForwarded = true

	// If the forwarded address is set, the forwarded protocol and username must be both be set or both be unset
	// (see SetRequestorHeaders).
	if (forwardedUsername == "" && forwardedProtocol != "") || (forwardedUsername != "" && forwardedProtocol == "") {
		return errors.New("Received forwarded request with missing username or protocol")
	}

	// If this request was forwarded, then [RequestorArgs.Trusted] will have been set to true because we've
	// authenticated the certificate of the forwarding cluster member. However, if the forwarding member did not
	// include a username or protocol header, this can only be because the original request was not authenticated!!
	//
	// In this case, set trusted to false. This means that an untrusted request will remain untrusted throughout
	// the cluster (provided the request context is used appropriately).
	if forwardedUsername == "" && forwardedProtocol == "" {
		r.isTrusted = false
	}

	// Set the origin address, username, and protocol to the forwarded values.
	r.OriginAddress = forwardedAddress
	r.Username = forwardedUsername
	r.Protocol = forwardedProtocol

	return nil
}

// setIdentity validates and sets the [identity.CacheEntry] in the Requestor.
// It must only be called when Requestor.trusted is true, and after setForwardingDetails has been called.
func (r *Requestor) setIdentity(ctx context.Context, hook RequestorHook) error {
	// If the caller is already an admin by virtue of their protocol, there is no reason to run the DB hook.
	if r.IsAdmin() {
		return nil
	}

	method := r.Protocol
	if method == ProtocolDevLXD {
		// For a trusted devlxd request, the only authentication method that can have been used is a bearer token.
		method = api.AuthenticationMethodBearer
	}

	// Expect the method to a remote API method at this point.
	err := identity.ValidateAuthenticationMethod(method)
	if err != nil {
		return fmt.Errorf("Received unexpected caller protocol %q: %w", r.Protocol, err)
	}

	if hook == nil {
		return errors.New("Requestor hook must be set")
	}

	// Get the identity details.
	res, err := hook(ctx, method, r.Username)
	if err != nil {
		return fmt.Errorf("Failed getting identity details: %w", err)
	}

	r.IdentityID = &res.IdentityID
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
		return errors.New("Cluster notification is not using trusted server certificate")
	}

	r := &Requestor{
		RequestorAuditor: RequestorAuditor{
			Username:      args.Username,
			Protocol:      args.Protocol,
			OriginAddress: req.RemoteAddr,
		},
		isTrusted:  args.Trusted,
		clientType: clientType,
		expiresAt:  args.ExpiresAt,
	}

	err := r.setForwardingDetails(req)
	if err != nil {
		return err
	}

	// Handle untrusted case
	if !r.isTrusted {
		// If the caller is not trusted, there should not be a username.
		if r.Username != "" {
			return errors.New("Caller is not trusted but a username was set")
		}

		// The only allowed protocols for the untrusted case are ProtocolDevLXD, or empty.
		// The protocol is empty when calls made to the main API are untrusted.
		if !slices.Contains([]string{ProtocolDevLXD, ""}, r.Protocol) {
			return errors.New("Unsupported protocol set for untrusted request")
		}

		SetContextValue(req, ctxRequestor, r)
		return nil
	}

	// Trusted

	// There must be a protocol.
	if r.Protocol == "" {
		return errors.New("Caller is trusted but no protocol was set")
	}

	// There must be a username.
	if r.Username == "" {
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
func GetRequestorAuditor(ctx context.Context) (*RequestorAuditor, error) {
	val := ctx.Value(ctxRequestor)
	if val == nil {
		return nil, ErrRequestorNotPresent
	}

	requestor, ok := val.(*Requestor)
	if ok {
		return &requestor.RequestorAuditor, nil
	}

	auditor, ok := val.(*RequestorAuditor)
	if !ok {
		return nil, ErrRequestorNotPresent
	}

	return auditor, nil
}

// WithRequestorAuditor is used to set the [RequestorAuditor] in the given context.
// This is used by operations to set the requestor in the context of an async task.
func WithRequestorAuditor(ctx context.Context, requestor *RequestorAuditor) context.Context {
	return context.WithValue(ctx, ctxRequestor, requestor)
}
