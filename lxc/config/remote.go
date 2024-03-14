package config

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/zitadel/oidc/v2/pkg/oidc"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// Remote holds details for communication with a remote daemon.
type Remote struct {
	Addr     string `yaml:"addr"`
	AuthType string `yaml:"auth_type,omitempty"`
	Project  string `yaml:"project,omitempty"`
	Protocol string `yaml:"protocol,omitempty"`
	Public   bool   `yaml:"public"`
	Global   bool   `yaml:"-"`
	Static   bool   `yaml:"-"`
}

// ParseRemote splits remote and object.
func (c *Config) ParseRemote(raw string) (remoteName string, resourceName string, err error) {
	result := strings.SplitN(raw, ":", 2)
	if len(result) == 1 {
		return c.DefaultRemote, raw, nil
	}

	_, ok := c.Remotes[result[0]]
	if !ok {
		// Attempt to play nice with snapshots containing ":"
		if shared.IsSnapshot(raw) && shared.IsSnapshot(result[0]) {
			return c.DefaultRemote, raw, nil
		}

		return "", "", fmt.Errorf("The remote \"%s\" doesn't exist", result[0])
	}

	return result[0], result[1], nil
}

// GetInstanceServer returns a InstanceServer struct for the remote.
func (c *Config) GetInstanceServer(name string) (lxd.InstanceServer, error) {
	// Handle "local" on non-Linux
	if name == "local" && runtime.GOOS != "linux" {
		return nil, ErrNotLinux
	}

	// Get the remote
	remote, ok := c.Remotes[name]
	if !ok {
		return nil, fmt.Errorf("The remote \"%s\" doesn't exist", name)
	}

	// Quick checks.
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
			var netErr *net.OpError

			if errors.As(err, &netErr) {
				if errors.Is(err, os.ErrNotExist) {
					return nil, fmt.Errorf("LXD unix socket %q not found: Please check LXD is running", netErr.Addr)
				}

				if errors.Is(err, os.ErrPermission) {
					return nil, fmt.Errorf("LXD unix socket %q not accessible: permission denied", netErr.Addr)
				}

				return nil, fmt.Errorf("LXD unix socket %q not accessible: %w", netErr.Addr, err)
			}

			return nil, fmt.Errorf("LXD unix socket not accessible: %w", err)
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
	if !shared.ValueInSlice(remote.AuthType, []string{api.AuthenticationMethodOIDC}) && (args.TLSClientCert == "" || args.TLSClientKey == "") {
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

// GetImageServer returns a ImageServer struct for the remote.
func (c *Config) GetImageServer(name string) (lxd.ImageServer, error) {
	// Handle "local" on non-Linux
	if name == "local" && runtime.GOOS != "linux" {
		return nil, ErrNotLinux
	}

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

// getConnectionArgs retrieves the connection arguments for the specified remote.
// It constructs the necessary connection arguments based on the remote's configuration, including authentication type,
// authentication interactors, cookie jar, OIDC tokens, TLS certificates, and client key.
// The function returns the connection arguments or an error if any configuration is missing or encounters a problem.
func (c *Config) getConnectionArgs(name string) (*lxd.ConnectionArgs, error) {
	remote := c.Remotes[name]
	args := lxd.ConnectionArgs{
		UserAgent: c.UserAgent,
		AuthType:  remote.AuthType,
	}

	if args.AuthType == api.AuthenticationMethodOIDC {
		if c.oidcTokens == nil {
			c.oidcTokens = map[string]*oidc.Tokens[*oidc.IDTokenClaims]{}
		}

		tokenPath := c.OIDCTokenPath(name)

		if c.oidcTokens[name] == nil {
			if shared.PathExists(tokenPath) {
				content, err := os.ReadFile(tokenPath)
				if err != nil {
					return nil, err
				}

				var tokens oidc.Tokens[*oidc.IDTokenClaims]

				err = json.Unmarshal(content, &tokens)
				if err != nil {
					return nil, err
				}

				c.oidcTokens[name] = &tokens
			} else {
				c.oidcTokens[name] = &oidc.Tokens[*oidc.IDTokenClaims]{}
			}
		}

		args.OIDCTokens = c.oidcTokens[name]
	}

	// Stop here if no TLS involved
	if strings.HasPrefix(remote.Addr, "unix:") {
		return &args, nil
	}

	// Server certificate
	if shared.PathExists(c.ServerCertPath(name)) {
		content, err := os.ReadFile(c.ServerCertPath(name))
		if err != nil {
			return nil, err
		}

		args.TLSServerCert = string(content)
	}

	// Stop here if no client certificate involved
	if remote.Protocol == "simplestreams" || shared.ValueInSlice(remote.AuthType, []string{api.AuthenticationMethodOIDC}) {
		return &args, nil
	}

	// Client certificate
	if shared.PathExists(c.ConfigPath("client.crt")) {
		content, err := os.ReadFile(c.ConfigPath("client.crt"))
		if err != nil {
			return nil, err
		}

		args.TLSClientCert = string(content)
	}

	// Client CA
	if shared.PathExists(c.ConfigPath("client.ca")) {
		content, err := os.ReadFile(c.ConfigPath("client.ca"))
		if err != nil {
			return nil, err
		}

		args.TLSCA = string(content)
	}

	// Client key
	if shared.PathExists(c.ConfigPath("client.key")) {
		content, err := os.ReadFile(c.ConfigPath("client.key"))
		if err != nil {
			return nil, err
		}

		pemKey, _ := pem.Decode(content)
		// Golang has deprecated all methods relating to PEM encryption due to a vulnerability.
		// However, the weakness does not make PEM unsafe for our purposes as it pertains to password protection on the
		// key file (client.key is only readable to the user in any case), so we'll ignore deprecation.
		if x509.IsEncryptedPEMBlock(pemKey) { //nolint:staticcheck
			if c.PromptPassword == nil {
				return nil, fmt.Errorf("Private key is password protected and no helper was configured")
			}

			password, err := c.PromptPassword("client.crt")
			if err != nil {
				return nil, err
			}

			derKey, err := x509.DecryptPEMBlock(pemKey, []byte(password)) //nolint:staticcheck
			if err != nil {
				return nil, err
			}

			content = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: derKey})
		}

		args.TLSClientKey = string(content)
	}

	return &args, nil
}
