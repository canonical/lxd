package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"

	"github.com/pborman/uuid"
	log "gopkg.in/inconshreveable/log15.v2"
)

type storageZfs struct {
	d       *Daemon
	zfsPool string

	storageShared
}

func (s *storageZfs) Init(config map[string]interface{}) (storage, error) {
	s.sType = storageTypeZfs
	s.sTypeName = storageTypeToString(s.sType)

	err := s.initShared()
	if err != nil {
		return s, err
	}

	if config["zfsPool"] == nil {
		zfsPool, err := s.d.ConfigValueGet("storage.zfs_pool_name")
		if err != nil {
			return s, fmt.Errorf("Error checking server config: %v", err)
		}

		if zfsPool == "" {
			return s, fmt.Errorf("ZFS isn't enabled")
		}

		s.zfsPool = zfsPool
	} else {
		s.zfsPool = config["zfsPool"].(string)
	}

	out, err := exec.LookPath("zfs")
	if err != nil || len(out) == 0 {
		return s, fmt.Errorf("The 'zfs' tool isn't available")
	}

	err = s.zfsCheckPool(s.zfsPool)
	if err != nil {
		if shared.PathExists(shared.VarPath("zfs.img")) {
			_, _ = exec.Command("modprobe", "zfs").CombinedOutput()

			output, err := exec.Command("zpool", "import",
				"-d", shared.VarPath(), s.zfsPool).CombinedOutput()
			if err != nil {
				return s, fmt.Errorf("Unable to import the ZFS pool: %s", output)
			}
		} else {
			return s, err
		}
	}

	output, err := exec.Command("zfs", "get", "version", "-H", "-o", "value", s.zfsPool).CombinedOutput()
	if err != nil {
		return s, fmt.Errorf("The 'zfs' tool isn't working properly")
	}

	count, err := fmt.Sscanf(string(output), "%s\n", &s.sTypeVersion)
	if err != nil || count != 1 {
		return s, fmt.Errorf("The 'zfs' tool isn't working properly")
	}

	return s, nil
}

// Things we don't need to care about
func (s *storageZfs) ContainerStart(container container) error {
	return nil
}

func (s *storageZfs) ContainerStop(container container) error {
	return nil
}

// Things we do have to care about
func (s *storageZfs) ContainerCreate(container container) error {
	cPath := container.Path()
	fs := fmt.Sprintf("containers/%s", container.Name())

	err := s.zfsCreate(fs)
	if err != nil {
		return err
	}

	err = os.Symlink(cPath+".zfs", cPath)
	if err != nil {
		return err
	}

	var mode os.FileMode
	if container.IsPrivileged() {
		mode = 0700
	} else {
		mode = 0755
	}

	err = os.Chmod(cPath, mode)
	if err != nil {
		return err
	}

	return container.TemplateApply("create")
}

func (s *storageZfs) ContainerCreateFromImage(container container, fingerprint string) error {
	cPath := container.Path()
	imagePath := shared.VarPath("images", fingerprint)
	subvol := fmt.Sprintf("%s.zfs", imagePath)
	fs := fmt.Sprintf("containers/%s", container.Name())
	fsImage := fmt.Sprintf("images/%s", fingerprint)

	if !shared.PathExists(subvol) {
		err := s.ImageCreate(fingerprint)
		if err != nil {
			return err
		}
	}

	err := s.zfsClone(fsImage, "readonly", fs, true)
	if err != nil {
		return err
	}

	err = os.Symlink(cPath+".zfs", cPath)
	if err != nil {
		return err
	}

	var mode os.FileMode
	if container.IsPrivileged() {
		mode = 0700
	} else {
		mode = 0755
	}

	err = os.Chmod(cPath, mode)
	if err != nil {
		return err
	}

	if !container.IsPrivileged() {
		err = s.shiftRootfs(container)
		if err != nil {
			return err
		}
	}

	return container.TemplateApply("create")
}

func (s *storageZfs) ContainerCanRestore(container container, sourceContainer container) error {
	fields := strings.SplitN(sourceContainer.Name(), shared.SnapshotDelimiter, 2)
	cName := fields[0]
	snapName := fmt.Sprintf("snapshot-%s", fields[1])

	snapshots, err := s.zfsListSnapshots(fmt.Sprintf("containers/%s", cName))
	if err != nil {
		return err
	}

	if snapshots[len(snapshots)-1] != snapName {
		return fmt.Errorf("ZFS only supports restoring state to the latest snapshot.")
	}

	return nil
}

func (s *storageZfs) ContainerDelete(container container) error {
	fs := fmt.Sprintf("containers/%s", container.Name())

	removable := true
	snaps, err := s.zfsListSnapshots(fs)
	if err != nil {
		return err
	}

	for _, snap := range snaps {
		var err error
		removable, err = s.zfsSnapshotRemovable(fs, snap)
		if err != nil {
			return err
		}

		if !removable {
			break
		}
	}

	if removable {
		origin, err := s.zfsGet(fs, "origin")
		if err != nil {
			return err
		}
		origin = strings.TrimPrefix(origin, fmt.Sprintf("%s/", s.zfsPool))

		err = s.zfsDestroy(fs)
		if err != nil {
			return err
		}

		err = s.zfsCleanup(origin)
		if err != nil {
			return err
		}
	} else {
		err := s.zfsSet(fs, "mountpoint", "none")
		if err != nil {
			return err
		}

		err = s.zfsRename(fs, fmt.Sprintf("deleted/containers/%s", uuid.NewRandom().String()))
		if err != nil {
			return err
		}
	}

	if shared.PathExists(shared.VarPath(fs)) {
		os.Remove(shared.VarPath(fs))
		if err != nil {
			return err
		}
	}

	if shared.PathExists(shared.VarPath(fs) + ".zfs") {
		os.Remove(shared.VarPath(fs) + ".zfs")
		if err != nil {
			return err
		}
	}

	s.zfsDestroy(fmt.Sprintf("snapshots/%s", container.Name()))

	return nil
}

func (s *storageZfs) ContainerCopy(container container, sourceContainer container) error {
	var sourceFs string
	var sourceSnap string

	sourceFields := strings.SplitN(sourceContainer.Name(), shared.SnapshotDelimiter, 2)
	sourceName := sourceFields[0]

	destName := container.Name()
	destFs := fmt.Sprintf("containers/%s", destName)

	if len(sourceFields) == 2 {
		sourceSnap = sourceFields[1]
	}

	if sourceSnap == "" {
		if s.zfsExists(fmt.Sprintf("containers/%s", sourceName)) {
			sourceSnap = fmt.Sprintf("copy-%s", uuid.NewRandom().String())
			sourceFs = fmt.Sprintf("containers/%s", sourceName)
			err := s.zfsSnapshotCreate(fmt.Sprintf("containers/%s", sourceName), sourceSnap)
			if err != nil {
				return err
			}
		}
	} else {
		if s.zfsExists(fmt.Sprintf("containers/%s@snapshot-%s", sourceName, sourceSnap)) {
			sourceFs = fmt.Sprintf("containers/%s", sourceName)
			sourceSnap = fmt.Sprintf("snapshot-%s", sourceSnap)
		}
	}

	if sourceFs != "" {
		err := s.zfsClone(sourceFs, sourceSnap, destFs, true)
		if err != nil {
			return err
		}
	} else {
		err := s.ContainerCreate(container)
		if err != nil {
			return err
		}

		output, err := storageRsyncCopy(sourceContainer.Path(), container.Path())
		if err != nil {
			return fmt.Errorf("rsync failed: %s", string(output))
		}
	}

	cPath := container.Path()
	err := os.Symlink(cPath+".zfs", cPath)
	if err != nil {
		return err
	}

	var mode os.FileMode
	if container.IsPrivileged() {
		mode = 0700
	} else {
		mode = 0755
	}

	err = os.Chmod(cPath, mode)
	if err != nil {
		return err
	}

	return container.TemplateApply("copy")
}

func (s *storageZfs) zfsMounted(path string) bool {
	output, err := exec.Command("zfs", "mount").CombinedOutput()
	if err != nil {
		shared.Log.Error("error listing zfs mounts", "err", output)
		return false
	}

	for _, line := range strings.Split(string(output), "\n") {
		zfsName := strings.Split(line, " ")[0]
		if zfsName == fmt.Sprintf("%s/%s", s.zfsPool, path) {
			return true
		}
	}

	return false
}

func (s *storageZfs) ContainerRename(container container, newName string) error {
	oldName := container.Name()

	// Unmount the filesystem
	err := s.zfsUnmount(fmt.Sprintf("containers/%s", oldName))
	if err != nil {
		return err
	}

	// Rename the filesystem
	err = s.zfsRename(fmt.Sprintf("containers/%s", oldName), fmt.Sprintf("containers/%s", newName))
	if err != nil {
		return err
	}

	// Update to the new mountpoint
	err = s.zfsSet(fmt.Sprintf("containers/%s", newName), "mountpoint", shared.VarPath(fmt.Sprintf("containers/%s.zfs", newName)))
	if err != nil {
		return err
	}

	// In case ZFS didn't mount the filesystem, do it ourselves
	if !shared.PathExists(shared.VarPath(fmt.Sprintf("containers/%s.zfs", newName))) {
		for i := 0; i < 20; i++ {
			err = s.zfsMount(fmt.Sprintf("containers/%s", newName))
			if err == nil {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if err != nil {
			return err
		}
	}

	// In case the change of mountpoint didn't remove the old path, do it ourselves
	if shared.PathExists(shared.VarPath(fmt.Sprintf("containers/%s.zfs", oldName))) {
		err = os.Remove(shared.VarPath(fmt.Sprintf("containers/%s.zfs", oldName)))
		if err != nil {
			return err
		}
	}

	// Remove the old symlink
	err = os.Remove(shared.VarPath(fmt.Sprintf("containers/%s", oldName)))
	if err != nil {
		return err
	}

	// Create a new symlink
	err = os.Symlink(shared.VarPath(fmt.Sprintf("containers/%s.zfs", newName)), shared.VarPath(fmt.Sprintf("containers/%s", newName)))
	if err != nil {
		return err
	}

	// Rename the snapshot path
	if shared.PathExists(shared.VarPath(fmt.Sprintf("snapshots/%s", oldName))) {
		err = os.Rename(shared.VarPath(fmt.Sprintf("snapshots/%s", oldName)), shared.VarPath(fmt.Sprintf("snapshots/%s", newName)))
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageZfs) ContainerRestore(container container, sourceContainer container) error {
	fields := strings.SplitN(sourceContainer.Name(), shared.SnapshotDelimiter, 2)
	cName := fields[0]
	snapName := fmt.Sprintf("snapshot-%s", fields[1])

	err := s.zfsSnapshotRestore(fmt.Sprintf("containers/%s", cName), snapName)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) ContainerSetQuota(container container, size int64) error {
	var err error

	fs := fmt.Sprintf("containers/%s", container.Name())

	if size > 0 {
		err = s.zfsSet(fs, "quota", fmt.Sprintf("%d", size))
	} else {
		err = s.zfsSet(fs, "quota", "none")
	}

	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) ContainerSnapshotCreate(snapshotContainer container, sourceContainer container) error {
	fields := strings.SplitN(snapshotContainer.Name(), shared.SnapshotDelimiter, 2)
	cName := fields[0]
	snapName := fmt.Sprintf("snapshot-%s", fields[1])

	err := s.zfsSnapshotCreate(fmt.Sprintf("containers/%s", cName), snapName)
	if err != nil {
		return err
	}

	if !shared.PathExists(shared.VarPath(fmt.Sprintf("snapshots/%s", cName))) {
		err = os.MkdirAll(shared.VarPath(fmt.Sprintf("snapshots/%s", cName)), 0700)
		if err != nil {
			return err
		}
	}

	err = os.Symlink("on-zfs", shared.VarPath(fmt.Sprintf("snapshots/%s/%s.zfs", cName, fields[1])))
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) ContainerSnapshotDelete(snapshotContainer container) error {
	fields := strings.SplitN(snapshotContainer.Name(), shared.SnapshotDelimiter, 2)
	cName := fields[0]
	snapName := fmt.Sprintf("snapshot-%s", fields[1])

	removable, err := s.zfsSnapshotRemovable(fmt.Sprintf("containers/%s", cName), snapName)
	if removable {
		err = s.zfsSnapshotDestroy(fmt.Sprintf("containers/%s", cName), snapName)
		if err != nil {
			return err
		}
	} else {
		err = s.zfsSnapshotRename(fmt.Sprintf("containers/%s", cName), snapName, fmt.Sprintf("copy-%s", uuid.NewRandom().String()))
		if err != nil {
			return err
		}
	}

	err = os.Remove(shared.VarPath(fmt.Sprintf("snapshots/%s/%s.zfs", cName, fields[1])))
	if err != nil {
		return err
	}

	parent := shared.VarPath(fmt.Sprintf("snapshots/%s", cName))
	if ok, _ := shared.PathIsEmpty(parent); ok {
		err = os.Remove(parent)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageZfs) ContainerSnapshotRename(snapshotContainer container, newName string) error {
	oldFields := strings.SplitN(snapshotContainer.Name(), shared.SnapshotDelimiter, 2)
	oldcName := oldFields[0]
	oldName := fmt.Sprintf("snapshot-%s", oldFields[1])

	newFields := strings.SplitN(newName, shared.SnapshotDelimiter, 2)
	newcName := newFields[0]
	newName = fmt.Sprintf("snapshot-%s", newFields[1])

	if oldName != newName {
		err := s.zfsSnapshotRename(fmt.Sprintf("containers/%s", oldcName), oldName, newName)
		if err != nil {
			return err
		}
	}

	err := os.Remove(shared.VarPath(fmt.Sprintf("snapshots/%s/%s.zfs", oldcName, oldFields[1])))
	if err != nil {
		return err
	}

	if !shared.PathExists(shared.VarPath(fmt.Sprintf("snapshots/%s", newcName))) {
		err = os.MkdirAll(shared.VarPath(fmt.Sprintf("snapshots/%s", newcName)), 0700)
		if err != nil {
			return err
		}
	}

	err = os.Symlink("on-zfs", shared.VarPath(fmt.Sprintf("snapshots/%s/%s.zfs", newcName, newFields[1])))
	if err != nil {
		return err
	}

	parent := shared.VarPath(fmt.Sprintf("snapshots/%s", oldcName))
	if ok, _ := shared.PathIsEmpty(parent); ok {
		err = os.Remove(parent)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageZfs) ContainerSnapshotStart(container container) error {
	fields := strings.SplitN(container.Name(), shared.SnapshotDelimiter, 2)
	if len(fields) < 2 {
		return fmt.Errorf("Invalid snapshot name: %s", container.Name())
	}
	cName := fields[0]
	sName := fields[1]
	sourceFs := fmt.Sprintf("containers/%s", cName)
	sourceSnap := fmt.Sprintf("snapshot-%s", sName)
	destFs := fmt.Sprintf("snapshots/%s/%s", cName, sName)

	err := s.zfsClone(sourceFs, sourceSnap, destFs, false)
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) ContainerSnapshotStop(container container) error {
	fields := strings.SplitN(container.Name(), shared.SnapshotDelimiter, 2)
	if len(fields) < 2 {
		return fmt.Errorf("Invalid snapshot name: %s", container.Name())
	}
	cName := fields[0]
	sName := fields[1]
	destFs := fmt.Sprintf("snapshots/%s/%s", cName, sName)

	err := s.zfsDestroy(destFs)
	if err != nil {
		return err
	}

	/* zfs creates this directory on clone (start), so we need to clean it
	 * up on stop */
	return os.RemoveAll(container.Path())
}

func (s *storageZfs) ContainerSnapshotCreateEmpty(snapshotContainer container) error {
	/* don't touch the fs yet, as migration will do that for us */
	return nil
}

func (s *storageZfs) ImageCreate(fingerprint string) error {
	imagePath := shared.VarPath("images", fingerprint)
	subvol := fmt.Sprintf("%s.zfs", imagePath)
	fs := fmt.Sprintf("images/%s", fingerprint)

	if s.zfsExists(fmt.Sprintf("deleted/%s", fs)) {
		err := s.zfsRename(fmt.Sprintf("deleted/%s", fs), fs)
		if err != nil {
			return err
		}

		err = s.zfsSet(fs, "mountpoint", subvol)
		if err != nil {
			return err
		}

		return nil
	}

	err := s.zfsCreate(fs)
	if err != nil {
		return err
	}

	err = untarImage(imagePath, subvol)
	if err != nil {
		return err
	}

	err = s.zfsSet(fs, "readonly", "on")
	if err != nil {
		return err
	}

	err = s.zfsSnapshotCreate(fs, "readonly")
	if err != nil {
		return err
	}

	return nil
}

func (s *storageZfs) ImageDelete(fingerprint string) error {
	fs := fmt.Sprintf("images/%s", fingerprint)

	removable, err := s.zfsSnapshotRemovable(fs, "readonly")

	if err != nil {
		return err
	}

	if removable {
		err := s.zfsDestroy(fs)
		if err != nil {
			return err
		}
	} else {
		err := s.zfsSet(fs, "mountpoint", "none")
		if err != nil {
			return err
		}

		err = s.zfsRename(fs, fmt.Sprintf("deleted/%s", fs))
		if err != nil {
			return err
		}
	}

	if shared.PathExists(shared.VarPath(fs + ".zfs")) {
		os.Remove(shared.VarPath(fs + ".zfs"))
	}
	return nil
}

// Helper functions
func (s *storageZfs) zfsCheckPool(pool string) error {
	output, err := exec.Command(
		"zfs", "get", "type", "-H", "-o", "value", pool).CombinedOutput()
	if err != nil {
		return fmt.Errorf(strings.Split(string(output), "\n")[0])
	}

	poolType := strings.Split(string(output), "\n")[0]
	if poolType != "filesystem" {
		return fmt.Errorf("Unsupported pool type: %s", poolType)
	}

	return nil
}

func (s *storageZfs) zfsClone(source string, name string, dest string, dotZfs bool) error {
	var mountpoint string

	mountpoint = shared.VarPath(dest)
	if dotZfs {
		mountpoint += ".zfs"
	}

	output, err := exec.Command(
		"zfs",
		"clone",
		"-p",
		"-o", fmt.Sprintf("mountpoint=%s", mountpoint),
		fmt.Sprintf("%s/%s@%s", s.zfsPool, source, name),
		fmt.Sprintf("%s/%s", s.zfsPool, dest)).CombinedOutput()
	if err != nil {
		s.log.Error("zfs clone failed", log.Ctx{"output": string(output)})
		return fmt.Errorf("Failed to clone the filesystem: %s", output)
	}

	subvols, err := s.zfsListSubvolumes(source)
	if err != nil {
		return err
	}

	for _, sub := range subvols {
		snaps, err := s.zfsListSnapshots(sub)
		if err != nil {
			return err
		}

		if !shared.StringInSlice(name, snaps) {
			continue
		}

		destSubvol := dest + strings.TrimPrefix(sub, source)
		mountpoint = shared.VarPath(destSubvol)
		if dotZfs {
			mountpoint += ".zfs"
		}

		output, err := exec.Command(
			"zfs",
			"clone",
			"-p",
			"-o", fmt.Sprintf("mountpoint=%s", mountpoint),
			fmt.Sprintf("%s/%s@%s", s.zfsPool, sub, name),
			fmt.Sprintf("%s/%s", s.zfsPool, destSubvol)).CombinedOutput()
		if err != nil {
			s.log.Error("zfs clone failed", log.Ctx{"output": string(output)})
			return fmt.Errorf("Failed to clone the sub-volume: %s", output)
		}
	}

	return nil
}

func (s *storageZfs) zfsCreate(path string) error {
	output, err := exec.Command(
		"zfs",
		"create",
		"-p",
		"-o", fmt.Sprintf("mountpoint=%s.zfs", shared.VarPath(path)),
		fmt.Sprintf("%s/%s", s.zfsPool, path)).CombinedOutput()
	if err != nil {
		s.log.Error("zfs create failed", log.Ctx{"output": string(output)})
		return fmt.Errorf("Failed to create ZFS filesystem: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsDestroy(path string) error {
	mountpoint, err := s.zfsGet(path, "mountpoint")
	if err != nil {
		return err
	}

	if mountpoint != "none" && shared.IsMountPoint(mountpoint) {
		err := syscall.Unmount(mountpoint, syscall.MNT_DETACH)
		if err != nil {
			s.log.Error("umount failed", log.Ctx{"err": err})
			return err
		}
	}

	// Due to open fds or kernel refs, this may fail for a bit, give it 10s
	var output []byte
	for i := 0; i < 20; i++ {
		output, err = exec.Command(
			"zfs",
			"destroy",
			"-r",
			fmt.Sprintf("%s/%s", s.zfsPool, path)).CombinedOutput()

		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		s.log.Error("zfs destroy failed", log.Ctx{"output": string(output)})
		return fmt.Errorf("Failed to destroy ZFS filesystem: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsCleanup(path string) error {
	if strings.HasPrefix(path, "deleted/") {
		removablePath, err := s.zfsSnapshotRemovable(path, "")
		if err != nil {
			return err
		}

		if removablePath {
			subPath := strings.SplitN(path, "@", 2)[0]

			origin, err := s.zfsGet(subPath, "origin")
			if err != nil {
				return err
			}
			origin = strings.TrimPrefix(origin, fmt.Sprintf("%s/", s.zfsPool))

			err = s.zfsDestroy(subPath)
			if err != nil {
				return err
			}

			s.zfsCleanup(origin)

			return nil
		}
	}

	return nil
}

func (s *storageZfs) zfsExists(path string) bool {
	output, _ := s.zfsGet(path, "name")

	if output == fmt.Sprintf("%s/%s", s.zfsPool, path) {
		return true
	}

	return false
}

func (s *storageZfs) zfsGet(path string, key string) (string, error) {
	output, err := exec.Command(
		"zfs",
		"get",
		"-H",
		"-o", "value",
		key,
		fmt.Sprintf("%s/%s", s.zfsPool, path)).CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("Failed to get ZFS config: %s", output)
	}

	return strings.TrimRight(string(output), "\n"), nil
}

func (s *storageZfs) zfsMount(path string) error {
	output, err := exec.Command(
		"zfs",
		"mount",
		fmt.Sprintf("%s/%s", s.zfsPool, path)).CombinedOutput()
	if err != nil {
		s.log.Error("zfs mount failed", log.Ctx{"output": string(output)})
		return fmt.Errorf("Failed to mount ZFS filesystem: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsRename(source string, dest string) error {
	output, err := exec.Command(
		"zfs",
		"rename",
		"-p",
		fmt.Sprintf("%s/%s", s.zfsPool, source),
		fmt.Sprintf("%s/%s", s.zfsPool, dest)).CombinedOutput()
	if err != nil {
		if s.zfsExists(source) || !s.zfsExists(dest) {
			s.log.Error("zfs rename failed", log.Ctx{"output": string(output)})
			return fmt.Errorf("Failed to rename ZFS filesystem: %s", output)
		}
	}

	return nil
}

func (s *storageZfs) zfsSet(path string, key string, value string) error {
	output, err := exec.Command(
		"zfs",
		"set",
		fmt.Sprintf("%s=%s", key, value),
		fmt.Sprintf("%s/%s", s.zfsPool, path)).CombinedOutput()
	if err != nil {
		s.log.Error("zfs set failed", log.Ctx{"output": string(output)})
		return fmt.Errorf("Failed to set ZFS config: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsSnapshotCreate(path string, name string) error {
	output, err := exec.Command(
		"zfs",
		"snapshot",
		"-r",
		fmt.Sprintf("%s/%s@%s", s.zfsPool, path, name)).CombinedOutput()
	if err != nil {
		s.log.Error("zfs snapshot failed", log.Ctx{"output": string(output)})
		return fmt.Errorf("Failed to create ZFS snapshot: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsSnapshotDestroy(path string, name string) error {
	output, err := exec.Command(
		"zfs",
		"destroy",
		"-r",
		fmt.Sprintf("%s/%s@%s", s.zfsPool, path, name)).CombinedOutput()
	if err != nil {
		s.log.Error("zfs destroy failed", log.Ctx{"output": string(output)})
		return fmt.Errorf("Failed to destroy ZFS snapshot: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsSnapshotRestore(path string, name string) error {
	output, err := exec.Command(
		"zfs",
		"rollback",
		fmt.Sprintf("%s/%s@%s", s.zfsPool, path, name)).CombinedOutput()
	if err != nil {
		s.log.Error("zfs rollback failed", log.Ctx{"output": string(output)})
		return fmt.Errorf("Failed to restore ZFS snapshot: %s", output)
	}

	subvols, err := s.zfsListSubvolumes(path)
	if err != nil {
		return err
	}

	for _, sub := range subvols {
		snaps, err := s.zfsListSnapshots(sub)
		if err != nil {
			return err
		}

		if !shared.StringInSlice(name, snaps) {
			continue
		}

		output, err := exec.Command(
			"zfs",
			"rollback",
			fmt.Sprintf("%s/%s@%s", s.zfsPool, sub, name)).CombinedOutput()
		if err != nil {
			s.log.Error("zfs rollback failed", log.Ctx{"output": string(output)})
			return fmt.Errorf("Failed to restore ZFS sub-volume snapshot: %s", output)
		}
	}

	return nil
}

func (s *storageZfs) zfsSnapshotRename(path string, oldName string, newName string) error {
	output, err := exec.Command(
		"zfs",
		"rename",
		"-r",
		fmt.Sprintf("%s/%s@%s", s.zfsPool, path, oldName),
		fmt.Sprintf("%s/%s@%s", s.zfsPool, path, newName)).CombinedOutput()
	if err != nil {
		s.log.Error("zfs snapshot rename failed", log.Ctx{"output": string(output)})
		return fmt.Errorf("Failed to rename ZFS snapshot: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsUnmount(path string) error {
	output, err := exec.Command(
		"zfs",
		"unmount",
		fmt.Sprintf("%s/%s", s.zfsPool, path)).CombinedOutput()
	if err != nil {
		s.log.Error("zfs unmount failed", log.Ctx{"output": string(output)})
		return fmt.Errorf("Failed to unmount ZFS filesystem: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsListSubvolumes(path string) ([]string, error) {
	path = strings.TrimRight(path, "/")
	fullPath := s.zfsPool
	if path != "" {
		fullPath = fmt.Sprintf("%s/%s", s.zfsPool, path)
	}

	output, err := exec.Command(
		"zfs",
		"list",
		"-t", "filesystem",
		"-o", "name",
		"-H",
		"-r", fullPath).CombinedOutput()
	if err != nil {
		s.log.Error("zfs list failed", log.Ctx{"output": string(output)})
		return []string{}, fmt.Errorf("Failed to list ZFS filesystems: %s", output)
	}

	children := []string{}
	for _, entry := range strings.Split(string(output), "\n") {
		if entry == "" {
			continue
		}

		if entry == fullPath {
			continue
		}

		children = append(children, strings.TrimPrefix(entry, fmt.Sprintf("%s/", s.zfsPool)))
	}

	return children, nil
}

func (s *storageZfs) zfsListSnapshots(path string) ([]string, error) {
	path = strings.TrimRight(path, "/")
	fullPath := s.zfsPool
	if path != "" {
		fullPath = fmt.Sprintf("%s/%s", s.zfsPool, path)
	}

	output, err := exec.Command(
		"zfs",
		"list",
		"-t", "snapshot",
		"-o", "name",
		"-H",
		"-d", "1",
		"-s", "creation",
		"-r", fullPath).CombinedOutput()
	if err != nil {
		s.log.Error("zfs list failed", log.Ctx{"output": string(output)})
		return []string{}, fmt.Errorf("Failed to list ZFS snapshots: %s", output)
	}

	children := []string{}
	for _, entry := range strings.Split(string(output), "\n") {
		if entry == "" {
			continue
		}

		if entry == fullPath {
			continue
		}

		children = append(children, strings.SplitN(entry, "@", 2)[1])
	}

	return children, nil
}

func (s *storageZfs) zfsSnapshotRemovable(path string, name string) (bool, error) {
	var snap string
	if name == "" {
		snap = path
	} else {
		snap = fmt.Sprintf("%s@%s", path, name)
	}

	clones, err := s.zfsGet(snap, "clones")
	if err != nil {
		return false, err
	}

	if clones == "-" || clones == "" {
		return true, nil
	}

	return false, nil
}

func (s *storageZfs) zfsGetPoolUsers() ([]string, error) {
	subvols, err := s.zfsListSubvolumes("")
	if err != nil {
		return []string{}, err
	}

	exceptions := []string{
		"containers",
		"images",
		"snapshots",
		"deleted",
		"deleted/containers",
		"deleted/images"}

	users := []string{}
	for _, subvol := range subvols {
		if shared.StringInSlice(subvol, exceptions) {
			continue
		}

		users = append(users, subvol)
	}

	return users, nil
}

// Global functions
func storageZFSSetPoolNameConfig(d *Daemon, poolname string) error {
	s := storageZfs{}

	// Confirm the backend is working
	err := s.initShared()
	if err != nil {
		return fmt.Errorf("Unable to initialize the ZFS backend: %v", err)
	}

	// Confirm the new pool exists and is compatible
	if poolname != "" {
		err = s.zfsCheckPool(poolname)
		if err != nil {
			return fmt.Errorf("Invalid ZFS pool: %v", err)
		}
	}

	// Check if we're switching pools
	oldPoolname, err := d.ConfigValueGet("storage.zfs_pool_name")
	if err != nil {
		return err
	}

	// Confirm the old pool isn't in use anymore
	if oldPoolname != "" {
		s.zfsPool = oldPoolname

		users, err := s.zfsGetPoolUsers()
		if err != nil {
			return fmt.Errorf("Error checking if a pool is already in use: %v", err)
		}

		if len(users) > 0 {
			return fmt.Errorf("Can not change ZFS config. Images or containers are still using the ZFS pool: %v", users)
		}
	}
	s.zfsPool = poolname

	// All good, set the new pool name
	err = d.ConfigValueSet("storage.zfs_pool_name", poolname)
	if err != nil {
		return err
	}

	return nil
}

type zfsMigrationSource struct {
	lxdName            string
	deleteAfterSending bool
	zfsName            string
	zfsParent          string

	zfs *storageZfs
}

func (s zfsMigrationSource) Name() string {
	return s.lxdName
}

func (s zfsMigrationSource) IsSnapshot() bool {
	return !s.deleteAfterSending
}

func (s zfsMigrationSource) Send(conn *websocket.Conn) error {
	args := []string{"send", fmt.Sprintf("%s/%s", s.zfs.zfsPool, s.zfsName)}
	if s.zfsParent != "" {
		args = append(args, "-i", fmt.Sprintf("%s/%s", s.zfs.zfsPool, s.zfsParent))
	}

	cmd := exec.Command("zfs", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		/* If this is not a lxd snapshot, that means it is the root container.
		 * The way we zfs send a root container is by taking a temporary zfs
		 * snapshot and sending that, then deleting that snapshot. Here's where
		 * we delete it.
		 *
		 * Note that we can't use a defer here, because zfsDestroy
		 * takes some time, and defer doesn't block the current
		 * goroutine. Due to our retry mechanism for network failures
		 * (and because zfsDestroy takes a while), we might retry
		 * moving (and thus creating a temporary snapshot) before the
		 * last one is deleted, resulting in either a snapshot name
		 * collision if it was fast enough, or an extra snapshot with
		 * an odd name on the destination side. Instead, we don't use
		 * defer so we always block until the snapshot is dead.
		 */
		if s.deleteAfterSending {
			s.zfs.zfsDestroy(s.zfsName)
		}
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		if s.deleteAfterSending {
			s.zfs.zfsDestroy(s.zfsName)
		}
		return err
	}

	if err := cmd.Start(); err != nil {
		if s.deleteAfterSending {
			s.zfs.zfsDestroy(s.zfsName)
		}
		return err
	}

	<-shared.WebsocketSendStream(conn, stdout)

	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		shared.Log.Error("problem reading zfs send stderr", "err", err)
	}

	err = cmd.Wait()
	if err != nil {
		shared.Log.Error("problem with zfs send", "output", string(output))
	}
	if s.deleteAfterSending {
		s.zfs.zfsDestroy(s.zfsName)
	}
	return err
}

func (s *storageZfs) MigrationType() MigrationFSType {
	return MigrationFSType_ZFS
}

func (s *storageZfs) MigrationSource(container container) ([]MigrationStorageSource, error) {
	sources := []MigrationStorageSource{}

	/* If the container is a snapshot, let's just send that; we don't need
	 * to send anything else, because that's all the user asked for.
	 */
	if container.IsSnapshot() {
		fields := strings.SplitN(container.Name(), shared.SnapshotDelimiter, 2)
		snapshotName := fmt.Sprintf("containers/%s@snapshot-%s", fields[0], fields[1])
		sources = append(sources, zfsMigrationSource{container.Name(), false, snapshotName, "", s})
		return sources, nil
	}

	/* List all the snapshots in order of reverse creation. The idea here
	 * is that we send the oldest to newest snapshot, hopefully saving on
	 * xfer costs. Then, after all that, we send the container itself.
	 */
	snapshots, err := s.zfsListSnapshots(fmt.Sprintf("containers/%s", container.Name()))
	if err != nil {
		return nil, err
	}

	for i, snap := range snapshots {
		/* In the case of e.g. multiple copies running at the same
		 * time, we will have potentially multiple migration-send
		 * snapshots. (Or in the case of the test suite, sometimes one
		 * will take too long to delete.)
		 */
		if !strings.HasPrefix(snap, "snapshot-") {
			continue
		}

		prev := ""
		if i > 0 {
			prev = snapshots[i-1]
		}

		lxdName := fmt.Sprintf("%s%s%s", container.Name(), shared.SnapshotDelimiter, snap[len("snapshot-"):])
		zfsName := fmt.Sprintf("containers/%s@%s", container.Name(), snap)
		parentName := ""
		if prev != "" {
			parentName = fmt.Sprintf("containers/%s@%s", container.Name(), prev)
		}

		sources = append(sources, zfsMigrationSource{lxdName, false, zfsName, parentName, s})
	}

	/* We can't send running fses, so let's snapshot the fs and send
	 * the snapshot.
	 */
	snapshotName := fmt.Sprintf("migration-send-%s", uuid.NewRandom().String())
	if err := s.zfsSnapshotCreate(fmt.Sprintf("containers/%s", container.Name()), snapshotName); err != nil {
		return nil, err
	}

	zfsName := fmt.Sprintf("containers/%s@%s", container.Name(), snapshotName)
	zfsParent := ""
	if len(sources) > 0 {
		zfsParent = sources[len(sources)-1].(zfsMigrationSource).zfsName
	}

	sources = append(sources, zfsMigrationSource{container.Name(), true, zfsName, zfsParent, s})

	return sources, nil
}

func (s *storageZfs) MigrationSink(container container, snapshots []container, conn *websocket.Conn) error {
	zfsRecv := func(zfsName string) error {
		zfsFsName := fmt.Sprintf("%s/%s", s.zfsPool, zfsName)
		args := []string{"receive", "-F", "-u", zfsFsName}
		cmd := exec.Command("zfs", args...)

		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			return err
		}

		if err := cmd.Start(); err != nil {
			return err
		}

		<-shared.WebsocketRecvStream(stdin, conn)

		output, err := ioutil.ReadAll(stderr)
		if err != nil {
			shared.Debugf("problem reading zfs recv stderr %s", "err", err)
		}

		err = cmd.Wait()
		if err != nil {
			shared.Log.Error("problem with zfs recv", "output", string(output))
		}
		return err
	}

	/* In some versions of zfs we can write `zfs recv -F` to mounted
	 * filesystems, and in some versions we can't. So, let's always unmount
	 * this fs (it's empty anyway) before we zfs recv. N.B. that `zfs recv`
	 * of a snapshot also needs tha actual fs that it has snapshotted
	 * unmounted, so we do this before receiving anything.
	 *
	 * Further, `zfs unmount` doesn't actually unmount things right away,
	 * so we ask /proc/self/mountinfo whether or not this path is mounted
	 * before continuing so that we're sure the fs is actually unmounted
	 * before doing a recv.
	 */
	zfsName := fmt.Sprintf("containers/%s", container.Name())
	fsPath := shared.VarPath(fmt.Sprintf("containers/%s.zfs", container.Name()))
	for i := 0; i < 20; i++ {
		if shared.IsMountPoint(fsPath) || s.zfsMounted(zfsName) {
			if err := s.zfsUnmount(zfsName); err != nil {
				shared.Log.Error("zfs umount error for", "path", zfsName, "err", err)
			}
		} else {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	for _, snap := range snapshots {
		fields := strings.SplitN(snap.Name(), shared.SnapshotDelimiter, 2)
		name := fmt.Sprintf("containers/%s@snapshot-%s", fields[0], fields[1])
		if err := zfsRecv(name); err != nil {
			return err
		}

		err := os.MkdirAll(shared.VarPath(fmt.Sprintf("snapshots/%s", fields[0])), 0700)
		if err != nil {
			return err
		}

		err = os.Symlink("on-zfs", shared.VarPath(fmt.Sprintf("snapshots/%s/%s.zfs", fields[0], fields[1])))
		if err != nil {
			return err
		}
	}

	/* finally, do the real container */
	if err := zfsRecv(zfsName); err != nil {
		return err
	}

	/* Sometimes, zfs recv mounts this anyway, even if we pass -u
	 * (https://forums.freebsd.org/threads/zfs-receive-u-shouldnt-mount-received-filesystem-right.36844/)
	 * but sometimes it doesn't. Let's try to mount, but not complain about
	 * failure.
	 */
	s.zfsMount(zfsName)
	return nil
}
