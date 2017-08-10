package config

import (
	"github.com/lxc/lxd/shared"
)

// HasClientCertificate will return true if a client certificate has already been generated
func (c *Config) HasClientCertificate() bool {
	certf := c.ConfigPath("client.crt")
	keyf := c.ConfigPath("client.key")
	if !shared.PathExists(certf) || !shared.PathExists(keyf) {
		return false
	}

	return true
}

// GenerateClientCertificate will generate the needed client.crt and client.key if needed
func (c *Config) GenerateClientCertificate() error {
	if c.HasClientCertificate() {
		return nil
	}

	certf := c.ConfigPath("client.crt")
	keyf := c.ConfigPath("client.key")

	return shared.FindOrGenCert(certf, keyf, true)
}
