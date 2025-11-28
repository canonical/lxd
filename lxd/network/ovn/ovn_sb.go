package ovn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"runtime"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"

	ovnSB "github.com/canonical/lxd/lxd/network/ovn/schema/ovn-sb"
	"github.com/canonical/lxd/shared"
)

// SB represents a Southbound database client.
type SB struct {
	client ovsdbClient.Client
	cookie ovsdbClient.MonitorCookie
}

// NewSB initializes new OVN client for Southbound operations.
func NewSB(dbAddr string, sslSettings func() (sslCACert string, sslClientCert string, sslClientKey string)) (*SB, error) {
	after, ok := strings.CutPrefix(dbAddr, "unix:")
	if ok {
		dbAddr = "unix:" + shared.HostPathFollow(after)
	}

	// Prepare the OVSDB client.
	dbSchema, err := ovnSB.FullDatabaseModel()
	if err != nil {
		return nil, err
	}

	discard := logr.Discard()

	options := []ovsdbClient.Option{ovsdbClient.WithLogger(&discard), ovsdbClient.WithReconnect(5*time.Second, &backoff.ZeroBackOff{})}
	for entry := range strings.SplitSeq(dbAddr, ",") {
		options = append(options, ovsdbClient.WithEndpoint(entry))
	}

	// If using SSL, then get the CA and client key pair.
	if strings.Contains(dbAddr, "ssl:") {
		sslCACert, sslClientCert, sslClientKey := sslSettings()

		if sslCACert == "" {
			sslCACert, err = readCertFile("/etc/ovn/ovn-central.crt", "SSL CA certificate")
			if err != nil {
				return nil, err
			}
		}

		if sslClientCert == "" {
			sslClientCert, err = readCertFile("/etc/ovn/cert_host", "SSL client certificate")
			if err != nil {
				return nil, err
			}
		}

		if sslClientKey == "" {
			sslClientKey, err = readCertFile("/etc/ovn/key_host", "SSL client key")
			if err != nil {
				return nil, err
			}
		}

		// Prepare the client.
		clientCert, err := tls.X509KeyPair([]byte(sslClientCert), []byte(sslClientKey))
		if err != nil {
			return nil, err
		}

		tlsCAder, _ := pem.Decode([]byte(sslCACert))
		if tlsCAder == nil {
			return nil, errors.New("Couldn't parse OVN CA certificate")
		}

		tlsCAcert, err := x509.ParseCertificate(tlsCAder.Bytes)
		if err != nil {
			return nil, err
		}

		clientCAPool := x509.NewCertPool()
		clientCAPool.AddCert(tlsCAcert)

		tlsConfig := &tls.Config{
			Certificates:       []tls.Certificate{clientCert},
			InsecureSkipVerify: true, // Don't use the default TLS verification.

			// We use custom TLS verification here to skip the hostname verification.
			VerifyPeerCertificate: func(rawCerts [][]byte, chains [][]*x509.Certificate) error {
				if len(rawCerts) < 1 {
					return errors.New("Missing server certificate")
				}

				// Parse the server certificate.
				cert, err := x509.ParseCertificate(rawCerts[0])
				if cert == nil || err != nil {
					return errors.New("Bad server certificate")
				}

				// Build the intermediate pool from remaining certs.
				intermediates := x509.NewCertPool()
				for _, rawCert := range rawCerts[1:] {
					intermediateCert, err := x509.ParseCertificate(rawCert)
					if err == nil {
						intermediates.AddCert(intermediateCert)
					}
				}

				// Verify against the CA we trust.
				opts := x509.VerifyOptions{
					Roots:         clientCAPool,
					Intermediates: intermediates,
				}

				_, err = cert.Verify(opts)
				return err
			},
		}

		// Add the TLS config to the client.
		options = append(options, ovsdbClient.WithTLSConfig(tlsConfig))
	}

	// Connect to OVSDB.
	ovn, err := ovsdbClient.NewOVSDBClient(dbSchema, options...)
	if err != nil {
		return nil, err
	}

	err = ovn.Connect(context.TODO())
	if err != nil {
		return nil, err
	}

	err = ovn.Echo(context.TODO())
	if err != nil {
		return nil, err
	}

	// Set up the monitor for the tables we use.
	monitorCookie, err := ovn.Monitor(context.TODO(), ovn.NewMonitor(
		ovsdbClient.WithTable(&ovnSB.Chassis{}),
		ovsdbClient.WithTable(&ovnSB.PortBinding{}),
		ovsdbClient.WithTable(&ovnSB.ServiceMonitor{}),
	))
	if err != nil {
		return nil, err
	}

	// Create the client struct.
	client := &SB{
		client: ovn,
		cookie: monitorCookie,
	}

	// Set finalizer to stop the monitor.
	runtime.SetFinalizer(client, func(o *SB) {
		_ = ovn.MonitorCancel(context.Background(), o.cookie)
		ovn.Close()
	})

	return client, nil
}
