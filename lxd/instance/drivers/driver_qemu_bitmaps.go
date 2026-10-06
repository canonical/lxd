package drivers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v2"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/warningtype"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/device/filters"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/drivers/qmp"
	"github.com/canonical/lxd/lxd/project"
	storagePools "github.com/canonical/lxd/lxd/storage"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/warnings"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/features"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/validate"
)

// qemuOverlayNodePrefix used as part of the name given the QEMU block nodes of overlays.
// Device names may contain underscores.
// A suffix on qemuDeviceNamePrefix would therefore collide with a device named <name>_overlay.
// No block node of a device starts with this prefix.
const qemuOverlayNodePrefix = "lxdoverlay_"

// qemuMetadataDiskNodePrefix used as part of the name given the metadata disk node of a disk, the
// QEMU block node over its volume metadata image, and to the fd set that passes the image to QEMU.
const qemuMetadataDiskNodePrefix = "lxdimage_"

// qemuBitmapGranularity is the size in bytes of the block that one bit of a bitmap LXD creates covers.
const qemuBitmapGranularity = 65536

// blockNodeName returns the QEMU block node name of a disk device, which the guest device is attached to.
func blockNodeName(deviceName string) string {
	return qemuDeviceNameOrID(qemuDeviceNamePrefix, deviceName, "", qemuDeviceNameMaxLength)
}

// overlayNodeName returns the QEMU block node name of the overlay of a disk device.
func overlayNodeName(deviceName string) string {
	return qemuDeviceNameOrID(qemuOverlayNodePrefix, deviceName, "", qemuDeviceNameMaxLength)
}

// metadataDiskNodeName returns the QEMU block node name of the metadata disk node of a disk
// device, which is also the name of the fd set that passes its volume metadata image to QEMU.
func metadataDiskNodeName(deviceName string) string {
	return qemuDeviceNameOrID(qemuMetadataDiskNodePrefix, deviceName, "", qemuDeviceNameMaxLength)
}

// qemuBitmapsDir is the directory on the config volume that contains the volume metadata images,
// the overlays and the snapshot bitmap files of the instance.
const qemuBitmapsDir = "bitmaps"

// qemuMetadataImageSuffix is the file name suffix of the metadata images.
const qemuMetadataImageSuffix = ".qcow2"

// qemuOverlaySuffix is the file name suffix of the overlay of a volume, which is stored next to its metadata image.
const qemuOverlaySuffix = ".overlay.qcow2"

// openBitmapsDir opens the bitmaps directory as a root beneath the instance path, which rejects a
// symlink on the config volume that resolves outside the directory. The caller closes the root.
func (d *qemu) openBitmapsDir() (*os.Root, error) {
	return d.openSubPath(qemuBitmapsDir)
}

// withBitmapsDir runs task with the bitmaps directory opened as a root.
// The config volume must be mounted.
// When the directory does not exist, it returns nil without running task, as there is no file for task to act on.
func (d *qemu) withBitmapsDir(task func(root *os.Root) error) error {
	root, err := d.openBitmapsDir()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return err
	}

	defer func() { _ = root.Close() }()

	return task(root)
}

// removeMetadataImagesFiles deletes the files of the given names from the bitmaps directory.
// A file that does not exist is skipped.
func (d *qemu) removeMetadataImagesFiles(names ...string) error {
	return d.withBitmapsDir(func(root *os.Root) error {
		for _, name := range names {
			err := root.Remove(name)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}

		return nil
	})
}

// rootEntryExists reports whether root has an entry of the given name, without following a symlink.
func rootEntryExists(root *os.Root, name string) (bool, error) {
	_, err := root.Lstat(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// volumeMetadataImageName returns the name of the volume metadata image of a volume in the bitmaps
// directory, which stores the bitmaps of the volume as they were when the metadata disk node over
// it last closed. Validating the UUID keeps a caller from placing the file outside the directory.
func volumeMetadataImageName(volumeUUID string) (string, error) {
	err := validate.IsUUID(volumeUUID)
	if err != nil {
		return "", fmt.Errorf("Invalid volume UUID %q: %w", volumeUUID, err)
	}

	return volumeUUID + qemuMetadataImageSuffix, nil
}

// overlayFileName returns the name of the overlay in the bitmaps directory that the guest's writes
// to a volume go to while a snapshot with a bitmap is created.
// It stays on the config volume until it is committed into the volume.
// Validating the UUID keeps a caller from placing the file outside the directory.
func overlayFileName(volumeUUID string) (string, error) {
	err := validate.IsUUID(volumeUUID)
	if err != nil {
		return "", fmt.Errorf("Invalid volume UUID %q: %w", volumeUUID, err)
	}

	return volumeUUID + qemuOverlaySuffix, nil
}

// qemuSnapshotBitmapFilePrefix and qemuSnapshotBitmapFileSuffix frame the name of a snapshot
// bitmap file, snapshot.<uuid>.yaml, in the bitmaps directory.
const qemuSnapshotBitmapFilePrefix = "snapshot."
const qemuSnapshotBitmapFileSuffix = ".yaml"

// snapshotBitmapFile lists the bitmaps that the volume metadata images of a config volume snapshot store.
// A snapshot with a bitmap writes it next to the images once they are written, which includes it
// in the config volume snapshot. The snapshot removes it from the config volume afterwards.
// A snapshot without one was not created with a bitmap.
type snapshotBitmapFile struct {
	// Snapshot is the instance snapshot the file was written for.
	Snapshot snapshotBitmapFileSnapshot `yaml:"snapshot"`

	// Volumes has an entry per disk device whose volume the snapshot created its bitmap on.
	Volumes map[string]snapshotBitmapFileVolume `yaml:"volumes"`
}

// snapshotBitmapFileSnapshot identifies an instance snapshot by its instance snapshot UUID, the
// UUID of its root volume snapshot.
type snapshotBitmapFileSnapshot struct {
	UUID string `yaml:"uuid"`
}

// snapshotBitmapFileVolume describes the volume metadata image of one disk device of the snapshot.
// UUID is the UUID of the volume, which the image is named after.
// Bitmaps are the bitmaps that the image stores, without the one created with the snapshot.
type snapshotBitmapFileVolume struct {
	UUID    string                     `yaml:"uuid"`
	Bitmaps []snapshotBitmapFileBitmap `yaml:"bitmaps"`
}

// snapshotBitmapFileBitmap is a bitmap of a volume metadata image with the instance snapshot UUID
// of the snapshot it was created with and its granularity in bytes.
type snapshotBitmapFileBitmap struct {
	Name        string `yaml:"name"`
	UUID        string `yaml:"uuid"`
	Granularity int64  `yaml:"granularity"`
}

// snapshotBitmapFileName returns the name of the snapshot bitmap file of the instance snapshot of
// the given UUID in the bitmaps directory.
// Validating the UUID keeps a caller from placing the file outside the directory.
func snapshotBitmapFileName(snapshotUUID string) (string, error) {
	err := validate.IsUUID(snapshotUUID)
	if err != nil {
		return "", fmt.Errorf("Invalid snapshot UUID %q: %w", snapshotUUID, err)
	}

	return qemuSnapshotBitmapFilePrefix + snapshotUUID + qemuSnapshotBitmapFileSuffix, nil
}

// writeSnapshotBitmapFile writes the snapshot bitmap file into the bitmaps directory, named after
// the UUID of the snapshot it records.
func (d *qemu) writeSnapshotBitmapFile(file *snapshotBitmapFile) error {
	fileName, err := snapshotBitmapFileName(file.Snapshot.UUID)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(file)
	if err != nil {
		return err
	}

	root, err := d.openBitmapsDir()
	if err != nil {
		return err
	}

	defer func() { _ = root.Close() }()

	return root.WriteFile(fileName, data, 0600)
}

// readSnapshotBitmapFile returns the snapshot bitmap file of the bitmaps directory that records
// the instance snapshot of the given UUID, or nil when there is none.
// A snapshot without a UUID has no file, as the file is only written under a valid UUID.
func (d *qemu) readSnapshotBitmapFile(snapshotUUID string) (*snapshotBitmapFile, error) {
	if snapshotUUID == "" {
		return nil, nil
	}

	fileName, err := snapshotBitmapFileName(snapshotUUID)
	if err != nil {
		return nil, err
	}

	var found *snapshotBitmapFile
	err = d.withBitmapsDir(func(root *os.Root) error {
		data, err := root.ReadFile(fileName)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}

			return err
		}

		var file snapshotBitmapFile
		err = yaml.Unmarshal(data, &file)
		if err != nil {
			return fmt.Errorf("Failed parsing snapshot bitmap file %q: %w", fileName, err)
		}

		found = &file
		return nil
	})
	if err != nil {
		return nil, err
	}

	return found, nil
}

// RemoveSnapshotBitmapFile deletes the snapshot bitmap file of the instance snapshot of the given
// UUID from the config volume.
// The config volume snapshot keeps its copy.
func (d *qemu) RemoveSnapshotBitmapFile(snapshotUUID string) error {
	fileName, err := snapshotBitmapFileName(snapshotUUID)
	if err != nil {
		return err
	}

	return d.withConfigVolume(func() error {
		return d.removeMetadataImagesFiles(fileName)
	})
}

// bitmapsEnabled reports whether this QEMU process stores the bitmaps of its block disks in their
// volume metadata images.
// A live migration target does not, because the images on its config volume are the ones of the
// source, which still has them open, and it removes them once the migration is complete.
func (d *qemu) bitmapsEnabled() bool {
	return features.IsEnabled(features.ChangedBlockTracking) && d.migrationReceiveStateful == nil
}

// runningMonitor returns the monitor of the QEMU process of the instance, and nil for a stopped instance.
// The stop hook reports the instance as running after the process has ended, when the monitor cannot be reached.
// The process is therefore waited for as long as the stop hook waits for it, and the instance is handled as stopped.
func (d *qemu) runningMonitor() (*qmp.Monitor, error) {
	if !d.IsRunning() {
		return nil, nil
	}

	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err == nil {
		return monitor, nil
	}

	if !d.pidWait(time.Minute * 5) {
		return nil, err
	}

	return nil, nil
}

// withInstanceMounted runs task with the volumes of the instance, or of the instance snapshot, mounted.
// The mount info gives the block device of the root volume, and is nil while the QEMU process of the instance runs.
// The volumes of a running process are mounted already, and mounting them again would leave an LVM
// logical volume active after the instance unmounts it.
// The process is checked rather than the state, because the stop hook reports the instance as
// running after the process has ended and before the devices unmount the volumes.
func (d *qemu) withInstanceMounted(task func(mountInfo *storagePools.MountInfo) error) error {
	if !d.IsSnapshot() {
		pid, _ := d.pid()
		if pid > 0 {
			return task(nil)
		}
	}

	pool, err := d.getStoragePool()
	if err != nil {
		return err
	}

	if d.IsSnapshot() {
		mountInfo, err := pool.MountInstanceSnapshot(d, nil)
		if err != nil {
			return err
		}

		defer func() {
			err := pool.UnmountInstanceSnapshot(d, nil)
			if err != nil && !errors.Is(err, storageDrivers.ErrInUse) {
				d.logger.Warn("Failed unmounting config volume of snapshot", logger.Ctx{"err": err})
			}
		}()

		return task(mountInfo)
	}

	mountInfo, err := pool.MountInstance(d, nil)
	if err != nil {
		return err
	}

	defer func() {
		err := pool.UnmountInstance(d, nil)
		if err != nil && !errors.Is(err, storageDrivers.ErrInUse) {
			d.logger.Warn("Failed unmounting config volume", logger.Ctx{"err": err})
		}
	}()

	return task(mountInfo)
}

// withConfigVolume runs task with the config volume of the instance, or of the instance snapshot, mounted.
func (d *qemu) withConfigVolume(task func() error) error {
	return d.withInstanceMounted(func(*storagePools.MountInfo) error { return task() })
}

// qcow2BlockDev opens the qcow2 image with the given name in root, passes it to the running QEMU
// process by file descriptor under the given fd set name and returns the options of a qcow2 block
// node over it.
// The caller adds the node and adds any further options first, and the returned function removes
// the fd set once the node is gone.
func (d *qemu) qcow2BlockDev(monitor *qmp.Monitor, nodeName string, fdSetName string, root *os.Root, name string, readOnly bool) (map[string]any, func(), error) {
	flags := unix.O_RDWR
	if readOnly {
		flags = unix.O_RDONLY
	}

	file, err := root.OpenFile(name, flags, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed opening image %q: %w", filepath.Join(root.Name(), name), err)
	}

	// Closed once QEMU has its own descriptor, as an open descriptor would prevent a clean unmount on stop.
	defer func() { _ = file.Close() }()

	info, err := monitor.SendFileWithFDSet(fdSetName, file, readOnly)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed sending file descriptor of %q for block node %q: %w", file.Name(), nodeName, err)
	}

	// A writable node is reopened read-only once it becomes the backing node of an overlay, which
	// needs a read-only descriptor in the fd set.
	// The reopen fails without it after the persistent bitmaps of the node were already marked
	// read-only, and QEMU then rejects every write to the node.
	if !readOnly {
		roFile, err := root.OpenFile(name, unix.O_RDONLY, 0)
		if err != nil {
			_ = monitor.RemoveFDFromFDSet(fdSetName)
			return nil, nil, fmt.Errorf("Failed opening image %q: %w", file.Name(), err)
		}

		defer func() { _ = roFile.Close() }()

		err = monitor.AddFileToFDSet(info.ID, fdSetName, roFile, true)
		if err != nil {
			_ = monitor.RemoveFDFromFDSet(fdSetName)
			return nil, nil, fmt.Errorf("Failed sending read-only file descriptor of %q for block node %q: %w", file.Name(), nodeName, err)
		}
	}

	blockDev := map[string]any{
		"driver":    "qcow2",
		"node-name": nodeName,
		"read-only": readOnly,
		"file": map[string]any{
			"driver":   "file",
			"filename": fmt.Sprintf("/dev/fdset/%d", info.ID),
			"locking":  "off",
		},
	}

	return blockDev, func() { _ = monitor.RemoveFDFromFDSet(fdSetName) }, nil
}

// addQcow2Node adds the qcow2 image with the given name in root to the running QEMU process as a
// block node of the given name and with the given further options, without a guest visible device.
// The returned function removes the node and its fd set.
// Removing a writable node writes its persistent bitmaps into the image.
func (d *qemu) addQcow2Node(monitor *qmp.Monitor, nodeName string, root *os.Root, name string, readOnly bool, options map[string]any) (func(), error) {
	blockDev, removeFDSet, err := d.qcow2BlockDev(monitor, nodeName, nodeName, root, name, readOnly)
	if err != nil {
		return nil, err
	}

	maps.Copy(blockDev, options)

	err = monitor.AddBlockDevice(blockDev, nil)
	if err != nil {
		removeFDSet()
		return nil, fmt.Errorf("Failed adding block node %q: %w", nodeName, err)
	}

	return func() { d.removeQcow2Node(monitor, nodeName) }, nil
}

// removeQcow2Node removes a block node added by addQcow2Node together with its fd set.
func (d *qemu) removeQcow2Node(monitor *qmp.Monitor, nodeName string) {
	err := monitor.RemoveBlockDevice(nodeName)
	if err != nil {
		d.logger.Warn("Failed removing block node", logger.Ctx{"node": nodeName, "err": err})
	}

	err = monitor.RemoveFDFromFDSet(nodeName)
	if err != nil {
		d.logger.Warn("Failed removing file descriptor set", logger.Ctx{"node": nodeName, "err": err})
	}
}

// createQcow2Node creates an empty qcow2 image of the given virtual size with the given name in
// root and adds it to the running QEMU process as a writable block node of the given name and with
// the given further options. The returned function removes the node and leaves the file in place.
func (d *qemu) createQcow2Node(monitor *qmp.Monitor, nodeName string, root *os.Root, name string, size int64, options map[string]any) (func(), error) {
	err := storagePools.Qcow2Create(root, name, size)
	if err != nil {
		return nil, err
	}

	removeNode, err := d.addQcow2Node(monitor, nodeName, root, name, false, options)
	if err != nil {
		_ = root.Remove(name)
		return nil, err
	}

	return removeNode, nil
}

// addOverlay adds an empty qcow2 overlay block node of the given virtual size to the running QEMU
// process, for blockdev-snapshot to use as the overlay of a disk.
// The overlay file is created on the instance config volume, where the root disk's size.state
// property limits its growth.
// It is unlinked once QEMU has opened it, which keeps a migration from transferring it and deletes it when QEMU exits.
// The returned function removes the overlay block node and its file descriptor set.
func (d *qemu) addOverlay(monitor *qmp.Monitor, overlayNode string, size int64) (func(), error) {
	root, err := d.OpenRoot()
	if err != nil {
		return nil, err
	}

	defer func() { _ = root.Close() }()

	overlayFile := overlayNode + ".qcow2"
	removeOverlay, err := d.createQcow2Node(monitor, overlayNode, root, overlayFile, size, map[string]any{"backing": nil})
	if err != nil {
		return nil, err
	}

	err = root.Remove(overlayFile)
	if err != nil {
		removeOverlay()
		return nil, err
	}

	return removeOverlay, nil
}

// bitmapDisk describes a disk device whose volume supports bitmaps, which is the root disk or a
// custom block volume that is not shared, as the bitmaps of the instance's QEMU process record
// every write to such a volume.
type bitmapDisk struct {
	deviceName string
	volume     api.InstanceBitmapVolume
}

// nodeName returns the disk node of the disk, the block node of its volume that the guest device is attached to.
func (disk bitmapDisk) nodeName() string {
	return blockNodeName(disk.deviceName)
}

// metadataDiskNodeName returns the metadata disk node of the disk, the qcow2 node over its volume metadata image.
func (disk bitmapDisk) metadataDiskNodeName() string {
	return metadataDiskNodeName(disk.deviceName)
}

// volumeProject returns the project the volume of the disk is stored in.
func (disk bitmapDisk) volumeProject(instProject *api.Project) string {
	if disk.volume.Type == dbCluster.StoragePoolVolumeTypeNameCustom {
		return project.StorageVolumeProjectFromRecord(instProject, dbCluster.StoragePoolVolumeTypeCustom)
	}

	return instProject.Name
}

// diskVolume returns the volume attached through a disk device, or nil for a disk device whose
// volume does not support bitmaps. pools caches the storage pools by name across calls.
func (d *qemu) diskVolume(deviceName string, devConf map[string]string, isRootDisk bool, pools map[string]storagePools.Pool) (*api.InstanceBitmapVolume, error) {
	// Only the root disk and a custom volume attached without a path are block volumes.
	if !isRootDisk && !filters.IsCustomVolumeBlockDisk(devConf) {
		return nil, nil
	}

	// A disk device can attach the root volume of another virtual machine or a snapshot, and
	// neither is written by this instance alone. A read-only disk is never written.
	if !isRootDisk && (devConf["source.type"] != "" && devConf["source.type"] != dbCluster.StoragePoolVolumeTypeNameCustom) {
		return nil, nil
	}

	if devConf["source.snapshot"] != "" || shared.IsTrue(devConf["readonly"]) {
		return nil, nil
	}

	poolName := devConf["pool"]
	pool, ok := pools[poolName]
	if !ok {
		var err error
		pool, err = storagePools.LoadByName(d.state, poolName)
		if err != nil {
			return nil, fmt.Errorf("Failed loading storage pool %q: %w", poolName, err)
		}

		pools[poolName] = pool
	}

	volType := storageDrivers.VolumeTypeCustom
	volName := devConf["source"]
	volProject := project.StorageVolumeProjectFromRecord(&d.project, dbCluster.StoragePoolVolumeTypeCustom)
	if isRootDisk {
		volType = storageDrivers.VolumeTypeVM
		volName = d.name
		volProject = d.project.Name
	}

	dbVol, err := storagePools.VolumeDBGet(pool, volProject, volName, volType)
	if err != nil {
		return nil, fmt.Errorf("Failed loading volume %q of disk %q: %w", volName, deviceName, err)
	}

	if dbVol.ContentType != dbCluster.StoragePoolVolumeContentTypeNameBlock {
		return nil, nil
	}

	// Several instances can write to a shared volume.
	// A bitmap of one QEMU process therefore does not record every write to it.
	if shared.IsTrue(dbVol.Config["security.shared"]) {
		return nil, nil
	}

	return &api.InstanceBitmapVolume{
		Pool:   pool.Name(),
		Type:   dbVol.Type,
		Name:   dbVol.Name,
		UUID:   dbVol.Config["volatile.uuid"],
		Device: deviceName,
	}, nil
}

// bitmapDisk returns the disk device of the given name with its volume, or nil when the device is
// not a disk whose volume supports bitmaps.
func (d *qemu) bitmapDisk(deviceName string) (*bitmapDisk, error) {
	devConf, ok := d.ExpandedDevices()[deviceName]
	if !ok || !filters.IsDisk(devConf) {
		return nil, nil
	}

	return d.bitmapDiskFromConfig(deviceName, devConf)
}

// bitmapDiskFromConfig returns the disk device of the given name and config with its volume, or
// nil when its volume does not support bitmaps.
// The config is not required to be in the current devices of the instance, which no longer list a
// device that is being removed or renamed.
func (d *qemu) bitmapDiskFromConfig(deviceName string, devConf map[string]string) (*bitmapDisk, error) {
	rootDiskName, _, err := d.getRootDiskDevice()
	if err != nil {
		return nil, fmt.Errorf("Failed getting root disk: %w", err)
	}

	volume, err := d.diskVolume(deviceName, devConf, deviceName == rootDiskName, make(map[string]storagePools.Pool))
	if err != nil {
		return nil, err
	}

	if volume == nil {
		return nil, nil
	}

	return &bitmapDisk{deviceName: deviceName, volume: *volume}, nil
}

// disksSupportingBitmaps returns the disk devices whose volumes support bitmaps, sorted by device name.
func (d *qemu) disksSupportingBitmaps() ([]bitmapDisk, error) {
	rootDiskName, _, err := d.getRootDiskDevice()
	if err != nil {
		return nil, fmt.Errorf("Failed getting root disk: %w", err)
	}

	pools := make(map[string]storagePools.Pool)
	disks := []bitmapDisk{}
	for deviceName, devConf := range d.ExpandedDevices() {
		if !filters.IsDisk(devConf) {
			continue
		}

		volume, err := d.diskVolume(deviceName, devConf, deviceName == rootDiskName, pools)
		if err != nil {
			return nil, err
		}

		if volume == nil {
			continue
		}

		disks = append(disks, bitmapDisk{deviceName: deviceName, volume: *volume})
	}

	slices.SortFunc(disks, func(a bitmapDisk, b bitmapDisk) int { return strings.Compare(a.deviceName, b.deviceName) })

	return disks, nil
}

// selectDisks returns the disks of the given devices among disks, in the order of disks.
func selectDisks(disks []bitmapDisk, deviceNames []string) []bitmapDisk {
	selected := make([]bitmapDisk, 0, len(deviceNames))
	for _, disk := range disks {
		if slices.Contains(deviceNames, disk.deviceName) {
			selected = append(selected, disk)
		}
	}

	return selected
}

// prepareVolumeMetadataImage returns the name of the volume metadata image of the disk in root,
// the bitmaps directory, and the size of the disk node, which the image must have.
// An image is deleted when its size cannot be read or differs from the volume, which a resize of the volume causes.
// A missing image is created when create is set, and reported with an error that wraps fs.ErrNotExist otherwise.
func (d *qemu) prepareVolumeMetadataImage(monitor *qmp.Monitor, root *os.Root, disk bitmapDisk, create bool) (string, int64, error) {
	size, err := monitor.BlockNodeSize(disk.nodeName())
	if err != nil {
		return "", 0, fmt.Errorf("Failed getting size of disk %q: %w", disk.deviceName, err)
	}

	imageName, err := volumeMetadataImageName(disk.volume.UUID)
	if err != nil {
		return "", 0, err
	}

	exists, err := rootEntryExists(root, imageName)
	if err != nil {
		return "", 0, err
	}

	if exists {
		imageSize, err := storagePools.Qcow2VirtualSize(root, imageName)
		if err == nil && imageSize == size {
			return imageName, size, nil
		}

		d.logger.Warn("Deleting metadata image that does not match the volume", logger.Ctx{"device": disk.deviceName, "imageSize": imageSize, "volumeSize": size, "err": err})
		err = root.Remove(imageName)
		if err != nil {
			return "", 0, fmt.Errorf("Failed removing volume metadata image of disk %q: %w", disk.deviceName, err)
		}
	}

	if !create {
		return "", 0, fmt.Errorf("The volume metadata image of disk %q does not exist: %w", disk.deviceName, fs.ErrNotExist)
	}

	err = storagePools.Qcow2CreateMetadataImage(root, imageName, size)
	if err != nil {
		return "", 0, err
	}

	return imageName, size, nil
}

// nullBlockDev returns the options of a read-only null block device of the given size, used in
// place of the volume as the data file of a metadata image node.
// Nothing reads or writes guest data through such a node.
func nullBlockDev(size int64) map[string]any {
	return map[string]any{"driver": "null-co", "size": size, "read-only": true}
}

// addMetadataDiskNode adds the metadata disk node of the disk to the running QEMU process.
// It is a writable qcow2 node over the volume metadata image, with a null block device as its data
// file and no guest device. The disk node must exist.
// With create set, the bitmaps directory and the image are created when missing.
// Without it, a missing image gives an error that wraps fs.ErrNotExist.
// The shutdown action is set to pause first, which makes a guest shutdown or reboot pause QEMU.
// This lets every process that has a metadata disk node persist its bitmaps before it ends.
// Removing the node writes its bitmaps into the image.
func (d *qemu) addMetadataDiskNode(monitor *qmp.Monitor, disk bitmapDisk, create bool) (func(), error) {
	var root *os.Root
	var err error
	if create {
		root, err = d.openOrCreateSubPath(qemuBitmapsDir, 0700)
	} else {
		root, err = d.openBitmapsDir()
	}

	if err != nil {
		return nil, err
	}

	defer func() { _ = root.Close() }()

	imageName, size, err := d.prepareVolumeMetadataImage(monitor, root, disk, create)
	if err != nil {
		return nil, err
	}

	err = monitor.SetAction(map[string]string{"shutdown": "pause"})
	if err != nil {
		return nil, err
	}

	return d.addQcow2Node(monitor, disk.metadataDiskNodeName(), root, imageName, false, map[string]any{"data-file": nullBlockDev(size)})
}

// bitmapPair is a bitmap of the volume of a disk in the running QEMU process.
// The disk bitmap is the transient bitmap of the disk node, which records the writes of this run.
// The store bitmap is the persistent, disabled bitmap of the same name on the metadata disk node,
// which contains the writes of the earlier runs and which the disk bitmap is merged into before
// the process ends. A half is nil when its node lacks the bitmap.
type bitmapPair struct {
	name  string
	disk  *qmp.BlockDirtyInfo
	store *qmp.BlockDirtyInfo
}

// valid reports whether the bitmap recorded every write since it was created: both halves exist,
// the disk bitmap is recording and neither half is inconsistent.
func (pair bitmapPair) valid() bool {
	return pair.disk != nil && pair.store != nil && pair.disk.Recording && !pair.disk.Inconsistent && !pair.store.Inconsistent
}

// queryBitmapPairs returns the bitmaps of the disk node and of the metadata disk node of the disk
// as pairs, sorted by name. Both nodes must exist.
func (d *qemu) queryBitmapPairs(monitor *qmp.Monitor, disk bitmapDisk) ([]bitmapPair, error) {
	diskBitmaps, err := monitor.QueryNodeDirtyBitmaps(disk.nodeName())
	if err != nil {
		return nil, fmt.Errorf("Failed querying bitmaps of disk %q: %w", disk.deviceName, err)
	}

	storeBitmaps, err := monitor.QueryNodeDirtyBitmaps(disk.metadataDiskNodeName())
	if err != nil {
		return nil, fmt.Errorf("Failed querying bitmaps of the volume metadata image of disk %q: %w", disk.deviceName, err)
	}

	byName := make(map[string]*bitmapPair, len(storeBitmaps))
	for i := range diskBitmaps {
		byName[diskBitmaps[i].Name] = &bitmapPair{name: diskBitmaps[i].Name, disk: &diskBitmaps[i]}
	}

	for i := range storeBitmaps {
		pair, found := byName[storeBitmaps[i].Name]
		if !found {
			pair = &bitmapPair{name: storeBitmaps[i].Name}
			byName[storeBitmaps[i].Name] = pair
		}

		pair.store = &storeBitmaps[i]
	}

	pairs := make([]bitmapPair, 0, len(byName))
	for _, pair := range byName {
		pairs = append(pairs, *pair)
	}

	slices.SortFunc(pairs, func(a bitmapPair, b bitmapPair) int { return strings.Compare(a.name, b.name) })

	return pairs, nil
}

// removeBitmapPair removes the bitmap from the nodes of the disk that have it.
func (d *qemu) removeBitmapPair(monitor *qmp.Monitor, disk bitmapDisk, pair bitmapPair) error {
	if pair.disk != nil {
		err := monitor.RemoveDirtyBitmap(disk.nodeName(), pair.name)
		if err != nil {
			return fmt.Errorf("Failed removing bitmap %q of disk %q: %w", pair.name, disk.deviceName, err)
		}
	}

	if pair.store != nil {
		err := monitor.RemoveDirtyBitmap(disk.metadataDiskNodeName(), pair.name)
		if err != nil {
			return fmt.Errorf("Failed removing bitmap %q of the volume metadata image of disk %q: %w", pair.name, disk.deviceName, err)
		}
	}

	return nil
}

// restoreBitmaps creates on the disk node the disk bitmap of every bitmap that loaded on the
// metadata disk node of the disk, before the guest runs.
// A bitmap that loaded inconsistent, because the process that loaded it did not write it back,
// missed writes and gets no disk bitmap.
// It stays on the metadata disk node until the next snapshot with a bitmap removes it.
// A process that waits for the state of the guest loads the bitmaps only after the state is restored.
// Its bitmaps are therefore read from the volume metadata image, where a bitmap that is in use
// loads inconsistent.
func (d *qemu) restoreBitmaps(monitor *qmp.Monitor, disk bitmapDisk) error {
	status, err := monitor.Status()
	if err != nil {
		return err
	}

	var storeBitmaps []qmp.BlockDirtyInfo
	if status == "inmigrate" {
		imageName, err := volumeMetadataImageName(disk.volume.UUID)
		if err != nil {
			return err
		}

		err = d.withBitmapsDir(func(root *os.Root) error {
			imageBitmaps, err := storagePools.Qcow2Bitmaps(root, imageName)
			if err != nil {
				return err
			}

			for _, bitmap := range imageBitmaps {
				storeBitmaps = append(storeBitmaps, qmp.BlockDirtyInfo{Name: bitmap.Name, Granularity: int(bitmap.Granularity), Inconsistent: !bitmap.Valid})
			}

			return nil
		})
		if err != nil {
			return fmt.Errorf("Failed reading bitmaps of the volume metadata image of disk %q: %w", disk.deviceName, err)
		}
	} else {
		storeBitmaps, err = monitor.QueryNodeDirtyBitmaps(disk.metadataDiskNodeName())
		if err != nil {
			return fmt.Errorf("Failed querying bitmaps of the volume metadata image of disk %q: %w", disk.deviceName, err)
		}
	}

	actions := make([]qmp.TransactionAction, 0, len(storeBitmaps))
	for _, bitmap := range storeBitmaps {
		if bitmap.Inconsistent {
			d.logger.Warn("Bitmap missed writes and is not restored", logger.Ctx{"device": disk.deviceName, "bitmap": bitmap.Name})
			continue
		}

		actions = append(actions, qmp.BlockDirtyBitmapAddAction(disk.nodeName(), bitmap.Name, bitmap.Granularity, false, false))
	}

	if len(actions) == 0 {
		return nil
	}

	err = monitor.RunTransaction(actions)
	if err != nil {
		return fmt.Errorf("Failed restoring bitmaps of disk %q: %w", disk.deviceName, err)
	}

	return nil
}

// persistBitmaps merges the disk bitmap of every pair of the given disks into its store bitmap.
// QEMU writes the store bitmaps into the volume metadata image when the metadata disk node closes,
// and the guest must not write in between, as such a write is not recorded.
// A store bitmap whose merge fails is removed.
// All store bitmaps of a disk with an overlay node are removed as well, as its disk bitmaps lack
// the writes in the overlay. This keeps the image from storing a bitmap that lacks writes.
// A disk without both nodes is skipped.
func (d *qemu) persistBitmaps(monitor *qmp.Monitor, disks []bitmapDisk) error {
	nodeNames, err := monitor.QueryNamedBlockNodes()
	if err != nil {
		return err
	}

	var errs []error
	for _, disk := range disks {
		if !slices.Contains(nodeNames, disk.nodeName()) || !slices.Contains(nodeNames, disk.metadataDiskNodeName()) {
			continue
		}

		pairs, err := d.queryBitmapPairs(monitor, disk)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		hasOverlay := slices.Contains(nodeNames, overlayNodeName(disk.deviceName))
		if hasOverlay {
			d.logger.Warn("Removing bitmaps of a disk with an uncommitted overlay", logger.Ctx{"device": disk.deviceName})
		}

		for _, pair := range pairs {
			if pair.store == nil || (pair.disk == nil && !hasOverlay) {
				continue
			}

			if !hasOverlay {
				err = monitor.BlockDirtyBitmapMerge(disk.metadataDiskNodeName(), pair.name, []qmp.BlockDirtyBitmapSource{{Node: disk.nodeName(), Name: pair.name}})
				if err == nil {
					continue
				}

				d.logger.Error("Failed persisting bitmap, removing it", logger.Ctx{"device": disk.deviceName, "bitmap": pair.name, "err": err})
			}

			err = monitor.RemoveDirtyBitmap(disk.metadataDiskNodeName(), pair.name)
			if err != nil {
				errs = append(errs, fmt.Errorf("Failed removing bitmap %q of the volume metadata image of disk %q: %w", pair.name, disk.deviceName, err))
			}
		}
	}

	return errors.Join(errs...)
}

// pauseAndPersist pauses the guest, adds back the missing metadata disk nodes and persists the
// bitmaps of every disk.
// An overlay is not committed here.
// Its file stays on the config volume for the next start or offline commit.
func (d *qemu) pauseAndPersist(monitor *qmp.Monitor) error {
	err := monitor.Pause()
	if err != nil {
		return fmt.Errorf("Failed pausing instance: %w", err)
	}

	disks, err := d.disksSupportingBitmaps()
	if err != nil {
		return err
	}

	err = d.addMissingMetadataDiskNodes(monitor, disks)
	if err != nil {
		d.logger.Error("Failed adding volume metadata images back", logger.Ctx{"err": err})
	}

	err = d.persistBitmaps(monitor, disks)
	if err != nil {
		return fmt.Errorf("Failed persisting bitmaps: %w", err)
	}

	return nil
}

// persistAndQuit persists the bitmaps of every disk of the paused guest and asks QEMU to quit,
// which closes the metadata disk nodes and writes the images.
// When pauseAndPersist fails, the process is left for the caller to kill, and the image keeps its
// bitmaps marked in use instead of storing bitmaps that lack writes.
func (d *qemu) persistAndQuit(monitor *qmp.Monitor) error {
	err := d.pauseAndPersist(monitor)
	if err != nil {
		return err
	}

	return monitor.Quit()
}

// pruneMetadataImages deletes from the bitmaps directory every file that is not the volume
// metadata image or the overlay of a volume attached through one of the given disks.
// A snapshot bitmap file left on the config volume by a failed snapshot and the volume metadata
// image of a detached volume are removed this way.
func (d *qemu) pruneMetadataImages(disks []bitmapDisk) error {
	return d.withBitmapsDir(func(root *os.Root) error {
		entries, err := fs.ReadDir(root.FS(), ".")
		if err != nil {
			return err
		}

		keep := make([]string, 0, len(disks)*2)
		for _, disk := range disks {
			imageName, err := volumeMetadataImageName(disk.volume.UUID)
			if err != nil {
				return err
			}

			overlayName, err := overlayFileName(disk.volume.UUID)
			if err != nil {
				return err
			}

			keep = append(keep, imageName, overlayName)
		}

		for _, entry := range entries {
			if slices.Contains(keep, entry.Name()) {
				continue
			}

			d.logger.Debug("Removing metadata image", logger.Ctx{"file": entry.Name()})
			err := root.RemoveAll(entry.Name())
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// commitOverlay commits the overlay of a disk into its volume, removes the overlay node and deletes the overlay file.
// The bitmaps of the volume record the committed writes.
// The commit is retried, because the guest writes to the overlay until it succeeds.
func (d *qemu) commitOverlay(monitor *qmp.Monitor, disk bitmapDisk) error {
	overlayNode := overlayNodeName(disk.deviceName)

	var err error
	for attempt := range 3 {
		if attempt > 0 {
			time.Sleep(time.Second)
		}

		err = monitor.BlockCommit(overlayNode)
		if err == nil {
			// BlockCommit returns once the job is completed, before the job concludes and moves
			// the disk device back to the disk node.
			err = monitor.BlockJobWaitGone(overlayNode)
		}

		if err == nil {
			break
		}

		d.logger.Warn("Failed committing overlay", logger.Ctx{"device": disk.deviceName, "attempt": attempt + 1, "err": err})
	}

	if err != nil {
		d.raiseOverlayWarning(disk)
		return fmt.Errorf("Failed committing overlay of disk %q: %w", disk.deviceName, err)
	}

	// The overlay node stays in use while the guest writes to it.
	// A failed removal therefore means that the commit did not take effect, and the overlay file is kept.
	err = monitor.RemoveBlockDevice(overlayNode)
	if err != nil {
		d.raiseOverlayWarning(disk)
		return fmt.Errorf("Failed removing overlay node of disk %q: %w", disk.deviceName, err)
	}

	err = monitor.RemoveFDFromFDSet(overlayNode)
	if err != nil {
		d.logger.Warn("Failed removing file descriptor set", logger.Ctx{"node": overlayNode, "err": err})
	}

	overlayName, err := overlayFileName(disk.volume.UUID)
	if err != nil {
		return err
	}

	err = d.removeMetadataImagesFiles(overlayName)
	if err != nil {
		return fmt.Errorf("Failed removing overlay of disk %q: %w", disk.deviceName, err)
	}

	d.resolveOverlayWarning()

	return nil
}

// raiseOverlayWarning raises the warning of a failed commit.
// Until a commit succeeds the volume lacks the writes of the guest.
// This means every storage snapshot, copy and backup of it is inconsistent.
func (d *qemu) raiseOverlayWarning(disk bitmapDisk) {
	_ = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpsertWarning(ctx, d.node, d.project.Name, entity.TypeInstance, d.ID(), warningtype.InstanceDiskOverlayNotCommitted, fmt.Sprintf("The volume of disk %q lacks the writes of the guest since the last snapshot with a bitmap", disk.deviceName))
	})
}

// resolveOverlayWarning resolves the warning of a failed commit once no disk of the instance has an overlay file.
// A successful commit deletes the overlay file.
func (d *qemu) resolveOverlayWarning() {
	disks, err := d.disksSupportingBitmaps()
	if err != nil {
		return
	}

	// A disk whose volume UUID is invalid cannot have an overlay file, as overlayFileName rejects
	// it before one is created.
	hasOverlay := false
	err = d.withBitmapsDir(func(root *os.Root) error {
		for _, disk := range disks {
			overlayName, err := overlayFileName(disk.volume.UUID)
			if err != nil {
				continue
			}

			hasOverlay, err = rootEntryExists(root, overlayName)
			if err != nil || hasOverlay {
				return err
			}
		}

		return nil
	})
	if err != nil || hasOverlay {
		return
	}

	_ = warnings.ResolveWarningsByNodeAndProjectAndTypeAndEntity(d.state.DB.Cluster, d.node, d.project.Name, warningtype.InstanceDiskOverlayNotCommitted, entity.TypeInstance, d.ID())
}

// addMissingMetadataDiskNodes adds back the metadata disk node of every given disk that has an image but no node.
// CreateSnapshotBitmaps removes these nodes to write the images for the storage snapshot, while
// the disk bitmaps keep recording.
// With the node back, the next persist merges the writes of the whole run.
// The image of a disk whose node cannot be added back is removed, as its bitmaps lack the writes
// recorded since the node was removed.
func (d *qemu) addMissingMetadataDiskNodes(monitor *qmp.Monitor, disks []bitmapDisk) error {
	if !d.bitmapsEnabled() {
		return nil
	}

	nodeNames, err := monitor.QueryNamedBlockNodes()
	if err != nil {
		return err
	}

	var errs []error
	for _, disk := range disks {
		if !slices.Contains(nodeNames, disk.nodeName()) || slices.Contains(nodeNames, disk.metadataDiskNodeName()) {
			continue
		}

		_, err := d.addMetadataDiskNode(monitor, disk, false)
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			continue
		}

		d.logger.Error("Failed adding volume metadata image back, removing it", logger.Ctx{"device": disk.deviceName, "err": err})
		imageName, err := volumeMetadataImageName(disk.volume.UUID)
		if err == nil {
			err = d.removeMetadataImagesFiles(imageName)
		}

		if err != nil {
			errs = append(errs, fmt.Errorf("Failed removing volume metadata image of disk %q: %w", disk.deviceName, err))
		}
	}

	return errors.Join(errs...)
}

// commitOverlays adds the missing metadata disk nodes of the given disks back and commits the
// overlays of the disks that have one.
func (d *qemu) commitOverlays(monitor *qmp.Monitor, disks []bitmapDisk) error {
	var errs []error
	err := d.addMissingMetadataDiskNodes(monitor, disks)
	if err != nil {
		errs = append(errs, err)
	}

	nodeNames, err := monitor.QueryNamedBlockNodes()
	if err != nil {
		return errors.Join(append(errs, err)...)
	}

	for _, disk := range disks {
		if !slices.Contains(nodeNames, overlayNodeName(disk.deviceName)) {
			continue
		}

		err := d.commitOverlay(monitor, disk)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// disksWithOverlayFiles returns the disks whose volume has an overlay file on the config volume,
// which a commit that failed or did not run left there. The config volume must be mounted.
func (d *qemu) disksWithOverlayFiles() ([]bitmapDisk, error) {
	disks, err := d.disksSupportingBitmaps()
	if err != nil {
		return nil, err
	}

	withOverlay := make([]bitmapDisk, 0, len(disks))
	err = d.withBitmapsDir(func(root *os.Root) error {
		for _, disk := range disks {
			overlayName, err := overlayFileName(disk.volume.UUID)
			if err != nil {
				return err
			}

			exists, err := rootEntryExists(root, overlayName)
			if err != nil {
				return err
			}

			if exists {
				withOverlay = append(withOverlay, disk)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return withOverlay, nil
}

// diskDevicePath returns the path of the block device of the volume of a disk of the stopped
// instance, with the volume mounted until the returned function runs.
// rootDevicePath is the block device of the root volume, which the caller mounted.
func (d *qemu) diskDevicePath(disk bitmapDisk, rootDevicePath string) (string, func(), error) {
	if disk.volume.Type != dbCluster.StoragePoolVolumeTypeNameCustom {
		if rootDevicePath == "" {
			return "", nil, errors.New("Instance mount has no block device path")
		}

		return rootDevicePath, func() {}, nil
	}

	pool, err := storagePools.LoadByName(d.state, disk.volume.Pool)
	if err != nil {
		return "", nil, err
	}

	volProject := disk.volumeProject(&d.project)
	_, err = pool.MountCustomVolume(volProject, disk.volume.Name, nil)
	if err != nil {
		return "", nil, err
	}

	unmount := func() {
		_, err := pool.UnmountCustomVolume(volProject, disk.volume.Name, nil)
		if err != nil && !errors.Is(err, storageDrivers.ErrInUse) {
			d.logger.Warn("Failed unmounting volume", logger.Ctx{"device": disk.deviceName, "err": err})
		}
	}

	// Mounting a custom volume activates its block device without reporting its path.
	dbVol, err := storagePools.VolumeDBGet(pool, volProject, disk.volume.Name, storageDrivers.VolumeTypeCustom)
	if err != nil {
		unmount()
		return "", nil, err
	}

	vol := pool.GetVolume(storageDrivers.VolumeTypeCustom, storageDrivers.ContentTypeBlock, project.StorageVolume(volProject, disk.volume.Name), dbVol.Config)
	devicePath, err := pool.Driver().GetVolumeDiskPath(vol)
	if err != nil {
		unmount()
		return "", nil, err
	}

	return devicePath, unmount, nil
}

// commitOverlayFiles commits the overlay file of every given disk of the stopped instance into its
// volume and deletes the file. A disk without an overlay file is skipped.
func (d *qemu) commitOverlayFiles(disks []bitmapDisk) error {
	return d.withInstanceMounted(func(mountInfo *storagePools.MountInfo) error {
		rootDevicePath := ""
		if mountInfo != nil {
			devSource, ok := mountInfo.DevSource.(deviceConfig.DevSourcePath)
			if ok {
				rootDevicePath = devSource.Path
			}
		}

		return d.withBitmapsDir(func(root *os.Root) error {
			var errs []error
			for _, disk := range disks {
				overlayName, err := overlayFileName(disk.volume.UUID)
				if err != nil {
					errs = append(errs, err)
					continue
				}

				exists, err := rootEntryExists(root, overlayName)
				if err != nil {
					errs = append(errs, err)
					continue
				}

				if !exists {
					continue
				}

				err = d.commitOverlayFile(root, disk, overlayName, rootDevicePath)
				if err != nil {
					d.raiseOverlayWarning(disk)
					errs = append(errs, err)
				}
			}

			return errors.Join(errs...)
		})
	})
}

// commitOverlayFile commits the overlay file of the given name in root, the bitmaps directory, of
// a disk of the stopped instance into its volume and deletes it.
// The bitmaps of the volume lack the writes in the overlay.
// The volume metadata image of the disk is therefore deleted first, even if the commit then fails halfway.
// The next snapshot with a bitmap creates the image again.
func (d *qemu) commitOverlayFile(root *os.Root, disk bitmapDisk, overlayName string, rootDevicePath string) error {
	imageName, err := volumeMetadataImageName(disk.volume.UUID)
	if err != nil {
		return err
	}

	err = root.Remove(imageName)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("Failed removing volume metadata image of disk %q: %w", disk.deviceName, err)
	}

	devicePath, unmount, err := d.diskDevicePath(disk, rootDevicePath)
	if err != nil {
		return fmt.Errorf("Failed committing leftover snapshot disk overlay %q: Failed getting block device path of the volume: %w", disk.deviceName, err)
	}

	defer unmount()

	err = storagePools.Qcow2Commit(root, overlayName, devicePath)
	if err != nil {
		return fmt.Errorf("Failed committing leftover snapshot disk overlay %q: %w. Removing the overlay loses the recent disk writes and must be done manually, or the instance must be restored from a snapshot or a backup", disk.deviceName, err)
	}

	err = root.Remove(overlayName)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("Failed removing overlay of disk %q: %w", disk.deviceName, err)
	}

	d.resolveOverlayWarning()

	return nil
}

// CommitDiskOverlays commits the overlays of the given disk devices into their volumes.
// While the instance runs, the overlays are committed through their disk nodes.
// While it is stopped, the overlay files are committed through the block devices of the volumes,
// which deletes their volume metadata images. A device without an overlay is skipped.
func (d *qemu) CommitDiskOverlays(deviceNames []string) error {
	// A snapshot has no overlay to commit.
	// The overlay files in its config volume snapshot contain guest writes made after the snapshot,
	// which the instance commits into its own volumes.
	if d.IsSnapshot() {
		return nil
	}

	disks, err := d.disksSupportingBitmaps()
	if err != nil {
		return err
	}

	disks = selectDisks(disks, deviceNames)
	if len(disks) == 0 {
		return nil
	}

	monitor, err := d.runningMonitor()
	if err != nil {
		return err
	}

	if monitor == nil {
		return d.commitOverlayFiles(disks)
	}

	return d.commitOverlays(monitor, disks)
}

// instanceSnapshotUUIDs returns the instance snapshot UUID of every snapshot of the instance by
// snapshot name, which is the UUID of the root volume snapshot of the instance snapshot.
func (d *qemu) instanceSnapshotUUIDs() (map[string]string, error) {
	pool, err := d.getStoragePool()
	if err != nil {
		return nil, err
	}

	dbVolSnaps, err := storagePools.VolumeDBSnapshotsGet(pool, d.project.Name, d.name, storageDrivers.VolumeTypeVM)
	if err != nil {
		return nil, err
	}

	uuids := make(map[string]string, len(dbVolSnaps))
	for _, dbVolSnap := range dbVolSnaps {
		_, snapName, _ := api.GetParentAndSnapshotName(dbVolSnap.Name)
		uuids[snapName] = dbVolSnap.Config["volatile.uuid"]
	}

	return uuids, nil
}

// snapshotBitmapNames returns the names of the instance snapshots a bitmap of the disk can belong to.
// On the root disk these are the instance snapshots.
// On a custom volume they are the instance snapshots that record a snapshot of the volume in
// volatile.attached_volumes and whose recorded snapshot still exists.
func (d *qemu) snapshotBitmapNames(disk bitmapDisk) (map[string]struct{}, error) {
	snapshots, err := d.Snapshots()
	if err != nil {
		return nil, err
	}

	names := make(map[string]struct{}, len(snapshots))
	if disk.volume.Type != dbCluster.StoragePoolVolumeTypeNameCustom {
		for _, snapshot := range snapshots {
			_, snapName, _ := api.GetParentAndSnapshotName(snapshot.Name())
			names[snapName] = struct{}{}
		}

		return names, nil
	}

	pool, err := storagePools.LoadByName(d.state, disk.volume.Pool)
	if err != nil {
		return nil, err
	}

	dbVolSnaps, err := storagePools.VolumeDBSnapshotsGet(pool, disk.volumeProject(&d.project), disk.volume.Name, storageDrivers.VolumeTypeCustom)
	if err != nil {
		return nil, err
	}

	volSnapUUIDs := make([]string, 0, len(dbVolSnaps))
	for _, dbVolSnap := range dbVolSnaps {
		volSnapUUIDs = append(volSnapUUIDs, dbVolSnap.Config["volatile.uuid"])
	}

	for _, snapshot := range snapshots {
		attachedVolumes, err := parseVolatileAttachedVolumes(snapshot)
		if err != nil {
			return nil, err
		}

		// The volume is attached through one device, whose name may have changed since the snapshot.
		// The snapshot is therefore matched by the volume snapshot UUIDs in its
		// volatile.attached_volumes rather than by device.
		for _, snapshotUUID := range attachedVolumes {
			if slices.Contains(volSnapUUIDs, snapshotUUID) {
				_, snapName, _ := api.GetParentAndSnapshotName(snapshot.Name())
				names[snapName] = struct{}{}
				break
			}
		}
	}

	return names, nil
}

// removeStaleBitmaps removes from both nodes of the disk every bitmap that is not valid, and every
// bitmap that a failed snapshot left behind.
// Such a bitmap has a name that matches no snapshot the bitmap can belong to, or the name of the
// snapshot being created, whose database record exists already.
func (d *qemu) removeStaleBitmaps(monitor *qmp.Monitor, disk bitmapDisk, newBitmapName string) error {
	pairs, err := d.queryBitmapPairs(monitor, disk)
	if err != nil {
		return err
	}

	if len(pairs) == 0 {
		return nil
	}

	names, err := d.snapshotBitmapNames(disk)
	if err != nil {
		return err
	}

	for _, pair := range pairs {
		_, found := names[pair.name]
		if found && pair.name != newBitmapName && pair.valid() {
			continue
		}

		d.logger.Info("Removing stale bitmap", logger.Ctx{"device": disk.deviceName, "bitmap": pair.name, "valid": pair.valid(), "snapshot": found})
		err = d.removeBitmapPair(monitor, disk, pair)
		if err != nil {
			return err
		}
	}

	return nil
}

// snapshotMetadataImage is the snapshot metadata image of a volume snapshot, the volume metadata
// image of the volume as the config volume snapshot of an instance snapshot contains it.
type snapshotMetadataImage struct {
	path           string // Path relative to the config volume snapshot.
	deviceName     string
	volumeUUID     string
	snapshotUUID   string
	bitmaps        []snapshotBitmapFileBitmap // The bitmaps the image stores, as the snapshot bitmap file lists them.
	pool           string                     // Pool of the custom volume snapshot, empty for the root volume snapshot.
	volumeSnapshot string                     // Name of the custom volume snapshot, empty for the root volume snapshot.
}

// snapshotMetadataImages returns the snapshot metadata images that the snapshot bitmap file of the
// instance snapshot lists, with the disk device each volume was attached through.
// For a custom volume, the volume snapshot is the one that volatile.attached_volumes of the
// instance snapshot lists for the device.
// It must exist and belong to the volume that the file lists.
// The config volume snapshot must be mounted.
func (d *qemu) snapshotMetadataImages() ([]snapshotMetadataImage, error) {
	pool, err := d.getStoragePool()
	if err != nil {
		return nil, err
	}

	rootSnapVol, err := storagePools.VolumeDBGet(pool, d.project.Name, d.name, storageDrivers.VolumeTypeVM)
	if err != nil {
		return nil, err
	}

	file, err := d.readSnapshotBitmapFile(rootSnapVol.Config["volatile.uuid"])
	if err != nil {
		return nil, err
	}

	if file == nil {
		return nil, nil
	}

	rootDiskName, _, err := d.getRootDiskDevice()
	if err != nil {
		return nil, fmt.Errorf("Failed getting root disk: %w", err)
	}

	attachedVolumes, err := parseVolatileAttachedVolumes(d)
	if err != nil {
		return nil, err
	}

	volProject := project.StorageVolumeProjectFromRecord(&d.project, dbCluster.StoragePoolVolumeTypeCustom)
	customType := dbCluster.StoragePoolVolumeTypeCustom
	pools := map[string]storagePools.Pool{pool.Name(): pool}
	images := []snapshotMetadataImage{}
	for deviceName, volume := range file.Volumes {
		imageName, err := volumeMetadataImageName(volume.UUID)
		if err != nil {
			return nil, err
		}

		image := snapshotMetadataImage{
			path:       filepath.Join(qemuBitmapsDir, imageName),
			deviceName: deviceName,
			volumeUUID: volume.UUID,
			bitmaps:    volume.Bitmaps,
		}

		if deviceName == rootDiskName {
			image.snapshotUUID = file.Snapshot.UUID
			images = append(images, image)
			continue
		}

		snapshotUUID, ok := attachedVolumes[deviceName]
		if !ok {
			d.logger.Warn("Skipping snapshot metadata image of a device missing from volatile.attached_volumes of the instance snapshot", logger.Ctx{"device": deviceName})
			continue
		}

		// The custom volume snapshot can be deleted on its own.
		var dbSnapVols []*db.StorageVolume
		err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			dbSnapVols, err = tx.GetStorageVolumes(ctx, true, db.StorageVolumeFilter{Type: &customType, Project: &volProject, UUIDs: []string{snapshotUUID}})
			return err
		})
		if err != nil {
			return nil, err
		}

		if len(dbSnapVols) == 0 {
			d.logger.Warn("Skipping snapshot metadata image of a volume snapshot that no longer exists", logger.Ctx{"device": deviceName, "snapshotUUID": snapshotUUID})
			continue
		}

		dbSnapVol := dbSnapVols[0]
		volPool, ok := pools[dbSnapVol.Pool]
		if !ok {
			volPool, err = storagePools.LoadByName(d.state, dbSnapVol.Pool)
			if err != nil {
				return nil, err
			}

			pools[dbSnapVol.Pool] = volPool
		}

		volName, _, _ := api.GetParentAndSnapshotName(dbSnapVol.Name)
		dbVol, err := storagePools.VolumeDBGet(volPool, volProject, volName, storageDrivers.VolumeTypeCustom)
		if err != nil {
			return nil, err
		}

		if dbVol.Config["volatile.uuid"] != volume.UUID {
			d.logger.Warn("Skipping snapshot metadata image of a volume snapshot that belongs to another volume", logger.Ctx{"device": deviceName, "snapshotUUID": snapshotUUID, "volumeUUID": volume.UUID})
			continue
		}

		image.snapshotUUID = snapshotUUID
		image.pool = dbSnapVol.Pool
		image.volumeSnapshot = dbSnapVol.Name
		images = append(images, image)
	}

	slices.SortFunc(images, func(a snapshotMetadataImage, b snapshotMetadataImage) int {
		return strings.Compare(a.deviceName, b.deviceName)
	})

	return images, nil
}

// snapshotVolumes returns the volumes of the instance snapshot by disk device, with the pool, type
// and name they had when the snapshot was created, as the devices of the snapshot record them.
func (d *qemu) snapshotVolumes() (map[string]api.InstanceBitmapVolume, error) {
	rootDiskName, rootDiskConf, err := d.getRootDiskDevice()
	if err != nil {
		return nil, fmt.Errorf("Failed getting root disk: %w", err)
	}

	parentName, _, _ := api.GetParentAndSnapshotName(d.name)
	volumes := map[string]api.InstanceBitmapVolume{
		rootDiskName: {Pool: rootDiskConf["pool"], Type: dbCluster.StoragePoolVolumeTypeNameVM, Name: parentName, Device: rootDiskName},
	}

	for deviceName, devConf := range d.ExpandedDevices() {
		if !filters.IsCustomVolumeBlockDisk(devConf) {
			continue
		}

		volumes[deviceName] = api.InstanceBitmapVolume{Pool: devConf["pool"], Type: dbCluster.StoragePoolVolumeTypeNameCustom, Name: devConf["source"], Device: deviceName}
	}

	return volumes, nil
}

// SnapshotMetadataImages returns the snapshot metadata images of the volume snapshots of the
// instance snapshot, keyed by the disk device each volume was attached through.
// The images are on the config volume snapshot, which the caller mounts to use them.
func (d *qemu) SnapshotMetadataImages() (map[string]instance.SnapshotMetadataImage, error) {
	if !d.IsSnapshot() {
		return nil, errors.New("Instance must be a snapshot")
	}

	images := map[string]instance.SnapshotMetadataImage{}
	err := d.withConfigVolume(func() error {
		found, err := d.snapshotMetadataImages()
		if err != nil {
			return err
		}

		for _, image := range found {
			images[image.deviceName] = instance.SnapshotMetadataImage{
				Path:           image.path,
				VolumeUUID:     image.volumeUUID,
				SnapshotUUID:   image.snapshotUUID,
				Pool:           image.pool,
				VolumeSnapshot: image.volumeSnapshot,
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return images, nil
}

// bitmapEntry is a bitmap found on one volume.
type bitmapEntry struct {
	name        string
	volume      api.InstanceBitmapVolume
	granularity int64
	recording   bool
}

// groupBitmaps groups the bitmaps found on the volumes by name, sorted by name, with the instance
// snapshot UUID each name maps to.
func groupBitmaps(entries []bitmapEntry, uuids map[string]string) []api.InstanceBitmap {
	byName := make(map[string]*api.InstanceBitmap)
	for _, entry := range entries {
		bitmap, found := byName[entry.name]
		if !found {
			bitmap = &api.InstanceBitmap{Name: entry.name, UUID: uuids[entry.name]}
			byName[entry.name] = bitmap
		}

		volume := entry.volume
		volume.Granularity = entry.granularity
		volume.Recording = entry.recording
		bitmap.Volumes = append(bitmap.Volumes, volume)
	}

	bitmaps := make([]api.InstanceBitmap, 0, len(byName))
	for _, bitmap := range byName {
		slices.SortFunc(bitmap.Volumes, func(a api.InstanceBitmapVolume, b api.InstanceBitmapVolume) int {
			return strings.Compare(a.Device, b.Device)
		})

		bitmaps = append(bitmaps, *bitmap)
	}

	slices.SortFunc(bitmaps, func(a api.InstanceBitmap, b api.InstanceBitmap) int {
		return strings.Compare(a.Name, b.Name)
	})

	return bitmaps
}

// Bitmaps returns the bitmaps of the block volumes of the instance, grouped by name with one entry per volume.
// On a running instance they are read from its QEMU process, on a stopped instance from the volume
// metadata images on its config volume, and on an instance snapshot from the snapshot bitmap file
// on its config volume snapshot.
// An invalid bitmap, which did not record every write since it was created, is reported as not recording.
func (d *qemu) Bitmaps() ([]api.InstanceBitmap, error) {
	if d.IsSnapshot() {
		return d.snapshotBitmaps()
	}

	disks, err := d.disksSupportingBitmaps()
	if err != nil {
		return nil, err
	}

	uuids, err := d.instanceSnapshotUUIDs()
	if err != nil {
		return nil, err
	}

	monitor, err := d.runningMonitor()
	if err != nil {
		return nil, err
	}

	entries := []bitmapEntry{}
	if monitor != nil {
		nodeNames, err := monitor.QueryNamedBlockNodes()
		if err != nil {
			return nil, err
		}

		for _, disk := range disks {
			if !slices.Contains(nodeNames, disk.nodeName()) || !slices.Contains(nodeNames, disk.metadataDiskNodeName()) {
				continue
			}

			pairs, err := d.queryBitmapPairs(monitor, disk)
			if err != nil {
				return nil, err
			}

			for _, pair := range pairs {
				if pair.store == nil {
					continue
				}

				entries = append(entries, bitmapEntry{name: pair.name, volume: disk.volume, granularity: int64(pair.store.Granularity), recording: pair.valid()})
			}
		}

		return groupBitmaps(entries, uuids), nil
	}

	err = d.withConfigVolume(func() error {
		return d.withBitmapsDir(func(root *os.Root) error {
			for _, disk := range disks {
				imageName, err := volumeMetadataImageName(disk.volume.UUID)
				if err != nil {
					return err
				}

				exists, err := rootEntryExists(root, imageName)
				if err != nil {
					return err
				}

				if !exists {
					continue
				}

				bitmaps, err := storagePools.Qcow2Bitmaps(root, imageName)
				if err != nil {
					return err
				}

				// The bitmaps of a volume with an overlay file lack the writes in the overlay.
				overlayName, err := overlayFileName(disk.volume.UUID)
				if err != nil {
					return err
				}

				hasOverlay, err := rootEntryExists(root, overlayName)
				if err != nil {
					return err
				}

				for _, bitmap := range bitmaps {
					entries = append(entries, bitmapEntry{name: bitmap.Name, volume: disk.volume, granularity: bitmap.Granularity, recording: bitmap.Valid && !hasOverlay})
				}
			}

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return groupBitmaps(entries, uuids), nil
}

// snapshotBitmaps returns the bitmaps that the snapshot bitmap file of the instance snapshot
// lists, with the instance snapshot UUID and the granularity the file records for each.
// The file was written once the volume metadata images were verified, and the images are not read.
// The bitmap created with the snapshot is not listed.
// Every bitmap of a snapshot is disabled and reported as not recording.
// A snapshot created without a bitmap has no snapshot bitmap file and is rejected.
func (d *qemu) snapshotBitmaps() ([]api.InstanceBitmap, error) {
	entries := []bitmapEntry{}
	uuids := map[string]string{}
	err := d.withConfigVolume(func() error {
		images, err := d.snapshotMetadataImages()
		if err != nil {
			return err
		}

		if len(images) == 0 {
			return api.StatusErrorf(http.StatusBadRequest, "Snapshot was not created with a bitmap")
		}

		volumes, err := d.snapshotVolumes()
		if err != nil {
			return err
		}

		for _, image := range images {
			volume, ok := volumes[image.deviceName]
			if !ok {
				d.logger.Warn("Skipping snapshot metadata image of a device that is not a custom block volume disk of the instance snapshot", logger.Ctx{"device": image.deviceName, "volumeUUID": image.volumeUUID})
				continue
			}

			volume.UUID = image.volumeUUID

			for _, bitmap := range image.bitmaps {
				uuids[bitmap.Name] = bitmap.UUID
				entries = append(entries, bitmapEntry{name: bitmap.Name, volume: volume, granularity: bitmap.Granularity, recording: false})
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return groupBitmaps(entries, uuids), nil
}

// removeBitmaps removes from the given disks every bitmap whose name matches, from both nodes of a
// running instance and from the volume metadata images of a stopped one.
// The snapshot metadata images keep their copies, as a snapshot is never modified.
func (d *qemu) removeBitmaps(disks []bitmapDisk, match func(bitmapName string) bool) error {
	monitor, err := d.runningMonitor()
	if err != nil {
		return err
	}

	if monitor != nil {
		return d.removeLiveBitmaps(monitor, disks, match)
	}

	return d.withConfigVolume(func() error {
		return d.withBitmapsDir(func(root *os.Root) error {
			for _, disk := range disks {
				imageName, err := volumeMetadataImageName(disk.volume.UUID)
				if err != nil {
					return err
				}

				exists, err := rootEntryExists(root, imageName)
				if err != nil {
					return err
				}

				if !exists {
					continue
				}

				bitmaps, err := storagePools.Qcow2Bitmaps(root, imageName)
				if err != nil {
					return err
				}

				for _, bitmap := range bitmaps {
					if !match(bitmap.Name) {
						continue
					}

					err = storagePools.Qcow2RemoveBitmap(root, imageName, bitmap.Name)
					if err != nil {
						return err
					}
				}
			}

			return nil
		})
	})
}

// removeLiveBitmaps removes from both nodes of the given disks of the running instance every bitmap whose name matches.
// A disk without both nodes is skipped.
func (d *qemu) removeLiveBitmaps(monitor *qmp.Monitor, disks []bitmapDisk, match func(bitmapName string) bool) error {
	nodeNames, err := monitor.QueryNamedBlockNodes()
	if err != nil {
		return err
	}

	for _, disk := range disks {
		if !slices.Contains(nodeNames, disk.nodeName()) || !slices.Contains(nodeNames, disk.metadataDiskNodeName()) {
			continue
		}

		pairs, err := d.queryBitmapPairs(monitor, disk)
		if err != nil {
			return err
		}

		for _, pair := range pairs {
			if !match(pair.name) {
				continue
			}

			err = d.removeBitmapPair(monitor, disk, pair)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// DeleteBitmap removes the named bitmap from every block volume of the instance.
// A volume without the bitmap is left as it is.
func (d *qemu) DeleteBitmap(bitmapName string) error {
	if d.IsSnapshot() {
		return api.StatusErrorf(http.StatusBadRequest, "Bitmaps cannot be deleted from a snapshot")
	}

	disks, err := d.disksSupportingBitmaps()
	if err != nil {
		return err
	}

	return d.removeBitmaps(disks, func(name string) bool { return name == bitmapName })
}

// DeleteDiskBitmap removes the named bitmap from the volume attached through the disk device.
// A volume without the bitmap is left as it is.
func (d *qemu) DeleteDiskBitmap(deviceName string, bitmapName string) error {
	if d.IsSnapshot() {
		return api.StatusErrorf(http.StatusBadRequest, "Bitmaps cannot be deleted from a snapshot")
	}

	disk, err := d.bitmapDisk(deviceName)
	if err != nil {
		return err
	}

	if disk == nil {
		return nil
	}

	return d.removeBitmaps([]bitmapDisk{*disk}, func(name string) bool { return name == bitmapName })
}

// DeleteVolumeBitmaps deletes every bitmap of the volume attached through the disk device, from
// the disk node of a running instance and from the volume metadata image on the config volume.
// It runs before the volume is written by something other than the instance's own QEMU process, or
// once another instance can write to it.
func (d *qemu) DeleteVolumeBitmaps(deviceName string) error {
	devConf, ok := d.ExpandedDevices()[deviceName]
	if !ok {
		return fmt.Errorf("Disk device %q not found", deviceName)
	}

	return d.deleteDiskBitmaps(deviceName, devConf)
}

// deleteDiskBitmaps deletes every bitmap of the volume attached through the disk device of the
// given config, which is not required to be in the current devices of the instance, and its volume
// metadata image. The next snapshot with a bitmap creates the image again.
func (d *qemu) deleteDiskBitmaps(deviceName string, devConf map[string]string) error {
	disk, err := d.bitmapDiskFromConfig(deviceName, devConf)
	if err != nil {
		return err
	}

	if disk == nil {
		return nil
	}

	monitor, err := d.runningMonitor()
	if err != nil {
		return err
	}

	if monitor == nil {
		return d.RemoveVolumeMetadataImage(disk.volume.UUID)
	}

	nodeNames, err := monitor.QueryNamedBlockNodes()
	if err != nil {
		return err
	}

	// The bitmaps of the disk node are removed without the metadata disk node too, as a snapshot
	// with a bitmap removes the metadata disk nodes until it commits its overlays.
	// The overlay file is kept, as an overlay node may still write to it.
	if slices.Contains(nodeNames, disk.nodeName()) {
		diskBitmaps, err := monitor.QueryNodeDirtyBitmaps(disk.nodeName())
		if err != nil {
			return fmt.Errorf("Failed querying bitmaps of disk %q: %w", disk.deviceName, err)
		}

		for _, bitmap := range diskBitmaps {
			err = monitor.RemoveDirtyBitmap(disk.nodeName(), bitmap.Name)
			if err != nil {
				return fmt.Errorf("Failed removing bitmap %q of disk %q: %w", bitmap.Name, disk.deviceName, err)
			}
		}
	}

	if slices.Contains(nodeNames, disk.metadataDiskNodeName()) {
		d.removeQcow2Node(monitor, disk.metadataDiskNodeName())
	}

	imageName, err := volumeMetadataImageName(disk.volume.UUID)
	if err != nil {
		return err
	}

	return d.removeMetadataImagesFiles(imageName)
}

// RemoveVolumeMetadataImage deletes the volume metadata image of the volume of the given UUID from
// the config volume, and the overlay of the volume with it.
// A running QEMU process that has the image open keeps its own descriptor, and writes the bitmaps
// of the volume into the unlinked file when it closes it.
func (d *qemu) RemoveVolumeMetadataImage(volumeUUID string) error {
	imageName, err := volumeMetadataImageName(volumeUUID)
	if err != nil {
		return err
	}

	overlayName, err := overlayFileName(volumeUUID)
	if err != nil {
		return err
	}

	return d.withConfigVolume(func() error {
		return d.removeMetadataImagesFiles(imageName, overlayName)
	})
}

// RemoveAllMetadataImages deletes the bitmaps directory and every file in it from the config volume.
// None of the files matches the volumes of an instance that was created by a copy, a refresh, an
// import or a move, or that was restored from a snapshot.
func (d *qemu) RemoveAllMetadataImages() error {
	return d.withConfigVolume(func() error {
		root, err := d.OpenRoot()
		if err != nil {
			return err
		}

		defer func() { _ = root.Close() }()

		return root.RemoveAll(qemuBitmapsDir)
	})
}

// removeDetachedMetadataImages deletes the volume metadata images of the volumes that were
// attached through the given removed devices and are no longer attached through any device.
// A device rename removes and adds the device while the volume stays attached.
// The image of its volume is therefore kept.
// On a stopped instance an overlay left on the volume is committed first, as the overlay is deleted with the image.
func (d *qemu) removeDetachedMetadataImages(removeDevices deviceConfig.Devices) error {
	disks, err := d.disksSupportingBitmaps()
	if err != nil {
		return err
	}

	attached := make([]string, 0, len(disks))
	for _, disk := range disks {
		attached = append(attached, disk.volume.UUID)
	}

	detached := []bitmapDisk{}
	for deviceName, devConf := range removeDevices {
		if !filters.IsCustomVolumeBlockDisk(devConf) {
			continue
		}

		volume, err := d.diskVolume(deviceName, devConf, false, make(map[string]storagePools.Pool))
		if err != nil {
			return err
		}

		if volume == nil || slices.Contains(attached, volume.UUID) {
			continue
		}

		detached = append(detached, bitmapDisk{deviceName: deviceName, volume: *volume})
	}

	if len(detached) == 0 {
		return nil
	}

	return d.withInstanceMounted(func(mountInfo *storagePools.MountInfo) error {
		rootDevicePath := ""
		if mountInfo != nil {
			devSource, ok := mountInfo.DevSource.(deviceConfig.DevSourcePath)
			if ok {
				rootDevicePath = devSource.Path
			}
		}

		return d.withBitmapsDir(func(root *os.Root) error {
			for _, disk := range detached {
				imageName, err := volumeMetadataImageName(disk.volume.UUID)
				if err != nil {
					return err
				}

				overlayName, err := overlayFileName(disk.volume.UUID)
				if err != nil {
					return err
				}

				hasOverlay, err := rootEntryExists(root, overlayName)
				if err != nil {
					return err
				}

				if !d.IsRunning() && hasOverlay {
					err := d.commitOverlayFile(root, disk, overlayName, rootDevicePath)
					if err != nil {
						return err
					}
				}

				for _, name := range []string{imageName, overlayName} {
					err := root.Remove(name)
					if err != nil && !errors.Is(err, fs.ErrNotExist) {
						return fmt.Errorf("Failed removing metadata image of disk %q: %w", disk.deviceName, err)
					}
				}
			}

			return nil
		})
	})
}

// CreateSnapshotBitmaps creates the bitmap of a snapshot on the volumes attached through the given disk devices.
// A missing volume metadata image is created first and kept when a later step fails.
// One QEMU transaction merges every valid disk bitmap into its store bitmap, creates the new
// bitmap and adds an overlay to each volume.
// The guest then writes to the overlays, which keeps the volumes as they were at the transaction
// for the storage snapshots taken afterwards.
// Removing the metadata disk nodes writes the images, and the snapshot bitmap file is written next to them.
// CommitDiskOverlays commits the overlays and adds the metadata disk nodes back.
// A device whose volume does not support bitmaps is skipped, and the devices that got an overlay are returned.
func (d *qemu) CreateSnapshotBitmaps(snapshotUUID string, deviceNames []string, bitmapName string) ([]string, error) {
	if !d.IsRunning() {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Instance is not running")
	}

	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return nil, err
	}

	nodeNames, err := monitor.QueryNamedBlockNodes()
	if err != nil {
		return nil, err
	}

	disks, err := d.disksSupportingBitmaps()
	if err != nil {
		return nil, err
	}

	type snapshotDisk struct {
		bitmapDisk
		bitmaps []qmp.BlockDirtyInfo // The store bitmaps of the valid pairs, which the image is written with.
	}

	selected := make([]snapshotDisk, 0, len(deviceNames))
	for _, disk := range selectDisks(disks, deviceNames) {
		if !slices.Contains(nodeNames, disk.nodeName()) {
			continue
		}

		// An overlay left by a failed commit is committed before a new overlay is added.
		if slices.Contains(nodeNames, overlayNodeName(disk.deviceName)) {
			err = d.commitOverlay(monitor, disk)
			if err != nil {
				return nil, err
			}
		}

		// The first snapshot with a bitmap that covers the disk creates its volume metadata image.
		// The disk bitmaps that the disk kept without a metadata disk node lack their store half
		// and are removed as stale.
		if !slices.Contains(nodeNames, disk.metadataDiskNodeName()) {
			_, err = d.addMetadataDiskNode(monitor, disk, true)
			if err != nil {
				return nil, fmt.Errorf("Failed adding volume metadata image of disk %q: %w", disk.deviceName, err)
			}
		}

		err = d.removeStaleBitmaps(monitor, disk, bitmapName)
		if err != nil {
			return nil, err
		}

		pairs, err := d.queryBitmapPairs(monitor, disk)
		if err != nil {
			return nil, err
		}

		bitmaps := make([]qmp.BlockDirtyInfo, 0, len(pairs))
		for _, pair := range pairs {
			bitmaps = append(bitmaps, *pair.store)
		}

		selected = append(selected, snapshotDisk{bitmapDisk: disk, bitmaps: bitmaps})
	}

	if len(selected) == 0 {
		return []string{}, nil
	}

	uuids, err := d.instanceSnapshotUUIDs()
	if err != nil {
		return nil, err
	}

	root, err := d.openBitmapsDir()
	if err != nil {
		return nil, err
	}

	defer func() { _ = root.Close() }()

	revert := revert.New()
	defer revert.Fail()

	file := &snapshotBitmapFile{
		Snapshot: snapshotBitmapFileSnapshot{UUID: snapshotUUID},
		Volumes:  make(map[string]snapshotBitmapFileVolume, len(selected)),
	}

	overlayDevices := make([]string, 0, len(selected))
	actions := make([]qmp.TransactionAction, 0, len(selected)*3)
	for _, disk := range selected {
		size, err := monitor.BlockNodeSize(disk.nodeName())
		if err != nil {
			return nil, fmt.Errorf("Failed getting size of disk %q: %w", disk.deviceName, err)
		}

		overlayNode := overlayNodeName(disk.deviceName)
		overlayName, err := overlayFileName(disk.volume.UUID)
		if err != nil {
			return nil, err
		}

		removeOverlay, err := d.createQcow2Node(monitor, overlayNode, root, overlayName, size, map[string]any{"backing": nil})
		if err != nil {
			return nil, fmt.Errorf("Failed creating overlay of disk %q: %w", disk.deviceName, err)
		}

		revert.Add(func() {
			removeOverlay()
			_ = d.removeMetadataImagesFiles(overlayName)
		})

		overlayDevices = append(overlayDevices, disk.deviceName)

		// Merging in the same transaction that adds the overlay persists every write up to the instant of the snapshot.
		volume := snapshotBitmapFileVolume{UUID: disk.volume.UUID, Bitmaps: make([]snapshotBitmapFileBitmap, 0, len(disk.bitmaps))}
		for _, bitmap := range disk.bitmaps {
			actions = append(actions, qmp.BlockDirtyBitmapMergeAction(disk.metadataDiskNodeName(), bitmap.Name, []qmp.BlockDirtyBitmapSource{{Node: disk.nodeName(), Name: bitmap.Name}}))
			volume.Bitmaps = append(volume.Bitmaps, snapshotBitmapFileBitmap{Name: bitmap.Name, UUID: uuids[bitmap.Name], Granularity: int64(bitmap.Granularity)})
		}

		file.Volumes[disk.deviceName] = volume

		// The new bitmap is created on the disk node rather than on the overlay.
		// This lets it record the writes committed from the overlay.
		// Its store bitmap is created at the same instant.
		actions = append(actions,
			qmp.BlockDirtyBitmapAddAction(disk.metadataDiskNodeName(), bitmapName, qemuBitmapGranularity, true, true),
			qmp.BlockDirtyBitmapAddAction(disk.nodeName(), bitmapName, qemuBitmapGranularity, false, false),
			qmp.BlockDevSnapshotAction(disk.nodeName(), overlayNode))
	}

	err = monitor.RunTransaction(actions)
	if err != nil {
		return nil, fmt.Errorf("Failed creating bitmaps: %w", err)
	}

	// From here on the guest writes to the overlays, which only a commit may remove.
	revert.Success()

	// QEMU writes the bitmaps into the volume metadata image when the metadata disk node is
	// removed, and reports a failed write on its standard error only.
	// Therefore, every image is read back.
	// The disk bitmaps record the writes of the run until the commit of the overlays adds the metadata disk nodes back.
	for _, disk := range selected {
		d.removeQcow2Node(monitor, disk.metadataDiskNodeName())
	}

	for _, disk := range selected {
		err = d.verifySnapshotMetadataImage(root, disk.bitmapDisk, disk.bitmaps, bitmapName)
		if err != nil {
			break
		}
	}

	if err == nil {
		err = d.writeSnapshotBitmapFile(file)
		if err != nil {
			err = fmt.Errorf("Failed writing snapshot bitmap file: %w", err)
		}
	}

	if err != nil {
		// Undo the snapshot as the caller does when a later step fails.
		// Committing the overlays adds the metadata disk nodes back.
		selectedDisks := make([]bitmapDisk, 0, len(selected))
		for _, disk := range selected {
			selectedDisks = append(selectedDisks, disk.bitmapDisk)
		}

		commitErr := d.commitOverlays(monitor, selectedDisks)
		if commitErr != nil {
			d.logger.Error("Failed committing overlays", logger.Ctx{"err": commitErr})
		}

		removeErr := d.removeLiveBitmaps(monitor, selectedDisks, func(name string) bool { return name == bitmapName })
		if removeErr != nil {
			d.logger.Warn("Failed removing bitmap", logger.Ctx{"bitmap": bitmapName, "err": removeErr})
		}

		_ = d.RemoveSnapshotBitmapFile(snapshotUUID)

		return nil, err
	}

	return overlayDevices, nil
}

// verifySnapshotMetadataImage checks that the volume metadata image of the disk in root, the
// bitmaps directory, stores every given bitmap and the bitmap of the given name, and that none is
// in use, before the storage snapshot of the config volume.
func (d *qemu) verifySnapshotMetadataImage(root *os.Root, disk bitmapDisk, bitmaps []qmp.BlockDirtyInfo, bitmapName string) error {
	imageName, err := volumeMetadataImageName(disk.volume.UUID)
	if err != nil {
		return err
	}

	stored, err := storagePools.Qcow2Bitmaps(root, imageName)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(bitmaps)+1)
	for _, bitmap := range bitmaps {
		names = append(names, bitmap.Name)
	}

	names = append(names, bitmapName)
	for _, name := range names {
		index := slices.IndexFunc(stored, func(bitmap storagePools.Qcow2Bitmap) bool { return bitmap.Name == name })
		if index < 0 || !stored[index].Valid {
			return fmt.Errorf("Failed writing the volume metadata image of disk %q: Bitmap %q is missing or in use", disk.deviceName, name)
		}
	}

	return nil
}
