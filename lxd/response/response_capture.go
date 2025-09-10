package response

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// responseCapture is a custom http.ResponseWriter that captures the response
// header, body, and status code for later inspection.
type responseCapture struct {
	request    *http.Request
	header     http.Header
	body       *bytes.Buffer
	statusCode int
}

// NewResponseCapture creates a new responseCapture for the given request.
func NewResponseCapture(req *http.Request) *responseCapture {
	return &responseCapture{
		request: req,
		header:  make(http.Header),
		body:    new(bytes.Buffer),
	}
}

// Header returns the header map.
func (rc *responseCapture) Header() http.Header {
	return rc.header
}

// Write writes the data to the buffer.
func (rc *responseCapture) Write(b []byte) (int, error) {
	return rc.body.Write(b)
}

// WriteHeader sets the response status code.
func (rc *responseCapture) WriteHeader(statusCode int) {
	rc.statusCode = statusCode
}

// ToAPIResponse decodes the captured response body into an api.Response
// and retrieves the ETag from the header.
func (rc *responseCapture) ToAPIResponse() (*api.Response, string, error) {
	// Get the ETag.
	etag := rc.Header().Get("ETag")

	// Decode the response.
	decoder := json.NewDecoder(rc.body)
	response := api.Response{}

	err := decoder.Decode(&response)
	if err != nil {
		// Check the return value for a cleaner error.
		if rc.statusCode != http.StatusOK {
			return nil, "", fmt.Errorf("Failed to fetch %s: %d", rc.request.URL.String(), rc.statusCode)
		}

		return nil, "", err
	}

	// Handle errors.
	if response.Type == api.ErrorResponse {
		return nil, "", api.NewStatusError(rc.statusCode, response.Error)
	}

	return &response, etag, nil
}

// Render renders the response and returns a potential error.
func (rc *responseCapture) Render(resp Response) error {
	err := resp.Render(rc, rc.request)
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
func (rc *responseCapture) RenderToStruct(resp Response, target any) (etag string, err error) {
	err = resp.Render(rc, rc.request)
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
