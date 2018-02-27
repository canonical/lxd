package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

// Return a TLS configuration suitable for establishing inter-node network
// connections using the cluster certificate.
func tlsClientConfig(info *shared.CertInfo) (*tls.Config, error) {
	keypair := info.KeyPair()
	ca := info.CA()
	config := shared.InitTLSConfig()
	config.Certificates = []tls.Certificate{keypair}
	config.RootCAs = x509.NewCertPool()
	if ca != nil {
		config.RootCAs.AddCert(ca)
	}
	// Since the same cluster keypair is used both as server and as client
	// cert, let's add it to the CA pool to make it trusted.
	cert, err := x509.ParseCertificate(keypair.Certificate[0])
	if err != nil {
		return nil, err
	}
	cert.IsCA = true
	cert.KeyUsage = x509.KeyUsageCertSign
	config.RootCAs.AddCert(cert)

	if cert.DNSNames != nil {
		config.ServerName = cert.DNSNames[0]
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
	trustedCerts := []x509.Certificate{*cert}
	return r.TLS != nil && util.CheckTrustState(*r.TLS.PeerCertificates[0], trustedCerts)
}
