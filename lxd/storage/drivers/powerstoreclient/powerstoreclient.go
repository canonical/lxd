package powerstoreclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/canonical/lxd/lxd/storage/drivers/tokencache"
	"github.com/canonical/lxd/shared/api"
)

const (
	authorizationCookieName = "auth_cookie"
	csrfHeaderName          = "DELL-EMC-TOKEN"
)

// LoginSession describes PowerStore login session.
type LoginSession struct {
	ID              string
	IdleTimeout     time.Duration
	LastInteraction atomic.Pointer[time.Time]
	AuthToken       string
	CSRFToken       string
}

func newPowerStoreLoginSession(id string, idleTimeout time.Duration, authToken, csrfToken string) *LoginSession {
	ls := &LoginSession{
		ID:          id,
		IdleTimeout: idleTimeout - 30*time.Second, // subtract to add safety margin for a potential time skew
		AuthToken:   authToken,
		CSRFToken:   csrfToken,
	}

	ls.Interacted()
	return ls
}

// IsValid inform if the token associated with the login session is not
// expired.
func (ls *LoginSession) IsValid() bool {
	if ls == nil {
		return false
	}

	lastInteraction := *ls.LastInteraction.Load()
	return time.Now().Before(lastInteraction.Add(ls.IdleTimeout))
}

// Interacted informs the login session object that interaction occurred and
// last interaction time should be updated.
func (ls *LoginSession) Interacted() {
	now := time.Now()
	ls.LastInteraction.Store(&now)
}

// makeFingerprint creates a makeFingerprint of the provided strings uniquely
// identifying them.
func makeFingerprint(pieces ...string) string {
	raw := bytes.Buffer{}
	// Use base64 on the provided strings and separate pieces with ':' to make
	// sure fingerprint is unique regardless of the strings content.
	for i, p := range pieces {
		_, _ = raw.WriteString(base64.StdEncoding.EncodeToString([]byte(p)))
		if i < len(pieces)-1 {
			raw.WriteByte(':')
		}
	}

	// Hash the concatenated data to shorten the resulting fingerprint.
	hash := sha256.Sum256(raw.Bytes())
	return base64.StdEncoding.EncodeToString(hash[:])
}

// PowerStoreError contains arbitrary error responses from PowerStore.
type PowerStoreError struct {
	httpStatusCode int
	details        errorResponseResource
	decoderErr     error
}

func newPowerStoreError(resp *http.Response) error {
	if resp.StatusCode == http.StatusUnauthorized {
		return api.NewStatusError(http.StatusUnauthorized, "Unauthorized request")
	}

	e := &PowerStoreError{httpStatusCode: resp.StatusCode}
	if resp.Header.Get("Content-Type") != "application/json" || resp.Header.Get("Content-Length") == "0" {
		return e
	}

	err := json.NewDecoder(resp.Body).Decode(&e.details)
	if err != nil {
		e.decoderErr = fmt.Errorf("Unmarshal HTTP error response body: %w", err)
	}

	return e
}

// Error attempts to return all kinds of errors from the PowerStore API in
// a nicely formatted way.
func (e *PowerStoreError) Error() string {
	msg := "PowerStore API error"
	if e.httpStatusCode != 0 {
		msg = fmt.Sprintf("%s %d response", msg, e.httpStatusCode)
	}

	details, err := json.Marshal(e.details)
	if err == nil && len(details) > 0 && !bytes.Equal(details, []byte("{}")) && !bytes.Equal(details, []byte("null")) {
		msg = fmt.Sprintf("%s; details: %s", msg, details)
	}

	if e.decoderErr != nil {
		msg = fmt.Sprintf("%s; response decoding error: %s", msg, e.decoderErr.Error())
	}

	return msg
}

// ErrorCode attempts to extract the PowerStore error code value. If the error
// do not contains the PowerStore error code code function returns an empty
// string.
func (e *PowerStoreError) ErrorCode() string {
	for _, em := range e.details.Messages {
		if em != nil && em.Code != "" {
			return em.Code
		}
	}
	return ""
}

// HTTPStatusCode attempts to extract the HTTP status code value from
// a PowerStore response. If the error is not associated with some HTTP error
// code function returns zero.
func (e *PowerStoreError) HTTPStatusCode() int {
	return e.httpStatusCode
}

type errorResponseResource struct {
	Messages []*errorMessageResource `json:"messages,omitempty"`
}

type errorMessageResource struct {
	Severity    string                          `json:"severity"`
	Code        string                          `json:"code"`
	MessageL10n string                          `json:"message_l10n"`
	Arguments   []*errorMessageArgumentResource `json:"arguments,omitempty"`
}

type errorMessageArgumentResource struct {
	Delimiter string                   `json:"delimiter,omitempty"`
	Messages  []*errorInstanceResource `json:"messages,omitempty"`
}

type errorInstanceResource struct {
	Severity    string   `json:"severity"`
	Code        string   `json:"code"`
	MessageL10n string   `json:"message_l10n"`
	Arguments   []string `json:"arguments,omitempty"`
}

// Client holds the PowerStore HTTP API client.
type Client struct {
	Gateway              string
	GatewaySkipTLSVerify bool
	Username             string
	Password             string
	TokenCache           *tokencache.TokenCache[LoginSession]
	VolumeNamePrefix     string
}

func (c *Client) startNewLoginSession(ctx context.Context) (*LoginSession, error) {
	resp, info, err := c.getLoginSessionInfoWithBasicAuthorization(ctx)
	if err != nil {
		return nil, fmt.Errorf("Starting PowerStore session: %w", err)
	}

	if len(info) < 1 {
		return nil, errors.New("Starting PowerStore session: Invalid session information")
	}

	sessionInfo := info[0]

	if sessionInfo.IsPasswordChangeRequired {
		return nil, errors.New("Starting PowerStore session: Password change required")
	}

	var authCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name != authorizationCookieName {
			continue
		}

		authCookie = c
		break
	}

	if authCookie == nil {
		return nil, errors.New("Starting PowerStore session: Missing PowerStore authorization cookie")
	}

	csrf := resp.Header.Get(csrfHeaderName)
	if csrf == "" {
		return nil, errors.New("Starting PowerStore session: Missing PowerStore CSRF token")
	}

	return newPowerStoreLoginSession(sessionInfo.ID, time.Duration(sessionInfo.IdleTimeout)*time.Second, authCookie.Value, csrf), nil
}

func (c *Client) getOrCreateLoginSession(ctx context.Context, sessionKey string) (*LoginSession, error) {
	if c.TokenCache == nil {
		return c.startNewLoginSession(ctx)
	}

	session := c.TokenCache.Load(sessionKey)
	if session.IsValid() {
		return session, nil
	}

	return c.TokenCache.Replace(sessionKey, func(ls *LoginSession) (*LoginSession, error) {
		if ls != session && ls.IsValid() {
			return ls, nil // session was already replaced with a new valid session
		}

		return c.startNewLoginSession(ctx)
	})
}

func (c *Client) forceLoginSessionRemoval(sessionKey string, sessionToRemove *LoginSession) {
	if c.TokenCache == nil {
		return
	}

	_, _ = c.TokenCache.Replace(sessionKey, func(ls *LoginSession) (*LoginSession, error) {
		if ls != sessionToRemove {
			return ls, nil // session was already replaced
		}

		return nil, nil // delete session
	})
}

func (c *Client) marshalHTTPRequestBody(src any) (io.Reader, error) {
	if src == nil {
		return nil, nil
	}

	dst := &bytes.Buffer{}
	err := json.NewEncoder(dst).Encode(src)
	if err != nil {
		return nil, err
	}

	return dst, nil
}

func (c *Client) doUnauthenticatedHTTPRequest(ctx context.Context, method string, path string, requestData, responseData any, requestEditors ...func(*http.Request) error) (*http.Response, error) {
	body, err := c.marshalHTTPRequestBody(requestData)
	if err != nil {
		return nil, fmt.Errorf("Marshal HTTP request body: %s: %w", path, err)
	}

	url := c.Gateway + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("Create request: %w", err)
	}

	req.Header.Add("Accept", "application/json")
	if body != nil {
		req.Header.Add("Content-Type", "application/json")
	}

	for _, edit := range requestEditors {
		err := edit(req)
		if err != nil {
			return nil, err
		}
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: c.GatewaySkipTLSVerify,
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Send request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode > 299 {
		return resp, newPowerStoreError(resp)
	}

	if responseData != nil {
		err := json.NewDecoder(resp.Body).Decode(responseData)
		if err != nil {
			return resp, fmt.Errorf("Unmarshal HTTP response body: %s: %w", path, err)
		}
	}
	return resp, nil
}

func (c *Client) doAuthenticatedHTTPRequest(ctx context.Context, method string, path string, requestData, responseData any, requestEditors ...func(*http.Request) error) (*http.Response, error) {
	sessionKey := makeFingerprint(c.Gateway, c.Username, c.Password)

	session, err := c.getOrCreateLoginSession(ctx, sessionKey)
	if err != nil {
		return nil, err
	}

	requestEditors = append([]func(*http.Request) error{c.withLoginSession(session)}, requestEditors...)
	resp, err := c.doUnauthenticatedHTTPRequest(ctx, method, path, requestData, responseData, requestEditors...)
	if resp != nil && resp.StatusCode == http.StatusUnauthorized {
		// there is something wrong with the session token, remove it
		c.forceLoginSessionRemoval(sessionKey, session)
	}

	return resp, err
}

func (c *Client) withBasicAuthorization(username, password string) func(req *http.Request) error {
	return func(req *http.Request) error {
		token := base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%s:%s", username, password))
		req.Header.Set("Authorization", "Basic "+token)
		return nil
	}
}

func (c *Client) withLoginSession(ls *LoginSession) func(req *http.Request) error {
	return func(req *http.Request) error {
		req.Header.Add("Cookie", fmt.Sprintf("%s=%s", authorizationCookieName, ls.AuthToken))
		req.Header.Set(csrfHeaderName, ls.CSRFToken)
		return nil
	}
}

func (c *Client) withQuery(query query) func(req *http.Request) error {
	return func(req *http.Request) error {
		if len(query) == 0 {
			req.URL.RawQuery = ""
			return nil
		}

		req.URL.RawQuery = query.URLParameters().Encode()
		return nil
	}
}

// QueryResponseLimit is the maximum number of items PowerStore can return in
// a single query response.
const QueryResponseLimit = 2000

// pagination encapsulates query request pagination data.
type pagination struct {
	Page         int
	ItemsPerPage int
}

// Offset computes offset value for the provided pagination state.
func (p pagination) Offset() int {
	page := max(0, p.Page)
	limit := p.Limit()
	return page * limit
}

// Limit computes limit value for the provided pagination state.
func (p pagination) Limit() int {
	return min(max(0, p.ItemsPerPage), QueryResponseLimit)
}

// query is a container for PowerStore query request parameters.
type query map[string]string

// Clone clones the provided query. If the query is nil it returns
// an initialized empty query.
func (q query) Clone() query {
	if q == nil {
		return query{}
	}

	return maps.Clone(q)
}

// Set sets the provided value under the specified key returning the new query.
func (q query) Set(key, val string) query {
	q = q.Clone()
	q[key] = val
	return q
}

// Paginate adds pagination parameters returning a new query.
func (q query) Paginate(pagination pagination) query {
	q = q.Clone()
	q["offset"] = strconv.Itoa(pagination.Offset())
	q["limit"] = strconv.Itoa(pagination.Limit())
	return q
}

// URLParameters transforms query into URL parameters.
func (q query) URLParameters() url.Values {
	params := url.Values{}
	for key, val := range q {
		params.Set(key, val)
	}

	return params
}

// queryResponseHasMoreItems informs if there are more items available for the HTTP PowerStore query response.
func queryResponseHasMoreItems(resp *http.Response) (bool, error) {
	if resp == nil || resp.StatusCode != http.StatusPartialContent {
		return false, nil
	}

	// valid Content-Range HTTP headers returned by PowerStore have a form:
	// - firstOffset '-' lastOffset '/' totalItems
	// - '*' '/' totalItems
	header := resp.Header.Get("Content-Range")
	if header == "" {
		return false, nil
	}

	rangeStr, totalItemsStr, ok := strings.Cut(header, "/")
	if !ok {
		return false, fmt.Errorf("Invalid format of Content-Range header: %q", header)
	}

	if rangeStr == "*" {
		return false, nil
	}

	fistOffsetStr, lastOffsetStr, ok := strings.Cut(rangeStr, "-")
	if !ok {
		return false, fmt.Errorf("Invalid format of Content-Range header: %q", header)
	}

	_, err := strconv.ParseUint(fistOffsetStr, 10, 64)
	if err != nil {
		return false, fmt.Errorf("Invalid format of Content-Range header: %q", header)
	}

	lastOffset, err := strconv.ParseUint(lastOffsetStr, 10, 64)
	if err != nil {
		return false, fmt.Errorf("Invalid format of Content-Range header: %q", header)
	}

	totalItems, err := strconv.ParseUint(totalItemsStr, 10, 64)
	if err != nil {
		return false, fmt.Errorf("Invalid format of Content-Range header: %q", header)
	}

	return totalItems > lastOffset+1, nil
}

// IDResource is any resource that just contains ID. This type is often used
// a substitute when only ID of some resource should be retrieved or used.
type IDResource struct {
	ID string `json:"id"`
}

type loginSessionResource struct {
	ID                       string   `json:"id"`
	User                     string   `json:"user"`
	RoleIDs                  []string `json:"role_ids"`
	IdleTimeout              int64    `json:"idle_timeout"`
	IsPasswordChangeRequired bool     `json:"is_password_change_required"`
	IsBuiltInUser            bool     `json:"is_built_in_user"`
}

func (c *Client) getLoginSessionInfoWithBasicAuthorization(ctx context.Context) (*http.Response, []*loginSessionResource, error) {
	body := []*loginSessionResource{}
	resp, err := c.doUnauthenticatedHTTPRequest(ctx, http.MethodGet, "/api/rest/login_session", nil, &body,
		c.withBasicAuthorization(c.Username, c.Password),
		c.withQuery(query{"select": "id,user,role_ids,idle_timeout,is_password_change_required,is_built_in_user"}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("Retrieving PowerStore login session info: %w", err)
	}

	return resp, body, nil
}

// VolumeResource describes a volume resource in PowerStore API.
type VolumeResource struct {
	ID            string                       `json:"id,omitempty"`
	Name          string                       `json:"name,omitempty"`
	Description   string                       `json:"description,omitempty"`
	Type          string                       `json:"type,omitempty"`
	State         string                       `json:"state,omitempty"`
	Size          int64                        `json:"size,omitempty"`
	LogicalUsed   int64                        `json:"logical_used,omitempty"`
	WWN           string                       `json:"wwn,omitempty"`
	AppType       string                       `json:"app_type,omitempty"`
	AppTypeOther  string                       `json:"app_type_other,omitempty"`
	VolumeGroups  []*IDResource                `json:"volume_groups,omitempty"`
	MappedVolumes []*HostVolumeMappingResource `json:"mapped_volumes,omitempty"`
}

// HostVolumeMappingResource describes a mapping between host and volume in
// PowerStore API.
type HostVolumeMappingResource struct {
	ID       string `json:"id,omitempty"`
	HostID   string `json:"host_id,omitempty"`
	VolumeID string `json:"volume_id,omitempty"`
}

func (c *Client) getVolumesByQuery(ctx context.Context, query query, filterOwnedByLxd bool) ([]*VolumeResource, bool, error) {
	query = query.Set("select", "id,name,description,type,state,size,logical_used,wwn,app_type,app_type_other,volume_groups(id),mapped_volumes(id,host_id,volume_id)")

	body := []*VolumeResource{}
	resp, err := c.doAuthenticatedHTTPRequest(ctx, http.MethodGet, "/api/rest/volume", nil, &body,
		c.withQuery(query),
	)
	if err != nil {
		return nil, false, fmt.Errorf("Retrieving information about PowerStore volumes: %w", err)
	}

	hasMore, err := queryResponseHasMoreItems(resp)
	if err != nil {
		return nil, false, fmt.Errorf("Retrieving information about PowerStore volumes: %w", err)
	}

	if !filterOwnedByLxd {
		return body, hasMore, nil
	}

	// in most cases all items in the returned body will belong to the current storage pool and no item will be filtered out
	filtered := make([]*VolumeResource, 0, len(body))
	for _, v := range body {
		if !strings.HasPrefix(v.Name, c.VolumeNamePrefix) {
			continue
		}

		filtered = append(filtered, v)
	}

	return filtered, hasMore, nil
}

func (c *Client) getVolumeByQuery(ctx context.Context, query query, filterOwnedByLxd bool) (*VolumeResource, error) {
	vols, _, err := c.getVolumesByQuery(ctx, query.Paginate(pagination{ItemsPerPage: 1}), filterOwnedByLxd)
	if err != nil {
		return nil, err
	}

	if len(vols) == 0 {
		return nil, nil
	}

	return vols[0], nil
}

// GetVolumes retrieves list of volume associated with the storage pool.
func (c *Client) GetVolumes(ctx context.Context) ([]*VolumeResource, error) {
	query := query{"name": fmt.Sprintf("ilike.%s*", c.VolumeNamePrefix)}

	var vols []*VolumeResource
	for page := 0; ; page++ {
		volsPage, hasMore, err := c.getVolumesByQuery(ctx, query.Paginate(pagination{Page: page}), true)
		if err != nil {
			return nil, err
		}

		vols = append(vols, volsPage...)
		if !hasMore {
			return vols, nil
		}
	}
}

// GetVolumeByID retrieves volume using its ID.
func (c *Client) GetVolumeByID(ctx context.Context, id string) (*VolumeResource, error) {
	return c.getVolumeByQuery(ctx, query{"id": "eq." + id}, true)
}

// GetVolumeByName retrieves volume using its name.
func (c *Client) GetVolumeByName(ctx context.Context, name string) (*VolumeResource, error) {
	return c.getVolumeByQuery(ctx, query{"name": "eq." + name}, true)
}

// CreateVolume creates a new volume.
func (c *Client) CreateVolume(ctx context.Context, vol *VolumeResource) error {
	body := &IDResource{}
	_, err := c.doAuthenticatedHTTPRequest(ctx, http.MethodPost, "/api/rest/volume", vol, body)
	if err != nil {
		return fmt.Errorf("Creating PowerStore volume: %w", err)
	}

	// Fetch volume to populate all fields.
	created, err := c.GetVolumeByID(ctx, body.ID)
	if err != nil {
		return fmt.Errorf("Creating PowerStore volume: %w", err)
	}

	if created == nil {
		return errors.New("Creating PowerStore volume: No data of new volume found")
	}

	*vol = *created
	return nil
}

// DeleteVolumeByID deletes volume using its ID.
func (c *Client) DeleteVolumeByID(ctx context.Context, id string) error {
	_, err := c.doAuthenticatedHTTPRequest(ctx, http.MethodDelete, "/api/rest/volume/"+id, nil, nil)
	if err != nil {
		return fmt.Errorf("Deleting PowerStore volume: %w", err)
	}

	return nil
}

type volumeModifyResource struct {
	Size int64 `json:"size,omitempty"`
}

// ResizeVolumeByID creates a new volume.
func (c *Client) ResizeVolumeByID(ctx context.Context, id string, newSize int64) error {
	reqBody := &volumeModifyResource{Size: newSize}
	_, err := c.doAuthenticatedHTTPRequest(ctx, http.MethodPatch, "/api/rest/volume/"+id, reqBody, nil)
	if err != nil {
		return fmt.Errorf("Resizing PowerStore volume: %w", err)
	}

	return nil
}

type volumeGroupRemoveMembersResource struct {
	VolumeIDs []string `json:"volume_ids,omitempty"`
}

// RemoveMembersFromVolumeGroup removes volumes from the volume group.
func (c *Client) RemoveMembersFromVolumeGroup(ctx context.Context, id string, volumeIDs []string) error {
	reqBody := &volumeGroupRemoveMembersResource{VolumeIDs: volumeIDs}
	_, err := c.doAuthenticatedHTTPRequest(ctx, http.MethodPost, "/api/rest/volume_group/"+id+"/remove_members", reqBody, nil)
	if err != nil {
		return fmt.Errorf("Removing members from PowerStore volume group: %w", err)
	}

	return nil
}
