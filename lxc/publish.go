package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/i18n"
	"github.com/lxc/lxd/shared/gnuflag"
)

type publishCmd struct{}

func (c *publishCmd) showByDefault() bool {
	return true
}

func (c *publishCmd) usage() string {
	return i18n.G(
		`Publish containers as images.

lxc publish [remote:]container [remote:] [--alias=ALIAS]... [prop-key=prop-value]...`)
}

var pAliases aliasList // aliasList defined in lxc/image.go
var makePublic bool

func (c *publishCmd) flags() {
	gnuflag.BoolVar(&makePublic, "public", false, i18n.G("Make the image public"))
	gnuflag.Var(&pAliases, "alias", i18n.G("New alias to define at target"))
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
	} else {
		iRemote, iName = config.ParseRemoteAndContainer("")
	}

	if cName == "" {
		return fmt.Errorf(i18n.G("Container name is mandatory"))
	}
	if iName != "" {
		return fmt.Errorf(i18n.G("There is no \"image name\".  Did you want an alias?"))
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

	var fp string

	// Optimized local publish
	if cRemote == iRemote {
		fp, err = d.ImageFromContainer(cName, makePublic, pAliases, properties)
		if err != nil {
			return err
		}
		fmt.Printf(i18n.G("Container published with fingerprint: %s")+"\n", fp)
		return nil
	}

	s, err := lxd.NewClient(config, cRemote)
	if err != nil {
		return err
	}

	fp, err = s.ImageFromContainer(cName, false, nil, properties)
	if err != nil {
		return err
	}
	defer s.DeleteImage(fp)

	err = s.CopyImage(fp, d, false, pAliases, makePublic, nil)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Container published with fingerprint: %s")+"\n", fp)

	return nil
}
