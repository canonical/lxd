package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

var storageLvmDefaultThinLVSize = "100GiB"
var storageLvmDefaultThinPoolName = "LXDPool"

func storageLVMCheckVolumeGroup(vgName string) error {
	output, err := exec.Command("vgdisplay", "-s", vgName).CombinedOutput()
	if err != nil {
		shared.Log.Debug("vgdisplay failed to find vg", log.Ctx{"output": output})
		return fmt.Errorf("LVM volume group '%s' not found", vgName)
	}

	return nil
}

func storageLVMThinpoolExists(vgName string, poolName string) (bool, error) {
	output, err := exec.Command("vgs", "--noheadings", "-o", "lv_attr", fmt.Sprintf("%s/%s", vgName, poolName)).CombinedOutput()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			waitStatus := exitError.Sys().(syscall.WaitStatus)
			if waitStatus.ExitStatus() == 5 {
				// pool LV was not found
				return false, nil
			}
		}
		return false, fmt.Errorf("Error checking for pool '%s'", poolName)
	}
	// Found LV named poolname, check type:
	attrs := strings.TrimSpace(string(output[:]))
	if strings.HasPrefix(attrs, "t") {
		return true, nil
	}

	return false, fmt.Errorf("Pool named '%s' exists but is not a thin pool.", poolName)
}

func storageLVMSetThinPoolNameConfig(d *Daemon, poolname string) error {
	vgname, err := d.ConfigValueGet("core.lvm_vg_name")
	if err != nil {
		return fmt.Errorf("Error getting lvm_vg_name config")
	}

	if poolname != "" {
		if vgname == "" {
			return fmt.Errorf("Can not set lvm_thinpool_name without lvm_vg_name set.")
		}

		poolExists, err := storageLVMThinpoolExists(vgname, poolname)
		if err != nil {
			return fmt.Errorf("Error checking for thin pool '%s' in '%s': %v", poolname, vgname, err)
		}
		if !poolExists {
			return fmt.Errorf("Pool '%s' does not exist in Volume Group '%s'", poolname, vgname)
		}
	}

	err = d.ConfigValueSet("core.lvm_thinpool_name", poolname)
	if err != nil {
		return err
	}

	return nil
}

func storageLVMSetVolumeGroupNameConfig(d *Daemon, vgname string) error {
	if vgname != "" {
		err := storageLVMCheckVolumeGroup(vgname)
		if err != nil {
			return err
		}
	}

	err := d.ConfigValueSet("core.lvm_vg_name", vgname)
	if err != nil {
		return err
	}

	return nil
}

type storageLvm struct {
	d      *Daemon
	vgName string

	storageShared
}

func (s *storageLvm) Init(config map[string]interface{}) (storage, error) {
	s.sType = storageTypeLvm
	s.sTypeName = storageTypeToString(s.sType)
	if err := s.initShared(); err != nil {
		return s, err
	}

	if config["vgName"] == nil {
		vgName, err := s.d.ConfigValueGet("core.lvm_vg_name")
		if err != nil {
			return s, fmt.Errorf("Error checking server config: %v", err)
		}
		if vgName == "" {
			return s, fmt.Errorf("LVM isn't enabled")
		}

		if err := storageLVMCheckVolumeGroup(vgName); err != nil {
			return s, err
		}
		s.vgName = vgName
	} else {
		s.vgName = config["vgName"].(string)
	}

	return s, nil
}

func (s *storageLvm) ContainerCreate(container container) error {
	return fmt.Errorf(
		"ContainerCreate is not implemented in the LVM backend.")
}

func (s *storageLvm) ContainerCreateFromImage(
	container container, imageFingerprint string) error {

	imageLVFilename := shared.VarPath(
		"images", fmt.Sprintf("%s.lv", imageFingerprint))

	if !shared.PathExists(imageLVFilename) {
		if err := s.ImageCreate(imageLVFilename); err != nil {
			return err
		}
	}

	lvpath, err := s.createSnapshotLV(container.NameGet(), imageFingerprint)
	if err != nil {
		return err
	}

	destPath := container.PathGet("")
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return fmt.Errorf("Error creating container directory: %v", err)
	}

	dst := shared.VarPath("containers", fmt.Sprintf("%s.lv", container.NameGet()))
	err = os.Symlink(lvpath, dst)
	if err != nil {
		return err
	}

	if !container.IsPrivileged() {
		output, err := exec.Command("mount", "-o", "discard", lvpath, destPath).CombinedOutput()
		if err != nil {
			s.ContainerDelete(container)
			return fmt.Errorf("Error mounting snapshot LV: %v\noutput:'%s'", err, output)
		}

		if err = s.shiftRootfs(container); err != nil {
			s.ContainerDelete(container)
			return fmt.Errorf("Error in shiftRootfs: %v", err)
		}

		output, err = exec.Command("umount", destPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("Error unmounting '%s' after shiftRootfs: %v", destPath, err)
		}
	}

	return container.TemplateApply("create")
}

func (s *storageLvm) ContainerDelete(container container) error {
	// First remove the LVM LV
	if err := s.removeLV(container.NameGet()); err != nil {
		return err
	}

	// Then remove the symlink
	lvLinkPath := shared.VarPath("containers", fmt.Sprintf("%s.lv", container.NameGet()))
	if err := os.Remove(lvLinkPath); err != nil {
		return err
	}

	// Then the container path (e.g. /var/lib/lxd/containers/<name>)
	cPath := container.PathGet("")
	if err := os.RemoveAll(cPath + "/*"); err != nil {
		s.log.Error("ContainerDelete: failed", log.Ctx{"cPath": cPath, "err": err})
		return fmt.Errorf("Cleaning up %s: %s", cPath, err)
	}

	// If its name contains a "/" also remove the parent,
	// this should only happen with snapshot containers
	if strings.Contains(container.NameGet(), "/") {
		oldPathParent := filepath.Dir(container.PathGet(""))
		if ok, _ := shared.PathIsEmpty(oldPathParent); ok {
			os.Remove(oldPathParent)
		} else {
			shared.Log.Debug(
				"Cannot remove the parent of this container its not empty",
				log.Ctx{"container": container.NameGet(), "parent": oldPathParent})
		}
	}

	return nil
}

func (s *storageLvm) ContainerCopy(container container, sourceContainer container) error {

	return fmt.Errorf(
		"ContainerCopy is not implemented in the LVM backend.")

	// return container.TemplateApply("copy")
}

func (s *storageLvm) ContainerStart(container container) error {
	lvpath := fmt.Sprintf("/dev/%s/%s", s.vgName, container.NameGet())
	output, err := exec.Command(
		"mount", "-o", "discard", lvpath, container.PathGet("")).CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"Error mounting snapshot LV path='%s': %v\noutput:'%s'",
			container.PathGet(""),
			err,
			output)
	}

	return nil
}

func (s *storageLvm) ContainerStop(container container) error {
	output, err := exec.Command("umount", container.PathGet("")).CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"failed to unmount container path '%s'.\nError: %v\nOutput: %s",
			container.PathGet(""),
			err,
			output)
	}

	return nil
}

// ContainerRename should rename a LVM Container.
// TODO: Not implemented, yet.
func (s *storageLvm) ContainerRename(
	container container, newName string) error {

	// TODO: No TemplateApply here?

	return fmt.Errorf(
		"ContainerRename is not implemented in the LVM backend.")
}

func (s *storageLvm) ContainerRestore(
	container container, sourceContainer container) error {

	return fmt.Errorf(
		"ContainerRestore is not implemented in the LVM backend.")
}

func (s *storageLvm) ContainerSnapshotCreate(
	snapshotContainer container, sourceContainer container) error {

	return fmt.Errorf(
		"ContainerSnapshotCreate is not implement in the LVM Backend.")
}
func (s *storageLvm) ContainerSnapshotDelete(
	snapshotContainer container) error {

	return fmt.Errorf(
		"ContainerSnapshotDelete is not implement in the LVM Backend.")
}

func (s *storageLvm) ContainerSnapshotRename(
	snapshotContainer container, newName string) error {

	return fmt.Errorf(
		"ContainerSnapshotRename is not implement in the LVM Backend.")
}

func (s *storageLvm) ImageCreate(fingerprint string) error {
	finalName := shared.VarPath("images", fingerprint)

	poolname, err := s.d.ConfigValueGet("core.lvm_thinpool_name")
	if err != nil {
		return fmt.Errorf("Error checking server config, err=%v", err)
	}

	if poolname == "" {
		poolname, err = s.createDefaultThinPool()
		if err != nil {
			return fmt.Errorf("Error creating LVM thin pool: %v", err)
		}
		err = storageLVMSetThinPoolNameConfig(s.d, poolname)
		if err != nil {
			s.log.Error("Setting thin pool name", log.Ctx{"err": err})
			return fmt.Errorf("Error setting LVM thin pool config: %v", err)
		}
	}

	lvpath, err := s.createThinLV(fingerprint, poolname)
	if err != nil {
		s.log.Error("LVMCreateThinLV", log.Ctx{"err": err})
		return fmt.Errorf("Error Creating LVM LV for new image: %v", err)
	}

	dst := shared.VarPath("images", fmt.Sprintf("%s.lv", fingerprint))
	err = os.Symlink(lvpath, dst)
	if err != nil {
		return err
	}

	output, err := exec.Command(
		"mkfs.ext4",
		"-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0",
		lvpath).CombinedOutput()

	if err != nil {
		s.log.Error("mkfs.ext4", log.Ctx{"output": output})
		return fmt.Errorf("Error making filesystem on image LV: %v", err)
	}

	tempLVMountPoint, err := ioutil.TempDir(shared.VarPath("images"), "tmp_lv_mnt")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(tempLVMountPoint); err != nil {
			s.log.Error("Deleting temporary LVM mount point", log.Ctx{"err": err})
		}
	}()

	output, err = exec.Command(
		"mount",
		"-o", "discard",
		lvpath,
		tempLVMountPoint).CombinedOutput()

	if err != nil {
		shared.Logf("Error mounting image LV for untarring: '%s'", output)
		return fmt.Errorf("Error mounting image LV: %v", err)

	}

	untarErr := untarImage(finalName, tempLVMountPoint)

	output, err = exec.Command("umount", tempLVMountPoint).CombinedOutput()
	if err != nil {
		s.log.Warn("could not unmount LV. Will not remove",
			log.Ctx{"lvpath": lvpath, "mountpoint": tempLVMountPoint, "err": err})
		if untarErr == nil {
			return err
		}

		return fmt.Errorf(
			"Error unmounting '%s' during cleanup of error %v",
			tempLVMountPoint, untarErr)
	}

	return untarErr
}

func (s *storageLvm) ImageDelete(fingerprint string) error {
	err := s.removeLV(fingerprint)
	if err != nil {
		return err
	}

	lvsymlink := fmt.Sprintf(
		"%s.lv", shared.VarPath("images", fingerprint))
	err = os.Remove(lvsymlink)
	if err != nil {
		return fmt.Errorf(
			"Failed to remove symlink to deleted image LV: '%s': %v", lvsymlink, err)
	}

	return nil
}

func (s *storageLvm) createDefaultThinPool() (string, error) {
	output, err := exec.Command(
		"lvcreate",
		"--poolmetadatasize", "1G",
		"-l", "100%FREE",
		"--thinpool",
		fmt.Sprintf("%s/%s", s.vgName, storageLvmDefaultThinPoolName)).CombinedOutput()

	if err != nil {
		s.log.Debug(
			"Could not create thin pool",
			log.Ctx{
				"name":   storageLvmDefaultThinPoolName,
				"err":    err,
				"output": output})

		return "", fmt.Errorf(
			"Could not create LVM thin pool named %s", storageLvmDefaultThinPoolName)
	}
	return storageLvmDefaultThinPoolName, nil
}

func (s *storageLvm) createThinLV(lvname string, poolname string) (string, error) {
	output, err := exec.Command(
		"lvcreate",
		"--thin",
		"-n", lvname,
		"--virtualsize", storageLvmDefaultThinLVSize,
		fmt.Sprintf("%s/%s", s.vgName, poolname)).CombinedOutput()

	if err != nil {
		s.log.Debug("Could not create LV", log.Ctx{"lvname": lvname, "output": output})
		return "", fmt.Errorf("Could not create thin LV named %s", lvname)
	}

	return fmt.Sprintf("/dev/%s/%s", s.vgName, lvname), nil
}

func (s *storageLvm) removeLV(lvname string) error {
	output, err := exec.Command(
		"lvremove", "-f", fmt.Sprintf("%s/%s", s.vgName, lvname)).CombinedOutput()
	if err != nil {
		s.log.Debug("Could not remove LV", log.Ctx{"lvname": lvname, "output": output})
		return fmt.Errorf("Could not remove LV named %s", lvname)
	}
	return nil
}

func (s *storageLvm) createSnapshotLV(lvname string, origlvname string) (string, error) {
	output, err := exec.Command(
		"lvcreate",
		"-kn",
		"-n", lvname,
		"-s", fmt.Sprintf("/dev/%s/%s", s.vgName, origlvname)).CombinedOutput()
	if err != nil {
		s.log.Debug("Could not create LV snapshot", log.Ctx{"lvname": lvname, "origlvname": origlvname, "output": output})
		return "", fmt.Errorf("Could not create snapshot LV named %s", lvname)
	}

	snapshotFullName := fmt.Sprintf("/dev/%s/%s", s.vgName, lvname)
	output, err = exec.Command("lvchange", "-ay", snapshotFullName).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Could not activate new snapshot '%s': %v\noutput:%s", lvname, err, output)
	}

	return snapshotFullName, nil
}
