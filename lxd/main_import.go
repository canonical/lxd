package main

import (
	"fmt"
	"net/http"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared/api"
)

func cmdImport(args []string) error {
	name := args[1]

	c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/internal/containers?target=%s", c.BaseURL, name)

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return err
	}

	raw, err := c.Http.Do(req)
	_, err = lxd.HoistResponse(raw, api.SyncResponse)
	if err != nil {
		return err
	}

	return nil
}
