package lxd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/tcp"
)

// ProtocolLXD represents a LXD API server.
type ProtocolLXD struct {
	ctx                context.Context
	server             *api.Server
	ctxConnected       context.Context
	ctxConnectedCancel context.CancelFunc

	// eventListenersLock is used to synchronize access to the event listeners.
	eventListenerManager *eventListenerManager

	http            *http.Client
	httpCertificate string
	httpBaseURL     neturl.URL
	httpUnixPath    string
	httpProtocol    string
	httpUserAgent   string

	requireAuthenticated bool

	clusterTarget string
	project       string

	oidcClient *oidcClient
}

// Disconnect gets rid of any background goroutines.
func (r *ProtocolLXD) Disconnect() {
	if r.ctxConnected.Err() != nil {
		r.ctxConnectedCancel()
	}
}

// GetConnectionInfo returns the basic connection information used to interact with the server.
func (r *ProtocolLXD) GetConnectionInfo() (*ConnectionInfo, error) {
	info := ConnectionInfo{}
	info.Certificate = r.httpCertificate
	info.Protocol = "lxd"
	info.URL = r.httpBaseURL.String()
	info.SocketPath = r.httpUnixPath

	info.Project = r.project
	if info.Project == "" {
		info.Project = "default"
	}

	info.Target = r.clusterTarget
	if info.Target == "" && r.server != nil {
		info.Target = r.server.Environment.ServerName
	}

	urls := []string{}
	if r.httpProtocol == "https" {
		urls = append(urls, r.httpBaseURL.String())
	}

	if r.server != nil && len(r.server.Environment.Addresses) > 0 {
		for _, addr := range r.server.Environment.Addresses {
			if strings.HasPrefix(addr, ":") {
				continue
			}

			url := "https://" + addr
			if !slices.Contains(urls, url) {
				urls = append(urls, url)
			}
		}
	}

	info.Addresses = urls

	return &info, nil
}

// isSameServer compares the calling ProtocolLXD object with the provided server object to check if they are the same server.
// It verifies the equality based on their connection information (Protocol, Certificate, Project, and Target).
func (r *ProtocolLXD) isSameServer(server Server) bool {
	// Short path checking if the two structs are identical.
	if r == server {
		return true
	}

	// Short path if either of the structs are nil.
	if r == nil || server == nil {
		return false
	}

	// When dealing with uninitialized servers, we can't safely compare.
	if r.server == nil {
		return false
	}

	// Get the connection info from both servers.
	srcInfo, err := r.GetConnectionInfo()
	if err != nil {
		return false
	}

	dstInfo, err := server.GetConnectionInfo()
	if err != nil {
		return false
	}

	// Check whether we're dealing with the same server.
	return srcInfo.Protocol == dstInfo.Protocol && srcInfo.Certificate == dstInfo.Certificate &&
		srcInfo.Project == dstInfo.Project && srcInfo.Target == dstInfo.Target
}

// GetHTTPClient returns the http client used for the connection. This can be used to set custom http options.
func (r *ProtocolLXD) GetHTTPClient() (*http.Client, error) {
	if r.http == nil {
		return nil, errors.New("HTTP client isn't set, bad connection")
	}

	return r.http, nil
}

// DoHTTP performs a Request, using OIDC authentication if set.
func (r *ProtocolLXD) DoHTTP(req *http.Request) (*http.Response, error) {
	r.addClientHeaders(req)

	if r.oidcClient != nil {
		var oidcScopesExtensionPresent bool
		err := r.CheckExtension("oidc_scopes")
		if err == nil {
			oidcScopesExtensionPresent = true
		}

		return r.oidcClient.do(req, oidcScopesExtensionPresent)
	}

	return r.http.Do(req)
}

// addClientHeaders sets headers from client settings.
// User-Agent (if r.httpUserAgent is set).
// X-LXD-authenticated (if r.requireAuthenticated is set).
// OIDC Authorization header (if r.oidcClient is set).
func (r *ProtocolLXD) addClientHeaders(req *http.Request) {
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	if r.requireAuthenticated {
		req.Header.Set("X-LXD-authenticated", "true")
	}

	if r.oidcClient != nil {
		req.Header.Set("Authorization", "Bearer "+r.oidcClient.getAccessToken())
	}
}

// RequireAuthenticated sets whether we expect to be authenticated with the server.
func (r *ProtocolLXD) RequireAuthenticated(authenticated bool) {
	r.requireAuthenticated = authenticated
}

// RawQuery allows directly querying the LXD API
//
// This should only be used by internal LXD tools.
func (r *ProtocolLXD) RawQuery(method string, path string, data any, ETag string) (*api.Response, string, error) {
	// Generate the URL
	url := r.httpBaseURL.String() + path

	return r.rawQuery(method, url, data, ETag)
}

// RawWebsocket allows directly connection to LXD API websockets
//
// This should only be used by internal LXD tools.
func (r *ProtocolLXD) RawWebsocket(path string) (*websocket.Conn, error) {
	return r.websocket(path)
}

// RawOperation allows direct querying of a LXD API endpoint returning
// background operations.
func (r *ProtocolLXD) RawOperation(method string, path string, data any, ETag string) (Operation, string, error) {
	return r.queryOperation(method, path, data, ETag, true)
}

// Internal functions.
func lxdParseResponse(resp *http.Response) (*api.Response, string, error) {
	// Get the ETag
	etag := resp.Header.Get("ETag")

	// Decode the response
	decoder := json.NewDecoder(resp.Body)
	response := api.Response{}

	err := decoder.Decode(&response)
	if err != nil {
		// Check the return value for a cleaner error
		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("Failed to fetch %s: %s", resp.Request.URL.String(), resp.Status)
		}

		return nil, "", err
	}

	// Handle errors
	if response.Type == api.ErrorResponse {
		return nil, "", api.NewStatusError(resp.StatusCode, response.Error)
	}

	return &response, etag, nil
}

// rawQuery is a method that sends an HTTP request to the LXD server with the provided method, URL, data, and ETag.
// It processes the request based on the data's type and handles the HTTP response, returning parsed results or an error if it occurs.
func (r *ProtocolLXD) rawQuery(method string, url string, data any, ETag string) (*api.Response, string, error) {
	// Log the request
	logger.Debug("Sending request to LXD", logger.Ctx{
		"method": method,
		"url":    url,
		"etag":   ETag,
	})

	// Setup new request.
	req, err := NewRequestWithContext(r.ctx, method, url, data, ETag)
	if err != nil {
		return nil, "", err
	}

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, "", err
	}

	defer func() {
		err := resp.Body.Close()
		if err != nil {
			logger.Debug("Failed to close response body", logger.Ctx{"err": err})
		}
	}()

	return lxdParseResponse(resp)
}

// setURLQueryAttributes modifies the supplied URL's query string with the client's current target and project.
func (r *ProtocolLXD) setURLQueryAttributes(apiURL *neturl.URL) {
	// Extract query fields and update for cluster targeting or project
	values := apiURL.Query()
	if r.clusterTarget != "" {
		if values.Get("target") == "" {
			values.Set("target", r.clusterTarget)
		}
	}

	if r.project != "" {
		if values.Get("project") == "" && values.Get("all-projects") == "" {
			values.Set("project", r.project)
		}
	}

	apiURL.RawQuery = values.Encode()
}

func (r *ProtocolLXD) setQueryAttributes(uri string) (string, error) {
	// Parse the full URI
	fields, err := neturl.Parse(uri)
	if err != nil {
		return "", err
	}

	r.setURLQueryAttributes(fields)

	return fields.String(), nil
}

func (r *ProtocolLXD) query(method string, path string, data any, ETag string) (*api.Response, string, error) {
	// Generate the URL
	url := r.httpBaseURL.String() + "/1.0" + path

	// Add project/target
	url, err := r.setQueryAttributes(url)
	if err != nil {
		return nil, "", err
	}

	// Run the actual query
	return r.rawQuery(method, url, data, ETag)
}

// queryStruct sends a query to the LXD server, then converts the response metadata into the specified target struct.
// The function logs the retrieved data, returns the etag of the response, and handles any errors during this process.
func (r *ProtocolLXD) queryStruct(method string, path string, data any, ETag string, target any) (string, error) {
	resp, etag, err := r.query(method, path, data, ETag)
	if err != nil {
		return "", err
	}

	err = resp.MetadataAsStruct(&target)
	if err != nil {
		return "", err
	}

	// Log the data
	logger.Debug("Got response struct from LXD")
	logger.Debug(logger.Pretty(target))

	return etag, nil
}

// queryOperation sends a query to the LXD server and then converts the response metadata into an Operation object.
// If useEventListener is true it will set up an early event listener and manage its lifecycle.
// If useEventListener is false, it will not set up an event listener and calls to Operation.Wait will use the operations API instead.
// In this case the returned Operation will error if the user calls Operation.AddHandler or Operation.RemoveHandler.
func (r *ProtocolLXD) queryOperation(method string, path string, data any, ETag string, useEventListener bool) (Operation, string, error) {
	// Attempt to setup an early event listener if requested.
	var listener *EventListener
	var err error

	if useEventListener {
		listener, err = r.GetEvents()

		if err != nil {
			logger.Debug("Failed to get events", logger.Ctx{"err": err})
		}
	}

	// Send the query
	resp, etag, err := r.query(method, path, data, ETag)
	if err != nil {
		if listener != nil {
			listener.Disconnect()
		}

		return nil, "", err
	}

	// Get to the operation
	respOperation, err := resp.MetadataAsOperation()
	if err != nil {
		if listener != nil {
			listener.Disconnect()
		}

		return nil, "", err
	}

	// Setup an Operation wrapper
	op := operation{
		Operation:    *respOperation,
		r:            r,
		listener:     listener,
		chActive:     make(chan bool),
		skipListener: !useEventListener,
	}

	// Log the data
	logger.Debug("Got operation from LXD")
	logger.Debug(logger.Pretty(op.Operation))

	return &op, etag, nil
}

// rawWebsocket creates a websocket connection to the provided URL using the underlying HTTP transport of the ProtocolLXD receiver.
// It sets up the request headers, manages the connection handshake, sets TCP timeouts, and handles any errors that may occur during these operations.
func (r *ProtocolLXD) rawWebsocket(url string) (*websocket.Conn, error) {
	// Grab the http transport handler
	httpTransport, err := r.getUnderlyingHTTPTransport()
	if err != nil {
		return nil, err
	}

	// Setup a new websocket dialer based on it
	dialer := websocket.Dialer{
		NetDialContext:   httpTransport.DialContext,
		TLSClientConfig:  httpTransport.TLSClientConfig,
		Proxy:            httpTransport.Proxy,
		HandshakeTimeout: time.Second * 5,
	}

	// Create temporary http.Request using the http url, not the ws one, so that we can add the client headers
	// for the websocket request.
	req := &http.Request{URL: &r.httpBaseURL, Header: http.Header{}}
	r.addClientHeaders(req)

	// Establish the connection
	conn, resp, err := dialer.Dial(url, req.Header)
	if err != nil {
		if resp != nil {
			_, _, err = lxdParseResponse(resp)
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

	// Log the data
	logger.Debugf("Connected to the websocket: %v", url)

	return conn, nil
}

// websocket generates a websocket URL based on the provided path and the base URL of the ProtocolLXD receiver.
// It then leverages the rawWebsocket method to establish and return a websocket connection to the generated URL.
func (r *ProtocolLXD) websocket(path string) (*websocket.Conn, error) {
	// Generate the URL
	url := r.httpBaseURL.Host + "/1.0" + path
	if r.httpBaseURL.Scheme == "https" {
		return r.rawWebsocket("wss://" + url)
	}

	return r.rawWebsocket("ws://" + url)
}

// WithContext returns a client that will add context.Context.
func (r *ProtocolLXD) WithContext(ctx context.Context) InstanceServer {
	rr := r
	rr.ctx = ctx
	return rr
}

// getUnderlyingHTTPTransport returns the *http.Transport used by the http client. If the http
// client was initialized with a HTTPTransporter, it returns the wrapped *http.Transport.
func (r *ProtocolLXD) getUnderlyingHTTPTransport() (*http.Transport, error) {
	switch t := r.http.Transport.(type) {
	case *http.Transport:
		return t, nil
	case HTTPTransporter:
		return t.Transport(), nil
	default:
		return nil, fmt.Errorf("Unexpected http.Transport type, %T", r)
	}
}

// getSourceImageConnectionInfo returns the connection information for the source image.
// The returned `info` is nil if the source image is local. In this process, the `instSrc`
// is also updated with the minimal source fields.
func (r *ProtocolLXD) getSourceImageConnectionInfo(source ImageServer, image api.Image, instSrc *api.InstanceSource) (info *ConnectionInfo, err error) {
	// Set the minimal source fields
	instSrc.Type = api.SourceTypeImage

	// Optimization for the local image case
	if r.isSameServer(source) {
		// Always use fingerprints for local case
		instSrc.Fingerprint = image.Fingerprint
		instSrc.Alias = ""
		return nil, nil
	}

	// Minimal source fields for remote image
	instSrc.Mode = "pull"

	// If we have an alias and the image is public, use that
	if instSrc.Alias != "" && image.Public {
		instSrc.Fingerprint = ""
	} else {
		instSrc.Fingerprint = image.Fingerprint
		instSrc.Alias = ""
	}

	// Get source server connection information
	info, err = source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	instSrc.Protocol = info.Protocol
	instSrc.Certificate = info.Certificate

	// Generate secret token if needed
	if !image.Public {
		secret, err := source.GetImageSecret(image.Fingerprint)
		if err != nil {
			return nil, err
		}

		instSrc.Secret = secret
	}

	return info, nil
}
