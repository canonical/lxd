package request

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared/api"
)

// NewRequest creates a new HTTP request with the specified method, URL, data, and ETag.
func NewRequest(method string, url string, data any, ETag string) (*http.Request, error) {
	return NewRequestWithContext(context.Background(), method, url, data, ETag)
}

// NewRequestWithContext creates a new HTTP request with the specified method, URL, data, and ETag.
func NewRequestWithContext(ctx context.Context, method string, url string, data any, ETag string) (*http.Request, error) {
	var req *http.Request
	var err error

	// Create new HTTP request.
	if data != nil {
		switch data := data.(type) {
		case io.Reader:
			// Some data to be sent along with the request.
			req, err = http.NewRequestWithContext(ctx, method, url, data)
			if err != nil {
				return nil, err
			}

			// Set the encoding accordingly
			req.Header.Set("Content-Type", "application/octet-stream")
		default:
			// Encode the provided data.
			buf := bytes.Buffer{}
			err := json.NewEncoder(&buf).Encode(data)
			if err != nil {
				return nil, err
			}

			// Some data to be sent along with the request.
			// Use a reader since the request body needs to be seekable.
			req, err = http.NewRequestWithContext(ctx, method, url, bytes.NewReader(buf.Bytes()))
			if err != nil {
				return nil, err
			}

			// Set the encoding accordingly.
			req.Header.Set("Content-Type", "application/json")
		}
	} else {
		// No data to be sent along with the request.
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return nil, err
		}
	}

	// Set the ETag if provided.
	if ETag != "" {
		req.Header.Set("If-Match", ETag)
	}

	return req, nil
}

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
