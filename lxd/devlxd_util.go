package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/lxd/response"
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

// responseCapture is a custom http.ResponseWriter that captures the response
// header, body, and status code for later inspection.
type responseCapture struct {
	header     http.Header
	body       *bytes.Buffer
	statusCode int
	url        url.URL
}

// NewResponseCapture creates a new responseCapture for the given request.
func NewResponseCapture(req *http.Request) *responseCapture {
	return &responseCapture{
		header: make(http.Header),
		body:   new(bytes.Buffer),
		url:    *req.URL,
	}
}

// Header returns the header map.
func (r *responseCapture) Header() http.Header {
	return r.header
}

// Write writes the data to the buffer.
func (r *responseCapture) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

// WriteHeader sets the response status code.
func (r *responseCapture) WriteHeader(statusCode int) {
	r.statusCode = statusCode
}

// ToAPIResponse decodes the captured response body into an api.Response
// and retrieves the ETag from the header.
func (r *responseCapture) ToAPIResponse() (*api.Response, string, error) {
	// Get the ETag.
	etag := r.Header().Get("ETag")

	// Decode the response.
	decoder := json.NewDecoder(r.body)
	response := api.Response{}

	err := decoder.Decode(&response)
	if err != nil {
		// Check the return value for a cleaner error.
		if r.statusCode != http.StatusOK {
			return nil, "", fmt.Errorf("Failed to fetch %s: %d", r.url.String(), r.statusCode)
		}

		return nil, "", err
	}

	// Handle errors.
	if response.Type == api.ErrorResponse {
		return nil, "", api.NewStatusError(r.statusCode, response.Error)
	}

	return &response, etag, nil
}

// Render renders the response and returns a potential error.
func Render(req *http.Request, resp response.Response) error {
	rc := NewResponseCapture(req)
	err := resp.Render(rc, req)
	if err != nil {
		return err
	}

	_, _, err = rc.ToAPIResponse()
	if err != nil {
		return err
	}

	return nil
}

// RenderToStruct renders the response into a struct and returns the ETag.
func RenderToStruct(req *http.Request, resp response.Response, target any) (etag string, err error) {
	rc := NewResponseCapture(req)
	err = resp.Render(rc, req)
	if err != nil {
		return "", err
	}

	apiResp, etag, err := rc.ToAPIResponse()
	if err != nil {
		return "", err
	}

	err = apiResp.MetadataAsStruct(target)
	if err != nil {
		return "", err
	}

	return etag, nil
}

// RenderToOperation renders the response into an operation and returns the ETag.
func RenderToOperation(req *http.Request, resp response.Response) (operation *api.Operation, err error) {
	rc := NewResponseCapture(req)
	err = resp.Render(rc, req)
	if err != nil {
		return nil, err
	}

	apiResp, _, err := rc.ToAPIResponse()
	if err != nil {
		return nil, err
	}

	// Get the operation from metadata.
	op, err := apiResp.MetadataAsOperation()
	if err != nil {
		return nil, err
	}

	return op, nil
}
