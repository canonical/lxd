package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/i18n"
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
	return i18n.G(
		`Copy containers within or in between lxd instances.

lxc copy [remote:]<source container> [remote:]<destination container> [--ephemeral|e]`)
}

func (c *copyCmd) flags() {
	gnuflag.BoolVar(&c.ephem, "ephemeral", false, i18n.G("Ephemeral container"))
	gnuflag.BoolVar(&c.ephem, "e", false, i18n.G("Ephemeral container"))
}

func copyContainer(config *lxd.Config, sourceResource string, destResource string, keepVolatile bool, ephemeral int) error {
	sourceRemote, sourceName := config.ParseRemoteAndContainer(sourceResource)
	destRemote, destName := config.ParseRemoteAndContainer(destResource)

	if sourceName == "" {
		return fmt.Errorf(i18n.G("you must specify a source container name"))
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
			return fmt.Errorf(i18n.G("can't copy to the same container name"))
		}

		cp, err := source.LocalCopy(sourceName, destName, status.Config, status.Profiles, ephemeral == 1)
		if err != nil {
			return err
		}

		return source.WaitForSuccess(cp.Operation)
	} else {
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
			return fmt.Errorf(i18n.G("not all the profiles from the source exist on the target"))
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

		/* Since we're trying a bunch of different network ports that
		 * may be invalid, we can get "bad handshake" errors when the
		 * websocket code tries to connect. If the first error is a
		 * real error, but the subsequent errors are only network
		 * errors, we should try to report the first real error. Of
		 * course, if all the errors are websocket errors, let's just
		 * report that.
		 */
		var realError error

		for _, addr := range addresses {
			var migration *lxd.Response

			sourceWSUrl := "https://" + addr + sourceWSResponse.Operation
			migration, err = dest.MigrateFrom(destName, sourceWSUrl, secrets, status.Architecture, status.Config, status.Devices, status.Profiles, baseImage, ephemeral == 1)
			if err != nil {
				if !strings.Contains(err.Error(), "websocket: bad handshake") {
					realError = err
				}
				shared.Debugf("intermediate error: %s", err)
				continue
			}

			if err = dest.WaitForSuccess(migration.Operation); err != nil {
				if !strings.Contains(err.Error(), "websocket: bad handshake") {
					realError = err
				}
				shared.Debugf("intermediate error: %s", err)
				// FIXME: This is a backward compatibility codepath
				sourceWSUrl := "wss://" + addr + sourceWSResponse.Operation + "/websocket"

				migration, err = dest.MigrateFrom(destName, sourceWSUrl, secrets, status.Architecture, status.Config, status.Devices, status.Profiles, baseImage, ephemeral == 1)
				if err != nil {
					if !strings.Contains(err.Error(), "websocket: bad handshake") {
						realError = err
					}
					shared.Debugf("intermediate error: %s", err)
					continue
				}

				if err = dest.WaitForSuccess(migration.Operation); err != nil {
					if !strings.Contains(err.Error(), "websocket: bad handshake") {
						realError = err
					}
					shared.Debugf("intermediate error: %s", err)
					continue
				}
			}

			return nil
		}

		if realError != nil {
			return realError
		} else {
			return err
		}
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
