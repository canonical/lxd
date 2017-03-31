package config

import (
	"fmt"
	"path/filepath"
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
