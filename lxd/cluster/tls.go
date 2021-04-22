package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

// Return a TLS configuration suitable for establishing intra-member network connections using the server cert.
func tlsClientConfig(networkCert *shared.CertInfo, serverCert *shared.CertInfo) (*tls.Config, error) {
	if networkCert == nil {
		return nil, fmt.Errorf("Invalid networkCert")
	}

	if serverCert == nil {
		return nil, fmt.Errorf("Invalid serverCert")
	}

	keypair := serverCert.KeyPair()
	config := shared.InitTLSConfig()
	config.Certificates = []tls.Certificate{keypair}
	config.RootCAs = x509.NewCertPool()
	ca := serverCert.CA()
	if ca != nil {
		config.RootCAs.AddCert(ca)
	}

	// Since the same cluster keypair is used both as server and as client
	// cert, let's add it to the CA pool to make it trusted.
	networkKeypair := networkCert.KeyPair()
	netCert, err := x509.ParseCertificate(networkKeypair.Certificate[0])
	if err != nil {
		return nil, err
	}

	netCert.IsCA = true
	netCert.KeyUsage = x509.KeyUsageCertSign
	config.RootCAs.AddCert(netCert)

	// Always use network certificate's DNS name rather than server cert, so that it matches.
	if len(netCert.DNSNames) > 0 {
		config.ServerName = netCert.DNSNames[0]
	}

	return config, nil
}

// Return true if the given request is presenting the given cluster certificate.
func tlsCheckCert(r *http.Request, info *shared.CertInfo) bool {
	cert, err := x509.ParseCertificate(info.KeyPair().Certificate[0])
	if err != nil {
		// Since we have already loaded this certificate, typically
		// using LoadX509KeyPair, an error should never happen, but
		// check for good measure.
		panic(fmt.Sprintf("invalid keypair material: %v", err))
	}
	trustedCerts := map[string]x509.Certificate{"0": *cert}

	trusted, _ := util.CheckTrustState(*r.TLS.PeerCertificates[0], trustedCerts, nil, false)

	return r.TLS != nil && trusted
}

// Return an http.Transport configured using the given configuration and a
// cleanup function to use to close all connections the transport has been
// used.
func tlsTransport(config *tls.Config) (*http.Transport, func()) {
	transport := &http.Transport{
		TLSClientConfig:   config,
		DisableKeepAlives: true,
		MaxIdleConns:      0,
	}
	return transport, transport.CloseIdleConnections
}
