package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

// Rebuild.
type cmdRebuild struct {
	global            *cmdGlobal
	flagEmpty         bool
	flagForce         bool
	flagTargetProject string
}

func (c *cmdRebuild) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rebuild", i18n.G("[<remote>:]<image> [<remote>:]<instance>"))
	cmd.Short = i18n.G("Rebuild instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Wipe the instance root disk and re-initialize.
The original image is used to re-initialize the instance if a different image or --empty is not specified.

Note: The --project flag sets the project for both the image remote and the instance remote.
If the image remote is a public remote (e.g. simplestreams) then this project is ignored by the image remote.
If the image remote is another LXD server, specify the source project for the image remote 
with --project and the instance remote with --target-project (if different from --project).`))

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagEmpty, "empty", false, i18n.G("Rebuild as an empty instance"))
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("If an instance is running, stop it and then rebuild it"))
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Project containing the instance (if different from --project)")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 0 {
			return c.global.cmpImages(toComplete, false)
		}

		return c.global.cmpTopLevelResource("instance", toComplete)
	}

	return cmd
}

func (c *cmdRebuild) rebuild(conf *config.Config, args []string) error {
	var name, image, remote, iremote string
	var err error

	switch len(args) {
	case 1:
		remote, name, err = conf.ParseRemote(args[0])
		if err != nil {
			return err
		}

	case 2:
		iremote, image, err = conf.ParseRemote(args[0])
		if err != nil {
			return err
		}

		remote, name, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}

	default:
		return errors.New(i18n.G("Missing instance name"))
	}

	if c.flagEmpty && len(args) > 1 {
		return errors.New(i18n.G("--empty cannot be combined with an image name"))
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// Set the target project if provided.
	if c.flagTargetProject != "" {
		d = d.UseProject(c.flagTargetProject)
	}

	// We are not rebuilding just a snapshot but an instance
	if strings.Contains(name, shared.SnapshotDelimiter) {
		return fmt.Errorf(i18n.G("Instance snapshots cannot be rebuilt: %s"), name)
	}

	current, _, err := d.GetInstance(name)
	if err != nil {
		return err
	}

	// If the instance is running, stop it first.
	if c.flagForce && current.StatusCode == api.Running {
		req := api.InstanceStatePut{
			Action: "stop",
			Force:  true,
		}

		// Update the instance.
		op, err := d.UpdateInstanceState(name, req, "")
		if err != nil {
			return err
		}

		progress := cli.ProgressRenderer{
			Quiet: c.global.flagQuiet,
		}

		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		err = cli.CancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return err
		}
	}

	// Base request
	req := api.InstanceRebuildPost{
		Source: api.InstanceSource{},
	}

	if !c.flagEmpty {
		if image == "" && iremote == "" {
			return errors.New(i18n.G("You need to specify an image name or use --empty"))
		}

		iremote, image := guessImage(conf, d, remote, iremote, image)
		imgRemote, imgInfo, err := getImgInfo(d, conf, iremote, remote, image, &req.Source)
		if err != nil {
			return err
		}

		if conf.Remotes[iremote].Protocol != "simplestreams" {
			if imgInfo.Type != "virtual-machine" && current.Type == "virtual-machine" {
				return errors.New(i18n.G("Asked for a VM but image is of type container"))
			}
		}

		op, err := d.RebuildInstanceFromImage(imgRemote, *imgInfo, name, req)
		if err != nil {
			return err
		}

		progress := cli.ProgressRenderer{
			Quiet: c.global.flagQuiet,
		}

		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		// Wait for operation to finish
		err = cli.CancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return err
		}

		progress.Done("")
	} else {
		// This is a rebuild as an empty instance
		if image != "" || iremote != "" {
			return errors.New(i18n.G("Can't use an image with --empty"))
		}

		req.Source.Type = api.SourceTypeNone
		op, err := d.RebuildInstance(name, req)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	// If the instance was stopped, start it back up.
	if c.flagForce && current.StatusCode == api.Running {
		req := api.InstanceStatePut{
			Action: "start",
		}

		// Update the instance.
		op, err := d.UpdateInstanceState(name, req, "")
		if err != nil {
			return err
		}

		progress := cli.ProgressRenderer{
			Quiet: c.global.flagQuiet,
		}

		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		err = cli.CancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return err
		}
	}

	return nil
}

func (c *cmdRebuild) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf
	if len(args) == 0 {
		_ = cmd.Usage()
		return nil
	}

	err := c.rebuild(conf, args)
	if err != nil {
		return err
	}

	return nil
}
