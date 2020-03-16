package main

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/migration"
)

func transferRootfs(dst lxd.ContainerServer, op lxd.Operation, rootfs string, rsyncArgs string) error {
	opAPI := op.Get()

	// Connect to the websockets
	wsControl, err := op.GetWebsocket(opAPI.Metadata["control"].(string))
	if err != nil {
		return err
	}

	wsFs, err := op.GetWebsocket(opAPI.Metadata["fs"].(string))
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
		wsControl.Close()
		return err
	}

	if !*msg.Success {
		return fmt.Errorf(*msg.Message)
	}

	return nil
}

func setupSource(path string, mounts []string) error {
	prefix := "/"
	if len(mounts) > 0 {
		prefix = mounts[0]
	}

	// Mount everything
	for _, mount := range mounts {
		target := fmt.Sprintf("%s/%s", path, strings.TrimPrefix(mount, prefix))

		// Mount the path
		err := unix.Mount(mount, target, "none", unix.MS_BIND, "")
		if err != nil {
			return fmt.Errorf("Failed to mount %s: %v", mount, err)
		}

		// Make it read-only
		err = unix.Mount("", target, "none", unix.MS_BIND|unix.MS_RDONLY|unix.MS_REMOUNT, "")
		if err != nil {
			return fmt.Errorf("Failed to make %s read-only: %v", mount, err)
		}
	}

	return nil
}
