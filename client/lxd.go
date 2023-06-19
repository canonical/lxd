package lxd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/tcp"
)

// ProtocolLXD represents a LXD API server.
type ProtocolLXD struct {
	ctx                context.Context
	server             *api.Server
	ctxConnected       context.Context
	ctxConnectedCancel context.CancelFunc

	// eventConns contains event listener connections associated to a project name (or empty for all projects).
	eventConns map[string]*websocket.Conn

	// eventConnsLock controls write access to the eventConns.
	eventConnsLock sync.Mutex

	// eventListeners is a slice of event listeners associated to a project name (or empty for all projects).
	eventListeners     map[string][]*EventListener
	eventListenersLock sync.Mutex

	http            *http.Client
	httpCertificate string
	httpBaseURL     neturl.URL
	httpUnixPath    string
	httpProtocol    string
	httpUserAgent   string

	bakeryClient         *httpbakery.Client
	bakeryInteractor     []httpbakery.Interactor
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

			url := fmt.Sprintf("https://%s", addr)
			if !shared.StringInSlice(url, urls) {
				urls = append(urls, url)
			}
		}
	}

	info.Addresses = urls

	return &info, nil
}

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
		return nil, fmt.Errorf("HTTP client isn't set, bad connection")
	}

	return r.http, nil
}

// DoHTTP performs a Request, using macaroon authentication if set.
func (r *ProtocolLXD) DoHTTP(req *http.Request) (*http.Response, error) {
	r.addClientHeaders(req)

	// Send the request through
	if r.bakeryClient != nil {
		return r.bakeryClient.Do(req)
	}

	if r.oidcClient != nil {
		return r.oidcClient.do(req)
	}

	return r.http.Do(req)
}

// addClientHeaders sets headers from client settings.
// User-Agent (if r.httpUserAgent is set).
// X-LXD-authenticated (if r.requireAuthenticated is set).
// Bakery authentication header and cookie (if r.bakeryClient is set).
// OIDC Authorization header (if r.oidcClient is set).
func (r *ProtocolLXD) addClientHeaders(req *http.Request) {
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	if r.requireAuthenticated {
		req.Header.Set("X-LXD-authenticated", "true")
	}

	if r.bakeryClient != nil {
		req.Header.Set(httpbakery.BakeryProtocolHeader, fmt.Sprint(bakery.LatestVersion))

		for _, cookie := range r.http.Jar.Cookies(req.URL) {
			req.AddCookie(cookie)
		}
	}

	if r.oidcClient != nil {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", r.oidcClient.getAccessToken()))
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
	url := fmt.Sprintf("%s%s", r.httpBaseURL.String(), path)

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
	return r.queryOperation(method, path, data, ETag)
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
		return nil, "", api.StatusErrorf(resp.StatusCode, response.Error)
	}

	return &response, etag, nil
}

func (r *ProtocolLXD) rawQuery(method string, url string, data any, ETag string) (*api.Response, string, error) {
	var req *http.Request
	var err error

	// Log the request
	logger.Debug("Sending request to LXD", logger.Ctx{
		"method": method,
		"url":    url,
		"etag":   ETag,
	})

	// Get a new HTTP request setup
	if data != nil {
		switch data := data.(type) {
		case io.Reader:
			// Some data to be sent along with the request
			req, err = http.NewRequestWithContext(r.ctx, method, url, data)
			if err != nil {
				return nil, "", err
			}

			// Set the encoding accordingly
			req.Header.Set("Content-Type", "application/octet-stream")
		default:
			// Encode the provided data
			buf := bytes.Buffer{}
			err := json.NewEncoder(&buf).Encode(data)
			if err != nil {
				return nil, "", err
			}

			// Some data to be sent along with the request
			// Use a reader since the request body needs to be seekable
			req, err = http.NewRequestWithContext(r.ctx, method, url, bytes.NewReader(buf.Bytes()))
			if err != nil {
				return nil, "", err
			}

			// Set the encoding accordingly
			req.Header.Set("Content-Type", "application/json")

			// Log the data
			logger.Debugf(logger.Pretty(data))
		}
	} else {
		// No data to be sent along with the request
		req, err = http.NewRequestWithContext(r.ctx, method, url, nil)
		if err != nil {
			return nil, "", err
		}
	}

	// Set the ETag
	if ETag != "" {
		req.Header.Set("If-Match", ETag)
	}

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, "", err
	}

	defer func() { _ = resp.Body.Close() }()

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
	url := fmt.Sprintf("%s/1.0%s", r.httpBaseURL.String(), path)

	// Add project/target
	url, err := r.setQueryAttributes(url)
	if err != nil {
		return nil, "", err
	}

	// Run the actual query
	return r.rawQuery(method, url, data, ETag)
}

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
	logger.Debugf("Got response struct from LXD")
	logger.Debugf(logger.Pretty(target))

	return etag, nil
}

func (r *ProtocolLXD) queryOperation(method string, path string, data any, ETag string) (Operation, string, error) {
	// Attempt to setup an early event listener
	listener, err := r.GetEvents()
	if err != nil {
		listener = nil
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
		Operation: *respOperation,
		r:         r,
		listener:  listener,
		chActive:  make(chan bool),
	}

	// Log the data
	logger.Debugf("Got operation from LXD")
	logger.Debugf(logger.Pretty(op.Operation))

	return &op, etag, nil
}

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
	remoteTCP, _ := tcp.ExtractConn(conn.UnderlyingConn())
	if remoteTCP != nil {
		err = tcp.SetTimeouts(remoteTCP, 0)
		if err != nil {
			logger.Warn("Failed setting TCP timeouts on remote connection", logger.Ctx{"err": err})
		}
	}

	// Log the data
	logger.Debugf("Connected to the websocket: %v", url)

	return conn, nil
}

func (r *ProtocolLXD) websocket(path string) (*websocket.Conn, error) {
	// Generate the URL
	var url string
	if r.httpBaseURL.Scheme == "https" {
		url = fmt.Sprintf("wss://%s/1.0%s", r.httpBaseURL.Host, path)
	} else {
		url = fmt.Sprintf("ws://%s/1.0%s", r.httpBaseURL.Host, path)
	}

	return r.rawWebsocket(url)
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
