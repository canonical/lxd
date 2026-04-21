package clients

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/shared/api"
)

const (
	// powerStoreAuthCookieName is the name of the cookie which contains PowerStore auth token.
	powerStoreAuthCookieName = "auth_cookie"

	// powerStoreCSRFHeaderName is the name of the header which contains PowerStore CSRF token.
	powerStoreCSRFHeaderName = "dell-emc-token"

	// powerStoreQueryResponseLimit defines maximum number of items PowerStore can return in
	// a single query response.
	powerStoreQueryResponseLimit = 2000
)

// powerStoreResourceID represents a PowerStore resource ID.
type powerStoreResourceID struct {
	ID string `json:"id"`
}

func (powerStoreResourceID) selector() string {
	return "id"
}

// PowerStoreHostInitiator represents a PowerStore host initiator.
type PowerStoreHostInitiator struct {
	HostID   string `json:"host_id,omitempty"`
	PortName string `json:"port_name,omitempty"`
	PortType string `json:"port_type,omitempty"`
}

func (PowerStoreHostInitiator) selector() string {
	return "host_id,port_name,port_type"
}

// PowerStoreHostVolumeMapping represents a mapping between PowerStore host and volume.
type PowerStoreHostVolumeMapping struct {
	HostID   string `json:"host_id,omitempty"`
	VolumeID string `json:"volume_id,omitempty"`
}

// PowerStoreHost represents a PowerStore host.
type PowerStoreHost struct {
	ID            string                         `json:"id,omitempty"`
	Name          string                         `json:"name,omitempty"`
	OsType        string                         `json:"os_type,omitempty"`
	Initiators    []*PowerStoreHostInitiator     `json:"initiators,omitempty"`
	MappedVolumes []*PowerStoreHostVolumeMapping `json:"mapped_hosts,omitempty"`
}

func (PowerStoreHost) selector() string {
	return "id,name,os_type,initiators(id,port_name,port_type),mapped_hosts(id,host_id,volume_id)"
}

// PowerStoreVolume represents a PowerStore volume.
type PowerStoreVolume struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Type        string `json:"type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	LogicalUsed int64  `json:"logical_used,omitempty"`
	WWN         string `json:"wwn,omitempty"`
}

func (PowerStoreVolume) selector() string {
	return "id,name,type,size,logical_used,wwn"
}

type powerStoreErrorMessage struct {
	Message string `json:"message_l10n"`
}

// powerStoreError represents PowerStore error response.
type powerStoreError struct {
	StatusCode int                      `json:"-"`
	Messages   []powerStoreErrorMessage `json:"messages,omitempty"`
}

func newPowerStoreError(resp *http.Response) error {
	if resp.StatusCode == http.StatusUnauthorized {
		return api.NewStatusError(http.StatusUnauthorized, "Unauthorized request")
	}

	psErr := &powerStoreError{
		StatusCode: resp.StatusCode,
	}

	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		return psErr
	}

	err := json.NewDecoder(resp.Body).Decode(psErr)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return psErr
		}

		return fmt.Errorf("Failed unmarshalling HTTP error response body: %w", err)
	}

	return psErr
}

// Error returns formatted PowerStore API error message.
func (e *powerStoreError) Error() string {
	msg := "PowerStore API error"
	if e.StatusCode != 0 {
		msg = msg + " " + strconv.Itoa(e.StatusCode)
	}

	for _, em := range e.Messages {
		if em.Message != "" {
			msg = msg + ": " + em.Message
		}
	}

	return msg
}

// isPowerStoreError checks if the error is of type powerStoreError and matches the provided
// status code. If substrings are provided, it also checks if the error message contains any
// of the substrings.
func isPowerStoreError(err error, statusCode int, substrings ...string) bool {
	perr, ok := err.(*powerStoreError)
	if !ok {
		return false
	}

	if perr.StatusCode != statusCode {
		return false
	}

	if len(substrings) == 0 {
		return true
	}

	errMsg := strings.ToLower(perr.Error())
	for _, substring := range substrings {
		if strings.Contains(errMsg, strings.ToLower(substring)) {
			return true
		}
	}

	return false
}

var powerStoreSessions = make(map[string]powerStoreSession)
var powerStoreSessionsLock = &sync.RWMutex{}

// powerStoreSession describes PowerStore login session.
type powerStoreSession struct {
	ID        string
	AuthToken string
	CSRFToken string
}

// PowerStoreClient holds the PowerStore HTTP API client.
type PowerStoreClient struct {
	url                string
	skipTLSVerify      bool
	username           string
	password           string
	resourceNamePrefix string

	// currentSession holds the current session, which is set after successful login
	// and used for subsequent requests. Use [PowerStoreClient.session] to retrieve
	// the current session.
	currentSession *powerStoreSession
}

// NewPowerStoreClient creates a new instance of the PowerStore HTTP API client.
func NewPowerStoreClient(url string, username string, password string, skipTLSVerify bool, resourceNamePrefix string) *PowerStoreClient {
	return &PowerStoreClient{
		url:                url,
		skipTLSVerify:      skipTLSVerify,
		username:           username,
		password:           password,
		resourceNamePrefix: resourceNamePrefix,
	}
}

// sessionKey generates a hashtable key for a session.
func (c *PowerStoreClient) sessionKey() string {
	return c.url + c.username + c.password
}

// session retrieves the current session.
func (c *PowerStoreClient) session() *powerStoreSession {
	if c.currentSession != nil {
		return c.currentSession
	}

	key := c.sessionKey()

	powerStoreSessionsLock.RLock()
	defer powerStoreSessionsLock.RUnlock()

	session, ok := powerStoreSessions[key]
	if ok {
		s := session
		c.currentSession = &s
		return c.currentSession
	}

	return nil
}

// setSession sets the current session.
func (c *PowerStoreClient) setSession(session powerStoreSession) {
	key := c.sessionKey()

	powerStoreSessionsLock.Lock()
	defer powerStoreSessionsLock.Unlock()

	powerStoreSessions[key] = session
	c.currentSession = &session
}

// invalidateSession invalidates the current session.
func (c *PowerStoreClient) invalidateSession() {
	key := c.sessionKey()

	powerStoreSessionsLock.Lock()
	defer powerStoreSessionsLock.Unlock()

	delete(powerStoreSessions, key)
	c.currentSession = nil
}

// login initiates request() using PowerStore username and password.
// If successful, the session key is retrieved and stored within client structure.
// Once stored, the session key is reused for further requests.
func (c *PowerStoreClient) login() (*powerStoreSession, error) {
	session := c.session()
	if session != nil {
		return session, nil
	}

	url := api.NewURL().Path("/api/rest/login_session")
	url = url.WithQuery("select", "id,user,role_ids,idle_timeout,is_password_change_required,is_built_in_user")

	// Base64 encode username and password for basic authentication.
	reqHeaders := map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(c.username+":"+c.password)),
	}

	respHeaders := make(http.Header)
	respBody := []struct {
		ID                       string `json:"id"`
		User                     string `json:"user"`
		IsPasswordChangeRequired bool   `json:"is_password_change_required"`
	}{}

	err := c.request(http.MethodGet, url.URL, nil, reqHeaders, &respBody, respHeaders)
	if err != nil {
		return nil, fmt.Errorf("Failed logging into PowerStore: %w", err)
	}

	if len(respBody) < 1 {
		return nil, errors.New("Failed logging into PowerStore: Login response is missing session information")
	}

	sessionInfo := respBody[0]
	if sessionInfo.IsPasswordChangeRequired {
		return nil, errors.New("Failed logging into PowerStore: Password change required")
	}

	resp := &http.Response{Header: http.Header{}}

	// Parse CSRF token from response headers.
	csrf := resp.Header.Get(powerStoreCSRFHeaderName)
	if csrf == "" {
		return nil, errors.New("Failed logging into PowerStore: Login response missing CSRF token")
	}

	// Parse auth cookie.
	var authCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name != powerStoreAuthCookieName {
			continue
		}

		authCookie = c
		break
	}

	if authCookie == nil {
		return nil, errors.New("Starting PowerStore session: Missing PowerStore authorization cookie")
	}

	// Cache new session.
	session = &powerStoreSession{
		ID:        sessionInfo.ID,
		AuthToken: authCookie.Value,
		CSRFToken: csrf,
	}

	c.setSession(*session)
	return session, nil
}

// request issues a HTTP request against the PowerStore gateway.
func (c *PowerStoreClient) request(method string, url url.URL, reqBody map[string]any, reqHeaders map[string]string, respBody any, respHeaders http.Header) error {
	gw := c.url
	if !strings.Contains(gw, "://") {
		return fmt.Errorf("Invalid PowerStore URL %q: Missing protocol", gw)
	}

	gwURL, err := url.Parse(gw)
	if err != nil {
		return fmt.Errorf("Failed parsing PowerStore URL %q: %w", gw, err)
	}

	url.Scheme = gwURL.Scheme
	url.Host = gwURL.Host

	// Prepend gateway path with the request path in case PowerStore is served on a sub-path.
	url.Path = path.Join(gwURL.Path, url.Path)

	var reqBodyReader io.Reader

	if reqBody != nil {
		reqBodyReader, err = createBodyReader(reqBody)
		if err != nil {
			return err
		}
	}

	req, err := http.NewRequest(method, url.String(), reqBodyReader)
	if err != nil {
		return fmt.Errorf("Failed creating request: %w", err)
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
				InsecureSkipVerify: c.skipTLSVerify,
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Failed sending request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return newPowerStoreError(resp)
	}

	if respBody != nil {
		err := json.NewDecoder(resp.Body).Decode(respBody)
		if err != nil {
			return fmt.Errorf("Failed reading response body from %q: %w", url.String(), err)
		}
	}

	// Extract the response headers if requested.
	if respHeaders != nil {
		maps.Copy(respHeaders, resp.Header)
	}

	return nil
}

// requestAuthenticated issues an authenticated HTTP request against the PowerStore API gateway.
// In case the access token is expired, the function automatically attempts to obtain a new one.
func (c *PowerStoreClient) requestAuthenticated(method string, url url.URL, reqBody map[string]any, respBody any, respHeaders http.Header) error {
	// If request fails with an unauthorized error, the request will be retried after
	// requesting a new access token.
	retries := 1

	for {
		// Ensure we are logged into the PowerStore.
		session, err := c.login()
		if err != nil {
			return err
		}

		// Set access token as request header.
		reqHeaders := map[string]string{
			"Cookie":                 powerStoreAuthCookieName + "=" + session.AuthToken,
			powerStoreCSRFHeaderName: session.CSRFToken,
		}

		// Initiate request.
		err = c.request(method, url, reqBody, reqHeaders, respBody, respHeaders)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusUnauthorized) && retries > 0 {
				// The failure is likely due to an expired session.
				// Invalidate session and try again.
				c.invalidateSession()
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

// withPaginationQuery adds pagination parameters to the provided URL query.
func withPaginationQuery(url url.URL, offset uint64, limit int) url.URL {
	if limit <= 0 || limit > powerStoreQueryResponseLimit {
		limit = powerStoreQueryResponseLimit
	}

	q := url.Query()
	q.Set("offset", strconv.FormatUint(offset, 10))
	q.Set("limit", strconv.Itoa(limit))
	url.RawQuery = q.Encode()
	return url
}

// parsePaginationOffset determines whether the response headers indicate there are more items
// available to be retrieved and the offset to be used for the next query.
func parsePaginationOffset(headers http.Header) (newOffset uint64, hasMore bool, err error) {
	if headers == nil {
		return 0, false, nil
	}

	// valid Content-Range HTTP headers returned by PowerStore have a form:
	// - firstOffset '-' lastOffset '/' totalItems
	// - '*' '/' totalItems
	header := headers.Get("Content-Range")
	if header == "" {
		return 0, false, nil
	}

	errInvalidHeader := func() (uint64, bool, error) {
		return 0, false, fmt.Errorf("Invalid format of Content-Range header: %q", header)
	}

	rangeStr, totalItemsStr, ok := strings.Cut(header, "/")
	if !ok {
		return errInvalidHeader()
	}

	if rangeStr == "*" {
		return 0, false, nil
	}

	_, lastOffsetStr, ok := strings.Cut(rangeStr, "-")
	if !ok {
		return errInvalidHeader()
	}

	lastOffset, err := strconv.ParseUint(lastOffsetStr, 10, 64)
	if err != nil {
		return errInvalidHeader()
	}

	totalItems, err := strconv.ParseUint(totalItemsStr, 10, 64)
	if err != nil {
		return errInvalidHeader()
	}

	newOffset = lastOffset + 1
	return newOffset, totalItems > newOffset, nil
}

// DiscoveryAddresses retrieves list of discovery addresses used to discover storage targets for
// the provided connector type.
func (c *PowerStoreClient) DiscoveryAddresses(connectorType string) ([]string, error) {
	var resp = []struct {
		Address  string   `json:"address,omitempty"`
		Purposes []string `json:"purposes,omitempty"`
	}{}

	url := api.NewURL().Path("api", "rest", "ip_pool_address")
	url = url.WithQuery("select", "address,purposes")

	err := c.requestAuthenticated(http.MethodGet, url.URL, nil, &resp, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving configured PowerStore IP addresses: %w", err)
	}

	// Filter IP addresses based on their purpose, which should match the connector type.
	var purpose string
	switch connectorType {
	case connectors.TypeISCSI:
		purpose = "Storage_Iscsi_Target"
	case connectors.TypeNVME:
		purpose = "Storage_NVMe_TCP_Port"
	default:
		return nil, fmt.Errorf("Unsupported connector type: %q", connectorType)
	}

	// Additionally, PowerStore might have floating IP address that can be used for
	// NVMe/iSCSI discovery.
	genericIPPurpose := "Storage_Cluster_Floating"

	var addresses []string
	for _, ip := range resp {
		if slices.Contains(addresses, ip.Address) {
			continue
		}

		if slices.Contains(ip.Purposes, purpose) || slices.Contains(ip.Purposes, genericIPPurpose) {
			addresses = append(addresses, ip.Address)
		}
	}

	return addresses, nil
}

// GetCurrentHost retrieves the PowerStore host linked to the current LXD host.
// The PowerStore host is considered a match if it includes the fully qualified
// name of the LXD host that is determined by the configured mode.
func (c *PowerStoreClient) GetCurrentHost(connectorType string, qn string) (*PowerStoreHost, error) {
	portType, err := powerStoreConnectorToPortType(connectorType)
	if err != nil {
		return nil, err
	}

	// Find initiator with the provided port type (connector type) and name (qualified name),
	// and retrieve the ID of the host it belongs to.
	var initiator PowerStoreHostInitiator

	url := api.NewURL().Path("api", "rest", "initiator")
	url = url.WithQuery("select", initiator.selector())
	url = url.WithQuery("port_type", "eq."+string(portType))
	url = url.WithQuery("port_name", "eq."+qn)

	var initiators []PowerStoreHostInitiator
	err = c.requestAuthenticated(http.MethodGet, url.URL, nil, &initiators, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving PowerStore host initiator: %w", err)
	}

	switch len(initiators) {
	case 0:
		return nil, api.StatusErrorf(http.StatusNotFound, "Host initiator with port name %q and type %q not found", qn, portType)
	case 1:
		initiator = initiators[0]
	default:
		return nil, fmt.Errorf("Multiple host initiators found with port name %q and type %q", qn, portType)
	}

	// Retrieve the actual host.
	var host PowerStoreHost
	url = api.NewURL().Path("api", "rest", "host", initiator.HostID)
	url = url.WithQuery("select", host.selector())

	err = c.requestAuthenticated(http.MethodGet, url.URL, nil, &host, nil)
	if err != nil {
		if isPowerStoreError(err, http.StatusNotFound) {
			return nil, api.StatusErrorf(http.StatusNotFound, "Host with initiator port name %q and type %q not found", qn, portType)
		}

		return nil, fmt.Errorf("Failed retrieving PowerStore host: %w", err)
	}

	return &host, nil
}

// GetHost retrieves host using its ID.
func (c *PowerStoreClient) GetHost(hostID string) (*PowerStoreHost, error) {
	var host PowerStoreHost

	url := api.NewURL().Path("api", "rest", "host", hostID)
	url = url.WithQuery("select", host.selector())

	err := c.requestAuthenticated(http.MethodGet, url.URL, nil, &host, nil)
	if err != nil {
		if isPowerStoreError(err, http.StatusNotFound) {
			return nil, api.StatusErrorf(http.StatusNotFound, "Host with ID %q not found", hostID)
		}

		return nil, fmt.Errorf("Failed retrieving PowerStore host: %w", err)
	}

	return &host, nil
}

// CreateHost creates new host and returns its ID.
func (c *PowerStoreClient) CreateHost(hostName string, connectorType string, qn string) (string, error) {
	portType, err := powerStoreConnectorToPortType(connectorType)
	if err != nil {
		return "", err
	}

	req := map[string]any{
		"name":    hostName,
		"os_type": "Linux", // Required by PowerStore API.
		"initiators": []map[string]any{
			{
				"port_name": qn,
				"port_type": portType,
			},
		},
	}

	var resp powerStoreResourceID

	url := api.NewURL().Path("api", "rest", "host")
	err = c.requestAuthenticated(http.MethodPost, url.URL, req, &resp, nil)
	if err != nil {
		return "", fmt.Errorf("Failed creating PowerStore host: %w", err)
	}

	return resp.ID, nil
}

// DeleteHost deletes host using its ID.
func (c *PowerStoreClient) DeleteHost(hostID string) error {
	url := api.NewURL().Path("api", "rest", "host", hostID)
	err := c.requestAuthenticated(http.MethodDelete, url.URL, nil, nil, nil)
	if err != nil {
		if isPowerStoreError(err, http.StatusNotFound) {
			return api.StatusErrorf(http.StatusNotFound, "Host with ID %q not found", hostID)
		}

		return fmt.Errorf("Failed deleting PowerStore host: %w", err)
	}

	return nil
}

func (c *PowerStoreClient) getVolumes(queryFilter map[string]string) ([]PowerStoreVolume, error) {
	url := api.NewURL().Path("api", "rest", "volume")
	url = url.WithQuery("select", PowerStoreVolume{}.selector())

	for k, v := range queryFilter {
		url = url.WithQuery(k, v)
	}

	var offset uint64
	var volumes []PowerStoreVolume

	for {
		respBody := []PowerStoreVolume{}
		respHeaders := make(http.Header)

		pageURL := withPaginationQuery(url.URL, offset, powerStoreQueryResponseLimit)
		err := c.requestAuthenticated(http.MethodGet, pageURL, nil, &respBody, respHeaders)
		if err != nil {
			return nil, err
		}

		nextOffset, hasMoreItems, err := parsePaginationOffset(respHeaders)
		if err != nil {
			return nil, err
		}

		volumes = append(volumes, respBody...)
		offset = nextOffset

		if !hasMoreItems {
			break
		}
	}

	return volumes, nil
}

// GetVolumes retrieves list of volume associated with the storage pool.
func (c *PowerStoreClient) GetVolumes() ([]PowerStoreVolume, error) {
	filter := map[string]string{
		"name": "ilike." + c.resourceNamePrefix + "*",
		"or":   "(type.eq.Primary,type.eq.Clone)",
	}

	vols, err := c.getVolumes(filter)
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving PowerStore volumes: %w", err)
	}

	return vols, nil
}

// GetVolumeID retrieves ID of a volume with a given name.
func (c *PowerStoreClient) GetVolumeID(volumeName string) (string, error) {
	var resp powerStoreResourceID

	url := api.NewURL().Path("api", "rest", "volume", "name:"+volumeName)
	url = url.WithQuery("or", "(type.eq.Primary,type.eq.Clone)")
	url = url.WithQuery("select", "id")

	err := c.requestAuthenticated(http.MethodGet, url.URL, nil, &resp, nil)
	if err != nil {
		if isPowerStoreError(err, http.StatusNotFound) {
			return "", api.StatusErrorf(http.StatusNotFound, "Volume with name %q not found", volumeName)
		}

		return "", fmt.Errorf("Failed retrieving PowerStore volume with name %q: %w", volumeName, err)
	}

	return resp.ID, nil
}

// GetVolume retrieves volume using its name.
func (c *PowerStoreClient) GetVolume(volumeID string) (*PowerStoreVolume, error) {
	var resp PowerStoreVolume

	url := api.NewURL().Path("api", "rest", "volume", volumeID)
	url = url.WithQuery("or", "(type.eq.Primary,type.eq.Clone)")
	url = url.WithQuery("select", resp.selector())

	err := c.requestAuthenticated(http.MethodGet, url.URL, nil, &resp, nil)
	if err != nil {
		if isPowerStoreError(err, http.StatusNotFound) {
			return nil, api.StatusErrorf(http.StatusNotFound, "Volume with ID %q not found", volumeID)
		}

		return nil, fmt.Errorf("Failed retrieving PowerStore volume: %w", err)
	}

	return &resp, nil
}

// CreateVolume creates a new volume.
func (c *PowerStoreClient) CreateVolume(volumeName string, sizeBytes int64) (volumeID string, err error) {
	req := map[string]any{
		"name": volumeName,
		"size": sizeBytes,
	}

	var resp powerStoreResourceID

	url := api.NewURL().Path("api", "rest", "volume")
	err = c.requestAuthenticated(http.MethodPost, url.URL, req, &resp, nil)
	if err != nil {
		return "", fmt.Errorf("Failed creating PowerStore volume: %w", err)
	}

	return resp.ID, nil
}

// DeleteVolume deletes volume using its ID.
func (c *PowerStoreClient) DeleteVolume(volumeID string) error {
	req := map[string]any{
		"immediate": true, // Do not move the volume to the "recycle bin".
	}

	url := api.NewURL().Path("api", "rest", "volume", volumeID)
	err := c.requestAuthenticated(http.MethodDelete, url.URL, req, nil, nil)
	if err != nil && !isPowerStoreError(err, http.StatusNotFound) {
		return fmt.Errorf("Failed deleting PowerStore volume: %w", err)
	}

	return nil
}

// GetVolumeSnapshots retrieves list of snapshots associated with the provided volume.
func (c *PowerStoreClient) GetVolumeSnapshots(volumeID string) ([]PowerStoreVolume, error) {
	filter := map[string]string{
		"type":                        "eq.Snapshot",
		"protection_data->>parent_id": "eq." + volumeID,
	}

	snapshots, err := c.getVolumes(filter)
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving PowerStore snapshots: %w", err)
	}

	return snapshots, nil
}

// GetVolumeSnapshotID retrieves ID of a volume snapshot with a given name .
func (c *PowerStoreClient) GetVolumeSnapshotID(volumeID string, snapshotName string) (string, error) {
	var resp powerStoreResourceID

	url := api.NewURL().Path("api", "rest", "volume", "name:"+snapshotName)
	url = url.WithQuery("protection_data->>parent_id", "eq."+volumeID)
	url = url.WithQuery("type", "eq.Snapshot")
	url = url.WithQuery("select", resp.selector())

	err := c.requestAuthenticated(http.MethodGet, url.URL, nil, &resp, nil)
	if err != nil {
		if isPowerStoreError(err, http.StatusNotFound) {
			return "", api.StatusErrorf(http.StatusNotFound, "Volume snapshot with name %q not found", snapshotName)
		}

		return "", fmt.Errorf("Failed retrieving PowerStore volume snapshot: %w", err)
	}

	return resp.ID, nil
}

// CreateVolumeSnapshot creates a new snapshot of a volume.
func (c *PowerStoreClient) CreateVolumeSnapshot(volumeID string, snapshotName string) (string, error) {
	req := map[string]any{
		"name":        snapshotName,
		"description": "LXD Volume Snapshot of " + snapshotName,
	}

	var resp powerStoreResourceID

	url := api.NewURL().Path("api", "rest", "volume", volumeID, "snapshot")
	err := c.requestAuthenticated(http.MethodPost, url.URL, req, &resp, nil)
	if err != nil {
		return "", fmt.Errorf("Failed creating PowerStore volume snapshot: %w", err)
	}

	return resp.ID, nil
}

// DeleteVolumeSnapshot deletes a snapshot of a volume.
func (c *PowerStoreClient) DeleteVolumeSnapshot(snapshotID string) error {
	req := map[string]any{
		"immediate": true, // Do not move the snapshot to the "recycle bin".
	}

	url := api.NewURL().Path("api", "rest", "volume", snapshotID)
	err := c.requestAuthenticated(http.MethodDelete, url.URL, req, nil, nil)
	if err != nil {
		return fmt.Errorf("Failed deleting PowerStore volume snapshot: %w", err)
	}

	return nil
}

// powerStoreConnectorToPortType converts connector type to PowerStore port type used in initiators.
func powerStoreConnectorToPortType(connectorType string) (string, error) {
	switch connectorType {
	case connectors.TypeISCSI:
		return "iSCSI", nil
	case connectors.TypeNVME:
		return "NVMe", nil
	default:
		return "", fmt.Errorf("Unsupported connector type: %q", connectorType)
	}
}
