package main

import (
	"fmt"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared/api"
)

func transferRootfs(dst lxd.ContainerServer, op lxd.Operation, rootfs string, rsyncArgs string) error {
	opAPI := op.Get()

	// Connect to the websockets
	wsControl, err := op.GetWebsocket(opAPI.Metadata[api.SecretNameControl].(string))
	if err != nil {
		return err
	}

	wsFs, err := op.GetWebsocket(opAPI.Metadata[api.SecretNameFilesystem].(string))
	if err != nil {
		return err
	}

	// Setup control struct
	fs := migration.MigrationFSType_RSYNC
	rsyncHasFeature := true
	header := migration.MigrationHeader{
		Fs: &fs,
		RsyncFeatures: &migration.RsyncFeatures{
			Xattrs:   &rsyncHasFeature,
			Delete:   &rsyncHasFeature,
			Compress: &rsyncHasFeature,
		},
	}

	err = migration.ProtoSend(wsControl, &header)
	if err != nil {
		protoSendError(wsControl, err)
		return err
	}

	err = migration.ProtoRecv(wsControl, &header)
	if err != nil {
		protoSendError(wsControl, err)
		return err
	}

	// Send the filesystem
	abort := func(err error) error {
		protoSendError(wsControl, err)
		return err
	}

	err = rsyncSend(wsFs, rootfs, rsyncArgs)
	if err != nil {
		return abort(err)
	}

	// Check the result
	msg := migration.MigrationControl{}
	err = migration.ProtoRecv(wsControl, &msg)
	if err != nil {
		_ = wsControl.Close()
		return err
	}

	if !*msg.Success {
		return fmt.Errorf(*msg.Message)
	}

	return nil
}
