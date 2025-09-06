package response

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/shared/api"
)

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
