package main

import (
	"fmt"
	"strings"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

type launchCmd struct{}

func (c *launchCmd) showByDefault() bool {
	return false
}

func (c *launchCmd) usage() string {
	return gettext.Gettext(
		"lxc launch ubuntu [<name>]\n" +
			"\n" +
			"Launches a container using the specified image and name.\n")
}

func (c *launchCmd) flags() {}

func (c *launchCmd) run(config *lxd.Config, args []string) error {

	if len(args) > 2 || len(args) < 1 {
		return errArgs
	}

	if args[0] != "ubuntu" {
		return fmt.Errorf(gettext.Gettext("Only the default ubuntu image is supported. Try `lxc launch ubuntu foo`."))
	}

	var name string
	var remote string
	if len(args) == 2 {
		remote, name = config.ParseRemoteAndContainer(args[1])
	} else {
		name = ""
		remote = ""
	}

	fmt.Printf("Creating container...")
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	resp, err := d.Init(name)
	if err != nil {
		return err
	}

	if name == "" {
		if resp.Resources == nil {
			return fmt.Errorf(gettext.Gettext("didn't get any affected resources from server"))
		}

		containers, ok := resp.Resources["containers"]
		if !ok || len(containers) == 0 {
			return fmt.Errorf(gettext.Gettext("didn't get any affected resources from server"))
		}

		var version string
		toScan := strings.Replace(containers[0], "/", " ", -1)
		count, err := fmt.Sscanf(toScan, " %s containers %s", &version, &name)
		if err != nil {
			return err
		}

		if count != 2 {
			return fmt.Errorf(gettext.Gettext("bad number of things scanned from resource"))
		}

		if version != shared.Version {
			return fmt.Errorf(gettext.Gettext("got bad version"))
		}
	}

	if err = d.WaitForSuccess(resp.Operation); err != nil {
		return err
	}
	fmt.Println("done")

	fmt.Printf("Starting container...")
	resp, err = d.Action(name, shared.Start, -1, false)
	if err != nil {
		return err
	}

	err = d.WaitForSuccess(resp.Operation)
	fmt.Println("done")

	return err
}
