package main

import (
	"fmt"
	"net/url"

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

	conn, err := c.RawWebsocket(fmt.Sprintf("/1.0/operations/%s/websocket?%s", args[1], url.Values{"secret": []string{args[2]}}))
	if err != nil {
		return err
	}
	conn.Close()

	resp, _, err := c.RawQuery("GET", fmt.Sprintf("/1.0/operations/%s/wait", args[1]), nil, "")
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
