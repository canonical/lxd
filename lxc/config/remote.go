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
	"slices"
	"strings"

	"github.com/zitadel/oidc/v3/pkg/oidc"

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
	remote, object, found := strings.Cut(raw, ":")
	if !found {
		return c.DefaultRemote, raw, nil
	}

	_, ok := c.Remotes[remote]
	if !ok {
		// Attempt to play nice with snapshots containing ":"
		if shared.IsSnapshot(raw) && shared.IsSnapshot(remote) {
			return c.DefaultRemote, raw, nil
		}

		return "", "", fmt.Errorf("The remote \"%s\" doesn't exist", remote)
	}

	return remote, object, nil
}

// GetInstanceServer returns a lxd.InstanceServer for the remote with the given name.
func (c *Config) GetInstanceServer(name string) (lxd.InstanceServer, error) {
	return c.GetInstanceServerWithConnectionArgs(name, nil)
}

// GetInstanceServerWithConnectionArgs returns a lxd.InstanceServer for the remote with the given name. Any
// populated fields of the given connection arguments override the default connection arguments for the remote.
func (c *Config) GetInstanceServerWithConnectionArgs(name string, inArgs *lxd.ConnectionArgs) (lxd.InstanceServer, error) {
	remote, err := c.getPrivateRemoteByName(name)
	if err != nil {
		return nil, err
	}

	// Get connection arguments
	args, err := c.getConnectionArgs(name)
	if err != nil {
		return nil, err
	}

	if inArgs != nil {
		args = mergeConnectionArgs(*args, *inArgs)
	}

	return c.connectRemote(*remote, args)
}

// mergeConnectionArgs returns a copy of baseArgs where each field is overwritten with fields from additionalArgs (if
// non-zero). This is useful for when the CLI needs the base connection arguments for a remote, but also needs to set
// a transport wrapper for extracting headers, or to skip calling GET /1.0 on each API call.
func mergeConnectionArgs(baseArgs lxd.ConnectionArgs, additionalArgs lxd.ConnectionArgs) *lxd.ConnectionArgs {
	args := baseArgs

	if additionalArgs.TLSServerCert != "" {
		args.TLSServerCert = additionalArgs.TLSServerCert
	}

	if additionalArgs.TLSClientCert != "" {
		args.TLSClientCert = additionalArgs.TLSClientCert
	}

	if additionalArgs.TLSClientKey != "" {
		args.TLSClientKey = additionalArgs.TLSClientKey
	}

	if additionalArgs.TLSCA != "" {
		args.TLSCA = additionalArgs.TLSCA
	}

	if additionalArgs.UserAgent != "" {
		args.UserAgent = additionalArgs.UserAgent
	}

	if additionalArgs.AuthType != "" {
		args.AuthType = additionalArgs.AuthType
	}

	if additionalArgs.Proxy != nil {
		args.Proxy = additionalArgs.Proxy
	}

	if additionalArgs.HTTPClient != nil {
		args.HTTPClient = additionalArgs.HTTPClient
	}

	if additionalArgs.TransportWrapper != nil {
		args.TransportWrapper = additionalArgs.TransportWrapper
	}

	if additionalArgs.InsecureSkipVerify {
		args.InsecureSkipVerify = additionalArgs.InsecureSkipVerify
	}

	if additionalArgs.CookieJar != nil {
		args.CookieJar = additionalArgs.CookieJar
	}

	if additionalArgs.OIDCTokens != nil {
		args.OIDCTokens = additionalArgs.OIDCTokens
	}

	if additionalArgs.SkipGetServer {
		args.SkipGetServer = additionalArgs.SkipGetServer
	}

	if additionalArgs.CachePath != "" {
		args.CachePath = additionalArgs.CachePath
	}

	if additionalArgs.CacheExpiry != 0 {
		args.CacheExpiry = additionalArgs.CacheExpiry
	}

	return &args
}

// getPrivateRemoteByName returns the Remote with the given name and ensures that the remote is not public.
func (c *Config) getPrivateRemoteByName(name string) (*Remote, error) {
	remote, err := c.getPublicRemoteByName(name)
	if err != nil {
		return nil, err
	}

	// Check the remote is private.
	if remote.Public || remote.Protocol == "simplestreams" {
		return nil, errors.New("The remote isn't a private LXD server")
	}

	return remote, nil
}

// getPublicRemoteByName returns the Remote with the given name.
func (c *Config) getPublicRemoteByName(name string) (*Remote, error) {
	// Handle "local" on non-Linux
	if name == "local" && runtime.GOOS != "linux" {
		return nil, ErrNotLinux
	}

	// Get the remote
	remote, ok := c.Remotes[name]
	if !ok {
		return nil, fmt.Errorf("The remote \"%s\" doesn't exist", name)
	}

	return &remote, nil
}

// connectRemote returns a lxd.InstanceServer for the given Remote and configures it with the given lxd.ConnectionArgs.
func (c *Config) connectRemote(remote Remote, args *lxd.ConnectionArgs) (lxd.InstanceServer, error) {
	// Unix socket
	after, ok := strings.CutPrefix(remote.Addr, "unix:")
	if ok {
		d, err := lxd.ConnectLXDUnix(strings.TrimPrefix(after, "//"), args)
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

	// HTTPS
	if !slices.Contains([]string{api.AuthenticationMethodOIDC}, remote.AuthType) && (args.TLSClientCert == "" || args.TLSClientKey == "") {
		return nil, errors.New("Missing TLS client certificate and key")
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
	remote, err := c.getPublicRemoteByName(name)
	if err != nil {
		return nil, err
	}

	// Get connection arguments
	args, err := c.getConnectionArgs(name)
	if err != nil {
		return nil, err
	}

	// Unix socket
	after, ok := strings.CutPrefix(remote.Addr, "unix:")
	if ok {
		d, err := lxd.ConnectLXDUnix(strings.TrimPrefix(after, "//"), args)
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
	if remote.Protocol == "simplestreams" || slices.Contains([]string{api.AuthenticationMethodOIDC}, remote.AuthType) {
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
				return nil, errors.New("Private key is password protected and no helper was configured")
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
