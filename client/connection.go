package lxd

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/simplestreams"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
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
}

// ConnectLXD lets you connect to a remote LXD daemon over HTTPs.
//
// A client certificate (TLSClientCert) and key (TLSClientKey) must be provided.
//
// If connecting to a LXD daemon running in PKI mode, the PKI CA (TLSCA) must also be provided.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectLXD(url string, args *ConnectionArgs) (ContainerServer, error) {
	logger.Debugf("Connecting to a remote LXD over HTTPs")

	return httpsLXD(url, args)
}

// ConnectLXDUnix lets you connect to a remote LXD daemon over a local unix socket.
//
// If the path argument is empty, then $LXD_SOCKET will be used, if
// unset $LXD_DIR/unix.socket will be used and if that one isn't set
// either, then the path will default to /var/lib/lxd/unix.socket.
func ConnectLXDUnix(path string, args *ConnectionArgs) (ContainerServer, error) {
	logger.Debugf("Connecting to a local LXD over a Unix socket")

	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	// Initialize the client struct
	server := ProtocolLXD{
		httpHost:      "http://unix.socket",
		httpProtocol:  "unix",
		httpUserAgent: args.UserAgent,
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

	return httpsLXD(url, args)
}

// ConnectSimpleStreams lets you connect to a remote SimpleStreams image server over HTTPs.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectSimpleStreams(url string, args *ConnectionArgs) (ImageServer, error) {
	logger.Debugf("Connecting to a remote simplestreams server")

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

	return &server, nil
}

// Internal function called by ConnectLXD and ConnectPublicLXD
func httpsLXD(url string, args *ConnectionArgs) (ContainerServer, error) {
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
