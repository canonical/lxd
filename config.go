package lxd

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/lxc/lxd/3rdParty/gopkg.in/yaml.v2"
)

// Config holds settings to be used by a client or daemon.
type Config struct {
	// TestOption is used only for testing purposes.
	TestOption string `yaml:"test-option,omitempty"`

	// DefaultRemote holds the remote daemon name from the Remotes map
	// that the client should communicate with by default.
	// If empty it defaults to "local".
	DefaultRemote string `yaml:"default-remote"`

	// Remotes defines a map of remote daemon names to the details for
	// communication with the named daemon.
	// The implicit "local" remote is always available and communicates
	// with the local daemon over a unix socket.
	Remotes map[string]RemoteConfig `yaml:"remotes"`

	// ListenAddr defines an alternative address for the local daemon
	// to listen on. If empty, the daemon will listen only on the local
	// unix socket address.
	ListenAddr string `yaml:"listen-addr"`
}

// RemoteConfig holds details for communication with a remote daemon.
type RemoteConfig struct {
	Addr string `yaml:"addr"`
}

func configPath(file string) string {
	return os.ExpandEnv(fmt.Sprintf("$HOME/.config/lxc/%s", file))
}

func renderConfigPath(path string) string {
	if path == "" {
		path = configPath("config.yml")
	}

	return os.ExpandEnv(path)
}

// LoadConfig reads the configuration from the config path.
func LoadConfig(configPath string) (*Config, error) {
	data, err := ioutil.ReadFile(renderConfigPath(configPath))
	if os.IsNotExist(err) {
		// A missing file is equivalent to the default configuration.
		return &Config{}, nil
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

// SaveConfig writes the provided configuration to the config path.
func SaveConfig(configPath string, c *Config) error {
	fname := renderConfigPath(configPath)

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
	err = os.Rename(fname+".new", fname)
	if err != nil {
		return fmt.Errorf("cannot rename temporary config file: %v", err)
	}
	return nil
}
