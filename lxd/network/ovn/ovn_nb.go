package ovn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/ovn-org/libovsdb/cache"
	ovsdbClient "github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"

	"github.com/canonical/lxd/lxd/linux"
	ovnNB "github.com/canonical/lxd/lxd/network/ovn/schema/ovn-nb"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

// nbWaitMode is used to define the waiting behavior of NB.transact.
type nbWaitMode uint

const (
	// nbWaitNone instructs NB.transact not to wait for configuration changes to be applied in the southbound database or on the hypervisors.
	nbWaitNone nbWaitMode = 0

	// nbWaitSB instructs NB.transact to wait for configuration changes to be applied in the southbound database.
	nbWaitSB nbWaitMode = 1

	// nbWaitHV instructs NB.transact to wait for configuration changes to be applied on the hypervisors.
	nbWaitHV nbWaitMode = 2
)

// NB client.
type NB struct {
	client  ovsdbClient.Client
	cookie  ovsdbClient.MonitorCookie
	sbCfgCh chan int
	hvCfgCh chan int

	// For nbctl command calls.
	dbAddr        string
	sslCACert     string
	sslClientCert string
	sslClientKey  string
}

// NewNB initialises new OVN client for Northbound operations.
func NewNB(s *state.State) (*NB, error) {
	// Get database connection string.
	dbAddr := s.GlobalConfig.NetworkOVNNorthboundConnection()
	if strings.HasPrefix(dbAddr, "unix:") {
		dbAddr = fmt.Sprintf("unix:%s", shared.HostPathFollow(strings.TrimPrefix(dbAddr, "unix:")))
	}

	// Create the NB struct.
	client := &NB{
		dbAddr: dbAddr,
	}

	// Prepare the OVSDB client.
	dbSchema, err := ovnNB.FullDatabaseModel()
	if err != nil {
		return nil, err
	}

	discard := logr.Discard()

	options := []ovsdbClient.Option{ovsdbClient.WithLogger(&discard)}
	for _, entry := range strings.Split(dbAddr, ",") {
		options = append(options, ovsdbClient.WithEndpoint(entry))
	}

	// Handle SSL.
	if strings.Contains(dbAddr, "ssl:") {
		// Get the OVN SSL keys from the daemon config.
		sslCACert, sslClientCert, sslClientKey := s.GlobalConfig.NetworkOVNSSL()

		// Fallback to filesystem keys.
		if sslCACert == "" {
			content, err := os.ReadFile("/etc/ovn/ovn-central.crt")
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("OVN configured to use SSL but no SSL CA certificate defined")
				}

				return nil, err
			}

			sslCACert = string(content)
		}

		if sslClientCert == "" {
			content, err := os.ReadFile("/etc/ovn/cert_host")
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("OVN configured to use SSL but no SSL client certificate defined")
				}

				return nil, err
			}

			sslClientCert = string(content)
		}

		if sslClientKey == "" {
			content, err := os.ReadFile("/etc/ovn/key_host")
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("OVN configured to use SSL but no SSL client key defined")
				}

				return nil, err
			}

			sslClientKey = string(content)
		}

		// Validation.
		if sslClientCert == "" {
			return nil, fmt.Errorf("OVN is configured to use SSL but no client certificate was found")
		}

		if sslClientKey == "" {
			return nil, fmt.Errorf("OVN is configured to use SSL but no client key was found")
		}

		// Prepare the client.
		clientCert, err := tls.X509KeyPair([]byte(sslClientCert), []byte(sslClientKey))
		if err != nil {
			return nil, err
		}

		tlsConfig := &tls.Config{
			Certificates:       []tls.Certificate{clientCert},
			InsecureSkipVerify: true,
		}

		// Add CA check if provided.
		if sslCACert != "" {
			tlsCAder, _ := pem.Decode([]byte(sslCACert))
			if tlsCAder == nil {
				return nil, fmt.Errorf("Couldn't parse CA certificate")
			}

			tlsCAcert, err := x509.ParseCertificate(tlsCAder.Bytes)
			if err != nil {
				return nil, err
			}

			tlsCAcert.IsCA = true
			tlsCAcert.KeyUsage = x509.KeyUsageCertSign

			clientCAPool := x509.NewCertPool()
			clientCAPool.AddCert(tlsCAcert)

			tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, chains [][]*x509.Certificate) error {
				if len(rawCerts) < 1 {
					return fmt.Errorf("Missing server certificate")
				}

				// Load the chain.
				roots := x509.NewCertPool()
				for _, rawCert := range rawCerts {
					cert, _ := x509.ParseCertificate(rawCert)
					if cert != nil {
						roots.AddCert(cert)
					}
				}

				// Load the main server certificate.
				cert, _ := x509.ParseCertificate(rawCerts[0])
				if cert == nil {
					return fmt.Errorf("Bad server certificate")
				}

				// Validate.
				opts := x509.VerifyOptions{
					Roots: roots,
				}

				_, err := cert.Verify(opts)
				return err
			}
		}

		// Add the TLS config to the client.
		options = append(options, ovsdbClient.WithTLSConfig(tlsConfig))

		// Fill the fields need for the CLI calls.
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

	// Add the client to the struct.
	client.client = ovn
	client.cookie = monitorCookie

	// Create channels for sb_cfg and hv_cfg.
	client.sbCfgCh = make(chan int)
	client.hvCfgCh = make(chan int)

	// Add an event handler that sends new values of sb_cfg or hv_cfg to their respective channels.
	// This is used to detect configuration changes at the southbound database or hypervisor level without polling.
	handler := cache.EventHandlerFuncs{
		UpdateFunc: func(table string, oldModel model.Model, newModel model.Model) {
			if table != "NB_Global" {
				return
			}

			oldNBGlobal, ok := oldModel.(*ovnNB.NBGlobal)
			if !ok {
				logger.Error("Northbound global table has invalid schema", logger.Ctx{"expected": fmt.Sprintf("%T", &ovnNB.NBGlobal{}), "got": newModel})
				return
			}

			newNBGlobal, ok := newModel.(*ovnNB.NBGlobal)
			if !ok {
				logger.Error("Northbound global table has invalid schema", logger.Ctx{"expected": fmt.Sprintf("%T", &ovnNB.NBGlobal{}), "got": newModel})
				return
			}

			if newNBGlobal.SbCfg != oldNBGlobal.SbCfg {
				client.sbCfgCh <- newNBGlobal.SbCfg
			}

			if newNBGlobal.HvCfg != oldNBGlobal.HvCfg {
				client.hvCfgCh <- newNBGlobal.HvCfg
			}
		},
	}

	ovn.Cache().AddEventHandler(&handler)

	// Set finalizer to stop the monitor.
	runtime.SetFinalizer(client, func(o *NB) {
		_ = ovn.MonitorCancel(context.Background(), o.cookie)
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

// transact executes the given list of operations to the northbound database. The given nbWaitMode determines blocking
// behavior for configuration propagation.
func (o *NB) transact(ctx context.Context, waitMode nbWaitMode, operations ...ovsdb.Operation) error {
	// Nothing to do, return.
	if len(operations) == 0 {
		return nil
	}

	// If we're waiting for configuration changes to be applied, the client must increment the `nb_cfg` attribute
	// of NB_Global. This block adds this logic as an operation to the beginning of the operation list passed by the caller.
	var nbGlobal *ovnNB.NBGlobal
	if waitMode > nbWaitNone {
		var err error
		nbGlobal, err = o.nbGlobal()
		if err != nil {
			return err
		}

		nbGlobal.NbCfg++
		nbGlobal.NbCfgTimestamp = int(time.Now().UnixMilli())
		preOps, err := o.client.Where(nbGlobal).Update(nbGlobal)
		if err != nil {
			return err
		}

		operations = append(preOps, operations...)
	}

	// Perform the transaction.
	res, err := o.client.Transact(ctx, operations...)
	if err != nil {
		return err
	}

	// Check the results.
	_, err = ovsdb.CheckOperationResults(res, operations)
	if err != nil {
		return err
	}

	// If we're not waiting for anything, return now.
	if waitMode == nbWaitNone {
		return nil
	}

	// Current values, these are updated in the select statement below.
	sbCfg := nbGlobal.SbCfg
	hvCfg := nbGlobal.HvCfg

	// If waiting for hypervisors, we check the minimum of sb_cfg and hv_cfg (see https://manpages.ubuntu.com/manpages/noble/en/man5/ovn-nb.5.html
	// and https://github.com/ovn-org/ovn/blob/474bdfcad038e91aeaa036944b6b4be7c3e1ec15/utilities/ovn-nbctl.c#L118-L147)
	// If waiting for the southbound database, we check sb_cfg only.
	nextCfg := func() int {
		if waitMode == nbWaitHV {
			return min(sbCfg, hvCfg)
		}

		return sbCfg
	}

	// Wait for sb_cfg or hv_cfg to be updated. Update the local variables whenever a new value is received and recalculate
	// nextCfg. If nextCfg is greater than or equal to nb_cfg, the configuration has been applied at the requested level.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sbCfg = <-o.sbCfgCh:
			if nextCfg() >= nbGlobal.NbCfg {
				return nil
			}

		case hvCfg = <-o.hvCfgCh:
			if nextCfg() >= nbGlobal.NbCfg {
				return nil
			}
		}
	}
}

// nbGlobal gets the contents of the singleton row of the NB_Global table.
func (o *NB) nbGlobal() (*ovnNB.NBGlobal, error) {
	rows := o.client.Cache().Table("NB_Global").Rows()
	if len(rows) > 1 {
		return nil, errors.New("Northbound global table is not unique")
	}

	for _, m := range rows {
		nbGlobal, ok := m.(*ovnNB.NBGlobal)
		if !ok {
			return nil, errors.New("Northbound global table has invalid schema")
		}

		return nbGlobal, nil
	}

	return nil, errors.New("Northbound global table is not present")
}
