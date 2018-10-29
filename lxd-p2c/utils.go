package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh/terminal"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func transferRootfs(dst lxd.ContainerServer, op lxd.Operation, rootfs string, rsyncArgs string) error {
	opAPI := op.Get()

	// Connect to the websockets
	wsControl, err := op.GetWebsocket(opAPI.Metadata["control"].(string))
	if err != nil {
		return err
	}

	wsFs, err := op.GetWebsocket(opAPI.Metadata["fs"].(string))
	if err != nil {
		return err
	}

	// Setup control struct
	fs := migration.MigrationFSType_RSYNC
	rsyncHasFeature := true
	header := migration.MigrationHeader{
		RsyncFeatures: &migration.RsyncFeatures{
			Xattrs:   &rsyncHasFeature,
			Delete:   &rsyncHasFeature,
			Compress: &rsyncHasFeature,
		},
		Fs: &fs,
	}

	err = migration.ProtoSend(wsControl, &header)
	if err != nil {
		protoSendError(wsControl, err)
		return err
	}

	err = migration.ProtoRecv(wsControl, &header)
	if err != nil {
		protoSendError(wsControl, err)
		return err
	}

	// Send the filesystem
	abort := func(err error) error {
		protoSendError(wsControl, err)
		return err
	}

	err = rsyncSend(wsFs, rootfs, rsyncArgs)
	if err != nil {
		return abort(err)
	}

	// Check the result
	msg := migration.MigrationControl{}
	err = migration.ProtoRecv(wsControl, &msg)
	if err != nil {
		wsControl.Close()
		return err
	}

	if !*msg.Success {
		return fmt.Errorf(*msg.Message)
	}

	return nil
}

func connectTarget(url string) (lxd.ContainerServer, error) {
	// Generate a new client certificate for this
	fmt.Println("Generating a temporary client certificate. This may take a minute...")
	clientCrt, clientKey, err := shared.GenerateMemCert(true)
	if err != nil {
		return nil, err
	}

	// Attempt to connect using the system CA
	args := lxd.ConnectionArgs{}
	args.TLSClientCert = string(clientCrt)
	args.TLSClientKey = string(clientKey)
	args.UserAgent = "LXD-P2C"
	c, err := lxd.ConnectLXD(url, &args)

	var certificate *x509.Certificate
	if err != nil {
		// Failed to connect using the system CA, so retrieve the remote certificate
		certificate, err = shared.GetRemoteCertificate(url)
		if err != nil {
			return nil, err
		}
	}

	// Handle certificate prompt
	if certificate != nil {
		digest := shared.CertFingerprint(certificate)

		fmt.Printf("Certificate fingerprint: %s\n", digest)
		fmt.Printf("ok (y/n)? ")
		line, err := shared.ReadStdin()
		if err != nil {
			return nil, err
		}

		if len(line) < 1 || line[0] != 'y' && line[0] != 'Y' {
			return nil, fmt.Errorf("Server certificate rejected by user")
		}

		serverCrt := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
		args.TLSServerCert = string(serverCrt)

		// Setup a new connection, this time with the remote certificate
		c, err = lxd.ConnectLXD(url, &args)
		if err != nil {
			return nil, err
		}
	}

	// Get server information
	srv, _, err := c.GetServer()
	if err != nil {
		return nil, err
	}

	// Check if our cert is already trusted
	if srv.Auth == "trusted" {
		return c, nil
	}

	// Prompt for trust password
	fmt.Printf("Admin password for %s: ", url)
	pwd, err := terminal.ReadPassword(0)
	if err != nil {
		return nil, err
	}
	fmt.Println("")

	// Add client certificate to trust store
	req := api.CertificatesPost{
		Password: string(pwd),
	}
	req.Type = "client"

	err = c.CreateCertificate(req)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func setupSource(path string, mounts []string) error {
	prefix := "/"
	if len(mounts) > 0 {
		prefix = mounts[0]
	}

	// Mount everything
	for _, mount := range mounts {
		target := fmt.Sprintf("%s/%s", path, strings.TrimPrefix(mount, prefix))

		// Mount the path
		err := syscall.Mount(mount, target, "none", syscall.MS_BIND, "")
		if err != nil {
			return fmt.Errorf("Failed to mount %s: %v", mount, err)
		}

		// Make it read-only
		err = syscall.Mount("", target, "none", syscall.MS_BIND|syscall.MS_RDONLY|syscall.MS_REMOUNT, "")
		if err != nil {
			return fmt.Errorf("Failed to make %s read-only: %v", mount, err)
		}
	}

	return nil
}

func parseURL(URL string) (string, error) {
	u, err := url.Parse(URL)
	if err != nil {
		return "", err
	}

	// Create a URL with scheme and hostname since it wasn't provided
	if u.Scheme == "" && u.Host == "" && u.Path != "" {
		u, err = url.Parse(fmt.Sprintf("https://%s", u.Path))
		if err != nil {
			return "", err
		}
	}

	// If no port was provided, use port 8443
	if u.Port() == "" {
		u.Host = fmt.Sprintf("%s:8443", u.Hostname())
	}

	return u.String(), nil
}
