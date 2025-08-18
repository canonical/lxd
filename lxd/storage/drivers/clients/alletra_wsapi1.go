package clients

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

const (
	sessionTypeRegular = 1

	apiErrorInvalidSessionKey = 6
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
		if resp.StatusCode == http.StatusForbidden && hpeErr.Code == apiErrorInvalidSessionKey {
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
		"sessionType": sessionTypeRegular,
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
