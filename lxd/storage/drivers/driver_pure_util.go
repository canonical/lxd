package drivers

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// pureVolTypePrefixes maps volume type to storage volume name prefix.
// Use smallest possible prefixes since Pure Storage volume names are limited to 63 characters.
var pureVolTypePrefixes = map[VolumeType]string{
	VolumeTypeContainer: "c",
	VolumeTypeVM:        "v",
	VolumeTypeImage:     "i",
	VolumeTypeCustom:    "u",
}

// pureContentTypeSuffixes maps volume's content type to storage volume name suffix.
var pureContentTypeSuffixes = map[ContentType]string{
	// Suffix used for block content type volumes.
	ContentTypeBlock: "b",

	// Suffix used for ISO content type volumes.
	ContentTypeISO: "i",
}

// pureSnapshotPrefix is a prefix used for Pure Storage snapshots to avoid name conflicts
// when creating temporary volume from the snapshot.
var pureSnapshotPrefix = "s"

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

// pureEntity represents a generic entity in Pure Storage.
type pureEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// pureSpace represents the usage data of Pure Storage resource.
type pureSpace struct {
	// Total reserved space.
	// For volumes, this is the available space or quota.
	// For storage pools, this is the total reserved space (not the quota).
	TotalBytes int64 `json:"total_provisioned"`

	// Amount of logically written data that a volume or a snapshot references.
	// This value is compared against the quota, therefore, it should be used for
	// showing the actual used space. Although, the actual used space is most likely
	// less than this value due to the data reduction that is done by Pure Storage.
	UsedBytes int64 `json:"virtual"`
}

// pureStorageArray represents a storage array in Pure Storage.
type pureStorageArray struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Capacity int64     `json:"capacity"`
	Space    pureSpace `json:"space"`
}

// pureStoragePool represents a storage pool (pod) in Pure Storage.
type pureStoragePool struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	IsDestroyed bool         `json:"destroyed"`
	Quota       int64        `json:"quota_limit"`
	Space       pureSpace    `json:"space"`
	Arrays      []pureEntity `json:"arrays"`
}

// pureVolume represents a volume in Pure Storage.
type pureVolume struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Serial      string    `json:"serial"`
	IsDestroyed bool      `json:"destroyed"`
	Space       pureSpace `json:"space"`
}

// pureHost represents a host in Pure Storage.
type pureHost struct {
	Name            string   `json:"name"`
	IQNs            []string `json:"iqns"`
	NQNs            []string `json:"nqns"`
	ConnectionCount int      `json:"connection_count"`
}

// pureTarget represents a target in Pure Storage.
type pureTarget struct {
	IQN    *string `json:"iqn"`
	NQN    *string `json:"nqn"`
	Portal *string `json:"portal"`
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

// getStorageArray returns the list of storage arrays.
// If arrayNames are provided, only those are returned.
func (p *pureClient) getStorageArrays(arrayNames ...string) ([]pureStorageArray, error) {
	var resp pureResponse[pureStorageArray]
	err := p.requestAuthenticated(http.MethodGet, fmt.Sprintf("/arrays?names=%s", strings.Join(arrayNames, ",")), nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to get storage arrays: %w", err)
	}

	return resp.Items, nil
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

// updateStoragePool updates an existing storage pool (Pure Storage pod).
func (p *pureClient) updateStoragePool(poolName string, size int64) error {
	reqBody := make(map[string]any)
	if size > 0 {
		reqBody["quota_limit"] = size
	}

	req, err := p.createBodyReader(reqBody)
	if err != nil {
		return err
	}

	err = p.requestAuthenticated(http.MethodPatch, fmt.Sprintf("/pods?names=%s", poolName), req, nil)
	if err != nil {
		return fmt.Errorf("Failed to update storage pool %q: %w", poolName, err)
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

// getVolume returns the volume behind volumeID.
func (p *pureClient) getVolume(poolName string, volName string) (*pureVolume, error) {
	var resp pureResponse[pureVolume]

	err := p.requestAuthenticated(http.MethodGet, fmt.Sprintf("/volumes?names=%s::%s", poolName, volName), nil, &resp)
	if err != nil {
		if isPureErrorNotFound(err) {
			return nil, api.StatusErrorf(http.StatusNotFound, "Volume %q not found", volName)
		}

		return nil, fmt.Errorf("Failed to get volume %q: %w", volName, err)
	}

	if len(resp.Items) == 0 {
		return nil, api.StatusErrorf(http.StatusNotFound, "Volume %q not found", volName)
	}

	return &resp.Items[0], nil
}

// createVolume creates a new volume in the given storage pool. The volume is created with
// supplied size in bytes. Upon successful creation, volume's ID is returned.
func (p *pureClient) createVolume(poolName string, volName string, sizeBytes int64) error {
	req, err := p.createBodyReader(map[string]any{
		"provisioned": sizeBytes,
	})
	if err != nil {
		return err
	}

	// Prevent default protection groups to be applied on the new volume, which can
	// prevent us from eradicating the volume once deleted.
	err = p.requestAuthenticated(http.MethodPost, fmt.Sprintf("/volumes?names=%s::%s&with_default_protection=false", poolName, volName), req, nil)
	if err != nil {
		return fmt.Errorf("Failed to create volume %q in storage pool %q: %w", volName, poolName, err)
	}

	return nil
}

// deleteVolume deletes an exisiting volume in the given storage pool.
func (p *pureClient) deleteVolume(poolName string, volName string) error {
	req, err := p.createBodyReader(map[string]any{
		"destroyed": true,
	})
	if err != nil {
		return err
	}

	// To destroy the volume, we need to patch it by setting the destroyed to true.
	err = p.requestAuthenticated(http.MethodPatch, fmt.Sprintf("/volumes?names=%s::%s", poolName, volName), req, nil)
	if err != nil {
		return fmt.Errorf("Failed to destroy volume %q in storage pool %q: %w", volName, poolName, err)
	}

	// Afterwards, we can eradicate the volume. If this operation fails, the volume will remain
	// in the destroyed state.
	err = p.requestAuthenticated(http.MethodDelete, fmt.Sprintf("/volumes?names=%s::%s", poolName, volName), nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete volume %q in storage pool %q: %w", volName, poolName, err)
	}

	return nil
}

// resizeVolume resizes an existing volume. This function does not resize any filesystem inside the volume.
func (p *pureClient) resizeVolume(poolName string, volName string, sizeBytes int64, truncate bool) error {
	req, err := p.createBodyReader(map[string]any{
		"provisioned": sizeBytes,
	})
	if err != nil {
		return err
	}

	err = p.requestAuthenticated(http.MethodPatch, fmt.Sprintf("/volumes?names=%s::%s&truncate=%v", poolName, volName, truncate), req, nil)
	if err != nil {
		return fmt.Errorf("Failed to resize volume %q in storage pool %q: %w", volName, poolName, err)
	}

	return nil
}

// copyVolume copies a source volume into destination volume. If overwrite is set to true,
// the destination volume will be overwritten if it already exists.
func (p *pureClient) copyVolume(srcPoolName string, srcVolName string, dstPoolName string, dstVolName string, overwrite bool) error {
	req, err := p.createBodyReader(map[string]any{
		"source": map[string]string{
			"name": fmt.Sprintf("%s::%s", srcPoolName, srcVolName),
		},
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("/volumes?names=%s::%s&overwrite=%v", dstPoolName, dstVolName, overwrite)

	if !overwrite {
		// Disable default protection groups when creating a new volume to avoid potential issues
		// when deleting the volume because protection group may prevent volume eridication.
		url = fmt.Sprintf("%s&with_default_protection=false", url)
	}

	err = p.requestAuthenticated(http.MethodPost, url, req, nil)
	if err != nil {
		return fmt.Errorf(`Failed to copy volume "%s/%s" to "%s/%s": %w`, srcPoolName, srcVolName, dstPoolName, dstVolName, err)
	}

	return nil
}

// getVolumeSnapshots retrieves all existing snapshot for the given storage volume.
func (p *pureClient) getVolumeSnapshots(poolName string, volName string) ([]pureVolume, error) {
	var resp pureResponse[pureVolume]

	err := p.requestAuthenticated(http.MethodGet, fmt.Sprintf("/volume-snapshots?source_names=%s::%s", poolName, volName), nil, &resp)
	if err != nil {
		if isPureErrorNotFound(err) {
			return nil, api.StatusErrorf(http.StatusNotFound, "Volume %q not found", volName)
		}

		return nil, fmt.Errorf("Failed to retrieve snapshots for volume %q in storage pool %q: %w", volName, poolName, err)
	}

	return resp.Items, nil
}

// getVolumeSnapshot retrieves an existing snapshot for the given storage volume.
func (p *pureClient) getVolumeSnapshot(poolName string, volName string, snapshotName string) (*pureVolume, error) {
	var resp pureResponse[pureVolume]

	err := p.requestAuthenticated(http.MethodGet, fmt.Sprintf("/volume-snapshots?names=%s::%s.%s", poolName, volName, snapshotName), nil, &resp)
	if err != nil {
		if isPureErrorNotFound(err) {
			return nil, api.StatusErrorf(http.StatusNotFound, "Snapshot %q not found", snapshotName)
		}

		return nil, fmt.Errorf("Failed to retrieve snapshot %q for volume %q in storage pool %q: %w", snapshotName, volName, poolName, err)
	}

	if len(resp.Items) == 0 {
		return nil, api.StatusErrorf(http.StatusNotFound, "Snapshot %q not found", snapshotName)
	}

	return &resp.Items[0], nil
}

// createVolumeSnapshot creates a new snapshot for the given storage volume.
func (p *pureClient) createVolumeSnapshot(poolName string, volName string, snapshotName string) error {
	req, err := p.createBodyReader(map[string]any{
		"suffix": snapshotName,
	})
	if err != nil {
		return err
	}

	err = p.requestAuthenticated(http.MethodPost, fmt.Sprintf("/volume-snapshots?source_names=%s::%s", poolName, volName), req, nil)
	if err != nil {
		return fmt.Errorf("Failed to create snapshot %q for volume %q in storage pool %q: %w", snapshotName, volName, poolName, err)
	}

	return nil
}

// deleteVolumeSnapshot deletes an existing snapshot for the given storage volume.
func (p *pureClient) deleteVolumeSnapshot(poolName string, volName string, snapshotName string) error {
	snapshot, err := p.getVolumeSnapshot(poolName, volName, snapshotName)
	if err != nil {
		return err
	}

	if !snapshot.IsDestroyed {
		// First destroy the snapshot.
		req, err := p.createBodyReader(map[string]any{
			"destroyed": true,
		})
		if err != nil {
			return err
		}

		// Destroy snapshot.
		err = p.requestAuthenticated(http.MethodPatch, fmt.Sprintf("/volume-snapshots?names=%s::%s.%s", poolName, volName, snapshotName), req, nil)
		if err != nil {
			return fmt.Errorf("Failed to destroy snapshot %q for volume %q in storage pool %q: %w", snapshotName, volName, poolName, err)
		}
	}

	// Delete (eradicate) snapshot.
	err = p.requestAuthenticated(http.MethodDelete, fmt.Sprintf("/volume-snapshots?names=%s::%s.%s", poolName, volName, snapshotName), nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete snapshot %q for volume %q in storage pool %q: %w", snapshotName, volName, poolName, err)
	}

	return nil
}

// restoreVolumeSnapshot restores the volume by copying the volume snapshot into its parent volume.
func (p *pureClient) restoreVolumeSnapshot(poolName string, volName string, snapshotName string) error {
	return p.copyVolume(poolName, fmt.Sprintf("%s.%s", volName, snapshotName), poolName, volName, true)
}

// copyVolumeSnapshot copies the volume snapshot into destination volume. Destination volume is overwritten
// if already exists.
func (p *pureClient) copyVolumeSnapshot(srcPoolName string, srcVolName string, srcSnapshotName string, dstPoolName string, dstVolName string) error {
	return p.copyVolume(srcPoolName, fmt.Sprintf("%s.%s", srcVolName, srcSnapshotName), dstPoolName, dstVolName, true)
}

// getHosts retrieves an existing Pure Storage host.
func (p *pureClient) getHosts() ([]pureHost, error) {
	var resp pureResponse[pureHost]

	err := p.requestAuthenticated(http.MethodGet, "/hosts", nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to get hosts: %w", err)
	}

	return resp.Items, nil
}

// getCurrentHost retrieves the Pure Storage host linked to the current LXD host.
// The Pure Storage host is considered a match if it includes the fully qualified
// name of the LXD host that is determined by the configured mode.
func (p *pureClient) getCurrentHost() (*pureHost, error) {
	qn, err := p.driver.connector().QualifiedName()
	if err != nil {
		return nil, err
	}

	hosts, err := p.getHosts()
	if err != nil {
		return nil, err
	}

	mode := p.driver.config["pure.mode"]

	for _, host := range hosts {
		if mode == connectors.TypeISCSI && slices.Contains(host.IQNs, qn) {
			return &host, nil
		}

		if mode == connectors.TypeNVME && slices.Contains(host.NQNs, qn) {
			return &host, nil
		}
	}

	return nil, api.StatusErrorf(http.StatusNotFound, "Host with qualified name %q not found", qn)
}

// createHost creates a new host with provided initiator qualified names that can be associated
// with specific volumes.
func (p *pureClient) createHost(hostName string, qns []string) error {
	body := make(map[string]any, 1)
	mode := p.driver.config["pure.mode"]

	switch mode {
	case connectors.TypeISCSI:
		body["iqns"] = qns
	case connectors.TypeNVME:
		body["nqns"] = qns
	default:
		return fmt.Errorf("Unsupported Pure Storage mode %q", mode)
	}

	req, err := p.createBodyReader(body)
	if err != nil {
		return err
	}

	err = p.requestAuthenticated(http.MethodPost, fmt.Sprintf("/hosts?names=%s", hostName), req, nil)
	if err != nil {
		if isPureErrorOf(err, http.StatusBadRequest, "Host already exists.") {
			return api.StatusErrorf(http.StatusConflict, "Host %q already exists", hostName)
		}

		return fmt.Errorf("Failed to create host %q: %w", hostName, err)
	}

	return nil
}

// updateHost updates an existing host.
func (p *pureClient) updateHost(hostName string, qns []string) error {
	body := make(map[string]any, 1)
	mode := p.driver.config["pure.mode"]

	switch mode {
	case connectors.TypeISCSI:
		body["iqns"] = qns
	case connectors.TypeNVME:
		body["nqns"] = qns
	default:
		return fmt.Errorf("Unsupported Pure Storage mode %q", mode)
	}

	req, err := p.createBodyReader(body)
	if err != nil {
		return err
	}

	// To destroy the volume, we need to patch it by setting the destroyed to true.
	err = p.requestAuthenticated(http.MethodPatch, fmt.Sprintf("/hosts?names=%s", hostName), req, nil)
	if err != nil {
		return fmt.Errorf("Failed to update host %q: %w", hostName, err)
	}

	return nil
}

// deleteHost deletes an existing host.
func (p *pureClient) deleteHost(hostName string) error {
	err := p.requestAuthenticated(http.MethodDelete, fmt.Sprintf("/hosts?names=%s", hostName), nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete host %q: %w", hostName, err)
	}

	return nil
}

// connectHostToVolume creates a connection between a host and volume. It returns true if the connection
// was created, and false if it already existed.
func (p *pureClient) connectHostToVolume(poolName string, volName string, hostName string) (bool, error) {
	err := p.requestAuthenticated(http.MethodPost, fmt.Sprintf("/connections?host_names=%s&volume_names=%s::%s", hostName, poolName, volName), nil, nil)
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
	err := p.requestAuthenticated(http.MethodDelete, fmt.Sprintf("/connections?host_names=%s&volume_names=%s::%s", hostName, poolName, volName), nil, nil)
	if err != nil {
		if isPureErrorNotFound(err) {
			return api.StatusErrorf(http.StatusNotFound, "Connection between host %q and volume %q not found", volName, hostName)
		}

		return fmt.Errorf("Failed to disconnect volume %q from host %q: %w", volName, hostName, err)
	}

	return nil
}

// getTarget retrieves the Pure Storage address and the its qualified name for the configured mode.
func (p *pureClient) getTarget() (targetAddr string, targetQN string, err error) {
	var resp pureResponse[pureTarget]

	err = p.requestAuthenticated(http.MethodGet, "/ports", nil, &resp)
	if err != nil {
		return "", "", fmt.Errorf("Failed to retrieve Pure Storage targets: %w", err)
	}

	mode := p.driver.config["pure.mode"]

	// Find and return the target that has address (portal) and qualified name configured.
	for _, target := range resp.Items {
		if target.Portal == nil {
			continue
		}

		// Strip the port from the portal address.
		portal := strings.Split(*target.Portal, ":")[0]

		if mode == connectors.TypeISCSI && target.IQN != nil {
			return portal, *target.IQN, nil
		}

		if mode == connectors.TypeNVME && target.NQN != nil {
			return portal, *target.NQN, nil
		}
	}

	return "", "", api.StatusErrorf(http.StatusNotFound, "No Pure Storage target found")
}

// ensureHost returns a name of the host that is configured with a given IQN. If such host
// does not exist, a new one is created, where host's name equals to the server name with a
// mode included as a suffix because Pure Storage does not allow mixing IQNs, NQNs, and WWNs
// on a single host.
func (d *pure) ensureHost() (hostName string, cleanup revert.Hook, err error) {
	var hostname string

	qn, err := d.connector().QualifiedName()
	if err != nil {
		return "", nil, err
	}

	revert := revert.New()
	defer revert.Fail()

	// Fetch an existing Pure Storage host.
	host, err := d.client().getCurrentHost()
	if err != nil {
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return "", nil, err
		}

		// The Pure Storage host with a qualified name of the current LXD host does not exist.
		// Therefore, create a new one and name it after the server name.
		serverName, err := ResolveServerName(d.state.ServerName)
		if err != nil {
			return "", nil, err
		}

		// Append the mode to the server name because Pure Storage does not allow mixing
		// NQNs, IQNs, and WWNs for a single host.
		hostname = serverName + "-" + d.config["pure.mode"]

		err = d.client().createHost(hostname, []string{qn})
		if err != nil {
			if !api.StatusErrorCheck(err, http.StatusConflict) {
				return "", nil, err
			}

			// The host with the given name already exists, update it instead.
			err = d.client().updateHost(hostname, []string{qn})
			if err != nil {
				return "", nil, err
			}
		} else {
			revert.Add(func() { _ = d.client().deleteHost(hostname) })
		}
	} else {
		// Hostname already exists with the given IQN.
		hostname = host.Name
	}

	cleanup = revert.Clone().Fail
	revert.Success()
	return hostname, cleanup, nil
}

// mapVolume maps the given volume onto this host.
func (d *pure) mapVolume(vol Volume) error {
	revert := revert.New()
	defer revert.Fail()

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return err
	}

	// Use lock to prevent a concurrent operation from the same of different
	// storage pool from disconnecting the volume or even removing the Pure
	// Storage host. This can happen if the last volume is unmapped just
	// before this volume is mapped.
	unlock, err := locking.Lock(d.state.ShutdownCtx, fmt.Sprintf("storage_pure_%s", d.config["pure.mode"]))
	if err != nil {
		return err
	}

	defer unlock()

	// Ensure the host exists and is configured with the correct QN.
	hostname, cleanup, err := d.ensureHost()
	if err != nil {
		return err
	}

	revert.Add(cleanup)

	// Ensure the volume is connected to the host.
	connCreated, err := d.client().connectHostToVolume(vol.pool, volName, hostname)
	if err != nil {
		return err
	}

	if connCreated {
		revert.Add(func() { _ = d.client().disconnectHostFromVolume(vol.pool, volName, hostname) })
	}

	// Find the array's qualified name for the configured mode.
	targetAddr, targetQN, err := d.client().getTarget()
	if err != nil {
		return err
	}

	// Connect to the array. Note that the connection can only be established
	// when at least one volume is mapped with the corresponding Pure Storage
	// host.
	err = d.connector().Connect(d.state.ShutdownCtx, targetAddr, targetQN)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// unmapVolume unmaps the given volume from this host.
func (d *pure) unmapVolume(vol Volume) error {
	volName, err := d.getVolumeName(vol)
	if err != nil {
		return err
	}

	unlock, err := locking.Lock(d.state.ShutdownCtx, fmt.Sprintf("storage_pure_%s", d.config["pure.mode"]))
	if err != nil {
		return err
	}

	defer unlock()

	host, err := d.client().getCurrentHost()
	if err != nil {
		return err
	}

	// Disconnect the volume from the host and ignore error if connection does not exist.
	err = d.client().disconnectHostFromVolume(vol.pool, volName, host.Name)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return err
	}

	volumePath, _, _ := d.getMappedDevPath(vol, false)
	if volumePath != "" {
		if d.config["pure.mode"] == connectors.TypeISCSI {
			// When volume is disconnected from the host, the device will remain on the system.
			//
			// To remove the device, we need to either logout from the session or remove the
			// device manually. Logging out of the session is not desired as it would disconnect
			// from all connected volumes. Therefore, we need to manually remove the device.
			split := strings.Split(filepath.Base(volumePath), "/")
			devName := split[len(split)-1]

			path := fmt.Sprintf("/sys/block/%s/device/delete", devName)
			if shared.PathExists(path) {
				err := os.WriteFile(path, []byte("1"), 0400)
				if err != nil {
					return fmt.Errorf("Failed to unmap volume %q: Failed to remove device %q: %w", vol.name, devName, err)
				}
			}
		}

		// Wait until the volume has disappeared.
		ctx, cancel := context.WithTimeout(d.state.ShutdownCtx, 10*time.Second)
		defer cancel()

		if !waitGone(ctx, volumePath) {
			return fmt.Errorf("Timeout exceeded waiting for Pure Storage volume %q to disappear on path %q", vol.name, volumePath)
		}
	}

	// If this was the last volume being unmapped from this system, terminate
	// an active session and remove the host from Pure Storage.
	if host.ConnectionCount == 1 {
		_, targetQN, err := d.client().getTarget()
		if err != nil {
			return err
		}

		// Disconnect from the target.
		err = d.connector().Disconnect(targetQN)
		if err != nil {
			return err
		}

		// Remove the host from Pure Storage.
		err = d.client().deleteHost(host.Name)
		if err != nil {
			return err
		}
	}

	return nil
}

// getMappedDevPath returns the local device path for the given volume.
// Indicate with mapVolume if the volume should get mapped to the system if it isn't present.
func (d *pure) getMappedDevPath(vol Volume, mapVolume bool) (string, revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	if mapVolume {
		err := d.mapVolume(vol)
		if err != nil {
			return "", nil, err
		}

		revert.Add(func() { _ = d.unmapVolume(vol) })
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return "", nil, err
	}

	var diskPrefix string
	var diskSuffix string
	var devicePath string

	switch d.config["pure.mode"] {
	case connectors.TypeISCSI:
		diskPrefix = "scsi-"
		diskSuffix = fmt.Sprintf("%s::%s", vol.pool, volName)
	case connectors.TypeNVME:
		diskPrefix = "nvme-eui."

		pureVol, err := d.client().getVolume(vol.pool, volName)
		if err != nil {
			return "", nil, err
		}

		// The serial number is used to identify the device. The last 10 characters
		// of the serial number appear as a disk device suffix. This check ensures
		// we do not panic if the reported serial number is too short for parsing.
		if len(pureVol.Serial) <= 10 {
			// Serial number is too short.
			return "", nil, fmt.Errorf("Failed to locate device for volume %q: Invalid serial number %q", vol.name, pureVol.Serial)
		}

		// Extract the last 10 characters of the serial number. Also convert
		// it to lower case, as on host the device ID is completely lower case.
		diskSuffix = strings.ToLower(pureVol.Serial[len(pureVol.Serial)-10:])
	default:
		return "", nil, fmt.Errorf("Unsupported Pure Storage mode %q", d.config["pure.mode"])
	}

	// Get the device path.
	if mapVolume {
		// Wait for the device path to appear as the volume has been just mapped to the host.
		devicePath, err = connectors.WaitDiskDevicePath(d.state.ShutdownCtx, diskPrefix, diskSuffix)
	} else {
		// Get the the device path without waiting.
		devicePath, err = connectors.GetDiskDevicePath(diskPrefix, diskSuffix)
	}

	if err != nil {
		return "", nil, fmt.Errorf("Failed to locate device for volume %q: %w", vol.name, err)
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return devicePath, cleanup, nil
}

// getVolumeName returns the fully qualified name derived from the volume's UUID.
func (d *pure) getVolumeName(vol Volume) (string, error) {
	volUUID, err := uuid.Parse(vol.config["volatile.uuid"])
	if err != nil {
		return "", fmt.Errorf(`Failed parsing "volatile.uuid" from volume %q: %w`, vol.name, err)
	}

	// Remove hypens from the UUID to create a volume name.
	volName := strings.ReplaceAll(volUUID.String(), "-", "")

	// Search for the volume type prefix, and if found, prepend it to the volume name.
	volumeTypePrefix, ok := pureVolTypePrefixes[vol.volType]
	if ok {
		volName = fmt.Sprintf("%s-%s", volumeTypePrefix, volName)
	}

	// Search for the content type suffix, and if found, append it to the volume name.
	contentTypeSuffix, ok := pureContentTypeSuffixes[vol.contentType]
	if ok {
		volName = fmt.Sprintf("%s-%s", volName, contentTypeSuffix)
	}

	// If volume is snapshot, prepend snapshot prefix to its name.
	if vol.IsSnapshot() {
		volName = fmt.Sprintf("%s%s", pureSnapshotPrefix, volName)
	}

	return volName, nil
}
