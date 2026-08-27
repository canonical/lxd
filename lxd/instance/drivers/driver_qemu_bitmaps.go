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

	"go.yaml.in/yaml/v2"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/device/filters"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/drivers/qmp"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/project"
	storagePools "github.com/canonical/lxd/lxd/storage"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/features"
	"github.com/canonical/lxd/shared/logger"
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
// bitmap file, snapshot.<name>.yaml, in the bitmaps directory.
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
// UUID of its root volume snapshot, and by the name it was created under.
type snapshotBitmapFileSnapshot struct {
	UUID string `yaml:"uuid"`
	Name string `yaml:"name"`
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

// snapshotBitmapFileName returns the name of the snapshot bitmap file of the snapshot of the given
// name in the bitmaps directory.
// Validating the name keeps a caller from placing the file outside the directory.
func snapshotBitmapFileName(snapshotName string) (string, error) {
	err := instancetype.ValidSnapName(snapshotName)
	if err != nil {
		return "", err
	}

	return qemuSnapshotBitmapFilePrefix + snapshotName + qemuSnapshotBitmapFileSuffix, nil
}

// writeSnapshotBitmapFile writes the snapshot bitmap file into the bitmaps directory, named after
// the snapshot it records.
func (d *qemu) writeSnapshotBitmapFile(file *snapshotBitmapFile) error {
	fileName, err := snapshotBitmapFileName(file.Snapshot.Name)
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
// The files are matched by UUID rather than by name, because a renamed snapshot keeps the file of
// its name at creation, and files left by failed snapshots can be in the same directory.
func (d *qemu) readSnapshotBitmapFile(snapshotUUID string) (*snapshotBitmapFile, error) {
	var found *snapshotBitmapFile
	err := d.withBitmapsDir(func(root *os.Root) error {
		entries, err := fs.ReadDir(root.FS(), ".")
		if err != nil {
			return err
		}

		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), qemuSnapshotBitmapFilePrefix) || !strings.HasSuffix(entry.Name(), qemuSnapshotBitmapFileSuffix) {
				continue
			}

			data, err := root.ReadFile(entry.Name())
			if err != nil {
				return err
			}

			var file snapshotBitmapFile
			err = yaml.Unmarshal(data, &file)
			if err != nil {
				return fmt.Errorf("Failed parsing snapshot bitmap file %q: %w", entry.Name(), err)
			}

			if file.Snapshot.UUID == snapshotUUID {
				found = &file
				return nil
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return found, nil
}

// bitmapsEnabled reports whether this QEMU process stores the bitmaps of its block disks in their
// volume metadata images.
// A live migration target does not, because the images on its config volume are the ones of the
// source, which still has them open, and it removes them once the migration is complete.
func (d *qemu) bitmapsEnabled() bool {
	return features.IsEnabled(features.ChangedBlockTracking) && d.migrationReceiveStateful == nil
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

	entries := []bitmapEntry{}
	if d.IsRunning() {
		monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
		if err != nil {
			return nil, err
		}

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
func (d *qemu) snapshotBitmaps() ([]api.InstanceBitmap, error) {
	entries := []bitmapEntry{}
	uuids := map[string]string{}
	err := d.withConfigVolume(func() error {
		images, err := d.snapshotMetadataImages()
		if err != nil {
			return err
		}

		if len(images) == 0 {
			return nil
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
	if d.IsRunning() {
		monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
		if err != nil {
			return err
		}

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
	rootDiskName, _, err := d.getRootDiskDevice()
	if err != nil {
		return fmt.Errorf("Failed getting root disk: %w", err)
	}

	volume, err := d.diskVolume(deviceName, devConf, deviceName == rootDiskName, make(map[string]storagePools.Pool))
	if err != nil {
		return err
	}

	if volume == nil {
		return nil
	}

	if !d.IsRunning() {
		return d.RemoveVolumeMetadataImage(volume.UUID)
	}

	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return err
	}

	nodeNames, err := monitor.QueryNamedBlockNodes()
	if err != nil {
		return err
	}

	// The bitmaps of the disk node are removed without the metadata disk node too, as a snapshot
	// with a bitmap removes the metadata disk nodes until it commits its overlays.
	// The overlay file is kept, as an overlay node may still write to it.
	disk := bitmapDisk{deviceName: deviceName, volume: *volume}
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

	imageName, err := volumeMetadataImageName(volume.UUID)
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
