package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/version"
)

type launchCmd struct {
	init initCmd
}

func (c *launchCmd) showByDefault() bool {
	return true
}

func (c *launchCmd) usage() string {
	return i18n.G(
		`Usage: lxc launch [<remote>:]<image> [<remote>:][<name>] [--ephemeral|-e] [--profile|-p <profile>...] [--config|-c <key=value>...]

Create and start containers from images.

Not specifying -p will result in the default profile.
Specifying "-p" with no argument will result in no profile.

Examples:
    lxc launch ubuntu:16.04 u1`)
}

func (c *launchCmd) flags() {
	c.init = initCmd{}
	c.init.flags()
}

func (c *launchCmd) run(conf *config.Config, args []string) error {
	if len(args) > 2 || len(args) < 1 {
		return errArgs
	}

	iremote, image, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	var name string
	var remote string
	if len(args) == 2 {
		remote, name, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}
	} else {
		remote, name, err = conf.ParseRemote("")
		if err != nil {
			return err
		}
	}

	d, err := lxd.NewClient(conf.Legacy(), remote)
	if err != nil {
		return err
	}

	/*
	 * initRequestedEmptyProfiles means user requested empty
	 * !initRequestedEmptyProfiles but len(profArgs) == 0 means use profile default
	 */
	var resp *api.Response
	profiles := []string{}
	for _, p := range c.init.profArgs {
		profiles = append(profiles, p)
	}

	iremote, image = c.init.guessImage(conf, d, remote, iremote, image)

	if !initRequestedEmptyProfiles && len(profiles) == 0 {
		resp, err = d.Init(name, iremote, image, nil, configMap, c.init.ephem)
	} else {
		resp, err = d.Init(name, iremote, image, &profiles, configMap, c.init.ephem)
	}

	if err != nil {
		return err
	}

	progress := ProgressRenderer{}
	c.init.initProgressTracker(d, &progress, resp.Operation)

	if name == "" {
		op, err := resp.MetadataAsOperation()
		if err != nil {
			progress.Done("")
			return fmt.Errorf(i18n.G("didn't get any affected image, container or snapshot from server"))
		}

		containers, ok := op.Resources["containers"]
		if !ok || len(containers) == 0 {
			progress.Done("")
			return fmt.Errorf(i18n.G("didn't get any affected image, container or snapshot from server"))
		}

		var restVersion string
		toScan := strings.Replace(containers[0], "/", " ", -1)
		count, err := fmt.Sscanf(toScan, " %s containers %s", &restVersion, &name)
		if err != nil {
			progress.Done("")
			return err
		}

		if count != 2 {
			progress.Done("")
			return fmt.Errorf(i18n.G("bad number of things scanned from image, container or snapshot"))
		}

		if restVersion != version.APIVersion {
			progress.Done("")
			return fmt.Errorf(i18n.G("got bad version"))
		}
	}
	fmt.Printf(i18n.G("Creating %s")+"\n", name)

	if err = d.WaitForSuccess(resp.Operation); err != nil {
		progress.Done("")
		return err
	}
	progress.Done("")

	fmt.Printf(i18n.G("Starting %s")+"\n", name)
	resp, err = d.Action(name, shared.Start, -1, false, false)
	if err != nil {
		return err
	}

	err = d.WaitForSuccess(resp.Operation)
	if err != nil {
		prettyName := name
		if remote != "" {
			prettyName = fmt.Sprintf("%s:%s", remote, name)
		}

		return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, prettyName)
	}

	return nil
}
