package ovn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	ovsdbModel "github.com/ovn-kubernetes/libovsdb/model"

	"github.com/canonical/lxd/lxd/linux"
	ovnNB "github.com/canonical/lxd/lxd/network/ovn/schema/ovn-nb"
	"github.com/canonical/lxd/shared"
)

// NB represents a Northbound database client.
type NB struct {
	client ovsdbClient.Client
	cookie ovsdbClient.MonitorCookie

	// For ovn-nbctl command calls.
	dbAddr        string
	sslCACert     string
	sslClientCert string
	sslClientKey  string
}

// NewNB initialises new OVN client for Northbound operations.
func NewNB(dbAddr string, sslSettings func() (sslCACert string, sslClientCert string, sslClientKey string)) (*NB, error) {
	after, ok := strings.CutPrefix(dbAddr, "unix:")
	if ok {
		dbAddr = "unix:" + shared.HostPathFollow(after)
	}

	// Create the client struct.
	client := &NB{dbAddr: dbAddr}

	// Prepare the OVSDB client.
	dbSchema, err := ovnNB.FullDatabaseModel()
	if err != nil {
		return nil, err
	}

	// Add some missing indexes.
	dbSchema.SetIndexes(map[string][]ovsdbModel.ClientIndex{
		"Load_Balancer":       {{Columns: []ovsdbModel.ColumnKey{{Column: "name"}}}},
		"Logical_Router":      {{Columns: []ovsdbModel.ColumnKey{{Column: "name"}}}},
		"Logical_Switch":      {{Columns: []ovsdbModel.ColumnKey{{Column: "name"}}}},
		"Logical_Switch_Port": {{Columns: []ovsdbModel.ColumnKey{{Column: "name"}}}},
	})

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

		// Set the fields needed for the ovn-nbctl CLI calls.
		client.sslCACert = sslCACert
		client.sslClientCert = sslClientCert
		client.sslClientKey = sslClientKey
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

	monitorCookie, err := ovn.MonitorAll(context.TODO())
	if err != nil {
		return nil, err
	}

	// Set the fields needed for the libovsdb client.
	client.client = ovn
	client.cookie = monitorCookie

	// Set finalizer to stop the monitor.
	runtime.SetFinalizer(client, func(o *NB) {
		_ = ovn.MonitorCancel(context.Background(), o.cookie)
		ovn.Close()
	})

	return client, nil
}

// nbctl executes ovn-nbctl with arguments to connect to wrapper's northbound database.
func (o *NB) nbctl(extraArgs ...string) (string, error) {
	// Figure out args.
	args := []string{"--wait=sb", "--timeout=10", "--db", o.dbAddr}

	// Handle SSL args.
	files := []*os.File{}
	if strings.Contains(o.dbAddr, "ssl:") {
		// Handle client certificate.
		clientCertFile, err := linux.CreateMemfd([]byte(o.sslClientCert))
		if err != nil {
			return "", err
		}

		defer clientCertFile.Close()
		files = append(files, clientCertFile)

		// Handle client key.
		clientKeyFile, err := linux.CreateMemfd([]byte(o.sslClientKey))
		if err != nil {
			return "", err
		}

		defer clientKeyFile.Close()
		files = append(files, clientKeyFile)

		// Handle CA certificate.
		caCertFile, err := linux.CreateMemfd([]byte(o.sslCACert))
		if err != nil {
			return "", err
		}

		defer caCertFile.Close()
		files = append(files, caCertFile)

		args = append(args,
			"-c", "/proc/self/fd/3",
			"-p", "/proc/self/fd/4",
			"-C", "/proc/self/fd/5",
		)
	}

	args = append(args, extraArgs...)
	return shared.RunCommandInheritFds(context.Background(), files, "ovn-nbctl", args...)
}

// get is used to perform a libovsdb Get call while also making use of the custom defined indexes.
// The libovsdb Get() function only uses the built-in indices rather than considering the user provided ones.
// This seems to be by design but makes it harder to fetch records from some tables.
func (o *NB) get(ctx context.Context, m ovsdbModel.Model) error {
	var collection any

	// Check if the model is one of the types with custom defined index.
	switch m.(type) {
	case *ovnNB.LoadBalancer:
		s := []ovnNB.LoadBalancer{}
		collection = &s
	case *ovnNB.LogicalRouter:
		s := []ovnNB.LogicalRouter{}
		collection = &s
	case *ovnNB.LogicalSwitch:
		s := []ovnNB.LogicalSwitch{}
		collection = &s
	case *ovnNB.LogicalSwitchPort:
		s := []ovnNB.LogicalSwitchPort{}
		collection = &s
	default:
		// Fallback to normal Get.
		return o.client.Get(ctx, m)
	}

	// Check and assign the resulting value.
	err := o.client.Where(m).List(ctx, collection)
	if err != nil {
		return err
	}

	rVal := reflect.ValueOf(collection)
	if rVal.Kind() != reflect.Pointer {
		return errors.New("Bad collection type")
	}

	rVal = rVal.Elem()
	if rVal.Kind() != reflect.Slice {
		return errors.New("Bad collection type")
	}

	if rVal.Len() == 0 {
		return ovsdbClient.ErrNotFound
	}

	if rVal.Len() > 1 {
		return ErrTooMany
	}

	reflect.ValueOf(m).Elem().Set(rVal.Index(0))
	return nil
}
