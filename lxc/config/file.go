package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
)

// LoadConfig reads the configuration from the config path; if the path does
// not exist, it returns a default configuration.
func LoadConfig(path string) (*Config, error) {
	// Open the config file
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Unable to read the configuration file: %v", err)
	}

	// Decode the yaml document
	c := Config{}
	err = yaml.Unmarshal(content, &c)
	if err != nil {
		return nil, fmt.Errorf("Unable to decode the configuration: %v", err)
	}

	// Set default values
	if c.Remotes == nil {
		c.Remotes = make(map[string]Remote)
	}
	c.ConfigDir = filepath.Dir(path)

	// Apply the static remotes
	for k, v := range StaticRemotes {
		c.Remotes[k] = v
	}

	// NOTE: Remove this once we only see a small fraction of non-simplestreams users
	// Upgrade users to the "simplestreams" protocol
	images, ok := c.Remotes["images"]
	if ok && images.Protocol != ImagesRemote.Protocol && images.Addr == ImagesRemote.Addr {
		c.Remotes["images"] = ImagesRemote
		c.SaveConfig(path)
	}

	return &c, nil
}

// SaveConfig writes the provided configuration to the config file.
func (c *Config) SaveConfig(path string) error {
	// Create a new copy for the config file
	conf := Config{}
	err := shared.DeepCopy(c, &conf)
	if err != nil {
		return fmt.Errorf("Unable to copy the configuration: %v", err)
	}

	// Remove the static remotes
	for k := range StaticRemotes {
		delete(conf.Remotes, k)
	}

	// Create the config file (or truncate an existing one)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("Unable to create the configuration file: %v", err)
	}
	defer f.Close()

	// Write the new config
	data, err := yaml.Marshal(c)
	_, err = f.Write(data)
	if err != nil {
		return fmt.Errorf("Unable to write the configuration: %v", err)
	}

	return nil
}
