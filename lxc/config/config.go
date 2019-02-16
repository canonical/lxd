package config

import (
	"fmt"
	"path/filepath"

	"github.com/juju/persistent-cookiejar"
)

// Config holds settings to be used by a client or daemon
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

	// ProjectOverride allows overriding the default project for container queries
	ProjectOverride string `yaml:"-"`

	// Cookie jars
	cookieJars map[string]*cookiejar.Jar
}

// ConfigPath returns a joined path of the configuration directory and passed arguments
func (c *Config) ConfigPath(paths ...string) string {
	path := []string{c.ConfigDir}
	path = append(path, paths...)

	return filepath.Join(path...)
}

// CookiesPath returns the path for the remote's cookie jar
func (c *Config) CookiesPath(remote string) string {
	return c.ConfigPath("jars", remote)
}

// ServerCertPath returns the path for the remote's server certificate
func (c *Config) ServerCertPath(remote string) string {
	return c.ConfigPath("servercerts", fmt.Sprintf("%s.crt", remote))
}

// SaveCookies saves cookies to file
func (c *Config) SaveCookies() {
	for _, jar := range c.cookieJars {
		jar.Save()
	}
}

// NewConfig returns a Config, optionally using default remotes.
func NewConfig(configDir string, defaults bool) *Config {
	config := &Config{ConfigDir: configDir}
	if defaults {
		config.Remotes = DefaultRemotes
		config.DefaultRemote = "local"
	}

	return config
}
