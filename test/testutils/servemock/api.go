package servemock

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared"
)

// CAPrivateKey returns the private key for the mockserver CA.
func CAPrivateKey() *ecdsa.PrivateKey {
	return testECCP256
}

// testECCP256 is an insecure, test-only key from RFC 9500, Section 2.3.
// It can be used in tests to avoid slow key generation.
var testECCP256 *ecdsa.PrivateKey

func init() {
	block, _ := pem.Decode([]byte(strings.ReplaceAll(
		`-----BEGIN EC TESTING KEY-----
MHcCAQEEIObLW92AqkWunJXowVR2Z5/+yVPBaFHnEedDk5WJxk/BoAoGCCqGSM49
AwEHoUQDQgAEQiVI+I+3gv+17KN0RFLHKh5Vj71vc75eSOkyMsxFxbFsTNEMTLjV
uKFxOelIgsiZJXKZNCX0FBmrfpCkKklCcg==
-----END EC TESTING KEY-----`, "TESTING KEY", "PRIVATE KEY")))

	testECCP256, _ = x509.ParseECPrivateKey(block.Bytes)
}

// Handler represents a basic mux handler.
type Handler struct {
	Pattern     string
	HTTPHandler func(w http.ResponseWriter, r *http.Request)
}

// Config is config for the mock server.
type Config struct {
	Address    string
	NotFound   http.HandlerFunc
	Handlers   []Handler
	CACertPath string
	UseTLS     bool
}

// RunResult returns details about the running server and an error channel to listen on. Either nil or an error is sent
// to the channel when the server is stopped (depending whether it was cleanly closed).
type RunResult struct {
	CertInfo  *shared.CertInfo
	TLSConfig *tls.Config
	Server    *http.Server
	Listener  net.Listener
	Err       <-chan error
}

func tlsConfig(address string, caCertPath string) (*tls.Config, *shared.CertInfo, error) {
	var err error
	caKey := testECCP256

	var caCert *x509.Certificate
	if caCertPath != "" {
		caCert, err = shared.ReadCert(caCertPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("Failed reading CA cert: %w", err)
		}
	}

	if caCert == nil {
		caTemplate := &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: "mockserver CA"},
			NotBefore:             time.Now(),
			NotAfter:              time.Now().Add(6 * 24 * time.Hour),
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
			BasicConstraintsValid: true,
			IsCA:                  true,
		}

		caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
		if err != nil {
			return nil, nil, fmt.Errorf("Failed creating CA certificate: %w", err)
		}

		caCert, err = x509.ParseCertificate(caCertDER)
		if err != nil {
			return nil, nil, fmt.Errorf("Failed parsing CA certificate: %w", err)
		}
	}

	// Generate a TLS serving certificate for the listen address signed by the CA.
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed getting listen IP address: %w", err)
	}

	hostIP := net.ParseIP(host)
	if hostIP == nil {
		return nil, nil, fmt.Errorf("Invalid IP %q", host)
	}

	tlsCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "mockserver"},
		IPAddresses:  []net.IP{hostIP},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, caCert, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed creating TLS certificate: %w", err)
	}

	tlsCert := tls.Certificate{
		Certificate: [][]byte{tlsCertDER},
		PrivateKey:  caKey,
	}

	conf := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS13,
	}

	info := shared.NewCertInfo(tlsCert, caCert, nil)

	return conf, info, nil
}

// API starts an HTTP server with the given handler and returns the address it's
// listening on, a channel for errors, and any error encountered while starting
// the server.
func API(ctx context.Context, config Config) (*RunResult, error) {
	mux := http.NewServeMux()
	for _, h := range config.Handlers {
		mux.HandleFunc(h.Pattern, h.HTTPHandler)
	}

	notFound := config.NotFound
	if config.NotFound == nil {
		notFound = func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}
	}

	// Catch-all handler for unmatched routes.
	mux.HandleFunc("/", notFound)

	ctx, cancel := signal.NotifyContext(ctx, unix.SIGINT, unix.SIGKILL)
	errCh := make(chan error)
	result := RunResult{
		Err: errCh,
	}

	var listener net.Listener
	if config.UseTLS {
		tlsConfig, info, err := tlsConfig(config.Address, config.CACertPath)
		if err != nil {
			return nil, errors.New("Failed loading TLS configuration")
		}

		if config.CACertPath != "" {
			caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: info.CA().Raw})

			// Write the CA certificate so that clients can use it to trust this server.
			err = os.WriteFile(config.CACertPath, caCertPEM, 0644)
			if err != nil {
				return nil, fmt.Errorf("Error writing CA cert: %v", err)
			}
		}

		listener, err = tls.Listen("tcp", config.Address, tlsConfig)
		if err != nil {
			return nil, err
		}

		result.CertInfo = info
		result.TLSConfig = tlsConfig
	} else {
		var err error
		listener, err = net.Listen("tcp", config.Address)
		if err != nil {
			return nil, err
		}
	}

	server := &http.Server{Handler: mux}
	go func() {
		err := server.Serve(listener)
		if !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		} else {
			errCh <- nil
		}

		cancel()
	}()

	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()

	result.Server = server
	result.Listener = listener
	return &result, nil
}
