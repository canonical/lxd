package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
)

// IsTrusted returns true if the value for `request.CtxTrusted` is set and is true.
func IsTrusted(ctx context.Context) bool {
	// The zero-value of a bool is false, so even if it isn't present in the context we'll return false.
	// This will only return true when the value is present and is true.
	trusted, _ := request.GetCtxValue[bool](ctx, request.CtxTrusted)
	return trusted
}

// IsServerAdmin inspects the context and returns true if the request was made over the unix socket, initiated by
// another cluster member, or sent by a client with an unrestricted certificate.
func IsServerAdmin(ctx context.Context) (bool, error) {
	method, err := GetAuthenticationMethodFromCtx(ctx)
	if err != nil {
		return false, err
	}

	// Unix and cluster requests have root access.
	if method == AuthenticationMethodUnix || method == AuthenticationMethodCluster {
		return true, nil
	}

	id, err := GetIdentityFromCtx(ctx)
	if err != nil || id == nil {
		// AuthenticationMethodPKI is only set as the value of request.CtxProtocol when `core.trust_ca_certificates` is
		// true. This setting grants full access to LXD for all clients with CA-signed certificates.
		if method == AuthenticationMethodPKI {
			return true, nil
		}

		return false, fmt.Errorf("Failed to get caller identity: %w", err)
	}

	isRestricted, err := identity.IsRestrictedIdentityType(id.Type)
	if err != nil {
		return false, fmt.Errorf("Failed to check restricted status of identity: %w", err)
	}

	return !isRestricted, nil
}

// GetCertificateFromCtx returns the api.Certificate for the current authenticated caller.
func GetCertificateFromCtx(ctx context.Context) (*api.Certificate, error) {
	return request.GetCtxValue[*api.Certificate](ctx, request.CtxCertificateInfo)
}

// GetIdentityFromCtx returns the api.IdentityInfo for the current authenticated caller.
func GetIdentityFromCtx(ctx context.Context) (*api.IdentityInfo, error) {
	return request.GetCtxValue[*api.IdentityInfo](ctx, request.CtxIdentityInfo)
}

// GetUsernameFromCtx inspects the context and returns the username of the initial caller.
// If the request was forwarded by another cluster member, we return the value for `request.CtxForwardedUsername`.
// Otherwise, we return the value for `request.CtxUsername`.
func GetUsernameFromCtx(ctx context.Context) (string, error) {
	// Request protocol cannot be empty.
	protocol, err := request.GetCtxValue[string](ctx, request.CtxProtocol)
	if err != nil {
		return "", api.StatusErrorf(http.StatusInternalServerError, "Failed getting protocol: %w", err)
	}

	// Username cannot be empty.
	username, err := request.GetCtxValue[string](ctx, request.CtxUsername)
	if err != nil {
		return "", api.StatusErrorf(http.StatusInternalServerError, "Failed getting username: %w", err)
	}

	// Forwarded username can be empty.
	forwardedUsername, _ := request.GetCtxValue[string](ctx, request.CtxForwardedUsername)

	if protocol == AuthenticationMethodCluster && forwardedUsername != "" {
		return forwardedUsername, nil
	}

	return username, nil
}

// GetAuthenticationMethodFromCtx gets the authentication method from the request context.
// If the request was forwarded by another cluster member, the value for `request.CtxForwardedProtocol` is returned.
// Otherwise, `request.CtxProtocol` is returned.
func GetAuthenticationMethodFromCtx(ctx context.Context) (string, error) {
	// Request protocol cannot be empty.
	protocol, err := request.GetCtxValue[string](ctx, request.CtxProtocol)
	if err != nil {
		return "", api.StatusErrorf(http.StatusInternalServerError, "Failed getting protocol: %w", err)
	}

	// Forwarded protocol can be empty.
	forwardedProtocol, _ := request.GetCtxValue[string](ctx, request.CtxForwardedProtocol)
	if protocol == AuthenticationMethodCluster && forwardedProtocol != "" {
		return forwardedProtocol, nil
	}

	return protocol, nil
}
