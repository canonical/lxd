package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdQuery struct {
	global *cmdGlobal

	flagRespWait bool
	flagRespRaw  bool
	flagAction   string
	flagData     string
}

func (c *cmdQuery) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("query [<remote>:]<API path>")
	cmd.Short = i18n.G("Send a raw query to LXD")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Send a raw query to LXD`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc query -X DELETE --wait /1.0/containers/c1
    Delete local container "c1".`))
	cmd.Hidden = true

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagRespWait, "wait", false, i18n.G("Wait for the operation to complete"))
	cmd.Flags().BoolVar(&c.flagRespRaw, "raw", false, i18n.G("Print the raw response"))
	cmd.Flags().StringVarP(&c.flagAction, "request", "X", "GET", i18n.G("Action (defaults to GET)")+"``")
	cmd.Flags().StringVarP(&c.flagData, "data", "d", "", i18n.G("Input data")+"``")

	return cmd
}

func (c *cmdQuery) pretty(input interface{}) string {
	pretty, err := json.MarshalIndent(input, "", "\t")
	if err != nil {
		return fmt.Sprintf("%v", input)
	}

	return fmt.Sprintf("%s", pretty)
}

func (c *cmdQuery) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
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
	err = json.Unmarshal([]byte(c.flagData), &data)
	if err != nil {
		data = c.flagData
	}

	// Perform the query
	resp, _, err := d.RawQuery(c.flagAction, path, data, "")
	if err != nil {
		cleanErr := err

		// Lets assume the endpoint is raw output
		// Get a raw http client
		httpClient, err := d.GetHTTPClient()
		if err != nil {
			return err
		}

		// Get the URL prefix
		httpInfo, err := d.GetConnectionInfo()
		if err != nil {
			return err
		}

		// Setup the request
		req, err := http.NewRequest(c.flagAction, fmt.Sprintf("%s%s", httpInfo.URL, path), bytes.NewReader([]byte(c.flagData)))
		if err != nil {
			return err
		}

		// Set the encoding accordingly
		req.Header.Set("Content-Type", "plain/text")

		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			return cleanErr
		}

		content, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		fmt.Print(string(content))

		return nil
	}

	if c.flagRespWait && resp.Operation != "" {
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

	if c.flagRespRaw {
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
