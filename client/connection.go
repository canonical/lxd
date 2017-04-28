package lxd

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"

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

	// User agent string
	UserAgent string

	// Custom proxy
	Proxy func(*http.Request) (*url.URL, error)
}

// ConnectLXD lets you connect to a remote LXD daemon over HTTPs.
//
// A client certificate (TLSClientCert) and key (TLSClientKey) must be provided.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectLXD(url string, args *ConnectionArgs) (ContainerServer, error) {
	logger.Infof("Connecting to a remote LXD over HTTPs")

	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	// Initialize the client struct
	server := ProtocolLXD{
		httpHost:        url,
		httpUserAgent:   args.UserAgent,
		httpCertificate: args.TLSServerCert,
	}

	// Setup the HTTP client
	httpClient, err := tlsHTTPClient(args.TLSClientCert, args.TLSClientKey, args.TLSServerCert, args.Proxy)
	if err != nil {
		return nil, err
	}
	server.http = httpClient

	// Test the connection and seed the server information
	_, _, err = server.GetServer()
	if err != nil {
		return nil, err
	}

	return &server, nil
}

// ConnectLXDUnix lets you connect to a remote LXD daemon over a local unix socket.
//
// If the path argument is empty, then $LXD_DIR/unix.socket will be used.
// If that one isn't set either, then the path will default to /var/lib/lxd/unix.socket.
func ConnectLXDUnix(path string, args *ConnectionArgs) (ContainerServer, error) {
	logger.Infof("Connecting to a local LXD over a Unix socket")

	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	// Initialize the client struct
	server := ProtocolLXD{
		httpHost:      "http://unix.socket",
		httpUserAgent: args.UserAgent,
	}

	// Determine the socket path
	if path == "" {
		lxdDir := os.Getenv("LXD_DIR")
		if lxdDir == "" {
			lxdDir = "/var/lib/lxd"
		}

		path = filepath.Join(lxdDir, "unix.socket")
	}

	// Setup the HTTP client
	httpClient, err := unixHTTPClient(path)
	if err != nil {
		return nil, err
	}
	server.http = httpClient

	// Test the connection and seed the server information
	serverStatus, _, err := server.GetServer()
	if err != nil {
		return nil, err
	}

	// Record the server certificate
	server.httpCertificate = serverStatus.Environment.Certificate

	return &server, nil
}

// ConnectPublicLXD lets you connect to a remote public LXD daemon over HTTPs.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectPublicLXD(url string, args *ConnectionArgs) (ImageServer, error) {
	logger.Infof("Connecting to a remote public LXD over HTTPs")

	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	// Initialize the client struct
	server := ProtocolLXD{
		httpHost:        url,
		httpUserAgent:   args.UserAgent,
		httpCertificate: args.TLSServerCert,
	}

	// Setup the HTTP client
	httpClient, err := tlsHTTPClient(args.TLSClientCert, args.TLSClientKey, args.TLSServerCert, args.Proxy)
	if err != nil {
		return nil, err
	}
	server.http = httpClient

	// Test the connection and seed the server information
	_, _, err = server.GetServer()
	if err != nil {
		return nil, err
	}

	return &server, nil
}

// ConnectSimpleStreams lets you connect to a remote SimpleStreams image server over HTTPs.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectSimpleStreams(url string, args *ConnectionArgs) (ImageServer, error) {
	logger.Infof("Connecting to a remote simplestreams server")

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
	httpClient, err := tlsHTTPClient(args.TLSClientCert, args.TLSClientKey, args.TLSServerCert, args.Proxy)
	if err != nil {
		return nil, err
	}
	server.http = httpClient

	// Get simplestreams client
	ssClient := simplestreams.NewClient(url, *httpClient, args.UserAgent)
	server.ssClient = ssClient

	return &server, nil
}
