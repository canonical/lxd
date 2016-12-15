package main

import (
	"fmt"

	"github.com/lxc/lxd"
)

func cmdMigrateDumpSuccess(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("bad migrate dump success args %s", args)
	}

	c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
	if err != nil {
		return err
	}

	conn, err := c.Websocket(args[1], args[2])
	if err != nil {
		return err
	}
	conn.Close()

	return c.WaitForSuccess(args[1])
}
