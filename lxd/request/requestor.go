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

// RequestorAuditor contains fields used to reference an identity for auditing purposes.
// The IdentityID field is nilable because e.g. a unix requestor is not associated with an identity.
type RequestorAuditor struct {
	IdentityID    *int64
	OriginAddress string
	Username      string
	Protocol      string
}

// Requestor encompasses all known information about a requestor.
type Requestor struct {
	*RequestorAuditor
	IsTrusted              bool
	IdentityProviderGroups []string
	AuthorizationGroups    []string
	mappedAuthGroups       []string
	Projects               []string
	IdentityType           identity.Type
	ExpiresAt              *time.Time
}

// IsAdmin returns true if the caller is an administrator and false otherwise.
func (r *Requestor) IsAdmin() bool {
	if slices.Contains([]string{ProtocolUnix, ProtocolCluster, ProtocolPKI}, r.Protocol) {
		return true
	}

	if r.IdentityType == nil {
		return false
	}

	return r.IdentityType.IsAdmin()
}

// CallerEffectiveAuthorizationGroupNames returns a list of all authorization groups that the identity belongs to either directly or via a mapped identity provider group.
func (r *Requestor) CallerEffectiveAuthorizationGroupNames() []string {
	effectiveGroups := r.AuthorizationGroups
	for _, mappedGroup := range r.mappedAuthGroups {
		if !slices.Contains(effectiveGroups, mappedGroup) {
			effectiveGroups = append(effectiveGroups, mappedGroup)
		}
	}

	return effectiveGroups
}

// UserAgentClientType returns the client type, which is derived from the "User-Agent" request header.
func UserAgentClientType(r *http.Request) ClientType {
	_, err := GetContextValue[string](r.Context(), ctxClusterMemberCertificateFingerprint)
	if err != nil {
		return ClientTypeNormal
	}

	return userAgentClientType(r.UserAgent())
}

// EventLifecycleRequestor returns an api.EventLifecycleRequestor representing the original caller.
func (r *RequestorAuditor) EventLifecycleRequestor() *api.EventLifecycleRequestor {
	return &api.EventLifecycleRequestor{
		Username: r.Username,
		Protocol: r.Protocol,
		Address:  r.OriginAddress,
	}
}

// CallerIsEqual returns true if the given RequestorAuditor is the same caller as this RequestorAuditor.
func (r *RequestorAuditor) CallerIsEqual(requestor *RequestorAuditor) bool {
	if r == nil || requestor == nil {
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
	if r.IdentityType == nil {
		return nil, errors.New("No identity type present in request details")
	}

	return r.IdentityType, nil
}

// CallerIdentityID returns the database ID of the calling identity (it is always zero if the calling identity is using
// an admin protocol - cluster, unix, pki).
func (r *Requestor) CallerIdentityID() *int64 {
	return r.IdentityID
}

// IsForwarded returns true if the request was forwarded from another cluster member and false otherwise.
func IsForwarded(r *http.Request) bool {
	_, err := GetContextValue[string](r.Context(), ctxClusterMemberCertificateFingerprint)
	if err != nil {
		return false
	}

	return r.Header.Get(headerForwardedAddress) != ""
}

// RequestorForwardProxy returns a proxy function that adds the requestor details as headers to be inspected by the receiving cluster member.
func RequestorForwardProxy(r RequestorAuditor) func(req *http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		req.Header.Add(headerForwardedAddress, r.OriginAddress)

		if r.Username != "" {
			req.Header.Add(headerForwardedUsername, r.Username)
		}

		if r.Protocol != "" {
			req.Header.Add(headerForwardedProtocol, r.Protocol)
		}

		return shared.ProxyFromEnvironment(req)
	}
}

// ClusterMemberTLSCertificateFingerprint returns the TLS certificate fingerprint of the cluster member that
// sent the request. It returns an error if the request was not sent by another cluster member.
func ClusterMemberTLSCertificateFingerprint(r *http.Request) (string, error) {
	fingerprint, err := GetContextValue[string](r.Context(), ctxClusterMemberCertificateFingerprint)
	if err != nil {
		return "", ErrRequestNotInternal
	}

	return fingerprint, nil
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

	// Protocol is cluster. Set cluster member fingerprint in context.
	SetContextValue(req, ctxClusterMemberCertificateFingerprint, r.Username)

	// RequestorForwardProxy always sets the forwarded address. If there is no forwarded address, then it
	// is a cluster internal request initiated by the calling member.
	if forwardedAddress != "" {
		// The forwarded username and protocol are only set if not empty.
		// If they are empty, the calling member was not able to authenticate the caller.
		// This can happen if forwarding an operation websocket request to another member, where authentication is
		// performed via operation secret instead.
		if forwardedUsername == "" || forwardedProtocol == "" {
			r.IsTrusted = false
		}

		r.OriginAddress = forwardedAddress
		r.Username = forwardedUsername
		r.Protocol = forwardedProtocol
	}

	return nil
}

// setIdentity validates and sets the [identity.CacheEntry] in the RequestorAuditor.
// It must only be called when RequestorAuditor.trusted is true, and after setForwardingDetails has been called.
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
		return errors.New("RequestorAuditor hook must be set")
	}

	// Get the identity details.
	res, err := hook(ctx, method, r.Username)
	if err != nil {
		return fmt.Errorf("Failed to get identity details: %w", err)
	}

	r.IdentityID = &res.IdentityID
	r.IdentityType = res.IdentityType
	r.AuthorizationGroups = res.AuthGroups
	r.mappedAuthGroups = res.EffectiveAuthGroups
	r.IdentityProviderGroups = res.IdentityProviderGroups
	r.Projects = res.Projects

	return nil
}

// SetRequestor validates the given RequestorArgs against the request, then populates the additional fields
// that requestor contains and sets a requestor in the context.
func SetRequestor(req *http.Request, hook RequestorHook, args RequestorArgs) error {
	r := &Requestor{
		RequestorAuditor: &RequestorAuditor{
			OriginAddress: req.RemoteAddr,
			Username:      args.Username,
			Protocol:      args.Protocol,
		},
		IsTrusted: args.Trusted,
		ExpiresAt: args.ExpiresAt,
	}

	err := r.setForwardingDetails(req)
	if err != nil {
		return err
	}

	SetContextValue(req, ctxRequestorAuditor, r.RequestorAuditor)

	// Handle untrusted case
	if !r.IsTrusted {
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

// GetRequestor gets a RequestorAuditor from the request context.
func GetRequestor(ctx context.Context) (*Requestor, error) {
	r, ok := ctx.Value(ctxRequestor).(*Requestor)
	if !ok {
		return nil, ErrRequestorNotPresent
	}

	return r, nil
}

// GetRequestorAuditor gets an RequestorAuditor from the context.
func GetRequestorAuditor(ctx context.Context) (*RequestorAuditor, error) {
	r, ok := ctx.Value(ctxRequestorAuditor).(*RequestorAuditor)
	if !ok {
		return nil, ErrRequestorNotPresent
	}

	return r, nil
}

// WithRequestorAuditor is used to set the requestor in the given context.
// This is used by operations to set the requestor in the context of an async task.
func WithRequestorAuditor(ctx context.Context, requestor *RequestorAuditor) context.Context {
	return context.WithValue(ctx, ctxRequestorAuditor, requestor)
}
