package vsock

import (
	"crypto/tls"
	"net"
	"net/http"

	"github.com/lxc/lxd/shared"
)

func vsockHTTPClient(vsockID int, tlsClientCert string, tlsClientKey string, tlsCA string, tlsServerCert string, insecureSkipVerify bool) (*http.Client, error) {
	client := &http.Client{}

	// Get the TLS configuration
	tlsConfig, err := shared.GetTLSConfigMem(tlsClientCert, tlsClientKey, tlsCA, tlsServerCert, insecureSkipVerify)
	if err != nil {
		return nil, err
	}

	client.Transport = &http.Transport{
		TLSClientConfig: tlsConfig,
		// Setup a VM socket dialer
		Dial: func(network, addr string) (net.Conn, error) {
			conn, err := dial(uint32(vsockID), 8443)
			if err != nil {
				return nil, err
			}

			tlsConn := tls.Client(conn, tlsConfig)

			// Validate the connection
			err = tlsConn.Handshake()
			if err != nil {
				conn.Close()
				return nil, err
			}

			return tlsConn, nil
		},
		DisableKeepAlives: true,
	}

	// Setup redirect policy
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Replicate the headers
		req.Header = via[len(via)-1].Header

		return nil
	}

	return client, nil
}
