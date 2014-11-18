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
		if len(args) != 4 && len(args) != 3 {
			return errArgs
		}

		action := args[1]
		if len(args) == 4 {
			action = args[2]
		}
		if action == "password" {
			server := ""
			password := args[2]
			if len(args) == 4 {
				servername := fmt.Sprintf("%s:", args[1])
				r, ok := config.Remotes[servername]
				if !ok {
					return fmt.Errorf("remote .%s. doesn't exist", servername)
				}
				server = r.Addr
				fmt.Printf("using servername .%s.", servername)
				password = args[3]
			}

			c, _, err := lxd.NewClient(config, server)
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
