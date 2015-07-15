package main

import (
	"fmt"
	"os/exec"
	"strings"

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

func btrfsMakeSubvol(subvol string) error {
	if err := btrfsCmdIsInstalled(); err != nil {
		return err
	}

	output, err := exec.Command("btrfs", "subvolume", "create", subvol).CombinedOutput()
	if err != nil {
		shared.Debugf("btrfs subvolume create %s failed\n", subvol)
		shared.Debugf(string(output))
		return err
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

/*
 * btrfsIsSubvolume returns true if the given Path is a btrfs subvolume
 * else false.
 */
func btrfsIsSubvolume(subvolPath string) bool {
	if err := btrfsCmdIsInstalled(); err != nil {
		return false
	}

	out, err := exec.Command("btrfs", "subvolume", "show", subvolPath).CombinedOutput()
	if err != nil || strings.HasPrefix(string(out), "ERROR: ") {
		return false
	}

	return true
}

/*
 * btrfsSnapshot creates a snapshot of "source" to "dest"
 * the result will be readonly if "readonly" is True.
 */
func btrfsSnapshot(source string, dest string, readonly bool) (string, error) {
	if err := btrfsCmdIsInstalled(); err != nil {
		return "", err
	}

	var out []byte
	var err error
	if readonly {
		out, err = exec.Command("btrfs", "subvolume", "snapshot", "-r", source, dest).CombinedOutput()
	} else {
		out, err = exec.Command("btrfs", "subvolume", "snapshot", source, dest).CombinedOutput()
	}

	return string(out), err
}

func btrfsCopyImage(hash string, name string, d *Daemon) (string, error) {
	if err := btrfsCmdIsInstalled(); err != nil {
		return "", err
	}

	dest := shared.VarPath("lxc", name)
	source := fmt.Sprintf("%s.btrfs", shared.VarPath("images", hash))

	return btrfsSnapshot(source, dest, false)
}
