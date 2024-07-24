package main

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
	"github.com/canonical/lxd/shared/ws"
)

func transferRootDiskForMigration(ctx context.Context, op lxd.Operation, rootfs string, rsyncArgs string, instanceType api.InstanceType) error {
	opAPI := op.Get()

	// Connect to the websockets
	wsControl, err := op.GetWebsocket(opAPI.Metadata[api.SecretNameControl].(string))
	if err != nil {
		return err
	}

	abort := func(err error) error {
		protoSendError(wsControl, err)
		return err
	}

	wsFs, err := op.GetWebsocket(opAPI.Metadata[api.SecretNameFilesystem].(string))
	if err != nil {
		return abort(err)
	}

	// Setup control struct
	var fs migration.MigrationFSType
	var rsyncHasFeature bool

	if instanceType == api.InstanceTypeVM {
		fs = migration.MigrationFSType_BLOCK_AND_RSYNC
		rsyncHasFeature = false
	} else {
		fs = migration.MigrationFSType_RSYNC
		rsyncHasFeature = true
	}

	offerHeader := migration.MigrationHeader{
		RsyncFeatures: &migration.RsyncFeatures{
			Xattrs:   &rsyncHasFeature,
			Delete:   &rsyncHasFeature,
			Compress: &rsyncHasFeature,
		},
		Fs: &fs,
	}

	if instanceType == api.InstanceTypeVM {
		stat, err := os.Stat(filepath.Join(rootfs, "root.img"))
		if err != nil {
			return abort(err)
		}

		size := stat.Size()
		offerHeader.VolumeSize = &size
		rootfs = shared.AddSlash(rootfs)
	}

	err = migration.ProtoSend(wsControl, &offerHeader)
	if err != nil {
		return abort(err)
	}

	var respHeader migration.MigrationHeader
	err = migration.ProtoRecv(wsControl, &respHeader)
	if err != nil {
		return abort(err)
	}

	rsyncFeaturesOffered := offerHeader.GetRsyncFeaturesSlice()
	rsyncFeaturesResponse := respHeader.GetRsyncFeaturesSlice()

	if !reflect.DeepEqual(rsyncFeaturesOffered, rsyncFeaturesResponse) {
		return abort(fmt.Errorf("Offered rsync features (%v) differ from those in the migration response (%v)", rsyncFeaturesOffered, rsyncFeaturesResponse))
	}

	// Send the filesystem
	err = rsyncSend(ctx, wsFs, rootfs, rsyncArgs, instanceType)
	if err != nil {
		return abort(fmt.Errorf("Failed sending filesystem volume: %w", err))
	}

	// Send block volume
	if instanceType == api.InstanceTypeVM {
		err := sendBlockVol(ctx, ws.NewWrapper(wsFs), filepath.Join(rootfs, "root.img"))
		if err != nil {
			return abort(err)
		}
	}

	// Check the result
	msg := migration.MigrationControl{}
	err = migration.ProtoRecv(wsControl, &msg)
	if err != nil {
		_ = wsControl.Close()
		return err
	}

	if !msg.GetSuccess() {
		return fmt.Errorf(msg.GetMessage())
	}

	return nil
}

func transferRootDiskForConversion(ctx context.Context, op lxd.Operation, rootfs string, rsyncArgs string, instanceType api.InstanceType) error {
	opAPI := op.Get()

	// Establish websocket connection.
	wsFs, err := op.GetWebsocket(opAPI.Metadata[api.SecretNameFilesystem].(string))
	if err != nil {
		return err
	}

	if instanceType == api.InstanceTypeContainer {
		// Send container filesystem.
		err = rsyncSend(ctx, wsFs, rootfs, rsyncArgs, instanceType)
		if err != nil {
			return fmt.Errorf("Failed sending filesystem volume: %w", err)
		}
	} else {
		// Send VM block volume (image / partition).
		err := sendBlockVol(ctx, ws.NewWrapper(wsFs), filepath.Join(rootfs, "root.img"))
		if err != nil {
			return fmt.Errorf("Failed sending block volume: %w", err)
		}
	}

	return op.Wait()
}

func (c *cmdMigrate) connectLocal() (lxd.InstanceServer, error) {
	args := lxd.ConnectionArgs{}
	args.UserAgent = fmt.Sprintf("LXD-MIGRATE %s", version.Version)

	return lxd.ConnectLXDUnix("", &args)
}

func (c *cmdMigrate) connectTarget(url string, certPath string, keyPath string, authType string, token string) (lxd.InstanceServer, string, error) {
	args := lxd.ConnectionArgs{
		AuthType: authType,
	}

	clientFingerprint := ""

	if authType == api.AuthenticationMethodTLS {
		var clientCrt []byte
		var clientKey []byte

		// Generate a new client certificate for this
		if certPath == "" || keyPath == "" {
			var err error

			clientCrt, clientKey, err = shared.GenerateMemCert(true, shared.CertOptions{})
			if err != nil {
				return nil, "", err
			}

			clientFingerprint, err = shared.CertFingerprintStr(string(clientCrt))
			if err != nil {
				return nil, "", err
			}

			// When using certificate add tokens, there's no need to show the temporary certificate.
			if token == "" {
				fmt.Printf("\nYour temporary certificate is:\n%s\n", string(clientCrt))
			}
		} else {
			var err error

			clientCrt, err = os.ReadFile(certPath)
			if err != nil {
				return nil, "", fmt.Errorf("Failed to read client certificate: %w", err)
			}

			clientKey, err = os.ReadFile(keyPath)
			if err != nil {
				return nil, "", fmt.Errorf("Failed to read client key: %w", err)
			}
		}

		args.TLSClientCert = string(clientCrt)
		args.TLSClientKey = string(clientKey)
	}

	// Attempt to connect using the system CA
	args.UserAgent = fmt.Sprintf("LXD-MIGRATE %s", version.Version)
	instanceServer, err := lxd.ConnectLXD(url, &args)

	var certificate *x509.Certificate
	if err != nil {
		// Failed to connect using the system CA, so retrieve the remote certificate
		certificate, err = shared.GetRemoteCertificate(url, args.UserAgent)
		if err != nil {
			return nil, "", err
		}
	}

	// Handle certificate prompt
	if certificate != nil {
		serverCrt := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
		args.TLSServerCert = string(serverCrt)

		// Setup a new connection, this time with the remote certificate
		instanceServer, err = lxd.ConnectLXD(url, &args)
		if err != nil {
			return nil, "", err
		}
	}

	// Get server information
	srv, _, err := instanceServer.GetServer()
	if err != nil {
		return nil, "", err
	}

	// Check if our cert is already trusted
	if srv.Auth == "trusted" {
		fmt.Printf("\nRemote LXD server:\n  Hostname: %s\n  Version: %s\n\n", srv.Environment.ServerName, srv.Environment.ServerVersion)
		return instanceServer, "", nil
	}

	if authType == api.AuthenticationMethodTLS {
		if token != "" {
			req := api.CertificatesPost{}

			if instanceServer.HasExtension("explicit_trust_token") {
				req.TrustToken = token
			} else {
				req.Password = token
			}

			err = instanceServer.CreateCertificate(req)
			if err != nil {
				return nil, "", fmt.Errorf("Failed to create certificate: %w", err)
			}
		} else if instanceServer.HasExtension("explicit_trust_token") {
			fmt.Println("A temporary client certificate was generated, use `lxc config trust add` on the target server.")
			fmt.Println("")

			fmt.Print("Press ENTER after the certificate was added to the remote server: ")
			_, err = bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil {
				return nil, "", err
			}
		} else {
			fmt.Println("It is recommended to have this certificate be manually added to LXD through `lxc config trust add` on the target server.\nAlternatively you could use a pre-defined trust password to add it remotely (use of a trust password can be a security issue).")
			fmt.Println("")

			useTrustPassword, err := c.global.asker.AskBool("Would you like to use a trust password? [default=no]: ", "no")
			if err != nil {
				return nil, "", err
			}

			if useTrustPassword {
				// Prompt for trust password
				fmt.Print("Trust password: ")
				pwd, err := term.ReadPassword(0)
				if err != nil {
					return nil, "", err
				}

				fmt.Println("")

				// Add client certificate to trust store
				req := api.CertificatesPost{
					Password: string(pwd),
				}

				req.Type = api.CertificateTypeClient

				err = instanceServer.CreateCertificate(req)
				if err != nil {
					return nil, "", err
				}
			} else {
				fmt.Print("Press ENTER after the certificate was added to the remote server: ")
				_, err = bufio.NewReader(os.Stdin).ReadString('\n')
				if err != nil {
					return nil, "", err
				}
			}
		}
	} else {
		instanceServer.RequireAuthenticated(true)
	}

	// Get full server information
	srv, _, err = instanceServer.GetServer()
	if err != nil {
		if clientFingerprint != "" {
			_ = instanceServer.DeleteCertificate(clientFingerprint)
		}

		return nil, "", err
	}

	if srv.Auth == "untrusted" {
		return nil, "", fmt.Errorf("Server doesn't trust us after authentication")
	}

	fmt.Printf("\nRemote LXD server:\n  Hostname: %s\n  Version: %s\n\n", srv.Environment.ServerName, srv.Environment.ServerVersion)

	return instanceServer, clientFingerprint, nil
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
		err := unix.Mount(mount, target, "none", unix.MS_BIND, "")
		if err != nil {
			return fmt.Errorf("Failed to mount %s: %w", mount, err)
		}

		// Make it read-only
		err = unix.Mount("", target, "none", unix.MS_BIND|unix.MS_RDONLY|unix.MS_REMOUNT, "")
		if err != nil {
			return fmt.Errorf("Failed to make %s read-only: %w", mount, err)
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

	// If no port was provided, use default port
	if u.Port() == "" {
		u.Host = fmt.Sprintf("%s:%d", u.Hostname(), shared.HTTPSDefaultPort)
	}

	return u.String(), nil
}

// isImageTypeRaw checks whether the file on a given path represents a disk,
// partition, or image in raw format.
func isImageTypeRaw(path string) (bool, error) {
	cmd := exec.Command("file", "--brief", path)
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("Failed to extract image file type: %v", err)
	}

	isRaw := strings.HasPrefix(string(out), "DOS/MBR boot sector") || strings.HasPrefix(string(out), "block special")
	return isRaw, nil
}
