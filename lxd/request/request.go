package request

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// ErrNoCertificate is returned if the request does not contain a certificate.
var ErrNoCertificate = errors.New("No certificate present in request")

// ErrNoTLS is returned if the request does not have a TLS connection.
var ErrNoTLS = errors.New("No TLS connection state")

// CreateRequestor extracts the lifecycle event requestor data from an http.Request context.
func CreateRequestor(r *http.Request) *api.EventLifecycleRequestor {
	ctx := r.Context()
	requestor := &api.EventLifecycleRequestor{}

	// Normal requestor.
	val, ok := ctx.Value(CtxUsername).(string)
	if ok {
		requestor.Username = val
	}

	val, ok = ctx.Value(CtxProtocol).(string)
	if ok {
		requestor.Protocol = val
	}

	requestor.Address = r.RemoteAddr

	// Forwarded requestor override.
	val, ok = ctx.Value(CtxForwardedUsername).(string)
	if ok {
		requestor.Username = val
	}

	val, ok = ctx.Value(CtxForwardedProtocol).(string)
	if ok {
		requestor.Protocol = val
	}

	val, ok = ctx.Value(CtxForwardedAddress).(string)
	if ok {
		requestor.Address = val
	}

	return requestor
}

// SaveConnectionInContext can be set as the ConnContext field of a http.Server to set the connection
// in the request context for later use.
func SaveConnectionInContext(ctx context.Context, connection net.Conn) context.Context {
	return context.WithValue(ctx, CtxConn, connection)
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
