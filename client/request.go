package lxd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
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
