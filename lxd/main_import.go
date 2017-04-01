package main

import (
	"github.com/lxc/lxd/client"
)

func cmdImport(args []string) error {
	name := args[1]
	req := map[string]interface{}{
		"name":  name,
		"force": *argForce,
	}

	c, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	_, _, err = c.RawQuery("POST", "/internal/containers", req, "")
	if err != nil {
		return err
	}

	return nil
}
