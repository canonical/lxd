package lxd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/cancel"
	"github.com/lxc/lxd/shared/ioprogress"
)

func tlsHTTPClient(client *http.Client, tlsClientCert string, tlsClientKey string, tlsCA string, tlsServerCert string, insecureSkipVerify bool, proxy func(req *http.Request) (*url.URL, error)) (*http.Client, error) {
	// Get the TLS configuration
	tlsConfig, err := shared.GetTLSConfigMem(tlsClientCert, tlsClientKey, tlsCA, tlsServerCert, insecureSkipVerify)
	if err != nil {
		return nil, err
	}

	// Define the http transport
	transport := &http.Transport{
		TLSClientConfig:   tlsConfig,
		Dial:              shared.RFC3493Dialer,
		Proxy:             shared.ProxyFromEnvironment,
		DisableKeepAlives: true,
	}

	// Allow overriding the proxy
	if proxy != nil {
		transport.Proxy = proxy
	}

	// Define the http client
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

func unixHTTPClient(client *http.Client, path string) (*http.Client, error) {
	// Setup a Unix socket dialer
	unixDial := func(network, addr string) (net.Conn, error) {
		raddr, err := net.ResolveUnixAddr("unix", path)
		if err != nil {
			return nil, err
		}

		return net.DialUnix("unix", nil, raddr)
	}

	// Define the http transport
	transport := &http.Transport{
		Dial:              unixDial,
		DisableKeepAlives: true,
	}

	// Define the http client
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

func downloadFileSha256(httpClient *http.Client, useragent string, progress func(progress ProgressData), canceler *cancel.Canceler, filename string, url string, hash string, target io.WriteSeeker) (int64, error) {
	// Always seek to the beginning
	target.Seek(0, 0)

	// Prepare the download request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return -1, err
	}

	if useragent != "" {
		req.Header.Set("User-Agent", useragent)
	}

	// Perform the request
	r, doneCh, err := cancel.CancelableDownload(canceler, httpClient, req)
	if err != nil {
		return -1, err
	}
	defer r.Body.Close()
	defer close(doneCh)

	if r.StatusCode != http.StatusOK {
		return -1, fmt.Errorf("Unable to fetch %s: %s", url, r.Status)
	}

	// Handle the data
	body := r.Body
	if progress != nil {
		body = &ioprogress.ProgressReader{
			ReadCloser: r.Body,
			Tracker: &ioprogress.ProgressTracker{
				Length: r.ContentLength,
				Handler: func(percent int64, speed int64) {
					if filename != "" {
						progress(ProgressData{Text: fmt.Sprintf("%s: %d%% (%s/s)", filename, percent, shared.GetByteSizeString(speed, 2))})
					} else {
						progress(ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, shared.GetByteSizeString(speed, 2))})
					}
				},
			},
		}
	}

	sha256 := sha256.New()
	size, err := io.Copy(io.MultiWriter(target, sha256), body)
	if err != nil {
		return -1, err
	}

	result := fmt.Sprintf("%x", sha256.Sum(nil))
	if result != hash {
		return -1, fmt.Errorf("Hash mismatch for %s: %s != %s", url, result, hash)
	}

	return size, nil
}

type nullReadWriteCloser int

func (nullReadWriteCloser) Close() error                { return nil }
func (nullReadWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nullReadWriteCloser) Read(p []byte) (int, error)  { return 0, io.EOF }
