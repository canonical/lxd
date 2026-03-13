package lxd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/canonical/lxd/shared"
)

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

	// Copy path values stored in the context by lxdMux so they survive
	// internal request forwarding (e.g., devLXD -> main API handlers).
	vars, ok := ctx.Value(shared.CtxMuxPathVars).(map[string]string)
	if ok {
		for name, value := range vars {
			req.SetPathValue(name, value)
		}
	}

	return req, nil
}
