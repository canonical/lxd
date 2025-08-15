package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
)

// IsTrusted returns true if the value for Trusted in request Info is set to true.
func IsTrusted(ctx context.Context) bool {
	reqInfo := request.GetContextInfo(ctx)
	return reqInfo != nil && reqInfo.Trusted
}

// IsServerAdmin inspects the context and returns true if the request was made over the unix socket, initiated by
// another cluster member, or sent by a client with an unrestricted certificate.
func IsServerAdmin(ctx context.Context, identityCache *identity.Cache) (bool, error) {
	method, err := GetAuthenticationMethodFromCtx(ctx)
	if err != nil {
		return false, err
	}

	// Unix and cluster requests have root access.
	if method == request.ProtocolUnix || method == request.ProtocolCluster {
		return true, nil
	}

	id, err := GetIdentityFromCtx(ctx, identityCache)
	if err != nil {
		// request.ProtocolPKI is only set as the value of request.CtxProtocol when `core.trust_ca_certificates` is
		// true. This setting grants full access to LXD for all clients with CA-signed certificates.
		if method == request.ProtocolPKI && api.StatusErrorCheck(err, http.StatusNotFound) {
			return true, nil
		}

		return false, fmt.Errorf("Failed to get caller identity: %w", err)
	}

	identityType, err := identity.New(id.IdentityType)
	if err != nil {
		return false, fmt.Errorf("Failed to check restricted status of identity: %w", err)
	}

	return identityType.IsAdmin(), nil
}

// GetIdentityFromCtx returns the identity.CacheEntry for the current authenticated caller.
func GetIdentityFromCtx(ctx context.Context, identityCache *identity.Cache) (*identity.CacheEntry, error) {
	authenticationMethod, err := GetAuthenticationMethodFromCtx(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed to get caller authentication method: %w", err)
	}

	// If the caller authenticated via a CA-signed certificate and `core.trust_ca_certificates` is enabled. We still
	// want to check for any potential trust store entries corresponding to their certificate fingerprint.
	if authenticationMethod == request.ProtocolPKI {
		authenticationMethod = api.AuthenticationMethodTLS
	}

	username, err := GetUsernameFromCtx(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed to get caller username: %w", err)
	}

	return identityCache.Get(authenticationMethod, username)
}

// GetUsernameFromCtx inspects the context and returns the username of the initial caller.
// If the request was forwarded by another cluster member, we return the value for `request.CtxForwardedUsername`.
// Otherwise, we return the value for `request.CtxUsername`.
func GetUsernameFromCtx(ctx context.Context) (string, error) {
	reqInfo := request.GetContextInfo(ctx)
	if reqInfo == nil {
		return "", errors.New("Failed to get request info from context")
	}

	// Request protocol cannot be empty.
	if reqInfo.Protocol == "" {
		return "", api.NewStatusError(http.StatusInternalServerError, "Failed to get protocol from the request context")
	}

	// Username cannot be empty.
	if reqInfo.Username == "" {
		return "", api.StatusErrorf(http.StatusInternalServerError, "Failed to get username from the request context")
	}

	// Forwarded username can be empty.
	if reqInfo.Protocol == request.ProtocolCluster && reqInfo.ForwardedUsername != "" {
		return reqInfo.ForwardedUsername, nil
	}

	return reqInfo.Username, nil
}

// GetAuthenticationMethodFromCtx gets the authentication method from the request context.
// If the request was forwarded by another cluster member, the value for `request.CtxForwardedProtocol` is returned.
// Otherwise, `request.CtxProtocol` is returned.
func GetAuthenticationMethodFromCtx(ctx context.Context) (string, error) {
	reqInfo := request.GetContextInfo(ctx)
	if reqInfo == nil {
		return "", errors.New("Failed to get request info from context")
	}

	// Request protocol cannot be empty.
	if reqInfo.Protocol == "" {
		return "", api.NewStatusError(http.StatusInternalServerError, "Failed to get protocol from the request context")
	}

	// Forwarded protocol can be empty.
	if reqInfo.Protocol == request.ProtocolCluster && reqInfo.ForwardedProtocol != "" {
		return reqInfo.ForwardedProtocol, nil
	}

	return reqInfo.Protocol, nil
}

// GetIdentityProviderGroupsFromCtx gets the identity provider groups from the request context if present.
// If the request was forwarded by another cluster member, the value for `request.CtxForwardedIdentityProviderGroups` is
// returned. Otherwise, the value for `request.CtxIdentityProviderGroups` is returned.
func GetIdentityProviderGroupsFromCtx(ctx context.Context) ([]string, error) {
	reqInfo := request.GetContextInfo(ctx)
	if reqInfo == nil {
		return nil, errors.New("Failed to get request info from context")
	}

	// Request protocol cannot be empty.
	if reqInfo.Protocol == "" {
		return nil, api.NewStatusError(http.StatusInternalServerError, "Failed to get protocol from the request context")
	}

	if reqInfo.Protocol == request.ProtocolCluster && reqInfo.ForwardedIdentityProviderGroups != nil {
		return reqInfo.ForwardedIdentityProviderGroups, nil
	}

	return reqInfo.IdentityProviderGroups, nil
}
