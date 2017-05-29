package lxd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// ProtocolLXD represents a LXD API server
type ProtocolLXD struct {
	server *api.Server

	eventListeners     []*EventListener
	eventListenersLock sync.Mutex

	http            *http.Client
	httpCertificate string
	httpHost        string
	httpProtocol    string
	httpUserAgent   string
}

// GetConnectionInfo returns the basic connection information used to interact with the server
func (r *ProtocolLXD) GetConnectionInfo() (*ConnectionInfo, error) {
	info := ConnectionInfo{}
	info.Certificate = r.httpCertificate
	info.Protocol = "lxd"

	urls := []string{}
	if len(r.server.Environment.Addresses) > 0 {
		if r.httpProtocol == "https" {
			urls = append(urls, r.httpHost)
		}

		for _, addr := range r.server.Environment.Addresses {
			url := fmt.Sprintf("https://%s", addr)
			if !shared.StringInSlice(url, urls) {
				urls = append(urls, url)
			}
		}
	}
	info.Addresses = urls

	return &info, nil
}

// RawQuery allows directly querying the LXD API
//
// This should only be used by internal LXD tools.
func (r *ProtocolLXD) RawQuery(method string, path string, data interface{}, ETag string) (*api.Response, string, error) {
	// Generate the URL
	url := fmt.Sprintf("%s%s", r.httpHost, path)

	return r.rawQuery(method, url, data, ETag)
}

// RawWebsocket allows directly connection to LXD API websockets
//
// This should only be used by internal LXD tools.
func (r *ProtocolLXD) RawWebsocket(path string) (*websocket.Conn, error) {
	// Generate the URL
	url := fmt.Sprintf("%s%s", r.httpHost, path)

	return r.rawWebsocket(url)
}

// Internal functions
func (r *ProtocolLXD) rawQuery(method string, url string, data interface{}, ETag string) (*api.Response, string, error) {
	var req *http.Request
	var err error

	// Log the request
	logger.Info("Sending request to LXD",
		"method", method,
		"url", url,
		"etag", ETag,
	)

	// Get a new HTTP request setup
	if data != nil {
		// Encode the provided data
		buf := bytes.Buffer{}
		err := json.NewEncoder(&buf).Encode(data)
		if err != nil {
			return nil, "", err
		}

		// Some data to be sent along with the request
		req, err = http.NewRequest(method, url, &buf)
		if err != nil {
			return nil, "", err
		}

		// Set the encoding accordingly
		req.Header.Set("Content-Type", "application/json")

		// Log the data
		logger.Debugf(logger.Pretty(data))
	} else {
		// No data to be sent along with the request
		req, err = http.NewRequest(method, url, nil)
		if err != nil {
			return nil, "", err
		}
	}

	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Set the ETag
	if ETag != "" {
		req.Header.Set("If-Match", ETag)
	}

	// Send the request
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	// Get the ETag
	etag := resp.Header.Get("ETag")

	// Decode the response
	decoder := json.NewDecoder(resp.Body)
	response := api.Response{}

	err = decoder.Decode(&response)
	if err != nil {
		// Check the return value for a cleaner error
		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("Failed to fetch %s: %s", url, resp.Status)
		}

		return nil, "", err
	}

	// Handle errors
	if response.Type == api.ErrorResponse {
		return nil, "", fmt.Errorf(response.Error)
	}

	return &response, etag, nil
}

func (r *ProtocolLXD) query(method string, path string, data interface{}, ETag string) (*api.Response, string, error) {
	// Generate the URL
	url := fmt.Sprintf("%s/1.0%s", r.httpHost, path)

	return r.rawQuery(method, url, data, ETag)
}

func (r *ProtocolLXD) queryStruct(method string, path string, data interface{}, ETag string, target interface{}) (string, error) {
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

func (r *ProtocolLXD) queryOperation(method string, path string, data interface{}, ETag string) (*Operation, string, error) {
	// Send the query
	resp, etag, err := r.query(method, path, data, ETag)
	if err != nil {
		return nil, "", err
	}

	// Get to the operation
	respOperation, err := resp.MetadataAsOperation()
	if err != nil {
		return nil, "", err
	}

	// Setup an Operation wrapper
	op := Operation{
		Operation: *respOperation,
		r:         r,
		chActive:  make(chan bool),
	}

	// Log the data
	logger.Debugf("Got operation from LXD")
	logger.Debugf(logger.Pretty(op.Operation))

	return &op, etag, nil
}

func (r *ProtocolLXD) rawWebsocket(url string) (*websocket.Conn, error) {
	// Grab the http transport handler
	httpTransport := r.http.Transport.(*http.Transport)

	// Setup a new websocket dialer based on it
	dialer := websocket.Dialer{
		NetDial:         httpTransport.Dial,
		TLSClientConfig: httpTransport.TLSClientConfig,
		Proxy:           httpTransport.Proxy,
	}

	// Set the user agent
	headers := http.Header{}
	if r.httpUserAgent != "" {
		headers.Set("User-Agent", r.httpUserAgent)
	}

	// Establish the connection
	conn, _, err := dialer.Dial(url, headers)
	if err != nil {
		return nil, err
	}

	// Log the data
	logger.Debugf("Connected to the websocket")

	return conn, err
}

func (r *ProtocolLXD) websocket(path string) (*websocket.Conn, error) {
	// Generate the URL
	var url string
	if strings.HasPrefix(r.httpHost, "https://") {
		url = fmt.Sprintf("wss://%s/1.0%s", strings.TrimPrefix(r.httpHost, "https://"), path)
	} else {
		url = fmt.Sprintf("ws://%s/1.0%s", strings.TrimPrefix(r.httpHost, "http://"), path)
	}

	return r.rawWebsocket(url)
}
