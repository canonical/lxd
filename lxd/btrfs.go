package main

import (
	"fmt"
	"os/exec"

	"github.com/lxc/lxd/shared"
)

/*
 * btrfsCmdIsInstalled returns true if the "btrfs" tool is in PATH else false.
 *
 * TODO: Move this to the main code somewhere and call it once?
 */
func btrfsCmdIsInstalled() error {

	out, err := exec.LookPath("btrfs")
	if err != nil || len(out) == 0 {
		shared.Debugf("The 'btrfs' tool isn't available")
		return fmt.Errorf("The 'btrfs' tool isn't available")
	}

	return nil
}

func btrfsDeleteSubvol(subvol string) error {
	if err := btrfsCmdIsInstalled(); err != nil {
		return err
	}

	output, err := exec.Command("btrfs", "subvolume", "delete", subvol).CombinedOutput()
	if err != nil {
		shared.Debugf("btrfs subvolume delete %s failed\n", subvol)
		shared.Debugf(string(output))
		return err
	}

	return nil
}
