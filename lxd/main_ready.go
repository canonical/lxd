package main

import (
	"net/http"

	"github.com/lxc/lxd"
)

func cmdReady() error {
	c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", c.BaseURL+"/internal/ready", nil)
	if err != nil {
		return err
	}

	raw, err := c.Http.Do(req)
	if err != nil {
		return err
	}

	_, err = lxd.HoistResponse(raw, lxd.Sync)
	if err != nil {
		return err
	}

	return nil
}
