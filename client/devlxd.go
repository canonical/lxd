package lxd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

// ProtocolDevLXD represents a devLXD API server.
type ProtocolDevLXD struct {
	ctx context.Context

	// Context related to the current connection.
	ctxConnected       context.Context
	ctxConnectedCancel context.CancelFunc

	// HTTP client information.
	http          *http.Client
	httpBaseURL   url.URL
	httpUnixPath  string
	httpUserAgent string
}

// GetConnectionInfo returns the basic connection information used to interact with the server.
func (r *ProtocolDevLXD) GetConnectionInfo() (*ConnectionInfo, error) {
	return &ConnectionInfo{
		Protocol:   "devlxd",
		URL:        r.httpBaseURL.String(),
		SocketPath: r.httpUnixPath,
	}, nil
}

// GetHTTPClient returns the http client used for the connection. This can be used to set custom http options.
func (r *ProtocolDevLXD) GetHTTPClient() (*http.Client, error) {
	if r.http == nil {
		return nil, fmt.Errorf("HTTP client isn't set, bad connection")
	}

	return r.http, nil
}

// DoHTTP performs a Request.
func (r *ProtocolDevLXD) DoHTTP(req *http.Request) (resp *http.Response, err error) {
	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	return r.http.Do(req)
}

// Disconnect is a no-op for devLXD.
func (r *ProtocolDevLXD) Disconnect() {
	r.ctxConnectedCancel()
}

// rawQuery is a method that sends HTTP request to the devLXD with the provided
// method, URL, data, and ETag. It processes the request based on the data's
// type and handles the HTTP response, returning parsed results or an error
// if it occurs.
func (r *ProtocolDevLXD) rawQuery(method string, url string, data any, ETag string) (devLXDResp *api.DevLXDResponse, etag string, err error) {
	var req *http.Request

	// Log the request
	logger.Debug("Sending request to devLXD", logger.Ctx{
		"method": method,
		"url":    url,
		"etag":   ETag,
	})

	// Get a new HTTP request setup
	if data != nil {
		// Encode the provided data
		buf := bytes.Buffer{}
		err := json.NewEncoder(&buf).Encode(data)
		if err != nil {
			return nil, "", err
		}

		// Some data to be sent along with the request
		// Use a reader since the request body needs to be seekable
		req, err = http.NewRequestWithContext(r.ctx, method, url, bytes.NewReader(buf.Bytes()))
		if err != nil {
			return nil, "", err
		}

		// Set the encoding accordingly
		req.Header.Set("Content-Type", "application/json")
	} else {
		// No data to be sent along with the request
		req, err = http.NewRequestWithContext(r.ctx, method, url, nil)
		if err != nil {
			return nil, "", err
		}
	}

	// Set the ETag.
	if ETag != "" {
		req.Header.Set("If-Match", ETag)
	}

	req.Header.Set("User-Agent", r.httpUserAgent)

	// Send the request.
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, "", err
	}

	defer resp.Body.Close()

	return devLXDParseResponse(resp)
}

// query sends a query to the devLXD and returns the response.
func (r *ProtocolDevLXD) query(method string, path string, data any, ETag string) (devLXDResp *api.DevLXDResponse, etag string, err error) {
	// Generate the URL
	urlString := r.httpBaseURL.String() + "/" + version.APIVersion
	if path != "" {
		urlString += path
	}

	url, err := url.Parse(urlString)
	if err != nil {
		return nil, "", err
	}

	url.RawQuery = url.Query().Encode()

	// Run the actual query
	return r.rawQuery(method, url.String(), data, ETag)
}

// queryStruct sends a query to the devLXD, then converts the response content into the specified target struct.
// The function returns the etag of the response, and handles any errors during this process.
func (r *ProtocolDevLXD) queryStruct(method string, urlPath string, data any, ETag string, target any) (etag string, err error) {
	resp, etag, err := r.query(method, urlPath, data, ETag)
	if err != nil {
		return "", err
	}

	err = resp.ContentAsStruct(&target)
	if err != nil {
		return "", err
	}

	return etag, nil
}

// devLXDParseResponse processes the HTTP response from the devLXD. It reads the response body,
// checks the status code, and returns a DevLXDResponse struct containing the content and status code.
// If the response is not successful, it returns an error instead.
func devLXDParseResponse(resp *http.Response) (*api.DevLXDResponse, string, error) {
	var content []byte
	var err error

	// Get the ETag
	etag := resp.Header.Get("ETag")

	// Read response body.
	content, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("Failed to read response body from %q: %v", resp.Request.URL.String(), err)
	}

	// Handel error response.
	if resp.StatusCode != http.StatusOK {
		if len(content) == 0 {
			return nil, "", api.NewGenericStatusError(resp.StatusCode)
		}

		return nil, "", api.NewStatusError(resp.StatusCode, string(content))
	}

	return &api.DevLXDResponse{
		Content:    content,
		StatusCode: resp.StatusCode,
	}, etag, nil
}
