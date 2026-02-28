package config

import (
	"fmt"
	"io"
	"os"

	"github.com/canonical/lxd/shared"
)

// GenerateClientCertificate will generate the needed client.crt and client.key if needed.
func (c *Config) GenerateClientCertificate() error {
	certf := c.ConfigPath("client.crt")
	keyf := c.ConfigPath("client.key")

	if shared.PathExists(certf) && shared.PathExists(keyf) {
		return nil
	}

	fmt.Fprint(os.Stderr, "Generating a client certificate. This may take a moment...\n")

	return shared.FindOrGenCert(certf, keyf, true, shared.CertOptions{})
}

// CopyGlobalCert will copy global (system-wide) certificate to the user config path.
func (c *Config) CopyGlobalCert(src string, dst string) error {
	oldPath := c.GlobalConfigPath("servercerts", src+".crt")
	newPath := c.ConfigPath("servercerts", dst+".crt")
	sourceFile, err := os.Open(oldPath)
	if err != nil {
		return err
	}

	defer func() { _ = sourceFile.Close() }()

	// Create new file
	newFile, err := os.Create(newPath)
	if err != nil {
		return err
	}

	defer func() { _ = newFile.Close() }()

	_, err = io.Copy(newFile, sourceFile)
	if err != nil {
		return err
	}

	return newFile.Close()
}
