package lxd

//
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

	// Command line aliases for `lxc`
	Aliases map[string]string `yaml:"aliases"`

	// This is the path to the config directory, so the client can find
	// previously stored server certs, give good error messages, and save
	// new server certs, etc.
	//
	// We don't need to store it, because of course once we've loaded this
	// structure we already know where it is :)
	ConfigDir string `yaml:"-"`
}

// RemoteConfig holds details for communication with a remote daemon.
type RemoteConfig struct {
	Addr     string `yaml:"addr"`
	Public   bool   `yaml:"public"`
	Protocol string `yaml:"protocol,omitempty"`
	Static   bool   `yaml:"-"`
}

var LocalRemote = RemoteConfig{
	Addr:   "unix://",
	Static: true,
	Public: false}

var ImagesRemote = RemoteConfig{
	Addr:     "https://images.linuxcontainers.org",
	Public:   true,
	Protocol: "simplestreams"}

var UbuntuRemote = RemoteConfig{
	Addr:     "https://cloud-images.ubuntu.com/releases",
	Static:   true,
	Public:   true,
	Protocol: "simplestreams"}

var UbuntuDailyRemote = RemoteConfig{
	Addr:     "https://cloud-images.ubuntu.com/daily",
	Static:   true,
	Public:   true,
	Protocol: "simplestreams"}

var StaticRemotes = map[string]RemoteConfig{
	"local":        LocalRemote,
	"ubuntu":       UbuntuRemote,
	"ubuntu-daily": UbuntuDailyRemote}

var DefaultRemotes = map[string]RemoteConfig{
	"images":       ImagesRemote,
	"local":        LocalRemote,
	"ubuntu":       UbuntuRemote,
	"ubuntu-daily": UbuntuDailyRemote}

var DefaultConfig = Config{
	Remotes:       DefaultRemotes,
	DefaultRemote: "local"}

// LoadConfig reads the configuration from the config path; if the path does
// not exist, it returns a default configuration.
func LoadConfig(path string) (*Config, error) {
	data, err := ioutil.ReadFile(path)
	if os.IsNotExist(err) {
		// A missing file is equivalent to the default configuration.
		withPath := DefaultConfig
		withPath.ConfigDir = filepath.Dir(path)
		return &withPath, nil
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
	c.ConfigDir = filepath.Dir(path)

	for k, v := range StaticRemotes {
		c.Remotes[k] = v
	}

	// NOTE: Remove this once we only see a small fraction of non-simplestreams users
	// Upgrade users to the "simplestreams" protocol
	images, ok := c.Remotes["images"]
	if ok && images.Protocol != ImagesRemote.Protocol && images.Addr == ImagesRemote.Addr {
		c.Remotes["images"] = ImagesRemote
		SaveConfig(&c, path)
	}

	return &c, nil
}

// SaveConfig writes the provided configuration to the config file.
func SaveConfig(c *Config, fname string) error {
	for k := range StaticRemotes {
		delete(c.Remotes, k)
	}

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

func (c *Config) ConfigPath(file string) string {
	return path.Join(c.ConfigDir, file)
}

func (c *Config) ServerCertPath(name string) string {
	return path.Join(c.ConfigDir, "servercerts", fmt.Sprintf("%s.crt", name))
}
