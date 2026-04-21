package clients

import (
	"crypto/tls"
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

	"github.com/canonical/lxd/shared/api"
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

// PowerStoreClient holds the PowerStore HTTP API client.
type PowerStoreClient struct {
	url                string
	skipTLSVerify      bool
	username           string
	password           string
	resourceNamePrefix string
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
