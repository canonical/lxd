package vsock

import (
	"crypto/tls"
	"net"
	"net/http"

	"github.com/lxc/lxd/shared"
)

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
			conn, err := dial(uint32(vsockID), 8443)
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
