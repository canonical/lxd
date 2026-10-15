package request

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared/api"
)

// ErrNoCertificate is returned if the request does not contain a certificate.
var ErrNoCertificate = errors.New("No certificate present in request")

// ErrNoTLS is returned if the request does not have a TLS connection.
var ErrNoTLS = errors.New("No TLS connection state")

// CreateRequestor extracts the lifecycle event requestor data from the provided context.
func CreateRequestor(ctx context.Context) *api.EventLifecycleRequestor {
	requestor, err := GetRequestor(ctx)
	if err != nil {
		return &api.EventLifecycleRequestor{}
	}

	return requestor.EventLifecycleRequestor()
}

// SaveConnectionInContext can be set as the ConnContext field of a http.Server to set the connection
// in the request context for later use.
func SaveConnectionInContext(ctx context.Context, connection net.Conn) context.Context {
	return context.WithValue(ctx, CtxConn, connection)
}

// GetCallerIdentityFromContext extracts the identity of the caller from the request context.
// The identity is expected to be present and have a valid identifier. Otherwise, an error is returned.
func GetCallerIdentityFromContext(ctx context.Context) (*identity.CacheEntry, error) {
	requestor, err := GetRequestor(ctx)
	if err != nil {
		return nil, err
	}

	identity := requestor.CallerIdentity()
	if identity == nil {
		return nil, errors.New("Request context identity is missing")
	}

	if identity.Identifier == "" {
		return nil, errors.New("Request context identity is missing an identifier")
	}

	return identity, nil
}

// GetPeerCertificate returns the first peer certificate in the request TLS connection states peer certificate list.
func GetPeerCertificate(r *http.Request) (*x509.Certificate, error) {
	if r.TLS == nil {
		return nil, api.StatusErrorf(http.StatusBadRequest, "%w", ErrNoTLS)
	}

	if len(r.TLS.PeerCertificates) == 0 {
		return nil, api.StatusErrorf(http.StatusBadRequest, "%w", ErrNoCertificate)
	}

	return r.TLS.PeerCertificates[0], nil
}
