package drivers

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// pureAPIVersion is the Pure Storage API version used by LXD.
// The 2.21 version is the first version that supports NVMe/TCP.
const pureAPIVersion = "2.21"

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

// pureHost represents a host in Pure Storage.
type pureHost struct {
	Name            string `json:"name"`
	ConnectionCount int    `json:"connection_count"`
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
func (p *pureClient) request(method string, url url.URL, reqBody io.Reader, reqHeaders map[string]string, respBody any, respHeaders map[string]string) error {
	// Extract scheme and host from the gateway URL.
	scheme, host, found := strings.Cut(p.driver.config["pure.gateway"], "://")
	if !found {
		return fmt.Errorf("Invalid Pure Storage gateway URL: %q", p.driver.config["pure.gateway"])
	}

	// Set request URL scheme and host.
	url.Scheme = scheme
	url.Host = host

	// Prefixes the given path with the API version in the format "/api/<version>/<path>".
	// If the path is "/api/api_version", the API version is not included as this path
	// is used to retrieve supported API versions.
	if url.Path != "/api/api_version" {
		// If API version is not known yet, retrieve and cache it first.
		if p.driver.apiVersion == "" {
			apiVersions, err := p.getAPIVersions()
			if err != nil {
				return fmt.Errorf("Failed to retrieve supported Pure Storage API versions: %w", err)
			}

			// Ensure the required API version is supported by Pure Storage array.
			if !slices.Contains(apiVersions, pureAPIVersion) {
				return fmt.Errorf("Required API version %q is not supported by Pure Storage array", pureAPIVersion)
			}

			// Set API version to the driver to avoid checking the API version
			// for each subsequent request.
			p.driver.apiVersion = pureAPIVersion
		}

		// Prefix current path with the API version.
		url.Path = path.Join("api", p.driver.apiVersion, url.Path)
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
			return fmt.Errorf("Failed to read response body from %q: %w", url.String(), err)
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
func (p *pureClient) requestAuthenticated(method string, url url.URL, reqBody io.Reader, respBody any) error {
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
		err = p.request(method, url, reqBody, reqHeaders, respBody, nil)
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

	url := api.NewURL().Path("api", "api_version")
	err := p.request(http.MethodGet, url.URL, nil, nil, &resp, nil)
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

	url := api.NewURL().Path("login")
	err := p.request(http.MethodPost, url.URL, nil, reqHeaders, nil, respHeaders)
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

	url := api.NewURL().Path("pods").WithQuery("names", poolName)
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
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

		url := api.NewURL().Path("pods").WithQuery("names", poolName)
		err = p.requestAuthenticated(http.MethodPatch, url.URL, req, nil)
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
	url := api.NewURL().Path("pods").WithQuery("names", poolName)
	err = p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
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

		url := api.NewURL().Path("pods").WithQuery("names", poolName).WithQuery("destroy_contents", "true")
		err = p.requestAuthenticated(http.MethodPatch, url.URL, req, nil)
		if err != nil {
			if isPureErrorNotFound(err) {
				return nil
			}

			return fmt.Errorf("Failed to destroy storage pool %q: %w", poolName, err)
		}
	}

	// Eradicate the storage pool by permanently deleting it along all of its contents.
	url := api.NewURL().Path("pods").WithQuery("names", poolName).WithQuery("eradicate_contents", "true")
	err = p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
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

// getHosts retrieves an existing Pure Storage host.
func (p *pureClient) getHosts() ([]pureHost, error) {
	var resp pureResponse[pureHost]

	url := api.NewURL().Path("hosts")
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to get hosts: %w", err)
	}

	return resp.Items, nil
}

// createHost creates a new host that can be associated with specific volumes.
func (p *pureClient) createHost(hostName string) error {
	req, err := p.createBodyReader(map[string]any{})
	if err != nil {
		return err
	}

	url := api.NewURL().Path("hosts").WithQuery("names", hostName)
	err = p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {
		if isPureErrorOf(err, http.StatusBadRequest, "Host already exists.") {
			return api.StatusErrorf(http.StatusConflict, "Host %q already exists", hostName)
		}

		return fmt.Errorf("Failed to create host %q: %w", hostName, err)
	}

	return nil
}

// updateHost updates an existing host.
func (p *pureClient) updateHost(hostName string) error {
	req, err := p.createBodyReader(map[string]any{})
	if err != nil {
		return err
	}

	// To destroy the volume, we need to patch it by setting the destroyed to true.
	url := api.NewURL().Path("hosts").WithQuery("names", hostName)
	err = p.requestAuthenticated(http.MethodPatch, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("Failed to update host %q: %w", hostName, err)
	}

	return nil
}

// deleteHost deletes an existing host.
func (p *pureClient) deleteHost(hostName string) error {
	url := api.NewURL().Path("hosts").WithQuery("names", hostName)
	err := p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete host %q: %w", hostName, err)
	}

	return nil
}

// connectHostToVolume creates a connection between a host and volume. It returns true if the connection
// was created, and false if it already existed.
func (p *pureClient) connectHostToVolume(poolName string, volName string, hostName string) (bool, error) {
	url := api.NewURL().Path("connections").WithQuery("host_names", hostName).WithQuery("volume_names", poolName+"::"+volName)

	err := p.requestAuthenticated(http.MethodPost, url.URL, nil, nil)
	if err != nil {
		if isPureErrorOf(err, http.StatusBadRequest, "Connection already exists.") {
			// Do not error out if connection already exists.
			return false, nil
		}

		return false, fmt.Errorf("Failed to connect volume %q with host %q: %w", volName, hostName, err)
	}

	return true, nil
}

// disconnectHostFromVolume deletes a connection between a host and volume.
func (p *pureClient) disconnectHostFromVolume(poolName string, volName string, hostName string) error {
	url := api.NewURL().Path("connections").WithQuery("host_names", hostName).WithQuery("volume_names", poolName+"::"+volName)

	err := p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
	if err != nil {
		if isPureErrorNotFound(err) {
			return api.StatusErrorf(http.StatusNotFound, "Connection between host %q and volume %q not found", volName, hostName)
		}

		return fmt.Errorf("Failed to disconnect volume %q from host %q: %w", volName, hostName, err)
	}

	return nil
}
