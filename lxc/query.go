package main

import (
	"encoding/json"
	"fmt"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type queryCmd struct {
	respWait bool
	respRaw  bool
	action   string
	data     string
}

func (c *queryCmd) showByDefault() bool {
	return false
}

func (c *queryCmd) usage() string {
	return i18n.G(
		`Usage: lxc query [-X <action>] [-d <data>] [--wait] [--raw] [<remote>:]<API path>

Send a raw query to LXD.

*Examples*
lxc query -X DELETE --wait /1.0/containers/c1
    Delete local container "c1".`)
}

func (c *queryCmd) flags() {
	gnuflag.BoolVar(&c.respWait, "wait", false, i18n.G("Wait for the operation to complete"))
	gnuflag.BoolVar(&c.respRaw, "raw", false, i18n.G("Print the raw response"))
	gnuflag.StringVar(&c.action, "X", "GET", i18n.G("Action (defaults to GET)"))
	gnuflag.StringVar(&c.data, "d", "", i18n.G("Input data"))
}

func (c *queryCmd) pretty(input interface{}) string {
	pretty, err := json.MarshalIndent(input, "", "\t")
	if err != nil {
		return fmt.Sprintf("%v", input)
	}

	return fmt.Sprintf("%s", pretty)
}

func (c *queryCmd) run(conf *config.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	// Parse the remote
	remote, path, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	// Attempt to connect
	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	// Guess the encoding of the input
	var data interface{}
	err = json.Unmarshal([]byte(c.data), &data)
	if err != nil {
		data = c.data
	}

	// Perform the query
	resp, _, err := d.RawQuery(c.action, path, data, "")
	if err != nil {
		return err
	}

	if c.respWait && resp.Operation != "" {
		resp, _, err = d.RawQuery("GET", fmt.Sprintf("%s/wait", resp.Operation), "", "")
		if err != nil {
			return err
		}

		op := api.Operation{}
		err = json.Unmarshal(resp.Metadata, &op)
		if err == nil && op.Err != "" {
			return fmt.Errorf(op.Err)
		}
	}

	if c.respRaw {
		fmt.Println(c.pretty(resp))
	} else if resp.Metadata != nil && string(resp.Metadata) != "{}" {
		var content interface{}
		err := json.Unmarshal(resp.Metadata, &content)
		if err != nil {
			return err
		}

		if content != nil {
			fmt.Println(c.pretty(content))
		}
	}

	return nil
}
