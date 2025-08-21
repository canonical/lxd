package drivers

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dell/goscaleio"
	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
)

// powerFlexBlockVolSuffix suffix used for block content type volumes.
const powerFlexBlockVolSuffix = ".b"

// powerFlexISOVolSuffix suffix used for iso content type volumes.
const powerFlexISOVolSuffix = ".i"

// powerFlexCodes are returned by the API in case of error.
const powerFlexCodeVolumeNotFound = 79
const powerFlexCodeDomainNotFound = 142
const powerFlexCodeNameTooLong = 226
const powerFlexInvalidMapping = 4039

type powerFlexVolumeType string
type powerFlexSnapshotMode string

const powerFlexVolumeThin powerFlexVolumeType = "ThinProvisioned"
const powerFlexVolumeThick powerFlexVolumeType = "ThickProvisioned"

const powerFlexSnapshotRW powerFlexSnapshotMode = "ReadWrite"

// powerFlexVolTypePrefixes maps volume type to storage volume name prefix.
// Use smallest possible prefixes since PowerFlex volume names are limited to 31 characters.
var powerFlexVolTypePrefixes = map[VolumeType]string{
	VolumeTypeContainer: "c",
	VolumeTypeVM:        "v",
	VolumeTypeImage:     "i",
	VolumeTypeCustom:    "u",
}

// powerFlexSnapshotPrefix is a prefix used ONLY for actual PowerFlex snapshots of a volume.
// It is NOT used for snapshots made when copying a volume with powerflex.snapshot_copy=true.
var powerFlexSnapshotPrefix = "s"

// powerFlexError contains arbitrary error responses from PowerFlex.
// The maps values can be of various types.
// Reading of the actual values is performed by specific receiver
// functions which are implemented on the type itself.
type powerFlexError map[string]any

// Error tries to return all kinds of errors from the PowerFlex API in a nicely formatted way.
func (p *powerFlexError) Error() string {
	errorStrings := make([]string, 0, len(*p))
	for k, v := range *p {
		errorStrings = append(errorStrings, fmt.Sprint(k, ": ", v))
	}

	return strings.Join(errorStrings, ", ")
}

// ErrorCode extracts the errorCode value from a PowerFlex response.
func (p *powerFlexError) ErrorCode() float64 {
	// In case the errorCode value is returned from the PowerFlex API,
	// the respective integer value gets unmarshalled as float64.
	// See https://pkg.go.dev/encoding/json#Unmarshal for JSON numbers.
	code, ok := (*p)["errorCode"].(float64)
	if !ok {
		return 0
	}

	return code
}

// HTTPStatusCode extracts the httpStatusCode value from a PowerFlex response.
func (p *powerFlexError) HTTPStatusCode() float64 {
	// In case the httpStatusCode value is returned from the PowerFlex API,
	// the respective integer value gets unmarshalled as float64.
	// See https://pkg.go.dev/encoding/json#Unmarshal for JSON numbers.
	code, ok := (*p)["httpStatusCode"].(float64)
	if !ok {
		return 0
	}

	return code
}

// powerFlexStoragePool represents a storage pool in PowerFlex.
type powerFlexStoragePool struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	ProtectionDomainID string `json:"protectionDomainId"`
	ZeroPaddingEnabled bool   `json:"zeroPaddingEnabled"`
}

// powerFlexStoragePoolStatistics represents the statistics of a storage pool in PowerFlex.
type powerFlexStoragePoolStatistics struct {
	// Unused usable storage capacity.
	NetUnusedCapacityInKb uint64 `json:"netUnusedCapacityInKb"`
	// Actual used storage capacity.
	NetCapacityInUseInKb uint64 `json:"netCapacityInUseInKb"`
}

// powerFlexProtectionDomain represents a protection domain in PowerFlex.
type powerFlexProtectionDomain struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	SystemID string `json:"systemId"`
}

// powerFlexProtectionDomainStoragePool represents a storage pool related to a protection domain in PowerFlex.
type powerFlexProtectionDomainStoragePool struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// powerFlexProtectionDomainSDSRelation represents an SDS related to a protection domain in PowerFlex.
type powerFlexProtectionDomainSDTRelation struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IPList []struct {
		IP string `json:"ip"`
	} `json:"ipList"`
}

// powerFlexSDC represents a SDC host in PowerFlex.
// The same data structure is used to identify NVMe/TCP hosts.
type powerFlexSDC struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	HostType string `json:"hostType"`
	NQN      string `json:"nqn"`
	SDCGuid  string `json:"sdcGuid"`
	SystemID string `json:"systemId"`
}

// powerFlexVolume represents a volume in PowerFlex.
type powerFlexVolume struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	VolumeType       string `json:"volumeType"`
	VTreeID          string `json:"vtreeId"`
	AncestorVolumeID string `json:"ancestorVolumeId"`
	// PowerFlex reports the value in KB but it's actually KiB.
	SizeInKiB     int64 `json:"sizeInKb"`
	MappedSDCInfo []struct {
		SDCID    string `json:"sdcId"`
		SDCName  string `json:"sdcName"`
		NQN      string `json:"nqn"`
		HostType string `json:"hostType"`
	} `json:"mappedSdcInfo"`
}

// powerFlexClient holds the PowerFlex HTTP client and an access token factory.
type powerFlexClient struct {
	driver *powerflex
	token  string
}

// newPowerFlexClient creates a new instance of the HTTP PowerFlex client.
func newPowerFlexClient(driver *powerflex) *powerFlexClient {
	return &powerFlexClient{
		driver: driver,
	}
}

// createBodyReader creates a reader for the given request body contents.
func (p *powerFlexClient) createBodyReader(contents map[string]any) (io.Reader, error) {
	body := &bytes.Buffer{}
	encoder := json.NewEncoder(body)
	err := encoder.Encode(contents)
	if err != nil {
		return nil, fmt.Errorf("Failed to write request body: %w", err)
	}

	return body, nil
}

// request issues a HTTP request against the PowerFlex gateway.
func (p *powerFlexClient) request(method string, path string, body io.Reader, response any) error {
	url := p.driver.config["powerflex.gateway"] + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return fmt.Errorf("Failed to create request: %w", err)
	}

	req.Header.Add("Accept", "application/json")
	if body != nil {
		req.Header.Add("Content-Type", "application/json")
	}

	if p.token != "" {
		req.Header.Add("Authorization", "Bearer "+p.token)
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: shared.IsFalse(p.driver.config["powerflex.gateway.verify"]),
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Failed to send request: %w", err)
	}

	defer resp.Body.Close()

	// Exit right away if not authorized.
	// We cannot parse the returned body since it's not in JSON format.
	if resp.StatusCode == http.StatusUnauthorized && resp.Header.Get("Content-Type") != "application/json" {
		return api.NewStatusError(http.StatusUnauthorized, "Unauthorized request")
	}

	// Overwrite the response data type if an error is detected.
	// Both HTTP status code and PowerFlex error code get mapped to the
	// custom error struct from the response body.
	if resp.StatusCode != http.StatusOK {
		response = &powerFlexError{}
	}

	if response != nil {
		decoder := json.NewDecoder(resp.Body)
		err = decoder.Decode(response)
		if err != nil {
			return fmt.Errorf("Failed to read response body: %s: %w", path, err)
		}
	}

	// Return the formatted error from the body
	powerFlexErr, ok := response.(*powerFlexError)
	if ok {
		return powerFlexErr
	}

	return nil
}

// requestAuthenticated issues an authenticated HTTP request against the PowerFlex gateway.
func (p *powerFlexClient) requestAuthenticated(method string, path string, body map[string]any, response any) error {
	retries := 0
	for {
		err := p.login()
		if err != nil {
			return err
		}

		// In the loop create a reader of the request body to allow retry of the request.
		// The reader provided for the request's body will be read after the first request.
		var bodyReader io.Reader
		if body != nil {
			bodyReader, err = p.createBodyReader(body)
			if err != nil {
				return fmt.Errorf("Failed to create reader from request's body: %w", err)
			}
		}

		err = p.request(method, path, bodyReader, response)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusUnauthorized) && retries == 0 {
				// Access token seems to be expired.
				// Reset the token and try one more time.
				p.token = ""
				retries++
				continue
			}

			// Non unauthorized error or retries exceeded.
			return err
		}

		return nil
	}
}

// login creates a new access token and authenticates the client.
func (p *powerFlexClient) login() error {
	if p.token != "" {
		return nil
	}

	body, err := p.createBodyReader(map[string]any{
		"username": p.driver.config["powerflex.user.name"],
		"password": p.driver.config["powerflex.user.password"],
	})
	if err != nil {
		return err
	}

	var actualResponse struct {
		AccessToken string `json:"access_token"`
	}

	err = p.request(http.MethodPost, "/rest/auth/login", body, &actualResponse)
	if err != nil {
		return fmt.Errorf("Failed to login: %w", err)
	}

	p.token = actualResponse.AccessToken
	return nil
}

// getStoragePool returns the storage pool behind poolID.
func (p *powerFlexClient) getStoragePool(poolID string) (*powerFlexStoragePool, error) {
	var actualResponse powerFlexStoragePool
	err := p.requestAuthenticated(http.MethodGet, "/api/instances/StoragePool::"+poolID, nil, &actualResponse)
	if err != nil {
		return nil, fmt.Errorf("Failed to get storage pool: %q: %w", poolID, err)
	}

	return &actualResponse, nil
}

// getStoragePoolStatistics returns the storage pools statistics.
func (p *powerFlexClient) getStoragePoolStatistics(poolID string) (*powerFlexStoragePoolStatistics, error) {
	var actualResponse powerFlexStoragePoolStatistics
	err := p.requestAuthenticated(http.MethodGet, "/api/instances/StoragePool::"+poolID+"/relationships/Statistics", nil, &actualResponse)
	if err != nil {
		return nil, fmt.Errorf("Failed to get storage pool statistics: %q: %w", poolID, err)
	}

	return &actualResponse, nil
}

// getProtectionDomainID returns the ID of the protection domain behind domainName.
func (p *powerFlexClient) getProtectionDomainID(domainName string) (string, error) {
	body := map[string]any{
		"name": domainName,
	}

	var actualResponse string
	err := p.requestAuthenticated(http.MethodPost, "/api/types/ProtectionDomain/instances/action/queryIdByKey", body, &actualResponse)
	if err != nil {
		powerFlexError, ok := err.(*powerFlexError)
		if ok {
			// API returns 500 if the volume does not exist.
			// To not confuse it with other 500 that might occur check the error code too.
			if powerFlexError.HTTPStatusCode() == http.StatusInternalServerError && powerFlexError.ErrorCode() == powerFlexCodeDomainNotFound {
				return "", api.StatusErrorf(http.StatusNotFound, "PowerFlex protection domain not found: %q", domainName)
			}
		}

		return "", fmt.Errorf("Failed to get protection domain ID for %q: %w", domainName, err)
	}

	return actualResponse, nil
}

// getProtectionDomain returns the protection domain behind domainID.
func (p *powerFlexClient) getProtectionDomain(domainID string) (*powerFlexProtectionDomain, error) {
	var actualResponse powerFlexProtectionDomain
	err := p.requestAuthenticated(http.MethodGet, "/api/instances/ProtectionDomain::"+domainID, nil, &actualResponse)
	if err != nil {
		return nil, fmt.Errorf("Failed to get protection domain: %q: %w", domainID, err)
	}

	return &actualResponse, nil
}

// getProtectionDomainStoragePools returns the protection domains storage pools.
func (p *powerFlexClient) getProtectionDomainStoragePools(domainID string) ([]powerFlexProtectionDomainStoragePool, error) {
	var actualResponse []powerFlexProtectionDomainStoragePool
	err := p.requestAuthenticated(http.MethodGet, "/api/instances/ProtectionDomain::"+domainID+"/relationships/StoragePool", nil, &actualResponse)
	if err != nil {
		return nil, fmt.Errorf("Failed to get protection domain storage pools: %q: %w", domainID, err)
	}

	return actualResponse, nil
}

// getProtectionDomainSDTRelations returns the protection domains SDT relations.
func (p *powerFlexClient) getProtectionDomainSDTRelations(domainID string) ([]powerFlexProtectionDomainSDTRelation, error) {
	var actualResponse []powerFlexProtectionDomainSDTRelation
	err := p.requestAuthenticated(http.MethodGet, "/api/instances/ProtectionDomain::"+domainID+"/relationships/Sdt", nil, &actualResponse)
	if err != nil {
		return nil, fmt.Errorf("Failed to get protection domain SDT relations: %q: %w", domainID, err)
	}

	return actualResponse, nil
}

// getVolumeID returns the volume ID for the given name.
func (p *powerFlexClient) getVolumeID(name string) (string, error) {
	body := map[string]any{
		"name": name,
	}

	var actualResponse string
	err := p.requestAuthenticated(http.MethodPost, "/api/types/Volume/instances/action/queryIdByKey", body, &actualResponse)
	if err != nil {
		powerFlexError, ok := err.(*powerFlexError)
		if ok {
			// API returns 500 if the volume does not exist.
			// To not confuse it with other 500 that might occur check the error code too.
			if powerFlexError.HTTPStatusCode() == http.StatusInternalServerError && powerFlexError.ErrorCode() == powerFlexCodeVolumeNotFound {
				return "", api.StatusErrorf(http.StatusNotFound, "PowerFlex volume not found: %q", name)
			}
		}

		return "", fmt.Errorf("Failed to get volume ID: %q: %w", name, err)
	}

	return actualResponse, nil
}

// getVolume returns the volume behind volumeID.
func (p *powerFlexClient) getVolume(volumeID string) (*powerFlexVolume, error) {
	var actualResponse powerFlexVolume
	err := p.requestAuthenticated(http.MethodGet, "/api/instances/Volume::"+volumeID, nil, &actualResponse)
	if err != nil {
		return nil, fmt.Errorf("Failed to get volume: %q: %w", volumeID, err)
	}

	return &actualResponse, nil
}

// createVolume creates a new volume.
// The size needs to be a number in multiples of 8.
// The unit used by PowerFlex is GiB.
// The returned string represents the ID of the volume.
func (p *powerFlexClient) createVolume(volumeName string, sizeGiB int64, volumeType powerFlexVolumeType, poolID string) (string, error) {
	stringSize := strconv.FormatInt(sizeGiB, 10)
	body := map[string]any{
		"name":           volumeName,
		"volumeSizeInGb": stringSize,
		"volumeType":     volumeType,
		"storagePoolId":  poolID,
	}

	var actualResponse struct {
		ID string `json:"id"`
	}

	err := p.requestAuthenticated(http.MethodPost, "/api/types/Volume/instances", body, &actualResponse)
	if err != nil {
		powerFlexError, ok := err.(*powerFlexError)
		if ok {
			// API returns 500 if the volume name is too long.
			// To not confuse it with other 500 that might occur check the error code too.
			if powerFlexError.HTTPStatusCode() == http.StatusInternalServerError && powerFlexError.ErrorCode() == powerFlexCodeNameTooLong {
				return "", api.StatusErrorf(http.StatusNotFound, "Volume name exceeds the allowed length of 31 characters: %q", volumeName)
			}
		}

		return "", fmt.Errorf("Failed to create volume: %q: %w", volumeName, err)
	}

	return actualResponse.ID, nil
}

// setVolumeSize sets the size of the volume behind volumeID to size.
// The size needs to be a number in multiples of 8.
// The unit used by PowerFlex is GiB.
func (p *powerFlexClient) setVolumeSize(volumeID string, sizeGiB int64) error {
	stringSize := strconv.FormatInt(sizeGiB, 10)
	body := map[string]any{
		"sizeInGB": stringSize,
	}

	err := p.requestAuthenticated(http.MethodPost, "/api/instances/Volume::"+volumeID+"/action/setVolumeSize", body, nil)
	if err != nil {
		return fmt.Errorf("Failed to set volume size: %q: %w", volumeID, err)
	}

	return nil
}

// overwriteVolume overwrites the volumes contents behind volumeID with the given snapshot.
func (p *powerFlexClient) overwriteVolume(volumeID string, snapshotID string) error {
	body := map[string]any{
		"srcVolumeId": snapshotID,
	}

	err := p.requestAuthenticated(http.MethodPost, "/api/instances/Volume::"+volumeID+"/action/overwriteVolumeContent", body, nil)
	if err != nil {
		return fmt.Errorf("Failed to overwrite volume: %q: %w", volumeID, err)
	}

	return nil
}

// renameVolume renames the volume behind volumeID to newName.
func (p *powerFlexClient) renameVolume(volumeID string, newName string) error {
	body := map[string]any{
		"newName": newName,
	}

	err := p.requestAuthenticated(http.MethodPost, "/api/instances/Volume::"+volumeID+"/action/setVolumeName", body, nil)
	if err != nil {
		return fmt.Errorf("Failed to rename volume: %q: %w", volumeID, err)
	}

	return nil
}

// createVolumeSnapshot creates a new volume snapshot under the given systemID for the volume behind volumeID.
// The accessMode can be either ReadWrite or ReadOnly.
// The returned string represents the ID of the snapshot.
func (p *powerFlexClient) createVolumeSnapshot(systemID string, volumeID string, snapshotName string, accessMode powerFlexSnapshotMode) (string, error) {
	body := map[string]any{
		"snapshotDefs": []map[string]string{
			{
				"volumeId":     volumeID,
				"snapshotName": snapshotName,
			},
		},
		"accessModeLimit": accessMode,
	}

	var actualResponse struct {
		VolumeIDs []string `json:"volumeIdList"`
	}

	err := p.requestAuthenticated(http.MethodPost, "/api/instances/System::"+systemID+"/action/snapshotVolumes", body, &actualResponse)
	if err != nil {
		powerFlexError, ok := err.(*powerFlexError)
		if ok {
			// API returns 500 if the snapshot name is too long.
			// To not confuse it with other 500 that might occur check the error code too.
			if powerFlexError.HTTPStatusCode() == http.StatusInternalServerError && powerFlexError.ErrorCode() == powerFlexCodeNameTooLong {
				return "", api.StatusErrorf(http.StatusNotFound, "Snapshot name exceeds the allowed length of 31 characters: %q", snapshotName)
			}
		}

		return "", fmt.Errorf("Failed to create volume snapshot: %q: %w", snapshotName, err)
	}

	if len(actualResponse.VolumeIDs) == 0 {
		return "", errors.New("Response does not contain a single snapshot ID")
	}

	return actualResponse.VolumeIDs[0], nil
}

// getVolumeSnapshots returns the snapshots of the volume behind volumeID.
func (p *powerFlexClient) getVolumeSnapshots(volumeID string) ([]powerFlexVolume, error) {
	volume, err := p.getVolume(volumeID)
	if err != nil {
		return nil, err
	}

	var actualResponse []powerFlexVolume
	err = p.requestAuthenticated(http.MethodGet, "/api/instances/VTree::"+volume.VTreeID+"/relationships/Volume", nil, &actualResponse)
	if err != nil {
		return nil, fmt.Errorf("Failed to get volume snapshots: %q: %w", volumeID, err)
	}

	var filteredVolumes []powerFlexVolume
	for _, volume := range actualResponse {
		if volume.AncestorVolumeID == volumeID {
			filteredVolumes = append(filteredVolumes, volume)
		}
	}

	return filteredVolumes, nil
}

// deleteVolume deletes the volume behind volumeID.
// The deleteMode can be one of ONLY_ME, INCLUDING_DESCENDANTS, DESCENDANTS_ONLY or WHOLE_VTREE.
// It describes the impact when deleting a volume from the underlying VTree. ONLY_ME deletes the
// provided volume only whereas WHOLE_VTREE also deletes the volumes parent(s) and child(s).
func (p *powerFlexClient) deleteVolume(volumeID string, deleteMode string) error {
	body := map[string]any{
		"removeMode": deleteMode,
	}

	err := p.requestAuthenticated(http.MethodPost, "/api/instances/Volume::"+volumeID+"/action/removeVolume", body, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete volume: %q: %w", volumeID, err)
	}

	return nil
}

// getHosts returns all hosts.
func (p *powerFlexClient) getHosts() ([]powerFlexSDC, error) {
	var actualResponse []powerFlexSDC
	err := p.requestAuthenticated(http.MethodGet, "/api/types/Sdc/instances", nil, &actualResponse)
	if err != nil {
		return nil, fmt.Errorf("Failed to get hosts: %w", err)
	}

	return actualResponse, nil
}

// getNVMeHosts returns all NVMe hosts.
func (p *powerFlexClient) getNVMeHosts() ([]powerFlexSDC, error) {
	allHosts, err := p.getHosts()
	if err != nil {
		return nil, err
	}

	var nvmeHosts []powerFlexSDC
	for _, host := range allHosts {
		if host.HostType == "NVMeHost" {
			nvmeHosts = append(nvmeHosts, host)
		}
	}

	return nvmeHosts, nil
}

// getSDCHosts returns all SDC hosts.
func (p *powerFlexClient) getSDCHosts() ([]powerFlexSDC, error) {
	allHosts, err := p.getHosts()
	if err != nil {
		return nil, err
	}

	var sdcHosts []powerFlexSDC
	for _, host := range allHosts {
		if host.HostType == "SdcHost" {
			sdcHosts = append(sdcHosts, host)
		}
	}

	return sdcHosts, nil
}

// getNVMeHostByNQN returns the NVMe host matching the nqn.
func (p *powerFlexClient) getNVMeHostByNQN(nqn string) (*powerFlexSDC, error) {
	allNVMeHosts, err := p.getNVMeHosts()
	if err != nil {
		return nil, err
	}

	for _, host := range allNVMeHosts {
		if host.NQN == nqn {
			return &host, nil
		}
	}

	return nil, api.StatusErrorf(http.StatusNotFound, "Host not found using nqn: %q", nqn)
}

// getSDCHostByGUID returns the SDC host matching the GUID.
func (p *powerFlexClient) getSDCHostByGUID(guid string) (*powerFlexSDC, error) {
	allSDCHosts, err := p.getSDCHosts()
	if err != nil {
		return nil, err
	}

	for _, host := range allSDCHosts {
		if host.SDCGuid == guid {
			return &host, nil
		}
	}

	return nil, api.StatusErrorf(http.StatusNotFound, "Host not found using GUID: %q", guid)
}

// createHost creates a new host.
func (p *powerFlexClient) createHost(hostName string, nqn string) (string, error) {
	body := map[string]any{
		"name": hostName,
		"nqn":  nqn,
	}

	var actualResponse struct {
		ID string `json:"id"`
	}

	err := p.requestAuthenticated(http.MethodPost, "/api/types/Host/instances", body, &actualResponse)
	if err != nil {
		return "", fmt.Errorf("Failed to create host: %w", err)
	}

	return actualResponse.ID, nil
}

// deleteHost deletes the host behind hostID.
func (p *powerFlexClient) deleteHost(hostID string) error {
	err := p.requestAuthenticated(http.MethodPost, "/api/instances/Sdc::"+hostID+"/action/removeSdc", nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete host: %w", err)
	}

	return nil
}

// createHostVolumeMapping creates the mapping between a host and volume.
func (p *powerFlexClient) createHostVolumeMapping(hostID string, volumeID string) error {
	body := map[string]any{
		"hostId": hostID,
		// This is required in live migration scenarios.
		"allowMultipleMappings": "true",
	}

	err := p.requestAuthenticated(http.MethodPost, "/api/instances/Volume::"+volumeID+"/action/addMappedHost", body, nil)
	if err != nil {
		return fmt.Errorf("Failed to create host volume mapping between %q and %q: %w", hostID, volumeID, err)
	}

	return nil
}

// deleteHostVolumeMapping deletes the mapping between a host and volume.
// Set hostIdentification to either its hostID in PowerFlex or SDC guid.
func (p *powerFlexClient) deleteHostVolumeMapping(hostID string, volumeID string) error {
	body := map[string]any{
		"hostId": hostID,
	}

	err := p.requestAuthenticated(http.MethodPost, "/api/instances/Volume::"+volumeID+"/action/removeMappedHost", body, nil)
	if err != nil {
		powerFlexError, ok := err.(*powerFlexError)
		if ok {
			// API returns 500 if the mapping doesn't anymore exist.
			// To not confuse it with other 500 that might occur check the error code too.
			if powerFlexError.HTTPStatusCode() == http.StatusInternalServerError && powerFlexError.ErrorCode() == powerFlexInvalidMapping {
				return api.StatusErrorf(http.StatusNotFound, "The mapping between %q and %q does not exist", hostID, volumeID)
			}
		}
		return fmt.Errorf("Failed to delete host volume mapping between %q and %q: %w", hostID, volumeID, err)
	}

	return nil
}

// getHostVolumeMappings returns the volume mappings for the host behind hostID.
func (p *powerFlexClient) getHostVolumeMappings(hostID string) ([]powerFlexVolume, error) {
	var actualResponse []powerFlexVolume
	err := p.requestAuthenticated(http.MethodGet, "/api/instances/Sdc::"+hostID+"/relationships/Volume", nil, &actualResponse)
	if err != nil {
		return nil, fmt.Errorf("Failed to get host volume mappings: %w", err)
	}

	return actualResponse, nil
}

// client returns the drivers PowerFlex client.
// A new client gets created if it not yet exists.
func (d *powerflex) client() *powerFlexClient {
	if d.httpClient == nil {
		d.httpClient = newPowerFlexClient(d)
	}

	return d.httpClient
}

// getHostGUID returns the SDC GUID.
// The GUID is unique for a single host.
// Cache the GUID as it never changes for a single host.
func (d *powerflex) getHostGUID() (string, error) {
	if d.sdcGUID == "" {
		guid, err := goscaleio.DrvCfgQueryGUID()
		if err != nil {
			return "", fmt.Errorf("Failed to query SDC GUID: %w", err)
		}

		d.sdcGUID = guid
	}

	return d.sdcGUID, nil
}

// getNVMeTargetQN discovers the targetQN used for the given addresses.
// The targetQN is unqiue per PowerFlex storage pool.
// Cache the targetQN as it doesn't change throughout the lifetime of the storage pool.
func (d *powerflex) getNVMeTargetQN(targetAddresses ...string) (string, error) {
	if d.nvmeTargetQN == "" {
		connector, err := d.connector()
		if err != nil {
			return "", err
		}

		// The discovery log from the first reachable target address is returned.
		discoveryLogRecords, err := connector.Discover(d.state.ShutdownCtx, targetAddresses...)
		if err != nil {
			return "", fmt.Errorf("Failed to discover SDT NQN: %w", err)
		}

		for _, recordAny := range discoveryLogRecords {
			record, ok := recordAny.(connectors.NVMeDiscoveryLogRecord)
			if !ok {
				return "", fmt.Errorf("Invalid discovery log record entry type %T is not connectors.NVMeDiscoveryLogRecord", recordAny)
			}

			if record.SubType != connectors.SubtypeNVMESubsys {
				continue
			}

			// The targetQN is listed together with every log record of type SubtypeNVMESubsys.
			d.nvmeTargetQN = record.SubNQN
			break
		}
	}

	return d.nvmeTargetQN, nil
}

// getNVMeTargetAddresses discovers all SDTs (targets) from PowerFlex for the respective storage pool.
// If the pool has one ore more SDTs defined using the powerflex.sdt config keys, use them instead.
// This allows overriding the list defined in PowerFlex.
func (d *powerflex) getNVMeTargetAddresses() ([]string, error) {
	targetAddresses := shared.SplitNTrimSpace(d.config["powerflex.sdt"], ",", -1, true)

	client := d.client()
	pool, err := d.resolvePool()
	if err != nil {
		return nil, err
	}

	if targetAddresses == nil {
		// Do not cache the fetched addresses to allow coping with administrative address changes performed in PowerFlex.
		relations, err := client.getProtectionDomainSDTRelations(pool.ProtectionDomainID)
		if err != nil {
			return nil, err
		}

		for _, relation := range relations {
			for _, ip := range relation.IPList {
				targetAddresses = append(targetAddresses, ip.IP)
			}
		}

		if len(targetAddresses) == 0 {
			return nil, fmt.Errorf("Failed to retrieve at least one SDT for the given storage pool: %q", pool.ID)
		}
	}

	return targetAddresses, nil
}

// getVolumeType returns the selected provisioning type of the volume.
// As a default it returns type thin.
func (d *powerflex) getVolumeType(vol Volume) powerFlexVolumeType {
	var volumeType string
	if vol.config["block.type"] != "" {
		volumeType = vol.config["block.type"]
	}

	if volumeType == "thick" {
		return powerFlexVolumeThick
	}

	return powerFlexVolumeThin
}

// createNVMeHost creates this NVMe host in PowerFlex.
func (d *powerflex) createNVMeHost() (string, revert.Hook, error) {
	var hostID string

	connector, err := d.connector()
	if err != nil {
		return "", nil, err
	}

	hostNQN, err := connector.QualifiedName()
	if err != nil {
		return "", nil, err
	}

	revert := revert.New()
	defer revert.Fail()

	client := d.client()
	host, err := client.getNVMeHostByNQN(hostNQN)
	if err != nil {
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return "", nil, err
		}

		hostname, err := ResolveServerName(d.state.ServerName)
		if err != nil {
			return "", nil, err
		}

		hostID, err = client.createHost(hostname, hostNQN)
		if err != nil {
			return "", nil, err
		}

		revert.Add(func() { _ = client.deleteHost(hostID) })
	}

	if hostID == "" {
		hostID = host.ID
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return hostID, cleanup, nil
}

// deleteNVMeHost deletes this NVMe host in PowerFlex.
func (d *powerflex) deleteNVMeHost() error {
	client := d.client()

	connector, err := d.connector()
	if err != nil {
		return err
	}

	hostNQN, err := connector.QualifiedName()
	if err != nil {
		return err
	}

	host, err := client.getNVMeHostByNQN(hostNQN)
	if err != nil {
		// Skip the deletion if the host doesn't exist anymore.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}

		return err
	}

	return client.deleteHost(host.ID)
}

// mapVolume maps the given volume onto this host.
func (d *powerflex) mapVolume(vol Volume) (revert.Hook, error) {
	var hostID string

	reverter := revert.New()
	defer reverter.Fail()

	connector, err := d.connector()
	if err != nil {
		return nil, err
	}

	switch d.config["powerflex.mode"] {
	case connectors.TypeNVME:
		unlock, err := remoteVolumeMapLock(connector.Type(), d.Info().Name)
		if err != nil {
			return nil, err
		}

		defer unlock()

		var cleanup revert.Hook
		hostID, cleanup, err = d.createNVMeHost()
		if err != nil {
			return nil, err
		}

		reverter.Add(cleanup)
	case connectors.TypeSDC:
		hostGUID, err := d.getHostGUID()
		if err != nil {
			return nil, err
		}

		client := d.client()
		host, err := client.getSDCHostByGUID(hostGUID)
		if err != nil {
			return nil, err
		}

		hostID = host.ID
	}

	volumeName, err := d.getVolumeName(vol)
	if err != nil {
		return nil, err
	}

	client := d.client()
	volumeID, err := client.getVolumeID(volumeName)
	if err != nil {
		return nil, err
	}

	volume, err := client.getVolume(volumeID)
	if err != nil {
		return nil, err
	}

	mapped := false
	for _, mapping := range volume.MappedSDCInfo {
		if mapping.SDCID == hostID {
			mapped = true
		}
	}

	if !mapped {
		err = client.createHostVolumeMapping(hostID, volumeID)
		if err != nil {
			return nil, err
		}

		reverter.Add(func() { _ = client.deleteHostVolumeMapping(hostID, volumeID) })
	}

	var cleanup revert.Hook
	if d.config["powerflex.mode"] == connectors.TypeNVME {
		// Discover all SDTs from PowerFlex for the respective storage pool.
		targetAddresses, err := d.getNVMeTargetAddresses()
		if err != nil {
			return nil, err
		}

		// Discover the SDT's targetQN from any of the addresses.
		targetQN, err := d.getNVMeTargetQN(targetAddresses...)
		if err != nil {
			return nil, err
		}

		// Connect to the storage subsystem.
		// In case of NVMe/TCP, we have to connect after the first mapping was established,
		// as PowerFlex does not offer any discovery log entries until a volume gets mapped
		// to the host.
		cleanup, err = connector.Connect(d.state.ShutdownCtx, targetQN, targetAddresses...)
		if err != nil {
			return nil, err
		}
	}

	// Reverting mapping or connection outside mapVolume function
	// could conflict with other ongoing operations as lock will
	// already be released. Therefore, use unmapVolume instead
	// because it ensures the lock is acquired and accounts for
	// an existing session before unmapping a volume.
	outerReverter := revert.New()

	if !mapped {
		outerReverter.Add(func() { _ = d.unmapVolume(vol) })
	}

	// Add the cleanup hooks of the connection attempt to the outer reverter.
	// This ensures that ongoing connection attempts that haven't yet finished are cancelled
	// before potentially running unmap volume.
	// As the revert hooks are called in reverse order add the connection cleanup after unmap.
	if d.config["powerflex.mode"] == connectors.TypeNVME {
		outerReverter.Add(cleanup)
	}

	reverter.Success()
	return outerReverter.Fail, nil
}

// getMappedDevPath returns the local device path for the given volume.
// Indicate with mapVolume if the volume should get mapped to the system if it isn't present.
func (d *powerflex) getMappedDevPath(vol Volume, mapVolume bool) (string, revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	if mapVolume {
		cleanup, err := d.mapVolume(vol)
		if err != nil {
			return "", nil, err
		}

		revert.Add(cleanup)
	}

	volumeName, err := d.getVolumeName(vol)
	if err != nil {
		return "", nil, err
	}

	powerFlexVolumeID, err := d.client().getVolumeID(volumeName)
	if err != nil {
		return "", nil, err
	}

	var prefix string
	switch d.config["powerflex.mode"] {
	case connectors.TypeNVME:
		prefix = "nvme-eui."
	case connectors.TypeSDC:
		prefix = "emc-vol-"
	}

	devicePathFilter := func(path string) bool {
		return strings.Contains(path, powerFlexVolumeID)
	}

	var devicePath string
	if mapVolume {
		// Wait for the device path to appear as the volume has been just mapped to the host.
		devicePath, err = block.WaitDiskDevicePath(d.state.ShutdownCtx, prefix, devicePathFilter)
	} else {
		// Get the the device path without waiting.
		devicePath, err = block.GetDiskDevicePath(prefix, devicePathFilter)
	}

	if err != nil {
		return "", nil, fmt.Errorf("Failed to locate device for volume %q: %w", vol.name, err)
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return devicePath, cleanup, nil
}

// unmapVolume unmaps the given volume from this host.
func (d *powerflex) unmapVolume(vol Volume) error {
	connector, err := d.connector()
	if err != nil {
		return err
	}

	volumeName, err := d.getVolumeName(vol)
	if err != nil {
		return err
	}

	client := d.client()
	volume, err := client.getVolumeID(volumeName)
	if err != nil {
		return err
	}

	var host *powerFlexSDC
	switch d.config["powerflex.mode"] {
	case connectors.TypeNVME:
		hostNQN, err := connector.QualifiedName()
		if err != nil {
			return err
		}

		host, err = client.getNVMeHostByNQN(hostNQN)
		if err != nil {
			return err
		}

		unlock, err := remoteVolumeMapLock(connector.Type(), d.Info().Name)
		if err != nil {
			return err
		}

		defer unlock()
	case connectors.TypeSDC:
		hostGUID, err := d.getHostGUID()
		if err != nil {
			return err
		}

		host, err = client.getSDCHostByGUID(hostGUID)
		if err != nil {
			return err
		}
	}

	err = client.deleteHostVolumeMapping(host.ID, volume)
	if err != nil {
		return err
	}

	// Wait until the volume has disappeared.
	ctx, cancel := context.WithTimeout(d.state.ShutdownCtx, 10*time.Second)
	defer cancel()

	volumePath, _, _ := d.getMappedDevPath(vol, false)
	if volumePath != "" && !block.WaitDiskDeviceGone(ctx, volumePath) {
		return fmt.Errorf("Timeout whilst waiting for PowerFlex volume to disappear: %q", vol.name)
	}

	// In case of SDC the driver doesn't manage the underlying connection to PowerFlex.
	// Therefore if this was the last volume being unmapped from this system
	// LXD will not try to cleanup the connection.
	if d.config["powerflex.mode"] == connectors.TypeNVME {
		mappings, err := client.getHostVolumeMappings(host.ID)
		if err != nil {
			return err
		}

		if len(mappings) == 0 {
			targetAddresses, err := d.getNVMeTargetAddresses()
			if err != nil {
				return err
			}

			targetQN, err := d.getNVMeTargetQN(targetAddresses...)
			if err != nil {
				return err
			}

			// Disconnect from the NVMe subsystem.
			// Do this first before removing the host from PowerFlex.
			err = connector.Disconnect(targetQN)
			if err != nil {
				return err
			}

			// Delete the host from PowerFlex if the last volume mapping got removed.
			// This requires the host to be already disconnected from the NVMe subsystem.
			err = d.deleteNVMeHost()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// resolvePool looks up the selected storage pool.
// If only the pool is provided, it's expected to be the ID of the pool.
// In case both pool and domain are set, the pool will get looked up
// by name within the domain.
func (d *powerflex) resolvePool() (*powerFlexStoragePool, error) {
	client := d.client()
	if d.config["powerflex.domain"] != "" {
		domainID, err := client.getProtectionDomainID(d.config["powerflex.domain"])
		if err != nil {
			return nil, err
		}

		domainPools, err := client.getProtectionDomainStoragePools(domainID)
		if err != nil {
			return nil, err
		}

		for _, v := range domainPools {
			// Allow both ID or name to be set for `powerflex.pool`.
			// This ensures compatibility if the domain is set since powerflex.pool
			// can be used to specify the pools ID directly.
			if v.Name == d.config["powerflex.pool"] || v.ID == d.config["powerflex.pool"] {
				pool, err := client.getStoragePool(v.ID)
				if err != nil {
					return nil, err
				}

				return pool, nil
			}
		}

		return nil, fmt.Errorf("Cannot find storage pool %q in protection domain %q", d.config["powerflex.pool"], d.config["powerflex.domain"])
	}

	return client.getStoragePool(d.config["powerflex.pool"])
}

// getPowerFlexVolumeName returns the fully qualified name derived from the volume.
func (d *powerflex) getVolumeName(vol Volume) (string, error) {
	volUUID, err := uuid.Parse(vol.config["volatile.uuid"])
	if err != nil {
		return "", fmt.Errorf("Failed parsing %q from volume %q: %w", "volatile.uuid", vol.name, err)
	}

	binUUID, err := volUUID.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("Failed marshalling the %q of volume %q to binary format: %w", "volatile.uuid", vol.name, err)
	}

	// The volume's name in base64 encoded format.
	volName := base64.StdEncoding.EncodeToString(binUUID)

	var suffix string
	switch vol.contentType {
	case ContentTypeBlock:
		suffix = powerFlexBlockVolSuffix
	case ContentTypeISO:
		suffix = powerFlexISOVolSuffix
	}

	// Use storage volume prefix from powerFlexVolTypePrefixes depending on type.
	// If the volume's type is unknown, don't put any prefix to accommodate the volume name size constraint.
	volumeTypePrefix, ok := powerFlexVolTypePrefixes[vol.volType]
	if ok {
		volumeTypePrefix = volumeTypePrefix + "_"
	}

	volName = volumeTypePrefix + volName + suffix

	// If volume is snapshot, prepend snapshot prefix to its name.
	// This allows differentiating between snapshots which actually belong to its parent volume
	// and snapshots which were being created as part of copying a volume using powerflex.snapshot_copy.
	// The latter are snapshots of the original volume stand on their own and don't use the snapshot prefix.
	if vol.IsSnapshot() {
		volName = powerFlexSnapshotPrefix + volName
	}

	return volName, nil
}
