package config

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
)

// Remote holds details for communication with a remote daemon
type Remote struct {
	Addr     string `yaml:"addr"`
	Public   bool   `yaml:"public"`
	Protocol string `yaml:"protocol,omitempty"`
	AuthType string `yaml:"auth_type,omitempty"`
	Static   bool   `yaml:"-"`
	Project  string `yaml:"project,omitempty"`
}

// ParseRemote splits remote and object
func (c *Config) ParseRemote(raw string) (string, string, error) {
	result := strings.SplitN(raw, ":", 2)
	if len(result) == 1 {
		return c.DefaultRemote, raw, nil
	}

	_, ok := c.Remotes[result[0]]
	if !ok {
		// Attempt to play nice with snapshots containing ":"
		if shared.IsSnapshot(raw) && strings.Contains(result[0], "/") {
			return c.DefaultRemote, raw, nil
		}

		return "", "", fmt.Errorf("The remote \"%s\" doesn't exist", result[0])
	}

	return result[0], result[1], nil
}

// GetContainerServer returns a ContainerServer struct for the remote
func (c *Config) GetContainerServer(name string) (lxd.ContainerServer, error) {
	// Get the remote
	remote, ok := c.Remotes[name]
	if !ok {
		return nil, fmt.Errorf("The remote \"%s\" doesn't exist", name)
	}

	// Sanity checks
	if remote.Public || remote.Protocol == "simplestreams" {
		return nil, fmt.Errorf("The remote isn't a private LXD server")
	}

	// Get connection arguments
	args, err := c.getConnectionArgs(name)
	if err != nil {
		return nil, err
	}

	// Unix socket
	if strings.HasPrefix(remote.Addr, "unix:") {
		d, err := lxd.ConnectLXDUnix(strings.TrimPrefix(strings.TrimPrefix(remote.Addr, "unix:"), "//"), args)
		if err != nil {
			return nil, err
		}

		if remote.Project != "" && remote.Project != "default" {
			d = d.UseProject(remote.Project)
		}

		if c.ProjectOverride != "" {
			d = d.UseProject(c.ProjectOverride)
		}

		return d, nil
	}

	// HTTPs
	if remote.AuthType != "candid" && (args.TLSClientCert == "" || args.TLSClientKey == "") {
		return nil, fmt.Errorf("Missing TLS client certificate and key")
	}

	d, err := lxd.ConnectLXD(remote.Addr, args)
	if err != nil {
		return nil, err
	}

	if remote.Project != "" && remote.Project != "default" {
		d = d.UseProject(remote.Project)
	}

	if c.ProjectOverride != "" {
		d = d.UseProject(c.ProjectOverride)
	}

	return d, nil
}

// GetImageServer returns a ImageServer struct for the remote
func (c *Config) GetImageServer(name string) (lxd.ImageServer, error) {
	// Get the remote
	remote, ok := c.Remotes[name]
	if !ok {
		return nil, fmt.Errorf("The remote \"%s\" doesn't exist", name)
	}

	// Get connection arguments
	args, err := c.getConnectionArgs(name)
	if err != nil {
		return nil, err
	}

	// Unix socket
	if strings.HasPrefix(remote.Addr, "unix:") {
		d, err := lxd.ConnectLXDUnix(strings.TrimPrefix(strings.TrimPrefix(remote.Addr, "unix:"), "//"), args)
		if err != nil {
			return nil, err
		}

		if remote.Project != "" && remote.Project != "default" {
			d = d.UseProject(remote.Project)
		}

		if c.ProjectOverride != "" {
			d = d.UseProject(c.ProjectOverride)
		}

		return d, nil
	}

	// HTTPs (simplestreams)
	if remote.Protocol == "simplestreams" {
		d, err := lxd.ConnectSimpleStreams(remote.Addr, args)
		if err != nil {
			return nil, err
		}

		return d, nil
	}

	// HTTPs (public LXD)
	if remote.Public {
		d, err := lxd.ConnectPublicLXD(remote.Addr, args)
		if err != nil {
			return nil, err
		}

		return d, nil
	}

	// HTTPs (private LXD)
	d, err := lxd.ConnectLXD(remote.Addr, args)
	if err != nil {
		return nil, err
	}

	if remote.Project != "" && remote.Project != "default" {
		d = d.UseProject(remote.Project)
	}

	if c.ProjectOverride != "" {
		d = d.UseProject(c.ProjectOverride)
	}

	return d, nil
}

func (c *Config) getConnectionArgs(name string) (*lxd.ConnectionArgs, error) {
	remote, _ := c.Remotes[name]
	args := lxd.ConnectionArgs{
		UserAgent:      c.UserAgent,
		AuthType:       remote.AuthType,
		AuthInteractor: c.authInteractor,
	}

	if c.cookiejar != nil {
		args.CookieJar = c.cookiejar
	}

	// Stop here if no TLS involved
	if strings.HasPrefix(remote.Addr, "unix:") {
		return &args, nil
	}

	// Server certificate
	if shared.PathExists(c.ServerCertPath(name)) {
		content, err := ioutil.ReadFile(c.ServerCertPath(name))
		if err != nil {
			return nil, err
		}

		args.TLSServerCert = string(content)
	}

	// Stop here if no client certificate involved
	if remote.Protocol == "simplestreams" || remote.AuthType == "candid" {
		return &args, nil
	}

	// Client certificate
	if shared.PathExists(c.ConfigPath("client.crt")) {
		content, err := ioutil.ReadFile(c.ConfigPath("client.crt"))
		if err != nil {
			return nil, err
		}

		args.TLSClientCert = string(content)
	}

	// Client CA
	if shared.PathExists(c.ConfigPath("client.ca")) {
		content, err := ioutil.ReadFile(c.ConfigPath("client.ca"))
		if err != nil {
			return nil, err
		}

		args.TLSCA = string(content)
	}

	// Client key
	if shared.PathExists(c.ConfigPath("client.key")) {
		content, err := ioutil.ReadFile(c.ConfigPath("client.key"))
		if err != nil {
			return nil, err
		}

		pemKey, _ := pem.Decode(content)
		if x509.IsEncryptedPEMBlock(pemKey) {
			if c.PromptPassword == nil {
				return nil, fmt.Errorf("Private key is password protected and no helper was configured")
			}

			password, err := c.PromptPassword("client.crt")
			if err != nil {
				return nil, err
			}

			derKey, err := x509.DecryptPEMBlock(pemKey, []byte(password))
			if err != nil {
				return nil, err
			}

			content = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: derKey})
		}

		args.TLSClientKey = string(content)
	}

	return &args, nil
}
