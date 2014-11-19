package main

import (
	"fmt"
	"github.com/lxc/lxd"
)

type configCmd struct {
	httpAddr string
}

const configUsage = `
Manage configuration.

lxc config set [remote] password <newpwd>        Set admin password
`

func (c *configCmd) usage() string {
	return configUsage
}

func (c *configCmd) flags() {}

func (c *configCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	switch args[0] {

	case "set":
		action := args[1]
		if action == "password" {
			if len(args) != 3 {
				return errArgs
			}

			password := args[2]
			c, _, err := lxd.NewClient(config, "")
			if err != nil {
				return err
			}

			_, err = c.SetRemotePwd(password)
			return err
		}

		return fmt.Errorf("Only 'password' can be set currently")
	}
	return fmt.Errorf("Only admin password setting can be done currently")

}
