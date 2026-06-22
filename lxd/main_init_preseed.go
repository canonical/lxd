package main

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/canonical/lxd/shared/api"
)

func (c *cmdInit) runPreseed() (*api.InitPreseed, error) {
	// Read the YAML
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("Failed reading from stdin: %w", err)
	}

	// Parse the YAML
	config := api.InitPreseed{}
	// Use strict checking to notify about unknown keys.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	err = dec.Decode(&config)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing the preseed: %w", err)
	}

	return &config, nil
}
