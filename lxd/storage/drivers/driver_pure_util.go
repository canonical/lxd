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
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// pureAPIVersion is the Pure Storage API version used by LXD.
// The 2.21 version is the first version that supports NVMe/TCP.
const pureAPIVersion = "2.21"

// pureServiceNameMapping maps Pure Storage mode in LXD to the corresponding Pure Storage
// service name.
var pureServiceNameMapping = map[string]string{
	connectors.TypeISCSI: "iscsi",
	connectors.TypeNVME:  "nvme-tcp",
}

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

	// statusCode is not part of the response body but is used
	// to store the HTTP status code.
	statusCode int
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

	if perr.statusCode != statusCode {
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

// purePort represents a network interface in Pure Storage.
type pureNetworkInterface struct {
	Name     string `json:"name"`
	Ethernet struct {
		Address string `json:"address,omitempty"`
	} `json:"eth,omitempty"`
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

// pureProtectionGroup represents a protection group in Pure Storage.
type pureProtectionGroup struct {
	Name        string `json:"name"`
	IsDestroyed bool   `json:"destroyed"`
}

// pureDefaultProtection represents a default protection in Pure Storage.
type pureDefaultProtection struct {
	Name string `json:"name"`
	Type string `json:"type"`
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

// purePort represents a port in Pure Storage.
type purePort struct {
	Name string `json:"name"`
	IQN  string `json:"iqn,omitempty"`
	NQN  string `json:"nqn,omitempty"`
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
func (p *pureClient) request(method string, url url.URL, reqBody map[string]any, reqHeaders map[string]string, respBody any, respHeaders map[string]string) error {
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

	var err error
	var reqBodyReader io.Reader

	if reqBody != nil {
		reqBodyReader, err = p.createBodyReader(reqBody)
		if err != nil {
			return err
		}
	}

	req, err := http.NewRequest(method, url.String(), reqBodyReader)
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
		pureErr.statusCode = resp.StatusCode
		return pureErr
	}

	return nil
}

// requestAuthenticated issues an authenticated HTTP request against the Pure Storage gateway.
// In case the access token is expired, the function will try to obtain a new one.
func (p *pureClient) requestAuthenticated(method string, url url.URL, reqBody map[string]any, respBody any) error {
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
		return nil, errors.New("Pure Storage does not support any API versions")
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

// getNetworkInterfaces retrieves a valid Pure Storage network interfaces, which
// means the interface has an IP address configured and is enabled. The result
// can be filtered by a specific service name, where an empty string represents
// no filtering.
func (p *pureClient) getNetworkInterfaces(service string) ([]pureNetworkInterface, error) {
	var resp pureResponse[pureNetworkInterface]

	// Retrieve enabled network interfaces that have an IP address configured.
	url := api.NewURL().Path("network-interfaces").WithQuery("filter", "enabled='true'").WithQuery("filter", "eth.address")
	if service != "" {
		url = url.WithQuery("filter", "services='"+service+"'")
	}

	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve Pure Storage network interfaces: %w", err)
	}

	return resp.Items, nil
}

// getProtectionGroup returns the protection group with the given name.
func (p *pureClient) getProtectionGroup(name string) (*pureProtectionGroup, error) {
	var resp pureResponse[pureProtectionGroup]

	url := api.NewURL().Path("protection-groups").WithQuery("names", name)
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		if isPureErrorNotFound(err) {
			return nil, api.StatusErrorf(http.StatusNotFound, "Protection group %q not found", name)
		}

		return nil, fmt.Errorf("Failed to get protection group %q: %w", name, err)
	}

	if len(resp.Items) == 0 {
		return nil, api.StatusErrorf(http.StatusNotFound, "Protection group %q not found", name)
	}

	return &resp.Items[0], nil
}

// deleteProtectionGroup deletes the protection group with the given name.
func (p *pureClient) deleteProtectionGroup(name string) error {
	pg, err := p.getProtectionGroup(name)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			// Already removed.
			return nil
		}

		return err
	}

	url := api.NewURL().Path("protection-groups").WithQuery("names", name)

	// Ensure the protection group is destroyed.
	if !pg.IsDestroyed {
		req := map[string]any{
			"destroyed": true,
		}

		err = p.requestAuthenticated(http.MethodPatch, url.URL, req, nil)
		if err != nil {
			return fmt.Errorf("Failed to destroy protection group %q: %w", name, err)
		}
	}

	// Delete the protection group.
	err = p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete protection group %q: %w", name, err)
	}

	return nil
}

// deleteStoragePoolDefaultProtections unsets default protections for the given
// storage pool and removes its default protection groups.
func (p *pureClient) deleteStoragePoolDefaultProtections(poolName string) error {
	var resp pureResponse[struct {
		Type               string                  `json:"type"`
		DefaultProtections []pureDefaultProtection `json:"default_protections"`
	}]

	url := api.NewURL().Path("container-default-protections").WithQuery("names", poolName)

	// Extract default protections for the given storage pool.
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		if isPureErrorNotFound(err) {
			// Default protections does not exist.
			return nil
		}

		return fmt.Errorf("Failed to get default protections for storage pool %q: %w", poolName, err)
	}

	// Remove default protections and protection groups related to the storage pool.
	for _, item := range resp.Items {
		// Ensure protection applies to the storage pool.
		if item.Type != "pod" {
			continue
		}

		// To be able to delete default protection groups, they have to
		// be removed from the list of default protections.
		req := map[string]any{
			"default_protections": []pureDefaultProtection{},
		}

		err = p.requestAuthenticated(http.MethodPatch, url.URL, req, nil)
		if err != nil {
			if isPureErrorNotFound(err) {
				// Default protection already removed.
				continue
			}

			return fmt.Errorf("Failed to unset default protections for storage pool %q: %w", poolName, err)
		}

		// Iterate over default protections and extract protection group names.
		for _, pg := range item.DefaultProtections {
			if pg.Type != "protection_group" {
				continue
			}

			// Remove protection groups.
			err := p.deleteProtectionGroup(pg.Name)
			if err != nil {
				return fmt.Errorf("Failed to remove protection group %q for storage pool %q: %w", pg.Name, poolName, err)
			}
		}
	}

	return nil
}

// getStorageArray returns the list of storage arrays.
// If arrayNames are provided, only those are returned.
func (p *pureClient) getStorageArrays(arrayNames ...string) ([]pureStorageArray, error) {
	var resp pureResponse[pureStorageArray]

	url := api.NewURL().Path("arrays").WithQuery("names", strings.Join(arrayNames, ","))
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to get storage arrays: %w", err)
	}

	return resp.Items, nil
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
	revert := revert.New()
	defer revert.Fail()

	req := make(map[string]any)
	if size > 0 {
		req["quota_limit"] = size
	}

	pool, err := p.getStoragePool(poolName)
	if err == nil && pool.IsDestroyed {
		// Storage pool exists in destroyed state, therefore, restore it.
		req["destroyed"] = false

		url := api.NewURL().Path("pods").WithQuery("names", poolName)
		err = p.requestAuthenticated(http.MethodPatch, url.URL, req, nil)
		if err != nil {
			return fmt.Errorf("Failed to restore storage pool %q: %w", poolName, err)
		}

		logger.Info("Storage pool has been restored", logger.Ctx{"pool": poolName})
	} else {
		// Storage pool does not exist in destroyed state, therefore, try to create a new one.
		url := api.NewURL().Path("pods").WithQuery("names", poolName)
		err = p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
		if err != nil {
			return fmt.Errorf("Failed to create storage pool %q: %w", poolName, err)
		}
	}

	revert.Add(func() { _ = p.deleteStoragePool(poolName) })

	// Delete default protection groups of the new storage pool to ensure
	// there is no limitations when deleting the pool or volume.
	err = p.deleteStoragePoolDefaultProtections(poolName)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// updateStoragePool updates an existing storage pool (Pure Storage pod).
func (p *pureClient) updateStoragePool(poolName string, size int64) error {
	req := make(map[string]any)
	if size > 0 {
		req["quota_limit"] = size
	}

	url := api.NewURL().Path("pods").WithQuery("names", poolName)
	err := p.requestAuthenticated(http.MethodPatch, url.URL, req, nil)
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
		req := map[string]any{
			"destroyed": true,
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

// getVolume returns the volume behind volumeID.
func (p *pureClient) getVolume(poolName string, volName string) (*pureVolume, error) {
	var resp pureResponse[pureVolume]

	url := api.NewURL().Path("volumes").WithQuery("names", poolName+"::"+volName)
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
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
	req := map[string]any{
		"provisioned": sizeBytes,
	}

	// Prevent default protection groups to be applied on the new volume, which can
	// prevent us from eradicating the volume once deleted.
	url := api.NewURL().Path("volumes").WithQuery("names", poolName+"::"+volName).WithQuery("with_default_protection", "false")
	err := p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("Failed to create volume %q in storage pool %q: %w", volName, poolName, err)
	}

	return nil
}

// deleteVolume deletes an exisiting volume in the given storage pool.
func (p *pureClient) deleteVolume(poolName string, volName string) error {
	req := map[string]any{
		"destroyed": true,
	}

	url := api.NewURL().Path("volumes").WithQuery("names", poolName+"::"+volName)

	// To destroy the volume, we need to patch it by setting the destroyed to true.
	err := p.requestAuthenticated(http.MethodPatch, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("Failed to destroy volume %q in storage pool %q: %w", volName, poolName, err)
	}

	// Afterwards, we can eradicate the volume. If this operation fails, the volume will remain
	// in the destroyed state.
	err = p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete volume %q in storage pool %q: %w", volName, poolName, err)
	}

	return nil
}

// resizeVolume resizes an existing volume. This function does not resize any filesystem inside the volume.
func (p *pureClient) resizeVolume(poolName string, volName string, sizeBytes int64, truncate bool) error {
	req := map[string]any{
		"provisioned": sizeBytes,
	}

	url := api.NewURL().Path("volumes").WithQuery("names", poolName+"::"+volName).WithQuery("truncate", strconv.FormatBool(truncate))
	err := p.requestAuthenticated(http.MethodPatch, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("Failed to resize volume %q in storage pool %q: %w", volName, poolName, err)
	}

	return nil
}

// copyVolume copies a source volume into destination volume. If overwrite is set to true,
// the destination volume will be overwritten if it already exists.
func (p *pureClient) copyVolume(srcPoolName string, srcVolName string, dstPoolName string, dstVolName string, overwrite bool) error {
	req := map[string]any{
		"source": map[string]string{
			"name": srcPoolName + "::" + srcVolName,
		},
	}

	url := api.NewURL().Path("volumes").WithQuery("names", dstPoolName+"::"+dstVolName).WithQuery("overwrite", strconv.FormatBool(overwrite))

	if !overwrite {
		// Disable default protection groups when creating a new volume to avoid potential issues
		// when deleting the volume because protection group may prevent volume eridication.
		url = url.WithQuery("with_default_protection", "false")
	}

	err := p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf(`Failed to copy volume "%s/%s" to "%s/%s": %w`, srcPoolName, srcVolName, dstPoolName, dstVolName, err)
	}

	return nil
}

// getVolumeSnapshots retrieves all existing snapshot for the given storage volume.
func (p *pureClient) getVolumeSnapshots(poolName string, volName string) ([]pureVolume, error) {
	var resp pureResponse[pureVolume]

	url := api.NewURL().Path("volume-snapshots").WithQuery("source_names", poolName+"::"+volName)
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
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

	url := api.NewURL().Path("volume-snapshots").WithQuery("names", poolName+"::"+volName+"."+snapshotName)
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
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
	req := map[string]any{
		"suffix": snapshotName,
	}

	url := api.NewURL().Path("volume-snapshots").WithQuery("source_names", poolName+"::"+volName)
	err := p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
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
		req := map[string]any{
			"destroyed": true,
		}

		// Destroy snapshot.
		url := api.NewURL().Path("volume-snapshots").WithQuery("names", poolName+"::"+volName+"."+snapshotName)
		err = p.requestAuthenticated(http.MethodPatch, url.URL, req, nil)
		if err != nil {
			return fmt.Errorf("Failed to destroy snapshot %q for volume %q in storage pool %q: %w", snapshotName, volName, poolName, err)
		}
	}

	// Delete (eradicate) snapshot.
	url := api.NewURL().Path("volume-snapshots").WithQuery("names", poolName+"::"+volName+"."+snapshotName)
	err = p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete snapshot %q for volume %q in storage pool %q: %w", snapshotName, volName, poolName, err)
	}

	return nil
}

// restoreVolumeSnapshot restores the volume by copying the volume snapshot into its parent volume.
func (p *pureClient) restoreVolumeSnapshot(poolName string, volName string, snapshotName string) error {
	return p.copyVolume(poolName, volName+"."+snapshotName, poolName, volName, true)
}

// copyVolumeSnapshot copies the volume snapshot into destination volume. Destination volume is overwritten
// if already exists.
func (p *pureClient) copyVolumeSnapshot(srcPoolName string, srcVolName string, srcSnapshotName string, dstPoolName string, dstVolName string) error {
	return p.copyVolume(srcPoolName, srcVolName+"."+srcSnapshotName, dstPoolName, dstVolName, true)
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

// getCurrentHost retrieves the Pure Storage host linked to the current LXD host.
// The Pure Storage host is considered a match if it includes the fully qualified
// name of the LXD host that is determined by the configured mode.
func (p *pureClient) getCurrentHost() (*pureHost, error) {
	connector, err := p.driver.connector()
	if err != nil {
		return nil, err
	}

	qn, err := connector.QualifiedName()
	if err != nil {
		return nil, err
	}

	hosts, err := p.getHosts()
	if err != nil {
		return nil, err
	}

	mode := connector.Type()

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
	req := make(map[string]any, 1)

	connector, err := p.driver.connector()
	if err != nil {
		return err
	}

	switch connector.Type() {
	case connectors.TypeISCSI:
		req["iqns"] = qns
	case connectors.TypeNVME:
		req["nqns"] = qns
	default:
		return fmt.Errorf("Unsupported Pure Storage mode %q", connector.Type())
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
func (p *pureClient) updateHost(hostName string, qns []string) error {
	req := make(map[string]any, 1)

	connector, err := p.driver.connector()
	if err != nil {
		return err
	}

	switch connector.Type() {
	case connectors.TypeISCSI:
		req["iqns"] = qns
	case connectors.TypeNVME:
		req["nqns"] = qns
	default:
		return fmt.Errorf("Unsupported Pure Storage mode %q", connector.Type())
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

// getTarget retrieves the qualified name and addresses of Pure Storage target for the configured mode.
func (p *pureClient) getTarget() (targetQN string, targetAddrs []string, err error) {
	connector, err := p.driver.connector()
	if err != nil {
		return "", nil, err
	}

	mode := connector.Type()

	// Get Pure Storage service name based on the configured mode.
	service, ok := pureServiceNameMapping[mode]
	if !ok {
		return "", nil, fmt.Errorf("Failed to determine service name for Pure Storage mode %q", mode)
	}

	// Retrieve the list of Pure Storage network interfaces.
	interfaces, err := p.getNetworkInterfaces(service)
	if err != nil {
		return "", nil, err
	}

	if len(interfaces) == 0 {
		return "", nil, api.StatusErrorf(http.StatusNotFound, "Enabled network interface with %q service not found", service)
	}

	// First check if target addresses are configured, otherwise, use the discovered ones.
	targetAddrs = shared.SplitNTrimSpace(p.driver.config["pure.target"], ",", -1, true)
	if len(targetAddrs) == 0 {
		targetAddrs = make([]string, 0, len(interfaces))
		for _, iface := range interfaces {
			targetAddrs = append(targetAddrs, iface.Ethernet.Address)
		}
	}

	// Get the qualified name of the target by iterating over the available
	// ports until the one with the qualified name is found. All ports have
	// the same IQN, but it may happen that IQN is not reported for a
	// specific port, for example, if the port is misconfigured.
	var nq string
	for _, iface := range interfaces {
		var resp pureResponse[purePort]

		url := api.NewURL().Path("ports").WithQuery("filter", "name='"+iface.Name+"'")
		err = p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
		if err != nil {
			return "", nil, fmt.Errorf("Failed to retrieve Pure Storage targets: %w", err)
		}

		if len(resp.Items) == 0 {
			continue
		}

		port := resp.Items[0]

		if mode == connectors.TypeISCSI {
			nq = port.IQN
		}

		if mode == connectors.TypeNVME {
			nq = port.NQN
		}

		if nq != "" {
			break
		}
	}

	if nq == "" {
		return "", nil, api.StatusErrorf(http.StatusNotFound, "Qualified name for %q target not found", mode)
	}

	return nq, targetAddrs, nil
}

// ensureHost returns a name of the host that is configured with a given IQN. If such host
// does not exist, a new one is created, where host's name equals to the server name with a
// mode included as a suffix because Pure Storage does not allow mixing IQNs, NQNs, and WWNs
// on a single host.
func (d *pure) ensureHost() (hostName string, cleanup revert.Hook, err error) {
	var hostname string

	revert := revert.New()
	defer revert.Fail()

	connector, err := d.connector()
	if err != nil {
		return "", nil, err
	}

	// Get the qualified name of the host.
	qn, err := connector.QualifiedName()
	if err != nil {
		return "", nil, err
	}

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
		hostname = serverName + "-" + connector.Type()

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
func (d *pure) mapVolume(vol Volume) (cleanup revert.Hook, err error) {
	reverter := revert.New()
	defer reverter.Fail()

	connector, err := d.connector()
	if err != nil {
		return nil, err
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return nil, err
	}

	unlock, err := remoteVolumeMapLock(connector.Type(), d.Info().Name)
	if err != nil {
		return nil, err
	}

	defer unlock()

	// Ensure the host exists and is configured with the correct QN.
	hostname, cleanup, err := d.ensureHost()
	if err != nil {
		return nil, err
	}

	reverter.Add(cleanup)

	// Ensure the volume is connected to the host.
	connCreated, err := d.client().connectHostToVolume(vol.pool, volName, hostname)
	if err != nil {
		return nil, err
	}

	if connCreated {
		reverter.Add(func() { _ = d.client().disconnectHostFromVolume(vol.pool, volName, hostname) })
	}

	// Find the array's qualified name for the configured mode.
	targetQN, targetAddrs, err := d.client().getTarget()
	if err != nil {
		return nil, err
	}

	// Connect to the array.
	connReverter, err := connector.Connect(d.state.ShutdownCtx, targetQN, targetAddrs...)
	if err != nil {
		return nil, err
	}

	reverter.Add(connReverter)

	// If connect succeeded it means we have at least one established connection.
	// However, it's reverter does not cleanup the establised connections or a newly
	// created session. Therefore, if we created a mapping, add unmapVolume to the
	// returned (outer) reverter. Unmap ensures the target is disconnected only when
	// no other device is using it.
	outerReverter := revert.New()
	if connCreated {
		outerReverter.Add(func() { _ = d.unmapVolume(vol) })
	}

	// Add connReverter to the outer reverter, as it will immediately stop
	// any ongoing connection attempts. Note that it must be added after
	// unmapVolume to ensure it is called first.
	outerReverter.Add(connReverter)

	reverter.Success()
	return outerReverter.Fail, nil
}

// unmapVolume unmaps the given volume from this host.
func (d *pure) unmapVolume(vol Volume) error {
	connector, err := d.connector()
	if err != nil {
		return err
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return err
	}

	unlock, err := remoteVolumeMapLock(connector.Type(), d.Info().Name)
	if err != nil {
		return err
	}

	defer unlock()

	host, err := d.client().getCurrentHost()
	if err != nil {
		return err
	}

	// Get a path of a block device we want to unmap.
	volumePath, _, _ := d.getMappedDevPath(vol, false)

	// When iSCSI volume is disconnected from the host, the device will remain on the system.
	//
	// To remove the device, we need to either logout from the session or remove the
	// device manually. Logging out of the session is not desired as it would disconnect
	// from all connected volumes. Therefore, we need to manually remove the device.
	//
	// Also, for iSCSI we don't want to unmap the device on the storage array side before removing it
	// from the host, because on some storage arrays (for example, HPE Alletra and Pure) we've seen that removing
	// a vLUN from the array immediately makes device inaccessible and traps any task tries to access it
	// to D-state (and this task can be systemd-udevd which tries to remove a device node!).
	// That's why it is better to remove the device node from the host and then remove vLUN.
	if volumePath != "" && connector.Type() == connectors.TypeISCSI {
		// removeDevice removes device from the system if the device is removable.
		removeDevice := func(devName string) error {
			path := "/sys/block/" + devName + "/device/delete"
			if shared.PathExists(path) {
				// Delete device.
				err := os.WriteFile(path, []byte("1"), 0400)
				if err != nil {
					return err
				}
			}

			return nil
		}

		devName := filepath.Base(volumePath)
		if strings.HasPrefix(devName, "dm-") {
			_, err := shared.RunCommandContext(d.state.ShutdownCtx, "multipath", "-f", volumePath)
			if err != nil {
				return fmt.Errorf("Failed to unmap volume %q: Failed to remove multipath device %q: %w", vol.name, devName, err)
			}
		} else {
			// For non-multipath device (/dev/sd*), remove the device itself.
			err := removeDevice(devName)
			if err != nil {
				return fmt.Errorf("Failed to unmap volume %q: Failed to remove device %q: %w", vol.name, devName, err)
			}
		}

		// Wait until the volume has disappeared.
		ctx, cancel := context.WithTimeout(d.state.ShutdownCtx, 30*time.Second)
		defer cancel()

		if !block.WaitDiskDeviceGone(ctx, volumePath) {
			return fmt.Errorf("Timeout exceeded waiting for Pure Storage volume %q to disappear on path %q", vol.name, volumePath)
		}

		// Device is not there anymore.
		volumePath = ""
	}

	// Disconnect the volume from the host and ignore error if connection does not exist.
	err = d.client().disconnectHostFromVolume(vol.pool, volName, host.Name)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return err
	}

	// When NVMe/TCP volume is disconnected from the host, the device automatically disappears.
	if volumePath != "" && connector.Type() == connectors.TypeNVME {
		// Wait until the volume has disappeared.
		ctx, cancel := context.WithTimeout(d.state.ShutdownCtx, 30*time.Second)
		defer cancel()

		if !block.WaitDiskDeviceGone(ctx, volumePath) {
			return fmt.Errorf("Timeout exceeded waiting for Pure Storage volume %q to disappear on path %q", vol.name, volumePath)
		}
	}

	// If this was the last volume being unmapped from this system, disconnect the active session
	// and remove the host from Pure Storage.
	if host.ConnectionCount <= 1 {
		targetQN, _, err := d.client().getTarget()
		if err != nil {
			return err
		}

		// Disconnect from the target.
		err = connector.Disconnect(targetQN)
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

	connector, err := d.connector()
	if err != nil {
		return "", nil, err
	}

	if mapVolume {
		cleanup, err := d.mapVolume(vol)
		if err != nil {
			return "", nil, err
		}

		revert.Add(cleanup)
	}

	volName, err := d.getVolumeName(vol)
	if err != nil {
		return "", nil, err
	}

	pureVol, err := d.client().getVolume(vol.pool, volName)
	if err != nil {
		return "", nil, err
	}

	// Ensure the serial number is exactly 24 characters long, as it uniquely
	// identifies the device. This check should never succeed, but prevents
	// out-of-bounds errors when slicing the string later.
	if len(pureVol.Serial) != 24 {
		return "", nil, fmt.Errorf("Failed to locate device for volume %q: Unexpected length of serial number %q (%d)", vol.name, pureVol.Serial, len(pureVol.Serial))
	}

	var diskPrefix string
	var diskSuffix string

	switch connector.Type() {
	case connectors.TypeISCSI:
		diskPrefix = "scsi-"
		diskSuffix = pureVol.Serial
	case connectors.TypeNVME:
		diskPrefix = "nvme-eui."

		// The disk device ID (e.g. "008726b5033af24324a9373d00014196") is constructed as:
		// - "00"             - Padding
		// - "8726b5033af243" - First 14 characters of serial number
		// - "24a937"         - OUI (Organizationally Unique Identifier)
		// - "3d00014196"     - Last 10 characters of serial number
		diskSuffix = "00" + pureVol.Serial[0:14] + "24a937" + pureVol.Serial[14:]
	default:
		return "", nil, fmt.Errorf("Unsupported Pure Storage mode %q", connector.Type())
	}

	// Filters devices by matching the device path with the lowercase disk suffix.
	// Pure Storage reports serial numbers in uppercase, so the suffix is converted
	// to lowercase.
	diskPathFilter := func(devPath string) bool {
		return strings.HasSuffix(devPath, strings.ToLower(diskSuffix))
	}

	var devicePath string
	if mapVolume {
		// Wait until the disk device is mapped to the host.
		devicePath, err = block.WaitDiskDevicePath(d.state.ShutdownCtx, diskPrefix, diskPathFilter)
	} else {
		// Expect device to be already mapped.
		devicePath, err = block.GetDiskDevicePath(diskPrefix, diskPathFilter)
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
		volName = volumeTypePrefix + "-" + volName
	}

	// Search for the content type suffix, and if found, append it to the volume name.
	contentTypeSuffix, ok := pureContentTypeSuffixes[vol.contentType]
	if ok {
		volName = volName + "-" + contentTypeSuffix
	}

	// If volume is snapshot, prepend snapshot prefix to its name.
	if vol.IsSnapshot() {
		volName = pureSnapshotPrefix + volName
	}

	return volName, nil
}
