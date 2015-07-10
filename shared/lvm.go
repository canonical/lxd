package shared

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

var defaultThinLVSize = "100GiB"
var defaultThinPoolName = "LXDPool"
var snapshotCreateTimeout = time.Duration(60) // seconds

func LVMCheckVolumeGroup(vgname string) error {
	output, err := exec.Command("vgdisplay", "-s", vgname).CombinedOutput()
	if err != nil {
		Debugf("vgdisplay failed to find vg:\n%s", output)
		return fmt.Errorf("LVM volume group '%s' not found.", vgname)
	}

	return nil
}

func LVMThinPoolLVExists(vgname string, poolname string) (bool, error) {
	output, err := exec.Command("vgs", "--noheadings", "-o", "lv_attr", fmt.Sprintf("%s/%s", vgname, poolname)).CombinedOutput()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			waitStatus := exitError.Sys().(syscall.WaitStatus)
			if waitStatus.ExitStatus() == 5 {
				// pool LV was not found
				return false, nil
			}
		}
		return false, fmt.Errorf("Error checking for pool '%s'", poolname)
	}
	// Found LV named poolname, check type:
	attrs := strings.TrimSpace(string(output[:]))
	if strings.HasPrefix(attrs, "t") {
		return true, nil
	} else {
		return false, fmt.Errorf("Pool named '%s' exists but is not a thin pool.", poolname)
	}

}

func LVMCreateDefaultThinPool(vgname string) (string, error) {
	output, err := exec.Command("lvcreate", "--poolmetadatasize", "1G", "-l", "100%FREE", "--thinpool", fmt.Sprintf("%s/%s", vgname, defaultThinPoolName)).CombinedOutput()
	if err != nil {
		Debugf("could not create thin pool named '%s'. Error:'%s'\nOutput:'%s'", defaultThinPoolName, err, output)
		return "", fmt.Errorf("Could not create LVM thin pool named %s", defaultThinPoolName)
	}
	return defaultThinPoolName, nil
}

func LVMCreateThinLV(lvname string, poolname string, vgname string) (string, error) {
	output, err := exec.Command("lvcreate", "--thin", "-n", lvname, "--virtualsize", defaultThinLVSize, fmt.Sprintf("%s/%s", vgname, poolname)).CombinedOutput()
	if err != nil {
		Debugf("could not create LV named '%s': '%s'", lvname, output)
		return "", fmt.Errorf("Could not create thin LV named %s", lvname)
	}
	return fmt.Sprintf("/dev/%s/%s", vgname, lvname), nil
}

func LVMCreateSnapshotLV(lvname string, origlvname string, vgname string) (string, error) {
	cmd := exec.Command("lvcreate", "-kn", "-n", lvname, "-s", fmt.Sprintf("/dev/%s/%s", vgname, origlvname))

	var errbuf bytes.Buffer
	cmd.Stderr = &errbuf

	err := cmd.Start()
	if err != nil {
		Debugf("could not create LV named '%s' as snapshot of '%s': '%s'", lvname, origlvname, errbuf.String())
		return "", fmt.Errorf("Could not create snapshot LV named %s", lvname)
	}
	err = cmd.Wait()

	if err != nil {
		return "", fmt.Errorf("Snapshot LV creation error: '%v'", err)
	}

	if err != nil {
		Debugf("could not create LV named '%s' as snapshot of '%s': '%s'", lvname, origlvname, errbuf.String())
		return "", fmt.Errorf("Could not create snapshot LV named %s", lvname)
	}

	snapshotFullName := fmt.Sprintf("/dev/%s/%s", vgname, lvname)
	output, err := exec.Command("lvchange", "-ay", snapshotFullName).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Could not activate new snapshot '%s': %v\noutput:%s", lvname, err, output)
	}

	return snapshotFullName, nil
}

func LVMRemoveLV(vgname string, lvname string) error {
	output, err := exec.Command("lvremove", "-f", fmt.Sprintf("%s/%s", vgname, lvname)).CombinedOutput()
	if err != nil {
		Debugf("could not remove LV named '%s': '%s'", lvname, output)
		return fmt.Errorf("Could not remove LV named %s", lvname)
	}
	return nil
}
