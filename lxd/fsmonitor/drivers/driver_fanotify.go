package drivers

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/fsmonitor"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared/logger"
)

type fanotify struct {
	common

	fd int
}

type fanotifyEventInfoHeader struct {
	InfoType uint8
	Pad      uint8
	Len      uint16
}

type fanotifyEventInfoFid struct {
	fanotifyEventInfoHeader
	FSID uint64
}

var fanotifyEventToFSMonitorEvent = map[uint64]fsmonitor.Event{
	unix.FAN_CREATE:      fsmonitor.EventAdd,
	unix.FAN_DELETE:      fsmonitor.EventRemove,
	unix.FAN_DELETE_SELF: fsmonitor.EventRemove,
	unix.FAN_CLOSE_WRITE: fsmonitor.EventWrite,
	unix.FAN_MOVED_TO:    fsmonitor.EventRename,
}

var fsMonitorEventToFANotifyEvent = map[fsmonitor.Event]uint64{
	fsmonitor.EventAdd:    unix.FAN_CREATE,
	fsmonitor.EventRemove: unix.FAN_DELETE | unix.FAN_DELETE_SELF,
	fsmonitor.EventWrite:  unix.FAN_CLOSE_WRITE,
	fsmonitor.EventRename: unix.FAN_MOVED_TO,
}

func (d *fanotify) toFSMonitorEvent(mask uint64) (fsmonitor.Event, error) {
	for knownFANotifyEvent, event := range fanotifyEventToFSMonitorEvent {
		if mask&knownFANotifyEvent != 0 {
			return event, nil
		}
	}

	return -1, fmt.Errorf(`Unknown fanotify event "%d"`, mask)
}

func (d *fanotify) eventMask() (uint64, error) {
	// ON_DIR is required so that we can determine if the event occurred on a file or a directory.
	var mask uint64 = unix.FAN_ONDIR
	for _, e := range d.events {
		fanotifyEvent, ok := fsMonitorEventToFANotifyEvent[e]
		if !ok {
			return 0, fmt.Errorf(`Unknown fsmonitor event "%d"`, e)
		}

		mask = mask | fanotifyEvent
	}

	return mask, nil
}

// DriverName returns the name of the driver.
func (d *fanotify) DriverName() string {
	return fsmonitor.DriverNameFANotify
}

func (d *fanotify) load(ctx context.Context) error {
	if !filesystem.IsMountPoint(d.prefixPath) {
		return errors.New("Path needs to be a mountpoint")
	}

	var err error

	d.fd, err = unix.FanotifyInit(unix.FAN_CLOEXEC|unix.FAN_REPORT_DFID_NAME, unix.O_CLOEXEC)
	if err != nil {
		return fmt.Errorf("Failed to initialize fanotify: %w", err)
	}

	mask, err := d.eventMask()
	if err != nil {
		return fmt.Errorf("Failed to get a fanotify event mask: %w", err)
	}

	err = unix.FanotifyMark(d.fd, unix.FAN_MARK_ADD|unix.FAN_MARK_FILESYSTEM, mask, unix.AT_FDCWD, d.prefixPath)
	if err != nil {
		_ = unix.Close(d.fd)
		return fmt.Errorf("Failed to watch directory %q: %w", d.prefixPath, err)
	}

	fd, err := unix.Open(d.prefixPath, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = unix.Close(d.fd)
		return fmt.Errorf("Failed to open directory %q: %w", d.prefixPath, err)
	}

	go func() {
		<-ctx.Done()
		_ = unix.Close(d.fd)
	}()

	go d.getEvents(ctx, fd)

	return nil
}

func (d *fanotify) getEvents(ctx context.Context, mountFd int) {
	for {
		buf := make([]byte, 256)

		// Although the event is less than 256 bytes, we read as much to ensure the entire event
		// is captured and following events are readable. Using only binary.Read() would require
		// more manual cleanup as otherwise bytes from a previous event would still be present and
		// make everything unreadable.
		_, err := unix.Read(d.fd, buf)
		if err != nil {
			// Stop listening for events as the fanotify fd has been closed due to cleanup.
			if ctx.Err() != nil || errors.Is(err, unix.EBADF) {
				_ = unix.Close(mountFd)
				return
			}

			d.logger.Error("Failed to read event", logger.Ctx{"err": err})
			continue
		}

		rd := bytes.NewReader(buf)

		event := unix.FanotifyEventMetadata{}

		err = binary.Read(rd, binary.LittleEndian, &event)
		if err != nil {
			d.logger.Error("Failed to read event metadata", logger.Ctx{"err": err})
			continue
		}

		// Read event info fid
		fid := fanotifyEventInfoFid{}

		err = binary.Read(rd, binary.LittleEndian, &fid)
		if err != nil {
			d.logger.Error("Failed to read event fid", logger.Ctx{"err": err})
			continue
		}

		// Although unix.FileHandle exists, it cannot be used with binary.Read() as the
		// variables inside are not exported.
		type fileHandleInfo struct {
			Bytes uint32
			Type  int32
		}

		// Read file handle information
		fhInfo := fileHandleInfo{}

		err = binary.Read(rd, binary.LittleEndian, &fhInfo)
		if err != nil {
			d.logger.Error("Failed to read file handle info", logger.Ctx{"err": err})
			continue
		}

		// Read file handle
		fileHandle := make([]byte, fhInfo.Bytes)

		err = binary.Read(rd, binary.LittleEndian, fileHandle)
		if err != nil {
			d.logger.Error("Failed to read file handle", logger.Ctx{"err": err})
			continue
		}

		fh := unix.NewFileHandle(fhInfo.Type, fileHandle)

		// The file handle is followed by a null terminated string that identifies the
		// created/deleted directory entry name.
		// Read it now so we can use it whether or not [OpenByHandleAt] succeeds.
		sb := strings.Builder{}
		for {
			b, err := rd.ReadByte()
			if err != nil || b == 0 {
				break
			}

			err = sb.WriteByte(b)
			if err != nil {
				break
			}
		}

		name := sb.String()

		fd, err := unix.OpenByHandleAt(mountFd, fh, 0)
		if err != nil {
			// If the handle can't be opened (e.g. ESTALE because the fs entry was removed),
			// attempt a dispatch using the entry name.
			errno, ok := err.(unix.Errno)
			if ctx.Err() == nil && ok && errno != unix.ESTALE {
				d.logger.Error("Failed to open file", logger.Ctx{"err": err})
			}

			action, err := d.toFSMonitorEvent(event.Mask)
			if err != nil {
				d.logger.Warn("Failed to match fanotify event, skipping", logger.Ctx{"err": err})
				continue
			}

			candidate := filepath.Clean(filepath.Join(d.prefixPath, name))

			d.mu.Lock()
			for path := range d.watches {
				if path != candidate && filepath.Base(path) != name {
					continue
				}

				for identifier, f := range d.watches[path] {
					ret := f(path, action)
					if !ret {
						delete(d.watches[path], identifier)

						if len(d.watches[path]) == 0 {
							delete(d.watches, path)
						}
					}
				}
			}
			d.mu.Unlock()

			continue
		}

		unix.CloseOnExec(fd)

		// Determine the directory of the created or deleted file.
		target, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
		if err != nil {
			d.logger.Error("Failed to read symlink", logger.Ctx{"err": err})
			_ = unix.Close(fd)
			continue
		}

		_ = unix.Close(fd)

		// If the target file has been deleted, the returned value might contain a " (deleted)" suffix.
		// This needs to be removed.
		target = strings.TrimSuffix(target, " (deleted)")

		// Build the full event path from the resolved directory and the entry name.
		eventPath := filepath.Clean(filepath.Join(target, name))

		// Check whether there's a watch on a specific file or directory.
		d.mu.Lock()
		for path := range d.watches {
			if eventPath != path {
				continue
			}

			action, err := d.toFSMonitorEvent(event.Mask)
			if err != nil {
				logger.Warn("Failed to match fanotify event, skipping", logger.Ctx{"err": err})
				continue
			}

			for identifier, f := range d.watches[path] {
				ret := f(path, action)
				if !ret {
					delete(d.watches[path], identifier)

					if len(d.watches[path]) == 0 {
						delete(d.watches, path)
					}
				}
			}

			break
		}

		d.mu.Unlock()
	}
}
