package lxd

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// tlsHTTPClient creates an HTTP client with a specified Transport Layer Security (TLS) configuration.
// It takes in parameters for client certificates, keys, Certificate Authority, server certificates,
// a boolean for skipping verification, a boolean for including only legacy curves in ClientHello, a
// proxy function, and a transport wrapper function.
// It returns the HTTP client with the provided configurations and handles any errors that might occur during the setup process.
func tlsHTTPClient(client *http.Client, tlsClientCert string, tlsClientKey string, tlsCA string, tlsServerCert string, insecureSkipVerify bool, legacyCurvesOnly bool, proxy func(req *http.Request) (*url.URL, error), transportWrapper func(t *http.Transport) HTTPTransporter) (*http.Client, error) {
	// Get the TLS configuration
	tlsConfig, err := shared.GetTLSConfigMem(tlsClientCert, tlsClientKey, tlsCA, tlsServerCert, insecureSkipVerify)
	if err != nil {
		return nil, err
	}

	// If legacyCurvesOnly is true, don't include the post-quantum curve
	// (`X25519MLKEM768` or `X25519Kyber768Draft00`) in the list of supported
	// curves. Those extra curves causes the ClientHello message to be too
	// large to fit in a single packet, causing some broken middleboxes to reset
	// the connection because they failed to reassemble the TCP packets.
	if legacyCurvesOnly {
		// Matches the default list of curves in Go 1.23 with `GODEBUG=tlskyber=0,tlsmlkem=0`
		tlsConfig.CurvePreferences = []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
			tls.CurveP384,
			tls.CurveP521,
		}
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

// unixHTTPClient creates an HTTP client that communicates over a Unix socket.
// It takes in the connection arguments and the Unix socket path as parameters.
// The function sets up a Unix socket dialer, configures the HTTP transport, and returns the HTTP client with the specified configurations.
// Any errors encountered during the setup process are also handled by the function.
func unixHTTPClient(args *ConnectionArgs, path string, transportWrapper func(t *http.Transport) HTTPTransporter) (*http.Client, error) {
	// Setup a Unix socket dialer
	unixDial := func(_ context.Context, _ string, _ string) (net.Conn, error) {
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

		_, after, found := strings.Cut(u.Path, matchPathPrefix+"/")
		if !found {
			return nil, fmt.Errorf("Unexpected URL path %q", u)
		}

		resourceNames = append(resourceNames, after)
	}

	return resourceNames, nil
}

// urlsToResourceNames returns a map of project name to list of resource names, where the resource name is extracted from
// the final element of each given URL.
func urlsToResourceNamesAllProjects(matchPathPrefix string, urls ...string) (map[string][]string, error) {
	resourceNames := make(map[string][]string)
	for _, urlRaw := range urls {
		u, err := url.Parse(urlRaw)
		if err != nil {
			return nil, fmt.Errorf("Failed parsing URL %q: %w", urlRaw, err)
		}

		_, after, found := strings.Cut(u.Path, matchPathPrefix+"/")
		if !found {
			return nil, fmt.Errorf("Unexpected URL path %q", u)
		}

		project := u.Query().Get("project")
		if project == "" {
			project = api.ProjectDefaultName
		}

		resourceNames[project] = append(resourceNames[project], after)
	}

	return resourceNames, nil
}

// parseFilters translates filters passed at client side to form acceptable by server-side API.
func parseFilters(filters []string) string {
	var result []string
	for _, filter := range filters {
		if strings.Contains(filter, "=") {
			before, after, _ := strings.Cut(filter, "=")
			result = append(result, before+" eq "+after)
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

func openBrowser(url string) error {
	var err error

	browser := os.Getenv("BROWSER")
	if browser != "" {
		if browser == "none" {
			return nil
		}

		err = exec.Command(browser, url).Start()
		return err
	}

	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}

	if err != nil {
		return err
	}

	return nil
}
