package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
)

func (c *cmdInit) RunPreseed(cmd *cobra.Command, args []string, d lxd.InstanceServer) (*cmdInitData, error) {
	// Read the YAML
	bytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("Failed to read from stdin: %w", err)
	}

	// Parse the YAML
	config := cmdInitData{}
	err = yaml.Unmarshal(bytes, &config)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse the preseed: %w", err)
	}

	return &config, nil
}
