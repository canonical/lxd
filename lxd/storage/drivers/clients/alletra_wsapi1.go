package clients

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

const (
	hpeSessionTypeRegular    = 1
	hpeTransportTypeTCP      = 2
	hpeLinkStateReady        = 4
	hpePortProtocolISCSI     = 2
	hpePortProtocolNVME      = 6
	hpeTaskStatusDone        = 1
	hpeTaskStatusActive      = 2
	hpeTaskStatusCanceled    = 3
	hpeTaskStatusFailed      = 4
	hpeOperationPriorityHigh = 1

	hpeAPIErrorInvalidSessionKey       = 6
	hpeAPIErrorExistentHost            = 16
	hpeAPIErrorNonExistentVol          = 23
	hpeAPIErrorVolumeIsAlreadyExported = 29

	hpeAPIVolumeSetMemberAdd       = 1
	hpeAPIVolumeSetMemberRemove    = 2
	hpeAPIActionGrowVolume         = 3
	hpeAPIActionPromoteVirtualCopy = 4
)

// createBodyReader creates a reader for the given request body contents.
func createBodyReader(contents map[string]any) (io.Reader, error) {
	body := &bytes.Buffer{}

	err := json.NewEncoder(body).Encode(contents)
	if err != nil {
		return nil, fmt.Errorf("Failed to write request body: %w", err)
	}

	return body, nil
}

// AlletraClient holds the HPE Alletra Storage HTTP client and an access token.
type AlletraClient struct {
	logger     logger.Logger
	url        string
	username   string
	password   string
	verifyTLS  bool
	cpg        string
	sessionKey string
}

// NewAlletraClient creates a new instance of the HPE Alletra Storage HTTP client.
func NewAlletraClient(logger logger.Logger, url string, username string, password string, verifyTLS bool, cpg string) *AlletraClient {
	return &AlletraClient{
		logger:    logger,
		url:       url,
		username:  username,
		password:  password,
		verifyTLS: verifyTLS,
		cpg:       cpg,
	}
}

// hpePort represents a port in HPE Storage.
type hpePort struct {
	Protocol  int    `json:"protocol"`
	NodeWWN   string `json:"nodeWWN"`
	LinkState int    `json:"linkState"`
	IPAddr    string `json:"IPAddr"`
}

// hpePortPos represents the port position in HPE Storage.
type hpePortPos struct {
	Node     int `json:"node"`
	Slot     int `json:"slot"`
	CardPort int `json:"cardPort"`
}

// hpeFCPath represents a Fibre Channel Path in HPE Storage.
type hpeFCPath struct {
	WWN     string     `json:"wwn"`
	PortPos hpePortPos `json:"portPos"`
}

// hpeISCSIPath represents an iSCSI Path in HPE Storage.
type hpeISCSIPath struct {
	Name      string `json:"name"`
	IPAddr    string `json:"IPAddr"`
	HostSpeed int    `json:"hostSpeed"`
}

// hpeNVMETCPPath represents a NVMe TCP Path in HPE Storage.
type hpeNVMETCPPath struct {
	IP      string     `json:"IP"`
	PortPos hpePortPos `json:"portPos"`
	NQN     string     `json:"nqn"`
}

// hpeHost represents a host in HPE Storage.
type hpeHost struct {
	ID           int              `json:"id"`
	Name         string           `json:"name"`
	FCPaths      []hpeFCPath      `json:"FCPaths"`
	ISCSIPaths   []hpeISCSIPath   `json:"iSCSIPaths"`
	NVMETCPPaths []hpeNVMETCPPath `json:"NVMETCPPaths"`
}

type hpeVolume struct {
	Name         string `json:"name"`
	TotalUsedMiB int64  `json:"totalUsedMiB"`
	SizeMiB      int64  `json:"sizeMiB"`
	NGUID        string `json:"nguid"`
	WWN          string `json:"wwn"`
}

type hpeVLUN struct {
	LUN      int    `json:"lun"`
	Hostname string `json:"hostname"`
	Serial   string `json:"serial"`
}

// hpeTaskState represents a HPE Alletra task state.
type hpeTaskState struct {
	Status int `json:"status"`
}

// hpePromoteResponse represents a HPE Alletra virtual copy promote response.
type hpePromoteResponse struct {
	TaskID int `json:"taskid"`
}

// hpeCreatePhysicalCopyResponse represents a HPE Alletra create physical copy response.
type hpeCreatePhysicalCopyResponse struct {
	TaskID int `json:"taskid"`
}

type hpeRespMembers[T any] struct {
	Total   int `json:"total"`
	Members []T `json:"members"`
}

type hpeSpaceReport struct {
	OverprovisionedUsedMiB int64 `json:"overprovisionedUsedMiB"`
	OverProvisionedFreeMiB int64 `json:"overProvisionedFreeMiB"`
}

// hpeError represents an error response from the HPE Storage API.
type hpeError struct {
	Code           int    `json:"code"`
	Desc           string `json:"desc"`
	HTTPStatusCode int
}

// Error implements the error interface for hpeError.
func (p *hpeError) Error() string {
	if p == nil {
		return ""
	}

	return fmt.Sprintf("HTTP Error Code: %d. Alletra WSAPI Error Code: %d. Alletra WSAPI Description: %s", p.HTTPStatusCode, p.Code, p.Desc)
}

// isHpeErrorOf checks if the error is of type hpeError and matches the status code.
func isHpeErrorOf(err error, statusCode int, substrings ...string) bool {
	perr, ok := err.(*hpeError)
	if !ok {
		return false
	}

	if perr.Code != statusCode {
		return false
	}

	if len(substrings) == 0 {
		return true
	}

	errMsg := strings.ToLower(perr.Desc)
	for _, substring := range substrings {
		if strings.Contains(errMsg, strings.ToLower(substring)) {
			return true
		}
	}

	return false
}

// hpeIsNotFoundError returns true if the error is of type hpeError, its status code is 400 (bad request),
// and the error message contains a substring indicating the resource was not found.
func isHpeErrorNotFound(err error) bool {
	return isHpeErrorOf(err, http.StatusNotFound, "Not found", "Does not exist", "No such volume or snapshot")
}

// request issues a HTTP request against Alletra Storage WSAPI.
func (p *AlletraClient) request(method string, url url.URL, reqBody map[string]any, reqHeaders map[string]string, respBody any, respHeaders map[string]string) error {
	// Extract scheme and host from the gateway URL.
	scheme, host, found := strings.Cut(p.url, "://")
	if !found {
		return fmt.Errorf("Invalid Alletra Storage WSAPI URL: %q", p.url)
	}

	// Set request URL scheme and host.
	url.Scheme = scheme
	url.Host = host

	var err error
	var reqBodyReader io.Reader

	if reqBody != nil {
		reqBodyReader, err = createBodyReader(reqBody)
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
				InsecureSkipVerify: p.verifyTLS,
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Failed to send request: %w", err)
	}

	defer resp.Body.Close()

	var responseBodyBuffer bytes.Buffer
	teeReader := io.TeeReader(resp.Body, &responseBodyBuffer)
	bodyBytes, err := io.ReadAll(teeReader)
	if err != nil {
		return fmt.Errorf("Failed to read response body for TeeReader: %w", err)
	}

	if resp.Header.Get("Content-Type") != "application/json" && len(bodyBytes) > 0 {
		return fmt.Errorf("Response Content-type: %q. Only application/json is allowed for non-empty response body", resp.Header.Get("Content-Type"))
	}

	if resp.StatusCode >= 400 {
		respBody = &hpeError{}
		err := json.Unmarshal(bodyBytes, respBody)
		if err != nil {
			return fmt.Errorf("HPE failed to parse WSAPI error response: %w", err)
		}
	}

	// Extract the response body if requested.
	if len(bodyBytes) > 0 {
		err = json.Unmarshal(bodyBytes, &respBody)
		if err != nil {
			return fmt.Errorf("HPE failed to read response body from %q: %w", url.String(), err)
		}
	}

	// Extract the response headers if requested.
	if respHeaders != nil {
		for k, v := range resp.Header {
			respHeaders[k] = strings.Join(v, ",")
		}
	}

	// Return the formatted error from the body
	hpeErr, assert := respBody.(*hpeError)
	if assert {
		hpeErr.HTTPStatusCode = resp.StatusCode

		// The unauthorized error is reported when an invalid (or expired) access token is provided.
		// Wrap unauthorized requests into an API status error to allow easier checking for expired
		// token in the requestAuthenticated function.
		if resp.StatusCode == http.StatusForbidden && hpeErr.Code == hpeAPIErrorInvalidSessionKey {
			return api.StatusErrorf(http.StatusForbidden, "Unauthorized request")
		}

		return hpeErr
	}

	return nil
}

var sessionKeys = make(map[string]string)
var sessionKeysMtx = &sync.RWMutex{}

// getSessionKeysCacheKey() generates a hashtable key for a session key.
func (p *AlletraClient) getSessionKeysCacheKey() string {
	return p.url + p.username + p.password
}

// invalidateSessionKey() removes a session key from the cache.
func (p *AlletraClient) invalidateSessionKey() {
	key := p.getSessionKeysCacheKey()

	sessionKeysMtx.Lock()
	delete(sessionKeys, key)
	p.sessionKey = ""
	sessionKeysMtx.Unlock()
}

// cacheSessionKey() adds a session key to the cache.
func (p *AlletraClient) cacheSessionKey(sessionKey string) {
	key := p.getSessionKeysCacheKey()

	sessionKeysMtx.Lock()
	sessionKeys[key] = sessionKey
	p.sessionKey = sessionKey
	sessionKeysMtx.Unlock()
}

// getSessionKey() gets a session key from the cache.
func (p *AlletraClient) getSessionKey() bool {
	key := p.getSessionKeysCacheKey()

	sessionKeysMtx.RLock()
	defer sessionKeysMtx.RUnlock()

	sessionKey, ok := sessionKeys[key]
	if ok {
		p.sessionKey = sessionKey
		return true
	}

	return false
}

// requestAuthenticated issues an authenticated HTTP request against the HPE Storage gateway.
// In case the Session Key is expired, the function will try to obtain a new one.
func (p *AlletraClient) requestAuthenticated(method string, url url.URL, reqBody map[string]any, respBody any) error {
	// If request fails with an unauthorized error, the request will be retried after
	// requesting a new session key.
	retries := 1

	for {
		// Ensure we are logged into the WSAPI.
		err := p.login()
		if err != nil {
			return err
		}

		// Add Session Key to Headers.
		reqHeaders := map[string]string{
			"X-HP3PAR-WSAPI-SessionKey": p.sessionKey,
		}

		// Initiate request.
		err = p.request(method, url, reqBody, reqHeaders, &respBody, nil)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusForbidden) && retries > 0 {
				// Session key seems to be expired.
				// Reset the token and try one more time.
				p.invalidateSessionKey()
				retries--
				continue
			}

			hpeErr, assert := err.(*hpeError)
			if assert {
				p.logger.Debug("Alletra WSAPI Error", logger.Ctx{"httpStatusCode": hpeErr.HTTPStatusCode, "wsapiCode": hpeErr.Code, "wsapiDesc": hpeErr.Desc})
			} else {
				p.logger.Debug("Alletra WSAPI Error", logger.Ctx{"err": err})
			}

			// Either the error is not of type unauthorized or the maximum number of
			// retries has been exceeded.
			return err
		}

		return nil
	}
}

// login initiates request() using WSAPI username and password.
// If successful then Session Key is retrieved and stored within client structure.
// Once stored the Session Key is reused for further requests.
func (p *AlletraClient) login() error {
	if p.getSessionKey() {
		return nil
	}

	var respBody struct {
		Key  string `json:"key"`
		Desc string `json:"desc"`
	}

	body := map[string]any{
		"user":        p.username,
		"password":    p.password,
		"sessionType": hpeSessionTypeRegular,
	}

	url := api.NewURL().Path("api", "v1", "credentials")
	respHeaders := make(map[string]string)

	err := p.request(http.MethodPost, url.URL, body, nil, &respBody, respHeaders)
	if err != nil {
		return fmt.Errorf("Failed to send login request to HPE Alletra WSAPI: %w", err)
	}

	if respBody.Key == "" {
		return errors.New("Received an empty Session Key from HPE Alletra WSAPI")
	}

	p.cacheSessionKey(respBody.Key)
	return nil
}

// CreateVolumeSet creates a volume set (representation of LXD storage pool).
func (p *AlletraClient) CreateVolumeSet(volumeSetName string) error {
	req := map[string]any{
		"name":    volumeSetName,
		"comment": "Created and managed by LXD",
	}

	url := api.NewURL().Path("api", "v1", "volumesets")
	err := p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("Failed to create volume set for a storage pool %q: %w", volumeSetName, err)
	}

	return nil
}

// DeleteVolumeSet deletes a volume set.
func (p *AlletraClient) DeleteVolumeSet(volumeSetName string) error {
	url := api.NewURL().Path("api", "v1", "volumesets", volumeSetName)
	err := p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete volume set for a storage pool %q: %w", volumeSetName, err)
	}

	return nil
}

// modifyVolumeSet is used to add/remove a volume from a volume set.
// Argument action can be hpeAPIVolumeSetMemberAdd or hpeAPIVolumeSetMemberRemove.
func (p *AlletraClient) modifyVolumeSet(volumeSetName string, action int, volName string) error {
	req := map[string]any{
		"action":     action,
		"setmembers": []string{volName},
	}

	url := api.NewURL().Path("api", "v1", "volumesets", volumeSetName)
	err := p.requestAuthenticated(http.MethodPut, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("Failed to modify volume set for a storage pool %q: %w", volumeSetName, err)
	}

	return nil
}

// getHosts retrieves an existing HPE Alletra Storage host info.
func (p *AlletraClient) getHosts() ([]hpeHost, error) {
	var resp hpeRespMembers[hpeHost]

	url := api.NewURL().Path("api", "v1", "hosts")
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to get hosts: %w", err)
	}

	return resp.Members, nil
}

// GetCurrentHost retrieves the HPE Alletra Storage host linked to the current LXD host.
// The Alletra Storage host is considered a match if it includes the fully qualified
// name of the LXD host that is determined by the configured mode.
func (p *AlletraClient) GetCurrentHost(connectorType string, qn string) (*hpeHost, error) {
	hosts, err := p.getHosts()
	if err != nil {
		return nil, err
	}

	for _, host := range hosts {
		if connectorType == connectors.TypeISCSI {
			for _, iscsiPath := range host.ISCSIPaths {
				if iscsiPath.Name == qn {
					return &host, nil
				}
			}
		}

		if connectorType == connectors.TypeNVME {
			for _, nvmePath := range host.NVMETCPPaths {
				if nvmePath.NQN == qn {
					return &host, nil
				}
			}
		}
	}

	return nil, api.StatusErrorf(http.StatusNotFound, "Host with qualified name %q not found", qn)
}

// CreateHost creates a new host with provided initiator qualified names that can be associated
// with specific volumes.
func (p *AlletraClient) CreateHost(connectorType string, hostName string, qns []string) error {
	req := map[string]any{
		"descriptors": map[string]any{
			"comment": "Created and managed by LXD",
		},
	}

	req["name"] = hostName

	switch connectorType {
	case connectors.TypeISCSI:
		req["iSCSINames"] = qns
	case connectors.TypeNVME:
		req["NQN"] = qns[0]
		req["transportType"] = hpeTransportTypeTCP
	default:
		return fmt.Errorf("Unsupported HPE Alletra Storage mode %q", connectorType)
	}

	url := api.NewURL().Path("api", "v1", "hosts")
	err := p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {
		hpeErr, ok := err.(*hpeError)
		if ok {
			switch hpeErr.Code {
			case hpeAPIErrorExistentHost:
				return nil
			default:
				return fmt.Errorf("Unexpected Alletra WSAPI response: Code: %d. Desc: %q", hpeErr.Code, hpeErr.Desc)
			}
		}

		return fmt.Errorf("Failed to create host %q: %w", hostName, err)
	}

	return nil
}

// DeleteHost deletes an existing host.
func (p *AlletraClient) DeleteHost(hostName string) error {
	url := api.NewURL().Path("api", "v1", "hosts", hostName)
	err := p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete host %q: %w", hostName, err)
	}

	return nil
}

// UpdateHost updates an existing host. This should be never called
// and only needed to make code sharing with Pure easier.
func (p *AlletraClient) UpdateHost(hostName string, qns []string) error {
	return fmt.Errorf("Failed to update host %q. Operation not supported", hostName)
}

func (p *AlletraClient) createVolume(poolName string, volName string, sizeBytes int64) error {
	req := map[string]any{
		"name":    volName,
		"cpg":     p.cpg,
		"sizeMiB": sizeBytes / 1024 / 1024,
		"tpvv":    true, // thinly provisioned volume
	}

	url := api.NewURL().Path("api", "v1", "volumes")
	err := p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("Failed to create volume %q in storage pool %q: %w", volName, poolName, err)
	}

	return nil
}

// CreateVolume creates a new volume in the given storage pool (volume set). The volume is created with
// supplied size in bytes.
func (p *AlletraClient) CreateVolume(poolName string, volName string, sizeBytes int64) error {
	err := p.createVolume(poolName, volName, sizeBytes)
	if err != nil {
		return err
	}

	// Add a newly created volume to a volume set
	err = p.modifyVolumeSet(poolName, hpeAPIVolumeSetMemberAdd, volName)
	return err
}

// GetVolume returns the volume for a given volName.
func (p *AlletraClient) GetVolume(poolName string, volName string) (*hpeVolume, error) {
	var resp hpeVolume

	url := api.NewURL().Path("api", "v1", "volumes", volName)
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		hpeErr, ok := err.(*hpeError)
		if ok {
			switch hpeErr.Code {
			case hpeAPIErrorNonExistentVol:
				return nil, api.StatusErrorf(http.StatusNotFound, "Volume (or snapshot) %q not found", volName)
			default:
				return nil, fmt.Errorf("Unexpected Alletra WSAPI response: Code: %d. Desc: %q", hpeErr.Code, hpeErr.Desc)
			}
		}

		return nil, fmt.Errorf("Failed to get hpeVolume %q: %w", volName, err)
	}

	if resp.Name == "" {
		return nil, fmt.Errorf("Unexpected Alletra WSAPI response: volume %q exists but has empty name", volName)
	}

	return &resp, nil
}

// deleteVolume deletes a volume or snapshot. Recursively.
func (p *AlletraClient) deleteVolume(poolName string, volName string) error {
	url := api.NewURL().Path("api", "v1", "volumes", volName).WithQuery("cascade", "true")

	err := p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
	if err != nil {
		hpeErr, ok := err.(*hpeError)
		if ok {
			switch hpeErr.Code {
			case hpeAPIErrorNonExistentVol:
				return nil
			default:
				return fmt.Errorf("Unexpected Alletra WSAPI response: Code: %d. Desc: %q", hpeErr.Code, hpeErr.Desc)
			}
		}

		return fmt.Errorf("Failed to delete volume (or snapshot) %q in pool %q: %w", volName, poolName, err)
	}

	return nil
}

// DeleteVolume deletes an exisiting volume in the given storage pool.
func (p *AlletraClient) DeleteVolume(poolName string, volName string) error {
	err := p.modifyVolumeSet(poolName, hpeAPIVolumeSetMemberRemove, volName)
	if err != nil {
		return err
	}

	return p.deleteVolume(poolName, volName)
}

// GetTargetAddrs gets an information about IP addresses of storage array targets.
func (p *AlletraClient) GetTargetAddrs(connectorType string) (targetAddrs []string, err error) {
	var portData hpeRespMembers[hpePort]

	apiPorts := api.NewURL().Path("api", "v1", "ports")

	err = p.requestAuthenticated(http.MethodGet, apiPorts.URL, nil, &portData)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve port list: %w", err)
	}

	if len(portData.Members) == 0 {
		return nil, errors.New("Alletra no Ports information found")
	}

	for _, member := range portData.Members {
		if member.LinkState != hpeLinkStateReady {
			continue // skip down or unlinked ports
		}

		switch connectorType {
		case connectors.TypeISCSI:
			if member.Protocol != hpePortProtocolISCSI {
				continue
			}

			if member.IPAddr != "" {
				targetAddrs = append(targetAddrs, member.IPAddr)
			}

		case connectors.TypeNVME:
			if member.Protocol != hpePortProtocolNVME {
				continue
			}

			if member.NodeWWN != "" {
				targetAddrs = append(targetAddrs, member.NodeWWN)
			}
		}
	}

	return targetAddrs, nil
}

// ConnectHostToVolume creates a connection between a host and volume. It returns true if the connection
// was created, and false if it already existed.
func (p *AlletraClient) ConnectHostToVolume(poolName string, volName string, hostName string) (bool, error) {
	vlun, errVLUN := p.GetVLUN(volName)
	if errVLUN != nil {
		return false, fmt.Errorf("HPE Error %w", errVLUN)
	}

	if vlun != nil && vlun.Hostname == hostName {
		p.logger.Debug("No need to connect host to volume as there is a vLUN", logger.Ctx{"volName": volName, "hostName": hostName})
		return false, nil
	}

	url := api.NewURL().Path("api", "v1", "vluns")

	req := make(map[string]any)

	req["hostname"] = hostName
	req["volumeName"] = volName
	req["lun"] = 0
	req["autoLun"] = true

	err := p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {
		hpeErr, ok := err.(*hpeError)
		if ok {
			switch hpeErr.Code {
			case hpeAPIErrorVolumeIsAlreadyExported:
				p.logger.Debug("New vLUN hasn't been created. Volume %q already attached to %q", logger.Ctx{"volName": volName, "hostName": hostName})
				return false, nil
			default:
				return false, fmt.Errorf("Unexpected Alletra WSAPI response: Code: %d. Desc: %q", hpeErr.Code, hpeErr.Desc)
			}
		}

		return false, fmt.Errorf("Failed to connect volume %q with host %q: %w", volName, hostName, err)
	}

	return true, nil
}

// DisconnectHostFromVolume deletes a connection (vLUN) between a host and volume.
func (p *AlletraClient) DisconnectHostFromVolume(poolName string, volName string, hostName string) error {
	vlun, errVLUN := p.GetVLUN(volName)
	if errVLUN != nil {
		return fmt.Errorf("HPE Error %w", errVLUN)
	}

	if vlun == nil {
		p.logger.Debug("No need to disconnect host from volume as there is no vLUN", logger.Ctx{"volName": volName, "hostName": hostName})
		return nil
	}

	customParam := volName + "," + strconv.Itoa(vlun.LUN) + "," + hostName
	url := api.NewURL().Path("api", "v1", "vluns", customParam)

	err := p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
	if err != nil {
		if isHpeErrorNotFound(err) {
			return api.StatusErrorf(http.StatusNotFound, "Connection between host %q and volume %q not found", volName, hostName)
		}

		return fmt.Errorf("Failed to disconnect volume %q from host %q: %w", volName, hostName, err)
	}

	return nil
}

// GetVLUN returns vLUNs list for a given volumeName.
func (p *AlletraClient) GetVLUN(volumeName string) (*hpeVLUN, error) {
	var resp hpeRespMembers[hpeVLUN]

	url := api.NewURL().Path("api", "v1", "vluns").WithQuery("query", `"volumeName==`+volumeName+`"`)

	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Fail to get vLUN data for volume %q: %w", volumeName, err)
	}

	if len(resp.Members) == 0 {
		p.logger.Debug("No VLUN found for volume: %q", logger.Ctx{"volumeName": volumeName})
		return nil, nil
	}

	return &resp.Members[0], nil
}

// GetVLUNsForHost returns vLUNs list for a given hostName.
func (p *AlletraClient) GetVLUNsForHost(hostName string) ([]hpeVLUN, error) {
	var resp hpeRespMembers[hpeVLUN]

	url := api.NewURL().Path("api", "v1", "vluns").WithQuery("query", `"hostname==`+hostName+`"`)

	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to get vLUNs list for host %q: %w", hostName, err)
	}

	return resp.Members, nil
}

// GrowVolume resize an existing volume. This function does not resize any filesystem inside the volume.
func (p *AlletraClient) GrowVolume(poolName string, volName string, sizeBytes int64) error {
	req := map[string]any{
		"action":  hpeAPIActionGrowVolume,
		"sizeMiB": sizeBytes / (1024 * 1024),
	}

	url := api.NewURL().Path("api", "v1", "volumes", volName)
	err := p.requestAuthenticated(http.MethodPut, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("Failed to resize volume %q in storage pool %q: %w", volName, poolName, err)
	}

	return nil
}

// GetVolumeSnapshots retrieves all existing snapshot for the given storage volume.
func (p *AlletraClient) GetVolumeSnapshots(poolName string, volName string) ([]hpeVolume, error) {
	var resp hpeRespMembers[hpeVolume]

	url := api.NewURL().Path("api", "v1", "volumes").WithQuery("query", fmt.Sprintf(`"copyOf==%s"`, volName))

	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve snapshots for volume %q in storage pool %q: %w", volName, poolName, err)
	}

	return resp.Members, nil
}

// GetVolumeSnapshot retrieves an existing snapshot for the given storage volume.
func (p *AlletraClient) GetVolumeSnapshot(poolName string, volName string, snapshotName string) (*hpeVolume, error) {
	return p.GetVolume(poolName, snapshotName)
}

// CreateVolumeSnapshot creates a new snapshot for the given storage volume.
func (p *AlletraClient) CreateVolumeSnapshot(poolName string, volName string, snapshotName string) error {
	req := map[string]any{
		"action": "createSnapshot",
		"parameters": map[string]any{
			"name": snapshotName,
		},
	}

	url := api.NewURL().Path("api", "v1", "volumes", volName)
	err := p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("Failed to create snapshot %q for volume %q in storage pool %q: %w", snapshotName, volName, poolName, err)
	}

	return nil
}

// DeleteVolumeSnapshot deletes an existing snapshot for the given storage volume.
func (p *AlletraClient) DeleteVolumeSnapshot(poolName string, volName string, snapshotName string) error {
	err := p.deleteVolume(poolName, snapshotName)
	if err != nil {
		return fmt.Errorf("Failed to delete snapshot %q for volume %q in storage pool %q: %w", snapshotName, volName, poolName, err)
	}

	return nil
}

// getTaskState retrieves a running task state.
func (p *AlletraClient) getTaskState(taskID string) (*hpeTaskState, error) {
	var resp hpeTaskState

	url := api.NewURL().Path("api", "v1", "tasks", taskID).WithQuery("view", "excludeDetail")
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		if isHpeErrorNotFound(err) {
			return nil, api.StatusErrorf(http.StatusNotFound, "Task with ID %q not found", taskID)
		}

		return nil, fmt.Errorf("Failed to retrieve task with ID %q: %w", taskID, err)
	}

	return &resp, nil
}

// waitTaskFinish waits for HPE Alletra task to finish with any result (error, canceled, success).
func (p *AlletraClient) waitTaskFinish(ctx context.Context, taskID string) (int, error) {
	_, ok := ctx.Deadline()
	if !ok {
		// Set a default timeout of 180 seconds for the context
		// if no deadline is already configured.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 180*time.Second)
		defer cancel()
	}

	for {
		taskState, err := p.getTaskState(taskID)
		if err != nil {
			return -1, err
		}

		if taskState.Status != hpeTaskStatusActive {
			return taskState.Status, nil
		}

		if ctx.Err() != nil {
			return -1, ctx.Err()
		}

		time.Sleep(5 * time.Second)
	}
}

// RestoreVolumeSnapshot restores the volume by copying the volume snapshot into its parent volume.
func (p *AlletraClient) RestoreVolumeSnapshot(ctx context.Context, poolName string, volName string, snapshotName string) error {
	req := map[string]any{
		"action": hpeAPIActionPromoteVirtualCopy,
	}

	var resp hpePromoteResponse

	url := api.NewURL().Path("api", "v1", "volumes", snapshotName)

	err := p.requestAuthenticated(http.MethodPut, url.URL, req, &resp)
	if err != nil {
		return fmt.Errorf(`Failed to restore snapshot "%s/%s" to "%s/%s": %w`, poolName, snapshotName, poolName, volName, err)
	}

	status, err := p.waitTaskFinish(ctx, strconv.Itoa(resp.TaskID))
	if err != nil {
		return fmt.Errorf(`Failed to wait for restore snapshot operation "%s/%s" to "%s/%s": %w`, poolName, snapshotName, poolName, volName, err)
	}

	switch status {
	case hpeTaskStatusDone:
		return nil
	case hpeTaskStatusActive:
		return fmt.Errorf(`Failed to restore snapshot "%s/%s" to "%s/%s": timeout`, poolName, snapshotName, poolName, volName)
	case hpeTaskStatusCanceled:
		return fmt.Errorf(`Failed to restore snapshot "%s/%s" to "%s/%s": cancelled`, poolName, snapshotName, poolName, volName)
	case hpeTaskStatusFailed:
		return fmt.Errorf(`Failed to restore snapshot "%s/%s" to "%s/%s": task failed`, poolName, snapshotName, poolName, volName)
	default:
		return fmt.Errorf(`Failed to restore snapshot "%s/%s" to "%s/%s": unknown task state. Alletra API change?`, poolName, snapshotName, poolName, volName)
	}
}

// CreateVolumePhysicalCopy creates a new physical copy for the given storage volume or snapshot.
func (p *AlletraClient) CreateVolumePhysicalCopy(ctx context.Context, poolName string, volName string, copyName string) error {
	req := map[string]any{
		"action": "createPhysicalCopy",
		"parameters": map[string]any{
			"destVolume":   copyName,
			"saveSnapshot": false,
			"priority":     hpeOperationPriorityHigh,
		},
	}

	var resp hpeCreatePhysicalCopyResponse

	url := api.NewURL().Path("api", "v1", "volumes", volName)
	err := p.requestAuthenticated(http.MethodPost, url.URL, req, &resp)
	if err != nil {
		return fmt.Errorf("Failed to create a physical copy %q for volume/snapshot %q in storage pool %q: %w", copyName, volName, poolName, err)
	}

	status, err := p.waitTaskFinish(ctx, strconv.Itoa(resp.TaskID))
	if err != nil {
		return fmt.Errorf(`Failed to wait for create a physical copy operation "%s/%s" to "%s/%s": %w`, poolName, volName, poolName, copyName, err)
	}

	switch status {
	case hpeTaskStatusDone:
		return nil
	case hpeTaskStatusActive:
		return fmt.Errorf(`Failed to create a physical copy "%s/%s" to "%s/%s": timeout`, poolName, volName, poolName, copyName)
	case hpeTaskStatusCanceled:
		return fmt.Errorf(`Failed to create a physical copy "%s/%s" to "%s/%s": cancelled`, poolName, volName, poolName, copyName)
	case hpeTaskStatusFailed:
		return fmt.Errorf(`Failed to create a physical copy "%s/%s" to "%s/%s": task failed`, poolName, volName, poolName, copyName)
	default:
		return fmt.Errorf(`Failed to create a physical copy "%s/%s" to "%s/%s": unknown task state. Alletra API change?`, poolName, volName, copyName, volName)
	}
}

// GetCPGSpaceReport retrieves a space report about CPG.
func (p *AlletraClient) GetCPGSpaceReport() (*hpeSpaceReport, error) {
	var resp hpeSpaceReport

	req := map[string]any{
		"cpg": p.cpg,
	}

	url := api.NewURL().Path("api", "v1", "spacereporter")

	err := p.requestAuthenticated(http.MethodPost, url.URL, req, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve a space report for a CPG %q: %w", p.cpg, err)
	}

	return &resp, nil
}
