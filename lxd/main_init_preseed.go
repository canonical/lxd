package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
)

func (c *cmdInit) RunPreseed(cmd *cobra.Command, args []string, d lxd.InstanceServer) (*api.InitPreseed, error) {
	// Read the YAML
	bytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("Failed to read from stdin: %w", err)
	}

	// Parse the YAML
	config := api.InitPreseed{}
	err = yaml.Unmarshal(bytes, &config)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse the preseed: %w", err)
	}

	return &config, nil
}
