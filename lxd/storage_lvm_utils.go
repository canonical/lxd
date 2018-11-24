package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
	"github.com/pkg/errors"
)

func (s *storageLvm) lvExtend(lvPath string, lvSize int64, fsType string, fsMntPoint string, volumeType int, data interface{}) error {
	// Round the size to closest 512 bytes
	lvSize = int64(lvSize/512) * 512
	lvSizeString := shared.GetByteSizeString(lvSize, 0)

	msg, err := shared.TryRunCommand(
		"lvextend",
		"-L", lvSizeString,
		"-f",
		lvPath)
	if err != nil {
		logger.Errorf("Could not extend LV \"%s\": %s", lvPath, msg)
		return fmt.Errorf("could not extend LV \"%s\": %s", lvPath, msg)
	}

	switch volumeType {
	case storagePoolVolumeTypeContainer:
		c := data.(container)
		ourMount, err := c.StorageStart()
		if err != nil {
			return err
		}
		if ourMount {
			defer c.StorageStop()
		}
	case storagePoolVolumeTypeCustom:
		ourMount, err := s.StoragePoolVolumeMount()
		if err != nil {
			return err
		}
		if ourMount {
			defer s.StoragePoolVolumeUmount()
		}
	default:
		return fmt.Errorf(`Resizing not implemented for storage `+
			`volume type %d`, volumeType)
	}

	return growFileSystem(fsType, lvPath, fsMntPoint)
}

func (s *storageLvm) lvReduce(lvPath string, lvSize int64, fsType string, fsMntPoint string, volumeType int, data interface{}) error {
	var err error
	var msg string

	// Round the size to closest 512 bytes
	lvSize = int64(lvSize/512) * 512
	lvSizeString := shared.GetByteSizeString(lvSize, 0)

	cleanupFunc, err := shrinkVolumeFilesystem(s, volumeType, fsType, lvPath, fsMntPoint, lvSize, data)
	if cleanupFunc != nil {
		defer cleanupFunc()
	}
	if err != nil {
		return err
	}

	msg, err = shared.TryRunCommand(
		"lvreduce",
		"-L", lvSizeString,
		"-f",
		lvPath)
	if err != nil {
		logger.Errorf("Could not reduce LV \"%s\": %s", lvPath, msg)
		return fmt.Errorf("could not reduce LV \"%s\": %s", lvPath, msg)
	}

	logger.Debugf("Reduced underlying %s filesystem for LV \"%s\"", fsType, lvPath)
	return nil
}

func (s *storageLvm) getLvmMountOptions() string {
	if s.volume.Config["block.mount_options"] != "" {
		return s.volume.Config["block.mount_options"]
	}

	if s.pool.Config["volume.block.mount_options"] != "" {
		return s.pool.Config["volume.block.mount_options"]
	}

	if s.getLvmFilesystem() == "btrfs" {
		return "user_subvol_rm_allowed,discard"
	}

	return "discard"
}

func (s *storageLvm) getLvmFilesystem() string {
	if s.volume.Config["block.filesystem"] != "" {
		return s.volume.Config["block.filesystem"]
	}

	if s.pool.Config["volume.block.filesystem"] != "" {
		return s.pool.Config["volume.block.filesystem"]
	}

	return "ext4"
}

func (s *storageLvm) getLvmVolumeSize() (string, error) {
	sz, err := shared.ParseByteSizeString(s.volume.Config["size"])
	if err != nil {
		return "", err
	}

	// Safety net: Set to default value.
	if sz == 0 {
		sz, _ = shared.ParseByteSizeString("10GB")
	}

	return fmt.Sprintf("%d", sz), nil
}

func (s *storageLvm) getLvmThinpoolName() string {
	if s.pool.Config["lvm.thinpool_name"] != "" {
		return s.pool.Config["lvm.thinpool_name"]
	}

	return "LXDThinPool"
}

func (s *storageLvm) usesThinpool() bool {
	// Default is to use a thinpool.
	if s.pool.Config["lvm.use_thinpool"] == "" {
		return true
	}

	return shared.IsTrue(s.pool.Config["lvm.use_thinpool"])
}

func (s *storageLvm) setLvmThinpoolName(newThinpoolName string) {
	s.pool.Config["lvm.thinpool_name"] = newThinpoolName
}

func (s *storageLvm) getOnDiskPoolName() string {
	if s.vgName != "" {
		return s.vgName
	}

	return s.pool.Name
}

func (s *storageLvm) setOnDiskPoolName(newName string) {
	s.vgName = newName
	s.pool.Config["source"] = newName
}

func (s *storageLvm) renameLVByPath(project, oldName string, newName string, volumeType string) error {
	oldLvmName := getPrefixedLvName(project, volumeType, oldName)
	newLvmName := getPrefixedLvName(project, volumeType, newName)
	poolName := s.getOnDiskPoolName()
	return lvmLVRename(poolName, oldLvmName, newLvmName)
}

func removeLV(project, vgName string, volumeType string, lvName string) error {
	lvmVolumePath := getLvmDevPath(project, vgName, volumeType, lvName)
	output, err := shared.TryRunCommand("lvremove", "-f", lvmVolumePath)

	if err != nil {
		logger.Errorf("Could not remove LV \"%s\": %s", lvName, output)
		return fmt.Errorf("Could not remove LV named %s", lvName)
	}

	return nil
}

func (s *storageLvm) createSnapshotLV(project, vgName string, origLvName string, origVolumeType string, lvName string, volumeType string, readonly bool, makeThinLv bool) (string, error) {
	sourceProject := project
	if origVolumeType == storagePoolVolumeAPIEndpointImages {
		// Image volumes are shared across projects.
		sourceProject = "default"
	}

	sourceLvmVolumePath := getLvmDevPath(sourceProject, vgName, origVolumeType, origLvName)
	isRecent, err := lvmVersionIsAtLeast(s.sTypeVersion, "2.02.99")
	if err != nil {
		return "", fmt.Errorf("Error checking LVM version: %v", err)
	}

	lvmPoolVolumeName := getPrefixedLvName(project, volumeType, lvName)
	var output string
	args := []string{"-n", lvmPoolVolumeName, "-s", sourceLvmVolumePath}
	if isRecent {
		args = append(args, "-kn")
	}

	// If the source is not a thin volume the size needs to be specified.
	// According to LVM tools 15-20% of the original volume should be
	// sufficient. However, let's not be stingy at first otherwise we might
	// force users to fiddle around with lvextend.
	if !makeThinLv {
		lvSize, err := s.getLvmVolumeSize()
		if lvSize == "" {
			return "", err
		}

		// Round the size to closest 512 bytes
		lvSizeInt, err := shared.ParseByteSizeString(lvSize)
		if err != nil {
			return "", err
		}

		lvSizeInt = int64(lvSizeInt/512) * 512
		lvSizeString := shared.GetByteSizeString(lvSizeInt, 0)

		args = append(args, "--size", lvSizeString)
	}

	if readonly {
		args = append(args, "-pr")
	} else {
		args = append(args, "-prw")
	}

	output, err = shared.TryRunCommand("lvcreate", args...)
	if err != nil {
		logger.Errorf("Could not create LV snapshot: %s to %s: %s", origLvName, lvName, output)
		return "", fmt.Errorf("Could not create snapshot LV named %s", lvName)
	}

	targetLvmVolumePath := getLvmDevPath(project, vgName, volumeType, lvName)
	if makeThinLv {
		// Snapshots of thin logical volumes can be directly activated.
		// Normal snapshots will complain about changing the origin
		// (Which they never do.), so skip the activation since the
		// logical volume will be automatically activated anyway.
		err := storageLVActivate(targetLvmVolumePath)
		if err != nil {
			return "", errors.Wrap(err, "Activate LVM volume")
		}
	}

	return targetLvmVolumePath, nil
}

func (s *storageLvm) createSnapshotContainer(snapshotContainer container, sourceContainer container, readonly bool) error {
	tryUndo := true

	sourceContainerName := sourceContainer.Name()
	targetContainerName := snapshotContainer.Name()
	sourceContainerLvmName := containerNameToLVName(sourceContainerName)
	targetContainerLvmName := containerNameToLVName(targetContainerName)
	logger.Debugf("Creating snapshot: %s to %s", sourceContainerName, targetContainerName)

	poolName := s.getOnDiskPoolName()
	_, err := s.createSnapshotLV(sourceContainer.Project(), poolName, sourceContainerLvmName, storagePoolVolumeAPIEndpointContainers, targetContainerLvmName, storagePoolVolumeAPIEndpointContainers, readonly, s.useThinpool)
	if err != nil {
		return fmt.Errorf("Error creating snapshot LV: %s", err)
	}
	defer func() {
		if tryUndo {
			s.ContainerDelete(snapshotContainer)
		}
	}()

	targetContainerMntPoint := ""
	targetContainerPath := snapshotContainer.Path()
	targetIsSnapshot := snapshotContainer.IsSnapshot()
	targetPool, err := snapshotContainer.StoragePool()
	if err != nil {
		return errors.Wrap(err, "Get snapshot storage pool")
	}
	if targetIsSnapshot {
		targetContainerMntPoint = getSnapshotMountPoint(sourceContainer.Project(), s.pool.Name, targetContainerName)
		sourceName, _, _ := containerGetParentAndSnapshotName(sourceContainerName)
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", projectPrefix(sourceContainer.Project(), sourceName))
		snapshotMntPointSymlink := shared.VarPath("snapshots", projectPrefix(sourceContainer.Project(), sourceName))
		err = createSnapshotMountpoint(targetContainerMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	} else {
		targetContainerMntPoint = getContainerMountPoint(sourceContainer.Project(), targetPool, targetContainerName)
		err = createContainerMountpoint(targetContainerMntPoint, targetContainerPath, snapshotContainer.IsPrivileged())
	}
	if err != nil {
		return errors.Wrap(err, "Create mount point")
	}

	tryUndo = false

	return nil
}

// Copy a container on a storage pool that does use a thinpool.
func (s *storageLvm) copyContainerThinpool(target container, source container, readonly bool) error {
	err := s.createSnapshotContainer(target, source, readonly)
	if err != nil {
		logger.Errorf("Error creating snapshot LV for copy: %s", err)
		return err
	}

	// Generate a new xfs's UUID
	LVFilesystem := s.getLvmFilesystem()
	poolName := s.getOnDiskPoolName()
	containerName := target.Name()
	containerLvmName := containerNameToLVName(containerName)
	containerLvDevPath := getLvmDevPath(target.Project(), poolName,
		storagePoolVolumeAPIEndpointContainers, containerLvmName)

	// If btrfstune sees two btrfs filesystems with the same UUID it
	// gets confused and wants both of them unmounted. So unmount
	// the source as well.
	if LVFilesystem == "btrfs" {
		ourUmount, err := s.ContainerUmount(source, source.Path())
		if err != nil {
			return err
		}

		if ourUmount {
			defer s.ContainerMount(source)
		}
	}

	msg, err := fsGenerateNewUUID(LVFilesystem, containerLvDevPath)
	if err != nil {
		logger.Errorf("Failed to create new \"%s\" UUID for container \"%s\" on storage pool \"%s\": %s", LVFilesystem, containerName, s.pool.Name, msg)
		return err
	}

	return nil
}

func (s *storageLvm) copySnapshot(target container, source container, refresh bool) error {
	sourcePool, err := source.StoragePool()
	if err != nil {
		return err
	}

	targetParentName, _, _ := containerGetParentAndSnapshotName(target.Name())
	containersPath := getSnapshotMountPoint(target.Project(), s.pool.Name, targetParentName)
	snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", projectPrefix(target.Project(), targetParentName))
	snapshotMntPointSymlink := shared.VarPath("snapshots", projectPrefix(target.Project(), targetParentName))
	err = createSnapshotMountpoint(containersPath, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	if err != nil {
		return err
	}

	if s.useThinpool && sourcePool == s.pool.Name && !refresh {
		err = s.copyContainerThinpool(target, source, true)
	} else {
		err = s.copyContainerLv(target, source, true, refresh)
	}
	if err != nil {
		logger.Errorf("Error creating snapshot LV for copy: %s", err)
		return err
	}

	return nil
}

// Copy a container on a storage pool that does not use a thinpool.
func (s *storageLvm) copyContainerLv(target container, source container, readonly bool, refresh bool) error {
	exists, err := storageLVExists(getLvmDevPath(target.Project(), s.getOnDiskPoolName(),
		storagePoolVolumeAPIEndpointContainers, containerNameToLVName(target.Name())))
	if err != nil {
		return err
	}

	// Only create container/snapshot if it doesn't already exist
	if !exists {
		err := s.ContainerCreate(target)
		if err != nil {
			return err
		}
	}

	targetName := target.Name()
	targetStart, err := target.StorageStart()
	if err != nil {
		return err
	}
	if targetStart {
		defer target.StorageStop()
	}

	sourceName := source.Name()
	sourceStart, err := source.StorageStart()
	if err != nil {
		return err
	}
	if sourceStart {
		defer source.StorageStop()
	}

	sourcePool, err := source.StoragePool()
	if err != nil {
		return err
	}
	sourceContainerMntPoint := getContainerMountPoint(source.Project(), sourcePool, sourceName)
	if source.IsSnapshot() {
		sourceContainerMntPoint = getSnapshotMountPoint(source.Project(), sourcePool, sourceName)
	}

	targetContainerMntPoint := getContainerMountPoint(target.Project(), s.pool.Name, targetName)
	if target.IsSnapshot() {
		targetContainerMntPoint = getSnapshotMountPoint(source.Project(), s.pool.Name, targetName)
	}

	if source.IsRunning() {
		err = source.Freeze()
		if err != nil {
			return err
		}
		defer source.Unfreeze()
	}

	bwlimit := s.pool.Config["rsync.bwlimit"]
	output, err := rsyncLocalCopy(sourceContainerMntPoint, targetContainerMntPoint, bwlimit)
	if err != nil {
		return fmt.Errorf("failed to rsync container: %s: %s", string(output), err)
	}

	if readonly {
		targetLvmName := containerNameToLVName(targetName)
		poolName := s.getOnDiskPoolName()
		output, err := shared.TryRunCommand("lvchange", "-pr", fmt.Sprintf("%s/%s_%s", poolName, storagePoolVolumeAPIEndpointContainers, targetLvmName))
		if err != nil {
			logger.Errorf("Failed to make LVM snapshot \"%s\" read-write: %s", targetName, output)
			return err
		}
	}

	return nil
}

// Copy an lvm container.
func (s *storageLvm) copyContainer(target container, source container, refresh bool) error {
	targetPool, err := target.StoragePool()
	if err != nil {
		return err
	}

	targetContainerMntPoint := getContainerMountPoint(target.Project(), targetPool, target.Name())
	err = createContainerMountpoint(targetContainerMntPoint, target.Path(), target.IsPrivileged())
	if err != nil {
		return err
	}

	sourcePool, err := source.StoragePool()
	if err != nil {
		return err
	}

	if s.useThinpool && targetPool == sourcePool && !refresh {
		// If the storage pool uses a thinpool we can have snapshots of
		// snapshots.
		err = s.copyContainerThinpool(target, source, false)
	} else {
		// If the storage pools does not use a thinpool we need to
		// perform full copies.
		err = s.copyContainerLv(target, source, false, refresh)
	}
	if err != nil {
		return err
	}

	err = s.setUnprivUserACL(source, targetContainerMntPoint)
	if err != nil {
		return err
	}

	err = target.TemplateApply("copy")
	if err != nil {
		return err
	}

	return nil
}

func (s *storageLvm) containerCreateFromImageLv(c container, fp string) error {
	containerName := c.Name()

	err := s.ContainerCreate(c)
	if err != nil {
		logger.Errorf(`Failed to create non-thinpool LVM storage volume for container "%s" on storage pool "%s": %s`, containerName, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Created non-thinpool LVM storage volume for container "%s" on storage pool "%s"`, containerName, s.pool.Name)

	containerPath := c.Path()
	_, err = s.ContainerMount(c)
	if err != nil {
		logger.Errorf(`Failed to mount non-thinpool LVM storage volume for container "%s" on storage pool "%s": %s`, containerName, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Mounted non-thinpool LVM storage volume for container "%s" on storage pool "%s"`, containerName, s.pool.Name)

	imagePath := shared.VarPath("images", fp)
	containerMntPoint := getContainerMountPoint(c.Project(), s.pool.Name, containerName)
	err = unpackImage(imagePath, containerMntPoint, storageTypeLvm, s.s.OS.RunningInUserNS)
	if err != nil {
		logger.Errorf(`Failed to unpack image "%s" into non-thinpool LVM storage volume "%s" for container "%s" on storage pool "%s": %s`, imagePath, containerMntPoint, containerName, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Unpacked image "%s" into non-thinpool LVM storage volume "%s" for container "%s" on storage pool "%s"`, imagePath, containerMntPoint, containerName, s.pool.Name)

	s.ContainerUmount(c, containerPath)

	return nil
}

func (s *storageLvm) containerCreateFromImageThinLv(c container, fp string) error {
	poolName := s.getOnDiskPoolName()
	// Check if the image already exists.
	imageLvmDevPath := getLvmDevPath("default", poolName, storagePoolVolumeAPIEndpointImages, fp)

	imageStoragePoolLockID := getImageCreateLockID(poolName, fp)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[imageStoragePoolLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore, this should not have happened")
		}
	} else {
		lxdStorageOngoingOperationMap[imageStoragePoolLockID] = make(chan bool)
		lxdStorageMapLock.Unlock()

		var imgerr error
		ok, _ := storageLVExists(imageLvmDevPath)
		if ok {
			_, volume, err := s.s.Cluster.StoragePoolNodeVolumeGetType(fp, db.StoragePoolVolumeTypeImage, s.poolID)
			if err != nil {
				return errors.Wrapf(err, "Fetch image volume %s", fp)
			}
			if volume.Config["block.filesystem"] != s.getLvmFilesystem() {
				// The storage pool volume.blockfilesystem property has changed, re-import the image
				err := s.ImageDelete(fp)
				if err != nil {
					return errors.Wrap(err, "Image delete")
				}
				ok = false
			}
		}

		if !ok {
			imgerr = s.ImageCreate(fp)
		}

		lxdStorageMapLock.Lock()
		if waitChannel, ok := lxdStorageOngoingOperationMap[imageStoragePoolLockID]; ok {
			close(waitChannel)
			delete(lxdStorageOngoingOperationMap, imageStoragePoolLockID)
		}
		lxdStorageMapLock.Unlock()

		if imgerr != nil {
			return errors.Wrap(imgerr, "Image create")
		}
	}

	containerName := c.Name()
	containerLvmName := containerNameToLVName(containerName)
	_, err := s.createSnapshotLV(c.Project(), poolName, fp, storagePoolVolumeAPIEndpointImages, containerLvmName, storagePoolVolumeAPIEndpointContainers, false, s.useThinpool)
	if err != nil {
		return errors.Wrap(err, "Create snapshot")
	}

	return nil
}

func lvmGetLVCount(vgName string) (int, error) {
	output, err := shared.TryRunCommand("vgs", "--noheadings", "-o", "lv_count", vgName)
	if err != nil {
		return -1, err
	}

	output = strings.TrimSpace(output)
	return strconv.Atoi(output)
}

func lvmLvIsWritable(lvName string) (bool, error) {
	output, err := shared.TryRunCommand("lvs", "--noheadings", "-o", "lv_attr", lvName)
	if err != nil {
		return false, errors.Wrapf(err, "Error retrieving attributes for logical volume %q", lvName)
	}

	output = strings.TrimSpace(output)
	return rune(output[1]) == 'w', nil
}

func storageVGActivate(lvmVolumePath string) error {
	output, err := shared.TryRunCommand("vgchange", "-ay", lvmVolumePath)
	if err != nil {
		return fmt.Errorf("could not activate volume group \"%s\": %s", lvmVolumePath, output)
	}

	return nil
}

func storageLVActivate(lvmVolumePath string) error {
	output, err := shared.TryRunCommand("lvchange", "-ay", lvmVolumePath)
	if err != nil {
		return fmt.Errorf("could not activate logival volume \"%s\": %s", lvmVolumePath, output)
	}

	return nil
}

func storagePVExists(pvName string) (bool, error) {
	_, err := shared.RunCommand("pvs", "--noheadings", "-o", "lv_attr", pvName)
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 5 {
					// physical volume not found
					return false, nil
				}
			}
		}
		return false, fmt.Errorf("error checking for physical volume \"%s\"", pvName)
	}

	return true, nil
}

func storageVGExists(vgName string) (bool, error) {
	_, err := shared.RunCommand("vgs", "--noheadings", "-o", "lv_attr", vgName)
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 5 {
					// volume group not found
					return false, nil
				}
			}
		}

		return false, fmt.Errorf("error checking for volume group \"%s\"", vgName)
	}

	return true, nil
}

func storageLVExists(lvName string) (bool, error) {
	_, err := shared.RunCommand("lvs", "--noheadings", "-o", "lv_attr", lvName)
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 5 {
					// logical volume not found
					return false, nil
				}
			}
		}

		return false, fmt.Errorf("error checking for logical volume \"%s\"", lvName)
	}

	return true, nil
}

func lvmGetLVSize(lvPath string) (string, error) {
	msg, err := shared.TryRunCommand("lvs", "--noheadings", "-o", "size", "--nosuffix", "--units", "b", lvPath)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve size of logical volume: %s: %s", string(msg), err)
	}

	sizeString := string(msg)
	sizeString = strings.TrimSpace(sizeString)
	size, err := strconv.ParseInt(sizeString, 10, 64)
	if err != nil {
		return "", err
	}

	detectedSize := shared.GetByteSizeString(size, 0)

	return detectedSize, nil
}

func storageLVMThinpoolExists(vgName string, poolName string) (bool, error) {
	output, err := shared.RunCommand("vgs", "--noheadings", "-o", "lv_attr", fmt.Sprintf("%s/%s", vgName, poolName))
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 5 {
					// pool LV was not found
					return false, nil
				}
			}
		}

		return false, fmt.Errorf("error checking for pool \"%s\"", poolName)
	}
	// Found LV named poolname, check type:
	attrs := strings.TrimSpace(string(output[:]))
	if strings.HasPrefix(attrs, "t") {
		return true, nil
	}

	return false, fmt.Errorf("pool named \"%s\" exists but is not a thin pool", poolName)
}

func storageLVMGetThinPoolUsers(s *state.State) ([]string, error) {
	results := []string{}

	cNames, err := s.Cluster.ContainersNodeList(db.CTypeRegular)
	if err != nil {
		return results, err
	}

	for _, cName := range cNames {
		var lvLinkPath string
		if strings.Contains(cName, shared.SnapshotDelimiter) {
			lvLinkPath = shared.VarPath("snapshots", fmt.Sprintf("%s.lv", cName))
		} else {
			lvLinkPath = shared.VarPath("containers", fmt.Sprintf("%s.lv", cName))
		}

		if shared.PathExists(lvLinkPath) {
			results = append(results, cName)
		}
	}

	imageNames, err := s.Cluster.ImagesGet("default", false)
	if err != nil {
		return results, err
	}

	for _, imageName := range imageNames {
		imageLinkPath := shared.VarPath("images", fmt.Sprintf("%s.lv", imageName))
		if shared.PathExists(imageLinkPath) {
			results = append(results, imageName)
		}
	}

	return results, nil
}

func storageLVMValidateThinPoolName(s *state.State, vgName string, value string) error {
	users, err := storageLVMGetThinPoolUsers(s)
	if err != nil {
		return fmt.Errorf("error checking if a pool is already in use: %v", err)
	}

	if len(users) > 0 {
		return fmt.Errorf("can not change LVM config. Images or containers are still using LVs: %v", users)
	}

	if value != "" {
		if vgName == "" {
			return fmt.Errorf("can not set lvm.thinpool_name without lvm.vg_name set")
		}

		poolExists, err := storageLVMThinpoolExists(vgName, value)
		if err != nil {
			return fmt.Errorf("error checking for thin pool \"%s\" in \"%s\": %v", value, vgName, err)
		}

		if !poolExists {
			return fmt.Errorf("pool \"'%s\" does not exist in Volume Group \"%s\"", value, vgName)
		}
	}

	return nil
}

func lvmVGRename(oldName string, newName string) error {
	output, err := shared.TryRunCommand("vgrename", oldName, newName)
	if err != nil {
		return fmt.Errorf("could not rename volume group from \"%s\" to \"%s\": %s", oldName, newName, output)
	}

	return nil
}

func lvmLVRename(vgName string, oldName string, newName string) error {
	output, err := shared.TryRunCommand("lvrename", vgName, oldName, newName)
	if err != nil {
		return fmt.Errorf("could not rename volume group from \"%s\" to \"%s\": %s", oldName, newName, output)
	}

	return nil
}

func containerNameToLVName(containerName string) string {
	lvName := strings.Replace(containerName, "-", "--", -1)
	return strings.Replace(lvName, shared.SnapshotDelimiter, "-", -1)
}

func getLvmDevPath(project, lvmPool string, volumeType string, lvmVolume string) string {
	lvmVolume = projectPrefix(project, lvmVolume)
	if volumeType == "" {
		return fmt.Sprintf("/dev/%s/%s", lvmPool, lvmVolume)
	}

	return fmt.Sprintf("/dev/%s/%s_%s", lvmPool, volumeType, lvmVolume)
}

func getLVName(lvmPool string, volumeType string, lvmVolume string) string {
	if volumeType == "" {
		return fmt.Sprintf("%s/%s", lvmPool, lvmVolume)
	}

	return fmt.Sprintf("%s/%s_%s", lvmPool, volumeType, lvmVolume)
}

func getPrefixedLvName(project, volumeType string, lvmVolume string) string {
	lvmVolume = projectPrefix(project, lvmVolume)
	return fmt.Sprintf("%s_%s", volumeType, lvmVolume)
}

func lvmCreateLv(project, vgName string, thinPoolName string, lvName string, lvFsType string, lvSize string, volumeType string, makeThinLv bool) error {
	var output string
	var err error

	// Round the size to closest 512 bytes
	lvSizeInt, err := shared.ParseByteSizeString(lvSize)
	if err != nil {
		return err
	}

	lvSizeInt = int64(lvSizeInt/512) * 512
	lvSizeString := shared.GetByteSizeString(lvSizeInt, 0)

	lvmPoolVolumeName := getPrefixedLvName(project, volumeType, lvName)
	if makeThinLv {
		targetVg := fmt.Sprintf("%s/%s", vgName, thinPoolName)
		output, err = shared.TryRunCommand("lvcreate", "--thin", "-n", lvmPoolVolumeName, "--virtualsize", lvSizeString, targetVg)
	} else {
		output, err = shared.TryRunCommand("lvcreate", "-n", lvmPoolVolumeName, "--size", lvSizeString, vgName)
	}
	if err != nil {
		logger.Errorf("Could not create LV \"%s\": %s", lvmPoolVolumeName, output)
		return fmt.Errorf("Could not create thin LV named %s", lvmPoolVolumeName)
	}

	fsPath := getLvmDevPath(project, vgName, volumeType, lvName)

	output, err = makeFSType(fsPath, lvFsType, nil)
	if err != nil {
		logger.Errorf("Filesystem creation failed: %s", output)
		return fmt.Errorf("Error making filesystem on image LV: %v", err)
	}

	return nil
}

func lvmCreateThinpool(s *state.State, sTypeVersion string, vgName string, thinPoolName string, lvFsType string) error {
	exists, err := storageLVMThinpoolExists(vgName, thinPoolName)
	if err != nil {
		return err
	}

	if exists {
		return nil
	}

	err = createDefaultThinPool(sTypeVersion, vgName, thinPoolName, lvFsType)
	if err != nil {
		return err
	}

	err = storageLVMValidateThinPoolName(s, vgName, thinPoolName)
	if err != nil {
		logger.Errorf("Setting thin pool name: %s", err)
		return fmt.Errorf("Error setting LVM thin pool config: %v", err)
	}

	return nil
}

func createDefaultThinPool(sTypeVersion string, vgName string, thinPoolName string, lvFsType string) error {
	isRecent, err := lvmVersionIsAtLeast(sTypeVersion, "2.02.99")
	if err != nil {
		return fmt.Errorf("Error checking LVM version: %s", err)
	}

	// Create the thin pool
	lvmThinPool := fmt.Sprintf("%s/%s", vgName, thinPoolName)
	var output string
	if isRecent {
		output, err = shared.TryRunCommand(
			"lvcreate",
			"--poolmetadatasize", "1G",
			"-l", "100%FREE",
			"--thinpool", lvmThinPool)
	} else {
		output, err = shared.TryRunCommand(
			"lvcreate",
			"--poolmetadatasize", "1G",
			"-L", "1G",
			"--thinpool", lvmThinPool)
	}

	if err != nil {
		logger.Errorf("Could not create thin pool \"%s\": %s", thinPoolName, string(output))
		return fmt.Errorf("Could not create LVM thin pool named %s", thinPoolName)
	}

	if !isRecent {
		// Grow it to the maximum VG size (two step process required by old LVM)
		output, err = shared.TryRunCommand("lvextend", "--alloc", "anywhere", "-l", "100%FREE", lvmThinPool)

		if err != nil {
			logger.Errorf("Could not grow thin pool: \"%s\": %s", thinPoolName, string(output))
			return fmt.Errorf("Could not grow LVM thin pool named %s", thinPoolName)
		}
	}

	return nil
}

func lvmVersionIsAtLeast(sTypeVersion string, versionString string) (bool, error) {
	lvmVersionString := strings.Split(sTypeVersion, "/")[0]

	lvmVersion, err := version.Parse(lvmVersionString)
	if err != nil {
		return false, err
	}

	inVersion, err := version.Parse(versionString)
	if err != nil {
		return false, err
	}

	if lvmVersion.Compare(inVersion) < 0 {
		return false, nil
	}

	return true, nil
}
