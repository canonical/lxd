package main

import (
	"io/ioutil"
	"os"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
)

func (c *cmdInit) RunPreseed(cmd *cobra.Command, args []string, d lxd.ContainerServer) (*cmdInitData, error) {
	// Read the YAML
	bytes, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to read from stdin")
	}

	// Parse the YAML
	config := cmdInitData{}
	err = yaml.Unmarshal(bytes, &config)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to parse the preseed")
	}

	return &config, nil
}
