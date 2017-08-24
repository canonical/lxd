package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
)

func cmdMigrateDumpSuccess(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("bad migrate dump success args %s", args)
	}

	c, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/websocket?secret=%s", strings.TrimPrefix(args[1], "/1.0"), args[2])
	conn, err := c.RawWebsocket(url)
	if err != nil {
		return err
	}
	conn.Close()

	resp, _, err := c.RawQuery("GET", fmt.Sprintf("%s/wait", args[1]), nil, "")
	if err != nil {
		return err
	}

	op, err := resp.MetadataAsOperation()
	if err != nil {
		return err
	}

	if op.StatusCode == api.Success {
		return nil
	}

	return fmt.Errorf(op.Err)
}
