package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/pkg/errors"
)

type cmdMove struct {
	global *cmdGlobal

	flagNoProfiles    bool
	flagProfile       []string
	flagConfig        []string
	flagContainerOnly bool
	flagDevice        []string
	flagMode          string
	flagStateless     bool
	flagStorage       string
	flagTarget        string
	flagTargetProject string
}

func (c *cmdMove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("move [<remote>:]<container>[/<snapshot>] [<remote>:][<container>[/<snapshot>]]")
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Move containers within or in between LXD instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Move containers within or in between LXD instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc move [<remote>:]<source container> [<remote>:][<destination container>] [--container-only]
    Move a container between two hosts, renaming it if destination name differs.

lxc move <old name> <new name> [--container-only]
    Rename a local container.

lxc move <container>/<old snapshot name> <container>/<new snapshot name>
    Rename a snapshot.`))

	cmd.RunE = c.Run
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the target container")+"``")
	cmd.Flags().StringArrayVarP(&c.flagDevice, "device", "d", nil, i18n.G("New key/value to apply to a specific device")+"``")
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, i18n.G("Profile to apply to the target container")+"``")
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, i18n.G("Unset all profiles on the target container"))
	cmd.Flags().BoolVar(&c.flagContainerOnly, "container-only", false, i18n.G("Move the container without its snapshots"))
	cmd.Flags().StringVar(&c.flagMode, "mode", moveDefaultMode, i18n.G("Transfer mode. One of pull (default), push or relay.")+"``")
	cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, i18n.G("Copy a stateful container stateless"))
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", i18n.G("Storage pool name")+"``")
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")

	return cmd
}

func (c *cmdMove) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	if c.flagTarget == "" {
		exit, err := c.global.CheckArgs(cmd, args, 2, 2)
		if exit {
			return err
		}
	} else {
		exit, err := c.global.CheckArgs(cmd, args, 1, 2)
		if exit {
			return err
		}
	}

	// Parse the mode
	mode := moveDefaultMode
	if c.flagMode != "" {
		mode = c.flagMode
	}

	sourceRemote, sourceName, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	destRemote := sourceRemote
	destName := ""
	if len(args) == 2 {
		var err error
		destRemote, destName, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}
	}

	// As an optimization, if the source an destination are the same, do
	// this via a simple rename. This only works for containers that aren't
	// running, containers that are running should be live migrated (of
	// course, this changing of hostname isn't supported right now, so this
	// simply won't work).
	if sourceRemote == destRemote && c.flagTarget == "" && c.flagStorage == "" && c.flagTargetProject == "" {
		if c.flagConfig != nil || c.flagDevice != nil || c.flagProfile != nil || c.flagNoProfiles {
			return fmt.Errorf(i18n.G("Can't override configuration or profiles in local rename"))
		}

		source, err := conf.GetContainerServer(sourceRemote)
		if err != nil {
			return err
		}

		if shared.IsSnapshot(sourceName) {
			// Snapshot rename
			srcFields := strings.SplitN(sourceName, shared.SnapshotDelimiter, 2)
			dstFields := strings.SplitN(destName, shared.SnapshotDelimiter, 2)

			op, err := source.RenameContainerSnapshot(srcFields[0], srcFields[1], api.ContainerSnapshotPost{Name: dstFields[1]})
			if err != nil {
				return err
			}

			return op.Wait()
		}

		// Container rename
		op, err := source.RenameContainer(sourceName, api.ContainerPost{Name: destName})
		if err != nil {
			return err
		}

		return op.Wait()
	}

	sourceResource := args[0]
	destResource := sourceResource
	if len(args) == 2 {
		destResource = args[1]
	}

	// If the target option was specified, we're moving a container from a
	// cluster member to another, let's use the dedicated API.
	if c.flagTarget != "" {
		if c.flagStateless {
			return fmt.Errorf(i18n.G("The --stateless flag can't be used with --target"))
		}

		if c.flagContainerOnly {
			return fmt.Errorf(i18n.G("The --container-only flag can't be used with --target"))
		}

		if c.flagMode != moveDefaultMode {
			return fmt.Errorf(i18n.G("The --mode flag can't be used with --target"))
		}

		return moveClusterContainer(conf, sourceResource, destResource, c.flagTarget)
	}

	cpy := cmdCopy{}
	cpy.global = c.global
	cpy.flagTarget = c.flagTarget
	cpy.flagTargetProject = c.flagTargetProject
	cpy.flagConfig = c.flagConfig
	cpy.flagDevice = c.flagDevice
	cpy.flagProfile = c.flagProfile
	cpy.flagNoProfiles = c.flagNoProfiles

	stateful := !c.flagStateless

	// A move is just a copy followed by a delete; however, we want to
	// keep the volatile entries around since we are moving the container.
	err = cpy.copyContainer(conf, sourceResource, destResource, true, -1, stateful, c.flagContainerOnly, mode, c.flagStorage)
	if err != nil {
		return err
	}

	del := cmdDelete{global: c.global}
	del.flagForce = true
	err = del.Run(cmd, args[:1])
	if err != nil {
		return errors.Wrap(err, "Failed to delete original container after copying it")
	}

	return nil
}

// Move a container using special POST /containers/<name>?target=<member> API.
func moveClusterContainer(conf *config.Config, sourceResource, destResource, target string) error {
	// Parse the source.
	sourceRemote, sourceName, err := conf.ParseRemote(sourceResource)
	if err != nil {
		return err
	}

	// Parse the destination.
	_, destName, err := conf.ParseRemote(destResource)
	if err != nil {
		return err
	}

	// Make sure we have a container or snapshot name.
	if sourceName == "" {
		return fmt.Errorf(i18n.G("You must specify a source container name"))
	}

	// The destination name is optional.
	if destName == "" {
		destName = sourceName
	}

	// Connect to the source host
	source, err := conf.GetContainerServer(sourceRemote)
	if err != nil {
		return errors.Wrap(err, i18n.G("Failed to connect to cluster member"))
	}

	// Check that it's a cluster
	if !source.IsClustered() {
		return fmt.Errorf(i18n.G("The source LXD instance is not clustered"))
	}

	// The migrate API will do the right thing when passed a target.
	source = source.UseTarget(target)
	req := api.ContainerPost{Name: destName, Migration: true}
	op, err := source.MigrateContainer(sourceName, req)
	if err != nil {
		return errors.Wrap(err, i18n.G("Migration API failure"))
	}

	err = op.Wait()
	if err != nil {
		return errors.Wrap(err, i18n.G("Migration operation failure"))
	}

	return nil
}

// Default migration mode when moving a container.
const moveDefaultMode = "pull"
