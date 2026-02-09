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
	"net/url"
	"sync/atomic"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

const (
	powerStoreAuthorizationCookieName = "auth_cookie"
	powerStoreCSRFHeaderName          = "DELL-EMC-TOKEN"
)

// powerStoreTokenCache stores shared PowerStore login sessions.
var powerStoreTokenCache = &tokenCache[powerStoreLoginSession]{}

// powerStoreLoginSession describes PowerStore login session.
type powerStoreLoginSession struct {
	ID              string
	IdleTimeout     time.Duration
	LastInteraction atomic.Pointer[time.Time]
	AuthToken       string
	CSRFToken       string
}

func newPowerStoreLoginSession(id string, idleTimeout time.Duration, authToken, csrfToken string) *powerStoreLoginSession {
	ls := &powerStoreLoginSession{
		ID:          id,
		IdleTimeout: idleTimeout - 30*time.Second, // subtract to add safety margin for a potential time skew
		AuthToken:   authToken,
		CSRFToken:   csrfToken,
	}
	ls.Interacted()
	return ls
}

// IsValid inform is the token associated with the login session is not expired.
func (ls *powerStoreLoginSession) IsValid() bool {
	if ls == nil {
		return false
	}
	lastInteraction := *ls.LastInteraction.Load()
	return time.Now().Before(lastInteraction.Add(ls.IdleTimeout))
}

// Interacted informs the login session object that interaction occurred and last interaction time should be updated.
func (ls *powerStoreLoginSession) Interacted() {
	now := time.Now()
	ls.LastInteraction.Store(&now)
}

func makePowerStoreLoginSessionKey(url, username, password string) string {
	url = base64.StdEncoding.EncodeToString([]byte(url))
	username = base64.StdEncoding.EncodeToString([]byte(username))
	password = base64.StdEncoding.EncodeToString([]byte(password))
	return fmt.Sprintf("%s:%s:%s", url, username, password)
}

// powerStoreSprintfLimit acts just like fmt.Sprintf, but trims the output to the specified number of characters.
func powerStoreSprintfLimit(limit int, format string, args ...any) string {
	x := fmt.Sprintf(format, args...)
	if len(x) > limit {
		x = x[:limit]
	}
	return x
}

// powerStoreError contains arbitrary error responses from PowerStore.
type powerStoreError struct {
	httpStatusCode int
	details        powerStoreErrorResponseResource
	decoderErr     error
}

func newPowerStoreError(resp *http.Response) error {
	if resp.StatusCode == http.StatusUnauthorized {
		return api.NewStatusError(http.StatusUnauthorized, "Unauthorized request")
	}
	e := &powerStoreError{httpStatusCode: resp.StatusCode}
	if resp.Header.Get("Content-Type") != "application/json" || resp.Header.Get("Content-Length") == "0" {
		return e
	}
	if err := json.NewDecoder(resp.Body).Decode(&e.details); err != nil {
		e.decoderErr = fmt.Errorf("unmarshal HTTP error response body: %w", err)
	}
	return e
}

// Error attempts to return all kinds of errors from the PowerStore API in a nicely formatted way.
func (e *powerStoreError) Error() string {
	msg := "PowerSore API error"
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

// ErrorCode attempts to extract the error code value from a PowerStore response.
func (e *powerStoreError) ErrorCode() string {
	for _, em := range e.details.Messages {
		if em != nil && em.Code != "" {
			return em.Code
		}
	}
	return ""
}

// HTTPStatusCode attempts to extract the HTTP status code value from a PowerStore response.
func (e *powerStoreError) HTTPStatusCode() int {
	return e.httpStatusCode
}

type powerStoreErrorResponseResource struct {
	Messages []*powerStoreErrorMessageResource `json:"messages,omitempty"`
}

type powerStoreErrorMessageResource struct {
	Severity    string                                    `json:"severity"`
	Code        string                                    `json:"code"`
	MessageL10n string                                    `json:"message_l10n"`
	Arguments   []*powerStoreErrorMessageArgumentResource `json:"arguments,omitempty"`
}

type powerStoreErrorMessageArgumentResource struct {
	Delimiter string                             `json:"delimiter,omitempty"`
	Messages  []*powerStoreErrorInstanceResource `json:"messages,omitempty"`
}

type powerStoreErrorInstanceResource struct {
	Severity    string   `json:"severity"`
	Code        string   `json:"code"`
	MessageL10n string   `json:"message_l10n"`
	Arguments   []string `json:"arguments,omitempty"`
}

// powerStoreClient holds the PowerStore HTTP API client.
type powerStoreClient struct {
	gateway              string
	gatewaySkipTLSVerify bool
	username             string
	password             string
}

// newPowerStoreClient creates a new instance of the PowerStore HTTP API client.
func newPowerStoreClient(driver *powerstore) *powerStoreClient {
	return &powerStoreClient{
		gateway:              driver.config["powerstore.gateway"],
		gatewaySkipTLSVerify: shared.IsFalse(driver.config["powerstore.gateway.verify"]),
		username:             driver.config["powerstore.user.name"],
		password:             driver.config["powerstore.user.password"],
	}
}

func (c *powerStoreClient) marshalHTTPRequestBody(src any) (io.Reader, error) {
	if src == nil {
		return nil, nil
	}
	dst := &bytes.Buffer{}
	encoder := json.NewEncoder(dst)
	if err := encoder.Encode(src); err != nil {
		return nil, err
	}
	return dst, nil
}

func (c *powerStoreClient) doHTTPRequest(ctx context.Context, method string, path string, requestData, responseData any, requestEditors ...func(*http.Request) error) (*http.Response, error) {
	body, err := c.marshalHTTPRequestBody(requestData)
	if err != nil {
		return nil, fmt.Errorf("marshall HTTP request body: %s: %w", path, err)
	}

	url := c.gateway + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Add("Accept", "application/json")
	if body != nil {
		req.Header.Add("Content-Type", "application/json")
	}

	for _, edit := range requestEditors {
		if err := edit(req); err != nil {
			return nil, err
		}
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: c.gatewaySkipTLSVerify,
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode > 299 {
		return resp, newPowerStoreError(resp)
	}
	if responseData != nil {
		if err := json.NewDecoder(resp.Body).Decode(responseData); err != nil {
			return resp, fmt.Errorf("unmarshal HTTP response body: %s: %w", path, err)
		}
	}
	return resp, nil
}

func (c *powerStoreClient) startNewLoginSession(ctx context.Context) (*powerStoreLoginSession, error) {
	resp, info, err := c.getLoginSessionInfoWithBasicAuthorization(ctx)
	if err != nil {
		return nil, fmt.Errorf("starting PowerStore session: %w", err)
	}
	if len(info) < 1 {
		return nil, errors.New("starting PowerStore session: invalid session information")
	}

	if info[0].IsPasswordChangeRequired {
		return nil, errors.New("starting PowerStore session: password change required")
	}

	var authCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name != powerStoreAuthorizationCookieName {
			continue
		}
		authCookie = c
		break
	}
	if authCookie == nil {
		return nil, errors.New("starting PowerStore session: missing PowerStore authorization cookie")
	}

	csrf := resp.Header.Get(powerStoreCSRFHeaderName)
	if csrf == "" {
		return nil, errors.New("starting PowerStore session: missing PowerStore CSRF token")
	}

	return newPowerStoreLoginSession(info[0].ID, time.Duration(info[0].IdleTimeout)*time.Second, authCookie.Value, csrf), nil
}

func (c *powerStoreClient) getOrCreateLoginSession(ctx context.Context, sessionKey string) (*powerStoreLoginSession, error) {
	session := powerStoreTokenCache.Load(sessionKey)
	if session.IsValid() {
		return session, nil
	}
	return powerStoreTokenCache.Replace(sessionKey, func(ls *powerStoreLoginSession) (*powerStoreLoginSession, error) {
		if ls != session && ls.IsValid() {
			return ls, nil // session was already replaced with a new valid session
		}
		return c.startNewLoginSession(ctx)
	})
}

func (c *powerStoreClient) forceLoginSessionRemoval(sessionKey string, sessionToRemove *powerStoreLoginSession) {
	//nolint:errcheck // Replace returns error only if inner callback returns error.
	powerStoreTokenCache.Replace(sessionKey, func(ls *powerStoreLoginSession) (*powerStoreLoginSession, error) {
		if ls != sessionToRemove {
			return ls, nil // session was already replaced
		}
		return nil, nil // delete session
	})
}

func (c *powerStoreClient) doHTTPRequestWithLoginSession(ctx context.Context, method string, path string, requestData, responseData any, requestEditors ...func(*http.Request) error) (*http.Response, error) {
	sessionKey := makePowerStoreLoginSessionKey(c.gateway, c.username, c.password)

	session, err := c.getOrCreateLoginSession(ctx, sessionKey)
	if err != nil {
		return nil, err
	}

	requestEditors = append([]func(*http.Request) error{c.withLoginSession(session)}, requestEditors...)
	resp, err := c.doHTTPRequest(ctx, method, path, requestData, responseData, requestEditors...)
	if resp != nil && resp.StatusCode == http.StatusUnauthorized {
		// there is something wrong with the session token, remove it
		c.forceLoginSessionRemoval(sessionKey, session)
	}
	return resp, err
}

func (c *powerStoreClient) withBasicAuthorization(username, password string) func(req *http.Request) error {
	return func(req *http.Request) error {
		token := base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%s:%s", username, password))
		req.Header.Set("Authorization", "Basic "+token)
		return nil
	}
}

func (c *powerStoreClient) withLoginSession(ls *powerStoreLoginSession) func(req *http.Request) error {
	return func(req *http.Request) error {
		req.Header.Add("Cookie", fmt.Sprintf("%s=%s", powerStoreAuthorizationCookieName, ls.AuthToken))
		req.Header.Set(powerStoreCSRFHeaderName, ls.CSRFToken)
		return nil
	}
}

func (c *powerStoreClient) withQueryParams(params url.Values) func(req *http.Request) error {
	return func(req *http.Request) error {
		if params == nil {
			req.URL.RawQuery = ""
			return nil
		}
		req.URL.RawQuery = params.Encode()
		return nil
	}
}

type powerStoreIDResource struct {
	ID string `json:"id"`
}

type powerStoreLoginSessionResource struct {
	ID                       string   `json:"id"`
	User                     string   `json:"user"`
	RoleIDs                  []string `json:"role_ids"`
	IdleTimeout              int64    `json:"idle_timeout"`
	IsPasswordChangeRequired bool     `json:"is_password_change_required"`
	IsBuiltInUser            bool     `json:"is_built_in_user"`
}

func (c *powerStoreClient) getLoginSessionInfoWithBasicAuthorization(ctx context.Context) (*http.Response, []*powerStoreLoginSessionResource, error) {
	body := []*powerStoreLoginSessionResource{}
	resp, err := c.doHTTPRequest(ctx, http.MethodGet, "/api/rest/login_session", nil, &body,
		c.withBasicAuthorization(c.username, c.password),
		c.withQueryParams(url.Values{"select": []string{"id,user,role_ids,idle_timeout,is_password_change_required,is_built_in_user"}}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("retrieving PowerStore login session info: %w", err)
	}
	return resp, body, nil
}
