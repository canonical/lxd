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
	"strconv"
	"strings"
	"sync"

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
