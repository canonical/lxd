package drivers

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// pureError represents an error responses from Pure Storage API.
type pureError struct {
	// List of errors returned by the Pure Storage API.
	Errors []struct {
		Context string `json:"context"`
		Message string `json:"message"`
	} `json:"errors"`

	// StatusCode is not part of the response body but is used
	// to store the HTTP status code.
	StatusCode int `json:"-"`
}

// Error returns the first error message from the Pure Storage API error.
func (p *pureError) Error() string {
	if p == nil || len(p.Errors) == 0 {
		return ""
	}

	// Return the first error message without the trailing dot.
	return strings.TrimSuffix(p.Errors[0].Message, ".")
}

// isPureErrorOf checks if the given error is of type pureError, has the specified status code,
// and its error messages contain any of the provided substrings. Note that the error message
// comparison is case-insensitive.
func isPureErrorOf(err error, statusCode int, substrings ...string) bool {
	perr, ok := err.(*pureError)
	if !ok {
		return false
	}

	if perr.StatusCode != statusCode {
		return false
	}

	if len(substrings) == 0 {
		// Error matches the given status code and no substrings are provided.
		return true
	}

	// Check if any error message contains a provided substring.
	// Perform case-insensitive matching by converting both the
	// error message and the substring to lowercase.
	for _, err := range perr.Errors {
		errMsg := strings.ToLower(err.Message)

		for _, substring := range substrings {
			if strings.Contains(errMsg, strings.ToLower(substring)) {
				return true
			}
		}
	}

	return false
}

// pureIsNotFoundError returns true if the error is of type pureError, its status code is 400 (bad request),
// and the error message contains a substring indicating the resource was not found.
func isPureErrorNotFound(err error) bool {
	return isPureErrorOf(err, http.StatusBadRequest, "Not found", "Does not exist", "No such volume or snapshot")
}

// pureResponse wraps the response from the Pure Storage API. In most cases, the response
// contains a list of items, even if only one item is returned.
type pureResponse[T any] struct {
	Items []T `json:"items"`
}

// pureStoragePool represents a storage pool (pod) in Pure Storage.
type pureStoragePool struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	IsDestroyed bool   `json:"destroyed"`
}

// pureClient holds the Pure Storage HTTP client and an access token.
type pureClient struct {
	driver      *pure
	accessToken string
}

// newPureClient creates a new instance of the HTTP Pure Storage client.
func newPureClient(driver *pure) *pureClient {
	return &pureClient{
		driver: driver,
	}
}

// createBodyReader creates a reader for the given request body contents.
func (p *pureClient) createBodyReader(contents map[string]any) (io.Reader, error) {
	body := &bytes.Buffer{}

	err := json.NewEncoder(body).Encode(contents)
	if err != nil {
		return nil, fmt.Errorf("Failed to write request body: %w", err)
	}

	return body, nil
}

// request issues a HTTP request against the Pure Storage gateway.
func (p *pureClient) request(method string, urlPath string, reqBody io.Reader, reqHeaders map[string]string, respBody any, respHeaders map[string]string) error {
	// Extract scheme and host from the gateway URL.
	urlParts := strings.Split(p.driver.config["pure.gateway"], "://")
	if len(urlParts) != 2 {
		return fmt.Errorf("Invalid Pure Storage gateway URL: %q", p.driver.config["pure.gateway"])
	}

	// Construct the request URL.
	url := api.NewURL().Scheme(urlParts[0]).Host(urlParts[1]).URL

	// Prefixes the given path with the API version in the format "/api/<version>/<path>".
	// If the path is "/api/api_version", the API version is not included as this path
	// is used to retrieve supported API versions.
	if urlPath == "/api/api_version" {
		url.Path = urlPath
	} else {
		// If API version is not known yet, retrieve and cache it first.
		if p.driver.apiVersion == "" {
			apiVersions, err := p.getAPIVersions()
			if err != nil {
				return fmt.Errorf("Failed to retrieve supported Pure Storage API versions: %w", err)
			}

			// Use the latest available API version.
			p.driver.apiVersion = apiVersions[len(apiVersions)-1]
		}

		url.Path = path.Join("api", p.driver.apiVersion, urlPath)
	}

	req, err := http.NewRequest(method, url.String(), reqBody)
	if err != nil {
		return fmt.Errorf("Failed to create request: %w", err)
	}

	// Set custom request headers.
	for k, v := range reqHeaders {
		req.Header.Add(k, v)
	}

	req.Header.Add("Accept", "application/json")
	if reqBody != nil {
		req.Header.Add("Content-Type", "application/json")
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: shared.IsFalse(p.driver.config["pure.gateway.verify"]),
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Failed to send request: %w", err)
	}

	defer resp.Body.Close()

	// The unauthorized error is reported when an invalid (or expired) access token is provided.
	// Wrap unauthorized requests into an API status error to allow easier checking for expired
	// token in the requestAuthenticated function.
	if resp.StatusCode == http.StatusUnauthorized {
		return api.StatusErrorf(http.StatusUnauthorized, "Unauthorized request")
	}

	// Overwrite the response data type if an error is detected.
	if resp.StatusCode != http.StatusOK {
		respBody = &pureError{}
	}

	// Extract the response body if requested.
	if respBody != nil {
		err = json.NewDecoder(resp.Body).Decode(respBody)
		if err != nil {
			return fmt.Errorf("Failed to read response body from %q: %w", urlPath, err)
		}
	}

	// Extract the response headers if requested.
	if respHeaders != nil {
		for k, v := range resp.Header {
			respHeaders[k] = strings.Join(v, ",")
		}
	}

	// Return the formatted error from the body
	pureErr, ok := respBody.(*pureError)
	if ok {
		pureErr.StatusCode = resp.StatusCode
		return pureErr
	}

	return nil
}

// requestAuthenticated issues an authenticated HTTP request against the Pure Storage gateway.
// In case the access token is expired, the function will try to obtain a new one.
func (p *pureClient) requestAuthenticated(method string, path string, reqBody io.Reader, respBody any) error {
	// If request fails with an unauthorized error, the request will be retried after
	// requesting a new access token.
	retries := 1

	for {
		// Ensure we are logged into the Pure Storage.
		err := p.login()
		if err != nil {
			return err
		}

		// Set access token as request header.
		reqHeaders := map[string]string{
			"X-Auth-Token": p.accessToken,
		}

		// Initiate request.
		err = p.request(method, path, reqBody, reqHeaders, respBody, nil)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusUnauthorized) && retries > 0 {
				// Access token seems to be expired.
				// Reset the token and try one more time.
				p.accessToken = ""
				retries--
				continue
			}

			// Either the error is not of type unauthorized or the maximum number of
			// retries has been exceeded.
			return err
		}

		return nil
	}
}

// getAPIVersion returns the list of API versions that are supported by the Pure Storage.
func (p *pureClient) getAPIVersions() ([]string, error) {
	var resp struct {
		APIVersions []string `json:"version"`
	}

	err := p.request(http.MethodGet, "/api/api_version", nil, nil, &resp, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve available API versions from Pure Storage: %w", err)
	}

	if len(resp.APIVersions) == 0 {
		return nil, fmt.Errorf("Pure Storage does not support any API versions")
	}

	return resp.APIVersions, nil
}

// login initiates an authentication request against the Pure Storage using the API token. If successful,
// an access token is retrieved and stored within a client. The access token is then used for further
// authentication.
func (p *pureClient) login() error {
	if p.accessToken != "" {
		// Token has been already obtained.
		return nil
	}

	reqHeaders := map[string]string{
		"api-token": p.driver.config["pure.api.token"],
	}

	respHeaders := make(map[string]string)

	err := p.request(http.MethodPost, "/login", nil, reqHeaders, nil, respHeaders)
	if err != nil {
		return fmt.Errorf("Failed to login: %w", err)
	}

	p.accessToken = respHeaders["X-Auth-Token"]
	if p.accessToken == "" {
		return errors.New("Failed to obtain access token")
	}

	return nil
}

// getStoragePool returns the storage pool with the given name.
func (p *pureClient) getStoragePool(poolName string) (*pureStoragePool, error) {
	var resp pureResponse[pureStoragePool]
	err := p.requestAuthenticated(http.MethodGet, fmt.Sprintf("/pods?names=%s", poolName), nil, &resp)
	if err != nil {
		if isPureErrorNotFound(err) {
			return nil, api.StatusErrorf(http.StatusNotFound, "Storage pool %q not found", poolName)
		}

		return nil, fmt.Errorf("Failed to get storage pool %q: %w", poolName, err)
	}

	if len(resp.Items) == 0 {
		return nil, api.StatusErrorf(http.StatusNotFound, "Storage pool %q not found", poolName)
	}

	return &resp.Items[0], nil
}

// createStoragePool creates a storage pool (Pure Storage pod).
func (p *pureClient) createStoragePool(poolName string, size int64) error {
	reqBody := make(map[string]any)
	if size > 0 {
		reqBody["quota_limit"] = size
	}

	pool, err := p.getStoragePool(poolName)
	if err == nil && pool.IsDestroyed {
		// Storage pool exists in destroyed state, therefore, restore it.
		reqBody["destroyed"] = false

		req, err := p.createBodyReader(reqBody)
		if err != nil {
			return err
		}

		err = p.requestAuthenticated(http.MethodPatch, fmt.Sprintf("/pods?names=%s", poolName), req, nil)
		if err != nil {
			return fmt.Errorf("Failed to restore storage pool %q: %w", poolName, err)
		}

		logger.Info("Storage pool has been restored", logger.Ctx{"pool": poolName})
		return nil
	}

	req, err := p.createBodyReader(reqBody)
	if err != nil {
		return err
	}

	// Storage pool does not exist in destroyed state, therefore, try to create a new one.
	err = p.requestAuthenticated(http.MethodPost, fmt.Sprintf("/pods?names=%s", poolName), req, nil)
	if err != nil {
		return fmt.Errorf("Failed to create storage pool %q: %w", poolName, err)
	}

	return nil
}

// deleteStoragePool deletes a storage pool (Pure Storage pod).
func (p *pureClient) deleteStoragePool(poolName string) error {
	pool, err := p.getStoragePool(poolName)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			// Storage pool has been already removed.
			return nil
		}

		return err
	}

	// To delete the storage pool, we need to destroy it first by setting the destroyed property to true.
	// In addition, we want to destroy all of its contents to allow the pool to be deleted.
	// If the pool is already destroyed, we can skip this step.
	if !pool.IsDestroyed {
		req, err := p.createBodyReader(map[string]any{
			"destroyed": true,
		})
		if err != nil {
			return err
		}

		err = p.requestAuthenticated(http.MethodPatch, fmt.Sprintf("/pods?names=%s&destroy_contents=true", poolName), req, nil)
		if err != nil {
			if isPureErrorNotFound(err) {
				return nil
			}

			return fmt.Errorf("Failed to destroy storage pool %q: %w", poolName, err)
		}
	}

	// Eradicate the storage pool by permanently deleting it along all of its contents.
	err = p.requestAuthenticated(http.MethodDelete, fmt.Sprintf("/pods?names=%s&eradicate_contents=true", poolName), nil, nil)
	if err != nil {
		if isPureErrorNotFound(err) {
			return nil
		}

		if isPureErrorOf(err, http.StatusBadRequest, "Cannot eradicate pod") {
			// Eradication failed, therefore the pool remains in the destroyed state.
			// However, we still consider it as deleted because Pure Storage SafeMode
			// may be enabled, which prevents immediate eradication of the pool.
			logger.Warn("Storage pool is left in destroyed state", logger.Ctx{"pool": poolName, "err": err})
			return nil
		}

		return fmt.Errorf("Failed to delete storage pool %q: %w", poolName, err)
	}

	return nil
}
