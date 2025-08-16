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

// CallerAddress returns the original caller address.
func (r *Requestor) CallerAddress() string {
	if r.forwardedSourceAddress != "" {
		return r.forwardedSourceAddress
	}

	return r.sourceAddress
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

// EventLifecycleRequestor returns an api.EventLifecycleRequestor representing the original caller.
func (r *Requestor) EventLifecycleRequestor() *api.EventLifecycleRequestor {
	return &api.EventLifecycleRequestor{
		Username: r.CallerUsername(),
		Protocol: r.CallerProtocol(),
		Address:  r.CallerAddress(),
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
	return r.forwardedSourceAddress != ""
}

// ForwardProxy returns a proxy function that adds the requestor details as headers to be inspected by the receiving cluster member.
func (r *Requestor) ForwardProxy() func(req *http.Request) (*url.URL, error) {
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

// ForwardingMemberFingerprint returns the fingerprint of the cluster member that forwarded the request. It returns
// an error if the request was not sent by another cluster member.
func (r *Requestor) ForwardingMemberFingerprint() (string, error) {
	if r.protocol != ProtocolCluster {
		return "", ErrRequestNotInternal
	}

	return r.username, nil
}

// setForwardingDetails validates and sets forwarding details from the request headers.
func (r *Requestor) setForwardingDetails(req *http.Request) error {
	forwardedAddress := req.Header.Get(HeaderForwardedAddress)
	forwardedUsername := req.Header.Get(HeaderForwardedUsername)
	forwardedProtocol := req.Header.Get(HeaderForwardedProtocol)
	forwardedIdentityProviderGroupsJSON := req.Header.Get(HeaderForwardedIdentityProviderGroups)

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

	r.forwardedSourceAddress = forwardedAddress
	r.forwardedUsername = forwardedUsername
	r.forwardedProtocol = forwardedProtocol
	r.forwardedIdentityProviderGroups = forwardedIdentityProviderGroups
	return nil
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
