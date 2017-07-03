package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type copyCmd struct {
	ephem bool
}

func (c *copyCmd) showByDefault() bool {
	return true
}

func (c *copyCmd) usage() string {
	return i18n.G(
		`Usage: lxc copy [<remote>:]<source>[/<snapshot>] [[<remote>:]<destination>] [--ephemeral|e]

Copy containers within or in between LXD instances.`)
}

func (c *copyCmd) flags() {
	gnuflag.BoolVar(&c.ephem, "ephemeral", false, i18n.G("Ephemeral container"))
	gnuflag.BoolVar(&c.ephem, "e", false, i18n.G("Ephemeral container"))
}

func (c *copyCmd) copyContainer(conf *config.Config, sourceResource string, destResource string, keepVolatile bool, ephemeral int) error {
	// Parse the source
	sourceRemote, sourceName, err := conf.ParseRemote(sourceResource)
	if err != nil {
		return err
	}

	// Parse the destination
	destRemote, destName, err := conf.ParseRemote(destResource)
	if err != nil {
		return err
	}

	// Make sure we have a container or snapshot name
	if sourceName == "" {
		return fmt.Errorf(i18n.G("You must specify a source container name"))
	}

	// If no destination name was provided, use the same as the source
	if destName == "" {
		destName = sourceName
	}

	// Connect to the source host
	source, err := conf.GetContainerServer(sourceRemote)
	if err != nil {
		return err
	}

	// Connect to the destination host
	var dest lxd.ContainerServer
	if sourceRemote == destRemote {
		// Source and destination are the same
		dest = source
	} else {
		// Destination is different, connect to it
		dest, err = conf.GetContainerServer(destRemote)
		if err != nil {
			return err
		}
	}

	var op *lxd.RemoteOperation
	if shared.IsSnapshot(sourceName) {
		// Prepare the container creation request
		args := lxd.ContainerSnapshotCopyArgs{
			Name: destName,
		}

		// Copy of a snapshot into a new container
		srcFields := strings.SplitN(sourceName, shared.SnapshotDelimiter, 2)
		entry, _, err := source.GetContainerSnapshot(srcFields[0], srcFields[1])
		if err != nil {
			return err
		}

		// Allow overriding the ephemeral status
		if ephemeral == 1 {
			entry.Ephemeral = true
		} else if ephemeral == 0 {
			entry.Ephemeral = false
		}

		// Strip the volatile keys if requested
		if !keepVolatile {
			for k := range entry.Config {
				if k == "volatile.base_image" {
					continue
				}

				if strings.HasPrefix(k, "volatile") {
					delete(entry.Config, k)
				}
			}
		}

		// Do the actual copy
		op, err = dest.CopyContainerSnapshot(source, *entry, &args)
		if err != nil {
			return err
		}
	} else {
		// Prepare the container creation request
		args := lxd.ContainerCopyArgs{
			Name: destName,
		}

		// Copy of a container into a new container
		entry, _, err := source.GetContainer(sourceName)
		if err != nil {
			return err
		}

		// Allow overriding the ephemeral status
		if ephemeral == 1 {
			entry.Ephemeral = true
		} else if ephemeral == 0 {
			entry.Ephemeral = false
		}

		// Strip the volatile keys if requested
		if !keepVolatile {
			for k := range entry.Config {
				if k == "volatile.base_image" {
					continue
				}

				if strings.HasPrefix(k, "volatile") {
					delete(entry.Config, k)
				}
			}
		}

		// Do the actual copy
		op, err = dest.CopyContainer(source, *entry, &args)
		if err != nil {
			return err
		}
	}

	// Watch the background operation
	progress := ProgressRenderer{Format: i18n.G("Transferring container: %s")}
	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for the copy to complete
	err = op.Wait()
	if err != nil {
		progress.Done("")
		return err
	}
	progress.Done("")

	// If choosing a random name, show it to the user
	if destResource == "" {
		// Get the successful operation data
		opInfo, err := op.GetTarget()
		if err != nil {
			return err
		}

		// Extract the list of affected containers
		containers, ok := opInfo.Resources["containers"]
		if !ok || len(containers) != 1 {
			return fmt.Errorf(i18n.G("Failed to get the new container name"))
		}

		// Extract the name of the container
		fields := strings.Split(containers[0], "/")
		fmt.Printf(i18n.G("Container name is: %s")+"\n", fields[len(fields)-1])
	}

	return nil
}

func (c *copyCmd) run(conf *config.Config, args []string) error {
	if len(args) != 2 {
		return errArgs
	}

	// For copies, default to non-ephemeral and allow override (move uses -1)
	ephem := 0
	if c.ephem {
		ephem = 1
	}

	return c.copyContainer(conf, args[0], args[1], false, ephem)
}
