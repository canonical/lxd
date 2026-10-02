package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/refcount"
	"github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// nbdSocketPath returns the socket path of the NBD server session called name.
// The name is encoded to keep a snapshot name from producing a nested path, and hashed when the
// path would not fit the unix socket path limit.
func nbdSocketPath(name string) string {
	dir := shared.VarPath("nbd")
	name = filesystem.PathNameEncode(name)

	// sun_path is 108 bytes long including the terminating NUL.
	// The path is kept below 100 bytes as a margin.
	maxNameLength := 100 - len(dir) - len("/.sock")
	if maxNameLength > 0 && len(name) > maxNameLength {
		hash := sha256.Sum256([]byte(name))
		name = base64.RawURLEncoding.EncodeToString(hash[:])
		if len(name) > maxNameLength {
			name = name[:maxNameLength]
		}
	}

	return filepath.Join(dir, name+".sock")
}

// nbdVolumeLockMember returns the member name that nbdVolumeLockName scopes a custom volume's NBD lock with.
func (b *lxdBackend) nbdVolumeLockMember() string {
	if b.driver.Info().Remote {
		return ""
	}

	return b.state.ServerName
}

// nbdExportRefName returns the name of the reference counter of the snapshot NBD exports served by
// this member for a volume.
// volName is a snapshot, or the volume itself for the exports of all its snapshots.
func nbdExportRefName(poolName string, volType drivers.VolumeType, projectName string, volName string) string {
	return drivers.OperationLockName("NBDExport", poolName, volType, drivers.ContentTypeBlock, project.StorageVolume(projectName, volName))
}

// nbdExportCount counts an NBD export of the given volume snapshot, and of its volume, until the
// returned function runs. Every client opens its own export of a snapshot.
// Therefore, the exports are counted where an import session takes a lock.
func nbdExportCount(poolName string, volType drivers.VolumeType, projectName string, snapshotName string) func() {
	parentName, _, _ := api.GetParentAndSnapshotName(snapshotName)
	refNames := []string{nbdExportRefName(poolName, volType, projectName, snapshotName), nbdExportRefName(poolName, volType, projectName, parentName)}
	for _, refName := range refNames {
		refcount.Increment(refName, 1)
	}

	return func() {
		for _, refName := range refNames {
			refcount.Decrement(refName, 1)
		}
	}
}

// NBDExportInUse returns an in use error while this member serves an NBD export of the named volume snapshot.
// Given a volume, it returns the error while an export of any of its snapshots is served.
// An export has the volume snapshots and the config volume snapshot of the instance snapshot mounted.
func NBDExportInUse(poolName string, volType drivers.VolumeType, projectName string, volName string) error {
	if refcount.Get(nbdExportRefName(poolName, volType, projectName, volName)) == 0 {
		return nil
	}

	if shared.IsSnapshot(volName) {
		return api.StatusErrorf(http.StatusConflict, "Snapshot %q is exported over NBD: %w", volName, drivers.ErrInUse)
	}

	return api.StatusErrorf(http.StatusConflict, "A snapshot of %q is exported over NBD: %w", volName, drivers.ErrInUse)
}

// GetVolumeNBD returns a read-write NBD connection to a block volume, served by qemu-nbd against
// the volume, for writing a backup back.
// The virtual machine that uses the volume must be stopped and on this member, as qemu-nbd opens the volume itself.
// An overlay left on the disk is committed first, and the bitmaps of the volume are deleted, as
// the export writes the volume. The returned conflict reference is the lock name of the session.
func (b *lxdBackend) GetVolumeNBD(projectName string, volType drivers.VolumeType, volName string) (net.Conn, func(), string, error) {
	l := b.logger.AddContext(logger.Ctx{"project": projectName, "volType": volType, "volume": volName})
	l.Debug("GetVolumeNBD started")
	defer l.Debug("GetVolumeNBD finished")

	err := b.isStatusReady()
	if err != nil {
		return nil, nil, "", err
	}

	if shared.IsSnapshot(volName) {
		return nil, nil, "", api.StatusErrorf(http.StatusBadRequest, "Snapshots are exported over NBD with their instance snapshot")
	}

	switch volType {
	case drivers.VolumeTypeVM:
		inst, rootDiskName, err := InstanceByVolumeName(b.state, b.name, projectName, volName, cluster.StoragePoolVolumeTypeVM)
		if err != nil {
			return nil, nil, "", err
		}

		dbVol, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeVM)
		if err != nil {
			return nil, nil, "", err
		}

		if shared.IsTrue(dbVol.Config["security.shared"]) {
			return nil, nil, "", api.StatusErrorf(http.StatusBadRequest, "NBD export is not supported for shared volumes")
		}

		if inst.IsRunning() {
			return nil, nil, "", api.StatusErrorf(http.StatusBadRequest, "NBD export requires the instance to be stopped")
		}

		// Generate the effective root device volume for instance.
		vol := b.GetVolume(drivers.VolumeTypeVM, drivers.ContentTypeBlock, project.Instance(projectName, volName), dbVol.Config)
		err = b.applyInstanceRootDiskOverrides(inst, &vol)
		if err != nil {
			return nil, nil, "", err
		}

		lockName := nbdInstanceLockName(projectName, volName)
		return nbdLockedSession(b.state, lockName, fmt.Sprintf("instance %q", volName), func() (net.Conn, func(), error) {
			err := inst.CommitDiskOverlays([]string{rootDiskName})
			if err != nil {
				return nil, nil, err
			}

			err = inst.DeleteVolumeBitmaps(rootDiskName)
			if err != nil {
				return nil, nil, fmt.Errorf("Failed deleting bitmaps: %w", err)
			}

			return b.connectOfflineNBD(vol)
		})
	case drivers.VolumeTypeCustom:
		dbVol, err := VolumeDBGet(b, projectName, volName, drivers.VolumeTypeCustom)
		if err != nil {
			return nil, nil, "", err
		}

		if dbVol.ContentType != cluster.StoragePoolVolumeContentTypeNameBlock {
			return nil, nil, "", api.StatusErrorf(http.StatusBadRequest, "NBD export is only supported for block volumes")
		}

		if shared.IsTrue(dbVol.Config["security.shared"]) {
			return nil, nil, "", api.StatusErrorf(http.StatusBadRequest, "NBD export is not supported for shared volumes")
		}

		// A non-shared block volume is attached to at most one instance, which must be stopped and
		// on this member before qemu-nbd can open the volume.
		instanceDevices := make(map[instance.Instance][]string)
		err = VolumeUsedByInstanceDevices(b.state, b.name, projectName, &dbVol.StorageVolume, true, func(dbInst db.InstanceArgs, project api.Project, usedByDevices []string) error {
			if dbInst.Node != b.state.ServerName {
				return api.StatusErrorf(http.StatusBadRequest, "Volume is attached to instance %q on cluster member %q", dbInst.Name, dbInst.Node)
			}

			inst, err := instance.Load(b.state, dbInst, project)
			if err != nil {
				return err
			}

			if inst.IsRunning() {
				return api.StatusErrorf(http.StatusBadRequest, "NBD export requires instance %q to be stopped", dbInst.Name)
			}

			instanceDevices[inst] = usedByDevices
			return nil
		})
		if err != nil {
			return nil, nil, "", err
		}

		vol := b.GetVolume(drivers.VolumeTypeCustom, drivers.ContentTypeBlock, project.StorageVolume(projectName, volName), dbVol.Config)

		lockName := nbdVolumeLockName(b.nbdVolumeLockMember(), b.name, projectName, volName)
		return nbdLockedSession(b.state, lockName, fmt.Sprintf("volume %q", b.name+"/"+volName), func() (net.Conn, func(), error) {
			for inst, deviceNames := range instanceDevices {
				err := inst.CommitDiskOverlays(deviceNames)
				if err != nil {
					return nil, nil, err
				}

				for _, deviceName := range deviceNames {
					err := inst.DeleteVolumeBitmaps(deviceName)
					if err != nil {
						return nil, nil, fmt.Errorf("Failed deleting bitmaps: %w", err)
					}
				}
			}

			return b.connectOfflineNBD(vol)
		})
	default:
		return nil, nil, "", api.StatusErrorf(http.StatusBadRequest, "NBD export is not supported for volumes of type %q", volType)
	}
}

// connectOfflineNBD serves the volume read-write through qemu-nbd and returns a connection to it.
// The volume stays activated until qemu-nbd exits, which happens once the connection is closed or
// the returned disconnect function is called.
// The disconnect function returns once the volume has been released.
func (b *lxdBackend) connectOfflineNBD(vol drivers.Volume) (net.Conn, func(), error) {
	socketPath := nbdSocketPath(vol.Pool() + "_" + string(vol.Type()) + "_" + vol.Name())

	err := os.MkdirAll(filepath.Dir(socketPath), 0700)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed creating NBD socket directory: %w", err)
	}

	// A socket left behind by a session that did not shut down cleanly would pass the readiness
	// check before qemu-nbd listens on it.
	_ = os.Remove(socketPath)

	// LXD block volumes are raw, and format probing must never run on guest controlled data.
	args := []string{"--socket=" + socketPath, "--format=raw"}

	// Share the qemu-nbd process safely between the activation goroutine and this function.
	var procMu sync.Mutex
	var proc *os.Process
	aborted := false

	// Buffered so the goroutine never blocks if this function has already given up.
	errCh := make(chan error, 1)

	// started is closed once qemu-nbd runs and done once the goroutine has released the volume.
	started := make(chan struct{})
	done := make(chan struct{})

	// The goroutine returns only when qemu-nbd exits.
	// This keeps the volume active for the whole session.
	go func() {
		defer close(done)

		err := b.driver.ActivateTask(vol, func(devPath string) error {
			var stderr bytes.Buffer
			cmd := exec.Command("qemu-nbd", append(args, devPath)...)
			cmd.Stderr = &stderr

			procMu.Lock()
			if aborted {
				procMu.Unlock()
				return errors.New("Request timed out before qemu-nbd could be started")
			}

			err := cmd.Start()
			if err != nil {
				procMu.Unlock()
				return fmt.Errorf("Failed starting qemu-nbd: %w", err)
			}

			proc = cmd.Process
			procMu.Unlock()
			close(started)

			err = cmd.Wait()
			if err != nil {
				return fmt.Errorf("Failed running qemu-nbd: %w (%s)", err, strings.TrimSpace(stderr.String()))
			}

			return nil
		})
		if err != nil {
			procMu.Lock()
			stopped := aborted
			procMu.Unlock()

			// qemu-nbd reports an error when LXD itself stopped it.
			if stopped {
				b.logger.Debug("Stopped serving volume over NBD", logger.Ctx{"volume": vol.Name(), "err": err})
			} else {
				b.logger.Error("Failed serving volume over NBD", logger.Ctx{"volume": vol.Name(), "err": err})
			}

			errCh <- err
		}
	}()

	// stopNBD signals qemu-nbd if it was started and waits for the goroutine to release the volume.
	// The process is killed if it has not exited after 30 seconds.
	stopNBD := func(sig os.Signal) {
		procMu.Lock()
		aborted = true
		if proc != nil {
			_ = proc.Signal(sig)
		}

		procMu.Unlock()

		select {
		case <-done:
		case <-time.After(30 * time.Second):
			procMu.Lock()
			if proc != nil {
				_ = proc.Kill()
			}

			procMu.Unlock()

			select {
			case <-done:
			case <-time.After(30 * time.Second):
				b.logger.Error("Timed out waiting for qemu-nbd to exit", logger.Ctx{"volume": vol.Name()})
			}
		}

		_ = os.Remove(socketPath)
	}

	// Activating the volume can take a while on drivers that wait for device nodes.
	// The socket readiness timeout therefore only starts once qemu-nbd runs.
	select {
	case <-started:
	case err := <-errCh:
		stopNBD(unix.SIGKILL)
		return nil, nil, err
	case <-time.After(2 * time.Minute):
		stopNBD(unix.SIGKILL)
		return nil, nil, errors.New("Timed out waiting for volume activation")
	}

	// Wait for qemu-nbd to listen on the socket.
	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for ready := false; !ready; {
		select {
		case <-timeout:
			stopNBD(unix.SIGKILL)
			return nil, nil, errors.New("Timed out waiting for qemu-nbd socket")
		case err := <-errCh:
			stopNBD(unix.SIGKILL)
			return nil, nil, err
		case <-ticker.C:
			_, err := os.Stat(socketPath)
			ready = err == nil
		}
	}

	b.logger.Debug("Connecting to qemu-nbd socket", logger.Ctx{"volume": vol.Name(), "socketPath": socketPath})
	nbdConn, err := net.Dial("unix", socketPath)
	if err != nil {
		// qemu-nbd may have exited before the connection, in which case its error contains the reason.
		select {
		case err = <-errCh:
		default:
			err = fmt.Errorf("Failed connecting to qemu-nbd socket: %w", err)
		}

		stopNBD(unix.SIGKILL)
		return nil, nil, err
	}

	disconnect := func() {
		b.logger.Debug("Stopping qemu-nbd", logger.Ctx{"volume": vol.Name()})
		_ = nbdConn.Close()
		stopNBD(unix.SIGTERM)
	}

	return nbdConn, disconnect, nil
}

// nbdExport describes one export served by a snapshot NBD server.
type nbdExport struct {
	name          string   // Export name.
	devPath       string   // Block device of the volume snapshot.
	metadataImage *os.File // Snapshot metadata image with the bitmaps of the snapshot.
	bitmapNames   []string // Names of the bitmaps of the image to expose.
}

// serveSnapshotNBD serves the given exports read-only through qemu-storage-daemon listening on
// socketPath and returns a connection to it.
// Each export exposes the given bitmaps of its snapshot metadata image, which the daemon inherits
// and opens as a separate read-only node over the same block device.
// The returned disconnect function ends the daemon and returns once it has exited.
func (b *lxdBackend) serveSnapshotNBD(socketPath string, exports []nbdExport) (net.Conn, func(), error) {
	err := os.MkdirAll(filepath.Dir(socketPath), 0700)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed creating NBD socket directory: %w", err)
	}

	// A socket left behind by a session that did not shut down cleanly would pass the readiness
	// check before the daemon listens on it.
	_ = os.Remove(socketPath)

	// The daemon processes its options in order.
	// The server must start before the exports are added.
	args := []string{"--nbd-server", "addr.type=unix,addr.path=" + socketPath}
	metadataImages := make([]*os.File, 0, len(exports))
	for i, export := range exports {
		info, err := os.Stat(export.devPath)
		if err != nil {
			return nil, nil, err
		}

		dataDriver := "file"
		if shared.IsBlockdev(info.Mode()) {
			dataDriver = "host_device"
		}

		dataNode := fmt.Sprintf("data%d", i)
		metadataImageNode := fmt.Sprintf("metadata%d", i)
		options := []map[string]any{
			{"driver": dataDriver, "node-name": dataNode + "-file", "filename": export.devPath, "read-only": true},
			// LXD block volumes are raw, and format probing must never run on guest controlled data.
			{"driver": "raw", "node-name": dataNode, "file": dataNode + "-file", "read-only": true},
			// The image is opened over the block device as its data file, as the data file
			// recorded in the image is a temporary file that no longer exists.
			{"driver": "file", "node-name": metadataImageNode + "-file", "filename": fmt.Sprintf("/proc/self/fd/%d", 3+len(metadataImages)), "read-only": true},
			{"driver": "qcow2", "node-name": metadataImageNode, "file": metadataImageNode + "-file", "data-file": dataNode, "read-only": true},
		}

		published := make([]map[string]any, 0, len(export.bitmapNames))
		for _, bitmapName := range export.bitmapNames {
			published = append(published, map[string]any{"node": metadataImageNode, "name": bitmapName})
		}

		exportOptions := map[string]any{"type": "nbd", "id": fmt.Sprintf("export%d", i), "node-name": dataNode, "name": export.name}
		if len(published) > 0 {
			exportOptions["bitmaps"] = published
		}

		// The options are given as JSON, which needs no escaping of the paths and names in them.
		for _, option := range options {
			blockdev, err := json.Marshal(option)
			if err != nil {
				return nil, nil, err
			}

			args = append(args, "--blockdev", string(blockdev))
		}

		exportArg, err := json.Marshal(exportOptions)
		if err != nil {
			return nil, nil, err
		}

		args = append(args, "--export", string(exportArg))
		metadataImages = append(metadataImages, export.metadataImage)
	}

	var stderr bytes.Buffer
	cmd := exec.Command("qemu-storage-daemon", args...)
	cmd.Stderr = &stderr
	cmd.ExtraFiles = metadataImages

	err = cmd.Start()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed starting qemu-storage-daemon: %w", err)
	}

	// stopped is set before LXD signals the daemon.
	// The daemon creates the socket before it opens the exports.
	// It can therefore fail after the connection is returned, and its stderr is the only record of the reason.
	var stopped atomic.Bool

	// exited is closed once the daemon has exited.
	exited := make(chan struct{})
	go func() {
		err := cmd.Wait()
		if !stopped.Load() {
			b.logger.Error("Failed serving instance snapshot over NBD", logger.Ctx{"socketPath": socketPath, "err": err, "stderr": strings.TrimSpace(stderr.String())})
		}

		close(exited)
	}()

	// stop signals the daemon and waits for it to exit.
	// The process is killed if it has not exited after 30 seconds.
	stop := func(sig os.Signal) {
		stopped.Store(true)
		_ = cmd.Process.Signal(sig)

		select {
		case <-exited:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}

		_ = os.Remove(socketPath)
	}

	// Wait for the daemon to listen on the socket.
	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for ready := false; !ready; {
		select {
		case <-timeout:
			stop(unix.SIGKILL)
			return nil, nil, errors.New("Timed out waiting for qemu-storage-daemon socket")
		case <-exited:
			_ = os.Remove(socketPath)
			return nil, nil, fmt.Errorf("Failed running qemu-storage-daemon: %s", strings.TrimSpace(stderr.String()))
		case <-ticker.C:
			_, err := os.Stat(socketPath)
			ready = err == nil
		}
	}

	nbdConn, err := net.Dial("unix", socketPath)
	if err != nil {
		stop(unix.SIGKILL)
		return nil, nil, fmt.Errorf("Failed connecting to qemu-storage-daemon socket: %w", err)
	}

	disconnect := func() {
		b.logger.Debug("Stopping qemu-storage-daemon", logger.Ctx{"socketPath": socketPath})
		_ = nbdConn.Close()
		stop(unix.SIGTERM)
	}

	return nbdConn, disconnect, nil
}

// exposedSnapshotBitmaps returns the names of the bitmaps of the instance snapshot to expose over NBD, by disk device.
// Without a previous snapshot UUID every bitmap of the snapshot is exposed.
// With one, only the bitmaps created with the instance snapshot of that UUID are exposed, and only
// while an instance snapshot of that UUID exists.
// This keeps a client from reading the changes since a snapshot that was deleted and created again under the same name.
// When none match, the exports have no bitmap and the client takes a full backup.
func (b *lxdBackend) exposedSnapshotBitmaps(snapInst instance.Instance, previousSnapshotUUID string) (map[string][]string, error) {
	bitmaps, err := snapInst.Bitmaps()
	if err != nil {
		return nil, err
	}

	exposed := map[string][]string{}
	if previousSnapshotUUID != "" {
		parentName, _, _ := api.GetParentAndSnapshotName(snapInst.Name())
		dbVolSnaps, err := VolumeDBSnapshotsGet(b, snapInst.Project().Name, parentName, drivers.VolumeTypeVM)
		if err != nil {
			return nil, err
		}

		found := false
		for _, dbVolSnap := range dbVolSnaps {
			if dbVolSnap.Config["volatile.uuid"] == previousSnapshotUUID {
				found = true
				break
			}
		}

		if !found {
			b.logger.Warn("No instance snapshot has the previous snapshot UUID, exposing no bitmap", logger.Ctx{"instance": snapInst.Name(), "previousSnapshotUUID": previousSnapshotUUID})
			return exposed, nil
		}
	}

	for _, bitmap := range bitmaps {
		if previousSnapshotUUID != "" && bitmap.UUID != previousSnapshotUUID {
			continue
		}

		for _, volume := range bitmap.Volumes {
			exposed[volume.Device] = append(exposed[volume.Device], bitmap.Name)
		}
	}

	return exposed, nil
}

// GetInstanceSnapshotNBD returns a read-only NBD connection serving the volume snapshots of a
// virtual machine snapshot that have a snapshot metadata image, each under an export named after
// its disk device together with the bitmaps of the snapshot.
// deviceNames selects a subset of the devices, and every device is served when it is empty.
// previousSnapshotUUID limits the exposed bitmaps to the ones created with the instance snapshot of that UUID.
// A volume snapshot that no longer exists is skipped.
// The returned conflict reference is empty, as every client opens its own session.
func (b *lxdBackend) GetInstanceSnapshotNBD(snapInst instance.Instance, deviceNames []string, previousSnapshotUUID string) (net.Conn, func(), string, error) {
	l := b.logger.AddContext(logger.Ctx{"project": snapInst.Project().Name, "instance": snapInst.Name()})
	l.Debug("GetInstanceSnapshotNBD started")
	defer l.Debug("GetInstanceSnapshotNBD finished")

	err := b.isStatusReady()
	if err != nil {
		return nil, nil, "", err
	}

	if !snapInst.IsSnapshot() {
		return nil, nil, "", errors.New("Instance must be a snapshot")
	}

	if snapInst.Type() != instancetype.VM {
		return nil, nil, "", api.StatusErrorf(http.StatusBadRequest, "NBD export is only supported for virtual machines")
	}

	images, err := snapInst.SnapshotMetadataImages()
	if err != nil {
		return nil, nil, "", err
	}

	if len(images) == 0 {
		return nil, nil, "", api.StatusErrorf(http.StatusBadRequest, "Snapshot was not created with a bitmap")
	}

	for _, deviceName := range deviceNames {
		_, ok := images[deviceName]
		if !ok {
			return nil, nil, "", api.StatusErrorf(http.StatusNotFound, "Snapshot has no volume snapshot with bitmaps for device %q", deviceName)
		}
	}

	exposed, err := b.exposedSnapshotBitmaps(snapInst, previousSnapshotUUID)
	if err != nil {
		return nil, nil, "", err
	}

	revert := revert.New()
	defer revert.Fail()

	cleanups := []func(){}

	// The exported snapshots cannot be deleted or renamed while the export has them mounted.
	// Each one is therefore counted before it is mounted and released after it is unmounted.
	uncount := nbdExportCount(b.name, drivers.VolumeTypeVM, snapInst.Project().Name, snapInst.Name())
	revert.Add(uncount)
	cleanups = append(cleanups, uncount)

	// The snapshot metadata images are on the config volume snapshot, which is mounted with the root volume snapshot.
	mountInfo, err := b.MountInstanceSnapshot(snapInst, nil)
	if err != nil {
		return nil, nil, "", err
	}

	unmountRoot := func() { _ = b.UnmountInstanceSnapshot(snapInst, nil) }
	revert.Add(unmountRoot)
	cleanups = append(cleanups, unmountRoot)

	devSource, isPath := mountInfo.DevSource.(config.DevSourcePath)
	if !isPath {
		return nil, nil, "", errors.New("Failed getting disk path of snapshot")
	}

	// The images are opened through a root, as the config volume snapshot can contain a symlink
	// that resolves outside of it, and the daemon inherits them.
	snapRoot, err := snapInst.OpenRoot()
	if err != nil {
		return nil, nil, "", err
	}

	defer func() { _ = snapRoot.Close() }()

	metadataImages := []*os.File{}
	defer func() {
		for _, metadataImage := range metadataImages {
			_ = metadataImage.Close()
		}
	}()

	instProject := snapInst.Project()
	effectiveProject := project.StorageVolumeProjectFromRecord(&instProject, cluster.StoragePoolVolumeTypeCustom)
	pools := map[string]Pool{b.name: b}
	exports := []nbdExport{}
	for _, deviceName := range slices.Sorted(maps.Keys(images)) {
		image := images[deviceName]
		if len(deviceNames) > 0 && !slices.Contains(deviceNames, deviceName) {
			continue
		}

		metadataImage, err := snapRoot.Open(image.Path)
		if err != nil {
			return nil, nil, "", fmt.Errorf("Failed opening snapshot metadata image of disk %q: %w", deviceName, err)
		}

		metadataImages = append(metadataImages, metadataImage)

		if image.VolumeSnapshot == "" {
			exports = append(exports, nbdExport{name: deviceName, devPath: devSource.Path, metadataImage: metadataImage, bitmapNames: exposed[deviceName]})
			continue
		}

		// The volume snapshot of an attached custom volume.
		pool, ok := pools[image.Pool]
		if !ok {
			pool, err = LoadByName(b.state, image.Pool)
			if err != nil {
				return nil, nil, "", err
			}

			pools[image.Pool] = pool
		}

		lxdPool, ok := pool.(*lxdBackend)
		if !ok {
			return nil, nil, "", fmt.Errorf("Unexpected storage pool type for %q", image.Pool)
		}

		dbSnapVol, err := VolumeDBGet(lxdPool, effectiveProject, image.VolumeSnapshot, drivers.VolumeTypeCustom)
		if err != nil {
			return nil, nil, "", err
		}

		snapVol := lxdPool.GetVolume(drivers.VolumeTypeCustom, drivers.ContentTypeBlock, project.StorageVolume(effectiveProject, dbSnapVol.Name), dbSnapVol.Config)
		if lxdPool.driver.Info().PopulateParentVolumeUUID {
			parentUUID, err := lxdPool.getParentVolumeUUID(snapVol, effectiveProject)
			if err != nil {
				return nil, nil, "", err
			}

			snapVol.SetParentUUID(parentUUID)
		}

		uncountVol := nbdExportCount(image.Pool, drivers.VolumeTypeCustom, effectiveProject, dbSnapVol.Name)
		revert.Add(uncountVol)
		cleanups = append(cleanups, uncountVol)

		err = lxdPool.driver.MountVolumeSnapshot(snapVol, nil)
		if err != nil {
			return nil, nil, "", err
		}

		unmount := func() { _, _ = lxdPool.driver.UnmountVolumeSnapshot(snapVol, nil) }
		revert.Add(unmount)
		cleanups = append(cleanups, unmount)

		devPath, err := lxdPool.driver.GetVolumeDiskPath(snapVol)
		if err != nil {
			return nil, nil, "", fmt.Errorf("Failed getting disk path of volume snapshot %q: %w", dbSnapVol.Name, err)
		}

		exports = append(exports, nbdExport{name: deviceName, devPath: devPath, metadataImage: metadataImage, bitmapNames: exposed[deviceName]})
	}

	if len(exports) == 0 {
		return nil, nil, "", api.StatusErrorf(http.StatusBadRequest, "Snapshot has no volume snapshot to export")
	}

	socketPath := nbdSocketPath(project.Instance(snapInst.Project().Name, snapInst.Name()) + "_" + uuid.New().String())
	conn, disconnect, err := b.serveSnapshotNBD(socketPath, exports)
	if err != nil {
		return nil, nil, "", err
	}

	revert.Success()
	return conn, func() {
		disconnect()

		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}, "", nil
}
