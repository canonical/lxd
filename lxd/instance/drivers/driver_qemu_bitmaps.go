package drivers

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/instance/drivers/qmp"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/shared/logger"
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
