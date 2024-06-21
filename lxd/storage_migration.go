package main

import (
	"time"

	"github.com/canonical/lxd/lxd/db"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/shared"
)

func snapshotProtobufToInstanceArgs(inst instance.Instance, snap *migration.Snapshot) db.InstanceArgs {
	config := map[string]string{}

	for _, ent := range snap.GetLocalConfig() {
		config[ent.GetKey()] = ent.GetValue()
	}

	devices := deviceConfig.Devices{}
	for _, ent := range snap.GetLocalDevices() {
		props := map[string]string{}
		for _, prop := range ent.GetConfig() {
			props[prop.GetKey()] = prop.GetValue()
		}

		devices[ent.GetName()] = props
	}

	args := db.InstanceArgs{
		Architecture: int(snap.GetArchitecture()),
		Config:       config,
		Type:         inst.Type(),
		Snapshot:     true,
		Devices:      devices,
		Ephemeral:    snap.GetEphemeral(),
		Name:         inst.Name() + shared.SnapshotDelimiter + snap.GetName(),
		Profiles:     snap.Profiles,
		Stateful:     snap.GetStateful(),
		Project:      inst.Project(),
	}

	if snap.GetCreationDate() != 0 {
		args.CreationDate = time.Unix(snap.GetCreationDate(), 0)
	}

	if snap.GetLastUsedDate() != 0 {
		args.LastUsedDate = time.Unix(snap.GetLastUsedDate(), 0)
	}

	if snap.GetExpiryDate() != 0 {
		args.ExpiryDate = time.Unix(snap.GetExpiryDate(), 0)
	}

	return args
}
