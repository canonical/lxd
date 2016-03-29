package main

import (
	"fmt"
	"strings"

	"github.com/codegangsta/cli"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

var commandPublish = cli.Command{
	Name:      "publish",
	Usage:     i18n.G("Publish containers as images."),
	ArgsUsage: i18n.G("[remote:]container [remote:] [--alias=ALIAS]... [prop-key=prop-value]..."),

	Flags: commandGlobalFlagsWrapper(
		cli.BoolFlag{
			Name:  "force",
			Usage: i18n.G("Stop the container if currently running."),
		},

		cli.StringSliceFlag{
			Name:  "alias",
			Usage: i18n.G("New alias to define at target."),
		},

		cli.BoolFlag{
			Name:  "public",
			Usage: i18n.G("Make the image public."),
		},
	),
	Action: commandWrapper(commmandActionPublish),
}

func commmandActionPublish(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	var pAliases = context.StringSlice("alias")
	var makePublic = context.Bool("public")
	var force = context.Bool("force")

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

	s := d
	if cRemote != iRemote {
		s, err = lxd.NewClient(config, cRemote)
		if err != nil {
			return err
		}
	}

	if !shared.IsSnapshot(cName) {
		ct, err := s.ContainerInfo(cName)
		if err != nil {
			return err
		}

		wasRunning := ct.StatusCode != 0 && ct.StatusCode != shared.Stopped
		wasEphemeral := ct.Ephemeral

		if wasRunning {
			if !force {
				return fmt.Errorf(i18n.G("The container is currently running. Use --force to have it stopped and restarted."))
			}

			if ct.Ephemeral {
				ct.Ephemeral = false
				err := s.UpdateContainerConfig(cName, ct.Brief())
				if err != nil {
					return err
				}
			}

			resp, err := s.Action(cName, shared.Stop, -1, true, false)
			if err != nil {
				return err
			}

			op, err := s.WaitFor(resp.Operation)
			if err != nil {
				return err
			}

			if op.StatusCode == shared.Failure {
				return fmt.Errorf(i18n.G("Stopping container failed!"))
			}
			defer s.Action(cName, shared.Start, -1, true, false)

			if wasEphemeral {
				ct.Ephemeral = true
				err := s.UpdateContainerConfig(cName, ct.Brief())
				if err != nil {
					return err
				}
			}
		}
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

	fp, err = s.ImageFromContainer(cName, false, nil, properties)
	if err != nil {
		return err
	}
	defer s.DeleteImage(fp)

	err = s.CopyImage(fp, d, false, pAliases, makePublic, false, nil)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Container published with fingerprint: %s")+"\n", fp)

	return nil
}
