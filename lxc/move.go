package main

import (
	"encoding/json"
	"fmt"
	"path"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

type moveCmd struct {
	httpAddr string
}

func (c *moveCmd) showByDefault() bool {
	return false
}

func (c *moveCmd) usage() string {
	return gettext.Gettext(
		"Move containers within or in between lxd instances.\n" +
			"\n" +
			"(currently only live migration is supported)\n" +
			"lxc move <source container> <destination container>\n")
}

func (c *moveCmd) flags() {}

func (c *moveCmd) run(config *lxd.Config, args []string) error {
	if len(args) != 2 {
		return errArgs
	}

	sourceRemote, sourceName := config.ParseRemoteAndContainer(args[0])
	destRemote, destName := config.ParseRemoteAndContainer(args[1])

	if sourceRemote == "" || destRemote == "" {
		return fmt.Errorf("non-http remotes are not supported for migration right now")
	}

	if sourceName == "" || destName == "" {
		return fmt.Errorf("you must specify both a source and a destination container name")
	}

	source, err := lxd.NewClient(config, sourceRemote)
	if err != nil {
		return err
	}

	dest, err := lxd.NewClient(config, destRemote)
	if err != nil {
		return err
	}

	status, err := source.ContainerStatus(sourceName)
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

	to, err := source.MigrateTo(sourceName, dest)
	if err != nil {
		return err
	}

	secrets := map[string]string{}
	if err := json.Unmarshal(to.Metadata, &secrets); err != nil {
		return err
	}

	url := "wss://" + source.Remote.Addr + path.Join(to.Operation, "websocket")
	migration, err := dest.MigrateFrom(destName, url, secrets, status.Config, status.Profiles)
	if err != nil {
		return err
	}

	return dest.WaitForSuccess(migration.Operation)
}
