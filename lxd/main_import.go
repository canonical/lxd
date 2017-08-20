package main

import (
	"fmt"

	"github.com/lxc/lxd/client"
)

func cmdImport(args *Args) error {
	if len(args.Params) < 1 {
		return fmt.Errorf("please specify a container to import")
	}
	name := args.Params[0]
	req := map[string]interface{}{
		"name":  name,
		"force": args.Force,
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
