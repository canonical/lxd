package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

func cmdImport(args []string) error {
	name := args[1]
	b := shared.Jmap{
		"name":  name,
		"force": *argForce,
	}

	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(b)
	if err != nil {
		return err
	}

	c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/internal/containers", c.BaseURL)

	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", version.UserAgent)
	req.Header.Set("Content-Type", "application/json")

	raw, err := c.Http.Do(req)
	_, err = lxd.HoistResponse(raw, api.SyncResponse)
	if err != nil {
		return err
	}

	return nil
}
