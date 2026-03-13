package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"

	log "github.com/sirupsen/logrus"

	"github.com/canonical/lxd/lxd/ucred"
	"github.com/canonical/lxd/shared"
)

func tlsConfig(uid uint32) (*tls.Config, error) {
	userDir := filepath.Join("users", strconv.FormatUint(uint64(uid), 10))

	// Load the client certificate.
	content, err := os.ReadFile(filepath.Join(userDir, "client.crt"))
	if err != nil {
		return nil, fmt.Errorf("Cannot open client certificate: %w", err)
	}

	tlsClientCert := string(content)

	// Load the client key.
	content, err = os.ReadFile(filepath.Join(userDir, "client.key"))
	if err != nil {
		return nil, fmt.Errorf("Cannot open client key: %w", err)
	}

	tlsClientKey := string(content)

	// Load the server certificate.
	certPath := shared.VarPath("cluster.crt")

	content, err = os.ReadFile(certPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Cannot open server certificate: %w", err)
	}

	if os.IsNotExist(err) {
		certPath = shared.VarPath("server.crt")
		content, err = os.ReadFile(certPath)
		if err != nil {
			return nil, fmt.Errorf("Cannot open server certificate: %w", err)
		}
	}

	tlsServerCert := string(content)

	return shared.GetTLSConfigMem(tlsClientCert, tlsClientKey, "", tlsServerCert, false)
}

func proxyConnection(conn *net.UnixConn) {
	defer func() {
		_ = conn.Close()

		mu.Lock()
		connections -= 1
		mu.Unlock()
	}()

	// Increase counters.
	mu.Lock()
	transactions += 1
	connections += 1
	mu.Unlock()

	// Get credentials.
	creds, err := ucred.GetCred(conn)
	if err != nil {
		log.Errorf("Cannot get user credentials: %s", err)
		return
	}

	// Setup logging context.
	logger := log.WithFields(log.Fields{
		"uid": creds.Uid,
		"gid": creds.Gid,
		"pid": creds.Pid,
	})

	logger.Debug("Connected")
	defer logger.Debug("Disconnected")

	// Check if the user was setup.
	userDir := filepath.Join("users", strconv.FormatUint(uint64(creds.Uid), 10))
	if !shared.PathExists(userDir) {
		log.Infof("Setting up LXD for uid %d", creds.Uid)
		err := lxdSetupUser(creds.Uid)
		if err != nil {
			log.Errorf("Failed setting up new user: %v", err)
			return
		}
	}

	// Connect to LXD.
	unixAddr, err := net.ResolveUnixAddr("unix", shared.VarPath("unix.socket"))
	if err != nil {
		log.Errorf("Cannot resolve the target server: %v", err)
		return
	}

	client, err := net.DialUnix("unix", nil, unixAddr)
	if err != nil {
		log.Errorf("Cannot connect to target server: %v", err)
		return
	}

	defer func() { _ = client.Close() }()

	// Get the TLS configuration
	tlsConfig, err := tlsConfig(creds.Uid)
	if err != nil {
		log.Errorf("Failed loading TLS connection settings: %v", err)
		return
	}

	// Setup TLS.
	_, err = client.Write([]byte("STARTTLS\n"))
	if err != nil {
		log.Errorf("Failed setting up TLS connection to target server: %v", err)
		return
	}

	tlsClient := tls.Client(client, tlsConfig)

	// Establish the TLS handshake.
	err = tlsClient.Handshake()
	if err != nil {
		_ = conn.Close()
		log.Errorf("Failed TLS handshake with target server: %v", err)
		return
	}

	// Start proxying.
	go func() { _, _ = io.Copy(conn, tlsClient) }()
	_, _ = io.Copy(tlsClient, conn)
}
