package lxd

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/macaroon-bakery.v2/httpbakery"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/simplestreams"
)

// ConnectionArgs represents a set of common connection properties
type ConnectionArgs struct {
	// TLS certificate of the remote server. If not specified, the system CA is used.
	TLSServerCert string

	// TLS certificate to use for client authentication.
	TLSClientCert string

	// TLS key to use for client authentication.
	TLSClientKey string

	// TLS CA to validate against when in PKI mode.
	TLSCA string

	// User agent string
	UserAgent string

	// Authentication type
	AuthType string

	// Authentication interactor
	AuthInteractor []httpbakery.Interactor

	// Custom proxy
	Proxy func(*http.Request) (*url.URL, error)

	// Custom HTTP Client (used as base for the connection)
	HTTPClient *http.Client

	// Controls whether a client verifies the server's certificate chain and host name.
	InsecureSkipVerify bool

	// Cookie jar
	CookieJar http.CookieJar

	// Skip automatic GetServer request upon connection
	SkipGetServer bool

	// Caching support for image servers
	CachePath   string
	CacheExpiry time.Duration
}

// ConnectLXD lets you connect to a remote LXD daemon over HTTPs.
//
// A client certificate (TLSClientCert) and key (TLSClientKey) must be provided.
//
// If connecting to a LXD daemon running in PKI mode, the PKI CA (TLSCA) must also be provided.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectLXD(url string, args *ConnectionArgs) (InstanceServer, error) {
	logger.Debugf("Connecting to a remote LXD over HTTPs")

	// Cleanup URL
	url = strings.TrimSuffix(url, "/")

	return httpsLXD(url, args)
}

// ConnectLXDHTTP lets you connect to a VM agent over a VM socket.
func ConnectLXDHTTP(args *ConnectionArgs, client *http.Client) (InstanceServer, error) {
	logger.Debugf("Connecting to a VM agent over a VM socket")

	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	// Initialize the client struct
	server := ProtocolLXD{
		httpHost:      "https://custom.socket",
		httpProtocol:  "custom",
		httpUserAgent: args.UserAgent,
		chConnected:   make(chan struct{}, 1),
	}

	// Setup the HTTP client
	server.http = client

	// Test the connection and seed the server information
	if !args.SkipGetServer {
		serverStatus, _, err := server.GetServer()
		if err != nil {
			return nil, err
		}

		// Record the server certificate
		server.httpCertificate = serverStatus.Environment.Certificate
	}

	return &server, nil
}

// ConnectLXDUnix lets you connect to a remote LXD daemon over a local unix socket.
//
// If the path argument is empty, then $LXD_SOCKET will be used, if
// unset $LXD_DIR/unix.socket will be used and if that one isn't set
// either, then the path will default to /var/lib/lxd/unix.socket.
func ConnectLXDUnix(path string, args *ConnectionArgs) (InstanceServer, error) {
	logger.Debugf("Connecting to a local LXD over a Unix socket")

	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	// Initialize the client struct
	server := ProtocolLXD{
		httpHost:      "http://unix.socket",
		httpUnixPath:  path,
		httpProtocol:  "unix",
		httpUserAgent: args.UserAgent,
		chConnected:   make(chan struct{}, 1),
	}

	// Determine the socket path
	if path == "" {
		path = os.Getenv("LXD_SOCKET")
		if path == "" {
			lxdDir := os.Getenv("LXD_DIR")
			if lxdDir == "" {
				lxdDir = "/var/lib/lxd"
			}

			path = filepath.Join(lxdDir, "unix.socket")
		}
	}

	path = shared.HostPath(path)

	// Setup the HTTP client
	httpClient, err := unixHTTPClient(args.HTTPClient, path)
	if err != nil {
		return nil, err
	}
	server.http = httpClient

	// Test the connection and seed the server information
	if !args.SkipGetServer {
		serverStatus, _, err := server.GetServer()
		if err != nil {
			return nil, err
		}

		// Record the server certificate
		server.httpCertificate = serverStatus.Environment.Certificate
	}

	return &server, nil
}

// ConnectPublicLXD lets you connect to a remote public LXD daemon over HTTPs.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectPublicLXD(url string, args *ConnectionArgs) (ImageServer, error) {
	logger.Debugf("Connecting to a remote public LXD over HTTPs")

	// Cleanup URL
	url = strings.TrimSuffix(url, "/")

	return httpsLXD(url, args)
}

// ConnectSimpleStreams lets you connect to a remote SimpleStreams image server over HTTPs.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectSimpleStreams(url string, args *ConnectionArgs) (ImageServer, error) {
	logger.Debugf("Connecting to a remote simplestreams server")

	// Cleanup URL
	url = strings.TrimSuffix(url, "/")

	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	// Initialize the client struct
	server := ProtocolSimpleStreams{
		httpHost:        url,
		httpUserAgent:   args.UserAgent,
		httpCertificate: args.TLSServerCert,
	}

	// Setup the HTTP client
	httpClient, err := tlsHTTPClient(args.HTTPClient, args.TLSClientCert, args.TLSClientKey, args.TLSCA, args.TLSServerCert, args.InsecureSkipVerify, args.Proxy)
	if err != nil {
		return nil, err
	}
	server.http = httpClient

	// Get simplestreams client
	ssClient := simplestreams.NewClient(url, *httpClient, args.UserAgent)
	server.ssClient = ssClient

	// Setup the cache
	if args.CachePath != "" {
		if !shared.PathExists(args.CachePath) {
			return nil, fmt.Errorf("Cache directory '%s' doesn't exist", args.CachePath)
		}

		hashedURL := fmt.Sprintf("%x", sha256.Sum256([]byte(url)))

		cachePath := filepath.Join(args.CachePath, hashedURL)
		cacheExpiry := args.CacheExpiry
		if cacheExpiry == 0 {
			cacheExpiry = time.Hour
		}

		if !shared.PathExists(cachePath) {
			err := os.Mkdir(cachePath, 0755)
			if err != nil {
				return nil, err
			}
		}

		ssClient.SetCache(cachePath, cacheExpiry)
	}

	return &server, nil
}

// Internal function called by ConnectLXD and ConnectPublicLXD
func httpsLXD(url string, args *ConnectionArgs) (InstanceServer, error) {
	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	// Initialize the client struct
	server := ProtocolLXD{
		httpCertificate:  args.TLSServerCert,
		httpHost:         url,
		httpProtocol:     "https",
		httpUserAgent:    args.UserAgent,
		bakeryInteractor: args.AuthInteractor,
		chConnected:      make(chan struct{}, 1),
	}

	if args.AuthType == "candid" {
		server.RequireAuthenticated(true)
	}

	// Setup the HTTP client
	httpClient, err := tlsHTTPClient(args.HTTPClient, args.TLSClientCert, args.TLSClientKey, args.TLSCA, args.TLSServerCert, args.InsecureSkipVerify, args.Proxy)
	if err != nil {
		return nil, err
	}

	if args.CookieJar != nil {
		httpClient.Jar = args.CookieJar
	}

	server.http = httpClient
	if args.AuthType == "candid" {
		server.setupBakeryClient()
	}

	// Test the connection and seed the server information
	if !args.SkipGetServer {
		_, _, err := server.GetServer()
		if err != nil {
			return nil, err
		}
	}
	return &server, nil
}
