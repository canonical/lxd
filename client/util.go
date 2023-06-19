package lxd

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lxc/lxd/shared"
)

func tlsHTTPClient(client *http.Client, tlsClientCert string, tlsClientKey string, tlsCA string, tlsServerCert string, insecureSkipVerify bool, proxy func(req *http.Request) (*url.URL, error), transportWrapper func(t *http.Transport) HTTPTransporter) (*http.Client, error) {
	// Get the TLS configuration
	tlsConfig, err := shared.GetTLSConfigMem(tlsClientCert, tlsClientKey, tlsCA, tlsServerCert, insecureSkipVerify)
	if err != nil {
		return nil, err
	}

	// Define the http transport
	transport := &http.Transport{
		TLSClientConfig:       tlsConfig,
		Proxy:                 shared.ProxyFromEnvironment,
		DisableKeepAlives:     true,
		ExpectContinueTimeout: time.Second * 30,
		ResponseHeaderTimeout: time.Second * 3600,
		TLSHandshakeTimeout:   time.Second * 5,
	}

	// Allow overriding the proxy
	if proxy != nil {
		transport.Proxy = proxy
	}

	// Special TLS handling
	transport.DialTLSContext = func(ctx context.Context, network string, addr string) (net.Conn, error) {
		tlsDial := func(network string, addr string, config *tls.Config, resetName bool) (net.Conn, error) {
			conn, err := shared.RFC3493Dialer(ctx, network, addr)
			if err != nil {
				return nil, err
			}

			// Setup TLS
			if resetName {
				hostName, _, err := net.SplitHostPort(addr)
				if err != nil {
					hostName = addr
				}

				config = config.Clone()
				config.ServerName = hostName
			}

			tlsConn := tls.Client(conn, config)

			// Validate the connection
			err = tlsConn.Handshake()
			if err != nil {
				_ = conn.Close()
				return nil, err
			}

			if !config.InsecureSkipVerify {
				err := tlsConn.VerifyHostname(config.ServerName)
				if err != nil {
					_ = conn.Close()
					return nil, err
				}
			}

			return tlsConn, nil
		}

		conn, err := tlsDial(network, addr, transport.TLSClientConfig, false)
		if err != nil {
			// We may have gotten redirected to a non-LXD machine
			return tlsDial(network, addr, transport.TLSClientConfig, true)
		}

		return conn, nil
	}

	// Define the http client
	if client == nil {
		client = &http.Client{}
	}

	if transportWrapper != nil {
		client.Transport = transportWrapper(transport)
	} else {
		client.Transport = transport
	}

	// Setup redirect policy
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Replicate the headers
		req.Header = via[len(via)-1].Header

		return nil
	}

	return client, nil
}

func unixHTTPClient(args *ConnectionArgs, path string) (*http.Client, error) {
	// Setup a Unix socket dialer
	unixDial := func(_ context.Context, network, addr string) (net.Conn, error) {
		raddr, err := net.ResolveUnixAddr("unix", path)
		if err != nil {
			return nil, err
		}

		return net.DialUnix("unix", nil, raddr)
	}

	if args == nil {
		args = &ConnectionArgs{}
	}

	// Define the http transport
	transport := &http.Transport{
		DialContext:           unixDial,
		DisableKeepAlives:     true,
		Proxy:                 args.Proxy,
		ExpectContinueTimeout: time.Second * 30,
		ResponseHeaderTimeout: time.Second * 3600,
		TLSHandshakeTimeout:   time.Second * 5,
	}

	// Define the http client
	client := args.HTTPClient
	if client == nil {
		client = &http.Client{}
	}

	client.Transport = transport

	// Setup redirect policy
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Replicate the headers
		req.Header = via[len(via)-1].Header

		return nil
	}

	return client, nil
}

// remoteOperationResult used for storing the error that occurred for a particular remote URL.
type remoteOperationResult struct {
	URL   string
	Error error
}

func remoteOperationError(msg string, errors []remoteOperationResult) error {
	// Check if empty
	if len(errors) == 0 {
		return nil
	}

	// Check if all identical
	var err error
	for _, entry := range errors {
		if err != nil && entry.Error.Error() != err.Error() {
			errorStrs := make([]string, 0, len(errors))
			for _, error := range errors {
				errorStrs = append(errorStrs, fmt.Sprintf("%s: %v", error.URL, error.Error))
			}

			return fmt.Errorf("%s:\n - %s", msg, strings.Join(errorStrs, "\n - "))
		}

		err = entry.Error
	}

	// Check if successful
	if err != nil {
		return fmt.Errorf("%s: %w", msg, err)
	}

	return nil
}

// Set the value of a query parameter in the given URI.
func setQueryParam(uri, param, value string) (string, error) {
	fields, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	values := fields.Query()
	values.Set(param, url.QueryEscape(value))

	fields.RawQuery = values.Encode()

	return fields.String(), nil
}

// urlsToResourceNames returns a list of resource names extracted from one or more URLs of the same resource type.
// The resource type path prefix to match is provided by the matchPathPrefix argument.
func urlsToResourceNames(matchPathPrefix string, urls ...string) ([]string, error) {
	resourceNames := make([]string, 0, len(urls))

	for _, urlRaw := range urls {
		u, err := url.Parse(urlRaw)
		if err != nil {
			return nil, fmt.Errorf("Failed parsing URL %q: %w", urlRaw, err)
		}

		_, after, found := strings.Cut(u.Path, fmt.Sprintf("%s/", matchPathPrefix))
		if !found {
			return nil, fmt.Errorf("Unexpected URL path %q", u)
		}

		resourceNames = append(resourceNames, after)
	}

	return resourceNames, nil
}

// parseFilters translates filters passed at client side to form acceptable by server-side API.
func parseFilters(filters []string) string {
	var result []string
	for _, filter := range filters {
		if strings.Contains(filter, "=") {
			membs := strings.SplitN(filter, "=", 2)
			result = append(result, fmt.Sprintf("%s eq %s", membs[0], membs[1]))
		}
	}
	return strings.Join(result, " and ")
}

// HTTPTransporter represents a wrapper around *http.Transport.
// It is used to add some pre and postprocessing logic to http requests / responses.
type HTTPTransporter interface {
	http.RoundTripper

	// Transport what this struct wraps
	Transport() *http.Transport
}
