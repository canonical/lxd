package lxd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/tcp"
	"github.com/canonical/lxd/shared/version"
)

// ProtocolDevLXD represents a devLXD API server.
type ProtocolDevLXD struct {
	ctx context.Context

	// Context related to the current connection.
	ctxConnected       context.Context
	ctxConnectedCancel context.CancelFunc

	// HTTP client information.
	http          *http.Client
	httpBaseURL   url.URL
	httpUnixPath  string
	httpUserAgent string

	eventListenerManager *eventListenerManager

	// isDevLXDOverVsock indicates whether the devLXD connection is over vsock.
	isDevLXDOverVsock bool

	bearerToken string

	// clusterTarget is the name of the cluster member that this client is targeting.
	clusterTarget string
}

// GetConnectionInfo returns the basic connection information used to interact with the server.
func (r *ProtocolDevLXD) GetConnectionInfo() (*ConnectionInfo, error) {
	return &ConnectionInfo{
		Protocol:   "devlxd",
		URL:        r.httpBaseURL.String(),
		SocketPath: r.httpUnixPath,
		Target:     r.clusterTarget,
	}, nil
}

// GetHTTPClient returns the http client used for the connection. This can be used to set custom http options.
func (r *ProtocolDevLXD) GetHTTPClient() (*http.Client, error) {
	if r.http == nil {
		return nil, errors.New("HTTP client isn't set, bad connection")
	}

	return r.http, nil
}

// DoHTTP performs a Request.
func (r *ProtocolDevLXD) DoHTTP(req *http.Request) (resp *http.Response, err error) {
	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	return r.http.Do(req)
}

// Disconnect is a no-op for devLXD.
func (r *ProtocolDevLXD) Disconnect() {
	r.ctxConnectedCancel()
}

// UseTarget returns a client that will target a specific cluster member.
// Use this for member-specific operations such as creating a local storage
// volume on a specific cluster member.
func (r *ProtocolDevLXD) UseTarget(name string) DevLXDServer {
	server := *r
	server.clusterTarget = name
	return &server
}

// UseBearerToken returns a client that will use the provided bearer token for authentication.
func (r *ProtocolDevLXD) UseBearerToken(bearerToken string) DevLXDServer {
	server := *r
	server.bearerToken = bearerToken
	return &server
}

// RawQuery allows directly querying the devLXD.
//
// This should only be used by internal LXD tools.
func (r *ProtocolDevLXD) RawQuery(method string, path string, data any, ETag string) (*api.DevLXDResponse, string, error) {
	url := r.httpBaseURL.String() + path
	return r.rawQuery(method, url, data, ETag)
}

// rawQuery is a method that sends HTTP request to the devLXD with the provided
// method, URL, data, and ETag. It processes the request based on the data's
// type and handles the HTTP response, returning parsed results or an error
// if it occurs.
func (r *ProtocolDevLXD) rawQuery(method string, url string, data any, ETag string) (devLXDResp *api.DevLXDResponse, etag string, err error) {
	// Log the request
	logger.Debug("Sending request to devLXD", logger.Ctx{
		"method": method,
		"url":    url,
		"etag":   ETag,
	})

	// Setup new request.
	req, err := NewRequestWithContext(r.ctx, method, url, data, ETag)
	if err != nil {
		return nil, "", err
	}

	// Set the bearer token
	if r.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+r.bearerToken)
	}

	req.Header.Set("User-Agent", r.httpUserAgent)

	// Send the request.
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, "", err
	}

	defer resp.Body.Close()

	// If client is connected over vsock, the response is expected to be in LXD format (api.Response).
	if r.isDevLXDOverVsock {
		resp, etag, err := lxdParseResponse(resp)
		if err != nil {
			return nil, "", err
		}

		return &api.DevLXDResponse{
			Content:    resp.Metadata,
			StatusCode: resp.StatusCode,
		}, etag, nil
	}

	// Otherwise, parse the response as a devLXD response.
	return devLXDParseResponse(resp)
}

// query sends a query to the devLXD and returns the response.
func (r *ProtocolDevLXD) query(method string, path string, data any, ETag string) (devLXDResp *api.DevLXDResponse, etag string, err error) {
	// Generate the URL
	urlString := r.httpBaseURL.String() + "/" + version.APIVersion
	if path != "" {
		urlString += path
	}

	url, err := url.Parse(urlString)
	if err != nil {
		return nil, "", err
	}

	url.RawQuery = url.Query().Encode()

	// Run the actual query
	return r.rawQuery(method, url.String(), data, ETag)
}

// queryStruct sends a query to the devLXD, then converts the response content into the specified target struct.
// The function returns the etag of the response, and handles any errors during this process.
func (r *ProtocolDevLXD) queryStruct(method string, urlPath string, data any, ETag string, target any) (etag string, err error) {
	resp, etag, err := r.query(method, urlPath, data, ETag)
	if err != nil {
		return "", err
	}

	err = resp.ContentAsStruct(&target)
	if err != nil {
		return "", err
	}

	return etag, nil
}

// queryOperation sends a query to the devLXD, then converts the response content into the Operation.
func (r *ProtocolDevLXD) queryOperation(method string, path string, data any, ETag string) (op DevLXDOperation, etag string, err error) {
	var apiOp api.DevLXDOperation

	// Send the query.
	etag, err = r.queryStruct(method, path, data, ETag, &apiOp)
	if err != nil {
		return nil, "", err
	}

	// Setup an Operation wrapper.
	op = &devLXDOperation{
		DevLXDOperation: apiOp,
		r:               r,
	}

	return op, etag, nil
}

// RawWebsocket allows connection to LXD API websockets over the devLXD.
// It generates a websocket URL based on the provided path and the base URL of the ProtocolDevLXD receiver.
// It then leverages the rawWebsocket method to establish and return a websocket connection to the generated URL.
//
// This should only be used by internal LXD tools.
func (r *ProtocolDevLXD) RawWebsocket(path string) (*websocket.Conn, error) {
	// Generate the URL
	url := r.httpBaseURL.Host + "/1.0" + path
	if r.httpBaseURL.Scheme == "https" {
		return r.rawWebsocket("wss://" + url)
	}

	return r.rawWebsocket("ws://" + url)
}

// rawWebsocket creates a websocket connection to the provided URL using the underlying HTTP transport of
// the ProtocolDevLXD receiver. It sets up the request headers, manages the connection handshake, sets TCP
// timeouts, and handles any errors that may occur during these operations.
func (r *ProtocolDevLXD) rawWebsocket(url string) (*websocket.Conn, error) {
	var httpTransport *http.Transport

	switch t := r.http.Transport.(type) {
	case *http.Transport:
		httpTransport = t
	case HTTPTransporter:
		httpTransport = t.Transport()
	default:
		return nil, fmt.Errorf("Unexpected http.Transport type, %T", r)
	}

	// Setup a new websocket dialer based on it
	dialer := websocket.Dialer{
		NetDialContext:   httpTransport.DialContext,
		TLSClientConfig:  httpTransport.TLSClientConfig,
		Proxy:            httpTransport.Proxy,
		HandshakeTimeout: time.Second * 5,
	}

	// Create client headersfor the websocket request.
	headers := http.Header{}
	headers.Set("User-Agent", r.httpUserAgent)

	// Establish the connection.
	conn, resp, err := dialer.Dial(url, headers)
	if err != nil {
		if resp != nil {
			_, _, err = devLXDParseResponse(resp)
		}

		return nil, err
	}

	// Set TCP timeout options.
	remoteTCP, err := tcp.ExtractConn(conn.NetConn())
	if err == nil && remoteTCP != nil {
		err = tcp.SetTimeouts(remoteTCP, 0)
		if err != nil {
			logger.Warn("Failed setting TCP timeouts on remote connection", logger.Ctx{"err": err})
		}
	}

	// Log the data.
	logger.Debugf("Connected to the websocket: %v", url)

	return conn, nil
}

// setURLQueryAttributes modifies the supplied URL's query string with the client's current target.
func (r *ProtocolDevLXD) setURLQueryAttributes(url *url.URL) {
	values := url.Query()
	if r.clusterTarget != "" && values.Get("target") == "" {
		values.Set("target", r.clusterTarget)
	}

	url.RawQuery = values.Encode()
}

// devLXDParseResponse processes the HTTP response from the devLXD. It reads the response body,
// checks the status code, and returns a DevLXDResponse struct containing the content and status code.
// If the response is not successful, it returns an error instead.
func devLXDParseResponse(resp *http.Response) (*api.DevLXDResponse, string, error) {
	var content []byte
	var err error

	// Get the ETag
	etag := resp.Header.Get("ETag")

	// Read response body.
	content, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("Failed to read response body from %q: %v", resp.Request.URL.String(), err)
	}

	// Handel error response.
	if resp.StatusCode != http.StatusOK {
		if len(content) == 0 {
			return nil, "", api.NewGenericStatusError(resp.StatusCode)
		}

		return nil, "", api.NewStatusError(resp.StatusCode, string(content))
	}

	return &api.DevLXDResponse{
		Content:    content,
		StatusCode: resp.StatusCode,
	}, etag, nil
}
