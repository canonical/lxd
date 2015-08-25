package lxd

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
)

// Config holds settings to be used by a client or daemon.
type Config struct {
	// DefaultRemote holds the remote daemon name from the Remotes map
	// that the client should communicate with by default.
	// If empty it defaults to "local".
	DefaultRemote string `yaml:"default-remote"`

	// Remotes defines a map of remote daemon names to the details for
	// communication with the named daemon.
	// The implicit "local" remote is always available and communicates
	// with the local daemon over a unix socket.
	Remotes map[string]RemoteConfig `yaml:"remotes"`
}

// RemoteConfig holds details for communication with a remote daemon.
type RemoteConfig struct {
	Addr string `yaml:"addr"`
}

var localRemote = RemoteConfig{Addr: "unix://" + shared.VarPath("unix.socket")}
var defaultRemote = map[string]RemoteConfig{"local": localRemote}

var ConfigDir = "$HOME/.config/lxc"
var configFileName = "config.yml"

func ConfigPath(file string) string {
	return os.ExpandEnv(path.Join(ConfigDir, file))
}

func ServerCertPath(name string) string {
	return path.Join(ConfigPath("servercerts"), fmt.Sprintf("%s.crt", name))
}

// LoadConfig reads the configuration from the config path.
func LoadConfig() (*Config, error) {
	data, err := ioutil.ReadFile(ConfigPath(configFileName))
	if os.IsNotExist(err) {
		// A missing file is equivalent to the default configuration.
		return &Config{
			Remotes:       defaultRemote,
			DefaultRemote: "local"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %v", err)
	}

	var c Config
	err = yaml.Unmarshal(data, &c)
	if err != nil {
		return nil, fmt.Errorf("cannot parse configuration: %v", err)
	}
	if c.Remotes == nil {
		c.Remotes = make(map[string]RemoteConfig)
	}

	return &c, nil
}

// SaveConfig writes the provided configuration to the config file.
func SaveConfig(c *Config) error {
	fname := ConfigPath(configFileName)

	// Ignore errors on these two calls. Create will report any problems.
	os.Remove(fname + ".new")
	os.Mkdir(filepath.Dir(fname), 0700)
	f, err := os.Create(fname + ".new")
	if err != nil {
		return fmt.Errorf("cannot create config file: %v", err)
	}

	// If there are any errors, do not leave it around.
	defer f.Close()
	defer os.Remove(fname + ".new")

	data, err := yaml.Marshal(c)
	_, err = f.Write(data)
	if err != nil {
		return fmt.Errorf("cannot write configuration: %v", err)
	}

	f.Close()
	err = shared.FileMove(fname+".new", fname)
	if err != nil {
		return fmt.Errorf("cannot rename temporary config file: %v", err)
	}
	return nil
}

func (c *Config) ParseRemoteAndContainer(raw string) (string, string) {
	result := strings.SplitN(raw, ":", 2)
	if len(result) == 1 {
		return c.DefaultRemote, result[0]
	}
	return result[0], result[1]
}

func (c *Config) ParseRemote(raw string) string {
	return strings.SplitN(raw, ":", 2)[0]
}
