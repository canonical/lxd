package main

import (
	"fmt"
	"github.com/lxc/lxd"
)

type remoteCmd struct {
	httpAddr string
}

const remoteUsage = `
Manage remote lxc servers.

lxc remote add <name> <url>        Add the remote <name> at <url>.
lxc remote rm <name>               Remove the remote <name>.
lxc remote list                    List all remotes.
lxc remote rename <old> <new>      Rename remote <old> to <new>.
lxc remote set-url <name> <url>    Update <name>'s url to <url>.
lxc remote set-default <name>      Set the default remote.
`

func (c *remoteCmd) usage() string {
	return remoteUsage
}

func (c *remoteCmd) flags() {}

func (c *remoteCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	switch args[0] {
	case "add":
		if len(args) != 3 {
			return errArgs
		}

		if rc, ok := config.Remotes[args[1]]; ok {
			return fmt.Errorf("remote %s exists as <%s>", args[1], rc.Addr)
		}

		if config.Remotes == nil {
			config.Remotes = make(map[string]lxd.RemoteConfig)
		}
		config.Remotes[args[1]] = lxd.RemoteConfig{args[2]}
	case "rm":
		if len(args) != 2 {
			return errArgs
		}

		if _, ok := config.Remotes[args[1]]; !ok {
			return fmt.Errorf("remote %s doesn't exist", args[1])
		}

		if config.DefaultRemote == args[1] {
			config.DefaultRemote = ""
		}

		delete(config.Remotes, args[1])
	case "list":
		for name, rc := range config.Remotes {
			fmt.Println(fmt.Sprintf("%s <%s>", name, rc.Addr))
		}
		/* Here, we don't need to save since we didn't actually modify
		 * anything, so just return. */
		return nil
	case "rename":
		if len(args) != 3 {
			return errArgs
		}

		rc, ok := config.Remotes[args[1]]
		if !ok {
			return fmt.Errorf("remote %s doesn't exist", args[1])
		}

		if _, ok := config.Remotes[args[2]]; ok {
			return fmt.Errorf("remote %s already exists", args[2])
		}

		config.Remotes[args[2]] = rc
		delete(config.Remotes, args[1])

		if config.DefaultRemote == args[1] {
			config.DefaultRemote = args[2]
		}
	case "set-url":
		if len(args) != 3 {
			return errArgs
		}
		_, ok := config.Remotes[args[1]]
		if !ok {
			return fmt.Errorf("remote %s doesn't exist", args[1])
		}
		config.Remotes[args[1]] = lxd.RemoteConfig{args[2]}
	case "set-default":
		if len(args) != 2 {
			return errArgs
		}

		_, ok := config.Remotes[args[1]]
		if !ok {
			return fmt.Errorf("remote %s doesn't exist", args[1])
		}
		config.DefaultRemote = args[1]
	case "get-default":
		if len(args) != 1 {
			return errArgs
		}
		fmt.Println(config.DefaultRemote)
		return nil
	}

	return lxd.SaveConfig(*configPath, config)
}
