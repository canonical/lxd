package storage

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

type driverCommon struct {
	s *state.State

	poolID int64
	pool   *api.StoragePool

	volume *api.StorageVolume

	sTypeVersion string
}

func (d *driverCommon) commonInit(s *state.State, pool *api.StoragePool, poolID int64, volume *api.StorageVolume) {
	d.s = s
	d.pool = pool
	d.poolID = poolID
	d.volume = volume
}

func (d *driverCommon) GetVersion() string {
	return d.sTypeVersion
}

func (d *driverCommon) UsesThinpool() bool {
	if d.pool == nil {
		return false
	}

	// Default is to use a thinpool.
	if d.pool.Config["lvm.use_thinpool"] == "" {
		return true
	}

	return shared.IsTrue(d.pool.Config["lvm.use_thinpool"])
}

func (d *driverCommon) GetBlockFilesystem() string {
	if d.pool == nil && d.volume == nil {
		return ""
	}

	if d.volume != nil && d.volume.Config["block.filesystem"] != "" {
		return d.volume.Config["block.filesystem"]
	}

	if d.pool != nil && d.pool.Config["volume.block.filesystem"] != "" {
		return d.pool.Config["volume.block.filesystem"]
	}

	return "ext4"
}

func (d *driverCommon) rsync(source string, dest string) error {
	bwlimit := d.pool.Config["rsync.bwlimit"]

	err := os.MkdirAll(dest, 0755)
	if err != nil {
		return fmt.Errorf("Failed to rsync: %s", err)
	}

	rsyncVerbosity := "-q"
	// Handle debug
	/*
		if debug {
			rsyncVerbosity = "-vi"
		}
	*/

	if bwlimit == "" {
		bwlimit = "0"
	}

	msg, err := shared.RunCommand("rsync",
		"-a",
		"-HAX",
		"--sparse",
		"--devices",
		"--delete",
		"--checksum",
		"--numeric-ids",
		"--xattrs",
		"--bwlimit", bwlimit,
		rsyncVerbosity,
		shared.AddSlash(source),
		dest)
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 24 {
					return nil
				}
			}
		}
		return fmt.Errorf("Failed to rsync: %s: %s", string(msg), err)

	}

	return nil
}
