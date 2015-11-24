package main

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/chai2010/gettext-go/gettext"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
)

type copyCmd struct {
	ephem bool
}

func (c *copyCmd) showByDefault() bool {
	return true
}

func (c *copyCmd) usage() string {
	return gettext.Gettext(
		`Copy containers within or in between lxd instances.

lxc copy [remote:]<source container> [remote:]<destination container> [--ephemeral|e]`)
}

func (c *copyCmd) flags() {
	gnuflag.BoolVar(&c.ephem, "ephemeral", false, gettext.Gettext("Ephemeral container"))
	gnuflag.BoolVar(&c.ephem, "e", false, gettext.Gettext("Ephemeral container"))
}

func copyContainer(config *lxd.Config, sourceResource string, destResource string, keepVolatile bool, ephemeral int) error {
	sourceRemote, sourceName := config.ParseRemoteAndContainer(sourceResource)
	destRemote, destName := config.ParseRemoteAndContainer(destResource)

	if sourceName == "" {
		return fmt.Errorf(gettext.Gettext("you must specify a source container name"))
	}

	if destName == "" {
		destName = sourceName
	}

	source, err := lxd.NewClient(config, sourceRemote)
	if err != nil {
		return err
	}

	status := &shared.ContainerState{}

	// TODO: presumably we want to do this for copying snapshots too? We
	// need to think a bit more about how we track the baseImage in the
	// face of LVM and snapshots in general; this will probably make more
	// sense once that work is done.
	baseImage := ""

	if !shared.IsSnapshot(sourceName) {
		status, err = source.ContainerStatus(sourceName)
		if err != nil {
			return err
		}

		baseImage = status.Config["volatile.base_image"]

		if !keepVolatile {
			for k := range status.Config {
				if strings.HasPrefix(k, "volatile") {
					delete(status.Config, k)
				}
			}
		}
	}

	// Do a local copy if the remotes are the same, otherwise do a migration
	if sourceRemote == destRemote {
		if sourceName == destName {
			return fmt.Errorf(gettext.Gettext("can't copy to the same container name"))
		}

		cp, err := source.LocalCopy(sourceName, destName, status.Config, status.Profiles, ephemeral == 1)
		if err != nil {
			return err
		}

		return source.WaitForSuccess(cp.Operation)
	} else {
		if strings.Contains(sourceName, shared.SnapshotDelimiter) {
			return fmt.Errorf("Copying from a remote snapshot isn't implemented yet.")
		}

		dest, err := lxd.NewClient(config, destRemote)
		if err != nil {
			return err
		}

		sourceProfs := shared.NewStringSet(status.Profiles)
		destProfs, err := dest.ListProfiles()
		if err != nil {
			return err
		}

		if !sourceProfs.IsSubset(shared.NewStringSet(destProfs)) {
			return fmt.Errorf(gettext.Gettext("not all the profiles from the source exist on the target"))
		}

		if ephemeral == -1 {
			ct, err := source.ContainerStatus(sourceName)
			if err != nil {
				return err
			}

			if ct.Ephemeral {
				ephemeral = 1
			} else {
				ephemeral = 0
			}
		}

		sourceWSResponse, err := source.GetMigrationSourceWS(sourceName)
		if err != nil {
			return err
		}

		secrets := map[string]string{}

		op, err := sourceWSResponse.MetadataAsOperation()
		if err == nil && op.Metadata != nil {
			for k, v := range *op.Metadata {
				secrets[k] = v.(string)
			}
		} else {
			// FIXME: This is a backward compatibility codepath
			if err := json.Unmarshal(sourceWSResponse.Metadata, &secrets); err != nil {
				return err
			}
		}

		addresses, err := source.Addresses()
		if err != nil {
			return err
		}

		for _, addr := range addresses {
			sourceWSUrl := "wss://" + addr + path.Join(sourceWSResponse.Operation, "websocket")

			var migration *lxd.Response
			migration, err = dest.MigrateFrom(destName, sourceWSUrl, secrets, status.Architecture, status.Config, status.Devices, status.Profiles, baseImage, ephemeral == 1)
			if err != nil {
				continue
			}

			if err = dest.WaitForSuccess(migration.Operation); err != nil {
				continue
			}

			return nil
		}

		return err
	}
}

func (c *copyCmd) run(config *lxd.Config, args []string) error {
	if len(args) != 2 {
		return errArgs
	}

	ephem := 0
	if c.ephem {
		ephem = 1
	}

	return copyContainer(config, args[0], args[1], false, ephem)
}
