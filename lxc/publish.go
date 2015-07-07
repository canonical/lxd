package main

import (
	"fmt"
	"strings"

	"github.com/chai2010/gettext-go/gettext"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
)

type publishCmd struct{}

func (c *publishCmd) showByDefault() bool {
	return true
}

func (c *publishCmd) usage() string {
	return gettext.Gettext(
		"Publish containers as images.\n" +
			"\n" +
			"lxc publish [remote:]container [remote:] [--alias=ALIAS]... [prop-key=prop-value]...\n")
}

var pAliases aliasList // aliasList defined in lxc/image.go
var makePublic bool

func (c *publishCmd) flags() {
	gnuflag.BoolVar(&makePublic, "public", false, gettext.Gettext("Make the image public"))
	gnuflag.Var(&pAliases, "alias", "New alias to define at target")
}

func (c *publishCmd) run(config *lxd.Config, args []string) error {
	var cRemote string
	var cName string
	iName := ""
	iRemote := ""
	properties := map[string]string{}
	firstprop := 1 // first property is arg[2] if arg[1] is image remote, else arg[1]

	if len(args) < 1 {
		return errArgs
	}

	cRemote, cName = config.ParseRemoteAndContainer(args[0])
	if len(args) >= 2 && !strings.Contains(args[1], "=") {
		firstprop = 2
		iRemote, iName = config.ParseRemoteAndContainer(args[1])
	}

	if cName == "" {
		return fmt.Errorf(gettext.Gettext("Container name is mandatory"))
	}
	if iName != "" {
		return fmt.Errorf(gettext.Gettext("There is no \"image name\".  Did you want an alias?"))
	}

	if cRemote != iRemote {
		/*
		 * Get the source remote to export the container over a websocket,
		 * pass that ws to the dest remote, and have it import it as an
		 * image
		 */
		return fmt.Errorf(gettext.Gettext("Publish to remote server is not supported yet"))
	}

	d, err := lxd.NewClient(config, iRemote)
	if err != nil {
		return err
	}

	for i := firstprop; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return errArgs
		}
		properties[entry[0]] = entry[1]
	}

	fp, err := d.ImageFromContainer(cName, makePublic, pAliases, properties)

	if err == nil {
		fmt.Printf("Container published with fingerprint %s\n", fp)
	}
	return err
}
