package main

import (
	"github.com/lxc/lxd/client"
)

func cmdReady(args *Args) error {
	c, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	_, _, err = c.RawQuery("PUT", "/internal/ready", nil, "")
	if err != nil {
		return err
	}

	return nil
}
