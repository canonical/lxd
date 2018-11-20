package config

import (
	"fmt"
	"path/filepath"

	"github.com/juju/persistent-cookiejar"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
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

	authInteractor []httpbakery.Interactor

	cookiejar *cookiejar.Jar
}

// ConfigPath returns a joined path of the configuration directory and passed arguments
func (c *Config) ConfigPath(paths ...string) string {
	path := []string{c.ConfigDir}
	path = append(path, paths...)

	return filepath.Join(path...)
}

// ServerCertPath returns the path for the remote's server certificate
func (c *Config) ServerCertPath(remote string) string {
	return c.ConfigPath("servercerts", fmt.Sprintf("%s.crt", remote))
}

// SetAuthInteractor sets the interactor for macaroon-based authorization
func (c *Config) SetAuthInteractor(interactor []httpbakery.Interactor) {
	c.authInteractor = interactor
}

// SaveCookies saves cookies to file
func (c *Config) SaveCookies() {
	if c.cookiejar != nil {
		c.cookiejar.Save()
	}
}

// NewConfig returns a Config, optionally using default remotes.
func NewConfig(configDir string, defaults bool) *Config {
	config := &Config{ConfigDir: configDir}
	if defaults {
		config.Remotes = DefaultRemotes
		config.DefaultRemote = "local"
	}

	if configDir != "" {
		config.cookiejar, _ = cookiejar.New(
			&cookiejar.Options{
				Filename: filepath.Join(configDir, "cookies")})
	}

	return config
}
