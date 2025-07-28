package drivers

// Most code in this file was written by Sergiu Strat <nevrax@gmail.com>

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// hpeServiceNameMapping maps HPE Storage mode in LXD to the corresponding HPE Storage
// service name.
var hpeServiceNameMapping = map[string]string{
	connectors.TypeISCSI: "iscsi",
	connectors.TypeNVME:  "nvme-tcp",
}

type hpeVolume struct {
	ID                    int         `json:"id"`
	Name                  string      `json:"name"`
	ShortName             string      `json:"shortName"`
	DeduplicationState    int         `json:"deduplicationState"`
	CompressionState      int         `json:"compressionState"`
	ProvisioningType      int         `json:"provisioningType"`
	CopyType              int         `json:"copyType"`
	BaseID                int         `json:"baseId"`
	ReadOnly              bool        `json:"readOnly"`
	State                 int         `json:"state"`
	FailedStates          []string    `json:"failedStates"`
	DegradedStates        []string    `json:"degradedStates"`
	AdditionalStates      []string    `json:"additionalStates"`
	TotalReservedMiB      int         `json:"totalReservedMiB"`
	TotalUsedMiB          int         `json:"totalUsedMiB"`
	SizeMiB               int         `json:"sizeMiB"`
	HostWriteMiB          int         `json:"hostWriteMiB"`
	WWN                   string      `json:"wwn"`
	NGUID                 string      `json:"nguid"`
	CreationTimeSec       int         `json:"creationTimeSec"`
	CreationTime8601      string      `json:"creationTime8601"`
	UsrSpcAllocWarningPct int         `json:"usrSpcAllocWarningPct"`
	UsrSpcAllocLimitPct   int         `json:"usrSpcAllocLimitPct"`
	Policies              hpePolicies `json:"policies"`
	UserCPG               string      `json:"userCPG"`
	UUID                  string      `json:"uuid"`
	UDID                  int         `json:"udid"`
	CapacityEfficiency    hpeCapacity `json:"capacityEfficiency"`
	RcopyStatus           int         `json:"rcopyStatus"`
	Links                 []hpeLink   `json:"links"`
}

type hpePolicies struct {
	StaleSS    bool `json:"staleSS"`
	OneHost    bool `json:"oneHost"`
	ZeroDetect bool `json:"zeroDetect"`
	System     bool `json:"system"`
	Caching    bool `json:"caching"`
}

type hpeCapacity struct {
	Compaction float64 `json:"compaction"`
}

type hpeLink struct {
	Href string `json:"href"`
	Rel  string `json:"rel"`
}

type hpeVLUN struct {
	LUN      int    `json:"lun"`
	Hostname string `json:"hostname"`
	Serial   string `json:"serial"`
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

type hpeRespMembers[T any] struct {
	Total   int `json:"total"`
	Members []T `json:"members"`
}

// hpePort represents a port in HPE Storage.
type hpePort struct {
	Protocol  int    `json:"protocol"`
	NodeWWN   string `json:"nodeWWN"`
	LinkState int    `json:"linkState"`
}

// hpeError represents an error response from the HPE Storage API.
type hpeError struct {
	Code           int    `json:"code"`
	Desc           string `json:"desc"`
	HttpStatusCode int
}

// Error implements the error interface for hpeError.
func (p *hpeError) Error() string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("HTTP Error Code: %d. HPE Error Code: %d. HPE Description: %s", p.HttpStatusCode, p.Code, p.Desc)
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

// request issues a HTTP request against HPE Storage WSAPI
func (p *hpeAlletraClient) request(method string, url url.URL, reqBody map[string]any, reqHeaders map[string]string, respBody any, respHeaders map[string]string) error {
	logger.Debug("HPE request()")
	// Extract scheme and host from the gateway URL.
	scheme, host, found := strings.Cut(p.driver.config["hpe_alletra.wsapi.url"], "://")
	if !found {
		return fmt.Errorf("Invalid HPE Storage WSAPI URL: %q", p.driver.config["hpe_alletra.wsapi.url"])
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
				InsecureSkipVerify: shared.IsFalse(p.driver.config["hpe_alletra.wsapi.verify"]),
			},
		},
	}

	logger.Debugf("Request URL: %s", url.String())
	logger.Debugf("Request Method: %s", req.Method)

	parsedReqHeaders := ""
	for name, values := range req.Header {
		parsedReqHeaders += fmt.Sprintf("%s: %s\n", name, strings.Join(values, ", "))
	}
	logger.Debugf("Request Headers:\n%s", parsedReqHeaders)

	// logger.Debugf("Request Body:\n%s", reqBody)

	// reqBodyDisplay := make([]string, 0, len(reqBody))
	// for k, v := range reqBody {
	// 	reqBodyDisplay = append(reqBodyDisplay, fmt.Sprintf("%s=%v", k, v))
	// }
	// logger.Debugf("Request Body (KV):\n%s", strings.Join(reqBodyDisplay, "\n"))

	reqBodyJSON, err := json.MarshalIndent(reqBody, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}
	logger.Debugf("Request Body (JSON):\n%s", string(reqBodyJSON))

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Failed to send request: %w", err)
	}

	logger.Debugf("Response HTTP Status Code: %d", resp.StatusCode)

	defer resp.Body.Close()

	var responseBodyBuffer bytes.Buffer
	teeReader := io.TeeReader(resp.Body, &responseBodyBuffer)
	bodyBytes, err := io.ReadAll(teeReader)
	if err != nil {
		return fmt.Errorf("Failed to read response body for TeeReader: %w", err)
	}

	allowedCT := "application/json"
	if resp.Header.Get("Content-Type") != allowedCT &&
		len(bodyBytes) > 0 {
		return fmt.Errorf("Response Header Content-type: %q. Only %q responses allowed for non-empty Response Body", resp.Header.Get("Content-Type"), allowedCT)
	}

	parsedRespHeaders := ""
	undesiredRespHeaders := []string{
		"X-Frame-Options",
		"Set-Cookie",
		"Content-Security-Policy",
		"Strict-Transport-Security",
		"Cache-Control",
		"Vary",
		"X-Content-Type-Options",
		"Content-Language",
		"X-Xss-Protection",
		"Pragma",
		"Accept-Ranges",
		"Last-Modified",
	}

	for name, values := range resp.Header {
		skip := slices.Contains(undesiredRespHeaders, name)
		if skip {
			continue
		}
		parsedRespHeaders += fmt.Sprintf("%s: %s\n", name, strings.Join(values, ", "))
	}
	logger.Debugf("Response Headers (filtered):\n%s", parsedRespHeaders)

	var prettyBody any
	if json.Unmarshal(bodyBytes, &prettyBody) == nil {
		b, _ := json.MarshalIndent(prettyBody, "", "  ")
		lines := strings.Split(string(b), "\n")
		logger.Debugf("Response Body size (JSON): %d", len(b))
		var outputBuffer strings.Builder
		if len(lines) > 100000 {
			for _, line := range lines[:5] {
				outputBuffer.WriteString(fmt.Sprintf("%s\n", line))
			}
			outputBuffer.WriteString("....\n....\n....\n")
			for _, line := range lines[len(lines)-5:] {
				outputBuffer.WriteString(fmt.Sprintf("%s\n", line))
			}
			logger.Debugf("Response Body (JSON) - Output truncated:\n%s", outputBuffer.String())
		} else {
			logger.Debugf("Response Body (JSON):\n%s", string(b))
		}
	} else {
		logger.Debugf("Response Body size (RAW): %d", len(bodyBytes))
		logger.Debugf("Response Body (RAW):\n%s", string(bodyBytes))
	}

	// The unauthorized error is reported when an invalid (or expired) access token is provided.
	// Wrap unauthorized requests into an API status error to allow easier checking for expired
	// token in the requestAuthenticated function.
	// if resp.StatusCode == http.StatusUnauthorized {
	// 	return api.StatusErrorf(http.StatusUnauthorized, "xxx 1 Unauthorized request")
	// } else if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
	// 	return api.StatusErrorf(http.StatusForbidden, "xxx 2 Unauthorized status code: %d", resp.StatusCode)
	// }

	// Overwrite the response data type if an error is detected.
	// if resp.StatusCode != http.StatusOK &&
	// 	resp.StatusCode != http.StatusCreated &&
	// 	resp.StatusCode != http.StatusAccepted {
	// 	respBody = &hpeError{}
	// }

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
		hpeErr.HttpStatusCode = resp.StatusCode
		return hpeErr
	}

	return nil
}

// requestAuthenticated issues an authenticated HTTP request against the HPE Storage gateway.
// In case the Session Key is expired, the function will try to obtain a new one.
func (p *hpeAlletraClient) requestAuthenticated(method string, url url.URL, reqBody map[string]any, respBody any) error {
	logger.Debug("HPE requestAuthenticated()")

	// If request fails with an unauthorized error, the request will be retried after
	// requesting a new Session Key.
	// retries := 0

	// for {
	// 	if retries >= 1 {
	// 		return fmt.Errorf("HPE maximium retires reached: %d. URL: %q. Method: %s", retries, url.String(), method)
	// 	}

	// retries++
	// logger.Debugf("HPE atempt: %d", retries)

	// Ensure we are logged into the HPE Storage.
	err := p.login()
	if err != nil {
		return err
	}

	// Add Session Key to Headers.
	reqHeaders := map[string]string{
		"X-HP3PAR-WSAPI-SessionKey": p.sessionKey,
	}
	logger.Debugf("HPE add Session Key to Headers: %s", p.sessionKey)

	// Initiate request.
	err = p.request(method, url, reqBody, reqHeaders, &respBody, nil)

	if err != nil {
		// if api.StatusErrorCheck(err, http.StatusForbidden) {
		// logger.Debugf("HPE resetting Session Key: %s", err)
		// 	p.sessionKey = ""
		// } else {
		// 	logger.Debugf("HPE authorized request failed for %s %q with error: %s", strings.ToUpper(method), url.String(), err)
		// }
		// // continue
		// }

		// return nil

		// errMsg := err.Error()
		// errMsgSize := len(errMsg)

		// if errMsgSize > 0 {
		hpeErr, assert := err.(*hpeError)

		if assert {
			logger.Debugf("HPE debug hpeErr.statusCode: %d", hpeErr.HttpStatusCode)
			logger.Debugf("HPE debug hpeErr.Code: %d", hpeErr.Code)
			logger.Debugf("HPE debug hpeErr.Desc: %s", hpeErr.Desc)
		} else {
			logger.Debugf("HPE debug cannot assert hpeError: %s", hpeErr)
		}
		// }

		logger.Debugf("HPE authorized request failed for %s %q with error: %s", strings.ToUpper(method), url.String(), err)
		return err
	}

	return nil
}

// getAPIVersion returns the list of API versions that are supported by the HPE Storage.
func (p *hpeAlletraClient) getAPIVersions() ([]string, error) {
	var resp struct {
		APIVersions []string `json:"version"`
	}

	url := api.NewURL().Path("api", "v1", "wsapiconfiguration")
	err := p.request(http.MethodGet, url.URL, nil, nil, &resp, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve available API versions from HPE Storage: %w", err)
	}

	if len(resp.APIVersions) == 0 {
		return nil, fmt.Errorf("HPE Storage does not support any API versions")
	}

	return resp.APIVersions, nil
}

// login initiates request() using WSAPI username and password.
// If successful then Session Key is retrieved and stored within hpeClient client.
// Once stored the Session Key is reused for further requests.
func (p *hpeAlletraClient) login() error {
	logger.Debug("HPE login()")

	if p.sessionKey != "" {
		logger.Debugf("HPE reusing stored Session Key: %s", p.sessionKey)
		return nil
	}

	var respBody struct {
		Key  string `json:"key"`
		Desc string `json:"desc"`
	}

	body := map[string]any{
		"user":        p.driver.config["hpe_alletra.wsapi.user.name"],
		"password":    p.driver.config["hpe_alletra.wsapi.user.password"],
		"sessionType": 1,
	}

	url := api.NewURL().Path("api", "v1", "credentials")
	respHeaders := make(map[string]string)

	err := p.request(http.MethodPost, url.URL, body, nil, &respBody, respHeaders)
	if err != nil {
		return fmt.Errorf("HPE failed to send login request: %w", err)
	}

	if respBody.Key != "" {
		p.sessionKey = respBody.Key
		logger.Debugf("HPE saved Session Key: %s", p.sessionKey)
		return nil
	}

	return errors.New("HPE received an empty Session Key")
}

func (p *hpeAlletraClient) getTarget() (targetNQN string, targetAddrs []string, err error) {
	logger.Debugf("HPE getTarget()")

	connector, err := p.driver.connector()
	if err != nil {
		return "", nil, err
	}
	mode := connector.Type()

	// Get HPE Storage service name based on the configured mode.
	service, ok := hpeServiceNameMapping[mode]
	if !ok {
		return "", nil, fmt.Errorf("Failed to determine service name for HPE Storage mode %q", mode)
	}
	logger.Debugf("HPE mode: %s", service)

	var portData hpeRespMembers[hpePort]
	var portMembers []string

	apiPorts := api.NewURL().Path("api", "v1", "ports")

	err = p.requestAuthenticated(http.MethodGet, apiPorts.URL, nil, &portData)
	if err != nil {
		return "", nil, fmt.Errorf("failed to retrieve port list: %w", err)
	}

	if len(portData.Members) == 0 {
		return "", nil, fmt.Errorf("HPE no port data found")
	}

	for _, member := range portData.Members {
		if member.LinkState != 4 {
			continue // skip down or unlinked ports
		}
		logger.Debugf("HPE member: %v", member)
		switch mode {
		case connectors.TypeISCSI:
			if member.Protocol != 2 {
				continue
			}
			if member.NodeWWN != "" {
				portMembers = append(portMembers, member.NodeWWN)
			}

		case connectors.TypeNVME:
			if member.Protocol != 6 {
				continue
			}
			if member.NodeWWN != "" {
				portMembers = append(portMembers, member.NodeWWN)
			}
		}
	}
	logger.Debugf("HPE retrieved WSAPI target addresses %s", portMembers)

	// First check if target addresses are configured, otherwise, use the discovered ones.
	var configAddrs = shared.SplitNTrimSpace(p.driver.config["hpe_alletra.target.addresses"], ",", -1, true)
	if len(configAddrs) > 0 {
		// targetAddrs = make([]string, 0, len(interfaces))
		// for _, iface := range interfaces {
		// 	targetAddrs = append(targetAddrs, iface.Ethernet.Address)
		// }
		targetAddrs = configAddrs
		logger.Debugf("HPE using already configured driver hpe_alletra.target.addresses: %s", targetAddrs)
	} else {
		targetAddrs = portMembers
		logger.Debugf("HPE using WSAPI target addresses: %s", targetAddrs)
	}

	var hpeRespSystem struct {
		Name string `json:"name"`
	}

	apiSystem := api.NewURL().Path("api", "v1", "system")

	err = p.requestAuthenticated(http.MethodGet, apiSystem.URL, nil, &hpeRespSystem)
	if err != nil {
		return "", nil, fmt.Errorf("HPE failed to retrieve WSAPI System data: %w", err)
	}

	if p.driver.config["hpe_alletra.target.nqn"] != "" {
		targetNQN = p.driver.config["hpe_alletra.target.nqn"]
		logger.Debugf("HPE using already configured hpe_alletra.target.nqn: %s", targetNQN)
	} else {
		targetNQN = hpeRespSystem.Name
		logger.Debugf("HPE using WSAPI target NQN: %s", targetNQN)
	}

	// // Retrieve the list of HPE Storage network interfaces.
	// interfaces, err := p.getNetworkInterfaces(service)
	// if err != nil {
	// 	return "", nil, err
	// }

	// if len(interfaces) == 0 {
	// 	return "", nil, api.StatusErrorf(http.StatusNotFound, "Enabled network interface with %q service not found", service)
	// }

	if targetNQN == "" || len(targetAddrs) == 0 {
		return "", nil, fmt.Errorf("HPE no usable target found for mode %q", mode)
	}

	// targetAddrs = nil
	// targetAddrs = append(targetAddrs, "127.0.0.1")
	// logger.Debugf("HPE found Target Addressess: %s", targetAddrs)

	// this small chunk is copied from powerflex
	// Discover the SDT's targetQN from any of the addresses.
	targetQN, err := p.driver.getNVMeTargetQN(targetAddrs...) // TODO: take this!
	if err != nil {
		return "", nil, err
	}

	logger.Debugf("getTarget NQN: %s", targetQN)

	return targetQN, targetAddrs, nil // FIXME
}

func (p *hpeAlletraClient) createVolumeSet() error {
	logger.Warn("createVolumeSet()")
	defer logger.Warn("return createVolumeSet()")

	req := map[string]any{
		"name":    p.driver.name,
		"comment": "Created and managed by LXD",
	}

	url := api.NewURL().Path("api", "v1", "volumesets")
	err := p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("Failed to create storage pool %q: %w", p.driver.name, err)
	}

	return nil
}

func (p *hpeAlletraClient) deleteVolumeSet() error {
	logger.Warn("deleteVolumeSet()")
	defer logger.Warn("return deleteVolumeSet()")

	url := api.NewURL().Path("api", "v1", "volumesets", p.driver.name)
	err := p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete storage pool %q: %w", p.driver.name, err)
	}

	return nil
}

func (p *hpeAlletraClient) modifyVolumeSet(action int, volName string) error {
	logger.Warn("modifyVolumeSet()")
	defer logger.Warn("return modifyVolumeSet()")

	req := map[string]any{
		"action":     action,
		"setmembers": []string{volName},
	}

	url := api.NewURL().Path("api", "v1", "volumesets", p.driver.name)
	err := p.requestAuthenticated(http.MethodPut, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("Failed to modify storage pool %q: %w", p.driver.name, err)
	}

	return nil
}

// createVolume creates a new volume in the given storage pool. The volume is created with
// supplied size in bytes. Upon successful creation, volume's ID is returned. TODO
func (p *hpeAlletraClient) _createVolume(poolName string, volName string, sizeBytes int64) error {
	logger.Debugf("HPE createVolume()")

	req := map[string]any{
		"name":    volName,
		"cpg":     p.driver.config["hpe_alletra.wsapi.cpg"],
		"sizeMiB": sizeBytes / 1024 / 1024,
		"tpvv":    true, // thin provisioned? fails if I set to false
		//"reduce":  false,
	}

	// Prevent default protection groups to be applied on the new volume, which can
	// prevent us from eradicating the volume once deleted.
	url := api.NewURL().Path("api", "v1", "volumes")
	err := p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("Failed to create volume %q in storage pool %q: %w", volName, p.driver.name, err)
	}

	return nil
}

func (p *hpeAlletraClient) createVolume(poolName string, volName string, sizeBytes int64) error {
	err := p._createVolume(poolName, volName, sizeBytes)
	if err != nil {
		return err
	}

	err = p.modifyVolumeSet(1, volName) // 1 = memAdd
	return err
}

// getVolume returns the volume behind volumeID.
func (p *hpeAlletraClient) getVolume(poolName string, volName string) (*hpeVolume, error) {
	logger.Debugf("HPE getVolume()")

	var resp hpeVolume

	url := api.NewURL().Path("api", "v1", "volumes", volName)
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		if hpeErr, ok := err.(*hpeError); ok {
			switch hpeErr.Code {
			case 23:
				logger.Debugf("HPE volume does not exist: %s", volName)
				return nil, nil
			default:
				return nil, fmt.Errorf("HPE debug. hpeErr.Code: %d. hpeErr.Desc: %s", hpeErr.Code, hpeErr.Desc)
			}
		}

		logger.Debugf("HPE Failed to get hpeVolume: %s", volName)
		return nil, fmt.Errorf("Failed to get hpeVolume %q: %w", volName, err)
	}

	if resp.Name == "" {
		logger.Debugf("HPE volume response is empty for volume name: %s", volName)
		return nil, fmt.Errorf("HPE volume %q exists but returned an empty response", volName)
	}

	return &resp, nil
}

// deleteVolume deletes an exisiting volume in the given storage pool.
func (p *hpeAlletraClient) _deleteVolume(poolName string, volName string) error {
	logger.Debugf("HPE deleteVolume()")
	url := api.NewURL().Path("api", "v1", "volumes", volName)

	err := p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
	if err != nil {
		if hpeErr, ok := err.(*hpeError); ok {
			switch hpeErr.Code {
			case 23:
				logger.Debugf("HPE debug volume does not exist: %s", volName)
				return nil
			default:
				return fmt.Errorf("HPE debug. hpeErr.Code: %d. hpeErr.Desc: %s", hpeErr.Code, hpeErr.Desc)
			}
		}

		logger.Debugf("HPE debug failed to delete hpeVolume %s from CPG %s", volName, poolName)
		return fmt.Errorf("Failed to delete hpeVolume %q in CPG %q: %w", volName, poolName, err)
	}

	return nil
}

func (p *hpeAlletraClient) deleteVolume(poolName string, volName string) error {
	err := p.modifyVolumeSet(2, volName) // 2 = memRemove
	if err != nil {
		return err
	}

	err = p._deleteVolume(poolName, volName)
	return err
}

// getVolumeSnapshots retrieves all existing snapshot for the given storage volume.
func (p *hpeAlletraClient) getVolumeSnapshots(poolName string, volName string) ([]hpeVolume, error) {
	logger.Debugf("HPE getVolumeSnapshots()")

	var resp hpeRespMembers[hpeVolume]

	url := api.NewURL().Path("api", "v1", "volumes").WithQuery("query", fmt.Sprintf("\"copyOf==%s\"", volName))

	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve snapshots for volume %q in storage pool %q: %w", volName, poolName, err)
	}

	return resp.Members, nil
}

// getVolumeSnapshot retrieves an existing snapshot for the given storage volume.
func (p *hpeAlletraClient) getVolumeSnapshot(poolName string, volName string, snapshotName string) (*hpeVolume, error) {
	logger.Debugf("HPE getVolumeSnapshot()")

	var resp hpeRespMembers[hpeVolume]

	url := api.NewURL().Path("api", "v1", "volumes").WithQuery("query", fmt.Sprintf("\"copyOf==%s\"", volName))
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		if isHpeErrorNotFound(err) {
			return nil, api.StatusErrorf(http.StatusNotFound, "Snapshot %q not found", snapshotName)
		}

		return nil, fmt.Errorf("Failed to retrieve snapshot %q for volume %q in storage pool %q: %w", snapshotName, volName, poolName, err)
	}

	if len(resp.Members) == 0 {
		return nil, api.StatusErrorf(http.StatusNotFound, "Snapshot %q not found", snapshotName)
	}

	return &resp.Members[0], nil
}

// createVolumeSnapshot creates a new snapshot for the given storage volume.
func (p *hpeAlletraClient) createVolumeSnapshot(poolName string, volName string, snapshotName string) error {
	logger.Debugf("HPE createVolumeSnapshot()")

	req := map[string]any{
		"action": "createSnapshot",
		"parameters": map[string]any{
			"name": snapshotName,
		},
	}

	url := api.NewURL().Path("api", "v1", "volumes", volName)
	err := p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {
		return fmt.Errorf("HPE failed to create snapshot %q for volume %q in storage pool %q: %w", snapshotName, volName, poolName, err)
	}

	return nil
}

// deleteVolumeSnapshot deletes an existing snapshot for the given storage volume.
func (p *hpeAlletraClient) deleteVolumeSnapshot(poolName string, volName string, snapshotName string) error {
	logger.Debugf("HPE getVolumeSnapshot()")

	err := p.deleteVolume(poolName, snapshotName)
	if err != nil {
		return fmt.Errorf("Failed to delete snapshot %q for volume %q in storage pool %q: %w", snapshotName, volName, poolName, err)
	}

	return nil
}

// connectHostToVolume creates a connection between a host and volume. It returns true if the connection
// was created, and false if it already existed.
func (p *hpeAlletraClient) connectHostToVolume(poolName string, volName string, hostName string) (bool, error) {
	logger.Debugf("HPE connectHostToVolume()")

	url := api.NewURL().Path("api", "v1", "vluns")

	req := make(map[string]any)

	req["hostname"] = hostName
	req["volumeName"] = volName
	req["lun"] = 0
	req["autoLun"] = true

	// var respBody any
	err := p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {
		if hpeErr, ok := err.(*hpeError); ok {
			switch hpeErr.Code {
			case 29:
				logger.Debugf("HPE using existing settings. Volume %s already attached to %s", volName, hostName)
				return false, nil
			default:
				return false, fmt.Errorf("HPE debug. hpeErr.Code: %d. hpeErr.Desc: %s", hpeErr.Code, hpeErr.Desc)
			}
		}

		// // Handle connection already exists case specifically
		// if isHpeErrorOf(err, http.StatusBadRequest, "Connection already exists.") {
		// 	return false, nil
		// }

		return false, fmt.Errorf("Failed to connect volume %q with host %q: %w", volName, hostName, err)
	}

	return true, nil
}

// getVLUN returns VLUN related data of given volumeName
func (p *hpeAlletraClient) getVLUN(volumeName string) (*hpeVLUN, error) {
	logger.Debugf("HPE getVLUN()")
	var resp hpeRespMembers[hpeVLUN]

	url := api.NewURL().Path("api", "v1", "vluns").WithQuery("query", "\"volumeName"+"=="+volumeName+"\"")

	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Fail to get LUN related data for volume %s | %w", volumeName, err)
	}

	if len(resp.Members) == 0 {
		logger.Debugf("HPE debug no VLUN found for volume: %s", volumeName)
		return nil, nil
	}

	member := resp.Members[0]
	logger.Debugf("VLUN: %d | Hostname: %s | Serial: %s", member.LUN, member.Hostname, member.Serial)
	return &member, nil
}

// disconnectHostFromVolume deletes a connection between a host and volume.
func (p *hpeAlletraClient) disconnectHostFromVolume(poolName string, volName string, hostName string) error {
	logger.Debugf("HPE disconnectHostFromVolume()")

	vlun, errVLUN := p.getVLUN(volName)
	if errVLUN != nil {
		logger.Debugf("HPE debug error: %s", errVLUN)
		return fmt.Errorf("HPE Error %w", errVLUN)
	}

	if vlun == nil {
		logger.Debugf("HPE debug no need to disconnect host %s from volume %s", hostName, volName)
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

// getHosts retrieves an existing HPE Storage host.
func (p *hpeAlletraClient) getHosts() ([]hpeHost, error) {
	logger.Debugf("HPE getHosts()")

	var resp hpeRespMembers[hpeHost]

	url := api.NewURL().Path("api", "v1", "hosts")
	err := p.requestAuthenticated(http.MethodGet, url.URL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to get hosts: %w", err)
	}

	logger.Debugf("HPE total hosts found: %d", len(resp.Members))
	return resp.Members, nil
}

// getCurrentHost retrieves the HPE Storage host linked to the current LXD host.
// The HPE Storage host is considered a match if it includes the fully qualified
// name of the LXD host that is determined by the configured mode.
func (p *hpeAlletraClient) getCurrentHost() (*hpeHost, error) {
	logger.Debugf("HPE getCurrentHost()")

	connector, err := p.driver.connector()
	if err != nil {
		return nil, err
	}

	qn, err := connector.QualifiedName()
	if err != nil {
		return nil, err
	}
	logger.Debugf("HPE searching for: %s", qn)

	hosts, err := p.getHosts()
	if err != nil {
		return nil, err
	}

	mode := connector.Type()

	for _, host := range hosts {
		if mode == connectors.TypeISCSI {
			for _, iscsiPath := range host.ISCSIPaths {
				if iscsiPath.Name == qn {
					logger.Debugf("HPE iSCSI QN found: %s", qn)
					return &host, nil
				}
			}
		}

		if mode == connectors.TypeNVME {
			for _, nvmePath := range host.NVMETCPPaths {
				if nvmePath.NQN == qn {
					logger.Debugf("HPE NVMe/TCP QN found: %s", qn)
					return &host, nil
				}
			}
		}
	}

	logger.Debugf("HPE getCurrentHost() host NOT found: %s", qn)

	return nil, api.StatusErrorf(http.StatusNotFound, "Host with qualified name %q not found", qn)
}

// createHost creates a new host with provided initiator qualified names that can be associated
// with specific volumes.
func (p *hpeAlletraClient) createHost(hostName string, qns []string) error {
	logger.Debugf("HPE createHost()")

	req := map[string]any{
		"descriptors": map[string]any{
			"comment": "Created and managed by LXD",
		},
	}

	connector, err := p.driver.connector()
	if err != nil {
		return err
	}
	req["name"] = hostName

	switch connector.Type() {
	case connectors.TypeISCSI:
		req["iqns"] = qns[0] //FIXME: ugly hack to allign types between pure and hpe
	case connectors.TypeNVME:
		req["NQN"] = qns[0]
		req["transportType"] = 2 // TCP
	default:
		return fmt.Errorf("Unsupported HPE Storage mode %q", connector.Type())
	}

	url := api.NewURL().Path("api", "v1", "hosts")
	err = p.requestAuthenticated(http.MethodPost, url.URL, req, nil)
	if err != nil {

		if hpeErr, ok := err.(*hpeError); ok {
			switch hpeErr.Code {
			case 16:
				logger.Debugf("HPE debug host already exists: %s", hostName)
				return nil
			default:
				return fmt.Errorf("HPE debug. hpeErr.Code: %d. hpeErr.Desc: %s", hpeErr.Code, hpeErr.Desc)
			}
		}

		logger.Debugf("HPE debug failed to create host: %s", hostName)
		return fmt.Errorf("Failed to create host %q: %w", hostName, err)
	}

	return nil
}

// updateHost updates an existing host.
func (p *hpeAlletraClient) updateHost(hostName string, qns []string) error {
	return fmt.Errorf("Failed to update host %q. Operation not supported.", hostName)
}

// deleteHost deletes an existing host.
func (p *hpeAlletraClient) deleteHost(hostName string) error {
	logger.Debugf("HPE deleteHost()")

	url := api.NewURL().Path("api", "v1", "hosts", hostName)
	err := p.requestAuthenticated(http.MethodDelete, url.URL, nil, nil)
	if err != nil {
		if hpeErr, ok := err.(*hpeError); ok {
			switch hpeErr.Code {
			case 26:
				logger.Debugf("HPE debug. Host %s will not be deleted since it has exported volumes", hostName)
				return nil
			}
		}
		return fmt.Errorf("Failed to delete host %q: %w", hostName, err)
	}

	return nil
}
