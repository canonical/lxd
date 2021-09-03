package vsock

import (
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mdlayher/vsock"

	"github.com/lxc/lxd/shared"
)

// Dial connects to a remote vsock.
func Dial(cid, port uint32) (net.Conn, error) {
	return vsock.Dial(cid, port)
}

// Listen listens for a connection.
func Listen(port uint32) (net.Listener, error) {
	return vsock.Listen(port)
}

// HTTPClient provides an HTTP client for using over vsock.
func HTTPClient(vsockID int, tlsClientCert string, tlsClientKey string, tlsServerCert string) (*http.Client, error) {
	client := &http.Client{}

	// Get the TLS configuration.
	tlsConfig, err := shared.GetTLSConfigMem(tlsClientCert, tlsClientKey, "", tlsServerCert, false)
	if err != nil {
		return nil, err
	}

	client.Transport = &http.Transport{
		TLSClientConfig: tlsConfig,
		// Setup a VM socket dialer.
		Dial: func(network, addr string) (net.Conn, error) {
			var conn net.Conn
			var err error

			// Retry for up to 1s at 100ms interval to handle various failures.
			for i := 0; i < 10; i++ {
				conn, err = Dial(uint32(vsockID), shared.HTTPSDefaultPort)
				if err == nil {
					break
				} else {
					// Handle some fatal errors.
					msg := err.Error()
					if strings.Contains(msg, "connection timed out") {
						// Retry once.
						conn, err = Dial(uint32(vsockID), shared.HTTPSDefaultPort)
						break
					} else if strings.Contains(msg, "connection refused") {
						break
					}

					// Retry the rest.
				}

				time.Sleep(100 * time.Millisecond)
			}
			if err != nil {
				return nil, err
			}

			tlsConn := tls.Client(conn, tlsConfig)

			// Validate the connection.
			err = tlsConn.Handshake()
			if err != nil {
				conn.Close()
				return nil, err
			}

			return tlsConn, nil
		},
		DisableKeepAlives: true,
	}

	// Setup redirect policy.
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Replicate the headers.
		req.Header = via[len(via)-1].Header

		return nil
	}

	return client, nil
}
