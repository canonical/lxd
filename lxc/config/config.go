package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/juju/persistent-cookiejar"
	"github.com/zitadel/oidc/v3/pkg/oidc"

	"github.com/canonical/lxd/shared"
)

// Config holds settings to be used by a client or daemon.
type Config struct {
	// DefaultRemote holds the remote daemon name from the Remotes map
	// that the client should communicate with by default
	DefaultRemote string `yaml:"default-remote"`

	// Remotes defines a map of remote daemon names to the details for
	// communication with the named daemon
	Remotes map[string]Remote `yaml:"remotes"`

	// Command line aliases for `lxc`
	Aliases map[string]string `yaml:"aliases"`

	// Configuration directory
	ConfigDir string `yaml:"-"`

	// The UserAgent to pass for all queries
	UserAgent string `yaml:"-"`

	// PromptPassword is a helper function used when encountering an encrypted key
	PromptPassword func(filename string) (string, error) `yaml:"-"`

	// ProjectOverride allows overriding the default project
	ProjectOverride string `yaml:"-"`

	// OIDC tokens
	oidcTokens map[string]*oidc.Tokens[*oidc.IDTokenClaims]

	// cookie jars
	cookieJars map[string]*cookiejar.Jar
}

// GlobalConfigPath returns a joined path of the global configuration directory and passed arguments.
func (c *Config) GlobalConfigPath(paths ...string) string {
	configDir := "/etc/lxd"
	if os.Getenv("LXD_GLOBAL_CONF") != "" {
		configDir = os.Getenv("LXD_GLOBAL_CONF")
	}

	path := []string{configDir}
	path = append(path, paths...)

	return filepath.Join(path...)
}

// ConfigPath returns a joined path of the configuration directory and passed arguments.
func (c *Config) ConfigPath(paths ...string) string {
	path := []string{c.ConfigDir}
	path = append(path, paths...)

	return filepath.Join(path...)
}

// ServerCertPath returns the path for the remote's server certificate.
func (c *Config) ServerCertPath(remote string) string {
	if c.Remotes[remote].Global {
		return c.GlobalConfigPath("servercerts", remote+".crt")
	}

	return c.ConfigPath("servercerts", remote+".crt")
}

// OIDCTokenPath returns the path for the remote's OIDC tokens.
func (c *Config) OIDCTokenPath(remote string) string {
	return c.ConfigPath("oidctokens", remote+".json")
}

// SaveOIDCTokens saves OIDC tokens to disk.
func (c *Config) SaveOIDCTokens() {
	if len(c.oidcTokens) == 0 {
		return
	}

	tokenParentPath := c.ConfigPath("oidctokens")

	if !shared.PathExists(tokenParentPath) {
		_ = os.MkdirAll(tokenParentPath, 0750)
	}

	for remote, tokens := range c.oidcTokens {
		tokenPath := c.OIDCTokenPath(remote)
		data, _ := json.Marshal(tokens)
		_ = os.WriteFile(tokenPath, data, 0600)
	}
}

// CookiePath returns the path for saving the session cookie.
func (c *Config) CookiePath(remote string) string {
	return c.ConfigPath("cookies", remote+".json")
}

// SaveCookies saves any cookies in the cookiejar to disk.
func (c *Config) SaveCookies() {
	if len(c.cookieJars) == 0 {
		return
	}

	cookieParentPath := c.ConfigPath("cookies")

	if !shared.PathExists(cookieParentPath) {
		_ = os.MkdirAll(cookieParentPath, 0750)
	}

	for _, jar := range c.cookieJars {
		if jar == nil {
			continue
		}

		_ = jar.Save()
	}
}

// NewConfig returns a Config, optionally using default remotes.
func NewConfig(configDir string, defaults bool) *Config {
	var config *Config
	if defaults {
		config = DefaultConfig()
	} else {
		config = &Config{}
	}

	config.ConfigDir = configDir

	return config
}
